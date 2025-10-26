package runtime

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	specs "github.com/opencontainers/runtime-spec/specs-go"
	"go.uber.org/zap"
)

// CreateContainer creates a new container from the spec
func (r *ContainerdRuntime) CreateContainer(ctx context.Context, spec ContainerSpec) (*Container, error) {
	ctx = r.withNamespace(ctx)

	r.logger.Info("Creating container",
		zap.String("id", spec.ID),
		zap.String("image", spec.Image),
	)

	// Normalize image reference (same as in PullImage)
	normalizedRef := normalizeImageRef(spec.Image)

	// Pull image if not present
	image, err := r.client.GetImage(ctx, normalizedRef)
	if err != nil {
		r.logger.Info("Image not found, pulling...", zap.String("image", spec.Image))
		if err := r.PullImage(ctx, spec.Image); err != nil {
			return nil, fmt.Errorf("failed to pull image: %w", err)
		}
		image, err = r.client.GetImage(ctx, normalizedRef)
		if err != nil {
			return nil, fmt.Errorf("failed to get image after pull: %w", err)
		}
	}

	// Build OCI spec options
	specOpts := []oci.SpecOpts{
		oci.WithImageConfig(image),
	}

	// Add command if specified
	if len(spec.Command) > 0 {
		specOpts = append(specOpts, oci.WithProcessArgs(spec.Command...))
	}

	// Add environment variables
	if len(spec.Env) > 0 {
		envVars := make([]string, 0, len(spec.Env))
		for k, v := range spec.Env {
			envVars = append(envVars, fmt.Sprintf("%s=%s", k, v))
		}
		specOpts = append(specOpts, oci.WithEnv(envVars))
	}

	// Add working directory
	if spec.WorkingDir != "" {
		specOpts = append(specOpts, oci.WithProcessCwd(spec.WorkingDir))
	}

	// Add resource limits
	if spec.Resources.Limits.CPUMillicores > 0 {
		// Convert millicores to CPU quota
		period := uint64(100000) // 100ms
		quota := int64(spec.Resources.Limits.CPUMillicores) * int64(period) / 1000
		specOpts = append(specOpts, oci.WithCPUCFS(quota, period))
	}

	if spec.Resources.Limits.MemoryBytes > 0 {
		specOpts = append(specOpts, oci.WithMemoryLimit(uint64(spec.Resources.Limits.MemoryBytes)))
	}

	// Storage limits (via blkio)
	if spec.Resources.Limits.StorageBytes > 0 {
		// Use OCI spec to set block IO weight and throttle
		// Note: This requires cgroup v2 or blkio cgroup controller
		specOpts = append(specOpts, func(ctx context.Context, client oci.Client, c *containers.Container, s *oci.Spec) error {
			if s.Linux == nil {
				s.Linux = &specs.Linux{}
			}
			if s.Linux.Resources == nil {
				s.Linux.Resources = &specs.LinuxResources{}
			}
			if s.Linux.Resources.BlockIO == nil {
				s.Linux.Resources.BlockIO = &specs.LinuxBlockIO{}
			}

			// Set block IO weight (100-1000, with 500 as default)
			weight := uint16(500)
			s.Linux.Resources.BlockIO.Weight = &weight

			// Add annotations for storage quota (to be enforced by orchestrator)
			if c.Labels == nil {
				c.Labels = make(map[string]string)
			}
			c.Labels["cloudless.storage.limit"] = fmt.Sprintf("%d", spec.Resources.Limits.StorageBytes)

			return nil
		})
	}

	// Network bandwidth limits
	// Note: Network QoS typically requires CNI plugins or tc (traffic control)
	// We'll add metadata that can be used by network plugins
	if spec.Network.BandwidthBPS > 0 {
		specOpts = append(specOpts, func(ctx context.Context, client oci.Client, c *containers.Container, s *oci.Spec) error {
			if c.Labels == nil {
				c.Labels = make(map[string]string)
			}
			c.Labels["cloudless.network.bandwidth.limit"] = fmt.Sprintf("%d", spec.Network.BandwidthBPS)
			return nil
		})
	}

	// GPU device access
	if spec.Resources.Limits.GPUCount > 0 {
		specOpts = append(specOpts, func(ctx context.Context, client oci.Client, c *containers.Container, s *oci.Spec) error {
			if s.Linux == nil {
				s.Linux = &specs.Linux{}
			}
			if s.Linux.Resources == nil {
				s.Linux.Resources = &specs.LinuxResources{}
			}

			// Add GPU device access
			// This is a simplified approach - in production, you'd use nvidia-container-runtime
			// or AMD ROCm container runtime to properly expose GPUs
			if s.Linux.Resources.Devices == nil {
				s.Linux.Resources.Devices = []specs.LinuxDeviceCgroup{}
			}

			// Allow access to GPU devices
			// /dev/nvidia* for NVIDIA GPUs
			// /dev/dri/* for AMD/Intel GPUs
			allow := true
			gpuDevice := specs.LinuxDeviceCgroup{
				Allow:  allow,
				Type:   "c",         // character device
				Major:  intPtr(195), // NVIDIA major number
				Minor:  intPtr(-1),  // All NVIDIA devices
				Access: "rwm",       // read, write, mknod
			}
			s.Linux.Resources.Devices = append(s.Linux.Resources.Devices, gpuDevice)

			// Also allow DRI devices for AMD/Intel
			driDevice := specs.LinuxDeviceCgroup{
				Allow:  allow,
				Type:   "c",
				Major:  intPtr(226), // DRI major number
				Minor:  intPtr(-1),  // All DRI devices
				Access: "rwm",
			}
			s.Linux.Resources.Devices = append(s.Linux.Resources.Devices, driDevice)

			// Add label for GPU count
			if c.Labels == nil {
				c.Labels = make(map[string]string)
			}
			c.Labels["cloudless.gpu.count"] = fmt.Sprintf("%d", spec.Resources.Limits.GPUCount)

			return nil
		})
	}

	// Add mounts
	for _, mount := range spec.Mounts {
		specOpts = append(specOpts, oci.WithMounts([]specs.Mount{
			{
				Source:      mount.Source,
				Destination: mount.Destination,
				Type:        mount.Type,
				Options:     mount.Options,
			},
		}))
	}

	// Create container
	container, err := r.client.NewContainer(
		ctx,
		spec.ID,
		containerd.WithImage(image),
		containerd.WithNewSnapshot(spec.ID+"-snapshot", image),
		containerd.WithNewSpec(specOpts...),
		containerd.WithContainerLabels(spec.Labels),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	r.logger.Info("Container created successfully",
		zap.String("id", spec.ID),
	)

	return r.containerToInfo(ctx, container)
}

// StartContainer starts a container
func (r *ContainerdRuntime) StartContainer(ctx context.Context, containerID string) error {
	ctx = r.withNamespace(ctx)

	r.logger.Info("Starting container", zap.String("id", containerID))

	container, err := r.client.LoadContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to load container: %w", err)
	}

	// Create task
	task, err := container.NewTask(ctx, cio.NewCreator(cio.WithStdio))
	if err != nil {
		return fmt.Errorf("failed to create task: %w", err)
	}

	// Start task
	if err := task.Start(ctx); err != nil {
		task.Delete(ctx)
		return fmt.Errorf("failed to start task: %w", err)
	}

	r.logger.Info("Container started successfully",
		zap.String("id", containerID),
		zap.Uint32("pid", task.Pid()),
	)

	return nil
}

// StopContainer stops a container
func (r *ContainerdRuntime) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	ctx = r.withNamespace(ctx)

	r.logger.Info("Stopping container",
		zap.String("id", containerID),
		zap.Duration("timeout", timeout),
	)

	container, err := r.client.LoadContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to load container: %w", err)
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		// No task means container is not running
		return nil
	}

	// Send SIGTERM
	if err := task.Kill(ctx, syscall.SIGTERM); err != nil {
		return fmt.Errorf("failed to send SIGTERM: %w", err)
	}

	// Wait for container to stop or timeout
	statusC, err := task.Wait(ctx)
	if err != nil {
		return fmt.Errorf("failed to wait for task: %w", err)
	}

	select {
	case <-statusC:
		// Container stopped gracefully
	case <-time.After(timeout):
		// Timeout, force kill
		r.logger.Warn("Container did not stop gracefully, forcing kill",
			zap.String("id", containerID),
		)
		if err := task.Kill(ctx, syscall.SIGKILL); err != nil {
			return fmt.Errorf("failed to force kill: %w", err)
		}
		<-statusC
	}

	// Delete task
	if _, err := task.Delete(ctx); err != nil {
		return fmt.Errorf("failed to delete task: %w", err)
	}

	r.logger.Info("Container stopped successfully",
		zap.String("id", containerID),
	)

	return nil
}

// DeleteContainer deletes a container
func (r *ContainerdRuntime) DeleteContainer(ctx context.Context, containerID string) error {
	ctx = r.withNamespace(ctx)

	r.logger.Info("Deleting container", zap.String("id", containerID))

	container, err := r.client.LoadContainer(ctx, containerID)
	if err != nil {
		return fmt.Errorf("failed to load container: %w", err)
	}

	// Make sure task is stopped
	task, err := container.Task(ctx, nil)
	if err == nil {
		// Task exists, kill it
		task.Kill(ctx, syscall.SIGKILL)
		task.Wait(ctx)
		task.Delete(ctx)
	}

	// Delete container
	if err := container.Delete(ctx, containerd.WithSnapshotCleanup); err != nil {
		return fmt.Errorf("failed to delete container: %w", err)
	}

	r.logger.Info("Container deleted successfully",
		zap.String("id", containerID),
	)

	return nil
}

// GetContainer gets information about a container
func (r *ContainerdRuntime) GetContainer(ctx context.Context, containerID string) (*Container, error) {
	ctx = r.withNamespace(ctx)

	container, err := r.client.LoadContainer(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to load container: %w", err)
	}

	return r.containerToInfo(ctx, container)
}

// GetContainerIP retrieves the IP address of a running container
//
// CLD-REQ-032: Required for health probe configuration (probes need container IP)
//
// Implementation: Executes `hostname -i` inside the container to retrieve its IP address.
// This works reliably across different network configurations (bridge, host, overlay).
//
// Returns empty string if container is not running or has no IP assigned.
func (r *ContainerdRuntime) GetContainerIP(ctx context.Context, containerID string) (string, error) {
	ctx = r.withNamespace(ctx)

	// Load container
	container, err := r.client.LoadContainer(ctx, containerID)
	if err != nil {
		return "", fmt.Errorf("failed to load container: %w", err)
	}

	// Get task (running container)
	task, err := container.Task(ctx, nil)
	if err != nil {
		// Container not running
		return "", fmt.Errorf("container not running: %w", err)
	}

	// Check if task is running
	status, err := task.Status(ctx)
	if err != nil {
		return "", fmt.Errorf("failed to get task status: %w", err)
	}

	if status.Status != containerd.Running {
		return "", fmt.Errorf("container not in running state: %s", status.Status)
	}

	// Execute `hostname -i` to get IP address (with 5 second timeout)
	execCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	execResult, err := r.ExecContainer(execCtx, containerID, ExecConfig{
		Command: []string{"sh", "-c", "hostname -i"},
	})
	if err != nil {
		return "", fmt.Errorf("failed to execute hostname command: %w", err)
	}

	if execResult.ExitCode != 0 {
		return "", fmt.Errorf("hostname command failed with exit code %d: %s",
			execResult.ExitCode, execResult.Stderr)
	}

	// Parse IP address from output (trim whitespace/newlines)
	ip := strings.TrimSpace(string(execResult.Stdout))
	if ip == "" {
		return "", fmt.Errorf("no IP address found in container")
	}

	// Validate it looks like an IP (basic check)
	if !strings.Contains(ip, ".") && !strings.Contains(ip, ":") {
		return "", fmt.Errorf("invalid IP address format: %s", ip)
	}

	return ip, nil
}

// ListContainers lists all containers
func (r *ContainerdRuntime) ListContainers(ctx context.Context) ([]*Container, error) {
	ctx = r.withNamespace(ctx)

	containers, err := r.client.Containers(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	result := make([]*Container, 0, len(containers))
	for _, c := range containers {
		info, err := r.containerToInfo(ctx, c)
		if err != nil {
			r.logger.Warn("Failed to get container info",
				zap.String("id", c.ID()),
				zap.Error(err),
			)
			continue
		}
		result = append(result, info)
	}

	return result, nil
}

// containerToInfo converts containerd container to our Container type
func (r *ContainerdRuntime) containerToInfo(ctx context.Context, c containerd.Container) (*Container, error) {
	info, err := c.Info(ctx)
	if err != nil {
		return nil, err
	}

	container := &Container{
		ID:        info.ID,
		Image:     info.Image,
		CreatedAt: info.CreatedAt,
		Labels:    info.Labels,
		State:     ContainerStateUnknown,
		Status:    "unknown",
	}

	// Get container name from labels
	if name, ok := info.Labels["name"]; ok {
		container.Name = name
	}

	// Check task status
	task, err := c.Task(ctx, nil)
	if err == nil {
		status, err := task.Status(ctx)
		if err == nil {
			container.Status = string(status.Status)
			switch status.Status {
			case containerd.Running:
				container.State = ContainerStateRunning
			case containerd.Created:
				container.State = ContainerStateCreated
			case containerd.Stopped:
				container.State = ContainerStateStopped
			case containerd.Paused:
				container.State = ContainerStatePaused
			default:
				container.State = ContainerStateUnknown
			}

			if status.ExitStatus != 0 {
				container.State = ContainerStateFailed
				container.ExitCode = int(status.ExitStatus)
			}
		}
	} else {
		// No task means container is created but not started
		container.State = ContainerStateCreated
		container.Status = "created"
	}

	return container, nil
}

// intPtr returns a pointer to an int64 value
func intPtr(i int) *int64 {
	v := int64(i)
	return &v
}

// ExecContainer executes a command inside a running container
func (r *ContainerdRuntime) ExecContainer(ctx context.Context, containerID string, config ExecConfig) (*ExecResult, error) {
	ctx = r.withNamespace(ctx)

	r.logger.Info("Executing command in container",
		zap.String("container_id", containerID),
		zap.Strings("command", config.Command),
	)

	// Get container
	container, err := r.client.LoadContainer(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to load container: %w", err)
	}

	// Get running task
	task, err := container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get container task: %w", err)
	}

	// Check if task is running
	status, err := task.Status(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get task status: %w", err)
	}
	if status.Status != containerd.Running {
		return nil, fmt.Errorf("container is not running: %s", status.Status)
	}

	// Get container spec
	spec, err := container.Spec(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get container spec: %w", err)
	}

	// Build process spec
	pspec := spec.Process
	pspec.Args = config.Command

	// Add environment variables if specified
	if len(config.Env) > 0 {
		pspec.Env = append(pspec.Env, config.Env...)
	}

	// Set terminal flags
	pspec.Terminal = config.Tty

	// Create exec process
	execID := fmt.Sprintf("%s-exec-%d", containerID, time.Now().UnixNano())

	// Create IO for capturing output
	var stdout, stderr bytes.Buffer
	ioOpts := []cio.Opt{
		cio.WithStreams(nil, &stdout, &stderr),
	}

	process, err := task.Exec(ctx, execID, pspec, cio.NewCreator(ioOpts...))
	if err != nil {
		return nil, fmt.Errorf("failed to create exec process: %w", err)
	}

	// Wait for process to start
	if err := process.Start(ctx); err != nil {
		return nil, fmt.Errorf("failed to start exec process: %w", err)
	}

	// Wait for process to complete
	statusC, err := process.Wait(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to wait for exec process: %w", err)
	}

	// Wait for exit
	exitStatus := <-statusC

	// Get exit code
	exitCode := int(exitStatus.ExitCode())

	// Clean up process
	if _, err := process.Delete(ctx); err != nil {
		r.logger.Warn("Failed to delete exec process",
			zap.String("exec_id", execID),
			zap.Error(err),
		)
	}

	result := &ExecResult{
		ExitCode: exitCode,
		Stdout:   stdout.Bytes(),
		Stderr:   stderr.Bytes(),
	}

	r.logger.Info("Command executed successfully",
		zap.String("container_id", containerID),
		zap.Int("exit_code", exitCode),
	)

	return result, nil
}
