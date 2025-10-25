package coordinator

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudless/cloudless/pkg/coordinator/membership"
	"github.com/cloudless/cloudless/pkg/mtls"
	"github.com/cloudless/cloudless/pkg/overlay"
	"github.com/cloudless/cloudless/pkg/raft"
	"github.com/cloudless/cloudless/pkg/scheduler"
	"go.uber.org/zap"
	"google.golang.org/grpc"
)

// Config represents the coordinator configuration
type Config struct {
	DataDir       string
	BindAddr      string
	MetricsAddr   string
	RaftAddr      string
	RaftID        string
	RaftBootstrap bool
	Logger        *zap.Logger

	// Optional configurations
	HeartbeatInterval time.Duration
	HeartbeatTimeout  time.Duration

	// Scheduler weights
	SchedulerConfig scheduler.ScorerConfig

	// Overlay networking
	OverlayConfig overlay.OverlayConfig
}

// Validate validates the coordinator configuration
func (c *Config) Validate() error {
	if c.DataDir == "" {
		return fmt.Errorf("data directory is required")
	}
	if c.BindAddr == "" {
		return fmt.Errorf("bind address is required")
	}
	if c.MetricsAddr == "" {
		return fmt.Errorf("metrics address is required")
	}
	if c.RaftAddr == "" {
		c.RaftAddr = c.BindAddr // Default to same as bind address
	}
	if c.RaftID == "" {
		c.RaftID = "coordinator-1" // Default ID
	}
	if c.Logger == nil {
		return fmt.Errorf("logger is required")
	}
	if c.HeartbeatInterval == 0 {
		c.HeartbeatInterval = 10 * time.Second
	}
	if c.HeartbeatTimeout == 0 {
		c.HeartbeatTimeout = 30 * time.Second
	}
	return nil
}

// Coordinator manages the control plane
type Coordinator struct {
	config *Config
	logger *zap.Logger

	// Core components
	raftStore      *raft.Store
	membershipMgr  *membership.Manager
	scheduler      *scheduler.Scheduler
	ca             *mtls.CertificateAuthority
	tokenManager   *mtls.TokenManager

	// Overlay networking components
	transport       *overlay.QUICTransport
	serviceRegistry *overlay.ServiceRegistry
	loadBalancer    *overlay.L4LoadBalancer
	peerManager     *overlay.PeerManager
	meshManager     *overlay.MeshManager
	natTraversal    *overlay.NATTraversal

	// State
	isLeader bool
}

// New creates a new coordinator instance
func New(config *Config) (*Coordinator, error) {
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	c := &Coordinator{
		config: config,
		logger: config.Logger,
	}

	// Create necessary directories
	if err := os.MkdirAll(config.DataDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create data directory: %w", err)
	}

	raftDir := filepath.Join(config.DataDir, "raft")
	if err := os.MkdirAll(raftDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create raft directory: %w", err)
	}

	caDir := filepath.Join(config.DataDir, "ca")
	if err := os.MkdirAll(caDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create CA directory: %w", err)
	}

	// Initialize Certificate Authority
	config.Logger.Info("Initializing Certificate Authority")
	caConfig := mtls.CAConfig{
		Organization: "Cloudless",
		ValidityDays: 3650, // 10 years
		KeySize:      4096,
		CADir:        caDir,
	}
	ca, err := mtls.NewCertificateAuthority(caConfig, config.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize CA: %w", err)
	}
	c.ca = ca

	// Initialize Token Manager
	config.Logger.Info("Initializing Token Manager")
	tokenConfig := mtls.TokenConfig{
		TokenExpiry: 24 * time.Hour,
		Secret:      []byte("change-this-secret-in-production"), // TODO: Make configurable
	}
	tokenManager := mtls.NewTokenManager(tokenConfig, config.Logger)
	c.tokenManager = tokenManager

	// Initialize Membership Manager
	config.Logger.Info("Initializing Membership Manager")
	membershipConfig := membership.ManagerConfig{
		HeartbeatInterval: config.HeartbeatInterval,
		HeartbeatTimeout:  config.HeartbeatTimeout,
		CleanupInterval:   1 * time.Minute,
	}
	membershipMgr := membership.NewManager(membershipConfig, config.Logger)
	c.membershipMgr = membershipMgr

	// Initialize Scheduler
	config.Logger.Info("Initializing Scheduler")
	schedulerInstance := scheduler.NewScheduler(config.SchedulerConfig, config.Logger)
	c.scheduler = schedulerInstance

	// Initialize RAFT Store
	config.Logger.Info("Initializing RAFT Store")
	raftConfig := raft.Config{
		RaftID:        config.RaftID,
		RaftAddr:      config.RaftAddr,
		RaftDir:       raftDir,
		Bootstrap:     config.RaftBootstrap,
		SnapshotCount: 1024,
	}
	raftStore, err := raft.NewStore(raftConfig, config.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize RAFT store: %w", err)
	}
	c.raftStore = raftStore

	// Initialize Overlay Networking Components
	config.Logger.Info("Initializing Overlay Networking")

	// Set default overlay config if not provided
	if config.OverlayConfig.NodeID == "" {
		config.OverlayConfig.NodeID = config.RaftID
	}
	if config.OverlayConfig.Transport.ListenAddress == "" {
		config.OverlayConfig.Transport.ListenAddress = ":9090" // Default overlay port
	}

	// Create TLS config from CA
	serverCert, err := ca.IssueCertificate(config.RaftID, []string{config.RaftID})
	if err != nil {
		return nil, fmt.Errorf("failed to issue server certificate: %w", err)
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAnyClientCert,
		NextProtos:   []string{"cloudless-overlay"},
	}

	// Initialize transport
	transport := overlay.NewQUICTransport(
		config.OverlayConfig.Transport,
		tlsConfig,
		config.Logger,
	)
	c.transport = transport

	// Initialize service registry
	serviceRegistry, err := overlay.NewServiceRegistry(
		config.OverlayConfig.Registry,
		config.Logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize service registry: %w", err)
	}
	c.serviceRegistry = serviceRegistry

	// Initialize load balancer
	loadBalancer := overlay.NewL4LoadBalancer(
		config.OverlayConfig.LoadBalancer,
		serviceRegistry,
		config.Logger,
	)
	c.loadBalancer = loadBalancer

	// Initialize peer manager
	peerManager := overlay.NewPeerManager(
		config.OverlayConfig.NodeID,
		transport,
		config.OverlayConfig.Mesh,
		config.Logger,
	)
	c.peerManager = peerManager

	// Initialize mesh manager
	meshManager := overlay.NewMeshManager(
		config.OverlayConfig.NodeID,
		config.OverlayConfig.Mesh,
		peerManager,
		config.Logger,
	)
	c.meshManager = meshManager

	// Initialize NAT traversal
	natTraversal := overlay.NewNATTraversal(
		config.OverlayConfig.NAT,
		config.Logger,
	)
	c.natTraversal = natTraversal

	config.Logger.Info("Coordinator initialized",
		zap.String("raft_id", config.RaftID),
		zap.String("raft_addr", config.RaftAddr),
		zap.Bool("bootstrap", config.RaftBootstrap),
		zap.String("overlay_addr", config.OverlayConfig.Transport.ListenAddress),
	)

	return c, nil
}

// Start starts the coordinator
func (c *Coordinator) Start(ctx context.Context) error {
	c.logger.Info("Starting coordinator")

	// Start RAFT Store
	if err := c.raftStore.Open(); err != nil {
		return fmt.Errorf("failed to start RAFT store: %w", err)
	}

	// If bootstrapping, initialize cluster
	if c.config.RaftBootstrap {
		c.logger.Info("Bootstrapping new cluster")
		if err := c.raftStore.Bootstrap(); err != nil {
			return fmt.Errorf("failed to bootstrap RAFT cluster: %w", err)
		}
	}

	// Wait for leader election
	c.logger.Info("Waiting for leader election")
	timeout := time.After(30 * time.Second)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeout:
			return fmt.Errorf("timeout waiting for leader election")
		case <-ticker.C:
			if c.raftStore.IsLeader() {
				c.isLeader = true
				c.logger.Info("This node is the leader")
				goto leaderElected
			}
			leader := c.raftStore.Leader()
			if leader != "" {
				c.logger.Info("Leader elected", zap.String("leader", leader))
				goto leaderElected
			}
		}
	}

leaderElected:

	// Start Membership Manager
	if err := c.membershipMgr.Start(); err != nil {
		return fmt.Errorf("failed to start membership manager: %w", err)
	}

	// Start Overlay Networking Components
	c.logger.Info("Starting overlay networking")

	// Start QUIC transport
	if err := c.transport.Listen(ctx, c.config.OverlayConfig.Transport.ListenAddress); err != nil {
		return fmt.Errorf("failed to start overlay transport: %w", err)
	}

	// Start peer manager
	if err := c.peerManager.Start(); err != nil {
		return fmt.Errorf("failed to start peer manager: %w", err)
	}

	// Start mesh manager
	if err := c.meshManager.Start(); err != nil {
		return fmt.Errorf("failed to start mesh manager: %w", err)
	}

	// Start NAT traversal
	if err := c.natTraversal.Start(c.config.OverlayConfig.Transport.ListenAddress); err != nil {
		c.logger.Warn("Failed to start NAT traversal", zap.Error(err))
		// Continue even if NAT traversal fails
	}

	c.logger.Info("Coordinator started successfully",
		zap.Bool("is_leader", c.isLeader),
	)
	return nil
}

// Stop stops the coordinator
func (c *Coordinator) Stop(ctx context.Context) error {
	c.logger.Info("Stopping coordinator")

	// Stop Overlay Networking Components
	if c.natTraversal != nil {
		if err := c.natTraversal.Stop(); err != nil {
			c.logger.Error("Failed to stop NAT traversal", zap.Error(err))
		}
	}

	if c.meshManager != nil {
		if err := c.meshManager.Stop(); err != nil {
			c.logger.Error("Failed to stop mesh manager", zap.Error(err))
		}
	}

	if c.peerManager != nil {
		if err := c.peerManager.Stop(); err != nil {
			c.logger.Error("Failed to stop peer manager", zap.Error(err))
		}
	}

	if c.transport != nil {
		if err := c.transport.Close(); err != nil {
			c.logger.Error("Failed to stop overlay transport", zap.Error(err))
		}
	}

	// Stop Membership Manager
	if c.membershipMgr != nil {
		if err := c.membershipMgr.Stop(); err != nil {
			c.logger.Error("Failed to stop membership manager", zap.Error(err))
		}
	}

	// Stop RAFT Store
	if c.raftStore != nil {
		if err := c.raftStore.Close(); err != nil {
			c.logger.Error("Failed to stop RAFT store", zap.Error(err))
		}
	}

	c.logger.Info("Coordinator stopped")
	return nil
}

// RegisterServices registers gRPC services
func (c *Coordinator) RegisterServices(server *grpc.Server) {
	// TODO: Register CoordinatorService
	// api.RegisterCoordinatorServiceServer(server, c)
}

// GetMembershipManager returns the membership manager
func (c *Coordinator) GetMembershipManager() *membership.Manager {
	return c.membershipMgr
}

// GetScheduler returns the scheduler
func (c *Coordinator) GetScheduler() *scheduler.Scheduler {
	return c.scheduler
}

// GetCA returns the certificate authority
func (c *Coordinator) GetCA() *mtls.CertificateAuthority {
	return c.ca
}

// GetTokenManager returns the token manager
func (c *Coordinator) GetTokenManager() *mtls.TokenManager {
	return c.tokenManager
}

// IsLeader returns whether this coordinator is the RAFT leader
func (c *Coordinator) IsLeader() bool {
	if c.raftStore == nil {
		return false
	}
	return c.raftStore.IsLeader()
}

// GetLeader returns the current RAFT leader address
func (c *Coordinator) GetLeader() string {
	if c.raftStore == nil {
		return ""
	}
	return c.raftStore.Leader()
}

// JoinCluster joins an existing RAFT cluster
func (c *Coordinator) JoinCluster(nodeID, addr string) error {
	if c.raftStore == nil {
		return fmt.Errorf("RAFT store not initialized")
	}
	return c.raftStore.Join(nodeID, addr)
}

// ScheduleWorkload schedules a workload across the cluster
func (c *Coordinator) ScheduleWorkload(ctx context.Context, spec scheduler.WorkloadSpec) ([]scheduler.PlacementDecision, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	// Get all ready nodes
	nodes := c.membershipMgr.ListNodes(membership.StateReady)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no ready nodes available")
	}

	// Schedule the workload
	decisions, err := c.scheduler.Schedule(ctx, spec, nodes)
	if err != nil {
		return nil, fmt.Errorf("failed to schedule workload: %w", err)
	}

	c.logger.Info("Workload scheduled",
		zap.String("workload", spec.Name),
		zap.Int("replicas", len(decisions)),
	)

	return decisions, nil
}

// Overlay Networking Methods

// GetServiceRegistry returns the service registry
func (c *Coordinator) GetServiceRegistry() *overlay.ServiceRegistry {
	return c.serviceRegistry
}

// GetLoadBalancer returns the load balancer
func (c *Coordinator) GetLoadBalancer() *overlay.L4LoadBalancer {
	return c.loadBalancer
}

// GetPeerManager returns the peer manager
func (c *Coordinator) GetPeerManager() *overlay.PeerManager {
	return c.peerManager
}

// GetMeshManager returns the mesh manager
func (c *Coordinator) GetMeshManager() *overlay.MeshManager {
	return c.meshManager
}

// GetNATTraversal returns the NAT traversal handler
func (c *Coordinator) GetNATTraversal() *overlay.NATTraversal {
	return c.natTraversal
}

// RegisterService registers a service in the overlay network
func (c *Coordinator) RegisterService(service *overlay.Service) error {
	if !c.IsLeader() {
		return fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	return c.serviceRegistry.RegisterService(service)
}

// DeregisterService removes a service from the overlay network
func (c *Coordinator) DeregisterService(name, namespace string) error {
	if !c.IsLeader() {
		return fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	return c.serviceRegistry.DeregisterService(name, namespace)
}

// AddPeer adds a peer to the mesh network
func (c *Coordinator) AddPeer(peer *overlay.Peer) error {
	return c.peerManager.AddPeer(peer)
}

// RemovePeer removes a peer from the mesh network
func (c *Coordinator) RemovePeer(peerID string) error {
	return c.peerManager.RemovePeer(peerID)
}

// GetMeshStats returns statistics about the mesh network
func (c *Coordinator) GetMeshStats() overlay.MeshStats {
	return c.meshManager.GetMeshStats()
}