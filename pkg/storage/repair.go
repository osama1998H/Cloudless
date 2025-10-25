package storage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
)

// RepairEngine manages anti-entropy repair across the cluster
type RepairEngine struct {
	config             StorageConfig
	logger             *zap.Logger
	replicationManager *ReplicationManager
	chunkStore         *ChunkStore

	repairQueue    chan *RepairTask
	activeRepairs  sync.Map // taskID -> *RepairTask
	repairHistory  []RepairTask
	merkleCache    sync.Map // nodeID -> *MerkleTree
	repairMetrics  RepairMetrics

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.RWMutex
}

// NewRepairEngine creates a new repair engine
func NewRepairEngine(config StorageConfig, replicationManager *ReplicationManager, chunkStore *ChunkStore, logger *zap.Logger) *RepairEngine {
	ctx, cancel := context.WithCancel(context.Background())

	return &RepairEngine{
		config:             config,
		logger:             logger,
		replicationManager: replicationManager,
		chunkStore:         chunkStore,
		repairQueue:        make(chan *RepairTask, 1000),
		repairHistory:      make([]RepairTask, 0, 100),
		ctx:                ctx,
		cancel:             cancel,
	}
}

// Start starts the repair engine
func (re *RepairEngine) Start() error {
	re.logger.Info("Starting repair engine",
		zap.Duration("interval", re.config.RepairInterval),
	)

	// Start repair worker pool
	workerCount := 5
	for i := 0; i < workerCount; i++ {
		re.wg.Add(1)
		go re.repairWorker(i)
	}

	// Start periodic anti-entropy loop
	re.wg.Add(1)
	go re.antiEntropyLoop()

	return nil
}

// Stop stops the repair engine
func (re *RepairEngine) Stop() error {
	re.logger.Info("Stopping repair engine")

	re.cancel()
	close(re.repairQueue)
	re.wg.Wait()

	re.logger.Info("Repair engine stopped")
	return nil
}

// antiEntropyLoop runs periodic anti-entropy checks
func (re *RepairEngine) antiEntropyLoop() {
	defer re.wg.Done()

	ticker := time.NewTicker(re.config.RepairInterval)
	defer ticker.Stop()

	for {
		select {
		case <-re.ctx.Done():
			return
		case <-ticker.C:
			if err := re.performAntiEntropy(); err != nil {
				re.logger.Error("Anti-entropy check failed", zap.Error(err))
			}
		}
	}
}

// performAntiEntropy performs an anti-entropy check across the cluster
func (re *RepairEngine) performAntiEntropy() error {
	re.logger.Info("Starting anti-entropy check")

	startTime := time.Now()

	// Get all chunks
	chunks := re.chunkStore.ListChunks()

	// Check each chunk's replication status
	tasksCreated := 0
	for _, chunk := range chunks {
		if err := re.checkChunkHealth(chunk.ID); err != nil {
			re.logger.Error("Failed to check chunk health",
				zap.String("chunk_id", chunk.ID),
				zap.Error(err),
			)
		} else {
			tasksCreated++
		}
	}

	duration := time.Since(startTime)
	re.logger.Info("Anti-entropy check completed",
		zap.Int("chunks_checked", len(chunks)),
		zap.Int("tasks_created", tasksCreated),
		zap.Duration("duration", duration),
	)

	// Update metrics
	re.mu.Lock()
	re.repairMetrics.LastCheckTime = time.Now()
	re.repairMetrics.ChunksChecked += int64(len(chunks))
	re.mu.Unlock()

	return nil
}

// checkChunkHealth checks if a chunk needs repair
func (re *RepairEngine) checkChunkHealth(chunkID string) error {
	// Get replication status
	hasEnough, healthyCount, err := re.replicationManager.CheckReplicationHealth(chunkID)
	if err != nil {
		return err
	}

	if !hasEnough {
		// Under-replicated - create repair task
		task := &RepairTask{
			ChunkID:   chunkID,
			Type:      RepairTypeMissing,
			Priority:  1, // High priority
			CreatedAt: time.Now(),
			Status:    RepairStatusPending,
		}

		select {
		case re.repairQueue <- task:
			re.logger.Debug("Queued repair task",
				zap.String("chunk_id", chunkID),
				zap.String("type", string(RepairTypeMissing)),
				zap.Int("healthy_replicas", healthyCount),
			)
		default:
			re.logger.Warn("Repair queue full, dropping task",
				zap.String("chunk_id", chunkID),
			)
		}
	}

	// Check for corrupted or stale replicas
	replicas, err := re.replicationManager.GetReplicas(chunkID)
	if err != nil {
		return err
	}

	for _, replica := range replicas {
		if replica.Status == ReplicaStatusCorrupted {
			task := &RepairTask{
				ChunkID:      chunkID,
				Type:         RepairTypeCorrupted,
				Priority:     2, // Medium priority
				TargetNodeID: replica.NodeID,
				CreatedAt:    time.Now(),
				Status:       RepairStatusPending,
			}

			select {
			case re.repairQueue <- task:
			default:
				re.logger.Warn("Repair queue full")
			}
		} else if replica.Status == ReplicaStatusStale {
			task := &RepairTask{
				ChunkID:      chunkID,
				Type:         RepairTypeStale,
				Priority:     3, // Low priority
				TargetNodeID: replica.NodeID,
				CreatedAt:    time.Now(),
				Status:       RepairStatusPending,
			}

			select {
			case re.repairQueue <- task:
			default:
				re.logger.Warn("Repair queue full")
			}
		}
	}

	return nil
}

// repairWorker processes repair tasks from the queue
func (re *RepairEngine) repairWorker(id int) {
	defer re.wg.Done()

	re.logger.Debug("Repair worker started", zap.Int("worker_id", id))

	for {
		select {
		case <-re.ctx.Done():
			return
		case task, ok := <-re.repairQueue:
			if !ok {
				return
			}

			if err := re.executeRepairTask(task); err != nil {
				re.logger.Error("Repair task failed",
					zap.Int("worker_id", id),
					zap.String("chunk_id", task.ChunkID),
					zap.String("type", string(task.Type)),
					zap.Error(err),
				)

				task.Status = RepairStatusFailed
				task.ErrorMessage = err.Error()
			} else {
				task.Status = RepairStatusCompleted
			}

			task.CompletedAt = time.Now()

			// Record in history
			re.recordRepairTask(task)
		}
	}
}

// executeRepairTask executes a specific repair task
func (re *RepairEngine) executeRepairTask(task *RepairTask) error {
	task.Status = RepairStatusInProgress
	task.StartedAt = time.Now()

	re.activeRepairs.Store(task.ChunkID, task)
	defer re.activeRepairs.Delete(task.ChunkID)

	ctx, cancel := context.WithTimeout(re.ctx, 5*time.Minute)
	defer cancel()

	switch task.Type {
	case RepairTypeMissing:
		return re.repairMissingReplica(ctx, task)
	case RepairTypeCorrupted:
		return re.repairCorruptedReplica(ctx, task)
	case RepairTypeStale:
		return re.repairStaleReplica(ctx, task)
	default:
		return fmt.Errorf("unknown repair type: %s", task.Type)
	}
}

// repairMissingReplica creates a missing replica
func (re *RepairEngine) repairMissingReplica(ctx context.Context, task *RepairTask) error {
	// Find a healthy node to create a new replica on
	// In a full implementation, would use placement strategy
	targetNode := task.TargetNodeID
	if targetNode == "" {
		targetNode = "new-node" // Placeholder
	}

	// Repair the replica
	if err := re.replicationManager.RepairReplica(ctx, task.ChunkID, targetNode); err != nil {
		return fmt.Errorf("failed to repair missing replica: %w", err)
	}

	re.logger.Info("Repaired missing replica",
		zap.String("chunk_id", task.ChunkID),
		zap.String("node_id", targetNode),
	)

	// Update metrics
	re.mu.Lock()
	re.repairMetrics.ReplicasRepaired++
	re.mu.Unlock()

	return nil
}

// repairCorruptedReplica replaces a corrupted replica
func (re *RepairEngine) repairCorruptedReplica(ctx context.Context, task *RepairTask) error {
	// Remove corrupted replica
	if err := re.replicationManager.RemoveReplica(task.ChunkID, task.TargetNodeID); err != nil {
		return fmt.Errorf("failed to remove corrupted replica: %w", err)
	}

	// Create new replica
	if err := re.replicationManager.RepairReplica(ctx, task.ChunkID, task.TargetNodeID); err != nil {
		return fmt.Errorf("failed to create replacement replica: %w", err)
	}

	re.logger.Info("Repaired corrupted replica",
		zap.String("chunk_id", task.ChunkID),
		zap.String("node_id", task.TargetNodeID),
	)

	// Update metrics
	re.mu.Lock()
	re.repairMetrics.CorruptionsFixed++
	re.mu.Unlock()

	return nil
}

// repairStaleReplica updates a stale replica
func (re *RepairEngine) repairStaleReplica(ctx context.Context, task *RepairTask) error {
	// Repair the stale replica
	if err := re.replicationManager.RepairReplica(ctx, task.ChunkID, task.TargetNodeID); err != nil {
		return fmt.Errorf("failed to repair stale replica: %w", err)
	}

	// Update status to healthy
	if err := re.replicationManager.UpdateReplicaHealth(task.ChunkID, task.TargetNodeID, ReplicaStatusHealthy); err != nil {
		return fmt.Errorf("failed to update replica health: %w", err)
	}

	re.logger.Debug("Repaired stale replica",
		zap.String("chunk_id", task.ChunkID),
		zap.String("node_id", task.TargetNodeID),
	)

	// Update metrics
	re.mu.Lock()
	re.repairMetrics.StaleReplicasFixed++
	re.mu.Unlock()

	return nil
}

// recordRepairTask adds a task to the history
func (re *RepairEngine) recordRepairTask(task *RepairTask) {
	re.mu.Lock()
	defer re.mu.Unlock()

	re.repairHistory = append(re.repairHistory, *task)

	// Keep only last 100 tasks
	if len(re.repairHistory) > 100 {
		re.repairHistory = re.repairHistory[len(re.repairHistory)-100:]
	}

	// Update metrics
	if task.Status == RepairStatusCompleted {
		re.repairMetrics.TasksCompleted++
	} else if task.Status == RepairStatusFailed {
		re.repairMetrics.TasksFailed++
	}
}

// GetRepairMetrics returns repair metrics
func (re *RepairEngine) GetRepairMetrics() RepairMetrics {
	re.mu.RLock()
	defer re.mu.RUnlock()

	return re.repairMetrics
}

// GetActiveRepairs returns currently active repair tasks
func (re *RepairEngine) GetActiveRepairs() []*RepairTask {
	var tasks []*RepairTask

	re.activeRepairs.Range(func(key, value interface{}) bool {
		task := value.(*RepairTask)
		tasks = append(tasks, task)
		return true
	})

	return tasks
}

// GetRepairHistory returns recent repair tasks
func (re *RepairEngine) GetRepairHistory() []RepairTask {
	re.mu.RLock()
	defer re.mu.RUnlock()

	history := make([]RepairTask, len(re.repairHistory))
	copy(history, re.repairHistory)

	return history
}

// Merkle Tree for efficient replica comparison

// MerkleTree represents a merkle tree for chunk verification
type MerkleTree struct {
	Root   string
	Leaves []string
	Depth  int
}

// BuildMerkleTree builds a merkle tree from chunk IDs
func (re *RepairEngine) BuildMerkleTree(chunkIDs []string) *MerkleTree {
	if len(chunkIDs) == 0 {
		return &MerkleTree{Root: "", Leaves: []string{}, Depth: 0}
	}

	// Sort chunk IDs for consistency
	sorted := make([]string, len(chunkIDs))
	copy(sorted, chunkIDs)
	sort.Strings(sorted)

	// Build tree from leaves
	leaves := sorted
	root := re.buildMerkleRoot(leaves)

	tree := &MerkleTree{
		Root:   root,
		Leaves: leaves,
		Depth:  re.calculateDepth(len(leaves)),
	}

	return tree
}

// buildMerkleRoot builds the root hash from leaves
func (re *RepairEngine) buildMerkleRoot(leaves []string) string {
	if len(leaves) == 0 {
		return ""
	}

	if len(leaves) == 1 {
		return leaves[0]
	}

	// Build next level
	nextLevel := make([]string, 0, (len(leaves)+1)/2)

	for i := 0; i < len(leaves); i += 2 {
		if i+1 < len(leaves) {
			// Hash pair
			combined := leaves[i] + leaves[i+1]
			hash := sha256.Sum256([]byte(combined))
			nextLevel = append(nextLevel, hex.EncodeToString(hash[:]))
		} else {
			// Odd leaf out, carry it up
			nextLevel = append(nextLevel, leaves[i])
		}
	}

	return re.buildMerkleRoot(nextLevel)
}

// calculateDepth calculates tree depth
func (re *RepairEngine) calculateDepth(leafCount int) int {
	if leafCount <= 1 {
		return 0
	}

	depth := 0
	for leafCount > 1 {
		leafCount = (leafCount + 1) / 2
		depth++
	}

	return depth
}

// CompareMerkleTrees compares two merkle trees and returns differing chunks
func (re *RepairEngine) CompareMerkleTrees(tree1, tree2 *MerkleTree) []string {
	// Simple comparison - just compare leaves
	// In a full implementation, would do hierarchical comparison

	if tree1.Root == tree2.Root {
		return []string{} // Trees identical
	}

	// Find differences in leaves
	leaveSet1 := make(map[string]bool)
	for _, leaf := range tree1.Leaves {
		leaveSet1[leaf] = true
	}

	leaveSet2 := make(map[string]bool)
	for _, leaf := range tree2.Leaves {
		leaveSet2[leaf] = true
	}

	var differences []string

	// Find leaves in tree1 but not in tree2
	for _, leaf := range tree1.Leaves {
		if !leaveSet2[leaf] {
			differences = append(differences, leaf)
		}
	}

	// Find leaves in tree2 but not in tree1
	for _, leaf := range tree2.Leaves {
		if !leaveSet1[leaf] {
			differences = append(differences, leaf)
		}
	}

	return differences
}

// RepairMetrics contains repair statistics
type RepairMetrics struct {
	ChunksChecked      int64
	ReplicasRepaired   int64
	CorruptionsFixed   int64
	StaleReplicasFixed int64
	TasksCompleted     int64
	TasksFailed        int64
	LastCheckTime      time.Time
}
