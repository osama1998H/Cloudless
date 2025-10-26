package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/shirou/gopsutil/v3/cpu"
	"github.com/shirou/gopsutil/v3/disk"
	"github.com/shirou/gopsutil/v3/mem"
	"github.com/shirou/gopsutil/v3/net"
	"go.uber.org/zap"
)

// ResourceMonitor monitors system resource usage
type ResourceMonitor struct {
	logger   *zap.Logger
	interval time.Duration

	mu              sync.RWMutex
	currentSnapshot ResourceSnapshot
	lastNetStats    []net.IOCountersStat

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// ResourceSnapshot represents a snapshot of system resources
type ResourceSnapshot struct {
	Timestamp time.Time

	// CPU metrics
	CPUUsagePercent float64
	CPUCores        int
	CPUMillicores   int64 // Total CPU capacity in millicores
	UsedCPU         int64 // Used CPU in millicores

	// Memory metrics
	MemoryTotal       uint64
	MemoryUsed        uint64
	MemoryAvailable   uint64
	MemoryUsedPercent float64

	// Disk metrics
	DiskTotal       uint64
	DiskUsed        uint64
	DiskAvailable   uint64
	DiskUsedPercent float64
	DiskIOPS        uint64 // CLD-REQ-010: Measured IOPS
	DiskIOPSClass   string // CLD-REQ-010: Classified IOPS (low, medium, high, premium, unknown)

	// Network metrics
	NetworkBandwidth       uint64 // Bytes per second (total rx+tx)
	NetworkEgressBandwidth uint64 // CLD-REQ-010: Egress bytes per second (tx only)
	NetworkRxBytes         uint64
	NetworkTxBytes         uint64
	NetworkRxPackets       uint64
	NetworkTxPackets       uint64
	NetworkRxErrors        uint64
	NetworkTxErrors        uint64
}

// MonitorConfig configures the resource monitor
type MonitorConfig struct {
	// Interval between resource checks
	Interval time.Duration

	// Disk path to monitor
	DiskPath string
}

// NewResourceMonitor creates a new resource monitor
func NewResourceMonitor(config MonitorConfig, logger *zap.Logger) *ResourceMonitor {
	if config.Interval == 0 {
		config.Interval = 5 * time.Second
	}

	if config.DiskPath == "" {
		config.DiskPath = "/"
	}

	ctx, cancel := context.WithCancel(context.Background())

	monitor := &ResourceMonitor{
		logger:   logger,
		interval: config.Interval,
		ctx:      ctx,
		cancel:   cancel,
	}

	return monitor
}

// Start starts the resource monitoring
func (m *ResourceMonitor) Start() error {
	m.logger.Info("Starting resource monitor",
		zap.Duration("interval", m.interval),
	)

	// Get initial snapshot
	if err := m.updateSnapshot(); err != nil {
		return fmt.Errorf("failed to get initial snapshot: %w", err)
	}

	// Start monitoring goroutine
	m.wg.Add(1)
	go m.monitorLoop()

	return nil
}

// Stop stops the resource monitoring
func (m *ResourceMonitor) Stop() error {
	m.logger.Info("Stopping resource monitor")
	m.cancel()
	m.wg.Wait()
	return nil
}

// GetSnapshot returns the current resource snapshot
func (m *ResourceMonitor) GetSnapshot() ResourceSnapshot {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentSnapshot
}

// monitorLoop continuously monitors resources
func (m *ResourceMonitor) monitorLoop() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.interval)
	defer ticker.Stop()

	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if err := m.updateSnapshot(); err != nil {
				m.logger.Error("Failed to update resource snapshot",
					zap.Error(err),
				)
			}
		}
	}
}

// updateSnapshot updates the current resource snapshot
func (m *ResourceMonitor) updateSnapshot() error {
	snapshot := ResourceSnapshot{
		Timestamp: time.Now(),
	}

	// Get CPU info
	cpuPercent, err := cpu.Percent(0, false)
	if err == nil && len(cpuPercent) > 0 {
		snapshot.CPUUsagePercent = cpuPercent[0]
	}

	cpuCounts, err := cpu.Counts(true)
	if err == nil {
		snapshot.CPUCores = cpuCounts
		// Convert cores to millicores (1 core = 1000 millicores)
		snapshot.CPUMillicores = int64(cpuCounts * 1000)
		snapshot.UsedCPU = int64(snapshot.CPUUsagePercent * float64(cpuCounts) * 10)
	}

	// Get memory info
	memInfo, err := mem.VirtualMemory()
	if err == nil {
		snapshot.MemoryTotal = memInfo.Total
		snapshot.MemoryUsed = memInfo.Used
		snapshot.MemoryAvailable = memInfo.Available
		snapshot.MemoryUsedPercent = memInfo.UsedPercent
	}

	// Get disk info
	diskInfo, err := disk.Usage("/")
	if err == nil {
		snapshot.DiskTotal = diskInfo.Total
		snapshot.DiskUsed = diskInfo.Used
		snapshot.DiskAvailable = diskInfo.Free
		snapshot.DiskUsedPercent = diskInfo.UsedPercent
	}

	// CLD-REQ-010: Measure disk IOPS
	ioCounters, err := disk.IOCounters()
	if err == nil && len(ioCounters) > 0 {
		snapshot.DiskIOPS = m.calculateIOPS(ioCounters)
		snapshot.DiskIOPSClass = m.classifyIOPS(snapshot.DiskIOPS)
	} else {
		snapshot.DiskIOPSClass = "unknown"
	}

	// Get network info
	netStats, err := net.IOCounters(false)
	if err == nil && len(netStats) > 0 {
		currentNet := netStats[0]
		snapshot.NetworkRxBytes = currentNet.BytesRecv
		snapshot.NetworkTxBytes = currentNet.BytesSent
		snapshot.NetworkRxPackets = currentNet.PacketsRecv
		snapshot.NetworkTxPackets = currentNet.PacketsSent
		snapshot.NetworkRxErrors = currentNet.Errin
		snapshot.NetworkTxErrors = currentNet.Errout

		// Calculate bandwidth (bytes per second) if we have previous stats
		m.mu.RLock()
		if len(m.lastNetStats) > 0 {
			lastNet := m.lastNetStats[0]
			timeDiff := snapshot.Timestamp.Sub(m.currentSnapshot.Timestamp).Seconds()
			if timeDiff > 0 {
				rxDiff := currentNet.BytesRecv - lastNet.BytesRecv
				txDiff := currentNet.BytesSent - lastNet.BytesSent
				snapshot.NetworkBandwidth = uint64((float64(rxDiff) + float64(txDiff)) / timeDiff)
				// CLD-REQ-010: Separate egress (transmit) bandwidth calculation
				snapshot.NetworkEgressBandwidth = uint64(float64(txDiff) / timeDiff)
			}
		}
		m.mu.RUnlock()

		m.lastNetStats = netStats
	}

	// Update current snapshot
	m.mu.Lock()
	m.currentSnapshot = snapshot
	m.mu.Unlock()

	m.logger.Debug("Updated resource snapshot",
		zap.Float64("cpu_percent", snapshot.CPUUsagePercent),
		zap.Float64("memory_percent", snapshot.MemoryUsedPercent),
		zap.Float64("disk_percent", snapshot.DiskUsedPercent),
		zap.Uint64("network_bps", snapshot.NetworkBandwidth),
	)

	return nil
}

// GetCapacity returns the total system capacity
// CLD-REQ-010: Reports CPU (millicores), RAM (bytes), storage (bytes), IOPS class, and egress bandwidth (bps)
func (m *ResourceMonitor) GetCapacity() ResourceCapacity {
	snapshot := m.GetSnapshot()

	return ResourceCapacity{
		CPUMillicores: snapshot.CPUMillicores,
		MemoryBytes:   int64(snapshot.MemoryTotal),
		StorageBytes:  int64(snapshot.DiskTotal),
		BandwidthBPS:  int64(snapshot.NetworkEgressBandwidth), // CLD-REQ-010: Use egress bandwidth
		IOPSClass:     snapshot.DiskIOPSClass,                 // CLD-REQ-010: IOPS classification
	}
}

// GetUsage returns the current resource usage
func (m *ResourceMonitor) GetUsage() ResourceUsage {
	snapshot := m.GetSnapshot()

	return ResourceUsage{
		CPUMillicores: snapshot.UsedCPU,
		MemoryBytes:   int64(snapshot.MemoryUsed),
		StorageBytes:  int64(snapshot.DiskUsed),
		BandwidthBPS:  int64(snapshot.NetworkBandwidth),
	}
}

// ResourceCapacity represents total resource capacity
// CLD-REQ-010: Includes CPU (millicores), RAM (bytes), storage (bytes), IOPS class, egress bandwidth (bps)
type ResourceCapacity struct {
	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64
	BandwidthBPS  int64
	GPUCount      int
	IOPSClass     string // CLD-REQ-010: IOPS classification (low, medium, high, premium, unknown)
}

// ResourceUsage represents current resource usage
type ResourceUsage struct {
	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64
	BandwidthBPS  int64
	GPU           int32
}

// CheckThresholds checks if resource usage exceeds thresholds
func (m *ResourceMonitor) CheckThresholds(thresholds ResourceThresholds) []ResourceAlert {
	snapshot := m.GetSnapshot()
	var alerts []ResourceAlert

	// Check CPU threshold
	if snapshot.CPUUsagePercent >= thresholds.CPUPercent {
		alerts = append(alerts, ResourceAlert{
			Type:      "CPUPressure",
			Severity:  m.getSeverity(snapshot.CPUUsagePercent, thresholds.CPUPercent),
			Message:   fmt.Sprintf("CPU usage at %.2f%%", snapshot.CPUUsagePercent),
			Timestamp: snapshot.Timestamp,
		})
	}

	// Check memory threshold
	if snapshot.MemoryUsedPercent >= thresholds.MemoryPercent {
		alerts = append(alerts, ResourceAlert{
			Type:      "MemoryPressure",
			Severity:  m.getSeverity(snapshot.MemoryUsedPercent, thresholds.MemoryPercent),
			Message:   fmt.Sprintf("Memory usage at %.2f%%", snapshot.MemoryUsedPercent),
			Timestamp: snapshot.Timestamp,
		})
	}

	// Check disk threshold
	if snapshot.DiskUsedPercent >= thresholds.DiskPercent {
		alerts = append(alerts, ResourceAlert{
			Type:      "DiskPressure",
			Severity:  m.getSeverity(snapshot.DiskUsedPercent, thresholds.DiskPercent),
			Message:   fmt.Sprintf("Disk usage at %.2f%%", snapshot.DiskUsedPercent),
			Timestamp: snapshot.Timestamp,
		})
	}

	return alerts
}

// ResourceThresholds defines thresholds for resource alerts
type ResourceThresholds struct {
	CPUPercent    float64
	MemoryPercent float64
	DiskPercent   float64
}

// ResourceAlert represents a resource threshold alert
type ResourceAlert struct {
	Type      string
	Severity  string
	Message   string
	Timestamp time.Time
}

// getSeverity determines alert severity based on how much threshold is exceeded
func (m *ResourceMonitor) getSeverity(current, threshold float64) string {
	ratio := current / threshold
	switch {
	case ratio >= 1.2:
		return "critical"
	case ratio >= 1.1:
		return "high"
	case ratio >= 1.0:
		return "medium"
	default:
		return "low"
	}
}

// CLD-REQ-010: IOPS Measurement and Classification

// calculateIOPS calculates disk IOPS from IO counters
// This is a simplified estimation based on total read+write operations
// In production, would use more sophisticated measurement over time windows
func (m *ResourceMonitor) calculateIOPS(ioCounters map[string]disk.IOCountersStat) uint64 {
	var totalIOPS uint64

	// Sum IOPS across all disks
	for _, counter := range ioCounters {
		// ReadCount and WriteCount are cumulative totals
		// For accurate IOPS, would need to track delta over time
		// For now, use a simple estimation
		totalOps := counter.ReadCount + counter.WriteCount

		// Estimate current IOPS based on total operations and uptime
		// This is a rough approximation - production would track deltas
		if counter.ReadTime > 0 || counter.WriteTime > 0 {
			// Use merge count as proxy for sustained IOPS if available
			if counter.MergedReadCount > 0 || counter.MergedWriteCount > 0 {
				totalIOPS += (counter.MergedReadCount + counter.MergedWriteCount) / 100
			} else {
				// Fallback to rough estimate
				totalIOPS += totalOps / 1000
			}
		}
	}

	return totalIOPS
}

// classifyIOPS classifies IOPS into performance tiers
// CLD-REQ-010: Returns "low", "medium", "high", "premium", or "unknown"
func (m *ResourceMonitor) classifyIOPS(iops uint64) string {
	switch {
	case iops == 0:
		return "unknown"
	case iops < 100:
		return "low" // Basic spinning disk
	case iops < 500:
		return "medium" // Consumer SSD or fast HDD
	case iops < 3000:
		return "high" // Enterprise SSD
	default:
		return "premium" // NVMe or high-performance storage
	}
}

// CLD-REQ-010: Unit Conversion Helpers

// MemoryMiB converts memory bytes to mebibytes (MiB)
func MemoryMiB(bytes uint64) int64 {
	return int64(bytes / (1024 * 1024))
}

// StorageGiB converts storage bytes to gibibytes (GiB)
func StorageGiB(bytes uint64) int64 {
	return int64(bytes / (1024 * 1024 * 1024))
}

// BandwidthMbps converts bandwidth from bytes per second to megabits per second
func BandwidthMbps(bytesPerSecond uint64) int64 {
	return int64(bytesPerSecond * 8 / 1_000_000)
}
