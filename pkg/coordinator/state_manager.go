package coordinator

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/osama1998H/Cloudless/pkg/raft"
	"github.com/osama1998H/Cloudless/pkg/scheduler"
	"go.uber.org/zap"
)

// WorkloadState represents the persistent state of a workload
type WorkloadState struct {
	// Workload spec
	ID          string                         `json:"id"`
	Name        string                         `json:"name"`
	Namespace   string                         `json:"namespace"`
	Image       string                         `json:"image"`
	Command     []string                       `json:"command,omitempty"`
	Args        []string                       `json:"args,omitempty"`
	Env         map[string]string              `json:"env,omitempty"`
	Volumes     []VolumeMount                  `json:"volumes,omitempty"`
	Ports       []PortMapping                  `json:"ports,omitempty"`
	Resources   scheduler.ResourceRequirements `json:"resources"`
	Placement   scheduler.PlacementPolicy      `json:"placement"`
	Restart     scheduler.RestartPolicy        `json:"restart"`
	Rollout     scheduler.RolloutStrategy      `json:"rollout"`
	Priority    int32                          `json:"priority"`
	Labels      map[string]string              `json:"labels,omitempty"`
	Annotations map[string]string              `json:"annotations,omitempty"`

	// Deployment config
	DesiredReplicas int32 `json:"desired_replicas"`

	// Runtime state
	Status            WorkloadStatus `json:"status"`
	Replicas          []ReplicaState `json:"replicas"`
	CurrentReplicas   int32          `json:"current_replicas"`
	ReadyReplicas     int32          `json:"ready_replicas"`
	AvailableReplicas int32          `json:"available_replicas"`

	// Timestamps
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// WorkloadStatus represents the lifecycle state
type WorkloadStatus string

const (
	WorkloadStatusPending    WorkloadStatus = "Pending"
	WorkloadStatusScheduling WorkloadStatus = "Scheduling"
	WorkloadStatusRunning    WorkloadStatus = "Running"
	WorkloadStatusUpdating   WorkloadStatus = "Updating"
	WorkloadStatusScaling    WorkloadStatus = "Scaling"
	WorkloadStatusFailed     WorkloadStatus = "Failed"
	WorkloadStatusCompleted  WorkloadStatus = "Completed"
	WorkloadStatusDeleting   WorkloadStatus = "Deleting"
)

// ReplicaState represents the state of a single replica
type ReplicaState struct {
	ID         string        `json:"id"`
	NodeID     string        `json:"node_id"`
	NodeName   string        `json:"node_name"`
	FragmentID string        `json:"fragment_id"`
	Status     ReplicaStatus `json:"status"`
	Ready      bool          `json:"ready"`
	Message    string        `json:"message,omitempty"`
	CreatedAt  time.Time     `json:"created_at"`
	StartedAt  *time.Time    `json:"started_at,omitempty"`
	StoppedAt  *time.Time    `json:"stopped_at,omitempty"`

	// CLD-REQ-032: Health probe status
	LivenessHealthy              bool      `json:"liveness_healthy"`
	LivenessConsecutiveFailures  int32     `json:"liveness_consecutive_failures"`
	LivenessLastCheckTime        time.Time `json:"liveness_last_check_time,omitempty"`
	ReadinessHealthy             bool      `json:"readiness_healthy"`
	ReadinessConsecutiveFailures int32     `json:"readiness_consecutive_failures"`
	ReadinessLastCheckTime       time.Time `json:"readiness_last_check_time,omitempty"`
	ContainerID                  string    `json:"container_id,omitempty"` // For health data correlation
}

// ReplicaStatus represents replica state
type ReplicaStatus string

const (
	ReplicaStatusPending   ReplicaStatus = "Pending"
	ReplicaStatusScheduled ReplicaStatus = "Scheduled"
	ReplicaStatusStarting  ReplicaStatus = "Starting"
	ReplicaStatusRunning   ReplicaStatus = "Running"
	ReplicaStatusStopping  ReplicaStatus = "Stopping"
	ReplicaStatusStopped   ReplicaStatus = "Stopped"
	ReplicaStatusFailed    ReplicaStatus = "Failed"
)

// VolumeMount represents a volume mount
type VolumeMount struct {
	Name      string `json:"name"`
	MountPath string `json:"mount_path"`
	ReadOnly  bool   `json:"read_only,omitempty"`
}

// PortMapping represents a port mapping
type PortMapping struct {
	Name          string `json:"name,omitempty"`
	ContainerPort int32  `json:"container_port"`
	Protocol      string `json:"protocol"` // TCP, UDP
}

// WorkloadStateManager manages workload state in RAFT
type WorkloadStateManager struct {
	store  *raft.Store
	logger *zap.Logger
	mu     sync.RWMutex
	cache  map[string]*WorkloadState // workloadID -> state (in-memory cache)
}

// NewWorkloadStateManager creates a new workload state manager
func NewWorkloadStateManager(store *raft.Store, logger *zap.Logger) (*WorkloadStateManager, error) {
	if store == nil {
		return nil, fmt.Errorf("raft store is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	wsm := &WorkloadStateManager{
		store:  store,
		logger: logger,
		cache:  make(map[string]*WorkloadState),
	}

	// Load existing workloads from RAFT on startup
	if err := wsm.loadFromRAFT(); err != nil {
		logger.Warn("Failed to load workloads from RAFT", zap.Error(err))
		// Continue anyway - RAFT might be empty on first start
	}

	return wsm, nil
}

// SaveWorkload saves workload state to RAFT with quorum write
func (wsm *WorkloadStateManager) SaveWorkload(ctx context.Context, workload *WorkloadState) error {
	wsm.mu.Lock()
	defer wsm.mu.Unlock()

	// Update timestamp
	workload.UpdatedAt = time.Now()

	// Serialize to JSON
	data, err := json.Marshal(workload)
	if err != nil {
		return fmt.Errorf("failed to marshal workload: %w", err)
	}

	// Write to RAFT
	key := fmt.Sprintf("workload:%s", workload.ID)
	if err := wsm.store.Set(key, data); err != nil {
		return fmt.Errorf("failed to write to RAFT: %w", err)
	}

	// Update cache
	wsm.cache[workload.ID] = workload

	wsm.logger.Info("Workload saved to RAFT",
		zap.String("workload_id", workload.ID),
		zap.String("status", string(workload.Status)),
		zap.Int32("desired_replicas", workload.DesiredReplicas),
		zap.Int32("current_replicas", workload.CurrentReplicas),
	)

	return nil
}

// GetWorkload retrieves workload state with consistent read
func (wsm *WorkloadStateManager) GetWorkload(ctx context.Context, workloadID string) (*WorkloadState, error) {
	wsm.mu.RLock()

	// Check cache first
	if cached, exists := wsm.cache[workloadID]; exists {
		wsm.mu.RUnlock()
		return cached, nil
	}
	wsm.mu.RUnlock()

	// Not in cache, read from RAFT
	key := fmt.Sprintf("workload:%s", workloadID)
	data, err := wsm.store.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to read from RAFT: %w", err)
	}
	if data == nil {
		return nil, fmt.Errorf("workload not found: %s", workloadID)
	}

	var workload WorkloadState
	if err := json.Unmarshal(data, &workload); err != nil {
		return nil, fmt.Errorf("failed to unmarshal workload: %w", err)
	}

	// Update cache
	wsm.mu.Lock()
	wsm.cache[workloadID] = &workload
	wsm.mu.Unlock()

	return &workload, nil
}

// ListWorkloads lists all workloads with optional filtering
func (wsm *WorkloadStateManager) ListWorkloads(ctx context.Context, namespace string, labels map[string]string) ([]*WorkloadState, error) {
	wsm.mu.RLock()
	defer wsm.mu.RUnlock()

	workloads := make([]*WorkloadState, 0, len(wsm.cache))

	for _, workload := range wsm.cache {
		// Skip deleted workloads
		if workload.DeletedAt != nil {
			continue
		}

		// Filter by namespace
		if namespace != "" && workload.Namespace != namespace {
			continue
		}

		// Filter by labels
		if len(labels) > 0 {
			matches := true
			for key, value := range labels {
				if workload.Labels[key] != value {
					matches = false
					break
				}
			}
			if !matches {
				continue
			}
		}

		workloads = append(workloads, workload)
	}

	return workloads, nil
}

// DeleteWorkload marks a workload as deleted (soft delete)
func (wsm *WorkloadStateManager) DeleteWorkload(ctx context.Context, workloadID string) error {
	workload, err := wsm.GetWorkload(ctx, workloadID)
	if err != nil {
		return err
	}

	now := time.Now()
	workload.DeletedAt = &now
	workload.Status = WorkloadStatusDeleting

	return wsm.SaveWorkload(ctx, workload)
}

// PurgeWorkload permanently removes a workload from RAFT
func (wsm *WorkloadStateManager) PurgeWorkload(ctx context.Context, workloadID string) error {
	wsm.mu.Lock()
	defer wsm.mu.Unlock()

	key := fmt.Sprintf("workload:%s", workloadID)
	if err := wsm.store.Delete(key); err != nil {
		return fmt.Errorf("failed to delete from RAFT: %w", err)
	}

	// Remove from cache
	delete(wsm.cache, workloadID)

	wsm.logger.Info("Workload purged from RAFT",
		zap.String("workload_id", workloadID),
	)

	return nil
}

// UpdateWorkloadStatus updates only the status of a workload
func (wsm *WorkloadStateManager) UpdateWorkloadStatus(ctx context.Context, workloadID string, status WorkloadStatus, message string) error {
	workload, err := wsm.GetWorkload(ctx, workloadID)
	if err != nil {
		return err
	}

	workload.Status = status
	if message != "" {
		// Store message in annotations
		if workload.Annotations == nil {
			workload.Annotations = make(map[string]string)
		}
		workload.Annotations["status_message"] = message
	}

	return wsm.SaveWorkload(ctx, workload)
}

// AddReplica adds a replica to a workload
func (wsm *WorkloadStateManager) AddReplica(ctx context.Context, workloadID string, replica ReplicaState) error {
	workload, err := wsm.GetWorkload(ctx, workloadID)
	if err != nil {
		return err
	}

	workload.Replicas = append(workload.Replicas, replica)
	workload.CurrentReplicas = int32(len(workload.Replicas))

	return wsm.SaveWorkload(ctx, workload)
}

// UpdateReplica updates the state of a specific replica
func (wsm *WorkloadStateManager) UpdateReplica(ctx context.Context, workloadID, replicaID string, status ReplicaStatus, ready bool, message string) error {
	workload, err := wsm.GetWorkload(ctx, workloadID)
	if err != nil {
		return err
	}

	// Find and update replica
	found := false
	for i := range workload.Replicas {
		if workload.Replicas[i].ID == replicaID {
			workload.Replicas[i].Status = status
			workload.Replicas[i].Ready = ready
			workload.Replicas[i].Message = message

			now := time.Now()
			if status == ReplicaStatusRunning && workload.Replicas[i].StartedAt == nil {
				workload.Replicas[i].StartedAt = &now
			}
			if status == ReplicaStatusStopped || status == ReplicaStatusFailed {
				workload.Replicas[i].StoppedAt = &now
			}

			found = true
			break
		}
	}

	if !found {
		return fmt.Errorf("replica not found: %s", replicaID)
	}

	// Recalculate ready/available counts
	workload.ReadyReplicas = 0
	workload.AvailableReplicas = 0
	for _, replica := range workload.Replicas {
		if replica.Ready {
			workload.ReadyReplicas++
		}
		if replica.Status == ReplicaStatusRunning {
			workload.AvailableReplicas++
		}
	}

	return wsm.SaveWorkload(ctx, workload)
}

// UpdateReplicaHealth updates health probe status for a replica
//
// CLD-REQ-032: Processes health data from agent heartbeats.
//
// Parameters:
//   - containerID: Container identifier from heartbeat
//   - livenessHealthy: Current liveness probe status
//   - readinessHealthy: Current readiness probe status
//   - livenessFailures: Consecutive liveness failures
//   - readinessFailures: Consecutive readiness failures
//
// This method:
// 1. Finds replica by containerID
// 2. Updates health fields
// 3. Updates Ready field based on readiness status
// 4. Recalculates workload ReadyReplicas count
func (wsm *WorkloadStateManager) UpdateReplicaHealth(
	ctx context.Context,
	containerID string,
	livenessHealthy, readinessHealthy bool,
	livenessFailures, readinessFailures int32,
) error {
	wsm.mu.Lock()
	defer wsm.mu.Unlock()

	// Find workload and replica by containerID
	var targetWorkload *WorkloadState
	var replicaIndex int
	found := false

	for _, workload := range wsm.cache {
		for i, replica := range workload.Replicas {
			if replica.ContainerID == containerID {
				targetWorkload = workload
				replicaIndex = i
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		wsm.logger.Debug("No replica found for container health update",
			zap.String("container_id", containerID),
		)
		return nil // Not an error - container might not be tracked yet
	}

	// Update health fields
	replica := &targetWorkload.Replicas[replicaIndex]
	replica.LivenessHealthy = livenessHealthy
	replica.LivenessConsecutiveFailures = livenessFailures
	replica.LivenessLastCheckTime = time.Now()
	replica.ReadinessHealthy = readinessHealthy
	replica.ReadinessConsecutiveFailures = readinessFailures
	replica.ReadinessLastCheckTime = time.Now()

	// Update Ready status based on readiness (CLD-REQ-032: traffic gating)
	previousReady := replica.Ready
	replica.Ready = readinessHealthy

	// Log readiness changes
	if previousReady != readinessHealthy {
		wsm.logger.Info("Replica readiness changed",
			zap.String("workload_id", targetWorkload.ID),
			zap.String("replica_id", replica.ID),
			zap.String("container_id", containerID),
			zap.Bool("previous_ready", previousReady),
			zap.Bool("current_ready", readinessHealthy),
		)
	}

	// Recalculate ready/available counts
	targetWorkload.ReadyReplicas = 0
	targetWorkload.AvailableReplicas = 0
	for _, r := range targetWorkload.Replicas {
		if r.Ready {
			targetWorkload.ReadyReplicas++
		}
		if r.Status == ReplicaStatusRunning {
			targetWorkload.AvailableReplicas++
		}
	}

	// Save updated workload
	targetWorkload.UpdatedAt = time.Now()

	// Serialize to JSON
	data, err := json.Marshal(targetWorkload)
	if err != nil {
		return fmt.Errorf("failed to marshal workload: %w", err)
	}

	// Write to RAFT
	key := fmt.Sprintf("workload:%s", targetWorkload.ID)
	if err := wsm.store.Set(key, data); err != nil {
		return fmt.Errorf("failed to write to RAFT: %w", err)
	}

	wsm.logger.Debug("Replica health updated",
		zap.String("workload_id", targetWorkload.ID),
		zap.String("replica_id", replica.ID),
		zap.String("container_id", containerID),
		zap.Bool("liveness_healthy", livenessHealthy),
		zap.Bool("readiness_healthy", readinessHealthy),
		zap.Int32("ready_replicas", targetWorkload.ReadyReplicas),
	)

	return nil
}

// RemoveReplica removes a replica from a workload
func (wsm *WorkloadStateManager) RemoveReplica(ctx context.Context, workloadID, replicaID string) error {
	workload, err := wsm.GetWorkload(ctx, workloadID)
	if err != nil {
		return err
	}

	// Find and remove replica
	newReplicas := make([]ReplicaState, 0, len(workload.Replicas))
	for _, replica := range workload.Replicas {
		if replica.ID != replicaID {
			newReplicas = append(newReplicas, replica)
		}
	}

	workload.Replicas = newReplicas
	workload.CurrentReplicas = int32(len(workload.Replicas))

	return wsm.SaveWorkload(ctx, workload)
}

// loadFromRAFT loads all workloads from RAFT into cache
func (wsm *WorkloadStateManager) loadFromRAFT() error {
	// Get all keys with workload prefix
	keys, err := wsm.store.ListKeys("workload:")
	if err != nil {
		return fmt.Errorf("failed to list keys: %w", err)
	}

	wsm.logger.Info("Loading workloads from RAFT",
		zap.Int("count", len(keys)),
	)

	wsm.mu.Lock()
	defer wsm.mu.Unlock()

	loaded := 0
	for _, key := range keys {
		data, err := wsm.store.Get(key)
		if err != nil {
			wsm.logger.Warn("Failed to load workload",
				zap.String("key", key),
				zap.Error(err),
			)
			continue
		}

		var workload WorkloadState
		if err := json.Unmarshal(data, &workload); err != nil {
			wsm.logger.Warn("Failed to unmarshal workload",
				zap.String("key", key),
				zap.Error(err),
			)
			continue
		}

		wsm.cache[workload.ID] = &workload
		loaded++
	}

	wsm.logger.Info("Loaded workloads from RAFT",
		zap.Int("loaded", loaded),
		zap.Int("failed", len(keys)-loaded),
	)

	return nil
}
