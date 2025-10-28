package observability

import (
	"context"
	"strings"
	"testing"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// TestWithRequestID_GetRequestID tests request ID context management
func TestWithRequestID_GetRequestID(t *testing.T) {
	ctx := context.Background()

	// Test with valid request ID
	requestID := "req-12345"
	ctx = WithRequestID(ctx, requestID)

	retrieved := GetRequestID(ctx)
	if retrieved != requestID {
		t.Errorf("Expected request ID %s, got %s", requestID, retrieved)
	}

	// Test with empty request ID
	ctx = WithRequestID(context.Background(), "")
	retrieved = GetRequestID(ctx)
	if retrieved != "" {
		t.Errorf("Expected empty request ID, got %s", retrieved)
	}

	// Test without request ID
	ctx = context.Background()
	retrieved = GetRequestID(ctx)
	if retrieved != "" {
		t.Errorf("Expected empty string for context without request ID, got %s", retrieved)
	}
}

// TestWithCorrelationID_GetCorrelationID tests correlation ID context management
func TestWithCorrelationID_GetCorrelationID(t *testing.T) {
	ctx := context.Background()

	// Test with valid correlation ID
	correlationID := "corr-67890"
	ctx = WithCorrelationID(ctx, correlationID)

	retrieved := GetCorrelationID(ctx)
	if retrieved != correlationID {
		t.Errorf("Expected correlation ID %s, got %s", correlationID, retrieved)
	}

	// Test without correlation ID
	ctx = context.Background()
	retrieved = GetCorrelationID(ctx)
	if retrieved != "" {
		t.Errorf("Expected empty string for context without correlation ID, got %s", retrieved)
	}
}

// TestWithUserID_GetUserID tests user ID context management
func TestWithUserID_GetUserID(t *testing.T) {
	ctx := context.Background()

	// Test with valid user ID
	userID := "user-abc123"
	ctx = WithUserID(ctx, userID)

	retrieved := GetUserID(ctx)
	if retrieved != userID {
		t.Errorf("Expected user ID %s, got %s", userID, retrieved)
	}

	// Test without user ID
	ctx = context.Background()
	retrieved = GetUserID(ctx)
	if retrieved != "" {
		t.Errorf("Expected empty string for context without user ID, got %s", retrieved)
	}
}

// TestWithWorkloadID_GetWorkloadID tests workload ID context management
func TestWithWorkloadID_GetWorkloadID(t *testing.T) {
	ctx := context.Background()

	// Test with valid workload ID
	workloadID := "wl-xyz789"
	ctx = WithWorkloadID(ctx, workloadID)

	retrieved := GetWorkloadID(ctx)
	if retrieved != workloadID {
		t.Errorf("Expected workload ID %s, got %s", workloadID, retrieved)
	}

	// Test without workload ID
	ctx = context.Background()
	retrieved = GetWorkloadID(ctx)
	if retrieved != "" {
		t.Errorf("Expected empty string for context without workload ID, got %s", retrieved)
	}
}

// TestWithNodeID_GetNodeID tests node ID context management
func TestWithNodeID_GetNodeID(t *testing.T) {
	ctx := context.Background()

	// Test with valid node ID
	nodeID := "node-001"
	ctx = WithNodeID(ctx, nodeID)

	retrieved := GetNodeID(ctx)
	if retrieved != nodeID {
		t.Errorf("Expected node ID %s, got %s", nodeID, retrieved)
	}

	// Test without node ID
	ctx = context.Background()
	retrieved = GetNodeID(ctx)
	if retrieved != "" {
		t.Errorf("Expected empty string for context without node ID, got %s", retrieved)
	}
}

// TestGenerateRequestID tests UUID generation for request IDs
func TestGenerateRequestID(t *testing.T) {
	// Generate multiple IDs
	id1 := GenerateRequestID()
	id2 := GenerateRequestID()
	id3 := GenerateRequestID()

	// Verify IDs are not empty
	if id1 == "" || id2 == "" || id3 == "" {
		t.Error("GenerateRequestID() returned empty string")
	}

	// Verify IDs are unique
	if id1 == id2 || id2 == id3 || id1 == id3 {
		t.Error("GenerateRequestID() generated duplicate IDs")
	}

	// Verify ID format (UUID v4 format: 8-4-4-4-12)
	parts := strings.Split(id1, "-")
	if len(parts) != 5 {
		t.Errorf("Expected UUID format with 5 parts, got %d parts", len(parts))
	}
}

// TestContextLogger tests logger with correlation IDs
func TestContextLogger(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name          string
		setupContext  func(context.Context) context.Context
		expectedCount int // Number of expected fields
	}{
		{
			name: "Context with request ID",
			setupContext: func(ctx context.Context) context.Context {
				return WithRequestID(ctx, "req-123")
			},
			expectedCount: 1,
		},
		{
			name: "Context with correlation ID",
			setupContext: func(ctx context.Context) context.Context {
				return WithCorrelationID(ctx, "corr-456")
			},
			expectedCount: 1,
		},
		{
			name: "Context with all IDs",
			setupContext: func(ctx context.Context) context.Context {
				ctx = WithRequestID(ctx, "req-123")
				ctx = WithCorrelationID(ctx, "corr-456")
				ctx = WithUserID(ctx, "user-789")
				ctx = WithWorkloadID(ctx, "wl-abc")
				ctx = WithNodeID(ctx, "node-xyz")
				return ctx
			},
			expectedCount: 5,
		},
		{
			name: "Empty context",
			setupContext: func(ctx context.Context) context.Context {
				return ctx
			},
			expectedCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setupContext(context.Background())
			ctxLogger := ContextLogger(ctx, logger)

			if ctxLogger == nil {
				t.Error("ContextLogger() returned nil")
			}

			// Verify logger is functional
			ctxLogger.Info("test message")
		})
	}
}

// TestUnaryServerInterceptorWithCorrelation tests correlation ID propagation in unary RPC
func TestUnaryServerInterceptorWithCorrelation(t *testing.T) {
	logger := zap.NewNop()
	interceptor := UnaryServerInterceptorWithCorrelation(logger)

	tests := []struct {
		name              string
		metadata          metadata.MD
		expectRequestID   bool
		expectCorrelationID bool
	}{
		{
			name: "Metadata with request and correlation IDs",
			metadata: metadata.New(map[string]string{
				RequestIDMetadataKey:     "req-123",
				CorrelationIDMetadataKey: "corr-456",
			}),
			expectRequestID:   true,
			expectCorrelationID: true,
		},
		{
			name: "Metadata with only request ID",
			metadata: metadata.New(map[string]string{
				RequestIDMetadataKey: "req-only",
			}),
			expectRequestID:   true,
			expectCorrelationID: true, // Should use request ID
		},
		{
			name:              "No metadata",
			metadata:          nil,
			expectRequestID:   true, // Should generate
			expectCorrelationID: true, // Should generate
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create context with metadata
			ctx := context.Background()
			if tt.metadata != nil {
				ctx = metadata.NewIncomingContext(ctx, tt.metadata)
			}

			// Mock handler that checks context
			handlerCalled := false
			handler := func(ctx context.Context, req interface{}) (interface{}, error) {
				handlerCalled = true

				// Verify request ID
				requestID := GetRequestID(ctx)
				if tt.expectRequestID && requestID == "" {
					t.Error("Expected request ID in context, got empty string")
				}

				// Verify correlation ID
				correlationID := GetCorrelationID(ctx)
				if tt.expectCorrelationID && correlationID == "" {
					t.Error("Expected correlation ID in context, got empty string")
				}

				return "response", nil
			}

			info := &grpc.UnaryServerInfo{
				FullMethod: "/test.Service/Test",
			}

			_, err := interceptor(ctx, "request", info, handler)
			if err != nil {
				t.Errorf("Interceptor error = %v", err)
			}

			if !handlerCalled {
				t.Error("Handler was not called")
			}
		})
	}
}

// TestStreamServerInterceptorWithCorrelation tests correlation ID propagation in streaming RPC
func TestStreamServerInterceptorWithCorrelation(t *testing.T) {
	logger := zap.NewNop()
	interceptor := StreamServerInterceptorWithCorrelation(logger)

	tests := []struct {
		name     string
		metadata metadata.MD
	}{
		{
			name: "With metadata",
			metadata: metadata.New(map[string]string{
				RequestIDMetadataKey:     "stream-req-123",
				CorrelationIDMetadataKey: "stream-corr-456",
			}),
		},
		{
			name:     "Without metadata",
			metadata: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create context with metadata
			ctx := context.Background()
			if tt.metadata != nil {
				ctx = metadata.NewIncomingContext(ctx, tt.metadata)
			}

			mockStream := &mockServerStream{
				ctx: ctx,
			}

			handlerCalled := false
			handler := func(srv interface{}, stream grpc.ServerStream) error {
				handlerCalled = true

				// Verify stream context has correlation IDs
				streamCtx := stream.Context()
				requestID := GetRequestID(streamCtx)
				if requestID == "" {
					t.Error("Expected request ID in stream context")
				}

				correlationID := GetCorrelationID(streamCtx)
				if correlationID == "" {
					t.Error("Expected correlation ID in stream context")
				}

				return nil
			}

			info := &grpc.StreamServerInfo{
				FullMethod:     "/test.Service/StreamTest",
				IsClientStream: true,
				IsServerStream: true,
			}

			err := interceptor(nil, mockStream, info, handler)
			if err != nil {
				t.Errorf("Interceptor error = %v", err)
			}

			if !handlerCalled {
				t.Error("Handler was not called")
			}
		})
	}
}

// TestUnaryClientInterceptorWithCorrelation tests correlation ID propagation for client calls
func TestUnaryClientInterceptorWithCorrelation(t *testing.T) {
	interceptor := UnaryClientInterceptorWithCorrelation()

	tests := []struct {
		name              string
		setupContext      func(context.Context) context.Context
		expectRequestID   bool
		expectCorrelationID bool
	}{
		{
			name: "Context with IDs",
			setupContext: func(ctx context.Context) context.Context {
				ctx = WithRequestID(ctx, "client-req-123")
				ctx = WithCorrelationID(ctx, "client-corr-456")
				return ctx
			},
			expectRequestID:   true,
			expectCorrelationID: true,
		},
		{
			name: "Empty context",
			setupContext: func(ctx context.Context) context.Context {
				return ctx
			},
			expectRequestID:   false,
			expectCorrelationID: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setupContext(context.Background())

			invokerCalled := false
			invoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
				invokerCalled = true

				// Extract outgoing metadata
				md, ok := metadata.FromOutgoingContext(ctx)
				if !ok && tt.expectRequestID {
					t.Error("Expected metadata in outgoing context")
					return nil
				}

				if tt.expectRequestID {
					requestIDs := md.Get(RequestIDMetadataKey)
					if len(requestIDs) == 0 {
						t.Error("Expected request ID in metadata")
					}
				}

				if tt.expectCorrelationID {
					corrIDs := md.Get(CorrelationIDMetadataKey)
					if len(corrIDs) == 0 {
						t.Error("Expected correlation ID in metadata")
					}
				}

				return nil
			}

			err := interceptor(ctx, "/test.Service/Test", "request", "reply", nil, invoker)
			if err != nil {
				t.Errorf("Interceptor error = %v", err)
			}

			if !invokerCalled {
				t.Error("Invoker was not called")
			}
		})
	}
}

// TestStreamClientInterceptorWithCorrelation tests correlation ID propagation for client streams
func TestStreamClientInterceptorWithCorrelation(t *testing.T) {
	interceptor := StreamClientInterceptorWithCorrelation()

	tests := []struct {
		name         string
		setupContext func(context.Context) context.Context
	}{
		{
			name: "Context with IDs",
			setupContext: func(ctx context.Context) context.Context {
				ctx = WithRequestID(ctx, "stream-client-req")
				ctx = WithCorrelationID(ctx, "stream-client-corr")
				ctx = WithUserID(ctx, "user-123")
				return ctx
			},
		},
		{
			name: "Empty context",
			setupContext: func(ctx context.Context) context.Context {
				return ctx
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setupContext(context.Background())

			streamerCalled := false
			streamer := func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, opts ...grpc.CallOption) (grpc.ClientStream, error) {
				streamerCalled = true

				// Verify metadata was added to context
				_, ok := metadata.FromOutgoingContext(ctx)
				if !ok {
					t.Error("Expected metadata in outgoing context")
				}

				return nil, nil
			}

			_, err := interceptor(ctx, nil, nil, "/test.Service/Stream", streamer)
			if err != nil {
				t.Errorf("Interceptor error = %v", err)
			}

			if !streamerCalled {
				t.Error("Streamer was not called")
			}
		})
	}
}

// TestCorrelationIDPropagation tests end-to-end correlation ID propagation
func TestCorrelationIDPropagation(t *testing.T) {
	logger := zap.NewNop()

	// Server-side interceptor
	serverInterceptor := UnaryServerInterceptorWithCorrelation(logger)

	// Client-side interceptor
	clientInterceptor := UnaryClientInterceptorWithCorrelation()

	// Create initial context with correlation ID
	initialCorrelationID := "e2e-corr-12345"
	ctx := WithCorrelationID(context.Background(), initialCorrelationID)

	// Client call
	clientInvoker := func(ctx context.Context, method string, req, reply interface{}, cc *grpc.ClientConn, opts ...grpc.CallOption) error {
		// Extract metadata that client would send
		md, _ := metadata.FromOutgoingContext(ctx)

		// Simulate server receiving this metadata
		serverCtx := metadata.NewIncomingContext(context.Background(), md)

		// Server handles request
		serverHandler := func(ctx context.Context, req interface{}) (interface{}, error) {
			// Verify correlation ID propagated
			receivedCorrelationID := GetCorrelationID(ctx)
			if receivedCorrelationID != initialCorrelationID {
				t.Errorf("Expected correlation ID %s, got %s", initialCorrelationID, receivedCorrelationID)
			}
			return "response", nil
		}

		info := &grpc.UnaryServerInfo{
			FullMethod: "/test.Service/E2ETest",
		}

		_, err := serverInterceptor(serverCtx, req, info, serverHandler)
		return err
	}

	// Execute client call with interceptor
	err := clientInterceptor(ctx, "/test.Service/E2ETest", "request", "reply", nil, clientInvoker)
	if err != nil {
		t.Errorf("End-to-end test error = %v", err)
	}
}
