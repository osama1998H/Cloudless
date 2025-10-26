package overlay

import (
	"context"
	"net"
	"testing"
	"time"

	"go.uber.org/zap"
)

// CLD-REQ-003: NAT traversal must be supported via STUN; fallback to TURN or relay through the VPS anchor.
// This test file verifies STUN protocol implementation for NAT discovery.

// TestSTUNClient_NewSTUNClient verifies STUN client initialization
// CLD-REQ-003: STUN client must be properly initialized with servers
func TestSTUNClient_NewSTUNClient(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name            string
		servers         []string
		expectedServers []string
	}{
		{
			name:    "with custom servers",
			servers: []string{"stun.example.com:3478", "stun2.example.com:3478"},
			expectedServers: []string{"stun.example.com:3478", "stun2.example.com:3478"},
		},
		{
			name:    "with empty servers (should use defaults)",
			servers: []string{},
			expectedServers: []string{
				"stun.l.google.com:19302",
				"stun1.l.google.com:19302",
				"stun2.l.google.com:19302",
			},
		},
		{
			name:    "with nil servers (should use defaults)",
			servers: nil,
			expectedServers: []string{
				"stun.l.google.com:19302",
				"stun1.l.google.com:19302",
				"stun2.l.google.com:19302",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Create STUN client
			client := NewSTUNClient(tt.servers, logger)

			if client == nil {
				t.Fatal("NewSTUNClient returned nil")
			}

			if client.logger == nil {
				t.Error("STUN client logger not set")
			}

			if client.timeout != 5*time.Second {
				t.Errorf("Expected timeout 5s, got %v", client.timeout)
			}

			if len(client.servers) != len(tt.expectedServers) {
				t.Errorf("Expected %d servers, got %d", len(tt.expectedServers), len(client.servers))
			}

			// Verify servers match
			for i, expected := range tt.expectedServers {
				if i >= len(client.servers) {
					t.Errorf("Missing server at index %d: %s", i, expected)
					continue
				}
				if client.servers[i] != expected {
					t.Errorf("Server mismatch at index %d: expected %s, got %s", i, expected, client.servers[i])
				}
			}
		})
	}
}

// TestSTUNClient_DetectNATType verifies NAT type detection logic
// CLD-REQ-003: STUN must correctly identify NAT type
func TestSTUNClient_DetectNATType(t *testing.T) {
	logger := zap.NewNop()
	client := NewSTUNClient([]string{"stun.example.com:3478"}, logger)

	tests := []struct {
		name           string
		localIP        string
		localPort      int
		publicIP       string
		publicPort     int
		expectedNATType NATType
	}{
		{
			name:           "no NAT - IPs match",
			localIP:        "192.168.1.100",
			localPort:      5000,
			publicIP:       "192.168.1.100",
			publicPort:     5000,
			expectedNATType: NATTypeNone,
		},
		{
			name:           "full cone NAT - ports match",
			localIP:        "192.168.1.100",
			localPort:      5000,
			publicIP:       "203.0.113.10",
			publicPort:     5000,
			expectedNATType: NATTypeFullCone,
		},
		{
			name:           "port-restricted cone NAT - ports differ",
			localIP:        "192.168.1.100",
			localPort:      5000,
			publicIP:       "203.0.113.10",
			publicPort:     54321,
			expectedNATType: NATTypePortRestrictedCone,
		},
		{
			name:           "port-restricted cone NAT - different IPs",
			localIP:        "10.0.0.50",
			localPort:      8000,
			publicIP:       "198.51.100.20",
			publicPort:     62000,
			expectedNATType: NATTypePortRestrictedCone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Detect NAT type based on mapping behavior
			natType := client.detectNATType(tt.localIP, tt.localPort, tt.publicIP, tt.publicPort)

			if natType != tt.expectedNATType {
				t.Errorf("Expected NAT type %s, got %s", tt.expectedNATType, natType)
			}
		})
	}
}

// TestParseAddress verifies address parsing helper function
func TestParseAddress(t *testing.T) {
	tests := []struct {
		name        string
		addr        string
		expectedIP  string
		expectedPort int
		expectError bool
	}{
		{
			name:        "valid IPv4 with port",
			addr:        "192.168.1.100:5000",
			expectedIP:  "192.168.1.100",
			expectedPort: 5000,
			expectError: false,
		},
		{
			name:        "localhost with port",
			addr:        "127.0.0.1:8080",
			expectedIP:  "127.0.0.1",
			expectedPort: 8080,
			expectError: false,
		},
		{
			name:        "empty host (should default to 0.0.0.0)",
			addr:        ":5000",
			expectedIP:  "0.0.0.0",
			expectedPort: 5000,
			expectError: false,
		},
		{
			name:        "IPv6 with port",
			addr:        "[::1]:8080",
			expectedIP:  "::1",
			expectedPort: 8080,
			expectError: false,
		},
		{
			name:        "invalid address format",
			addr:        "invalid",
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ip, port, err := parseAddress(tt.addr)

			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if ip != tt.expectedIP {
				t.Errorf("Expected IP %s, got %s", tt.expectedIP, ip)
			}

			if port != tt.expectedPort {
				t.Errorf("Expected port %d, got %d", tt.expectedPort, port)
			}
		})
	}
}

// TestSTUNClient_GetPublicEndpoint_Error verifies error handling
// CLD-REQ-003: STUN discovery must handle errors gracefully
func TestSTUNClient_GetPublicEndpoint_Error(t *testing.T) {
	logger := zap.NewNop()

	// Use unreachable servers to force failure
	client := NewSTUNClient([]string{"198.51.100.1:9999"}, logger)
	client.timeout = 100 * time.Millisecond // Short timeout for test

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// CLD-REQ-003: Should fail gracefully when all STUN servers are unreachable
	endpoint, err := client.GetPublicEndpoint(ctx, "127.0.0.1:5000")

	if err == nil {
		t.Error("Expected error when STUN servers unreachable, got nil")
	}

	if endpoint != "" {
		t.Errorf("Expected empty endpoint on error, got %s", endpoint)
	}

	if err.Error() != "all STUN servers failed" {
		t.Errorf("Expected 'all STUN servers failed' error, got: %v", err)
	}
}

// TestSTUNClient_DiscoverNATInfo_InvalidLocalAddr verifies error handling for invalid local address
func TestSTUNClient_DiscoverNATInfo_InvalidLocalAddr(t *testing.T) {
	logger := zap.NewNop()
	client := NewSTUNClient([]string{"stun.l.google.com:19302"}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tests := []struct {
		name      string
		localAddr string
	}{
		{
			name:      "invalid address format",
			localAddr: "invalid-address",
		},
		{
			name:      "empty address",
			localAddr: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Should return error for invalid local address
			natInfo, err := client.DiscoverNATInfo(ctx, tt.localAddr)

			if err == nil {
				t.Error("Expected error for invalid address, got nil")
			}

			if natInfo != nil {
				t.Errorf("Expected nil NAT info on error, got %+v", natInfo)
			}
		})
	}
}

// TestSTUNClient_TestConnectivity_Timeout verifies connectivity testing timeout
func TestSTUNClient_TestConnectivity_Timeout(t *testing.T) {
	logger := zap.NewNop()
	client := NewSTUNClient([]string{"stun.example.com:3478"}, logger)
	client.timeout = 100 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	// CLD-REQ-003: Connectivity test should timeout for unreachable peers
	err := client.TestConnectivity(ctx, "127.0.0.1:5000", "198.51.100.1:9999")

	if err == nil {
		t.Error("Expected timeout error, got nil")
	}
}

// TestSTUNClient_PerformHolePunch_InvalidAddress verifies hole punching error handling
func TestSTUNClient_PerformHolePunch_InvalidAddress(t *testing.T) {
	logger := zap.NewNop()
	client := NewSTUNClient([]string{"stun.example.com:3478"}, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	tests := []struct {
		name      string
		localAddr string
		remoteAddr string
	}{
		{
			name:      "invalid local address",
			localAddr: "invalid",
			remoteAddr: "192.168.1.100:5000",
		},
		{
			name:      "invalid remote address",
			localAddr: "127.0.0.1:5000",
			remoteAddr: "invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Hole punching should return error for invalid addresses
			err := client.PerformHolePunch(ctx, tt.localAddr, tt.remoteAddr)

			if err == nil {
				t.Error("Expected error for invalid address, got nil")
			}
		})
	}
}

// TestSTUNClient_PerformHolePunch_UnreachablePeer verifies hole punching with unreachable peer
func TestSTUNClient_PerformHolePunch_UnreachablePeer(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping network test in short mode")
	}

	logger := zap.NewNop()
	client := NewSTUNClient([]string{"stun.example.com:3478"}, logger)
	client.timeout = 500 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Use a local address that we can bind to
	localListener, err := net.ListenUDP("udp", &net.UDPAddr{IP: net.ParseIP("127.0.0.1"), Port: 0})
	if err != nil {
		t.Fatalf("Failed to create local listener: %v", err)
	}
	defer localListener.Close()

	localAddr := localListener.LocalAddr().String()

	// CLD-REQ-003: Hole punching should timeout for unreachable peer
	err = client.PerformHolePunch(ctx, localAddr, "198.51.100.1:9999")

	if err == nil {
		t.Error("Expected error for unreachable peer, got nil")
	}
}

// TestSTUNClient_Race verifies thread safety
// CLD-REQ-003: STUN client operations must be thread-safe
func TestSTUNClient_Race(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping race test in short mode")
	}

	logger := zap.NewNop()
	client := NewSTUNClient([]string{
		"stun.l.google.com:19302",
		"stun1.l.google.com:19302",
	}, logger)

	// Run multiple concurrent operations to detect race conditions
	const concurrency = 10
	done := make(chan bool, concurrency)

	for i := 0; i < concurrency; i++ {
		go func(index int) {
			// Try NAT type detection (doesn't require network)
			_ = client.detectNATType("192.168.1.100", 5000+index, "203.0.113.10", 54321+index)

			// Try address parsing
			_, _, _ = parseAddress("127.0.0.1:5000")

			done <- true
		}(i)
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
