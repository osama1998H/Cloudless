package policy

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestNewEngine tests policy engine creation
func TestNewEngine(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name   string
		config EngineConfig
	}{
		{
			name:   "Default configuration",
			config: DefaultEngineConfig(),
		},
		{
			name: "Custom configuration",
			config: EngineConfig{
				DefaultAction:      PolicyActionAllow,
				EnableAuditLogging: false,
				MaxEvaluationTime:  10 * time.Second,
				CacheEnabled:       false,
				CacheTTL:           10 * time.Minute,
			},
		},
		{
			name: "Default deny configuration",
			config: EngineConfig{
				DefaultAction:      PolicyActionDeny,
				EnableAuditLogging: true,
				MaxEvaluationTime:  5 * time.Second,
				CacheEnabled:       true,
				CacheTTL:           5 * time.Minute,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine := NewEngine(tt.config, logger)
			if engine == nil {
				t.Fatal("Expected non-nil engine")
			}

			if engine.config.DefaultAction != tt.config.DefaultAction {
				t.Errorf("Expected default action %v, got %v", tt.config.DefaultAction, engine.config.DefaultAction)
			}

			if engine.evaluator == nil {
				t.Error("Expected non-nil evaluator")
			}

			if engine.policies == nil {
				t.Error("Expected non-nil policies map")
			}

			if engine.cache == nil {
				t.Error("Expected non-nil cache map")
			}
		})
	}
}

// TestEngine_AddPolicy tests adding policies to the engine
func TestEngine_AddPolicy(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Add valid policy", func(t *testing.T) {
		policy := &Policy{
			Name:        "test-policy",
			Namespace:   "default",
			Description: "Test policy",
			Enabled:     true,
			Priority:    100,
			Action:      PolicyActionDeny,
			Rules:       []Rule{},
		}

		err := engine.AddPolicy(policy)
		if err != nil {
			t.Fatalf("AddPolicy() error = %v", err)
		}

		retrieved, err := engine.GetPolicy("test-policy")
		if err != nil {
			t.Fatalf("GetPolicy() error = %v", err)
		}

		if retrieved.Name != "test-policy" {
			t.Errorf("Expected policy name 'test-policy', got '%s'", retrieved.Name)
		}

		if retrieved.CreatedAt.IsZero() {
			t.Error("Expected non-zero CreatedAt timestamp")
		}

		if retrieved.UpdatedAt.IsZero() {
			t.Error("Expected non-zero UpdatedAt timestamp")
		}
	})

	t.Run("Add policy without name", func(t *testing.T) {
		policy := &Policy{
			Namespace: "default",
			Enabled:   true,
			Action:    PolicyActionDeny,
		}

		err := engine.AddPolicy(policy)
		if err == nil {
			t.Error("Expected error for policy without name")
		}

		if !strings.Contains(err.Error(), "name is required") {
			t.Errorf("Expected 'name is required' error, got: %v", err)
		}
	})

	t.Run("Update existing policy", func(t *testing.T) {
		policy := &Policy{
			Name:        "update-test",
			Namespace:   "default",
			Description: "Original description",
			Enabled:     true,
			Priority:    50,
			Action:      PolicyActionDeny,
		}

		err := engine.AddPolicy(policy)
		if err != nil {
			t.Fatalf("AddPolicy() error = %v", err)
		}

		// Update policy
		policy.Description = "Updated description"
		policy.Priority = 75

		err = engine.AddPolicy(policy)
		if err != nil {
			t.Fatalf("AddPolicy() update error = %v", err)
		}

		retrieved, _ := engine.GetPolicy("update-test")
		if retrieved.Description != "Updated description" {
			t.Errorf("Expected updated description, got '%s'", retrieved.Description)
		}

		if retrieved.Priority != 75 {
			t.Errorf("Expected updated priority 75, got %d", retrieved.Priority)
		}
	})
}

// TestEngine_RemovePolicy tests removing policies
func TestEngine_RemovePolicy(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Remove existing policy", func(t *testing.T) {
		policy := &Policy{
			Name:      "remove-test",
			Namespace: "default",
			Enabled:   true,
			Action:    PolicyActionDeny,
		}

		engine.AddPolicy(policy)

		err := engine.RemovePolicy("remove-test")
		if err != nil {
			t.Fatalf("RemovePolicy() error = %v", err)
		}

		_, err = engine.GetPolicy("remove-test")
		if err == nil {
			t.Error("Expected error when getting removed policy")
		}
	})

	t.Run("Remove non-existent policy", func(t *testing.T) {
		err := engine.RemovePolicy("does-not-exist")
		if err == nil {
			t.Error("Expected error when removing non-existent policy")
		}

		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("Expected 'not found' error, got: %v", err)
		}
	})
}

// TestEngine_ListPolicies tests listing policies
func TestEngine_ListPolicies(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Empty policy list", func(t *testing.T) {
		policies := engine.ListPolicies()
		if len(policies) != 0 {
			t.Errorf("Expected 0 policies, got %d", len(policies))
		}
	})

	t.Run("List multiple policies", func(t *testing.T) {
		policy1 := &Policy{Name: "policy1", Namespace: "ns1", Enabled: true, Action: PolicyActionDeny}
		policy2 := &Policy{Name: "policy2", Namespace: "ns2", Enabled: true, Action: PolicyActionAllow}
		policy3 := &Policy{Name: "policy3", Namespace: "ns3", Enabled: false, Action: PolicyActionAudit}

		engine.AddPolicy(policy1)
		engine.AddPolicy(policy2)
		engine.AddPolicy(policy3)

		policies := engine.ListPolicies()
		if len(policies) != 3 {
			t.Errorf("Expected 3 policies, got %d", len(policies))
		}
	})

	t.Run("List does not include deleted policies", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{Name: "deleted-policy", Namespace: "ns", Enabled: true, Action: PolicyActionDeny}
		deletedTime := time.Now()
		policy.DeletedAt = &deletedTime

		engine2.AddPolicy(policy)

		policies := engine2.ListPolicies()
		for _, p := range policies {
			if p.Name == "deleted-policy" {
				t.Error("Expected deleted policy to be excluded from list")
			}
		}
	})
}

// TestEngine_GetPolicy tests retrieving policies
func TestEngine_GetPolicy(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	policy := &Policy{
		Name:        "get-test",
		Namespace:   "default",
		Description: "Test policy for retrieval",
		Enabled:     true,
		Priority:    100,
		Action:      PolicyActionDeny,
	}

	engine.AddPolicy(policy)

	t.Run("Get existing policy", func(t *testing.T) {
		retrieved, err := engine.GetPolicy("get-test")
		if err != nil {
			t.Fatalf("GetPolicy() error = %v", err)
		}

		if retrieved.Name != "get-test" {
			t.Errorf("Expected policy name 'get-test', got '%s'", retrieved.Name)
		}

		if retrieved.Description != policy.Description {
			t.Errorf("Expected description '%s', got '%s'", policy.Description, retrieved.Description)
		}
	})

	t.Run("Get non-existent policy", func(t *testing.T) {
		_, err := engine.GetPolicy("does-not-exist")
		if err == nil {
			t.Error("Expected error for non-existent policy")
		}

		if !strings.Contains(err.Error(), "not found") {
			t.Errorf("Expected 'not found' error, got: %v", err)
		}
	})
}

// TestEngine_Evaluate_DefaultDeny tests default deny behavior
func TestEngine_Evaluate_DefaultDeny(t *testing.T) {
	logger := zap.NewNop()
	config := DefaultEngineConfig()
	config.DefaultAction = PolicyActionDeny
	engine := NewEngine(config, logger)

	spec := WorkloadSpec{
		Name:      "test-workload",
		Namespace: "default",
		Image:     "nginx:latest",
	}

	ctx := PolicyContext{
		Namespace: "default",
		User:      "test-user",
	}

	result, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	if result.Allowed {
		t.Error("Expected workload to be denied by default deny")
	}

	if !strings.Contains(result.Reason, "default deny") {
		t.Errorf("Expected 'default deny' in reason, got: %s", result.Reason)
	}
}

// TestEngine_Evaluate_DefaultAllow tests default allow behavior
func TestEngine_Evaluate_DefaultAllow(t *testing.T) {
	logger := zap.NewNop()
	config := DefaultEngineConfig()
	config.DefaultAction = PolicyActionAllow
	engine := NewEngine(config, logger)

	spec := WorkloadSpec{
		Name:      "test-workload",
		Namespace: "default",
		Image:     "nginx:latest",
	}

	ctx := PolicyContext{
		Namespace: "default",
		User:      "test-user",
	}

	result, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	if !result.Allowed {
		t.Error("Expected workload to be allowed by default allow")
	}

	if !strings.Contains(result.Reason, "default allow") {
		t.Errorf("Expected 'default allow' in reason, got: %s", result.Reason)
	}
}

// TestEngine_Evaluate_Priority tests policy priority ordering
func TestEngine_Evaluate_Priority(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	// Add low priority allow policy
	allowPolicy := &Policy{
		Name:      "low-priority-allow",
		Namespace: "*",
		Enabled:   true,
		Priority:  10,
		Action:    PolicyActionAllow,
		Rules:     []Rule{},
	}
	engine.AddPolicy(allowPolicy)

	// Add high priority deny policy
	denyPolicy := &Policy{
		Name:      "high-priority-deny",
		Namespace: "*",
		Enabled:   true,
		Priority:  100,
		Action:    PolicyActionDeny,
		Rules: []Rule{
			{
				Name: "deny-privileged",
				Type: RuleTypePrivilegedMode,
				Config: map[string]interface{}{
					"allow_privileged": false,
				},
			},
		},
	}
	engine.AddPolicy(denyPolicy)

	spec := WorkloadSpec{
		Name:       "test-workload",
		Namespace:  "default",
		Image:      "nginx:latest",
		Privileged: true,
	}

	ctx := PolicyContext{
		Namespace: "default",
	}

	result, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	// High priority deny should override low priority allow
	if result.Allowed {
		t.Error("Expected high priority deny policy to block workload")
	}

	// Check that high priority policy was matched
	matchedHighPriority := false
	for _, matched := range result.MatchedPolicies {
		if matched == "high-priority-deny" {
			matchedHighPriority = true
			break
		}
	}

	if !matchedHighPriority {
		t.Error("Expected high priority deny policy to be matched")
	}
}

// TestEvaluateImageRegistry tests image registry restrictions
func TestEvaluateImageRegistry(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Allow approved registry", func(t *testing.T) {
		policy := &Policy{
			Name:      "registry-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "registry-allowlist",
					Type: RuleTypeImageRegistry,
					Config: map[string]interface{}{
						"allowed_registries": []interface{}{
							"docker.io",
							"gcr.io",
						},
					},
				},
			},
		}
		engine.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "docker.io/library/nginx:latest",
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) > 0 {
			t.Errorf("Expected no violations for approved registry, got %d violations", len(result.Violations))
		}
	})

	t.Run("Deny unapproved registry", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "registry-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "registry-allowlist",
					Type: RuleTypeImageRegistry,
					Config: map[string]interface{}{
						"allowed_registries": []interface{}{
							"docker.io",
						},
					},
				},
			},
		}
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "malicious.registry.com/bad/image:latest",
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for unapproved registry")
		}

		if !strings.Contains(result.Violations[0].Message, "not in allowed list") {
			t.Errorf("Expected registry violation message, got: %s", result.Violations[0].Message)
		}
	})

	t.Run("Deny explicitly denied registry", func(t *testing.T) {
		engine3 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "registry-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "registry-denylist",
					Type: RuleTypeImageRegistry,
					Config: map[string]interface{}{
						"denied_registries": []interface{}{
							"*.malicious.com",
						},
					},
				},
			},
		}
		engine3.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "repo.malicious.com/bad/image:latest",
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine3.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for denied registry")
		}

		if !strings.Contains(result.Violations[0].Message, "denied by policy") {
			t.Errorf("Expected denied registry message, got: %s", result.Violations[0].Message)
		}
	})
}

// TestEvaluateCapabilities tests Linux capabilities restrictions
func TestEvaluateCapabilities(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Allow safe capabilities", func(t *testing.T) {
		policy := &Policy{
			Name:      "caps-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "allowed-caps",
					Type: RuleTypeCapabilities,
					Config: map[string]interface{}{
						"allowed_capabilities": []interface{}{
							"NET_BIND_SERVICE",
							"CHOWN",
						},
					},
				},
			},
		}
		engine.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:         "test-workload",
			Namespace:    "default",
			Image:        "nginx:latest",
			Capabilities: []string{"NET_BIND_SERVICE", "CHOWN"},
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) > 0 {
			t.Errorf("Expected no violations for safe capabilities, got %d", len(result.Violations))
		}
	})

	t.Run("Deny dangerous capabilities", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "caps-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "denied-caps",
					Type: RuleTypeCapabilities,
					Config: map[string]interface{}{
						"denied_capabilities": []interface{}{
							"SYS_ADMIN",
							"SYS_MODULE",
						},
					},
				},
			},
		}
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:         "test-workload",
			Namespace:    "default",
			Image:        "nginx:latest",
			Capabilities: []string{"SYS_ADMIN"},
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for dangerous capabilities")
		}

		if !strings.Contains(result.Violations[0].Message, "SYS_ADMIN") {
			t.Errorf("Expected SYS_ADMIN in violation message, got: %s", result.Violations[0].Message)
		}

		if result.Violations[0].Severity != "Critical" {
			t.Errorf("Expected Critical severity, got: %s", result.Violations[0].Severity)
		}
	})

	t.Run("Deny capability not in allowlist", func(t *testing.T) {
		engine3 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "caps-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "strict-allowlist",
					Type: RuleTypeCapabilities,
					Config: map[string]interface{}{
						"allowed_capabilities": []interface{}{
							"NET_BIND_SERVICE",
						},
					},
				},
			},
		}
		engine3.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:         "test-workload",
			Namespace:    "default",
			Image:        "nginx:latest",
			Capabilities: []string{"CHOWN"}, // Not in allowlist
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine3.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for capability not in allowlist")
		}

		if !strings.Contains(result.Violations[0].Message, "not in allowed list") {
			t.Errorf("Expected 'not in allowed list' message, got: %s", result.Violations[0].Message)
		}
	})
}

// TestEvaluatePrivilegedMode tests privileged container restrictions
func TestEvaluatePrivilegedMode(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Deny privileged containers", func(t *testing.T) {
		policy := &Policy{
			Name:      "no-privileged",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "restrict-privileged",
					Type: RuleTypePrivilegedMode,
					Config: map[string]interface{}{
						"allow_privileged": false,
					},
				},
			},
		}
		engine.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:       "test-workload",
			Namespace:  "default",
			Image:      "nginx:latest",
			Privileged: true,
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for privileged container")
		}

		if !strings.Contains(result.Violations[0].Message, "Privileged mode is not allowed") {
			t.Errorf("Expected privileged mode violation, got: %s", result.Violations[0].Message)
		}

		if result.Violations[0].Severity != "Critical" {
			t.Errorf("Expected Critical severity, got: %s", result.Violations[0].Severity)
		}
	})

	t.Run("Allow non-privileged containers", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "no-privileged",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "restrict-privileged",
					Type: RuleTypePrivilegedMode,
					Config: map[string]interface{}{
						"allow_privileged": false,
					},
				},
			},
		}
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:       "test-workload",
			Namespace:  "default",
			Image:      "nginx:latest",
			Privileged: false,
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) > 0 {
			t.Errorf("Expected no violations for non-privileged container, got %d", len(result.Violations))
		}
	})
}

// TestEvaluateHostNetwork tests host namespace restrictions
func TestEvaluateHostNetwork(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Deny host network access", func(t *testing.T) {
		policy := &Policy{
			Name:      "no-host-namespaces",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "restrict-host-network",
					Type: RuleTypeHostNetwork,
					Config: map[string]interface{}{
						"allow_host_network": false,
						"allow_host_pid":     false,
						"allow_host_ipc":     false,
					},
				},
			},
		}
		engine.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:        "test-workload",
			Namespace:   "default",
			Image:       "nginx:latest",
			HostNetwork: true,
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for host network access")
		}

		if !strings.Contains(result.Violations[0].Message, "Host network access is not allowed") {
			t.Errorf("Expected host network violation, got: %s", result.Violations[0].Message)
		}
	})

	t.Run("Deny host PID namespace", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "no-host-namespaces",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "restrict-host-pid",
					Type: RuleTypeHostNetwork,
					Config: map[string]interface{}{
						"allow_host_network": false,
						"allow_host_pid":     false,
						"allow_host_ipc":     false,
					},
				},
			},
		}
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "nginx:latest",
			HostPID:   true,
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for host PID namespace")
		}

		if !strings.Contains(result.Violations[0].Message, "Host PID namespace") {
			t.Errorf("Expected host PID violation, got: %s", result.Violations[0].Message)
		}
	})

	t.Run("Deny host IPC namespace", func(t *testing.T) {
		engine3 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "no-host-namespaces",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "restrict-host-ipc",
					Type: RuleTypeHostNetwork,
					Config: map[string]interface{}{
						"allow_host_network": false,
						"allow_host_pid":     false,
						"allow_host_ipc":     false,
					},
				},
			},
		}
		engine3.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "nginx:latest",
			HostIPC:   true,
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine3.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for host IPC namespace")
		}

		if !strings.Contains(result.Violations[0].Message, "Host IPC namespace") {
			t.Errorf("Expected host IPC violation, got: %s", result.Violations[0].Message)
		}
	})

	t.Run("Allow workload without host namespaces", func(t *testing.T) {
		engine4 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "no-host-namespaces",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "restrict-host-access",
					Type: RuleTypeHostNetwork,
					Config: map[string]interface{}{
						"allow_host_network": false,
						"allow_host_pid":     false,
						"allow_host_ipc":     false,
					},
				},
			},
		}
		engine4.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:        "test-workload",
			Namespace:   "default",
			Image:       "nginx:latest",
			HostNetwork: false,
			HostPID:     false,
			HostIPC:     false,
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine4.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) > 0 {
			t.Errorf("Expected no violations, got %d", len(result.Violations))
		}
	})
}

// TestEvaluateVolumeType tests volume type restrictions
func TestEvaluateVolumeType(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Deny hostPath volumes", func(t *testing.T) {
		policy := &Policy{
			Name:      "no-hostpath",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "restrict-hostpath",
					Type: RuleTypeVolumeType,
					Config: map[string]interface{}{
						"denied_types": []interface{}{
							"hostPath",
						},
					},
				},
			},
		}
		engine.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "nginx:latest",
			Volumes: []VolumeSpec{
				{
					Name:      "host-vol",
					Type:      "hostPath",
					Source:    "/var/lib/data",
					MountPath: "/data",
				},
			},
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for hostPath volume")
		}

		if !strings.Contains(result.Violations[0].Message, "hostPath") {
			t.Errorf("Expected hostPath in violation, got: %s", result.Violations[0].Message)
		}
	})

	t.Run("Allow safe volume types", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "volume-allowlist",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "allowed-volumes",
					Type: RuleTypeVolumeType,
					Config: map[string]interface{}{
						"allowed_types": []interface{}{
							"emptyDir",
							"configMap",
							"secret",
						},
					},
				},
			},
		}
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "nginx:latest",
			Volumes: []VolumeSpec{
				{Name: "config", Type: "configMap", Source: "app-config"},
				{Name: "secrets", Type: "secret", Source: "app-secret"},
				{Name: "temp", Type: "emptyDir"},
			},
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) > 0 {
			t.Errorf("Expected no violations for safe volume types, got %d", len(result.Violations))
		}
	})
}

// TestEvaluateEgressDestination tests egress destination restrictions
func TestEvaluateEgressDestination(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Allow egress to approved destinations", func(t *testing.T) {
		policy := &Policy{
			Name:      "egress-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "egress-allowlist",
					Type: RuleTypeEgressDestination,
					Config: map[string]interface{}{
						"allowed_destinations": []interface{}{
							"*.example.com",
							"api.company.com",
						},
					},
				},
			},
		}
		engine.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:          "test-workload",
			Namespace:     "default",
			Image:         "nginx:latest",
			EgressDomains: []string{"api.example.com", "api.company.com"},
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) > 0 {
			t.Errorf("Expected no violations for approved destinations, got %d", len(result.Violations))
		}
	})

	t.Run("Deny egress to unapproved destinations", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "egress-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "egress-allowlist",
					Type: RuleTypeEgressDestination,
					Config: map[string]interface{}{
						"allowed_destinations": []interface{}{
							"*.example.com",
						},
					},
				},
			},
		}
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:          "test-workload",
			Namespace:     "default",
			Image:         "nginx:latest",
			EgressDomains: []string{"malicious.com"},
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for unapproved egress destination")
		}

		if !strings.Contains(result.Violations[0].Message, "not in allowed list") {
			t.Errorf("Expected 'not in allowed list' message, got: %s", result.Violations[0].Message)
		}
	})
}

// TestEvaluateResourceLimit tests resource limit enforcement
func TestEvaluateResourceLimit(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Enforce CPU limit", func(t *testing.T) {
		policy := &Policy{
			Name:      "resource-limits",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "max-cpu",
					Type: RuleTypeResourceLimit,
					Config: map[string]interface{}{
						"max_cpu_millicores": float64(4000), // 4 cores max
					},
				},
			},
		}
		engine.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:       "test-workload",
			Namespace:  "default",
			Image:      "nginx:latest",
			CPURequest: 8000, // Exceeds limit
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for CPU limit exceeded")
		}

		if !strings.Contains(result.Violations[0].Message, "exceeds maximum") {
			t.Errorf("Expected 'exceeds maximum' message, got: %s", result.Violations[0].Message)
		}
	})

	t.Run("Enforce memory limit", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "resource-limits",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "max-memory",
					Type: RuleTypeResourceLimit,
					Config: map[string]interface{}{
						"max_memory_bytes": float64(4 * 1024 * 1024 * 1024), // 4GB max
					},
				},
			},
		}
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:          "test-workload",
			Namespace:     "default",
			Image:         "nginx:latest",
			MemoryRequest: 8 * 1024 * 1024 * 1024, // 8GB exceeds limit
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for memory limit exceeded")
		}

		if !strings.Contains(result.Violations[0].Message, "Memory request") {
			t.Errorf("Expected memory request violation, got: %s", result.Violations[0].Message)
		}
	})

	t.Run("Require resource limits", func(t *testing.T) {
		engine3 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "require-limits",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "must-set-limits",
					Type: RuleTypeResourceLimit,
					Config: map[string]interface{}{
						"require_limits": true,
					},
				},
			},
		}
		engine3.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "nginx:latest",
			CPULimit:  0, // Not set
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine3.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for missing resource limits")
		}

		foundCPUViolation := false
		for _, v := range result.Violations {
			if strings.Contains(v.Message, "CPU limit is required") {
				foundCPUViolation = true
				break
			}
		}

		if !foundCPUViolation {
			t.Error("Expected CPU limit required violation")
		}
	})
}

// TestEvaluateNodeSelector tests node selector restrictions
func TestEvaluateNodeSelector(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Require specific node labels", func(t *testing.T) {
		requiredLabels := map[string]interface{}{
			"environment": "production",
			"zone":        "", // Must exist but any value
		}

		policy := &Policy{
			Name:      "node-selector-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "require-node-labels",
					Type: RuleTypeNodeSelector,
					Config: map[string]interface{}{
						"required_labels": requiredLabels,
					},
				},
			},
		}
		engine.AddPolicy(policy)

		// Missing required label
		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "nginx:latest",
			NodeSelector: map[string]string{
				"zone": "us-east-1a",
				// Missing "environment" label
			},
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for missing required node label")
		}

		if !strings.Contains(result.Violations[0].Message, "missing") {
			t.Errorf("Expected 'missing' in violation, got: %s", result.Violations[0].Message)
		}
	})
}

// TestMatchPattern tests wildcard pattern matching
func TestMatchPattern(t *testing.T) {
	logger := zap.NewNop()
	evaluator := NewEvaluator(logger)

	tests := []struct {
		name    string
		value   string
		pattern string
		matches bool
	}{
		{
			name:    "Exact match",
			value:   "docker.io",
			pattern: "docker.io",
			matches: true,
		},
		{
			name:    "Wildcard subdomain",
			value:   "repo.example.com",
			pattern: "*.example.com",
			matches: true,
		},
		{
			name:    "Wildcard no match",
			value:   "other.com",
			pattern: "*.example.com",
			matches: false,
		},
		{
			name:    "Match all wildcard",
			value:   "anything",
			pattern: "*",
			matches: true,
		},
		{
			name:    "Case insensitive",
			value:   "Docker.IO",
			pattern: "docker.io",
			matches: true,
		},
		{
			name:    "No match different domain",
			value:   "gcr.io",
			pattern: "docker.io",
			matches: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluator.matchesPattern(tt.value, tt.pattern)
			if result != tt.matches {
				t.Errorf("matchesPattern(%s, %s) = %v, want %v", tt.value, tt.pattern, result, tt.matches)
			}
		})
	}
}

// TestExtractRegistry tests registry extraction from image references
func TestExtractRegistry(t *testing.T) {
	logger := zap.NewNop()
	evaluator := NewEvaluator(logger)

	tests := []struct {
		name     string
		image    string
		expected string
	}{
		{
			name:     "Full image reference",
			image:    "docker.io/library/nginx:latest",
			expected: "docker.io",
		},
		{
			name:     "GCR image",
			image:    "gcr.io/project/image:tag",
			expected: "gcr.io",
		},
		{
			name:     "Image without registry",
			image:    "nginx:latest",
			expected: "docker.io",
		},
		{
			name:     "Image without tag",
			image:    "nginx",
			expected: "docker.io",
		},
		{
			name:     "Private registry with port",
			image:    "registry.company.com:5000/app:v1",
			expected: "registry.company.com:5000",
		},
		{
			name:     "Docker Hub library image",
			image:    "library/nginx",
			expected: "docker.io",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := evaluator.extractRegistry(tt.image)
			if result != tt.expected {
				t.Errorf("extractRegistry(%s) = %s, want %s", tt.image, result, tt.expected)
			}
		})
	}
}

// TestRestrictPrivilegedContainersPolicy tests the default privileged policy
func TestRestrictPrivilegedContainersPolicy(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	policy := RestrictPrivilegedContainersPolicy()
	engine.AddPolicy(policy)

	t.Run("Block privileged container", func(t *testing.T) {
		spec := WorkloadSpec{
			Name:       "privileged-workload",
			Namespace:  "default",
			Image:      "nginx:latest",
			Privileged: true,
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if result.Allowed {
			t.Error("Expected privileged container to be blocked")
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations for privileged container")
		}
	})

	t.Run("Allow non-privileged container", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)
		engine2.AddPolicy(AllowDefaultNamespacePolicy())
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:       "normal-workload",
			Namespace:  "default",
			Image:      "nginx:latest",
			Privileged: false,
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		// With allow policy, non-privileged should be allowed
		if !result.Allowed {
			t.Error("Expected non-privileged container to be allowed")
		}
	})
}

// TestRestrictHostNamespacesPolicy tests the default host namespaces policy
func TestRestrictHostNamespacesPolicy(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	policy := RestrictHostNamespacesPolicy()
	engine.AddPolicy(policy)

	spec := WorkloadSpec{
		Name:        "test-workload",
		Namespace:   "default",
		Image:       "nginx:latest",
		HostNetwork: true,
		HostPID:     true,
		HostIPC:     true,
	}

	ctx := PolicyContext{Namespace: "default"}
	result, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	if result.Allowed {
		t.Error("Expected workload with host namespaces to be blocked")
	}

	// Should have 3 violations (network, PID, IPC)
	if len(result.Violations) != 3 {
		t.Errorf("Expected 3 violations, got %d", len(result.Violations))
	}
}

// TestDenyDangerousCapabilitiesPolicy tests the default capabilities policy
func TestDenyDangerousCapabilitiesPolicy(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	policy := DenyDangerousCapabilitiesPolicy()
	engine.AddPolicy(policy)

	t.Run("Block dangerous capabilities", func(t *testing.T) {
		spec := WorkloadSpec{
			Name:         "test-workload",
			Namespace:    "default",
			Image:        "nginx:latest",
			Capabilities: []string{"SYS_ADMIN", "SYS_MODULE"},
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if result.Allowed {
			t.Error("Expected workload with dangerous capabilities to be blocked")
		}

		// Should have violations for both SYS_ADMIN and SYS_MODULE
		if len(result.Violations) < 2 {
			t.Errorf("Expected at least 2 violations, got %d", len(result.Violations))
		}
	})

	t.Run("Allow safe capabilities", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)
		engine2.AddPolicy(AllowDefaultNamespacePolicy())
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:         "test-workload",
			Namespace:    "default",
			Image:        "nginx:latest",
			Capabilities: []string{"NET_BIND_SERVICE", "CHOWN"},
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		// Safe capabilities should have no violations from capabilities rule
		hasCapViolation := false
		for _, v := range result.Violations {
			if strings.Contains(v.Field, "capabilities") {
				hasCapViolation = true
				break
			}
		}

		if hasCapViolation {
			t.Error("Expected safe capabilities to be allowed")
		}
	})
}

// TestAllowedRegistriesPolicy tests the default registry policy
func TestAllowedRegistriesPolicy(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	policy := AllowedRegistriesPolicy()
	engine.AddPolicy(policy)

	t.Run("Allow approved registry", func(t *testing.T) {
		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "docker.io/library/nginx:latest",
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		hasRegistryViolation := false
		for _, v := range result.Violations {
			if v.Field == "image" {
				hasRegistryViolation = true
				break
			}
		}

		if hasRegistryViolation {
			t.Error("Expected approved registry to be allowed")
		}
	})

	t.Run("Block unapproved registry", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "malicious-registry.com/bad/image:latest",
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		hasRegistryViolation := false
		for _, v := range result.Violations {
			if v.Field == "image" {
				hasRegistryViolation = true
				break
			}
		}

		if !hasRegistryViolation {
			t.Error("Expected unapproved registry to be blocked")
		}
	})
}

// TestRequireResourceLimitsPolicy tests the default resource limits policy
func TestRequireResourceLimitsPolicy(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	policy := RequireResourceLimitsPolicy()
	engine.AddPolicy(policy)

	spec := WorkloadSpec{
		Name:        "test-workload",
		Namespace:   "default",
		Image:       "nginx:latest",
		CPULimit:    0, // Not set
		MemoryLimit: 0, // Not set
	}

	ctx := PolicyContext{Namespace: "default"}
	result, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	if result.Allowed {
		t.Error("Expected workload without resource limits to be blocked")
	}

	// Should have violations for both CPU and memory limits
	if len(result.Violations) < 2 {
		t.Errorf("Expected at least 2 violations (CPU and memory), got %d", len(result.Violations))
	}
}

// TestPolicyPriority tests priority-based evaluation
func TestPolicyPriority(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	// Add multiple policies with different priorities
	highPriorityDeny := &Policy{
		Name:      "high-priority-deny",
		Namespace: "*",
		Enabled:   true,
		Priority:  200,
		Action:    PolicyActionDeny,
		Rules: []Rule{
			{
				Name: "deny-privileged",
				Type: RuleTypePrivilegedMode,
				Config: map[string]interface{}{
					"allow_privileged": false,
				},
			},
		},
	}

	mediumPriorityAllow := &Policy{
		Name:      "medium-priority-allow",
		Namespace: "*",
		Enabled:   true,
		Priority:  100,
		Action:    PolicyActionAllow,
		Rules:     []Rule{}, // Matches all
	}

	lowPriorityDeny := &Policy{
		Name:      "low-priority-deny",
		Namespace: "*",
		Enabled:   true,
		Priority:  50,
		Action:    PolicyActionDeny,
		Rules: []Rule{
			{
				Name: "deny-all",
				Type: RuleTypePrivilegedMode,
				Config: map[string]interface{}{
					"allow_privileged": false,
				},
			},
		},
	}

	engine.AddPolicy(lowPriorityDeny)
	engine.AddPolicy(highPriorityDeny)
	engine.AddPolicy(mediumPriorityAllow)

	spec := WorkloadSpec{
		Name:       "test-workload",
		Namespace:  "default",
		Image:      "nginx:latest",
		Privileged: true,
	}

	ctx := PolicyContext{Namespace: "default"}
	result, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	// High priority deny should block despite medium priority allow
	if result.Allowed {
		t.Error("Expected high priority deny to block workload")
	}

	// Check that high priority policy was evaluated
	matchedHighPriority := false
	for _, matched := range result.MatchedPolicies {
		if matched == "high-priority-deny" {
			matchedHighPriority = true
			break
		}
	}

	if !matchedHighPriority {
		t.Error("Expected high priority policy to be matched")
	}
}

// TestPolicyConditions tests conditional rule application
func TestPolicyConditions(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	policy := &Policy{
		Name:      "conditional-policy",
		Namespace: "production",
		Enabled:   true,
		Priority:  100,
		Action:    PolicyActionDeny,
		Rules: []Rule{
			{
				Name: "prod-restrictions",
				Type: RuleTypePrivilegedMode,
				Config: map[string]interface{}{
					"allow_privileged": false,
				},
				Conditions: []Condition{
					{
						Field:    "labels.env",
						Operator: OperatorEqual,
						Value:    "production",
					},
				},
			},
		},
	}
	engine.AddPolicy(policy)

	t.Run("Condition met - apply rule", func(t *testing.T) {
		spec := WorkloadSpec{
			Name:       "test-workload",
			Namespace:  "production",
			Image:      "nginx:latest",
			Privileged: true,
			Labels: map[string]string{
				"env": "production",
			},
		}

		ctx := PolicyContext{Namespace: "production"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		if len(result.Violations) == 0 {
			t.Error("Expected violations when condition is met")
		}
	})

	t.Run("Condition not met - skip rule", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)
		engine2.AddPolicy(AllowDefaultNamespacePolicy())
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:       "test-workload",
			Namespace:  "production",
			Image:      "nginx:latest",
			Privileged: true,
			Labels: map[string]string{
				"env": "development", // Condition not met
			},
		}

		ctx := PolicyContext{Namespace: "production"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		// Rule should be skipped, no privileged violation
		hasPrivilegedViolation := false
		for _, v := range result.Violations {
			if strings.Contains(v.Message, "Privileged") {
				hasPrivilegedViolation = true
				break
			}
		}

		if hasPrivilegedViolation {
			t.Error("Expected rule to be skipped when condition not met")
		}
	})
}

// TestEngine_ConcurrentEvaluation tests parallel policy evaluations
func TestEngine_ConcurrentEvaluation(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	// Add default policies
	for _, policy := range DefaultPolicies() {
		engine.AddPolicy(policy)
	}

	var wg sync.WaitGroup
	numGoroutines := 50

	// Run multiple evaluations concurrently
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			spec := WorkloadSpec{
				Name:       fmt.Sprintf("workload-%d", id),
				Namespace:  "default",
				Image:      "nginx:latest",
				Privileged: false,
			}

			ctx := PolicyContext{
				Namespace: "default",
				User:      fmt.Sprintf("user-%d", id),
			}

			_, err := engine.Evaluate(ctx, spec)
			if err != nil {
				t.Errorf("Concurrent evaluation error: %v", err)
			}
		}(i)
	}

	wg.Wait()
}

// TestEngine_ConcurrentPolicyModification tests concurrent policy add/remove
func TestEngine_ConcurrentPolicyModification(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	var wg sync.WaitGroup
	numGoroutines := 20

	// Concurrently add and remove policies
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			policyName := fmt.Sprintf("policy-%d", id)

			// Add policy
			policy := &Policy{
				Name:      policyName,
				Namespace: "default",
				Enabled:   true,
				Priority:  id,
				Action:    PolicyActionDeny,
			}

			err := engine.AddPolicy(policy)
			if err != nil {
				t.Errorf("Concurrent add policy error: %v", err)
			}

			// Retrieve policy
			_, err = engine.GetPolicy(policyName)
			if err != nil {
				t.Errorf("Concurrent get policy error: %v", err)
			}

			// Remove policy
			err = engine.RemovePolicy(policyName)
			if err != nil {
				t.Errorf("Concurrent remove policy error: %v", err)
			}
		}(i)
	}

	wg.Wait()
}

// TestEngine_NilInputs tests nil input handling
func TestEngine_NilInputs(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Add nil policy", func(t *testing.T) {
		err := engine.AddPolicy(nil)
		if err == nil {
			t.Error("Expected error for nil policy")
		}
	})

	t.Run("Evaluate with empty context", func(t *testing.T) {
		spec := WorkloadSpec{
			Name:      "test",
			Namespace: "default",
		}

		ctx := PolicyContext{} // Empty context

		_, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}
		// Should not panic with empty context
	})
}

// TestEngine_EmptyRules tests policies with no rules
func TestEngine_EmptyRules(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	t.Run("Allow policy with no rules", func(t *testing.T) {
		policy := &Policy{
			Name:      "allow-all",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionAllow,
			Rules:     []Rule{}, // No rules
		}
		engine.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "nginx:latest",
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		// Allow policy with no rules should match
		if !result.Allowed {
			t.Error("Expected allow policy with no rules to allow workload")
		}

		matchedPolicy := false
		for _, matched := range result.MatchedPolicies {
			if matched == "allow-all" {
				matchedPolicy = true
				break
			}
		}

		if !matchedPolicy {
			t.Error("Expected allow-all policy to be matched")
		}
	})

	t.Run("Deny policy with no rules", func(t *testing.T) {
		engine2 := NewEngine(DefaultEngineConfig(), logger)

		policy := &Policy{
			Name:      "deny-policy",
			Namespace: "*",
			Enabled:   true,
			Priority:  100,
			Action:    PolicyActionDeny,
			Rules:     []Rule{}, // No rules
		}
		engine2.AddPolicy(policy)

		spec := WorkloadSpec{
			Name:      "test-workload",
			Namespace: "default",
			Image:     "nginx:latest",
		}

		ctx := PolicyContext{Namespace: "default"}
		result, err := engine2.Evaluate(ctx, spec)
		if err != nil {
			t.Fatalf("Evaluate() error = %v", err)
		}

		// Deny policy with no rules should not match (needs violations)
		if !result.Allowed {
			// Check reason - should be default deny
			if !strings.Contains(result.Reason, "default deny") {
				t.Errorf("Expected default deny reason, got: %s", result.Reason)
			}
		}
	})
}

// TestEngine_InvalidRuleType tests unknown rule types
func TestEngine_InvalidRuleType(t *testing.T) {
	logger := zap.NewNop()
	engine := NewEngine(DefaultEngineConfig(), logger)

	policy := &Policy{
		Name:      "invalid-rule-policy",
		Namespace: "*",
		Enabled:   true,
		Priority:  100,
		Action:    PolicyActionDeny,
		Rules: []Rule{
			{
				Name:   "unknown-rule",
				Type:   RuleType("UnknownRuleType"),
				Config: map[string]interface{}{},
			},
		},
	}
	engine.AddPolicy(policy)

	spec := WorkloadSpec{
		Name:      "test-workload",
		Namespace: "default",
		Image:     "nginx:latest",
	}

	ctx := PolicyContext{Namespace: "default"}

	// Should not panic with unknown rule type
	_, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}
}

// TestEngine_EvaluationTimeout tests timeout handling
func TestEngine_EvaluationTimeout(t *testing.T) {
	logger := zap.NewNop()
	config := DefaultEngineConfig()
	config.MaxEvaluationTime = 1 * time.Nanosecond // Very short timeout

	engine := NewEngine(config, logger)

	// Add many policies to increase evaluation time
	for i := 0; i < 100; i++ {
		policy := &Policy{
			Name:      fmt.Sprintf("policy-%d", i),
			Namespace: "*",
			Enabled:   true,
			Priority:  i,
			Action:    PolicyActionDeny,
			Rules: []Rule{
				{
					Name: "test-rule",
					Type: RuleTypePrivilegedMode,
					Config: map[string]interface{}{
						"allow_privileged": false,
					},
				},
			},
		}
		engine.AddPolicy(policy)
	}

	spec := WorkloadSpec{
		Name:      "test-workload",
		Namespace: "default",
		Image:     "nginx:latest",
	}

	ctx := PolicyContext{Namespace: "default"}

	// Evaluation may timeout
	_, err := engine.Evaluate(ctx, spec)
	if err != nil {
		if !strings.Contains(err.Error(), "timeout") {
			t.Errorf("Expected timeout error, got: %v", err)
		}
	}
}

// TestEngine_CacheExpiration tests cache TTL
func TestEngine_CacheExpiration(t *testing.T) {
	logger := zap.NewNop()
	config := DefaultEngineConfig()
	config.CacheEnabled = true
	config.CacheTTL = 100 * time.Millisecond

	engine := NewEngine(config, logger)

	policy := AllowDefaultNamespacePolicy()
	engine.AddPolicy(policy)

	spec := WorkloadSpec{
		Name:      "test-workload",
		Namespace: "default",
		Image:     "nginx:latest",
	}

	ctx := PolicyContext{Namespace: "default"}

	// First evaluation - not cached
	result1, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	// Second evaluation - should hit cache
	result2, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	if result1.Allowed != result2.Allowed {
		t.Error("Cached result should match original result")
	}

	// Wait for cache expiration
	time.Sleep(150 * time.Millisecond)

	// Third evaluation - cache expired, fresh evaluation
	result3, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	// Result should still be correct after cache expiration
	if result1.Allowed != result3.Allowed {
		t.Error("Result after cache expiration should match original")
	}
}

// TestEngine_AuditAction tests audit logging
func TestEngine_AuditAction(t *testing.T) {
	logger := zap.NewNop()
	config := DefaultEngineConfig()
	config.EnableAuditLogging = true

	engine := NewEngine(config, logger)

	policy := &Policy{
		Name:      "audit-policy",
		Namespace: "*",
		Enabled:   true,
		Priority:  100,
		Action:    PolicyActionAudit, // Audit action
		Rules: []Rule{
			{
				Name: "audit-privileged",
				Type: RuleTypePrivilegedMode,
				Config: map[string]interface{}{
					"allow_privileged": false,
				},
			},
		},
	}
	engine.AddPolicy(policy)

	spec := WorkloadSpec{
		Name:       "test-workload",
		Namespace:  "default",
		Image:      "nginx:latest",
		Privileged: true,
	}

	ctx := PolicyContext{Namespace: "default"}
	result, err := engine.Evaluate(ctx, spec)
	if err != nil {
		t.Fatalf("Evaluate() error = %v", err)
	}

	// Audit action should not block (allowed by default)
	if !result.Allowed {
		t.Error("Expected audit action to allow workload")
	}

	// Should have audit entries
	if len(result.AuditEntries) == 0 {
		t.Error("Expected audit entries for audit action")
	}

	// Check audit entry content
	auditEntry := result.AuditEntries[0]
	if auditEntry.PolicyName != "audit-policy" {
		t.Errorf("Expected policy name 'audit-policy', got '%s'", auditEntry.PolicyName)
	}

	if auditEntry.Action != "audit" {
		t.Errorf("Expected action 'audit', got '%s'", auditEntry.Action)
	}
}

// TestDefaultEngineConfig tests default configuration
func TestDefaultEngineConfig(t *testing.T) {
	config := DefaultEngineConfig()

	if config.DefaultAction != PolicyActionDeny {
		t.Errorf("Expected default action Deny, got %v", config.DefaultAction)
	}

	if !config.EnableAuditLogging {
		t.Error("Expected audit logging enabled by default")
	}

	if config.MaxEvaluationTime != 5*time.Second {
		t.Errorf("Expected max evaluation time 5s, got %v", config.MaxEvaluationTime)
	}

	if !config.CacheEnabled {
		t.Error("Expected cache enabled by default")
	}

	if config.CacheTTL != 5*time.Minute {
		t.Errorf("Expected cache TTL 5m, got %v", config.CacheTTL)
	}
}
