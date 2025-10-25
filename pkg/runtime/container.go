package runtime

import (
	"context"
	"fmt"
	"syscall"
	"time"

	"github.com/containerd/containerd"
	"github.com/containerd/containerd/cio"
	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/oci"
	"go.uber.org/zap"
)

// CreateContainer creates a new container from the spec
func (r *ContainerdRuntime) CreateContainer(ctx context.Context, spec ContainerSpec) (*Container, error) {
	ctx = r.withNamespace(ctx)

	r.logger.Info("Creating container",
		zap.String("id", spec.ID),
		zap.String("image", spec.Image),
	)

	// Pull image if not present
	image, err := r.client.GetImage(ctx, spec.Image)
	if err != nil {
		r.logger.Info("Image not found, pulling...", zap.String("image", spec.Image))
		if err := r.PullImage(ctx, spec.Image); err != nil {
			return nil, fmt.Errorf("failed to pull image: %w", err)
		}
		image, err = r.client.GetImage(ctx, spec.Image)
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

	// Add mounts
	for _, mount := range spec.Mounts {
		specOpts = append(specOpts, oci.WithMounts([]oci.Mount{
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
		ID:          info.ID,
		Image:       info.Image,
		CreatedAt:   info.CreatedAt,
		Labels:      info.Labels,
		State:       ContainerStateUnknown,
		Status:      "unknown",
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
