package policy

import (
	"time"
)

// DefaultPolicies returns a set of recommended default policies
func DefaultPolicies() []*Policy {
	return []*Policy{
		AllowDefaultNamespacePolicy(), // Allow policy (must come first)
		RestrictPrivilegedContainersPolicy(),
		RestrictHostNamespacesPolicy(),
		AllowedRegistriesPolicy(),
		DenyDangerousCapabilitiesPolicy(),
		RequireResourceLimitsPolicy(),
	}
}

// RestrictPrivilegedContainersPolicy denies privileged containers
func RestrictPrivilegedContainersPolicy() *Policy {
	return &Policy{
		Name:        "restrict-privileged-containers",
		Namespace:   "*",
		Description: "Denies privileged containers for security",
		Enabled:     true,
		Priority:    100,
		Action:      PolicyActionDeny,
		Rules: []Rule{
			{
				Name:        "no-privileged",
				Description: "Containers must not run in privileged mode",
				Type:        RuleTypePrivilegedMode,
				Config: map[string]interface{}{
					"allow_privileged": false,
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// RestrictHostNamespacesPolicy denies host namespace access
func RestrictHostNamespacesPolicy() *Policy {
	return &Policy{
		Name:        "restrict-host-namespaces",
		Namespace:   "*",
		Description: "Denies host network, PID, and IPC namespace access",
		Enabled:     true,
		Priority:    100,
		Action:      PolicyActionDeny,
		Rules: []Rule{
			{
				Name:        "no-host-namespaces",
				Description: "Containers must not access host namespaces",
				Type:        RuleTypeHostNetwork,
				Config: map[string]interface{}{
					"allow_host_network": false,
					"allow_host_pid":     false,
					"allow_host_ipc":     false,
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// AllowedRegistriesPolicy restricts image registries
func AllowedRegistriesPolicy() *Policy {
	return &Policy{
		Name:        "allowed-registries",
		Namespace:   "*",
		Description: "Restricts images to trusted registries",
		Enabled:     true,
		Priority:    90,
		Action:      PolicyActionDeny,
		Rules: []Rule{
			{
				Name:        "registry-allowlist",
				Description: "Images must come from allowed registries",
				Type:        RuleTypeImageRegistry,
				Config: map[string]interface{}{
					"allowed_registries": []interface{}{
						"docker.io",
						"gcr.io",
						"ghcr.io",
						"quay.io",
						"*.azurecr.io",
						"*.amazonaws.com",
					},
					"denied_registries": []interface{}{
						// Add specific denied registries here
					},
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// DenyDangerousCapabilitiesPolicy denies dangerous Linux capabilities
func DenyDangerousCapabilitiesPolicy() *Policy {
	return &Policy{
		Name:        "deny-dangerous-capabilities",
		Namespace:   "*",
		Description: "Denies dangerous Linux capabilities",
		Enabled:     true,
		Priority:    95,
		Action:      PolicyActionDeny,
		Rules: []Rule{
			{
				Name:        "no-dangerous-caps",
				Description: "Containers must not use dangerous capabilities",
				Type:        RuleTypeCapabilities,
				Config: map[string]interface{}{
					"denied_capabilities": []interface{}{
						"SYS_ADMIN",
						"SYS_MODULE",
						"SYS_RAWIO",
						"SYS_PTRACE",
						"SYS_BOOT",
						"MAC_ADMIN",
						"NET_ADMIN",
						"SETPCAP",
						"ALL",
					},
					"allowed_capabilities": []interface{}{
						// Allow safe capabilities
						"CHOWN",
						"DAC_OVERRIDE",
						"FOWNER",
						"FSETID",
						"KILL",
						"SETGID",
						"SETUID",
						"NET_BIND_SERVICE",
					},
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// RequireResourceLimitsPolicy requires resource limits on containers
func RequireResourceLimitsPolicy() *Policy {
	return &Policy{
		Name:        "require-resource-limits",
		Namespace:   "*",
		Description: "Requires CPU and memory limits on all containers",
		Enabled:     true,
		Priority:    80,
		Action:      PolicyActionDeny,
		Rules: []Rule{
			{
				Name:        "must-set-limits",
				Description: "Containers must define resource limits",
				Type:        RuleTypeResourceLimit,
				Config: map[string]interface{}{
					"require_limits":             true,
					"max_cpu_millicores":         float64(16000),                   // 16 cores max
					"max_memory_bytes":           float64(32 * 1024 * 1024 * 1024), // 32GB max
					"max_limit_to_request_ratio": float64(4.0),                     // Limit can't be more than 4x request
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// ProductionNamespacePolicy applies stricter rules to production namespaces
func ProductionNamespacePolicy() *Policy {
	return &Policy{
		Name:        "production-namespace-restrictions",
		Namespace:   "production",
		Description: "Stricter security requirements for production workloads",
		Enabled:     true,
		Priority:    110,
		Action:      PolicyActionDeny,
		Labels: map[string]string{
			"env": "production",
		},
		Rules: []Rule{
			{
				Name:        "prod-registry-restriction",
				Description: "Production must use verified registries only",
				Type:        RuleTypeImageRegistry,
				Config: map[string]interface{}{
					"allowed_registries": []interface{}{
						"gcr.io/company-prod",
						"company.azurecr.io",
					},
					"require_signed_images": true,
				},
			},
			{
				Name:        "prod-resource-limits",
				Description: "Production workloads must have conservative resource limits",
				Type:        RuleTypeResourceLimit,
				Config: map[string]interface{}{
					"require_limits":             true,
					"max_cpu_millicores":         float64(8000),                    // 8 cores max in prod
					"max_memory_bytes":           float64(16 * 1024 * 1024 * 1024), // 16GB max in prod
					"max_limit_to_request_ratio": float64(2.0),                     // Tighter ratio for prod
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// DevelopmentNamespacePolicy allows more flexibility for dev environments
func DevelopmentNamespacePolicy() *Policy {
	return &Policy{
		Name:        "development-namespace-permissions",
		Namespace:   "development",
		Description: "More permissive policy for development workloads",
		Enabled:     true,
		Priority:    50,
		Action:      PolicyActionAudit, // Audit only, don't block
		Labels: map[string]string{
			"env": "development",
		},
		Rules: []Rule{
			{
				Name:        "dev-registry-audit",
				Description: "Audit registry usage in development",
				Type:        RuleTypeImageRegistry,
				Config: map[string]interface{}{
					"allowed_registries": []interface{}{
						"*", // Allow all registries
					},
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// RestrictHostPathVolumesPolicy denies hostPath volume mounts
func RestrictHostPathVolumesPolicy() *Policy {
	return &Policy{
		Name:        "restrict-hostpath-volumes",
		Namespace:   "*",
		Description: "Denies hostPath volume mounts for security",
		Enabled:     true,
		Priority:    95,
		Action:      PolicyActionDeny,
		Rules: []Rule{
			{
				Name:        "no-hostpath",
				Description: "Containers must not mount hostPath volumes",
				Type:        RuleTypeVolumeType,
				Config: map[string]interface{}{
					"denied_types": []interface{}{
						"hostPath",
					},
					"allowed_types": []interface{}{
						"emptyDir",
						"configMap",
						"secret",
						"persistentVolume",
					},
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// RestrictEgressPolicy restricts network egress destinations
func RestrictEgressPolicy() *Policy {
	return &Policy{
		Name:        "restrict-egress-destinations",
		Namespace:   "*",
		Description: "Restricts network egress to known safe destinations",
		Enabled:     false, // Disabled by default, can be enabled per namespace
		Priority:    85,
		Action:      PolicyActionDeny,
		Rules: []Rule{
			{
				Name:        "egress-allowlist",
				Description: "Containers can only egress to allowed destinations",
				Type:        RuleTypeEgressDestination,
				Config: map[string]interface{}{
					"allowed_destinations": []interface{}{
						"*.example.com",
						"api.company.com",
						"10.0.0.0/8",     // Private network
						"172.16.0.0/12",  // Private network
						"192.168.0.0/16", // Private network
					},
					"denied_destinations": []interface{}{
						"*.onion", // Tor sites
						"*.suspicious.com",
					},
					"allow_private_ips": true,
				},
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// RequireNodeAffinityPolicy requires specific node placement
func RequireNodeAffinityPolicy(requiredLabels map[string]string) *Policy {
	config := make(map[string]interface{})
	requiredLabelsInterface := make(map[string]interface{})
	for k, v := range requiredLabels {
		requiredLabelsInterface[k] = v
	}
	config["required_labels"] = requiredLabelsInterface

	return &Policy{
		Name:        "require-node-affinity",
		Namespace:   "*",
		Description: "Requires specific node labels for placement",
		Enabled:     false, // Disabled by default
		Priority:    70,
		Action:      PolicyActionDeny,
		Rules: []Rule{
			{
				Name:        "node-label-requirement",
				Description: "Workloads must specify required node selectors",
				Type:        RuleTypeNodeSelector,
				Config:      config,
			},
		},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
}

// AllowDefaultNamespacePolicy allows workloads in the default namespace
// This provides a simple allow policy for testing and development
func AllowDefaultNamespacePolicy() *Policy {
	return &Policy{
		Name:        "allow-default-namespace",
		Namespace:   "default",
		Description: "Allows workloads in the default namespace (for testing)",
		Enabled:     true,
		Priority:    10, // Low priority, easily overridden by deny policies
		Action:      PolicyActionAllow,
		Rules:       []Rule{}, // No specific rules, just allow all in namespace
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
}
