// CLD-REQ-063: Secrets Management Manual Test
// This program demonstrates the complete secrets management workflow
//
// Usage:
//   go run test/manual/secrets_demo.go --coordinator=localhost:8080
//
// Prerequisites:
//   - Coordinator running with secrets enabled (e.g., docker-compose cluster)
//   - gRPC port accessible (default: 8080)

package main

import (
	"context"
	"flag"
	"fmt"
	"time"

	"github.com/osama1998H/Cloudless/pkg/api"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

var (
	coordinatorAddr = flag.String("coordinator", "localhost:8080", "Coordinator gRPC address")
	namespace       = flag.String("namespace", "default", "Secret namespace")
)

func main() {
	flag.Parse()

	// Initialize logger
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	logger.Info("Starting Secrets Management Demo",
		zap.String("coordinator", *coordinatorAddr),
		zap.String("namespace", *namespace),
	)

	// Connect to coordinator
	conn, err := grpc.Dial(*coordinatorAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		logger.Fatal("Failed to connect to coordinator", zap.Error(err))
	}
	defer conn.Close()

	client := api.NewSecretsServiceClient(conn)
	ctx := context.Background()

	// Run demo workflow
	if err := runSecretsDemo(ctx, client, logger); err != nil {
		logger.Fatal("Demo failed", zap.Error(err))
	}

	logger.Info("✅ Secrets Management Demo completed successfully!")
}

func runSecretsDemo(ctx context.Context, client api.SecretsServiceClient, logger *zap.Logger) error {
	// ========================================
	// Step 1: Create a secret
	// ========================================
	logger.Info("📝 Step 1: Creating secret 'db-credentials'...")

	createResp, err := client.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace: *namespace,
		Name:      "db-credentials",
		Data: map[string][]byte{
			"DB_HOST":     []byte("postgres.example.com"),
			"DB_PORT":     []byte("5432"),
			"DB_NAME":     []byte("myapp"),
			"DB_USER":     []byte("app_user"),
			"DB_PASSWORD": []byte("super-secure-password-123"),
		},
		Audiences:  []string{"backend-api", "worker"},
		TtlSeconds: 3600,
		Immutable:  false,
	})
	if err != nil {
		return fmt.Errorf("failed to create secret: %w", err)
	}

	logger.Info("✅ Secret created",
		zap.String("name", createResp.Metadata.Name),
		zap.Uint64("version", createResp.Metadata.Version),
		zap.Strings("audiences", createResp.Metadata.Audiences),
	)

	// ========================================
	// Step 2: Generate access token
	// ========================================
	logger.Info("🔑 Step 2: Generating access token for audience 'backend-api'...")

	tokenResp, err := client.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  *namespace,
		Name:       "db-credentials",
		Audience:   "backend-api",
		TtlSeconds: 1800,
		MaxUses:    100,
	})
	if err != nil {
		return fmt.Errorf("failed to generate access token: %w", err)
	}

	logger.Info("✅ Access token generated",
		zap.String("token_id", tokenResp.Token.TokenId),
		zap.String("audience", tokenResp.Token.Audience),
		zap.Int32("uses_remaining", tokenResp.Token.UsesRemaining),
	)

	accessToken := tokenResp.Token.Token

	// ========================================
	// Step 3: Retrieve secret using token
	// ========================================
	logger.Info("📖 Step 3: Retrieving secret using access token...")

	getResp, err := client.GetSecret(ctx, &api.GetSecretRequest{
		Namespace:   *namespace,
		Name:        "db-credentials",
		Audience:    "backend-api",
		AccessToken: accessToken,
	})
	if err != nil {
		return fmt.Errorf("failed to get secret: %w", err)
	}

	logger.Info("✅ Secret retrieved",
		zap.String("name", getResp.Metadata.Name),
		zap.Uint64("version", getResp.Metadata.Version),
		zap.Int("keys", len(getResp.Data)),
	)

	// Verify data
	if string(getResp.Data["DB_HOST"]) != "postgres.example.com" {
		return fmt.Errorf("unexpected DB_HOST value: %s", getResp.Data["DB_HOST"])
	}
	logger.Info("✅ Secret data verified")

	// ========================================
	// Step 4: Update secret
	// ========================================
	logger.Info("✏️  Step 4: Updating secret with new password...")

	updateResp, err := client.UpdateSecret(ctx, &api.UpdateSecretRequest{
		Namespace: *namespace,
		Name:      "db-credentials",
		Data: map[string][]byte{
			"DB_HOST":     []byte("postgres.example.com"),
			"DB_PORT":     []byte("5432"),
			"DB_NAME":     []byte("myapp"),
			"DB_USER":     []byte("app_user"),
			"DB_PASSWORD": []byte("new-rotated-password-456"),
		},
	})
	if err != nil {
		return fmt.Errorf("failed to update secret: %w", err)
	}

	logger.Info("✅ Secret updated",
		zap.String("name", updateResp.Metadata.Name),
		zap.Uint64("new_version", updateResp.Metadata.Version),
	)

	// ========================================
	// Step 5: List secrets
	// ========================================
	logger.Info("📋 Step 5: Listing all secrets in namespace...")

	listResp, err := client.ListSecrets(ctx, &api.ListSecretsRequest{
		Namespace: *namespace,
		Limit:     10,
	})
	if err != nil {
		return fmt.Errorf("failed to list secrets: %w", err)
	}

	logger.Info("✅ Secrets listed",
		zap.Int("count", len(listResp.Secrets)),
	)

	for i, secret := range listResp.Secrets {
		logger.Info(fmt.Sprintf("  [%d] %s (version: %d, audiences: %v)",
			i+1, secret.Name, secret.Version, secret.Audiences))
	}

	// ========================================
	// Step 6: Get master key info
	// ========================================
	logger.Info("🔐 Step 6: Getting master key information...")

	keyInfoResp, err := client.GetMasterKeyInfo(ctx, &api.GetMasterKeyInfoRequest{})
	if err != nil {
		return fmt.Errorf("failed to get master key info: %w", err)
	}

	logger.Info("✅ Master key info retrieved",
		zap.String("key_id", keyInfoResp.KeyId),
		zap.String("algorithm", keyInfoResp.Algorithm),
		zap.Bool("active", keyInfoResp.Active),
	)

	// ========================================
	// Step 7: Get audit log
	// ========================================
	logger.Info("📜 Step 7: Retrieving audit log...")

	auditResp, err := client.GetSecretAuditLog(ctx, &api.GetSecretAuditLogRequest{
		SecretRef: fmt.Sprintf("%s/db-credentials", *namespace),
		Limit:     10,
	})
	if err != nil {
		return fmt.Errorf("failed to get audit log: %w", err)
	}

	logger.Info("✅ Audit log retrieved",
		zap.Int("entries", len(auditResp.Entries)),
	)

	for i, entry := range auditResp.Entries {
		logger.Info(fmt.Sprintf("  [%d] %s - %s by %s",
			i+1,
			entry.Timestamp.AsTime().Format(time.RFC3339),
			entry.Action,
			entry.Actor,
		))
	}

	// ========================================
	// Step 8: Create immutable secret
	// ========================================
	logger.Info("🔒 Step 8: Creating immutable secret...")

	_, err = client.CreateSecret(ctx, &api.CreateSecretRequest{
		Namespace: *namespace,
		Name:      "tls-certificates",
		Data: map[string][]byte{
			"tls.crt": []byte("-----BEGIN CERTIFICATE-----\n..."),
			"tls.key": []byte("-----BEGIN PRIVATE KEY-----\n..."),
		},
		Audiences: []string{"web-server"},
		Immutable: true,
	})
	if err != nil {
		return fmt.Errorf("failed to create immutable secret: %w", err)
	}

	logger.Info("✅ Immutable secret created")

	// Verify immutability by trying to update
	logger.Info("🧪 Testing immutability protection...")
	_, err = client.UpdateSecret(ctx, &api.UpdateSecretRequest{
		Namespace: *namespace,
		Name:      "tls-certificates",
		Data: map[string][]byte{
			"tls.crt": []byte("modified"),
		},
	})
	if err == nil {
		return fmt.Errorf("expected error when updating immutable secret")
	}
	logger.Info("✅ Immutability protection verified (update rejected)")

	// ========================================
	// Step 9: Test audience restrictions
	// ========================================
	logger.Info("🛡️  Step 9: Testing audience-based access control...")

	// Try to generate token for unauthorized audience
	_, err = client.GenerateAccessToken(ctx, &api.GenerateAccessTokenRequest{
		Namespace:  *namespace,
		Name:       "db-credentials",
		Audience:   "unauthorized-service",
		TtlSeconds: 1800,
		MaxUses:    10,
	})
	if err == nil {
		return fmt.Errorf("expected error for unauthorized audience")
	}
	logger.Info("✅ Audience restriction verified (unauthorized access denied)")

	// ========================================
	// Step 10: Cleanup - Delete secrets
	// ========================================
	logger.Info("🗑️  Step 10: Cleaning up test secrets...")

	_, err = client.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: *namespace,
		Name:      "db-credentials",
	})
	if err != nil {
		return fmt.Errorf("failed to delete db-credentials: %w", err)
	}

	_, err = client.DeleteSecret(ctx, &api.DeleteSecretRequest{
		Namespace: *namespace,
		Name:      "tls-certificates",
	})
	if err != nil {
		return fmt.Errorf("failed to delete tls-certificates: %w", err)
	}

	logger.Info("✅ Test secrets deleted")

	return nil
}
