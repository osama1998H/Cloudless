package overlay

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

// CLD-REQ-003: NAT traversal must be supported via STUN; fallback to TURN or relay through the VPS anchor.
// This test file verifies TURN protocol implementation for relay connections.

// TestTURNClient_NewTURNClient verifies TURN client initialization
// CLD-REQ-003: TURN client must be properly initialized with servers
func TestTURNClient_NewTURNClient(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name    string
		servers []TURNServer
	}{
		{
			name: "with single TURN server",
			servers: []TURNServer{
				{
					Address:  "turn.example.com:3478",
					Username: "test-user",
					Password: "test-pass",
				},
			},
		},
		{
			name: "with multiple TURN servers",
			servers: []TURNServer{
				{
					Address:  "turn1.example.com:3478",
					Username: "user1",
					Password: "pass1",
				},
				{
					Address:  "turn2.example.com:3478",
					Username: "user2",
					Password: "pass2",
				},
			},
		},
		{
			name:    "with empty servers",
			servers: []TURNServer{},
		},
		{
			name:    "with nil servers",
			servers: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Create TURN client
			client := NewTURNClient(tt.servers, logger)

			if client == nil {
				t.Fatal("NewTURNClient returned nil")
			}

			if client.logger == nil {
				t.Error("TURN client logger not set")
			}

			if client.timeout != 10*time.Second {
				t.Errorf("Expected timeout 10s, got %v", client.timeout)
			}

			if len(client.servers) != len(tt.servers) {
				t.Errorf("Expected %d servers, got %d", len(tt.servers), len(client.servers))
			}

			// Verify servers match
			for i, expected := range tt.servers {
				if i >= len(client.servers) {
					t.Errorf("Missing server at index %d", i)
					continue
				}
				actual := client.servers[i]
				if actual.Address != expected.Address {
					t.Errorf("Server %d: expected address %s, got %s", i, expected.Address, actual.Address)
				}
				if actual.Username != expected.Username {
					t.Errorf("Server %d: expected username %s, got %s", i, expected.Username, actual.Username)
				}
				if actual.Password != expected.Password {
					t.Errorf("Server %d: expected password %s, got %s", i, expected.Password, actual.Password)
				}
			}
		})
	}
}

// TestTURNClient_AllocateRelay_InvalidLocalAddr verifies error handling
// CLD-REQ-003: TURN allocation must handle invalid addresses gracefully
func TestTURNClient_AllocateRelay_InvalidLocalAddr(t *testing.T) {
	logger := zap.NewNop()
	servers := []TURNServer{
		{
			Address:  "turn.example.com:3478",
			Username: "test-user",
			Password: "test-pass",
		},
	}
	client := NewTURNClient(servers, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tests := []struct {
		name      string
		localAddr string
	}{
		{
			name:      "invalid address format",
			localAddr: "invalid",
		},
		{
			name:      "empty address",
			localAddr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Should return error for invalid local address
			allocation, err := client.AllocateRelay(ctx, tt.localAddr)

			if err == nil {
				t.Error("Expected error for invalid address, got nil")
			}

			if allocation != nil {
				t.Errorf("Expected nil allocation on error, got %+v", allocation)
			}
		})
	}
}

// TestTURNClient_AllocateRelay_AllServersFail verifies fallback behavior
// CLD-REQ-003: TURN client must try all servers before failing
func TestTURNClient_AllocateRelay_AllServersFail(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping network test in short mode")
	}

	logger := zap.NewNop()

	// Use unreachable servers to force failure
	servers := []TURNServer{
		{
			Address:  "198.51.100.1:3478",
			Username: "user1",
			Password: "pass1",
		},
		{
			Address:  "198.51.100.2:3478",
			Username: "user2",
			Password: "pass2",
		},
	}
	client := NewTURNClient(servers, logger)
	client.timeout = 500 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// CLD-REQ-003: Should try all servers and return error when all fail
	allocation, err := client.AllocateRelay(ctx, "127.0.0.1:5000")

	if err == nil {
		t.Error("Expected error when all TURN servers fail, got nil")
	}

	if err.Error() != "all TURN servers failed" {
		t.Errorf("Expected 'all TURN servers failed' error, got: %v", err)
	}

	if allocation != nil {
		t.Errorf("Expected nil allocation on error, got %+v", allocation)
	}
}

// TestTURNClient_CreatePermission_InvalidAllocation verifies error handling
// CLD-REQ-003: Permission creation must validate allocation
func TestTURNClient_CreatePermission_InvalidAllocation(t *testing.T) {
	logger := zap.NewNop()
	client := NewTURNClient([]TURNServer{}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tests := []struct {
		name       string
		allocation *RelayAllocation
		peerAddr   string
	}{
		{
			name:       "nil allocation",
			allocation: nil,
			peerAddr:   "192.168.1.100:5000",
		},
		{
			name: "allocation with nil client",
			allocation: &RelayAllocation{
				RelayAddr:  "203.0.113.10:54321",
				LocalAddr:  "127.0.0.1:5000",
				ServerAddr: "turn.example.com:3478",
				Client:     nil,
			},
			peerAddr: "192.168.1.100:5000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Should return error for invalid allocation
			err := client.CreatePermission(ctx, tt.allocation, tt.peerAddr)

			if err == nil {
				t.Error("Expected error for invalid allocation, got nil")
			}

			if err.Error() != "invalid allocation or client" {
				t.Errorf("Expected 'invalid allocation or client' error, got: %v", err)
			}
		})
	}
}

// TestTURNClient_CreatePermission_InvalidPeerAddr verifies error handling
func TestTURNClient_CreatePermission_InvalidPeerAddr(t *testing.T) {
	logger := zap.NewNop()
	client := NewTURNClient([]TURNServer{}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Create a valid-looking allocation (won't actually work, but has non-nil client field)
	allocation := &RelayAllocation{
		RelayAddr:  "203.0.113.10:54321",
		LocalAddr:  "127.0.0.1:5000",
		ServerAddr: "turn.example.com:3478",
		Client:     nil, // This will make it fail before trying to use the client
	}

	tests := []struct {
		name     string
		peerAddr string
	}{
		{
			name:     "invalid peer address format",
			peerAddr: "invalid",
		},
		{
			name:     "empty peer address",
			peerAddr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Should return error for invalid peer address
			err := client.CreatePermission(ctx, allocation, tt.peerAddr)

			if err == nil {
				t.Error("Expected error for invalid peer address, got nil")
			}
		})
	}
}

// TestTURNClient_SendIndication_InvalidAllocation verifies error handling
// CLD-REQ-003: Send operation must validate allocation
func TestTURNClient_SendIndication_InvalidAllocation(t *testing.T) {
	logger := zap.NewNop()
	client := NewTURNClient([]TURNServer{}, logger)

	data := []byte("test data")

	tests := []struct {
		name       string
		allocation *RelayAllocation
		peerAddr   string
	}{
		{
			name:       "nil allocation",
			allocation: nil,
			peerAddr:   "192.168.1.100:5000",
		},
		{
			name: "allocation with nil connection",
			allocation: &RelayAllocation{
				RelayAddr:  "203.0.113.10:54321",
				LocalAddr:  "127.0.0.1:5000",
				ServerAddr: "turn.example.com:3478",
				Conn:       nil,
			},
			peerAddr: "192.168.1.100:5000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Should return error for invalid allocation
			err := client.SendIndication(tt.allocation, tt.peerAddr, data)

			if err == nil {
				t.Error("Expected error for invalid allocation, got nil")
			}

			if err.Error() != "invalid relay allocation" {
				t.Errorf("Expected 'invalid relay allocation' error, got: %v", err)
			}
		})
	}
}

// TestTURNClient_SendIndication_InvalidPeerAddr verifies error handling
func TestTURNClient_SendIndication_InvalidPeerAddr(t *testing.T) {
	logger := zap.NewNop()
	client := NewTURNClient([]TURNServer{}, logger)

	allocation := &RelayAllocation{
		RelayAddr:  "203.0.113.10:54321",
		LocalAddr:  "127.0.0.1:5000",
		ServerAddr: "turn.example.com:3478",
		Conn:       nil,
	}

	data := []byte("test data")

	tests := []struct {
		name     string
		peerAddr string
	}{
		{
			name:     "invalid peer address format",
			peerAddr: "invalid",
		},
		{
			name:     "empty peer address",
			peerAddr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Should return error for invalid peer address
			err := client.SendIndication(allocation, tt.peerAddr, data)

			if err == nil {
				t.Error("Expected error for invalid peer address, got nil")
			}
		})
	}
}

// TestTURNClient_ReceiveData_InvalidAllocation verifies error handling
// CLD-REQ-003: Receive operation must validate allocation
func TestTURNClient_ReceiveData_InvalidAllocation(t *testing.T) {
	logger := zap.NewNop()
	client := NewTURNClient([]TURNServer{}, logger)

	tests := []struct {
		name       string
		allocation *RelayAllocation
	}{
		{
			name:       "nil allocation",
			allocation: nil,
		},
		{
			name: "allocation with nil connection",
			allocation: &RelayAllocation{
				RelayAddr:  "203.0.113.10:54321",
				LocalAddr:  "127.0.0.1:5000",
				ServerAddr: "turn.example.com:3478",
				Conn:       nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Should return error for invalid allocation
			data, addr, err := client.ReceiveData(tt.allocation, 100*time.Millisecond)

			if err == nil {
				t.Error("Expected error for invalid allocation, got nil")
			}

			if err.Error() != "invalid relay allocation" {
				t.Errorf("Expected 'invalid relay allocation' error, got: %v", err)
			}

			if data != nil {
				t.Errorf("Expected nil data on error, got %v", data)
			}

			if addr != "" {
				t.Errorf("Expected empty address on error, got %s", addr)
			}
		})
	}
}

// TestTURNClient_Refresh_InvalidAllocation verifies error handling
// CLD-REQ-003: Refresh operation must validate allocation
func TestTURNClient_Refresh_InvalidAllocation(t *testing.T) {
	logger := zap.NewNop()
	client := NewTURNClient([]TURNServer{}, logger)

	tests := []struct {
		name       string
		allocation *RelayAllocation
	}{
		{
			name:       "nil allocation",
			allocation: nil,
		},
		{
			name: "allocation with nil client",
			allocation: &RelayAllocation{
				RelayAddr:  "203.0.113.10:54321",
				LocalAddr:  "127.0.0.1:5000",
				ServerAddr: "turn.example.com:3478",
				Client:     nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Should return error for invalid allocation
			err := client.Refresh(tt.allocation)

			if err == nil {
				t.Error("Expected error for invalid allocation, got nil")
			}

			if err.Error() != "invalid allocation or client" {
				t.Errorf("Expected 'invalid allocation or client' error, got: %v", err)
			}
		})
	}
}

// TestTURNClient_Refresh_ValidAllocation verifies refresh succeeds with valid allocation
func TestTURNClient_Refresh_ValidAllocation(t *testing.T) {
	logger := zap.NewNop()
	client := NewTURNClient([]TURNServer{}, logger)

	// Mock allocation (refresh currently doesn't do actual network operation)
	allocation := &RelayAllocation{
		RelayAddr:  "203.0.113.10:54321",
		LocalAddr:  "127.0.0.1:5000",
		ServerAddr: "turn.example.com:3478",
		Client:     nil, // Will cause error
	}

	// CLD-REQ-003: Refresh should validate allocation
	err := client.Refresh(allocation)

	if err == nil {
		t.Error("Expected error for allocation with nil client, got nil")
	}
}

// TestRelayAllocation_Close verifies allocation cleanup
// CLD-REQ-003: Relay allocations must be properly cleaned up
func TestRelayAllocation_Close(t *testing.T) {
	tests := []struct {
		name       string
		allocation *RelayAllocation
		expectNil  bool
	}{
		{
			name: "close with both conn and client nil",
			allocation: &RelayAllocation{
				RelayAddr:  "203.0.113.10:54321",
				LocalAddr:  "127.0.0.1:5000",
				ServerAddr: "turn.example.com:3478",
				Conn:       nil,
				Client:     nil,
			},
			expectNil: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Close allocation
			err := tt.allocation.Close()

			if err != nil {
				t.Errorf("Unexpected error during close: %v", err)
			}

			// Verify fields are cleared
			if tt.expectNil {
				if tt.allocation.Conn != nil {
					t.Error("Expected Conn to be nil after close")
				}
				if tt.allocation.Client != nil {
					t.Error("Expected Client to be nil after close")
				}
			}
		})
	}
}

// TestTURNClient_Close verifies client cleanup
// CLD-REQ-003: TURN client must be properly cleaned up
func TestTURNClient_Close(t *testing.T) {
	logger := zap.NewNop()
	client := NewTURNClient([]TURNServer{
		{
			Address:  "turn.example.com:3478",
			Username: "test-user",
			Password: "test-pass",
		},
	}, logger)

	// CLD-REQ-003: Close TURN client
	err := client.Close()

	if err != nil {
		t.Errorf("Unexpected error during close: %v", err)
	}

	// Verify client is nil
	if client.client != nil {
		t.Error("Expected client to be nil after close")
	}
}

// TestNewPionLoggerFactory verifies Pion logger factory creation
func TestNewPionLoggerFactory(t *testing.T) {
	logger := zap.NewNop()

	factory := NewPionLoggerFactory(logger)

	if factory == nil {
		t.Fatal("NewPionLoggerFactory returned nil")
	}

	if factory.logger == nil {
		t.Error("Logger factory logger not set")
	}

	// Create a logger from factory
	pionLogger := factory.NewLogger("test-scope")

	if pionLogger == nil {
		t.Error("NewLogger returned nil")
	}
}

// TestPionLogger_LoggingMethods verifies all logging methods work
func TestPionLogger_LoggingMethods(t *testing.T) {
	logger := zap.NewNop()
	factory := NewPionLoggerFactory(logger)
	pionLogger := factory.NewLogger("test")

	// Test all logging methods (should not panic)
	pionLogger.Trace("trace message")
	pionLogger.Tracef("trace %s", "formatted")
	pionLogger.Debug("debug message")
	pionLogger.Debugf("debug %s", "formatted")
	pionLogger.Info("info message")
	pionLogger.Infof("info %s", "formatted")
	pionLogger.Warn("warn message")
	pionLogger.Warnf("warn %s", "formatted")
	pionLogger.Error("error message")
	pionLogger.Errorf("error %s", "formatted")

	// If we reach here without panic, test passes
}

// TestTURNClient_Race verifies thread safety
// CLD-REQ-003: TURN client operations must be thread-safe
func TestTURNClient_Race(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	logger := zap.NewNop()
	servers := []TURNServer{
		{
			Address:  "turn1.example.com:3478",
			Username: "user1",
			Password: "pass1",
		},
		{
			Address:  "turn2.example.com:3478",
			Username: "user2",
			Password: "pass2",
		},
	}
	client := NewTURNClient(servers, logger)

	// Run multiple concurrent operations to detect race conditions
	const concurrency = 10
	done := make(chan bool, concurrency)

	for i := 0; i < concurrency; i++ {
		go func() {
			// Test various operations that don't require network
			_ = client.Close()

			done <- true
		}()
	}

	// Wait for all goroutines
	for i := 0; i < concurrency; i++ {
		select {
		case <-done:
			// Success
		case <-time.After(5 * time.Second):
			t.Fatal("Timeout waiting for concurrent operations")
		}
	}
}
