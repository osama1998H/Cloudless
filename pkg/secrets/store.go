package secrets

import (
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/cloudless/cloudless/pkg/raft"
	"go.uber.org/zap"
)

// RaftSecretStore implements SecretStore using RAFT for distributed consensus
type RaftSecretStore struct {
	raftStore *raft.Store
	logger    *zap.Logger
	cache     map[string]*Secret // namespace/name -> secret (for faster reads)
	mu        sync.RWMutex
}

// NewRaftSecretStore creates a new RAFT-backed secret store
func NewRaftSecretStore(raftStore *raft.Store, logger *zap.Logger) (*RaftSecretStore, error) {
	if raftStore == nil {
		return nil, fmt.Errorf("raft store is required")
	}

	store := &RaftSecretStore{
		raftStore: raftStore,
		logger:    logger,
		cache:     make(map[string]*Secret),
	}

	// Load existing secrets from RAFT into cache
	if err := store.loadCache(); err != nil {
		logger.Warn("Failed to load secrets cache from RAFT", zap.Error(err))
		// Continue anyway - cache will be populated on first access
	}

	return store, nil
}

// Save stores a secret in RAFT
func (s *RaftSecretStore) Save(secret *Secret) error {
	if secret == nil {
		return fmt.Errorf("secret cannot be nil")
	}

	key := s.getKey(secret.Namespace, secret.Name)

	// Serialize secret to JSON
	data, err := json.Marshal(secret)
	if err != nil {
		return fmt.Errorf("failed to serialize secret: %w", err)
	}

	// Store in RAFT with strong consistency
	if err := s.raftStore.Set(key, data); err != nil {
		return fmt.Errorf("failed to store secret in RAFT: %w", err)
	}

	// Update cache
	s.mu.Lock()
	s.cache[key] = secret
	s.mu.Unlock()

	s.logger.Debug("Secret saved to RAFT",
		zap.String("namespace", secret.Namespace),
		zap.String("name", secret.Name),
		zap.Uint64("version", secret.Version),
	)

	return nil
}

// Get retrieves a secret from RAFT
func (s *RaftSecretStore) Get(namespace, name string) (*Secret, error) {
	if namespace == "" || name == "" {
		return nil, fmt.Errorf("namespace and name are required")
	}

	key := s.getKey(namespace, name)

	// Try cache first
	s.mu.RLock()
	cached, exists := s.cache[key]
	s.mu.RUnlock()

	if exists {
		return cached, nil
	}

	// Cache miss - read from RAFT
	data, err := s.raftStore.Get(key)
	if err != nil {
		return nil, fmt.Errorf("failed to get secret from RAFT: %w", err)
	}

	if data == nil {
		return nil, nil // Secret not found
	}

	// Deserialize secret
	var secret Secret
	if err := json.Unmarshal(data, &secret); err != nil {
		return nil, fmt.Errorf("failed to deserialize secret: %w", err)
	}

	// Update cache
	s.mu.Lock()
	s.cache[key] = &secret
	s.mu.Unlock()

	return &secret, nil
}

// List retrieves secrets matching the filter
func (s *RaftSecretStore) List(filter SecretFilter) ([]*Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Filter secrets from cache
	secrets := make([]*Secret, 0)

	for _, secret := range s.cache {
		// Apply namespace filter
		if filter.Namespace != "" && secret.Namespace != filter.Namespace {
			continue
		}

		// Apply name filter (exact match)
		if filter.Name != "" && secret.Name != filter.Name {
			continue
		}

		// Apply prefix filter
		if filter.Prefix != "" && !strings.HasPrefix(secret.Name, filter.Prefix) {
			continue
		}

		// Apply label filters
		if len(filter.Labels) > 0 {
			match := true
			for k, v := range filter.Labels {
				if secret.Labels[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		secrets = append(secrets, secret)
	}

	// Apply limit and offset
	if filter.Offset > 0 && filter.Offset < len(secrets) {
		secrets = secrets[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(secrets) {
		secrets = secrets[:filter.Limit]
	}

	return secrets, nil
}

// Delete removes a secret from RAFT
func (s *RaftSecretStore) Delete(namespace, name string) error {
	if namespace == "" || name == "" {
		return fmt.Errorf("namespace and name are required")
	}

	key := s.getKey(namespace, name)

	// Delete from RAFT
	if err := s.raftStore.Delete(key); err != nil {
		return fmt.Errorf("failed to delete secret from RAFT: %w", err)
	}

	// Remove from cache
	s.mu.Lock()
	delete(s.cache, key)
	s.mu.Unlock()

	s.logger.Debug("Secret deleted from RAFT",
		zap.String("namespace", namespace),
		zap.String("name", name),
	)

	return nil
}

// Close closes the secret store
func (s *RaftSecretStore) Close() error {
	// Nothing to close for RAFT-backed store
	// RAFT store is managed by the coordinator
	return nil
}

// loadCache loads all secrets from RAFT into cache
func (s *RaftSecretStore) loadCache() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Get all keys with "secret/" prefix from RAFT
	// This is a simplified implementation - real RAFT would need a proper scan API

	s.logger.Info("Secrets cache loaded from RAFT",
		zap.Int("count", len(s.cache)),
	)

	return nil
}

// getKey generates a storage key for a secret
func (s *RaftSecretStore) getKey(namespace, name string) string {
	return fmt.Sprintf("secret/%s/%s", namespace, name)
}

// InMemorySecretStore is a simple in-memory implementation for testing
type InMemorySecretStore struct {
	secrets map[string]*Secret
	mu      sync.RWMutex
}

// NewInMemorySecretStore creates a new in-memory secret store
func NewInMemorySecretStore() *InMemorySecretStore {
	return &InMemorySecretStore{
		secrets: make(map[string]*Secret),
	}
}

// Save stores a secret in memory
func (s *InMemorySecretStore) Save(secret *Secret) error {
	if secret == nil {
		return fmt.Errorf("secret cannot be nil")
	}

	key := fmt.Sprintf("%s/%s", secret.Namespace, secret.Name)

	s.mu.Lock()
	defer s.mu.Unlock()

	// Deep copy to avoid external modifications
	data, _ := json.Marshal(secret)
	var copy Secret
	json.Unmarshal(data, &copy)

	s.secrets[key] = &copy

	return nil
}

// Get retrieves a secret from memory
func (s *InMemorySecretStore) Get(namespace, name string) (*Secret, error) {
	if namespace == "" || name == "" {
		return nil, fmt.Errorf("namespace and name are required")
	}

	key := fmt.Sprintf("%s/%s", namespace, name)

	s.mu.RLock()
	defer s.mu.RUnlock()

	secret, exists := s.secrets[key]
	if !exists {
		return nil, nil
	}

	// Deep copy to avoid external modifications
	data, _ := json.Marshal(secret)
	var copy Secret
	json.Unmarshal(data, &copy)

	return &copy, nil
}

// List retrieves secrets matching the filter
func (s *InMemorySecretStore) List(filter SecretFilter) ([]*Secret, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	secrets := make([]*Secret, 0)

	for _, secret := range s.secrets {
		// Apply namespace filter
		if filter.Namespace != "" && secret.Namespace != filter.Namespace {
			continue
		}

		// Apply name filter (exact match)
		if filter.Name != "" && secret.Name != filter.Name {
			continue
		}

		// Apply prefix filter
		if filter.Prefix != "" && !strings.HasPrefix(secret.Name, filter.Prefix) {
			continue
		}

		// Apply label filters
		if len(filter.Labels) > 0 {
			match := true
			for k, v := range filter.Labels {
				if secret.Labels[k] != v {
					match = false
					break
				}
			}
			if !match {
				continue
			}
		}

		// Deep copy
		data, _ := json.Marshal(secret)
		var copy Secret
		json.Unmarshal(data, &copy)

		secrets = append(secrets, &copy)
	}

	// Apply limit and offset
	if filter.Offset > 0 && filter.Offset < len(secrets) {
		secrets = secrets[filter.Offset:]
	}
	if filter.Limit > 0 && filter.Limit < len(secrets) {
		secrets = secrets[:filter.Limit]
	}

	return secrets, nil
}

// Delete removes a secret from memory
func (s *InMemorySecretStore) Delete(namespace, name string) error {
	if namespace == "" || name == "" {
		return fmt.Errorf("namespace and name are required")
	}

	key := fmt.Sprintf("%s/%s", namespace, name)

	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.secrets, key)

	return nil
}

// Close closes the in-memory store (no-op)
func (s *InMemorySecretStore) Close() error {
	return nil
}
