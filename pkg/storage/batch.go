package storage

import (
	"encoding/json"
	"fmt"

	"github.com/cloudless/cloudless/pkg/raft"
	"go.uber.org/zap"
)

// BatchOperation represents a single operation in a batch
type BatchOperation struct {
	Type   string      `json:"type"`   // create_bucket, delete_bucket, put_object, delete_object
	Bucket string      `json:"bucket"` // Bucket name
	Key    string      `json:"key,omitempty"`
	Value  []byte      `json:"value,omitempty"` // Serialized metadata
}

// MetadataBatch accumulates multiple metadata operations for batch execution
// CLD-REQ-051: Batch operations reduce RAFT round trips, improving throughput
type MetadataBatch struct {
	operations []*BatchOperation
	store      *MetadataStore
}

// NewBatch creates a new metadata batch
func NewBatch(store *MetadataStore) *MetadataBatch {
	return &MetadataBatch{
		operations: make([]*BatchOperation, 0, 10),
		store:      store,
	}
}

// CreateBucket adds a bucket creation to the batch
func (b *MetadataBatch) CreateBucket(bucket *Bucket) *MetadataBatch {
	value, err := json.Marshal(bucket)
	if err != nil {
		// Store error for later execution
		b.operations = append(b.operations, &BatchOperation{
			Type:   "error",
			Bucket: bucket.Name,
			Value:  []byte(err.Error()),
		})
		return b
	}

	b.operations = append(b.operations, &BatchOperation{
		Type:   "create_bucket",
		Bucket: bucket.Name,
		Value:  value,
	})

	return b
}

// DeleteBucket adds a bucket deletion to the batch
func (b *MetadataBatch) DeleteBucket(bucketName string) *MetadataBatch {
	b.operations = append(b.operations, &BatchOperation{
		Type:   "delete_bucket",
		Bucket: bucketName,
	})

	return b
}

// PutObject adds an object metadata write to the batch
func (b *MetadataBatch) PutObject(object *Object) *MetadataBatch {
	value, err := json.Marshal(object)
	if err != nil {
		b.operations = append(b.operations, &BatchOperation{
			Type:   "error",
			Bucket: object.Bucket,
			Key:    object.Key,
			Value:  []byte(err.Error()),
		})
		return b
	}

	b.operations = append(b.operations, &BatchOperation{
		Type:   "put_object_meta",
		Bucket: object.Bucket,
		Key:    object.Key,
		Value:  value,
	})

	return b
}

// DeleteObject adds an object metadata deletion to the batch
func (b *MetadataBatch) DeleteObject(bucket, key string) *MetadataBatch {
	b.operations = append(b.operations, &BatchOperation{
		Type:   "delete_object_meta",
		Bucket: bucket,
		Key:    key,
	})

	return b
}

// Size returns the number of operations in the batch
func (b *MetadataBatch) Size() int {
	return len(b.operations)
}

// Clear removes all operations from the batch
func (b *MetadataBatch) Clear() *MetadataBatch {
	b.operations = b.operations[:0]
	return b
}

// Execute executes all operations in the batch as a single RAFT transaction
// Returns a BatchResult containing outcomes for each operation
func (b *MetadataBatch) Execute() (*BatchResult, error) {
	if len(b.operations) == 0 {
		return &BatchResult{
			TotalOps:     0,
			SuccessCount: 0,
			FailureCount: 0,
			Results:      make([]OperationResult, 0),
		}, nil
	}

	// Check for errors accumulated during batch building
	for i, op := range b.operations {
		if op.Type == "error" {
			return nil, fmt.Errorf("operation %d has error: %s", i, string(op.Value))
		}
	}

	// Execute batch through metadata store
	return b.store.ExecuteBatch(b.operations)
}

// BatchResult contains the results of a batch execution
type BatchResult struct {
	TotalOps     int               // Total operations in batch
	SuccessCount int               // Number of successful operations
	FailureCount int               // Number of failed operations
	Results      []OperationResult // Per-operation results
}

// OperationResult represents the result of a single operation in a batch
type OperationResult struct {
	Index     int    // Index in the batch
	Type      string // Operation type
	Bucket    string // Bucket name
	Key       string // Object key (for object operations)
	Success   bool   // Whether operation succeeded
	Error     string // Error message if failed
}

// IsSuccess returns true if all operations succeeded
func (br *BatchResult) IsSuccess() bool {
	return br.FailureCount == 0
}

// GetFailures returns only the failed operations
func (br *BatchResult) GetFailures() []OperationResult {
	failures := make([]OperationResult, 0, br.FailureCount)
	for _, result := range br.Results {
		if !result.Success {
			failures = append(failures, result)
		}
	}
	return failures
}

// ExecuteBatch executes a batch of operations as a single RAFT transaction
// CLD-REQ-051: Batch operations improve throughput by reducing RAFT round trips
func (ms *MetadataStore) ExecuteBatch(operations []*BatchOperation) (*BatchResult, error) {
	if len(operations) == 0 {
		return &BatchResult{}, nil
	}

	// Create RAFT batch command
	cmd := &raft.MetadataBatchCommand{
		Operations: make([]raft.MetadataCommand, len(operations)),
	}

	// Convert BatchOperation to raft.MetadataCommand
	for i, op := range operations {
		cmd.Operations[i] = raft.MetadataCommand{
			Op:     op.Type,
			Bucket: op.Bucket,
			Key:    op.Key,
			Value:  op.Value,
		}
	}

	// Serialize batch command
	cmdBytes, err := json.Marshal(cmd)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal batch command: %w", err)
	}

	// Apply to RAFT (ensures strong consistency)
	if err := ms.applyRAFTCommand(cmdBytes); err != nil {
		return nil, fmt.Errorf("failed to apply batch to RAFT: %w", err)
	}

	// Update local cache for each operation (RAFT has committed)
	ms.mu.Lock()
	defer ms.mu.Unlock()

	result := &BatchResult{
		TotalOps:     len(operations),
		SuccessCount: 0,
		FailureCount: 0,
		Results:      make([]OperationResult, len(operations)),
	}

	for i, op := range operations {
		var err error
		success := true

		// Apply to local cache
		switch op.Type {
		case "create_bucket":
			var bucket Bucket
			if err = json.Unmarshal(op.Value, &bucket); err == nil {
				ms.buckets.Store(bucket.Name, &bucket)
			}
		case "delete_bucket":
			ms.buckets.Delete(op.Bucket)
		case "put_object_meta":
			var object Object
			if err = json.Unmarshal(op.Value, &object); err == nil {
				key := ms.getObjectKey(object.Bucket, object.Key)
				ms.objects.Store(key, &object)
			}
		case "delete_object_meta":
			key := ms.getObjectKey(op.Bucket, op.Key)
			ms.objects.Delete(key)
		}

		if err != nil {
			success = false
			result.FailureCount++
		} else {
			result.SuccessCount++
		}

		result.Results[i] = OperationResult{
			Index:   i,
			Type:    op.Type,
			Bucket:  op.Bucket,
			Key:     op.Key,
			Success: success,
			Error:   fmt.Sprintf("%v", err),
		}
	}

	// Update bucket count metric
	ms.updateBucketCountMetric()

	ms.logger.Info("Executed batch operation",
		zap.Int("operations", len(operations)),
		zap.Int("success", result.SuccessCount),
		zap.Int("failures", result.FailureCount),
	)

	return result, nil
}

// BeginBatch creates a new batch for this metadata store
// Fluent API for building and executing batches:
//
//	result, err := store.BeginBatch().
//	    CreateBucket(bucket1).
//	    CreateBucket(bucket2).
//	    PutObject(object1).
//	    Execute()
func (ms *MetadataStore) BeginBatch() *MetadataBatch {
	return NewBatch(ms)
}
