package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/containerd"
	v1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	v2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"github.com/containerd/cgroups/v3"
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

	// Parse metric.Data based on cgroups version
	// Cgroups v2 (Unified) is the modern cgroups implementation
	// Cgroups v1 (Legacy) is the older implementation
	if cgroups.Mode() == cgroups.Unified {
		// Cgroups v2 parsing
		if v2Metrics, ok := metric.Data.(*v2.Metrics); ok {
			stats = parseV2Metrics(v2Metrics)
			r.logger.Debug("Parsed cgroups v2 metrics",
				zap.String("container", containerID),
				zap.Uint64("cpu_usage_ns", stats.CPUStats.UsageNanoseconds),
				zap.Uint64("memory_usage", stats.MemoryStats.UsageBytes),
			)
		} else {
			r.logger.Warn("Failed to cast metrics to cgroups v2 format",
				zap.String("container", containerID),
			)
		}
	} else {
		// Cgroups v1 parsing
		if v1Metrics, ok := metric.Data.(*v1.Metrics); ok {
			stats = parseV1Metrics(v1Metrics)
			r.logger.Debug("Parsed cgroups v1 metrics",
				zap.String("container", containerID),
				zap.Uint64("cpu_usage_ns", stats.CPUStats.UsageNanoseconds),
				zap.Uint64("memory_usage", stats.MemoryStats.UsageBytes),
			)
		} else {
			r.logger.Warn("Failed to cast metrics to cgroups v1 format",
				zap.String("container", containerID),
			)
		}
	}

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

// parseV1Metrics parses cgroups v1 metrics into ContainerStats
func parseV1Metrics(m *v1.Metrics) *ContainerStats {
	stats := &ContainerStats{
		Timestamp: time.Now(),
	}

	// Parse CPU stats
	if m.CPU != nil && m.CPU.Usage != nil {
		stats.CPUStats = CPUStats{
			UsageNanoseconds:  m.CPU.Usage.Total,
			SystemNanoseconds: m.CPU.Usage.Kernel,
		}
		if m.CPU.Throttling != nil {
			stats.CPUStats.ThrottlingCount = m.CPU.Throttling.ThrottledPeriods
		}
	}

	// Parse memory stats
	if m.Memory != nil {
		stats.MemoryStats = MemoryStats{
			UsageBytes:    m.Memory.Usage.Usage,
			MaxUsageBytes: m.Memory.Usage.Max,
			LimitBytes:    m.Memory.Usage.Limit,
		}
		if m.Memory.Cache != nil {
			stats.MemoryStats.CacheBytes = *m.Memory.Cache
		}
		if m.Memory.RSS != nil {
			stats.MemoryStats.RSSBytes = *m.Memory.RSS
		}
		if m.Memory.Swap != nil {
			stats.MemoryStats.SwapUsageBytes = m.Memory.Swap.Usage
		}
	}

	// Parse network stats
	// Cgroups v1 network stats are aggregated across all interfaces
	if m.Network != nil && len(m.Network) > 0 {
		var rxBytes, rxPackets, rxErrors, rxDropped uint64
		var txBytes, txPackets, txErrors, txDropped uint64

		for _, iface := range m.Network {
			rxBytes += iface.RxBytes
			rxPackets += iface.RxPackets
			rxErrors += iface.RxErrors
			rxDropped += iface.RxDropped
			txBytes += iface.TxBytes
			txPackets += iface.TxPackets
			txErrors += iface.TxErrors
			txDropped += iface.TxDropped
		}

		stats.NetworkStats = NetworkStats{
			RxBytes:   rxBytes,
			RxPackets: rxPackets,
			RxErrors:  rxErrors,
			RxDropped: rxDropped,
			TxBytes:   txBytes,
			TxPackets: txPackets,
			TxErrors:  txErrors,
			TxDropped: txDropped,
		}
	}

	// Parse Block I/O stats
	if m.Blkio != nil {
		var readBytes, writeBytes, readOps, writeOps uint64

		// Aggregate read operations
		for _, entry := range m.Blkio.IoServiceBytesRecursive {
			if entry.Op == "Read" {
				readBytes += entry.Value
			} else if entry.Op == "Write" {
				writeBytes += entry.Value
			}
		}

		// Aggregate I/O operations count
		for _, entry := range m.Blkio.IoServicedRecursive {
			if entry.Op == "Read" {
				readOps += entry.Value
			} else if entry.Op == "Write" {
				writeOps += entry.Value
			}
		}

		stats.BlockIOStats = BlockIOStats{
			ReadBytes:  readBytes,
			WriteBytes: writeBytes,
			ReadOps:    readOps,
			WriteOps:   writeOps,
		}
	}

	return stats
}

// parseV2Metrics parses cgroups v2 metrics into ContainerStats
func parseV2Metrics(m *v2.Metrics) *ContainerStats {
	stats := &ContainerStats{
		Timestamp: time.Now(),
	}

	// Parse CPU stats
	if m.CPU != nil {
		stats.CPUStats = CPUStats{
			UsageNanoseconds:  m.CPU.UsageUsec * 1000, // Convert microseconds to nanoseconds
			SystemNanoseconds: m.CPU.SystemUsec * 1000,
		}
		if m.CPU.NrThrottled != nil {
			stats.CPUStats.ThrottlingCount = *m.CPU.NrThrottled
		}
	}

	// Parse memory stats
	if m.Memory != nil {
		stats.MemoryStats = MemoryStats{
			UsageBytes:     m.Memory.Usage,
			SwapUsageBytes: m.Memory.SwapUsage,
		}
		if m.Memory.UsageLimit != nil {
			stats.MemoryStats.LimitBytes = *m.Memory.UsageLimit
		}
		// Cgroups v2 provides file cache in the memory stats
		if m.Memory.File != nil {
			stats.MemoryStats.CacheBytes = *m.Memory.File
		}
		// RSS in cgroups v2 is called "anon"
		if m.Memory.Anon != nil {
			stats.MemoryStats.RSSBytes = *m.Memory.Anon
		}
	}

	// Parse I/O stats (cgroups v2 combines block I/O)
	if m.Io != nil && m.Io.Usage != nil {
		var readBytes, writeBytes uint64

		for _, entry := range m.Io.Usage {
			readBytes += entry.Rbytes
			writeBytes += entry.Wbytes
		}

		stats.BlockIOStats = BlockIOStats{
			ReadBytes:  readBytes,
			WriteBytes: writeBytes,
			ReadOps:    0, // Cgroups v2 doesn't provide operation counts in the same way
			WriteOps:   0,
		}
	}

	// Note: Cgroups v2 doesn't provide network stats directly
	// Network stats would need to be collected separately via netlink or /proc
	// For now, leave NetworkStats as zero values
	stats.NetworkStats = NetworkStats{}

	return stats
}
