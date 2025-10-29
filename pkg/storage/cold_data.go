package storage

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// ColdDataManager handles cold data classification and erasure coding
// CLD-REQ-053: Manages transition of cold data to erasure coded storage
type ColdDataManager struct {
	metadataStore  *MetadataStore
	contentEngine  *ContentEngine
	chunkStore     *ChunkStore
	erasureEncoder *ErasureEncoder
	config         StorageConfig
	logger         *zap.Logger

	// Node count provider for EC eligibility check
	nodeCountFunc func() int
}

// ColdDataManagerConfig contains configuration for the cold data manager
type ColdDataManagerConfig struct {
	MetadataStore *MetadataStore
	ContentEngine *ContentEngine
	ChunkStore    *ChunkStore
	Config        StorageConfig
	Logger        *zap.Logger
	// NodeCountFunc returns the current active node count for EC eligibility
	NodeCountFunc func() int
}

// NewColdDataManager creates a new cold data manager
// CLD-REQ-053: Implements cold data identification and erasure coding
func NewColdDataManager(cfg ColdDataManagerConfig) (*ColdDataManager, error) {
	if cfg.MetadataStore == nil {
		return nil, fmt.Errorf("metadata store is required")
	}
	if cfg.ContentEngine == nil {
		return nil, fmt.Errorf("content engine is required")
	}
	if cfg.ChunkStore == nil {
		return nil, fmt.Errorf("chunk store is required")
	}
	if cfg.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	// Create erasure encoder if EC is enabled
	var encoder *ErasureEncoder
	if cfg.Config.ErasureConfig.Enabled {
		var err error
		encoder, err = NewErasureEncoder(cfg.Config.ErasureConfig, cfg.Logger)
		if err != nil {
			return nil, fmt.Errorf("failed to create erasure encoder: %w", err)
		}
	}

	cdm := &ColdDataManager{
		metadataStore:  cfg.MetadataStore,
		contentEngine:  cfg.ContentEngine,
		chunkStore:     cfg.ChunkStore,
		erasureEncoder: encoder,
		config:         cfg.Config,
		logger:         cfg.Logger,
		nodeCountFunc:  cfg.NodeCountFunc,
	}

	return cdm, nil
}

// IdentifyColdObjects finds objects that qualify as cold data
// CLD-REQ-053: Cold data is eligible for erasure coding
func (cdm *ColdDataManager) IdentifyColdObjects(ctx context.Context, threshold time.Duration) ([]*Object, error) {
	// List all buckets
	buckets, err := cdm.metadataStore.ListBuckets()
	if err != nil {
		return nil, fmt.Errorf("failed to list buckets: %w", err)
	}

	var coldObjects []*Object

	// Check objects in each bucket
	for _, bucket := range buckets {
		objectMetadata, err := cdm.metadataStore.ListObjects(bucket.Name, "", 1000)
		if err != nil {
			cdm.logger.Error("Failed to list objects in bucket",
				zap.String("bucket", bucket.Name),
				zap.Error(err),
			)
			continue
		}

		// Get full object details for each
		for _, objMeta := range objectMetadata {
			obj, err := cdm.metadataStore.GetObject(bucket.Name, objMeta.Key)
			if err != nil {
				cdm.logger.Warn("Failed to get object details",
					zap.String("bucket", bucket.Name),
					zap.String("key", objMeta.Key),
					zap.Error(err),
				)
				continue
			}

			// Skip if already erasure coded
			if obj.ECEnabled {
				continue
			}

			// Check if object qualifies as cold
			if IsObjectCold(obj, threshold) {
				coldObjects = append(coldObjects, obj)
			}
		}
	}

	cdm.logger.Info("Identified cold objects",
		zap.Int("count", len(coldObjects)),
		zap.Duration("threshold", threshold),
	)

	return coldObjects, nil
}

// TransitionToCold changes an object's storage class to cold
// CLD-REQ-053: Prepares object for erasure coding
func (cdm *ColdDataManager) TransitionToCold(ctx context.Context, bucket, key string) error {
	// Get object
	object, err := cdm.metadataStore.GetObject(bucket, key)
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}

	// Already cold
	if object.StorageClass == StorageClassCold {
		return nil
	}

	// Update storage class
	object.StorageClass = StorageClassCold
	object.UpdatedAt = time.Now()

	// Save updated metadata
	if err := cdm.metadataStore.PutObject(object); err != nil {
		return fmt.Errorf("failed to update object metadata: %w", err)
	}

	cdm.logger.Info("Transitioned object to cold storage",
		zap.String("bucket", bucket),
		zap.String("key", key),
	)

	return nil
}

// EncodeWithEC encodes an object using erasure coding
// CLD-REQ-053: Converts replicated chunks to erasure coded shards
func (cdm *ColdDataManager) EncodeWithEC(ctx context.Context, bucket, key string) error {
	// Check EC eligibility
	if cdm.erasureEncoder == nil {
		return fmt.Errorf("erasure coding is not enabled")
	}

	// Check node count threshold
	if cdm.nodeCountFunc != nil {
		nodeCount := cdm.nodeCountFunc()
		if !IsErasureCodingEligible(cdm.config.ErasureConfig, nodeCount) {
			return fmt.Errorf("cluster does not meet erasure coding requirements: need %d nodes, have %d",
				cdm.config.ErasureConfig.MinNodeCount, nodeCount)
		}
	}

	// Get object
	object, err := cdm.metadataStore.GetObject(bucket, key)
	if err != nil {
		return fmt.Errorf("failed to get object: %w", err)
	}

	// Already erasure coded
	if object.ECEnabled {
		return nil
	}

	// Retrieve original data from chunks
	originalData, err := cdm.contentEngine.RetrieveContent(object.ChunkIDs)
	if err != nil {
		return fmt.Errorf("failed to retrieve object content: %w", err)
	}

	// Encode data into shards
	shards, err := cdm.erasureEncoder.Encode(originalData)
	if err != nil {
		return fmt.Errorf("failed to encode data: %w", err)
	}

	// Store each shard as a chunk
	var newChunkIDs []string
	var shardIndices []int

	for i, shard := range shards {
		chunk, err := cdm.chunkStore.WriteChunk(shard)
		if err != nil {
			// Cleanup on failure
			cdm.cleanupShards(ctx, newChunkIDs)
			return fmt.Errorf("failed to write shard %d: %w", i, err)
		}

		newChunkIDs = append(newChunkIDs, chunk.ID)
		shardIndices = append(shardIndices, i)
	}

	// Update object metadata
	object.ECEnabled = true
	object.DataShards = cdm.config.ErasureConfig.DataShards
	object.ParityShards = cdm.config.ErasureConfig.ParityShards
	object.ChunkIDs = newChunkIDs
	object.ShardIndices = shardIndices
	object.UpdatedAt = time.Now()

	// Save updated metadata
	if err := cdm.metadataStore.PutObject(object); err != nil {
		// Cleanup on failure
		cdm.cleanupShards(ctx, newChunkIDs)
		return fmt.Errorf("failed to update object metadata: %w", err)
	}

	// TODO(osama): Implement garbage collection for old replicated chunks. See issue #19.
	// After erasure coding an object, the original replicated chunks should be deleted
	// to reclaim storage. This is Phase 4 of the cold data implementation (CLD-REQ-041).
	// For now, keep them for safety during testing.

	cdm.logger.Info("Encoded object with erasure coding",
		zap.String("bucket", bucket),
		zap.String("key", key),
		zap.Int("data_shards", object.DataShards),
		zap.Int("parity_shards", object.ParityShards),
		zap.Int("total_shards", len(newChunkIDs)),
	)

	return nil
}

// DecodeFromEC reconstructs an object from erasure coded shards
// CLD-REQ-053: Enables reads of erasure coded objects
func (cdm *ColdDataManager) DecodeFromEC(ctx context.Context, bucket, key string) ([]byte, error) {
	if cdm.erasureEncoder == nil {
		return nil, fmt.Errorf("erasure coding is not enabled")
	}

	// Get object
	object, err := cdm.metadataStore.GetObject(bucket, key)
	if err != nil {
		return nil, fmt.Errorf("failed to get object: %w", err)
	}

	if !object.ECEnabled {
		return nil, fmt.Errorf("object is not erasure coded")
	}

	// Read all shards
	shards := make([][]byte, len(object.ChunkIDs))
	availableCount := 0

	for i, chunkID := range object.ChunkIDs {
		data, err := cdm.chunkStore.ReadChunk(chunkID)
		if err != nil {
			cdm.logger.Warn("Failed to read shard, will reconstruct",
				zap.String("chunk_id", chunkID),
				zap.Int("shard_index", i),
				zap.Error(err),
			)
			shards[i] = nil // Mark as missing
			continue
		}

		shards[i] = data
		availableCount++
	}

	// Check if we have enough shards to reconstruct
	if !cdm.erasureEncoder.CanReconstruct(availableCount) {
		return nil, fmt.Errorf("insufficient shards for reconstruction: have %d, need %d",
			availableCount, cdm.erasureEncoder.GetDataShardCount())
	}

	// Decode data from shards
	data, err := cdm.erasureEncoder.Decode(shards, int(object.Size))
	if err != nil {
		return nil, fmt.Errorf("failed to decode shards: %w", err)
	}

	cdm.logger.Debug("Decoded object from erasure coded shards",
		zap.String("bucket", bucket),
		zap.String("key", key),
		zap.Int("available_shards", availableCount),
		zap.Int("total_shards", len(shards)),
	)

	return data, nil
}

// cleanupShards removes shard chunks (for rollback on error)
func (cdm *ColdDataManager) cleanupShards(ctx context.Context, chunkIDs []string) {
	for _, chunkID := range chunkIDs {
		if err := cdm.chunkStore.DeleteChunk(chunkID); err != nil {
			cdm.logger.Error("Failed to cleanup shard",
				zap.String("chunk_id", chunkID),
				zap.Error(err),
			)
		}
	}
}

// ScheduleColdDataTransition runs periodic cold data identification and encoding
// CLD-REQ-053: Automated cold data management
func (cdm *ColdDataManager) ScheduleColdDataTransition(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := cdm.ProcessColdData(ctx); err != nil {
				cdm.logger.Error("Failed to process cold data", zap.Error(err))
			}
		}
	}
}

// ProcessColdData identifies and encodes cold objects
func (cdm *ColdDataManager) ProcessColdData(ctx context.Context) error {
	// Check if EC is enabled
	if !cdm.config.ErasureConfig.Enabled {
		return nil
	}

	// Check node count
	if cdm.nodeCountFunc != nil {
		nodeCount := cdm.nodeCountFunc()
		if !IsErasureCodingEligible(cdm.config.ErasureConfig, nodeCount) {
			cdm.logger.Debug("Skipping cold data processing, insufficient nodes",
				zap.Int("node_count", nodeCount),
				zap.Int("required", cdm.config.ErasureConfig.MinNodeCount),
			)
			return nil
		}
	}

	// Identify cold objects
	coldObjects, err := cdm.IdentifyColdObjects(ctx, cdm.config.ErasureConfig.ColdThreshold)
	if err != nil {
		return fmt.Errorf("failed to identify cold objects: %w", err)
	}

	// Encode each cold object
	encoded := 0
	for _, obj := range coldObjects {
		if err := cdm.EncodeWithEC(ctx, obj.Bucket, obj.Key); err != nil {
			cdm.logger.Error("Failed to encode cold object",
				zap.String("bucket", obj.Bucket),
				zap.String("key", obj.Key),
				zap.Error(err),
			)
			continue
		}
		encoded++
	}

	cdm.logger.Info("Processed cold data",
		zap.Int("identified", len(coldObjects)),
		zap.Int("encoded", encoded),
	)

	return nil
}
