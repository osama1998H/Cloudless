package runtime

import (
	"time"
)

// ContainerSpec defines the specification for creating a container
type ContainerSpec struct {
	ID          string
	Image       string
	Name        string
	Command     []string
	Args        []string
	Env         map[string]string
	WorkingDir  string
	Labels      map[string]string
	Annotations map[string]string

	// Resource limits
	Resources ResourceRequirements

	// Network configuration
	Network NetworkConfig

	// Volume mounts
	Mounts []Mount

	// Restart policy
	RestartPolicy RestartPolicy
}

// Container represents a running container
type Container struct {
	ID          string
	Name        string
	Image       string
	State       ContainerState
	Status      string
	CreatedAt   time.Time
	StartedAt   time.Time
	FinishedAt  time.Time
	ExitCode    int
	Labels      map[string]string
	Annotations map[string]string
	Resources   ResourceRequirements
}

// ContainerState represents the state of a container
type ContainerState string

const (
	ContainerStateCreated  ContainerState = "created"
	ContainerStateRunning  ContainerState = "running"
	ContainerStateStopped  ContainerState = "stopped"
	ContainerStatePaused   ContainerState = "paused"
	ContainerStateFailed   ContainerState = "failed"
	ContainerStateUnknown  ContainerState = "unknown"
)

// ResourceRequirements defines resource requirements and limits
type ResourceRequirements struct {
	Requests ResourceList
	Limits   ResourceList
}

// ResourceList defines a list of resources
type ResourceList struct {
	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64
}

// NetworkConfig defines network configuration for a container
type NetworkConfig struct {
	NetworkMode string
	Hostname    string
	DNS         []string
	DNSSearch   []string
	Ports       []PortMapping
}

// PortMapping defines a port mapping
type PortMapping struct {
	HostPort      int32
	ContainerPort int32
	Protocol      string
	HostIP        string
}

// Mount defines a volume mount
type Mount struct {
	Source      string
	Destination string
	Type        string // bind, volume, tmpfs
	ReadOnly    bool
	Options     []string
}

// RestartPolicy defines the restart policy for a container
type RestartPolicy struct {
	Name              string // no, on-failure, always, unless-stopped
	MaximumRetryCount int
}

// Image represents a container image
type Image struct {
	Name      string
	Tag       string
	Digest    string
	Size      int64
	CreatedAt time.Time
	Labels    map[string]string
}

// ContainerStats represents container resource usage statistics
type ContainerStats struct {
	CPUStats    CPUStats
	MemoryStats MemoryStats
	NetworkStats NetworkStats
	BlockIOStats BlockIOStats
	Timestamp   time.Time
}

// CPUStats represents CPU usage statistics
type CPUStats struct {
	UsageNanoseconds  uint64
	SystemNanoseconds uint64
	ThrottlingCount   uint64
}

// MemoryStats represents memory usage statistics
type MemoryStats struct {
	UsageBytes     uint64
	MaxUsageBytes  uint64
	LimitBytes     uint64
	CacheBytes     uint64
	RSSBytes       uint64
	SwapUsageBytes uint64
}

// NetworkStats represents network usage statistics
type NetworkStats struct {
	RxBytes   uint64
	RxPackets uint64
	RxErrors  uint64
	RxDropped uint64
	TxBytes   uint64
	TxPackets uint64
	TxErrors  uint64
	TxDropped uint64
}

// BlockIOStats represents block I/O statistics
type BlockIOStats struct {
	ReadBytes  uint64
	WriteBytes uint64
	ReadOps    uint64
	WriteOps   uint64
}

// LogEntry represents a container log entry
type LogEntry struct {
	Timestamp time.Time
	Stream    string // stdout or stderr
	Log       string
}

// ExecConfig defines configuration for executing a command in a container
type ExecConfig struct {
	Command []string
	Env     []string
	Tty     bool
	Stdin   bool
	Stdout  bool
	Stderr  bool
}

// ExecResult represents the result of an exec operation
type ExecResult struct {
	ExitCode int
	Stdout   []byte
	Stderr   []byte
}
