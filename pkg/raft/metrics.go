package raft

import (
	"context"
	"fmt"
	"time"

	"github.com/hashicorp/raft"
	"github.com/osama1998H/Cloudless/pkg/observability"
	"go.uber.org/zap"
)

// MetricsCollector periodically collects RAFT metrics and publishes to Prometheus
// CLD-REQ-051: Provides observability for RAFT consensus health
type MetricsCollector struct {
	store    *Store
	logger   *zap.Logger
	interval time.Duration
	ctx      context.Context
	cancel   context.CancelFunc
}

// NewMetricsCollector creates a new RAFT metrics collector
// Recommended interval: 5-15 seconds for production
func NewMetricsCollector(store *Store, logger *zap.Logger, interval time.Duration) *MetricsCollector {
	ctx, cancel := context.WithCancel(context.Background())
	return &MetricsCollector{
		store:    store,
		logger:   logger,
		interval: interval,
		ctx:      ctx,
		cancel:   cancel,
	}
}

// Start begins periodic metrics collection
// Runs in a goroutine - caller is responsible for calling Stop()
func (mc *MetricsCollector) Start() {
	mc.logger.Info("Starting RAFT metrics collector",
		zap.Duration("interval", mc.interval),
	)

	go mc.collectLoop()
}

// Stop gracefully stops metrics collection
func (mc *MetricsCollector) Stop() {
	mc.logger.Info("Stopping RAFT metrics collector")
	mc.cancel()
}

// collectLoop runs the periodic collection loop
func (mc *MetricsCollector) collectLoop() {
	ticker := time.NewTicker(mc.interval)
	defer ticker.Stop()

	// Collect immediately on start
	mc.collectMetrics()

	for {
		select {
		case <-mc.ctx.Done():
			return
		case <-ticker.C:
			mc.collectMetrics()
		}
	}
}

// collectMetrics collects and publishes all RAFT metrics
func (mc *MetricsCollector) collectMetrics() {
	// Get current RAFT state
	state := mc.store.raft.State()
	stats := mc.store.raft.Stats()

	// Update node state metric
	mc.updateNodeStateMetric(state)

	// Update log indices
	mc.updateLogMetrics(stats)

	// If leader, collect leader-specific metrics
	if state == raft.Leader {
		mc.collectLeaderMetrics()
	}

	// Update snapshot metrics
	mc.updateSnapshotMetrics(stats)

	// Update peer connectivity
	mc.updatePeerMetrics()

	mc.logger.Debug("Collected RAFT metrics",
		zap.String("state", state.String()),
		zap.String("commit_index", stats["commit_index"]),
		zap.String("applied_index", stats["applied_index"]),
	)
}

// updateNodeStateMetric updates the RAFT node state metric
func (mc *MetricsCollector) updateNodeStateMetric(state raft.RaftState) {
	nodeID := mc.store.config.RaftID

	// Convert RAFT state to numeric value
	// 0=follower, 1=candidate, 2=leader, 3=shutdown
	var stateValue float64
	switch state {
	case raft.Follower:
		stateValue = 0
	case raft.Candidate:
		stateValue = 1
	case raft.Leader:
		stateValue = 2
	case raft.Shutdown:
		stateValue = 3
	}

	observability.RaftNodeState.WithLabelValues(nodeID).Set(stateValue)

	// Also update legacy coordinator metric
	if state == raft.Leader {
		observability.CoordinatorIsLeader.Set(1)
	} else {
		observability.CoordinatorIsLeader.Set(0)
	}
}

// updateLogMetrics updates RAFT log index metrics
func (mc *MetricsCollector) updateLogMetrics(stats map[string]string) {
	// Parse commit index
	if commitIndex := mc.parseStatInt(stats["commit_index"]); commitIndex >= 0 {
		observability.RaftCommitIndex.Set(float64(commitIndex))
		observability.CoordinatorRaftAppliedIndex.Set(float64(commitIndex)) // Legacy metric
	}

	// Parse applied index
	if appliedIndex := mc.parseStatInt(stats["applied_index"]); appliedIndex >= 0 {
		// Applied index already tracked via CoordinatorRaftAppliedIndex
		// No separate metric needed
	}

	// Parse last log index
	if lastLogIndex := mc.parseStatInt(stats["last_log_index"]); lastLogIndex >= 0 {
		observability.RaftLastLogIndex.Set(float64(lastLogIndex))
		observability.CoordinatorRaftLogEntries.Set(float64(lastLogIndex)) // Legacy metric
	}

	// Parse last log term
	if lastLogTerm := mc.parseStatInt(stats["last_log_term"]); lastLogTerm >= 0 {
		observability.RaftLastLogTerm.Set(float64(lastLogTerm))
	}
}

// collectLeaderMetrics collects leader-specific metrics
func (mc *MetricsCollector) collectLeaderMetrics() {
	config := mc.store.raft.GetConfiguration()
	if err := config.Error(); err != nil {
		mc.logger.Warn("Failed to get RAFT configuration for leader metrics",
			zap.Error(err),
		)
		return
	}

	leaderAddr, leaderID := mc.store.raft.LeaderWithID()
	stats := mc.store.raft.Stats()

	// Get last contact times for followers
	for _, server := range config.Configuration().Servers {
		if server.ID == leaderID {
			// Skip self (leader)
			continue
		}

		followerID := string(server.ID)

		// Get replication state for this follower
		// Note: HashiCorp RAFT doesn't expose per-follower stats directly
		// We can only track global replication lag
		// For production, consider using raft.ReplicationState interface

		// Track connectivity (1=connected, 0=disconnected)
		// We assume connected if in configuration
		observability.RaftPeerConnections.WithLabelValues(followerID).Set(1)
	}

	mc.logger.Debug("Collected leader metrics",
		zap.String("leader_addr", string(leaderAddr)),
		zap.String("leader_id", string(leaderID)),
		zap.Int("peers", len(config.Configuration().Servers)-1),
	)

	// Parse num_peers from stats
	if numPeers := mc.parseStatInt(stats["num_peers"]); numPeers >= 0 {
		// Successfully got peer count
		mc.logger.Debug("Peer count", zap.Int("num_peers", int(numPeers)))
	}
}

// updateSnapshotMetrics updates snapshot-related metrics
func (mc *MetricsCollector) updateSnapshotMetrics(stats map[string]string) {
	// Parse snapshot size if available
	if snapshotSize := mc.parseStatInt(stats["last_snapshot_size"]); snapshotSize >= 0 {
		observability.RaftSnapshotSizeBytes.Set(float64(snapshotSize))
	}

	// Note: Snapshot creation/restore counters are updated
	// by the Store.Snapshot() and FSM.Restore() methods directly
}

// updatePeerMetrics updates peer connectivity metrics
func (mc *MetricsCollector) updatePeerMetrics() {
	config := mc.store.raft.GetConfiguration()
	if err := config.Error(); err != nil {
		mc.logger.Warn("Failed to get RAFT configuration for peer metrics",
			zap.Error(err),
		)
		return
	}

	// Track all configured peers
	for _, server := range config.Configuration().Servers {
		peerID := string(server.ID)

		// Track as connected (basic implementation)
		// For production, implement actual connectivity checks
		observability.RaftPeerConnections.WithLabelValues(peerID).Set(1)
	}
}

// parseStatInt parses an integer value from RAFT stats map
// Returns -1 if key doesn't exist or parsing fails
func (mc *MetricsCollector) parseStatInt(value string) int64 {
	if value == "" {
		return -1
	}

	var result int64
	_, err := fmt.Sscanf(value, "%d", &result)
	if err != nil {
		return -1
	}

	return result
}

// RecordApplyLatency records the latency of applying a RAFT log entry
// Call this from FSM.Apply() method
func RecordApplyLatency(duration time.Duration) {
	observability.RaftApplyLatencySeconds.Observe(duration.Seconds())
}

// RecordCommitLatency records the latency from log append to commit
// Call this from Store.ApplyRaw() or similar methods
func RecordCommitLatency(duration time.Duration) {
	observability.RaftCommitLatencySeconds.Observe(duration.Seconds())
}

// RecordSnapshotOperation records a snapshot creation or restore operation
func RecordSnapshotOperation(operation string, duration time.Duration, err error) {
	result := "success"
	if err != nil {
		result = "failure"
	}

	// Update counters
	switch operation {
	case "create":
		observability.RaftSnapshotCreationTotal.WithLabelValues(result).Inc()
	case "restore":
		observability.RaftSnapshotRestoreTotal.WithLabelValues(result).Inc()
	}

	// Update duration histogram
	observability.RaftSnapshotDurationSeconds.WithLabelValues(operation).Observe(duration.Seconds())
}
