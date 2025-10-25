package observability

import (
	"context"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// Context keys for correlation
type contextKey string

const (
	// RequestIDKey is the context key for request ID
	RequestIDKey contextKey = "request-id"

	// CorrelationIDKey is the context key for correlation ID (spans multiple requests)
	CorrelationIDKey contextKey = "correlation-id"

	// UserIDKey is the context key for user ID
	UserIDKey contextKey = "user-id"

	// WorkloadIDKey is the context key for workload ID
	WorkloadIDKey contextKey = "workload-id"

	// NodeIDKey is the context key for node ID
	NodeIDKey contextKey = "node-id"
)

// Metadata keys for gRPC propagation
const (
	RequestIDMetadataKey     = "x-request-id"
	CorrelationIDMetadataKey = "x-correlation-id"
	UserIDMetadataKey        = "x-user-id"
)

// WithRequestID adds a request ID to the context
func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, RequestIDKey, requestID)
}

// GetRequestID retrieves the request ID from the context
func GetRequestID(ctx context.Context) string {
	if id, ok := ctx.Value(RequestIDKey).(string); ok {
		return id
	}
	return ""
}

// WithCorrelationID adds a correlation ID to the context
func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, CorrelationIDKey, correlationID)
}

// GetCorrelationID retrieves the correlation ID from the context
func GetCorrelationID(ctx context.Context) string {
	if id, ok := ctx.Value(CorrelationIDKey).(string); ok {
		return id
	}
	return ""
}

// WithUserID adds a user ID to the context
func WithUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, UserIDKey, userID)
}

// GetUserID retrieves the user ID from the context
func GetUserID(ctx context.Context) string {
	if id, ok := ctx.Value(UserIDKey).(string); ok {
		return id
	}
	return ""
}

// WithWorkloadID adds a workload ID to the context
func WithWorkloadID(ctx context.Context, workloadID string) context.Context {
	return context.WithValue(ctx, WorkloadIDKey, workloadID)
}

// GetWorkloadID retrieves the workload ID from the context
func GetWorkloadID(ctx context.Context) string {
	if id, ok := ctx.Value(WorkloadIDKey).(string); ok {
		return id
	}
	return ""
}

// WithNodeID adds a node ID to the context
func WithNodeID(ctx context.Context, nodeID string) context.Context {
	return context.WithValue(ctx, NodeIDKey, nodeID)
}

// GetNodeID retrieves the node ID from the context
func GetNodeID(ctx context.Context) string {
	if id, ok := ctx.Value(NodeIDKey).(string); ok {
		return id
	}
	return ""
}

// GenerateRequestID generates a new request ID
func GenerateRequestID() string {
	return uuid.New().String()
}

// ContextLogger returns a logger with correlation IDs from context
func ContextLogger(ctx context.Context, logger *zap.Logger) *zap.Logger {
	fields := []zap.Field{}

	if requestID := GetRequestID(ctx); requestID != "" {
		fields = append(fields, zap.String("request_id", requestID))
	}

	if correlationID := GetCorrelationID(ctx); correlationID != "" {
		fields = append(fields, zap.String("correlation_id", correlationID))
	}

	if userID := GetUserID(ctx); userID != "" {
		fields = append(fields, zap.String("user_id", userID))
	}

	if workloadID := GetWorkloadID(ctx); workloadID != "" {
		fields = append(fields, zap.String("workload_id", workloadID))
	}

	if nodeID := GetNodeID(ctx); nodeID != "" {
		fields = append(fields, zap.String("node_id", nodeID))
	}

	// Add trace ID if available
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().HasTraceID() {
		fields = append(fields, zap.String("trace_id", span.SpanContext().TraceID().String()))
		fields = append(fields, zap.String("span_id", span.SpanContext().SpanID().String()))
	}

	return logger.With(fields...)
}

// UnaryServerInterceptorWithCorrelation creates a gRPC interceptor that propagates correlation IDs
func UnaryServerInterceptorWithCorrelation(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Extract metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			// Extract or generate request ID
			if ids := md.Get(RequestIDMetadataKey); len(ids) > 0 {
				ctx = WithRequestID(ctx, ids[0])
			} else {
				ctx = WithRequestID(ctx, GenerateRequestID())
			}

			// Extract or generate correlation ID
			if ids := md.Get(CorrelationIDMetadataKey); len(ids) > 0 {
				ctx = WithCorrelationID(ctx, ids[0])
			} else {
				// If no correlation ID, use request ID
				ctx = WithCorrelationID(ctx, GetRequestID(ctx))
			}

			// Extract user ID if present
			if ids := md.Get(UserIDMetadataKey); len(ids) > 0 {
				ctx = WithUserID(ctx, ids[0])
			}
		} else {
			// No metadata, generate new IDs
			requestID := GenerateRequestID()
			ctx = WithRequestID(ctx, requestID)
			ctx = WithCorrelationID(ctx, requestID)
		}

		// Log request with correlation IDs
		ctxLogger := ContextLogger(ctx, logger)
		ctxLogger.Debug("Handling gRPC request",
			zap.String("method", info.FullMethod),
		)

		// Call handler with enriched context
		resp, err := handler(ctx, req)

		// Log response
		if err != nil {
			ctxLogger.Error("gRPC request failed",
				zap.String("method", info.FullMethod),
				zap.Error(err),
			)
		} else {
			ctxLogger.Debug("gRPC request completed",
				zap.String("method", info.FullMethod),
			)
		}

		return resp, err
	}
}

// StreamServerInterceptorWithCorrelation creates a gRPC stream interceptor that propagates correlation IDs
func StreamServerInterceptorWithCorrelation(logger *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()

		// Extract metadata
		md, ok := metadata.FromIncomingContext(ctx)
		if ok {
			// Extract or generate request ID
			if ids := md.Get(RequestIDMetadataKey); len(ids) > 0 {
				ctx = WithRequestID(ctx, ids[0])
			} else {
				ctx = WithRequestID(ctx, GenerateRequestID())
			}

			// Extract or generate correlation ID
			if ids := md.Get(CorrelationIDMetadataKey); len(ids) > 0 {
				ctx = WithCorrelationID(ctx, ids[0])
			} else {
				ctx = WithCorrelationID(ctx, GetRequestID(ctx))
			}

			// Extract user ID if present
			if ids := md.Get(UserIDMetadataKey); len(ids) > 0 {
				ctx = WithUserID(ctx, ids[0])
			}
		} else {
			// No metadata, generate new IDs
			requestID := GenerateRequestID()
			ctx = WithRequestID(ctx, requestID)
			ctx = WithCorrelationID(ctx, requestID)
		}

		// Log stream start
		ctxLogger := ContextLogger(ctx, logger)
		ctxLogger.Debug("Handling gRPC stream",
			zap.String("method", info.FullMethod),
		)

		// Wrap stream with enriched context
		wrapped := &correlatedServerStream{
			ServerStream: ss,
			ctx:          ctx,
		}

		// Call handler
		err := handler(srv, wrapped)

		// Log stream end
		if err != nil {
			ctxLogger.Error("gRPC stream failed",
				zap.String("method", info.FullMethod),
				zap.Error(err),
			)
		} else {
			ctxLogger.Debug("gRPC stream completed",
				zap.String("method", info.FullMethod),
			)
		}

		return err
	}
}

// correlatedServerStream wraps grpc.ServerStream with enriched context
type correlatedServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (s *correlatedServerStream) Context() context.Context {
	return s.ctx
}

// UnaryClientInterceptorWithCorrelation creates a gRPC client interceptor that propagates correlation IDs
func UnaryClientInterceptorWithCorrelation() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		// Create metadata from context
		md := metadata.New(nil)

		if requestID := GetRequestID(ctx); requestID != "" {
			md.Set(RequestIDMetadataKey, requestID)
		}

		if correlationID := GetCorrelationID(ctx); correlationID != "" {
			md.Set(CorrelationIDMetadataKey, correlationID)
		}

		if userID := GetUserID(ctx); userID != "" {
			md.Set(UserIDMetadataKey, userID)
		}

		// Attach metadata to outgoing context
		ctx = metadata.NewOutgoingContext(ctx, md)

		// Call the remote method
		return invoker(ctx, method, req, reply, cc, opts...)
	}
}

// StreamClientInterceptorWithCorrelation creates a gRPC stream client interceptor that propagates correlation IDs
func StreamClientInterceptorWithCorrelation() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		// Create metadata from context
		md := metadata.New(nil)

		if requestID := GetRequestID(ctx); requestID != "" {
			md.Set(RequestIDMetadataKey, requestID)
		}

		if correlationID := GetCorrelationID(ctx); correlationID != "" {
			md.Set(CorrelationIDMetadataKey, correlationID)
		}

		if userID := GetUserID(ctx); userID != "" {
			md.Set(UserIDMetadataKey, userID)
		}

		// Attach metadata to outgoing context
		ctx = metadata.NewOutgoingContext(ctx, md)

		// Create the stream
		return streamer(ctx, desc, cc, method, opts...)
	}
}
