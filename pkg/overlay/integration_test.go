//go:build integration

package overlay

import (
	"context"
	"fmt"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/pion/stun"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// MockSTUNServer is a mock STUN server for integration testing
type MockSTUNServer struct {
	addr     *net.UDPAddr
	conn     *net.UDPConn
	running  bool
	mu       sync.Mutex
	logger   *zap.Logger
	stopChan chan struct{}
}

// NewMockSTUNServer creates a new mock STUN server
func NewMockSTUNServer(t *testing.T, port int) *MockSTUNServer {
	logger := zap.NewNop()

	addr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: port,
	}

	conn, err := net.ListenUDP("udp", addr)
	require.NoError(t, err, "failed to create mock STUN server")

	return &MockSTUNServer{
		addr:     addr,
		conn:     conn,
		logger:   logger,
		stopChan: make(chan struct{}),
	}
}

// Start starts the mock STUN server
func (s *MockSTUNServer) Start(t *testing.T) {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	go s.handleRequests(t)
}

// Stop stops the mock STUN server
func (s *MockSTUNServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		close(s.stopChan)
		s.conn.Close()
		s.running = false
	}
}

// handleRequests handles incoming STUN requests
func (s *MockSTUNServer) handleRequests(t *testing.T) {
	buf := make([]byte, 1500)

	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		s.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}

		// Parse STUN message
		msg := &stun.Message{Raw: buf[:n]}
		if err := msg.Decode(); err != nil {
			s.logger.Error("Failed to decode STUN message", zap.Error(err))
			continue
		}

		// Only handle binding requests
		if msg.Type != stun.BindingRequest {
			continue
		}

		// Create response with XOR-MAPPED-ADDRESS
		response := stun.MustBuild(msg, stun.BindingSuccess,
			&stun.XORMappedAddress{
				IP:   addr.IP,
				Port: addr.Port,
			},
		)

		// Send response
		if _, err := s.conn.WriteToUDP(response.Raw, addr); err != nil {
			s.logger.Error("Failed to send STUN response", zap.Error(err))
		}
	}
}

// Address returns the server address
func (s *MockSTUNServer) Address() string {
	return s.addr.String()
}

// MockTURNServer is a mock TURN server for integration testing
type MockTURNServer struct {
	addr        *net.UDPAddr
	conn        *net.UDPConn
	running     bool
	mu          sync.Mutex
	logger      *zap.Logger
	stopChan    chan struct{}
	allocations map[string]*mockTURNAllocation
}

// mockTURNAllocation represents a TURN allocation
type mockTURNAllocation struct {
	clientAddr *net.UDPAddr
	relayAddr  *net.UDPAddr
	username   string
	password   string
}

// NewMockTURNServer creates a new mock TURN server
func NewMockTURNServer(t *testing.T, port int) *MockTURNServer {
	logger := zap.NewNop()

	addr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: port,
	}

	conn, err := net.ListenUDP("udp", addr)
	require.NoError(t, err, "failed to create mock TURN server")

	return &MockTURNServer{
		addr:        addr,
		conn:        conn,
		logger:      logger,
		stopChan:    make(chan struct{}),
		allocations: make(map[string]*mockTURNAllocation),
	}
}

// Start starts the mock TURN server
func (s *MockTURNServer) Start(t *testing.T) {
	s.mu.Lock()
	s.running = true
	s.mu.Unlock()

	go s.handleRequests(t)
}

// Stop stops the mock TURN server
func (s *MockTURNServer) Stop() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.running {
		close(s.stopChan)
		s.conn.Close()
		s.running = false
	}
}

// handleRequests handles incoming TURN requests
func (s *MockTURNServer) handleRequests(t *testing.T) {
	buf := make([]byte, 1500)

	for {
		select {
		case <-s.stopChan:
			return
		default:
		}

		s.conn.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
				continue
			}
			return
		}

		// Parse STUN message (TURN uses STUN framing)
		msg := &stun.Message{Raw: buf[:n]}
		if err := msg.Decode(); err != nil {
			s.logger.Error("Failed to decode TURN message", zap.Error(err))
			continue
		}

		// Handle different TURN methods
		switch msg.Type.Method {
		case stun.MethodAllocate:
			s.handleAllocate(t, msg, addr)
		case stun.MethodCreatePermission:
			s.handleCreatePermission(t, msg, addr)
		case stun.MethodRefresh:
			s.handleRefresh(t, msg, addr)
		}
	}
}

// handleAllocate handles TURN Allocate requests
func (s *MockTURNServer) handleAllocate(t *testing.T, msg *stun.Message, addr *net.UDPAddr) {
	// Create a mock relay address
	relayAddr := &net.UDPAddr{
		IP:   net.ParseIP("127.0.0.1"),
		Port: 50000 + len(s.allocations),
	}

	// Store allocation
	s.mu.Lock()
	s.allocations[addr.String()] = &mockTURNAllocation{
		clientAddr: addr,
		relayAddr:  relayAddr,
		username:   "testuser",
		password:   "testpass",
	}
	s.mu.Unlock()

	// Create success response with XOR-RELAYED-ADDRESS
	response := stun.MustBuild(msg, stun.NewType(stun.MethodAllocate, stun.ClassSuccessResponse),
		&stun.XORMappedAddress{
			IP:   relayAddr.IP,
			Port: relayAddr.Port,
		},
	)

	// Send response
	if _, err := s.conn.WriteToUDP(response.Raw, addr); err != nil {
		s.logger.Error("Failed to send TURN allocate response", zap.Error(err))
	}
}

// handleCreatePermission handles TURN CreatePermission requests
func (s *MockTURNServer) handleCreatePermission(t *testing.T, msg *stun.Message, addr *net.UDPAddr) {
	// Create success response
	response := stun.MustBuild(msg, stun.NewType(stun.MethodCreatePermission, stun.ClassSuccessResponse))

	// Send response
	if _, err := s.conn.WriteToUDP(response.Raw, addr); err != nil {
		s.logger.Error("Failed to send TURN permission response", zap.Error(err))
	}
}

// handleRefresh handles TURN Refresh requests
func (s *MockTURNServer) handleRefresh(t *testing.T, msg *stun.Message, addr *net.UDPAddr) {
	// Create success response
	response := stun.MustBuild(msg, stun.NewType(stun.MethodRefresh, stun.ClassSuccessResponse))

	// Send response
	if _, err := s.conn.WriteToUDP(response.Raw, addr); err != nil {
		s.logger.Error("Failed to send TURN refresh response", zap.Error(err))
	}
}

// Address returns the server address
func (s *MockTURNServer) Address() string {
	return s.addr.String()
}

// Integration Tests

// TestIntegration_STUN_Discovery tests STUN NAT discovery with mock server
func TestIntegration_STUN_Discovery(t *testing.T) {
	// Start mock STUN server
	stunServer := NewMockSTUNServer(t, 19302)
	stunServer.Start(t)
	defer stunServer.Stop()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Create STUN client
	logger := zap.NewNop()
	client := NewSTUNClient([]string{stunServer.Address()}, logger)

	// Test NAT discovery
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	localAddr := "127.0.0.1:0" // Random port
	natInfo, err := client.DiscoverNATInfo(ctx, localAddr)

	// Verify results
	require.NoError(t, err, "STUN discovery should succeed")
	assert.NotNil(t, natInfo, "NAT info should not be nil")
	assert.Equal(t, "127.0.0.1", natInfo.PublicIP, "Public IP should match")
	assert.True(t, natInfo.PublicPort > 0, "Public port should be set")
	assert.True(t, natInfo.Mapped, "Should be marked as mapped")
}

// TestIntegration_STUN_MultipleServers tests STUN with multiple servers
func TestIntegration_STUN_MultipleServers(t *testing.T) {
	// Start multiple mock STUN servers
	stunServer1 := NewMockSTUNServer(t, 19303)
	stunServer1.Start(t)
	defer stunServer1.Stop()

	stunServer2 := NewMockSTUNServer(t, 19304)
	stunServer2.Start(t)
	defer stunServer2.Stop()

	// Give servers time to start
	time.Sleep(100 * time.Millisecond)

	// Create STUN client with multiple servers
	logger := zap.NewNop()
	client := NewSTUNClient([]string{
		stunServer1.Address(),
		stunServer2.Address(),
	}, logger)

	// Test NAT discovery
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	localAddr := "127.0.0.1:0"
	natInfo, err := client.DiscoverNATInfo(ctx, localAddr)

	// Verify results
	require.NoError(t, err, "STUN discovery should succeed with any server")
	assert.NotNil(t, natInfo, "NAT info should not be nil")
}

// TestIntegration_STUN_Timeout tests STUN timeout behavior
func TestIntegration_STUN_Timeout(t *testing.T) {
	// Create STUN client with non-existent server
	logger := zap.NewNop()
	client := NewSTUNClient([]string{"127.0.0.1:9999"}, logger)
	client.timeout = 500 * time.Millisecond

	// Test NAT discovery with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	localAddr := "127.0.0.1:0"
	_, err := client.DiscoverNATInfo(ctx, localAddr)

	// Should fail
	assert.Error(t, err, "STUN discovery should timeout")
	assert.Contains(t, err.Error(), "all STUN servers failed", "Error should mention server failure")
}

// TestIntegration_NAT_Traversal_EndToEnd tests full NAT traversal flow
func TestIntegration_NAT_Traversal_EndToEnd(t *testing.T) {
	// Start mock servers
	stunServer := NewMockSTUNServer(t, 19305)
	stunServer.Start(t)
	defer stunServer.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create NAT traversal config
	config := NATConfig{
		STUNServers: []string{stunServer.Address()},
		TURNServers: []TURNServer{},
	}

	// Create NAT traversal handler
	logger := zap.NewNop()
	nt := NewNATTraversal(config, logger)

	// Start NAT traversal
	localAddr := "127.0.0.1:0"
	err := nt.Start(localAddr)
	require.NoError(t, err, "NAT traversal start should succeed")
	defer nt.Stop()

	// Give STUN discovery time to complete
	time.Sleep(200 * time.Millisecond)

	// Get NAT info
	natInfo := nt.GetNATInfo()
	require.NotNil(t, natInfo, "NAT info should be available")
	assert.Equal(t, "127.0.0.1", natInfo.PublicIP, "Public IP should be discovered")

	// Get public endpoint
	endpoint, err := nt.GetPublicEndpoint()
	require.NoError(t, err, "Get public endpoint should succeed")
	assert.Contains(t, endpoint, "127.0.0.1", "Endpoint should contain public IP")
}

// TestIntegration_NATTraversal_StrategySelection tests strategy selection
func TestIntegration_NATTraversal_StrategySelection(t *testing.T) {
	tests := []struct {
		name          string
		localNATType  NATType
		peerNATType   NATType
		expectedStrat NATTraversalStrategy
	}{
		{
			name:          "both no NAT - direct",
			localNATType:  NATTypeNone,
			peerNATType:   NATTypeNone,
			expectedStrat: StrategyDirect,
		},
		{
			name:          "symmetric NAT - relay",
			localNATType:  NATTypeSymmetric,
			peerNATType:   NATTypeFullCone,
			expectedStrat: StrategyRelay,
		},
		{
			name:          "full cone - hole punch",
			localNATType:  NATTypeFullCone,
			peerNATType:   NATTypePortRestrictedCone,
			expectedStrat: StrategyHolePunch,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create NAT traversal handler
			config := NATConfig{
				STUNServers: []string{},
				TURNServers: []TURNServer{},
			}
			logger := zap.NewNop()
			nt := NewNATTraversal(config, logger)

			// Test strategy determination
			strategy := nt.determineStrategy(tt.localNATType, tt.peerNATType)
			assert.Equal(t, tt.expectedStrat, strategy, "Strategy should match expected")
		})
	}
}

// TestIntegration_Concurrent_STUN_Requests tests concurrent STUN requests
func TestIntegration_Concurrent_STUN_Requests(t *testing.T) {
	// Start mock STUN server
	stunServer := NewMockSTUNServer(t, 19306)
	stunServer.Start(t)
	defer stunServer.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create STUN client
	logger := zap.NewNop()
	client := NewSTUNClient([]string{stunServer.Address()}, logger)

	// Run concurrent discovery requests
	const numRequests = 10
	var wg sync.WaitGroup
	errors := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()

			localAddr := fmt.Sprintf("127.0.0.1:%d", 30000+id)
			_, err := client.DiscoverNATInfo(ctx, localAddr)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent request failed: %v", err)
	}
}

// Benchmark tests

// BenchmarkIntegration_STUN_Discovery benchmarks STUN discovery
func BenchmarkIntegration_STUN_Discovery(b *testing.B) {
	// Start mock STUN server
	stunServer := NewMockSTUNServer(&testing.T{}, 19307)
	stunServer.Start(&testing.T{})
	defer stunServer.Stop()

	time.Sleep(100 * time.Millisecond)

	// Create STUN client
	logger := zap.NewNop()
	client := NewSTUNClient([]string{stunServer.Address()}, logger)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		localAddr := fmt.Sprintf("127.0.0.1:%d", 40000+(i%1000))
		_, _ = client.DiscoverNATInfo(ctx, localAddr)
		cancel()
	}
}

// Helper functions for TURN message creation (simplified)
func createTURNAllocateRequest() *stun.Message {
	msg := stun.MustBuild(stun.TransactionID, stun.NewType(stun.MethodAllocate, stun.ClassRequest))
	return msg
}

func decodeTURNResponse(data []byte) (*net.UDPAddr, error) {
	msg := &stun.Message{Raw: data}
	if err := msg.Decode(); err != nil {
		return nil, err
	}

	var xorAddr stun.XORMappedAddress
	if err := xorAddr.GetFrom(msg); err != nil {
		return nil, err
	}

	return &net.UDPAddr{
		IP:   xorAddr.IP,
		Port: xorAddr.Port,
	}, nil
}
