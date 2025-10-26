package raft

import (
	"bytes"
	"encoding/json"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/raft"
	"go.uber.org/zap"
)

// CLD-REQ-012: Test suite for RAFT FSM (Finite State Machine)
// Tests verify that the FSM correctly applies commands, maintains consistency,
// and supports snapshot/restore for VRN inventory durability

// TestFSM_ApplySet_StoresValue verifies CLD-REQ-012 FSM state updates
func TestFSM_ApplySet_StoresValue(t *testing.T) {
	// CLD-REQ-012: Verify FSM stores VRN inventory entries
	logger := zap.NewNop()
	fsm := NewFSM(logger)

	tests := []struct {
		name  string
		key   string
		value string
	}{
		{
			name:  "node enrollment",
			key:   "node:node-1",
			value: `{"id":"node-1","state":"enrolling"}`,
		},
		{
			name:  "node state transition",
			key:   "node:node-2",
			value: `{"id":"node-2","state":"ready","capacity":{"cpu":4000}}`,
		},
		{
			name:  "workload assignment",
			key:   "workload:wl-1",
			value: `{"id":"wl-1","replicas":3,"placement":[]}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-012: Create set command
			cmd := &Command{
				Op:    "set",
				Key:   tt.key,
				Value: []byte(tt.value),
			}

			cmdData, err := json.Marshal(cmd)
			if err != nil {
				t.Fatalf("Failed to marshal command: %v", err)
			}

			// Apply command through RAFT log
			logEntry := &raft.Log{
				Index: 1,
				Term:  1,
				Type:  raft.LogCommand,
				Data:  cmdData,
			}

			result := fsm.Apply(logEntry)
			if err, ok := result.(error); ok && err != nil {
				t.Fatalf("Apply failed: %v", err)
			}

			// Verify value stored
			got, err := fsm.Get(tt.key)
			if err != nil {
				t.Fatalf("Get failed: %v", err)
			}

			if string(got) != tt.value {
				t.Errorf("Expected value %s, got %s", tt.value, string(got))
			}
		})
	}

	t.Logf("FSM successfully stored %d VRN inventory entries", len(tests))
}

// TestFSM_ApplyDelete_RemovesValue verifies CLD-REQ-012 FSM node removal
func TestFSM_ApplyDelete_RemovesValue(t *testing.T) {
	// CLD-REQ-012: Verify FSM removes VRN inventory entries
	logger := zap.NewNop()
	fsm := NewFSM(logger)

	// First, add an entry
	setCmd := &Command{
		Op:    "set",
		Key:   "node:to-delete",
		Value: []byte(`{"id":"to-delete","state":"offline"}`),
	}

	setCmdData, _ := json.Marshal(setCmd)
	setLog := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  setCmdData,
	}

	result := fsm.Apply(setLog)
	if err, ok := result.(error); ok && err != nil {
		t.Fatalf("Apply Set failed: %v", err)
	}

	// Verify entry exists
	_, err := fsm.Get("node:to-delete")
	if err != nil {
		t.Fatalf("Entry should exist before delete: %v", err)
	}

	// CLD-REQ-012: Delete the entry
	deleteCmd := &Command{
		Op:  "delete",
		Key: "node:to-delete",
	}

	deleteCmdData, _ := json.Marshal(deleteCmd)
	deleteLog := &raft.Log{
		Index: 2,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  deleteCmdData,
	}

	result = fsm.Apply(deleteLog)
	if err, ok := result.(error); ok && err != nil {
		t.Fatalf("Apply Delete failed: %v", err)
	}

	// Verify entry removed
	_, err = fsm.Get("node:to-delete")
	if err == nil {
		t.Error("Expected Get to fail after Delete, but it succeeded")
	}

	t.Log("FSM successfully removed VRN inventory entry")
}

// TestFSM_ApplyBatch_Atomic verifies CLD-REQ-012 atomic FSM updates
func TestFSM_ApplyBatch_Atomic(t *testing.T) {
	// CLD-REQ-012: Verify FSM batch operations are atomic
	logger := zap.NewNop()
	fsm := NewFSM(logger)

	// CLD-REQ-012: Create batch command for multiple VRN entries
	batch := &BatchCommand{
		Commands: []*Command{
			{
				Op:    "set",
				Key:   "node:batch-1",
				Value: []byte(`{"id":"batch-1"}`),
			},
			{
				Op:    "set",
				Key:   "node:batch-2",
				Value: []byte(`{"id":"batch-2"}`),
			},
			{
				Op:    "set",
				Key:   "node:batch-3",
				Value: []byte(`{"id":"batch-3"}`),
			},
		},
	}

	batchData, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("Failed to marshal batch: %v", err)
	}

	logEntry := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  batchData,
	}

	result := fsm.Apply(logEntry)
	if err, ok := result.(error); ok && err != nil {
		t.Fatalf("Apply batch failed: %v", err)
	}

	// Verify all batch entries committed
	for _, cmd := range batch.Commands {
		got, err := fsm.Get(cmd.Key)
		if err != nil {
			t.Errorf("Get failed for %s: %v", cmd.Key, err)
			continue
		}
		if string(got) != string(cmd.Value) {
			t.Errorf("Value mismatch for %s: expected %s, got %s", cmd.Key, string(cmd.Value), string(got))
		}
	}

	t.Logf("FSM batch operation committed %d entries atomically", len(batch.Commands))
}

// TestFSM_Snapshot_Restore verifies CLD-REQ-012 FSM snapshot durability
func TestFSM_Snapshot_Restore(t *testing.T) {
	// CLD-REQ-012: Verify FSM snapshots preserve VRN inventory
	logger := zap.NewNop()
	fsm := NewFSM(logger)

	// Populate FSM with VRN inventory
	testData := map[string]string{
		"node:snap-1": `{"id":"snap-1","state":"ready"}`,
		"node:snap-2": `{"id":"snap-2","state":"ready"}`,
		"node:snap-3": `{"id":"snap-3","state":"offline"}`,
	}

	for key, value := range testData {
		cmd := &Command{
			Op:    "set",
			Key:   key,
			Value: []byte(value),
		}
		cmdData, _ := json.Marshal(cmd)
		logEntry := &raft.Log{
			Index: 1,
			Term:  1,
			Type:  raft.LogCommand,
			Data:  cmdData,
		}
		fsm.Apply(logEntry)
	}

	// CLD-REQ-012: Create snapshot
	snapshot, err := fsm.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot failed: %v", err)
	}

	// Persist snapshot to buffer (simulates disk write)
	var buf bytes.Buffer
	mockSink := &mockSnapshotSink{buf: &buf}

	if err := snapshot.Persist(mockSink); err != nil {
		t.Fatalf("Persist failed: %v", err)
	}

	snapshot.Release()

	// CLD-REQ-012: Create new FSM and restore from snapshot
	fsm2 := NewFSM(logger)
	reader := io.NopCloser(&buf)

	if err := fsm2.Restore(reader); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Verify all data restored
	for key, expectedValue := range testData {
		got, err := fsm2.Get(key)
		if err != nil {
			t.Errorf("Get failed for %s after restore: %v", key, err)
			continue
		}
		if string(got) != expectedValue {
			t.Errorf("Value mismatch for %s: expected %s, got %s", key, expectedValue, string(got))
		}
	}

	t.Logf("FSM snapshot/restore preserved %d VRN inventory entries", len(testData))
}

// TestFSM_GetExpired_ReturnsError verifies CLD-REQ-012 FSM expiration handling
func TestFSM_GetExpired_ReturnsError(t *testing.T) {
	// CLD-REQ-012: Verify FSM handles expired VRN inventory entries
	logger := zap.NewNop()
	fsm := NewFSM(logger)

	// Set entry with expiration in the past
	expiredTime := time.Now().Add(-1 * time.Hour).Unix()
	cmd := &Command{
		Op:        "set",
		Key:       "node:expired",
		Value:     []byte(`{"id":"expired","state":"ready"}`),
		ExpiresAt: expiredTime,
	}

	cmdData, _ := json.Marshal(cmd)
	logEntry := &raft.Log{
		Index: 1,
		Term:  1,
		Type:  raft.LogCommand,
		Data:  cmdData,
	}

	result := fsm.Apply(logEntry)
	if err, ok := result.(error); ok && err != nil {
		t.Fatalf("Apply failed: %v", err)
	}

	// CLD-REQ-012: Verify expired entry not returned
	_, err := fsm.Get("node:expired")
	if err == nil {
		t.Error("Expected Get to fail for expired entry, but it succeeded")
	}

	t.Log("FSM correctly rejects expired VRN inventory entries")
}

// TestFSM_ThreadSafety_ConcurrentAccess verifies CLD-REQ-012 FSM concurrency safety
func TestFSM_ThreadSafety_ConcurrentAccess(t *testing.T) {
	// CLD-REQ-012: Verify FSM is safe for concurrent VRN inventory access
	logger := zap.NewNop()
	fsm := NewFSM(logger)

	const numGoroutines = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 2) // Writers + readers

	// Concurrent writers
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				key := raftKey("node", id, j)
				cmd := &Command{
					Op:    "set",
					Key:   key,
					Value: []byte(`{"id":"` + key + `"}`),
				}
				cmdData, _ := json.Marshal(cmd)
				logEntry := &raft.Log{
					Index: uint64(id*opsPerGoroutine + j),
					Term:  1,
					Type:  raft.LogCommand,
					Data:  cmdData,
				}
				fsm.Apply(logEntry)
			}
		}(i)
	}

	// Concurrent readers
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				_, _ = fsm.GetAll()
			}
		}(i)
	}

	wg.Wait()

	// Verify FSM still consistent
	allData, err := fsm.GetAll()
	if err != nil {
		t.Fatalf("GetAll failed after concurrent access: %v", err)
	}

	expectedEntries := numGoroutines * opsPerGoroutine
	if len(allData) != expectedEntries {
		t.Errorf("Expected %d entries after concurrent writes, got %d", expectedEntries, len(allData))
	}

	t.Logf("FSM handled %d concurrent operations safely, final state: %d entries", numGoroutines*opsPerGoroutine*2, len(allData))
}

// TestFSM_CleanupExpired_RemovesOldEntries verifies CLD-REQ-012 FSM cleanup
func TestFSM_CleanupExpired_RemovesOldEntries(t *testing.T) {
	// CLD-REQ-012: Verify FSM cleans up expired VRN inventory entries
	logger := zap.NewNop()
	fsm := NewFSM(logger)

	// Add entry that will expire in 1 second
	shortLivedCmd := &Command{
		Op:        "set",
		Key:       "node:short-lived",
		Value:     []byte(`{"id":"short-lived"}`),
		ExpiresAt: time.Now().Add(1 * time.Second).Unix(),
	}
	shortLivedCmdData, _ := json.Marshal(shortLivedCmd)
	fsm.Apply(&raft.Log{Index: 1, Term: 1, Type: raft.LogCommand, Data: shortLivedCmdData})

	// Add non-expiring entry
	validCmd := &Command{
		Op:    "set",
		Key:   "node:valid",
		Value: []byte(`{"id":"valid"}`),
	}
	validCmdData, _ := json.Marshal(validCmd)
	fsm.Apply(&raft.Log{Index: 2, Term: 1, Type: raft.LogCommand, Data: validCmdData})

	// Wait for entry to expire
	time.Sleep(1100 * time.Millisecond)

	// CLD-REQ-012: Cleanup expired entries
	removed := fsm.CleanupExpired()
	if removed != 1 {
		t.Errorf("Expected 1 expired entry removed, got %d", removed)
	}

	// Verify valid entry still exists
	_, err := fsm.Get("node:valid")
	if err != nil {
		t.Errorf("Valid entry should still exist: %v", err)
	}

	// Verify expired entry removed
	_, err = fsm.Get("node:short-lived")
	if err == nil {
		t.Error("Expired entry should have been removed")
	}

	t.Log("FSM cleanup removed expired VRN inventory entries")
}

// TestFSM_GetAll_FiltersExpired verifies CLD-REQ-012 FSM expired entry filtering
func TestFSM_GetAll_FiltersExpired(t *testing.T) {
	// CLD-REQ-012: Verify GetAll excludes expired VRN inventory entries
	logger := zap.NewNop()
	fsm := NewFSM(logger)

	// Add valid entries
	for i := 1; i <= 3; i++ {
		cmd := &Command{
			Op:    "set",
			Key:   raftKey("node", i),
			Value: []byte(`{"id":"valid-` + string(rune('0'+i)) + `"}`),
		}
		cmdData, _ := json.Marshal(cmd)
		fsm.Apply(&raft.Log{Index: uint64(i), Term: 1, Type: raft.LogCommand, Data: cmdData})
	}

	// Add expired entries
	for i := 4; i <= 6; i++ {
		cmd := &Command{
			Op:        "set",
			Key:       raftKey("node", i),
			Value:     []byte(`{"id":"expired-` + string(rune('0'+i)) + `"}`),
			ExpiresAt: time.Now().Add(-1 * time.Hour).Unix(),
		}
		cmdData, _ := json.Marshal(cmd)
		fsm.Apply(&raft.Log{Index: uint64(i), Term: 1, Type: raft.LogCommand, Data: cmdData})
	}

	// CLD-REQ-012: GetAll should exclude expired entries
	allData, err := fsm.GetAll()
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}

	if len(allData) != 3 {
		t.Errorf("Expected 3 non-expired entries, got %d", len(allData))
	}

	// Verify only valid entries returned
	for key := range allData {
		if len(key) < 5 || key[:5] != "node:" {
			continue
		}
		// All keys should be node:1, node:2, node:3 (not 4, 5, 6)
		lastChar := key[len(key)-1]
		if lastChar > '3' {
			t.Errorf("Expired entry %s should not be in GetAll results", key)
		}
	}

	t.Logf("GetAll correctly filtered out %d expired entries", 3)
}

// TestFSM_Stats_ReturnsMetrics verifies CLD-REQ-012 FSM metrics collection
func TestFSM_Stats_ReturnsMetrics(t *testing.T) {
	// CLD-REQ-012: Verify FSM provides VRN inventory statistics
	logger := zap.NewNop()
	fsm := NewFSM(logger)

	// Add entry without expiration
	cmd1 := &Command{
		Op:    "set",
		Key:   "node:1",
		Value: []byte(`{"id":"1"}`),
	}
	cmdData1, _ := json.Marshal(cmd1)
	fsm.Apply(&raft.Log{Index: 1, Term: 1, Type: raft.LogCommand, Data: cmdData1})

	// Add entry with future expiration
	cmd2 := &Command{
		Op:        "set",
		Key:       "node:2",
		Value:     []byte(`{"id":"2"}`),
		ExpiresAt: time.Now().Add(1 * time.Hour).Unix(),
	}
	cmdData2, _ := json.Marshal(cmd2)
	fsm.Apply(&raft.Log{Index: 2, Term: 1, Type: raft.LogCommand, Data: cmdData2})

	// Add entry that expires in 1 second
	cmd3 := &Command{
		Op:        "set",
		Key:       "node:3",
		Value:     []byte(`{"id":"3"}`),
		ExpiresAt: time.Now().Add(1 * time.Second).Unix(),
	}
	cmdData3, _ := json.Marshal(cmd3)
	fsm.Apply(&raft.Log{Index: 3, Term: 1, Type: raft.LogCommand, Data: cmdData3})

	// Wait for entry to expire
	time.Sleep(1100 * time.Millisecond)

	// CLD-REQ-012: Get FSM statistics
	stats := fsm.Stats()

	// Should have 3 entries (expired ones aren't removed from map until CleanupExpired)
	if stats.TotalEntries != 3 {
		t.Errorf("Expected 3 total entries, got %d", stats.TotalEntries)
	}

	if stats.ExpiringEntries != 1 {
		t.Errorf("Expected 1 expiring entry (with future expiration), got %d", stats.ExpiringEntries)
	}

	if stats.ExpiredEntries != 1 {
		t.Errorf("Expected 1 expired entry, got %d", stats.ExpiredEntries)
	}

	if stats.TotalBytes <= 0 {
		t.Error("Expected total bytes > 0")
	}

	t.Logf("FSM stats: %+v", stats)
}

// Helper functions

// mockSnapshotSink implements raft.SnapshotSink for testing
type mockSnapshotSink struct {
	buf *bytes.Buffer
}

func (m *mockSnapshotSink) Write(p []byte) (n int, err error) {
	return m.buf.Write(p)
}

func (m *mockSnapshotSink) Close() error {
	return nil
}

func (m *mockSnapshotSink) ID() string {
	return "mock-snapshot"
}

func (m *mockSnapshotSink) Cancel() error {
	m.buf.Reset()
	return nil
}

// raftKey generates a test key with format "prefix:id" or "prefix:id-subid"
func raftKey(prefix string, parts ...int) string {
	key := prefix + ":"
	for i, p := range parts {
		if i > 0 {
			key += "-"
		}
		key += string(rune('0' + p))
	}
	return key
}
