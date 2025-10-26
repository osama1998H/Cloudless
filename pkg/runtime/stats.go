package runtime

import (
	"context"
	"fmt"
	"time"

	"github.com/containerd/cgroups/v3"
	v1 "github.com/containerd/cgroups/v3/cgroup1/stats"
	v2 "github.com/containerd/cgroups/v3/cgroup2/stats"
	"go.uber.org/zap"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
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
		// Cgroups v2 parsing - unmarshal from Any
		v2Metrics := &v2.Metrics{}
		if err := anypb.UnmarshalTo(metric.Data, v2Metrics, proto.UnmarshalOptions{}); err == nil {
			stats = parseV2Metrics(v2Metrics)
			r.logger.Debug("Parsed cgroups v2 metrics",
				zap.String("container", containerID),
				zap.Uint64("cpu_usage_ns", stats.CPUStats.UsageNanoseconds),
				zap.Uint64("memory_usage", stats.MemoryStats.UsageBytes),
			)
		} else {
			r.logger.Warn("Failed to unmarshal cgroups v2 metrics",
				zap.String("container", containerID),
				zap.Error(err),
			)
		}
	} else {
		// Cgroups v1 parsing - unmarshal from Any
		v1Metrics := &v1.Metrics{}
		if err := anypb.UnmarshalTo(metric.Data, v1Metrics, proto.UnmarshalOptions{}); err == nil {
			stats = parseV1Metrics(v1Metrics)
			r.logger.Debug("Parsed cgroups v1 metrics",
				zap.String("container", containerID),
				zap.Uint64("cpu_usage_ns", stats.CPUStats.UsageNanoseconds),
				zap.Uint64("memory_usage", stats.MemoryStats.UsageBytes),
			)
		} else {
			r.logger.Warn("Failed to unmarshal cgroups v1 metrics",
				zap.String("container", containerID),
				zap.Error(err),
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
			v2Metrics := &v2.Metrics{}
			if err := anypb.UnmarshalTo(metric.Data, v2Metrics, proto.UnmarshalOptions{}); err == nil {
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
		stats.MemoryStats.CacheBytes = m.Memory.Cache
		stats.MemoryStats.RSSBytes = m.Memory.RSS
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

		// Estimate storage usage from block I/O
		totalIO := readBytes + writeBytes
		stats.StorageStats = StorageStats{
			UsageBytes: totalIO, // Approximation
			IOPSRead:   readOps,
			IOPSWrite:  writeOps,
		}
	}

	// GPU stats are not available in cgroups v1
	// Would need to be collected separately via nvidia-smi or similar
	stats.GPUStats = GPUStats{}

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
		stats.CPUStats.ThrottlingCount = m.CPU.NrThrottled
	}

	// Parse memory stats
	if m.Memory != nil {
		stats.MemoryStats = MemoryStats{
			UsageBytes:     m.Memory.Usage,
			SwapUsageBytes: m.Memory.SwapUsage,
		}
		stats.MemoryStats.LimitBytes = m.Memory.UsageLimit
		// Cgroups v2 provides file cache in the memory stats
		stats.MemoryStats.CacheBytes = m.Memory.File
		// RSS in cgroups v2 is called "anon"
		stats.MemoryStats.RSSBytes = m.Memory.Anon
	}

	// Parse I/O stats (cgroups v2 combines block I/O)
	if m.Io != nil && m.Io.Usage != nil {
		var readBytes, writeBytes, readOps, writeOps uint64

		for _, entry := range m.Io.Usage {
			readBytes += entry.Rbytes
			writeBytes += entry.Wbytes
			readOps += entry.Rios
			writeOps += entry.Wios
		}

		stats.BlockIOStats = BlockIOStats{
			ReadBytes:  readBytes,
			WriteBytes: writeBytes,
			ReadOps:    readOps,
			WriteOps:   writeOps,
		}

		// Estimate storage usage from block I/O
		totalIO := readBytes + writeBytes
		stats.StorageStats = StorageStats{
			UsageBytes: totalIO, // Approximation
			IOPSRead:   readOps,
			IOPSWrite:  writeOps,
		}
	}

	// Note: Cgroups v2 doesn't provide network stats directly
	// Network stats would need to be collected separately via netlink or /proc
	// For now, leave NetworkStats as zero values
	stats.NetworkStats = NetworkStats{}

	// GPU stats are not available in cgroups v2
	// Would need to be collected separately via nvidia-smi or similar
	stats.GPUStats = GPUStats{}

	return stats
}
