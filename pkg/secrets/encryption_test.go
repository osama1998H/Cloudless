package secrets

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"io"
	"testing"
	"time"
)

// TestGenerateMasterKey tests master key generation
func TestGenerateMasterKey(t *testing.T) {
	key, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("GenerateMasterKey() error = %v", err)
	}

	// Verify key properties
	if key == nil {
		t.Fatal("GenerateMasterKey() returned nil key")
	}
	if key.ID == "" {
		t.Error("Master key ID is empty")
	}
	if len(key.Key) != DataKeySize {
		t.Errorf("Master key size = %d, want %d", len(key.Key), DataKeySize)
	}
	if key.Algorithm != AlgorithmAES256GCM {
		t.Errorf("Master key algorithm = %s, want %s", key.Algorithm, AlgorithmAES256GCM)
	}
	if !key.Active {
		t.Error("Newly generated key should be active")
	}
	if key.CreatedAt.IsZero() {
		t.Error("Master key CreatedAt is zero")
	}
}

// TestGenerateMasterKey_Uniqueness tests that generated keys are unique
func TestGenerateMasterKey_Uniqueness(t *testing.T) {
	keys := make(map[string]bool)
	iterations := 100

	for i := 0; i < iterations; i++ {
		key, err := GenerateMasterKey()
		if err != nil {
			t.Fatalf("GenerateMasterKey() iteration %d error = %v", i, err)
		}

		keyHex := hex.EncodeToString(key.Key)
		if keys[keyHex] {
			t.Errorf("Duplicate key generated at iteration %d", i)
		}
		keys[keyHex] = true

		// IDs should also be unique
		if keys[key.ID] {
			t.Errorf("Duplicate key ID generated at iteration %d", i)
		}
		keys[key.ID] = true
	}
}

// TestGenerateKeyID tests key ID generation
func TestGenerateKeyID(t *testing.T) {
	id := GenerateKeyID()
	if id == "" {
		t.Error("GenerateKeyID() returned empty string")
	}
	if len(id) < 10 {
		t.Errorf("GenerateKeyID() = %s, too short (len=%d)", id, len(id))
	}
}

// TestRotateMasterKey tests master key rotation
func TestRotateMasterKey(t *testing.T) {
	oldKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate old key: %v", err)
	}

	newKey, err := RotateMasterKey(oldKey)
	if err != nil {
		t.Fatalf("RotateMasterKey() error = %v", err)
	}

	// Verify new key properties
	if newKey.ID == oldKey.ID {
		t.Error("New key ID should be different from old key ID")
	}
	if bytes.Equal(newKey.Key, oldKey.Key) {
		t.Error("New key material should be different from old key")
	}
	if !newKey.Active {
		t.Error("New key should be active")
	}
	if oldKey.Active {
		t.Error("Old key should be marked inactive after rotation")
	}
	if oldKey.RotatedAt.IsZero() {
		t.Error("Old key RotatedAt timestamp should be set")
	}
}

// TestValidateMasterKey tests master key validation
func TestValidateMasterKey(t *testing.T) {
	tests := []struct {
		name    string
		key     *MasterKey
		wantErr bool
	}{
		{
			name: "valid key",
			key: &MasterKey{
				ID:        "test-key-1",
				Algorithm: AlgorithmAES256GCM,
				Key:       make([]byte, DataKeySize),
				Active:    true,
			},
			wantErr: false,
		},
		{
			name:    "nil key",
			key:     nil,
			wantErr: true,
		},
		{
			name: "empty ID",
			key: &MasterKey{
				ID:        "",
				Algorithm: AlgorithmAES256GCM,
				Key:       make([]byte, DataKeySize),
			},
			wantErr: true,
		},
		{
			name: "wrong key size",
			key: &MasterKey{
				ID:        "test-key-2",
				Algorithm: AlgorithmAES256GCM,
				Key:       make([]byte, 16), // 16 bytes instead of 32
			},
			wantErr: true,
		},
		{
			name: "unsupported algorithm",
			key: &MasterKey{
				ID:        "test-key-3",
				Algorithm: "AES-128-CBC",
				Key:       make([]byte, DataKeySize),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMasterKey(tt.key)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateMasterKey() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestEnvelopeEncryptor_EncryptDecrypt tests basic encryption and decryption
func TestEnvelopeEncryptor_EncryptDecrypt(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	encryptor, err := NewEnvelopeEncryptor(masterKey)
	if err != nil {
		t.Fatalf("NewEnvelopeEncryptor() error = %v", err)
	}

	tests := []struct {
		name      string
		plaintext []byte
		wantErr   bool
	}{
		{
			name:      "small data",
			plaintext: []byte("hello world"),
			wantErr:   false,
		},
		{
			name:      "single byte",
			plaintext: []byte("x"),
			wantErr:   false,
		},
		{
			name:      "binary data",
			plaintext: []byte{0x00, 0x01, 0x02, 0xFF, 0xFE, 0xFD},
			wantErr:   false,
		},
		{
			name:      "large data",
			plaintext: make([]byte, 10*1024), // 10KB
			wantErr:   false,
		},
		{
			name:      "unicode data",
			plaintext: []byte("Hello 世界 🔐"),
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Encrypt
			ciphertext, encryptedDataKey, nonce, err := encryptor.Encrypt(tt.plaintext)
			if err != nil {
				t.Fatalf("Encrypt() error = %v", err)
			}

			// Verify nonce size
			if len(nonce) != NonceSize {
				t.Errorf("Nonce size = %d, want %d", len(nonce), NonceSize)
			}

			// Verify encrypted data key is not empty
			if len(encryptedDataKey) == 0 {
				t.Error("Encrypted data key is empty")
			}

			// Verify ciphertext is different from plaintext (except for empty)
			if len(tt.plaintext) > 0 && bytes.Equal(ciphertext, tt.plaintext) {
				t.Error("Ciphertext should be different from plaintext")
			}

			// Decrypt
			decrypted, err := encryptor.Decrypt(ciphertext, encryptedDataKey, nonce)
			if err != nil {
				t.Fatalf("Decrypt() error = %v", err)
			}

			// Verify decrypted matches original
			if !bytes.Equal(decrypted, tt.plaintext) {
				t.Errorf("Decrypted data doesn't match original.\nGot:  %v\nWant: %v",
					decrypted, tt.plaintext)
			}
		})
	}
}

// TestEnvelopeEncryptor_EmptyData tests that empty data is rejected
func TestEnvelopeEncryptor_EmptyData(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	encryptor, err := NewEnvelopeEncryptor(masterKey)
	if err != nil {
		t.Fatalf("NewEnvelopeEncryptor() error = %v", err)
	}

	// Test encryption with empty data
	_, _, _, err = encryptor.Encrypt([]byte(""))
	if err == nil {
		t.Error("Encrypt() with empty data should fail")
	}

	// Test decryption with empty encrypted data
	validCiphertext, encryptedDataKey, nonce, _ := encryptor.Encrypt([]byte("test"))
	_, err = encryptor.Decrypt([]byte(""), encryptedDataKey, nonce)
	if err == nil {
		t.Error("Decrypt() with empty encrypted data should fail")
	}

	// Test decryption with empty encrypted data key
	_, err = encryptor.Decrypt(validCiphertext, []byte(""), nonce)
	if err == nil {
		t.Error("Decrypt() with empty encrypted data key should fail")
	}
}

// TestEnvelopeEncryptor_NonceUniqueness tests that nonces are unique
func TestEnvelopeEncryptor_NonceUniqueness(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	encryptor, err := NewEnvelopeEncryptor(masterKey)
	if err != nil {
		t.Fatalf("NewEnvelopeEncryptor() error = %v", err)
	}

	plaintext := []byte("test data")
	nonces := make(map[string]bool)
	iterations := 1000

	for i := 0; i < iterations; i++ {
		_, _, nonce, err := encryptor.Encrypt(plaintext)
		if err != nil {
			t.Fatalf("Encrypt() iteration %d error = %v", i, err)
		}

		nonceHex := hex.EncodeToString(nonce)
		if nonces[nonceHex] {
			t.Fatalf("Duplicate nonce found at iteration %d: %s", i, nonceHex)
		}
		nonces[nonceHex] = true
	}
}

// TestEnvelopeEncryptor_DecryptWithWrongKey tests decryption with wrong key fails
func TestEnvelopeEncryptor_DecryptWithWrongKey(t *testing.T) {
	// Encrypt with first key
	key1, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate key1: %v", err)
	}

	encryptor1, err := NewEnvelopeEncryptor(key1)
	if err != nil {
		t.Fatalf("NewEnvelopeEncryptor(key1) error = %v", err)
	}

	plaintext := []byte("secret data")
	ciphertext, encryptedDataKey, nonce, err := encryptor1.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Try to decrypt with different key
	key2, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate key2: %v", err)
	}

	encryptor2, err := NewEnvelopeEncryptor(key2)
	if err != nil {
		t.Fatalf("NewEnvelopeEncryptor(key2) error = %v", err)
	}

	_, err = encryptor2.Decrypt(ciphertext, encryptedDataKey, nonce)
	if err == nil {
		t.Error("Decrypt() with wrong key should fail")
	}
}

// TestEnvelopeEncryptor_DecryptWithCorruptedData tests decryption with corrupted data fails
func TestEnvelopeEncryptor_DecryptWithCorruptedData(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	encryptor, err := NewEnvelopeEncryptor(masterKey)
	if err != nil {
		t.Fatalf("NewEnvelopeEncryptor() error = %v", err)
	}

	plaintext := []byte("secret data")
	ciphertext, encryptedDataKey, nonce, err := encryptor.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	tests := []struct {
		name             string
		ciphertext       []byte
		encryptedDataKey []byte
		nonce            []byte
	}{
		{
			name:             "corrupted ciphertext",
			ciphertext:       append([]byte(nil), append(ciphertext[:len(ciphertext)/2], 0xFF)...),
			encryptedDataKey: encryptedDataKey,
			nonce:            nonce,
		},
		{
			name:             "corrupted encrypted data key",
			ciphertext:       ciphertext,
			encryptedDataKey: append([]byte(nil), append(encryptedDataKey[:len(encryptedDataKey)/2], 0xFF)...),
			nonce:            nonce,
		},
		{
			name:             "wrong nonce",
			ciphertext:       ciphertext,
			encryptedDataKey: encryptedDataKey,
			nonce:            make([]byte, NonceSize),
		},
		{
			name:             "short nonce",
			ciphertext:       ciphertext,
			encryptedDataKey: encryptedDataKey,
			nonce:            nonce[:NonceSize-1],
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := encryptor.Decrypt(tt.ciphertext, tt.encryptedDataKey, tt.nonce)
			if err == nil {
				t.Error("Decrypt() with corrupted data should fail")
			}
		})
	}
}

// TestReEncryptWithNewKey tests re-encryption during key rotation
func TestReEncryptWithNewKey(t *testing.T) {
	// Generate two master keys
	oldKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate old key: %v", err)
	}

	newKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate new key: %v", err)
	}

	// Encrypt with old key
	oldEncryptor, err := NewEnvelopeEncryptor(oldKey)
	if err != nil {
		t.Fatalf("NewEnvelopeEncryptor(oldKey) error = %v", err)
	}

	plaintext := []byte("sensitive data to rotate")
	ciphertext, encryptedDataKey, nonce, err := oldEncryptor.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() with old key error = %v", err)
	}

	// Re-encrypt with new key
	newCiphertext, newEncryptedDataKey, newNonce, err := ReEncryptWithNewKey(
		ciphertext,
		encryptedDataKey,
		nonce,
		oldKey,
		newKey,
	)
	if err != nil {
		t.Fatalf("ReEncryptWithNewKey() error = %v", err)
	}

	// Verify new nonce was generated
	if len(newNonce) != NonceSize {
		t.Errorf("New nonce size = %d, want %d", len(newNonce), NonceSize)
	}

	// Verify re-encrypted data is different
	if bytes.Equal(newCiphertext, ciphertext) {
		t.Error("Re-encrypted ciphertext should be different from original")
	}
	if bytes.Equal(newEncryptedDataKey, encryptedDataKey) {
		t.Error("Re-encrypted data key should be different from original")
	}

	// Decrypt with new key and verify plaintext matches
	newEncryptor, err := NewEnvelopeEncryptor(newKey)
	if err != nil {
		t.Fatalf("NewEnvelopeEncryptor(newKey) error = %v", err)
	}

	decryptedPlaintext, err := newEncryptor.Decrypt(newCiphertext, newEncryptedDataKey, newNonce)
	if err != nil {
		t.Fatalf("Decrypt() with new key error = %v", err)
	}

	if !bytes.Equal(decryptedPlaintext, plaintext) {
		t.Errorf("Decrypted plaintext doesn't match original: got %s, want %s",
			string(decryptedPlaintext), string(plaintext))
	}
}

// TestReEncryptWithNewKey_WithWrongOldKey tests that re-encryption fails with wrong old key
func TestReEncryptWithNewKey_WithWrongOldKey(t *testing.T) {
	// Generate three master keys
	oldKey, _ := GenerateMasterKey()
	wrongKey, _ := GenerateMasterKey()
	newKey, _ := GenerateMasterKey()

	// Encrypt with old key
	oldEncryptor, _ := NewEnvelopeEncryptor(oldKey)
	plaintext := []byte("test data")
	ciphertext, encryptedDataKey, nonce, _ := oldEncryptor.Encrypt(plaintext)

	// Try to re-encrypt with wrong old key
	_, _, _, err := ReEncryptWithNewKey(
		ciphertext,
		encryptedDataKey,
		nonce,
		wrongKey, // Wrong key!
		newKey,
	)
	if err == nil {
		t.Error("ReEncryptWithNewKey() with wrong old key should fail")
	}
}

// TestNewEnvelopeEncryptor_WithInvalidKey tests encryptor creation with invalid key
func TestNewEnvelopeEncryptor_WithInvalidKey(t *testing.T) {
	tests := []struct {
		name string
		key  *MasterKey
	}{
		{
			name: "nil key",
			key:  nil,
		},
		{
			name: "wrong size key",
			key: &MasterKey{
				ID:        "test",
				Algorithm: AlgorithmAES256GCM,
				Key:       make([]byte, 16),
			},
		},
		{
			name: "empty key material",
			key: &MasterKey{
				ID:        "test",
				Algorithm: AlgorithmAES256GCM,
				Key:       nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewEnvelopeEncryptor(tt.key)
			if err == nil {
				t.Error("NewEnvelopeEncryptor() with invalid key should fail")
			}
		})
	}
}

// TestEncryption_MaxSecretSize tests encryption with very large data
func TestEncryption_MaxSecretSize(t *testing.T) {
	masterKey, err := GenerateMasterKey()
	if err != nil {
		t.Fatalf("Failed to generate master key: %v", err)
	}

	encryptor, err := NewEnvelopeEncryptor(masterKey)
	if err != nil {
		t.Fatalf("NewEnvelopeEncryptor() error = %v", err)
	}

	// Test data at max size (1MB)
	plaintext := make([]byte, MaxSecretSize)
	if _, err := io.ReadFull(rand.Reader, plaintext); err != nil {
		t.Fatalf("Failed to generate random data: %v", err)
	}

	ciphertext, encryptedDataKey, nonce, err := encryptor.Encrypt(plaintext)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}

	// Decrypt and verify
	decrypted, err := encryptor.Decrypt(ciphertext, encryptedDataKey, nonce)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}

	if !bytes.Equal(decrypted, plaintext) {
		t.Error("Decrypted large data doesn't match original")
	}
}

// TestGetCurrentTime tests the mockable time function
func TestGetCurrentTime(t *testing.T) {
	before := time.Now()
	current := getCurrentTime()
	after := time.Now()

	if current.Before(before) || current.After(after) {
		t.Errorf("getCurrentTime() = %v, should be between %v and %v",
			current, before, after)
	}
}
