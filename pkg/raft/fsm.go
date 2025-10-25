package raft

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"sync"
	"time"

	"github.com/hashicorp/go-hclog"
	"github.com/hashicorp/raft"
	"go.uber.org/zap"
)

// Command represents a command to be applied to the FSM
type Command struct {
	Op        string `json:"op"`
	Key       string `json:"key"`
	Value     []byte `json:"value,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

// BatchCommand represents multiple commands to be applied atomically
type BatchCommand struct {
	Commands []*Command `json:"commands"`
}

// FSM implements the Raft FSM interface
type FSM struct {
	mu     sync.RWMutex
	data   map[string]*Entry
	logger *zap.Logger
}

// Entry represents a stored value with optional expiration
type Entry struct {
	Value     []byte    `json:"value"`
	ExpiresAt int64     `json:"expires_at,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NewFSM creates a new FSM
func NewFSM(logger *zap.Logger) *FSM {
	return &FSM{
		data:   make(map[string]*Entry),
		logger: logger,
	}
}

// Apply applies a Raft log entry to the FSM
func (f *FSM) Apply(l *raft.Log) interface{} {
	f.mu.Lock()
	defer f.mu.Unlock()

	// Try to parse as batch command first
	var batch BatchCommand
	if err := json.Unmarshal(l.Data, &batch); err == nil && len(batch.Commands) > 0 {
		return f.applyBatch(&batch)
	}

	// Parse as single command
	var cmd Command
	if err := json.Unmarshal(l.Data, &cmd); err != nil {
		f.logger.Error("Failed to unmarshal command",
			zap.Error(err),
			zap.Uint64("index", l.Index),
		)
		return err
	}

	return f.applyCommand(&cmd)
}

// applyCommand applies a single command
func (f *FSM) applyCommand(cmd *Command) interface{} {
	switch cmd.Op {
	case "set":
		return f.applySet(cmd.Key, cmd.Value, cmd.ExpiresAt)
	case "delete":
		return f.applyDelete(cmd.Key)
	default:
		err := fmt.Errorf("unknown operation: %s", cmd.Op)
		f.logger.Error("Unknown operation", zap.String("op", cmd.Op))
		return err
	}
}

// applyBatch applies a batch of commands
func (f *FSM) applyBatch(batch *BatchCommand) interface{} {
	var lastErr error
	applied := 0

	for _, cmd := range batch.Commands {
		if err := f.applyCommand(cmd); err != nil {
			lastErr = err
			f.logger.Error("Failed to apply command in batch",
				zap.Error(err),
				zap.String("op", cmd.Op),
				zap.String("key", cmd.Key),
			)
		} else {
			applied++
		}
	}

	f.logger.Debug("Applied batch",
		zap.Int("total", len(batch.Commands)),
		zap.Int("applied", applied),
	)

	return lastErr
}

// applySet stores or updates a key-value pair
func (f *FSM) applySet(key string, value []byte, expiresAt int64) interface{} {
	now := time.Now()

	// Check if entry has expired
	if expiresAt > 0 && time.Unix(expiresAt, 0).Before(now) {
		f.logger.Debug("Skipping expired entry",
			zap.String("key", key),
			zap.Time("expires_at", time.Unix(expiresAt, 0)),
		)
		return nil
	}

	entry := &Entry{
		Value:     value,
		ExpiresAt: expiresAt,
		CreatedAt: now,
		UpdatedAt: now,
	}

	// Check if updating existing entry
	if existing, exists := f.data[key]; exists {
		entry.CreatedAt = existing.CreatedAt
	}

	f.data[key] = entry

	f.logger.Debug("Set key",
		zap.String("key", key),
		zap.Int("value_size", len(value)),
		zap.Int64("expires_at", expiresAt),
	)

	return nil
}

// applyDelete removes a key
func (f *FSM) applyDelete(key string) interface{} {
	if _, exists := f.data[key]; exists {
		delete(f.data, key)
		f.logger.Debug("Deleted key", zap.String("key", key))
	}
	return nil
}

// Get retrieves a value by key
func (f *FSM) Get(key string) ([]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	entry, exists := f.data[key]
	if !exists {
		return nil, fmt.Errorf("key not found: %s", key)
	}

	// Check expiration
	if entry.ExpiresAt > 0 && time.Unix(entry.ExpiresAt, 0).Before(time.Now()) {
		return nil, fmt.Errorf("key expired: %s", key)
	}

	return entry.Value, nil
}

// GetAll returns all key-value pairs
func (f *FSM) GetAll() (map[string][]byte, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	now := time.Now()
	result := make(map[string][]byte)

	for key, entry := range f.data {
		// Skip expired entries
		if entry.ExpiresAt > 0 && time.Unix(entry.ExpiresAt, 0).Before(now) {
			continue
		}
		result[key] = entry.Value
	}

	return result, nil
}

// Snapshot creates a snapshot of the FSM state
func (f *FSM) Snapshot() (raft.FSMSnapshot, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	// Create a copy of the data
	dataCopy := make(map[string]*Entry)
	for k, v := range f.data {
		entryCopy := &Entry{
			Value:     make([]byte, len(v.Value)),
			ExpiresAt: v.ExpiresAt,
			CreatedAt: v.CreatedAt,
			UpdatedAt: v.UpdatedAt,
		}
		copy(entryCopy.Value, v.Value)
		dataCopy[k] = entryCopy
	}

	snapshot := &FSMSnapshot{
		data:   dataCopy,
		logger: f.logger,
	}

	f.logger.Info("Created snapshot", zap.Int("entries", len(dataCopy)))
	return snapshot, nil
}

// Restore restores the FSM from a snapshot
func (f *FSM) Restore(rc io.ReadCloser) error {
	defer rc.Close()

	// Decode snapshot data
	var snapshot SnapshotData
	if err := json.NewDecoder(rc).Decode(&snapshot); err != nil {
		return fmt.Errorf("failed to decode snapshot: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	// Clear existing data
	f.data = make(map[string]*Entry)

	// Restore data, filtering expired entries
	now := time.Now()
	restored := 0
	expired := 0

	for key, entry := range snapshot.Data {
		if entry.ExpiresAt > 0 && time.Unix(entry.ExpiresAt, 0).Before(now) {
			expired++
			continue
		}
		f.data[key] = entry
		restored++
	}

	f.logger.Info("Restored from snapshot",
		zap.Int("restored", restored),
		zap.Int("expired", expired),
		zap.Time("snapshot_time", snapshot.Timestamp),
	)

	return nil
}

// FSMSnapshot represents a point-in-time snapshot of the FSM
type FSMSnapshot struct {
	data   map[string]*Entry
	logger *zap.Logger
}

// SnapshotData represents the serialized snapshot data
type SnapshotData struct {
	Version   int                `json:"version"`
	Timestamp time.Time          `json:"timestamp"`
	Data      map[string]*Entry  `json:"data"`
}

// Persist writes the snapshot to the given sink
func (s *FSMSnapshot) Persist(sink raft.SnapshotSink) error {
	snapshotData := SnapshotData{
		Version:   1,
		Timestamp: time.Now(),
		Data:      s.data,
	}

	// Encode snapshot
	data, err := json.Marshal(snapshotData)
	if err != nil {
		sink.Cancel()
		return fmt.Errorf("failed to marshal snapshot: %w", err)
	}

	// Write to sink
	if _, err := sink.Write(data); err != nil {
		sink.Cancel()
		return fmt.Errorf("failed to write snapshot: %w", err)
	}

	// Close sink
	if err := sink.Close(); err != nil {
		return fmt.Errorf("failed to close snapshot sink: %w", err)
	}

	s.logger.Info("Persisted snapshot", zap.Int("entries", len(s.data)))
	return nil
}

// Release is called when the FSM can release the snapshot
func (s *FSMSnapshot) Release() {
	// Nothing to release as we work with copies
	s.logger.Debug("Released snapshot")
}

// CleanupExpired removes expired entries from the FSM
func (f *FSM) CleanupExpired() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	now := time.Now()
	removed := 0

	for key, entry := range f.data {
		if entry.ExpiresAt > 0 && time.Unix(entry.ExpiresAt, 0).Before(now) {
			delete(f.data, key)
			removed++
		}
	}

	if removed > 0 {
		f.logger.Info("Cleaned up expired entries", zap.Int("removed", removed))
	}

	return removed
}

// Size returns the number of entries in the FSM
func (f *FSM) Size() int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return len(f.data)
}

// Stats returns statistics about the FSM
func (f *FSM) Stats() FSMStats {
	f.mu.RLock()
	defer f.mu.RUnlock()

	stats := FSMStats{
		TotalEntries: len(f.data),
		TotalBytes:   0,
	}

	now := time.Now()
	for _, entry := range f.data {
		stats.TotalBytes += len(entry.Value)
		if entry.ExpiresAt > 0 {
			if time.Unix(entry.ExpiresAt, 0).Before(now) {
				stats.ExpiredEntries++
			} else {
				stats.ExpiringEntries++
			}
		}
	}

	return stats
}

// FSMStats contains statistics about the FSM
type FSMStats struct {
	TotalEntries    int `json:"total_entries"`
	TotalBytes      int `json:"total_bytes"`
	ExpiredEntries  int `json:"expired_entries"`
	ExpiringEntries int `json:"expiring_entries"`
}

// hashiLogger adapts zap.Logger to HashiCorp's logger interface
type hashiLogger struct {
	logger *zap.Logger
}

// newHashiLogger creates a new HashiCorp logger adapter
func newHashiLogger(logger *zap.Logger) *hashiLogger {
	return &hashiLogger{logger: logger}
}

// Implement HashiCorp logger interface methods

func (h *hashiLogger) Debug(msg string, args ...interface{}) {
	h.logger.Debug(fmt.Sprintf(msg, args...))
}

func (h *hashiLogger) Info(msg string, args ...interface{}) {
	h.logger.Info(fmt.Sprintf(msg, args...))
}

func (h *hashiLogger) Warn(msg string, args ...interface{}) {
	h.logger.Warn(fmt.Sprintf(msg, args...))
}

func (h *hashiLogger) Error(msg string, args ...interface{}) {
	h.logger.Error(fmt.Sprintf(msg, args...))
}

func (h *hashiLogger) IsDebug() bool {
	return h.logger.Core().Enabled(zap.DebugLevel)
}

func (h *hashiLogger) IsInfo() bool {
	return h.logger.Core().Enabled(zap.InfoLevel)
}

func (h *hashiLogger) IsWarn() bool {
	return h.logger.Core().Enabled(zap.WarnLevel)
}

func (h *hashiLogger) IsError() bool {
	return h.logger.Core().Enabled(zap.ErrorLevel)
}

func (h *hashiLogger) IsTrace() bool {
	// Zap doesn't have trace level, use debug
	return h.logger.Core().Enabled(zap.DebugLevel)
}

func (h *hashiLogger) Trace(msg string, args ...interface{}) {
	// Use debug for trace
	h.logger.Debug(fmt.Sprintf(msg, args...))
}

func (h *hashiLogger) GetLevel() string {
	if h.IsDebug() {
		return "DEBUG"
	}
	if h.IsInfo() {
		return "INFO"
	}
	if h.IsWarn() {
		return "WARN"
	}
	if h.IsError() {
		return "ERROR"
	}
	return "UNKNOWN"
}

func (h *hashiLogger) SetLevel(level string) {
	// Not implemented - level is controlled by zap configuration
}

func (h *hashiLogger) Named(name string) hclog.Logger {
	return &hashiLogger{logger: h.logger.Named(name)}
}

func (h *hashiLogger) With(args ...interface{}) hclog.Logger {
	// Convert args to zap fields
	fields := make([]zap.Field, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		if i+1 < len(args) {
			key := fmt.Sprint(args[i])
			value := args[i+1]
			fields = append(fields, zap.Any(key, value))
		}
	}
	return &hashiLogger{logger: h.logger.With(fields...)}
}

func (h *hashiLogger) StandardLogger(opts *hclog.StandardLoggerOptions) *log.Logger {
	// Not implemented
	return nil
}

func (h *hashiLogger) StandardWriter(opts *hclog.StandardLoggerOptions) io.Writer {
	// Not implemented
	return nil
}

func (h *hashiLogger) ResetNamed(name string) hclog.Logger {
	return &hashiLogger{logger: h.logger.Named(name)}
}

func (h *hashiLogger) ImpliedArgs() []interface{} {
	return nil
}