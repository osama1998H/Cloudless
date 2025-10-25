package storage

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ChunkStore manages content-addressed chunk storage
type ChunkStore struct {
	config StorageConfig
	logger *zap.Logger

	baseDir string
	chunks  sync.Map // chunkID -> *Chunk
	mu      sync.RWMutex
}

// NewChunkStore creates a new chunk store
func NewChunkStore(config StorageConfig, logger *zap.Logger) (*ChunkStore, error) {
	baseDir := filepath.Join(config.DataDir, "chunks")

	// Create chunks directory
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create chunks directory: %w", err)
	}

	cs := &ChunkStore{
		config:  config,
		logger:  logger,
		baseDir: baseDir,
	}

	// Load existing chunks from disk
	if err := cs.loadChunks(); err != nil {
		logger.Warn("Failed to load existing chunks", zap.Error(err))
	}

	return cs, nil
}

// WriteChunk writes data as a content-addressed chunk
func (cs *ChunkStore) WriteChunk(data []byte) (*Chunk, error) {
	// Calculate checksum
	checksum := cs.calculateChecksum(data)

	// Check if chunk already exists (deduplication)
	if chunk, exists := cs.GetChunk(checksum); exists {
		cs.logger.Debug("Chunk already exists, deduplicating",
			zap.String("chunk_id", checksum),
		)
		// Increment reference count
		chunk.RefCount++
		cs.chunks.Store(checksum, chunk)
		return chunk, nil
	}

	// Optionally compress data
	dataToWrite := data
	compressed := false
	if cs.config.EnableCompression {
		var buf bytes.Buffer
		gzipWriter := gzip.NewWriter(&buf)
		if _, err := gzipWriter.Write(data); err == nil {
			gzipWriter.Close()
			// Only use compressed version if it's actually smaller
			if buf.Len() < len(data) {
				dataToWrite = buf.Bytes()
				compressed = true
				cs.logger.Debug("Compressed chunk",
					zap.Int("original_size", len(data)),
					zap.Int("compressed_size", buf.Len()),
					zap.Float64("ratio", float64(buf.Len())/float64(len(data))),
				)
			}
		} else {
			cs.logger.Warn("Failed to compress chunk, using uncompressed",
				zap.Error(err),
			)
		}
	}

	// Write chunk to disk
	chunkPath := cs.getChunkPath(checksum)
	if err := os.MkdirAll(filepath.Dir(chunkPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create chunk directory: %w", err)
	}

	if err := os.WriteFile(chunkPath, dataToWrite, 0644); err != nil {
		return nil, fmt.Errorf("failed to write chunk: %w", err)
	}

	// Create chunk metadata
	chunk := &Chunk{
		ID:          checksum,
		Size:        int64(len(data)),
		Checksum:    checksum,
		CreatedAt:   time.Now(),
		RefCount:    1,
		Compressed:  compressed,
		ContentHash: checksum,
	}

	// Store in memory
	cs.chunks.Store(checksum, chunk)

	cs.logger.Debug("Wrote chunk",
		zap.String("chunk_id", checksum),
		zap.Int64("size", chunk.Size),
		zap.Bool("compressed", compressed),
	)

	return chunk, nil
}

// ReadChunk reads a chunk by ID and verifies its integrity
func (cs *ChunkStore) ReadChunk(chunkID string) ([]byte, error) {
	// Get chunk metadata
	chunk, exists := cs.GetChunk(chunkID)
	if !exists {
		return nil, fmt.Errorf("chunk not found: %s", chunkID)
	}

	// Read chunk from disk
	chunkPath := cs.getChunkPath(chunkID)
	data, err := os.ReadFile(chunkPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read chunk: %w", err)
	}

	// Decompress if needed
	if chunk.Compressed {
		reader, err := gzip.NewReader(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer reader.Close()

		decompressed, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("failed to decompress chunk: %w", err)
		}
		data = decompressed

		cs.logger.Debug("Decompressed chunk",
			zap.String("chunk_id", chunkID),
			zap.Int("decompressed_size", len(data)),
		)
	}

	// Verify checksum
	actualChecksum := cs.calculateChecksum(data)
	if actualChecksum != chunk.Checksum {
		return nil, fmt.Errorf("checksum mismatch for chunk %s: expected %s, got %s",
			chunkID, chunk.Checksum, actualChecksum)
	}

	return data, nil
}

// DeleteChunk removes a chunk (decrements ref count, deletes if zero)
func (cs *ChunkStore) DeleteChunk(chunkID string) error {
	chunk, exists := cs.GetChunk(chunkID)
	if !exists {
		return fmt.Errorf("chunk not found: %s", chunkID)
	}

	cs.mu.Lock()
	defer cs.mu.Unlock()

	// Decrement reference count
	chunk.RefCount--

	if chunk.RefCount <= 0 {
		// Remove from disk
		chunkPath := cs.getChunkPath(chunkID)
		if err := os.Remove(chunkPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to delete chunk file: %w", err)
		}

		// Remove from memory
		cs.chunks.Delete(chunkID)

		cs.logger.Debug("Deleted chunk",
			zap.String("chunk_id", chunkID),
		)
	} else {
		// Update metadata
		cs.chunks.Store(chunkID, chunk)

		cs.logger.Debug("Decremented chunk refcount",
			zap.String("chunk_id", chunkID),
			zap.Int("refcount", chunk.RefCount),
		)
	}

	return nil
}

// GetChunk retrieves chunk metadata
func (cs *ChunkStore) GetChunk(chunkID string) (*Chunk, bool) {
	if c, ok := cs.chunks.Load(chunkID); ok {
		chunk := c.(*Chunk)
		return chunk, true
	}
	return nil, false
}

// ListChunks returns all chunks
func (cs *ChunkStore) ListChunks() []*Chunk {
	var chunks []*Chunk

	cs.chunks.Range(func(key, value interface{}) bool {
		chunk := value.(*Chunk)
		chunks = append(chunks, chunk)
		return true
	})

	return chunks
}

// VerifyChunk verifies the integrity of a chunk
func (cs *ChunkStore) VerifyChunk(chunkID string) error {
	// Read chunk
	data, err := cs.ReadChunk(chunkID)
	if err != nil {
		return err
	}

	// Verify checksum
	actualChecksum := cs.calculateChecksum(data)
	if actualChecksum != chunkID {
		return fmt.Errorf("checksum verification failed for chunk %s", chunkID)
	}

	return nil
}

// GetChunkSize returns the size of a chunk
func (cs *ChunkStore) GetChunkSize(chunkID string) (int64, error) {
	chunk, exists := cs.GetChunk(chunkID)
	if !exists {
		return 0, fmt.Errorf("chunk not found: %s", chunkID)
	}

	return chunk.Size, nil
}

// GetStorageUsage returns total storage usage
func (cs *ChunkStore) GetStorageUsage() (int64, error) {
	var totalSize int64

	cs.chunks.Range(func(key, value interface{}) bool {
		chunk := value.(*Chunk)
		totalSize += chunk.Size
		return true
	})

	return totalSize, nil
}

// GarbageCollect removes orphaned chunks with zero references
func (cs *ChunkStore) GarbageCollect() (int, error) {
	cs.logger.Info("Starting garbage collection")

	var orphanedChunks []string

	// Find orphaned chunks
	cs.chunks.Range(func(key, value interface{}) bool {
		chunk := value.(*Chunk)
		if chunk.RefCount <= 0 {
			orphanedChunks = append(orphanedChunks, chunk.ID)
		}
		return true
	})

	// Delete orphaned chunks
	for _, chunkID := range orphanedChunks {
		if err := cs.DeleteChunk(chunkID); err != nil {
			cs.logger.Error("Failed to delete orphaned chunk",
				zap.String("chunk_id", chunkID),
				zap.Error(err),
			)
		}
	}

	cs.logger.Info("Garbage collection completed",
		zap.Int("chunks_removed", len(orphanedChunks)),
	)

	return len(orphanedChunks), nil
}

// calculateChecksum calculates SHA256 checksum of data
func (cs *ChunkStore) calculateChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// getChunkPath returns the filesystem path for a chunk
func (cs *ChunkStore) getChunkPath(chunkID string) string {
	// Organize chunks in subdirectories based on first 2 chars of hash
	// e.g., ab/cdef123456... to avoid too many files in one directory
	if len(chunkID) < 2 {
		return filepath.Join(cs.baseDir, chunkID)
	}

	return filepath.Join(cs.baseDir, chunkID[0:2], chunkID)
}

// loadChunks loads existing chunks from disk
func (cs *ChunkStore) loadChunks() error {
	cs.logger.Info("Loading existing chunks from disk")

	count := 0
	err := filepath.Walk(cs.baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip directories
		if info.IsDir() {
			return nil
		}

		// Extract chunk ID from path
		chunkID := filepath.Base(path)

		// Create chunk metadata
		chunk := &Chunk{
			ID:        chunkID,
			Size:      info.Size(),
			Checksum:  chunkID, // Chunk ID is the checksum
			CreatedAt: info.ModTime(),
			RefCount:  1, // Assume referenced until proven otherwise
		}

		cs.chunks.Store(chunkID, chunk)
		count++

		return nil
	})

	if err != nil {
		return fmt.Errorf("failed to walk chunks directory: %w", err)
	}

	cs.logger.Info("Loaded chunks from disk",
		zap.Int("count", count),
	)

	return nil
}

// WriteChunkStream writes a chunk from a reader
func (cs *ChunkStore) WriteChunkStream(reader io.Reader) (*Chunk, error) {
	// Create temporary file
	tmpFile, err := os.CreateTemp(cs.baseDir, "chunk-tmp-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	// Calculate checksum while writing
	hasher := sha256.New()
	writer := io.MultiWriter(tmpFile, hasher)

	// Copy data
	written, err := io.Copy(writer, reader)
	if err != nil {
		return nil, fmt.Errorf("failed to write chunk: %w", err)
	}

	// Get checksum
	checksum := hex.EncodeToString(hasher.Sum(nil))

	// Check for existing chunk
	if chunk, exists := cs.GetChunk(checksum); exists {
		chunk.RefCount++
		cs.chunks.Store(checksum, chunk)
		return chunk, nil
	}

	// Move temp file to final location
	chunkPath := cs.getChunkPath(checksum)
	if err := os.MkdirAll(filepath.Dir(chunkPath), 0755); err != nil {
		return nil, fmt.Errorf("failed to create chunk directory: %w", err)
	}

	tmpFile.Close()
	if err := os.Rename(tmpFile.Name(), chunkPath); err != nil {
		return nil, fmt.Errorf("failed to move chunk: %w", err)
	}

	// Create chunk metadata
	chunk := &Chunk{
		ID:        checksum,
		Size:      written,
		Checksum:  checksum,
		CreatedAt: time.Now(),
		RefCount:  1,
	}

	cs.chunks.Store(checksum, chunk)

	return chunk, nil
}
