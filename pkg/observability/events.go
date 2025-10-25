package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// EventType represents the type of event
type EventType string

const (
	// Membership events
	EventNodeEnrolled     EventType = "node.enrolled"
	EventNodeDrained      EventType = "node.drained"
	EventNodeUncordoned   EventType = "node.uncordoned"
	EventNodeOffline      EventType = "node.offline"
	EventNodeFailed       EventType = "node.failed"
	EventNodeRemoved      EventType = "node.removed"

	// Workload events
	EventWorkloadCreated   EventType = "workload.created"
	EventWorkloadUpdated   EventType = "workload.updated"
	EventWorkloadDeleted   EventType = "workload.deleted"
	EventWorkloadScaled    EventType = "workload.scaled"
	EventWorkloadScheduled EventType = "workload.scheduled"
	EventWorkloadFailed    EventType = "workload.failed"

	// Scheduling events
	EventSchedulingDecision EventType = "scheduling.decision"
	EventRescheduling       EventType = "scheduling.reschedule"
	EventPlacementFailed    EventType = "scheduling.placement_failed"

	// Security events
	EventAuthenticationSuccess EventType = "security.auth_success"
	EventAuthenticationFailed  EventType = "security.auth_failed"
	EventAuthorizationDenied   EventType = "security.authz_denied"
	EventCertificateIssued     EventType = "security.cert_issued"
	EventCertificateRevoked    EventType = "security.cert_revoked"
	EventPolicyViolation       EventType = "security.policy_violation"

	// Storage events
	EventObjectCreated  EventType = "storage.object_created"
	EventObjectDeleted  EventType = "storage.object_deleted"
	EventRepairStarted  EventType = "storage.repair_started"
	EventRepairComplete EventType = "storage.repair_complete"

	// Network events
	EventConnectionEstablished EventType = "network.connection_established"
	EventConnectionFailed      EventType = "network.connection_failed"
	EventNATTraversal          EventType = "network.nat_traversal"

	// Coordinator events
	EventLeaderElected   EventType = "coordinator.leader_elected"
	EventLeaderStepDown  EventType = "coordinator.leader_stepdown"
	EventRAFTSnapshot    EventType = "coordinator.raft_snapshot"
)

// EventSeverity represents the severity level of an event
type EventSeverity string

const (
	SeverityInfo     EventSeverity = "info"
	SeverityWarning  EventSeverity = "warning"
	SeverityError    EventSeverity = "error"
	SeverityCritical EventSeverity = "critical"
)

// Event represents an audit event
type Event struct {
	// Event metadata
	ID        string        `json:"id"`
	Type      EventType     `json:"type"`
	Severity  EventSeverity `json:"severity"`
	Timestamp time.Time     `json:"timestamp"`

	// Correlation IDs
	RequestID     string `json:"request_id,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`

	// Actor information
	ActorType string `json:"actor_type,omitempty"` // user, system, node
	ActorID   string `json:"actor_id,omitempty"`

	// Resource information
	ResourceType string `json:"resource_type,omitempty"` // node, workload, storage, etc.
	ResourceID   string `json:"resource_id,omitempty"`

	// Event details
	Action      string                 `json:"action"`
	Description string                 `json:"description"`
	Metadata    map[string]interface{} `json:"metadata,omitempty"`

	// Outcome
	Success bool   `json:"success"`
	Error   string `json:"error,omitempty"`
}

// EventStream manages the event stream for audit logging
type EventStream struct {
	logger   *zap.Logger
	events   []Event
	mu       sync.RWMutex
	maxSize  int
	watchers []chan Event
}

// EventStreamConfig holds configuration for the event stream
type EventStreamConfig struct {
	MaxSize   int           // Maximum number of events to keep in memory
	Retention time.Duration // How long to retain events
}

// NewEventStream creates a new event stream
func NewEventStream(cfg EventStreamConfig, logger *zap.Logger) *EventStream {
	if cfg.MaxSize == 0 {
		cfg.MaxSize = 10000 // Default to 10k events
	}

	return &EventStream{
		logger:   logger,
		events:   make([]Event, 0, cfg.MaxSize),
		maxSize:  cfg.MaxSize,
		watchers: make([]chan Event, 0),
	}
}

// RecordEvent records a new event to the stream
func (es *EventStream) RecordEvent(ctx context.Context, event Event) {
	es.mu.Lock()
	defer es.mu.Unlock()

	// Set timestamp if not provided
	if event.Timestamp.IsZero() {
		event.Timestamp = time.Now()
	}

	// Generate ID if not provided
	if event.ID == "" {
		event.ID = GenerateRequestID()
	}

	// Add correlation IDs from context if available
	if event.RequestID == "" {
		event.RequestID = GetRequestID(ctx)
	}
	if event.CorrelationID == "" {
		event.CorrelationID = GetCorrelationID(ctx)
	}

	// Append event
	es.events = append(es.events, event)

	// Trim if exceeds max size (FIFO)
	if len(es.events) > es.maxSize {
		es.events = es.events[len(es.events)-es.maxSize:]
	}

	// Log the event
	es.logEvent(event)

	// Notify watchers
	for _, ch := range es.watchers {
		select {
		case ch <- event:
		default:
			// Channel full, skip
		}
	}
}

// logEvent logs the event using structured logging
func (es *EventStream) logEvent(event Event) {
	fields := []zap.Field{
		zap.String("event_id", event.ID),
		zap.String("event_type", string(event.Type)),
		zap.String("severity", string(event.Severity)),
		zap.String("action", event.Action),
		zap.Bool("success", event.Success),
	}

	if event.RequestID != "" {
		fields = append(fields, zap.String("request_id", event.RequestID))
	}
	if event.CorrelationID != "" {
		fields = append(fields, zap.String("correlation_id", event.CorrelationID))
	}
	if event.ActorID != "" {
		fields = append(fields, zap.String("actor_id", event.ActorID))
	}
	if event.ResourceID != "" {
		fields = append(fields, zap.String("resource_id", event.ResourceID))
	}
	if event.Error != "" {
		fields = append(fields, zap.String("error", event.Error))
	}

	// Log at appropriate level based on severity
	switch event.Severity {
	case SeverityInfo:
		es.logger.Info(event.Description, fields...)
	case SeverityWarning:
		es.logger.Warn(event.Description, fields...)
	case SeverityError:
		es.logger.Error(event.Description, fields...)
	case SeverityCritical:
		es.logger.Error(fmt.Sprintf("CRITICAL: %s", event.Description), fields...)
	}
}

// GetEvents retrieves events with optional filtering
func (es *EventStream) GetEvents(filter EventFilter) []Event {
	es.mu.RLock()
	defer es.mu.RUnlock()

	result := make([]Event, 0)

	for _, event := range es.events {
		if filter.Matches(event) {
			result = append(result, event)
		}
	}

	return result
}

// Watch creates a channel that receives new events
func (es *EventStream) Watch() chan Event {
	es.mu.Lock()
	defer es.mu.Unlock()

	ch := make(chan Event, 100)
	es.watchers = append(es.watchers, ch)

	return ch
}

// Unwatch removes a watcher channel
func (es *EventStream) Unwatch(ch chan Event) {
	es.mu.Lock()
	defer es.mu.Unlock()

	for i, watcher := range es.watchers {
		if watcher == ch {
			es.watchers = append(es.watchers[:i], es.watchers[i+1:]...)
			close(ch)
			break
		}
	}
}

// Export exports events as JSON
func (es *EventStream) Export() ([]byte, error) {
	es.mu.RLock()
	defer es.mu.RUnlock()

	return json.MarshalIndent(es.events, "", "  ")
}

// EventFilter defines filtering criteria for events
type EventFilter struct {
	Types         []EventType
	Severities    []EventSeverity
	ActorID       string
	ResourceType  string
	ResourceID    string
	CorrelationID string
	StartTime     time.Time
	EndTime       time.Time
	Limit         int
}

// Matches checks if an event matches the filter
func (f EventFilter) Matches(event Event) bool {
	// Filter by type
	if len(f.Types) > 0 {
		found := false
		for _, t := range f.Types {
			if event.Type == t {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by severity
	if len(f.Severities) > 0 {
		found := false
		for _, s := range f.Severities {
			if event.Severity == s {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}

	// Filter by actor
	if f.ActorID != "" && event.ActorID != f.ActorID {
		return false
	}

	// Filter by resource type
	if f.ResourceType != "" && event.ResourceType != f.ResourceType {
		return false
	}

	// Filter by resource ID
	if f.ResourceID != "" && event.ResourceID != f.ResourceID {
		return false
	}

	// Filter by correlation ID
	if f.CorrelationID != "" && event.CorrelationID != f.CorrelationID {
		return false
	}

	// Filter by time range
	if !f.StartTime.IsZero() && event.Timestamp.Before(f.StartTime) {
		return false
	}
	if !f.EndTime.IsZero() && event.Timestamp.After(f.EndTime) {
		return false
	}

	return true
}

// Helper functions for creating specific events

// NewNodeEnrolledEvent creates a node enrolled event
func NewNodeEnrolledEvent(nodeID, region, zone string) Event {
	return Event{
		Type:         EventNodeEnrolled,
		Severity:     SeverityInfo,
		ActorType:    "system",
		ResourceType: "node",
		ResourceID:   nodeID,
		Action:       "enroll",
		Description:  fmt.Sprintf("Node %s enrolled in region %s, zone %s", nodeID, region, zone),
		Metadata: map[string]interface{}{
			"region": region,
			"zone":   zone,
		},
		Success: true,
	}
}

// NewSchedulingDecisionEvent creates a scheduling decision event
func NewSchedulingDecisionEvent(workloadID, nodeID string, score float64, success bool) Event {
	severity := SeverityInfo
	if !success {
		severity = SeverityWarning
	}

	return Event{
		Type:         EventSchedulingDecision,
		Severity:     severity,
		ActorType:    "system",
		ResourceType: "workload",
		ResourceID:   workloadID,
		Action:       "schedule",
		Description:  fmt.Sprintf("Workload %s scheduled to node %s (score: %.2f)", workloadID, nodeID, score),
		Metadata: map[string]interface{}{
			"node_id": nodeID,
			"score":   score,
		},
		Success: success,
	}
}

// NewAuthenticationFailedEvent creates an authentication failed event
func NewAuthenticationFailedEvent(actorID, reason string) Event {
	return Event{
		Type:        EventAuthenticationFailed,
		Severity:    SeverityWarning,
		ActorType:   "user",
		ActorID:     actorID,
		Action:      "authenticate",
		Description: fmt.Sprintf("Authentication failed for %s: %s", actorID, reason),
		Metadata: map[string]interface{}{
			"reason": reason,
		},
		Success: false,
		Error:   reason,
	}
}

// NewPolicyViolationEvent creates a policy violation event
func NewPolicyViolationEvent(actorID, resourceType, resourceID, policy, reason string) Event {
	return Event{
		Type:         EventPolicyViolation,
		Severity:     SeverityCritical,
		ActorType:    "user",
		ActorID:      actorID,
		ResourceType: resourceType,
		ResourceID:   resourceID,
		Action:       "policy_check",
		Description:  fmt.Sprintf("Policy violation: %s attempted to access %s/%s, denied by policy %s", actorID, resourceType, resourceID, policy),
		Metadata: map[string]interface{}{
			"policy": policy,
			"reason": reason,
		},
		Success: false,
		Error:   reason,
	}
}

// NewWorkloadCreatedEvent creates a workload created event
func NewWorkloadCreatedEvent(workloadID, namespace, image string, replicas int32) Event {
	return Event{
		Type:         EventWorkloadCreated,
		Severity:     SeverityInfo,
		ActorType:    "user",
		ResourceType: "workload",
		ResourceID:   workloadID,
		Action:       "create",
		Description:  fmt.Sprintf("Workload %s created in namespace %s", workloadID, namespace),
		Metadata: map[string]interface{}{
			"namespace": namespace,
			"image":     image,
			"replicas":  replicas,
		},
		Success: true,
	}
}
