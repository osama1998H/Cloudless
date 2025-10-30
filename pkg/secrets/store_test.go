package secrets

import (
	"sync"
	"testing"
	"time"
)

// TestNewInMemorySecretStore tests creating a new in-memory store
func TestNewInMemorySecretStore(t *testing.T) {
	store := NewInMemorySecretStore()
	if store == nil {
		t.Fatal("NewInMemorySecretStore returned nil")
	}
	if store.secrets == nil {
		t.Error("Store secrets map is nil")
	}
}

// TestInMemoryStore_Save tests saving secrets to memory
func TestInMemoryStore_Save(t *testing.T) {
	store := NewInMemorySecretStore()

	tests := []struct {
		name      string
		secret    *Secret
		wantError bool
	}{
		{
			name: "valid secret",
			secret: &Secret{
				Namespace: "test-ns",
				Name:      "test-secret",
				Version:   1,
				Data: map[string][]byte{
					"key1": []byte("encrypted-value-1"),
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantError: false,
		},
		{
			name:      "nil secret",
			secret:    nil,
			wantError: true,
		},
		{
			name: "overwrite existing",
			secret: &Secret{
				Namespace: "test-ns",
				Name:      "test-secret",
				Version:   2,
				Data: map[string][]byte{
					"key1": []byte("new-encrypted-value"),
				},
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Save(tt.secret)
			if (err != nil) != tt.wantError {
				t.Errorf("Save() error = %v, wantError %v", err, tt.wantError)
			}

			if err == nil && tt.secret != nil {
				// Verify secret was stored
				retrieved, err := store.Get(tt.secret.Namespace, tt.secret.Name)
				if err != nil {
					t.Errorf("Get() after Save() error = %v", err)
				}
				if retrieved == nil {
					t.Error("Get() after Save() returned nil")
				}
				if retrieved != nil && retrieved.Version != tt.secret.Version {
					t.Errorf("Retrieved version = %d, want %d", retrieved.Version, tt.secret.Version)
				}
			}
		})
	}
}

// TestInMemoryStore_SaveDeepCopy tests that Save makes a deep copy
func TestInMemoryStore_SaveDeepCopy(t *testing.T) {
	store := NewInMemorySecretStore()

	secret := &Secret{
		Namespace: "test-ns",
		Name:      "test-secret",
		Version:   1,
		Data: map[string][]byte{
			"key1": []byte("value1"),
		},
		Labels: map[string]string{
			"env": "test",
		},
	}

	err := store.Save(secret)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Modify original secret
	secret.Version = 99
	secret.Data["key1"] = []byte("modified")
	secret.Labels["env"] = "modified"

	// Retrieve and verify stored secret wasn't affected
	retrieved, _ := store.Get("test-ns", "test-secret")
	if retrieved.Version == 99 {
		t.Error("Store did not make deep copy - version was modified")
	}
	if string(retrieved.Data["key1"]) == "modified" {
		t.Error("Store did not make deep copy - data was modified")
	}
	if retrieved.Labels["env"] == "modified" {
		t.Error("Store did not make deep copy - labels were modified")
	}
}

// TestInMemoryStore_Get tests retrieving secrets from memory
func TestInMemoryStore_Get(t *testing.T) {
	store := NewInMemorySecretStore()

	// Store a test secret
	testSecret := &Secret{
		Namespace: "test-ns",
		Name:      "test-secret",
		Version:   1,
		Data: map[string][]byte{
			"key1": []byte("encrypted-value"),
		},
	}
	store.Save(testSecret)

	tests := []struct {
		name       string
		namespace  string
		secretName string
		wantFound  bool
		wantError  bool
	}{
		{
			name:       "found secret",
			namespace:  "test-ns",
			secretName: "test-secret",
			wantFound:  true,
			wantError:  false,
		},
		{
			name:       "not found",
			namespace:  "test-ns",
			secretName: "non-existent",
			wantFound:  false,
			wantError:  false,
		},
		{
			name:       "empty namespace",
			namespace:  "",
			secretName: "test-secret",
			wantFound:  false,
			wantError:  true,
		},
		{
			name:       "empty name",
			namespace:  "test-ns",
			secretName: "",
			wantFound:  false,
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			secret, err := store.Get(tt.namespace, tt.secretName)
			if (err != nil) != tt.wantError {
				t.Errorf("Get() error = %v, wantError %v", err, tt.wantError)
			}
			if tt.wantFound && secret == nil {
				t.Error("Get() returned nil, expected secret")
			}
			if !tt.wantFound && !tt.wantError && secret != nil {
				t.Error("Get() returned secret, expected nil")
			}
		})
	}
}

// TestInMemoryStore_GetDeepCopy tests that Get returns a deep copy
func TestInMemoryStore_GetDeepCopy(t *testing.T) {
	store := NewInMemorySecretStore()

	secret := &Secret{
		Namespace: "test-ns",
		Name:      "test-secret",
		Version:   1,
		Data: map[string][]byte{
			"key1": []byte("value1"),
		},
		Labels: map[string]string{
			"env": "test",
		},
	}
	store.Save(secret)

	// Retrieve secret
	retrieved1, _ := store.Get("test-ns", "test-secret")

	// Modify retrieved secret
	retrieved1.Version = 99
	retrieved1.Data["key1"] = []byte("modified")
	retrieved1.Labels["env"] = "modified"

	// Get again and verify it wasn't affected
	retrieved2, _ := store.Get("test-ns", "test-secret")
	if retrieved2.Version == 99 {
		t.Error("Store did not return deep copy - version was modified")
	}
	if string(retrieved2.Data["key1"]) == "modified" {
		t.Error("Store did not return deep copy - data was modified")
	}
	if retrieved2.Labels["env"] == "modified" {
		t.Error("Store did not return deep copy - labels were modified")
	}
}

// TestInMemoryStore_List tests listing secrets with various filters
func TestInMemoryStore_List(t *testing.T) {
	store := NewInMemorySecretStore()

	// Create test secrets
	secrets := []*Secret{
		{
			Namespace: "ns1",
			Name:      "secret1",
			Version:   1,
			Labels:    map[string]string{"env": "prod", "tier": "frontend"},
		},
		{
			Namespace: "ns1",
			Name:      "secret2",
			Version:   1,
			Labels:    map[string]string{"env": "prod", "tier": "backend"},
		},
		{
			Namespace: "ns1",
			Name:      "app-secret1",
			Version:   1,
			Labels:    map[string]string{"env": "dev"},
		},
		{
			Namespace: "ns2",
			Name:      "secret1",
			Version:   1,
			Labels:    map[string]string{"env": "prod"},
		},
		{
			Namespace: "ns2",
			Name:      "app-secret2",
			Version:   1,
			Labels:    map[string]string{"env": "dev"},
		},
	}

	for _, s := range secrets {
		store.Save(s)
	}

	tests := []struct {
		name      string
		filter    SecretFilter
		wantCount int
	}{
		{
			name:      "no filter - all secrets",
			filter:    SecretFilter{},
			wantCount: 5,
		},
		{
			name: "namespace filter",
			filter: SecretFilter{
				Namespace: "ns1",
			},
			wantCount: 3,
		},
		{
			name: "name filter - exact match",
			filter: SecretFilter{
				Name: "secret1",
			},
			wantCount: 2, // ns1/secret1 and ns2/secret1
		},
		{
			name: "prefix filter",
			filter: SecretFilter{
				Prefix: "app-",
			},
			wantCount: 2, // app-secret1 and app-secret2
		},
		{
			name: "single label filter",
			filter: SecretFilter{
				Labels: map[string]string{"env": "prod"},
			},
			wantCount: 3,
		},
		{
			name: "multiple label filter",
			filter: SecretFilter{
				Labels: map[string]string{
					"env":  "prod",
					"tier": "frontend",
				},
			},
			wantCount: 1, // Only ns1/secret1 matches both
		},
		{
			name: "combined namespace and prefix",
			filter: SecretFilter{
				Namespace: "ns1",
				Prefix:    "app-",
			},
			wantCount: 1, // ns1/app-secret1
		},
		{
			name: "combined namespace and labels",
			filter: SecretFilter{
				Namespace: "ns1",
				Labels:    map[string]string{"env": "dev"},
			},
			wantCount: 1, // ns1/app-secret1
		},
		{
			name: "limit",
			filter: SecretFilter{
				Limit: 2,
			},
			wantCount: 2,
		},
		{
			name: "offset",
			filter: SecretFilter{
				Offset: 3,
			},
			wantCount: 2, // 5 total - 3 offset = 2
		},
		{
			name: "limit and offset",
			filter: SecretFilter{
				Limit:  2,
				Offset: 1,
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			results, err := store.List(tt.filter)
			if err != nil {
				t.Errorf("List() error = %v", err)
			}
			if len(results) != tt.wantCount {
				t.Errorf("List() returned %d secrets, want %d", len(results), tt.wantCount)
			}

			// Verify results match filter
			for _, s := range results {
				// Check namespace
				if tt.filter.Namespace != "" && s.Namespace != tt.filter.Namespace {
					t.Errorf("Secret %s/%s has wrong namespace", s.Namespace, s.Name)
				}

				// Check name
				if tt.filter.Name != "" && s.Name != tt.filter.Name {
					t.Errorf("Secret %s has wrong name, want %s", s.Name, tt.filter.Name)
				}

				// Check prefix
				if tt.filter.Prefix != "" && !startsWithPrefix(s.Name, tt.filter.Prefix) {
					t.Errorf("Secret %s does not have prefix %s", s.Name, tt.filter.Prefix)
				}

				// Check labels
				for k, v := range tt.filter.Labels {
					if s.Labels[k] != v {
						t.Errorf("Secret %s/%s missing label %s=%s", s.Namespace, s.Name, k, v)
					}
				}
			}
		})
	}
}

// Helper function for prefix checking
func startsWithPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}

// TestInMemoryStore_ListDeepCopy tests that List returns deep copies
func TestInMemoryStore_ListDeepCopy(t *testing.T) {
	store := NewInMemorySecretStore()

	secret := &Secret{
		Namespace: "test-ns",
		Name:      "test-secret",
		Version:   1,
		Data: map[string][]byte{
			"key1": []byte("value1"),
		},
	}
	store.Save(secret)

	// List secrets
	secrets, _ := store.List(SecretFilter{})
	if len(secrets) != 1 {
		t.Fatalf("List returned %d secrets, want 1", len(secrets))
	}

	// Modify returned secret
	secrets[0].Version = 99
	secrets[0].Data["key1"] = []byte("modified")

	// Get original and verify it wasn't affected
	retrieved, _ := store.Get("test-ns", "test-secret")
	if retrieved.Version == 99 {
		t.Error("List did not return deep copy - version was modified")
	}
	if string(retrieved.Data["key1"]) == "modified" {
		t.Error("List did not return deep copy - data was modified")
	}
}

// TestInMemoryStore_Delete tests deleting secrets
func TestInMemoryStore_Delete(t *testing.T) {
	store := NewInMemorySecretStore()

	// Create test secret
	secret := &Secret{
		Namespace: "test-ns",
		Name:      "test-secret",
		Version:   1,
	}
	store.Save(secret)

	tests := []struct {
		name       string
		namespace  string
		secretName string
		wantError  bool
	}{
		{
			name:       "delete existing",
			namespace:  "test-ns",
			secretName: "test-secret",
			wantError:  false,
		},
		{
			name:       "delete non-existent",
			namespace:  "test-ns",
			secretName: "non-existent",
			wantError:  false,
		},
		{
			name:       "empty namespace",
			namespace:  "",
			secretName: "test-secret",
			wantError:  true,
		},
		{
			name:       "empty name",
			namespace:  "test-ns",
			secretName: "",
			wantError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := store.Delete(tt.namespace, tt.secretName)
			if (err != nil) != tt.wantError {
				t.Errorf("Delete() error = %v, wantError %v", err, tt.wantError)
			}

			// If delete succeeded, verify secret is gone
			if err == nil && tt.name == "delete existing" {
				retrieved, _ := store.Get(tt.namespace, tt.secretName)
				if retrieved != nil {
					t.Error("Secret still exists after Delete()")
				}
			}
		})
	}
}

// TestInMemoryStore_Close tests closing the store
func TestInMemoryStore_Close(t *testing.T) {
	store := NewInMemorySecretStore()

	err := store.Close()
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

// TestInMemoryStore_Concurrency tests concurrent access to the store
func TestInMemoryStore_Concurrency(t *testing.T) {
	store := NewInMemorySecretStore()

	const numGoroutines = 20
	const numOperations = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Launch concurrent operations
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numOperations; j++ {
				// Create unique secret for this goroutine
				secret := &Secret{
					Namespace: "test-ns",
					Name:      "secret-" + string(rune('a'+id)),
					Version:   uint64(j),
					Data: map[string][]byte{
						"key1": []byte("value"),
					},
				}

				// Save
				if err := store.Save(secret); err != nil {
					t.Errorf("Goroutine %d: Save() error = %v", id, err)
				}

				// Get
				_, err := store.Get(secret.Namespace, secret.Name)
				if err != nil {
					t.Errorf("Goroutine %d: Get() error = %v", id, err)
				}

				// List
				_, err = store.List(SecretFilter{Namespace: "test-ns"})
				if err != nil {
					t.Errorf("Goroutine %d: List() error = %v", id, err)
				}

				// Occasionally delete
				if j%10 == 0 {
					store.Delete(secret.Namespace, secret.Name)
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify final state is consistent
	secrets, err := store.List(SecretFilter{})
	if err != nil {
		t.Errorf("Final List() error = %v", err)
	}

	// Should have at least some secrets left
	if len(secrets) == 0 {
		t.Error("No secrets found after concurrent operations")
	}
}

// TestInMemoryStore_SaveAndRetrieveComplexSecret tests saving and retrieving a secret with all fields
func TestInMemoryStore_SaveAndRetrieveComplexSecret(t *testing.T) {
	store := NewInMemorySecretStore()

	now := time.Now()
	expiresAt := now.Add(30 * 24 * time.Hour)
	secret := &Secret{
		Namespace:        "production",
		Name:             "db-credentials",
		Version:          5,
		KeyID:            "key-abc123",
		Algorithm:        AlgorithmAES256GCM,
		EncryptedDataKey: []byte("encrypted-data-key-bytes"),
		Data: map[string][]byte{
			"username": []byte("encrypted-username"),
			"password": []byte("encrypted-password"),
			"host":     []byte("encrypted-host"),
		},
		Labels: map[string]string{
			"app":  "myapp",
			"env":  "prod",
			"tier": "database",
		},
		Annotations: map[string]string{
			"edk.username":   "abc123",
			"edk.password":   "def456",
			"edk.host":       "ghi789",
			"nonce.username": "111111",
			"nonce.password": "222222",
			"nonce.host":     "333333",
		},
		Audiences:   []string{"workload-1", "workload-2"},
		TTL:         900, // 15 minutes in seconds
		Description: "Database credentials for production",
		Immutable:   false,
		CreatedAt:   now,
		UpdatedAt:   now.Add(time.Hour),
		ExpiresAt:   &expiresAt,
		DeleteAfter: 7200, // 2 hours in seconds
	}

	// Save
	err := store.Save(secret)
	if err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	// Retrieve
	retrieved, err := store.Get("production", "db-credentials")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if retrieved == nil {
		t.Fatal("Get() returned nil")
	}

	// Verify all fields
	if retrieved.Namespace != secret.Namespace {
		t.Errorf("Namespace = %s, want %s", retrieved.Namespace, secret.Namespace)
	}
	if retrieved.Name != secret.Name {
		t.Errorf("Name = %s, want %s", retrieved.Name, secret.Name)
	}
	if retrieved.Version != secret.Version {
		t.Errorf("Version = %d, want %d", retrieved.Version, secret.Version)
	}
	if retrieved.KeyID != secret.KeyID {
		t.Errorf("KeyID = %s, want %s", retrieved.KeyID, secret.KeyID)
	}
	if retrieved.Algorithm != secret.Algorithm {
		t.Errorf("Algorithm = %s, want %s", retrieved.Algorithm, secret.Algorithm)
	}
	if string(retrieved.EncryptedDataKey) != string(secret.EncryptedDataKey) {
		t.Errorf("EncryptedDataKey mismatch")
	}
	if len(retrieved.Data) != len(secret.Data) {
		t.Errorf("Data length = %d, want %d", len(retrieved.Data), len(secret.Data))
	}
	for k, v := range secret.Data {
		if string(retrieved.Data[k]) != string(v) {
			t.Errorf("Data[%s] = %s, want %s", k, retrieved.Data[k], v)
		}
	}
	if len(retrieved.Labels) != len(secret.Labels) {
		t.Errorf("Labels length = %d, want %d", len(retrieved.Labels), len(secret.Labels))
	}
	for k, v := range secret.Labels {
		if retrieved.Labels[k] != v {
			t.Errorf("Labels[%s] = %s, want %s", k, retrieved.Labels[k], v)
		}
	}
	if len(retrieved.Annotations) != len(secret.Annotations) {
		t.Errorf("Annotations length = %d, want %d", len(retrieved.Annotations), len(secret.Annotations))
	}
	if len(retrieved.Audiences) != len(secret.Audiences) {
		t.Errorf("Audiences length = %d, want %d", len(retrieved.Audiences), len(secret.Audiences))
	}
	if retrieved.TTL != secret.TTL {
		t.Errorf("TTL = %d, want %d", retrieved.TTL, secret.TTL)
	}
	if retrieved.Immutable != secret.Immutable {
		t.Errorf("Immutable = %v, want %v", retrieved.Immutable, secret.Immutable)
	}
	if retrieved.Description != secret.Description {
		t.Errorf("Description = %s, want %s", retrieved.Description, secret.Description)
	}
	if !retrieved.CreatedAt.Equal(secret.CreatedAt) {
		t.Errorf("CreatedAt = %v, want %v", retrieved.CreatedAt, secret.CreatedAt)
	}
	if !retrieved.UpdatedAt.Equal(secret.UpdatedAt) {
		t.Errorf("UpdatedAt = %v, want %v", retrieved.UpdatedAt, secret.UpdatedAt)
	}
	if retrieved.ExpiresAt == nil || !retrieved.ExpiresAt.Equal(*secret.ExpiresAt) {
		t.Errorf("ExpiresAt mismatch")
	}
	if retrieved.DeleteAfter != secret.DeleteAfter {
		t.Errorf("DeleteAfter = %d, want %d", retrieved.DeleteAfter, secret.DeleteAfter)
	}
}

// TestValidationError tests the ValidationError type
func TestValidationError(t *testing.T) {
	err := &ValidationError{
		Field:   "namespace",
		Message: "cannot be empty",
	}

	expected := "namespace: cannot be empty"
	if err.Error() != expected {
		t.Errorf("Error() = %q, want %q", err.Error(), expected)
	}
}

// TestDefaultManagerConfig tests the default configuration
func TestDefaultManagerConfig(t *testing.T) {
	config := DefaultManagerConfig()

	if config.MasterKeyID != "default-master-key" {
		t.Errorf("MasterKeyID = %s, want default-master-key", config.MasterKeyID)
	}
	if !config.EnableAutoRotation {
		t.Error("EnableAutoRotation should be true by default")
	}
	if config.TokenTTL != 15*time.Minute {
		t.Errorf("TokenTTL = %v, want 15m", config.TokenTTL)
	}
	if config.MaxTokenUsesDefault != 1 {
		t.Errorf("MaxTokenUsesDefault = %d, want 1", config.MaxTokenUsesDefault)
	}
	if !config.EnableAuditLog {
		t.Error("EnableAuditLog should be true by default")
	}
}
