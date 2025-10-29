//go:build benchmark

package storage

import (
	"crypto/rand"
	"os"
	"testing"

	"go.uber.org/zap"
)

// BenchmarkChunkWrite benchmarks chunk write operations
func BenchmarkChunkWrite(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-chunks-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()
	config := StorageConfig{DataDir: tmpDir}
	cs, err := NewChunkStore(config, logger)
	if err != nil {
		b.Fatalf("Failed to create chunk store: %v", err)
	}

	tests := []struct {
		name      string
		chunkSize int
	}{
		{"1KB", 1024},
		{"4KB", 4 * 1024},
		{"16KB", 16 * 1024},
		{"64KB", 64 * 1024},
		{"256KB", 256 * 1024},
		{"1MB", 1024 * 1024},
		{"4MB", 4 * 1024 * 1024},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			data := generateRandomData(tt.chunkSize)

			b.ResetTimer()
			b.SetBytes(int64(tt.chunkSize))

			for i := 0; i < b.N; i++ {
				_, err := cs.WriteChunk(data)
				if err != nil {
					b.Fatalf("Write failed: %v", err)
				}
			}

			throughputMBps := (float64(b.N) * float64(tt.chunkSize)) / b.Elapsed().Seconds() / (1024 * 1024)
			b.ReportMetric(throughputMBps, "MB/s")
		})
	}
}

// BenchmarkChunkRead benchmarks chunk read operations
func BenchmarkChunkRead(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-chunks-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()
	config := StorageConfig{DataDir: tmpDir}
	cs, err := NewChunkStore(config, logger)
	if err != nil {
		b.Fatalf("Failed to create chunk store: %v", err)
	}

	tests := []struct {
		name      string
		chunkSize int
	}{
		{"1KB", 1024},
		{"4KB", 4 * 1024},
		{"16KB", 16 * 1024},
		{"64KB", 64 * 1024},
		{"256KB", 256 * 1024},
		{"1MB", 1024 * 1024},
		{"4MB", 4 * 1024 * 1024},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			data := generateRandomData(tt.chunkSize)
			chunk, err := cs.WriteChunk(data)
			if err != nil {
				b.Fatalf("Setup write failed: %v", err)
			}

			b.ResetTimer()
			b.SetBytes(int64(tt.chunkSize))

			for i := 0; i < b.N; i++ {
				_, err := cs.ReadChunk(chunk.ID)
				if err != nil {
					b.Fatalf("Read failed: %v", err)
				}
			}

			throughputMBps := (float64(b.N) * float64(tt.chunkSize)) / b.Elapsed().Seconds() / (1024 * 1024)
			b.ReportMetric(throughputMBps, "MB/s")
		})
	}
}

// BenchmarkChunkDeduplication benchmarks deduplication performance
func BenchmarkChunkDeduplication(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-chunks-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()
	config := StorageConfig{DataDir: tmpDir}
	cs, err := NewChunkStore(config, logger)
	if err != nil {
		b.Fatalf("Failed to create chunk store: %v", err)
	}

	tests := []struct {
		name         string
		uniqueChunks int
		duplicates   int
	}{
		{"10_unique_90_duplicates", 10, 90},
		{"50_unique_50_duplicates", 50, 50},
		{"90_unique_10_duplicates", 90, 10},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			// Create unique chunks
			uniqueData := make([][]byte, tt.uniqueChunks)
			for i := 0; i < tt.uniqueChunks; i++ {
				uniqueData[i] = generateRandomData(64 * 1024)
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				// Write unique chunks
				for _, data := range uniqueData {
					_, err := cs.WriteChunk(data)
					if err != nil {
						b.Fatalf("Write failed: %v", err)
					}
				}

				// Write duplicates
				for j := 0; j < tt.duplicates; j++ {
					data := uniqueData[j%len(uniqueData)]
					_, err := cs.WriteChunk(data)
					if err != nil {
						b.Fatalf("Duplicate write failed: %v", err)
					}
				}
			}

			b.StopTimer()
			dedupRatio := float64(tt.uniqueChunks+tt.duplicates) / float64(tt.uniqueChunks)
			b.ReportMetric(dedupRatio, "dedup_ratio")
		})
	}
}

// BenchmarkChunkCompression benchmarks compression performance
func BenchmarkChunkCompression(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-chunks-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()
	config := StorageConfig{DataDir: tmpDir}
	cs, err := NewChunkStore(config, logger)
	if err != nil {
		b.Fatalf("Failed to create chunk store: %v", err)
	}

	tests := []struct {
		name          string
		dataType      string
		dataGenerator func(int) []byte
		chunkSize     int
	}{
		{"text_highly_compressible_1MB", "text", generateRepeatableData, 1024 * 1024},
		{"random_not_compressible_1MB", "random", generateRandomData, 1024 * 1024},
		{"text_highly_compressible_4MB", "text", generateRepeatableData, 4 * 1024 * 1024},
		{"random_not_compressible_4MB", "random", generateRandomData, 4 * 1024 * 1024},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			data := tt.dataGenerator(tt.chunkSize)

			b.ResetTimer()
			b.SetBytes(int64(tt.chunkSize))

			for i := 0; i < b.N; i++ {
				chunk, err := cs.WriteChunk(data)
				if err != nil {
					b.Fatalf("Write with compression failed: %v", err)
				}

				// Verify decompression
				_, err = cs.ReadChunk(chunk.ID)
				if err != nil {
					b.Fatalf("Read with decompression failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkChunkVerification benchmarks integrity verification
func BenchmarkChunkVerification(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-chunks-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()
	config := StorageConfig{DataDir: tmpDir}
	cs, err := NewChunkStore(config, logger)
	if err != nil {
		b.Fatalf("Failed to create chunk store: %v", err)
	}

	tests := []struct {
		name      string
		chunkSize int
	}{
		{"64KB", 64 * 1024},
		{"256KB", 256 * 1024},
		{"1MB", 1024 * 1024},
		{"4MB", 4 * 1024 * 1024},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			data := generateRandomData(tt.chunkSize)
			chunk, err := cs.WriteChunk(data)
			if err != nil {
				b.Fatalf("Setup write failed: %v", err)
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				err := cs.VerifyChunk(chunk.ID)
				if err != nil {
					b.Fatalf("Verification failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkStorageManager benchmarks the storage manager operations
// TODO(osama): Update benchmark to use current StorageManager API. See issue #22.
// This benchmark uses the old chunk-based API. Migration required:
// 1. Replace WriteChunk → PutObject with proper PutObjectRequest
// 2. Replace ReadChunk → GetObject with proper GetObjectRequest
// 3. Update benchmark metrics to reflect object-level operations
// Note: Build tag 'benchmark' excludes this from CI until fixed.
/* func BenchmarkStorageManagerWrite(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-manager-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()
	ctx := context.Background()

	config := StorageConfig{
		DataDir:           tmpDir,
		ReplicationFactor: 3,
		ChunkSize:         64 * 1024,
	}

	mgr, err := NewStorageManager(config, logger)
	if err != nil {
		b.Fatalf("Failed to create storage manager: %v", err)
	}

	tests := []struct {
		name     string
		dataSize int
	}{
		{"256KB", 256 * 1024},
		{"1MB", 1024 * 1024},
		{"4MB", 4 * 1024 * 1024},
		{"16MB", 16 * 1024 * 1024},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			data := bytes.NewReader(generateRandomData(tt.dataSize))

			b.ResetTimer()
			b.SetBytes(int64(tt.dataSize))

			for i := 0; i < b.N; i++ {
				data.Seek(0, 0) // Reset reader
				objectID := fmt.Sprintf("bench-object-%d", i)

				// TODO(osama): Update to use PutObject with PutObjectRequest. See issue #22.
				// Example:
				// req := &PutObjectRequest{
				//     Bucket: "benchmark",
				//     Key: objectID,
				//     Data: data,
				//     ContentType: "application/octet-stream",
				// }
				// err := mgr.PutObject(ctx, req)
				// if err != nil {
				//     b.Fatalf("Write failed: %v", err)
				// }
			}

			throughputMBps := (float64(b.N) * float64(tt.dataSize)) / b.Elapsed().Seconds() / (1024 * 1024)
			b.ReportMetric(throughputMBps, "MB/s")
		})
	}
} */

// BenchmarkStorageManagerRead benchmarks reading objects
// TODO(osama): Update benchmark to use GetObject with GetObjectRequest. See issue #22.
// Need to migrate from old chunk-based API to current object-level API.
/* func BenchmarkStorageManagerRead(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-manager-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()
	ctx := context.Background()

	config := StorageConfig{
		DataDir:           tmpDir,
		ReplicationFactor: 3,
		ChunkSize:         64 * 1024,
	}

	mgr, err := NewStorageManager(config, logger)
	if err != nil {
		b.Fatalf("Failed to create storage manager: %v", err)
	}

	tests := []struct {
		name     string
		dataSize int
	}{
		{"256KB", 256 * 1024},
		{"1MB", 1024 * 1024},
		{"4MB", 4 * 1024 * 1024},
		{"16MB", 16 * 1024 * 1024},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			// Setup: write object
			data := bytes.NewReader(generateRandomData(tt.dataSize))
			objectID := "bench-read-object"
			// TODO(osama): Update setup to use PutObject. See issue #22.
			// req := &PutObjectRequest{
			//     Bucket: "benchmark",
			//     Key: objectID,
			//     Data: data,
			//     ContentType: "application/octet-stream",
			// }
			// err := mgr.PutObject(ctx, req)
			// if err != nil {
			//     b.Fatalf("Setup write failed: %v", err)
			// }

			b.ResetTimer()
			b.SetBytes(int64(tt.dataSize))

			for i := 0; i < b.N; i++ {
				// TODO(osama): Update to use GetObject with GetObjectRequest. See issue #22.
				// req := &GetObjectRequest{
				//     Bucket: "benchmark",
				//     Key: objectID,
				// }
				// resp, err := mgr.GetObject(ctx, req)
				// if err != nil {
				//     b.Fatalf("Read failed: %v", err)
				// }
			}

			throughputMBps := (float64(b.N) * float64(tt.dataSize)) / b.Elapsed().Seconds() / (1024 * 1024)
			b.ReportMetric(throughputMBps, "MB/s")
		})
	}
} */

// BenchmarkConcurrentWrites benchmarks concurrent write operations
func BenchmarkConcurrentWrites(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-concurrent-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()
	config := StorageConfig{DataDir: tmpDir}
	cs, err := NewChunkStore(config, logger)
	if err != nil {
		b.Fatalf("Failed to create chunk store: %v", err)
	}

	data := generateRandomData(64 * 1024)

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := cs.WriteChunk(data)
			if err != nil {
				b.Errorf("Concurrent write failed: %v", err)
			}
		}
	})
}

// BenchmarkConcurrentReads benchmarks concurrent read operations
func BenchmarkConcurrentReads(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-concurrent-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()
	config := StorageConfig{DataDir: tmpDir}
	cs, err := NewChunkStore(config, logger)
	if err != nil {
		b.Fatalf("Failed to create chunk store: %v", err)
	}

	// Setup: write chunks
	data := generateRandomData(64 * 1024)
	chunk, err := cs.WriteChunk(data)
	if err != nil {
		b.Fatalf("Setup write failed: %v", err)
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := cs.ReadChunk(chunk.ID)
			if err != nil {
				b.Errorf("Concurrent read failed: %v", err)
			}
		}
	})
}

// BenchmarkGarbageCollection benchmarks garbage collection performance
func BenchmarkGarbageCollection(b *testing.B) {
	tmpDir, err := os.MkdirTemp("", "cloudless-bench-gc-*")
	if err != nil {
		b.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger, _ := zap.NewDevelopment()

	tests := []struct {
		name        string
		totalChunks int
		orphans     int
	}{
		{"100_chunks_10_orphans", 100, 10},
		{"1000_chunks_100_orphans", 1000, 100},
		{"10000_chunks_1000_orphans", 10000, 1000},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			config := StorageConfig{DataDir: tmpDir}
			cs, err := NewChunkStore(config, logger)
			if err != nil {
				b.Fatalf("Failed to create chunk store: %v", err)
			}

			// Setup: create chunks with some orphans
			for i := 0; i < tt.totalChunks; i++ {
				data := generateRandomData(4 * 1024)
				chunk, _ := cs.WriteChunk(data)

				if i < tt.orphans {
					// Make it an orphan by setting refcount to 0
					cs.DeleteChunk(chunk.ID)
				}
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, err := cs.GarbageCollect()
				if err != nil {
					b.Fatalf("GC failed: %v", err)
				}
			}
		})
	}
}

// Helper functions

func generateRandomData(size int) []byte {
	data := make([]byte, size)
	rand.Read(data)
	return data
}

func generateRepeatableData(size int) []byte {
	// Generate highly compressible data (repeated patterns)
	pattern := []byte("The quick brown fox jumps over the lazy dog. ")
	data := make([]byte, size)
	for i := 0; i < size; i++ {
		data[i] = pattern[i%len(pattern)]
	}
	return data
}
