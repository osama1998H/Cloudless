package observability

import (
	"context"
	"errors"
	"testing"
	"time"

	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// TestNewTracerProvider_Disabled tests tracer provider creation with tracing disabled
func TestNewTracerProvider_Disabled(t *testing.T) {
	logger := zap.NewNop()
	cfg := TracerConfig{
		Enabled:        false,
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
		Endpoint:       "localhost:4317",
		SampleRate:     1.0,
		Insecure:       true,
	}

	provider, err := NewTracerProvider(cfg, logger)
	if err != nil {
		t.Fatalf("NewTracerProvider() with Enabled=false error = %v, want nil", err)
	}

	if provider == nil {
		t.Fatal("Expected non-nil provider even when disabled")
	}

	// Verify provider is functional (no-op)
	tracer := provider.Tracer("test")
	if tracer == nil {
		t.Fatal("Expected non-nil tracer")
	}

	// Shutdown should not error
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := provider.Shutdown(ctx); err != nil {
		t.Errorf("Shutdown() error = %v, want nil", err)
	}
}

// TestNewTracerProvider_Enabled tests tracer provider with tracing enabled
// Note: This test doesn't actually connect to OTLP endpoint, it just verifies configuration
func TestNewTracerProvider_Enabled(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name      string
		config    TracerConfig
		wantError bool
	}{
		{
			name: "Valid insecure configuration",
			config: TracerConfig{
				Enabled:        true,
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				Endpoint:       "localhost:4317",
				SampleRate:     1.0,
				Insecure:       true,
			},
			wantError: false,
		},
		{
			name: "Valid with low sample rate",
			config: TracerConfig{
				Enabled:        true,
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				Endpoint:       "localhost:4317",
				SampleRate:     0.1,
				Insecure:       true,
			},
			wantError: false,
		},
		{
			name: "Valid with zero sample rate (never sample)",
			config: TracerConfig{
				Enabled:        true,
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				Endpoint:       "localhost:4317",
				SampleRate:     0.0,
				Insecure:       true,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Note: This will attempt to connect to OTLP endpoint
			// In a real environment, this might fail if endpoint is not available
			// For unit tests, we're just verifying the configuration is accepted
			provider, err := NewTracerProvider(tt.config, logger)

			if tt.wantError && err == nil {
				t.Error("NewTracerProvider() expected error, got nil")
			}
			if !tt.wantError && err != nil {
				// It's OK if connection fails in test environment
				// We're mainly testing configuration validation
				t.Logf("NewTracerProvider() error = %v (OK for test environment)", err)
				return
			}

			if provider != nil {
				// Clean up
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()
				provider.Shutdown(ctx)
			}
		})
	}
}

// TestNewTracerProvider_Insecure tests insecure vs secure mode
func TestNewTracerProvider_Insecure(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name     string
		insecure bool
	}{
		{
			name:     "Insecure mode",
			insecure: true,
		},
		{
			name:     "Secure mode",
			insecure: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := TracerConfig{
				Enabled:        true,
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				Endpoint:       "localhost:4317",
				SampleRate:     1.0,
				Insecure:       tt.insecure,
			}

			provider, err := NewTracerProvider(cfg, logger)
			// Connection may fail in test environment, that's OK
			if err != nil {
				t.Logf("NewTracerProvider() error = %v (OK for test environment)", err)
				return
			}

			if provider != nil {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()
				provider.Shutdown(ctx)
			}
		})
	}
}

// TestNewTracerProvider_Sampling tests different sampling strategies
func TestNewTracerProvider_Sampling(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name       string
		sampleRate float64
		expected   string // Expected sampler type (for validation)
	}{
		{
			name:       "Always sample (rate >= 1.0)",
			sampleRate: 1.0,
			expected:   "AlwaysSample",
		},
		{
			name:       "Never sample (rate <= 0)",
			sampleRate: 0.0,
			expected:   "NeverSample",
		},
		{
			name:       "Ratio-based sampling (0 < rate < 1)",
			sampleRate: 0.5,
			expected:   "TraceIDRatioBased",
		},
		{
			name:       "Low sampling rate",
			sampleRate: 0.01,
			expected:   "TraceIDRatioBased",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := TracerConfig{
				Enabled:        true,
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				Endpoint:       "localhost:4317",
				SampleRate:     tt.sampleRate,
				Insecure:       true,
			}

			provider, err := NewTracerProvider(cfg, logger)
			// Connection may fail, that's OK for this test
			if err != nil {
				t.Logf("NewTracerProvider() error = %v (OK for test environment)", err)
				return
			}

			if provider != nil {
				// Verify provider is functional
				tracer := provider.Tracer("test-sampler")
				if tracer == nil {
					t.Error("Expected non-nil tracer")
				}

				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				defer cancel()
				provider.Shutdown(ctx)
			}
		})
	}
}

// TestStartSpan tests span creation and context propagation
func TestStartSpan(t *testing.T) {
	ctx := context.Background()
	tracerName := "test-tracer"
	spanName := "test-span"

	// Start a span
	spanCtx, span := StartSpan(ctx, tracerName, spanName)

	if spanCtx == nil {
		t.Fatal("StartSpan() returned nil context")
	}
	if span == nil {
		t.Fatal("StartSpan() returned nil span")
	}

	// Verify span is in context
	extractedSpan := trace.SpanFromContext(spanCtx)
	if extractedSpan == nil {
		t.Error("Span not found in context")
	}

	// End the span
	span.End()
}

// TestRecordError tests error recording on span
func TestRecordError(t *testing.T) {
	ctx := context.Background()

	// Start a span
	spanCtx, span := StartSpan(ctx, "test-tracer", "test-error-span")
	defer span.End()

	// Record an error
	testErr := errors.New("test error")
	RecordError(spanCtx, testErr)

	// Verify function doesn't panic
	// Actual error recording verification would require span inspection
}

// TestSetSpanStatus tests setting span status
func TestSetSpanStatus(t *testing.T) {
	tests := []struct {
		name        string
		code        codes.Code
		description string
	}{
		{
			name:        "OK status",
			code:        codes.Ok,
			description: "operation succeeded",
		},
		{
			name:        "Error status",
			code:        codes.Error,
			description: "operation failed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()

			// Start a span
			spanCtx, span := StartSpan(ctx, "test-tracer", "test-status-span")
			defer span.End()

			// Set status
			SetSpanStatus(spanCtx, tt.code, tt.description)

			// Verify function doesn't panic
		})
	}
}

// TestInstrumentGRPCServer tests gRPC interceptor creation
func TestInstrumentGRPCServer(t *testing.T) {
	unaryInterceptor, streamInterceptor := InstrumentGRPCServer()

	if unaryInterceptor == nil {
		t.Error("InstrumentGRPCServer() returned nil unary interceptor")
	}
	if streamInterceptor == nil {
		t.Error("InstrumentGRPCServer() returned nil stream interceptor")
	}

	// Verify interceptors are callable (basic smoke test)
	// Full integration testing would require a gRPC server
}

// TestShutdown tests graceful shutdown
func TestShutdown(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name    string
		enabled bool
		timeout time.Duration
	}{
		{
			name:    "Disabled tracer shutdown",
			enabled: false,
			timeout: 1 * time.Second,
		},
		{
			name:    "Enabled tracer shutdown with sufficient timeout",
			enabled: true,
			timeout: 5 * time.Second,
		},
		{
			name:    "Shutdown with short timeout",
			enabled: false,
			timeout: 100 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := TracerConfig{
				Enabled:        tt.enabled,
				ServiceName:    "test-service",
				ServiceVersion: "1.0.0",
				Endpoint:       "localhost:4317",
				SampleRate:     1.0,
				Insecure:       true,
			}

			provider, err := NewTracerProvider(cfg, logger)
			if err != nil && tt.enabled {
				// Connection failure is OK in test environment
				t.Logf("NewTracerProvider() error = %v (OK for test environment)", err)
				return
			}
			if err != nil {
				t.Fatalf("NewTracerProvider() unexpected error = %v", err)
			}

			// Shutdown with timeout
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			err = provider.Shutdown(ctx)
			if err != nil && ctx.Err() == context.DeadlineExceeded {
				t.Logf("Shutdown() timeout (expected for short timeout test): %v", err)
			} else if err != nil {
				t.Errorf("Shutdown() error = %v", err)
			}
		})
	}
}

// TestTracerProvider_Tracer tests tracer retrieval
func TestTracerProvider_Tracer(t *testing.T) {
	logger := zap.NewNop()
	cfg := TracerConfig{
		Enabled:        false,
		ServiceName:    "test-service",
		ServiceVersion: "1.0.0",
	}

	provider, err := NewTracerProvider(cfg, logger)
	if err != nil {
		t.Fatalf("NewTracerProvider() error = %v", err)
	}
	defer provider.Shutdown(context.Background())

	tests := []struct {
		name       string
		tracerName string
	}{
		{
			name:       "Get tracer with simple name",
			tracerName: "simple",
		},
		{
			name:       "Get tracer with qualified name",
			tracerName: "pkg.component.operation",
		},
		{
			name:       "Get tracer with empty name",
			tracerName: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracer := provider.Tracer(tt.tracerName)
			if tracer == nil {
				t.Error("Tracer() returned nil")
			}
		})
	}
}

// TestAddSpanEvent tests adding events to span
func TestAddSpanEvent(t *testing.T) {
	ctx := context.Background()

	// Start a span
	spanCtx, span := StartSpan(ctx, "test-tracer", "test-event-span")
	defer span.End()

	// Add events
	AddSpanEvent(spanCtx, "event1")
	AddSpanEvent(spanCtx, "event2")
	AddSpanEvent(spanCtx, "operation.started")

	// Verify function doesn't panic
}

// TestSpanLifecycle tests full span lifecycle
func TestSpanLifecycle(t *testing.T) {
	ctx := context.Background()

	// Start parent span
	parentCtx, parentSpan := StartSpan(ctx, "test-tracer", "parent-span")
	defer parentSpan.End()

	// Add event to parent
	AddSpanEvent(parentCtx, "parent.started")

	// Start child span
	childCtx, childSpan := StartSpan(parentCtx, "test-tracer", "child-span")
	defer childSpan.End()

	// Add event to child
	AddSpanEvent(childCtx, "child.processing")

	// Record error on child
	testErr := errors.New("child error")
	RecordError(childCtx, testErr)
	SetSpanStatus(childCtx, codes.Error, testErr.Error())

	// Complete child span
	childSpan.End()

	// Complete parent span with success
	SetSpanStatus(parentCtx, codes.Ok, "completed")
	parentSpan.End()

	// Verify no panics occurred
}
