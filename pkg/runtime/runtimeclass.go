package runtime

import (
	"context"
	"fmt"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/oci"
	"github.com/opencontainers/runtime-spec/specs-go"
	"go.uber.org/zap"
)

const (
	// RuntimeClassRunc is the default OCI runtime (runc)
	RuntimeClassRunc = "runc"

	// RuntimeClassGVisor is the gVisor runtime (runsc)
	RuntimeClassGVisor = "gvisor"

	// RuntimeClassKata is the Kata Containers runtime
	RuntimeClassKata = "kata"

	// RuntimeClassFirecracker is the Firecracker microVM runtime
	RuntimeClassFirecracker = "firecracker"
)

// RuntimeClass represents a container runtime configuration
type RuntimeClass struct {
	Name    string
	Handler string // Path to the runtime binary
	Options map[string]interface{}
}

// RuntimeClassManager handles runtime class operations
type RuntimeClassManager struct {
	logger          *zap.Logger
	supportedClasses map[string]*RuntimeClass
}

// NewRuntimeClassManager creates a new runtime class manager
func NewRuntimeClassManager(logger *zap.Logger) *RuntimeClassManager {
	return &RuntimeClassManager{
		logger: logger,
		supportedClasses: map[string]*RuntimeClass{
			RuntimeClassRunc: {
				Name:    RuntimeClassRunc,
				Handler: "io.containerd.runc.v2",
			},
			RuntimeClassGVisor: {
				Name:    RuntimeClassGVisor,
				Handler: "io.containerd.runsc.v1",
			},
			RuntimeClassKata: {
				Name:    RuntimeClassKata,
				Handler: "io.containerd.kata.v2",
			},
			RuntimeClassFirecracker: {
				Name:    RuntimeClassFirecracker,
				Handler: "aws.firecracker",
			},
		},
	}
}

// GetRuntimeClass returns the runtime class configuration
func (r *RuntimeClassManager) GetRuntimeClass(name string) (*RuntimeClass, error) {
	if name == "" {
		// Default to runc
		name = RuntimeClassRunc
	}

	class, exists := r.supportedClasses[name]
	if !exists {
		return nil, fmt.Errorf("runtime class not found: %s", name)
	}

	r.logger.Debug("Selected runtime class",
		zap.String("name", class.Name),
		zap.String("handler", class.Handler))

	return class, nil
}

// ApplyRuntimeClass applies a runtime class to a container creation
func (r *RuntimeClassManager) ApplyRuntimeClass(
	ctx context.Context,
	client *containerd.Client,
	containerID string,
	runtimeClassName string,
	spec *specs.Spec,
) ([]containerd.NewContainerOpts, error) {
	class, err := r.GetRuntimeClass(runtimeClassName)
	if err != nil {
		return nil, err
	}

	r.logger.Info("Applying runtime class",
		zap.String("container_id", containerID),
		zap.String("runtime_class", class.Name),
		zap.String("handler", class.Handler))

	// Create container options with runtime
	opts := []containerd.NewContainerOpts{
		containerd.WithRuntime(class.Handler, class.Options),
	}

	// Apply runtime-specific configurations
	switch class.Name {
	case RuntimeClassGVisor:
		// gVisor-specific options
		r.applyGVisorConfig(spec)

	case RuntimeClassKata:
		// Kata Containers-specific options
		r.applyKataConfig(spec)

	case RuntimeClassFirecracker:
		// Firecracker-specific options
		r.applyFirecrackerConfig(spec)

	default:
		// Default (runc) - no special config needed
	}

	return opts, nil
}

// applyGVisorConfig applies gVisor-specific configuration
func (r *RuntimeClassManager) applyGVisorConfig(spec *specs.Spec) {
	r.logger.Debug("Applying gVisor configuration")

	// gVisor (runsc) provides strong isolation by implementing
	// its own kernel in userspace
	// Most security features (seccomp, AppArmor) are handled by gVisor itself

	// Ensure we have process config
	if spec.Process == nil {
		spec.Process = &specs.Process{}
	}

	// gVisor benefits from specific configurations
	// Note: Some features may need to be adjusted for gVisor compatibility
}

// applyKataConfig applies Kata Containers-specific configuration
func (r *RuntimeClassManager) applyKataConfig(spec *specs.Spec) {
	r.logger.Debug("Applying Kata Containers configuration")

	// Kata Containers runs each container in its own lightweight VM
	// providing VM-level isolation

	if spec.Process == nil {
		spec.Process = &specs.Process{}
	}

	// Kata-specific settings could go here
}

// applyFirecrackerConfig applies Firecracker-specific configuration
func (r *RuntimeClassManager) applyFirecrackerConfig(spec *specs.Spec) {
	r.logger.Debug("Applying Firecracker configuration")

	// Firecracker provides microVM isolation

	if spec.Process == nil {
		spec.Process = &specs.Process{}
	}

	// Firecracker-specific settings could go here
}

// GetContainerdRuntimeOpt returns the containerd runtime option
func GetContainerdRuntimeOpt(runtimeClassName string, logger *zap.Logger) (containerd.NewContainerOpts, error) {
	manager := NewRuntimeClassManager(logger)
	class, err := manager.GetRuntimeClass(runtimeClassName)
	if err != nil {
		return nil, err
	}

	return containerd.WithRuntime(class.Handler, class.Options), nil
}

// ApplyRuntimeClassSpec applies runtime class-specific spec modifications
func ApplyRuntimeClassSpec(spec *specs.Spec, runtimeClassName string, logger *zap.Logger) error {
	if runtimeClassName == "" || runtimeClassName == RuntimeClassRunc {
		// Default runtime, no special handling
		return nil
	}

	manager := NewRuntimeClassManager(logger)

	switch runtimeClassName {
	case RuntimeClassGVisor:
		manager.applyGVisorConfig(spec)
	case RuntimeClassKata:
		manager.applyKataConfig(spec)
	case RuntimeClassFirecracker:
		manager.applyFirecrackerConfig(spec)
	default:
		return fmt.Errorf("unknown runtime class: %s", runtimeClassName)
	}

	logger.Info("Applied runtime class spec modifications",
		zap.String("runtime_class", runtimeClassName))

	return nil
}

// IsRuntimeAvailable checks if a specific runtime is available
func IsRuntimeAvailable(runtimeName string, client *containerd.Client, ctx context.Context) bool {
	// Check if the runtime plugin is available in containerd
	// This is a simplified check - in production, you'd query containerd's plugin API

	manager := NewRuntimeClassManager(nil)
	_, err := manager.GetRuntimeClass(runtimeName)
	return err == nil
}

// ListAvailableRuntimes returns a list of available runtime classes
func ListAvailableRuntimes(logger *zap.Logger) []string {
	manager := NewRuntimeClassManager(logger)
	runtimes := make([]string, 0, len(manager.supportedClasses))
	for name := range manager.supportedClasses {
		runtimes = append(runtimes, name)
	}
	return runtimes
}

// GetDefaultRuntimeClass returns the default runtime class name
func GetDefaultRuntimeClass() string {
	return RuntimeClassRunc
}
