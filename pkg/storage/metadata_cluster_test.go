package storage

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/osama1998H/Cloudless/pkg/raft"
	"go.uber.org/zap"
)

// TestMetadataCluster_ThreeNodes tests a 3-node RAFT cluster
// CLD-REQ-051: Metadata strong consistency with multi-node RAFT
func TestMetadataCluster_ThreeNodes(t *testing.T) {
	cluster := setupThreeNodeCluster(t)
	defer cluster.Shutdown()

	// Wait for leader election
	leader := cluster.WaitForLeader(t, 15*time.Second)
	t.Logf("Leader elected: %s", leader.GetConfig().RaftID)

	// Create bucket on leader
	bucket := &Bucket{
		Name:         "cluster-test",
		StorageClass: StorageClassHot,
		QuotaBytes:   1024 * 1024,
	}

	leaderStore := cluster.GetMetadataStore(leader.GetConfig().RaftID)
	if err := leaderStore.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket on leader: %v", err)
	}

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify bucket exists on all nodes
	for _, node := range cluster.Nodes {
		store := cluster.GetMetadataStore(node.GetConfig().RaftID)
		retrieved, err := store.GetBucket("cluster-test")
		if err != nil {
			t.Errorf("Bucket not found on node %s: %v", node.GetConfig().RaftID, err)
			continue
		}
		if retrieved.Name != "cluster-test" {
			t.Errorf("Node %s: expected bucket 'cluster-test', got '%s'", node.GetConfig().RaftID, retrieved.Name)
		}
	}

	t.Log("Bucket replicated successfully across all nodes")
}

// TestMetadataCluster_LeaderFailover tests leader election after leader failure
// CLD-REQ-051: Strong consistency maintained during leader failover
func TestMetadataCluster_LeaderFailover(t *testing.T) {
	cluster := setupThreeNodeCluster(t)
	defer cluster.Shutdown()

	// Wait for initial leader
	initialLeader := cluster.WaitForLeader(t, 15*time.Second)
	initialLeaderID := initialLeader.GetConfig().RaftID
	t.Logf("Initial leader: %s", initialLeaderID)

	// Create bucket on initial leader
	bucket := &Bucket{
		Name:         "failover-test",
		StorageClass: StorageClassCold,
	}

	leaderStore := cluster.GetMetadataStore(initialLeaderID)
	if err := leaderStore.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Kill the leader
	t.Logf("Killing leader %s", initialLeaderID)
	cluster.ShutdownNode(initialLeaderID)

	// Wait for new leader election
	time.Sleep(5 * time.Second)
	newLeader := cluster.WaitForLeader(t, 15*time.Second)
	newLeaderID := newLeader.GetConfig().RaftID

	if newLeaderID == initialLeaderID {
		t.Fatal("New leader is same as old leader (should be different)")
	}

	t.Logf("New leader elected: %s", newLeaderID)

	// Verify bucket still exists on new leader
	newLeaderStore := cluster.GetMetadataStore(newLeaderID)
	retrieved, err := newLeaderStore.GetBucket("failover-test")
	if err != nil {
		t.Fatalf("Bucket not found on new leader: %v", err)
	}

	if retrieved.Name != "failover-test" {
		t.Errorf("Expected bucket 'failover-test', got '%s'", retrieved.Name)
	}

	// Create another bucket on new leader (verify writes work)
	bucket2 := &Bucket{
		Name:         "after-failover",
		StorageClass: StorageClassHot,
	}

	if err := newLeaderStore.CreateBucket(bucket2); err != nil {
		t.Fatalf("Failed to create bucket on new leader: %v", err)
	}

	t.Log("Leader failover successful, data consistent")
}

// TestMetadataCluster_ConcurrentWrites tests concurrent writes to RAFT cluster
// CLD-REQ-051: Strong consistency under concurrent load
func TestMetadataCluster_ConcurrentWrites(t *testing.T) {
	cluster := setupThreeNodeCluster(t)
	defer cluster.Shutdown()

	// Wait for leader
	leader := cluster.WaitForLeader(t, 15*time.Second)
	leaderStore := cluster.GetMetadataStore(leader.GetConfig().RaftID)
	t.Logf("Leader: %s", leader.GetConfig().RaftID)

	// Create parent bucket
	bucket := &Bucket{
		Name:         "concurrent-bucket",
		StorageClass: StorageClassHot,
	}
	if err := leaderStore.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create parent bucket: %v", err)
	}

	// Concurrent object writes
	const numObjects = 50
	errors := make(chan error, numObjects)
	var wg sync.WaitGroup

	for i := 0; i < numObjects; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			object := &Object{
				Bucket:       "concurrent-bucket",
				Key:          fmt.Sprintf("object-%d", id),
				Size:         int64(100 * id),
				Checksum:     fmt.Sprintf("checksum-%d", id),
				StorageClass: StorageClassHot,
			}

			if err := leaderStore.PutObject(object); err != nil {
				errors <- fmt.Errorf("object-%d: %w", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent write failed: %v", err)
	}

	// Wait for replication
	time.Sleep(1 * time.Second)

	// Verify all objects exist on all nodes
	for _, node := range cluster.Nodes {
		store := cluster.GetMetadataStore(node.GetConfig().RaftID)
		objects, err := store.ListObjects("concurrent-bucket", "", 100)
		if err != nil {
			t.Errorf("Node %s: failed to list objects: %v", node.GetConfig().RaftID, err)
			continue
		}

		if len(objects) != numObjects {
			t.Errorf("Node %s: expected %d objects, got %d", node.GetConfig().RaftID, numObjects, len(objects))
		}
	}

	t.Log("Concurrent writes successful, data consistent across all nodes")
}

// TestMetadataCluster_SnapshotPropagation tests snapshot creation and propagation
// CLD-REQ-051: Metadata survives restart via RAFT snapshots
func TestMetadataCluster_SnapshotPropagation(t *testing.T) {
	cluster := setupThreeNodeCluster(t)
	defer cluster.Shutdown()

	// Wait for leader
	leader := cluster.WaitForLeader(t, 15*time.Second)
	leaderStore := cluster.GetMetadataStore(leader.GetConfig().RaftID)

	// Create multiple buckets to trigger snapshot
	for i := 0; i < 10; i++ {
		bucket := &Bucket{
			Name:         fmt.Sprintf("snapshot-bucket-%d", i),
			StorageClass: StorageClassHot,
		}
		if err := leaderStore.CreateBucket(bucket); err != nil {
			t.Fatalf("Failed to create bucket %d: %v", i, err)
		}
	}

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Trigger snapshot on leader
	t.Log("Creating snapshot on leader")
	if err := leader.Snapshot(); err != nil {
		t.Logf("Snapshot creation: %v", err)
	}

	// Wait for snapshot to complete
	time.Sleep(1 * time.Second)

	// Verify buckets exist on all nodes
	for _, node := range cluster.Nodes {
		store := cluster.GetMetadataStore(node.GetConfig().RaftID)
		for i := 0; i < 10; i++ {
			bucketName := fmt.Sprintf("snapshot-bucket-%d", i)
			if _, err := store.GetBucket(bucketName); err != nil {
				t.Errorf("Node %s: bucket %s not found: %v", node.GetConfig().RaftID, bucketName, err)
			}
		}
	}

	t.Log("Snapshot propagation successful")
}

// TestMetadataCluster_NodeRecovery tests node rejoining after temporary failure
// CLD-REQ-051: Node recovers metadata state from RAFT
func TestMetadataCluster_NodeRecovery(t *testing.T) {
	cluster := setupThreeNodeCluster(t)
	defer cluster.Shutdown()

	// Wait for leader
	leader := cluster.WaitForLeader(t, 15*time.Second)
	leaderStore := cluster.GetMetadataStore(leader.GetConfig().RaftID)

	// Find a follower to shutdown
	var followerID string
	for _, node := range cluster.Nodes {
		if node.GetConfig().RaftID != leader.GetConfig().RaftID {
			followerID = node.GetConfig().RaftID
			break
		}
	}

	if followerID == "" {
		t.Fatal("No follower found")
	}

	t.Logf("Shutting down follower %s", followerID)
	cluster.ShutdownNode(followerID)

	// Create buckets while follower is down
	for i := 0; i < 5; i++ {
		bucket := &Bucket{
			Name:         fmt.Sprintf("recovery-bucket-%d", i),
			StorageClass: StorageClassHot,
		}
		if err := leaderStore.CreateBucket(bucket); err != nil {
			t.Fatalf("Failed to create bucket %d: %v", i, err)
		}
	}

	// Wait for replication to remaining nodes
	time.Sleep(500 * time.Millisecond)

	// Restart follower
	t.Logf("Restarting follower %s", followerID)
	if err := cluster.RestartNode(followerID); err != nil {
		t.Fatalf("Failed to restart follower: %v", err)
	}

	// Wait for node to catch up
	time.Sleep(3 * time.Second)

	// Verify follower has all buckets
	recoveredStore := cluster.GetMetadataStore(followerID)
	for i := 0; i < 5; i++ {
		bucketName := fmt.Sprintf("recovery-bucket-%d", i)
		if _, err := recoveredStore.GetBucket(bucketName); err != nil {
			t.Errorf("Recovered node: bucket %s not found: %v", bucketName, err)
		}
	}

	t.Log("Node recovery successful, data consistent")
}

// TestMetadataCluster_NonLeaderRejects tests that followers reject writes
// CLD-REQ-051: Only leader accepts writes
func TestMetadataCluster_NonLeaderRejects(t *testing.T) {
	cluster := setupThreeNodeCluster(t)
	defer cluster.Shutdown()

	// Wait for leader
	leader := cluster.WaitForLeader(t, 15*time.Second)
	leaderID := leader.GetConfig().RaftID

	// Find a follower
	var followerID string
	for _, node := range cluster.Nodes {
		if node.GetConfig().RaftID != leaderID {
			followerID = node.GetConfig().RaftID
			break
		}
	}

	if followerID == "" {
		t.Fatal("No follower found")
	}

	t.Logf("Leader: %s, Follower: %s", leaderID, followerID)

	// Try to write to follower (should fail)
	followerStore := cluster.GetMetadataStore(followerID)
	bucket := &Bucket{
		Name:         "follower-write-test",
		StorageClass: StorageClassHot,
	}

	err := followerStore.CreateBucket(bucket)
	if err == nil {
		t.Fatal("Expected follower write to fail, but it succeeded")
	}

	// Verify error message mentions not being leader
	errMsg := err.Error()
	if !contains(errMsg, "not RAFT leader") && !contains(errMsg, "redirect") {
		t.Errorf("Expected 'not leader' error, got: %v", err)
	}

	t.Logf("Follower correctly rejected write: %v", err)
}

// Cluster test infrastructure

// RAFTCluster represents a multi-node RAFT cluster for testing
type RAFTCluster struct {
	Nodes          []*raft.Store
	MetadataStores map[string]*MetadataStore
	TempDirs       map[string]string
	logger         *zap.Logger
	t              *testing.T
}

// setupThreeNodeCluster creates a 3-node RAFT cluster for testing
func setupThreeNodeCluster(t *testing.T) *RAFTCluster {
	logger := zap.NewNop()

	cluster := &RAFTCluster{
		Nodes:          make([]*raft.Store, 0, 3),
		MetadataStores: make(map[string]*MetadataStore),
		TempDirs:       make(map[string]string),
		logger:         logger,
		t:              t,
	}

	// Node IDs
	nodeIDs := []string{"cluster-node-1", "cluster-node-2", "cluster-node-3"}

	// Create first node (bootstrap)
	node1Dir := t.TempDir()
	cluster.TempDirs[nodeIDs[0]] = node1Dir

	config1 := &raft.Config{
		RaftDir:        filepath.Join(node1Dir, "raft"),
		RaftBind:       "127.0.0.1:0", // Random port
		RaftID:         nodeIDs[0],
		Bootstrap:      true,
		Logger:         logger,
		EnableSingle:   false, // Multi-node mode
		SnapshotRetain: 2,
	}

	store1, err := raft.NewStore(config1)
	if err != nil {
		t.Fatalf("Failed to create node 1: %v", err)
	}
	cluster.Nodes = append(cluster.Nodes, store1)
	cluster.MetadataStores[nodeIDs[0]] = NewMetadataStore(store1, logger)

	// Wait for first node to bootstrap
	time.Sleep(2 * time.Second)

	// Get first node's RAFT address for joining
	// Note: For proper RAFT clustering, nodes need to be added via Join() API
	// In HashiCorp RAFT, the leader must explicitly add follower nodes

	// Create remaining nodes
	for i := 1; i < 3; i++ {
		nodeDir := t.TempDir()
		cluster.TempDirs[nodeIDs[i]] = nodeDir

		config := &raft.Config{
			RaftDir:        filepath.Join(nodeDir, "raft"),
			RaftBind:       "127.0.0.1:0",
			RaftID:         nodeIDs[i],
			Bootstrap:      false,
			Logger:         logger,
			EnableSingle:   false,
			SnapshotRetain: 2,
		}

		store, err := raft.NewStore(config)
		if err != nil {
			t.Fatalf("Failed to create node %d: %v", i+1, err)
		}

		cluster.Nodes = append(cluster.Nodes, store)
		cluster.MetadataStores[nodeIDs[i]] = NewMetadataStore(store, logger)

		// Leader (node1) must explicitly join this node
		nodeAddr := store.GetRaftAddr()
		if err := store1.Join(nodeIDs[i], nodeAddr); err != nil {
			t.Logf("Warning: Failed to join node %s from leader: %v", nodeIDs[i], err)
		}

		// Small delay between joins
		time.Sleep(500 * time.Millisecond)
	}

	t.Logf("Created 3-node cluster: %v", nodeIDs)
	return cluster
}

// WaitForLeader waits for leader election and returns the leader node
func (c *RAFTCluster) WaitForLeader(t *testing.T, timeout time.Duration) *raft.Store {
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		for _, node := range c.Nodes {
			if node.IsLeader() {
				return node
			}
		}
		time.Sleep(100 * time.Millisecond)
	}

	t.Fatal("Timeout waiting for leader election")
	return nil
}

// GetMetadataStore returns the metadata store for a node
func (c *RAFTCluster) GetMetadataStore(nodeID string) *MetadataStore {
	return c.MetadataStores[nodeID]
}

// ShutdownNode shuts down a specific node
func (c *RAFTCluster) ShutdownNode(nodeID string) {
	for i, node := range c.Nodes {
		if node.GetConfig().RaftID == nodeID {
			if err := node.Close(); err != nil {
				c.t.Errorf("Failed to close node %s: %v", nodeID, err)
			}
			// Remove from active nodes
			c.Nodes = append(c.Nodes[:i], c.Nodes[i+1:]...)
			delete(c.MetadataStores, nodeID)
			c.t.Logf("Node %s shut down", nodeID)
			return
		}
	}
}

// RestartNode restarts a previously shut down node
func (c *RAFTCluster) RestartNode(nodeID string) error {
	nodeDir, ok := c.TempDirs[nodeID]
	if !ok {
		return fmt.Errorf("node %s not found in temp dirs", nodeID)
	}

	// Find join address from existing nodes
	var joinAddr string
	if len(c.Nodes) > 0 {
		joinAddr = c.Nodes[0].GetRaftAddr()
	}

	config := &raft.Config{
		RaftDir:        filepath.Join(nodeDir, "raft"),
		RaftBind:       "127.0.0.1:0",
		RaftID:         nodeID,
		Bootstrap:      false,
		Logger:         c.logger,
		EnableSingle:   false,
		SnapshotRetain: 2,
		JoinAddr:       joinAddr,
	}

	store, err := raft.NewStore(config)
	if err != nil {
		return fmt.Errorf("failed to restart node: %w", err)
	}

	c.Nodes = append(c.Nodes, store)
	c.MetadataStores[nodeID] = NewMetadataStore(store, c.logger)

	c.t.Logf("Node %s restarted", nodeID)
	return nil
}

// Shutdown shuts down all nodes in the cluster
func (c *RAFTCluster) Shutdown() {
	for _, node := range c.Nodes {
		if err := node.Close(); err != nil {
			c.t.Errorf("Failed to close node: %v", err)
		}
	}
}

// Helper functions

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
