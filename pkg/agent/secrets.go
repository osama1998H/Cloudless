package agent

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// CLD-REQ-063: Secrets Management Client
// This file implements the agent-side secrets retrieval logic

// SecretCacheEntry represents a cached secret with its access token
type SecretCacheEntry struct {
	Data        map[string][]byte // Decrypted secret data
	AccessToken string            // Access token for this secret
	ExpiresAt   time.Time         // When the token expires
	Namespace   string
	Name        string
	Version     uint64
}

// SecretsClient manages secret retrieval and caching
type SecretsClient struct {
	client api.SecretsServiceClient
	logger *zap.Logger

	// Cache for secrets and access tokens
	mu    sync.RWMutex
	cache map[string]*SecretCacheEntry // key: "namespace/name/audience"

	// Configuration
	tokenRefreshThreshold time.Duration // Refresh tokens when this close to expiry
	cacheCleanupInterval  time.Duration
	stopCh                chan struct{}
}

// NewSecretsClient creates a new secrets client
func NewSecretsClient(client api.SecretsServiceClient, logger *zap.Logger) *SecretsClient {
	sc := &SecretsClient{
		client:                client,
		logger:                logger,
		cache:                 make(map[string]*SecretCacheEntry),
		tokenRefreshThreshold: 5 * time.Minute,
		cacheCleanupInterval:  10 * time.Minute,
		stopCh:                make(chan struct{}),
	}

	// Start background cache cleanup
	go sc.runCacheCleanup()

	return sc
}

// GetSecret retrieves a secret from the coordinator with caching
func (sc *SecretsClient) GetSecret(ctx context.Context, namespace, name, audience string) (map[string][]byte, error) {
	cacheKey := fmt.Sprintf("%s/%s/%s", namespace, name, audience)

	// Check cache first
	sc.mu.RLock()
	entry, exists := sc.cache[cacheKey]
	sc.mu.RUnlock()

	// Return cached secret if valid
	if exists && time.Now().Before(entry.ExpiresAt.Add(-sc.tokenRefreshThreshold)) {
		sc.logger.Debug("Using cached secret",
			zap.String("namespace", namespace),
			zap.String("name", name),
			zap.String("audience", audience),
		)
		return entry.Data, nil
	}

	// Need to fetch from coordinator
	sc.logger.Info("Fetching secret from coordinator",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.String("audience", audience),
	)

	// Step 1: Generate access token if we don't have a valid one
	var accessToken string
	if exists && entry.AccessToken != "" && time.Now().Before(entry.ExpiresAt.Add(-sc.tokenRefreshThreshold)) {
		accessToken = entry.AccessToken
	} else {
		token, err := sc.generateAccessToken(ctx, namespace, name, audience)
		if err != nil {
			return nil, fmt.Errorf("failed to generate access token: %w", err)
		}
		accessToken = token
	}

	// Step 2: Retrieve secret using the access token
	data, version, err := sc.retrieveSecret(ctx, namespace, name, audience, accessToken)
	if err != nil {
		return nil, err
	}

	// Step 3: Update cache
	sc.mu.Lock()
	sc.cache[cacheKey] = &SecretCacheEntry{
		Data:        data,
		AccessToken: accessToken,
		ExpiresAt:   time.Now().Add(1 * time.Hour), // TODO: Get actual TTL from token
		Namespace:   namespace,
		Name:        name,
		Version:     version,
	}
	sc.mu.Unlock()

	return data, nil
}

// generateAccessToken requests an access token from the coordinator
func (sc *SecretsClient) generateAccessToken(ctx context.Context, namespace, name, audience string) (string, error) {
	req := &api.GenerateAccessTokenRequest{
		Namespace: namespace,
		Name:      name,
		Audience:  audience,
		TtlSeconds: 3600, // 1 hour
		MaxUses:    100,  // Allow multiple uses
	}

	resp, err := sc.client.GenerateAccessToken(ctx, req)
	if err != nil {
		return "", fmt.Errorf("failed to generate access token: %w", err)
	}

	if resp.Token == nil {
		return "", fmt.Errorf("no token returned from coordinator")
	}

	sc.logger.Debug("Generated access token",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.String("audience", audience),
		zap.String("token_id", resp.Token.TokenId),
	)

	return resp.Token.Token, nil
}

// retrieveSecret fetches the actual secret data using an access token
func (sc *SecretsClient) retrieveSecret(ctx context.Context, namespace, name, audience, accessToken string) (map[string][]byte, uint64, error) {
	req := &api.GetSecretRequest{
		Namespace:   namespace,
		Name:        name,
		Audience:    audience,
		AccessToken: accessToken,
	}

	resp, err := sc.client.GetSecret(ctx, req)
	if err != nil {
		// Handle specific error cases
		if st, ok := status.FromError(err); ok {
			switch st.Code() {
			case codes.NotFound:
				return nil, 0, fmt.Errorf("secret not found: %s/%s", namespace, name)
			case codes.PermissionDenied:
				return nil, 0, fmt.Errorf("access denied to secret %s/%s for audience %s", namespace, name, audience)
			case codes.Unauthenticated:
				return nil, 0, fmt.Errorf("invalid or expired access token")
			}
		}
		return nil, 0, fmt.Errorf("failed to retrieve secret: %w", err)
	}

	if resp.Metadata == nil {
		return nil, 0, fmt.Errorf("no metadata in response")
	}

	sc.logger.Info("Retrieved secret",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Uint64("version", resp.Metadata.Version),
		zap.Int("keys", len(resp.Data)),
	)

	return resp.Data, resp.Metadata.Version, nil
}

// InvalidateCache removes a secret from the cache
func (sc *SecretsClient) InvalidateCache(namespace, name, audience string) {
	cacheKey := fmt.Sprintf("%s/%s/%s", namespace, name, audience)
	sc.mu.Lock()
	delete(sc.cache, cacheKey)
	sc.mu.Unlock()

	sc.logger.Debug("Invalidated secret cache",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.String("audience", audience),
	)
}

// ClearCache removes all cached secrets
func (sc *SecretsClient) ClearCache() {
	sc.mu.Lock()
	sc.cache = make(map[string]*SecretCacheEntry)
	sc.mu.Unlock()

	sc.logger.Info("Cleared all secret cache")
}

// runCacheCleanup periodically removes expired cache entries
func (sc *SecretsClient) runCacheCleanup() {
	ticker := time.NewTicker(sc.cacheCleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			sc.cleanupExpiredEntries()
		case <-sc.stopCh:
			return
		}
	}
}

// cleanupExpiredEntries removes expired cache entries
func (sc *SecretsClient) cleanupExpiredEntries() {
	now := time.Now()
	sc.mu.Lock()
	defer sc.mu.Unlock()

	expiredKeys := []string{}
	for key, entry := range sc.cache {
		if now.After(entry.ExpiresAt) {
			expiredKeys = append(expiredKeys, key)
		}
	}

	for _, key := range expiredKeys {
		delete(sc.cache, key)
	}

	if len(expiredKeys) > 0 {
		sc.logger.Debug("Cleaned up expired cache entries",
			zap.Int("count", len(expiredKeys)),
		)
	}
}

// Stop stops the secrets client and cleanup goroutine
func (sc *SecretsClient) Stop() {
	close(sc.stopCh)
	sc.logger.Info("Secrets client stopped")
}

// GetCacheStats returns statistics about the secret cache
func (sc *SecretsClient) GetCacheStats() map[string]interface{} {
	sc.mu.RLock()
	defer sc.mu.RUnlock()

	now := time.Now()
	validEntries := 0
	expiredEntries := 0

	for _, entry := range sc.cache {
		if now.Before(entry.ExpiresAt) {
			validEntries++
		} else {
			expiredEntries++
		}
	}

	return map[string]interface{}{
		"total_entries":   len(sc.cache),
		"valid_entries":   validEntries,
		"expired_entries": expiredEntries,
	}
}
