package runtime

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/opencontainers/runtime-spec/specs-go"
	"go.uber.org/zap"
)

const (
	// AppArmorProfileDir is the directory where AppArmor profiles are stored
	AppArmorProfileDir = "/etc/apparmor.d"

	// AppArmorProfilesPath is the path to the AppArmor profiles status file
	AppArmorProfilesPath = "/sys/kernel/security/apparmor/profiles"

	// CloudlessDefaultProfile is the name of the default Cloudless AppArmor profile
	CloudlessDefaultProfile = "cloudless-default"
)

// AppArmorLoader handles loading and applying AppArmor profiles
type AppArmorLoader struct {
	logger *zap.Logger
}

// NewAppArmorLoader creates a new AppArmor profile loader
func NewAppArmorLoader(logger *zap.Logger) *AppArmorLoader {
	return &AppArmorLoader{
		logger: logger,
	}
}

// GetProfileName returns the profile name to use based on the profile configuration
func (a *AppArmorLoader) GetProfileName(profile *AppArmorProfile) (string, error) {
	if profile == nil {
		return "", nil
	}

	switch profile.Type {
	case AppArmorProfileTypeUnconfined:
		a.logger.Debug("Using unconfined AppArmor profile")
		return "unconfined", nil

	case AppArmorProfileTypeRuntimeDefault:
		a.logger.Debug("Using runtime default AppArmor profile")
		// Use cloudless-default profile
		return CloudlessDefaultProfile, nil

	case AppArmorProfileTypeLocalhost:
		if profile.LocalhostProfile == "" {
			return "", fmt.Errorf("localhost profile name is required for AppArmorProfileTypeLocalhost")
		}
		a.logger.Debug("Using localhost AppArmor profile",
			zap.String("profile", profile.LocalhostProfile))
		return profile.LocalhostProfile, nil

	default:
		return "", fmt.Errorf("unknown AppArmor profile type: %s", profile.Type)
	}
}

// IsProfileLoaded checks if an AppArmor profile is loaded
func (a *AppArmorLoader) IsProfileLoaded(profileName string) bool {
	if !IsAppArmorAvailable() {
		return false
	}

	// Check if profile is loaded by reading /sys/kernel/security/apparmor/profiles
	data, err := os.ReadFile(AppArmorProfilesPath)
	if err != nil {
		a.logger.Warn("Failed to read AppArmor profiles",
			zap.Error(err))
		return false
	}

	profiles := string(data)
	lines := strings.Split(profiles, "\n")
	for _, line := range lines {
		// Format: "profile_name (mode)"
		if strings.HasPrefix(line, profileName+" ") || strings.HasPrefix(line, profileName+"\t") {
			a.logger.Debug("AppArmor profile is loaded",
				zap.String("profile", profileName))
			return true
		}
	}

	a.logger.Debug("AppArmor profile is not loaded",
		zap.String("profile", profileName))
	return false
}

// LoadProfile loads an AppArmor profile if not already loaded
func (a *AppArmorLoader) LoadProfile(profileName string) error {
	if profileName == "" || profileName == "unconfined" {
		return nil
	}

	if !IsAppArmorAvailable() {
		return fmt.Errorf("AppArmor is not available on this system")
	}

	// Check if already loaded
	if a.IsProfileLoaded(profileName) {
		a.logger.Debug("AppArmor profile already loaded",
			zap.String("profile", profileName))
		return nil
	}

	// Try to load the profile
	// Note: This requires apparmor_parser to be installed and appropriate permissions
	// In production, profiles should be loaded at system startup
	profilePath := filepath.Join(AppArmorProfileDir, profileName)
	if _, err := os.Stat(profilePath); err != nil {
		return fmt.Errorf("AppArmor profile not found: %s: %w", profilePath, err)
	}

	a.logger.Warn("AppArmor profile not loaded - should be loaded at system startup",
		zap.String("profile", profileName),
		zap.String("path", profilePath))

	return fmt.Errorf("AppArmor profile %s is not loaded - load it with: apparmor_parser -r %s", profileName, profilePath)
}

// ApplyAppArmorProfile applies an AppArmor profile to an OCI runtime spec
func ApplyAppArmorProfile(spec *specs.Spec, profile *AppArmorProfile, logger *zap.Logger) error {
	if spec == nil {
		return fmt.Errorf("OCI spec is nil")
	}

	if profile == nil {
		logger.Debug("No AppArmor profile specified, skipping")
		return nil
	}

	if !IsAppArmorAvailable() {
		logger.Warn("AppArmor is not available on this system, skipping profile application")
		return nil
	}

	// Create AppArmor loader
	loader := NewAppArmorLoader(logger)

	// Get the profile name
	profileName, err := loader.GetProfileName(profile)
	if err != nil {
		return fmt.Errorf("failed to get AppArmor profile name: %w", err)
	}

	if profileName == "" {
		return nil
	}

	// Verify profile is loaded (except for unconfined)
	if profileName != "unconfined" {
		if !loader.IsProfileLoaded(profileName) {
			logger.Warn("AppArmor profile is not loaded - container may fail to start",
				zap.String("profile", profileName))
			// Don't fail - let the container runtime handle it
		}
	}

	// Apply to OCI spec
	if spec.Process == nil {
		spec.Process = &specs.Process{}
	}
	spec.Process.ApparmorProfile = profileName

	logger.Info("Applied AppArmor profile",
		zap.String("type", string(profile.Type)),
		zap.String("profile", profileName))

	return nil
}

// IsAppArmorAvailable checks if AppArmor is available on the system
func IsAppArmorAvailable() bool {
	// Check if /sys/kernel/security/apparmor exists
	if _, err := os.Stat("/sys/kernel/security/apparmor"); err != nil {
		return false
	}

	// Check if AppArmor is enabled by reading /sys/module/apparmor/parameters/enabled
	data, err := os.ReadFile("/sys/module/apparmor/parameters/enabled")
	if err != nil {
		return false
	}

	enabled := strings.TrimSpace(string(data))
	return enabled == "Y"
}

// GetDefaultAppArmorProfile returns the default AppArmor profile
func GetDefaultAppArmorProfile() *AppArmorProfile {
	return &AppArmorProfile{
		Type: AppArmorProfileTypeRuntimeDefault,
	}
}

// GetUnconfinedAppArmorProfile returns an unconfined AppArmor profile
func GetUnconfinedAppArmorProfile() *AppArmorProfile {
	return &AppArmorProfile{
		Type: AppArmorProfileTypeUnconfined,
	}
}

// GetLocalhostAppArmorProfile returns a localhost AppArmor profile
func GetLocalhostAppArmorProfile(profileName string) *AppArmorProfile {
	return &AppArmorProfile{
		Type:             AppArmorProfileTypeLocalhost,
		LocalhostProfile: profileName,
	}
}
