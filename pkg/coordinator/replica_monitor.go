package coordinator

import (
	"context"
	"sync"
	"time"

	"github.com/osama1998H/Cloudless/pkg/api"
	"github.com/osama1998H/Cloudless/pkg/observability"
	"go.uber.org/zap"
)

// CLD-REQ-031: Failed replicas are rescheduled within 3s P50, 10s P95.
//
// ReplicaMonitor monitors workload replicas for failures and automatically
// triggers rescheduling when failures are detected. It tracks the latency
// from failure detection to successful rescheduling and reports metrics
// to verify compliance with the performance targets.

// ReplicaMonitorConfig configures the replica monitor
type ReplicaMonitorConfig struct {
	// ReconcileInterval is how often to check for failed replicas
	ReconcileInterval time.Duration

	// Logger for structured logging
	Logger *zap.Logger
}

// DefaultReplicaMonitorConfig returns default configuration
func DefaultReplicaMonitorConfig(logger *zap.Logger) ReplicaMonitorConfig {
	return ReplicaMonitorConfig{
		ReconcileInterval: 5 * time.Second, // Check every 5 seconds
		Logger:            logger,
	}
}

// ReplicaMonitor monitors replica health and triggers automatic rescheduling
type ReplicaMonitor struct {
	config            ReplicaMonitorConfig
	logger            *zap.Logger
	workloadStateMgr  *WorkloadStateManager
	coordinator       *Coordinator
	reconcileInterval time.Duration

	// Track failure timestamps for latency calculation
	mu                sync.RWMutex
	failureTimestamps map[string]time.Time // replicaID -> failure detection time

	// Lifecycle management
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewReplicaMonitor creates a new replica monitor
func NewReplicaMonitor(config ReplicaMonitorConfig, workloadStateMgr *WorkloadStateManager, coordinator *Coordinator) (*ReplicaMonitor, error) {
	if config.Logger == nil {
		config.Logger = zap.NewNop()
	}
	if config.ReconcileInterval == 0 {
		config.ReconcileInterval = 5 * time.Second
	}

	return &ReplicaMonitor{
		config:            config,
		logger:            config.Logger,
		workloadStateMgr:  workloadStateMgr,
		coordinator:       coordinator,
		reconcileInterval: config.ReconcileInterval,
		failureTimestamps: make(map[string]time.Time),
	}, nil
}

// Start starts the replica monitor background loop
func (rm *ReplicaMonitor) Start(ctx context.Context) error {
	rm.ctx, rm.cancel = context.WithCancel(ctx)

	rm.logger.Info("Starting replica monitor",
		zap.Duration("reconcile_interval", rm.reconcileInterval),
	)

	// Start reconciliation loop
	rm.wg.Add(1)
	go rm.reconcileLoop()

	return nil
}

// Stop stops the replica monitor
func (rm *ReplicaMonitor) Stop() error {
	rm.logger.Info("Stopping replica monitor")

	if rm.cancel != nil {
		rm.cancel()
	}

	// Wait for reconciliation loop to finish
	rm.wg.Wait()

	rm.logger.Info("Replica monitor stopped")
	return nil
}

// reconcileLoop is the main reconciliation loop that checks for failed replicas
func (rm *ReplicaMonitor) reconcileLoop() {
	defer rm.wg.Done()

	ticker := time.NewTicker(rm.reconcileInterval)
	defer ticker.Stop()

	for {
		select {
		case <-rm.ctx.Done():
			rm.logger.Info("Replica monitor reconciliation loop stopping")
			return
		case <-ticker.C:
			rm.reconcile()
		}
	}
}

// reconcile performs one reconciliation cycle
func (rm *ReplicaMonitor) reconcile() {
	// Get all workloads
	workloads, err := rm.workloadStateMgr.ListWorkloads(rm.ctx, "", nil)
	if err != nil {
		rm.logger.Error("Failed to list workloads during reconciliation",
			zap.Error(err),
		)
		return
	}

	for _, workload := range workloads {
		// Skip deleted workloads
		if workload.DeletedAt != nil {
			continue
		}

		// Check each replica for failures
		rm.reconcileWorkload(workload)
	}
}

// reconcileWorkload checks a single workload for failed replicas and triggers rescheduling
func (rm *ReplicaMonitor) reconcileWorkload(workload *WorkloadState) {
	// Find failed replicas
	var failedReplicas []ReplicaState
	var failedNodeIDs []string
	failedNodeMap := make(map[string]bool)

	for _, replica := range workload.Replicas {
		if replica.Status == ReplicaStatusFailed {
			failedReplicas = append(failedReplicas, replica)

			// Track unique failed nodes
			if !failedNodeMap[replica.NodeID] {
				failedNodeIDs = append(failedNodeIDs, replica.NodeID)
				failedNodeMap[replica.NodeID] = true
			}

			// Record failure timestamp if not already tracked
			rm.mu.Lock()
			if _, exists := rm.failureTimestamps[replica.ID]; !exists {
				rm.failureTimestamps[replica.ID] = time.Now()
				rm.logger.Info("Detected failed replica",
					zap.String("workload_id", workload.ID),
					zap.String("replica_id", replica.ID),
					zap.String("node_id", replica.NodeID),
					zap.String("message", replica.Message),
				)
			}
			rm.mu.Unlock()
		}
	}

	// If no failed replicas, nothing to do
	if len(failedReplicas) == 0 {
		return
	}

	// If coordinator is nil, we're in testing mode - just track failures, don't reschedule
	if rm.coordinator == nil {
		rm.logger.Debug("Skipping reschedule - coordinator is nil (likely testing)",
			zap.String("workload_id", workload.ID),
			zap.Int("failed_replicas", len(failedReplicas)),
		)
		// Failures are already tracked in failureTimestamps above
		return
	}

	// CLD-REQ-031: Automatically trigger rescheduling for failed replicas
	rm.logger.Info("Triggering automatic rescheduling for failed replicas",
		zap.String("workload_id", workload.ID),
		zap.Int("failed_replicas", len(failedReplicas)),
		zap.Strings("failed_nodes", failedNodeIDs),
	)

	// Record start time for latency tracking
	startTime := time.Now()

	// Increment rescheduling operations counter
	observability.ReschedulingOperationsTotal.WithLabelValues("replica_failure").Inc()

	// Call Reschedule via gRPC handler (simulating internal call)
	err := rm.triggerReschedule(workload, failedNodeIDs)

	// Calculate latency
	latency := time.Since(startTime)

	if err != nil {
		// Record failure
		rm.logger.Error("Failed to reschedule workload",
			zap.String("workload_id", workload.ID),
			zap.Error(err),
			zap.Duration("latency", latency),
		)

		// Determine failure reason
		reason := "scheduler_error"
		if err.Error() == "insufficient capacity" {
			reason = "insufficient_capacity"
		} else if err.Error() == "constraint violation" {
			reason = "constraint_violation"
		}
		observability.RescheduleFailuresTotal.WithLabelValues(reason).Inc()

		return
	}

	// Success - record latency for each failed replica
	rm.mu.Lock()
	for _, replica := range failedReplicas {
		if failureTime, exists := rm.failureTimestamps[replica.ID]; exists {
			// Calculate end-to-end latency from failure detection to rescheduling
			endToEndLatency := time.Since(failureTime)

			// CLD-REQ-031: Record latency metric for P50/P95 tracking
			observability.RescheduleLatencySeconds.Observe(endToEndLatency.Seconds())

			rm.logger.Info("Successfully rescheduled failed replica",
				zap.String("workload_id", workload.ID),
				zap.String("replica_id", replica.ID),
				zap.Duration("detection_to_reschedule_latency", endToEndLatency),
				zap.Duration("reschedule_call_latency", latency),
			)

			// Clean up timestamp
			delete(rm.failureTimestamps, replica.ID)
		}
	}
	rm.mu.Unlock()
}

// triggerReschedule triggers the Reschedule operation for a workload
func (rm *ReplicaMonitor) triggerReschedule(workload *WorkloadState, failedNodeIDs []string) error {
	// Build Reschedule request
	req := &api.RescheduleRequest{
		WorkloadId: workload.ID,
		Reason:     "replica_failure",
		// FailedNodes are derived from replica states, not passed in API
		// The Coordinator.Reschedule handler extracts them from WorkloadState
	}

	// Call Coordinator's Reschedule handler directly (internal call)
	_, err := rm.coordinator.Reschedule(rm.ctx, req)
	if err != nil {
		return err
	}

	return nil
}

// GetFailureCount returns the number of replicas currently being tracked as failed
func (rm *ReplicaMonitor) GetFailureCount() int {
	rm.mu.RLock()
	defer rm.mu.RUnlock()
	return len(rm.failureTimestamps)
}

// ClearFailureTimestamp manually clears a failure timestamp (for testing)
func (rm *ReplicaMonitor) ClearFailureTimestamp(replicaID string) {
	rm.mu.Lock()
	defer rm.mu.Unlock()
	delete(rm.failureTimestamps, replicaID)
}
