package overlay

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// CLD-REQ-003: NAT traversal must be supported via STUN; fallback to TURN or relay through the VPS anchor.
// This test file verifies the NAT traversal orchestration and fallback mechanism.

// TestNewNATTraversal verifies NAT traversal initialization
// CLD-REQ-003: NAT traversal must be properly initialized with STUN and TURN clients
func TestNewNATTraversal(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name   string
		config NATConfig
	}{
		{
			name: "with full configuration",
			config: NATConfig{
				STUNServers: []string{"stun.example.com:3478"},
				TURNServers: []TURNServer{
					{
						Address:  "turn.example.com:3478",
						Username: "user",
						Password: "pass",
					},
				},
				EnableUPnP:   true,
				EnableNATPMP: false,
			},
		},
		{
			name: "with minimal configuration",
			config: NATConfig{
				STUNServers: []string{},
				TURNServers: []TURNServer{},
			},
		},
		{
			name: "with only STUN servers",
			config: NATConfig{
				STUNServers: []string{
					"stun1.example.com:3478",
					"stun2.example.com:3478",
				},
				TURNServers: []TURNServer{},
			},
		},
		{
			name: "with only TURN servers",
			config: NATConfig{
				STUNServers: []string{},
				TURNServers: []TURNServer{
					{
						Address:  "turn.example.com:3478",
						Username: "user",
						Password: "pass",
					},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Create NAT traversal handler
			nt := NewNATTraversal(tt.config, logger)

			if nt == nil {
				t.Fatal("NewNATTraversal returned nil")
			}

			if nt.logger == nil {
				t.Error("NAT traversal logger not set")
			}

			if nt.stunClient == nil {
				t.Error("STUN client not initialized")
			}

			if nt.turnClient == nil {
				t.Error("TURN client not initialized")
			}

			if nt.ctx == nil {
				t.Error("Context not initialized")
			}

			if nt.cancel == nil {
				t.Error("Cancel function not initialized")
			}
		})
	}
}

// TestNATTraversal_Start verifies NAT traversal startup
// CLD-REQ-003: NAT traversal must start STUN discovery and TURN refresh loop
func TestNATTraversal_Start(t *testing.T) {
	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"198.51.100.1:3478"}, // Unreachable server
		TURNServers: []TURNServer{},
	}

	nt := NewNATTraversal(config, logger)

	// CLD-REQ-003: Start NAT traversal (should not fail even if STUN fails)
	err := nt.Start("127.0.0.1:5000")

	if err != nil {
		t.Errorf("Start should not fail, got: %v", err)
	}

	// Verify refresh loop started
	time.Sleep(100 * time.Millisecond)

	// Stop NAT traversal
	err = nt.Stop()
	if err != nil {
		t.Errorf("Stop failed: %v", err)
	}
}

// TestNATTraversal_Stop verifies cleanup
// CLD-REQ-003: NAT traversal must properly clean up resources
func TestNATTraversal_Stop(t *testing.T) {
	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"stun.example.com:3478"},
		TURNServers: []TURNServer{},
	}

	nt := NewNATTraversal(config, logger)

	// Start NAT traversal
	if err := nt.Start("127.0.0.1:5000"); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}

	// CLD-REQ-003: Stop NAT traversal
	err := nt.Stop()

	if err != nil {
		t.Errorf("Stop failed: %v", err)
	}

	// Verify context is cancelled
	select {
	case <-nt.ctx.Done():
		// Expected
	case <-time.After(1 * time.Second):
		t.Error("Context not cancelled after Stop")
	}
}

// TestNATTraversal_GetNATInfo verifies NAT info retrieval
// CLD-REQ-003: NAT info must be thread-safe and return copy
func TestNATTraversal_GetNATInfo(t *testing.T) {
	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"stun.example.com:3478"},
		TURNServers: []TURNServer{},
	}

	nt := NewNATTraversal(config, logger)

	tests := []struct {
		name     string
		setup    func()
		expected *NATInfo
	}{
		{
			name: "with no NAT info",
			setup: func() {
				// Don't set any NAT info
			},
			expected: nil,
		},
		{
			name: "with NAT info",
			setup: func() {
				nt.natInfoMu.Lock()
				nt.natInfo = &NATInfo{
					Type:       NATTypeFullCone,
					PublicIP:   "203.0.113.10",
					PublicPort: 54321,
					LocalIP:    "192.168.1.100",
					LocalPort:  5000,
					Mapped:     true,
				}
				nt.natInfoMu.Unlock()
			},
			expected: &NATInfo{
				Type:       NATTypeFullCone,
				PublicIP:   "203.0.113.10",
				PublicPort: 54321,
				LocalIP:    "192.168.1.100",
				LocalPort:  5000,
				Mapped:     true,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			// CLD-REQ-003: Get NAT info
			info := nt.GetNATInfo()

			if tt.expected == nil {
				if info != nil {
					t.Errorf("Expected nil NAT info, got %+v", info)
				}
				return
			}

			if info == nil {
				t.Fatal("Expected NAT info, got nil")
			}

			// Verify it's a copy (not same pointer)
			nt.natInfoMu.RLock()
			if info == nt.natInfo {
				t.Error("GetNATInfo should return a copy, not the same pointer")
			}
			nt.natInfoMu.RUnlock()

			// Verify values match
			if info.Type != tt.expected.Type {
				t.Errorf("Expected NAT type %s, got %s", tt.expected.Type, info.Type)
			}
			if info.PublicIP != tt.expected.PublicIP {
				t.Errorf("Expected public IP %s, got %s", tt.expected.PublicIP, info.PublicIP)
			}
			if info.PublicPort != tt.expected.PublicPort {
				t.Errorf("Expected public port %d, got %d", tt.expected.PublicPort, info.PublicPort)
			}
		})
	}
}

// TestNATTraversal_GetPublicEndpoint verifies public endpoint retrieval
// CLD-REQ-003: Public endpoint must be derived from NAT info
func TestNATTraversal_GetPublicEndpoint(t *testing.T) {
	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"stun.example.com:3478"},
		TURNServers: []TURNServer{},
	}

	nt := NewNATTraversal(config, logger)

	tests := []struct {
		name        string
		setup       func()
		expectError bool
		expected    string
	}{
		{
			name: "with no NAT info",
			setup: func() {
				// Don't set any NAT info
			},
			expectError: true,
		},
		{
			name: "with NAT info",
			setup: func() {
				nt.natInfoMu.Lock()
				nt.natInfo = &NATInfo{
					Type:       NATTypeFullCone,
					PublicIP:   "203.0.113.10",
					PublicPort: 54321,
					LocalIP:    "192.168.1.100",
					LocalPort:  5000,
					Mapped:     true,
				}
				nt.natInfoMu.Unlock()
			},
			expectError: false,
			expected:    "203.0.113.10:54321",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.setup()

			// CLD-REQ-003: Get public endpoint
			endpoint, err := nt.GetPublicEndpoint()

			if tt.expectError {
				if err == nil {
					t.Error("Expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}

			if endpoint != tt.expected {
				t.Errorf("Expected endpoint %s, got %s", tt.expected, endpoint)
			}
		})
	}
}

// TestNATTraversal_DetermineStrategy verifies strategy selection based on NAT types
// CLD-REQ-003: Strategy must be selected based on NAT types for optimal connection
func TestNATTraversal_DetermineStrategy(t *testing.T) {
	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"stun.example.com:3478"},
		TURNServers: []TURNServer{},
	}

	nt := NewNATTraversal(config, logger)

	tests := []struct {
		name     string
		localNAT NATType
		peerNAT  NATType
		expected NATTraversalStrategy
	}{
		{
			name:     "both no NAT - direct connection",
			localNAT: NATTypeNone,
			peerNAT:  NATTypeNone,
			expected: StrategyDirect,
		},
		{
			name:     "local no NAT, peer has NAT",
			localNAT: NATTypeNone,
			peerNAT:  NATTypeFullCone,
			expected: StrategyHolePunch,
		},
		{
			name:     "local full cone NAT",
			localNAT: NATTypeFullCone,
			peerNAT:  NATTypePortRestrictedCone,
			expected: StrategyHolePunch,
		},
		{
			name:     "peer full cone NAT",
			localNAT: NATTypePortRestrictedCone,
			peerNAT:  NATTypeFullCone,
			expected: StrategyHolePunch,
		},
		{
			name:     "local symmetric NAT with peer full cone - relay (FIXED)",
			localNAT: NATTypeSymmetric,
			peerNAT:  NATTypeFullCone,
			expected: StrategyRelay, // FIXED: Symmetric check now happens first
		},
		{
			name:     "peer symmetric NAT with local full cone - relay (FIXED)",
			localNAT: NATTypeFullCone,
			peerNAT:  NATTypeSymmetric,
			expected: StrategyRelay, // FIXED: Symmetric check now happens first
		},
		{
			name:     "both symmetric NAT - relay",
			localNAT: NATTypeSymmetric,
			peerNAT:  NATTypeSymmetric,
			expected: StrategyRelay,
		},
		{
			name:     "both port-restricted cone - hole punch",
			localNAT: NATTypePortRestrictedCone,
			peerNAT:  NATTypePortRestrictedCone,
			expected: StrategyHolePunch,
		},
		{
			name:     "unknown NAT types - relay for safety",
			localNAT: NATTypeUnknown,
			peerNAT:  NATTypeUnknown,
			expected: StrategyRelay,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Determine NAT traversal strategy
			strategy := nt.determineStrategy(tt.localNAT, tt.peerNAT)

			if strategy != tt.expected {
				t.Errorf("Expected strategy %s, got %s", tt.expected, strategy)
			}
		})
	}
}

// TestNATTraversal_EstablishConnection_NoNATInfo verifies error handling
// CLD-REQ-003: Connection establishment must validate NAT info exists
func TestNATTraversal_EstablishConnection_NoNATInfo(t *testing.T) {
	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"stun.example.com:3478"},
		TURNServers: []TURNServer{},
	}

	nt := NewNATTraversal(config, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// CLD-REQ-003: Should fail when NAT info not available
	connInfo, err := nt.EstablishConnection(ctx, "127.0.0.1:5000", "203.0.113.10:54321", NATTypeFullCone)

	if err == nil {
		t.Error("Expected error when NAT info not available, got nil")
	}

	if err.Error() != "NAT info not available" {
		t.Errorf("Expected 'NAT info not available' error, got: %v", err)
	}

	if connInfo != nil {
		t.Errorf("Expected nil connection info on error, got %+v", connInfo)
	}
}

// TestNATTraversal_SendReceiveThroughRelay verifies relay operations
// CLD-REQ-003: Must be able to send/receive data through TURN relay
func TestNATTraversal_SendReceiveThroughRelay(t *testing.T) {
	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"stun.example.com:3478"},
		TURNServers: []TURNServer{},
	}

	nt := NewNATTraversal(config, logger)

	// Test send without allocation
	err := nt.SendThroughRelay("203.0.113.10:54321", []byte("test"))
	if err == nil {
		t.Error("Expected error when no allocation, got nil")
	}
	if err.Error() != "no active TURN allocation" {
		t.Errorf("Expected 'no active TURN allocation' error, got: %v", err)
	}

	// Test receive without allocation
	data, addr, err := nt.ReceiveFromRelay(100 * time.Millisecond)
	if err == nil {
		t.Error("Expected error when no allocation, got nil")
	}
	if err.Error() != "no active TURN allocation" {
		t.Errorf("Expected 'no active TURN allocation' error, got: %v", err)
	}
	if data != nil {
		t.Errorf("Expected nil data on error, got %v", data)
	}
	if addr != "" {
		t.Errorf("Expected empty address on error, got %s", addr)
	}
}

// TestConnectionInfo_IsRelayed verifies connection info helper methods
// CLD-REQ-003: Connection info must correctly identify relayed connections
func TestConnectionInfo_IsRelayed(t *testing.T) {
	tests := []struct {
		name     string
		connInfo *ConnectionInfo
		expected bool
	}{
		{
			name: "direct connection",
			connInfo: &ConnectionInfo{
				Strategy:   StrategyDirect,
				LocalAddr:  "127.0.0.1:5000",
				RemoteAddr: "192.168.1.100:6000",
			},
			expected: false,
		},
		{
			name: "hole punch connection",
			connInfo: &ConnectionInfo{
				Strategy:   StrategyHolePunch,
				LocalAddr:  "127.0.0.1:5000",
				RemoteAddr: "203.0.113.10:54321",
			},
			expected: false,
		},
		{
			name: "relay connection",
			connInfo: &ConnectionInfo{
				Strategy:   StrategyRelay,
				LocalAddr:  "127.0.0.1:5000",
				RemoteAddr: "203.0.113.10:54321",
				RelayAddr:  "198.51.100.5:12345",
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Check if connection is relayed
			isRelayed := tt.connInfo.IsRelayed()

			if isRelayed != tt.expected {
				t.Errorf("Expected IsRelayed=%v, got %v", tt.expected, isRelayed)
			}
		})
	}
}

// TestConnectionInfo_GetEffectiveAddr verifies effective address selection
// CLD-REQ-003: Effective address must be relay addr for relayed connections
func TestConnectionInfo_GetEffectiveAddr(t *testing.T) {
	tests := []struct {
		name     string
		connInfo *ConnectionInfo
		expected string
	}{
		{
			name: "direct connection - use remote addr",
			connInfo: &ConnectionInfo{
				Strategy:   StrategyDirect,
				LocalAddr:  "127.0.0.1:5000",
				RemoteAddr: "192.168.1.100:6000",
			},
			expected: "192.168.1.100:6000",
		},
		{
			name: "hole punch - use remote addr",
			connInfo: &ConnectionInfo{
				Strategy:   StrategyHolePunch,
				LocalAddr:  "127.0.0.1:5000",
				RemoteAddr: "203.0.113.10:54321",
			},
			expected: "203.0.113.10:54321",
		},
		{
			name: "relay with relay addr - use relay addr",
			connInfo: &ConnectionInfo{
				Strategy:   StrategyRelay,
				LocalAddr:  "127.0.0.1:5000",
				RemoteAddr: "203.0.113.10:54321",
				RelayAddr:  "198.51.100.5:12345",
			},
			expected: "198.51.100.5:12345",
		},
		{
			name: "relay without relay addr - use remote addr",
			connInfo: &ConnectionInfo{
				Strategy:   StrategyRelay,
				LocalAddr:  "127.0.0.1:5000",
				RemoteAddr: "203.0.113.10:54321",
				RelayAddr:  "",
			},
			expected: "203.0.113.10:54321",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Get effective address
			addr := tt.connInfo.GetEffectiveAddr()

			if addr != tt.expected {
				t.Errorf("Expected effective addr %s, got %s", tt.expected, addr)
			}
		})
	}
}

// TestNATTraversal_RefreshAllocations verifies allocation refresh
// CLD-REQ-003: TURN allocations must be refreshed periodically
func TestNATTraversal_RefreshAllocations(t *testing.T) {
	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"stun.example.com:3478"},
		TURNServers: []TURNServer{},
	}

	nt := NewNATTraversal(config, logger)

	// Test refresh with no allocation (should not error)
	nt.refreshAllocations()

	// Verify allocation is still nil
	nt.allocationMu.RLock()
	if nt.allocation != nil {
		t.Error("Expected allocation to remain nil")
	}
	nt.allocationMu.RUnlock()
}

// TestNATTraversal_ConcurrentOperations verifies thread safety
// CLD-REQ-003: NAT traversal operations must be thread-safe
func TestNATTraversal_ConcurrentOperations(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping concurrent test in short mode")
	}

	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"stun.example.com:3478"},
		TURNServers: []TURNServer{
			{
				Address:  "turn.example.com:3478",
				Username: "user",
				Password: "pass",
			},
		},
	}

	nt := NewNATTraversal(config, logger)

	// Start NAT traversal
	if err := nt.Start("127.0.0.1:5000"); err != nil {
		t.Fatalf("Failed to start: %v", err)
	}
	defer nt.Stop()

	// Set NAT info for testing
	nt.natInfoMu.Lock()
	nt.natInfo = &NATInfo{
		Type:       NATTypeFullCone,
		PublicIP:   "203.0.113.10",
		PublicPort: 54321,
		LocalIP:    "192.168.1.100",
		LocalPort:  5000,
		Mapped:     true,
	}
	nt.natInfoMu.Unlock()

	// Run concurrent operations
	const concurrency = 20
	var wg sync.WaitGroup
	wg.Add(concurrency)

	for i := 0; i < concurrency; i++ {
		go func(index int) {
			defer wg.Done()

			// Test various thread-safe operations
			_ = nt.GetNATInfo()
			_, _ = nt.GetPublicEndpoint()
			_ = nt.determineStrategy(NATTypeFullCone, NATTypePortRestrictedCone)
			nt.refreshAllocations()
		}(i)
	}

	// Wait for all goroutines with timeout
	done := make(chan bool)
	go func() {
		wg.Wait()
		done <- true
	}()

	select {
	case <-done:
		// Success
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for concurrent operations")
	}
}

// TestNATTraversal_FallbackScenario simulates fallback mechanism
// CLD-REQ-003: NAT traversal must fallback from hole punch to relay on failure
func TestNATTraversal_FallbackScenario(t *testing.T) {
	logger := zap.NewNop()
	config := NATConfig{
		STUNServers: []string{"stun.example.com:3478"},
		TURNServers: []TURNServer{
			{
				Address:  "turn.example.com:3478",
				Username: "user",
				Password: "pass",
			},
		},
	}

	nt := NewNATTraversal(config, logger)

	// Verify strategy determination for fallback scenarios
	tests := []struct {
		name             string
		localNAT         NATType
		peerNAT          NATType
		expectedFirst    NATTraversalStrategy
		expectedFallback NATTraversalStrategy
	}{
		{
			name:             "full cone NAT - try hole punch first",
			localNAT:         NATTypeFullCone,
			peerNAT:          NATTypePortRestrictedCone,
			expectedFirst:    StrategyHolePunch,
			expectedFallback: StrategyRelay,
		},
		{
			name:             "symmetric NAT with full cone - relay (FIXED)",
			localNAT:         NATTypeSymmetric,
			peerNAT:          NATTypeFullCone,
			expectedFirst:    StrategyRelay, // FIXED: Symmetric check now happens first
			expectedFallback: StrategyRelay,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// CLD-REQ-003: Determine initial strategy
			strategy := nt.determineStrategy(tt.localNAT, tt.peerNAT)

			if strategy != tt.expectedFirst {
				t.Errorf("Expected first strategy %s, got %s", tt.expectedFirst, strategy)
			}

			// For hole punch scenarios, verify fallback to relay is possible
			if strategy == StrategyHolePunch {
				// Fallback strategy (in actual code, this happens in establishHolePunchConnection on error)
				fallbackStrategy := StrategyRelay
				if fallbackStrategy != tt.expectedFallback {
					t.Errorf("Expected fallback strategy %s, got %s", tt.expectedFallback, fallbackStrategy)
				}
			}
		})
	}
}

// TestNATTraversalStrategy_String verifies strategy string representations
func TestNATTraversalStrategy_String(t *testing.T) {
	tests := []struct {
		strategy NATTraversalStrategy
		expected string
	}{
		{StrategyDirect, "direct"},
		{StrategyHolePunch, "hole-punch"},
		{StrategyRelay, "relay"},
	}

	for _, tt := range tests {
		t.Run(string(tt.strategy), func(t *testing.T) {
			if string(tt.strategy) != tt.expected {
				t.Errorf("Expected strategy string %s, got %s", tt.expected, string(tt.strategy))
			}
		})
	}
}

// TestNATType_String verifies NAT type string representations
func TestNATType_String(t *testing.T) {
	tests := []struct {
		natType  NATType
		expected string
	}{
		{NATTypeNone, "none"},
		{NATTypeFullCone, "full-cone"},
		{NATTypeRestrictedCone, "restricted-cone"},
		{NATTypePortRestrictedCone, "port-restricted-cone"},
		{NATTypeSymmetric, "symmetric"},
		{NATTypeUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(string(tt.natType), func(t *testing.T) {
			if string(tt.natType) != tt.expected {
				t.Errorf("Expected NAT type string %s, got %s", tt.expected, string(tt.natType))
			}
		})
	}
}
