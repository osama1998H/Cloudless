package storage

import (
	"fmt"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestBatch_FluentAPI tests the fluent batch API
func TestBatch_FluentAPI(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Build batch using fluent API
	batch := ms.BeginBatch().
		CreateBucket(&Bucket{Name: "batch-bucket-1", StorageClass: StorageClassHot}).
		CreateBucket(&Bucket{Name: "batch-bucket-2", StorageClass: StorageClassCold}).
		PutObject(&Object{
			Bucket:       "batch-bucket-1",
			Key:          "object1.txt",
			Size:         1024,
			Checksum:     "abc123",
			StorageClass: StorageClassHot,
		})

	if batch.Size() != 3 {
		t.Errorf("Expected batch size 3, got %d", batch.Size())
	}

	// Execute batch
	result, err := batch.Execute()
	if err != nil {
		t.Fatalf("Batch execution failed: %v", err)
	}

	if !result.IsSuccess() {
		t.Errorf("Batch not successful: %d failures", result.FailureCount)
		for _, failure := range result.GetFailures() {
			t.Errorf("  Operation %d (%s) failed: %s", failure.Index, failure.Type, failure.Error)
		}
	}

	// Wait for replication
	time.Sleep(200 * time.Millisecond)

	// Verify buckets were created
	if _, err := ms.GetBucket("batch-bucket-1"); err != nil {
		t.Errorf("Bucket batch-bucket-1 not found: %v", err)
	}
	if _, err := ms.GetBucket("batch-bucket-2"); err != nil {
		t.Errorf("Bucket batch-bucket-2 not found: %v", err)
	}

	// Verify object was created
	if _, err := ms.GetObject("batch-bucket-1", "object1.txt"); err != nil {
		t.Errorf("Object not found: %v", err)
	}

	t.Log("Fluent API batch execution successful")
}

// TestBatch_LargeOperations tests batching many operations
func TestBatch_LargeOperations(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create parent bucket first
	parentBucket := &Bucket{Name: "large-batch", StorageClass: StorageClassHot}
	if err := ms.CreateBucket(parentBucket); err != nil {
		t.Fatalf("Failed to create parent bucket: %v", err)
	}

	// Build large batch
	batch := ms.BeginBatch()
	const numObjects = 100

	for i := 0; i < numObjects; i++ {
		batch.PutObject(&Object{
			Bucket:       "large-batch",
			Key:          fmt.Sprintf("object-%d.txt", i),
			Size:         int64(i * 100),
			Checksum:     fmt.Sprintf("checksum-%d", i),
			StorageClass: StorageClassHot,
		})
	}

	if batch.Size() != numObjects {
		t.Errorf("Expected batch size %d, got %d", numObjects, batch.Size())
	}

	// Execute batch
	start := time.Now()
	result, err := batch.Execute()
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Batch execution failed: %v", err)
	}

	if !result.IsSuccess() {
		t.Errorf("Batch not successful: %d failures", result.FailureCount)
	}

	t.Logf("Executed %d operations in %v (%.2f ops/sec)",
		numObjects, duration, float64(numObjects)/duration.Seconds())

	// Wait for replication
	time.Sleep(500 * time.Millisecond)

	// Verify objects exist
	objects, err := ms.ListObjects("large-batch", "", numObjects+10)
	if err != nil {
		t.Fatalf("Failed to list objects: %v", err)
	}

	if len(objects) != numObjects {
		t.Errorf("Expected %d objects, got %d", numObjects, len(objects))
	}

	t.Log("Large batch execution successful")
}

// TestBatch_MixedOperations tests batching creates and deletes
func TestBatch_MixedOperations(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Create initial buckets
	for i := 1; i <= 3; i++ {
		bucket := &Bucket{
			Name:         fmt.Sprintf("mixed-bucket-%d", i),
			StorageClass: StorageClassHot,
		}
		if err := ms.CreateBucket(bucket); err != nil {
			t.Fatalf("Failed to create initial bucket %d: %v", i, err)
		}
	}

	// Wait for replication
	time.Sleep(200 * time.Millisecond)

	// Mixed batch: create new buckets, delete old ones, add objects
	batch := ms.BeginBatch().
		CreateBucket(&Bucket{Name: "mixed-bucket-4", StorageClass: StorageClassCold}).
		DeleteBucket("mixed-bucket-1").
		PutObject(&Object{
			Bucket:       "mixed-bucket-2",
			Key:          "file.txt",
			Size:         2048,
			Checksum:     "xyz",
			StorageClass: StorageClassHot,
		}).
		DeleteBucket("mixed-bucket-3").
		CreateBucket(&Bucket{Name: "mixed-bucket-5", StorageClass: StorageClassHot})

	result, err := batch.Execute()
	if err != nil {
		t.Fatalf("Batch execution failed: %v", err)
	}

	if !result.IsSuccess() {
		t.Errorf("Batch not successful: %d failures", result.FailureCount)
	}

	// Wait for replication
	time.Sleep(200 * time.Millisecond)

	// Verify results
	if _, err := ms.GetBucket("mixed-bucket-1"); err == nil {
		t.Error("Bucket mixed-bucket-1 should have been deleted")
	}

	if _, err := ms.GetBucket("mixed-bucket-2"); err != nil {
		t.Error("Bucket mixed-bucket-2 should still exist")
	}

	if _, err := ms.GetBucket("mixed-bucket-3"); err == nil {
		t.Error("Bucket mixed-bucket-3 should have been deleted")
	}

	if _, err := ms.GetBucket("mixed-bucket-4"); err != nil {
		t.Error("Bucket mixed-bucket-4 should have been created")
	}

	if _, err := ms.GetBucket("mixed-bucket-5"); err != nil {
		t.Error("Bucket mixed-bucket-5 should have been created")
	}

	if _, err := ms.GetObject("mixed-bucket-2", "file.txt"); err != nil {
		t.Errorf("Object should have been created: %v", err)
	}

	t.Log("Mixed operations batch successful")
}

// TestBatch_EmptyBatch tests executing an empty batch
func TestBatch_EmptyBatch(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Execute empty batch
	batch := ms.BeginBatch()
	result, err := batch.Execute()
	if err != nil {
		t.Errorf("Empty batch should not error: %v", err)
	}

	if result.TotalOps != 0 {
		t.Errorf("Expected 0 operations, got %d", result.TotalOps)
	}

	t.Log("Empty batch handled correctly")
}

// TestBatch_Clear tests clearing a batch
func TestBatch_Clear(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Build batch
	batch := ms.BeginBatch().
		CreateBucket(&Bucket{Name: "clear-test", StorageClass: StorageClassHot}).
		CreateBucket(&Bucket{Name: "clear-test-2", StorageClass: StorageClassCold})

	if batch.Size() != 2 {
		t.Errorf("Expected size 2, got %d", batch.Size())
	}

	// Clear batch
	batch.Clear()

	if batch.Size() != 0 {
		t.Errorf("Expected size 0 after clear, got %d", batch.Size())
	}

	t.Log("Batch clear successful")
}

// TestBatch_ReuseAfterExecute tests reusing batch after execution
func TestBatch_ReuseAfterExecute(t *testing.T) {
	raftStore, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	ms := NewMetadataStore(raftStore, logger)

	// Wait for leader
	if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// First batch
	batch := ms.BeginBatch().CreateBucket(&Bucket{Name: "reuse-1", StorageClass: StorageClassHot})
	if _, err := batch.Execute(); err != nil {
		t.Fatalf("First batch failed: %v", err)
	}

	// Reuse batch for second execution
	batch.CreateBucket(&Bucket{Name: "reuse-2", StorageClass: StorageClassCold})
	if _, err := batch.Execute(); err != nil {
		t.Fatalf("Second batch failed: %v", err)
	}

	// Wait for replication
	time.Sleep(200 * time.Millisecond)

	// Verify both buckets exist
	if _, err := ms.GetBucket("reuse-1"); err != nil {
		t.Error("Bucket reuse-1 should exist")
	}
	if _, err := ms.GetBucket("reuse-2"); err != nil {
		t.Error("Bucket reuse-2 should exist")
	}

	t.Log("Batch reuse successful")
}

// BenchmarkBatch_vs_Individual compares batch vs individual operations
func BenchmarkBatch_vs_Individual(b *testing.B) {
	b.Run("Individual", func(b *testing.B) {
		raftStore, cleanup := setupBenchRAFTStore(b)
		defer cleanup()

		logger := zap.NewNop()
		ms := NewMetadataStore(raftStore, logger)

		if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
			b.Fatalf("Failed to wait for leader: %v", err)
		}

		// Create parent bucket
		parentBucket := &Bucket{Name: "bench-individual", StorageClass: StorageClassHot}
		if err := ms.CreateBucket(parentBucket); err != nil {
			b.Fatalf("Failed to create parent bucket: %v", err)
		}

		b.ResetTimer()

		for i := 0; i < b.N; i++ {
			object := &Object{
				Bucket:       "bench-individual",
				Key:          fmt.Sprintf("object-%d.txt", i),
				Size:         1024,
				Checksum:     fmt.Sprintf("checksum-%d", i),
				StorageClass: StorageClassHot,
			}
			if err := ms.PutObject(object); err != nil {
				b.Fatalf("Failed to put object: %v", err)
			}
		}
	})

	b.Run("Batch10", func(b *testing.B) {
		raftStore, cleanup := setupBenchRAFTStore(b)
		defer cleanup()

		logger := zap.NewNop()
		ms := NewMetadataStore(raftStore, logger)

		if err := raftStore.WaitForLeader(10 * time.Second); err != nil {
			b.Fatalf("Failed to wait for leader: %v", err)
		}

		// Create parent bucket
		parentBucket := &Bucket{Name: "bench-batch10", StorageClass: StorageClassHot}
		if err := ms.CreateBucket(parentBucket); err != nil {
			b.Fatalf("Failed to create parent bucket: %v", err)
		}

		b.ResetTimer()

		const batchSize = 10
		for i := 0; i < b.N; i += batchSize {
			batch := ms.BeginBatch()
			for j := 0; j < batchSize; j++ {
				batch.PutObject(&Object{
					Bucket:       "bench-batch10",
					Key:          fmt.Sprintf("object-%d.txt", i+j),
					Size:         1024,
					Checksum:     fmt.Sprintf("checksum-%d", i+j),
					StorageClass: StorageClassHot,
				})
			}
			if _, err := batch.Execute(); err != nil {
				b.Fatalf("Batch failed: %v", err)
			}
		}
	})
}
