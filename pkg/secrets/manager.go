package secrets

import (
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Manager manages secrets with envelope encryption and access control
type Manager struct {
	config    ManagerConfig
	logger    *zap.Logger
	encryptor *EnvelopeEncryptor
	tokens    *TokenManager
	store     SecretStore
	masterKey *MasterKey
	audit     []SecretAuditEntry
	mu        sync.RWMutex
}

// SecretStore defines the interface for secret persistence
type SecretStore interface {
	Save(secret *Secret) error
	Get(namespace, name string) (*Secret, error)
	List(filter SecretFilter) ([]*Secret, error)
	Delete(namespace, name string) error
	Close() error
}

// NewManager creates a new secrets manager
func NewManager(config ManagerConfig, store SecretStore, logger *zap.Logger) (*Manager, error) {
	// Validate master key
	if len(config.MasterKey) != DataKeySize {
		return nil, fmt.Errorf("master key must be %d bytes", DataKeySize)
	}

	// Create or load master key
	masterKey := &MasterKey{
		ID:        config.MasterKeyID,
		Algorithm: AlgorithmAES256GCM,
		Key:       config.MasterKey,
		Active:    true,
		CreatedAt: time.Now(),
	}

	// Create envelope encryptor
	encryptor, err := NewEnvelopeEncryptor(masterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create encryptor: %w", err)
	}

	// Create token manager
	tokenManager, err := NewTokenManager(config.TokenSigningKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create token manager: %w", err)
	}

	m := &Manager{
		config:    config,
		logger:    logger,
		encryptor: encryptor,
		tokens:    tokenManager,
		store:     store,
		masterKey: masterKey,
		audit:     []SecretAuditEntry{},
	}

	logger.Info("Secrets manager initialized",
		zap.String("master_key_id", masterKey.ID),
		zap.Bool("auto_rotation", config.EnableAutoRotation),
		zap.Duration("token_ttl", config.TokenTTL),
	)

	return m, nil
}

// CreateSecret creates a new secret
func (m *Manager) CreateSecret(namespace, name string, data map[string][]byte, opts ...SecretOption) (*Secret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := m.validateSecretName(namespace, name); err != nil {
		return nil, err
	}

	if err := m.validateSecretData(data); err != nil {
		return nil, err
	}

	// Check if secret already exists
	existing, _ := m.store.Get(namespace, name)
	if existing != nil && existing.DeletedAt == nil {
		return nil, fmt.Errorf("secret already exists: %s/%s", namespace, name)
	}

	// Encrypt each value
	encryptedData := make(map[string][]byte)
	perKeyEDKs := make(map[string][]byte)
	perKeyNonces := make(map[string][]byte)

	for key, value := range data {
		encrypted, encDataKey, nonce, err := m.encryptor.Encrypt(value)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt secret value for key %s: %w", key, err)
		}
		encryptedData[key] = encrypted
		perKeyEDKs[key] = encDataKey
		perKeyNonces[key] = nonce
	}

	// Create secret
	secret := &Secret{
		Name:      name,
		Namespace: namespace,
		Data:      encryptedData,
		// EncryptedDataKey is kept for legacy compatibility but not used for new secrets
		EncryptedDataKey: nil,
		KeyID:            m.masterKey.ID,
		Algorithm:        AlgorithmAES256GCM,
		Version:          1,
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	// Apply options
	for _, opt := range opts {
		opt(secret)
	}

	// Store per-key EDKs and nonces in annotations
	if secret.Annotations == nil {
		secret.Annotations = make(map[string]string)
	}
	for key := range encryptedData {
		secret.Annotations[fmt.Sprintf("edk.%s", key)] = fmt.Sprintf("%x", perKeyEDKs[key])
		secret.Annotations[fmt.Sprintf("nonce.%s", key)] = fmt.Sprintf("%x", perKeyNonces[key])
	}

	// Save to store
	if err := m.store.Save(secret); err != nil {
		return nil, fmt.Errorf("failed to save secret: %w", err)
	}

	// Audit log
	m.addAuditEntry(SecretAuditEntry{
		Timestamp: time.Now(),
		Action:    SecretActionCreate,
		SecretRef: fmt.Sprintf("%s/%s", namespace, name),
		Actor:     "system",
		Success:   true,
	})

	m.logger.Info("Secret created",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Int("keys", len(data)),
	)

	return secret, nil
}

// GetSecret retrieves a secret (requires valid access token)
func (m *Manager) GetSecret(req SecretAccessRequest) (*SecretAccessResponse, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Validate access token
	secretRef := fmt.Sprintf("%s/%s", req.Namespace, req.SecretName)
	_, err := m.tokens.ValidateToken(req.AccessToken, secretRef, req.Audience)
	if err != nil {
		m.addAuditEntry(SecretAuditEntry{
			Timestamp: time.Now(),
			Action:    SecretActionAccess,
			SecretRef: secretRef,
			Actor:     req.Audience,
			Success:   false,
			Reason:    fmt.Sprintf("token validation failed: %v", err),
		})
		return nil, fmt.Errorf("access denied: %w", err)
	}

	// Retrieve secret
	secret, err := m.store.Get(req.Namespace, req.SecretName)
	if err != nil {
		return nil, fmt.Errorf("failed to retrieve secret: %w", err)
	}

	if secret == nil {
		return nil, fmt.Errorf("secret not found: %s/%s", req.Namespace, req.SecretName)
	}

	if secret.DeletedAt != nil {
		return nil, fmt.Errorf("secret has been deleted")
	}

	// Check expiration
	if secret.ExpiresAt != nil && time.Now().After(*secret.ExpiresAt) {
		return nil, fmt.Errorf("secret has expired")
	}

	// Decrypt data
	decryptedData := make(map[string][]byte)

	for key, encryptedValue := range secret.Data {
		// Try per-key metadata first (new format)
		edkHex := secret.Annotations[fmt.Sprintf("edk.%s", key)]
		nonceHex := secret.Annotations[fmt.Sprintf("nonce.%s", key)]

		var edk, nonce []byte
		var err error

		if edkHex != "" && nonceHex != "" {
			// New format: per-key metadata
			edk, err = hex.DecodeString(edkHex)
			if err != nil {
				return nil, fmt.Errorf("failed to decode EDK for key %s: %w", key, err)
			}
			nonce, err = hex.DecodeString(nonceHex)
			if err != nil {
				return nil, fmt.Errorf("failed to decode nonce for key %s: %w", key, err)
			}
		} else {
			// Legacy format: single metadata for all keys (backward compatibility)
			if secret.EncryptedDataKey == nil || len(secret.Annotations["nonce"]) == 0 {
				return nil, fmt.Errorf("encryption metadata not found for key %s", key)
			}
			edk = secret.EncryptedDataKey
			nonce, err = hex.DecodeString(secret.Annotations["nonce"])
			if err != nil {
				return nil, fmt.Errorf("failed to decode legacy nonce: %w", err)
			}
		}

		decrypted, err := m.encryptor.Decrypt(encryptedValue, edk, nonce)
		if err != nil {
			return nil, fmt.Errorf("failed to decrypt secret value for key %s: %w", key, err)
		}
		decryptedData[key] = decrypted
	}

	// Audit log
	m.addAuditEntry(SecretAuditEntry{
		Timestamp: time.Now(),
		Action:    SecretActionAccess,
		SecretRef: secretRef,
		Actor:     req.Audience,
		Audience:  req.Audience,
		Success:   true,
	})

	m.logger.Debug("Secret accessed",
		zap.String("namespace", req.Namespace),
		zap.String("name", req.SecretName),
		zap.String("audience", req.Audience),
	)

	return &SecretAccessResponse{
		Name:      secret.Name,
		Namespace: secret.Namespace,
		Data:      decryptedData,
		Version:   secret.Version,
		ExpiresAt: secret.ExpiresAt,
	}, nil
}

// GenerateAccessToken generates a short-lived token for accessing a secret
func (m *Manager) GenerateAccessToken(namespace, name, audience string, ttl time.Duration, maxUses int) (*SecretAccessToken, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Verify secret exists
	secret, err := m.store.Get(namespace, name)
	if err != nil || secret == nil {
		return nil, fmt.Errorf("secret not found: %s/%s", namespace, name)
	}

	if secret.DeletedAt != nil {
		return nil, fmt.Errorf("secret has been deleted")
	}

	// Check if audience is allowed
	if len(secret.Audiences) > 0 {
		allowed := false
		for _, aud := range secret.Audiences {
			if aud == audience || aud == "*" {
				allowed = true
				break
			}
		}
		if !allowed {
			return nil, fmt.Errorf("audience %s is not allowed to access this secret", audience)
		}
	}

	// Use secret's TTL if configured, otherwise use provided/default
	if secret.TTL > 0 {
		ttl = time.Duration(secret.TTL) * time.Second
	}
	if ttl == 0 {
		ttl = m.config.TokenTTL
	}

	// Generate token
	secretRef := fmt.Sprintf("%s/%s", namespace, name)
	token, err := m.tokens.GenerateToken(secretRef, audience, ttl, maxUses)
	if err != nil {
		return nil, fmt.Errorf("failed to generate token: %w", err)
	}

	m.logger.Info("Access token generated",
		zap.String("secret", secretRef),
		zap.String("audience", audience),
		zap.Duration("ttl", ttl),
		zap.Int("max_uses", maxUses),
	)

	return token, nil
}

// UpdateSecret updates an existing secret
func (m *Manager) UpdateSecret(namespace, name string, data map[string][]byte) (*Secret, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Get existing secret
	secret, err := m.store.Get(namespace, name)
	if err != nil || secret == nil {
		return nil, fmt.Errorf("secret not found: %s/%s", namespace, name)
	}

	if secret.Immutable {
		return nil, fmt.Errorf("secret is immutable and cannot be updated")
	}

	if secret.DeletedAt != nil {
		return nil, fmt.Errorf("secret has been deleted")
	}

	// Encrypt new data
	encryptedData := make(map[string][]byte)
	perKeyEDKs := make(map[string][]byte)
	perKeyNonces := make(map[string][]byte)

	for key, value := range data {
		encrypted, encDataKey, nonce, err := m.encryptor.Encrypt(value)
		if err != nil {
			return nil, fmt.Errorf("failed to encrypt secret value for key %s: %w", key, err)
		}
		encryptedData[key] = encrypted
		perKeyEDKs[key] = encDataKey
		perKeyNonces[key] = nonce
	}

	// Update secret
	secret.Data = encryptedData
	// Clear legacy metadata
	secret.EncryptedDataKey = nil
	// Store per-key EDKs and nonces in annotations
	for key := range encryptedData {
		secret.Annotations[fmt.Sprintf("edk.%s", key)] = fmt.Sprintf("%x", perKeyEDKs[key])
		secret.Annotations[fmt.Sprintf("nonce.%s", key)] = fmt.Sprintf("%x", perKeyNonces[key])
	}
	secret.Version++
	secret.UpdatedAt = time.Now()

	// Save to store
	if err := m.store.Save(secret); err != nil {
		return nil, fmt.Errorf("failed to save updated secret: %w", err)
	}

	// Revoke existing tokens (secret data changed)
	revokedCount := m.tokens.RevokeTokensForSecret(fmt.Sprintf("%s/%s", namespace, name))

	// Audit log
	m.addAuditEntry(SecretAuditEntry{
		Timestamp: time.Now(),
		Action:    SecretActionUpdate,
		SecretRef: fmt.Sprintf("%s/%s", namespace, name),
		Actor:     "system",
		Success:   true,
		Metadata: map[string]interface{}{
			"tokens_revoked": revokedCount,
		},
	})

	m.logger.Info("Secret updated",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Uint64("version", secret.Version),
		zap.Int("tokens_revoked", revokedCount),
	)

	return secret, nil
}

// DeleteSecret deletes a secret
func (m *Manager) DeleteSecret(namespace, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	secret, err := m.store.Get(namespace, name)
	if err != nil || secret == nil {
		return fmt.Errorf("secret not found: %s/%s", namespace, name)
	}

	// Revoke all tokens for this secret
	revokedCount := m.tokens.RevokeTokensForSecret(fmt.Sprintf("%s/%s", namespace, name))

	// Mark as deleted (soft delete)
	now := time.Now()
	secret.DeletedAt = &now

	if err := m.store.Save(secret); err != nil {
		return fmt.Errorf("failed to mark secret as deleted: %w", err)
	}

	// Audit log
	m.addAuditEntry(SecretAuditEntry{
		Timestamp: time.Now(),
		Action:    SecretActionDelete,
		SecretRef: fmt.Sprintf("%s/%s", namespace, name),
		Actor:     "system",
		Success:   true,
		Metadata: map[string]interface{}{
			"tokens_revoked": revokedCount,
		},
	})

	m.logger.Info("Secret deleted",
		zap.String("namespace", namespace),
		zap.String("name", name),
		zap.Int("tokens_revoked", revokedCount),
	)

	return nil
}

// ListSecrets lists secrets matching the filter
func (m *Manager) ListSecrets(filter SecretFilter) ([]*SecretMetadata, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	secrets, err := m.store.List(filter)
	if err != nil {
		return nil, fmt.Errorf("failed to list secrets: %w", err)
	}

	// Convert to metadata (don't expose encrypted data)
	metadata := make([]*SecretMetadata, 0, len(secrets))
	for _, secret := range secrets {
		if secret.DeletedAt != nil {
			continue // Skip deleted secrets
		}

		keys := make([]string, 0, len(secret.Data))
		for key := range secret.Data {
			keys = append(keys, key)
		}

		metadata = append(metadata, &SecretMetadata{
			Name:      secret.Name,
			Namespace: secret.Namespace,
			Labels:    secret.Labels,
			Version:   secret.Version,
			CreatedAt: secret.CreatedAt,
			UpdatedAt: secret.UpdatedAt,
			Keys:      keys,
		})
	}

	return metadata, nil
}

// RotateMasterKey rotates the master encryption key
func (m *Manager) RotateMasterKey() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.logger.Info("Starting master key rotation",
		zap.String("old_key_id", m.masterKey.ID),
	)

	// Generate new master key
	oldKey := m.masterKey
	newKey, err := RotateMasterKey(m.masterKey)
	if err != nil {
		return fmt.Errorf("failed to generate new master key: %w", err)
	}

	// Re-encrypt all secrets with the new key
	// List all secrets from the store
	allSecrets, err := m.store.List(SecretFilter{})
	if err != nil {
		return fmt.Errorf("failed to list secrets for rotation: %w", err)
	}

	m.logger.Info("Re-encrypting secrets with new master key",
		zap.Int("total_secrets", len(allSecrets)),
		zap.String("new_key_id", newKey.ID),
	)

	// Track progress and failures
	successCount := 0
	failedSecrets := make(map[string]error)
	now := getCurrentTime()

	// Re-encrypt each secret
	for i, secret := range allSecrets {
		if secret.DeletedAt != nil {
			// Skip soft-deleted secrets
			m.logger.Debug("Skipping deleted secret",
				zap.String("namespace", secret.Namespace),
				zap.String("name", secret.Name),
			)
			continue
		}

		// Log progress every 100 secrets
		if i > 0 && i%100 == 0 {
			m.logger.Info("Key rotation progress",
				zap.Int("processed", i),
				zap.Int("total", len(allSecrets)),
				zap.Int("successful", successCount),
				zap.Int("failed", len(failedSecrets)),
			)
		}

		// Re-encrypt all data keys in this secret
		reencryptedData := make(map[string][]byte)
		for key, encryptedValue := range secret.Data {
			// Extract nonce from annotations (stored during encryption)
			nonceKey := fmt.Sprintf("nonce.%s", key)
			nonceHex, ok := secret.Annotations[nonceKey]
			if !ok {
				failedSecrets[fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)] =
					fmt.Errorf("missing nonce for key %s", key)
				m.logger.Error("Missing nonce during rotation",
					zap.String("namespace", secret.Namespace),
					zap.String("name", secret.Name),
					zap.String("key", key),
				)
				continue
			}

			// Decode nonce from hex
			nonce, err := hex.DecodeString(nonceHex)
			if err != nil || len(nonce) != NonceSize {
				failedSecrets[fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)] =
					fmt.Errorf("failed to decode nonce for key %s: invalid hex or wrong size", key)
				m.logger.Error("Invalid nonce during rotation",
					zap.String("namespace", secret.Namespace),
					zap.String("name", secret.Name),
					zap.String("key", key),
					zap.Int("nonce_size", len(nonce)),
					zap.Int("expected_size", NonceSize),
				)
				continue
			}

			// Extract encrypted data key from annotations
			edkKey := fmt.Sprintf("edk.%s", key)
			edkHex, ok := secret.Annotations[edkKey]
			if !ok {
				failedSecrets[fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)] =
					fmt.Errorf("missing encrypted data key for key %s", key)
				m.logger.Error("Missing encrypted data key during rotation",
					zap.String("namespace", secret.Namespace),
					zap.String("name", secret.Name),
					zap.String("key", key),
				)
				continue
			}

			encryptedDataKey, err := hex.DecodeString(edkHex)
			if err != nil {
				failedSecrets[fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)] =
					fmt.Errorf("failed to decode encrypted data key for key %s: %w", key, err)
				m.logger.Error("Invalid encrypted data key during rotation",
					zap.String("namespace", secret.Namespace),
					zap.String("name", secret.Name),
					zap.String("key", key),
					zap.Error(err),
				)
				continue
			}

			// Re-encrypt this value
			newEncryptedData, newEncryptedDataKey, newNonce, err := ReEncryptWithNewKey(
				encryptedValue,
				encryptedDataKey,
				nonce,
				oldKey,
				newKey,
			)
			if err != nil {
				failedSecrets[fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)] =
					fmt.Errorf("failed to re-encrypt key %s: %w", key, err)
				m.logger.Error("Failed to re-encrypt secret value",
					zap.String("namespace", secret.Namespace),
					zap.String("name", secret.Name),
					zap.String("key", key),
					zap.Error(err),
				)
				continue
			}

			reencryptedData[key] = newEncryptedData
			// Update encrypted data key and nonce in annotations
			secret.Annotations[fmt.Sprintf("edk.%s", key)] = fmt.Sprintf("%x", newEncryptedDataKey)
			secret.Annotations[fmt.Sprintf("nonce.%s", key)] = fmt.Sprintf("%x", newNonce)
		}

		// If all keys were re-encrypted successfully, update the secret
		if len(reencryptedData) == len(secret.Data) {
			secret.Data = reencryptedData
			secret.KeyID = newKey.ID
			secret.RotatedAt = &now
			secret.UpdatedAt = now

			// Save updated secret
			if err := m.store.Save(secret); err != nil {
				failedSecrets[fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)] =
					fmt.Errorf("failed to save re-encrypted secret: %w", err)
				m.logger.Error("Failed to save re-encrypted secret",
					zap.String("namespace", secret.Namespace),
					zap.String("name", secret.Name),
					zap.Error(err),
				)
				continue
			}

			successCount++
			m.logger.Debug("Secret re-encrypted successfully",
				zap.String("namespace", secret.Namespace),
				zap.String("name", secret.Name),
			)

			// Log audit entry
			m.audit = append(m.audit, SecretAuditEntry{
				Timestamp: now,
				Action:    SecretActionRotate,
				SecretRef: fmt.Sprintf("%s/%s", secret.Namespace, secret.Name),
				Actor:     "system",
				Audience:  "master-key-rotation",
				Success:   true,
				Reason:    fmt.Sprintf("Re-encrypted with key %s", newKey.ID),
			})
		}
	}

	// Check if rotation was successful
	if len(failedSecrets) > 0 {
		m.logger.Error("Master key rotation completed with errors",
			zap.Int("total_secrets", len(allSecrets)),
			zap.Int("successful", successCount),
			zap.Int("failed", len(failedSecrets)),
		)

		// Log first 10 failures for debugging
		count := 0
		for secretRef, err := range failedSecrets {
			if count >= 10 {
				break
			}
			m.logger.Error("Failed secret rotation",
				zap.String("secret", secretRef),
				zap.Error(err),
			)
			count++
		}

		return fmt.Errorf("failed to rotate %d out of %d secrets (see logs for details)",
			len(failedSecrets), len(allSecrets))
	}

	// All secrets re-encrypted successfully, activate new master key
	m.masterKey = newKey

	// Create new encryptor with new master key
	newEncryptor, err := NewEnvelopeEncryptor(newKey)
	if err != nil {
		return fmt.Errorf("failed to create encryptor with new key: %w", err)
	}
	m.encryptor = newEncryptor

	m.logger.Info("Master key rotated successfully",
		zap.String("old_key_id", oldKey.ID),
		zap.String("new_key_id", newKey.ID),
		zap.Int("secrets_re_encrypted", successCount),
	)

	// Log global audit entry for rotation
	m.audit = append(m.audit, SecretAuditEntry{
		Timestamp: now,
		Action:    SecretActionRotate,
		SecretRef: "all",
		Actor:     "system",
		Audience:  "master-key-rotation",
		Success:   true,
		Reason:    fmt.Sprintf("Rotated from %s to %s, re-encrypted %d secrets", oldKey.ID, newKey.ID, successCount),
	})

	return nil
}

// GetMasterKey returns information about the current master key (CLD-REQ-063)
// Note: Does not return the actual key material for security reasons
func (m *Manager) GetMasterKey() *MasterKey {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy of the master key without the actual key material
	return &MasterKey{
		ID:        m.masterKey.ID,
		Algorithm: m.masterKey.Algorithm,
		Key:       nil, // Don't expose actual key material
		Active:    m.masterKey.Active,
		CreatedAt: m.masterKey.CreatedAt,
		RotatedAt: m.masterKey.RotatedAt,
	}
}

// CleanupExpiredTokens removes expired access tokens
func (m *Manager) CleanupExpiredTokens() int {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.tokens.CleanupExpiredTokens()
}

// GetAuditLog returns the audit log
func (m *Manager) GetAuditLog(limit int) []SecretAuditEntry {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if limit <= 0 || limit > len(m.audit) {
		limit = len(m.audit)
	}

	// Return most recent entries
	start := len(m.audit) - limit
	if start < 0 {
		start = 0
	}

	return m.audit[start:]
}

// validateSecretName validates secret name and namespace
func (m *Manager) validateSecretName(namespace, name string) error {
	if namespace == "" {
		return &ValidationError{Field: "namespace", Message: "namespace is required"}
	}
	if name == "" {
		return &ValidationError{Field: "name", Message: "name is required"}
	}
	if len(name) > 253 {
		return &ValidationError{Field: "name", Message: "name must be <= 253 characters"}
	}
	if strings.Contains(name, "/") {
		return &ValidationError{Field: "name", Message: "name cannot contain '/'"}
	}
	return nil
}

// validateSecretData validates secret data
func (m *Manager) validateSecretData(data map[string][]byte) error {
	if len(data) == 0 {
		return &ValidationError{Field: "data", Message: "secret data cannot be empty"}
	}
	if len(data) > MaxSecretKeys {
		return &ValidationError{Field: "data", Message: fmt.Sprintf("secret cannot have more than %d keys", MaxSecretKeys)}
	}
	for key, value := range data {
		if key == "" {
			return &ValidationError{Field: "data", Message: "secret key cannot be empty"}
		}
		if len(value) == 0 {
			return &ValidationError{Field: "data", Message: fmt.Sprintf("secret value for key '%s' cannot be empty", key)}
		}
		if len(value) > MaxSecretSize {
			return &ValidationError{Field: "data", Message: fmt.Sprintf("secret value for key '%s' exceeds maximum size", key)}
		}
	}
	return nil
}

// addAuditEntry adds an entry to the audit log
func (m *Manager) addAuditEntry(entry SecretAuditEntry) {
	m.audit = append(m.audit, entry)
	// Keep only recent entries (e.g., last 10000)
	if len(m.audit) > 10000 {
		m.audit = m.audit[1:]
	}
}

// SecretOption is a functional option for secret creation
type SecretOption func(*Secret)

// WithLabels sets labels on a secret
func WithLabels(labels map[string]string) SecretOption {
	return func(s *Secret) {
		s.Labels = labels
	}
}

// WithAudiences sets allowed audiences for a secret
func WithAudiences(audiences []string) SecretOption {
	return func(s *Secret) {
		s.Audiences = audiences
	}
}

// WithTTL sets the token TTL for a secret
func WithTTL(ttl time.Duration) SecretOption {
	return func(s *Secret) {
		s.TTL = int64(ttl.Seconds())
	}
}

// WithExpiration sets an expiration time for a secret
func WithExpiration(expiresAt time.Time) SecretOption {
	return func(s *Secret) {
		s.ExpiresAt = &expiresAt
	}
}

// WithImmutable marks a secret as immutable
func WithImmutable() SecretOption {
	return func(s *Secret) {
		s.Immutable = true
	}
}
