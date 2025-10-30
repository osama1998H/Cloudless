//go:build integration

// CLD-REQ-063: Secrets Management Integration Tests
// These tests require a running coordinator with secrets enabled
//
// Run with: go test -tags=integration ./test/integration/secrets_test.go -v
//
// Prerequisites:
//   - Coordinator running on localhost:8080 (or override with COORDINATOR_ADDR env var)
//   - Secrets enabled (CLOUDLESS_SECRETS_ENABLED=true)
//   - TLS disabled for testing (CLOUDLESS_TLS_ENABLED=false)

package integration

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/osama1998H/Cloudless/pkg/api"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
)

var (
	coordinatorAddr string
	testClient      api.SecretsServiceClient
)

func TestMain(m *testing.M) {
	// Get coordinator address from environment or use default
	coordinatorAddr = os.Getenv("COORDINATOR_ADDR")
	if coordinatorAddr == "" {
		coordinatorAddr = "localhost:8080"
	}

	// Connect to coordinator
	conn, err := grpc.Dial(coordinatorAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to connect to coordinator at %s: %v\n", coordinatorAddr, err)
		os.Exit(1)
	}
	defer conn.Close()

	testClient = api.NewSecretsServiceClient(conn)

	// Run tests
	exitCode := m.Run()
	os.Exit(exitCode)
}

// TestSecretLifecycle tests the full lifecycle: create → access → update → delete
func TestSecretLifecycle(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("lifecycle-secret-%d", time.Now().Unix())

	// Step 1: Create secret
	createResp, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace: namespace,
		Name:      secretName,
		Data: map[string][]byte{
			"API_KEY":    []byte("test-key-123"),
			"API_SECRET": []byte("test-secret-456"),
		},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600, // 1 hour
		Immutable:  false,
	})
	require.NoError(t, err, "CreateSecret should succeed")
	assert.Equal(t, namespace, createResp.Metadata.Namespace)
	assert.Equal(t, secretName, createResp.Metadata.Name)
	assert.Equal(t, uint64(1), createResp.Metadata.Version)
	assert.Equal(t, []string{"test-service"}, createResp.Metadata.Audiences)

	// Step 2: Generate access token
	tokenResp, err := testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  namespace,
		Name:       secretName,
		Audience:   "test-service",
		TtlSeconds: 1800, // 30 minutes
		MaxUses:    10,
	})
	require.NoError(t, err, "GenerateAccessToken should succeed")
	assert.NotEmpty(t, tokenResp.Token.Token)
	assert.NotEmpty(t, tokenResp.Token.TokenId)
	assert.Equal(t, "test-service", tokenResp.Token.Audience)
	assert.Equal(t, int32(10), tokenResp.Token.UsesRemaining)

	accessToken := tokenResp.Token.Token

	// Step 3: Retrieve secret using token
	getResp, err := testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: accessToken,
	})
	require.NoError(t, err, "GetSecret should succeed")
	assert.Equal(t, secretName, getResp.Metadata.Name)
	assert.Equal(t, uint64(1), getResp.Metadata.Version)
	assert.Equal(t, []byte("test-key-123"), getResp.Data["API_KEY"])
	assert.Equal(t, []byte("test-secret-456"), getResp.Data["API_SECRET"])

	// Step 4: Update secret (version should increment)
	updateResp, err := testClient.UpdateSecret(ctx, &api.UpdateSecretRequest{
		Namespace: namespace,
		Name:      secretName,
		Data: map[string][]byte{
			"API_KEY":    []byte("test-key-123"),
			"API_SECRET": []byte("rotated-secret-789"),
		},
	})
	require.NoError(t, err, "UpdateSecret should succeed")
	assert.Equal(t, uint64(2), updateResp.Metadata.Version, "Version should increment")

	// Step 5: Retrieve updated secret
	getResp2, err := testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: accessToken, // Same token still valid
	})
	require.NoError(t, err, "GetSecret should succeed after update")
	assert.Equal(t, uint64(2), getResp2.Metadata.Version)
	assert.Equal(t, []byte("rotated-secret-789"), getResp2.Data["API_SECRET"])

	// Step 6: List secrets
	listResp, err := testClient.ListSecrets(ctx, &api.ListSecretsRequest{
		Namespace: namespace,
		Limit:     100,
	})
	require.NoError(t, err, "ListSecrets should succeed")
	found := false
	for _, s := range listResp.Secrets {
		if s.Name == secretName {
			found = true
			assert.Equal(t, uint64(2), s.Version)
			break
		}
	}
	assert.True(t, found, "Secret should appear in list")

	// Step 7: Delete secret
	_, err = testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: namespace,
		Name:      secretName,
	})
	require.NoError(t, err, "DeleteSecret should succeed")

	// Step 8: Verify secret no longer exists
	_, err = testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: accessToken,
	})
	require.Error(t, err, "GetSecret should fail after deletion")
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestTokenExhaustion verifies that tokens with max_uses are properly enforced
func TestTokenExhaustion(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("token-exhaust-%d", time.Now().Unix())

	// Create secret
	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  namespace,
		Name:       secretName,
		Data:       map[string][]byte{"key": []byte("value")},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.NoError(t, err)

	// Generate token with max_uses = 3
	tokenResp, err := testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  namespace,
		Name:       secretName,
		Audience:   "test-service",
		TtlSeconds: 1800,
		MaxUses:    3,
	})
	require.NoError(t, err)
	token := tokenResp.Token.Token

	// Use token 3 times (should succeed)
	for i := 1; i <= 3; i++ {
		_, err := testClient.GetSecret(ctx, &api.GetSecretRequest{
			Namespace:   namespace,
			Name:        secretName,
			Audience:    "test-service",
			AccessToken: token,
		})
		require.NoError(t, err, "Access %d should succeed", i)
	}

	// 4th access should fail (exhausted)
	_, err = testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: token,
	})
	require.Error(t, err, "4th access should fail (token exhausted)")
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// Cleanup
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: namespace,
		Name:      secretName,
	})
}

// TestSecretExpiration verifies that secrets with TTL are auto-deleted
func TestSecretExpiration(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("expiring-secret-%d", time.Now().Unix())

	// Create secret with 5-second TTL
	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  namespace,
		Name:       secretName,
		Data:       map[string][]byte{"key": []byte("value")},
		Audiences:  []string{"test-service"},
		TtlSeconds: 5, // 5 seconds
		Immutable:  false,
	})
	require.NoError(t, err)

	// Generate token
	tokenResp, err := testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  namespace,
		Name:       secretName,
		Audience:   "test-service",
		TtlSeconds: 60, // Token TTL longer than secret TTL
		MaxUses:    100,
	})
	require.NoError(t, err)
	token := tokenResp.Token.Token

	// Access should succeed immediately
	_, err = testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: token,
	})
	require.NoError(t, err, "Access should succeed before expiration")

	// Wait for expiration (5s + 2s buffer)
	t.Log("Waiting for secret to expire (7 seconds)...")
	time.Sleep(7 * time.Second)

	// Access should now fail
	_, err = testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: token,
	})
	require.Error(t, err, "Access should fail after expiration")
	assert.Equal(t, codes.NotFound, status.Code(err))
}

// TestImmutability verifies that immutable secrets cannot be updated
func TestImmutability(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("immutable-secret-%d", time.Now().Unix())

	// Create immutable secret
	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace: namespace,
		Name:      secretName,
		Data: map[string][]byte{
			"tls.crt": []byte("-----BEGIN CERTIFICATE-----\n..."),
			"tls.key": []byte("-----BEGIN PRIVATE KEY-----\n..."),
		},
		Audiences:  []string{"web-server"},
		TtlSeconds: 0, // No expiration
		Immutable:  true,
	})
	require.NoError(t, err)

	// Attempt to update should fail
	_, err = testClient.UpdateSecret(ctx, &api.UpdateSecretRequest{
		Namespace: namespace,
		Name:      secretName,
		Data: map[string][]byte{
			"tls.crt": []byte("modified"),
		},
	})
	require.Error(t, err, "Update should fail for immutable secret")
	assert.Equal(t, codes.FailedPrecondition, status.Code(err))

	// Cleanup
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: namespace,
		Name:      secretName,
	})
}

// TestAudienceRestriction verifies that audience-based access control works
func TestAudienceRestriction(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("audience-secret-%d", time.Now().Unix())

	// Create secret allowing only "backend-api" and "worker"
	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  namespace,
		Name:       secretName,
		Data:       map[string][]byte{"key": []byte("value")},
		Audiences:  []string{"backend-api", "worker"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.NoError(t, err)

	// Generate token for authorized audience (backend-api)
	tokenResp1, err := testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  namespace,
		Name:       secretName,
		Audience:   "backend-api",
		TtlSeconds: 1800,
		MaxUses:    10,
	})
	require.NoError(t, err, "Token generation for authorized audience should succeed")

	// Access with authorized token should succeed
	_, err = testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "backend-api",
		AccessToken: tokenResp1.Token.Token,
	})
	require.NoError(t, err, "Access with authorized audience should succeed")

	// Attempt to generate token for unauthorized audience
	_, err = testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  namespace,
		Name:       secretName,
		Audience:   "frontend-app", // NOT in allowed audiences
		TtlSeconds: 1800,
		MaxUses:    10,
	})
	require.Error(t, err, "Token generation for unauthorized audience should fail")
	assert.Equal(t, codes.PermissionDenied, status.Code(err))

	// Cleanup
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: namespace,
		Name:      secretName,
	})
}

// TestConcurrentAccess verifies that multiple concurrent accesses work correctly
func TestConcurrentAccess(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("concurrent-secret-%d", time.Now().Unix())

	// Create secret
	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  namespace,
		Name:       secretName,
		Data:       map[string][]byte{"key": []byte("value")},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.NoError(t, err)

	// Generate token with sufficient max_uses
	tokenResp, err := testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  namespace,
		Name:       secretName,
		Audience:   "test-service",
		TtlSeconds: 1800,
		MaxUses:    100,
	})
	require.NoError(t, err)
	token := tokenResp.Token.Token

	// Spawn 20 goroutines accessing the secret concurrently
	concurrency := 20
	var wg sync.WaitGroup
	errors := make(chan error, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := testClient.GetSecret(ctx, &api.GetSecretRequest{
				Namespace:   namespace,
				Name:        secretName,
				Audience:    "test-service",
				AccessToken: token,
			})
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: %w", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent access error: %v", err)
		errorCount++
	}
	assert.Equal(t, 0, errorCount, "All concurrent accesses should succeed")

	// Cleanup
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: namespace,
		Name:      secretName,
	})
}

// TestAuditLog verifies that audit logs are created for all operations
func TestAuditLog(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("audit-secret-%d", time.Now().Unix())

	// Create secret
	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  namespace,
		Name:       secretName,
		Data:       map[string][]byte{"key": []byte("value")},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.NoError(t, err)

	// Generate token
	tokenResp, err := testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  namespace,
		Name:       secretName,
		Audience:   "test-service",
		TtlSeconds: 1800,
		MaxUses:    10,
	})
	require.NoError(t, err)

	// Access secret
	_, err = testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: tokenResp.Token.Token,
	})
	require.NoError(t, err)

	// Update secret
	_, err = testClient.UpdateSecret(ctx, &api.UpdateSecretRequest{
		Namespace: namespace,
		Name:      secretName,
		Data:      map[string][]byte{"key": []byte("new-value")},
	})
	require.NoError(t, err)

	// Retrieve audit log
	auditResp, err := testClient.GetSecretAuditLog(ctx, &api.GetSecretAuditLogRequest{
		SecretRef: fmt.Sprintf("%s/%s", namespace, secretName),
		Limit:     100,
	})
	require.NoError(t, err, "GetSecretAuditLog should succeed")

	// Verify audit entries exist
	assert.GreaterOrEqual(t, len(auditResp.Entries), 4, "Should have at least 4 audit entries")

	// Check for expected actions
	actions := make(map[string]bool)
	for _, entry := range auditResp.Entries {
		actions[entry.Action] = true
		assert.NotEmpty(t, entry.Actor, "Actor should be recorded")
		assert.NotNil(t, entry.Timestamp, "Timestamp should be recorded")
	}

	assert.True(t, actions["CREATE"], "CREATE action should be audited")
	assert.True(t, actions["GENERATE_TOKEN"], "GENERATE_TOKEN action should be audited")
	assert.True(t, actions["ACCESS"], "ACCESS action should be audited")
	assert.True(t, actions["UPDATE"], "UPDATE action should be audited")

	// Cleanup
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: namespace,
		Name:      secretName,
	})
}

// TestMasterKeyInfo verifies that master key information can be retrieved
func TestMasterKeyInfo(t *testing.T) {
	ctx := context.Background()

	keyInfoResp, err := testClient.GetMasterKeyInfo(ctx, &api.GetMasterKeyInfoRequest{})
	require.NoError(t, err, "GetMasterKeyInfo should succeed")

	assert.NotEmpty(t, keyInfoResp.KeyId, "Key ID should be returned")
	assert.Equal(t, "AES-256-GCM", keyInfoResp.Algorithm, "Algorithm should be AES-256-GCM")
	assert.True(t, keyInfoResp.Active, "Master key should be active")
	assert.NotNil(t, keyInfoResp.CreatedAt, "Created timestamp should be present")
}

// TestDuplicateSecret verifies that creating a duplicate secret fails
func TestDuplicateSecret(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("duplicate-secret-%d", time.Now().Unix())

	// Create secret
	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  namespace,
		Name:       secretName,
		Data:       map[string][]byte{"key": []byte("value")},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.NoError(t, err, "First CreateSecret should succeed")

	// Attempt to create duplicate
	_, err = testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  namespace,
		Name:       secretName,
		Data:       map[string][]byte{"key": []byte("value2")},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.Error(t, err, "Duplicate CreateSecret should fail")
	assert.Equal(t, codes.AlreadyExists, status.Code(err))

	// Cleanup
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: namespace,
		Name:      secretName,
	})
}

// TestInvalidTokenAccess verifies that invalid tokens are rejected
func TestInvalidTokenAccess(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("invalid-token-secret-%d", time.Now().Unix())

	// Create secret
	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  namespace,
		Name:       secretName,
		Data:       map[string][]byte{"key": []byte("value")},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.NoError(t, err)

	// Attempt access with invalid token
	_, err = testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: "invalid.jwt.token",
	})
	require.Error(t, err, "Access with invalid token should fail")
	assert.Contains(t, []codes.Code{codes.Unauthenticated, codes.PermissionDenied}, status.Code(err))

	// Cleanup
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: namespace,
		Name:      secretName,
	})
}

// TestLargeSecretData verifies handling of larger secret payloads
func TestLargeSecretData(t *testing.T) {
	ctx := context.Background()
	namespace := "integration-test"
	secretName := fmt.Sprintf("large-secret-%d", time.Now().Unix())

	// Create secret with 100KB of data (below 1MB limit)
	largeData := make([]byte, 100*1024) // 100KB
	for i := range largeData {
		largeData[i] = byte(i % 256)
	}

	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace: namespace,
		Name:      secretName,
		Data: map[string][]byte{
			"large-key": largeData,
		},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.NoError(t, err, "Creating large secret should succeed")

	// Generate token and access
	tokenResp, err := testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  namespace,
		Name:       secretName,
		Audience:   "test-service",
		TtlSeconds: 1800,
		MaxUses:    10,
	})
	require.NoError(t, err)

	getResp, err := testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: tokenResp.Token.Token,
	})
	require.NoError(t, err, "Accessing large secret should succeed")
	assert.Equal(t, len(largeData), len(getResp.Data["large-key"]), "Data size should match")
	assert.Equal(t, largeData, getResp.Data["large-key"], "Data content should match")

	// Cleanup
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: namespace,
		Name:      secretName,
	})
}

// TestMultipleNamespaces verifies namespace isolation
func TestMultipleNamespaces(t *testing.T) {
	ctx := context.Background()
	secretName := fmt.Sprintf("ns-secret-%d", time.Now().Unix())

	// Create same secret name in different namespaces
	_, err := testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  "ns-1",
		Name:       secretName,
		Data:       map[string][]byte{"env": []byte("ns-1")},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.NoError(t, err, "Create in ns-1 should succeed")

	_, err = testClient.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace:  "ns-2",
		Name:       secretName,
		Data:       map[string][]byte{"env": []byte("ns-2")},
		Audiences:  []string{"test-service"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	require.NoError(t, err, "Create in ns-2 should succeed")

	// Generate tokens for each
	token1Resp, err := testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  "ns-1",
		Name:       secretName,
		Audience:   "test-service",
		TtlSeconds: 1800,
		MaxUses:    10,
	})
	require.NoError(t, err)

	token2Resp, err := testClient.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  "ns-2",
		Name:       secretName,
		Audience:   "test-service",
		TtlSeconds: 1800,
		MaxUses:    10,
	})
	require.NoError(t, err)

	// Access from ns-1
	get1Resp, err := testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   "ns-1",
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: token1Resp.Token.Token,
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("ns-1"), get1Resp.Data["env"])

	// Access from ns-2
	get2Resp, err := testClient.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   "ns-2",
		Name:        secretName,
		Audience:    "test-service",
		AccessToken: token2Resp.Token.Token,
	})
	require.NoError(t, err)
	assert.Equal(t, []byte("ns-2"), get2Resp.Data["env"])

	// Cleanup
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{Namespace: "ns-1", Name: secretName})
	testClient.DeleteSecret(ctx, &api.DeleteSecretRequest{Namespace: "ns-2", Name: secretName})
}
