package storage

import (
	"errors"
	"fmt"
)

// NotLeaderError indicates the operation was attempted on a non-leader node
// CLD-REQ-051: Clients should redirect writes to the current RAFT leader
type NotLeaderError struct {
	LeaderAddr string // Address of the current leader (e.g., "172.28.0.10:8080")
	NodeID     string // ID of this node
	Operation  string // Operation that was attempted (e.g., "create_bucket")
}

// Error implements the error interface
func (e *NotLeaderError) Error() string {
	if e.LeaderAddr == "" {
		return fmt.Sprintf("node %s is not the RAFT leader (no leader known) for operation: %s", e.NodeID, e.Operation)
	}
	return fmt.Sprintf("node %s is not the RAFT leader (redirect to %s) for operation: %s", e.NodeID, e.LeaderAddr, e.Operation)
}

// IsNotLeaderError checks if an error is a NotLeaderError
func IsNotLeaderError(err error) bool {
	var notLeaderErr *NotLeaderError
	return errors.As(err, &notLeaderErr)
}

// GetLeaderAddress extracts the leader address from a NotLeaderError
// Returns empty string if err is not a NotLeaderError or no leader is known
func GetLeaderAddress(err error) string {
	var notLeaderErr *NotLeaderError
	if errors.As(err, &notLeaderErr) {
		return notLeaderErr.LeaderAddr
	}
	return ""
}

// TimeoutError indicates a RAFT operation timed out
type TimeoutError struct {
	Operation string // Operation that timed out
	Duration  string // How long we waited
}

// Error implements the error interface
func (e *TimeoutError) Error() string {
	return fmt.Sprintf("operation %s timed out after %s", e.Operation, e.Duration)
}

// IsTimeoutError checks if an error is a TimeoutError
func IsTimeoutError(err error) bool {
	var timeoutErr *TimeoutError
	return errors.As(err, &timeoutErr)
}

// ConflictError indicates a conflict (e.g., bucket already exists)
type ConflictError struct {
	Resource string // Resource that had conflict (e.g., "bucket:my-bucket")
	Message  string // Descriptive message
}

// Error implements the error interface
func (e *ConflictError) Error() string {
	return fmt.Sprintf("conflict on resource %s: %s", e.Resource, e.Message)
}

// IsConflictError checks if an error is a ConflictError
func IsConflictError(err error) bool {
	var conflictErr *ConflictError
	return errors.As(err, &conflictErr)
}

// NotFoundError indicates a resource was not found
type NotFoundError struct {
	Resource string // Resource that was not found (e.g., "bucket:my-bucket")
}

// Error implements the error interface
func (e *NotFoundError) Error() string {
	return fmt.Sprintf("resource not found: %s", e.Resource)
}

// IsNotFoundError checks if an error is a NotFoundError
func IsNotFoundError(err error) bool {
	var notFoundErr *NotFoundError
	return errors.As(err, &notFoundErr)
}

// QuotaExceededError indicates a quota was exceeded
type QuotaExceededError struct {
	Resource string // Resource that exceeded quota (e.g., "bucket:my-bucket")
	Current  int64  // Current usage
	Limit    int64  // Quota limit
}

// Error implements the error interface
func (e *QuotaExceededError) Error() string {
	return fmt.Sprintf("quota exceeded for %s: current=%d, limit=%d", e.Resource, e.Current, e.Limit)
}

// IsQuotaExceededError checks if an error is a QuotaExceededError
func IsQuotaExceededError(err error) bool {
	var quotaErr *QuotaExceededError
	return errors.As(err, &quotaErr)
}

// VolumeAccessError indicates unauthorized access to a volume
// Satisfies CLD-REQ-052 isolation requirement and GO_ENGINEERING_SOP.md §8.2
type VolumeAccessError struct {
	VolumeID  string
	CallerID  string
	OwnerID   string
	Operation string
}

// Error implements the error interface
func (e *VolumeAccessError) Error() string {
	return fmt.Sprintf("workload %s cannot %s volume %s (owned by %s)",
		e.CallerID, e.Operation, e.VolumeID, e.OwnerID)
}

// IsVolumeAccessError checks if an error is a VolumeAccessError
func IsVolumeAccessError(err error) bool {
	var accessErr *VolumeAccessError
	return errors.As(err, &accessErr)
}

// Common errors
var (
	ErrNoLeader         = errors.New("no RAFT leader available")
	ErrNotInitialized   = errors.New("metadata store not initialized")
	ErrInvalidParameter = errors.New("invalid parameter")
	ErrInternalError    = errors.New("internal error")
)
