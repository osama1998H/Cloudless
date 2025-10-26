package observability

import (
	"context"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var (
	// Metrics for gRPC calls
	grpcRequestsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_grpc_requests_total",
			Help: "Total number of gRPC requests",
		},
		[]string{"method", "code"},
	)

	grpcRequestDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_grpc_request_duration_seconds",
			Help:    "Duration of gRPC requests in seconds",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"method"},
	)

	grpcStreamMessagesReceived = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_grpc_stream_messages_received_total",
			Help: "Total number of messages received on gRPC streams",
		},
		[]string{"method"},
	)

	grpcStreamMessagesSent = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_grpc_stream_messages_sent_total",
			Help: "Total number of messages sent on gRPC streams",
		},
		[]string{"method"},
	)
)

// UnaryServerInterceptor returns a gRPC unary server interceptor for logging
func UnaryServerInterceptor(logger *zap.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()

		// Log request
		logger.Debug("gRPC request started",
			zap.String("method", info.FullMethod),
			zap.Any("request", req),
		)

		// Call handler
		resp, err := handler(ctx, req)

		// Get status code
		code := codes.OK
		if err != nil {
			if st, ok := status.FromError(err); ok {
				code = st.Code()
			} else {
				code = codes.Unknown
			}
		}

		// Log response
		duration := time.Since(start)
		fields := []zap.Field{
			zap.String("method", info.FullMethod),
			zap.Duration("duration", duration),
			zap.String("code", code.String()),
		}

		if err != nil {
			fields = append(fields, zap.Error(err))
			logger.Error("gRPC request failed", fields...)
		} else {
			logger.Debug("gRPC request completed", fields...)
		}

		return resp, err
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor for logging
func StreamServerInterceptor(logger *zap.Logger) grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		start := time.Now()

		// Log stream start
		logger.Debug("gRPC stream started",
			zap.String("method", info.FullMethod),
			zap.Bool("client_stream", info.IsClientStream),
			zap.Bool("server_stream", info.IsServerStream),
		)

		// Wrap the stream to count messages
		wrapped := &wrappedServerStream{
			ServerStream: ss,
			method:       info.FullMethod,
			logger:       logger,
		}

		// Call handler
		err := handler(srv, wrapped)

		// Log stream end
		duration := time.Since(start)
		fields := []zap.Field{
			zap.String("method", info.FullMethod),
			zap.Duration("duration", duration),
			zap.Int("messages_received", wrapped.recvCount),
			zap.Int("messages_sent", wrapped.sendCount),
		}

		if err != nil {
			fields = append(fields, zap.Error(err))
			logger.Error("gRPC stream failed", fields...)
		} else {
			logger.Debug("gRPC stream completed", fields...)
		}

		return err
	}
}

// UnaryMetricsInterceptor returns a gRPC unary server interceptor for metrics
func UnaryMetricsInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		start := time.Now()

		// Call handler
		resp, err := handler(ctx, req)

		// Record metrics
		duration := time.Since(start).Seconds()
		grpcRequestDuration.WithLabelValues(info.FullMethod).Observe(duration)

		code := codes.OK
		if err != nil {
			if st, ok := status.FromError(err); ok {
				code = st.Code()
			} else {
				code = codes.Unknown
			}
		}
		grpcRequestsTotal.WithLabelValues(info.FullMethod, code.String()).Inc()

		return resp, err
	}
}

// StreamMetricsInterceptor returns a gRPC stream server interceptor for metrics
func StreamMetricsInterceptor() grpc.StreamServerInterceptor {
	return func(srv interface{}, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		// Wrap the stream to count messages
		wrapped := &metricsServerStream{
			ServerStream: ss,
			method:       info.FullMethod,
		}

		// Call handler
		err := handler(srv, wrapped)

		// Record request count
		code := codes.OK
		if err != nil {
			if st, ok := status.FromError(err); ok {
				code = st.Code()
			} else {
				code = codes.Unknown
			}
		}
		grpcRequestsTotal.WithLabelValues(info.FullMethod, code.String()).Inc()

		return err
	}
}

// wrappedServerStream wraps a grpc.ServerStream to add logging
type wrappedServerStream struct {
	grpc.ServerStream
	method    string
	logger    *zap.Logger
	recvCount int
	sendCount int
}

func (w *wrappedServerStream) RecvMsg(m interface{}) error {
	err := w.ServerStream.RecvMsg(m)
	if err == nil {
		w.recvCount++
		w.logger.Debug("gRPC stream message received",
			zap.String("method", w.method),
			zap.Int("count", w.recvCount),
		)
	}
	return err
}

func (w *wrappedServerStream) SendMsg(m interface{}) error {
	err := w.ServerStream.SendMsg(m)
	if err == nil {
		w.sendCount++
		w.logger.Debug("gRPC stream message sent",
			zap.String("method", w.method),
			zap.Int("count", w.sendCount),
		)
	}
	return err
}

// metricsServerStream wraps a grpc.ServerStream to add metrics
type metricsServerStream struct {
	grpc.ServerStream
	method string
}

func (m *metricsServerStream) RecvMsg(msg interface{}) error {
	err := m.ServerStream.RecvMsg(msg)
	if err == nil {
		grpcStreamMessagesReceived.WithLabelValues(m.method).Inc()
	}
	return err
}

func (m *metricsServerStream) SendMsg(msg interface{}) error {
	err := m.ServerStream.SendMsg(msg)
	if err == nil {
		grpcStreamMessagesSent.WithLabelValues(m.method).Inc()
	}
	return err
}
