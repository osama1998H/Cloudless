package runtime

import (
	"fmt"
	"os"
	"strings"

	"github.com/opencontainers/runtime-spec/specs-go"
	"go.uber.org/zap"
)

const (
	// SELinuxMountPath is the path to SELinux filesystem
	SELinuxMountPath = "/sys/fs/selinux"

	// DefaultSELinuxUser is the default SELinux user for containers
	DefaultSELinuxUser = "system_u"

	// DefaultSELinuxRole is the default SELinux role for containers
	DefaultSELinuxRole = "system_r"

	// DefaultSELinuxType is the default SELinux type for Cloudless containers
	DefaultSELinuxType = "cloudless_container_t"

	// DefaultSELinuxLevel is the default SELinux level (s0 with unique categories)
	DefaultSELinuxLevel = "s0"
)

// SELinuxManager handles SELinux context management
type SELinuxManager struct {
	logger *zap.Logger
}

// NewSELinuxManager creates a new SELinux manager
func NewSELinuxManager(logger *zap.Logger) *SELinuxManager {
	return &SELinuxManager{
		logger: logger,
	}
}

// BuildSELinuxLabel builds an SELinux label from SELinuxOptions
func (s *SELinuxManager) BuildSELinuxLabel(options *SELinuxOptions) (string, error) {
	if options == nil {
		return "", nil
	}

	// SELinux label format: user:role:type:level
	user := DefaultSELinuxUser
	if options.User != "" {
		user = options.User
	}

	role := DefaultSELinuxRole
	if options.Role != "" {
		role = options.Role
	}

	seType := DefaultSELinuxType
	if options.Type != "" {
		seType = options.Type
	}

	level := DefaultSELinuxLevel
	if options.Level != "" {
		level = options.Level
	}

	label := fmt.Sprintf("%s:%s:%s:%s", user, role, seType, level)

	s.logger.Debug("Built SELinux label",
		zap.String("label", label),
		zap.String("user", user),
		zap.String("role", role),
		zap.String("type", seType),
		zap.String("level", level))

	return label, nil
}

// ApplySELinuxOptions applies SELinux options to an OCI runtime spec
func ApplySELinuxOptions(spec *specs.Spec, options *SELinuxOptions, logger *zap.Logger) error {
	if spec == nil {
		return fmt.Errorf("OCI spec is nil")
	}

	if options == nil {
		logger.Debug("No SELinux options specified, skipping")
		return nil
	}

	if !IsSELinuxAvailable() {
		logger.Warn("SELinux is not available on this system, skipping SELinux options")
		return nil
	}

	if !IsSELinuxEnforcing() {
		logger.Warn("SELinux is not in enforcing mode, SELinux options may not be enforced")
	}

	// Create SELinux manager
	manager := NewSELinuxManager(logger)

	// Build SELinux label
	label, err := manager.BuildSELinuxLabel(options)
	if err != nil {
		return fmt.Errorf("failed to build SELinux label: %w", err)
	}

	if label == "" {
		return nil
	}

	// Apply to OCI spec
	if spec.Process == nil {
		spec.Process = &specs.Process{}
	}
	spec.Process.SelinuxLabel = label

	// Also set mount label (for file contexts)
	if spec.Linux == nil {
		spec.Linux = &specs.Linux{}
	}
	spec.Linux.MountLabel = label

	logger.Info("Applied SELinux context",
		zap.String("process_label", label),
		zap.String("mount_label", label),
		zap.String("user", options.User),
		zap.String("role", options.Role),
		zap.String("type", options.Type),
		zap.String("level", options.Level))

	return nil
}

// IsSELinuxAvailable checks if SELinux is available on the system
func IsSELinuxAvailable() bool {
	// Check if SELinux filesystem is mounted
	if _, err := os.Stat(SELinuxMountPath); err != nil {
		return false
	}

	// Check if /proc/self/attr/current exists (SELinux enabled)
	if _, err := os.Stat("/proc/self/attr/current"); err != nil {
		return false
	}

	return true
}

// IsSELinuxEnforcing checks if SELinux is in enforcing mode
func IsSELinuxEnforcing() bool {
	if !IsSELinuxAvailable() {
		return false
	}

	// Read /sys/fs/selinux/enforce
	enforcePath := SELinuxMountPath + "/enforce"
	data, err := os.ReadFile(enforcePath)
	if err != nil {
		return false
	}

	enforce := strings.TrimSpace(string(data))
	return enforce == "1"
}

// GetSELinuxMode returns the SELinux mode (enforcing, permissive, disabled)
func GetSELinuxMode() string {
	if !IsSELinuxAvailable() {
		return "disabled"
	}

	if IsSELinuxEnforcing() {
		return "enforcing"
	}

	return "permissive"
}

// GetCurrentSELinuxContext returns the current process's SELinux context
func GetCurrentSELinuxContext() (string, error) {
	if !IsSELinuxAvailable() {
		return "", fmt.Errorf("SELinux is not available")
	}

	data, err := os.ReadFile("/proc/self/attr/current")
	if err != nil {
		return "", fmt.Errorf("failed to read SELinux context: %w", err)
	}

	context := strings.TrimSpace(string(data))
	return context, nil
}

// GetDefaultSELinuxOptions returns default SELinux options for Cloudless containers
func GetDefaultSELinuxOptions() *SELinuxOptions {
	return &SELinuxOptions{
		User:  DefaultSELinuxUser,
		Role:  DefaultSELinuxRole,
		Type:  DefaultSELinuxType,
		Level: DefaultSELinuxLevel,
	}
}

// GetSELinuxOptionsWithLevel returns SELinux options with a specific MCS level
// The level should be in format "s0:c0,c1" for MCS or "s0" for basic level
func GetSELinuxOptionsWithLevel(level string) *SELinuxOptions {
	return &SELinuxOptions{
		User:  DefaultSELinuxUser,
		Role:  DefaultSELinuxRole,
		Type:  DefaultSELinuxType,
		Level: level,
	}
}

// GenerateUniqueMCSLevel generates a unique MCS (Multi-Category Security) level
// for container isolation. This is a simplified implementation.
// In production, you would want to track used categories and ensure uniqueness.
func GenerateUniqueMCSLevel(containerID string) string {
	// This is a simplified example - in production, implement proper category tracking
	// Use container ID hash to generate consistent categories
	// Format: s0:c<num1>,c<num2>

	// For now, use a fixed level - proper implementation would hash container ID
	// and map to available categories (c0-c1023)
	return "s0:c0,c1"
}
