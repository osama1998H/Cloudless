package secrets

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"fmt"
	"io"
)

// EnvelopeEncryptor handles envelope encryption for secrets
// Uses two-tier encryption: data keys encrypt secrets, master key encrypts data keys
type EnvelopeEncryptor struct {
	masterKey *MasterKey
}

// NewEnvelopeEncryptor creates a new envelope encryptor
func NewEnvelopeEncryptor(masterKey *MasterKey) (*EnvelopeEncryptor, error) {
	if masterKey == nil {
		return nil, fmt.Errorf("master key is required")
	}
	if len(masterKey.Key) != DataKeySize {
		return nil, fmt.Errorf("master key must be %d bytes, got %d", DataKeySize, len(masterKey.Key))
	}
	if masterKey.Algorithm != AlgorithmAES256GCM {
		return nil, fmt.Errorf("unsupported algorithm: %s", masterKey.Algorithm)
	}

	return &EnvelopeEncryptor{
		masterKey: masterKey,
	}, nil
}

// Encrypt encrypts data using envelope encryption
// Returns: encryptedData, encryptedDataKey, nonce, error
func (e *EnvelopeEncryptor) Encrypt(plaintext []byte) ([]byte, []byte, []byte, error) {
	if len(plaintext) == 0 {
		return nil, nil, nil, fmt.Errorf("plaintext cannot be empty")
	}
	if len(plaintext) > MaxSecretSize {
		return nil, nil, nil, fmt.Errorf("plaintext exceeds maximum size of %d bytes", MaxSecretSize)
	}

	// Generate a random data encryption key (DEK)
	dataKey := make([]byte, DataKeySize)
	if _, err := io.ReadFull(rand.Reader, dataKey); err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate data key: %w", err)
	}

	// Encrypt the plaintext with the data key
	encryptedData, nonce, err := e.encryptWithKey(plaintext, dataKey)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to encrypt data: %w", err)
	}

	// Encrypt the data key with the master key
	encryptedDataKey, _, err := e.encryptWithKey(dataKey, e.masterKey.Key)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to encrypt data key: %w", err)
	}

	return encryptedData, encryptedDataKey, nonce, nil
}

// Decrypt decrypts data using envelope encryption
func (e *EnvelopeEncryptor) Decrypt(encryptedData, encryptedDataKey, nonce []byte) ([]byte, error) {
	if len(encryptedData) == 0 {
		return nil, fmt.Errorf("encrypted data cannot be empty")
	}
	if len(encryptedDataKey) == 0 {
		return nil, fmt.Errorf("encrypted data key cannot be empty")
	}
	if len(nonce) != NonceSize {
		return nil, fmt.Errorf("invalid nonce size: expected %d, got %d", NonceSize, len(nonce))
	}

	// Decrypt the data key with the master key
	dataKey, err := e.decryptWithKey(encryptedDataKey, e.masterKey.Key, make([]byte, NonceSize))
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data key: %w", err)
	}

	if len(dataKey) != DataKeySize {
		return nil, fmt.Errorf("invalid data key size: expected %d, got %d", DataKeySize, len(dataKey))
	}

	// Decrypt the data with the data key
	plaintext, err := e.decryptWithKey(encryptedData, dataKey, nonce)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt data: %w", err)
	}

	return plaintext, nil
}

// encryptWithKey encrypts data using AES-256-GCM with the provided key
func (e *EnvelopeEncryptor) encryptWithKey(plaintext, key []byte) ([]byte, []byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate a random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt the plaintext
	ciphertext := gcm.Seal(nil, nonce, plaintext, nil)

	return ciphertext, nonce, nil
}

// decryptWithKey decrypts data using AES-256-GCM with the provided key
func (e *EnvelopeEncryptor) decryptWithKey(ciphertext, key, nonce []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCM: %w", err)
	}

	// Decrypt the ciphertext
	plaintext, err := gcm.Open(nil, nonce, ciphertext, nil)
	if err != nil {
		return nil, fmt.Errorf("decryption failed: %w", err)
	}

	return plaintext, nil
}

// GenerateMasterKey generates a new random master key
func GenerateMasterKey() (*MasterKey, error) {
	key := make([]byte, DataKeySize)
	if _, err := io.ReadFull(rand.Reader, key); err != nil {
		return nil, fmt.Errorf("failed to generate master key: %w", err)
	}

	return &MasterKey{
		ID:        GenerateKeyID(),
		Algorithm: AlgorithmAES256GCM,
		Key:       key,
		Active:    true,
		CreatedAt: getCurrentTime(),
	}, nil
}

// GenerateKeyID generates a unique key identifier
func GenerateKeyID() string {
	b := make([]byte, 16)
	if _, err := io.ReadFull(rand.Reader, b); err != nil {
		// Fallback to timestamp-based ID if random fails
		return fmt.Sprintf("key-%d", getCurrentTime().Unix())
	}
	return fmt.Sprintf("key-%x", b)
}

// RotateMasterKey rotates the master key by creating a new key
func RotateMasterKey(oldKey *MasterKey) (*MasterKey, error) {
	newKey, err := GenerateMasterKey()
	if err != nil {
		return nil, err
	}

	// Mark old key as inactive
	oldKey.Active = false
	oldKey.RotatedAt = getCurrentTime()

	return newKey, nil
}

// ReEncryptWithNewKey re-encrypts data with a new master key
// This is used during master key rotation
func ReEncryptWithNewKey(encryptedData, encryptedDataKey, nonce []byte, oldKey, newKey *MasterKey) ([]byte, []byte, error) {
	// Decrypt with old key
	oldEncryptor, err := NewEnvelopeEncryptor(oldKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create old encryptor: %w", err)
	}

	plaintext, err := oldEncryptor.Decrypt(encryptedData, encryptedDataKey, nonce)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to decrypt with old key: %w", err)
	}

	// Encrypt with new key
	newEncryptor, err := NewEnvelopeEncryptor(newKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create new encryptor: %w", err)
	}

	newEncryptedData, newEncryptedDataKey, _, err := newEncryptor.Encrypt(plaintext)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to encrypt with new key: %w", err)
	}

	return newEncryptedData, newEncryptedDataKey, nil
}

// ValidateMasterKey validates a master key
func ValidateMasterKey(key *MasterKey) error {
	if key == nil {
		return fmt.Errorf("master key is nil")
	}
	if key.ID == "" {
		return fmt.Errorf("master key ID is empty")
	}
	if len(key.Key) != DataKeySize {
		return fmt.Errorf("master key must be %d bytes, got %d", DataKeySize, len(key.Key))
	}
	if key.Algorithm != AlgorithmAES256GCM {
		return fmt.Errorf("unsupported algorithm: %s", key.Algorithm)
	}
	return nil
}

// getCurrentTime returns the current time (mockable for testing)
func getCurrentTime() time.Time {
	return time.Now()
}
