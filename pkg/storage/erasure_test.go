package storage

import (
	"bytes"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestErasureEncoder_Encode tests encoding data into shards
func TestErasureEncoder_Encode(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name          string
		dataShards    int
		parityShards  int
		dataSize      int
		expectError   bool
	}{
		{
			name:          "Valid 4+2 encoding",
			dataShards:    4,
			parityShards:  2,
			dataSize:      1024,
			expectError:   false,
		},
		{
			name:          "Valid 6+3 encoding",
			dataShards:    6,
			parityShards:  3,
			dataSize:      4096,
			expectError:   false,
		},
		{
			name:          "Small data",
			dataShards:    4,
			parityShards:  2,
			dataSize:      10,
			expectError:   false,
		},
		{
			name:          "Large data",
			dataShards:    4,
			parityShards:  2,
			dataSize:      1024 * 1024, // 1MB
			expectError:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ErasureConfig{
				Enabled:      true,
				DataShards:   tt.dataShards,
				ParityShards: tt.parityShards,
				MinNodeCount: tt.dataShards + tt.parityShards,
			}

			encoder, err := NewErasureEncoder(config, logger)
			if err != nil {
				t.Fatalf("Failed to create encoder: %v", err)
			}

			// Create test data
			data := make([]byte, tt.dataSize)
			for i := range data {
				data[i] = byte(i % 256)
			}

			// Encode
			shards, err := encoder.Encode(data)
			if (err != nil) != tt.expectError {
				t.Errorf("Encode() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectError {
				return
			}

			// Verify shard count
			expectedShards := tt.dataShards + tt.parityShards
			if len(shards) != expectedShards {
				t.Errorf("Expected %d shards, got %d", expectedShards, len(shards))
			}

			// Verify all shards have data
			for i, shard := range shards {
				if len(shard) == 0 {
					t.Errorf("Shard %d is empty", i)
				}
			}
		})
	}
}

// TestErasureEncoder_Decode tests decoding data from shards
func TestErasureEncoder_Decode(t *testing.T) {
	logger := zap.NewNop()

	config := ErasureConfig{
		Enabled:      true,
		DataShards:   4,
		ParityShards: 2,
		MinNodeCount: 6,
	}

	encoder, err := NewErasureEncoder(config, logger)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	// Create test data
	originalData := []byte("This is test data for erasure coding verification.")
	originalSize := len(originalData)

	// Encode
	shards, err := encoder.Encode(originalData)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	tests := []struct {
		name           string
		removeShards   []int // Indices of shards to remove
		expectError    bool
		expectRecovery bool
	}{
		{
			name:           "All shards available",
			removeShards:   []int{},
			expectError:    false,
			expectRecovery: true,
		},
		{
			name:           "One parity shard missing",
			removeShards:   []int{4},
			expectError:    false,
			expectRecovery: true,
		},
		{
			name:           "Two parity shards missing (CLD-REQ-053: m=2 tolerance)",
			removeShards:   []int{4, 5},
			expectError:    false,
			expectRecovery: true,
		},
		{
			name:           "One data shard missing",
			removeShards:   []int{0},
			expectError:    false,
			expectRecovery: true,
		},
		{
			name:           "Mix of data and parity shards missing",
			removeShards:   []int{1, 5},
			expectError:    false,
			expectRecovery: true,
		},
		{
			name:           "Too many shards missing (should fail)",
			removeShards:   []int{0, 1, 2},
			expectError:    true,
			expectRecovery: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Make a copy of shards
			testShards := make([][]byte, len(shards))
			for i, shard := range shards {
				testShards[i] = make([]byte, len(shard))
				copy(testShards[i], shard)
			}

			// Remove specified shards
			for _, idx := range tt.removeShards {
				testShards[idx] = nil
			}

			// Decode
			decoded, err := encoder.Decode(testShards, originalSize)
			if (err != nil) != tt.expectError {
				t.Errorf("Decode() error = %v, expectError %v", err, tt.expectError)
				return
			}

			if tt.expectRecovery {
				if !bytes.Equal(decoded, originalData) {
					t.Errorf("Decoded data does not match original.\nOriginal: %s\nDecoded:  %s",
						string(originalData), string(decoded))
				}
			}
		})
	}
}

// TestErasureEncoder_Verify tests shard verification
func TestErasureEncoder_Verify(t *testing.T) {
	logger := zap.NewNop()

	config := ErasureConfig{
		Enabled:      true,
		DataShards:   4,
		ParityShards: 2,
		MinNodeCount: 6,
	}

	encoder, err := NewErasureEncoder(config, logger)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	// Create test data
	data := []byte("Test data for verification")

	// Encode
	shards, err := encoder.Encode(data)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	t.Run("Valid shards", func(t *testing.T) {
		valid, err := encoder.Verify(shards)
		if err != nil {
			t.Errorf("Verify() error = %v", err)
		}
		if !valid {
			t.Error("Valid shards marked as invalid")
		}
	})

	t.Run("Corrupted shard", func(t *testing.T) {
		// Make a copy and corrupt one shard
		corruptedShards := make([][]byte, len(shards))
		for i, shard := range shards {
			corruptedShards[i] = make([]byte, len(shard))
			copy(corruptedShards[i], shard)
		}
		corruptedShards[0][0] ^= 0xFF // Flip bits

		valid, err := encoder.Verify(corruptedShards)
		if err != nil {
			t.Errorf("Verify() error = %v", err)
		}
		if valid {
			t.Error("Corrupted shards marked as valid")
		}
	})
}

// TestErasureEncoder_ReconstructShard tests single shard reconstruction
func TestErasureEncoder_ReconstructShard(t *testing.T) {
	logger := zap.NewNop()

	config := ErasureConfig{
		Enabled:      true,
		DataShards:   4,
		ParityShards: 2,
		MinNodeCount: 6,
	}

	encoder, err := NewErasureEncoder(config, logger)
	if err != nil {
		t.Fatalf("Failed to create encoder: %v", err)
	}

	// Create test data
	data := []byte("Test data for single shard reconstruction")

	// Encode
	shards, err := encoder.Encode(data)
	if err != nil {
		t.Fatalf("Failed to encode: %v", err)
	}

	// Save original shard
	originalShard := make([]byte, len(shards[0]))
	copy(originalShard, shards[0])

	// Remove one shard
	shards[0] = nil

	// Reconstruct
	err = encoder.ReconstructShard(shards, 0)
	if err != nil {
		t.Fatalf("Failed to reconstruct shard: %v", err)
	}

	// Verify reconstructed shard matches original
	if !bytes.Equal(shards[0], originalShard) {
		t.Error("Reconstructed shard does not match original")
	}
}

// TestIsErasureCodingEligible tests EC eligibility checking
// CLD-REQ-053: Node count threshold of 6
func TestIsErasureCodingEligible(t *testing.T) {
	tests := []struct {
		name            string
		config          ErasureConfig
		activeNodeCount int
		expected        bool
	}{
		{
			name: "EC enabled with sufficient nodes",
			config: ErasureConfig{
				Enabled:      true,
				DataShards:   4,
				ParityShards: 2,
				MinNodeCount: 6,
			},
			activeNodeCount: 6,
			expected:        true,
		},
		{
			name: "EC enabled with more nodes than needed",
			config: ErasureConfig{
				Enabled:      true,
				DataShards:   4,
				ParityShards: 2,
				MinNodeCount: 6,
			},
			activeNodeCount: 10,
			expected:        true,
		},
		{
			name: "EC enabled but insufficient nodes (CLD-REQ-053: needs ≥6)",
			config: ErasureConfig{
				Enabled:      true,
				DataShards:   4,
				ParityShards: 2,
				MinNodeCount: 6,
			},
			activeNodeCount: 5,
			expected:        false,
		},
		{
			name: "EC disabled",
			config: ErasureConfig{
				Enabled:      false,
				DataShards:   4,
				ParityShards: 2,
				MinNodeCount: 6,
			},
			activeNodeCount: 10,
			expected:        false,
		},
		{
			name: "Nodes less than total shards",
			config: ErasureConfig{
				Enabled:      true,
				DataShards:   4,
				ParityShards: 2,
				MinNodeCount: 6,
			},
			activeNodeCount: 5,
			expected:        false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsErasureCodingEligible(tt.config, tt.activeNodeCount)
			if result != tt.expected {
				t.Errorf("IsErasureCodingEligible() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// TestIsObjectCold tests cold data classification
// CLD-REQ-053: Cold data detection
func TestIsObjectCold(t *testing.T) {
	now := time.Now()
	threshold := 7 * 24 * time.Hour // 7 days

	tests := []struct {
		name     string
		object   *Object
		expected bool
	}{
		{
			name: "Already erasure coded",
			object: &Object{
				ECEnabled: true,
			},
			expected: true,
		},
		{
			name: "Cold storage class",
			object: &Object{
				StorageClass: StorageClassCold,
				ECEnabled:    false,
			},
			expected: true,
		},
		{
			name: "Not accessed for 8 days",
			object: &Object{
				StorageClass: StorageClassHot,
				CreatedAt:    now.Add(-8 * 24 * time.Hour),
				AccessedAt:   now.Add(-8 * 24 * time.Hour),
				ECEnabled:    false,
			},
			expected: true,
		},
		{
			name: "Recently accessed",
			object: &Object{
				StorageClass: StorageClassHot,
				CreatedAt:    now.Add(-1 * time.Hour),
				AccessedAt:   now.Add(-1 * time.Hour),
				ECEnabled:    false,
			},
			expected: false,
		},
		{
			name: "AccessedAt not set, use CreatedAt",
			object: &Object{
				StorageClass: StorageClassHot,
				CreatedAt:    now.Add(-10 * 24 * time.Hour),
				AccessedAt:   time.Time{}, // Zero time
				ECEnabled:    false,
			},
			expected: true,
		},
		{
			name:     "Nil object",
			object:   nil,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := IsObjectCold(tt.object, threshold)
			if result != tt.expected {
				t.Errorf("IsObjectCold() = %v, expected %v", result, tt.expected)
			}
		})
	}
}

// TestErasureEncoder_EdgeCases tests edge cases
func TestErasureEncoder_EdgeCases(t *testing.T) {
	logger := zap.NewNop()

	t.Run("Empty data", func(t *testing.T) {
		config := DefaultErasureConfig()
		encoder, err := NewErasureEncoder(config, logger)
		if err != nil {
			t.Fatalf("Failed to create encoder: %v", err)
		}

		_, err = encoder.Encode([]byte{})
		if err == nil {
			t.Error("Expected error for empty data")
		}
	})

	t.Run("Invalid configuration - too few data shards", func(t *testing.T) {
		config := ErasureConfig{
			Enabled:      true,
			DataShards:   1, // Too few
			ParityShards: 2,
			MinNodeCount: 3,
		}

		_, err := NewErasureEncoder(config, logger)
		if err == nil {
			t.Error("Expected error for invalid configuration")
		}
	})

	t.Run("Invalid configuration - no parity shards", func(t *testing.T) {
		config := ErasureConfig{
			Enabled:      true,
			DataShards:   4,
			ParityShards: 0, // Invalid
			MinNodeCount: 4,
		}

		_, err := NewErasureEncoder(config, logger)
		if err == nil {
			t.Error("Expected error for invalid configuration")
		}
	})
}

// TestErasureEncoder_MaxToleratedFailures tests failure tolerance calculation
func TestErasureEncoder_MaxToleratedFailures(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name          string
		dataShards    int
		parityShards  int
		expectedMax   int
	}{
		{
			name:          "4+2 configuration",
			dataShards:    4,
			parityShards:  2,
			expectedMax:   2,
		},
		{
			name:          "6+3 configuration",
			dataShards:    6,
			parityShards:  3,
			expectedMax:   3,
		},
		{
			name:          "8+4 configuration",
			dataShards:    8,
			parityShards:  4,
			expectedMax:   4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := ErasureConfig{
				Enabled:      true,
				DataShards:   tt.dataShards,
				ParityShards: tt.parityShards,
				MinNodeCount: tt.dataShards + tt.parityShards,
			}

			encoder, err := NewErasureEncoder(config, logger)
			if err != nil {
				t.Fatalf("Failed to create encoder: %v", err)
			}

			maxFailures := encoder.MaxToleratedFailures()
			if maxFailures != tt.expectedMax {
				t.Errorf("MaxToleratedFailures() = %d, expected %d", maxFailures, tt.expectedMax)
			}
		})
	}
}
