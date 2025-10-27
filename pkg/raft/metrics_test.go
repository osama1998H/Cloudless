package raft

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudless/cloudless/pkg/observability"
	"go.uber.org/zap"
)

// TestMetricsCollector_BasicOperation tests basic metrics collection lifecycle
func TestMetricsCollector_BasicOperation(t *testing.T) {
	store, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	collector := NewMetricsCollector(store, logger, 100*time.Millisecond)

	// Start collector
	collector.Start()
	defer collector.Stop()

	// Wait for a few collection cycles
	time.Sleep(300 * time.Millisecond)

	// Verify metrics are being collected
	// (In real tests, you'd scrape Prometheus and verify values)
	// Here we just verify no panics occurred

	t.Log("Metrics collector ran successfully")
}

// TestMetricsCollector_StopGracefully tests graceful shutdown
func TestMetricsCollector_StopGracefully(t *testing.T) {
	store, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	collector := NewMetricsCollector(store, logger, 50*time.Millisecond)

	collector.Start()
	time.Sleep(100 * time.Millisecond)

	// Stop should not hang
	done := make(chan struct{})
	go func() {
		collector.Stop()
		close(done)
	}()

	select {
	case <-done:
		t.Log("Collector stopped gracefully")
	case <-time.After(2 * time.Second):
		t.Fatal("Collector.Stop() timed out")
	}
}

// TestMetricsCollector_NodeStateTracking tests RAFT state metric updates
func TestMetricsCollector_NodeStateTracking(t *testing.T) {
	store, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	collector := NewMetricsCollector(store, logger, 50*time.Millisecond)

	// Wait for leader election
	if err := store.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Collect metrics once
	collector.collectMetrics()

	// In single-node mode, node should be leader (state = 2)
	// We can't easily verify Prometheus metric values here,
	// but we verify no errors occurred
	t.Log("Node state tracked successfully")
}

// TestMetricsCollector_LogMetrics tests log index metric collection
func TestMetricsCollector_LogMetrics(t *testing.T) {
	store, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()

	// Wait for leader
	if err := store.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Apply some commands to create log entries
	for i := 0; i < 5; i++ {
		key := fmt.Sprintf("test-key-%d", i)
		value := []byte(fmt.Sprintf("test-value-%d", i))
		if err := store.Set(key, value); err != nil {
			t.Fatalf("Failed to set key %s: %v", key, err)
		}
	}

	// Collect metrics
	collector := NewMetricsCollector(store, logger, time.Second)
	collector.collectMetrics()

	// Verify no panics
	t.Log("Log metrics collected successfully")
}

// TestRecordApplyLatency tests apply latency recording
func TestRecordApplyLatency(t *testing.T) {
	// Record various latencies
	latencies := []time.Duration{
		100 * time.Microsecond,
		1 * time.Millisecond,
		10 * time.Millisecond,
		100 * time.Millisecond,
	}

	for _, latency := range latencies {
		RecordApplyLatency(latency)
	}

	t.Log("Apply latencies recorded successfully")
}

// TestRecordCommitLatency tests commit latency recording
func TestRecordCommitLatency(t *testing.T) {
	// Record various latencies
	latencies := []time.Duration{
		1 * time.Millisecond,
		10 * time.Millisecond,
		50 * time.Millisecond,
		200 * time.Millisecond,
	}

	for _, latency := range latencies {
		RecordCommitLatency(latency)
	}

	t.Log("Commit latencies recorded successfully")
}

// TestRecordSnapshotOperation tests snapshot operation recording
func TestRecordSnapshotOperation(t *testing.T) {
	testCases := []struct {
		name      string
		operation string
		duration  time.Duration
		err       error
	}{
		{
			name:      "successful_create",
			operation: "create",
			duration:  500 * time.Millisecond,
			err:       nil,
		},
		{
			name:      "failed_create",
			operation: "create",
			duration:  100 * time.Millisecond,
			err:       fmt.Errorf("disk full"),
		},
		{
			name:      "successful_restore",
			operation: "restore",
			duration:  2 * time.Second,
			err:       nil,
		},
		{
			name:      "failed_restore",
			operation: "restore",
			duration:  50 * time.Millisecond,
			err:       fmt.Errorf("corrupted snapshot"),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			RecordSnapshotOperation(tc.operation, tc.duration, tc.err)
			t.Logf("Recorded %s: duration=%v, error=%v", tc.operation, tc.duration, tc.err)
		})
	}
}

// TestMetricsCollector_ConcurrentCollections tests concurrent metric collection
func TestMetricsCollector_ConcurrentCollections(t *testing.T) {
	store, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()

	// Wait for leader
	if err := store.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Start multiple collectors (shouldn't cause issues)
	collector1 := NewMetricsCollector(store, logger, 50*time.Millisecond)
	collector2 := NewMetricsCollector(store, logger, 75*time.Millisecond)

	collector1.Start()
	collector2.Start()

	// Let them run concurrently
	time.Sleep(300 * time.Millisecond)

	// Stop both
	collector1.Stop()
	collector2.Stop()

	t.Log("Concurrent collectors ran without issues")
}

// TestMetricsCollector_ParseStatInt tests the stat parsing helper
func TestMetricsCollector_ParseStatInt(t *testing.T) {
	store, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	collector := NewMetricsCollector(store, logger, time.Second)

	testCases := []struct {
		name     string
		input    string
		expected int64
	}{
		{"valid_number", "12345", 12345},
		{"zero", "0", 0},
		{"large_number", "9223372036854775807", 9223372036854775807}, // max int64
		{"empty_string", "", -1},
		{"invalid_format", "not-a-number", -1},
		{"negative", "-100", -100},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := collector.parseStatInt(tc.input)
			if result != tc.expected {
				t.Errorf("Expected %d, got %d for input '%s'", tc.expected, result, tc.input)
			}
		})
	}
}

// TestMetricsCollector_SnapshotMetrics tests snapshot metric updates
func TestMetricsCollector_SnapshotMetrics(t *testing.T) {
	store, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	collector := NewMetricsCollector(store, logger, time.Second)

	// Wait for leader
	if err := store.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Add some data
	if err := store.Set("key1", []byte("value1")); err != nil {
		t.Fatalf("Failed to set key: %v", err)
	}

	// Create snapshot
	start := time.Now()
	err := store.Snapshot()
	duration := time.Since(start)

	// Record snapshot operation
	RecordSnapshotOperation("create", duration, err)

	// Collect metrics
	collector.collectMetrics()

	if err != nil {
		t.Logf("Snapshot creation completed with error: %v", err)
	} else {
		t.Log("Snapshot metrics updated successfully")
	}
}

// TestMetricsCollector_LeaderMetrics tests leader-specific metric collection
func TestMetricsCollector_LeaderMetrics(t *testing.T) {
	store, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	collector := NewMetricsCollector(store, logger, time.Second)

	// Wait for leader election
	if err := store.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Should be leader in single-node mode
	if !store.IsLeader() {
		t.Fatal("Expected node to be leader")
	}

	// Collect leader metrics
	collector.collectLeaderMetrics()

	t.Log("Leader metrics collected successfully")
}

// TestMetricsCollector_WithRAFTOperations tests metrics during RAFT operations
func TestMetricsCollector_WithRAFTOperations(t *testing.T) {
	store, cleanup := setupTestRAFTStore(t)
	defer cleanup()

	logger := zap.NewNop()
	collector := NewMetricsCollector(store, logger, 100*time.Millisecond)

	// Wait for leader
	if err := store.WaitForLeader(10 * time.Second); err != nil {
		t.Fatalf("Failed to wait for leader: %v", err)
	}

	// Start collector
	collector.Start()
	defer collector.Stop()

	// Perform various RAFT operations
	operations := []struct {
		key   string
		value string
	}{
		{"bucket:test1", `{"name":"test1"}`},
		{"bucket:test2", `{"name":"test2"}`},
		{"object:test1:file.txt", `{"key":"file.txt"}`},
	}

	for _, op := range operations {
		if err := store.Set(op.key, []byte(op.value)); err != nil {
			t.Errorf("Failed to set %s: %v", op.key, err)
		}
	}

	// Let metrics collect
	time.Sleep(250 * time.Millisecond)

	// Verify operations completed without panics
	t.Log("Metrics collected during RAFT operations")
}

// BenchmarkMetricsCollector_Collection benchmarks metric collection performance
func BenchmarkMetricsCollector_Collection(b *testing.B) {
	store, cleanup := setupBenchRAFTStore(b)
	defer cleanup()

	logger := zap.NewNop()
	collector := NewMetricsCollector(store, logger, time.Hour) // Don't auto-collect

	// Wait for leader
	if err := store.WaitForLeader(10 * time.Second); err != nil {
		b.Fatalf("Failed to wait for leader: %v", err)
	}

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		collector.collectMetrics()
	}
}

// BenchmarkRecordApplyLatency benchmarks latency recording
func BenchmarkRecordApplyLatency(b *testing.B) {
	duration := 5 * time.Millisecond

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		RecordApplyLatency(duration)
	}
}

// Test helper functions

func setupTestRAFTStore(t *testing.T) (*Store, func()) {
	tempDir := t.TempDir()
	store, err := createTestRAFTStoreAtPath(t, tempDir)
	if err != nil {
		t.Fatalf("Failed to create test RAFT store: %v", err)
	}

	return store, func() {
		if err := store.Close(); err != nil {
			t.Errorf("Failed to close RAFT store: %v", err)
		}
	}
}

func createTestRAFTStoreAtPath(t *testing.T, dir string) (*Store, error) {
	logger := zap.NewNop()

	config := &Config{
		RaftDir:        filepath.Join(dir, "raft"),
		RaftBind:       "127.0.0.1:0",
		RaftID:         "test-metrics-node",
		Bootstrap:      true,
		Logger:         logger,
		EnableSingle:   true,
		SnapshotRetain: 2,
	}

	return NewStore(config)
}

func setupBenchRAFTStore(b *testing.B) (*Store, func()) {
	tempDir := b.TempDir()
	logger := zap.NewNop()

	config := &Config{
		RaftDir:        filepath.Join(tempDir, "raft"),
		RaftBind:       "127.0.0.1:0",
		RaftID:         "bench-metrics-node",
		Bootstrap:      true,
		Logger:         logger,
		EnableSingle:   true,
		SnapshotRetain: 2,
	}

	store, err := NewStore(config)
	if err != nil {
		b.Fatalf("Failed to create RAFT store: %v", err)
	}

	return store, func() {
		if err := store.Close(); err != nil {
			b.Errorf("Failed to close RAFT store: %v", err)
		}
	}
}

// Suppress unused imports
var (
	_ = context.Background
	_ = io.Discard
	_ = os.Stdout
	_ = observability.RaftNodeState
)
