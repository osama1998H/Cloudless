package observability

import (
	"context"
	"errors"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// mockUnaryHandler is a test handler for unary RPC
type mockUnaryHandler struct {
	shouldError bool
	errorCode   codes.Code
	response    interface{}
}

func (m *mockUnaryHandler) handle(ctx context.Context, req interface{}) (interface{}, error) {
	if m.shouldError {
		return nil, status.Error(m.errorCode, "mock error")
	}
	return m.response, nil
}

// mockStreamHandler is a test handler for streaming RPC
type mockStreamHandler struct {
	shouldError bool
}

func (m *mockStreamHandler) handle(srv interface{}, stream grpc.ServerStream) error {
	if m.shouldError {
		return errors.New("mock stream error")
	}
	return nil
}

// mockServerStream implements grpc.ServerStream for testing
type mockServerStream struct {
	grpc.ServerStream
	ctx         context.Context
	recvCount   int
	sendCount   int
	recvError   error
	sendError   error
	recvMessage interface{}
	sentMessage interface{}
}

func (m *mockServerStream) Context() context.Context {
	if m.ctx == nil {
		return context.Background()
	}
	return m.ctx
}

func (m *mockServerStream) RecvMsg(msg interface{}) error {
	m.recvCount++
	if m.recvError != nil {
		return m.recvError
	}
	if m.recvMessage != nil {
		// Copy mock message to msg (simplified for testing)
		msg = m.recvMessage
	}
	return nil
}

func (m *mockServerStream) SendMsg(msg interface{}) error {
	m.sendCount++
	m.sentMessage = msg
	if m.sendError != nil {
		return m.sendError
	}
	return nil
}

// TestUnaryServerInterceptor_Success tests successful unary RPC logging
func TestUnaryServerInterceptor_Success(t *testing.T) {
	logger := zap.NewNop()
	interceptor := UnaryServerInterceptor(logger)

	mockHandler := &mockUnaryHandler{
		shouldError: false,
		response:    "success response",
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/TestMethod",
	}

	ctx := context.Background()
	req := "test request"

	resp, err := interceptor(ctx, req, info, mockHandler.handle)

	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}
	if resp == nil {
		t.Error("Expected non-nil response")
	}
	if resp != mockHandler.response {
		t.Errorf("Expected response %v, got %v", mockHandler.response, resp)
	}
}

// TestUnaryServerInterceptor_Error tests error handling in unary RPC
func TestUnaryServerInterceptor_Error(t *testing.T) {
	logger := zap.NewNop()
	interceptor := UnaryServerInterceptor(logger)

	tests := []struct {
		name      string
		errorCode codes.Code
	}{
		{
			name:      "Internal error",
			errorCode: codes.Internal,
		},
		{
			name:      "Not found error",
			errorCode: codes.NotFound,
		},
		{
			name:      "Permission denied",
			errorCode: codes.PermissionDenied,
		},
		{
			name:      "Unavailable",
			errorCode: codes.Unavailable,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHandler := &mockUnaryHandler{
				shouldError: true,
				errorCode:   tt.errorCode,
			}

			info := &grpc.UnaryServerInfo{
				FullMethod: "/test.Service/TestMethod",
			}

			ctx := context.Background()
			req := "test request"

			resp, err := interceptor(ctx, req, info, mockHandler.handle)

			if err == nil {
				t.Error("Expected error, got nil")
			}
			if resp != nil {
				t.Errorf("Expected nil response on error, got %v", resp)
			}

			// Verify error code
			st, ok := status.FromError(err)
			if !ok {
				t.Fatal("Error is not a gRPC status error")
			}
			if st.Code() != tt.errorCode {
				t.Errorf("Expected error code %v, got %v", tt.errorCode, st.Code())
			}
		})
	}
}

// TestUnaryMetricsInterceptor tests metrics recording for unary RPC
func TestUnaryMetricsInterceptor(t *testing.T) {
	interceptor := UnaryMetricsInterceptor()

	tests := []struct {
		name        string
		shouldError bool
		errorCode   codes.Code
	}{
		{
			name:        "Successful request",
			shouldError: false,
		},
		{
			name:        "Failed request",
			shouldError: true,
			errorCode:   codes.Internal,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHandler := &mockUnaryHandler{
				shouldError: tt.shouldError,
				errorCode:   tt.errorCode,
				response:    "test response",
			}

			info := &grpc.UnaryServerInfo{
				FullMethod: "/test.Service/MetricsTest",
			}

			ctx := context.Background()
			req := "test request"

			resp, err := interceptor(ctx, req, info, mockHandler.handle)

			if tt.shouldError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Expected nil error, got %v", err)
			}
			if !tt.shouldError && resp == nil {
				t.Error("Expected non-nil response on success")
			}

			// Metrics are recorded to Prometheus - we can't easily verify them in unit tests
			// but we can verify the interceptor doesn't panic
		})
	}
}

// TestStreamServerInterceptor tests streaming RPC logging
func TestStreamServerInterceptor(t *testing.T) {
	logger := zap.NewNop()
	interceptor := StreamServerInterceptor(logger)

	tests := []struct {
		name        string
		shouldError bool
	}{
		{
			name:        "Successful stream",
			shouldError: false,
		},
		{
			name:        "Failed stream",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHandler := &mockStreamHandler{
				shouldError: tt.shouldError,
			}

			info := &grpc.StreamServerInfo{
				FullMethod:     "/test.Service/StreamTest",
				IsClientStream: true,
				IsServerStream: true,
			}

			mockStream := &mockServerStream{
				ctx: context.Background(),
			}

			err := interceptor(nil, mockStream, info, mockHandler.handle)

			if tt.shouldError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Expected nil error, got %v", err)
			}
		})
	}
}

// TestStreamMetricsInterceptor tests metrics recording for streaming RPC
func TestStreamMetricsInterceptor(t *testing.T) {
	interceptor := StreamMetricsInterceptor()

	tests := []struct {
		name        string
		shouldError bool
	}{
		{
			name:        "Successful stream",
			shouldError: false,
		},
		{
			name:        "Failed stream",
			shouldError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockHandler := &mockStreamHandler{
				shouldError: tt.shouldError,
			}

			info := &grpc.StreamServerInfo{
				FullMethod:     "/test.Service/StreamMetrics",
				IsClientStream: true,
				IsServerStream: true,
			}

			mockStream := &mockServerStream{
				ctx: context.Background(),
			}

			err := interceptor(nil, mockStream, info, mockHandler.handle)

			if tt.shouldError && err == nil {
				t.Error("Expected error, got nil")
			}
			if !tt.shouldError && err != nil {
				t.Errorf("Expected nil error, got %v", err)
			}

			// Metrics are recorded - verify no panic
		})
	}
}

// TestWrappedServerStream tests message counting in wrapped stream
func TestWrappedServerStream(t *testing.T) {
	logger := zap.NewNop()

	baseStream := &mockServerStream{
		ctx: context.Background(),
	}

	wrapped := &wrappedServerStream{
		ServerStream: baseStream,
		method:       "/test.Service/Test",
		logger:       logger,
		recvCount:    0,
		sendCount:    0,
	}

	// Test RecvMsg
	err := wrapped.RecvMsg(nil)
	if err != nil {
		t.Errorf("RecvMsg() error = %v", err)
	}
	if wrapped.recvCount != 1 {
		t.Errorf("Expected recvCount = 1, got %d", wrapped.recvCount)
	}

	// Test SendMsg
	err = wrapped.SendMsg("test message")
	if err != nil {
		t.Errorf("SendMsg() error = %v", err)
	}
	if wrapped.sendCount != 1 {
		t.Errorf("Expected sendCount = 1, got %d", wrapped.sendCount)
	}

	// Test multiple messages
	for i := 0; i < 5; i++ {
		wrapped.RecvMsg(nil)
		wrapped.SendMsg("message")
	}

	if wrapped.recvCount != 6 {
		t.Errorf("Expected recvCount = 6, got %d", wrapped.recvCount)
	}
	if wrapped.sendCount != 6 {
		t.Errorf("Expected sendCount = 6, got %d", wrapped.sendCount)
	}

	// Test Context()
	ctx := wrapped.Context()
	if ctx == nil {
		t.Error("Context() returned nil")
	}
}

// TestMetricsServerStream tests metrics stream wrapper
func TestMetricsServerStream(t *testing.T) {
	baseStream := &mockServerStream{
		ctx: context.Background(),
	}

	wrapped := &metricsServerStream{
		ServerStream: baseStream,
		method:       "/test.Service/MetricsStream",
	}

	// Test RecvMsg - should increment metrics
	err := wrapped.RecvMsg(nil)
	if err != nil {
		t.Errorf("RecvMsg() error = %v", err)
	}

	// Test SendMsg - should increment metrics
	err = wrapped.SendMsg("test")
	if err != nil {
		t.Errorf("SendMsg() error = %v", err)
	}

	// Verify no panics with multiple messages
	for i := 0; i < 10; i++ {
		wrapped.RecvMsg(nil)
		wrapped.SendMsg("msg")
	}
}

// TestInterceptorChaining tests using both logging and metrics interceptors together
func TestInterceptorChaining(t *testing.T) {
	logger := zap.NewNop()

	mockHandler := &mockUnaryHandler{
		shouldError: false,
		response:    "chained response",
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/ChainTest",
	}

	ctx := context.Background()
	req := "test request"

	// Apply logging interceptor first
	loggingInterceptor := UnaryServerInterceptor(logger)
	metricsInterceptor := UnaryMetricsInterceptor()

	// Chain: metrics -> logging -> handler
	chainedHandler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return loggingInterceptor(ctx, req, info, mockHandler.handle)
	}

	resp, err := metricsInterceptor(ctx, req, info, chainedHandler)

	if err != nil {
		t.Errorf("Chained interceptors error = %v", err)
	}
	if resp != mockHandler.response {
		t.Errorf("Expected response %v, got %v", mockHandler.response, resp)
	}
}

// TestInterceptorWithContextValues tests interceptors preserve context values
func TestInterceptorWithContextValues(t *testing.T) {
	logger := zap.NewNop()
	interceptor := UnaryServerInterceptor(logger)

	type contextKey string
	const testKey contextKey = "test-key"
	const testValue = "test-value"

	mockHandler := &mockUnaryHandler{
		shouldError: false,
		response:    "response",
	}

	// Create custom handler that checks context value
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		value := ctx.Value(testKey)
		if value == nil {
			return nil, errors.New("context value not preserved")
		}
		if value.(string) != testValue {
			return nil, errors.New("context value mismatch")
		}
		return mockHandler.handle(ctx, req)
	}

	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/ContextTest",
	}

	// Add value to context
	ctx := context.WithValue(context.Background(), testKey, testValue)
	req := "test request"

	resp, err := interceptor(ctx, req, info, handler)

	if err != nil {
		t.Errorf("Expected nil error, got %v", err)
	}
	if resp == nil {
		t.Error("Expected non-nil response")
	}
}
