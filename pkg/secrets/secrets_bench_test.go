//go:build benchmark

package secrets

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"

	"go.uber.org/zap"
)

// BenchmarkCreateSecret measures secret creation performance
func BenchmarkCreateSecret(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:   generateBenchKey(32),
		MasterKeyID: "bench-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	ctx := context.Background()
	secretData := map[string][]byte{
		"key1": []byte("value1-data"),
		"key2": []byte("value2-data"),
		"key3": []byte("value3-data"),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		secretName := fmt.Sprintf("bench-secret-%d", i)
		_, err := manager.CreateSecret(ctx, "benchmark", secretName, secretData,
			[]string{"benchmark-service"}, 0, false)
		if err != nil {
			b.Fatalf("CreateSecret failed: %v", err)
		}
	}
}

// BenchmarkCreateSecretWithLargeData measures performance with larger payloads
func BenchmarkCreateSecretWithLargeData(b *testing.B) {
	sizes := []int{
		1024,       // 1 KB
		10 * 1024,  // 10 KB
		100 * 1024, // 100 KB
	}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size%dKB", size/1024), func(b *testing.B) {
			logger, _ := zap.NewDevelopment()
			defer logger.Sync()

			dataDir := b.TempDir()
			config := &Config{
				MasterKey:   generateBenchKey(32),
				MasterKeyID: "bench-key",
				DataDir:     dataDir,
				Logger:      logger,
			}

			manager, err := NewManager(config)
			if err != nil {
				b.Fatal(err)
			}
			defer manager.Close()

			ctx := context.Background()
			largeData := make([]byte, size)
			rand.Read(largeData)

			secretData := map[string][]byte{
				"large-key": largeData,
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				secretName := fmt.Sprintf("large-secret-%d", i)
				_, err := manager.CreateSecret(ctx, "benchmark", secretName, secretData,
					[]string{"benchmark-service"}, 0, false)
				if err != nil {
					b.Fatalf("CreateSecret failed: %v", err)
				}
			}

			b.SetBytes(int64(size))
		})
	}
}

// BenchmarkGetSecret measures secret retrieval performance
func BenchmarkGetSecret(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:       generateBenchKey(32),
		MasterKeyID:     "bench-key",
		TokenSigningKey: generateBenchKey(32),
		DefaultTokenTTL: 3600,
		DataDir:         dataDir,
		Logger:          logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	ctx := context.Background()

	// Create secret
	secretData := map[string][]byte{
		"key1": []byte("value1"),
		"key2": []byte("value2"),
	}
	_, err = manager.CreateSecret(ctx, "benchmark", "test-secret", secretData,
		[]string{"benchmark-service"}, 0, false)
	if err != nil {
		b.Fatal(err)
	}

	// Generate token
	tokenResp, err := manager.GenerateAccessToken(ctx, "benchmark", "test-secret",
		"benchmark-service", 3600, 1000000)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := manager.GetSecret(ctx, "benchmark", "test-secret",
			"benchmark-service", tokenResp.Token)
		if err != nil {
			b.Fatalf("GetSecret failed: %v", err)
		}
	}
}

// BenchmarkGetSecretWithLargeData measures retrieval performance with large payloads
func BenchmarkGetSecretWithLargeData(b *testing.B) {
	sizes := []int{
		1024,       // 1 KB
		10 * 1024,  // 10 KB
		100 * 1024, // 100 KB
	}

	for _, size := range sizes {
		b.Run(fmt.Sprintf("Size%dKB", size/1024), func(b *testing.B) {
			logger, _ := zap.NewDevelopment()
			defer logger.Sync()

			dataDir := b.TempDir()
			config := &Config{
				MasterKey:       generateBenchKey(32),
				MasterKeyID:     "bench-key",
				TokenSigningKey: generateBenchKey(32),
				DefaultTokenTTL: 3600,
				DataDir:         dataDir,
				Logger:          logger,
			}

			manager, err := NewManager(config)
			if err != nil {
				b.Fatal(err)
			}
			defer manager.Close()

			ctx := context.Background()

			// Create secret with large data
			largeData := make([]byte, size)
			rand.Read(largeData)
			secretData := map[string][]byte{"large-key": largeData}

			_, err = manager.CreateSecret(ctx, "benchmark", "large-secret", secretData,
				[]string{"benchmark-service"}, 0, false)
			if err != nil {
				b.Fatal(err)
			}

			// Generate token
			tokenResp, err := manager.GenerateAccessToken(ctx, "benchmark", "large-secret",
				"benchmark-service", 3600, 1000000)
			if err != nil {
				b.Fatal(err)
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := manager.GetSecret(ctx, "benchmark", "large-secret",
					"benchmark-service", tokenResp.Token)
				if err != nil {
					b.Fatalf("GetSecret failed: %v", err)
				}
			}

			b.SetBytes(int64(size))
		})
	}
}

// BenchmarkGenerateAccessToken measures token generation performance
func BenchmarkGenerateAccessToken(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:       generateBenchKey(32),
		MasterKeyID:     "bench-key",
		TokenSigningKey: generateBenchKey(32),
		DefaultTokenTTL: 3600,
		DataDir:         dataDir,
		Logger:          logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	ctx := context.Background()

	// Create secret
	_, err = manager.CreateSecret(ctx, "benchmark", "token-secret",
		map[string][]byte{"key": []byte("value")},
		[]string{"benchmark-service"}, 0, false)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := manager.GenerateAccessToken(ctx, "benchmark", "token-secret",
			"benchmark-service", 1800, 100)
		if err != nil {
			b.Fatalf("GenerateAccessToken failed: %v", err)
		}
	}
}

// BenchmarkUpdateSecret measures secret update performance
func BenchmarkUpdateSecret(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:   generateBenchKey(32),
		MasterKeyID: "bench-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	ctx := context.Background()

	// Create secret
	_, err = manager.CreateSecret(ctx, "benchmark", "update-secret",
		map[string][]byte{"key": []byte("value")},
		[]string{"benchmark-service"}, 0, false)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := manager.UpdateSecret(ctx, "benchmark", "update-secret",
			map[string][]byte{"key": []byte(fmt.Sprintf("value-%d", i))})
		if err != nil {
			b.Fatalf("UpdateSecret failed: %v", err)
		}
	}
}

// BenchmarkDeleteSecret measures secret deletion performance
func BenchmarkDeleteSecret(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:   generateBenchKey(32),
		MasterKeyID: "bench-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	ctx := context.Background()

	// Pre-create secrets for deletion
	for i := 0; i < b.N; i++ {
		secretName := fmt.Sprintf("delete-secret-%d", i)
		_, err := manager.CreateSecret(ctx, "benchmark", secretName,
			map[string][]byte{"key": []byte("value")},
			[]string{"benchmark-service"}, 0, false)
		if err != nil {
			b.Fatal(err)
		}
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		secretName := fmt.Sprintf("delete-secret-%d", i)
		err := manager.DeleteSecret(ctx, "benchmark", secretName)
		if err != nil {
			b.Fatalf("DeleteSecret failed: %v", err)
		}
	}
}

// BenchmarkListSecrets measures secret listing performance
func BenchmarkListSecrets(b *testing.B) {
	secretCounts := []int{10, 100, 1000}

	for _, count := range secretCounts {
		b.Run(fmt.Sprintf("Count%d", count), func(b *testing.B) {
			logger, _ := zap.NewDevelopment()
			defer logger.Sync()

			dataDir := b.TempDir()
			config := &Config{
				MasterKey:   generateBenchKey(32),
				MasterKeyID: "bench-key",
				DataDir:     dataDir,
				Logger:      logger,
			}

			manager, err := NewManager(config)
			if err != nil {
				b.Fatal(err)
			}
			defer manager.Close()

			ctx := context.Background()

			// Create secrets
			for i := 0; i < count; i++ {
				secretName := fmt.Sprintf("list-secret-%d", i)
				_, err := manager.CreateSecret(ctx, "benchmark", secretName,
					map[string][]byte{"key": []byte("value")},
					[]string{"benchmark-service"}, 0, false)
				if err != nil {
					b.Fatal(err)
				}
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := manager.ListSecrets(ctx, "benchmark", 1000, "")
				if err != nil {
					b.Fatalf("ListSecrets failed: %v", err)
				}
			}
		})
	}
}

// BenchmarkEncryption measures raw encryption performance
func BenchmarkEncryption(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:   generateBenchKey(32),
		MasterKeyID: "bench-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	plaintext := []byte("This is test data for encryption benchmarking")

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, _, err := manager.encrypt(plaintext)
		if err != nil {
			b.Fatalf("Encryption failed: %v", err)
		}
	}
}

// BenchmarkDecryption measures raw decryption performance
func BenchmarkDecryption(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:   generateBenchKey(32),
		MasterKeyID: "bench-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	plaintext := []byte("This is test data for decryption benchmarking")
	encryptedDEK, ciphertext, err := manager.encrypt(plaintext)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := manager.decrypt(encryptedDEK, ciphertext)
		if err != nil {
			b.Fatalf("Decryption failed: %v", err)
		}
	}
}

// BenchmarkJWTSign measures JWT signing performance
func BenchmarkJWTSign(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:       generateBenchKey(32),
		MasterKeyID:     "bench-key",
		TokenSigningKey: generateBenchKey(32),
		DefaultTokenTTL: 3600,
		DataDir:         dataDir,
		Logger:          logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := manager.createJWT("benchmark/secret", "benchmark-service", 1800, 100)
		if err != nil {
			b.Fatalf("JWT signing failed: %v", err)
		}
	}
}

// BenchmarkJWTValidate measures JWT validation performance
func BenchmarkJWTValidate(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:       generateBenchKey(32),
		MasterKeyID:     "bench-key",
		TokenSigningKey: generateBenchKey(32),
		DefaultTokenTTL: 3600,
		DataDir:         dataDir,
		Logger:          logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	// Create a token
	token, err := manager.createJWT("benchmark/secret", "benchmark-service", 1800, 100)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := manager.validateJWT(token, "benchmark/secret", "benchmark-service")
		if err != nil {
			b.Fatalf("JWT validation failed: %v", err)
		}
	}
}

// BenchmarkAuditLog measures audit logging performance
func BenchmarkAuditLog(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:   generateBenchKey(32),
		MasterKeyID: "bench-key",
		DataDir:     dataDir,
		Logger:      logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	ctx := context.Background()

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		err := manager.logAudit(ctx, "benchmark/secret", "ACCESS", "benchmark-actor", nil)
		if err != nil {
			b.Fatalf("Audit logging failed: %v", err)
		}
	}
}

// BenchmarkConcurrentAccess measures performance under concurrent load
func BenchmarkConcurrentAccess(b *testing.B) {
	logger, _ := zap.NewDevelopment()
	defer logger.Sync()

	dataDir := b.TempDir()
	config := &Config{
		MasterKey:       generateBenchKey(32),
		MasterKeyID:     "bench-key",
		TokenSigningKey: generateBenchKey(32),
		DefaultTokenTTL: 3600,
		DataDir:         dataDir,
		Logger:          logger,
	}

	manager, err := NewManager(config)
	if err != nil {
		b.Fatal(err)
	}
	defer manager.Close()

	ctx := context.Background()

	// Create secret
	_, err = manager.CreateSecret(ctx, "benchmark", "concurrent-secret",
		map[string][]byte{"key": []byte("value")},
		[]string{"benchmark-service"}, 0, false)
	if err != nil {
		b.Fatal(err)
	}

	// Generate token
	tokenResp, err := manager.GenerateAccessToken(ctx, "benchmark", "concurrent-secret",
		"benchmark-service", 3600, 1000000)
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, err := manager.GetSecret(ctx, "benchmark", "concurrent-secret",
				"benchmark-service", tokenResp.Token)
			if err != nil {
				b.Fatalf("GetSecret failed: %v", err)
			}
		}
	})
}

// Helper function to generate benchmark keys
func generateBenchKey(size int) []byte {
	key := make([]byte, size)
	rand.Read(key)
	return key
}
