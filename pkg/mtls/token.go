package mtls

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TokenManager manages bootstrap tokens for node enrollment
type TokenManager struct {
	signingKey []byte
	tokens     map[string]*BootstrapToken // tokenID -> token
	logger     *zap.Logger
	mu         sync.RWMutex
}

// BootstrapToken represents a token for node enrollment
type BootstrapToken struct {
	ID         string
	Token      string
	HashedToken []byte
	NodeID     string
	NodeName   string
	Region     string
	Zone       string
	CreatedAt  time.Time
	ExpiresAt  time.Time
	Used       bool
	UsedAt     time.Time
	UsedBy     string
	MaxUses    int
	UseCount   int
}

// TokenClaims represents JWT claims for the token
type TokenClaims struct {
	TokenID  string `json:"token_id"`
	NodeID   string `json:"node_id,omitempty"`
	NodeName string `json:"node_name,omitempty"`
	Region   string `json:"region,omitempty"`
	Zone     string `json:"zone,omitempty"`
	MaxUses  int    `json:"max_uses"`
	jwt.RegisteredClaims
}

// TokenManagerConfig contains configuration for the token manager
type TokenManagerConfig struct {
	SigningKey []byte
	Logger     *zap.Logger
}

// NewTokenManager creates a new token manager
func NewTokenManager(config TokenManagerConfig) (*TokenManager, error) {
	if len(config.SigningKey) == 0 {
		// Generate a random signing key if not provided
		key := make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("failed to generate signing key: %w", err)
		}
		config.SigningKey = key
	}
	if config.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	return &TokenManager{
		signingKey: config.SigningKey,
		tokens:     make(map[string]*BootstrapToken),
		logger:     config.Logger,
	}, nil
}

// GenerateToken generates a new bootstrap token
func (tm *TokenManager) GenerateToken(nodeID, nodeName, region, zone string, validityDuration time.Duration, maxUses int) (*BootstrapToken, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Generate token ID
	tokenID := uuid.New().String()

	// Set default validity if not specified
	if validityDuration <= 0 {
		validityDuration = 24 * time.Hour
	}

	// Set default max uses
	if maxUses <= 0 {
		maxUses = 1
	}

	now := time.Now()
	expiresAt := now.Add(validityDuration)

	// Create JWT claims
	claims := TokenClaims{
		TokenID:  tokenID,
		NodeID:   nodeID,
		NodeName: nodeName,
		Region:   region,
		Zone:     zone,
		MaxUses:  maxUses,
		RegisteredClaims: jwt.RegisteredClaims{
			ID:        tokenID,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(expiresAt),
			Issuer:    "cloudless-coordinator",
		},
	}

	// Create JWT token
	jwtToken := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, err := jwtToken.SignedString(tm.signingKey)
	if err != nil {
		return nil, fmt.Errorf("failed to sign token: %w", err)
	}

	// Create bootstrap token
	// Note: We don't hash JWTs with bcrypt since they're already cryptographically signed
	token := &BootstrapToken{
		ID:          tokenID,
		Token:       tokenString,
		HashedToken: nil, // Not needed for JWTs
		NodeID:      nodeID,
		NodeName:    nodeName,
		Region:      region,
		Zone:        zone,
		CreatedAt:   now,
		ExpiresAt:   expiresAt,
		Used:        false,
		MaxUses:     maxUses,
		UseCount:    0,
	}

	// Store token (without the actual token string for security)
	storedToken := *token
	storedToken.Token = "" // Don't store the actual token
	tm.tokens[tokenID] = &storedToken

	tm.logger.Info("Generated bootstrap token",
		zap.String("token_id", tokenID),
		zap.String("node_id", nodeID),
		zap.String("node_name", nodeName),
		zap.Time("expires_at", expiresAt),
		zap.Int("max_uses", maxUses),
	)

	return token, nil
}

// ValidateToken validates a bootstrap token
func (tm *TokenManager) ValidateToken(tokenString string) (*BootstrapToken, error) {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	// Parse JWT token
	token, err := jwt.ParseWithClaims(tokenString, &TokenClaims{}, func(token *jwt.Token) (interface{}, error) {
		// Verify signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return tm.signingKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("failed to parse token: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("token is invalid")
	}

	claims, ok := token.Claims.(*TokenClaims)
	if !ok {
		return nil, fmt.Errorf("invalid token claims")
	}

	// Check if token exists
	storedToken, exists := tm.tokens[claims.TokenID]
	if !exists {
		return nil, fmt.Errorf("token not found")
	}

	// Note: JWT signature is already verified by jwt.ParseWithClaims above
	// No need for additional bcrypt verification

	// Check expiration
	if time.Now().After(storedToken.ExpiresAt) {
		return nil, fmt.Errorf("token has expired")
	}

	// Check usage limits
	if storedToken.UseCount >= storedToken.MaxUses {
		return nil, fmt.Errorf("token has reached maximum uses (%d/%d)", storedToken.UseCount, storedToken.MaxUses)
	}

	tm.logger.Info("Token validated successfully",
		zap.String("token_id", claims.TokenID),
		zap.String("node_id", claims.NodeID),
		zap.Int("use_count", storedToken.UseCount),
		zap.Int("max_uses", storedToken.MaxUses),
	)

	// Return a copy with the token claims
	result := *storedToken
	result.Token = tokenString
	return &result, nil
}

// UseToken marks a token as used
func (tm *TokenManager) UseToken(tokenID, usedBy string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	token, exists := tm.tokens[tokenID]
	if !exists {
		return fmt.Errorf("token not found: %s", tokenID)
	}

	// Check if already at max uses
	if token.UseCount >= token.MaxUses {
		return fmt.Errorf("token has reached maximum uses")
	}

	// Mark as used
	token.UseCount++
	token.UsedAt = time.Now()
	token.UsedBy = usedBy

	if token.UseCount >= token.MaxUses {
		token.Used = true
	}

	tm.logger.Info("Token used",
		zap.String("token_id", tokenID),
		zap.String("used_by", usedBy),
		zap.Int("use_count", token.UseCount),
		zap.Int("max_uses", token.MaxUses),
		zap.Bool("fully_used", token.Used),
	)

	return nil
}

// RevokeToken revokes a bootstrap token
func (tm *TokenManager) RevokeToken(tokenID string) error {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	token, exists := tm.tokens[tokenID]
	if !exists {
		return fmt.Errorf("token not found: %s", tokenID)
	}

	// Mark as used (effectively revoking it)
	token.Used = true
	token.UseCount = token.MaxUses

	tm.logger.Info("Token revoked",
		zap.String("token_id", tokenID),
		zap.String("node_id", token.NodeID),
	)

	return nil
}

// ListTokens returns a list of all tokens
func (tm *TokenManager) ListTokens() []*BootstrapToken {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	tokens := make([]*BootstrapToken, 0, len(tm.tokens))
	for _, token := range tm.tokens {
		// Return copies without the hashed token
		t := *token
		t.HashedToken = nil
		tokens = append(tokens, &t)
	}

	return tokens
}

// GetToken retrieves a token by ID
func (tm *TokenManager) GetToken(tokenID string) (*BootstrapToken, error) {
	tm.mu.RLock()
	defer tm.mu.RUnlock()

	token, exists := tm.tokens[tokenID]
	if !exists {
		return nil, fmt.Errorf("token not found: %s", tokenID)
	}

	// Return a copy without the hashed token
	t := *token
	t.HashedToken = nil
	return &t, nil
}

// CleanupExpiredTokens removes expired tokens
func (tm *TokenManager) CleanupExpiredTokens() int {
	tm.mu.Lock()
	defer tm.mu.Unlock()

	now := time.Now()
	removed := 0

	for id, token := range tm.tokens {
		if now.After(token.ExpiresAt) || (token.Used && token.UseCount >= token.MaxUses) {
			delete(tm.tokens, id)
			removed++
			tm.logger.Debug("Removed expired/used token",
				zap.String("token_id", id),
				zap.Bool("expired", now.After(token.ExpiresAt)),
				zap.Bool("fully_used", token.UseCount >= token.MaxUses),
			)
		}
	}

	if removed > 0 {
		tm.logger.Info("Cleaned up tokens", zap.Int("removed", removed))
	}

	return removed
}

// GenerateRandomToken generates a random token string
func GenerateRandomToken(length int) (string, error) {
	if length <= 0 {
		length = 32
	}

	bytes := make([]byte, length)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("failed to generate random bytes: %w", err)
	}

	return base64.URLEncoding.EncodeToString(bytes), nil
}

// TokenInfo provides information about a token for display
type TokenInfo struct {
	ID        string    `json:"id"`
	NodeID    string    `json:"node_id,omitempty"`
	NodeName  string    `json:"node_name,omitempty"`
	Region    string    `json:"region,omitempty"`
	Zone      string    `json:"zone,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	Used      bool      `json:"used"`
	UseCount  int       `json:"use_count"`
	MaxUses   int       `json:"max_uses"`
	UsedBy    string    `json:"used_by,omitempty"`
	UsedAt    time.Time `json:"used_at,omitempty"`
}

// GetTokenInfo returns token information for display
func (tm *TokenManager) GetTokenInfo(tokenID string) (*TokenInfo, error) {
	token, err := tm.GetToken(tokenID)
	if err != nil {
		return nil, err
	}

	return &TokenInfo{
		ID:        token.ID,
		NodeID:    token.NodeID,
		NodeName:  token.NodeName,
		Region:    token.Region,
		Zone:      token.Zone,
		CreatedAt: token.CreatedAt,
		ExpiresAt: token.ExpiresAt,
		Used:      token.Used,
		UseCount:  token.UseCount,
		MaxUses:   token.MaxUses,
		UsedBy:    token.UsedBy,
		UsedAt:    token.UsedAt,
	}, nil
}

// StartCleanupTask starts a periodic cleanup task for expired tokens
func (tm *TokenManager) StartCleanupTask(interval time.Duration) chan struct{} {
	if interval <= 0 {
		interval = 1 * time.Hour
	}

	stopCh := make(chan struct{})

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				tm.CleanupExpiredTokens()
			case <-stopCh:
				return
			}
		}
	}()

	tm.logger.Info("Started token cleanup task", zap.Duration("interval", interval))
	return stopCh
}