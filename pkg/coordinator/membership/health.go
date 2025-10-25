package membership

import (
	"sync"
	"time"

	"go.uber.org/zap"
)

// HealthMonitor monitors node health and detects failures
type HealthMonitor struct {
	logger           *zap.Logger
	heartbeatTimeout time.Duration
	healthRecords    map[string]*HealthRecord
	mu               sync.RWMutex
}

// HealthRecord tracks health metrics for a node
type HealthRecord struct {
	NodeID            string
	ConsecutiveMisses int
	LastHealthy       time.Time
	LastUnhealthy     time.Time
	TotalHeartbeats   int64
	MissedHeartbeats  int64
	Uptime            time.Duration
	DownTime          time.Duration
	LastTransition    time.Time
	CurrentState      string
	HealthHistory     []HealthEvent
}

// HealthEvent represents a health state change
type HealthEvent struct {
	Timestamp time.Time
	FromState string
	ToState   string
	Reason    string
}

// NewHealthMonitor creates a new health monitor
func NewHealthMonitor(logger *zap.Logger, heartbeatTimeout time.Duration) *HealthMonitor {
	return &HealthMonitor{
		logger:           logger,
		heartbeatTimeout: heartbeatTimeout,
		healthRecords:    make(map[string]*HealthRecord),
	}
}

// RecordHeartbeat records a successful heartbeat
func (h *HealthMonitor) RecordHeartbeat(nodeID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	record, exists := h.healthRecords[nodeID]
	if !exists {
		record = &HealthRecord{
			NodeID:       nodeID,
			LastHealthy:  time.Now(),
			CurrentState: "healthy",
		}
		h.healthRecords[nodeID] = record
	}

	now := time.Now()
	record.LastHealthy = now
	record.TotalHeartbeats++
	record.ConsecutiveMisses = 0

	// Update state if recovering
	if record.CurrentState != "healthy" {
		h.recordTransition(record, record.CurrentState, "healthy", "Heartbeat received")

		// Update downtime
		if !record.LastUnhealthy.IsZero() {
			record.DownTime += now.Sub(record.LastUnhealthy)
		}
	}
	record.CurrentState = "healthy"
}

// RecordMiss records a missed heartbeat
func (h *HealthMonitor) RecordMiss(nodeID string) {
	h.mu.Lock()
	defer h.mu.Unlock()

	record, exists := h.healthRecords[nodeID]
	if !exists {
		record = &HealthRecord{
			NodeID:       nodeID,
			CurrentState: "unknown",
		}
		h.healthRecords[nodeID] = record
	}

	now := time.Now()
	record.ConsecutiveMisses++
	record.MissedHeartbeats++
	record.LastUnhealthy = now

	// Determine new state based on consecutive misses
	var newState string
	switch {
	case record.ConsecutiveMisses >= 6: // 60 seconds with 10s heartbeat
		newState = "failed"
	case record.ConsecutiveMisses >= 3: // 30 seconds
		newState = "unhealthy"
	case record.ConsecutiveMisses >= 1:
		newState = "degraded"
	default:
		newState = "healthy"
	}

	if newState != record.CurrentState {
		h.recordTransition(record, record.CurrentState, newState, "Missed heartbeats")

		// Update uptime if transitioning from healthy
		if record.CurrentState == "healthy" && !record.LastHealthy.IsZero() {
			record.Uptime += now.Sub(record.LastHealthy)
		}
	}
	record.CurrentState = newState
}

// GetHealthStatus returns the current health status of a node
func (h *HealthMonitor) GetHealthStatus(nodeID string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	record, exists := h.healthRecords[nodeID]
	if !exists {
		return "unknown"
	}

	return record.CurrentState
}

// GetHealthRecord returns the complete health record for a node
func (h *HealthMonitor) GetHealthRecord(nodeID string) *HealthRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()

	record, exists := h.healthRecords[nodeID]
	if !exists {
		return nil
	}

	// Return a copy
	recordCopy := *record
	recordCopy.HealthHistory = make([]HealthEvent, len(record.HealthHistory))
	copy(recordCopy.HealthHistory, record.HealthHistory)

	return &recordCopy
}

// GetAvailability calculates the availability percentage for a node
func (h *HealthMonitor) GetAvailability(nodeID string) float64 {
	h.mu.RLock()
	defer h.mu.RUnlock()

	record, exists := h.healthRecords[nodeID]
	if !exists {
		return 0
	}

	if record.TotalHeartbeats == 0 {
		return 0
	}

	successRate := float64(record.TotalHeartbeats-record.MissedHeartbeats) / float64(record.TotalHeartbeats)
	return successRate * 100
}

// recordTransition records a health state transition
func (h *HealthMonitor) recordTransition(record *HealthRecord, from, to, reason string) {
	event := HealthEvent{
		Timestamp: time.Now(),
		FromState: from,
		ToState:   to,
		Reason:    reason,
	}

	record.HealthHistory = append(record.HealthHistory, event)
	record.LastTransition = event.Timestamp

	// Keep only last 100 events
	if len(record.HealthHistory) > 100 {
		record.HealthHistory = record.HealthHistory[len(record.HealthHistory)-100:]
	}

	h.logger.Info("Node health transition",
		zap.String("node_id", record.NodeID),
		zap.String("from", from),
		zap.String("to", to),
		zap.String("reason", reason),
	)
}

// CleanupOldRecords removes health records for nodes that have been gone for too long
func (h *HealthMonitor) CleanupOldRecords(maxAge time.Duration) int {
	h.mu.Lock()
	defer h.mu.Unlock()

	now := time.Now()
	removed := 0

	for nodeID, record := range h.healthRecords {
		lastSeen := record.LastHealthy
		if record.LastUnhealthy.After(lastSeen) {
			lastSeen = record.LastUnhealthy
		}

		if now.Sub(lastSeen) > maxAge {
			delete(h.healthRecords, nodeID)
			removed++
		}
	}

	if removed > 0 {
		h.logger.Info("Cleaned up old health records", zap.Int("removed", removed))
	}

	return removed
}

// GetUnhealthyNodes returns a list of unhealthy node IDs
func (h *HealthMonitor) GetUnhealthyNodes() []string {
	h.mu.RLock()
	defer h.mu.RUnlock()

	unhealthy := []string{}
	for nodeID, record := range h.healthRecords {
		if record.CurrentState != "healthy" {
			unhealthy = append(unhealthy, nodeID)
		}
	}

	return unhealthy
}

// GetStats returns overall health statistics
func (h *HealthMonitor) GetStats() HealthStats {
	h.mu.RLock()
	defer h.mu.RUnlock()

	stats := HealthStats{
		TotalNodes: len(h.healthRecords),
	}

	for _, record := range h.healthRecords {
		switch record.CurrentState {
		case "healthy":
			stats.HealthyNodes++
		case "degraded":
			stats.DegradedNodes++
		case "unhealthy":
			stats.UnhealthyNodes++
		case "failed":
			stats.FailedNodes++
		default:
			stats.UnknownNodes++
		}
	}

	return stats
}

// HealthStats contains aggregate health statistics
type HealthStats struct {
	TotalNodes     int `json:"total_nodes"`
	HealthyNodes   int `json:"healthy_nodes"`
	DegradedNodes  int `json:"degraded_nodes"`
	UnhealthyNodes int `json:"unhealthy_nodes"`
	FailedNodes    int `json:"failed_nodes"`
	UnknownNodes   int `json:"unknown_nodes"`
}