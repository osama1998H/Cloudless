package observability

import (
	"context"
	"fmt"
	"testing"
	"time"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// BenchmarkMetricsRecording benchmarks Prometheus metrics recording overhead
func BenchmarkMetricsRecording(b *testing.B) {
	tests := []struct {
		name   string
		metric func()
	}{
		{
			name: "counter_increment",
			metric: func() {
				SchedulingDecisionsTotal.WithLabelValues("success", "best-fit").Inc()
			},
		},
		{
			name: "gauge_set",
			metric: func() {
				NodesByState.WithLabelValues("ready").Set(100)
			},
		},
		{
			name: "histogram_observe",
			metric: func() {
				SchedulingDurationSeconds.WithLabelValues("best-fit").Observe(0.150)
			},
		},
		{
			name: "summary_observe",
			metric: func() {
				PlacementScoreDistribution.WithLabelValues("us-east-1a").Observe(85.5)
			},
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				tt.metric()
			}

			opsPerSec := float64(b.N) / b.Elapsed().Seconds()
			b.ReportMetric(opsPerSec, "ops/sec")
		})
	}
}

// BenchmarkMetricsWithMultipleLabels benchmarks metrics with varying label cardinality
func BenchmarkMetricsWithMultipleLabels(b *testing.B) {
	tests := []struct {
		name        string
		labelCount  int
	}{
		{"1_label", 1},
		{"2_labels", 2},
		{"4_labels", 4},
		{"8_labels", 8},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			labels := make([]string, tt.labelCount)
			for i := 0; i < tt.labelCount; i++ {
				labels[i] = fmt.Sprintf("label%d", i)
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				switch tt.labelCount {
				case 1:
					SchedulingDecisionsTotal.WithLabelValues("success").Inc()
				case 2:
					SchedulingDurationSeconds.WithLabelValues("best-fit").Observe(0.100)
				case 4:
					StorageOperationsTotal.WithLabelValues("write", "success", "node-1", "local").Inc()
				case 8:
					// Use a metric with many labels
					NodeReliabilityScore.WithLabelValues("node-1", "us-east-1a").Set(0.95)
				}
			}
		})
	}
}

// BenchmarkTracingOverhead benchmarks OpenTelemetry tracing overhead
func BenchmarkTracingOverhead(b *testing.B) {
	logger, _ := zap.NewDevelopment()

	// Create tracer provider with sampling
	config := TracerConfig{
		ServiceName:    "benchmark-test",
		ServiceVersion: "1.0.0",
		OTLPEndpoint:   "localhost:4317",
		SamplingRate:   1.0, // Always sample for benchmark
		Environment:    "test",
	}

	provider, err := NewTracerProvider(config, logger)
	if err != nil {
		b.Fatalf("Failed to create tracer provider: %v", err)
	}
	defer provider.Shutdown(context.Background())

	tracer := provider.Tracer("benchmark")

	tests := []struct {
		name       string
		spanDepth  int
	}{
		{"single_span", 1},
		{"nested_3_spans", 3},
		{"nested_5_spans", 5},
		{"nested_10_spans", 10},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				ctx := context.Background()
				createNestedSpans(ctx, tracer, tt.spanDepth)
			}
		})
	}
}

// BenchmarkSpanCreation benchmarks just span creation without nesting
func BenchmarkSpanCreation(b *testing.B) {
	logger, _ := zap.NewDevelopment()

	config := TracerConfig{
		ServiceName:    "benchmark-test",
		ServiceVersion: "1.0.0",
		OTLPEndpoint:   "localhost:4317",
		SamplingRate:   1.0,
		Environment:    "test",
	}

	provider, err := NewTracerProvider(config, logger)
	if err != nil {
		b.Fatalf("Failed to create tracer provider: %v", err)
	}
	defer provider.Shutdown(context.Background())

	tracer := provider.Tracer("benchmark")
	ctx := context.Background()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, span := tracer.Start(ctx, "benchmark-span")
		span.End()
	}

	opsPerSec := float64(b.N) / b.Elapsed().Seconds()
	b.ReportMetric(opsPerSec, "spans/sec")
}

// BenchmarkSpanWithAttributes benchmarks span creation with attributes
func BenchmarkSpanWithAttributes(b *testing.B) {
	logger, _ := zap.NewDevelopment()

	config := TracerConfig{
		ServiceName:    "benchmark-test",
		ServiceVersion: "1.0.0",
		OTLPEndpoint:   "localhost:4317",
		SamplingRate:   1.0,
		Environment:    "test",
	}

	provider, err := NewTracerProvider(config, logger)
	if err != nil {
		b.Fatalf("Failed to create tracer provider: %v", err)
	}
	defer provider.Shutdown(context.Background())

	tracer := provider.Tracer("benchmark")
	ctx := context.Background()

	tests := []struct {
		name      string
		attrCount int
	}{
		{"no_attributes", 0},
		{"3_attributes", 3},
		{"10_attributes", 10},
		{"20_attributes", 20},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_, span := tracer.Start(ctx, "benchmark-span")

				for j := 0; j < tt.attrCount; j++ {
					span.SetAttributes(
						trace.StringAttribute(fmt.Sprintf("attr%d", j), fmt.Sprintf("value%d", j)),
					)
				}

				span.End()
			}
		})
	}
}

// BenchmarkContextPropagation benchmarks correlation ID context propagation
func BenchmarkContextPropagation(b *testing.B) {
	ctx := context.Background()
	logger, _ := zap.NewDevelopment()

	tests := []struct {
		name       string
		operations int
	}{
		{"single_operation", 1},
		{"5_operations", 5},
		{"10_operations", 10},
		{"20_operations", 20},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				ctx := WithRequestID(ctx, GenerateRequestID())
				ctx = WithCorrelationID(ctx, GenerateCorrelationID())
				ctx = WithUserID(ctx, "user-123")

				// Simulate operations that extract context
				for j := 0; j < tt.operations; j++ {
					_ = GetRequestID(ctx)
					_ = GetCorrelationID(ctx)
					_ = GetUserID(ctx)
					_ = ContextLogger(ctx, logger)
				}
			}
		})
	}
}

// BenchmarkEventRecording benchmarks event stream recording
func BenchmarkEventRecording(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	es := NewEventStream(10000, logger)
	ctx := context.Background()

	tests := []struct {
		name         string
		eventType    EventType
		severity     EventSeverity
		metadataSize int
	}{
		{"simple_info_event", EventNodeEnrolled, SeverityInfo, 0},
		{"warning_with_metadata", EventNodeUnhealthy, SeverityWarning, 5},
		{"error_with_large_metadata", EventWorkloadFailed, SeverityError, 20},
		{"critical_with_metadata", EventSecurityViolation, SeverityCritical, 10},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			event := Event{
				Type:         tt.eventType,
				Severity:     tt.severity,
				ActorType:    "user",
				ActorID:      "user-123",
				ResourceType: "node",
				ResourceID:   "node-456",
				Action:       "test-action",
				Description:  "Benchmark event",
				Success:      true,
			}

			if tt.metadataSize > 0 {
				event.Metadata = make(map[string]interface{}, tt.metadataSize)
				for i := 0; i < tt.metadataSize; i++ {
					event.Metadata[fmt.Sprintf("key%d", i)] = fmt.Sprintf("value%d", i)
				}
			}

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				es.RecordEvent(ctx, event)
			}

			opsPerSec := float64(b.N) / b.Elapsed().Seconds()
			b.ReportMetric(opsPerSec, "events/sec")
		})
	}
}

// BenchmarkEventFiltering benchmarks event filtering operations
func BenchmarkEventFiltering(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	es := NewEventStream(10000, logger)
	ctx := context.Background()

	// Setup: populate event stream
	eventTypes := []EventType{
		EventNodeEnrolled, EventNodeDrained, EventNodeOffline,
		EventWorkloadCreated, EventWorkloadUpdated, EventWorkloadDeleted,
	}
	severities := []EventSeverity{SeverityInfo, SeverityWarning, SeverityError}

	for i := 0; i < 1000; i++ {
		event := Event{
			Type:         eventTypes[i%len(eventTypes)],
			Severity:     severities[i%len(severities)],
			ResourceType: "node",
			ResourceID:   fmt.Sprintf("resource-%d", i%100),
			Action:       "test",
			Description:  "Test event",
		}
		es.RecordEvent(ctx, event)
	}

	tests := []struct {
		name   string
		filter EventFilter
	}{
		{
			name: "filter_by_type",
			filter: EventFilter{
				Types: []EventType{EventNodeEnrolled, EventNodeDrained},
			},
		},
		{
			name: "filter_by_severity",
			filter: EventFilter{
				MinSeverity: SeverityWarning,
			},
		},
		{
			name: "filter_by_resource",
			filter: EventFilter{
				ResourceType: "node",
				ResourceID:   "resource-42",
			},
		},
		{
			name: "filter_by_time_range",
			filter: EventFilter{
				StartTime: time.Now().Add(-1 * time.Hour),
				EndTime:   time.Now(),
			},
		},
		{
			name: "complex_filter",
			filter: EventFilter{
				Types:        []EventType{EventNodeEnrolled, EventWorkloadCreated},
				MinSeverity:  SeverityInfo,
				ResourceType: "node",
				StartTime:    time.Now().Add(-1 * time.Hour),
				EndTime:      time.Now(),
			},
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_ = es.GetEvents(tt.filter)
			}
		})
	}
}

// BenchmarkConcurrentEventRecording benchmarks concurrent event recording
func BenchmarkConcurrentEventRecording(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	es := NewEventStream(10000, logger)
	ctx := context.Background()

	event := Event{
		Type:         EventNodeEnrolled,
		Severity:     SeverityInfo,
		ResourceType: "node",
		ResourceID:   "node-123",
		Action:       "enroll",
		Description:  "Concurrent test",
	}

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			es.RecordEvent(ctx, event)
		}
	})
}

// BenchmarkLoggerWithContext benchmarks structured logging with context
func BenchmarkLoggerWithContext(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	ctx := context.Background()
	ctx = WithRequestID(ctx, GenerateRequestID())
	ctx = WithCorrelationID(ctx, GenerateCorrelationID())
	ctx = WithUserID(ctx, "user-123")

	tests := []struct {
		name       string
		fieldCount int
	}{
		{"no_extra_fields", 0},
		{"3_extra_fields", 3},
		{"10_extra_fields", 10},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			ctxLogger := ContextLogger(ctx, logger)

			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				fields := make([]zap.Field, tt.fieldCount)
				for j := 0; j < tt.fieldCount; j++ {
					fields[j] = zap.String(fmt.Sprintf("field%d", j), fmt.Sprintf("value%d", j))
				}

				ctxLogger.Info("Benchmark log message", fields...)
			}

			opsPerSec := float64(b.N) / b.Elapsed().Seconds()
			b.ReportMetric(opsPerSec, "logs/sec")
		})
	}
}

// Helper functions

func createNestedSpans(ctx context.Context, tracer trace.Tracer, depth int) {
	if depth == 0 {
		return
	}

	ctx, span := tracer.Start(ctx, fmt.Sprintf("span-depth-%d", depth))
	defer span.End()

	// Add some attributes
	span.SetAttributes(
		trace.Int64Attribute("depth", int64(depth)),
		trace.StringAttribute("operation", "benchmark"),
	)

	// Create nested span
	if depth > 1 {
		createNestedSpans(ctx, tracer, depth-1)
	}
}
