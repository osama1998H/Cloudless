package storage

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestReadConsistency_Strong tests strong consistency reads require leader
func TestReadConsistency_Strong(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create test bucket
	bucket := &Bucket{Name: "strong-consistency-test", StorageClass: StorageClassHot}
	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Create test object
	object := &Object{
		Bucket:       "strong-consistency-test",
		Key:          "test.txt",
		Size:         1024,
		Checksum:     "abc123",
		StorageClass: StorageClassHot,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := ms.PutObject(object); err != nil {
		t.Fatalf("Failed to put object: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Test strong consistency reads (should succeed on leader)
	opts := DefaultReadOptions() // Strong consistency by default

	b, err := ms.GetBucketWithOptions("strong-consistency-test", opts)
	if err != nil {
		t.Errorf("Strong consistency read should succeed on leader: %v", err)
	}
	if b == nil || b.Name != "strong-consistency-test" {
		t.Error("Strong consistency read returned wrong bucket")
	}

	obj, err := ms.GetObjectWithOptions("strong-consistency-test", "test.txt", opts)
	if err != nil {
		t.Errorf("Strong consistency object read should succeed on leader: %v", err)
	}
	if obj == nil || obj.Key != "test.txt" {
		t.Error("Strong consistency read returned wrong object")
	}

	objects, err := ms.ListObjectsWithOptions("strong-consistency-test", "", 10, opts)
	if err != nil {
		t.Errorf("Strong consistency list should succeed on leader: %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("Expected 1 object, got %d", len(objects))
	}

	t.Log("Strong consistency reads successful on leader")
}

// TestReadConsistency_Eventual tests eventual consistency allows follower reads
func TestReadConsistency_Eventual(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create test bucket
	bucket := &Bucket{
		Name:         "eventual-consistency-test",
		StorageClass: StorageClassHot,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Create test object
	object := &Object{
		Bucket:       "eventual-consistency-test",
		Key:          "test.txt",
		Size:         2048,
		Checksum:     "def456",
		StorageClass: StorageClassHot,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := ms.PutObject(object); err != nil {
		t.Fatalf("Failed to put object: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Test eventual consistency reads (should succeed even without leader check)
	opts := EventualReadOptions()

	b, err := ms.GetBucketWithOptions("eventual-consistency-test", opts)
	if err != nil {
		t.Errorf("Eventual consistency read failed: %v", err)
	}
	if b == nil || b.Name != "eventual-consistency-test" {
		t.Error("Eventual consistency read returned wrong bucket")
	}

	obj, err := ms.GetObjectWithOptions("eventual-consistency-test", "test.txt", opts)
	if err != nil {
		t.Errorf("Eventual consistency object read failed: %v", err)
	}
	if obj == nil || obj.Key != "test.txt" {
		t.Error("Eventual consistency read returned wrong object")
	}

	objects, err := ms.ListObjectsWithOptions("eventual-consistency-test", "", 10, opts)
	if err != nil {
		t.Errorf("Eventual consistency list failed: %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("Expected 1 object, got %d", len(objects))
	}

	t.Log("Eventual consistency reads successful")
}

// TestReadConsistency_BoundedFresh tests bounded consistency with fresh data
func TestReadConsistency_BoundedFresh(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create test bucket
	bucket := &Bucket{
		Name:         "bounded-fresh-test",
		StorageClass: StorageClassHot,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Create test object
	object := &Object{
		Bucket:       "bounded-fresh-test",
		Key:          "fresh.txt",
		Size:         512,
		Checksum:     "ghi789",
		StorageClass: StorageClassHot,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := ms.PutObject(object); err != nil {
		t.Fatalf("Failed to put object: %v", err)
	}

	time.Sleep(100 * time.Millisecond)

	// Test bounded consistency with generous staleness (should succeed)
	opts := BoundedReadOptions(10 * time.Second)

	b, err := ms.GetBucketWithOptions("bounded-fresh-test", opts)
	if err != nil {
		t.Errorf("Bounded consistency read should succeed with fresh data: %v", err)
	}
	if b == nil || b.Name != "bounded-fresh-test" {
		t.Error("Bounded consistency read returned wrong bucket")
	}

	obj, err := ms.GetObjectWithOptions("bounded-fresh-test", "fresh.txt", opts)
	if err != nil {
		t.Errorf("Bounded consistency object read should succeed with fresh data: %v", err)
	}
	if obj == nil || obj.Key != "fresh.txt" {
		t.Error("Bounded consistency read returned wrong object")
	}

	objects, err := ms.ListObjectsWithOptions("bounded-fresh-test", "", 10, opts)
	if err != nil {
		t.Errorf("Bounded consistency list should succeed with fresh data: %v", err)
	}
	if len(objects) != 1 {
		t.Errorf("Expected 1 object, got %d", len(objects))
	}

	t.Log("Bounded consistency reads successful with fresh data")
}

// TestReadConsistency_BoundedStale tests bounded consistency rejects stale data
func TestReadConsistency_BoundedStale(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create bucket with old timestamp
	oldTime := time.Now().Add(-5 * time.Second)
	bucket := &Bucket{
		Name:         "bounded-stale-test",
		StorageClass: StorageClassHot,
		CreatedAt:    oldTime,
		UpdatedAt:    oldTime,
	}

	// Manually store with old timestamp (bypassing RAFT for testing)
	ms.buckets.Store(bucket.Name, bucket)

	// Create object with old timestamp
	object := &Object{
		Bucket:       "bounded-stale-test",
		Key:          "stale.txt",
		Size:         256,
		Checksum:     "jkl012",
		StorageClass: StorageClassHot,
		CreatedAt:    oldTime,
		UpdatedAt:    oldTime,
	}
	objectKey := ms.getObjectKey(object.Bucket, object.Key)
	ms.objects.Store(objectKey, object)

	// Test bounded consistency with strict staleness (should fail)
	opts := BoundedReadOptions(1 * time.Second)

	// Bucket read should fail due to staleness
	_, err := ms.GetBucketWithOptions("bounded-stale-test", opts)
	if err == nil {
		t.Error("Bounded consistency should reject stale bucket")
	}
	if !IsStaleReadError(err) {
		t.Errorf("Expected StaleReadError, got: %v", err)
	}

	// Object read should fail due to staleness
	_, err = ms.GetObjectWithOptions("bounded-stale-test", "stale.txt", opts)
	if err == nil {
		t.Error("Bounded consistency should reject stale object")
	}
	if !IsStaleReadError(err) {
		t.Errorf("Expected StaleReadError for object, got: %v", err)
	}

	t.Log("Bounded consistency correctly rejected stale data")
}

// TestReadConsistency_ListBoundedFiltering tests list operations filter stale objects
func TestReadConsistency_ListBoundedFiltering(t *testing.T) {
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
		Name:         "list-filtering-test",
		StorageClass: StorageClassHot,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	// Create fresh object
	freshObject := &Object{
		Bucket:       "list-filtering-test",
		Key:          "fresh.txt",
		Size:         1024,
		Checksum:     "fresh123",
		StorageClass: StorageClassHot,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := ms.PutObject(freshObject); err != nil {
		t.Fatalf("Failed to put fresh object: %v", err)
	}

	// Create stale object (manual injection for testing)
	staleTime := time.Now().Add(-10 * time.Second)
	staleObject := &Object{
		Bucket:       "list-filtering-test",
		Key:          "stale.txt",
		Size:         512,
		Checksum:     "stale456",
		StorageClass: StorageClassHot,
		CreatedAt:    staleTime,
		UpdatedAt:    staleTime,
	}
	staleKey := ms.getObjectKey(staleObject.Bucket, staleObject.Key)
	ms.objects.Store(staleKey, staleObject)

	time.Sleep(100 * time.Millisecond)

	// List with bounded consistency (5s staleness)
	opts := BoundedReadOptions(5 * time.Second)

	objects, err := ms.ListObjectsWithOptions("list-filtering-test", "", 10, opts)
	if err != nil {
		t.Fatalf("List with bounded consistency failed: %v", err)
	}

	// Should only return fresh object, filtering out stale one
	if len(objects) != 1 {
		t.Errorf("Expected 1 fresh object, got %d", len(objects))
	}

	if len(objects) > 0 && objects[0].Key != "fresh.txt" {
		t.Errorf("Expected fresh.txt, got %s", objects[0].Key)
	}

	// List with eventual consistency (should return both)
	opts = EventualReadOptions()
	objects, err = ms.ListObjectsWithOptions("list-filtering-test", "", 10, opts)
	if err != nil {
		t.Fatalf("List with eventual consistency failed: %v", err)
	}

	if len(objects) != 2 {
		t.Errorf("Expected 2 objects with eventual consistency, got %d", len(objects))
	}

	t.Log("List operation correctly filtered stale objects with bounded consistency")
}

// TestReadConsistency_NotFoundError tests structured error types
func TestReadConsistency_NotFoundError(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	opts := DefaultReadOptions()

	// Test bucket not found
	_, err := ms.GetBucketWithOptions("nonexistent-bucket", opts)
	if err == nil {
		t.Error("Expected NotFoundError for nonexistent bucket")
	}
	if !IsNotFoundError(err) {
		t.Errorf("Expected NotFoundError, got: %v", err)
	}

	// Test object not found
	bucket := &Bucket{Name: "error-test", StorageClass: StorageClassHot}
	if err := ms.CreateBucket(bucket); err != nil {
		t.Fatalf("Failed to create bucket: %v", err)
	}

	_, err = ms.GetObjectWithOptions("error-test", "nonexistent.txt", opts)
	if err == nil {
		t.Error("Expected NotFoundError for nonexistent object")
	}
	if !IsNotFoundError(err) {
		t.Errorf("Expected NotFoundError, got: %v", err)
	}

	t.Log("NotFoundError correctly returned for missing resources")
}

// TestReadConsistency_ConsistencyLevelStrings tests consistency level string representations
func TestReadConsistency_ConsistencyLevelStrings(t *testing.T) {
	tests := []struct {
		level    ReadConsistency
		expected string
	}{
		{ReadConsistencyStrong, "strong"},
		{ReadConsistencyEventual, "eventual"},
		{ReadConsistencyBounded, "bounded"},
		{ReadConsistency(99), "unknown"},
	}

	for _, tt := range tests {
		if got := tt.level.String(); got != tt.expected {
			t.Errorf("ReadConsistency(%d).String() = %q, want %q", tt.level, got, tt.expected)
		}
	}

	t.Log("Consistency level string representations correct")
}
