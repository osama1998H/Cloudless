package secrets

import (
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestNewManager tests manager creation
func TestNewManager(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey() error = %v", err)
	}

	config := ManagerConfig{
		MasterKeyID:     masterKey.ID,
		MasterKey:       masterKey.Key,
		TokenSigningKey: []byte("test-signing-key-32-bytes-long!!"),
		TokenTTL:        DefaultTokenTTL,
	}

	store := NewInMemorySecretStore()
	logger := zap.NewNop()

	manager, err := NewManager(config, store, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	if manager == nil {
		t.Fatal("NewManager() returned nil")
	}
}

// TestNewManager_InvalidKey tests manager creation with invalid master key
func TestNewManager_InvalidKey(t *testing.T) {
	config := ManagerConfig{
		MasterKeyID:     "test",
		MasterKey:       []byte("too-short"), // Wrong size
		TokenSigningKey: []byte("test-signing-key-32-bytes-long!!"),
	}

	store := NewInMemorySecretStore()
	logger := zap.NewNop()

	_, err := NewManager(config, store, logger)
	if err == nil {
		t.Error("NewManager() with invalid key should fail")
	}
}

// TestCreateSecret tests secret creation
func TestCreateSecret(t *testing.T) {
	manager := newTestManager(t)

	data := map[string][]byte{
		"username": []byte("admin"),
		"password": []byte("secret123"),
	}

	secret, err := manager.CreateSecret("test-ns", "test-secret", data,
		WithLabels(map[string]string{"env": "test"}),
		WithAudiences([]string{"workload-1"}),
	)

	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}

	if secret == nil {
		t.Fatal("CreateSecret() returned nil")
	}

	// Verify secret properties
	if secret.Name != "test-secret" {
		t.Errorf("Secret name = %s, want test-secret", secret.Name)
	}
	if secret.Namespace != "test-ns" {
		t.Errorf("Secret namespace = %s, want test-ns", secret.Namespace)
	}
	if secret.Version != 1 {
		t.Errorf("Secret version = %d, want 1", secret.Version)
	}
	if len(secret.Audiences) != 1 || secret.Audiences[0] != "workload-1" {
		t.Errorf("Secret audiences = %v, want [workload-1]", secret.Audiences)
	}
}

// TestCreateSecret_DuplicateName tests creating secret with duplicate name
func TestCreateSecret_DuplicateName(t *testing.T) {
	manager := newTestManager(t)

	data := map[string][]byte{"key": []byte("value")}

	// Create first secret
	_, err := manager.CreateSecret("test-ns", "duplicate", data)
	if err != nil {
		t.Fatalf("First CreateSecret() error = %v", err)
	}

	// Try to create duplicate
	_, err = manager.CreateSecret("test-ns", "duplicate", data)
	if err == nil {
		t.Error("CreateSecret() with duplicate name should fail")
	}
}

// TestGetSecret_WithToken tests retrieving and decrypting a secret with access token
func TestGetSecret_WithToken(t *testing.T) {
	manager := newTestManager(t)

	// Create secret
	data := map[string][]byte{
		"username": []byte("admin"),
		"password": []byte("secret123"),
	}

	_, err := manager.CreateSecret("test-ns", "get-test", data,
		WithAudiences([]string{"workload-1"}),
	)
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}

	// Generate access token
	token, err := manager.GenerateAccessToken("test-ns", "get-test", "workload-1", DefaultTokenTTL, 1)
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	// Access secret with token
	req := SecretAccessRequest{
		Namespace:   "test-ns",
		SecretName:  "get-test",
		Audience:    "workload-1",
		AccessToken: token.Token,
	}

	response, err := manager.GetSecret(req)
	if err != nil {
		t.Fatalf("GetSecret() error = %v", err)
	}

	// Verify decrypted data
	if string(response.Data["username"]) != "admin" {
		t.Errorf("Decrypted username = %s, want admin", response.Data["username"])
	}
	if string(response.Data["password"]) != "secret123" {
		t.Errorf("Decrypted password = %s, want secret123", response.Data["password"])
	}
}

// TestGetSecret_WrongAudience tests access denial for wrong audience
func TestGetSecret_WrongAudience(t *testing.T) {
	manager := newTestManager(t)

	// Create secret for workload-1 only
	data := map[string][]byte{"key": []byte("value")}
	_, err := manager.CreateSecret("test-ns", "audience-test", data,
		WithAudiences([]string{"workload-1"}),
	)
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}

	// Try to generate token for workload-2
	_, err = manager.GenerateAccessToken("test-ns", "audience-test", "workload-2", DefaultTokenTTL, 1)
	if err == nil {
		t.Error("GenerateAccessToken() for unauthorized audience should fail")
	}
}

// TestGetSecret_ExpiredToken tests access denial with expired token
func TestGetSecret_ExpiredToken(t *testing.T) {
	manager := newTestManager(t)

	// Create secret
	data := map[string][]byte{"key": []byte("value")}
	_, err := manager.CreateSecret("test-ns", "expire-test", data,
		WithAudiences([]string{"workload-1"}),
	)
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}

	// Generate token with very short TTL
	shortTTL := 1 * time.Millisecond
	token, _ := manager.GenerateAccessToken("test-ns", "expire-test", "workload-1", shortTTL, 1)

	// Wait for token to expire
	time.Sleep(10 * time.Millisecond)

	// Try to access with expired token
	req := SecretAccessRequest{
		Namespace:   "test-ns",
		SecretName:  "expire-test",
		Audience:    "workload-1",
		AccessToken: token.Token,
	}

	_, err = manager.GetSecret(req)
	if err == nil {
		t.Error("GetSecret() with expired token should fail")
	}
}

// TestUpdateSecret tests updating an existing secret
func TestUpdateSecret(t *testing.T) {
	manager := newTestManager(t)

	// Create secret
	originalData := map[string][]byte{"key": []byte("original")}
	secret, err := manager.CreateSecret("test-ns", "update-test", originalData)
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}

	originalVersion := secret.Version

	// Update secret
	updatedData := map[string][]byte{"key": []byte("updated")}
	updated, err := manager.UpdateSecret("test-ns", "update-test", updatedData)
	if err != nil {
		t.Fatalf("UpdateSecret() error = %v", err)
	}

	// Verify version incremented
	if updated.Version != originalVersion+1 {
		t.Errorf("Updated version = %d, want %d", updated.Version, originalVersion+1)
	}
}

// TestDeleteSecret tests deleting a secret
func TestDeleteSecret(t *testing.T) {
	manager := newTestManager(t)

	// Create secret
	data := map[string][]byte{"key": []byte("value")}
	_, err := manager.CreateSecret("test-ns", "delete-test", data,
		WithAudiences([]string{"workload-1"}),
	)
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}

	// Delete secret
	err = manager.DeleteSecret("test-ns", "delete-test")
	if err != nil {
		t.Fatalf("DeleteSecret() error = %v", err)
	}

	// Try to generate token after deletion (should fail)
	_, err = manager.GenerateAccessToken("test-ns", "delete-test", "workload-1", DefaultTokenTTL, 1)
	if err == nil {
		t.Error("GenerateAccessToken() after delete should fail")
	}
}

// TestListSecrets tests listing secrets
func TestListSecrets(t *testing.T) {
	manager := newTestManager(t)

	// Create multiple secrets
	for i := 1; i <= 5; i++ {
		data := map[string][]byte{"key": []byte("value")}
		name := "secret-" + string(rune('0'+i))
		_, _ = manager.CreateSecret("test-ns", name, data)
	}

	// List all secrets in namespace
	secrets, err := manager.ListSecrets(SecretFilter{Namespace: "test-ns"})
	if err != nil {
		t.Fatalf("ListSecrets() error = %v", err)
	}

	if len(secrets) != 5 {
		t.Errorf("ListSecrets() returned %d secrets, want 5", len(secrets))
	}
}

// TestListSecrets_WithLabels tests listing secrets filtered by labels
func TestListSecrets_WithLabels(t *testing.T) {
	manager := newTestManager(t)

	// Create secrets with different labels
	data := map[string][]byte{"key": []byte("value")}

	_, _ = manager.CreateSecret("prod", "app-secret", data,
		WithLabels(map[string]string{"env": "prod", "app": "web"}))

	_, _ = manager.CreateSecret("prod", "db-secret", data,
		WithLabels(map[string]string{"env": "prod", "app": "db"}))

	_, _ = manager.CreateSecret("dev", "test-secret", data,
		WithLabels(map[string]string{"env": "dev"}))

	// Filter by namespace
	prodSecrets, _ := manager.ListSecrets(SecretFilter{Namespace: "prod"})
	if len(prodSecrets) != 2 {
		t.Errorf("Prod namespace has %d secrets, want 2", len(prodSecrets))
	}

	// Filter by label
	webSecrets, _ := manager.ListSecrets(SecretFilter{
		Namespace: "prod",
		Labels:    map[string]string{"app": "web"},
	})
	if len(webSecrets) != 1 {
		t.Errorf("Web app has %d secrets, want 1", len(webSecrets))
	}
}

// TestManager_RotateMasterKey tests master key rotation
func TestManager_RotateMasterKey(t *testing.T) {
	manager := newTestManager(t)

	// Create multiple secrets
	for i := 1; i <= 3; i++ {
		data := map[string][]byte{"key": []byte("value")}
		name := "rotate-secret-" + string(rune('0'+i))
		_, _ = manager.CreateSecret("test-ns", name, data,
			WithAudiences([]string{"workload-1"}))
	}

	oldKeyID := manager.masterKey.ID

	// Rotate master key
	err := manager.RotateMasterKey()
	if err != nil {
		t.Fatalf("RotateMasterKey() error = %v", err)
	}

	// Verify new key is different
	newKeyID := manager.masterKey.ID
	if newKeyID == oldKeyID {
		t.Error("Master key ID did not change after rotation")
	}

	// Verify secrets can still be accessed with new key
	for i := 1; i <= 3; i++ {
		name := "rotate-secret-" + string(rune('0'+i))

		// Generate new token
		token, err := manager.GenerateAccessToken("test-ns", name, "workload-1", DefaultTokenTTL, 1)
		if err != nil {
			t.Errorf("GenerateAccessToken() after rotation failed for %s: %v", name, err)
			continue
		}

		// Access secret
		req := SecretAccessRequest{
			Namespace:   "test-ns",
			SecretName:  name,
			Audience:    "workload-1",
			AccessToken: token.Token,
		}

		_, err = manager.GetSecret(req)
		if err != nil {
			t.Errorf("GetSecret() after rotation failed for %s: %v", name, err)
		}
	}
}

// TestGenerateAccessToken tests token generation
func TestGenerateAccessToken(t *testing.T) {
	manager := newTestManager(t)

	// Create secret
	data := map[string][]byte{"key": []byte("value")}
	_, _ = manager.CreateSecret("test-ns", "token-test", data,
		WithAudiences([]string{"workload-1"}))

	// Generate token
	token, err := manager.GenerateAccessToken("test-ns", "token-test", "workload-1", DefaultTokenTTL, 1)
	if err != nil {
		t.Fatalf("GenerateAccessToken() error = %v", err)
	}

	if token.Token == "" {
		t.Error("Generated token is empty")
	}
	if token.SecretRef != "test-ns/token-test" {
		t.Errorf("Token SecretRef = %s, want test-ns/token-test", token.SecretRef)
	}
	if token.Audience != "workload-1" {
		t.Errorf("Token Audience = %s, want workload-1", token.Audience)
	}
}

// TestGenerateAccessToken_MultiUse tests multi-use tokens
func TestGenerateAccessToken_MultiUse(t *testing.T) {
	manager := newTestManager(t)

	// Create secret
	data := map[string][]byte{"key": []byte("value")}
	_, _ = manager.CreateSecret("test-ns", "multi-test", data,
		WithAudiences([]string{"workload-1"}))

	// Generate token with 3 uses
	token, _ := manager.GenerateAccessToken("test-ns", "multi-test", "workload-1", DefaultTokenTTL, 3)

	req := SecretAccessRequest{
		Namespace:   "test-ns",
		SecretName:  "multi-test",
		Audience:    "workload-1",
		AccessToken: token.Token,
	}

	// Use 1
	_, err := manager.GetSecret(req)
	if err != nil {
		t.Errorf("First use failed: %v", err)
	}

	// Use 2
	_, err = manager.GetSecret(req)
	if err != nil {
		t.Errorf("Second use failed: %v", err)
	}

	// Use 3
	_, err = manager.GetSecret(req)
	if err != nil {
		t.Errorf("Third use failed: %v", err)
	}

	// Use 4 should fail
	_, err = manager.GetSecret(req)
	if err == nil {
		t.Error("Fourth use should fail for 3-use token")
	}
}

// TestManager_CleanupExpiredTokens tests token cleanup
func TestManager_CleanupExpiredTokens(t *testing.T) {
	manager := newTestManager(t)

	// Create secret
	data := map[string][]byte{"key": []byte("value")}
	_, _ = manager.CreateSecret("test-ns", "cleanup-test", data,
		WithAudiences([]string{"workload-1"}))

	// Generate expired token
	shortTTL := 1 * time.Millisecond
	_, _ = manager.GenerateAccessToken("test-ns", "cleanup-test", "workload-1", shortTTL, 1)

	// Generate valid token
	_, _ = manager.GenerateAccessToken("test-ns", "cleanup-test", "workload-1", DefaultTokenTTL, 1)

	// Wait for first token to expire
	time.Sleep(10 * time.Millisecond)

	// Cleanup
	cleaned := manager.CleanupExpiredTokens()
	if cleaned != 1 {
		t.Errorf("CleanupExpiredTokens() = %d, want 1", cleaned)
	}
}

// TestGetAuditLog tests audit logging
func TestGetAuditLog(t *testing.T) {
	manager := newTestManager(t)

	// Perform operations
	data := map[string][]byte{"key": []byte("value")}
	secret, _ := manager.CreateSecret("test-ns", "audit-test", data,
		WithAudiences([]string{"workload-1"}))

	_, _ = manager.UpdateSecret("test-ns", "audit-test", data)
	_ = manager.DeleteSecret("test-ns", "audit-test")

	// Get audit log
	auditLog := manager.GetAuditLog(100)
	if len(auditLog) < 3 {
		t.Errorf("Audit log has %d entries, want at least 3", len(auditLog))
	}

	// Verify entry types
	actions := make(map[SecretAction]int)
	for _, entry := range auditLog {
		actions[entry.Action]++
	}

	if actions[SecretActionCreate] < 1 {
		t.Error("Audit log missing create action")
	}
	if actions[SecretActionUpdate] < 1 {
		t.Error("Audit log missing update action")
	}
	if actions[SecretActionDelete] < 1 {
		t.Error("Audit log missing delete action")
	}

	// Verify secret reference format
	for _, entry := range auditLog {
		if entry.SecretRef != "test-ns/audit-test" {
			t.Errorf("Audit entry SecretRef = %s, want test-ns/audit-test", entry.SecretRef)
		}
		if !entry.Success {
			t.Error("Audit entry should mark successful operations as Success=true")
		}
	}

	_ = secret // Silence unused variable warning
}

// TestSecretOptions tests various secret options
func TestSecretOptions(t *testing.T) {
	manager := newTestManager(t)

	data := map[string][]byte{"key": []byte("value")}

	// Test WithImmutable
	immutable, err := manager.CreateSecret("test-ns", "immutable-test", data,
		WithImmutable())
	if err != nil {
		t.Fatalf("CreateSecret() error = %v", err)
	}

	if !immutable.Immutable {
		t.Error("Secret should be marked as immutable")
	}

	// Try to update immutable secret (should fail)
	_, err = manager.UpdateSecret("test-ns", "immutable-test", map[string][]byte{"key": []byte("new")})
	if err == nil {
		t.Error("UpdateSecret() on immutable secret should fail")
	}

	// Test WithExpiration
	expiration := time.Now().Add(24 * time.Hour)
	expirable, _ := manager.CreateSecret("test-ns", "expirable-test", data,
		WithExpiration(expiration))

	if expirable.ExpiresAt == nil {
		t.Error("Secret should have ExpiresAt set")
	}
}

// newTestManager creates a test manager with default configuration
func newTestManager(t *testing.T) *Manager {
	t.Helper()

	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey() error = %v", err)
	}

	config := ManagerConfig{
		MasterKeyID:     masterKey.ID,
		MasterKey:       masterKey.Key,
		TokenSigningKey: []byte("test-signing-key-32-bytes-long!!"),
		TokenTTL:        DefaultTokenTTL,
	}

	store := NewInMemorySecretStore()
	logger := zap.NewNop()

	manager, err := NewManager(config, store, logger)
	if err != nil {
		t.Fatalf("NewManager() error = %v", err)
	}

	return manager
}
