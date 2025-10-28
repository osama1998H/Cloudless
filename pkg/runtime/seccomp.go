package runtime

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/opencontainers/runtime-spec/specs-go"
	"go.uber.org/zap"
)

//go:embed profiles/seccomp/default.json
var defaultSeccompProfile []byte

//go:embed profiles/seccomp/strict.json
var strictSeccompProfile []byte

// SeccompLoader handles loading and parsing seccomp profiles
type SeccompLoader struct {
	logger *zap.Logger
}

// NewSeccompLoader creates a new seccomp profile loader
func NewSeccompLoader(logger *zap.Logger) *SeccompLoader {
	return &SeccompLoader{
		logger: logger,
	}
}

// LoadProfile loads a seccomp profile based on the profile configuration
func (s *SeccompLoader) LoadProfile(profile *SeccompProfile) (*specs.LinuxSeccomp, error) {
	if profile == nil {
		return nil, nil
	}

	switch profile.Type {
	case SeccompProfileTypeUnconfined:
		s.logger.Debug("Using unconfined seccomp profile")
		return nil, nil

	case SeccompProfileTypeRuntimeDefault:
		s.logger.Debug("Using runtime default seccomp profile")
		return s.loadDefaultProfile()

	case SeccompProfileTypeLocalhost:
		if profile.LocalhostProfile == "" {
			return nil, fmt.Errorf("localhost profile path is required for SeccompProfileTypeLocalhost")
		}
		s.logger.Debug("Loading seccomp profile from file",
			zap.String("path", profile.LocalhostProfile))
		return s.loadProfileFromFile(profile.LocalhostProfile)

	default:
		return nil, fmt.Errorf("unknown seccomp profile type: %s", profile.Type)
	}
}

// loadDefaultProfile loads the embedded default seccomp profile
func (s *SeccompLoader) loadDefaultProfile() (*specs.LinuxSeccomp, error) {
	var seccomp specs.LinuxSeccomp
	if err := json.Unmarshal(defaultSeccompProfile, &seccomp); err != nil {
		return nil, fmt.Errorf("failed to parse default seccomp profile: %w", err)
	}
	return &seccomp, nil
}

// loadStrictProfile loads the embedded strict seccomp profile
func (s *SeccompLoader) loadStrictProfile() (*specs.LinuxSeccomp, error) {
	var seccomp specs.LinuxSeccomp
	if err := json.Unmarshal(strictSeccompProfile, &seccomp); err != nil {
		return nil, fmt.Errorf("failed to parse strict seccomp profile: %w", err)
	}
	return &seccomp, nil
}

// loadProfileFromFile loads a seccomp profile from a file path
func (s *SeccompLoader) loadProfileFromFile(path string) (*specs.LinuxSeccomp, error) {
	// Clean and validate the path
	cleanPath := filepath.Clean(path)

	// Check if file exists
	if _, err := os.Stat(cleanPath); err != nil {
		return nil, fmt.Errorf("seccomp profile file not found: %w", err)
	}

	// Read the file
	data, err := os.ReadFile(cleanPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read seccomp profile: %w", err)
	}

	// Parse the profile
	var seccomp specs.LinuxSeccomp
	if err := json.Unmarshal(data, &seccomp); err != nil {
		return nil, fmt.Errorf("failed to parse seccomp profile from %s: %w", path, err)
	}

	s.logger.Info("Loaded seccomp profile from file",
		zap.String("path", cleanPath),
		zap.String("default_action", string(seccomp.DefaultAction)),
		zap.Int("syscalls_count", len(seccomp.Syscalls)))

	return &seccomp, nil
}

// ApplySeccompProfile applies a seccomp profile to an OCI runtime spec
func ApplySeccompProfile(spec *specs.Spec, profile *SeccompProfile, logger *zap.Logger) error {
	if spec == nil {
		return fmt.Errorf("OCI spec is nil")
	}

	if profile == nil {
		logger.Debug("No seccomp profile specified, skipping")
		return nil
	}

	// Create seccomp loader
	loader := NewSeccompLoader(logger)

	// Load the profile
	seccompSpec, err := loader.LoadProfile(profile)
	if err != nil {
		return fmt.Errorf("failed to load seccomp profile: %w", err)
	}

	// Apply to OCI spec
	if seccompSpec != nil {
		if spec.Linux == nil {
			spec.Linux = &specs.Linux{}
		}
		spec.Linux.Seccomp = seccompSpec

		logger.Info("Applied seccomp profile",
			zap.String("type", string(profile.Type)),
			zap.String("default_action", string(seccompSpec.DefaultAction)),
			zap.Int("syscalls", len(seccompSpec.Syscalls)))
	}

	return nil
}

// IsSeccompAvailable checks if seccomp is available on the system
func IsSeccompAvailable() bool {
	// Check if /proc/self/status contains "Seccomp" field
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return false
	}

	// Look for "Seccomp:" in the status file
	// Seccomp: 2 means seccomp is available and in filter mode
	// Seccomp: 1 means seccomp is available but in strict mode
	// Seccomp: 0 means seccomp is disabled
	status := string(data)
	return contains(status, "Seccomp:")
}

// contains checks if a string contains a substring
func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsMiddle(s, substr)))
}

func containsMiddle(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

// GetDefaultSeccompProfile returns the default seccomp profile type
func GetDefaultSeccompProfile() *SeccompProfile {
	return &SeccompProfile{
		Type: SeccompProfileTypeRuntimeDefault,
	}
}

// GetStrictSeccompProfile returns a strict seccomp profile
func GetStrictSeccompProfile() *SeccompProfile {
	return &SeccompProfile{
		Type:             SeccompProfileTypeLocalhost,
		LocalhostProfile: "/etc/cloudless/seccomp/strict.json",
	}
}
