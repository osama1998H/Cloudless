package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/overlay"
	"github.com/cloudless/cloudless/pkg/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// Resources represents node resource configuration
type Resources struct {
	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64
	BandwidthBps  int64
	GPU           int32
}

// Config represents the agent configuration
type Config struct {
	DataDir           string
	CoordinatorAddr   string
	AgentAddr         string
	MetricsAddr       string
	NodeID            string
	NodeName          string
	Region            string
	Zone              string
	JoinToken         string
	HeartbeatInterval time.Duration
	ContainerRuntime  string
	ContainerSocket   string
	Resources         Resources
	Logger            *zap.Logger

	// Overlay networking
	OverlayConfig overlay.OverlayConfig

	// TLS certificate for overlay
	TLSCert *tls.Certificate

	// TLS configuration for coordinator connection
	CertificateFile string // Path to client certificate
	KeyFile         string // Path to client key
	CAFile          string // Path to CA certificate
	InsecureSkipVerify bool // Skip TLS verification (dev only!)
}

// Validate validates the agent configuration
func (c *Config) Validate() error {
	if c.DataDir == "" {
		return fmt.Errorf("data directory is required")
	}
	if c.CoordinatorAddr == "" {
		return fmt.Errorf("coordinator address is required")
	}
	if c.AgentAddr == "" {
		return fmt.Errorf("agent address is required")
	}
	if c.MetricsAddr == "" {
		return fmt.Errorf("metrics address is required")
	}
	if c.NodeID == "" {
		// Generate a random node ID if not provided
		c.NodeID = fmt.Sprintf("node-%d", time.Now().Unix())
	}
	if c.NodeName == "" {
		c.NodeName = c.NodeID
	}
	if c.HeartbeatInterval <= 0 {
		c.HeartbeatInterval = 10 * time.Second
	}
	if c.ContainerRuntime == "" {
		c.ContainerRuntime = "containerd"
	}
	if c.Logger == nil {
		return fmt.Errorf("logger is required")
	}
	return nil
}

// Agent manages the node agent
type Agent struct {
	// Embed UnimplementedAgentServiceServer for forward compatibility
	api.UnimplementedAgentServiceServer

	config *Config
	logger *zap.Logger

	// Components
	runtime         runtime.Runtime
	probeExecutor   *runtime.ProbeExecutor
	resourceMonitor *ResourceMonitor
	metricsStorage  *MetricsStorage
	coordinatorConn *grpc.ClientConn

	// Overlay networking components
	transport       *overlay.QUICTransport
	serviceRegistry *overlay.ServiceRegistry
	loadBalancer    *overlay.L4LoadBalancer
	peerManager     *overlay.PeerManager
	meshManager     *overlay.MeshManager
	natTraversal    *overlay.NATTraversal

	// State
	stopCh   chan struct{}
	draining bool
	mu       sync.RWMutex
}

// New creates a new agent instance
func New(config *Config) (*Agent, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	a := &Agent{
		config: config,
		logger: config.Logger,
		stopCh: make(chan struct{}),
	}

	// Create data directory
	if err := os.MkdirAll(config.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	// Initialize container runtime
	config.Logger.Info("Initializing container runtime", zap.String("runtime", config.ContainerRuntime))

	if config.ContainerRuntime == "containerd" {
		socketPath := config.ContainerSocket
		if socketPath == "" {
			socketPath = "/run/containerd/containerd.sock"
		}

		runtimeConfig := runtime.RuntimeConfig{
			SocketPath: socketPath,
			Namespace:  "cloudless",
			Timeout:    30 * time.Second,
		}

		containerRuntime, err := runtime.NewContainerdRuntime(runtimeConfig, config.Logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize containerd runtime: %w", err)
		}
		a.runtime = containerRuntime
	} else {
		return nil, fmt.Errorf("unsupported container runtime: %s", config.ContainerRuntime)
	}

	// Initialize health probe executor
	config.Logger.Info("Initializing health probe executor")
	a.probeExecutor = runtime.NewProbeExecutor(a.runtime, config.Logger)

	// Initialize resource monitor
	config.Logger.Info("Initializing resource monitor")
	monitorConfig := MonitorConfig{
		Interval: 5 * time.Second,
		DiskPath: filepath.Join(config.DataDir, "volumes"),
	}
	monitor := NewResourceMonitor(monitorConfig, config.Logger)
	a.resourceMonitor = monitor

	// Initialize metrics storage
	config.Logger.Info("Initializing metrics storage")
	metricsConfig := MetricsStorageConfig{
		RetentionDuration: 24 * time.Hour,
		MaxDataPoints:     1000,
		Thresholds: map[MetricType]MetricThreshold{
			MetricTypeCPU: {
				WarningPercent:  70.0,
				CriticalPercent: 90.0,
				Limit:           float64(config.Resources.CPUMillicores),
			},
			MetricTypeMemory: {
				WarningPercent:  80.0,
				CriticalPercent: 95.0,
				Limit:           float64(config.Resources.MemoryBytes),
			},
			MetricTypeStorage: {
				WarningPercent:  85.0,
				CriticalPercent: 95.0,
				Limit:           float64(config.Resources.StorageBytes),
			},
		},
	}
	metricsStorage := NewMetricsStorage(metricsConfig, config.Logger)
	a.metricsStorage = metricsStorage

	// Register default alert handler
	metricsStorage.RegisterAlertHandler(func(alert MetricsAlert) {
		config.Logger.Warn("Resource alert triggered",
			zap.String("type", string(alert.Type)),
			zap.String("level", alert.Level),
			zap.Float64("threshold", alert.Threshold),
			zap.Float64("current", alert.Current),
			zap.String("message", alert.Message),
		)
	})

	// Initialize Overlay Networking Components
	config.Logger.Info("Initializing overlay networking")

	// Set default overlay config if not provided
	if config.OverlayConfig.NodeID == "" {
		config.OverlayConfig.NodeID = config.NodeID
	}
	if config.OverlayConfig.Transport.ListenAddress == "" {
		config.OverlayConfig.Transport.ListenAddress = ":9091" // Default overlay port for agent
	}

	// Create TLS config for overlay
	var tlsConfig *tls.Config
	if config.TLSCert != nil {
		tlsConfig = &tls.Config{
			Certificates: []tls.Certificate{*config.TLSCert},
			ClientAuth:   tls.RequireAnyClientCert,
			NextProtos:   []string{"cloudless-overlay"},
		}
	} else {
		config.Logger.Warn("No TLS certificate provided for overlay, using insecure config")
		// Create a basic self-signed cert for testing
		// In production, this should always come from the CA
		tlsConfig = &tls.Config{
			InsecureSkipVerify: true,
			NextProtos:         []string{"cloudless-overlay"},
		}
	}

	// Initialize transport
	transport := overlay.NewQUICTransport(
		config.OverlayConfig.Transport,
		tlsConfig,
		config.Logger,
	)
	a.transport = transport

	// Initialize service registry
	serviceRegistry, err := overlay.NewServiceRegistry(
		config.OverlayConfig.Registry,
		config.Logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize service registry: %w", err)
	}
	a.serviceRegistry = serviceRegistry

	// Initialize load balancer
	loadBalancer := overlay.NewL4LoadBalancer(
		config.OverlayConfig.LoadBalancer,
		serviceRegistry,
		config.Logger,
	)
	a.loadBalancer = loadBalancer

	// Initialize peer manager
	peerManager := overlay.NewPeerManager(
		config.OverlayConfig.NodeID,
		transport,
		config.OverlayConfig.Mesh,
		config.Logger,
	)
	a.peerManager = peerManager

	// Initialize mesh manager
	meshManager := overlay.NewMeshManager(
		config.OverlayConfig.NodeID,
		config.OverlayConfig.Mesh,
		peerManager,
		config.Logger,
	)
	a.meshManager = meshManager

	// Initialize NAT traversal
	natTraversal := overlay.NewNATTraversal(
		config.OverlayConfig.NAT,
		config.Logger,
	)
	a.natTraversal = natTraversal

	config.Logger.Info("Agent initialized",
		zap.String("node_id", config.NodeID),
		zap.String("node_name", config.NodeName),
		zap.String("region", config.Region),
		zap.String("zone", config.Zone),
		zap.String("runtime", config.ContainerRuntime),
		zap.String("overlay_addr", config.OverlayConfig.Transport.ListenAddress),
	)

	return a, nil
}

// Start starts the agent
func (a *Agent) Start(ctx context.Context) error {
	a.logger.Info("Starting agent")

	// Start resource monitor
	if err := a.resourceMonitor.Start(); err != nil {
		return fmt.Errorf("failed to start resource monitor: %w", err)
	}

	// Start Overlay Networking Components
	a.logger.Info("Starting overlay networking")

	// Start QUIC transport
	if err := a.transport.Listen(ctx, a.config.OverlayConfig.Transport.ListenAddress); err != nil {
		return fmt.Errorf("failed to start overlay transport: %w", err)
	}

	// Start peer manager
	if err := a.peerManager.Start(); err != nil {
		return fmt.Errorf("failed to start peer manager: %w", err)
	}

	// Start mesh manager
	if err := a.meshManager.Start(); err != nil {
		return fmt.Errorf("failed to start mesh manager: %w", err)
	}

	// Start NAT traversal
	if err := a.natTraversal.Start(a.config.OverlayConfig.Transport.ListenAddress); err != nil {
		a.logger.Warn("Failed to start NAT traversal", zap.Error(err))
		// Continue even if NAT traversal fails
	} else {
		// Connect NAT traversal to peer manager
		a.peerManager.SetNATTraversal(a.natTraversal)
		a.logger.Info("NAT traversal connected to peer manager")
	}

	// Connect to coordinator
	if err := a.connectToCoordinator(ctx); err != nil {
		return fmt.Errorf("failed to connect to coordinator: %w", err)
	}

	// Connect to coordinator's overlay network
	if err := a.connectToCoordinatorOverlay(ctx); err != nil {
		a.logger.Warn("Failed to connect to coordinator overlay", zap.Error(err))
		// Continue even if overlay connection fails
	}

	// Start heartbeat loop
	go a.heartbeatLoop(ctx)

	// Start metrics pruning loop
	go a.metricsStorage.StartPruningLoop(ctx)

	// Start metrics collection loop
	go a.metricsCollectionLoop(ctx)

	a.logger.Info("Agent started successfully")
	return nil
}

// Stop stops the agent
func (a *Agent) Stop(ctx context.Context) error {
	a.logger.Info("Stopping agent")

	// Signal stop
	close(a.stopCh)

	// Stop Overlay Networking Components
	if a.natTraversal != nil {
		if err := a.natTraversal.Stop(); err != nil {
			a.logger.Error("Failed to stop NAT traversal", zap.Error(err))
		}
	}

	if a.meshManager != nil {
		if err := a.meshManager.Stop(); err != nil {
			a.logger.Error("Failed to stop mesh manager", zap.Error(err))
		}
	}

	if a.peerManager != nil {
		if err := a.peerManager.Stop(); err != nil {
			a.logger.Error("Failed to stop peer manager", zap.Error(err))
		}
	}

	if a.transport != nil {
		if err := a.transport.Close(); err != nil {
			a.logger.Error("Failed to stop overlay transport", zap.Error(err))
		}
	}

	// Stop resource monitor
	if a.resourceMonitor != nil {
		if err := a.resourceMonitor.Stop(); err != nil {
			a.logger.Error("Failed to stop resource monitor", zap.Error(err))
		}
	}

	// Stop health probe executor
	if a.probeExecutor != nil {
		a.probeExecutor.Stop()
		a.logger.Info("Health probe executor stopped")
	}

	// Close runtime
	if a.runtime != nil {
		if err := a.runtime.Close(); err != nil {
			a.logger.Error("Failed to close runtime", zap.Error(err))
		}
	}

	// Disconnect from coordinator
	if a.coordinatorConn != nil {
		if err := a.coordinatorConn.Close(); err != nil {
			a.logger.Error("Failed to close coordinator connection", zap.Error(err))
		}
	}

	a.logger.Info("Agent stopped")
	return nil
}

// RegisterServices registers gRPC services
func (a *Agent) RegisterServices(server *grpc.Server) {
	api.RegisterAgentServiceServer(server, a)
	a.logger.Info("Registered AgentService gRPC handlers")
}

// connectToCoordinator establishes connection to the coordinator
func (a *Agent) connectToCoordinator(ctx context.Context) error {
	a.logger.Info("Connecting to coordinator", zap.String("addr", a.config.CoordinatorAddr))

	// Set up gRPC connection options
	var opts []grpc.DialOption

	// Configure TLS
	if a.config.CertificateFile != "" && a.config.KeyFile != "" && a.config.CAFile != "" {
		// Load client certificate
		cert, err := tls.LoadX509KeyPair(a.config.CertificateFile, a.config.KeyFile)
		if err != nil {
			return fmt.Errorf("failed to load client certificate: %w", err)
		}

		// Load CA certificate
		caCert, err := os.ReadFile(a.config.CAFile)
		if err != nil {
			return fmt.Errorf("failed to read CA certificate: %w", err)
		}

		certPool := x509.NewCertPool()
		if !certPool.AppendCertsFromPEM(caCert) {
			return fmt.Errorf("failed to add CA certificate to pool")
		}

		// Create TLS config
		tlsConfig := &tls.Config{
			Certificates:       []tls.Certificate{cert},
			RootCAs:            certPool,
			InsecureSkipVerify: a.config.InsecureSkipVerify,
			MinVersion:         tls.VersionTLS13,
		}

		// Use TLS credentials
		creds := credentials.NewTLS(tlsConfig)
		opts = append(opts, grpc.WithTransportCredentials(creds))

		a.logger.Info("Using mTLS for coordinator connection",
			zap.String("cert", a.config.CertificateFile),
			zap.String("ca", a.config.CAFile),
		)
	} else {
		// Use insecure connection (development only!)
		a.logger.Warn("Using insecure connection to coordinator (NOT SUITABLE FOR PRODUCTION)")
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	// Create connection
	conn, err := grpc.DialContext(ctx, a.config.CoordinatorAddr, opts...)
	if err != nil {
		return fmt.Errorf("failed to dial coordinator: %w", err)
	}

	a.coordinatorConn = conn

	// Create coordinator client
	// Note: Once protobuf is generated, replace with:
	// a.coordinatorClient = api.NewCoordinatorServiceClient(conn)

	// Perform enrollment if we have a join token
	if a.config.JoinToken != "" {
		a.logger.Info("Starting node enrollment",
			zap.String("node_id", a.config.NodeID),
			zap.String("node_name", a.config.NodeName),
		)

		if err := a.performEnrollment(ctx); err != nil {
			return fmt.Errorf("enrollment failed: %w", err)
		}

		a.logger.Info("Node enrollment completed successfully")
	} else {
		a.logger.Info("Connected to coordinator (skipping enrollment - no join token)")
	}

	return nil
}

// performEnrollment performs the node enrollment process
func (a *Agent) performEnrollment(ctx context.Context) error {
	// Prepare enrollment request
	enrollReq := &api.EnrollNodeRequest{
		Token:    a.config.JoinToken,
		NodeName: a.config.NodeName,
		Region:   a.config.Region,
		Zone:     a.config.Zone,
		Capabilities: &api.NodeCapabilities{
			ContainerRuntimes: []string{"containerd"},
			SupportsGpu:       a.config.Resources.GPU > 0,
			SupportsArm:       false, // Would detect from runtime
			SupportsX86:       true,  // Would detect from runtime
			NetworkFeatures:   []string{"quic", "udp", "tcp"},
			StorageClasses:    []string{"local", "ephemeral"},
		},
		Capacity: &api.ResourceCapacity{
			CpuMillicores: a.config.Resources.CPUMillicores,
			MemoryBytes:   a.config.Resources.MemoryBytes,
			StorageBytes:  a.config.Resources.StorageBytes,
			BandwidthBps:  a.config.Resources.BandwidthBps,
			GpuCount:      a.config.Resources.GPU,
		},
	}

	a.logger.Info("Sending enrollment request",
		zap.String("node_name", enrollReq.NodeName),
		zap.String("region", enrollReq.Region),
		zap.String("zone", enrollReq.Zone),
		zap.Int64("cpu_millicores", enrollReq.Capacity.CpuMillicores),
		zap.Int64("memory_bytes", enrollReq.Capacity.MemoryBytes),
	)

	// Create coordinator client
	coordinatorClient := api.NewCoordinatorServiceClient(a.coordinatorConn)

	// Call enrollment RPC
	resp, err := coordinatorClient.EnrollNode(ctx, enrollReq)
	if err != nil {
		return fmt.Errorf("enrollment RPC failed: %w", err)
	}

	a.logger.Info("Enrollment successful",
		zap.String("node_id", resp.NodeId),
	)

	// Update node ID to the one assigned by coordinator
	a.config.NodeID = resp.NodeId

	// Create certificates directory
	certsDir := filepath.Join(a.config.DataDir, "certs")
	if err := os.MkdirAll(certsDir, 0700); err != nil {
		return fmt.Errorf("failed to create certs directory: %w", err)
	}

	// Store received node certificate
	if len(resp.Certificate) > 0 {
		certPath := filepath.Join(certsDir, "node.crt")
		if err := os.WriteFile(certPath, resp.Certificate, 0600); err != nil {
			return fmt.Errorf("failed to save certificate: %w", err)
		}
		a.logger.Info("Node certificate saved", zap.String("path", certPath))
	}

	// Store CA certificate
	if len(resp.CaCertificate) > 0 {
		caPath := filepath.Join(certsDir, "ca.crt")
		if err := os.WriteFile(caPath, resp.CaCertificate, 0600); err != nil {
			return fmt.Errorf("failed to save CA certificate: %w", err)
		}
		a.logger.Info("CA certificate saved", zap.String("path", caPath))
	}

	// Update heartbeat interval from response
	if resp.HeartbeatInterval != nil {
		a.config.HeartbeatInterval = resp.HeartbeatInterval.AsDuration()
		a.logger.Info("Updated heartbeat interval",
			zap.Duration("interval", a.config.HeartbeatInterval),
		)
	}

	return nil
}

// heartbeatLoop sends periodic heartbeats to the coordinator
func (a *Agent) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(a.config.HeartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-a.stopCh:
			return
		case <-ticker.C:
			if err := a.sendHeartbeat(ctx); err != nil {
				a.logger.Error("Failed to send heartbeat", zap.Error(err))
			}
		}
	}
}

// sendHeartbeat sends a heartbeat to the coordinator
func (a *Agent) sendHeartbeat(ctx context.Context) error {
	// Collect resource usage
	snapshot := a.resourceMonitor.GetSnapshot()
	capacity := a.resourceMonitor.GetCapacity()
	usage := a.resourceMonitor.GetUsage()

	a.logger.Debug("Sending heartbeat",
		zap.String("node_id", a.config.NodeID),
		zap.Float64("cpu_percent", snapshot.CPUUsagePercent),
		zap.Float64("memory_percent", snapshot.MemoryUsedPercent),
		zap.Int64("capacity_cpu", capacity.CPUMillicores),
		zap.Int64("usage_cpu", usage.CPUMillicores),
	)

	// Collect container status
	containerStatuses := []*api.ContainerStatus{}
	containers, err := a.runtime.ListContainers(ctx)
	if err != nil {
		a.logger.Warn("Failed to list containers", zap.Error(err))
	} else {
		a.logger.Debug("Container status",
			zap.Int("container_count", len(containers)),
		)

		// Convert to API container status
		for _, container := range containers {
			containerStatuses = append(containerStatuses, &api.ContainerStatus{
				ContainerId: container.ID,
				State:       string(container.State),
				Image:       container.Image,
			})
		}
	}

	// Prepare heartbeat request
	// CLD-REQ-010: Include both capacity (available resources) and usage (consumed resources)
	heartbeatReq := &api.HeartbeatRequest{
		NodeId: a.config.NodeID,
		Capacity: &api.ResourceCapacity{
			CpuMillicores: capacity.CPUMillicores,
			MemoryBytes:   capacity.MemoryBytes,
			StorageBytes:  capacity.StorageBytes,
			BandwidthBps:  capacity.BandwidthBPS, // CLD-REQ-010: Egress bandwidth
			GpuCount:      int32(capacity.GPUCount),
			IopsClass:     capacity.IOPSClass, // CLD-REQ-010: IOPS classification
		},
		Usage: &api.ResourceUsage{
			CpuMillicores: usage.CPUMillicores,
			MemoryBytes:   usage.MemoryBytes,
			StorageBytes:  usage.StorageBytes,
			BandwidthBps:  usage.BandwidthBPS,
			GpuCount:      int32(usage.GPU),
		},
		Containers: containerStatuses,
	}

	// Create coordinator client
	coordinatorClient := api.NewCoordinatorServiceClient(a.coordinatorConn)

	// Send heartbeat RPC
	resp, err := coordinatorClient.Heartbeat(ctx, heartbeatReq)
	if err != nil {
		return fmt.Errorf("heartbeat RPC failed: %w", err)
	}

	// Process heartbeat response
	return a.processHeartbeatResponse(ctx, resp)
}

// computeNodeConditions computes node health conditions
func (a *Agent) computeNodeConditions(snapshot ResourceSnapshot) []api.NodeCondition {
	conditions := []api.NodeCondition{}

	// Ready condition
	ready := api.NodeCondition{
		Type:   "Ready",
		Status: "True",
	}
	conditions = append(conditions, ready)

	// Memory pressure
	if snapshot.MemoryUsedPercent > 85.0 {
		conditions = append(conditions, api.NodeCondition{
			Type:    "MemoryPressure",
			Status:  "True",
			Message: fmt.Sprintf("Memory usage at %.1f%%", snapshot.MemoryUsedPercent),
		})
	}

	// Disk pressure
	if snapshot.DiskUsedPercent > 85.0 {
		conditions = append(conditions, api.NodeCondition{
			Type:    "DiskPressure",
			Status:  "True",
			Message: fmt.Sprintf("Disk usage at %.1f%%", snapshot.DiskUsedPercent),
		})
	}

	// Network availability (always available for now)
	conditions = append(conditions, api.NodeCondition{
		Type:   "NetworkAvailable",
		Status: "True",
	})

	return conditions
}

// processHeartbeatResponse processes the heartbeat response from coordinator
func (a *Agent) processHeartbeatResponse(ctx context.Context, resp *api.HeartbeatResponse) error {
	a.logger.Debug("Processing heartbeat response",
		zap.Int("assignments", len(resp.Assignments)),
		zap.Int("containers_to_stop", len(resp.ContainersToStop)),
	)

	// Process workload assignments
	for _, assignment := range resp.Assignments {
		workload := assignment.GetWorkload()
		if workload != nil {
			a.logger.Info("Received workload assignment",
				zap.String("workload_id", workload.Id),
				zap.String("fragment_id", assignment.FragmentId),
				zap.String("action", assignment.Action),
			)
		}

		go a.handleWorkloadAssignment(ctx, assignment)
	}

	// Process container stop commands
	for _, containerID := range resp.ContainersToStop {
		a.logger.Info("Received stop command",
			zap.String("container_id", containerID),
		)

		// Stop and delete container
		go func(cID string) {
			if err := a.runtime.StopContainer(ctx, cID, 30*time.Second); err != nil {
				a.logger.Error("Failed to stop container",
					zap.String("container_id", cID),
					zap.Error(err),
				)
				return
			}
			if err := a.runtime.DeleteContainer(ctx, cID); err != nil {
				a.logger.Warn("Failed to delete container",
					zap.String("container_id", cID),
					zap.Error(err),
				)
			}
		}(containerID)
	}

	return nil
}

// metricsCollectionLoop periodically collects metrics from running containers
func (a *Agent) metricsCollectionLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second) // Collect metrics every 30 seconds
	defer ticker.Stop()

	a.logger.Info("Starting metrics collection loop")

	for {
		select {
		case <-ctx.Done():
			a.logger.Info("Stopping metrics collection loop")
			return
		case <-a.stopCh:
			a.logger.Info("Stopping metrics collection loop")
			return
		case <-ticker.C:
			if err := a.collectAndStoreMetrics(ctx); err != nil {
				a.logger.Error("Failed to collect metrics", zap.Error(err))
			}
		}
	}
}

// collectAndStoreMetrics collects metrics from containers and stores them
func (a *Agent) collectAndStoreMetrics(ctx context.Context) error {
	// Get node-level resource usage
	usage := a.resourceMonitor.GetUsage()
	capacity := a.resourceMonitor.GetCapacity()

	// Store node-level metrics
	a.metricsStorage.RecordMetric(MetricTypeCPU, float64(usage.CPUMillicores), map[string]string{
		"source": "node",
		"node":   a.config.NodeID,
	})

	a.metricsStorage.RecordMetric(MetricTypeMemory, float64(usage.MemoryBytes), map[string]string{
		"source": "node",
		"node":   a.config.NodeID,
	})

	a.metricsStorage.RecordMetric(MetricTypeStorage, float64(usage.StorageBytes), map[string]string{
		"source": "node",
		"node":   a.config.NodeID,
	})

	if usage.BandwidthBPS > 0 {
		a.metricsStorage.RecordMetric(MetricTypeBandwidth, float64(usage.BandwidthBPS), map[string]string{
			"source": "node",
			"node":   a.config.NodeID,
		})
	}

	if capacity.GPUCount > 0 {
		// GPU usage is tracked as percentage of available GPUs in use
		a.metricsStorage.RecordMetric(MetricTypeGPU, float64(usage.GPU), map[string]string{
			"source": "node",
			"node":   a.config.NodeID,
		})
	}

	// Collect per-container metrics
	containers, err := a.runtime.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	for _, container := range containers {
		// Get container stats
		stats, err := a.runtime.GetContainerStats(ctx, container.ID)
		if err != nil {
			a.logger.Warn("Failed to get container stats",
				zap.String("container_id", container.ID),
				zap.Error(err),
			)
			continue
		}

		// Store container-level CPU metrics
		cpuMillicores := stats.CPUStats.UsageNanoseconds / 1000000 // Convert to millicores
		a.metricsStorage.RecordMetric(MetricTypeCPU, float64(cpuMillicores), map[string]string{
			"source":       "container",
			"container_id": container.ID,
			"container":    container.Name,
			"image":        container.Image,
		})

		// Store container-level memory metrics
		a.metricsStorage.RecordMetric(MetricTypeMemory, float64(stats.MemoryStats.UsageBytes), map[string]string{
			"source":       "container",
			"container_id": container.ID,
			"container":    container.Name,
			"image":        container.Image,
		})

		// Store container-level storage metrics if available
		if stats.StorageStats.UsageBytes > 0 {
			a.metricsStorage.RecordMetric(MetricTypeStorage, float64(stats.StorageStats.UsageBytes), map[string]string{
				"source":       "container",
				"container_id": container.ID,
				"container":    container.Name,
				"image":        container.Image,
			})
		}

		// Store container-level network metrics if available
		if stats.NetworkStats.RxBytes > 0 || stats.NetworkStats.TxBytes > 0 {
			totalBytes := stats.NetworkStats.RxBytes + stats.NetworkStats.TxBytes
			a.metricsStorage.RecordMetric(MetricTypeBandwidth, float64(totalBytes), map[string]string{
				"source":       "container",
				"container_id": container.ID,
				"container":    container.Name,
				"image":        container.Image,
			})
		}
	}

	a.logger.Debug("Collected metrics",
		zap.Int("containers", len(containers)),
		zap.Int64("node_cpu", usage.CPUMillicores),
		zap.Int64("node_memory", usage.MemoryBytes),
	)

	return nil
}

// handleWorkloadAssignment handles a workload assignment
func (a *Agent) handleWorkloadAssignment(ctx context.Context, assignment *api.WorkloadAssignment) {
	workload := assignment.GetWorkload()
	if workload == nil {
		a.logger.Error("Workload assignment missing workload")
		return
	}

	spec := workload.GetSpec()
	if spec == nil {
		a.logger.Error("Workload assignment missing spec")
		return
	}

	action := assignment.GetAction()
	fragmentID := assignment.GetFragmentId()

	a.logger.Info("Handling workload assignment",
		zap.String("workload_id", workload.Id),
		zap.String("fragment_id", fragmentID),
		zap.String("action", action),
		zap.String("image", spec.Image),
	)

	switch action {
	case "run":
		if err := a.runWorkload(ctx, workload, fragmentID); err != nil {
			a.logger.Error("Failed to run workload",
				zap.String("workload_id", workload.Id),
				zap.Error(err),
			)
		}
	case "update":
		if err := a.updateWorkload(ctx, workload, fragmentID); err != nil {
			a.logger.Error("Failed to update workload",
				zap.String("workload_id", workload.Id),
				zap.Error(err),
			)
		}
	case "stop":
		if err := a.stopWorkload(ctx, workload, fragmentID); err != nil {
			a.logger.Error("Failed to stop workload",
				zap.String("workload_id", workload.Id),
				zap.Error(err),
			)
		}
	default:
		a.logger.Warn("Unknown workload action",
			zap.String("action", action),
		)
	}
}

// runWorkload starts a new workload container
func (a *Agent) runWorkload(ctx context.Context, workload *api.Workload, fragmentID string) error {
	spec := workload.GetSpec()
	if spec == nil {
		return fmt.Errorf("workload spec is required")
	}

	a.logger.Info("Running workload",
		zap.String("workload_id", workload.Id),
		zap.String("image", spec.Image),
	)

	// Pull image
	a.logger.Info("Pulling image", zap.String("image", spec.Image))
	if err := a.runtime.PullImage(ctx, spec.Image); err != nil {
		return fmt.Errorf("failed to pull image: %w", err)
	}

	// Map volumes
	mounts := make([]runtime.Mount, 0, len(spec.Volumes))
	for _, vol := range spec.Volumes {
		mounts = append(mounts, runtime.Mount{
			Source:      vol.Source,
			Destination: vol.MountPath,
			Type:        "bind",
			ReadOnly:    vol.ReadOnly,
		})
	}

	// Map ports
	ports := make([]runtime.PortMapping, 0, len(spec.Ports))
	for _, port := range spec.Ports {
		ports = append(ports, runtime.PortMapping{
			ContainerPort: port.ContainerPort,
			HostPort:      port.HostPort,
			Protocol:      port.Protocol,
		})
	}

	// Map resources with nil-safety
	resources := runtime.ResourceRequirements{}
	if spec.Resources != nil {
		if spec.Resources.Requests != nil {
			resources.Requests = runtime.ResourceList{
				CPUMillicores: spec.Resources.Requests.CpuMillicores,
				MemoryBytes:   spec.Resources.Requests.MemoryBytes,
				StorageBytes:  spec.Resources.Requests.StorageBytes,
			}
		}
		if spec.Resources.Limits != nil {
			resources.Limits = runtime.ResourceList{
				CPUMillicores: spec.Resources.Limits.CpuMillicores,
				MemoryBytes:   spec.Resources.Limits.MemoryBytes,
				StorageBytes:  spec.Resources.Limits.StorageBytes,
			}
		}
	}

	// Map restart policy with nil-safety
	restartPolicy := runtime.RestartPolicy{
		Name:              "on-failure",
		MaximumRetryCount: 3, // Default
	}
	if spec.RestartPolicy != nil {
		restartPolicy.MaximumRetryCount = int(spec.RestartPolicy.MaxRetries)
		if spec.RestartPolicy.Policy == 0 { // ALWAYS
			restartPolicy.Name = "always"
		} else if spec.RestartPolicy.Policy == 2 { // NEVER
			restartPolicy.Name = "no"
		}
	}

	// Create container spec
	containerSpec := runtime.ContainerSpec{
		ID:      fmt.Sprintf("%s-%s", workload.Id, fragmentID),
		Image:   spec.Image,
		Name:    fmt.Sprintf("%s-%s-%s", workload.Namespace, workload.Name, fragmentID),
		Command: spec.Command,
		Args:    spec.Args,
		Env:     spec.Env,
		Labels: map[string]string{
			"workload.id":       workload.Id,
			"workload.name":     workload.Name,
			"workload.namespace": workload.Namespace,
			"fragment.id":       fragmentID,
		},
		Annotations: workload.Annotations,
		Resources:   resources,
		Network: runtime.NetworkConfig{
			NetworkMode: "bridge",
			Ports:       ports,
		},
		Mounts:        mounts,
		RestartPolicy: restartPolicy,
	}

	// Create container
	container, err := a.runtime.CreateContainer(ctx, containerSpec)
	if err != nil {
		return fmt.Errorf("failed to create container: %w", err)
	}

	// Start container
	if err := a.runtime.StartContainer(ctx, container.ID); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	a.logger.Info("Workload started successfully",
		zap.String("workload_id", workload.Id),
		zap.String("container_id", container.ID),
	)

	return nil
}

// updateWorkload updates an existing workload container
func (a *Agent) updateWorkload(ctx context.Context, workload *api.Workload, fragmentID string) error {
	a.logger.Info("Updating workload",
		zap.String("workload_id", workload.Id),
		zap.String("fragment_id", fragmentID),
	)

	// For now, implement update as stop + start
	// A more sophisticated implementation would do rolling updates
	if err := a.stopWorkload(ctx, workload, fragmentID); err != nil {
		a.logger.Warn("Failed to stop old workload during update",
			zap.String("workload_id", workload.Id),
			zap.Error(err),
		)
	}

	// Wait a bit for cleanup
	time.Sleep(2 * time.Second)

	// Start new version
	return a.runWorkload(ctx, workload, fragmentID)
}

// stopWorkload stops a workload container
func (a *Agent) stopWorkload(ctx context.Context, workload *api.Workload, fragmentID string) error {
	a.logger.Info("Stopping workload",
		zap.String("workload_id", workload.Id),
		zap.String("fragment_id", fragmentID),
	)

	// Find container by label
	containers, err := a.runtime.ListContainers(ctx)
	if err != nil {
		return fmt.Errorf("failed to list containers: %w", err)
	}

	var containerID string
	for _, container := range containers {
		if container.Labels["workload.id"] == workload.Id &&
		   container.Labels["fragment.id"] == fragmentID {
			containerID = container.ID
			break
		}
	}

	if containerID == "" {
		a.logger.Warn("Container not found for workload",
			zap.String("workload_id", workload.Id),
			zap.String("fragment_id", fragmentID),
		)
		return nil
	}

	// Stop container with 30 second timeout
	if err := a.runtime.StopContainer(ctx, containerID, 30*time.Second); err != nil {
		return fmt.Errorf("failed to stop container: %w", err)
	}

	// Delete container
	if err := a.runtime.DeleteContainer(ctx, containerID); err != nil {
		a.logger.Warn("Failed to delete container",
			zap.String("container_id", containerID),
			zap.Error(err),
		)
	}

	a.logger.Info("Workload stopped successfully",
		zap.String("workload_id", workload.Id),
		zap.String("container_id", containerID),
	)

	return nil
}

// handleCommand handles a command from the coordinator
func (a *Agent) handleCommand(ctx context.Context, command string) {
	a.logger.Info("Handling command", zap.String("command", command))

	// Parse command format: "action:parameter"
	parts := strings.SplitN(command, ":", 2)
	if len(parts) != 2 {
		a.logger.Warn("Invalid command format", zap.String("command", command))
		return
	}

	action := parts[0]
	parameter := parts[1]

	switch action {
	case "stop":
		// Stop a container by ID
		a.logger.Info("Stopping container", zap.String("container_id", parameter))
		if err := a.runtime.StopContainer(ctx, parameter, 30*time.Second); err != nil {
			a.logger.Error("Failed to stop container",
				zap.String("container_id", parameter),
				zap.Error(err),
			)
			return
		}
		// Delete the container
		if err := a.runtime.DeleteContainer(ctx, parameter); err != nil {
			a.logger.Warn("Failed to delete container",
				zap.String("container_id", parameter),
				zap.Error(err),
			)
		}

	case "drain":
		// Drain node - stop accepting new workloads and optionally stop existing ones
		graceful := parameter == "graceful"
		a.logger.Info("Draining node", zap.Bool("graceful", graceful))

		// Set draining flag
		a.mu.Lock()
		a.draining = true
		a.mu.Unlock()

		if !graceful {
			// Stop all running containers
			containers, err := a.runtime.ListContainers(ctx)
			if err != nil {
				a.logger.Error("Failed to list containers during drain", zap.Error(err))
				return
			}

			for _, container := range containers {
				a.logger.Info("Stopping container during drain",
					zap.String("container_id", container.ID),
				)
				if err := a.runtime.StopContainer(ctx, container.ID, 30*time.Second); err != nil {
					a.logger.Error("Failed to stop container during drain",
						zap.String("container_id", container.ID),
						zap.Error(err),
					)
				}
			}
		}

	case "update":
		// Update a workload - parameter is workload ID
		a.logger.Info("Updating workload", zap.String("workload_id", parameter))
		// This would typically trigger a rolling update
		// For now, log that we received the command
		a.logger.Info("Workload update command received, will be handled on next assignment")

	default:
		a.logger.Warn("Unknown command action",
			zap.String("action", action),
			zap.String("parameter", parameter),
		)
	}
}

// GetRuntime returns the container runtime
func (a *Agent) GetRuntime() runtime.Runtime {
	return a.runtime
}

// GetResourceMonitor returns the resource monitor
func (a *Agent) GetResourceMonitor() *ResourceMonitor {
	return a.resourceMonitor
}

// Overlay Networking Methods

// connectToCoordinatorOverlay connects to the coordinator's overlay network
func (a *Agent) connectToCoordinatorOverlay(ctx context.Context) error {
	a.logger.Info("Connecting to coordinator overlay network")

	// Extract coordinator address (remove port for now, would need proper parsing)
	// In production, would get overlay address from coordinator enrollment response
	coordAddr := a.config.CoordinatorAddr

	// Get NAT info for this agent
	natInfo := a.natTraversal.GetNATInfo()
	if natInfo != nil {
		a.logger.Info("NAT detected",
			zap.String("type", string(natInfo.Type)),
			zap.String("public_ip", natInfo.PublicIP),
		)
	}

	// Create peer entry for coordinator
	coordPeer := &overlay.Peer{
		ID:       "coordinator", // Would get from enrollment
		Address:  coordAddr,
		Port:     9090, // Default coordinator overlay port
		Region:   "",   // Would get from enrollment
		Zone:     "",
		Status:   overlay.PeerStatusDisconnected,
		Metadata: map[string]string{"role": "coordinator"},
	}

	// Add coordinator as a peer
	if err := a.peerManager.AddPeer(coordPeer); err != nil {
		return fmt.Errorf("failed to add coordinator peer: %w", err)
	}

	a.logger.Info("Connected to coordinator overlay network")
	return nil
}

// GetServiceRegistry returns the service registry
func (a *Agent) GetServiceRegistry() *overlay.ServiceRegistry {
	return a.serviceRegistry
}

// GetLoadBalancer returns the load balancer
func (a *Agent) GetLoadBalancer() *overlay.L4LoadBalancer {
	return a.loadBalancer
}

// GetPeerManager returns the peer manager
func (a *Agent) GetPeerManager() *overlay.PeerManager {
	return a.peerManager
}

// GetMeshManager returns the mesh manager
func (a *Agent) GetMeshManager() *overlay.MeshManager {
	return a.meshManager
}

// GetNATTraversal returns the NAT traversal handler
func (a *Agent) GetNATTraversal() *overlay.NATTraversal {
	return a.natTraversal
}

// RegisterLocalService registers a local service in the overlay network
func (a *Agent) RegisterLocalService(service *overlay.Service) error {
	return a.serviceRegistry.RegisterService(service)
}

// DeregisterLocalService removes a local service from the overlay network
func (a *Agent) DeregisterLocalService(name, namespace string) error {
	return a.serviceRegistry.DeregisterService(name, namespace)
}

// AddPeer adds a peer to the mesh network
func (a *Agent) AddPeer(peer *overlay.Peer) error {
	return a.peerManager.AddPeer(peer)
}

// RemovePeer removes a peer from the mesh network
func (a *Agent) RemovePeer(peerID string) error {
	return a.peerManager.RemovePeer(peerID)
}

// GetMeshStats returns statistics about the mesh network
func (a *Agent) GetMeshStats() overlay.MeshStats {
	return a.meshManager.GetMeshStats()
}