package observability

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

func TestEventStream_RecordEvent(t *testing.T) {
	logger := zap.NewNop()
	cfg := EventStreamConfig{
		MaxSize:   100,
		Retention: 24 * time.Hour,
	}

	es := NewEventStream(cfg, logger)

	// Record an event
	event := Event{
		Type:        EventNodeEnrolled,
		Severity:    SeverityInfo,
		Action:      "enroll",
		Description: "Test node enrolled",
		Success:     true,
	}

	ctx := context.Background()
	es.RecordEvent(ctx, event)

	// Verify event was recorded
	events := es.GetEvents(EventFilter{})
	if len(events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(events))
	}

	// Verify event has ID and timestamp
	if events[0].ID == "" {
		t.Error("Event should have an ID")
	}
	if events[0].Timestamp.IsZero() {
		t.Error("Event should have a timestamp")
	}
}

func TestEventStream_FilterByType(t *testing.T) {
	logger := zap.NewNop()
	cfg := EventStreamConfig{
		MaxSize:   100,
		Retention: 24 * time.Hour,
	}

	es := NewEventStream(cfg, logger)
	ctx := context.Background()

	// Record events of different types
	es.RecordEvent(ctx, Event{Type: EventNodeEnrolled, Severity: SeverityInfo})
	es.RecordEvent(ctx, Event{Type: EventNodeDrained, Severity: SeverityInfo})
	es.RecordEvent(ctx, Event{Type: EventWorkloadCreated, Severity: SeverityInfo})
	es.RecordEvent(ctx, Event{Type: EventNodeEnrolled, Severity: SeverityInfo})

	// Filter by type
	filter := EventFilter{
		Types: []EventType{EventNodeEnrolled},
	}

	events := es.GetEvents(filter)

	if len(events) != 2 {
		t.Errorf("Expected 2 events of type NodeEnrolled, got %d", len(events))
	}

	for _, event := range events {
		if event.Type != EventNodeEnrolled {
			t.Errorf("Expected type NodeEnrolled, got %s", event.Type)
		}
	}
}

func TestEventStream_FilterBySeverity(t *testing.T) {
	logger := zap.NewNop()
	cfg := EventStreamConfig{
		MaxSize:   100,
		Retention: 24 * time.Hour,
	}

	es := NewEventStream(cfg, logger)
	ctx := context.Background()

	// Record events of different severities
	es.RecordEvent(ctx, Event{Type: EventNodeEnrolled, Severity: SeverityInfo})
	es.RecordEvent(ctx, Event{Type: EventAuthenticationFailed, Severity: SeverityWarning})
	es.RecordEvent(ctx, Event{Type: EventPolicyViolation, Severity: SeverityCritical})
	es.RecordEvent(ctx, Event{Type: EventNodeFailed, Severity: SeverityError})

	// Filter by severity
	filter := EventFilter{
		Severities: []EventSeverity{SeverityCritical, SeverityError},
	}

	events := es.GetEvents(filter)

	if len(events) != 2 {
		t.Errorf("Expected 2 events with high severity, got %d", len(events))
	}

	for _, event := range events {
		if event.Severity != SeverityCritical && event.Severity != SeverityError {
			t.Errorf("Expected critical or error severity, got %s", event.Severity)
		}
	}
}

func TestEventStream_FilterByResource(t *testing.T) {
	logger := zap.NewNop()
	cfg := EventStreamConfig{
		MaxSize:   100,
		Retention: 24 * time.Hour,
	}

	es := NewEventStream(cfg, logger)
	ctx := context.Background()

	// Record events for different resources
	es.RecordEvent(ctx, Event{
		Type:         EventNodeEnrolled,
		ResourceType: "node",
		ResourceID:   "node-1",
	})
	es.RecordEvent(ctx, Event{
		Type:         EventWorkloadCreated,
		ResourceType: "workload",
		ResourceID:   "wl-1",
	})
	es.RecordEvent(ctx, Event{
		Type:         EventNodeDrained,
		ResourceType: "node",
		ResourceID:   "node-2",
	})

	// Filter by resource type
	filter := EventFilter{
		ResourceType: "node",
	}

	events := es.GetEvents(filter)

	if len(events) != 2 {
		t.Errorf("Expected 2 node events, got %d", len(events))
	}

	// Filter by resource ID
	filter = EventFilter{
		ResourceID: "node-1",
	}

	events = es.GetEvents(filter)

	if len(events) != 1 {
		t.Errorf("Expected 1 event for node-1, got %d", len(events))
	}
}

func TestEventStream_FilterByTimeRange(t *testing.T) {
	logger := zap.NewNop()
	cfg := EventStreamConfig{
		MaxSize:   100,
		Retention: 24 * time.Hour,
	}

	es := NewEventStream(cfg, logger)
	ctx := context.Background()

	now := time.Now()

	// Record events at different times
	event1 := Event{Type: EventNodeEnrolled, Timestamp: now.Add(-2 * time.Hour)}
	event2 := Event{Type: EventNodeDrained, Timestamp: now.Add(-1 * time.Hour)}
	event3 := Event{Type: EventWorkloadCreated, Timestamp: now}

	es.RecordEvent(ctx, event1)
	es.RecordEvent(ctx, event2)
	es.RecordEvent(ctx, event3)

	// Filter by time range (last hour)
	filter := EventFilter{
		StartTime: now.Add(-90 * time.Minute),
	}

	events := es.GetEvents(filter)

	if len(events) != 2 {
		t.Errorf("Expected 2 events in the last 90 minutes, got %d", len(events))
	}
}

func TestEventStream_MaxSize(t *testing.T) {
	logger := zap.NewNop()
	cfg := EventStreamConfig{
		MaxSize:   5,
		Retention: 24 * time.Hour,
	}

	es := NewEventStream(cfg, logger)
	ctx := context.Background()

	// Record more events than max size
	for i := 0; i < 10; i++ {
		es.RecordEvent(ctx, Event{
			Type:     EventNodeEnrolled,
			Severity: SeverityInfo,
		})
	}

	events := es.GetEvents(EventFilter{})

	if len(events) != 5 {
		t.Errorf("Expected 5 events (max size), got %d", len(events))
	}
}

func TestEventStream_Watch(t *testing.T) {
	logger := zap.NewNop()
	cfg := EventStreamConfig{
		MaxSize:   100,
		Retention: 24 * time.Hour,
	}

	es := NewEventStream(cfg, logger)
	ctx := context.Background()

	// Create a watcher
	watcher := es.Watch()
	defer es.Unwatch(watcher)

	// Record an event
	event := Event{
		Type:        EventNodeEnrolled,
		Severity:    SeverityInfo,
		Description: "Test event",
	}

	es.RecordEvent(ctx, event)

	// Receive event from watcher
	select {
	case receivedEvent := <-watcher:
		if receivedEvent.Type != EventNodeEnrolled {
			t.Errorf("Expected NodeEnrolled event, got %s", receivedEvent.Type)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for event from watcher")
	}
}

func TestEventStream_CorrelationIDs(t *testing.T) {
	logger := zap.NewNop()
	cfg := EventStreamConfig{
		MaxSize:   100,
		Retention: 24 * time.Hour,
	}

	es := NewEventStream(cfg, logger)

	// Create context with correlation IDs
	ctx := WithRequestID(context.Background(), "req-123")
	ctx = WithCorrelationID(ctx, "corr-456")

	// Record event
	event := Event{
		Type:        EventNodeEnrolled,
		Severity:    SeverityInfo,
		Description: "Test event",
	}

	es.RecordEvent(ctx, event)

	// Verify correlation IDs were added
	events := es.GetEvents(EventFilter{})
	if len(events) != 1 {
		t.Fatal("Expected 1 event")
	}

	if events[0].RequestID != "req-123" {
		t.Errorf("Expected request ID req-123, got %s", events[0].RequestID)
	}
	if events[0].CorrelationID != "corr-456" {
		t.Errorf("Expected correlation ID corr-456, got %s", events[0].CorrelationID)
	}
}

func TestNewNodeEnrolledEvent(t *testing.T) {
	event := NewNodeEnrolledEvent("node-1", "us-east", "us-east-1a")

	if event.Type != EventNodeEnrolled {
		t.Errorf("Expected type NodeEnrolled, got %s", event.Type)
	}
	if event.ResourceID != "node-1" {
		t.Errorf("Expected resource ID node-1, got %s", event.ResourceID)
	}
	if event.Severity != SeverityInfo {
		t.Errorf("Expected severity Info, got %s", event.Severity)
	}
	if !event.Success {
		t.Error("Event should be successful")
	}
}

func TestNewSchedulingDecisionEvent(t *testing.T) {
	// Successful scheduling
	event := NewSchedulingDecisionEvent("wl-1", "node-1", 85.5, true)

	if event.Type != EventSchedulingDecision {
		t.Errorf("Expected type SchedulingDecision, got %s", event.Type)
	}
	if event.Severity != SeverityInfo {
		t.Errorf("Expected severity Info for successful scheduling, got %s", event.Severity)
	}

	// Failed scheduling
	event = NewSchedulingDecisionEvent("wl-1", "node-1", 45.0, false)
	if event.Severity != SeverityWarning {
		t.Errorf("Expected severity Warning for failed scheduling, got %s", event.Severity)
	}
}

func TestNewPolicyViolationEvent(t *testing.T) {
	event := NewPolicyViolationEvent("user-1", "workload", "wl-1", "image-policy", "unauthorized image")

	if event.Type != EventPolicyViolation {
		t.Errorf("Expected type PolicyViolation, got %s", event.Type)
	}
	if event.Severity != SeverityCritical {
		t.Errorf("Expected severity Critical, got %s", event.Severity)
	}
	if event.Success {
		t.Error("Policy violation should not be successful")
	}
	if event.Error == "" {
		t.Error("Policy violation should have an error message")
	}
}

func TestEventStream_Export(t *testing.T) {
	logger := zap.NewNop()
	cfg := EventStreamConfig{
		MaxSize:   100,
		Retention: 24 * time.Hour,
	}

	es := NewEventStream(cfg, logger)
	ctx := context.Background()

	// Record some events
	es.RecordEvent(ctx, Event{Type: EventNodeEnrolled, Severity: SeverityInfo})
	es.RecordEvent(ctx, Event{Type: EventWorkloadCreated, Severity: SeverityInfo})

	// Export as JSON
	jsonData, err := es.Export()
	if err != nil {
		t.Fatalf("Failed to export events: %v", err)
	}

	if len(jsonData) == 0 {
		t.Error("Exported JSON should not be empty")
	}
}
