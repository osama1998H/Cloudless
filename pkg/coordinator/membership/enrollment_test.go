package membership

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/mtls"
	"github.com/cloudless/cloudless/pkg/raft"
	"go.uber.org/zap"
)

// TestManager_EnrollNode_Success verifies CLD-REQ-001 enrollment flow.
// CLD-REQ-001: Nodes must enroll using bootstrap credentials and complete mTLS handshake.
// This test ensures successful enrollment with valid tokens.
func TestManager_EnrollNode_Success(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	// Create certificate manager
	ca, err := mtls.NewCA(mtls.CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Cloudless Test",
		Country:      "US",
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("Failed to create CA: %v", err)
	}

	certManager, err := mtls.NewCertificateManager(mtls.CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("Failed to create cert manager: %v", err)
	}

	// Create token manager
	tokenManager, err := mtls.NewTokenManager(mtls.TokenManagerConfig{
		SigningKey: []byte("test-signing-key-32-bytes-long!!"),
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("Failed to create token manager: %v", err)
	}

	// Generate enrollment token
	token, err := tokenManager.GenerateToken("node-123", "test-node", "us-west", "us-west-1a", 24*time.Hour, 1)
	if err != nil {
		t.Fatalf("Failed to generate token: %v", err)
	}

	// Create RAFT store
	raftConfig := &raft.Config{
		RaftID:    "coordinator-1",
		RaftBind:  "127.0.0.1:0",
		RaftDir:   filepath.Join(tempDir, "raft"),
		Bootstrap: true,
		Logger:    logger,
	}
	raftStore, err := raft.NewStore(raftConfig)
	if err != nil {
		t.Fatalf("Failed to create RAFT store: %v", err)
	}
	defer raftStore.Close()

	// Wait for leader election
	time.Sleep(100 * time.Millisecond)

	// Create membership manager
	manager, err := NewManager(ManagerConfig{
		Store:        raftStore,
		TokenManager: tokenManager,
		CertManager:  certManager,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	// Enrollment request
	req := &api.EnrollNodeRequest{
		Token:    token.Token,
		NodeName: "test-node",
		Region:   "us-west",
		Zone:     "us-west-1a",
		Capabilities: &api.NodeCapabilities{
			ContainerRuntimes: []string{"containerd"},
			SupportsX86:       true,
		},
		Capacity: &api.ResourceCapacity{
			CpuMillicores: 4000,
			MemoryBytes:   8589934592,   // 8GB
			StorageBytes:  107374182400, // 100GB
		},
		Labels: map[string]string{
			"env": "test",
		},
	}

	// Enroll node
	ctx := context.Background()
	resp, err := manager.EnrollNode(ctx, req)
	if err != nil {
		t.Fatalf("Failed to enroll node: %v", err)
	}

	// Verify response
	if resp.NodeId != "node-123" {
		t.Errorf("Expected node ID node-123, got %s", resp.NodeId)
	}
	if len(resp.Certificate) == 0 {
		t.Error("Expected non-empty certificate")
	}
	if len(resp.CaCertificate) == 0 {
		t.Error("Expected non-empty CA certificate")
	}
	if resp.HeartbeatInterval == nil {
		t.Error("Expected heartbeat interval to be set")
	} else if resp.HeartbeatInterval.AsDuration() != DefaultHeartbeatInterval {
		t.Errorf("Expected heartbeat interval %v, got %v", DefaultHeartbeatInterval, resp.HeartbeatInterval.AsDuration())
	}

	// Verify node is stored in manager
	manager.mu.RLock()
	node, exists := manager.nodes["node-123"]
	manager.mu.RUnlock()

	if !exists {
		t.Fatal("Node should exist in manager")
	}

	if node.State != StateEnrolling {
		t.Errorf("Expected state %s, got %s", StateEnrolling, node.State)
	}
	if node.Name != "test-node" {
		t.Errorf("Expected name test-node, got %s", node.Name)
	}
	if node.Region != "us-west" {
		t.Errorf("Expected region us-west, got %s", node.Region)
	}
	if node.Zone != "us-west-1a" {
		t.Errorf("Expected zone us-west-1a, got %s", node.Zone)
	}
	if node.ReliabilityScore != 1.0 {
		t.Errorf("Expected initial reliability score 1.0, got %f", node.ReliabilityScore)
	}

	// Verify capabilities
	if !node.Capabilities.SupportsX86 {
		t.Error("Expected SupportsX86 to be true")
	}
	if len(node.Capabilities.ContainerRuntimes) != 1 || node.Capabilities.ContainerRuntimes[0] != "containerd" {
		t.Errorf("Expected ContainerRuntimes [containerd], got %v", node.Capabilities.ContainerRuntimes)
	}

	// Verify capacity
	if node.Capacity.CPUMillicores != 4000 {
		t.Errorf("Expected CPU 4000m, got %d", node.Capacity.CPUMillicores)
	}
	if node.Capacity.MemoryBytes != 8589934592 {
		t.Errorf("Expected Memory 8GB, got %d", node.Capacity.MemoryBytes)
	}

	// Verify labels
	if node.Labels["env"] != "test" {
		t.Errorf("Expected label env=test, got %s", node.Labels["env"])
	}

	// Verify token was marked as used
	tokenInfo, err := tokenManager.GetTokenInfo(token.ID)
	if err != nil {
		t.Fatalf("Failed to get token info: %v", err)
	}
	if tokenInfo.UseCount != 1 {
		t.Errorf("Expected token use count 1, got %d", tokenInfo.UseCount)
	}
}

// TestManager_EnrollNode_InvalidToken verifies CLD-REQ-001 token validation.
// CLD-REQ-001: Invalid tokens must be rejected to ensure secure enrollment.
func TestManager_EnrollNode_InvalidToken(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	// Create certificate manager
	ca, err := mtls.NewCA(mtls.CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Cloudless Test",
		Country:      "US",
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("Failed to create CA: %v", err)
	}

	certManager, err := mtls.NewCertificateManager(mtls.CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("Failed to create cert manager: %v", err)
	}

	// Create token manager
	tokenManager, err := mtls.NewTokenManager(mtls.TokenManagerConfig{
		SigningKey: []byte("test-signing-key-32-bytes-long!!"),
		Logger:     logger,
	})
	if err != nil {
		t.Fatalf("Failed to create token manager: %v", err)
	}

	// Create RAFT store
	raftConfig := &raft.Config{
		RaftID:    "coordinator-1",
		RaftBind:  "127.0.0.1:0",
		RaftDir:   filepath.Join(tempDir, "raft"),
		Bootstrap: true,
		Logger:    logger,
	}
	raftStore, err := raft.NewStore(raftConfig)
	if err != nil {
		t.Fatalf("Failed to create RAFT store: %v", err)
	}
	defer raftStore.Close()

	time.Sleep(100 * time.Millisecond)

	// Create membership manager
	manager, err := NewManager(ManagerConfig{
		Store:        raftStore,
		TokenManager: tokenManager,
		CertManager:  certManager,
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("Failed to create manager: %v", err)
	}

	tests := []struct {
		name          string
		token         string
		expectedError string
	}{
		{
			name:          "invalid token format",
			token:         "not-a-valid-jwt-token",
			expectedError: "invalid token",
		},
		{
			name:          "empty token",
			token:         "",
			expectedError: "invalid token",
		},
		{
			name:          "malformed JWT",
			token:         "eyJhbGciOiJIUzI1NiJ9.invalid.signature",
			expectedError: "invalid token",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &api.EnrollNodeRequest{
				Token:    tt.token,
				NodeName: "test-node",
				Region:   "us-west",
				Zone:     "us-west-1a",
			}

			ctx := context.Background()
			_, err := manager.EnrollNode(ctx, req)
			if err == nil {
				t.Error("Expected enrollment to fail with invalid token")
				return
			}

			if !strings.Contains(err.Error(), tt.expectedError) {
				t.Errorf("Expected error containing '%s', got '%s'", tt.expectedError, err.Error())
			}
		})
	}
}

// TestManager_EnrollNode_DuplicatePrevention verifies CLD-REQ-001 duplicate node handling.
// CLD-REQ-001: Duplicate enrollments must be prevented unless node is offline/failed.
func TestManager_EnrollNode_DuplicatePrevention(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	// Setup managers
	ca, _ := mtls.NewCA(mtls.CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Cloudless Test",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := mtls.NewCertificateManager(mtls.CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	tokenManager, _ := mtls.NewTokenManager(mtls.TokenManagerConfig{
		SigningKey: []byte("test-signing-key-32-bytes-long!!"),
		Logger:     logger,
	})

	raftStore, _ := raft.NewStore(&raft.Config{
		RaftID:    "coordinator-1",
		RaftBind:  "127.0.0.1:0",
		RaftDir:   filepath.Join(tempDir, "raft"),
		Bootstrap: true,
		Logger:    logger,
	})
	defer raftStore.Close()
	time.Sleep(100 * time.Millisecond)

	manager, _ := NewManager(ManagerConfig{
		Store:        raftStore,
		TokenManager: tokenManager,
		CertManager:  certManager,
		Logger:       logger,
	})

	// Generate first token
	token1, _ := tokenManager.GenerateToken("node-123", "test-node", "us-west", "us-west-1a", 24*time.Hour, 1)

	// First enrollment
	req1 := &api.EnrollNodeRequest{
		Token:    token1.Token,
		NodeName: "test-node",
		Region:   "us-west",
		Zone:     "us-west-1a",
	}

	ctx := context.Background()
	_, err := manager.EnrollNode(ctx, req1)
	if err != nil {
		t.Fatalf("First enrollment should succeed: %v", err)
	}

	// Generate second token for same node
	token2, _ := tokenManager.GenerateToken("node-123", "test-node", "us-west", "us-west-1a", 24*time.Hour, 1)

	// Second enrollment should fail
	req2 := &api.EnrollNodeRequest{
		Token:    token2.Token,
		NodeName: "test-node",
		Region:   "us-west",
		Zone:     "us-west-1a",
	}

	_, err = manager.EnrollNode(ctx, req2)
	if err == nil {
		t.Error("Second enrollment should fail for already enrolled node")
	}
	if !strings.Contains(err.Error(), "already enrolled") {
		t.Errorf("Expected 'already enrolled' error, got '%s'", err.Error())
	}
}

// TestManager_EnrollNode_ReenrollOfflineNode verifies CLD-REQ-001 re-enrollment.
// CLD-REQ-001: Offline/failed nodes should be allowed to re-enroll.
func TestManager_EnrollNode_ReenrollOfflineNode(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	// Setup managers
	ca, _ := mtls.NewCA(mtls.CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Cloudless Test",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := mtls.NewCertificateManager(mtls.CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	tokenManager, _ := mtls.NewTokenManager(mtls.TokenManagerConfig{
		SigningKey: []byte("test-signing-key-32-bytes-long!!"),
		Logger:     logger,
	})

	raftStore, _ := raft.NewStore(&raft.Config{
		RaftID:    "coordinator-1",
		RaftBind:  "127.0.0.1:0",
		RaftDir:   filepath.Join(tempDir, "raft"),
		Bootstrap: true,
		Logger:    logger,
	})
	defer raftStore.Close()
	time.Sleep(100 * time.Millisecond)

	manager, _ := NewManager(ManagerConfig{
		Store:        raftStore,
		TokenManager: tokenManager,
		CertManager:  certManager,
		Logger:       logger,
	})

	tests := []struct {
		name          string
		initialState  string
		shouldSucceed bool
	}{
		{
			name:          "re-enroll offline node",
			initialState:  StateOffline,
			shouldSucceed: true,
		},
		{
			name:          "re-enroll failed node",
			initialState:  StateFailed,
			shouldSucceed: true,
		},
		{
			name:          "cannot re-enroll ready node",
			initialState:  StateReady,
			shouldSucceed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodeID := "node-" + tt.initialState

			// Create node in initial state
			manager.mu.Lock()
			manager.nodes[nodeID] = &NodeInfo{
				ID:    nodeID,
				Name:  "test-node",
				State: tt.initialState,
			}
			manager.mu.Unlock()

			// Generate token
			token, _ := tokenManager.GenerateToken(nodeID, "test-node", "us-west", "us-west-1a", 24*time.Hour, 1)

			// Try to re-enroll
			req := &api.EnrollNodeRequest{
				Token:    token.Token,
				NodeName: "test-node",
				Region:   "us-west",
				Zone:     "us-west-1a",
			}

			ctx := context.Background()
			_, err := manager.EnrollNode(ctx, req)

			if tt.shouldSucceed {
				if err != nil {
					t.Errorf("Re-enrollment should succeed for %s node: %v", tt.initialState, err)
				}
			} else {
				if err == nil {
					t.Errorf("Re-enrollment should fail for %s node", tt.initialState)
				}
			}
		})
	}
}

// TestManager_EnrollNode_CertificateIssuance verifies CLD-REQ-001 mTLS certificate generation.
// CLD-REQ-001: Enrollment must complete mTLS handshake by issuing node certificates.
func TestManager_EnrollNode_CertificateIssuance(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	// Setup managers
	ca, _ := mtls.NewCA(mtls.CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Cloudless Test",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := mtls.NewCertificateManager(mtls.CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	tokenManager, _ := mtls.NewTokenManager(mtls.TokenManagerConfig{
		SigningKey: []byte("test-signing-key-32-bytes-long!!"),
		Logger:     logger,
	})

	raftStore, _ := raft.NewStore(&raft.Config{
		RaftID:    "coordinator-1",
		RaftBind:  "127.0.0.1:0",
		RaftDir:   filepath.Join(tempDir, "raft"),
		Bootstrap: true,
		Logger:    logger,
	})
	defer raftStore.Close()
	time.Sleep(100 * time.Millisecond)

	manager, _ := NewManager(ManagerConfig{
		Store:        raftStore,
		TokenManager: tokenManager,
		CertManager:  certManager,
		Logger:       logger,
	})

	// Generate token
	token, _ := tokenManager.GenerateToken("node-123", "test-node", "us-west", "us-west-1a", 24*time.Hour, 1)

	// Enrollment request
	req := &api.EnrollNodeRequest{
		Token:    token.Token,
		NodeName: "test-node",
		Region:   "us-west",
		Zone:     "us-west-1a",
	}

	ctx := context.Background()
	resp, err := manager.EnrollNode(ctx, req)
	if err != nil {
		t.Fatalf("Enrollment failed: %v", err)
	}

	// Verify certificate is PEM encoded
	if !strings.HasPrefix(string(resp.Certificate), "-----BEGIN CERTIFICATE-----") {
		t.Error("Certificate should be PEM encoded")
	}
	if !strings.HasSuffix(strings.TrimSpace(string(resp.Certificate)), "-----END CERTIFICATE-----") {
		t.Error("Certificate should have PEM footer")
	}

	// Verify CA certificate
	if !strings.HasPrefix(string(resp.CaCertificate), "-----BEGIN CERTIFICATE-----") {
		t.Error("CA certificate should be PEM encoded")
	}

	// Verify certificate is stored in node info
	manager.mu.RLock()
	node := manager.nodes["node-123"]
	manager.mu.RUnlock()

	if node.Certificate == nil {
		t.Fatal("Node certificate should be stored")
	}
	if len(node.Certificate.CertPEM) == 0 {
		t.Error("Certificate PEM should not be empty")
	}
	if len(node.Certificate.KeyPEM) == 0 {
		t.Error("Certificate private key should not be empty")
	}
}

// TestManager_EnrollNode_TokenUsageTracking verifies CLD-REQ-001 token consumption.
// CLD-REQ-001: Enrollment tokens must be marked as used to prevent replay attacks.
func TestManager_EnrollNode_TokenUsageTracking(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	// Setup managers
	ca, _ := mtls.NewCA(mtls.CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Cloudless Test",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := mtls.NewCertificateManager(mtls.CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	tokenManager, _ := mtls.NewTokenManager(mtls.TokenManagerConfig{
		SigningKey: []byte("test-signing-key-32-bytes-long!!"),
		Logger:     logger,
	})

	raftStore, _ := raft.NewStore(&raft.Config{
		RaftID:    "coordinator-1",
		RaftBind:  "127.0.0.1:0",
		RaftDir:   filepath.Join(tempDir, "raft"),
		Bootstrap: true,
		Logger:    logger,
	})
	defer raftStore.Close()
	time.Sleep(100 * time.Millisecond)

	manager, _ := NewManager(ManagerConfig{
		Store:        raftStore,
		TokenManager: tokenManager,
		CertManager:  certManager,
		Logger:       logger,
	})

	// Generate multi-use token
	token, _ := tokenManager.GenerateToken("", "test-node", "us-west", "us-west-1a", 24*time.Hour, 3)

	// First enrollment
	req1 := &api.EnrollNodeRequest{
		Token:    token.Token,
		NodeName: "test-node-1",
		Region:   "us-west",
		Zone:     "us-west-1a",
	}

	ctx := context.Background()
	_, err := manager.EnrollNode(ctx, req1)
	if err != nil {
		t.Fatalf("First enrollment failed: %v", err)
	}

	// Verify token use count incremented
	tokenInfo, _ := tokenManager.GetTokenInfo(token.ID)
	if tokenInfo.UseCount != 1 {
		t.Errorf("Expected use count 1 after first enrollment, got %d", tokenInfo.UseCount)
	}

	// Second enrollment with same token
	req2 := &api.EnrollNodeRequest{
		Token:    token.Token,
		NodeName: "test-node-2",
		Region:   "us-west",
		Zone:     "us-west-1a",
	}

	_, err = manager.EnrollNode(ctx, req2)
	if err != nil {
		t.Fatalf("Second enrollment failed: %v", err)
	}

	// Verify token use count incremented again
	tokenInfo, _ = tokenManager.GetTokenInfo(token.ID)
	if tokenInfo.UseCount != 2 {
		t.Errorf("Expected use count 2 after second enrollment, got %d", tokenInfo.UseCount)
	}
}

// TestManager_EnrollNode_GenerateNodeID verifies CLD-REQ-001 node ID generation.
// CLD-REQ-001: Nodes without IDs in token should receive generated IDs.
func TestManager_EnrollNode_GenerateNodeID(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	// Setup managers
	ca, _ := mtls.NewCA(mtls.CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Cloudless Test",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := mtls.NewCertificateManager(mtls.CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	tokenManager, _ := mtls.NewTokenManager(mtls.TokenManagerConfig{
		SigningKey: []byte("test-signing-key-32-bytes-long!!"),
		Logger:     logger,
	})

	raftStore, _ := raft.NewStore(&raft.Config{
		RaftID:    "coordinator-1",
		RaftBind:  "127.0.0.1:0",
		RaftDir:   filepath.Join(tempDir, "raft"),
		Bootstrap: true,
		Logger:    logger,
	})
	defer raftStore.Close()
	time.Sleep(100 * time.Millisecond)

	manager, _ := NewManager(ManagerConfig{
		Store:        raftStore,
		TokenManager: tokenManager,
		CertManager:  certManager,
		Logger:       logger,
	})

	// Generate token WITHOUT node ID (empty string)
	token, _ := tokenManager.GenerateToken("", "test-node", "us-west", "us-west-1a", 24*time.Hour, 1)

	req := &api.EnrollNodeRequest{
		Token:    token.Token,
		NodeName: "test-node",
		Region:   "us-west",
		Zone:     "us-west-1a",
	}

	ctx := context.Background()
	resp, err := manager.EnrollNode(ctx, req)
	if err != nil {
		t.Fatalf("Enrollment failed: %v", err)
	}

	// Verify node ID was generated
	if resp.NodeId == "" {
		t.Error("Node ID should be generated when not in token")
	}

	// Verify generated ID follows pattern "node-{name}-{timestamp}"
	if !strings.HasPrefix(resp.NodeId, "node-test-node-") {
		t.Errorf("Expected generated ID to start with 'node-test-node-', got %s", resp.NodeId)
	}
}

// TestManager_EnrollNode_DefaultValues verifies CLD-REQ-001 default value handling.
// CLD-REQ-001: Enrollment should apply sensible defaults for optional fields.
func TestManager_EnrollNode_DefaultValues(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	// Setup managers
	ca, _ := mtls.NewCA(mtls.CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Cloudless Test",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := mtls.NewCertificateManager(mtls.CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	tokenManager, _ := mtls.NewTokenManager(mtls.TokenManagerConfig{
		SigningKey: []byte("test-signing-key-32-bytes-long!!"),
		Logger:     logger,
	})

	raftStore, _ := raft.NewStore(&raft.Config{
		RaftID:    "coordinator-1",
		RaftBind:  "127.0.0.1:0",
		RaftDir:   filepath.Join(tempDir, "raft"),
		Bootstrap: true,
		Logger:    logger,
	})
	defer raftStore.Close()
	time.Sleep(100 * time.Millisecond)

	manager, _ := NewManager(ManagerConfig{
		Store:        raftStore,
		TokenManager: tokenManager,
		CertManager:  certManager,
		Logger:       logger,
	})

	token, _ := tokenManager.GenerateToken("node-123", "test-node", "us-west", "us-west-1a", 24*time.Hour, 1)

	// Minimal enrollment request (no capabilities, capacity, labels)
	req := &api.EnrollNodeRequest{
		Token:    token.Token,
		NodeName: "test-node",
		Region:   "us-west",
		Zone:     "us-west-1a",
		// Capabilities, Capacity, Labels are nil
	}

	ctx := context.Background()
	resp, err := manager.EnrollNode(ctx, req)
	if err != nil {
		t.Fatalf("Enrollment failed: %v", err)
	}

	// Verify response has defaults
	if resp.HeartbeatInterval.AsDuration() != DefaultHeartbeatInterval {
		t.Errorf("Expected default heartbeat interval %v, got %v", DefaultHeartbeatInterval, resp.HeartbeatInterval.AsDuration())
	}

	// Verify node has defaults
	manager.mu.RLock()
	node := manager.nodes["node-123"]
	manager.mu.RUnlock()

	if node.State != StateEnrolling {
		t.Errorf("Expected state %s, got %s", StateEnrolling, node.State)
	}
	if node.ReliabilityScore != 1.0 {
		t.Errorf("Expected initial reliability score 1.0, got %f", node.ReliabilityScore)
	}
	if node.HeartbeatInterval != DefaultHeartbeatInterval {
		t.Errorf("Expected heartbeat interval %v, got %v", DefaultHeartbeatInterval, node.HeartbeatInterval)
	}

	// Labels can be nil if not provided in request (no initialization in implementation)
	// This is acceptable behavior - labels are optional
}
