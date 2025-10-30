package coordinator

import (
	"context"
	"testing"
	"time"

	"github.com/osama1998H/Cloudless/pkg/api"
	"github.com/osama1998H/Cloudless/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// TestSecretsServiceServer_CreateSecret tests secret creation via gRPC
func TestSecretsServiceServer_CreateSecret(t *testing.T) {
	coord, cleanup := setupTestCoordinatorWithSecrets(t)
	defer cleanup()

	server := NewSecretsServiceServer(coord)
	ctx := context.Background()

	t.Run("create secret successfully", func(t *testing.T) {
		req := &api.CreateSecretRequest{
			Namespace: "default",
			Name:      "test-secret",
			Data: map[string][]byte{
				"username": []byte("admin"),
				"password": []byte("secure-password-123"),
			},
			Audiences:  []string{"workload-a"},
			TtlSeconds: 3600,
			Immutable:  false,
		}

		resp, err := server.CreateSecret(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Metadata)
		assert.Equal(t, "test-secret", resp.Metadata.Name)
		assert.Equal(t, "default", resp.Metadata.Namespace)
		assert.Equal(t, uint64(1), resp.Metadata.Version)
		assert.Equal(t, []string{"workload-a"}, resp.Metadata.Audiences)
	})

	t.Run("create secret with empty data", func(t *testing.T) {
		req := &api.CreateSecretRequest{
			Namespace: "default",
			Name:      "empty-secret",
			Data:      map[string][]byte{},
		}

		_, err := server.CreateSecret(ctx, req)
		assert.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.InvalidArgument, st.Code())
	})

	t.Run("create duplicate secret", func(t *testing.T) {
		req := &api.CreateSecretRequest{
			Namespace: "default",
			Name:      "duplicate-secret",
			Data: map[string][]byte{
				"key": []byte("value"),
			},
		}

		// Create first time
		_, err := server.CreateSecret(ctx, req)
		require.NoError(t, err)

		// Try to create again
		_, err = server.CreateSecret(ctx, req)
		assert.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.AlreadyExists, st.Code())
	})
}

// TestSecretsServiceServer_GetSecret tests secret retrieval via gRPC
func TestSecretsServiceServer_GetSecret(t *testing.T) {
	coord, cleanup := setupTestCoordinatorWithSecrets(t)
	defer cleanup()

	server := NewSecretsServiceServer(coord)
	ctx := context.Background()

	// Create a test secret
	secretData := map[string][]byte{
		"db_host":     []byte("localhost"),
		"db_port":     []byte("5432"),
		"db_password": []byte("super-secret"),
	}
	secret, err := coord.secretsManager.CreateSecret(
		"default",
		"db-config",
		secretData,
		secrets.WithAudiences([]string{"backend"}),
	)
	require.NoError(t, err)

	// Generate access token
	token, err := coord.secretsManager.GenerateAccessToken(
		"default",
		"db-config",
		"backend",
		1*time.Hour,
		10,
	)
	require.NoError(t, err)

	t.Run("get secret with valid token", func(t *testing.T) {
		req := &api.GetSecretRequest{
			Namespace:   "default",
			Name:        "db-config",
			Audience:    "backend",
			AccessToken: token.Token,
		}

		resp, err := server.GetSecret(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, "db-config", resp.Metadata.Name)
		assert.Equal(t, secret.Version, resp.Metadata.Version)
		assert.Equal(t, []byte("localhost"), resp.Data["db_host"])
		assert.Equal(t, []byte("5432"), resp.Data["db_port"])
		assert.Equal(t, []byte("super-secret"), resp.Data["db_password"])
	})

	t.Run("get secret without token", func(t *testing.T) {
		req := &api.GetSecretRequest{
			Namespace:   "default",
			Name:        "db-config",
			Audience:    "backend",
			AccessToken: "",
		}

		_, err := server.GetSecret(ctx, req)
		assert.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.Unauthenticated, st.Code())
	})

	t.Run("get non-existent secret", func(t *testing.T) {
		// For non-existent secrets, we need to test the GetSecret error handling
		// Since GenerateAccessToken validates secret existence, we'll create a secret,
		// generate a token, then delete the secret to simulate a race condition
		_, err := coord.secretsManager.CreateSecret(
			"default",
			"to-be-deleted",
			map[string][]byte{"key": []byte("value")},
		)
		require.NoError(t, err)

		token2, err := coord.secretsManager.GenerateAccessToken(
			"default",
			"to-be-deleted",
			"backend",
			1*time.Hour,
			10,
		)
		require.NoError(t, err)

		// Delete the secret
		err = coord.secretsManager.DeleteSecret("default", "to-be-deleted")
		require.NoError(t, err)

		// Now try to get the deleted secret
		req := &api.GetSecretRequest{
			Namespace:   "default",
			Name:        "to-be-deleted",
			Audience:    "backend",
			AccessToken: token2.Token,
		}

		_, err = server.GetSecret(ctx, req)
		assert.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

// TestSecretsServiceServer_UpdateSecret tests secret updates via gRPC
func TestSecretsServiceServer_UpdateSecret(t *testing.T) {
	coord, cleanup := setupTestCoordinatorWithSecrets(t)
	defer cleanup()

	server := NewSecretsServiceServer(coord)
	ctx := context.Background()

	// Create initial secret
	_, err := coord.secretsManager.CreateSecret(
		"default",
		"update-test",
		map[string][]byte{
			"key1": []byte("value1"),
		},
	)
	require.NoError(t, err)

	t.Run("update secret successfully", func(t *testing.T) {
		req := &api.UpdateSecretRequest{
			Namespace: "default",
			Name:      "update-test",
			Data: map[string][]byte{
				"key1": []byte("updated-value"),
				"key2": []byte("new-value"),
			},
		}

		resp, err := server.UpdateSecret(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Metadata)
		assert.Equal(t, uint64(2), resp.Metadata.Version)
		assert.Equal(t, "update-test", resp.Metadata.Name)
	})

	t.Run("update immutable secret", func(t *testing.T) {
		// Create immutable secret
		_, err := coord.secretsManager.CreateSecret(
			"default",
			"immutable-secret",
			map[string][]byte{
				"key": []byte("value"),
			},
			secrets.WithImmutable(),
		)
		require.NoError(t, err)

		req := &api.UpdateSecretRequest{
			Namespace: "default",
			Name:      "immutable-secret",
			Data: map[string][]byte{
				"key": []byte("new-value"),
			},
		}

		_, err = server.UpdateSecret(ctx, req)
		assert.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.FailedPrecondition, st.Code())
	})
}

// TestSecretsServiceServer_DeleteSecret tests secret deletion via gRPC
func TestSecretsServiceServer_DeleteSecret(t *testing.T) {
	coord, cleanup := setupTestCoordinatorWithSecrets(t)
	defer cleanup()

	server := NewSecretsServiceServer(coord)
	ctx := context.Background()

	// Create test secret
	_, err := coord.secretsManager.CreateSecret(
		"default",
		"delete-test",
		map[string][]byte{
			"key": []byte("value"),
		},
	)
	require.NoError(t, err)

	t.Run("delete secret successfully", func(t *testing.T) {
		req := &api.DeleteSecretRequest{
			Namespace: "default",
			Name:      "delete-test",
		}

		_, err := server.DeleteSecret(ctx, req)
		require.NoError(t, err)
	})

	t.Run("delete non-existent secret", func(t *testing.T) {
		req := &api.DeleteSecretRequest{
			Namespace: "default",
			Name:      "nonexistent",
		}

		_, err := server.DeleteSecret(ctx, req)
		assert.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.NotFound, st.Code())
	})
}

// TestSecretsServiceServer_ListSecrets tests secret listing via gRPC
func TestSecretsServiceServer_ListSecrets(t *testing.T) {
	coord, cleanup := setupTestCoordinatorWithSecrets(t)
	defer cleanup()

	server := NewSecretsServiceServer(coord)
	ctx := context.Background()

	// Create multiple secrets
	for i := 1; i <= 5; i++ {
		_, err := coord.secretsManager.CreateSecret(
			"default",
			string(rune('a'+i-1))+"-secret",
			map[string][]byte{
				"key": []byte("value"),
			},
		)
		require.NoError(t, err)
	}

	t.Run("list all secrets", func(t *testing.T) {
		req := &api.ListSecretsRequest{
			Namespace: "default",
		}

		resp, err := server.ListSecrets(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.GreaterOrEqual(t, len(resp.Secrets), 5)
	})

	t.Run("list with limit", func(t *testing.T) {
		req := &api.ListSecretsRequest{
			Namespace: "default",
			Limit:     2,
		}

		resp, err := server.ListSecrets(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.LessOrEqual(t, len(resp.Secrets), 2)
	})
}

// TestSecretsServiceServer_GenerateAccessToken tests token generation via gRPC
func TestSecretsServiceServer_GenerateAccessToken(t *testing.T) {
	coord, cleanup := setupTestCoordinatorWithSecrets(t)
	defer cleanup()

	server := NewSecretsServiceServer(coord)
	ctx := context.Background()

	// Create test secret
	_, err := coord.secretsManager.CreateSecret(
		"default",
		"token-test",
		map[string][]byte{
			"key": []byte("value"),
		},
		secrets.WithAudiences([]string{"app-a"}),
	)
	require.NoError(t, err)

	t.Run("generate token successfully", func(t *testing.T) {
		req := &api.GenerateAccessTokenRequest{
			Namespace:  "default",
			Name:       "token-test",
			Audience:   "app-a",
			TtlSeconds: 3600,
			MaxUses:    10,
		}

		resp, err := server.GenerateAccessToken(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.Token)
		assert.NotEmpty(t, resp.Token.Token)
		assert.NotEmpty(t, resp.Token.TokenId)
		assert.Equal(t, "app-a", resp.Token.Audience)
	})

	t.Run("generate token for wrong audience", func(t *testing.T) {
		req := &api.GenerateAccessTokenRequest{
			Namespace:  "default",
			Name:       "token-test",
			Audience:   "app-b", // Not in allowed audiences
			TtlSeconds: 3600,
			MaxUses:    10,
		}

		_, err := server.GenerateAccessToken(ctx, req)
		assert.Error(t, err)
		st, ok := status.FromError(err)
		require.True(t, ok)
		assert.Equal(t, codes.PermissionDenied, st.Code())
	})
}

// TestSecretsServiceServer_RotateMasterKey tests key rotation via gRPC
func TestSecretsServiceServer_RotateMasterKey(t *testing.T) {
	coord, cleanup := setupTestCoordinatorWithSecrets(t)
	defer cleanup()

	server := NewSecretsServiceServer(coord)
	ctx := context.Background()

	// Create some secrets before rotation
	for i := 1; i <= 3; i++ {
		_, err := coord.secretsManager.CreateSecret(
			"default",
			string(rune('a'+i-1))+"-rotate-test",
			map[string][]byte{
				"key": []byte("value"),
			},
		)
		require.NoError(t, err)
	}

	t.Run("rotate master key", func(t *testing.T) {
		req := &api.RotateMasterKeyRequest{}

		resp, err := server.RotateMasterKey(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		require.NotNil(t, resp.OldKey)
		require.NotNil(t, resp.NewKey)
		assert.NotEqual(t, resp.OldKey.KeyId, resp.NewKey.KeyId)
	})
}

// TestSecretsServiceServer_GetMasterKeyInfo tests master key info retrieval
func TestSecretsServiceServer_GetMasterKeyInfo(t *testing.T) {
	coord, cleanup := setupTestCoordinatorWithSecrets(t)
	defer cleanup()

	server := NewSecretsServiceServer(coord)
	ctx := context.Background()

	t.Run("get master key info", func(t *testing.T) {
		req := &api.GetMasterKeyInfoRequest{}

		resp, err := server.GetMasterKeyInfo(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.NotEmpty(t, resp.KeyId)
		assert.Equal(t, "AES-256-GCM", resp.Algorithm)
		assert.True(t, resp.Active)
	})
}

// TestSecretsServiceServer_GetSecretAuditLog tests audit log retrieval
func TestSecretsServiceServer_GetSecretAuditLog(t *testing.T) {
	coord, cleanup := setupTestCoordinatorWithSecrets(t)
	defer cleanup()

	server := NewSecretsServiceServer(coord)
	ctx := context.Background()

	// Perform some operations to generate audit entries
	_, err := coord.secretsManager.CreateSecret(
		"default",
		"audit-test",
		map[string][]byte{
			"key": []byte("value"),
		},
	)
	require.NoError(t, err)

	t.Run("get audit log", func(t *testing.T) {
		req := &api.GetSecretAuditLogRequest{
			Limit: 10,
		}

		resp, err := server.GetSecretAuditLog(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Greater(t, len(resp.Entries), 0)

		// Verify audit entry structure
		if len(resp.Entries) > 0 {
			entry := resp.Entries[0]
			assert.NotNil(t, entry.Timestamp)
			assert.NotEmpty(t, entry.Action)
			assert.NotEmpty(t, entry.SecretRef)
		}
	})

	t.Run("filter audit log by secret", func(t *testing.T) {
		req := &api.GetSecretAuditLogRequest{
			SecretRef: "default/audit-test",
			Limit:     10,
		}

		resp, err := server.GetSecretAuditLog(ctx, req)
		require.NoError(t, err)
		require.NotNil(t, resp)

		// All entries should be for the specified secret
		for _, entry := range resp.Entries {
			assert.Equal(t, "default/audit-test", entry.SecretRef)
		}
	})
}

// setupTestCoordinatorWithSecrets creates a test coordinator with secrets enabled
func setupTestCoordinatorWithSecrets(t *testing.T) (*Coordinator, func()) {
	t.Helper()

	logger, _ := zap.NewDevelopment()
	tmpDir := t.TempDir()

	// Generate master key
	masterKey, err := secrets.GenerateMasterKey()
	require.NoError(t, err)

	config := &Config{
		DataDir:       tmpDir,
		BindAddr:      "127.0.0.1:0",
		MetricsAddr:   "127.0.0.1:0",
		RaftAddr:      "127.0.0.1:0",
		RaftID:        "test-node",
		RaftBootstrap: true, // Bootstrap as single-node leader
		Logger:        logger,

		// Secrets configuration
		SecretsEnabled:         true,
		SecretsMasterKey:       masterKey.Key,
		SecretsMasterKeyID:     masterKey.ID,
		SecretsTokenSigningKey: []byte("test-signing-key-32-bytes-long!!"), // Exactly 32 bytes
		SecretsTokenTTL:        1 * time.Hour,
	}

	coord, err := New(config)
	require.NoError(t, err)
	require.NotNil(t, coord.secretsManager)

	// Start coordinator to establish RAFT leadership
	ctx := context.Background()
	err = coord.Start(ctx)
	require.NoError(t, err)

	// Wait a moment for RAFT leader election
	time.Sleep(100 * time.Millisecond)

	cleanup := func() {
		coord.Stop(context.Background())
	}

	return coord, cleanup
}
