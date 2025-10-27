package storage

import (
	"fmt"
	"time"
)

// ReadConsistency specifies the consistency level for read operations
// CLD-REQ-051: Allows trading consistency for availability and performance
type ReadConsistency int

const (
	// ReadConsistencyStrong requires reading from the RAFT leader
	// Guarantees linearizability but may have higher latency
	ReadConsistencyStrong ReadConsistency = iota

	// ReadConsistencyEventual allows reading from any replica (including followers)
	// May return stale data but offers better performance and availability
	ReadConsistencyEventual

	// ReadConsistencyBounded allows reading from any replica with bounded staleness
	// Returns error if data is older than MaxStaleness threshold
	ReadConsistencyBounded
)

// String returns the string representation of ReadConsistency
func (rc ReadConsistency) String() string {
	switch rc {
	case ReadConsistencyStrong:
		return "strong"
	case ReadConsistencyEventual:
		return "eventual"
	case ReadConsistencyBounded:
		return "bounded"
	default:
		return "unknown"
	}
}

// ReadOptions configures read operation behavior
type ReadOptions struct {
	// Consistency specifies the required consistency level
	Consistency ReadConsistency

	// MaxStaleness specifies the maximum acceptable staleness for bounded consistency
	// Only used when Consistency = ReadConsistencyBounded
	MaxStaleness time.Duration

	// PreferLocal indicates whether to prefer local reads when possible
	// Can improve latency by avoiding network round trips
	PreferLocal bool
}

// DefaultReadOptions returns the default read options (strong consistency)
func DefaultReadOptions() ReadOptions {
	return ReadOptions{
		Consistency:  ReadConsistencyStrong,
		MaxStaleness: 0,
		PreferLocal:  false,
	}
}

// EventualReadOptions returns options for eventual consistency reads
func EventualReadOptions() ReadOptions {
	return ReadOptions{
		Consistency:  ReadConsistencyEventual,
		MaxStaleness: 0,
		PreferLocal:  true,
	}
}

// BoundedReadOptions returns options for bounded staleness reads
func BoundedReadOptions(maxStaleness time.Duration) ReadOptions {
	return ReadOptions{
		Consistency:  ReadConsistencyBounded,
		MaxStaleness: maxStaleness,
		PreferLocal:  true,
	}
}

// StaleReadError indicates that data is too stale for the requested consistency level
type StaleReadError struct {
	MaxStaleness     time.Duration
	ActualStaleness  time.Duration
	LastModified     time.Time
	ConsistencyLevel string
}

// Error implements the error interface
func (e *StaleReadError) Error() string {
	return fmt.Sprintf("data too stale for %s consistency: last modified %v ago (max %v allowed)",
		e.ConsistencyLevel, e.ActualStaleness, e.MaxStaleness)
}

// IsStaleReadError checks if an error is a StaleReadError
func IsStaleReadError(err error) bool {
	_, ok := err.(*StaleReadError)
	return ok
}
