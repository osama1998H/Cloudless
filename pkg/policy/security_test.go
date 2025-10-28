package policy

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"go.uber.org/zap"
)

// TestEvaluateSecurityContext tests security context policy evaluation
func TestEvaluateSecurityContext(t *testing.T) {
	logger := zap.NewNop()
	evaluator := NewEvaluator(logger)

	tests := []struct {
		name             string
		rule             Rule
		spec             WorkloadSpec
		expectViolations bool
		violationCount   int
	}{
		{
			name: "require security context - present",
			rule: Rule{
				Name: "require-security-context",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"require_security_context": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{},
			},
			expectViolations: false,
		},
		{
			name: "require security context - missing",
			rule: Rule{
				Name: "require-security-context",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"require_security_context": true,
				},
			},
			spec:             WorkloadSpec{},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "forbid privileged - violation",
			rule: Rule{
				Name: "forbid-privileged",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"forbid_privileged": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Privileged: true,
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "forbid privileged - compliant",
			rule: Rule{
				Name: "forbid-privileged",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"forbid_privileged": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Privileged: false,
				},
			},
			expectViolations: false,
		},
		{
			name: "require non-root - violation",
			rule: Rule{
				Name: "require-non-root",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"require_non_root": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RunAsNonRoot: func() *bool { b := false; return &b }(),
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "require non-root - compliant",
			rule: Rule{
				Name: "require-non-root",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"require_non_root": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RunAsNonRoot: func() *bool { b := true; return &b }(),
				},
			},
			expectViolations: false,
		},
		{
			name: "require read-only root - violation",
			rule: Rule{
				Name: "require-read-only-root",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"require_read_only_root": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					ReadOnlyRootFilesystem: false,
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "require read-only root - compliant",
			rule: Rule{
				Name: "require-read-only-root",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"require_read_only_root": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					ReadOnlyRootFilesystem: true,
				},
			},
			expectViolations: false,
		},
		{
			name: "multiple requirements - all violations",
			rule: Rule{
				Name: "strict-security",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"forbid_privileged":      true,
					"require_non_root":       true,
					"require_read_only_root": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Privileged:             true,
					RunAsNonRoot:           func() *bool { b := false; return &b }(),
					ReadOnlyRootFilesystem: false,
				},
			},
			expectViolations: true,
			violationCount:   3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := evaluator.EvaluateRule(tt.rule, tt.spec)

			if tt.expectViolations {
				assert.NotEmpty(t, violations)
				assert.Equal(t, tt.violationCount, len(violations))
			} else {
				assert.Empty(t, violations)
			}
		})
	}
}

// TestEvaluateSeccompProfile tests seccomp profile policy evaluation
func TestEvaluateSeccompProfile(t *testing.T) {
	logger := zap.NewNop()
	evaluator := NewEvaluator(logger)

	tests := []struct {
		name             string
		rule             Rule
		spec             WorkloadSpec
		expectViolations bool
		violationCount   int
	}{
		{
			name: "require seccomp - present",
			rule: Rule{
				Name: "require-seccomp",
				Type: RuleTypeSeccompProfile,
				Config: map[string]interface{}{
					"require_seccomp": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						SeccompProfile: &PolicySeccompProfile{
							Type: "RuntimeDefault",
						},
					},
				},
			},
			expectViolations: false,
		},
		{
			name: "require seccomp - missing",
			rule: Rule{
				Name: "require-seccomp",
				Type: RuleTypeSeccompProfile,
				Config: map[string]interface{}{
					"require_seccomp": true,
				},
			},
			spec:             WorkloadSpec{},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "forbid unconfined - violation",
			rule: Rule{
				Name: "forbid-unconfined",
				Type: RuleTypeSeccompProfile,
				Config: map[string]interface{}{
					"forbid_unconfined": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						SeccompProfile: &PolicySeccompProfile{
							Type: "Unconfined",
						},
					},
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "allowed types - violation",
			rule: Rule{
				Name: "seccomp-allowed-types",
				Type: RuleTypeSeccompProfile,
				Config: map[string]interface{}{
					"allowed_types": []interface{}{"RuntimeDefault", "Localhost"},
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						SeccompProfile: &PolicySeccompProfile{
							Type: "Unconfined",
						},
					},
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "allowed types - compliant",
			rule: Rule{
				Name: "seccomp-allowed-types",
				Type: RuleTypeSeccompProfile,
				Config: map[string]interface{}{
					"allowed_types": []interface{}{"RuntimeDefault", "Localhost"},
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						SeccompProfile: &PolicySeccompProfile{
							Type: "RuntimeDefault",
						},
					},
				},
			},
			expectViolations: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := evaluator.EvaluateRule(tt.rule, tt.spec)

			if tt.expectViolations {
				assert.NotEmpty(t, violations)
				assert.Equal(t, tt.violationCount, len(violations))
			} else {
				assert.Empty(t, violations)
			}
		})
	}
}

// TestEvaluateAppArmorProfile tests AppArmor profile policy evaluation
func TestEvaluateAppArmorProfile(t *testing.T) {
	logger := zap.NewNop()
	evaluator := NewEvaluator(logger)

	tests := []struct {
		name             string
		rule             Rule
		spec             WorkloadSpec
		expectViolations bool
		violationCount   int
	}{
		{
			name: "require AppArmor - present",
			rule: Rule{
				Name: "require-apparmor",
				Type: RuleTypeAppArmorProfile,
				Config: map[string]interface{}{
					"require_apparmor": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						AppArmorProfile: &PolicyAppArmorProfile{
							Type: "RuntimeDefault",
						},
					},
				},
			},
			expectViolations: false,
		},
		{
			name: "require AppArmor - missing",
			rule: Rule{
				Name: "require-apparmor",
				Type: RuleTypeAppArmorProfile,
				Config: map[string]interface{}{
					"require_apparmor": true,
				},
			},
			spec:             WorkloadSpec{},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "forbid unconfined - violation",
			rule: Rule{
				Name: "forbid-unconfined",
				Type: RuleTypeAppArmorProfile,
				Config: map[string]interface{}{
					"forbid_unconfined": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						AppArmorProfile: &PolicyAppArmorProfile{
							Type: "Unconfined",
						},
					},
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "allowed types - compliant",
			rule: Rule{
				Name: "apparmor-allowed-types",
				Type: RuleTypeAppArmorProfile,
				Config: map[string]interface{}{
					"allowed_types": []interface{}{"RuntimeDefault", "Localhost"},
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						AppArmorProfile: &PolicyAppArmorProfile{
							Type: "RuntimeDefault",
						},
					},
				},
			},
			expectViolations: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := evaluator.EvaluateRule(tt.rule, tt.spec)

			if tt.expectViolations {
				assert.NotEmpty(t, violations)
				assert.Equal(t, tt.violationCount, len(violations))
			} else {
				assert.Empty(t, violations)
			}
		})
	}
}

// TestEvaluateSELinuxOptions tests SELinux options policy evaluation
func TestEvaluateSELinuxOptions(t *testing.T) {
	logger := zap.NewNop()
	evaluator := NewEvaluator(logger)

	tests := []struct {
		name             string
		rule             Rule
		spec             WorkloadSpec
		expectViolations bool
		violationCount   int
	}{
		{
			name: "require SELinux - present",
			rule: Rule{
				Name: "require-selinux",
				Type: RuleTypeSELinuxOptions,
				Config: map[string]interface{}{
					"require_selinux": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						SELinuxOptions: &PolicySELinuxOptions{
							Type: "container_t",
						},
					},
				},
			},
			expectViolations: false,
		},
		{
			name: "require SELinux - missing",
			rule: Rule{
				Name: "require-selinux",
				Type: RuleTypeSELinuxOptions,
				Config: map[string]interface{}{
					"require_selinux": true,
				},
			},
			spec:             WorkloadSpec{},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "allowed types - violation",
			rule: Rule{
				Name: "selinux-allowed-types",
				Type: RuleTypeSELinuxOptions,
				Config: map[string]interface{}{
					"allowed_types": []interface{}{"container_t", "svirt_sandbox_file_t"},
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						SELinuxOptions: &PolicySELinuxOptions{
							Type: "unconfined_t",
						},
					},
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "allowed types - compliant",
			rule: Rule{
				Name: "selinux-allowed-types",
				Type: RuleTypeSELinuxOptions,
				Config: map[string]interface{}{
					"allowed_types": []interface{}{"container_t", "svirt_sandbox_file_t"},
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						SELinuxOptions: &PolicySELinuxOptions{
							Type: "container_t",
						},
					},
				},
			},
			expectViolations: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := evaluator.EvaluateRule(tt.rule, tt.spec)

			if tt.expectViolations {
				assert.NotEmpty(t, violations)
				assert.Equal(t, tt.violationCount, len(violations))
			} else {
				assert.Empty(t, violations)
			}
		})
	}
}

// TestEvaluateRuntimeClass tests runtime class policy evaluation
func TestEvaluateRuntimeClass(t *testing.T) {
	logger := zap.NewNop()
	evaluator := NewEvaluator(logger)

	tests := []struct {
		name             string
		rule             Rule
		spec             WorkloadSpec
		expectViolations bool
		violationCount   int
	}{
		{
			name: "require runtime class - present",
			rule: Rule{
				Name: "require-runtime-class",
				Type: RuleTypeRuntimeClass,
				Config: map[string]interface{}{
					"require_runtime_class": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RuntimeClassName: "gvisor",
				},
			},
			expectViolations: false,
		},
		{
			name: "require runtime class - missing",
			rule: Rule{
				Name: "require-runtime-class",
				Type: RuleTypeRuntimeClass,
				Config: map[string]interface{}{
					"require_runtime_class": true,
				},
			},
			spec:             WorkloadSpec{},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "allowed runtime classes - violation",
			rule: Rule{
				Name: "allowed-runtime-classes",
				Type: RuleTypeRuntimeClass,
				Config: map[string]interface{}{
					"allowed_runtime_classes": []interface{}{"gvisor", "kata"},
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RuntimeClassName: "runc",
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "allowed runtime classes - compliant",
			rule: Rule{
				Name: "allowed-runtime-classes",
				Type: RuleTypeRuntimeClass,
				Config: map[string]interface{}{
					"allowed_runtime_classes": []interface{}{"gvisor", "kata"},
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RuntimeClassName: "gvisor",
				},
			},
			expectViolations: false,
		},
		{
			name: "require sandboxed runtime - violation (runc)",
			rule: Rule{
				Name: "require-sandboxed-runtime",
				Type: RuleTypeRuntimeClass,
				Config: map[string]interface{}{
					"require_sandboxed": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RuntimeClassName: "runc",
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "require sandboxed runtime - compliant (gvisor)",
			rule: Rule{
				Name: "require-sandboxed-runtime",
				Type: RuleTypeRuntimeClass,
				Config: map[string]interface{}{
					"require_sandboxed": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RuntimeClassName: "gvisor",
				},
			},
			expectViolations: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := evaluator.EvaluateRule(tt.rule, tt.spec)

			if tt.expectViolations {
				assert.NotEmpty(t, violations)
				assert.Equal(t, tt.violationCount, len(violations))
			} else {
				assert.Empty(t, violations)
			}
		})
	}
}

// TestEvaluateRunAsNonRoot tests non-root user policy evaluation
func TestEvaluateRunAsNonRoot(t *testing.T) {
	logger := zap.NewNop()
	evaluator := NewEvaluator(logger)

	tests := []struct {
		name             string
		rule             Rule
		spec             WorkloadSpec
		expectViolations bool
		violationCount   int
	}{
		{
			name: "enforce non-root - compliant with RunAsNonRoot",
			rule: Rule{
				Name: "enforce-non-root",
				Type: RuleTypeRunAsNonRoot,
				Config: map[string]interface{}{
					"enforce": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RunAsNonRoot: func() *bool { b := true; return &b }(),
				},
			},
			expectViolations: false,
		},
		{
			name: "enforce non-root - compliant with RunAsUser > 0",
			rule: Rule{
				Name: "enforce-non-root",
				Type: RuleTypeRunAsNonRoot,
				Config: map[string]interface{}{
					"enforce": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RunAsUser: func() *int64 { u := int64(1000); return &u }(),
				},
			},
			expectViolations: false,
		},
		{
			name: "enforce non-root - violation (root user)",
			rule: Rule{
				Name: "enforce-non-root",
				Type: RuleTypeRunAsNonRoot,
				Config: map[string]interface{}{
					"enforce": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RunAsUser: func() *int64 { u := int64(0); return &u }(),
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "enforce non-root - violation (RunAsNonRoot false)",
			rule: Rule{
				Name: "enforce-non-root",
				Type: RuleTypeRunAsNonRoot,
				Config: map[string]interface{}{
					"enforce": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					RunAsNonRoot: func() *bool { b := false; return &b }(),
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := evaluator.EvaluateRule(tt.rule, tt.spec)

			if tt.expectViolations {
				assert.NotEmpty(t, violations)
				assert.Equal(t, tt.violationCount, len(violations))
			} else {
				assert.Empty(t, violations)
			}
		})
	}
}

// TestEvaluateReadOnlyRootFS tests read-only root filesystem policy evaluation
func TestEvaluateReadOnlyRootFS(t *testing.T) {
	logger := zap.NewNop()
	evaluator := NewEvaluator(logger)

	tests := []struct {
		name             string
		rule             Rule
		spec             WorkloadSpec
		expectViolations bool
		violationCount   int
	}{
		{
			name: "enforce read-only root - compliant",
			rule: Rule{
				Name: "enforce-read-only-root",
				Type: RuleTypeReadOnlyRootFS,
				Config: map[string]interface{}{
					"enforce": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					ReadOnlyRootFilesystem: true,
				},
			},
			expectViolations: false,
		},
		{
			name: "enforce read-only root - violation",
			rule: Rule{
				Name: "enforce-read-only-root",
				Type: RuleTypeReadOnlyRootFS,
				Config: map[string]interface{}{
					"enforce": true,
				},
			},
			spec: WorkloadSpec{
				SecurityContext: &PolicySecurityContext{
					ReadOnlyRootFilesystem: false,
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
		{
			name: "allow exceptions - compliant (in exception list)",
			rule: Rule{
				Name: "read-only-with-exceptions",
				Type: RuleTypeReadOnlyRootFS,
				Config: map[string]interface{}{
					"enforce":    true,
					"exceptions": []interface{}{"dev-workload", "test-workload"},
				},
			},
			spec: WorkloadSpec{
				Name: "dev-workload",
				SecurityContext: &PolicySecurityContext{
					ReadOnlyRootFilesystem: false,
				},
			},
			expectViolations: false,
		},
		{
			name: "allow exceptions - violation (not in exception list)",
			rule: Rule{
				Name: "read-only-with-exceptions",
				Type: RuleTypeReadOnlyRootFS,
				Config: map[string]interface{}{
					"enforce":    true,
					"exceptions": []interface{}{"dev-workload", "test-workload"},
				},
			},
			spec: WorkloadSpec{
				Name: "prod-workload",
				SecurityContext: &PolicySecurityContext{
					ReadOnlyRootFilesystem: false,
				},
			},
			expectViolations: true,
			violationCount:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			violations := evaluator.EvaluateRule(tt.rule, tt.spec)

			if tt.expectViolations {
				assert.NotEmpty(t, violations)
				assert.Equal(t, tt.violationCount, len(violations))
			} else {
				assert.Empty(t, violations)
			}
		})
	}
}

// TestPolicyEngineWithSecurityContext tests end-to-end policy evaluation
func TestPolicyEngineWithSecurityContext(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	// Add a comprehensive security policy
	policy := &Policy{
		Name:        "strict-security-policy",
		Namespace:   "production",
		Description: "Strict security requirements for production workloads",
		Enabled:     true,
		Priority:    100,
		Action:      PolicyActionDeny,
		Rules: []Rule{
			{
				Name: "require-security-context",
				Type: RuleTypeSecurityContext,
				Config: map[string]interface{}{
					"require_security_context": true,
					"forbid_privileged":        true,
					"require_non_root":         true,
					"require_read_only_root":   true,
				},
			},
			{
				Name: "require-seccomp",
				Type: RuleTypeSeccompProfile,
				Config: map[string]interface{}{
					"require_seccomp":   true,
					"forbid_unconfined": true,
				},
			},
			{
				Name: "require-sandboxed-runtime",
				Type: RuleTypeRuntimeClass,
				Config: map[string]interface{}{
					"require_sandboxed": true,
				},
			},
		},
	}

	err := engine.AddPolicy(policy)
	assert.NoError(t, err)

	tests := []struct {
		name          string
		spec          WorkloadSpec
		expectAllowed bool
		expectReason  string
	}{
		{
			name: "compliant workload",
			spec: WorkloadSpec{
				Name:      "secure-app",
				Namespace: "production",
				SecurityContext: &PolicySecurityContext{
					Linux: &PolicyLinuxSecurityOptions{
						SeccompProfile: &PolicySeccompProfile{
							Type: "RuntimeDefault",
						},
					},
					Privileged:             false,
					RunAsNonRoot:           func() *bool { b := true; return &b }(),
					ReadOnlyRootFilesystem: true,
					RuntimeClassName:       "gvisor",
				},
			},
			expectAllowed: true,
		},
		{
			name: "missing security context",
			spec: WorkloadSpec{
				Name:      "insecure-app",
				Namespace: "production",
			},
			expectAllowed: false,
		},
		{
			name: "privileged container",
			spec: WorkloadSpec{
				Name:      "privileged-app",
				Namespace: "production",
				SecurityContext: &PolicySecurityContext{
					Privileged: true,
				},
			},
			expectAllowed: false,
		},
		{
			name: "missing seccomp profile",
			spec: WorkloadSpec{
				Name:      "no-seccomp-app",
				Namespace: "production",
				SecurityContext: &PolicySecurityContext{
					Linux: nil,
				},
			},
			expectAllowed: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := PolicyContext{
				Namespace: tt.spec.Namespace,
			}

			result, err := engine.Evaluate(ctx, tt.spec)
			assert.NoError(t, err)
			assert.Equal(t, tt.expectAllowed, result.Allowed)

			if !tt.expectAllowed {
				assert.NotEmpty(t, result.Violations)
				t.Logf("Violations for %s: %v", tt.name, result.Violations)
			}
		})
	}
}
