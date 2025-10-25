package storage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ReplicationManager handles data replication across nodes
type ReplicationManager struct {
	config     StorageConfig
	logger     *zap.Logger
	chunkStore *ChunkStore

	replicas      sync.Map // chunkID -> []*Replica
	replicaHealth sync.Map // replicaKey -> ReplicaStatus
	nodePeers     sync.Map // nodeID -> peer connection info

	mu sync.RWMutex
}

// NewReplicationManager creates a new replication manager
func NewReplicationManager(config StorageConfig, chunkStore *ChunkStore, logger *zap.Logger) *ReplicationManager {
	return &ReplicationManager{
		config:     config,
		logger:     logger,
		chunkStore: chunkStore,
	}
}

// ReplicateChunk replicates a chunk to R nodes
func (rm *ReplicationManager) ReplicateChunk(ctx context.Context, chunkID string, targetNodes []string) error {
	// Get chunk data
	data, err := rm.chunkStore.ReadChunk(chunkID)
	if err != nil {
		return fmt.Errorf("failed to read chunk for replication: %w", err)
	}

	// Determine replication factor
	replicationFactor := int(rm.config.ReplicationFactor)
	if len(targetNodes) < replicationFactor {
		return fmt.Errorf("insufficient target nodes: need %d, got %d",
			replicationFactor, len(targetNodes))
	}

	// Limit to replication factor
	targetNodes = targetNodes[:replicationFactor]

	// Replicate to each node
	var wg sync.WaitGroup
	errors := make(chan error, len(targetNodes))
	successCount := 0
	var successMu sync.Mutex

	for _, nodeID := range targetNodes {
		wg.Add(1)
		go func(nid string) {
			defer wg.Done()

			if err := rm.replicateToNode(ctx, chunkID, data, nid); err != nil {
				rm.logger.Error("Failed to replicate chunk to node",
					zap.String("chunk_id", chunkID),
					zap.String("node_id", nid),
					zap.Error(err),
				)
				errors <- err
			} else {
				successMu.Lock()
				successCount++
				successMu.Unlock()
			}
		}(nodeID)
	}

	wg.Wait()
	close(errors)

	// Check if we have quorum
	quorum := rm.getWriteQuorum()
	if successCount < quorum {
		return fmt.Errorf("replication failed: only %d/%d nodes succeeded (quorum: %d)",
			successCount, len(targetNodes), quorum)
	}

	rm.logger.Debug("Chunk replicated successfully",
		zap.String("chunk_id", chunkID),
		zap.Int("replicas", successCount),
		zap.Int("targets", len(targetNodes)),
	)

	return nil
}

// replicateToNode sends chunk data to a specific node
func (rm *ReplicationManager) replicateToNode(ctx context.Context, chunkID string, data []byte, nodeID string) error {
	// In a full implementation, would use the overlay network to transfer data
	// For now, just record the replica

	replica := &Replica{
		ChunkID:      chunkID,
		NodeID:       nodeID,
		Status:       ReplicaStatusHealthy,
		LastVerified: time.Now(),
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
		Size:         int64(len(data)),
		Checksum:     chunkID, // ChunkID is the checksum
	}

	// Store replica info
	replicaKey := rm.getReplicaKey(chunkID, nodeID)
	rm.replicaHealth.Store(replicaKey, ReplicaStatusHealthy)

	// Add to replicas list
	var replicas []*Replica
	if r, ok := rm.replicas.Load(chunkID); ok {
		replicas = r.([]*Replica)
	}
	replicas = append(replicas, replica)
	rm.replicas.Store(chunkID, replicas)

	rm.logger.Debug("Replicated chunk to node",
		zap.String("chunk_id", chunkID),
		zap.String("node_id", nodeID),
	)

	return nil
}

// GetReplicas returns all replicas for a chunk
func (rm *ReplicationManager) GetReplicas(chunkID string) ([]*Replica, error) {
	if r, ok := rm.replicas.Load(chunkID); ok {
		replicas := r.([]*Replica)
		return replicas, nil
	}

	return []*Replica{}, nil
}

// GetHealthyReplicas returns only healthy replicas for a chunk
func (rm *ReplicationManager) GetHealthyReplicas(chunkID string) ([]*Replica, error) {
	replicas, err := rm.GetReplicas(chunkID)
	if err != nil {
		return nil, err
	}

	var healthy []*Replica
	for _, replica := range replicas {
		if replica.Status == ReplicaStatusHealthy {
			healthy = append(healthy, replica)
		}
	}

	return healthy, nil
}

// SelectPrimaryReplica selects the best replica to read from
func (rm *ReplicationManager) SelectPrimaryReplica(chunkID string) (*Replica, error) {
	replicas, err := rm.GetHealthyReplicas(chunkID)
	if err != nil {
		return nil, err
	}

	if len(replicas) == 0 {
		return nil, fmt.Errorf("no healthy replicas available for chunk: %s", chunkID)
	}

	// For now, select the first healthy replica
	// In production, would consider:
	// - Node locality
	// - Network latency
	// - Node load
	return replicas[0], nil
}

// UpdateReplicaHealth updates the health status of a replica
func (rm *ReplicationManager) UpdateReplicaHealth(chunkID, nodeID string, status ReplicaStatus) error {
	replicaKey := rm.getReplicaKey(chunkID, nodeID)

	rm.mu.Lock()
	defer rm.mu.Unlock()

	// Update health status
	rm.replicaHealth.Store(replicaKey, status)

	// Update replica in list
	if r, ok := rm.replicas.Load(chunkID); ok {
		replicas := r.([]*Replica)
		for i, replica := range replicas {
			if replica.NodeID == nodeID {
				replicas[i].Status = status
				replicas[i].UpdatedAt = time.Now()
				rm.replicas.Store(chunkID, replicas)
				break
			}
		}
	}

	rm.logger.Debug("Updated replica health",
		zap.String("chunk_id", chunkID),
		zap.String("node_id", nodeID),
		zap.String("status", string(status)),
	)

	return nil
}

// VerifyReplica verifies the integrity of a replica
func (rm *ReplicationManager) VerifyReplica(chunkID, nodeID string) error {
	// In a full implementation, would:
	// 1. Fetch chunk from node
	// 2. Verify checksum
	// 3. Update replica health

	// For now, just update last verified time
	replicaKey := rm.getReplicaKey(chunkID, nodeID)

	if r, ok := rm.replicas.Load(chunkID); ok {
		replicas := r.([]*Replica)
		for i, replica := range replicas {
			if replica.NodeID == nodeID {
				replicas[i].LastVerified = time.Now()
				rm.replicas.Store(chunkID, replicas)
				break
			}
		}
	}

	rm.replicaHealth.Store(replicaKey, ReplicaStatusHealthy)

	return nil
}

// CheckReplicationHealth checks if chunk has sufficient healthy replicas
func (rm *ReplicationManager) CheckReplicationHealth(chunkID string) (bool, int, error) {
	healthy, err := rm.GetHealthyReplicas(chunkID)
	if err != nil {
		return false, 0, err
	}

	required := int(rm.config.ReplicationFactor)
	hasEnough := len(healthy) >= required

	return hasEnough, len(healthy), nil
}

// RepairReplica creates a new replica to replace a failed one
func (rm *ReplicationManager) RepairReplica(ctx context.Context, chunkID, targetNodeID string) error {
	// Find a healthy replica to copy from
	primary, err := rm.SelectPrimaryReplica(chunkID)
	if err != nil {
		return fmt.Errorf("no healthy replica to copy from: %w", err)
	}

	// Get chunk data from primary
	data, err := rm.chunkStore.ReadChunk(chunkID)
	if err != nil {
		return fmt.Errorf("failed to read chunk from primary: %w", err)
	}

	// Replicate to target node
	if err := rm.replicateToNode(ctx, chunkID, data, targetNodeID); err != nil {
		return fmt.Errorf("failed to repair replica: %w", err)
	}

	rm.logger.Info("Repaired replica",
		zap.String("chunk_id", chunkID),
		zap.String("source_node", primary.NodeID),
		zap.String("target_node", targetNodeID),
	)

	return nil
}

// RemoveReplica removes a replica from tracking
func (rm *ReplicationManager) RemoveReplica(chunkID, nodeID string) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	replicaKey := rm.getReplicaKey(chunkID, nodeID)
	rm.replicaHealth.Delete(replicaKey)

	// Remove from replicas list
	if r, ok := rm.replicas.Load(chunkID); ok {
		replicas := r.([]*Replica)
		newReplicas := make([]*Replica, 0, len(replicas))

		for _, replica := range replicas {
			if replica.NodeID != nodeID {
				newReplicas = append(newReplicas, replica)
			}
		}

		if len(newReplicas) > 0 {
			rm.replicas.Store(chunkID, newReplicas)
		} else {
			rm.replicas.Delete(chunkID)
		}
	}

	rm.logger.Info("Removed replica",
		zap.String("chunk_id", chunkID),
		zap.String("node_id", nodeID),
	)

	return nil
}

// GetReplicationStats returns replication statistics
func (rm *ReplicationManager) GetReplicationStats() ReplicationStats {
	stats := ReplicationStats{
		LastUpdated: time.Now(),
	}

	// Count replicas by status
	rm.replicas.Range(func(key, value interface{}) bool {
		replicas := value.([]*Replica)
		stats.TotalChunks++

		for _, replica := range replicas {
			stats.TotalReplicas++

			switch replica.Status {
			case ReplicaStatusHealthy:
				stats.HealthyReplicas++
			case ReplicaStatusStale:
				stats.StaleReplicas++
			case ReplicaStatusCorrupted:
				stats.CorruptedReplicas++
			case ReplicaStatusMissing:
				stats.MissingReplicas++
			}
		}

		return true
	})

	// Calculate under-replicated chunks
	rm.replicas.Range(func(key, value interface{}) bool {
		replicas := value.([]*Replica)
		healthyCount := 0

		for _, replica := range replicas {
			if replica.Status == ReplicaStatusHealthy {
				healthyCount++
			}
		}

		if healthyCount < int(rm.config.ReplicationFactor) {
			stats.UnderReplicatedChunks++
		}

		return true
	})

	return stats
}

// ReadRepair performs read-time repair of stale replicas
func (rm *ReplicationManager) ReadRepair(ctx context.Context, chunkID string) error {
	if !rm.config.EnableReadRepair {
		return nil
	}

	replicas, err := rm.GetReplicas(chunkID)
	if err != nil {
		return err
	}

	// Find stale replicas
	var staleReplicas []*Replica
	for _, replica := range replicas {
		if replica.Status == ReplicaStatusStale {
			staleReplicas = append(staleReplicas, replica)
		}
	}

	if len(staleReplicas) == 0 {
		return nil
	}

	// Get fresh data
	data, err := rm.chunkStore.ReadChunk(chunkID)
	if err != nil {
		return fmt.Errorf("failed to read chunk for repair: %w", err)
	}

	// Repair stale replicas
	for _, replica := range staleReplicas {
		if err := rm.replicateToNode(ctx, chunkID, data, replica.NodeID); err != nil {
			rm.logger.Error("Failed to repair stale replica",
				zap.String("chunk_id", chunkID),
				zap.String("node_id", replica.NodeID),
				zap.Error(err),
			)
		}
	}

	rm.logger.Debug("Performed read repair",
		zap.String("chunk_id", chunkID),
		zap.Int("repaired_count", len(staleReplicas)),
	)

	return nil
}

// getWriteQuorum returns the number of acks needed for writes
func (rm *ReplicationManager) getWriteQuorum() int {
	if !rm.config.QuorumWrites {
		return 1
	}

	// W = R/2 + 1 (majority)
	r := int(rm.config.ReplicationFactor)
	return (r / 2) + 1
}

// getReplicaKey creates a composite key for replica tracking
func (rm *ReplicationManager) getReplicaKey(chunkID, nodeID string) string {
	return fmt.Sprintf("%s:%s", chunkID, nodeID)
}

// ReplicationStats contains replication statistics
type ReplicationStats struct {
	TotalChunks            int64
	TotalReplicas          int64
	HealthyReplicas        int64
	StaleReplicas          int64
	CorruptedReplicas      int64
	MissingReplicas        int64
	UnderReplicatedChunks  int64
	OverReplicatedChunks   int64
	LastUpdated            time.Time
}
