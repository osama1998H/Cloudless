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
	CPUUsagePercent  float64
	CPUCores         int
	CPUMillicores    int64 // Total CPU capacity in millicores
	UsedCPU          int64 // Used CPU in millicores

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

	// Network metrics
	NetworkBandwidth uint64 // Bytes per second
	NetworkRxBytes   uint64
	NetworkTxBytes   uint64
	NetworkRxPackets uint64
	NetworkTxPackets uint64
	NetworkRxErrors  uint64
	NetworkTxErrors  uint64
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
func (m *ResourceMonitor) GetCapacity() ResourceCapacity {
	snapshot := m.GetSnapshot()

	return ResourceCapacity{
		CPUMillicores: snapshot.CPUMillicores,
		MemoryBytes:   int64(snapshot.MemoryTotal),
		StorageBytes:  int64(snapshot.DiskTotal),
		BandwidthBPS:  int64(snapshot.NetworkBandwidth),
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
type ResourceCapacity struct {
	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64
	BandwidthBPS  int64
	GPUCount      int
}

// ResourceUsage represents current resource usage
type ResourceUsage struct {
	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64
	BandwidthBPS  int64
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
