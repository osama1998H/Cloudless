package membership

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/mtls"
	"github.com/cloudless/cloudless/pkg/observability"
	"github.com/cloudless/cloudless/pkg/raft"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/durationpb"
)

const (
	// Node states
	StateEnrolling = "enrolling"
	StateReady     = "ready"
	StateDraining  = "draining"
	StateCordoned  = "cordoned"
	StateOffline   = "offline"
	StateFailed    = "failed"

	// Timeouts
	DefaultHeartbeatInterval = 10 * time.Second

	// CLD-REQ-002: HeartbeatTimeout reduced from 30s to 10s to meet leave convergence SLA
	//
	// Design Constraint: Setting this below 10s would achieve P50 < 5s for leave convergence,
	// but would cause false positives from:
	// - Network jitter (100-500ms common)
	// - GC pauses (100-300ms)
	// - Brief CPU spikes
	// - WiFi reconnects
	//
	// Current configuration:
	// - HeartbeatTimeout: 10s
	// - Health check interval: 2s (see monitorHealth)
	// - Leave convergence P50: ~11.8s (aspirational target: 5s)
	// - Leave convergence P95: ~11.9s (MEETS target: < 15s) ✅
	//
	// Rationale: P95 < 15s is the critical SLA. P50 ~11.8s is acceptable tradeoff
	// to avoid operational instability from false positive offline detections.
	//
	// See: CLD-REQ-002-DESIGN.md for full analysis
	HeartbeatTimeout  = 10 * time.Second
	EnrollmentTimeout = 60 * time.Second
)

// Manager manages node membership in the cluster
type Manager struct {
	store          *raft.Store
	tokenManager   *mtls.TokenManager
	certManager    *mtls.CertificateManager
	logger         *zap.Logger
	nodes          map[string]*NodeInfo
	healthMonitor  *HealthMonitor
	scoringEngine  *ScoringEngine

	// Workload assignment tracking
	pendingAssignments map[string][]*api.WorkloadAssignment // nodeID -> assignments
	pendingCommands    map[string][]string                 // nodeID -> commands
	assignmentMu       sync.RWMutex                         // protects assignment maps

	mu     sync.RWMutex
	stopCh chan struct{}
}

// NodeInfo represents comprehensive node information
type NodeInfo struct {
	ID                string              `json:"id"`
	Name              string              `json:"name"`
	Region            string              `json:"region"`
	Zone              string              `json:"zone"`
	State             string              `json:"state"`
	LastHeartbeat     time.Time           `json:"last_heartbeat"`
	EnrolledAt        time.Time           `json:"enrolled_at"`

	// CLD-REQ-002: Membership convergence timing tracking
	JoinStartedAt     time.Time           `json:"join_started_at,omitempty"`
	JoinConvergedAt   time.Time           `json:"join_converged_at,omitempty"`
	LeaveStartedAt    time.Time           `json:"leave_started_at,omitempty"`
	LeaveConvergedAt  time.Time           `json:"leave_converged_at,omitempty"`

	Capabilities      NodeCapabilities    `json:"capabilities"`
	Capacity          ResourceCapacity    `json:"capacity"`
	Usage             ResourceUsage       `json:"usage"`
	Labels            map[string]string   `json:"labels"`
	Taints            []Taint             `json:"taints"`
	Certificate       *mtls.Certificate   `json:"-"`
	ReliabilityScore  float64             `json:"reliability_score"`
	Fragments         []FragmentInfo      `json:"fragments"`
	Containers        []ContainerInfo     `json:"containers"`
	NetworkInfo       NetworkInfo         `json:"network_info"`
	Conditions        []NodeCondition     `json:"conditions"`
	HeartbeatInterval time.Duration       `json:"heartbeat_interval"`
}

// NodeCapabilities describes what a node can do
type NodeCapabilities struct {
	ContainerRuntimes []string `json:"container_runtimes"`
	SupportsGPU       bool     `json:"supports_gpu"`
	SupportsARM       bool     `json:"supports_arm"`
	SupportsX86       bool     `json:"supports_x86"`
	NetworkFeatures   []string `json:"network_features"`
	StorageClasses    []string `json:"storage_classes"`
}

// ResourceCapacity represents total resources
type ResourceCapacity struct {
	CPUMillicores int64 `json:"cpu_millicores"`
	MemoryBytes   int64 `json:"memory_bytes"`
	StorageBytes  int64 `json:"storage_bytes"`
	BandwidthBPS  int64 `json:"bandwidth_bps"`
	GPUCount      int32 `json:"gpu_count"`
}

// ResourceUsage represents current resource usage
type ResourceUsage struct {
	CPUMillicores int64   `json:"cpu_millicores"`
	MemoryBytes   int64   `json:"memory_bytes"`
	StorageBytes  int64   `json:"storage_bytes"`
	BandwidthBPS  int64   `json:"bandwidth_bps"`
	GPUCount      int32   `json:"gpu_count"`
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
}

// Taint marks a node with special conditions
type Taint struct {
	Key    string `json:"key"`
	Value  string `json:"value"`
	Effect string `json:"effect"` // NoSchedule, PreferNoSchedule, NoExecute
}

// FragmentInfo represents fragment allocation on a node
type FragmentInfo struct {
	ID         string           `json:"id"`
	WorkloadID string           `json:"workload_id"`
	Resources  ResourceCapacity `json:"resources"`
	State      string           `json:"state"`
}

// ContainerInfo represents container status on a node
type ContainerInfo struct {
	ID         string        `json:"id"`
	WorkloadID string        `json:"workload_id"`
	State      string        `json:"state"`
	StartedAt  time.Time     `json:"started_at"`
	Usage      ResourceUsage `json:"usage"`
}

// NetworkInfo contains node network information
type NetworkInfo struct {
	PrivateIP     string   `json:"private_ip"`
	PublicIP      string   `json:"public_ip"`
	Hostname      string   `json:"hostname"`
	DNSNames      []string `json:"dns_names"`
	OverlayIP     string   `json:"overlay_ip"`
	ListenAddress string   `json:"listen_address"`
}

// NodeCondition represents a condition of a node
type NodeCondition struct {
	Type           string    `json:"type"` // Ready, MemoryPressure, DiskPressure, NetworkUnavailable
	Status         string    `json:"status"` // True, False, Unknown
	Reason         string    `json:"reason"`
	Message        string    `json:"message"`
	LastTransition time.Time `json:"last_transition"`
}

// ManagerConfig contains configuration for the membership manager
type ManagerConfig struct {
	Store         *raft.Store
	TokenManager  *mtls.TokenManager
	CertManager   *mtls.CertificateManager
	Logger        *zap.Logger
}

// NewManager creates a new membership manager
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.Store == nil {
		return nil, fmt.Errorf("raft store is required")
	}
	if config.TokenManager == nil {
		return nil, fmt.Errorf("token manager is required")
	}
	if config.CertManager == nil {
		return nil, fmt.Errorf("certificate manager is required")
	}
	if config.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	m := &Manager{
		store:              config.Store,
		tokenManager:       config.TokenManager,
		certManager:        config.CertManager,
		logger:             config.Logger,
		nodes:              make(map[string]*NodeInfo),
		pendingAssignments: make(map[string][]*api.WorkloadAssignment),
		pendingCommands:    make(map[string][]string),
		stopCh:             make(chan struct{}),
	}

	// Initialize health monitor
	m.healthMonitor = NewHealthMonitor(config.Logger, HeartbeatTimeout)

	// Initialize scoring engine
	m.scoringEngine = NewScoringEngine(config.Logger)

	// Load existing nodes from store
	if err := m.loadNodes(); err != nil {
		config.Logger.Warn("Failed to load nodes from store", zap.Error(err))
	}

	return m, nil
}

// Start starts the membership manager
func (m *Manager) Start(ctx context.Context) error {
	m.logger.Info("Starting membership manager")

	// Start health monitoring
	go m.monitorHealth(ctx)

	// Start reliability scoring updates
	go m.updateReliabilityScores(ctx)

	// Start expired node cleanup
	go m.cleanupExpiredNodes(ctx)

	m.logger.Info("Membership manager started")
	return nil
}

// Stop stops the membership manager
func (m *Manager) Stop(ctx context.Context) error {
	m.logger.Info("Stopping membership manager")
	close(m.stopCh)

	// Save nodes to store
	if err := m.saveNodes(); err != nil {
		m.logger.Error("Failed to save nodes", zap.Error(err))
	}

	m.logger.Info("Membership manager stopped")
	return nil
}

// EnrollNode handles node enrollment
func (m *Manager) EnrollNode(ctx context.Context, req *api.EnrollNodeRequest) (*api.EnrollNodeResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.Info("Processing enrollment request",
		zap.String("node_name", req.NodeName),
		zap.String("region", req.Region),
		zap.String("zone", req.Zone),
	)

	// Validate token
	token, err := m.tokenManager.ValidateToken(req.Token)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}

	// Generate node ID if not in token
	nodeID := token.NodeID
	if nodeID == "" {
		nodeID = fmt.Sprintf("node-%s-%d", req.NodeName, time.Now().Unix())
	}

	// Check if node already exists
	if existing, exists := m.nodes[nodeID]; exists {
		if existing.State != StateOffline && existing.State != StateFailed {
			return nil, fmt.Errorf("node %s already enrolled", nodeID)
		}
	}

	// Extract IPs and DNS names
	var ips []net.IP
	var dnsNames []string

	if req.Capabilities != nil {
		// Extract network information from capabilities
		// This would come from the actual request
	}

	// Generate node certificate
	cert, err := m.certManager.GenerateNodeCertificate(
		nodeID,
		req.NodeName,
		ips,
		dnsNames,
		30, // 30 days validity
	)
	if err != nil {
		return nil, fmt.Errorf("failed to generate certificate: %w", err)
	}

	// Create node info
	now := time.Now()
	node := &NodeInfo{
		ID:            nodeID,
		Name:          req.NodeName,
		Region:        req.Region,
		Zone:          req.Zone,
		State:         StateEnrolling,
		EnrolledAt:    now,
		LastHeartbeat: now,

		// CLD-REQ-002: Track join convergence timing
		JoinStartedAt: now,

		Labels:        req.Labels,
		Certificate:   cert,
		HeartbeatInterval: DefaultHeartbeatInterval,
		ReliabilityScore: 1.0, // Start with perfect score
	}

	// Set capabilities
	if req.Capabilities != nil {
		node.Capabilities = NodeCapabilities{
			ContainerRuntimes: req.Capabilities.ContainerRuntimes,
			SupportsGPU:       req.Capabilities.SupportsGpu,
			SupportsARM:       req.Capabilities.SupportsArm,
			SupportsX86:       req.Capabilities.SupportsX86,
			NetworkFeatures:   req.Capabilities.NetworkFeatures,
			StorageClasses:    req.Capabilities.StorageClasses,
		}
	}

	// Set capacity
	if req.Capacity != nil {
		node.Capacity = ResourceCapacity{
			CPUMillicores: req.Capacity.CpuMillicores,
			MemoryBytes:   req.Capacity.MemoryBytes,
			StorageBytes:  req.Capacity.StorageBytes,
			BandwidthBPS:  req.Capacity.BandwidthBps,
			GPUCount:      req.Capacity.GpuCount,
		}
	}

	// Store node
	m.nodes[nodeID] = node

	// Mark token as used
	if err := m.tokenManager.UseToken(token.ID, nodeID); err != nil {
		m.logger.Error("Failed to mark token as used", zap.Error(err))
	}

	// Persist to RAFT store
	if err := m.saveNode(node); err != nil {
		m.logger.Error("Failed to persist node", zap.Error(err))
		// Continue anyway, will be saved on next update
	}

	m.logger.Info("Node enrolled successfully",
		zap.String("node_id", nodeID),
		zap.String("node_name", req.NodeName),
	)

	// Create response with proper duration
	heartbeatDuration := durationpb.New(DefaultHeartbeatInterval)

	// Get CA certificate from certificate manager
	caCertPEM := m.certManager.GetCACertificate()

	response := &api.EnrollNodeResponse{
		NodeId:            nodeID,
		Certificate:       cert.CertPEM,
		CaCertificate:     caCertPEM,
		HeartbeatInterval: heartbeatDuration,
	}

	return response, nil
}

// ProcessHeartbeat processes a heartbeat from a node
func (m *Manager) ProcessHeartbeat(ctx context.Context, req *api.HeartbeatRequest) (*api.HeartbeatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	node, exists := m.nodes[req.NodeId]
	if !exists {
		return nil, fmt.Errorf("node not found: %s", req.NodeId)
	}

	// Update heartbeat timestamp
	node.LastHeartbeat = time.Now()

	// Update state if necessary
	if node.State == StateEnrolling {
		node.State = StateReady

		// CLD-REQ-002: Track join convergence completion
		node.JoinConvergedAt = time.Now()
		convergenceTime := node.JoinConvergedAt.Sub(node.JoinStartedAt)

		m.logger.Info("Node transitioned to ready state",
			zap.String("node_id", req.NodeId),
			zap.Duration("join_convergence_time", convergenceTime),
		)

		// CLD-REQ-002: Emit join convergence metric
		observability.MembershipJoinConvergenceSeconds.Observe(convergenceTime.Seconds())
	}

	// Update resource usage
	if req.Usage != nil {
		node.Usage = ResourceUsage{
			CPUMillicores: req.Usage.CpuMillicores,
			MemoryBytes:   req.Usage.MemoryBytes,
			StorageBytes:  req.Usage.StorageBytes,
			BandwidthBPS:  req.Usage.BandwidthBps,
			GPUCount:      req.Usage.GpuCount,
		}

		// Calculate percentages
		if node.Capacity.CPUMillicores > 0 {
			node.Usage.CPUPercent = float64(node.Usage.CPUMillicores) / float64(node.Capacity.CPUMillicores) * 100
		}
		if node.Capacity.MemoryBytes > 0 {
			node.Usage.MemoryPercent = float64(node.Usage.MemoryBytes) / float64(node.Capacity.MemoryBytes) * 100
		}
	}

	// Update fragments
	if req.Fragments != nil {
		node.Fragments = make([]FragmentInfo, 0, len(req.Fragments))
		for _, frag := range req.Fragments {
			fragmentInfo := FragmentInfo{
				ID:         frag.Id,
				WorkloadID: frag.WorkloadId,
				State:      frag.GetStatus().GetState().String(),
			}

			// Map resources if available
			if frag.Resources != nil {
				fragmentInfo.Resources = ResourceCapacity{
					CPUMillicores: frag.Resources.CpuMillicores,
					MemoryBytes:   frag.Resources.MemoryBytes,
					StorageBytes:  frag.Resources.StorageBytes,
					BandwidthBPS:  frag.Resources.BandwidthBps,
					GPUCount:      frag.Resources.GpuCount,
				}
			}

			node.Fragments = append(node.Fragments, fragmentInfo)
		}
	}

	// Update containers
	if req.Containers != nil {
		node.Containers = make([]ContainerInfo, 0, len(req.Containers))
		for _, c := range req.Containers {
			node.Containers = append(node.Containers, ContainerInfo{
				ID:         c.ContainerId,
				WorkloadID: "", // Extract from container metadata
				State:      c.State,
			})
		}
	}

	// Persist updates
	if err := m.saveNode(node); err != nil {
		m.logger.Error("Failed to persist node updates", zap.Error(err))
	}

	// Get pending assignments and commands for this node
	m.assignmentMu.Lock()
	assignments := m.pendingAssignments[req.NodeId]
	commands := m.pendingCommands[req.NodeId]

	// Clear pending assignments and commands after retrieving
	delete(m.pendingAssignments, req.NodeId)
	delete(m.pendingCommands, req.NodeId)
	m.assignmentMu.Unlock()

	// Log if we're sending assignments or commands
	if len(assignments) > 0 {
		m.logger.Info("Sending workload assignments to node",
			zap.String("node_id", req.NodeId),
			zap.Int("count", len(assignments)),
		)
	}

	if len(commands) > 0 {
		m.logger.Info("Sending commands to node",
			zap.String("node_id", req.NodeId),
			zap.Int("count", len(commands)),
		)
	}

	// Check for containers that need to be stopped (based on node state)
	if node.State == StateDraining {
		// Add drain command if not already present
		drainCommandFound := false
		for _, cmd := range commands {
			if cmd == "drain:graceful" {
				drainCommandFound = true
				break
			}
		}
		if !drainCommandFound {
			commands = append(commands, "drain:graceful")
			m.logger.Info("Added drain command to node",
				zap.String("node_id", req.NodeId),
			)
		}
	}

	// Create response with proper duration
	nextHeartbeat := durationpb.New(node.HeartbeatInterval)

	response := &api.HeartbeatResponse{
		Assignments:      assignments,
		ContainersToStop: commands,
		NextHeartbeat:    nextHeartbeat,
	}

	return response, nil
}

// GetNode retrieves node information
func (m *Manager) GetNode(nodeID string) (*NodeInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	node, exists := m.nodes[nodeID]
	if !exists {
		return nil, fmt.Errorf("node not found: %s", nodeID)
	}

	return node, nil
}

// ListNodes lists all nodes
func (m *Manager) ListNodes(filters map[string]string) ([]*NodeInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	nodes := make([]*NodeInfo, 0, len(m.nodes))
	for _, node := range m.nodes {
		// Apply filters
		if !matchesFilters(node, filters) {
			continue
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// DrainNode starts draining a node
func (m *Manager) DrainNode(nodeID string, graceful bool, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	node, exists := m.nodes[nodeID]
	if !exists {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	if node.State == StateDraining {
		return fmt.Errorf("node already draining")
	}

	node.State = StateDraining

	// Add taint to prevent new workloads
	node.Taints = append(node.Taints, Taint{
		Key:    "cloudless.io/drain",
		Value:  "true",
		Effect: "NoSchedule",
	})

	// Persist state change
	if err := m.saveNode(node); err != nil {
		return fmt.Errorf("failed to persist node state: %w", err)
	}

	m.logger.Info("Node marked for draining",
		zap.String("node_id", nodeID),
		zap.Bool("graceful", graceful),
		zap.Duration("timeout", timeout),
	)

	// Trigger workload migration
	// This initiates the process of moving all workloads off the draining node
	if err := m.triggerWorkloadMigration(nodeID, graceful, timeout); err != nil {
		m.logger.Error("Failed to trigger workload migration",
			zap.String("node_id", nodeID),
			zap.Error(err),
		)
		// Continue anyway - the drain command will be sent on next heartbeat
	}

	return nil
}

// UncordonNode removes scheduling restrictions from a node
func (m *Manager) UncordonNode(nodeID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	node, exists := m.nodes[nodeID]
	if !exists {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	// Remove cordon state
	if node.State == StateCordoned {
		node.State = StateReady
	}

	// Remove drain taints
	newTaints := []Taint{}
	for _, taint := range node.Taints {
		if taint.Key != "cloudless.io/drain" && taint.Key != "cloudless.io/cordon" {
			newTaints = append(newTaints, taint)
		}
	}
	node.Taints = newTaints

	// Persist state change
	if err := m.saveNode(node); err != nil {
		return fmt.Errorf("failed to persist node state: %w", err)
	}

	m.logger.Info("Node uncordoned",
		zap.String("node_id", nodeID),
	)

	return nil
}

// monitorHealth monitors node health
//
// CLD-REQ-002 Design: This function runs periodically to detect nodes that have
// exceeded HeartbeatTimeout and mark them as offline.
//
// Health check interval: 2 seconds (reduced from 5s)
//
// Design Constraint: This interval determines detection granularity:
// - Smaller interval (e.g., 1s): Faster detection, higher CPU usage
// - Larger interval (e.g., 5s): Slower detection, lower CPU usage
//
// Current configuration achieves:
// - Detection latency: HeartbeatTimeout (10s) + interval (0-2s) = 10-12s
// - Leave convergence P95: ~11.9s (MEETS < 15s target)
// - CPU overhead: Negligible (simple timestamp comparison every 2s)
//
// See: CLD-REQ-002-DESIGN.md for full analysis
func (m *Manager) monitorHealth(ctx context.Context) {
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.checkNodeHealth()
		}
	}
}

// checkNodeHealth checks health of all nodes
func (m *Manager) checkNodeHealth() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	for nodeID, node := range m.nodes {
		timeSinceHeartbeat := now.Sub(node.LastHeartbeat)

		// Check if node is offline
		if timeSinceHeartbeat > HeartbeatTimeout {
			if node.State != StateOffline && node.State != StateFailed {
				// CLD-REQ-002: Track leave convergence timing
				// LeaveStartedAt is when the node stopped sending heartbeats
				// LeaveConvergedAt is when we detected and marked it as offline
				node.LeaveStartedAt = node.LastHeartbeat
				node.LeaveConvergedAt = now
				convergenceTime := node.LeaveConvergedAt.Sub(node.LeaveStartedAt)

				m.logger.Warn("Node marked as offline",
					zap.String("node_id", nodeID),
					zap.Duration("since_heartbeat", timeSinceHeartbeat),
					zap.Duration("leave_convergence_time", convergenceTime),
				)
				node.State = StateOffline

				// Update conditions
				node.Conditions = append(node.Conditions, NodeCondition{
					Type:           "Ready",
					Status:         "False",
					Reason:         "HeartbeatTimeout",
					Message:        fmt.Sprintf("No heartbeat for %v", timeSinceHeartbeat),
					LastTransition: now,
				})

				// CLD-REQ-002: Emit leave convergence metric
				observability.MembershipLeaveConvergenceSeconds.Observe(convergenceTime.Seconds())
			}
		}

		// Check resource pressure
		if node.Usage.MemoryPercent > 90 {
			addCondition(node, "MemoryPressure", "True", "HighMemoryUsage",
				fmt.Sprintf("Memory usage at %.1f%%", node.Usage.MemoryPercent))
		}

		if node.Usage.CPUPercent > 90 {
			addCondition(node, "CPUPressure", "True", "HighCPUUsage",
				fmt.Sprintf("CPU usage at %.1f%%", node.Usage.CPUPercent))
		}
	}
}

// updateReliabilityScores updates reliability scores for all nodes
func (m *Manager) updateReliabilityScores(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.mu.Lock()
			for _, node := range m.nodes {
				node.ReliabilityScore = m.scoringEngine.CalculateReliability(node)
			}
			m.mu.Unlock()
		}
	}
}

// cleanupExpiredNodes removes nodes that have been offline too long
func (m *Manager) cleanupExpiredNodes(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-m.stopCh:
			return
		case <-ticker.C:
			m.removeExpiredNodes()
		}
	}
}

// removeExpiredNodes removes nodes offline for more than 24 hours
func (m *Manager) removeExpiredNodes() {
	m.mu.Lock()
	defer m.mu.Unlock()

	now := time.Now()
	toRemove := []string{}

	for nodeID, node := range m.nodes {
		if node.State == StateOffline {
			offlineDuration := now.Sub(node.LastHeartbeat)
			if offlineDuration > 24*time.Hour {
				toRemove = append(toRemove, nodeID)
			}
		}
	}

	for _, nodeID := range toRemove {
		delete(m.nodes, nodeID)
		m.logger.Info("Removed expired node",
			zap.String("node_id", nodeID),
		)

		// Remove from RAFT store
		m.store.Delete(fmt.Sprintf("node:%s", nodeID))
	}
}

// saveNode persists a node to the RAFT store
func (m *Manager) saveNode(node *NodeInfo) error {
	data, err := json.Marshal(node)
	if err != nil {
		return fmt.Errorf("failed to marshal node: %w", err)
	}

	key := fmt.Sprintf("node:%s", node.ID)
	return m.store.Set(key, data)
}

// loadNodes loads all nodes from the RAFT store
func (m *Manager) loadNodes() error {
	// Get all keys with prefix "node:"
	allData, err := m.store.GetAll()
	if err != nil {
		return fmt.Errorf("failed to get nodes from store: %w", err)
	}

	for key, data := range allData {
		if len(key) > 5 && key[:5] == "node:" {
			var node NodeInfo
			if err := json.Unmarshal(data, &node); err != nil {
				m.logger.Warn("Failed to unmarshal node",
					zap.String("key", key),
					zap.Error(err),
				)
				continue
			}
			m.nodes[node.ID] = &node
		}
	}

	m.logger.Info("Loaded nodes from store", zap.Int("count", len(m.nodes)))
	return nil
}

// saveNodes persists all nodes to the RAFT store
func (m *Manager) saveNodes() error {
	for _, node := range m.nodes {
		if err := m.saveNode(node); err != nil {
			m.logger.Error("Failed to save node",
				zap.String("node_id", node.ID),
				zap.Error(err),
			)
		}
	}
	return nil
}

// matchesFilters checks if a node matches the given filters
func matchesFilters(node *NodeInfo, filters map[string]string) bool {
	for key, value := range filters {
		switch key {
		case "state":
			if node.State != value {
				return false
			}
		case "region":
			if node.Region != value {
				return false
			}
		case "zone":
			if node.Zone != value {
				return false
			}
		default:
			// Check labels
			if labelValue, exists := node.Labels[key]; !exists || labelValue != value {
				return false
			}
		}
	}
	return true
}

// addCondition adds or updates a node condition
func addCondition(node *NodeInfo, condType, status, reason, message string) {
	now := time.Now()

	// Check if condition already exists
	for i, cond := range node.Conditions {
		if cond.Type == condType {
			if cond.Status != status {
				node.Conditions[i] = NodeCondition{
					Type:           condType,
					Status:         status,
					Reason:         reason,
					Message:        message,
					LastTransition: now,
				}
			}
			return
		}
	}

	// Add new condition
	node.Conditions = append(node.Conditions, NodeCondition{
		Type:           condType,
		Status:         status,
		Reason:         reason,
		Message:        message,
		LastTransition: now,
	})
}

// QueueAssignment adds a workload assignment to the pending queue for a node
// This method is called by the scheduler after making placement decisions
func (m *Manager) QueueAssignment(nodeID string, assignment *api.WorkloadAssignment) error {
	m.assignmentMu.Lock()
	defer m.assignmentMu.Unlock()

	// Check if node exists
	m.mu.RLock()
	_, exists := m.nodes[nodeID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	m.pendingAssignments[nodeID] = append(m.pendingAssignments[nodeID], assignment)

	m.logger.Debug("Queued workload assignment",
		zap.String("node_id", nodeID),
		zap.String("fragment_id", assignment.FragmentId),
	)

	return nil
}

// QueueCommand adds a command to the pending queue for a node
// This method is called to send administrative commands to nodes
func (m *Manager) QueueCommand(nodeID string, command string) error {
	m.assignmentMu.Lock()
	defer m.assignmentMu.Unlock()

	// Check if node exists
	m.mu.RLock()
	_, exists := m.nodes[nodeID]
	m.mu.RUnlock()

	if !exists {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	m.pendingCommands[nodeID] = append(m.pendingCommands[nodeID], command)

	m.logger.Debug("Queued command for node",
		zap.String("node_id", nodeID),
		zap.String("command", command),
	)

	return nil
}

// triggerWorkloadMigration initiates migration of all workloads from a draining node
func (m *Manager) triggerWorkloadMigration(nodeID string, graceful bool, timeout time.Duration) error {
	m.mu.RLock()
	node, exists := m.nodes[nodeID]
	if !exists {
		m.mu.RUnlock()
		return fmt.Errorf("node not found: %s", nodeID)
	}

	// Get list of containers on the node
	containers := make([]ContainerInfo, len(node.Containers))
	copy(containers, node.Containers)
	m.mu.RUnlock()

	m.logger.Info("Triggering workload migration",
		zap.String("node_id", nodeID),
		zap.Int("container_count", len(containers)),
		zap.Bool("graceful", graceful),
	)

	// Queue stop commands for all containers on the draining node
	for _, container := range containers {
		var stopCmd string
		if graceful {
			stopCmd = fmt.Sprintf("stop:graceful:%s", container.ID)
		} else {
			stopCmd = fmt.Sprintf("stop:force:%s", container.ID)
		}

		if err := m.QueueCommand(nodeID, stopCmd); err != nil {
			m.logger.Error("Failed to queue stop command",
				zap.String("node_id", nodeID),
				zap.String("container_id", container.ID),
				zap.Error(err),
			)
			continue
		}

		m.logger.Debug("Queued container stop command",
			zap.String("node_id", nodeID),
			zap.String("container_id", container.ID),
			zap.String("workload_id", container.WorkloadID),
		)
	}

	// NOTE: The actual rescheduling of workloads is handled by the scheduler
	// The scheduler should detect that workloads are missing their desired replica count
	// and automatically reschedule them to other available nodes
	//
	// In a production implementation, you would:
	// 1. Get the workload IDs from the containers
	// 2. Call the scheduler to reschedule each workload
	// 3. Queue the new assignments to the target nodes
	//
	// For now, we're just stopping the containers and letting the reconciliation
	// loop handle the rescheduling

	m.logger.Info("Workload migration initiated",
		zap.String("node_id", nodeID),
		zap.Int("containers_to_stop", len(containers)),
	)

	return nil
}

// GetPendingAssignmentCount returns the number of pending assignments for a node
func (m *Manager) GetPendingAssignmentCount(nodeID string) int {
	m.assignmentMu.RLock()
	defer m.assignmentMu.RUnlock()

	return len(m.pendingAssignments[nodeID])
}