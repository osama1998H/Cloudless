package agent

import (
	"testing"
	"time"

	"github.com/shirou/gopsutil/v3/disk"
	"go.uber.org/zap"
)

// CLD-REQ-010: Test suite for node resource reporting
// Tests verify that each node reports available CPU (millicores), RAM (MiB),
// storage (GiB, IOPS class), and egress bandwidth (Mbps)

// TestResourceMonitor_GetCapacity_ReturnsAllMetrics verifies that GetCapacity
// returns all required metrics for CLD-REQ-010
func TestResourceMonitor_GetCapacity_ReturnsAllMetrics(t *testing.T) {
	// CLD-REQ-010: Verify all required capacity metrics are reported
	logger := zap.NewNop()
	config := MonitorConfig{
		Interval: 1 * time.Second,
		DiskPath: "/",
	}

	monitor := NewResourceMonitor(config, logger)
	if err := monitor.Start(); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop()

	// Wait for initial snapshot
	time.Sleep(100 * time.Millisecond)

	capacity := monitor.GetCapacity()

	// CLD-REQ-010: Verify CPU capacity in millicores
	if capacity.CPUMillicores <= 0 {
		t.Errorf("Expected CPU millicores > 0, got %d", capacity.CPUMillicores)
	}

	// CLD-REQ-010: Verify memory capacity in bytes (convertible to MiB)
	if capacity.MemoryBytes <= 0 {
		t.Errorf("Expected memory bytes > 0, got %d", capacity.MemoryBytes)
	}
	memoryMiB := MemoryMiB(uint64(capacity.MemoryBytes))
	if memoryMiB <= 0 {
		t.Errorf("Expected memory MiB > 0, got %d", memoryMiB)
	}

	// CLD-REQ-010: Verify storage capacity in bytes (convertible to GiB)
	if capacity.StorageBytes <= 0 {
		t.Errorf("Expected storage bytes > 0, got %d", capacity.StorageBytes)
	}
	storageGiB := StorageGiB(uint64(capacity.StorageBytes))
	if storageGiB <= 0 {
		t.Errorf("Expected storage GiB > 0, got %d", storageGiB)
	}

	// CLD-REQ-010: Verify IOPS class is reported
	validClasses := map[string]bool{
		"low":     true,
		"medium":  true,
		"high":    true,
		"premium": true,
		"unknown": true,
	}
	if !validClasses[capacity.IOPSClass] {
		t.Errorf("Expected valid IOPS class, got %s", capacity.IOPSClass)
	}

	// CLD-REQ-010: Verify bandwidth in bps (convertible to Mbps)
	// Note: Bandwidth may be 0 on first snapshot before network stats are collected
	if capacity.BandwidthBPS < 0 {
		t.Errorf("Expected bandwidth >= 0, got %d", capacity.BandwidthBPS)
	}

	t.Logf("Capacity metrics - CPU: %d millicores, RAM: %d MiB, Storage: %d GiB, IOPS: %s, Bandwidth: %d Mbps",
		capacity.CPUMillicores,
		memoryMiB,
		storageGiB,
		capacity.IOPSClass,
		BandwidthMbps(uint64(capacity.BandwidthBPS)),
	)
}

// TestResourceMonitor_ClassifyIOPS_ReturnsCorrectTiers tests IOPS classification
func TestResourceMonitor_ClassifyIOPS_ReturnsCorrectTiers(t *testing.T) {
	// CLD-REQ-010: Test IOPS classification into performance tiers
	logger := zap.NewNop()
	config := MonitorConfig{Interval: 1 * time.Second}
	monitor := NewResourceMonitor(config, logger)

	tests := []struct {
		name          string
		iops          uint64
		expectedClass string
	}{
		{"Zero IOPS", 0, "unknown"},
		{"Low IOPS - spinning disk", 50, "low"},
		{"Low IOPS - boundary", 99, "low"},
		{"Medium IOPS - consumer SSD", 100, "medium"},
		{"Medium IOPS - fast HDD", 300, "medium"},
		{"Medium IOPS - boundary", 499, "medium"},
		{"High IOPS - enterprise SSD", 500, "high"},
		{"High IOPS - fast SSD", 1500, "high"},
		{"High IOPS - boundary", 2999, "high"},
		{"Premium IOPS - NVMe", 3000, "premium"},
		{"Premium IOPS - high-end NVMe", 10000, "premium"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := monitor.classifyIOPS(tt.iops)
			if got != tt.expectedClass {
				t.Errorf("classifyIOPS(%d) = %s; want %s", tt.iops, got, tt.expectedClass)
			}
		})
	}
}

// TestResourceMonitor_CalculateIOPS_HandlesEmptyCounters tests IOPS calculation with empty input
func TestResourceMonitor_CalculateIOPS_HandlesEmptyCounters(t *testing.T) {
	// CLD-REQ-010: Verify graceful handling of missing IOPS data
	logger := zap.NewNop()
	config := MonitorConfig{Interval: 1 * time.Second}
	monitor := NewResourceMonitor(config, logger)

	tests := []struct {
		name     string
		counters map[string]disk.IOCountersStat
		wantZero bool
	}{
		{
			name:     "Empty counters",
			counters: map[string]disk.IOCountersStat{},
			wantZero: true,
		},
		{
			name: "Counter with no operations",
			counters: map[string]disk.IOCountersStat{
				"disk0": {
					ReadCount:  0,
					WriteCount: 0,
					ReadTime:   0,
					WriteTime:  0,
				},
			},
			wantZero: true,
		},
		{
			name: "Counter with merged operations",
			counters: map[string]disk.IOCountersStat{
				"disk0": {
					ReadCount:        1000,
					WriteCount:       1000,
					ReadTime:         100,
					WriteTime:        100,
					MergedReadCount:  10000,
					MergedWriteCount: 10000,
				},
			},
			wantZero: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			iops := monitor.calculateIOPS(tt.counters)
			if tt.wantZero && iops != 0 {
				t.Errorf("Expected IOPS = 0, got %d", iops)
			}
			if !tt.wantZero && iops == 0 {
				t.Errorf("Expected IOPS > 0, got 0")
			}
		})
	}
}

// TestResourceMonitor_UnitConversions tests conversion helpers for CLD-REQ-010
func TestResourceMonitor_UnitConversions(t *testing.T) {
	// CLD-REQ-010: Test unit conversions for spec compliance
	tests := []struct {
		name     string
		testFunc func(t *testing.T)
	}{
		{
			name: "Memory bytes to MiB",
			testFunc: func(t *testing.T) {
				tests := []struct {
					bytes uint64
					want  int64
				}{
					{0, 0},
					{1024 * 1024, 1},           // 1 MiB
					{1024 * 1024 * 1024, 1024}, // 1 GiB = 1024 MiB
					{1536 * 1024 * 1024, 1536}, // 1.5 GiB = 1536 MiB
				}
				for _, tt := range tests {
					got := MemoryMiB(tt.bytes)
					if got != tt.want {
						t.Errorf("MemoryMiB(%d) = %d; want %d", tt.bytes, got, tt.want)
					}
				}
			},
		},
		{
			name: "Storage bytes to GiB",
			testFunc: func(t *testing.T) {
				tests := []struct {
					bytes uint64
					want  int64
				}{
					{0, 0},
					{1024 * 1024 * 1024, 1},           // 1 GiB
					{500 * 1024 * 1024 * 1024, 500},   // 500 GiB
					{1024 * 1024 * 1024 * 1024, 1024}, // 1 TiB = 1024 GiB
				}
				for _, tt := range tests {
					got := StorageGiB(tt.bytes)
					if got != tt.want {
						t.Errorf("StorageGiB(%d) = %d; want %d", tt.bytes, got, tt.want)
					}
				}
			},
		},
		{
			name: "Bandwidth bps to Mbps",
			testFunc: func(t *testing.T) {
				tests := []struct {
					bps  uint64
					want int64
				}{
					{0, 0},
					{1_000_000, 8},      // 1 MB/s = 8 Mbps
					{10_000_000, 80},    // 10 MB/s = 80 Mbps
					{125_000_000, 1000}, // 125 MB/s = 1000 Mbps (1 Gbps)
				}
				for _, tt := range tests {
					got := BandwidthMbps(tt.bps)
					if got != tt.want {
						t.Errorf("BandwidthMbps(%d) = %d; want %d", tt.bps, got, tt.want)
					}
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.testFunc)
	}
}

// TestResourceMonitor_Snapshot_IncludesEgressBandwidth verifies egress bandwidth is tracked
func TestResourceMonitor_Snapshot_IncludesEgressBandwidth(t *testing.T) {
	// CLD-REQ-010: Verify egress (transmit) bandwidth is calculated separately
	logger := zap.NewNop()
	config := MonitorConfig{
		Interval: 100 * time.Millisecond,
		DiskPath: "/",
	}

	monitor := NewResourceMonitor(config, logger)
	if err := monitor.Start(); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop()

	// Wait for at least two snapshots to calculate bandwidth delta
	time.Sleep(300 * time.Millisecond)

	snapshot := monitor.GetSnapshot()

	// Verify egress bandwidth field exists and is >= 0
	if snapshot.NetworkEgressBandwidth < 0 {
		t.Errorf("Expected egress bandwidth >= 0, got %d", snapshot.NetworkEgressBandwidth)
	}

	// Verify total bandwidth includes both rx and tx
	if snapshot.NetworkBandwidth < snapshot.NetworkEgressBandwidth {
		t.Errorf("Total bandwidth (%d) should be >= egress bandwidth (%d)",
			snapshot.NetworkBandwidth, snapshot.NetworkEgressBandwidth)
	}

	t.Logf("Bandwidth metrics - Total: %d bps, Egress: %d bps",
		snapshot.NetworkBandwidth, snapshot.NetworkEgressBandwidth)
}

// TestResourceMonitor_Snapshot_IncludesIOPSMetrics verifies IOPS metrics are collected
func TestResourceMonitor_Snapshot_IncludesIOPSMetrics(t *testing.T) {
	// CLD-REQ-010: Verify IOPS metrics are included in resource snapshot
	logger := zap.NewNop()
	config := MonitorConfig{
		Interval: 100 * time.Millisecond,
		DiskPath: "/",
	}

	monitor := NewResourceMonitor(config, logger)
	if err := monitor.Start(); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop()

	// Wait for initial snapshot
	time.Sleep(200 * time.Millisecond)

	snapshot := monitor.GetSnapshot()

	// Verify IOPS fields are populated
	if snapshot.DiskIOPS < 0 {
		t.Errorf("Expected IOPS >= 0, got %d", snapshot.DiskIOPS)
	}

	validClasses := map[string]bool{
		"low": true, "medium": true, "high": true, "premium": true, "unknown": true,
	}
	if !validClasses[snapshot.DiskIOPSClass] {
		t.Errorf("Expected valid IOPS class, got %s", snapshot.DiskIOPSClass)
	}

	t.Logf("IOPS metrics - IOPS: %d, Class: %s", snapshot.DiskIOPS, snapshot.DiskIOPSClass)
}
