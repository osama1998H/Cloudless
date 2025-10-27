package storage

import (
	"time"
)

// StorageClass represents the storage tier for data
type StorageClass string

const (
	// StorageClassHot for frequently accessed data with high IOPS
	StorageClassHot StorageClass = "hot"
	// StorageClassCold for infrequently accessed data with lower IOPS
	StorageClassCold StorageClass = "cold"
	// StorageClassEphemeral for temporary node-local storage
	StorageClassEphemeral StorageClass = "ephemeral"
)

// IOPSClass represents the IOPS performance tier
type IOPSClass string

const (
	// IOPSClassHigh for high-performance storage (SSD)
	IOPSClassHigh IOPSClass = "high"
	// IOPSClassMedium for medium-performance storage
	IOPSClassMedium IOPSClass = "medium"
	// IOPSClassLow for low-performance storage (HDD)
	IOPSClassLow IOPSClass = "low"
)

// ReplicaStatus represents the health status of a replica
type ReplicaStatus string

const (
	// ReplicaStatusHealthy indicates replica is up-to-date and accessible
	ReplicaStatusHealthy ReplicaStatus = "healthy"
	// ReplicaStatusStale indicates replica is behind
	ReplicaStatusStale ReplicaStatus = "stale"
	// ReplicaStatusCorrupted indicates replica has checksum mismatch
	ReplicaStatusCorrupted ReplicaStatus = "corrupted"
	// ReplicaStatusMissing indicates replica is not accessible
	ReplicaStatusMissing ReplicaStatus = "missing"
)

// Bucket represents a storage bucket (namespace for objects)
type Bucket struct {
	Name         string
	StorageClass StorageClass
	QuotaBytes   int64
	UsedBytes    int64
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Labels       map[string]string
	Owner        string // Workload or user identity
}

// Object represents a stored object with metadata
type Object struct {
	Bucket       string
	Key          string
	Size         int64
	Checksum     string // SHA256 of entire object
	ChunkIDs     []string
	Chunks       []Chunk
	Replicas     []Replica
	Version      uint64
	StorageClass StorageClass
	ContentType  string
	Metadata     map[string]string
	CreatedAt    time.Time
	UpdatedAt    time.Time
	AccessedAt   time.Time
}

// Chunk represents a content-addressed storage chunk
type Chunk struct {
	ID          string // SHA256 hash of content
	Size        int64
	Checksum    string // SHA256 for verification
	Offset      int64  // Offset within parent object
	Replicas    []string
	CreatedAt   time.Time
	RefCount    int // Number of objects referencing this chunk
	Compressed  bool
	ContentHash string // Original content hash before compression
}

// Replica represents a copy of data on a specific node
type Replica struct {
	ChunkID      string
	NodeID       string
	Status       ReplicaStatus
	LastVerified time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
	Size         int64
	Checksum     string
}

// VolumeState represents the state of a volume
type VolumeState string

const (
	VolumeStateCreated VolumeState = "created"
	VolumeStateMounted VolumeState = "mounted"
	VolumeStateDeleted VolumeState = "deleted"
	VolumeStateFailed  VolumeState = "failed"
)

// Volume represents a node-local ephemeral volume
type Volume struct {
	ID           string
	Name         string
	WorkloadID   string
	NodeID       string
	Path         string
	SizeBytes    int64
	UsedBytes    int64
	MountPoint   string
	MountPath    string
	ReadOnly     bool
	StorageClass StorageClass
	IOPSClass    IOPSClass
	State        VolumeState
	CreatedAt    time.Time
	MountedAt    time.Time
	LastUsed     time.Time
	Labels       map[string]string
	Annotations  map[string]string
}

// ReplicationFactor defines the number of replicas for data
type ReplicationFactor int

const (
	// ReplicationFactorOne for ephemeral/local-only data
	ReplicationFactorOne ReplicationFactor = 1
	// ReplicationFactorTwo for MVP durability
	ReplicationFactorTwo ReplicationFactor = 2
	// ReplicationFactorThree for production durability (default)
	ReplicationFactorThree ReplicationFactor = 3
)

// StorageConfig contains configuration for the storage system
type StorageConfig struct {
	// DataDir is the base directory for local storage
	DataDir string

	// ChunkSize is the size of each chunk in bytes (default 4MB)
	ChunkSize int64

	// ReplicationFactor is the number of replicas to maintain
	ReplicationFactor ReplicationFactor

	// EnableCompression enables chunk compression
	EnableCompression bool

	// RepairInterval is how often to run anti-entropy repair
	RepairInterval time.Duration

	// GCInterval is how often to run garbage collection
	GCInterval time.Duration

	// MaxDiskUsagePercent is the maximum disk usage before rejecting writes
	MaxDiskUsagePercent int

	// EnableReadRepair enables read-time repair of stale replicas
	EnableReadRepair bool

	// QuorumWrites requires W=R/2+1 acknowledgments for writes
	QuorumWrites bool
}

// DefaultStorageConfig returns a default storage configuration
func DefaultStorageConfig() StorageConfig {
	return StorageConfig{
		DataDir:             "/var/lib/cloudless/storage",
		ChunkSize:           4 * 1024 * 1024, // 4MB
		ReplicationFactor:   ReplicationFactorThree, // CLD-REQ-050 requires R=3 default
		EnableCompression:   false,
		RepairInterval:      1 * time.Hour,
		GCInterval:          24 * time.Hour,
		MaxDiskUsagePercent: 90,
		EnableReadRepair:    true,
		QuorumWrites:        true,
	}
}

// NodeStorageInfo represents storage capacity and usage on a node
type NodeStorageInfo struct {
	NodeID            string
	TotalBytes        int64
	AvailableBytes    int64
	UsedBytes         int64
	ReservedBytes     int64
	ChunkCount        int
	IOPSClass         IOPSClass
	LastUpdated       time.Time
	HealthStatus      string
	StorageClasses    []StorageClass
	SupportedFeatures []string
}

// ObjectMetadata represents minimal object information for listings
type ObjectMetadata struct {
	Key          string
	Size         int64
	Checksum     string
	StorageClass StorageClass
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// BucketMetadata represents minimal bucket information for listings
type BucketMetadata struct {
	Name         string
	StorageClass StorageClass
	UsedBytes    int64
	ObjectCount  int
	CreatedAt    time.Time
}

// RepairTask represents a repair operation
type RepairTask struct {
	ChunkID       string
	Type          RepairType
	Priority      int
	SourceNodeID  string
	TargetNodeID  string
	CreatedAt     time.Time
	StartedAt     time.Time
	CompletedAt   time.Time
	Status        RepairStatus
	ErrorMessage  string
	BytesRepaired int64
}

// RepairType represents the type of repair needed
type RepairType string

const (
	// RepairTypeMissing for missing replicas
	RepairTypeMissing RepairType = "missing"
	// RepairTypeCorrupted for corrupted replicas
	RepairTypeCorrupted RepairType = "corrupted"
	// RepairTypeStale for out-of-date replicas
	RepairTypeStale RepairType = "stale"
)

// RepairStatus represents the status of a repair task
type RepairStatus string

const (
	// RepairStatusPending indicates repair is queued
	RepairStatusPending RepairStatus = "pending"
	// RepairStatusInProgress indicates repair is running
	RepairStatusInProgress RepairStatus = "in_progress"
	// RepairStatusCompleted indicates repair succeeded
	RepairStatusCompleted RepairStatus = "completed"
	// RepairStatusFailed indicates repair failed
	RepairStatusFailed RepairStatus = "failed"
)

// StorageStats contains storage system statistics
type StorageStats struct {
	TotalObjects      int64
	TotalChunks       int64
	TotalBytes        int64
	TotalReplicas     int64
	HealthyReplicas   int64
	StaleReplicas     int64
	CorruptedReplicas int64
	MissingReplicas   int64
	BucketCount       int64
	VolumeCount       int64
	LastUpdated       time.Time
}

// ChunkLocation represents where a chunk is stored
type ChunkLocation struct {
	ChunkID  string
	NodeID   string
	Path     string
	Size     int64
	Checksum string
}

// TransferProgress represents progress of a data transfer
type TransferProgress struct {
	ChunkID       string
	BytesTotal    int64
	BytesCurrent  int64
	PercentDone   float64
	StartTime     time.Time
	EstCompletion time.Time
	Speed         int64 // Bytes per second
}
