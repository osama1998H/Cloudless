package secrets

import (
	"time"
)

// Secret represents an encrypted secret with metadata
type Secret struct {
	// Metadata
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Description string            `json:"description,omitempty"`
	Labels      map[string]string `json:"labels,omitempty"`
	Annotations map[string]string `json:"annotations,omitempty"`

	// Secret data (encrypted)
	Data map[string][]byte `json:"data"` // key -> encrypted value

	// Encryption metadata
	EncryptedDataKey []byte `json:"encrypted_data_key"` // Data key encrypted with master key
	KeyID            string `json:"key_id"`             // Master key identifier
	Algorithm        string `json:"algorithm"`          // Encryption algorithm (AES-256-GCM)

	// Access control
	Audiences []string `json:"audiences,omitempty"` // Workloads/services that can access this secret
	TTL       int64    `json:"ttl,omitempty"`       // Time-to-live for secret access tokens (seconds)

	// Versioning
	Version   uint64 `json:"version"`
	Immutable bool   `json:"immutable,omitempty"`

	// Timestamps
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	RotatedAt   *time.Time `json:"rotated_at,omitempty"`
	ExpiresAt   *time.Time `json:"expires_at,omitempty"`
	DeletedAt   *time.Time `json:"deleted_at,omitempty"`
	DeleteAfter int64      `json:"delete_after,omitempty"` // Auto-delete after N seconds
}

// SecretMetadata contains minimal secret information for listings
type SecretMetadata struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Labels    map[string]string `json:"labels,omitempty"`
	Version   uint64            `json:"version"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Keys      []string          `json:"keys"` // Available keys (not values)
}

// SecretAccessToken represents a short-lived token for accessing a secret
type SecretAccessToken struct {
	// Token metadata
	ID        string    `json:"id"`
	SecretRef string    `json:"secret_ref"` // "namespace/name"
	Audience  string    `json:"audience"`   // Workload/service that can use this token
	IssuedAt  time.Time `json:"issued_at"`
	ExpiresAt time.Time `json:"expires_at"`

	// Token value (signed JWT)
	Token string `json:"token"`

	// Usage tracking
	UsesRemaining int       `json:"uses_remaining"` // -1 for unlimited
	LastUsedAt    time.Time `json:"last_used_at,omitempty"`
}

// SecretAccessRequest contains parameters for accessing a secret
type SecretAccessRequest struct {
	SecretName  string `json:"secret_name"`
	Namespace   string `json:"namespace"`
	Audience    string `json:"audience"` // Requesting workload/service
	AccessToken string `json:"access_token,omitempty"`
}

// SecretAccessResponse contains decrypted secret data
type SecretAccessResponse struct {
	Name      string            `json:"name"`
	Namespace string            `json:"namespace"`
	Data      map[string][]byte `json:"data"` // Decrypted data
	Version   uint64            `json:"version"`
	ExpiresAt *time.Time        `json:"expires_at,omitempty"`
}

// MasterKey represents a master encryption key
type MasterKey struct {
	ID        string    `json:"id"`
	Algorithm string    `json:"algorithm"` // AES-256
	Key       []byte    `json:"key"`       // The actual key material (32 bytes for AES-256)
	Active    bool      `json:"active"`    // Only one key should be active at a time
	CreatedAt time.Time `json:"created_at"`
	RotatedAt time.Time `json:"rotated_at,omitempty"`
}

// SecretRotationPolicy defines automatic secret rotation
type SecretRotationPolicy struct {
	Enabled          bool          `json:"enabled"`
	RotationInterval time.Duration `json:"rotation_interval"` // e.g., 90 days
	RetainVersions   int           `json:"retain_versions"`   // Number of old versions to keep
	NotifyOnRotation bool          `json:"notify_on_rotation"`
}

// SecretAuditEntry records secret access and modifications
type SecretAuditEntry struct {
	Timestamp time.Time              `json:"timestamp"`
	Action    SecretAction           `json:"action"`
	SecretRef string                 `json:"secret_ref"` // "namespace/name"
	Actor     string                 `json:"actor"`      // User or service account
	Audience  string                 `json:"audience,omitempty"`
	Success   bool                   `json:"success"`
	Reason    string                 `json:"reason,omitempty"`
	Metadata  map[string]interface{} `json:"metadata,omitempty"`
}

// SecretAction represents an action performed on a secret
type SecretAction string

const (
	SecretActionCreate SecretAction = "create"
	SecretActionRead   SecretAction = "read"
	SecretActionUpdate SecretAction = "update"
	SecretActionDelete SecretAction = "delete"
	SecretActionRotate SecretAction = "rotate"
	SecretActionAccess SecretAction = "access"
)

// ManagerConfig contains configuration for the secrets manager
type ManagerConfig struct {
	// Master key configuration
	MasterKeyID        string
	MasterKey          []byte
	EnableAutoRotation bool
	RotationInterval   time.Duration

	// Token configuration
	TokenTTL            time.Duration
	TokenSigningKey     []byte
	MaxTokenUsesDefault int

	// Storage
	DataDir string

	// Audit
	EnableAuditLog bool
	AuditRetention time.Duration
}

// DefaultManagerConfig returns default secrets manager configuration
func DefaultManagerConfig() ManagerConfig {
	return ManagerConfig{
		MasterKeyID:         "default-master-key",
		EnableAutoRotation:  true,
		RotationInterval:    90 * 24 * time.Hour, // 90 days
		TokenTTL:            15 * time.Minute,    // 15 minutes for short-lived tokens
		MaxTokenUsesDefault: 1,                   // Single-use tokens by default
		EnableAuditLog:      true,
		AuditRetention:      90 * 24 * time.Hour, // 90 days
	}
}

// EncryptionMetadata contains metadata about the encryption
type EncryptionMetadata struct {
	Algorithm        string    `json:"algorithm"`
	KeyID            string    `json:"key_id"`
	EncryptedDataKey []byte    `json:"encrypted_data_key"`
	Nonce            []byte    `json:"nonce"` // For AES-GCM
	CreatedAt        time.Time `json:"created_at"`
}

// SecretFilter is used for querying secrets
type SecretFilter struct {
	Namespace string
	Labels    map[string]string
	Name      string // Exact name match
	Prefix    string // Name prefix match
	Limit     int
	Offset    int
}

// SecretStats contains statistics about secrets
type SecretStats struct {
	TotalSecrets        int64     `json:"total_secrets"`
	ActiveSecrets       int64     `json:"active_secrets"`
	ExpiredSecrets      int64     `json:"expired_secrets"`
	DeletedSecrets      int64     `json:"deleted_secrets"`
	TotalAccessTokens   int64     `json:"total_access_tokens"`
	ActiveAccessTokens  int64     `json:"active_access_tokens"`
	ExpiredAccessTokens int64     `json:"expired_access_tokens"`
	LastRotation        time.Time `json:"last_rotation,omitempty"`
	NextRotation        time.Time `json:"next_rotation,omitempty"`
}

// ValidationError represents a secret validation error
type ValidationError struct {
	Field   string
	Message string
}

func (e *ValidationError) Error() string {
	return e.Field + ": " + e.Message
}

// Constants for encryption
const (
	// AlgorithmAES256GCM is AES-256 in GCM mode (recommended)
	AlgorithmAES256GCM = "AES-256-GCM"

	// DataKeySize is the size of data encryption keys (32 bytes for AES-256)
	DataKeySize = 32

	// NonceSize is the size of the nonce for AES-GCM (12 bytes)
	NonceSize = 12

	// MaxSecretSize is the maximum size of a single secret value (1MB)
	MaxSecretSize = 1024 * 1024

	// MaxSecretKeys is the maximum number of keys in a secret
	MaxSecretKeys = 100

	// DefaultTokenTTL is the default TTL for secret access tokens
	DefaultTokenTTL = 15 * time.Minute

	// MaxTokenTTL is the maximum TTL for secret access tokens
	MaxTokenTTL = 24 * time.Hour
)
