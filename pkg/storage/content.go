package storage

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"

	"go.uber.org/zap"
)

// ContentEngine handles chunking and assembly of objects
type ContentEngine struct {
	chunkStore *ChunkStore
	config     StorageConfig
	logger     *zap.Logger
}

// NewContentEngine creates a new content engine
func NewContentEngine(chunkStore *ChunkStore, config StorageConfig, logger *zap.Logger) *ContentEngine {
	return &ContentEngine{
		chunkStore: chunkStore,
		config:     config,
		logger:     logger,
	}
}

// StoreContent splits content into chunks and stores them
func (ce *ContentEngine) StoreContent(data []byte) ([]string, string, error) {
	if len(data) == 0 {
		return []string{}, "", fmt.Errorf("cannot store empty content")
	}

	// Calculate overall checksum
	overallChecksum := ce.calculateChecksum(data)

	// Split into chunks
	chunks := ce.splitIntoChunks(data)

	var chunkIDs []string
	for i, chunkData := range chunks {
		chunk, err := ce.chunkStore.WriteChunk(chunkData)
		if err != nil {
			// Cleanup already written chunks
			ce.cleanupChunks(chunkIDs)
			return nil, "", fmt.Errorf("failed to write chunk %d: %w", i, err)
		}

		chunkIDs = append(chunkIDs, chunk.ID)
	}

	ce.logger.Debug("Stored content",
		zap.Int("chunk_count", len(chunkIDs)),
		zap.Int("size", len(data)),
		zap.String("checksum", overallChecksum),
	)

	return chunkIDs, overallChecksum, nil
}

// RetrieveContent assembles chunks back into the original content
func (ce *ContentEngine) RetrieveContent(chunkIDs []string) ([]byte, error) {
	if len(chunkIDs) == 0 {
		return []byte{}, nil
	}

	var buffer bytes.Buffer

	for i, chunkID := range chunkIDs {
		data, err := ce.chunkStore.ReadChunk(chunkID)
		if err != nil {
			return nil, fmt.Errorf("failed to read chunk %d (%s): %w", i, chunkID, err)
		}

		if _, err := buffer.Write(data); err != nil {
			return nil, fmt.Errorf("failed to assemble chunk %d: %w", i, err)
		}
	}

	return buffer.Bytes(), nil
}

// StoreContentStream stores content from a stream
func (ce *ContentEngine) StoreContentStream(reader io.Reader) ([]string, string, error) {
	var chunkIDs []string
	hasher := sha256.New()
	buffer := make([]byte, ce.config.ChunkSize)

	for {
		// Read chunk-sized data
		n, err := io.ReadFull(reader, buffer)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			ce.cleanupChunks(chunkIDs)
			return nil, "", fmt.Errorf("failed to read stream: %w", err)
		}

		if n == 0 {
			break
		}

		// Update overall checksum
		hasher.Write(buffer[:n])

		// Store chunk
		chunk, err := ce.chunkStore.WriteChunk(buffer[:n])
		if err != nil {
			ce.cleanupChunks(chunkIDs)
			return nil, "", fmt.Errorf("failed to write chunk: %w", err)
		}

		chunkIDs = append(chunkIDs, chunk.ID)

		if err == io.EOF || err == io.ErrUnexpectedEOF {
			break
		}
	}

	overallChecksum := hex.EncodeToString(hasher.Sum(nil))

	ce.logger.Debug("Stored content from stream",
		zap.Int("chunk_count", len(chunkIDs)),
		zap.String("checksum", overallChecksum),
	)

	return chunkIDs, overallChecksum, nil
}

// RetrieveContentStream retrieves content as a stream
func (ce *ContentEngine) RetrieveContentStream(chunkIDs []string, writer io.Writer) error {
	for i, chunkID := range chunkIDs {
		data, err := ce.chunkStore.ReadChunk(chunkID)
		if err != nil {
			return fmt.Errorf("failed to read chunk %d (%s): %w", i, chunkID, err)
		}

		if _, err := writer.Write(data); err != nil {
			return fmt.Errorf("failed to write chunk %d: %w", i, err)
		}
	}

	return nil
}

// DeleteContent deletes chunks associated with content
func (ce *ContentEngine) DeleteContent(chunkIDs []string) error {
	for i, chunkID := range chunkIDs {
		if err := ce.chunkStore.DeleteChunk(chunkID); err != nil {
			ce.logger.Error("Failed to delete chunk",
				zap.Int("index", i),
				zap.String("chunk_id", chunkID),
				zap.Error(err),
			)
			// Continue with other chunks even if one fails
		}
	}

	return nil
}

// VerifyContent verifies the integrity of content
func (ce *ContentEngine) VerifyContent(chunkIDs []string, expectedChecksum string) error {
	// Retrieve all chunks
	data, err := ce.RetrieveContent(chunkIDs)
	if err != nil {
		return fmt.Errorf("failed to retrieve content: %w", err)
	}

	// Calculate checksum
	actualChecksum := ce.calculateChecksum(data)

	// Compare
	if actualChecksum != expectedChecksum {
		return fmt.Errorf("checksum mismatch: expected %s, got %s",
			expectedChecksum, actualChecksum)
	}

	return nil
}

// GetContentSize returns the total size of content
func (ce *ContentEngine) GetContentSize(chunkIDs []string) (int64, error) {
	var totalSize int64

	for _, chunkID := range chunkIDs {
		size, err := ce.chunkStore.GetChunkSize(chunkID)
		if err != nil {
			return 0, fmt.Errorf("failed to get size of chunk %s: %w", chunkID, err)
		}

		totalSize += size
	}

	return totalSize, nil
}

// splitIntoChunks splits data into fixed-size chunks
func (ce *ContentEngine) splitIntoChunks(data []byte) [][]byte {
	chunkSize := int(ce.config.ChunkSize)
	var chunks [][]byte

	for i := 0; i < len(data); i += chunkSize {
		end := i + chunkSize
		if end > len(data) {
			end = len(data)
		}

		// Create a copy of the chunk data
		chunk := make([]byte, end-i)
		copy(chunk, data[i:end])

		chunks = append(chunks, chunk)
	}

	if len(chunks) == 0 {
		chunks = append(chunks, data)
	}

	return chunks
}

// cleanupChunks removes chunks (for rollback on error)
func (ce *ContentEngine) cleanupChunks(chunkIDs []string) {
	for _, chunkID := range chunkIDs {
		if err := ce.chunkStore.DeleteChunk(chunkID); err != nil {
			ce.logger.Error("Failed to cleanup chunk",
				zap.String("chunk_id", chunkID),
				zap.Error(err),
			)
		}
	}
}

// calculateChecksum calculates SHA256 checksum
func (ce *ContentEngine) calculateChecksum(data []byte) string {
	hash := sha256.Sum256(data)
	return hex.EncodeToString(hash[:])
}

// DeduplicateContent checks if content already exists
func (ce *ContentEngine) DeduplicateContent(checksum string) ([]string, bool) {
	// In a full implementation, would maintain a checksum -> chunkIDs mapping
	// For now, return false (no deduplication at object level, only at chunk level)
	return nil, false
}

// GetChunkCount returns the number of chunks needed for a given size
func (ce *ContentEngine) GetChunkCount(size int64) int {
	chunkSize := ce.config.ChunkSize
	count := size / chunkSize
	if size%chunkSize != 0 {
		count++
	}

	if count == 0 {
		count = 1
	}

	return int(count)
}
