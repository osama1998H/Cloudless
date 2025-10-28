package coordinator

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/coordinator/membership"
	"github.com/cloudless/cloudless/pkg/mtls"
	"github.com/cloudless/cloudless/pkg/observability"
	"github.com/cloudless/cloudless/pkg/overlay"
	"github.com/cloudless/cloudless/pkg/policy"
	"github.com/cloudless/cloudless/pkg/raft"
	"github.com/cloudless/cloudless/pkg/scheduler"
	"github.com/cloudless/cloudless/pkg/secrets"
	hashicorpraft "github.com/hashicorp/raft"
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

	// Security configuration
	TokenSecret      []byte        // Secret for signing enrollment tokens
	TokenExpiry      time.Duration // Token expiry duration
	CertificatesPath string        // Path to TLS certificates

	// Secrets management configuration
	SecretsEnabled         bool          // Enable secrets management service
	SecretsMasterKey       []byte        // Master encryption key for secrets (32 bytes for AES-256)
	SecretsMasterKeyID     string        // Master key identifier
	SecretsTokenSigningKey []byte        // Signing key for secret access tokens
	SecretsTokenTTL        time.Duration // TTL for secret access tokens
	SecretsRotationEnabled bool          // Enable automatic master key rotation
	SecretsRotationInterval time.Duration // Rotation interval
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
	// Security defaults
	if len(c.TokenSecret) == 0 {
		// Try to load from environment variable
		if secret := os.Getenv("CLOUDLESS_TOKEN_SECRET"); secret != "" {
			c.TokenSecret = []byte(secret)
		} else {
			// Generate a random secret if none provided (development only!)
			c.Logger.Warn("No token secret configured - generating random secret (NOT SUITABLE FOR PRODUCTION)")
			c.TokenSecret = []byte(fmt.Sprintf("dev-secret-%d", time.Now().UnixNano()))
		}
	}
	if c.TokenExpiry == 0 {
		c.TokenExpiry = 24 * time.Hour
	}
	if c.CertificatesPath == "" {
		c.CertificatesPath = filepath.Join(c.DataDir, "certs")
	}

	// Secrets management defaults
	if c.SecretsEnabled {
		if len(c.SecretsMasterKey) > 0 && len(c.SecretsMasterKey) != 32 {
			return fmt.Errorf("secrets master key must be exactly 32 bytes for AES-256, got %d", len(c.SecretsMasterKey))
		}
		if c.SecretsMasterKeyID == "" {
			c.SecretsMasterKeyID = "default-master-key"
		}
		if len(c.SecretsTokenSigningKey) == 0 {
			// Use same key as enrollment tokens for simplicity
			c.SecretsTokenSigningKey = c.TokenSecret
		}
		if len(c.SecretsTokenSigningKey) < 32 {
			return fmt.Errorf("secrets token signing key must be at least 32 bytes, got %d", len(c.SecretsTokenSigningKey))
		}
		if c.SecretsTokenTTL == 0 {
			c.SecretsTokenTTL = 15 * time.Minute // Short-lived tokens by default
		}
		if c.SecretsRotationInterval == 0 {
			c.SecretsRotationInterval = 90 * 24 * time.Hour // 90 days
		}
	}

	return nil
}

// Coordinator manages the control plane
type Coordinator struct {
	// Embed UnimplementedCoordinatorServiceServer for forward compatibility
	api.UnimplementedCoordinatorServiceServer

	config *Config
	logger *zap.Logger

	// Core components
	raftStore        *raft.Store
	membershipMgr    *membership.Manager
	scheduler        *scheduler.Scheduler
	ca               *mtls.CA
	certManager      *mtls.CertificateManager
	tokenManager     *mtls.TokenManager
	workloadStateMgr *WorkloadStateManager
	replicaMonitor   *ReplicaMonitor // CLD-REQ-031: Automatic failed replica rescheduling
	policyEngine     policy.PolicyEngine
	eventStream      *observability.EventStream
	secretsManager   *secrets.Manager // CLD-REQ-063: Secrets management service

	// Overlay networking components
	transport       *overlay.QUICTransport
	serviceRegistry *overlay.PersistentServiceRegistry
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
		DataDir:      caDir,
		Organization: "Cloudless",
		Country:      "US",
		Logger:       config.Logger,
	}
	ca, err := mtls.NewCA(caConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize CA: %w", err)
	}
	c.ca = ca

	// Initialize Certificate Manager
	config.Logger.Info("Initializing Certificate Manager")
	certManagerConfig := mtls.CertificateManagerConfig{
		CA:               ca,
		DataDir:          config.DataDir,
		Logger:           config.Logger,
		RotationInterval: 24 * time.Hour,
	}
	certManager, err := mtls.NewCertificateManager(certManagerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize certificate manager: %w", err)
	}
	c.certManager = certManager

	// Initialize Token Manager
	config.Logger.Info("Initializing Token Manager")
	tokenManagerConfig := mtls.TokenManagerConfig{
		SigningKey: config.TokenSecret,
		Logger:     config.Logger,
	}
	tokenManager, err := mtls.NewTokenManager(tokenManagerConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize token manager: %w", err)
	}
	c.tokenManager = tokenManager

	// Generate a bootstrap token for development/testing
	// TODO: Remove this in production and use proper token management API
	bootstrapToken, err := tokenManager.GenerateToken("", "", "", "", 365*24*time.Hour, 999)
	if err != nil {
		config.Logger.Warn("Failed to generate bootstrap token", zap.Error(err))
	} else {
		// SECURITY: Never log raw tokens, only token IDs
		config.Logger.Info("Generated bootstrap token for development",
			zap.String("token_id", bootstrapToken.ID),
			zap.Time("expires_at", bootstrapToken.ExpiresAt),
			zap.Int("max_uses", bootstrapToken.MaxUses),
		)
	}

	// Initialize RAFT Store (must be before membership manager)
	config.Logger.Info("Initializing RAFT Store")
	raftConfig := &raft.Config{
		RaftID:         config.RaftID,
		RaftBind:       config.RaftAddr,
		RaftDir:        raftDir,
		Bootstrap:      config.RaftBootstrap,
		Logger:         config.Logger,
		LocalID:        hashicorpraft.ServerID(config.RaftID),
		EnableSingle:   config.RaftBootstrap,
		SnapshotRetain: 2,
	}
	raftStore, err := raft.NewStore(raftConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize RAFT store: %w", err)
	}
	c.raftStore = raftStore

	// Initialize Membership Manager
	config.Logger.Info("Initializing Membership Manager")
	membershipConfig := membership.ManagerConfig{
		Store:        raftStore,
		TokenManager: tokenManager,
		CertManager:  certManager,
		Logger:       config.Logger,
	}
	membershipMgr, err := membership.NewManager(membershipConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize membership manager: %w", err)
	}
	c.membershipMgr = membershipMgr

	// Initialize Scheduler
	config.Logger.Info("Initializing Scheduler")
	schedulerConfig := &scheduler.SchedulerConfig{
		LocalityWeight:       config.SchedulerConfig.LocalityWeight,
		ReliabilityWeight:    config.SchedulerConfig.ReliabilityWeight,
		CostWeight:           config.SchedulerConfig.CostWeight,
		UtilizationWeight:    config.SchedulerConfig.UtilizationWeight,
		NetworkPenaltyWeight: config.SchedulerConfig.NetworkPenaltyWeight,
		MaxRetries:           5,
		RetryBackoff:         5 * time.Second,
		SpreadPolicy:         "zone",
		PackingStrategy:      "binpack",
		EnforcePlacement:     false,
	}
	schedulerInstance, err := scheduler.NewScheduler(membershipMgr, schedulerConfig, config.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize scheduler: %w", err)
	}
	c.scheduler = schedulerInstance

	// Initialize Workload State Manager
	config.Logger.Info("Initializing Workload State Manager")
	workloadStateMgr, err := NewWorkloadStateManager(raftStore, config.Logger)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize workload state manager: %w", err)
	}
	c.workloadStateMgr = workloadStateMgr

	// Initialize Replica Monitor (CLD-REQ-031)
	// Note: We pass 'c' (coordinator) to ReplicaMonitor, but it's not fully initialized yet.
	// This is safe because ReplicaMonitor only uses the coordinator reference in its reconcile
	// loop, which doesn't start until Start() is called.
	config.Logger.Info("Initializing Replica Monitor")
	replicaMonitorConfig := DefaultReplicaMonitorConfig(config.Logger)
	replicaMonitor, err := NewReplicaMonitor(replicaMonitorConfig, workloadStateMgr, c)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize replica monitor: %w", err)
	}
	c.replicaMonitor = replicaMonitor

	// Initialize Secrets Manager (CLD-REQ-063)
	if config.SecretsEnabled {
		config.Logger.Info("Initializing Secrets Manager")

		// Create secrets directory
		secretsDir := filepath.Join(config.DataDir, "secrets")
		if err := os.MkdirAll(secretsDir, 0700); err != nil { // 0700 for sensitive data
			return nil, fmt.Errorf("failed to create secrets directory: %w", err)
		}

		// Generate or load master key
		var masterKey []byte
		if len(config.SecretsMasterKey) == 32 {
			masterKey = config.SecretsMasterKey
		} else {
			// Generate a new master key
			generatedKey, err := secrets.GenerateMasterKey()
			if err != nil {
				return nil, fmt.Errorf("failed to generate master key: %w", err)
			}
			masterKey = generatedKey.Key
			config.Logger.Warn("Generated new master key for secrets - MUST persist this for production",
				zap.String("key_id", generatedKey.ID),
			)
		}

		// Create secrets manager configuration
		secretsConfig := secrets.ManagerConfig{
			MasterKeyID:         config.SecretsMasterKeyID,
			MasterKey:           masterKey,
			EnableAutoRotation:  config.SecretsRotationEnabled,
			RotationInterval:    config.SecretsRotationInterval,
			TokenTTL:            config.SecretsTokenTTL,
			TokenSigningKey:     config.SecretsTokenSigningKey,
			MaxTokenUsesDefault: 1, // Single-use tokens by default
			DataDir:             secretsDir,
			EnableAuditLog:      true,
			AuditRetention:      90 * 24 * time.Hour,
		}

		// Create RAFT-backed secret store
		secretStore, err := secrets.NewRaftSecretStore(raftStore, config.Logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create secret store: %w", err)
		}

		// Initialize secrets manager
		secretsManager, err := secrets.NewManager(secretsConfig, secretStore, config.Logger)
		if err != nil {
			return nil, fmt.Errorf("failed to initialize secrets manager: %w", err)
		}
		c.secretsManager = secretsManager

		config.Logger.Info("Secrets Manager initialized",
			zap.String("master_key_id", config.SecretsMasterKeyID),
			zap.Duration("token_ttl", config.SecretsTokenTTL),
			zap.Bool("auto_rotation", config.SecretsRotationEnabled),
		)
	}

	// Initialize Overlay Networking Components
	config.Logger.Info("Initializing Overlay Networking")

	// Set default overlay config if not provided
	if config.OverlayConfig.NodeID == "" {
		config.OverlayConfig.NodeID = config.RaftID
	}
	if config.OverlayConfig.Transport.ListenAddress == "" {
		config.OverlayConfig.Transport.ListenAddress = ":9090" // Default overlay port
	}
	if config.OverlayConfig.Mesh.HealthCheckInterval == 0 {
		config.OverlayConfig.Mesh.HealthCheckInterval = 30 * time.Second
	}
	if config.OverlayConfig.Mesh.ReconnectInterval == 0 {
		config.OverlayConfig.Mesh.ReconnectInterval = 60 * time.Second
	}
	if config.OverlayConfig.Mesh.ConnectionTimeout == 0 {
		config.OverlayConfig.Mesh.ConnectionTimeout = 10 * time.Second
	}

	// Create TLS config from CertificateManager
	serverCertificate, err := certManager.GenerateNodeCertificate(
		config.RaftID,
		config.RaftID,
		[]net.IP{net.ParseIP("127.0.0.1")},
		[]string{config.RaftID, "localhost"},
		365, // 1 year validity
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate server certificate: %w", err)
	}

	// Convert to tls.Certificate
	serverCert, err := tls.X509KeyPair(serverCertificate.CertPEM, serverCertificate.KeyPEM)
	if err != nil {
		return nil, fmt.Errorf("failed to create TLS certificate: %w", err)
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

	// Initialize service registry with RAFT persistence (CLD-REQ-041)
	ctx := context.Background()
	serviceRegistry, err := overlay.NewPersistentServiceRegistry(
		ctx,
		raftStore,
		config.OverlayConfig.Registry,
		config.Logger,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize service registry: %w", err)
	}
	c.serviceRegistry = serviceRegistry

	// Initialize load balancer (uses embedded ServiceRegistry)
	loadBalancer := overlay.NewL4LoadBalancer(
		config.OverlayConfig.LoadBalancer,
		serviceRegistry.ServiceRegistry,
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

	// Initialize event stream for observability
	c.logger.Info("Initializing event stream")
	c.eventStream = observability.NewEventStream(observability.EventStreamConfig{
		MaxSize:   10000,
		Retention: 24 * time.Hour,
	}, c.logger)

	// Initialize policy engine
	c.logger.Info("Initializing policy engine")
	if err := c.InitializePolicyEngine(); err != nil {
		c.logger.Warn("Failed to initialize policy engine", zap.Error(err))
		// Continue without policy engine - it's optional for backward compatibility
	}

	// RAFT store is already initialized and bootstrapped during NewStore()
	// Wait for leader election
	c.logger.Info("Waiting for leader election")
	if err := c.raftStore.WaitForLeader(30 * time.Second); err != nil {
		return fmt.Errorf("timeout waiting for leader election: %w", err)
	}

	// Check if this node is the leader
	if c.raftStore.IsLeader() {
		c.isLeader = true
		c.logger.Info("This node is the leader")
	} else {
		leader := c.raftStore.GetLeader()
		c.logger.Info("Leader elected", zap.String("leader", leader))
	}

	// Start Membership Manager
	if err := c.membershipMgr.Start(ctx); err != nil {
		return fmt.Errorf("failed to start membership manager: %w", err)
	}

	// Start Replica Monitor (CLD-REQ-031)
	if c.replicaMonitor != nil {
		c.logger.Info("Starting replica monitor")
		if err := c.replicaMonitor.Start(ctx); err != nil {
			return fmt.Errorf("failed to start replica monitor: %w", err)
		}
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

	// Stop Replica Monitor (CLD-REQ-031)
	if c.replicaMonitor != nil {
		c.logger.Info("Stopping replica monitor")
		if err := c.replicaMonitor.Stop(); err != nil {
			c.logger.Error("Failed to stop replica monitor", zap.Error(err))
		}
	}

	// Stop Membership Manager
	if c.membershipMgr != nil {
		if err := c.membershipMgr.Stop(ctx); err != nil {
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
	api.RegisterCoordinatorServiceServer(server, c)
	c.logger.Info("Registered CoordinatorService gRPC handlers")

	// Register SecretsService if enabled (CLD-REQ-063)
	if c.secretsManager != nil {
		secretsService := NewSecretsServiceServer(c)
		api.RegisterSecretsServiceServer(server, secretsService)
		c.logger.Info("Registered SecretsService gRPC handlers")
	}
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
func (c *Coordinator) GetCA() *mtls.CA {
	return c.ca
}

// GetTokenManager returns the token manager
func (c *Coordinator) GetTokenManager() *mtls.TokenManager {
	return c.tokenManager
}

// GetSecretsManager returns the secrets manager (CLD-REQ-063)
func (c *Coordinator) GetSecretsManager() *secrets.Manager {
	return c.secretsManager
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
	return c.raftStore.GetLeader()
}

// JoinCluster joins an existing RAFT cluster
func (c *Coordinator) JoinCluster(nodeID, addr string) error {
	if c.raftStore == nil {
		return fmt.Errorf("RAFT store not initialized")
	}
	return c.raftStore.Join(nodeID, addr)
}

// ScheduleWorkload schedules a workload across the cluster
func (c *Coordinator) ScheduleWorkload(ctx context.Context, spec scheduler.WorkloadSpec) ([]scheduler.ScheduleDecision, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	// Schedule the workload
	result, err := c.scheduler.Schedule(ctx, &spec)
	if err != nil {
		return nil, fmt.Errorf("failed to schedule workload: %w", err)
	}

	if !result.Success {
		c.logger.Warn("Workload scheduling incomplete",
			zap.String("workload", spec.Name),
			zap.String("message", result.Message),
			zap.Int("unscheduled", result.UnscheduledReplicas),
		)
	}

	c.logger.Info("Workload scheduled",
		zap.String("workload", spec.Name),
		zap.Int("replicas", len(result.Decisions)),
	)

	return result.Decisions, nil
}

// Overlay Networking Methods

// GetServiceRegistry returns the persistent service registry
func (c *Coordinator) GetServiceRegistry() *overlay.PersistentServiceRegistry {
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
