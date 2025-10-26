package storage

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"
)

func TestChunkStore_WriteChunk(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "cloudless-chunk-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create chunk store
	logger := zap.NewNop()
	config := StorageConfig{
		DataDir:           tempDir,
		EnableCompression: false,
	}

	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	// Test data
	testData := []byte("Hello, Cloudless!")

	// Write chunk
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	// Verify chunk metadata
	if chunk.ID == "" {
		t.Error("Chunk ID should not be empty")
	}
	if chunk.Size != int64(len(testData)) {
		t.Errorf("Expected size %d, got %d", len(testData), chunk.Size)
	}
	if chunk.RefCount != 1 {
		t.Errorf("Expected refcount 1, got %d", chunk.RefCount)
	}
	if chunk.Compressed {
		t.Error("Chunk should not be compressed when compression is disabled")
	}

	// Verify chunk exists on disk
	chunkPath := cs.getChunkPath(chunk.ID)
	if _, err := os.Stat(chunkPath); os.IsNotExist(err) {
		t.Error("Chunk file should exist on disk")
	}
}

func TestChunkStore_ReadChunk(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "cloudless-chunk-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create chunk store
	logger := zap.NewNop()
	config := StorageConfig{
		DataDir:           tempDir,
		EnableCompression: false,
	}

	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	// Test data
	testData := []byte("Hello, Cloudless!")

	// Write chunk
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	// Read chunk
	readData, err := cs.ReadChunk(chunk.ID)
	if err != nil {
		t.Fatalf("Failed to read chunk: %v", err)
	}

	// Verify data matches
	if !bytes.Equal(testData, readData) {
		t.Errorf("Expected %s, got %s", testData, readData)
	}
}

func TestChunkStore_Deduplication(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "cloudless-chunk-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create chunk store
	logger := zap.NewNop()
	config := StorageConfig{
		DataDir:           tempDir,
		EnableCompression: false,
	}

	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	// Test data (same content)
	testData := []byte("Duplicate data")

	// Write chunk twice
	chunk1, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write first chunk: %v", err)
	}

	chunk2, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write second chunk: %v", err)
	}

	// Verify deduplication
	if chunk1.ID != chunk2.ID {
		t.Error("Chunks with same content should have same ID")
	}

	// Verify refcount increased
	storedChunk, exists := cs.GetChunk(chunk1.ID)
	if !exists {
		t.Fatal("Chunk should exist")
	}
	if storedChunk.RefCount != 2 {
		t.Errorf("Expected refcount 2, got %d", storedChunk.RefCount)
	}
}

func TestChunkStore_DeleteChunk(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "cloudless-chunk-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create chunk store
	logger := zap.NewNop()
	config := StorageConfig{
		DataDir:           tempDir,
		EnableCompression: false,
	}

	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	// Test data
	testData := []byte("To be deleted")

	// Write chunk
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	// Delete chunk
	err = cs.DeleteChunk(chunk.ID)
	if err != nil {
		t.Fatalf("Failed to delete chunk: %v", err)
	}

	// Verify chunk is deleted
	_, exists := cs.GetChunk(chunk.ID)
	if exists {
		t.Error("Chunk should not exist after deletion")
	}

	// Verify chunk file is deleted
	chunkPath := cs.getChunkPath(chunk.ID)
	if _, err := os.Stat(chunkPath); !os.IsNotExist(err) {
		t.Error("Chunk file should not exist after deletion")
	}
}

func TestChunkStore_RefCountManagement(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "cloudless-chunk-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create chunk store
	logger := zap.NewNop()
	config := StorageConfig{
		DataDir:           tempDir,
		EnableCompression: false,
	}

	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	// Test data
	testData := []byte("Reference counting test")

	// Write chunk three times (refcount = 3)
	chunk1, _ := cs.WriteChunk(testData)
	cs.WriteChunk(testData)
	cs.WriteChunk(testData)

	// Verify refcount
	storedChunk, _ := cs.GetChunk(chunk1.ID)
	if storedChunk.RefCount != 3 {
		t.Errorf("Expected refcount 3, got %d", storedChunk.RefCount)
	}

	// Delete once (refcount = 2)
	cs.DeleteChunk(chunk1.ID)
	storedChunk, exists := cs.GetChunk(chunk1.ID)
	if !exists {
		t.Fatal("Chunk should still exist with refcount > 0")
	}
	if storedChunk.RefCount != 2 {
		t.Errorf("Expected refcount 2, got %d", storedChunk.RefCount)
	}

	// Delete twice more
	cs.DeleteChunk(chunk1.ID)
	cs.DeleteChunk(chunk1.ID)

	// Verify chunk is deleted
	_, exists = cs.GetChunk(chunk1.ID)
	if exists {
		t.Error("Chunk should be deleted when refcount reaches 0")
	}
}

func TestChunkStore_Compression(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "cloudless-chunk-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create chunk store with compression enabled
	logger := zap.NewNop()
	config := StorageConfig{
		DataDir:           tempDir,
		EnableCompression: true,
	}

	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	// Test data (highly compressible)
	testData := bytes.Repeat([]byte("A"), 10000)

	// Write chunk
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	// Verify compression was applied
	if !chunk.Compressed {
		t.Error("Chunk should be compressed")
	}

	// Read and verify data
	readData, err := cs.ReadChunk(chunk.ID)
	if err != nil {
		t.Fatalf("Failed to read chunk: %v", err)
	}

	if !bytes.Equal(testData, readData) {
		t.Error("Decompressed data should match original")
	}
}

func TestChunkStore_VerifyChunk(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "cloudless-chunk-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create chunk store
	logger := zap.NewNop()
	config := StorageConfig{
		DataDir:           tempDir,
		EnableCompression: false,
	}

	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	// Test data
	testData := []byte("Verify me!")

	// Write chunk
	chunk, err := cs.WriteChunk(testData)
	if err != nil {
		t.Fatalf("Failed to write chunk: %v", err)
	}

	// Verify chunk integrity
	err = cs.VerifyChunk(chunk.ID)
	if err != nil {
		t.Errorf("Chunk verification should succeed: %v", err)
	}

	// Corrupt chunk
	chunkPath := cs.getChunkPath(chunk.ID)
	os.WriteFile(chunkPath, []byte("corrupted"), 0644)

	// Verify should fail
	err = cs.VerifyChunk(chunk.ID)
	if err == nil {
		t.Error("Chunk verification should fail for corrupted chunk")
	}
}

func TestChunkStore_GarbageCollect(t *testing.T) {
	// Create temp directory for testing
	tempDir, err := os.MkdirTemp("", "cloudless-chunk-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	// Create chunk store
	logger := zap.NewNop()
	config := StorageConfig{
		DataDir:           tempDir,
		EnableCompression: false,
	}

	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	// Create chunks with zero refcount
	chunk1, _ := cs.WriteChunk([]byte("orphan1"))
	chunk2, _ := cs.WriteChunk([]byte("orphan2"))

	// Manually set refcount to 0
	chunk1.RefCount = 0
	chunk2.RefCount = 0
	cs.chunks.Store(chunk1.ID, chunk1)
	cs.chunks.Store(chunk2.ID, chunk2)

	// Run garbage collection
	count, err := cs.GarbageCollect()
	if err != nil {
		t.Fatalf("Garbage collection failed: %v", err)
	}

	if count != 2 {
		t.Errorf("Expected 2 chunks collected, got %d", count)
	}

	// Verify chunks are removed
	_, exists1 := cs.GetChunk(chunk1.ID)
	_, exists2 := cs.GetChunk(chunk2.ID)
	if exists1 || exists2 {
		t.Error("Orphaned chunks should be removed by garbage collection")
	}
}

func TestChunkStore_GetChunkPath(t *testing.T) {
	logger := zap.NewNop()

	// Create a temporary directory for testing
	tmpDir, err := os.MkdirTemp("", "cloudless-test-chunks-*")
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	config := StorageConfig{
		DataDir:           tmpDir,
		EnableCompression: false,
	}

	cs, err := NewChunkStore(config, logger)
	if err != nil {
		t.Fatalf("Failed to create chunk store: %v", err)
	}

	tests := []struct {
		chunkID      string
		expectedPath string
	}{
		{
			chunkID:      "abcdef1234567890",
			expectedPath: filepath.Join(tmpDir, "chunks", "ab", "abcdef1234567890"),
		},
		{
			chunkID:      "a",
			expectedPath: filepath.Join(tmpDir, "chunks", "a"),
		},
		{
			chunkID:      "123456789",
			expectedPath: filepath.Join(tmpDir, "chunks", "12", "123456789"),
		},
	}

	for _, tt := range tests {
		got := cs.getChunkPath(tt.chunkID)
		if got != tt.expectedPath {
			t.Errorf("getChunkPath(%s) = %s, want %s", tt.chunkID, got, tt.expectedPath)
		}
	}
}
