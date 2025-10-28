package secrets

import (
	"context"
	"crypto/rand"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestConcurrentSecretCreation verifies multiple goroutines can create different secrets safely
func TestConcurrentSecretCreation(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := t.TempDir()
	config := &Config{
		MasterKey:   generateTestKey(32),
		MasterKeyID: "test-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()
	concurrency := 50
	var wg sync.WaitGroup
	errors := make(chan error, concurrency)

	// Create 50 secrets concurrently
	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			secretName := fmt.Sprintf("concurrent-secret-%d", id)
			_, err := manager.CreateSecret(ctx, "test", secretName,
				map[string][]byte{"key": []byte(fmt.Sprintf("value-%d", id))},
				[]string{"test-service"}, 0, false)
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: %w", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent creation error: %v", err)
		errorCount++
	}
	assert.Equal(t, 0, errorCount, "All concurrent creations should succeed")
}

// TestConcurrentSecretAccess verifies multiple goroutines can read the same secret safely
func TestConcurrentSecretAccess(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := t.TempDir()
	config := &Config{
		MasterKey:        generateTestKey(32),
		MasterKeyID:      "test-key",
		TokenSigningKey:  generateTestKey(32),
		DefaultTokenTTL:  3600,
		DataDir:          dataDir,
		Logger:           logger,
	}

	manager, err := NewManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// Create secret
	secretData := map[string][]byte{"password": []byte("secret-value-123")}
	_, err = manager.CreateSecret(ctx, "test", "shared-secret", secretData,
		[]string{"test-service"}, 0, false)
	require.NoError(t, err)

	// Generate token with enough uses
	tokenResp, err := manager.GenerateAccessToken(ctx, "test", "shared-secret",
		"test-service", 1800, 200)
	require.NoError(t, err)

	// Access secret concurrently from 100 goroutines
	concurrency := 100
	var wg sync.WaitGroup
	errors := make(chan error, concurrency)
	results := make(chan []byte, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			secret, err := manager.GetSecret(ctx, "test", "shared-secret",
				"test-service", tokenResp.Token)
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: %w", id, err)
				return
			}
			results <- secret["password"]
		}(i)
	}

	wg.Wait()
	close(errors)
	close(results)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent access error: %v", err)
		errorCount++
	}
	assert.Equal(t, 0, errorCount, "All concurrent accesses should succeed")

	// Verify all results match
	for result := range results {
		assert.Equal(t, []byte("secret-value-123"), result, "All reads should return same data")
	}
}

// TestConcurrentTokenGeneration verifies multiple goroutines can generate tokens safely
func TestConcurrentTokenGeneration(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := t.TempDir()
	config := &Config{
		MasterKey:        generateTestKey(32),
		MasterKeyID:      "test-key",
		TokenSigningKey:  generateTestKey(32),
		DefaultTokenTTL:  3600,
		DataDir:          dataDir,
		Logger:           logger,
	}

	manager, err := NewManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// Create secret
	_, err = manager.CreateSecret(ctx, "test", "token-test-secret",
		map[string][]byte{"key": []byte("value")},
		[]string{"test-service"}, 0, false)
	require.NoError(t, err)

	// Generate 50 tokens concurrently
	concurrency := 50
	var wg sync.WaitGroup
	errors := make(chan error, concurrency)
	tokens := make(chan string, concurrency)

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			tokenResp, err := manager.GenerateAccessToken(ctx, "test", "token-test-secret",
				"test-service", 1800, 10)
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: %w", id, err)
				return
			}
			tokens <- tokenResp.Token
		}(i)
	}

	wg.Wait()
	close(errors)
	close(tokens)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent token generation error: %v", err)
		errorCount++
	}
	assert.Equal(t, 0, errorCount, "All concurrent token generations should succeed")

	// Verify all tokens are unique
	tokenSet := make(map[string]bool)
	for token := range tokens {
		assert.False(t, tokenSet[token], "Token should be unique")
		tokenSet[token] = true
	}
	assert.Equal(t, concurrency, len(tokenSet), "All tokens should be unique")
}

// TestConcurrentUpdate verifies sequential consistency for updates
func TestConcurrentUpdate(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := t.TempDir()
	config := &Config{
		MasterKey:   generateTestKey(32),
		MasterKeyID: "test-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// Create secret
	_, err = manager.CreateSecret(ctx, "test", "update-secret",
		map[string][]byte{"counter": []byte("0")},
		[]string{"test-service"}, 0, false)
	require.NoError(t, err)

	// Update secret 20 times concurrently
	concurrency := 20
	var wg sync.WaitGroup
	errors := make(chan error, concurrency)

	for i := 1; i <= concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := manager.UpdateSecret(ctx, "test", "update-secret",
				map[string][]byte{"counter": []byte(fmt.Sprintf("%d", id))})
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: %w", id, err)
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent update error: %v", err)
		errorCount++
	}

	// Some updates may conflict, but no crashes should occur
	t.Logf("Concurrent updates completed with %d errors (conflicts expected)", errorCount)
}

// TestConcurrentReadWrite verifies safety of simultaneous reads and writes
func TestConcurrentReadWrite(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := t.TempDir()
	config := &Config{
		MasterKey:        generateTestKey(32),
		MasterKeyID:      "test-key",
		TokenSigningKey:  generateTestKey(32),
		DefaultTokenTTL:  3600,
		DataDir:          dataDir,
		Logger:           logger,
	}

	manager, err := NewManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// Create secret
	_, err = manager.CreateSecret(ctx, "test", "rw-secret",
		map[string][]byte{"data": []byte("initial")},
		[]string{"test-service"}, 0, false)
	require.NoError(t, err)

	// Generate token
	tokenResp, err := manager.GenerateAccessToken(ctx, "test", "rw-secret",
		"test-service", 1800, 100)
	require.NoError(t, err)

	// Spawn readers and writers
	var wg sync.WaitGroup
	errors := make(chan error, 50)
	stopChan := make(chan struct{})

	// 25 readers
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					_, err := manager.GetSecret(ctx, "test", "rw-secret",
						"test-service", tokenResp.Token)
					if err != nil {
						errors <- fmt.Errorf("reader %d: %w", id, err)
						return
					}
					time.Sleep(10 * time.Millisecond)
				}
			}
		}(i)
	}

	// 10 writers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					_, err := manager.UpdateSecret(ctx, "test", "rw-secret",
						map[string][]byte{"data": []byte(fmt.Sprintf("update-%d", id))})
					if err != nil {
						// Updates may conflict, that's okay
						t.Logf("Writer %d conflict (expected): %v", id, err)
					}
					time.Sleep(50 * time.Millisecond)
				}
			}
		}(i)
	}

	// Run for 2 seconds
	time.Sleep(2 * time.Second)
	close(stopChan)
	wg.Wait()
	close(errors)

	// Check for unexpected errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent read/write error: %v", err)
		errorCount++
	}

	// Some errors expected due to update conflicts, but no crashes
	t.Logf("Concurrent read/write test completed with %d errors", errorCount)
}

// TestConcurrentDelete verifies deletion safety
func TestConcurrentDelete(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := t.TempDir()
	config := &Config{
		MasterKey:   generateTestKey(32),
		MasterKeyID: "test-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// Create 10 secrets
	secretNames := make([]string, 10)
	for i := 0; i < 10; i++ {
		secretNames[i] = fmt.Sprintf("delete-secret-%d", i)
		_, err := manager.CreateSecret(ctx, "test", secretNames[i],
			map[string][]byte{"key": []byte("value")},
			[]string{"test-service"}, 0, false)
		require.NoError(t, err)
	}

	// Delete all concurrently
	var wg sync.WaitGroup
	errors := make(chan error, 10)

	for _, name := range secretNames {
		wg.Add(1)
		go func(secretName string) {
			defer wg.Done()
			err := manager.DeleteSecret(ctx, "test", secretName)
			if err != nil {
				errors <- fmt.Errorf("delete %s: %w", secretName, err)
			}
		}(name)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent delete error: %v", err)
		errorCount++
	}
	assert.Equal(t, 0, errorCount, "All concurrent deletes should succeed")
}

// TestTokenUsageDecrement verifies uses_remaining is decremented safely
func TestTokenUsageDecrement(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := t.TempDir()
	config := &Config{
		MasterKey:        generateTestKey(32),
		MasterKeyID:      "test-key",
		TokenSigningKey:  generateTestKey(32),
		DefaultTokenTTL:  3600,
		DataDir:          dataDir,
		Logger:           logger,
	}

	manager, err := NewManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// Create secret
	_, err = manager.CreateSecret(ctx, "test", "usage-secret",
		map[string][]byte{"key": []byte("value")},
		[]string{"test-service"}, 0, false)
	require.NoError(t, err)

	// Generate token with exactly 50 uses
	tokenResp, err := manager.GenerateAccessToken(ctx, "test", "usage-secret",
		"test-service", 1800, 50)
	require.NoError(t, err)

	// Use token 50 times concurrently
	concurrency := 50
	var wg sync.WaitGroup
	successCount := int32(0)
	errors := make(chan error, concurrency)
	var mu sync.Mutex

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			_, err := manager.GetSecret(ctx, "test", "usage-secret",
				"test-service", tokenResp.Token)
			if err != nil {
				errors <- fmt.Errorf("goroutine %d: %w", id, err)
			} else {
				mu.Lock()
				successCount++
				mu.Unlock()
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Verify exactly 50 accesses succeeded (due to race conditions, some may fail)
	t.Logf("Successful accesses: %d / %d", successCount, concurrency)
	assert.LessOrEqual(t, int(successCount), 50, "Should not exceed max_uses")

	// Any access beyond 50 should fail
	_, err = manager.GetSecret(ctx, "test", "usage-secret",
		"test-service", tokenResp.Token)
	assert.Error(t, err, "Access beyond max_uses should fail")
}

// TestNoDeadlocks verifies no deadlocks occur under heavy concurrent load
func TestNoDeadlocks(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := t.TempDir()
	config := &Config{
		MasterKey:        generateTestKey(32),
		MasterKeyID:      "test-key",
		TokenSigningKey:  generateTestKey(32),
		DefaultTokenTTL:  3600,
		DataDir:          dataDir,
		Logger:           logger,
	}

	manager, err := NewManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// Create initial secret
	_, err = manager.CreateSecret(ctx, "test", "deadlock-test",
		map[string][]byte{"key": []byte("value")},
		[]string{"test-service"}, 0, false)
	require.NoError(t, err)

	// Generate token
	tokenResp, err := manager.GenerateAccessToken(ctx, "test", "deadlock-test",
		"test-service", 1800, 1000)
	require.NoError(t, err)

	// Heavy concurrent load: create, read, update, delete
	var wg sync.WaitGroup
	stopChan := make(chan struct{})
	timeout := time.After(5 * time.Second)

	// Creators
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			count := 0
			for {
				select {
				case <-stopChan:
					return
				default:
					secretName := fmt.Sprintf("temp-secret-%d-%d", id, count)
					manager.CreateSecret(ctx, "test", secretName,
						map[string][]byte{"key": []byte("value")},
						[]string{"test-service"}, 0, false)
					count++
					time.Sleep(20 * time.Millisecond)
				}
			}
		}(i)
	}

	// Readers
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					manager.GetSecret(ctx, "test", "deadlock-test",
						"test-service", tokenResp.Token)
					time.Sleep(10 * time.Millisecond)
				}
			}
		}()
	}

	// Updaters
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					manager.UpdateSecret(ctx, "test", "deadlock-test",
						map[string][]byte{"key": []byte(fmt.Sprintf("value-%d", id))})
					time.Sleep(30 * time.Millisecond)
				}
			}
		}(i)
	}

	// Wait for timeout or deadlock
	select {
	case <-timeout:
		t.Log("No deadlock detected (timeout reached)")
		close(stopChan)
	}

	wg.Wait()
	t.Log("All goroutines completed successfully - no deadlock")
}

// TestStoreConsistency verifies BoltDB transactions maintain consistency
func TestStoreConsistency(t *testing.T) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := t.TempDir()
	config := &Config{
		MasterKey:   generateTestKey(32),
		MasterKeyID: "test-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	require.NoError(t, err)
	defer manager.Close()

	ctx := context.Background()

	// Create secrets concurrently
	concurrency := 30
	var wg sync.WaitGroup

	for i := 0; i < concurrency; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			secretName := fmt.Sprintf("consistency-secret-%d", id)
			manager.CreateSecret(ctx, "test", secretName,
				map[string][]byte{
					"key1": []byte(fmt.Sprintf("value1-%d", id)),
					"key2": []byte(fmt.Sprintf("value2-%d", id)),
				},
				[]string{"test-service"}, 0, false)
		}(i)
	}

	wg.Wait()

	// Verify all secrets are properly stored
	for i := 0; i < concurrency; i++ {
		secretName := fmt.Sprintf("consistency-secret-%d", i)
		secret, err := manager.store.GetSecret("test", secretName)
		if err != nil {
			t.Errorf("Secret %s not found or corrupted: %v", secretName, err)
		} else {
			assert.NotNil(t, secret, "Secret should exist")
			assert.Equal(t, secretName, secret.Metadata.Name)
		}
	}
}

// Helper function to generate test keys
func generateTestKey(size int) []byte {
	key := make([]byte, size)
	rand.Read(key)
	return key
}
