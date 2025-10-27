package storage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// StorageManager coordinates all storage operations at the cluster level
type StorageManager struct {
	config            StorageConfig
	logger            *zap.Logger
	objectStore       *ObjectStore
	placementStrategy *PlacementStrategy

	// Node tracking
	nodes     sync.Map // nodeID -> *NodeInfo
	nodeStats sync.Map // nodeID -> *NodeStorageStats

	// Rebalancing
	rebalancer        *Rebalancer
	rebalancingActive bool

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.RWMutex
}

// NewStorageManager creates a new storage manager
// Note: StorageManager runs on agents and doesn't have RAFT
// For coordinator with RAFT, see coordinator package
func NewStorageManager(config StorageConfig, logger *zap.Logger) (*StorageManager, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// Create object store without RAFT (agent mode)
	// Agents use local-only metadata storage
	objectStore, err := NewObjectStore(config, nil, logger)
	if err != nil {
		cancel()
		return nil, fmt.Errorf("failed to create object store: %w", err)
	}

	// Create placement strategy
	placementStrategy := NewPlacementStrategy(config, logger)

	// Create rebalancer
	rebalancer := NewRebalancer(placementStrategy, logger)

	sm := &StorageManager{
		config:            config,
		logger:            logger,
		objectStore:       objectStore,
		placementStrategy: placementStrategy,
		rebalancer:        rebalancer,
		ctx:               ctx,
		cancel:            cancel,
	}

	return sm, nil
}

// Start starts the storage manager
func (sm *StorageManager) Start() error {
	sm.logger.Info("Starting storage manager")

	// Start object store
	if err := sm.objectStore.Start(); err != nil {
		return fmt.Errorf("failed to start object store: %w", err)
	}

	// Start monitoring loop
	sm.wg.Add(1)
	go sm.monitoringLoop()

	// Start rebalancing loop
	sm.wg.Add(1)
	go sm.rebalancingLoop()

	sm.logger.Info("Storage manager started successfully")
	return nil
}

// Stop gracefully stops the storage manager
func (sm *StorageManager) Stop() error {
	sm.logger.Info("Stopping storage manager")

	sm.cancel()
	sm.wg.Wait()

	// Stop object store
	if err := sm.objectStore.Stop(); err != nil {
		sm.logger.Error("Failed to stop object store", zap.Error(err))
	}

	sm.logger.Info("Storage manager stopped")
	return nil
}

// Node Management

// RegisterNode registers a storage node
func (sm *StorageManager) RegisterNode(node *NodeInfo) error {
	sm.logger.Info("Registering storage node",
		zap.String("node_id", node.ID),
		zap.String("address", node.Address),
	)

	sm.mu.Lock()
	defer sm.mu.Unlock()

	// Store node info
	sm.nodes.Store(node.ID, node)

	// Register with placement strategy
	storageNode := &StorageNode{
		ID:        node.ID,
		Zone:      node.Zone,
		Region:    node.Region,
		Capacity:  node.StorageCapacity,
		Available: node.StorageCapacity,
		IOPSClass: node.IOPSClass,
		State:     NodeStateHealthy,
		Labels:    node.Labels,
	}

	if err := sm.placementStrategy.RegisterNode(storageNode); err != nil {
		return fmt.Errorf("failed to register node with placement strategy: %w", err)
	}

	sm.logger.Info("Storage node registered",
		zap.String("node_id", node.ID),
	)

	return nil
}

// UnregisterNode removes a storage node
func (sm *StorageManager) UnregisterNode(nodeID string) error {
	sm.logger.Info("Unregistering storage node",
		zap.String("node_id", nodeID),
	)

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.nodes.Delete(nodeID)
	sm.nodeStats.Delete(nodeID)

	if err := sm.placementStrategy.UnregisterNode(nodeID); err != nil {
		return fmt.Errorf("failed to unregister node: %w", err)
	}

	sm.logger.Info("Storage node unregistered",
		zap.String("node_id", nodeID),
	)

	return nil
}

// UpdateNodeStats updates storage statistics for a node
func (sm *StorageManager) UpdateNodeStats(nodeID string, stats *NodeStorageStats) error {
	sm.nodeStats.Store(nodeID, stats)

	// Update placement strategy
	if err := sm.placementStrategy.UpdateNodeCapacity(
		nodeID,
		stats.UsedBytes,
		stats.TotalBytes,
	); err != nil {
		return err
	}

	sm.logger.Debug("Updated node storage stats",
		zap.String("node_id", nodeID),
		zap.Int64("used", stats.UsedBytes),
		zap.Int64("total", stats.TotalBytes),
	)

	return nil
}

// GetNode retrieves node information
func (sm *StorageManager) GetNode(nodeID string) (*NodeInfo, error) {
	if n, ok := sm.nodes.Load(nodeID); ok {
		node := n.(*NodeInfo)
		return node, nil
	}

	return nil, fmt.Errorf("node not found: %s", nodeID)
}

// ListNodes lists all registered nodes
func (sm *StorageManager) ListNodes() []*NodeInfo {
	var nodes []*NodeInfo

	sm.nodes.Range(func(key, value interface{}) bool {
		node := value.(*NodeInfo)
		nodes = append(nodes, node)
		return true
	})

	return nodes
}

// Object Store Operations

// PutObject stores an object
func (sm *StorageManager) PutObject(ctx context.Context, req *PutObjectRequest) error {
	return sm.objectStore.PutObject(ctx, req)
}

// GetObject retrieves an object
func (sm *StorageManager) GetObject(ctx context.Context, bucket, key string) (*GetObjectResponse, error) {
	return sm.objectStore.GetObject(ctx, bucket, key)
}

// DeleteObject deletes an object
func (sm *StorageManager) DeleteObject(ctx context.Context, bucket, key string) error {
	return sm.objectStore.DeleteObject(ctx, bucket, key)
}

// ListObjects lists objects in a bucket
func (sm *StorageManager) ListObjects(ctx context.Context, req *ListObjectsRequest) ([]*ObjectMetadata, error) {
	return sm.objectStore.ListObjects(ctx, req)
}

// CreateBucket creates a new bucket
func (sm *StorageManager) CreateBucket(ctx context.Context, req *CreateBucketRequest) error {
	return sm.objectStore.CreateBucket(ctx, req)
}

// DeleteBucket deletes a bucket
func (sm *StorageManager) DeleteBucket(ctx context.Context, name string) error {
	return sm.objectStore.DeleteBucket(ctx, name)
}

// ListBuckets lists all buckets
func (sm *StorageManager) ListBuckets(ctx context.Context) ([]*BucketMetadata, error) {
	return sm.objectStore.ListBuckets(ctx)
}

// Volume Operations (coordinated across cluster)

// AllocateVolume allocates a volume on a specific node
func (sm *StorageManager) AllocateVolume(ctx context.Context, req *AllocateVolumeRequest) (*VolumeAllocation, error) {
	sm.logger.Info("Allocating volume",
		zap.String("volume_id", req.VolumeID),
		zap.String("workload_id", req.WorkloadID),
		zap.Int64("size", req.SizeBytes),
	)

	// Select node for volume
	placementReq := &PlacementRequest{
		StorageClass:  StorageClassEphemeral,
		IOPSClass:     req.IOPSClass,
		ReplicaCount:  1, // Ephemeral volumes are not replicated
		Size:          req.SizeBytes,
		PreferredZone: req.PreferredZone,
		ExcludeNodes:  req.ExcludeNodes,
	}

	nodes, err := sm.placementStrategy.SelectNodes(placementReq)
	if err != nil {
		return nil, fmt.Errorf("failed to select node for volume: %w", err)
	}

	nodeID := nodes[0]

	allocation := &VolumeAllocation{
		VolumeID:    req.VolumeID,
		NodeID:      nodeID,
		SizeBytes:   req.SizeBytes,
		IOPSClass:   req.IOPSClass,
		WorkloadID:  req.WorkloadID,
		AllocatedAt: time.Now(),
	}

	sm.logger.Info("Volume allocated",
		zap.String("volume_id", req.VolumeID),
		zap.String("node_id", nodeID),
	)

	return allocation, nil
}

// Cluster Statistics

// GetClusterStats returns cluster-wide storage statistics
func (sm *StorageManager) GetClusterStats() *ClusterStorageStats {
	stats := &ClusterStorageStats{
		LastUpdated: time.Now(),
	}

	// Get object store stats
	objectStats := sm.objectStore.GetStats()
	stats.TotalBuckets = objectStats.BucketCount
	stats.TotalObjects = objectStats.TotalObjects
	stats.TotalObjectBytes = objectStats.TotalBytes

	// Get cluster capacity
	capacity := sm.placementStrategy.GetClusterCapacity()
	stats.TotalNodes = capacity.TotalNodes
	stats.HealthyNodes = capacity.HealthyNodes
	stats.TotalCapacity = capacity.TotalCapacity
	stats.UsedCapacity = capacity.TotalUsed
	stats.AvailableCapacity = capacity.TotalAvailable
	stats.UsagePercent = capacity.UsagePercent

	// Get replication stats
	replicationStats := sm.objectStore.GetReplicationStats()
	stats.TotalChunks = replicationStats.TotalChunks
	stats.HealthyReplicas = replicationStats.HealthyReplicas
	stats.StaleReplicas = replicationStats.StaleReplicas
	stats.CorruptedReplicas = replicationStats.CorruptedReplicas
	stats.UnderReplicatedChunks = replicationStats.UnderReplicatedChunks

	// Get repair metrics
	repairMetrics := sm.objectStore.GetRepairMetrics()
	stats.RepairsCompleted = repairMetrics.TasksCompleted
	stats.RepairsFailed = repairMetrics.TasksFailed

	return stats
}

// Rebalancing

// TriggerRebalancing manually triggers rebalancing
func (sm *StorageManager) TriggerRebalancing() error {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.rebalancingActive {
		return fmt.Errorf("rebalancing already in progress")
	}

	sm.logger.Info("Triggering manual rebalancing")

	go func() {
		if err := sm.executeRebalancing(); err != nil {
			sm.logger.Error("Rebalancing failed", zap.Error(err))
		}
	}()

	return nil
}

// GetRebalancingStatus returns the current rebalancing status
func (sm *StorageManager) GetRebalancingStatus() *RebalancingStatus {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	return &RebalancingStatus{
		Active:              sm.rebalancingActive,
		LastRun:             sm.rebalancer.lastRun,
		OperationsCompleted: sm.rebalancer.completedOps,
		OperationsFailed:    sm.rebalancer.failedOps,
	}
}

// Background Loops

// monitoringLoop monitors node health and statistics
func (sm *StorageManager) monitoringLoop() {
	defer sm.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sm.ctx.Done():
			return
		case <-ticker.C:
			sm.performHealthChecks()
		}
	}
}

// rebalancingLoop periodically checks for rebalancing needs
func (sm *StorageManager) rebalancingLoop() {
	defer sm.wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-sm.ctx.Done():
			return
		case <-ticker.C:
			if !sm.rebalancingActive {
				if err := sm.executeRebalancing(); err != nil {
					sm.logger.Error("Automatic rebalancing failed", zap.Error(err))
				}
			}
		}
	}
}

// performHealthChecks checks health of all nodes
func (sm *StorageManager) performHealthChecks() {
	sm.nodes.Range(func(key, value interface{}) bool {
		node := value.(*NodeInfo)

		// Check if node is stale (no updates in 2 minutes)
		if stats, ok := sm.nodeStats.Load(node.ID); ok {
			nodeStats := stats.(*NodeStorageStats)
			if time.Since(nodeStats.LastUpdated) > 2*time.Minute {
				sm.logger.Warn("Node appears stale, marking as degraded",
					zap.String("node_id", node.ID),
					zap.Duration("since_last_update", time.Since(nodeStats.LastUpdated)),
				)

				// Get storage node from placement strategy and mark as degraded
				if storageNodeVal, ok := sm.placementStrategy.nodes.Load(node.ID); ok {
					storageNode := storageNodeVal.(*StorageNode)

					// Only update if not already degraded
					if storageNode.State != NodeStateDegraded {
						storageNode.State = NodeStateDegraded
						storageNode.LastUpdated = time.Now()
						sm.placementStrategy.nodes.Store(node.ID, storageNode)

						sm.logger.Info("Node state changed to degraded",
							zap.String("node_id", node.ID),
							zap.String("previous_state", string(NodeStateHealthy)),
							zap.String("new_state", string(NodeStateDegraded)),
						)
					}
				}
			} else {
				// Node is healthy - ensure it's marked as such
				if storageNodeVal, ok := sm.placementStrategy.nodes.Load(node.ID); ok {
					storageNode := storageNodeVal.(*StorageNode)

					// Restore to healthy if it was degraded
					if storageNode.State == NodeStateDegraded {
						storageNode.State = NodeStateHealthy
						storageNode.LastUpdated = time.Now()
						sm.placementStrategy.nodes.Store(node.ID, storageNode)

						sm.logger.Info("Node state restored to healthy",
							zap.String("node_id", node.ID),
						)
					}
				}
			}
		}

		return true
	})
}

// executeRebalancing executes rebalancing operations
func (sm *StorageManager) executeRebalancing() error {
	sm.mu.Lock()
	if sm.rebalancingActive {
		sm.mu.Unlock()
		return nil
	}
	sm.rebalancingActive = true
	sm.mu.Unlock()

	defer func() {
		sm.mu.Lock()
		sm.rebalancingActive = false
		sm.mu.Unlock()
	}()

	sm.logger.Info("Starting rebalancing")

	// Get rebalancing recommendations
	operations := sm.placementStrategy.RebalanceRecommendations()

	if len(operations) == 0 {
		sm.logger.Info("No rebalancing needed")
		return nil
	}

	// Execute rebalancing
	if err := sm.rebalancer.Execute(operations); err != nil {
		return fmt.Errorf("rebalancing failed: %w", err)
	}

	sm.logger.Info("Rebalancing completed",
		zap.Int("operations", len(operations)),
	)

	return nil
}

// Types

// NodeInfo contains information about a storage node
type NodeInfo struct {
	ID              string
	Address         string
	Zone            string
	Region          string
	StorageCapacity int64
	IOPSClass       IOPSClass
	Labels          map[string]string
	RegisteredAt    time.Time
}

// NodeStorageStats contains storage statistics for a node
type NodeStorageStats struct {
	TotalBytes     int64
	UsedBytes      int64
	AvailableBytes int64
	ChunkCount     int64
	VolumeCount    int64
	LastUpdated    time.Time
}

// AllocateVolumeRequest contains parameters for volume allocation
type AllocateVolumeRequest struct {
	VolumeID      string
	WorkloadID    string
	SizeBytes     int64
	IOPSClass     IOPSClass
	PreferredZone string
	ExcludeNodes  []string
}

// VolumeAllocation contains volume allocation information
type VolumeAllocation struct {
	VolumeID    string
	NodeID      string
	SizeBytes   int64
	IOPSClass   IOPSClass
	WorkloadID  string
	AllocatedAt time.Time
}

// ClusterStorageStats contains cluster-wide storage statistics
type ClusterStorageStats struct {
	// Cluster info
	TotalNodes        int
	HealthyNodes      int
	TotalCapacity     int64
	UsedCapacity      int64
	AvailableCapacity int64
	UsagePercent      float64

	// Object storage
	TotalBuckets     int64
	TotalObjects     int64
	TotalObjectBytes int64
	TotalChunks      int64

	// Replication
	HealthyReplicas       int64
	StaleReplicas         int64
	CorruptedReplicas     int64
	UnderReplicatedChunks int64

	// Repairs
	RepairsCompleted int64
	RepairsFailed    int64

	LastUpdated time.Time
}

// RebalancingStatus contains rebalancing status
type RebalancingStatus struct {
	Active              bool
	LastRun             time.Time
	OperationsCompleted int
	OperationsFailed    int
}

// Rebalancer handles rebalancing operations
type Rebalancer struct {
	placementStrategy *PlacementStrategy
	logger            *zap.Logger
	lastRun           time.Time
	completedOps      int
	failedOps         int
	mu                sync.Mutex
}

// NewRebalancer creates a new rebalancer
func NewRebalancer(placementStrategy *PlacementStrategy, logger *zap.Logger) *Rebalancer {
	return &Rebalancer{
		placementStrategy: placementStrategy,
		logger:            logger,
	}
}

// Execute executes rebalancing operations
func (r *Rebalancer) Execute(operations []RebalanceOperation) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.lastRun = time.Now()

	// Sort operations by priority
	// Priority 1 = highest priority
	// Execute operations in priority order

	for _, op := range operations {
		if err := r.executeOperation(op); err != nil {
			r.logger.Error("Rebalancing operation failed",
				zap.String("type", string(op.Type)),
				zap.String("source", op.SourceNodeID),
				zap.String("target", op.TargetNodeID),
				zap.Error(err),
			)
			r.failedOps++
		} else {
			r.completedOps++
		}
	}

	return nil
}

// executeOperation executes a single rebalancing operation
func (r *Rebalancer) executeOperation(op RebalanceOperation) error {
	// In production, would:
	// 1. Lock the chunk
	// 2. Copy/move data
	// 3. Verify integrity
	// 4. Update metadata
	// 5. Unlock

	r.logger.Info("Executing rebalancing operation",
		zap.String("type", string(op.Type)),
		zap.String("source", op.SourceNodeID),
		zap.String("target", op.TargetNodeID),
		zap.String("reason", op.Reason),
	)

	// Placeholder implementation
	time.Sleep(100 * time.Millisecond)

	return nil
}
