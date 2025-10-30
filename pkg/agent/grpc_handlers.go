package agent

import (
	"context"
	"fmt"
	"time"

	"github.com/osama1998H/Cloudless/pkg/api"
	"github.com/osama1998H/Cloudless/pkg/runtime"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// RunContainer starts a new container
func (a *Agent) RunContainer(ctx context.Context, req *api.RunContainerRequest) (*api.RunContainerResponse, error) {
	workload := req.GetWorkload()
	if workload == nil {
		return nil, fmt.Errorf("workload is required")
	}

	spec := workload.GetSpec()
	if spec == nil {
		return nil, fmt.Errorf("workload spec is required")
	}

	a.logger.Info("Running container",
		zap.String("workload_id", workload.Id),
		zap.String("image", spec.Image),
		zap.String("fragment_id", req.FragmentId),
	)

	// Pull image if it doesn't exist
	if err := a.runtime.PullImage(ctx, spec.Image); err != nil {
		a.logger.Warn("Failed to pull image, continuing anyway",
			zap.String("image", spec.Image),
			zap.Error(err),
		)
		// Continue - image might already exist
	}

	// Map volumes
	mounts := make([]runtime.Mount, 0, len(spec.Volumes))
	for _, vol := range spec.Volumes {
		mounts = append(mounts, runtime.Mount{
			Source:      vol.Source,
			Destination: vol.MountPath,
			Type:        "bind",
			ReadOnly:    vol.ReadOnly,
		})
	}

	// Map ports
	ports := make([]runtime.PortMapping, 0, len(spec.Ports))
	for _, port := range spec.Ports {
		ports = append(ports, runtime.PortMapping{
			ContainerPort: port.ContainerPort,
			HostPort:      port.HostPort,
			Protocol:      port.Protocol,
		})
	}

	// Map resources
	resources := runtime.ResourceRequirements{
		Requests: runtime.ResourceList{
			CPUMillicores: spec.Resources.Requests.CpuMillicores,
			MemoryBytes:   spec.Resources.Requests.MemoryBytes,
			StorageBytes:  spec.Resources.Requests.StorageBytes,
		},
		Limits: runtime.ResourceList{
			CPUMillicores: spec.Resources.Limits.CpuMillicores,
			MemoryBytes:   spec.Resources.Limits.MemoryBytes,
			StorageBytes:  spec.Resources.Limits.StorageBytes,
		},
	}

	// Map restart policy
	restartPolicy := runtime.RestartPolicy{
		Name:              "on-failure",
		MaximumRetryCount: int(spec.RestartPolicy.MaxRetries),
	}
	if spec.RestartPolicy.Policy == 0 { // ALWAYS
		restartPolicy.Name = "always"
	} else if spec.RestartPolicy.Policy == 2 { // NEVER
		restartPolicy.Name = "no"
	}

	// Convert API request to runtime spec
	containerSpec := runtime.ContainerSpec{
		ID:      fmt.Sprintf("%s-%s", workload.Id, req.FragmentId),
		Name:    fmt.Sprintf("%s-%s-%s", workload.Namespace, workload.Name, req.FragmentId),
		Image:   spec.Image,
		Command: spec.Command,
		Args:    spec.Args,
		Env:     spec.Env,
		Labels: map[string]string{
			"workload.id":        workload.Id,
			"workload.name":      workload.Name,
			"workload.namespace": workload.Namespace,
			"fragment.id":        req.FragmentId,
			"replica.id":         req.ReplicaId,
		},
		Annotations: workload.Annotations,
		Resources:   resources,
		Network: runtime.NetworkConfig{
			NetworkMode: "bridge",
			Ports:       ports,
		},
		Mounts:        mounts,
		RestartPolicy: restartPolicy,
	}

	// Create the container
	container, err := a.runtime.CreateContainer(ctx, containerSpec)
	if err != nil {
		a.logger.Error("Failed to create container",
			zap.String("image", spec.Image),
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
		zap.String("workload_id", workload.Id),
	)

	return &api.RunContainerResponse{
		ContainerId: container.ID,
		Status:      "running",
	}, nil
}

// StopContainer stops a running container
func (a *Agent) StopContainer(ctx context.Context, req *api.StopContainerRequest) (*emptypb.Empty, error) {
	a.logger.Info("Stopping container",
		zap.String("container_id", req.ContainerId),
	)

	// Determine timeout from grace period
	timeout := 30 * time.Second
	if req.GracePeriod != nil {
		timeout = req.GracePeriod.AsDuration()
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

	// Get container stats
	var usage *api.ResourceUsage
	containerStats, err := a.runtime.GetContainerStats(ctx, req.ContainerId)
	if err != nil {
		a.logger.Warn("Failed to get container stats",
			zap.String("container_id", req.ContainerId),
			zap.Error(err),
		)
		// Continue without stats rather than failing the entire request
	} else {
		// Calculate bandwidth from network stats (total bytes per second approximation)
		bandwidthBps := int64(containerStats.NetworkStats.RxBytes + containerStats.NetworkStats.TxBytes)

		usage = &api.ResourceUsage{
			CpuMillicores: int64(containerStats.CPUStats.UsageNanoseconds / 1000000), // Convert nanoseconds to millicores (approximate)
			MemoryBytes:   int64(containerStats.MemoryStats.UsageBytes),
			StorageBytes:  int64(containerStats.StorageStats.UsageBytes),
			BandwidthBps:  bandwidthBps,
			GpuCount:      containerStats.GPUStats.DeviceCount,
		}
	}

	// Convert to API response
	status := &api.ContainerStatus{
		ContainerId: info.ID,
		State:       mapContainerState(info.State),
		Image:       info.Image,
		Usage:       usage,
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
				Line:      logEntry.Line,
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
	// Convert map to slice of KEY=VALUE strings
	envSlice := make([]string, 0, len(req.Env))
	for k, v := range req.Env {
		envSlice = append(envSlice, fmt.Sprintf("%s=%s", k, v))
	}

	execConfig := runtime.ExecConfig{
		Command: req.Command,
		Env:     envSlice,
		Tty:     req.Tty,
		Stdin:   false, // Not supported in proto
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
	usage := req.GetUsage()
	if usage == nil {
		return &emptypb.Empty{}, nil
	}

	a.logger.Debug("Received resource report",
		zap.Int64("cpu_millicores", usage.CpuMillicores),
		zap.Int64("memory_bytes", usage.MemoryBytes),
	)

	// Store CPU metrics
	a.metricsStorage.RecordMetric(MetricTypeCPU, float64(usage.CpuMillicores), map[string]string{
		"source": "coordinator",
	})

	// Store memory metrics
	a.metricsStorage.RecordMetric(MetricTypeMemory, float64(usage.MemoryBytes), map[string]string{
		"source": "coordinator",
	})

	// Store storage metrics if provided
	if usage.StorageBytes > 0 {
		a.metricsStorage.RecordMetric(MetricTypeStorage, float64(usage.StorageBytes), map[string]string{
			"source": "coordinator",
		})
	}

	// Store bandwidth metrics if provided
	if usage.BandwidthBps > 0 {
		a.metricsStorage.RecordMetric(MetricTypeBandwidth, float64(usage.BandwidthBps), map[string]string{
			"source": "coordinator",
		})
	}

	// Store GPU metrics if provided
	if usage.GpuCount > 0 {
		a.metricsStorage.RecordMetric(MetricTypeGPU, float64(usage.GpuCount), map[string]string{
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

		// Determine fragment status based on container state
		fragmentStatus := &api.FragmentStatus{
			State: api.FragmentStatus_ALLOCATED, // Default to allocated for running containers
		}
		if container.State == runtime.ContainerStateRunning {
			fragmentStatus.State = api.FragmentStatus_ALLOCATED
		} else if container.State == runtime.ContainerStateStopped || container.State == runtime.ContainerStateFailed {
			fragmentStatus.State = api.FragmentStatus_RELEASING
		}

		fragment := &api.Fragment{
			Id:     container.ID,
			Status: fragmentStatus,
			Resources: &api.ResourceCapacity{
				CpuMillicores: int64(stats.CPUStats.UsageNanoseconds / 1000000),
				MemoryBytes:   int64(stats.MemoryStats.UsageBytes),
			},
			CreatedAt: timestamppb.New(container.CreatedAt),
		}

		// Extract workload information from container labels
		if workloadID, ok := container.Labels["workload.id"]; ok {
			fragment.WorkloadId = workloadID
		}
		if fragmentID, ok := container.Labels["fragment.id"]; ok {
			fragment.Id = fragmentID
		}
		if nodeID := a.config.NodeID; nodeID != "" {
			fragment.NodeId = nodeID
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

// mapContainerState converts runtime container state to API state string
func mapContainerState(state runtime.ContainerState) string {
	// ContainerState is already a string type in runtime package
	// Just convert it to string
	return string(state)
}
