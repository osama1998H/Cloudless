package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudless/cloudless/pkg/raft"
	"go.uber.org/zap"
)

// TestMetadataStore_WithRAFT_CreateBucket tests bucket creation with RAFT consensus
// CLD-REQ-051: Metadata (buckets, manifests) is strongly consistent via RAFT
func TestMetadataStore_WithRAFT_CreateBucket(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader election
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create bucket
	bucket := &Bucket{
		Name:         "test-bucket",
		StorageClass: StorageClassHot,
		QuotaBytes:   1024 * 1024 * 1024, // 1GB
	}

	err := ms.CreateBucket(bucket)
	if err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Verify bucket exists in local cache
	retrieved, err := ms.GetBucket("test-bucket")
	if err != nil {
		t.Fatalf("Failed to get bucket: %v", err)
	}

	if retrieved.Name != "test-bucket" {
		t.Errorf("Expected bucket name 'test-bucket', got '%s'", retrieved.Name)
	}

	if retrieved.StorageClass != StorageClassHot {
		t.Errorf("Expected storage class 'hot', got '%s'", retrieved.StorageClass)
	}

	// Verify bucket is persisted in RAFT
	key := "bucket:test-bucket"
	value, err := raftStore.Get(key)
	if err != nil {
		t.Errorf("Bucket not found in RAFT store: %v", err)
	}

	if len(value) == 0 {
		t.Error("Bucket value in RAFT is empty")
	}
}

// TestMetadataStore_WithRAFT_DeleteBucket tests bucket deletion with RAFT
func TestMetadataStore_WithRAFT_DeleteBucket(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create bucket
	bucket := &Bucket{
		Name:         "delete-test",
		StorageClass: StorageClassCold,
	}

	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Delete bucket
	if err := ms.DeleteBucket("delete-test"); err != nil {
		t.Fatalf("Failed to delete bucket: %v", err)
	}

	// Verify bucket is gone from cache
	_, err := ms.GetBucket("delete-test")
	if err == nil {
		t.Error("Expected error when getting deleted bucket, got nil")
	}

	// Verify bucket is gone from RAFT
	key := "bucket:delete-test"
	_, err = raftStore.Get(key)
	if err == nil {
		t.Error("Expected error when getting deleted bucket from RAFT, got nil")
	}
}

// TestMetadataStore_WithRAFT_PutObject tests object metadata with RAFT
func TestMetadataStore_WithRAFT_PutObject(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create bucket first
	bucket := &Bucket{
		Name:         "object-test",
		StorageClass: StorageClassHot,
		QuotaBytes:   1024 * 1024,
	}

	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Put object metadata
	object := &Object{
		Bucket:       "object-test",
		Key:          "test-object.txt",
		Size:         1024,
		Checksum:     "abc123",
		ChunkIDs:     []string{"chunk-1", "chunk-2"},
		StorageClass: StorageClassHot,
	}

	if err := ms.PutObject(object); err != nil {
		t.Fatalf("Failed to put object: %v", err)
	}

	// Verify object exists
	retrieved, err := ms.GetObject("object-test", "test-object.txt")
	if err != nil {
		t.Fatalf("Failed to get object: %v", err)
	}

	if retrieved.Size != 1024 {
		t.Errorf("Expected size 1024, got %d", retrieved.Size)
	}

	if retrieved.Checksum != "abc123" {
		t.Errorf("Expected checksum 'abc123', got '%s'", retrieved.Checksum)
	}

	// Verify object is in RAFT
	key := "object:object-test:test-object.txt"
	value, err := raftStore.Get(key)
	if err != nil {
		t.Errorf("Object not found in RAFT store: %v", err)
	}

	if len(value) == 0 {
		t.Error("Object value in RAFT is empty")
	}
}

// TestMetadataStore_WithRAFT_DeleteObject tests object deletion with RAFT
func TestMetadataStore_WithRAFT_DeleteObject(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create bucket
	bucket := &Bucket{
		Name:         "delete-object-test",
		StorageClass: StorageClassHot,
	}

	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Put object
	object := &Object{
		Bucket:       "delete-object-test",
		Key:          "delete-me.txt",
		Size:         512,
		Checksum:     "xyz789",
		StorageClass: StorageClassHot,
	}

	if err := ms.PutObject(object); err != nil {
		t.Fatalf("Failed to put object: %v", err)
	}

	// Delete object
	if err := ms.DeleteObject("delete-object-test", "delete-me.txt"); err != nil {
		t.Fatalf("Failed to delete object: %v", err)
	}

	// Verify object is gone
	_, err := ms.GetObject("delete-object-test", "delete-me.txt")
	if err == nil {
		t.Error("Expected error when getting deleted object, got nil")
	}

	// Verify object is gone from RAFT
	key := "object:delete-object-test:delete-me.txt"
	_, err = raftStore.Get(key)
	if err == nil {
		t.Error("Expected error when getting deleted object from RAFT, got nil")
	}
}

// TestMetadataStore_WithoutRAFT_AgentMode tests agent mode (no RAFT)
func TestMetadataStore_WithoutRAFT_AgentMode(t *testing.T) {
	logger := zap.NewNop()
	ms := NewMetadataStore(nil, logger) // No RAFT store

	// Create bucket (should work without RAFT)
	bucket := &Bucket{
		Name:         "agent-bucket",
		StorageClass: StorageClassEphemeral,
	}

	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket in agent mode: %v", err)
	}

	// Verify bucket exists
	retrieved, err := ms.GetBucket("agent-bucket")
	if err != nil {
		t.Fatalf("Failed to get bucket in agent mode: %v", err)
	}

	if retrieved.Name != "agent-bucket" {
		t.Errorf("Expected bucket name 'agent-bucket', got '%s'", retrieved.Name)
	}

	// Delete bucket
	if err := ms.DeleteBucket("agent-bucket"); err != nil {
		t.Fatalf("Failed to delete bucket in agent mode: %v", err)
	}

	// Verify deletion
	_, err = ms.GetBucket("agent-bucket")
	if err == nil {
		t.Error("Expected error after deletion, got nil")
	}
}

// TestMetadataStore_ConcurrentOperations tests concurrent metadata updates with RAFT
// CLD-REQ-051: Ensures strong consistency under concurrent load
func TestMetadataStore_ConcurrentOperations(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create parent bucket
	bucket := &Bucket{
		Name:         "concurrent-test",
		StorageClass: StorageClassHot,
	}

	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Concurrent object creation
	const numObjects = 10
	done := make(chan error, numObjects)

	for i := 0; i < numObjects; i++ {
		go func(id int) {
			object := &Object{
				Bucket:       "concurrent-test",
				Key:          fmt.Sprintf("object-%d.txt", id),
				Size:         int64(100 * id),
				Checksum:     fmt.Sprintf("checksum-%d", id),
				StorageClass: StorageClassHot,
			}

			done <- ms.PutObject(object)
		}(i)
	}

	// Wait for all operations
	for i := 0; i < numObjects; i++ {
		if err := <-done; err != nil {
			t.Errorf("Concurrent operation %d failed: %v", i, err)
		}
	}

	// Verify all objects exist
	objects, err := ms.ListObjects("concurrent-test", "", 100)
	if err != nil {
		t.Fatalf("Failed to list objects: %v", err)
	}

	if len(objects) != numObjects {
		t.Errorf("Expected %d objects, got %d", numObjects, len(objects))
	}
}

// TestMetadataStore_SnapshotRestore tests RAFT snapshot/restore
// CLD-REQ-051: Ensures metadata survives restart via RAFT persistence
func TestMetadataStore_SnapshotRestore(t *testing.T) {
	// Create first RAFT store and add data
	tempDir := t.TempDir()

	raftStore1, err := createTestRAFTStoreAtPath(t, tempDir)
	if err != nil {
		t.Fatalf("Failed to create first RAFT store: %v", err)
	}

	logger := zap.NewNop()
	ms1 := NewMetadataStore(raftStore1, logger)

	// Wait for leader
	if err := raftStore1.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create bucket
	bucket := &Bucket{
		Name:         "snapshot-test",
		StorageClass: StorageClassHot,
	}

	if err := ms1.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Trigger snapshot
	if err := raftStore1.Snapshot(); err != nil {
		t.Fatalf("Failed to create snapshot: %v", err)
	}

	// Close first store
	if err := raftStore1.Close(); err != nil {
		t.Fatalf("Failed to close first store: %v", err)
	}

	// Create second RAFT store from same directory (simulates restart)
	raftStore2, err := createTestRAFTStoreAtPath(t, tempDir)
	if err != nil {
		t.Fatalf("Failed to create second RAFT store: %v", err)
	}
	defer raftStore2.Close()

	// Wait for leader
	if err := raftStore2.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader after restore: %v", err)
	}

	// Create new metadata store (should load from RAFT)
	ms2 := NewMetadataStore(raftStore2, logger)

	// Verify bucket still exists after restore
	retrieved, err := ms2.GetBucket("snapshot-test")
	if err != nil {
		t.Fatalf("Failed to get bucket after restore: %v", err)
	}

	if retrieved.Name != "snapshot-test" {
		t.Errorf("Expected bucket 'snapshot-test', got '%s'", retrieved.Name)
	}
}

// setupTestRAFTStore creates a test RAFT store
func setupTestRAFTStore(t *testing.T) (*raft.Store, func()) {
	tempDir := t.TempDir()

	store, err := createTestRAFTStoreAtPath(t, tempDir)
	if err != nil {
		t.Fatalf("Failed to create test RAFT store: %v", err)
	}

	return store, func() {
		if err := store.Close(); err != nil {
			t.Errorf("Failed to close RAFT store: %v", err)
		}
	}
}

// createTestRAFTStoreAtPath creates a RAFT store at a specific path
func createTestRAFTStoreAtPath(t *testing.T, dir string) (*raft.Store, error) {
	logger := zap.NewNop()

	config := &raft.Config{
		RaftDir:        filepath.Join(dir, "raft"),
		RaftBind:       "127.0.0.1:0", // Random port
		RaftID:         "test-node",
		Bootstrap:      true,
		Logger:         logger,
		EnableSingle:   true,
		SnapshotRetain: 2,
	}

	return raft.NewStore(config)
}

// TestMetadataStore_NonLeaderRejects tests that non-leaders reject writes
func TestMetadataStore_NonLeaderRejects(t *testing.T) {
	// This test would require a multi-node RAFT cluster
	// For now, we test the single-node case
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// In single-node mode, this node is always the leader
	// So we'll just verify the operation succeeds
	bucket := &Bucket{
		Name:         "leader-test",
		StorageClass: StorageClassHot,
	}

	if err := ms.CreateBucket(bucket); err != nil {
		t.Errorf("Leader should be able to create bucket: %v", err)
	}
}

// BenchmarkMetadataStore_CreateBucket benchmarks bucket creation with RAFT
func BenchmarkMetadataStore_CreateBucket(b *testing.B) {
	raftStore, cleanup := setupBenchRAFTStore(b)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		b.Fatalf("Failed to wait for leader: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		bucket := &Bucket{
			Name:         fmt.Sprintf("bench-bucket-%d", i),
			StorageClass: StorageClassHot,
		}

		if err := ms.CreateBucket(bucket); err != nil {
			b.Fatalf("Failed to create bucket: %v", err)
		}
	}
}

func setupBenchRAFTStore(b *testing.B) (*raft.Store, func()) {
	tempDir := b.TempDir()

	logger := zap.NewNop()

	config := &raft.Config{
		RaftDir:        filepath.Join(tempDir, "raft"),
		RaftBind:       "127.0.0.1:0",
		RaftID:         "bench-node",
		Bootstrap:      true,
		Logger:         logger,
		EnableSingle:   true,
		SnapshotRetain: 2,
	}

	store, err := raft.NewStore(config)
	if err != nil {
		b.Fatalf("Failed to create RAFT store: %v", err)
	}

	return store, func() {
		if err := store.Close(); err != nil {
			b.Errorf("Failed to close RAFT store: %v", err)
		}
	}
}

// Suppress unused imports
var (
	_ = context.Background
	_ = io.Discard
	_ = os.Stdout
)
