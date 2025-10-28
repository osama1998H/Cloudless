package storage

import (
	"fmt"
	"time"

	"github.com/klauspost/reedsolomon"
	"go.uber.org/zap"
)

// ErasureEncoder handles erasure coding operations for cold data storage
// Implements CLD-REQ-053: Optional erasure coding for cold data when node count ≥ 6
type ErasureEncoder struct {
	config  ErasureConfig
	encoder reedsolomon.Encoder
	logger  *zap.Logger
}

// NewErasureEncoder creates a new erasure encoder
// CLD-REQ-053: Requires k+m >= 6 for minimum cluster size
func NewErasureEncoder(config ErasureConfig, logger *zap.Logger) (*ErasureEncoder, error) {
	// Validate configuration
	if config.DataShards < 2 {
		return nil, fmt.Errorf("data shards must be >= 2, got %d", config.DataShards)
	}

	if config.ParityShards < 1 {
		return nil, fmt.Errorf("parity shards must be >= 1, got %d", config.ParityShards)
	}

	totalShards := config.DataShards + config.ParityShards
	if totalShards < config.MinNodeCount {
		return nil, fmt.Errorf("total shards (%d) must be >= min node count (%d)",
			totalShards, config.MinNodeCount)
	}

	// Create Reed-Solomon encoder
	encoder, err := reedsolomon.New(config.DataShards, config.ParityShards)
	if err != nil {
		return nil, fmt.Errorf("failed to create Reed-Solomon encoder: %w", err)
	}

	ee := &ErasureEncoder{
		config:  config,
		encoder: encoder,
		logger:  logger,
	}

	logger.Info("Erasure encoder created",
		zap.Int("data_shards", config.DataShards),
		zap.Int("parity_shards", config.ParityShards),
		zap.Int("total_shards", totalShards),
		zap.Int("min_node_count", config.MinNodeCount),
	)

	return ee, nil
}

// Encode splits data into k data shards + m parity shards
// Returns a slice of shards where:
// - shards[0:k] are data shards
// - shards[k:k+m] are parity shards
// CLD-REQ-053: Enables storage efficiency for cold data
func (ee *ErasureEncoder) Encode(data []byte) ([][]byte, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("cannot encode empty data")
	}

	// Calculate shard size (must be equal for all shards)
	shardSize := (len(data) + ee.config.DataShards - 1) / ee.config.DataShards

	// Create shards slice (k data + m parity)
	totalShards := ee.config.DataShards + ee.config.ParityShards
	shards := make([][]byte, totalShards)

	// Initialize data shards
	for i := 0; i < ee.config.DataShards; i++ {
		shards[i] = make([]byte, shardSize)
	}

	// Copy data into shards
	for i := 0; i < len(data); i++ {
		shardIndex := i / shardSize
		offsetInShard := i % shardSize
		shards[shardIndex][offsetInShard] = data[i]
	}

	// Initialize parity shards
	for i := ee.config.DataShards; i < totalShards; i++ {
		shards[i] = make([]byte, shardSize)
	}

	// Encode to create parity shards
	if err := ee.encoder.Encode(shards); err != nil {
		return nil, fmt.Errorf("failed to encode shards: %w", err)
	}

	ee.logger.Debug("Data encoded into shards",
		zap.Int("original_size", len(data)),
		zap.Int("shard_size", shardSize),
		zap.Int("data_shards", ee.config.DataShards),
		zap.Int("parity_shards", ee.config.ParityShards),
	)

	return shards, nil
}

// Decode reconstructs original data from available shards
// Requires at least k shards (any combination of data and parity shards)
// CLD-REQ-053: Enables degraded reads when up to m nodes are unavailable
func (ee *ErasureEncoder) Decode(shards [][]byte, originalSize int) ([]byte, error) {
	if len(shards) != ee.config.DataShards+ee.config.ParityShards {
		return nil, fmt.Errorf("expected %d shards, got %d",
			ee.config.DataShards+ee.config.ParityShards, len(shards))
	}

	// Count available shards
	availableCount := 0
	for _, shard := range shards {
		if shard != nil {
			availableCount++
		}
	}

	if availableCount < ee.config.DataShards {
		return nil, fmt.Errorf("insufficient shards for reconstruction: have %d, need %d",
			availableCount, ee.config.DataShards)
	}

	// Reconstruct missing shards
	if err := ee.encoder.Reconstruct(shards); err != nil {
		return nil, fmt.Errorf("failed to reconstruct shards: %w", err)
	}

	// Assemble data from data shards
	var result []byte
	for i := 0; i < ee.config.DataShards; i++ {
		if shards[i] == nil {
			return nil, fmt.Errorf("data shard %d is still nil after reconstruction", i)
		}
		result = append(result, shards[i]...)
	}

	// Trim to original size
	if originalSize > 0 && originalSize <= len(result) {
		result = result[:originalSize]
	}

	ee.logger.Debug("Data decoded from shards",
		zap.Int("reconstructed_size", len(result)),
		zap.Int("original_size", originalSize),
		zap.Int("available_shards", availableCount),
	)

	return result, nil
}

// Verify checks the integrity of shards
// Returns true if all shards are valid and consistent
func (ee *ErasureEncoder) Verify(shards [][]byte) (bool, error) {
	if len(shards) != ee.config.DataShards+ee.config.ParityShards {
		return false, fmt.Errorf("expected %d shards, got %d",
			ee.config.DataShards+ee.config.ParityShards, len(shards))
	}

	// Count available shards
	availableCount := 0
	for _, shard := range shards {
		if shard != nil {
			availableCount++
		}
	}

	// Need all shards for full verification
	if availableCount != len(shards) {
		return false, fmt.Errorf("all shards must be present for verification, have %d/%d",
			availableCount, len(shards))
	}

	// Verify shards using Reed-Solomon
	valid, err := ee.encoder.Verify(shards)
	if err != nil {
		return false, fmt.Errorf("verification failed: %w", err)
	}

	ee.logger.Debug("Shard verification completed",
		zap.Bool("valid", valid),
		zap.Int("shard_count", len(shards)),
	)

	return valid, nil
}

// ReconstructShard reconstructs a single missing shard
// Useful for proactive repair when one shard is corrupted or missing
func (ee *ErasureEncoder) ReconstructShard(shards [][]byte, shardIndex int) error {
	if shardIndex < 0 || shardIndex >= len(shards) {
		return fmt.Errorf("shard index %d out of range [0, %d)", shardIndex, len(shards))
	}

	// Count available shards
	availableCount := 0
	for _, shard := range shards {
		if shard != nil {
			availableCount++
		}
	}

	if availableCount < ee.config.DataShards {
		return fmt.Errorf("insufficient shards for reconstruction: have %d, need %d",
			availableCount, ee.config.DataShards)
	}

	// Reconstruct using Reed-Solomon
	if err := ee.encoder.Reconstruct(shards); err != nil {
		return fmt.Errorf("failed to reconstruct shard %d: %w", shardIndex, err)
	}

	ee.logger.Debug("Reconstructed single shard",
		zap.Int("shard_index", shardIndex),
	)

	return nil
}

// GetShardSize calculates the size of each shard for a given data size
func (ee *ErasureEncoder) GetShardSize(dataSize int) int {
	return (dataSize + ee.config.DataShards - 1) / ee.config.DataShards
}

// GetTotalShardCount returns the total number of shards (k+m)
func (ee *ErasureEncoder) GetTotalShardCount() int {
	return ee.config.DataShards + ee.config.ParityShards
}

// GetDataShardCount returns the number of data shards (k)
func (ee *ErasureEncoder) GetDataShardCount() int {
	return ee.config.DataShards
}

// GetParityShardCount returns the number of parity shards (m)
func (ee *ErasureEncoder) GetParityShardCount() int {
	return ee.config.ParityShards
}

// CanReconstruct returns true if the given number of available shards is sufficient
func (ee *ErasureEncoder) CanReconstruct(availableShards int) bool {
	return availableShards >= ee.config.DataShards
}

// MaxToleratedFailures returns the maximum number of shard failures that can be tolerated
// CLD-REQ-053: With k=4, m=2, can tolerate up to 2 node failures
func (ee *ErasureEncoder) MaxToleratedFailures() int {
	return ee.config.ParityShards
}

// IsErasureCodingEligible checks if erasure coding should be enabled
// CLD-REQ-053: Requires node count >= MinNodeCount (default 6) and EC enabled
func IsErasureCodingEligible(config ErasureConfig, activeNodeCount int) bool {
	if !config.Enabled {
		return false
	}

	if activeNodeCount < config.MinNodeCount {
		return false
	}

	// Verify we have enough nodes for the configured shards
	totalShards := config.DataShards + config.ParityShards
	if activeNodeCount < totalShards {
		return false
	}

	return true
}

// IsObjectCold determines if an object qualifies as "cold" data
// CLD-REQ-053: Cold data is eligible for erasure coding
func IsObjectCold(object *Object, coldThreshold time.Duration) bool {
	if object == nil {
		return false
	}

	// Already erasure coded
	if object.ECEnabled {
		return true
	}

	// Check if object is in cold storage class
	if object.StorageClass == StorageClassCold {
		return true
	}

	// Check if object hasn't been accessed within threshold
	if object.AccessedAt.IsZero() {
		// Use CreatedAt if AccessedAt is not set
		return time.Since(object.CreatedAt) > coldThreshold
	}

	return time.Since(object.AccessedAt) > coldThreshold
}
