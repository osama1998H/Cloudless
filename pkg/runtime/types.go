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

	// Security context (CLD-REQ-062: sandboxing and least privilege)
	SecurityContext *SecurityContext
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
	LogPath     string // Path to container log file for log streaming
}

// ContainerState represents the state of a container
type ContainerState string

const (
	ContainerStateCreated ContainerState = "created"
	ContainerStateRunning ContainerState = "running"
	ContainerStateStopped ContainerState = "stopped"
	ContainerStatePaused  ContainerState = "paused"
	ContainerStateFailed  ContainerState = "failed"
	ContainerStateUnknown ContainerState = "unknown"
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
	GPUCount      int32
}

// NetworkConfig defines network configuration for a container
type NetworkConfig struct {
	NetworkMode  string
	Hostname     string
	DNS          []string
	DNSSearch    []string
	Ports        []PortMapping
	BandwidthBPS int64
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
	CPUStats     CPUStats
	MemoryStats  MemoryStats
	NetworkStats NetworkStats
	BlockIOStats BlockIOStats
	StorageStats StorageStats
	GPUStats     GPUStats
	Timestamp    time.Time
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

// StorageStats represents storage usage statistics
type StorageStats struct {
	UsageBytes uint64
	LimitBytes uint64
	IOPSRead   uint64
	IOPSWrite  uint64
}

// GPUStats represents GPU usage statistics
type GPUStats struct {
	DeviceCount        int32
	UtilizationPercent float64
	MemoryUsedBytes    uint64
	MemoryTotalBytes   uint64
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

// ============================================================================
// Security Context Types (CLD-REQ-062)
// ============================================================================

// SecurityContext defines security attributes for a container
type SecurityContext struct {
	// Linux-specific security options
	Linux *LinuxSecurityOptions

	// Run container as privileged (dangerous - requires explicit policy)
	Privileged bool

	// Allow host network access
	HostNetwork bool

	// Allow host PID namespace
	HostPID bool

	// Allow host IPC namespace
	HostIPC bool

	// User ID to run the container process as
	RunAsUser *int64

	// Group ID to run the container process as
	RunAsGroup *int64

	// Run as non-root user (enforced)
	RunAsNonRoot *bool

	// Read-only root filesystem
	ReadOnlyRootFilesystem bool

	// Capabilities to add to the container
	CapabilitiesAdd []string

	// Capabilities to drop from the container
	CapabilitiesDrop []string

	// RuntimeClassName for alternative runtimes (gVisor, kata, runc)
	RuntimeClassName string
}

// LinuxSecurityOptions contains Linux-specific security settings
type LinuxSecurityOptions struct {
	// Seccomp profile configuration
	SeccompProfile *SeccompProfile

	// AppArmor profile configuration
	AppArmorProfile *AppArmorProfile

	// SELinux context options
	SELinuxOptions *SELinuxOptions

	// List of supplemental groups for the container process
	SupplementalGroups []int64

	// FSGroup for volume ownership
	FSGroup *int64

	// Sysctls to set in the container
	Sysctls map[string]string
}

// SeccompProfile defines a seccomp (secure computing mode) profile
type SeccompProfile struct {
	// Type of seccomp profile to apply
	Type SeccompProfileType

	// LocalhostProfile indicates a profile defined in a file on the node
	// Should be an absolute path to the profile. Only used when Type=SeccompProfileTypeLocalhost
	LocalhostProfile string
}

// SeccompProfileType defines the type of seccomp profile
type SeccompProfileType string

const (
	// SeccompProfileTypeUnconfined - No seccomp filtering
	SeccompProfileTypeUnconfined SeccompProfileType = "Unconfined"

	// SeccompProfileTypeRuntimeDefault - Use the container runtime's default profile
	SeccompProfileTypeRuntimeDefault SeccompProfileType = "RuntimeDefault"

	// SeccompProfileTypeLocalhost - Use a profile from a file on the node
	SeccompProfileTypeLocalhost SeccompProfileType = "Localhost"
)

// AppArmorProfile defines an AppArmor profile
type AppArmorProfile struct {
	// Type of AppArmor profile to apply
	Type AppArmorProfileType

	// LocalhostProfile indicates a profile name or path on the node
	// Only used when Type=AppArmorProfileTypeLocalhost
	LocalhostProfile string
}

// AppArmorProfileType defines the type of AppArmor profile
type AppArmorProfileType string

const (
	// AppArmorProfileTypeUnconfined - No AppArmor enforcement
	AppArmorProfileTypeUnconfined AppArmorProfileType = "Unconfined"

	// AppArmorProfileTypeRuntimeDefault - Use the container runtime's default profile
	AppArmorProfileTypeRuntimeDefault AppArmorProfileType = "RuntimeDefault"

	// AppArmorProfileTypeLocalhost - Use a profile from the node
	AppArmorProfileTypeLocalhost AppArmorProfileType = "Localhost"
)

// SELinuxOptions defines SELinux context labels
type SELinuxOptions struct {
	// User is the SELinux user label
	User string

	// Role is the SELinux role label
	Role string

	// Type is the SELinux type label
	Type string

	// Level is the SELinux level label (MLS/MCS)
	Level string
}
