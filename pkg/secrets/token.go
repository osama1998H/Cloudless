package secrets

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

// TokenManager manages short-lived secret access tokens
type TokenManager struct {
	signingKey []byte
	tokens     map[string]*SecretAccessToken // tokenID -> token
}

// SecretTokenClaims represents JWT claims for secret access tokens
type SecretTokenClaims struct {
	SecretRef string `json:"secret_ref"` // "namespace/name"
	Audience  string `json:"audience"`   // Workload/service
	TokenID   string `json:"token_id"`
	jwt.RegisteredClaims
}

// NewTokenManager creates a new token manager
func NewTokenManager(signingKey []byte) (*TokenManager, error) {
	if len(signingKey) == 0 {
		return nil, fmt.Errorf("signing key is required")
	}

	return &TokenManager{
		signingKey: signingKey,
		tokens:     make(map[string]*SecretAccessToken),
	}, nil
}

// GenerateToken generates a short-lived access token for a secret
func (tm *TokenManager) GenerateToken(secretRef, audience string, ttl time.Duration, maxUses int) (*SecretAccessToken, error) {
	if secretRef == "" {
		return nil, fmt.Errorf("secret reference is required")
	}
	if audience == "" {
		return nil, fmt.Errorf("audience is required")
	}
	if ttl <= 0 {
		ttl = DefaultTokenTTL
	}
	if ttl > MaxTokenTTL {
		return nil, fmt.Errorf("TTL cannot exceed %s", MaxTokenTTL)
	}

	tokenID := uuid.New().String()
	now := time.Now()
	expiresAt := now.Add(ttl)

	// Create JWT claims
	claims := SecretTokenClaims{
		SecretRef: secretRef,
		Audience:  audience,
		TokenID:   tokenID,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        tokenID,
			Issuer:    "cloudless-secrets",
			Subject:   audience,
			Audience:  jwt.ClaimStrings{audience},
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			NotBefore: jwt.NewNumericDate(now),
			IssuedAt:  jwt.NewNumericDate(now),
		},
	}

	// Create and sign the token
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := token.SignedString(tm.signingKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign token: %w", err)
	}

	// Create access token record
	accessToken := &SecretAccessToken{
		ID:            tokenID,
		SecretRef:     secretRef,
		Audience:      audience,
		IssuedAt:      now,
		ExpiresAt:     expiresAt,
		Token:         tokenString,
		UsesRemaining: maxUses,
	}

	// Store token for tracking
	tm.tokens[tokenID] = accessToken

	return accessToken, nil
}

// ValidateToken validates a secret access token
func (tm *TokenManager) ValidateToken(tokenString, secretRef, audience string) (*SecretTokenClaims, error) {
	if tokenString == "" {
		return nil, fmt.Errorf("token is required")
	}

	// Parse and validate the token
	token, err := jwt.ParseWithClaims(tokenString, &SecretTokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return tm.signingKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("token is invalid")
	}

	// Extract claims
	claims, ok := token.Claims.(*SecretTokenClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Verify secret reference matches
	if claims.SecretRef != secretRef {
		return nil, fmt.Errorf("token secret reference mismatch: expected %s, got %s", secretRef, claims.SecretRef)
	}

	// Verify audience matches
	if claims.Audience != audience {
		return nil, fmt.Errorf("token audience mismatch: expected %s, got %s", audience, claims.Audience)
	}

	// Check if token exists in our tracking
	trackedToken, exists := tm.tokens[claims.TokenID]
	if !exists {
		return nil, fmt.Errorf("token not found in tracking system")
	}

	// Check expiration
	if time.Now().After(trackedToken.ExpiresAt) {
		return nil, fmt.Errorf("token has expired")
	}

	// Check usage limits
	if trackedToken.UsesRemaining == 0 {
		return nil, fmt.Errorf("token has no remaining uses")
	}

	// Decrement uses if not unlimited
	if trackedToken.UsesRemaining > 0 {
		trackedToken.UsesRemaining--
		trackedToken.LastUsedAt = time.Now()
	}

	return claims, nil
}

// RevokeToken revokes a secret access token
func (tm *TokenManager) RevokeToken(tokenID string) error {
	if tokenID == "" {
		return fmt.Errorf("token ID is required")
	}

	if _, exists := tm.tokens[tokenID]; !exists {
		return fmt.Errorf("token not found: %s", tokenID)
	}

	delete(tm.tokens, tokenID)
	return nil
}

// RevokeTokensForSecret revokes all tokens for a specific secret
func (tm *TokenManager) RevokeTokensForSecret(secretRef string) int {
	count := 0
	for tokenID, token := range tm.tokens {
		if token.SecretRef == secretRef {
			delete(tm.tokens, tokenID)
			count++
		}
	}
	return count
}

// CleanupExpiredTokens removes expired tokens from tracking
func (tm *TokenManager) CleanupExpiredTokens() int {
	count := 0
	now := time.Now()
	for tokenID, token := range tm.tokens {
		if now.After(token.ExpiresAt) || token.UsesRemaining == 0 {
			delete(tm.tokens, tokenID)
			count++
		}
	}
	return count
}

// GetActiveTokens returns all active (non-expired) tokens
func (tm *TokenManager) GetActiveTokens() []*SecretAccessToken {
	now := time.Now()
	active := []*SecretAccessToken{}
	for _, token := range tm.tokens {
		if now.Before(token.ExpiresAt) && token.UsesRemaining != 0 {
			active = append(active, token)
		}
	}
	return active
}

// GetTokensForSecret returns all active tokens for a specific secret
func (tm *TokenManager) GetTokensForSecret(secretRef string) []*SecretAccessToken {
	now := time.Now()
	tokens := []*SecretAccessToken{}
	for _, token := range tm.tokens {
		if token.SecretRef == secretRef &&
			now.Before(token.ExpiresAt) &&
			token.UsesRemaining != 0 {
			tokens = append(tokens, token)
		}
	}
	return tokens
}

// GetTokenStats returns statistics about tokens
func (tm *TokenManager) GetTokenStats() map[string]int {
	now := time.Now()
	stats := map[string]int{
		"total":   len(tm.tokens),
		"active":  0,
		"expired": 0,
		"used_up": 0,
	}

	for _, token := range tm.tokens {
		if now.After(token.ExpiresAt) {
			stats["expired"]++
		} else if token.UsesRemaining == 0 {
			stats["used_up"]++
		} else {
			stats["active"]++
		}
	}

	return stats
}
