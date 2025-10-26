package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/namespaces"
	"go.uber.org/zap"
)

// Runtime defines the interface for container runtime operations
type Runtime interface {
	// Container operations
	CreateContainer(ctx context.Context, spec ContainerSpec) (*Container, error)
	StartContainer(ctx context.Context, containerID string) error
	StopContainer(ctx context.Context, containerID string, timeout time.Duration) error
	DeleteContainer(ctx context.Context, containerID string) error
	GetContainer(ctx context.Context, containerID string) (*Container, error)
	GetContainerIP(ctx context.Context, containerID string) (string, error)
	ListContainers(ctx context.Context) ([]*Container, error)
	GetContainerLogs(ctx context.Context, containerID string, follow bool) (<-chan LogEntry, error)
	GetContainerStats(ctx context.Context, containerID string) (*ContainerStats, error)
	ExecContainer(ctx context.Context, containerID string, config ExecConfig) (*ExecResult, error)

	// Image operations
	PullImage(ctx context.Context, image string) error
	ListImages(ctx context.Context) ([]*Image, error)
	DeleteImage(ctx context.Context, image string) error

	// Lifecycle
	Close() error
}

// ContainerdRuntime implements Runtime using containerd
type ContainerdRuntime struct {
	client    *containerd.Client
	namespace string
	logger    *zap.Logger
}

// RuntimeConfig contains configuration for the runtime
type RuntimeConfig struct {
	// Containerd socket path
	SocketPath string

	// Namespace for this runtime
	Namespace string

	// Timeout for operations
	Timeout time.Duration
}

// NewContainerdRuntime creates a new containerd runtime
func NewContainerdRuntime(config RuntimeConfig, logger *zap.Logger) (*ContainerdRuntime, error) {
	if config.SocketPath == "" {
		config.SocketPath = "/run/containerd/containerd.sock"
	}

	if config.Namespace == "" {
		config.Namespace = "cloudless"
	}

	if config.Timeout == 0 {
		config.Timeout = 30 * time.Second
	}

	logger.Info("Connecting to containerd",
		zap.String("socket", config.SocketPath),
		zap.String("namespace", config.Namespace),
	)

	// Create containerd client
	client, err := containerd.New(config.SocketPath,
		containerd.WithTimeout(config.Timeout),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to containerd: %w", err)
	}

	// Verify connection
	ctx := context.Background()
	version, err := client.Version(ctx)
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to get containerd version: %w", err)
	}

	logger.Info("Connected to containerd",
		zap.String("version", version.Version),
		zap.String("revision", version.Revision),
	)

	return &ContainerdRuntime{
		client:    client,
		namespace: config.Namespace,
		logger:    logger,
	}, nil
}

// withNamespace wraps a context with the runtime namespace
func (r *ContainerdRuntime) withNamespace(ctx context.Context) context.Context {
	return namespaces.WithNamespace(ctx, r.namespace)
}

// Close closes the containerd client
func (r *ContainerdRuntime) Close() error {
	if r.client != nil {
		return r.client.Close()
	}
	return nil
}

// Ping checks if containerd is responsive
func (r *ContainerdRuntime) Ping(ctx context.Context) error {
	ctx = r.withNamespace(ctx)
	_, err := r.client.Version(ctx)
	return err
}
