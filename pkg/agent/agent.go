package agent

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/overlay"
	"github.com/cloudless/cloudless/pkg/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

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
	resourceMonitor *ResourceMonitor
	coordinatorConn *grpc.ClientConn

	// Overlay networking components
	transport       *overlay.QUICTransport
	serviceRegistry *overlay.ServiceRegistry
	loadBalancer    *overlay.L4LoadBalancer
	peerManager     *overlay.PeerManager
	meshManager     *overlay.MeshManager
	natTraversal    *overlay.NATTraversal

	// State
	stopCh chan struct{}
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

	// Initialize resource monitor
	config.Logger.Info("Initializing resource monitor")
	monitorConfig := MonitorConfig{
		Interval: 5 * time.Second,
		DiskPath: filepath.Join(config.DataDir, "volumes"),
	}
	monitor := NewResourceMonitor(monitorConfig, config.Logger)
	a.resourceMonitor = monitor

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
		opts = append(opts, creds)

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
		NodeID:   a.config.NodeID,
		NodeName: a.config.NodeName,
		Address:  a.config.AgentAddr,
		Region:   a.config.Region,
		Zone:     a.config.Zone,
		JoinToken: a.config.JoinToken,
		Capabilities: api.NodeCapabilities{
			GPUCount:        a.config.Resources.GPU,
			AcceleratorType: "", // TODO: Detect from hardware
			NetworkFeatures: []string{"quic", "udp", "tcp"},
			StorageTypes:    []string{"local", "ephemeral"},
		},
		Resources: api.NodeResources{
			CPUMillicores: a.config.Resources.CPUMillicores,
			MemoryBytes:   a.config.Resources.MemoryBytes,
			StorageBytes:  a.config.Resources.StorageBytes,
			BandwidthBPS:  a.config.Resources.BandwidthBps,
			GPUCount:      a.config.Resources.GPU,
		},
	}

	a.logger.Info("Sending enrollment request",
		zap.String("node_id", enrollReq.NodeID),
		zap.String("region", enrollReq.Region),
		zap.String("zone", enrollReq.Zone),
		zap.Int64("cpu_millicores", enrollReq.Resources.CPUMillicores),
		zap.Int64("memory_bytes", enrollReq.Resources.MemoryBytes),
	)

	// TODO: Once protobuf is generated, call:
	// resp, err := a.coordinatorClient.EnrollNode(ctx, enrollReq)
	// if err != nil {
	//     return fmt.Errorf("enrollment RPC failed: %w", err)
	// }

	// For now, just log that we would send the request
	a.logger.Info("Enrollment request prepared (waiting for protobuf generation to send actual RPC)",
		zap.Any("request", enrollReq),
	)

	// TODO: Store received certificate
	// if len(resp.Certificate) > 0 {
	//     certPath := filepath.Join(a.config.DataDir, "certs", "node.crt")
	//     if err := os.WriteFile(certPath, resp.Certificate, 0600); err != nil {
	//         return fmt.Errorf("failed to save certificate: %w", err)
	//     }
	//     a.logger.Info("Node certificate saved", zap.String("path", certPath))
	// }

	// TODO: Store CA certificate
	// if len(resp.CaCertificate) > 0 {
	//     caPath := filepath.Join(a.config.DataDir, "certs", "ca.crt")
	//     if err := os.WriteFile(caPath, resp.CaCertificate, 0600); err != nil {
	//         return fmt.Errorf("failed to save CA certificate: %w", err)
	//     }
	//     a.logger.Info("CA certificate saved", zap.String("path", caPath))
	// }

	// TODO: Update heartbeat interval from response
	// if resp.HeartbeatInterval != nil {
	//     a.config.HeartbeatInterval = resp.HeartbeatInterval.AsDuration()
	//     a.logger.Info("Updated heartbeat interval",
	//         zap.Duration("interval", a.config.HeartbeatInterval),
	//     )
	// }

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
	containerInfos := []api.ContainerInfo{}
	containers, err := a.runtime.ListContainers(ctx)
	if err != nil {
		a.logger.Warn("Failed to list containers", zap.Error(err))
	} else {
		a.logger.Debug("Container status",
			zap.Int("container_count", len(containers)),
		)

		// Convert to API container info
		for _, container := range containers {
			containerInfos = append(containerInfos, api.ContainerInfo{
				ID:    container.ID,
				Name:  container.Name,
				State: container.State,
				Image: container.Image,
			})
		}
	}

	// Collect node conditions
	conditions := a.computeNodeConditions(snapshot)

	// Prepare heartbeat request
	heartbeatReq := &api.HeartbeatRequest{
		NodeID: a.config.NodeID,
		Capacity: api.NodeResources{
			CPUMillicores: capacity.CPUMillicores,
			MemoryBytes:   capacity.MemoryBytes,
			StorageBytes:  capacity.StorageBytes,
			BandwidthBPS:  capacity.BandwidthBps,
			GPUCount:      capacity.GPU,
		},
		Usage: api.NodeResources{
			CPUMillicores: usage.CPUMillicores,
			MemoryBytes:   usage.MemoryBytes,
			StorageBytes:  usage.StorageBytes,
			BandwidthBPS:  usage.BandwidthBps,
			GPUCount:      usage.GPU,
		},
		Containers: containerInfos,
		Conditions: conditions,
	}

	// TODO: Once protobuf is generated, call:
	// resp, err := a.coordinatorClient.Heartbeat(ctx, heartbeatReq)
	// if err != nil {
	//     return fmt.Errorf("heartbeat RPC failed: %w", err)
	// }
	//
	// return a.processHeartbeatResponse(ctx, resp)

	// For now, just log that we would send the request
	a.logger.Debug("Heartbeat prepared (waiting for protobuf generation)",
		zap.Int("container_count", len(containerInfos)),
		zap.Int("condition_count", len(conditions)),
	)

	return nil
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
		zap.Int("commands", len(resp.Commands)),
	)

	// Process workload assignments
	for _, assignment := range resp.Assignments {
		a.logger.Info("Received workload assignment",
			zap.String("workload_id", assignment.WorkloadID),
			zap.String("image", assignment.Image),
		)

		// TODO: Start workload container
		// This would involve:
		// 1. Pull image if needed
		// 2. Create container with resource constraints
		// 3. Start container
		// 4. Report success/failure back
		go a.handleWorkloadAssignment(ctx, assignment)
	}

	// Process commands
	for _, command := range resp.Commands {
		a.logger.Info("Received command", zap.String("command", command))

		// TODO: Execute command
		// Commands might include:
		// - "stop:{containerID}" - Stop a container
		// - "update:{workloadID}" - Update a workload
		// - "drain" - Prepare for maintenance
		go a.handleCommand(ctx, command)
	}

	return nil
}

// handleWorkloadAssignment handles a workload assignment
func (a *Agent) handleWorkloadAssignment(ctx context.Context, assignment *api.WorkloadAssignment) {
	a.logger.Info("Handling workload assignment",
		zap.String("workload_id", assignment.WorkloadID),
		zap.String("image", assignment.Image),
	)

	// TODO: Implement workload lifecycle:
	// 1. Pull image
	// 2. Create container with resource limits from assignment.Resources
	// 3. Configure networking
	// 4. Start container
	// 5. Monitor health

	// For now, just log the intent
	a.logger.Info("Workload assignment handling not yet implemented (waiting for container runtime integration)")
}

// handleCommand handles a command from the coordinator
func (a *Agent) handleCommand(ctx context.Context, command string) {
	a.logger.Info("Handling command", zap.String("command", command))

	// TODO: Parse and execute commands
	// Format: "action:parameter"
	// Examples:
	// - "stop:container-123"
	// - "drain:graceful"
	// - "update:workload-456"

	// For now, just log the intent
	a.logger.Info("Command handling not yet implemented")
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