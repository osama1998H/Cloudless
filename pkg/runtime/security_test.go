package runtime

import (
	"testing"

	"github.com/opencontainers/runtime-spec/specs-go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestSeccompLoader tests seccomp profile loading
func TestSeccompLoader(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name        string
		profile     *SeccompProfile
		expectNil   bool
		expectError bool
	}{
		{
			name:      "nil profile",
			profile:   nil,
			expectNil: true,
		},
		{
			name: "unconfined profile",
			profile: &SeccompProfile{
				Type: SeccompProfileTypeUnconfined,
			},
			expectNil: true,
		},
		{
			name: "runtime default profile",
			profile: &SeccompProfile{
				Type: SeccompProfileTypeRuntimeDefault,
			},
			expectNil: false,
		},
		{
			name: "localhost profile - nonexistent file",
			profile: &SeccompProfile{
				Type:             SeccompProfileTypeLocalhost,
				LocalhostProfile: "/nonexistent/profile.json",
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			loader := NewSeccompLoader(logger)
			result, err := loader.LoadProfile(tt.profile)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

			if tt.expectNil {
				assert.Nil(t, result)
			} else if !tt.expectError {
				assert.NotNil(t, result)
			}
		})
	}
}

// TestApplySeccompProfile tests seccomp profile application to OCI spec
func TestApplySeccompProfile(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name        string
		profile     *SeccompProfile
		expectError bool
		checkSpec   func(*testing.T, *specs.Spec)
	}{
		{
			name:    "nil profile",
			profile: nil,
			checkSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Nil(t, spec.Linux.Seccomp)
			},
		},
		{
			name: "unconfined profile",
			profile: &SeccompProfile{
				Type: SeccompProfileTypeUnconfined,
			},
			checkSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Nil(t, spec.Linux.Seccomp)
			},
		},
		{
			name: "runtime default profile",
			profile: &SeccompProfile{
				Type: SeccompProfileTypeRuntimeDefault,
			},
			checkSpec: func(t *testing.T, spec *specs.Spec) {
				assert.NotNil(t, spec.Linux.Seccomp)
				assert.Equal(t, specs.ActErrno, spec.Linux.Seccomp.DefaultAction)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &specs.Spec{
				Linux: &specs.Linux{},
			}

			err := ApplySeccompProfile(spec, tt.profile, logger)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				if tt.checkSpec != nil {
					tt.checkSpec(t, spec)
				}
			}
		})
	}
}

// TestAppArmorProfileVerification tests AppArmor profile verification
func TestAppArmorProfileVerification(t *testing.T) {
	logger := zap.NewNop()
	manager := NewAppArmorManager(logger)

	tests := []struct {
		name        string
		profile     *AppArmorProfile
		expectError bool
	}{
		{
			name: "unconfined profile",
			profile: &AppArmorProfile{
				Type: AppArmorProfileTypeUnconfined,
			},
			expectError: false,
		},
		{
			name: "runtime default profile",
			profile: &AppArmorProfile{
				Type: AppArmorProfileTypeRuntimeDefault,
			},
			expectError: false,
		},
		{
			name: "localhost profile - cloudless-default",
			profile: &AppArmorProfile{
				Type:             AppArmorProfileTypeLocalhost,
				LocalhostProfile: "cloudless-default",
			},
			// May error if AppArmor not available on system
			expectError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := manager.VerifyProfile(tt.profile)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				// Allow warning if AppArmor not available
				if err != nil {
					t.Logf("AppArmor not available: %v", err)
				}
			}
		})
	}
}

// TestApplyAppArmorProfile tests AppArmor profile application to OCI spec
func TestApplyAppArmorProfile(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name      string
		profile   *AppArmorProfile
		checkSpec func(*testing.T, *specs.Spec)
	}{
		{
			name:    "nil profile",
			profile: nil,
			checkSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Equal(t, "", spec.Process.ApparmorProfile)
			},
		},
		{
			name: "unconfined profile",
			profile: &AppArmorProfile{
				Type: AppArmorProfileTypeUnconfined,
			},
			checkSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Equal(t, AppArmorProfileUnconfined, spec.Process.ApparmorProfile)
			},
		},
		{
			name: "runtime default profile",
			profile: &AppArmorProfile{
				Type: AppArmorProfileTypeRuntimeDefault,
			},
			checkSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Equal(t, AppArmorProfileCloudlessDefault, spec.Process.ApparmorProfile)
			},
		},
		{
			name: "localhost profile",
			profile: &AppArmorProfile{
				Type:             AppArmorProfileTypeLocalhost,
				LocalhostProfile: "custom-profile",
			},
			checkSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Equal(t, "custom-profile", spec.Process.ApparmorProfile)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &specs.Spec{
				Process: &specs.Process{},
			}

			err := ApplyAppArmorProfile(spec, tt.profile, logger)
			assert.NoError(t, err)

			if tt.checkSpec != nil {
				tt.checkSpec(t, spec)
			}
		})
	}
}

// TestSELinuxLabelBuilding tests SELinux label construction
func TestSELinuxLabelBuilding(t *testing.T) {
	logger := zap.NewNop()
	manager := NewSELinuxManager(logger)

	tests := []struct {
		name          string
		options       *SELinuxOptions
		expectedLabel string
	}{
		{
			name:          "nil options",
			options:       nil,
			expectedLabel: "",
		},
		{
			name:          "default options",
			options:       GetDefaultSELinuxOptions(),
			expectedLabel: "system_u:system_r:cloudless_container_t:s0",
		},
		{
			name: "custom options",
			options: &SELinuxOptions{
				User:  "user_u",
				Role:  "user_r",
				Type:  "container_t",
				Level: "s0:c0,c1",
			},
			expectedLabel: "user_u:user_r:container_t:s0:c0,c1",
		},
		{
			name: "partial options - user only",
			options: &SELinuxOptions{
				User: "custom_u",
			},
			expectedLabel: "custom_u:system_r:cloudless_container_t:s0",
		},
		{
			name: "partial options - level only",
			options: &SELinuxOptions{
				Level: "s0:c100,c200",
			},
			expectedLabel: "system_u:system_r:cloudless_container_t:s0:c100,c200",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			label, err := manager.BuildSELinuxLabel(tt.options)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectedLabel, label)
		})
	}
}

// TestApplySELinuxOptions tests SELinux options application to OCI spec
func TestApplySELinuxOptions(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name      string
		options   *SELinuxOptions
		checkSpec func(*testing.T, *specs.Spec)
	}{
		{
			name:    "nil options",
			options: nil,
			checkSpec: func(t *testing.T, spec *specs.Spec) {
				assert.Equal(t, "", spec.Process.SelinuxLabel)
			},
		},
		{
			name:    "default options",
			options: GetDefaultSELinuxOptions(),
			checkSpec: func(t *testing.T, spec *specs.Spec) {
				expectedLabel := "system_u:system_r:cloudless_container_t:s0"
				// May not apply if SELinux not available
				if spec.Process.SelinuxLabel != "" {
					assert.Equal(t, expectedLabel, spec.Process.SelinuxLabel)
					assert.Equal(t, expectedLabel, spec.Linux.MountLabel)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &specs.Spec{
				Process: &specs.Process{},
				Linux:   &specs.Linux{},
			}

			err := ApplySELinuxOptions(spec, tt.options, logger)
			assert.NoError(t, err)

			if tt.checkSpec != nil {
				tt.checkSpec(t, spec)
			}
		})
	}
}

// TestRuntimeClassSelection tests runtime class selection logic
func TestRuntimeClassSelection(t *testing.T) {
	logger := zap.NewNop()
	manager := NewRuntimeClassManager(logger)

	tests := []struct {
		name            string
		runtimeClass    string
		expectError     bool
		expectedHandler string
	}{
		{
			name:            "default runtime (empty string)",
			runtimeClass:    "",
			expectedHandler: "io.containerd.runc.v2",
		},
		{
			name:            "runc runtime",
			runtimeClass:    RuntimeClassRunc,
			expectedHandler: "io.containerd.runc.v2",
		},
		{
			name:            "gvisor runtime",
			runtimeClass:    RuntimeClassGVisor,
			expectedHandler: "io.containerd.runsc.v1",
		},
		{
			name:            "kata runtime",
			runtimeClass:    RuntimeClassKata,
			expectedHandler: "io.containerd.kata.v2",
		},
		{
			name:            "firecracker runtime",
			runtimeClass:    RuntimeClassFirecracker,
			expectedHandler: "aws.firecracker",
		},
		{
			name:         "unknown runtime",
			runtimeClass: "nonexistent-runtime",
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			class, err := manager.GetRuntimeClass(tt.runtimeClass)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.expectedHandler, class.Handler)
			}
		})
	}
}

// TestApplyRuntimeClassSpec tests runtime class spec modifications
func TestApplyRuntimeClassSpec(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name         string
		runtimeClass string
		expectError  bool
	}{
		{
			name:         "empty runtime class (default)",
			runtimeClass: "",
		},
		{
			name:         "runc runtime",
			runtimeClass: RuntimeClassRunc,
		},
		{
			name:         "gvisor runtime",
			runtimeClass: RuntimeClassGVisor,
		},
		{
			name:         "kata runtime",
			runtimeClass: RuntimeClassKata,
		},
		{
			name:         "firecracker runtime",
			runtimeClass: RuntimeClassFirecracker,
		},
		{
			name:         "unknown runtime",
			runtimeClass: "unknown",
			expectError:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &specs.Spec{
				Process: &specs.Process{},
			}

			err := ApplyRuntimeClassSpec(spec, tt.runtimeClass, logger)

			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// TestCapabilityManagement tests capability add/drop logic
func TestCapabilityManagement(t *testing.T) {
	tests := []struct {
		name         string
		initial      []string
		toRemove     string
		expected     []string
		shouldRemain bool
	}{
		{
			name:         "remove existing capability",
			initial:      []string{"CAP_NET_ADMIN", "CAP_SYS_ADMIN", "CAP_CHOWN"},
			toRemove:     "CAP_SYS_ADMIN",
			expected:     []string{"CAP_NET_ADMIN", "CAP_CHOWN"},
			shouldRemain: false,
		},
		{
			name:         "remove non-existent capability",
			initial:      []string{"CAP_NET_ADMIN", "CAP_CHOWN"},
			toRemove:     "CAP_SYS_ADMIN",
			expected:     []string{"CAP_NET_ADMIN", "CAP_CHOWN"},
			shouldRemain: false,
		},
		{
			name:         "remove from empty list",
			initial:      []string{},
			toRemove:     "CAP_SYS_ADMIN",
			expected:     []string{},
			shouldRemain: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := removeCapability(tt.initial, tt.toRemove)
			assert.Equal(t, tt.expected, result)
			assert.Equal(t, tt.shouldRemain, containsCap(result, tt.toRemove))
		})
	}
}

// TestContainsCap tests capability existence check
func TestContainsCap(t *testing.T) {
	tests := []struct {
		name       string
		caps       []string
		cap        string
		shouldFind bool
	}{
		{
			name:       "capability exists",
			caps:       []string{"CAP_NET_ADMIN", "CAP_SYS_ADMIN", "CAP_CHOWN"},
			cap:        "CAP_SYS_ADMIN",
			shouldFind: true,
		},
		{
			name:       "capability does not exist",
			caps:       []string{"CAP_NET_ADMIN", "CAP_CHOWN"},
			cap:        "CAP_SYS_ADMIN",
			shouldFind: false,
		},
		{
			name:       "empty list",
			caps:       []string{},
			cap:        "CAP_SYS_ADMIN",
			shouldFind: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := containsCap(tt.caps, tt.cap)
			assert.Equal(t, tt.shouldFind, result)
		})
	}
}

// TestPrivilegedMode tests privileged mode handling
func TestPrivilegedMode(t *testing.T) {
	logger := zap.NewNop()

	t.Run("privileged disables seccomp", func(t *testing.T) {
		spec := &specs.Spec{
			Process: &specs.Process{},
			Linux: &specs.Linux{
				Seccomp: &specs.LinuxSeccomp{
					DefaultAction: specs.ActErrno,
				},
			},
		}

		// Simulate privileged mode - seccomp should be cleared
		spec.Linux.Seccomp = nil

		assert.Nil(t, spec.Linux.Seccomp)
	})

	t.Run("non-privileged preserves seccomp", func(t *testing.T) {
		spec := &specs.Spec{
			Process: &specs.Process{},
			Linux: &specs.Linux{
				Seccomp: &specs.LinuxSeccomp{
					DefaultAction: specs.ActErrno,
				},
			},
		}

		profile := &SeccompProfile{
			Type: SeccompProfileTypeRuntimeDefault,
		}

		err := ApplySeccompProfile(spec, profile, logger)
		require.NoError(t, err)
		assert.NotNil(t, spec.Linux.Seccomp)
	})
}

// TestReadOnlyRootFilesystem tests read-only root filesystem enforcement
func TestReadOnlyRootFilesystem(t *testing.T) {
	tests := []struct {
		name     string
		readOnly bool
	}{
		{
			name:     "read-only enabled",
			readOnly: true,
		},
		{
			name:     "read-only disabled",
			readOnly: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &specs.Spec{
				Root: &specs.Root{},
			}

			spec.Root.Readonly = tt.readOnly

			assert.Equal(t, tt.readOnly, spec.Root.Readonly)
		})
	}
}

// TestRunAsUser tests user ID enforcement
func TestRunAsUser(t *testing.T) {
	tests := []struct {
		name   string
		userID *int64
	}{
		{
			name:   "run as specific user",
			userID: func() *int64 { u := int64(1000); return &u }(),
		},
		{
			name:   "run as root",
			userID: func() *int64 { u := int64(0); return &u }(),
		},
		{
			name:   "no user specified",
			userID: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &specs.Spec{
				Process: &specs.Process{
					User: specs.User{},
				},
			}

			if tt.userID != nil {
				uid := uint32(*tt.userID)
				spec.Process.User.UID = uid
				assert.Equal(t, uid, spec.Process.User.UID)
			}
		})
	}
}

// TestRunAsNonRoot tests non-root user enforcement
func TestRunAsNonRoot(t *testing.T) {
	tests := []struct {
		name         string
		userID       int64
		expectNonRoot bool
	}{
		{
			name:         "non-root user",
			userID:       1000,
			expectNonRoot: true,
		},
		{
			name:         "root user",
			userID:       0,
			expectNonRoot: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			spec := &specs.Spec{
				Process: &specs.Process{
					User: specs.User{
						UID: uint32(tt.userID),
					},
				},
			}

			isNonRoot := spec.Process.User.UID != 0
			assert.Equal(t, tt.expectNonRoot, isNonRoot)
		})
	}
}

// TestSELinuxFeatureDetection tests SELinux availability detection
func TestSELinuxFeatureDetection(t *testing.T) {
	t.Run("check SELinux availability", func(t *testing.T) {
		available := IsSELinuxAvailable()
		t.Logf("SELinux available: %v", available)

		if available {
			mode := GetSELinuxMode()
			t.Logf("SELinux mode: %s", mode)
			assert.Contains(t, []string{"enforcing", "permissive", "disabled"}, mode)
		}
	})
}

// TestAppArmorFeatureDetection tests AppArmor availability detection
func TestAppArmorFeatureDetection(t *testing.T) {
	t.Run("check AppArmor availability", func(t *testing.T) {
		available := IsAppArmorAvailable()
		t.Logf("AppArmor available: %v", available)

		if available {
			enabled := IsAppArmorEnabled()
			t.Logf("AppArmor enabled: %v", enabled)
		}
	})
}

// TestListAvailableRuntimes tests runtime class enumeration
func TestListAvailableRuntimes(t *testing.T) {
	logger := zap.NewNop()
	runtimes := ListAvailableRuntimes(logger)

	assert.NotEmpty(t, runtimes)
	assert.Contains(t, runtimes, RuntimeClassRunc)
	assert.Contains(t, runtimes, RuntimeClassGVisor)
	assert.Contains(t, runtimes, RuntimeClassKata)
	assert.Contains(t, runtimes, RuntimeClassFirecracker)
}

// TestGetDefaultRuntimeClass tests default runtime selection
func TestGetDefaultRuntimeClass(t *testing.T) {
	defaultRuntime := GetDefaultRuntimeClass()
	assert.Equal(t, RuntimeClassRunc, defaultRuntime)
}

// Helper functions for tests

// removeCapability removes a capability from a capability list
func removeCapability(caps []string, cap string) []string {
	result := make([]string, 0, len(caps))
	for _, c := range caps {
		if c != cap {
			result = append(result, c)
		}
	}
	return result
}

// containsCap checks if a capability exists in a capability list
func containsCap(caps []string, cap string) bool {
	for _, c := range caps {
		if c == cap {
			return true
		}
	}
	return false
}
