package secrets

import (
	"testing"
	"time"
)

// TestNewTokenManager tests token manager creation
func TestNewTokenManager(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, err := NewTokenManager(signingKey)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	if manager == nil {
		t.Fatal("NewTokenManager() returned nil")
	}
}

// TestNewTokenManager_InvalidConfig tests creation with invalid config
func TestNewTokenManager_InvalidConfig(t *testing.T) {
	tests := []struct {
		name       string
		signingKey []byte
	}{
		{
			name:       "empty signing key",
			signingKey: []byte(""),
		},
		{
			name:       "nil signing key",
			signingKey: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewTokenManager(tt.signingKey)
			if err == nil {
				t.Error("NewTokenManager() with invalid config should fail")
			}
		})
	}
}

// TestGenerateToken tests token generation
func TestGenerateToken(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, err := NewTokenManager(signingKey)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	token, err := manager.GenerateToken("test-ns/test-secret", "test-audience", DefaultTokenTTL, 1)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Verify token properties
	if token.ID == "" {
		t.Error("Token ID is empty")
	}
	if token.SecretRef != "test-ns/test-secret" {
		t.Errorf("Token SecretRef = %s, want test-ns/test-secret", token.SecretRef)
	}
	if token.Audience != "test-audience" {
		t.Errorf("Token Audience = %s, want test-audience", token.Audience)
	}
	if token.Token == "" {
		t.Error("Token value is empty")
	}
	if token.UsesRemaining != 1 {
		t.Errorf("Token UsesRemaining = %d, want 1", token.UsesRemaining)
	}
	if token.ExpiresAt.Before(time.Now()) {
		t.Error("Token already expired")
	}
	if token.ExpiresAt.After(time.Now().Add(16 * time.Minute)) {
		t.Error("Token expiration too far in future")
	}
}

// TestGenerateToken_CustomTTL tests token generation with custom TTL
func TestGenerateToken_CustomTTL(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	customTTL := 5 * time.Minute
	token, err := manager.GenerateToken("test-ns/secret", "audience", customTTL, 1)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	expectedExpiry := time.Now().Add(customTTL)
	if token.ExpiresAt.Before(expectedExpiry.Add(-1*time.Second)) ||
		token.ExpiresAt.After(expectedExpiry.Add(1*time.Second)) {
		t.Errorf("Token expiration = %v, want ~%v", token.ExpiresAt, expectedExpiry)
	}
}

// TestValidateToken tests token validation
func TestValidateToken(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	// Generate token
	token, err := manager.GenerateToken("test-ns/secret", "audience", DefaultTokenTTL, 1)
	if err != nil {
		t.Fatalf("GenerateToken() error = %v", err)
	}

	// Validate token
	claims, err := manager.ValidateToken(token.Token, "test-ns/secret", "audience")
	if err != nil {
		t.Fatalf("ValidateToken() error = %v", err)
	}

	if claims.TokenID != token.ID {
		t.Errorf("Validated token ID = %s, want %s", claims.TokenID, token.ID)
	}

	// After validation, token should have been used
	// The token object is updated in place by ValidateToken
	if token.UsesRemaining != 0 {
		t.Errorf("After use, UsesRemaining = %d, want 0", token.UsesRemaining)
	}
}

// TestValidateToken_SingleUse tests that single-use tokens can only be used once
func TestValidateToken_SingleUse(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	token, _ := manager.GenerateToken("test-ns/secret", "audience", DefaultTokenTTL, 1)

	// First use should succeed
	_, err := manager.ValidateToken(token.Token, "test-ns/secret", "audience")
	if err != nil {
		t.Fatalf("First ValidateToken() error = %v", err)
	}

	// Second use should fail
	_, err = manager.ValidateToken(token.Token, "test-ns/secret", "audience")
	if err == nil {
		t.Error("Second ValidateToken() should fail for single-use token")
	}
}

// TestValidateToken_WrongAudience tests validation with wrong audience
func TestValidateToken_WrongAudience(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	token, _ := manager.GenerateToken("test-ns/secret", "correct-audience", DefaultTokenTTL, 1)

	// Validate with wrong audience
	_, err := manager.ValidateToken(token.Token, "test-ns/secret", "wrong-audience")
	if err == nil {
		t.Error("ValidateToken() with wrong audience should fail")
	}
}

// TestValidateToken_WrongSecretRef tests validation with wrong secret reference
func TestValidateToken_WrongSecretRef(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	token, _ := manager.GenerateToken("test-ns/secret1", "audience", DefaultTokenTTL, 1)

	// Validate with wrong secret ref
	_, err := manager.ValidateToken(token.Token, "test-ns/secret2", "audience")
	if err == nil {
		t.Error("ValidateToken() with wrong secret ref should fail")
	}
}

// TestValidateToken_ExpiredToken tests validation of expired token
func TestValidateToken_ExpiredToken(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	// Generate token with very short TTL
	shortTTL := 1 * time.Millisecond
	token, _ := manager.GenerateToken("test-ns/secret", "audience", shortTTL, 1)

	// Wait for token to expire
	time.Sleep(10 * time.Millisecond)

	// Validation should fail
	_, err := manager.ValidateToken(token.Token, "test-ns/secret", "audience")
	if err == nil {
		t.Error("ValidateToken() with expired token should fail")
	}
}

// TestValidateToken_InvalidSignature tests validation with tampered token
func TestValidateToken_InvalidSignature(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	token, _ := manager.GenerateToken("test-ns/secret", "audience", DefaultTokenTTL, 1)

	// Tamper with token
	tamperedToken := token.Token + "tampered"

	// Validation should fail
	_, err := manager.ValidateToken(tamperedToken, "test-ns/secret", "audience")
	if err == nil {
		t.Error("ValidateToken() with tampered token should fail")
	}
}

// TestRevokeToken tests token revocation
func TestRevokeToken(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	token, _ := manager.GenerateToken("test-ns/secret", "audience", DefaultTokenTTL, 5)

	// Revoke token
	err := manager.RevokeToken(token.ID)
	if err != nil {
		t.Fatalf("RevokeToken() error = %v", err)
	}

	// Validation should fail after revocation
	_, err = manager.ValidateToken(token.Token, "test-ns/secret", "audience")
	if err == nil {
		t.Error("ValidateToken() after revocation should fail")
	}
}

// TestRevokeTokensForSecret tests revoking all tokens for a secret
func TestRevokeTokensForSecret(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	// Generate multiple tokens for same secret
	token1, _ := manager.GenerateToken("test-ns/secret", "audience1", DefaultTokenTTL, 5)
	token2, _ := manager.GenerateToken("test-ns/secret", "audience2", DefaultTokenTTL, 5)
	token3, _ := manager.GenerateToken("test-ns/other-secret", "audience3", DefaultTokenTTL, 5)

	// Revoke all tokens for test-ns/secret
	count := manager.RevokeTokensForSecret("test-ns/secret")
	if count != 2 {
		t.Errorf("RevokeTokensForSecret() revoked %d tokens, want 2", count)
	}

	// token1 and token2 should be revoked
	_, err := manager.ValidateToken(token1.Token, "test-ns/secret", "audience1")
	if err == nil {
		t.Error("token1 should be revoked")
	}

	_, err = manager.ValidateToken(token2.Token, "test-ns/secret", "audience2")
	if err == nil {
		t.Error("token2 should be revoked")
	}

	// token3 should still be valid
	_, err = manager.ValidateToken(token3.Token, "test-ns/other-secret", "audience3")
	if err != nil {
		t.Error("token3 should still be valid")
	}
}

// TestCleanupExpiredTokens tests cleanup of expired tokens
func TestCleanupExpiredTokens(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	// Generate expired token
	shortTTL := 1 * time.Millisecond
	_, _ = manager.GenerateToken("test-ns/secret1", "audience", shortTTL, 5)

	// Generate valid token
	validToken, _ := manager.GenerateToken("test-ns/secret2", "audience", DefaultTokenTTL, 5)

	// Wait for first token to expire
	time.Sleep(10 * time.Millisecond)

	// Cleanup
	cleaned := manager.CleanupExpiredTokens()
	if cleaned != 1 {
		t.Errorf("CleanupExpiredTokens() = %d, want 1", cleaned)
	}

	// Valid token should still work
	_, err := manager.ValidateToken(validToken.Token, "test-ns/secret2", "audience")
	if err != nil {
		t.Error("Valid token should still work after cleanup")
	}
}

// TestMultiUseToken tests tokens with multiple uses
func TestMultiUseToken(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	token, _ := manager.GenerateToken("test-ns/secret", "audience", DefaultTokenTTL, 3)

	// Use 1
	_, err := manager.ValidateToken(token.Token, "test-ns/secret", "audience")
	if err != nil {
		t.Fatalf("First use failed: %v", err)
	}
	if token.UsesRemaining != 2 {
		t.Errorf("After use 1, UsesRemaining = %d, want 2", token.UsesRemaining)
	}

	// Use 2
	_, err = manager.ValidateToken(token.Token, "test-ns/secret", "audience")
	if err != nil {
		t.Fatalf("Second use failed: %v", err)
	}
	if token.UsesRemaining != 1 {
		t.Errorf("After use 2, UsesRemaining = %d, want 1", token.UsesRemaining)
	}

	// Use 3
	_, err = manager.ValidateToken(token.Token, "test-ns/secret", "audience")
	if err != nil {
		t.Fatalf("Third use failed: %v", err)
	}
	if token.UsesRemaining != 0 {
		t.Errorf("After use 3, UsesRemaining = %d, want 0", token.UsesRemaining)
	}

	// Use 4 should fail
	_, err = manager.ValidateToken(token.Token, "test-ns/secret", "audience")
	if err == nil {
		t.Error("Fourth use should fail for 3-use token")
	}
}

// TestGetTokensForSecret tests retrieving tokens for a specific secret
func TestGetTokensForSecret(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	// Generate tokens for different secrets
	token1, _ := manager.GenerateToken("test-ns/secret1", "audience", DefaultTokenTTL, 1)
	token2, _ := manager.GenerateToken("test-ns/secret1", "audience", DefaultTokenTTL, 1)
	token3, _ := manager.GenerateToken("test-ns/secret2", "audience", DefaultTokenTTL, 1)

	// Get tokens for secret1
	tokens := manager.GetTokensForSecret("test-ns/secret1")
	if len(tokens) != 2 {
		t.Fatalf("GetTokensForSecret() returned %d tokens, want 2", len(tokens))
	}

	// Verify token IDs
	ids := make(map[string]bool)
	for _, tok := range tokens {
		ids[tok.ID] = true
	}
	if !ids[token1.ID] || !ids[token2.ID] {
		t.Error("GetTokensForSecret() missing expected tokens")
	}
	if ids[token3.ID] {
		t.Error("GetTokensForSecret() returned token from different secret")
	}
}

// TestTokenManager_Concurrency tests concurrent token operations
func TestTokenManager_Concurrency(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")

	manager, _ := NewTokenManager(signingKey)

	// Generate token with 10 uses
	token, _ := manager.GenerateToken("test-ns/secret", "audience", DefaultTokenTTL, 10)

	// Validate concurrently
	const goroutines = 20
	results := make(chan error, goroutines)

	for i := 0; i < goroutines; i++ {
		go func() {
			_, err := manager.ValidateToken(token.Token, "test-ns/secret", "audience")
			results <- err
		}()
	}

	// Collect results
	successCount := 0
	for i := 0; i < goroutines; i++ {
		err := <-results
		if err == nil {
			successCount++
		}
	}

	// Exactly 10 validations should succeed
	if successCount != 10 {
		t.Errorf("Concurrent validations: %d succeeded, want 10", successCount)
	}
}

// TestGetActiveTokens tests retrieving active tokens
func TestGetActiveTokens(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")
	manager, err := NewTokenManager(signingKey)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	// Create mix of tokens
	token1, _ := manager.GenerateToken("test-ns/secret1", "audience1", DefaultTokenTTL, 5)
	token2, _ := manager.GenerateToken("test-ns/secret2", "audience2", DefaultTokenTTL, 1)
	_, _ = manager.GenerateToken("test-ns/secret3", "audience3", 1*time.Millisecond, 5) // Will expire immediately

	// Use up token2
	_, _ = manager.ValidateToken(token2.Token, "test-ns/secret2", "audience2")

	// Wait for token3 to expire
	time.Sleep(5 * time.Millisecond)

	// Get active tokens
	activeTokens := manager.GetActiveTokens()

	// Should only have token1 (token2 used up, token3 expired)
	if len(activeTokens) != 1 {
		t.Errorf("GetActiveTokens() returned %d tokens, want 1", len(activeTokens))
	}

	if len(activeTokens) > 0 && activeTokens[0].ID != token1.ID {
		t.Errorf("GetActiveTokens() returned wrong token, got %s, want %s", activeTokens[0].ID, token1.ID)
	}
}

// TestGetTokenStats tests token statistics
func TestGetTokenStats(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")
	manager, err := NewTokenManager(signingKey)
	if err != nil {
		t.Fatalf("NewTokenManager() error = %v", err)
	}

	// Create tokens with different states
	token1, _ := manager.GenerateToken("test-ns/secret1", "audience1", DefaultTokenTTL, 5) // active
	token2, _ := manager.GenerateToken("test-ns/secret2", "audience2", DefaultTokenTTL, 1) // will be used up
	_, _ = manager.GenerateToken("test-ns/secret3", "audience3", 1*time.Millisecond, 5)    // will expire
	_, _ = manager.GenerateToken("test-ns/secret4", "audience4", DefaultTokenTTL, 3)       // active

	// Use up token2
	_, _ = manager.ValidateToken(token2.Token, "test-ns/secret2", "audience2")

	// Wait for token3 to expire
	time.Sleep(5 * time.Millisecond)

	// Revoke token1
	_ = manager.RevokeToken(token1.ID)

	// Get stats
	stats := manager.GetTokenStats()

	// Verify stats
	// Note: token1 was deleted by RevokeToken, so total is 3
	if stats["total"] != 3 {
		t.Errorf("stats[total] = %d, want 3", stats["total"])
	}
	if stats["active"] != 1 { // Only secret4 is active
		t.Errorf("stats[active] = %d, want 1", stats["active"])
	}
	if stats["expired"] != 1 { // token3 expired
		t.Errorf("stats[expired] = %d, want 1", stats["expired"])
	}
	if stats["used_up"] != 1 { // token2 used up (token1 was deleted, not set to 0)
		t.Errorf("stats[used_up] = %d, want 1", stats["used_up"])
	}
}
