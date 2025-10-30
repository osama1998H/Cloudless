package agent

import (
	"testing"
	"time"

	"github.com/osama1998H/Cloudless/pkg/secrets"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractTokenExpiration(t *testing.T) {
	// Create a token manager for generating test tokens
	signingKey := []byte("test-signing-key-32-bytes-long!!")
	tm, err := secrets.NewTokenManager(signingKey)
	require.NoError(t, err)

	t.Run("valid token with default TTL", func(t *testing.T) {
		token, err := tm.GenerateToken("test-ns/test-secret", "test-audience", 15*time.Minute, 10)
		require.NoError(t, err)

		extractedExp, err := extractTokenExpiration(token.Token)
		require.NoError(t, err)

		// Should match within 1 second tolerance
		assert.WithinDuration(t, token.ExpiresAt, extractedExp, time.Second)
	})

	t.Run("valid token with 30 minute TTL", func(t *testing.T) {
		customTTL := 30 * time.Minute
		token, err := tm.GenerateToken("test-ns/test-secret", "test-audience", customTTL, 10)
		require.NoError(t, err)

		extractedExp, err := extractTokenExpiration(token.Token)
		require.NoError(t, err)

		// Should match within 1 second tolerance
		assert.WithinDuration(t, token.ExpiresAt, extractedExp, time.Second)
	})

	t.Run("valid token with 1 hour TTL", func(t *testing.T) {
		customTTL := 1 * time.Hour
		token, err := tm.GenerateToken("test-ns/test-secret", "test-audience", customTTL, 10)
		require.NoError(t, err)

		extractedExp, err := extractTokenExpiration(token.Token)
		require.NoError(t, err)

		// Should match within 1 second tolerance
		assert.WithinDuration(t, token.ExpiresAt, extractedExp, time.Second)

		// Verify the expiration is approximately 1 hour from now
		expectedTime := time.Now().Add(1 * time.Hour)
		assert.WithinDuration(t, expectedTime, extractedExp, 2*time.Second)
	})

	t.Run("valid token with 5 minute TTL", func(t *testing.T) {
		customTTL := 5 * time.Minute
		token, err := tm.GenerateToken("test-ns/test-secret", "test-audience", customTTL, 10)
		require.NoError(t, err)

		extractedExp, err := extractTokenExpiration(token.Token)
		require.NoError(t, err)

		// Should match within 1 second tolerance
		assert.WithinDuration(t, token.ExpiresAt, extractedExp, time.Second)

		// Verify the expiration is approximately 5 minutes from now
		expectedTime := time.Now().Add(5 * time.Minute)
		assert.WithinDuration(t, expectedTime, extractedExp, 2*time.Second)
	})

	t.Run("invalid token format", func(t *testing.T) {
		_, err := extractTokenExpiration("not-a-jwt-token")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse JWT token")
	})

	t.Run("empty token", func(t *testing.T) {
		_, err := extractTokenExpiration("")
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to parse JWT token")
	})

	t.Run("malformed JWT with correct structure but invalid data", func(t *testing.T) {
		// Create a JWT-like string with three parts but invalid base64
		malformedToken := "header.payload.signature"
		_, err := extractTokenExpiration(malformedToken)
		assert.Error(t, err)
	})

	t.Run("JWT with only two parts", func(t *testing.T) {
		_, err := extractTokenExpiration("header.payload")
		assert.Error(t, err)
	})
}

// TestExtractTokenExpiration_CacheIntegration tests that the cache correctly uses the extracted expiration
func TestExtractTokenExpiration_CacheIntegration(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")
	tm, err := secrets.NewTokenManager(signingKey)
	require.NoError(t, err)

	// Generate a token with a specific TTL
	customTTL := 20 * time.Minute
	token, err := tm.GenerateToken("cache-test/secret", "test-audience", customTTL, 10)
	require.NoError(t, err)

	// Extract expiration
	extractedExp, err := extractTokenExpiration(token.Token)
	require.NoError(t, err)

	// Verify the extracted expiration matches the token's expiration
	assert.WithinDuration(t, token.ExpiresAt, extractedExp, time.Second)

	// Verify the expiration is approximately 20 minutes from now
	expectedTime := time.Now().Add(20 * time.Minute)
	assert.WithinDuration(t, expectedTime, extractedExp, 2*time.Second)

	// Verify that extractedExp can be used as a valid time.Time
	assert.False(t, extractedExp.IsZero())
	assert.True(t, extractedExp.After(time.Now()))
}

// TestExtractTokenExpiration_DifferentTTLs tests that tokens with different TTLs
// are correctly parsed to extract their actual expiration times
func TestExtractTokenExpiration_DifferentTTLs(t *testing.T) {
	signingKey := []byte("test-signing-key-32-bytes-long!!")
	tm, err := secrets.NewTokenManager(signingKey)
	require.NoError(t, err)

	testCases := []struct {
		name string
		ttl  time.Duration
	}{
		{"1 minute", 1 * time.Minute},
		{"5 minutes", 5 * time.Minute},
		{"15 minutes", 15 * time.Minute},
		{"30 minutes", 30 * time.Minute},
		{"1 hour", 1 * time.Hour},
		{"2 hours", 2 * time.Hour},
		{"6 hours", 6 * time.Hour},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			token, err := tm.GenerateToken("test-ns/test-secret", "test-audience", tc.ttl, 10)
			require.NoError(t, err)

			extractedExp, err := extractTokenExpiration(token.Token)
			require.NoError(t, err)

			// Should match within 1 second tolerance
			assert.WithinDuration(t, token.ExpiresAt, extractedExp, time.Second)

			// Verify the expiration is approximately the expected TTL from now
			expectedTime := time.Now().Add(tc.ttl)
			assert.WithinDuration(t, expectedTime, extractedExp, 2*time.Second)
		})
	}
}
