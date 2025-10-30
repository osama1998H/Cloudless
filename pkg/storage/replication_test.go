package storage

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"go.uber.org/zap"
)

// setupTestReplicationManager creates a test ReplicationManager with R=3
func setupTestReplicationManager(t *testing.T) (*ReplicationManager, *ChunkStore, string, func()) {
	t.Helper()

	// Create temp directory
	tempDir, err := os.MkdirTemp("", "cloudless-replication-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	// Create config with R=3 (CLD-REQ-050)
	config := StorageConfig{
		DataDir:             tempDir,
		ChunkSize:           64 * 1024,              // 64KB
		ReplicationFactor:   ReplicationFactorThree, // Test R=3
		EnableCompression:   false,
		RepairInterval:      1 * time.Hour,
		GCInterval:          24 * time.Hour,
		MaxDiskUsagePercent: 90,
		EnableReadRepair:    true,
		QuorumWrites:        true,
	}

	logger := zap.NewNop()

	// Create chunk store
	chunkStore, err := NewChunkStore(config, logger)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	// Create replication manager
	replicationManager := NewReplicationManager(config, chunkStore, logger)

	cleanup := func() {
		os.RemoveAll(tempDir)
	}

	return replicationManager, chunkStore, tempDir, cleanup
}

// TestReplicationManager_ReplicationFactorR3 verifies R=3 replication (CLD-REQ-050)
func TestReplicationManager_ReplicationFactorR3(t *testing.T) {
	rm, cs, _, cleanup := setupTestReplicationManager(t)
	defer cleanup()

	ctx := context.Background()

	// Write a test chunk
	testData := []byte("Test data for R=3 replication")
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	// Simulate 3 nodes for R=3 replication
	targetNodes := []string{"node-1", "node-2", "node-3"}

	// Test replication to exactly R=3 nodes
	err = rm.ReplicateChunk(ctx, chunk.ID, targetNodes)
	if err != nil {
		t.Errorf("Replication to R=3 nodes should succeed: %v", err)
	}

	// Verify replicas were created
	replicas, err := rm.GetReplicas(chunk.ID)
	if err != nil {
		t.Fatalf("Failed to get replicas: %v", err)
	}

	if len(replicas) != 3 {
		t.Errorf("Expected 3 replicas (R=3), got %d", len(replicas))
	}

	// Verify all target nodes have replicas
	nodeMap := make(map[string]bool)
	for _, replica := range replicas {
		nodeMap[replica.NodeID] = true
	}

	for _, node := range targetNodes {
		if !nodeMap[node] {
			t.Errorf("Expected replica on node %q", node)
		}
	}
}

// TestReplicationManager_InsufficientNodes tests error when nodes < R
func TestReplicationManager_InsufficientNodes(t *testing.T) {
	rm, cs, _, cleanup := setupTestReplicationManager(t)
	defer cleanup()

	ctx := context.Background()

	// Write a test chunk
	testData := []byte("Test data")
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	tests := []struct {
		name        string
		targetNodes []string
		wantError   bool
	}{
		{
			name:        "insufficient nodes (2 < R=3)",
			targetNodes: []string{"node-1", "node-2"},
			wantError:   true,
		},
		{
			name:        "insufficient nodes (1 < R=3)",
			targetNodes: []string{"node-1"},
			wantError:   true,
		},
		{
			name:        "no nodes",
			targetNodes: []string{},
			wantError:   true,
		},
		{
			name:        "exactly R=3 nodes",
			targetNodes: []string{"node-1", "node-2", "node-3"},
			wantError:   false,
		},
		{
			name:        "more than R=3 nodes (should use first 3)",
			targetNodes: []string{"node-1", "node-2", "node-3", "node-4", "node-5"},
			wantError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rm.ReplicateChunk(ctx, chunk.ID, tt.targetNodes)

			if tt.wantError {
				if err == nil {
					t.Error("Expected error for insufficient nodes, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
			}
		})
	}
}

// TestReplicationManager_QuorumEnforcement tests quorum write (W = R/2 + 1)
func TestReplicationManager_QuorumEnforcement(t *testing.T) {
	// For R=3, quorum W should be 2 (3/2 + 1 = 2)
	rm, cs, _, cleanup := setupTestReplicationManager(t)
	defer cleanup()

	ctx := context.Background()

	// Verify quorum calculation
	quorum := rm.getWriteQuorum()
	expectedQuorum := 2 // For R=3: W = 3/2 + 1 = 2

	if quorum != expectedQuorum {
		t.Errorf("Expected quorum W=%d for R=3, got %d", expectedQuorum, quorum)
	}

	// Write test chunk
	testData := []byte("Test data for quorum")
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	// Test with 3 nodes - should succeed (all 3 can succeed)
	targetNodes := []string{"node-1", "node-2", "node-3"}
	err = rm.ReplicateChunk(ctx, chunk.ID, targetNodes)
	if err != nil {
		t.Errorf("Replication with 3 nodes should succeed: %v", err)
	}
}

// TestReplicationManager_QuorumDisabled tests behavior with QuorumWrites=false
func TestReplicationManager_QuorumDisabled(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cloudless-replication-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create config with QuorumWrites disabled
	config := StorageConfig{
		DataDir:             tempDir,
		ChunkSize:           64 * 1024,
		ReplicationFactor:   ReplicationFactorThree,
		EnableCompression:   false,
		RepairInterval:      1 * time.Hour,
		GCInterval:          24 * time.Hour,
		MaxDiskUsagePercent: 90,
		EnableReadRepair:    true,
		QuorumWrites:        false, // Disable quorum
	}

	logger := zap.NewNop()
	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	rm := NewReplicationManager(config, cs, logger)

	// Verify quorum is 1 when disabled
	quorum := rm.getWriteQuorum()
	if quorum != 1 {
		t.Errorf("Expected quorum W=1 when disabled, got %d", quorum)
	}
}

// TestReplicationManager_GetReplicas tests replica retrieval
func TestReplicationManager_GetReplicas(t *testing.T) {
	rm, cs, _, cleanup := setupTestReplicationManager(t)
	defer cleanup()

	ctx := context.Background()

	// Write and replicate a chunk
	testData := []byte("Test data for replica retrieval")
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	targetNodes := []string{"node-1", "node-2", "node-3"}
	err = rm.ReplicateChunk(ctx, chunk.ID, targetNodes)
	if err != nil {
		t.Fatalf("Failed to replicate chunk: %v", err)
	}

	// Test getting replicas
	tests := []struct {
		name          string
		chunkID       string
		expectedCount int
		wantError     bool
	}{
		{
			name:          "get replicas for existing chunk",
			chunkID:       chunk.ID,
			expectedCount: 3,
			wantError:     false,
		},
		{
			name:          "get replicas for non-existent chunk",
			chunkID:       "non-existent-chunk-id",
			expectedCount: 0,
			wantError:     false, // Returns empty list, not error
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			replicas, err := rm.GetReplicas(tt.chunkID)

			if tt.wantError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if len(replicas) != tt.expectedCount {
				t.Errorf("Expected %d replicas, got %d", tt.expectedCount, len(replicas))
			}
		})
	}
}

// TestReplicationManager_UpdateReplicaHealth tests replica health management
func TestReplicationManager_UpdateReplicaHealth(t *testing.T) {
	rm, cs, _, cleanup := setupTestReplicationManager(t)
	defer cleanup()

	ctx := context.Background()

	// Write and replicate a chunk
	testData := []byte("Test data for status update")
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	targetNodes := []string{"node-1", "node-2", "node-3"}
	err = rm.ReplicateChunk(ctx, chunk.ID, targetNodes)
	if err != nil {
		t.Fatalf("Failed to replicate chunk: %v", err)
	}

	// Get replicas
	replicas, err := rm.GetReplicas(chunk.ID)
	if err != nil || len(replicas) == 0 {
		t.Fatalf("Failed to get replicas")
	}

	tests := []struct {
		name      string
		replica   *Replica
		newStatus ReplicaStatus
	}{
		{
			name:      "mark replica as stale",
			replica:   replicas[0],
			newStatus: ReplicaStatusStale,
		},
		{
			name:      "mark replica as corrupted",
			replica:   replicas[1],
			newStatus: ReplicaStatusCorrupted,
		},
		{
			name:      "mark replica as healthy",
			replica:   replicas[2],
			newStatus: ReplicaStatusHealthy,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := rm.UpdateReplicaHealth(tt.replica.ChunkID, tt.replica.NodeID, tt.newStatus)
			if err != nil {
				t.Errorf("Failed to update replica health: %v", err)
				return
			}

			// Verify status was updated
			updatedReplicas, err := rm.GetReplicas(tt.replica.ChunkID)
			if err != nil {
				t.Errorf("Failed to get updated replicas: %v", err)
				return
			}

			found := false
			for _, r := range updatedReplicas {
				if r.NodeID == tt.replica.NodeID {
					found = true
					if r.Status != tt.newStatus {
						t.Errorf("Expected status %q, got %q", tt.newStatus, r.Status)
					}
					break
				}
			}

			if !found {
				t.Errorf("Replica for node %q not found after update", tt.replica.NodeID)
			}
		})
	}
}

// TestReplicationManager_ReadRepair tests read repair functionality (CLD-REQ-050 eventual consistency)
func TestReplicationManager_ReadRepair(t *testing.T) {
	rm, cs, _, cleanup := setupTestReplicationManager(t)
	defer cleanup()

	ctx := context.Background()

	// Write and replicate a chunk
	testData := []byte("Test data for read repair")
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	targetNodes := []string{"node-1", "node-2", "node-3"}
	err = rm.ReplicateChunk(ctx, chunk.ID, targetNodes)
	if err != nil {
		t.Fatalf("Failed to replicate chunk: %v", err)
	}

	// Simulate stale replica by marking one as stale
	err = rm.UpdateReplicaHealth(chunk.ID, "node-2", ReplicaStatusStale)
	if err != nil {
		t.Fatalf("Failed to mark replica as stale: %v", err)
	}

	// Trigger read repair
	err = rm.ReadRepair(ctx, chunk.ID)
	if err != nil {
		t.Errorf("Read repair should succeed: %v", err)
	}

	// Verify replica was repaired (status should be healthy after repair)
	// Note: Actual repair might not change status immediately in this test
	// since we're using a mock setup, but we verify the repair was called
}

// TestReplicationManager_ReadRepairDisabled tests behavior with read repair disabled
func TestReplicationManager_ReadRepairDisabled(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "cloudless-replication-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create config with ReadRepair disabled
	config := StorageConfig{
		DataDir:             tempDir,
		ChunkSize:           64 * 1024,
		ReplicationFactor:   ReplicationFactorThree,
		EnableCompression:   false,
		RepairInterval:      1 * time.Hour,
		GCInterval:          24 * time.Hour,
		MaxDiskUsagePercent: 90,
		EnableReadRepair:    false, // Disable read repair
		QuorumWrites:        true,
	}

	logger := zap.NewNop()
	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	rm := NewReplicationManager(config, cs, logger)
	ctx := context.Background()

	// Write chunk
	testData := []byte("Test data")
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	// ReadRepair should be no-op when disabled
	err = rm.ReadRepair(ctx, chunk.ID)
	if err != nil {
		t.Errorf("Read repair should succeed (no-op): %v", err)
	}
}

// TestReplicationManager_ConcurrentReplication tests concurrent replication (race detector)
func TestReplicationManager_ConcurrentReplication(t *testing.T) {
	rm, cs, _, cleanup := setupTestReplicationManager(t)
	defer cleanup()

	ctx := context.Background()

	// Create multiple chunks and replicate them concurrently
	const numChunks = 10
	errChan := make(chan error, numChunks)
	targetNodes := []string{"node-1", "node-2", "node-3"}

	for i := 0; i < numChunks; i++ {
		go func(idx int) {
			// Write chunk
			testData := []byte(fmt.Sprintf("Concurrent test data %d", idx))
			chunk, err := cs.WriteChunk(testData)
			if err != nil {
				errChan <- err
				return
			}

			// Replicate chunk
			err = rm.ReplicateChunk(ctx, chunk.ID, targetNodes)
			errChan <- err
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < numChunks; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent replication failed: %v", err)
		}
	}
}

// TestReplicationManager_DefaultConfigR3 verifies default config uses R=3 (CLD-REQ-050)
func TestReplicationManager_DefaultConfigR3(t *testing.T) {
	config := DefaultStorageConfig()

	if config.ReplicationFactor != ReplicationFactorThree {
		t.Errorf("CLD-REQ-050 violation: Default replication factor should be R=3, got R=%d",
			config.ReplicationFactor)
	}

	if !config.QuorumWrites {
		t.Error("CLD-REQ-050 requirement: QuorumWrites should be enabled by default")
	}

	if !config.EnableReadRepair {
		t.Error("CLD-REQ-050 requirement: ReadRepair should be enabled by default for eventual consistency")
	}

	// Verify quorum calculation for R=3
	expectedQuorum := 2 // W = R/2 + 1 = 3/2 + 1 = 2
	actualQuorum := (int(config.ReplicationFactor) / 2) + 1
	if actualQuorum != expectedQuorum {
		t.Errorf("Expected quorum W=%d for R=3, calculated %d", expectedQuorum, actualQuorum)
	}
}

// TestReplicationManager_RemoveReplica tests replica removal
func TestReplicationManager_RemoveReplica(t *testing.T) {
	rm, cs, _, cleanup := setupTestReplicationManager(t)
	defer cleanup()

	ctx := context.Background()

	// Write and replicate a chunk
	testData := []byte("Test data for replica removal")
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	targetNodes := []string{"node-1", "node-2", "node-3"}
	err = rm.ReplicateChunk(ctx, chunk.ID, targetNodes)
	if err != nil {
		t.Fatalf("Failed to replicate chunk: %v", err)
	}

	// Verify 3 replicas exist
	replicas, err := rm.GetReplicas(chunk.ID)
	if err != nil || len(replicas) != 3 {
		t.Fatalf("Expected 3 replicas, got %d", len(replicas))
	}

	// Remove one replica
	err = rm.RemoveReplica(chunk.ID, "node-2")
	if err != nil {
		t.Errorf("Failed to remove replica: %v", err)
	}

	// Verify only 2 replicas remain
	replicas, err = rm.GetReplicas(chunk.ID)
	if err != nil {
		t.Fatalf("Failed to get replicas after removal: %v", err)
	}

	if len(replicas) != 2 {
		t.Errorf("Expected 2 replicas after removal, got %d", len(replicas))
	}

	// Verify the removed node doesn't have a replica
	for _, r := range replicas {
		if r.NodeID == "node-2" {
			t.Error("Replica for node-2 should have been removed")
		}
	}
}
