package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd"
	"go.uber.org/zap"
)

// GetContainerStats retrieves resource usage statistics for a container
func (r *ContainerdRuntime) GetContainerStats(ctx context.Context, containerID string) (*ContainerStats, error) {
	ctx = r.withNamespace(ctx)

	container, err := r.client.LoadContainer(ctx, containerID)
	if err != nil {
		return nil, fmt.Errorf("failed to load container: %w", err)
	}

	task, err := container.Task(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get task: %w", err)
	}

	// Get metrics
	metric, err := task.Metrics(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get metrics: %w", err)
	}

	stats := &ContainerStats{
		Timestamp: time.Now(),
	}

	// Note: Metrics parsing would require importing cgroups-specific types
	// For now, return basic stats structure
	// TODO: Parse metric.Data based on cgroups version
	_ = metric // Suppress unused variable warning

	return stats, nil
}

// MonitorContainerStats continuously monitors container stats
func (r *ContainerdRuntime) MonitorContainerStats(ctx context.Context, containerID string, interval time.Duration) (<-chan *ContainerStats, error) {
	statsCh := make(chan *ContainerStats, 10)

	go func() {
		defer close(statsCh)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				stats, err := r.GetContainerStats(ctx, containerID)
				if err != nil {
					r.logger.Warn("Failed to get container stats",
						zap.String("container", containerID),
						zap.Error(err),
					)
					continue
				}

				select {
				case statsCh <- stats:
				case <-ctx.Done():
					return
				}
			}
		}
	}()

	return statsCh, nil
}

// GetResourceUsage calculates the current resource usage across all containers
func (r *ContainerdRuntime) GetResourceUsage(ctx context.Context) (ResourceList, error) {
	ctx = r.withNamespace(ctx)

	containers, err := r.client.Containers(ctx)
	if err != nil {
		return ResourceList{}, fmt.Errorf("failed to list containers: %w", err)
	}

	var totalCPU int64
	var totalMemory int64

	for _, container := range containers {
		task, err := container.Task(ctx, nil)
		if err != nil {
			continue // Skip containers without running tasks
		}

		metric, err := task.Metrics(ctx)
		if err != nil {
			continue
		}

		if cgroups.Mode() == cgroups.Unified {
			if v2Metrics, ok := metric.Data.(*v2.Metrics); ok {
				if v2Metrics.CPU != nil {
					// Convert CPU usage to millicores (approximate)
					totalCPU += int64(v2Metrics.CPU.UsageUsec / 1000)
				}
				if v2Metrics.Memory != nil {
					totalMemory += int64(v2Metrics.Memory.Usage)
				}
			}
		}
	}

	return ResourceList{
		CPUMillicores: totalCPU,
		MemoryBytes:   totalMemory,
	}, nil
}
