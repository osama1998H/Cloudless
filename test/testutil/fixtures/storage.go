package fixtures

import (
	"crypto/rand"
)

// GenerateRandomData creates random byte data of specified size
func GenerateRandomData(size int) []byte {
	data := make([]byte, size)
	rand.Read(data)
	return data
}

// GenerateCompressibleData creates highly compressible data (repeated patterns)
func GenerateCompressibleData(size int) []byte {
	pattern := []byte("The quick brown fox jumps over the lazy dog. ")
	data := make([]byte, size)
	for i := 0; i < size; i++ {
		data[i] = pattern[i%len(pattern)]
	}
	return data
}

// Common data sizes for testing
const (
	SizeKB   = 1024
	Size4KB  = 4 * 1024
	Size16KB = 16 * 1024
	Size64KB = 64 * 1024
	SizeMB   = 1024 * 1024
	Size4MB  = 4 * 1024 * 1024
)
