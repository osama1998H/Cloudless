package observability

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TracerConfig holds configuration for distributed tracing
type TracerConfig struct {
	Enabled      bool
	Endpoint     string
	ServiceName  string
	ServiceVersion string
	SampleRate   float64
	Insecure     bool
}

// TracerProvider wraps the OpenTelemetry tracer provider
type TracerProvider struct {
	provider *sdktrace.TracerProvider
	logger   *zap.Logger
}

// NewTracerProvider creates a new tracer provider
func NewTracerProvider(cfg TracerConfig, logger *zap.Logger) (*TracerProvider, error) {
	if !cfg.Enabled {
		logger.Info("Tracing is disabled")
		return &TracerProvider{
			provider: sdktrace.NewTracerProvider(),
			logger:   logger,
		}, nil
	}

	logger.Info("Initializing distributed tracing",
		zap.String("endpoint", cfg.Endpoint),
		zap.String("service", cfg.ServiceName),
		zap.Float64("sample_rate", cfg.SampleRate),
	)

	// Create OTLP exporter
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	opts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.Endpoint),
	}

	if cfg.Insecure {
		opts = append(opts, otlptracegrpc.WithTLSCredentials(insecure.NewCredentials()))
		opts = append(opts, otlptracegrpc.WithDialOption(grpc.WithTransportCredentials(insecure.NewCredentials())))
	}

	exporter, err := otlptracegrpc.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create resource: %w", err)
	}

	// Create sampler
	var sampler sdktrace.Sampler
	if cfg.SampleRate >= 1.0 {
		sampler = sdktrace.AlwaysSample()
	} else if cfg.SampleRate <= 0 {
		sampler = sdktrace.NeverSample()
	} else {
		sampler = sdktrace.TraceIDRatioBased(cfg.SampleRate)
	}

	// Create tracer provider
	provider := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)

	// Set global tracer provider
	otel.SetTracerProvider(provider)

	// Set global propagator for context propagation
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Info("Distributed tracing initialized successfully")

	return &TracerProvider{
		provider: provider,
		logger:   logger,
	}, nil
}

// Shutdown gracefully shuts down the tracer provider
func (tp *TracerProvider) Shutdown(ctx context.Context) error {
	tp.logger.Info("Shutting down tracer provider")

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := tp.provider.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown tracer provider: %w", err)
	}

	return nil
}

// Tracer returns a tracer for the given name
func (tp *TracerProvider) Tracer(name string) trace.Tracer {
	return otel.Tracer(name)
}

// StartSpan starts a new span with the given name
func StartSpan(ctx context.Context, tracerName, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	tracer := otel.Tracer(tracerName)
	return tracer.Start(ctx, spanName, opts...)
}

// AddSpanEvent adds an event to the current span
func AddSpanEvent(ctx context.Context, name string, attrs ...trace.EventOption) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, attrs...)
}

// AddSpanAttributes adds attributes to the current span
func AddSpanAttributes(ctx context.Context, attrs ...trace.SpanStartEventOption) {
	// Note: This is a placeholder implementation
	// In production, you would use attribute.KeyValue for setting attributes
	_ = trace.SpanFromContext(ctx)
}

// RecordError records an error on the current span
func RecordError(ctx context.Context, err error) {
	span := trace.SpanFromContext(ctx)
	span.RecordError(err)
}

// SetSpanStatus sets the status of the current span
func SetSpanStatus(ctx context.Context, code codes.Code, description string) {
	span := trace.SpanFromContext(ctx)
	span.SetStatus(code, description)
}

// InstrumentGRPCServer returns gRPC server interceptors for tracing
func InstrumentGRPCServer() (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	// Simplified interceptor - in production would use otelgrpc
	unaryInterceptor := func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx, span := StartSpan(ctx, "grpc.server", info.FullMethod)
		defer span.End()

		resp, err := handler(ctx, req)

		if err != nil {
			RecordError(ctx, err)
			SetSpanStatus(ctx, codes.Error, err.Error())
		} else {
			SetSpanStatus(ctx, codes.Ok, "")
		}

		return resp, err
	}

	streamInterceptor := func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		ctx, span := StartSpan(ctx, "grpc.server", info.FullMethod)
		defer span.End()

		// Wrap the stream to propagate context
		wrapped := &tracedServerStream{
			ServerStream: ss,
			ctx:          ctx,
		}

		err := handler(srv, wrapped)

		if err != nil {
			RecordError(ctx, err)
			SetSpanStatus(ctx, codes.Error, err.Error())
		} else {
			SetSpanStatus(ctx, codes.Ok, "")
		}

		return err
	}

	return unaryInterceptor, streamInterceptor
}

// tracedServerStream wraps grpc.ServerStream to inject traced context
type tracedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *tracedServerStream) Context() context.Context {
	return s.ctx
}
