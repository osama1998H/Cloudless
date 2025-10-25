package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RunContainer starts a new container
func (a *Agent) RunContainer(ctx context.Context, req *api.RunContainerRequest) (*api.RunContainerResponse, error) {
	a.logger.Info("Running container",
		zap.String("image", req.Image),
		zap.String("name", req.Name),
	)

	// Pull image if it doesn't exist
	if err := a.runtime.PullImage(ctx, req.Image); err != nil {
		a.logger.Warn("Failed to pull image, continuing anyway",
			zap.String("image", req.Image),
			zap.Error(err),
		)
		// Continue - image might already exist
	}

	// Convert API request to runtime spec
	spec := runtime.ContainerSpec{
		Name:  req.Name,
		Image: req.Image,
		// TODO: Map environment variables, volumes, ports, etc.
	}

	// Map resource limits if provided
	if req.Resources != nil {
		spec.Resources = runtime.ResourceLimits{
			CPUMillicores: int64(req.Resources.CpuMillicores),
			MemoryBytes:   req.Resources.MemoryBytes,
			// TODO: Map other resource limits
		}
	}

	// Create the container
	container, err := a.runtime.CreateContainer(ctx, spec)
	if err != nil {
		a.logger.Error("Failed to create container",
			zap.String("image", req.Image),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to create container: %w", err)
	}

	// Start the container
	if err := a.runtime.StartContainer(ctx, container.ID); err != nil {
		a.logger.Error("Failed to start container",
			zap.String("container_id", container.ID),
			zap.Error(err),
		)
		// Try to clean up the container
		_ = a.runtime.DeleteContainer(ctx, container.ID)
		return nil, fmt.Errorf("failed to start container: %w", err)
	}

	a.logger.Info("Container started successfully",
		zap.String("container_id", container.ID),
		zap.String("image", req.Image),
	)

	return &api.RunContainerResponse{
		ContainerId: container.ID,
	}, nil
}

// StopContainer stops a running container
func (a *Agent) StopContainer(ctx context.Context, req *api.StopContainerRequest) (*emptypb.Empty, error) {
	a.logger.Info("Stopping container",
		zap.String("container_id", req.ContainerId),
		zap.Bool("force", req.Force),
	)

	// Determine timeout
	timeout := 30 * time.Second
	if req.Timeout != nil {
		timeout = req.Timeout.AsDuration()
	}

	// Stop the container
	// Note: Force and regular stop both use StopContainer
	// If force is true, we use a very short timeout
	if req.Force {
		timeout = 1 * time.Second
	}

	if err := a.runtime.StopContainer(ctx, req.ContainerId, timeout); err != nil {
		a.logger.Error("Failed to stop container",
			zap.String("container_id", req.ContainerId),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to stop container: %w", err)
	}

	a.logger.Info("Container stopped successfully",
		zap.String("container_id", req.ContainerId),
	)

	return &emptypb.Empty{}, nil
}

// GetContainerStatus retrieves the status of a container
func (a *Agent) GetContainerStatus(ctx context.Context, req *api.GetContainerStatusRequest) (*api.ContainerStatus, error) {
	a.logger.Debug("Getting container status",
		zap.String("container_id", req.ContainerId),
	)

	// Get container info from runtime
	info, err := a.runtime.GetContainer(ctx, req.ContainerId)
	if err != nil {
		a.logger.Error("Failed to get container status",
			zap.String("container_id", req.ContainerId),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to get container status: %w", err)
	}

	// Get container stats if requested
	var stats *api.ResourceUsage
	if req.IncludeStats {
		containerStats, err := a.runtime.GetContainerStats(ctx, req.ContainerId)
		if err != nil {
			a.logger.Warn("Failed to get container stats",
				zap.String("container_id", req.ContainerId),
				zap.Error(err),
			)
			// Continue without stats rather than failing the entire request
		} else {
			stats = &api.ResourceUsage{
				CpuMillicores: int32(containerStats.CPUStats.UsageNanoseconds / 1000000), // Convert nanoseconds to millicores (approximate)
				MemoryBytes:   int64(containerStats.MemoryStats.UsageBytes),
				// TODO: Map other resource stats
			}
		}
	}

	// Convert to API response
	status := &api.ContainerStatus{
		ContainerId: info.ID,
		Name:        info.Name,
		State:       mapContainerState(info.State),
		Image:       info.Image,
		CreatedAt:   timestamppb.New(info.CreatedAt),
		Stats:       stats,
	}

	return status, nil
}

// StreamLogs streams container logs
func (a *Agent) StreamLogs(req *api.StreamLogsRequest, stream grpc.ServerStreamingServer[api.LogEntry]) error {
	a.logger.Info("Streaming container logs",
		zap.String("container_id", req.ContainerId),
		zap.Bool("follow", req.Follow),
	)

	// Get log stream from runtime
	logCh, err := a.runtime.GetContainerLogs(stream.Context(), req.ContainerId, req.Follow)
	if err != nil {
		a.logger.Error("Failed to get container logs",
			zap.String("container_id", req.ContainerId),
			zap.Error(err),
		)
		return fmt.Errorf("failed to get container logs: %w", err)
	}

	// Stream logs to client
	for {
		select {
		case <-stream.Context().Done():
			a.logger.Debug("Log streaming cancelled by client")
			return stream.Context().Err()
		case logEntry, ok := <-logCh:
			if !ok {
				a.logger.Debug("Log stream closed")
				return nil
			}

			entry := &api.LogEntry{
				Timestamp: timestamppb.New(logEntry.Timestamp),
				Line:      logEntry.Message,
				Stream:    logEntry.Stream,
			}

			if err := stream.Send(entry); err != nil {
				a.logger.Error("Failed to send log entry",
					zap.Error(err),
				)
				return fmt.Errorf("failed to send log entry: %w", err)
			}
		}
	}
}

// ExecCommand executes a command inside a container
func (a *Agent) ExecCommand(ctx context.Context, req *api.ExecCommandRequest) (*api.ExecCommandResponse, error) {
	a.logger.Info("Executing command in container",
		zap.String("container_id", req.ContainerId),
		zap.Strings("command", req.Command),
	)

	// Build exec config
	execConfig := runtime.ExecConfig{
		Command: req.Command,
		Env:     req.Env,
		Tty:     req.Tty,
		Stdin:   req.Stdin,
		Stdout:  true,
		Stderr:  true,
	}

	// Execute command in container
	result, err := a.runtime.ExecContainer(ctx, req.ContainerId, execConfig)
	if err != nil {
		a.logger.Error("Failed to execute command",
			zap.String("container_id", req.ContainerId),
			zap.Strings("command", req.Command),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to execute command: %w", err)
	}

	a.logger.Info("Command executed successfully",
		zap.String("container_id", req.ContainerId),
		zap.Int("exit_code", result.ExitCode),
	)

	return &api.ExecCommandResponse{
		ExitCode: int32(result.ExitCode),
		Stdout:   result.Stdout,
		Stderr:   result.Stderr,
	}, nil
}

// ReportResources reports current resource usage
func (a *Agent) ReportResources(ctx context.Context, req *api.ReportResourcesRequest) (*emptypb.Empty, error) {
	a.logger.Debug("Received resource report",
		zap.Int32("cpu_millicores", req.CpuUsage),
		zap.Int64("memory_bytes", req.MemoryUsage),
	)

	// Store CPU metrics
	a.metricsStorage.RecordMetric(MetricTypeCPU, float64(req.CpuUsage), map[string]string{
		"source": "coordinator",
	})

	// Store memory metrics
	a.metricsStorage.RecordMetric(MetricTypeMemory, float64(req.MemoryUsage), map[string]string{
		"source": "coordinator",
	})

	// Store storage metrics if provided
	if req.StorageUsage > 0 {
		a.metricsStorage.RecordMetric(MetricTypeStorage, float64(req.StorageUsage), map[string]string{
			"source": "coordinator",
		})
	}

	// Store bandwidth metrics if provided
	if req.NetworkUsage > 0 {
		a.metricsStorage.RecordMetric(MetricTypeBandwidth, float64(req.NetworkUsage), map[string]string{
			"source": "coordinator",
		})
	}

	// Store GPU metrics if provided
	if req.GpuUsage > 0 {
		a.metricsStorage.RecordMetric(MetricTypeGPU, float64(req.GpuUsage), map[string]string{
			"source": "coordinator",
		})
	}

	return &emptypb.Empty{}, nil
}

// GetFragments returns the list of workload fragments running on this agent
func (a *Agent) GetFragments(ctx context.Context, req *api.GetFragmentsRequest) (*api.GetFragmentsResponse, error) {
	a.logger.Debug("Getting fragments")

	// List all containers
	containers, err := a.runtime.ListContainers(ctx)
	if err != nil {
		a.logger.Error("Failed to list containers", zap.Error(err))
		return nil, fmt.Errorf("failed to list containers: %w", err)
	}

	// Convert containers to fragments
	fragments := make([]*api.Fragment, 0, len(containers))
	for _, container := range containers {
		// Get container stats
		stats, err := a.runtime.GetContainerStats(ctx, container.ID)
		if err != nil {
			a.logger.Warn("Failed to get container stats",
				zap.String("container_id", container.ID),
				zap.Error(err),
			)
			// Continue without stats
			stats = &runtime.ContainerStats{}
		}

		fragment := &api.Fragment{
			Id:    container.ID,
			State: mapContainerState(container.State),
			Resources: &api.ResourceUsage{
				CpuMillicores: int32(stats.CPUStats.UsageNanoseconds / 1000000),
				MemoryBytes:   int64(stats.MemoryStats.UsageBytes),
			},
			CreatedAt: timestamppb.New(container.CreatedAt),
			// TODO: Map workload information if available from labels/metadata
		}

		fragments = append(fragments, fragment)
	}

	a.logger.Debug("Returning fragments",
		zap.Int("count", len(fragments)),
	)

	return &api.GetFragmentsResponse{
		Fragments: fragments,
	}, nil
}

// mapContainerState converts runtime container state to API state
func mapContainerState(state string) api.ContainerStatus_State {
	switch state {
	case "running":
		return api.ContainerStatus_RUNNING
	case "stopped", "exited":
		return api.ContainerStatus_STOPPED
	case "paused":
		return api.ContainerStatus_PAUSED
	case "restarting":
		return api.ContainerStatus_RESTARTING
	case "created":
		return api.ContainerStatus_CREATED
	default:
		return api.ContainerStatus_UNKNOWN
	}
}
