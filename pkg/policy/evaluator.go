package policy

import (
	"fmt"
	"regexp"
	"strings"

	"go.uber.org/zap"
)

// Evaluator evaluates policy rules against workload specs
type Evaluator struct {
	logger *zap.Logger
}

// NewEvaluator creates a new policy evaluator
func NewEvaluator(logger *zap.Logger) *Evaluator {
	return &Evaluator{
		logger: logger,
	}
}

// EvaluateRule evaluates a single rule and returns violations
func (e *Evaluator) EvaluateRule(rule Rule, spec WorkloadSpec) []Violation {
	switch rule.Type {
	case RuleTypeImageRegistry:
		return e.evaluateImageRegistry(rule, spec)
	case RuleTypeCapabilities:
		return e.evaluateCapabilities(rule, spec)
	case RuleTypeEgressDestination:
		return e.evaluateEgressDestination(rule, spec)
	case RuleTypeResourceLimit:
		return e.evaluateResourceLimit(rule, spec)
	case RuleTypeNodeSelector:
		return e.evaluateNodeSelector(rule, spec)
	case RuleTypePrivilegedMode:
		return e.evaluatePrivilegedMode(rule, spec)
	case RuleTypeHostNetwork:
		return e.evaluateHostNetwork(rule, spec)
	case RuleTypeVolumeType:
		return e.evaluateVolumeType(rule, spec)
	case RuleTypeSecurityContext:
		return e.evaluateSecurityContext(rule, spec)
	case RuleTypeSeccompProfile:
		return e.evaluateSeccompProfile(rule, spec)
	case RuleTypeAppArmorProfile:
		return e.evaluateAppArmorProfile(rule, spec)
	case RuleTypeSELinuxOptions:
		return e.evaluateSELinuxOptions(rule, spec)
	case RuleTypeRuntimeClass:
		return e.evaluateRuntimeClass(rule, spec)
	case RuleTypeRunAsNonRoot:
		return e.evaluateRunAsNonRoot(rule, spec)
	case RuleTypeReadOnlyRootFS:
		return e.evaluateReadOnlyRootFS(rule, spec)
	default:
		e.logger.Warn("Unknown rule type", zap.String("type", string(rule.Type)))
		return nil
	}
}

// evaluateImageRegistry checks image registry restrictions
func (e *Evaluator) evaluateImageRegistry(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	// Parse rule config
	allowedRegistries, _ := rule.Config["allowed_registries"].([]interface{})
	deniedRegistries, _ := rule.Config["denied_registries"].([]interface{})

	// Extract registry from image
	registry := e.extractRegistry(spec.Image)

	// Check denied registries first
	for _, deniedInterface := range deniedRegistries {
		if denied, ok := deniedInterface.(string); ok {
			if e.matchesPattern(registry, denied) {
				violations = append(violations, Violation{
					RuleName: rule.Name,
					Message:  fmt.Sprintf("Image registry '%s' is denied by policy", registry),
					Severity: "High",
					Field:    "image",
					Value:    spec.Image,
				})
				return violations
			}
		}
	}

	// Check allowed registries
	if len(allowedRegistries) > 0 {
		allowed := false
		for _, allowedInterface := range allowedRegistries {
			if allowedReg, ok := allowedInterface.(string); ok {
				if e.matchesPattern(registry, allowedReg) {
					allowed = true
					break
				}
			}
		}

		if !allowed {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  fmt.Sprintf("Image registry '%s' is not in allowed list", registry),
				Severity: "High",
				Field:    "image",
				Value:    spec.Image,
			})
		}
	}

	return violations
}

// evaluateCapabilities checks container capabilities restrictions
func (e *Evaluator) evaluateCapabilities(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	allowedCaps, _ := rule.Config["allowed_capabilities"].([]interface{})
	deniedCaps, _ := rule.Config["denied_capabilities"].([]interface{})

	// Check each capability in spec
	for _, cap := range spec.Capabilities {
		// Check denied capabilities
		for _, deniedInterface := range deniedCaps {
			if denied, ok := deniedInterface.(string); ok {
				if strings.EqualFold(cap, denied) {
					violations = append(violations, Violation{
						RuleName: rule.Name,
						Message:  fmt.Sprintf("Capability '%s' is denied by policy", cap),
						Severity: "Critical",
						Field:    "capabilities",
						Value:    cap,
					})
				}
			}
		}

		// Check allowed capabilities if specified
		if len(allowedCaps) > 0 {
			allowed := false
			for _, allowedInterface := range allowedCaps {
				if allowedCap, ok := allowedInterface.(string); ok {
					if strings.EqualFold(cap, allowedCap) {
						allowed = true
						break
					}
				}
			}

			if !allowed {
				violations = append(violations, Violation{
					RuleName: rule.Name,
					Message:  fmt.Sprintf("Capability '%s' is not in allowed list", cap),
					Severity: "High",
					Field:    "capabilities",
					Value:    cap,
				})
			}
		}
	}

	return violations
}

// evaluateEgressDestination checks egress destination restrictions
func (e *Evaluator) evaluateEgressDestination(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	allowedDests, _ := rule.Config["allowed_destinations"].([]interface{})
	deniedDests, _ := rule.Config["denied_destinations"].([]interface{})

	// Check each egress domain
	for _, domain := range spec.EgressDomains {
		// Check denied destinations
		for _, deniedInterface := range deniedDests {
			if denied, ok := deniedInterface.(string); ok {
				if e.matchesPattern(domain, denied) {
					violations = append(violations, Violation{
						RuleName: rule.Name,
						Message:  fmt.Sprintf("Egress to '%s' is denied by policy", domain),
						Severity: "High",
						Field:    "egress_domains",
						Value:    domain,
					})
				}
			}
		}

		// Check allowed destinations if specified
		if len(allowedDests) > 0 {
			allowed := false
			for _, allowedInterface := range allowedDests {
				if allowedDest, ok := allowedInterface.(string); ok {
					if e.matchesPattern(domain, allowedDest) {
						allowed = true
						break
					}
				}
			}

			if !allowed {
				violations = append(violations, Violation{
					RuleName: rule.Name,
					Message:  fmt.Sprintf("Egress to '%s' is not in allowed list", domain),
					Severity: "Medium",
					Field:    "egress_domains",
					Value:    domain,
				})
			}
		}
	}

	return violations
}

// evaluateResourceLimit checks resource limit restrictions
func (e *Evaluator) evaluateResourceLimit(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	maxCPU, _ := rule.Config["max_cpu_millicores"].(float64)
	maxMemory, _ := rule.Config["max_memory_bytes"].(float64)
	requireLimits, _ := rule.Config["require_limits"].(bool)

	// Check CPU request
	if maxCPU > 0 && float64(spec.CPURequest) > maxCPU {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  fmt.Sprintf("CPU request %dm exceeds maximum %dm", spec.CPURequest, int64(maxCPU)),
			Severity: "Medium",
			Field:    "resources.cpu_request",
			Value:    fmt.Sprintf("%d", spec.CPURequest),
		})
	}

	// Check memory request
	if maxMemory > 0 && float64(spec.MemoryRequest) > maxMemory {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  fmt.Sprintf("Memory request %d bytes exceeds maximum %d bytes", spec.MemoryRequest, int64(maxMemory)),
			Severity: "Medium",
			Field:    "resources.memory_request",
			Value:    fmt.Sprintf("%d", spec.MemoryRequest),
		})
	}

	// Check if limits are required
	if requireLimits {
		if spec.CPULimit == 0 {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  "CPU limit is required by policy",
				Severity: "Medium",
				Field:    "resources.cpu_limit",
				Value:    "0",
			})
		}
		if spec.MemoryLimit == 0 {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  "Memory limit is required by policy",
				Severity: "Medium",
				Field:    "resources.memory_limit",
				Value:    "0",
			})
		}
	}

	return violations
}

// evaluateNodeSelector checks node selector restrictions
func (e *Evaluator) evaluateNodeSelector(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	requiredLabels, _ := rule.Config["required_labels"].(map[string]interface{})
	deniedLabels, _ := rule.Config["denied_labels"].(map[string]interface{})

	// Check required labels
	for key, valueInterface := range requiredLabels {
		requiredValue, _ := valueInterface.(string)
		if specValue, exists := spec.NodeSelector[key]; !exists {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  fmt.Sprintf("Required node selector label '%s' is missing", key),
				Severity: "Medium",
				Field:    "node_selector",
				Value:    "",
			})
		} else if requiredValue != "" && specValue != requiredValue {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  fmt.Sprintf("Node selector label '%s' has value '%s', expected '%s'", key, specValue, requiredValue),
				Severity: "Medium",
				Field:    "node_selector",
				Value:    specValue,
			})
		}
	}

	// Check denied labels
	for key, valueInterface := range deniedLabels {
		deniedValue, _ := valueInterface.(string)
		if specValue, exists := spec.NodeSelector[key]; exists {
			if deniedValue == "" || specValue == deniedValue {
				violations = append(violations, Violation{
					RuleName: rule.Name,
					Message:  fmt.Sprintf("Node selector label '%s=%s' is denied by policy", key, specValue),
					Severity: "High",
					Field:    "node_selector",
					Value:    specValue,
				})
			}
		}
	}

	return violations
}

// evaluatePrivilegedMode checks privileged mode restrictions
func (e *Evaluator) evaluatePrivilegedMode(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	allowPrivileged, _ := rule.Config["allow_privileged"].(bool)

	if spec.Privileged && !allowPrivileged {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Privileged mode is not allowed by policy",
			Severity: "Critical",
			Field:    "privileged",
			Value:    "true",
		})
	}

	return violations
}

// evaluateHostNetwork checks host network restrictions
func (e *Evaluator) evaluateHostNetwork(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	allowHostNetwork, _ := rule.Config["allow_host_network"].(bool)
	allowHostPID, _ := rule.Config["allow_host_pid"].(bool)
	allowHostIPC, _ := rule.Config["allow_host_ipc"].(bool)

	if spec.HostNetwork && !allowHostNetwork {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Host network access is not allowed by policy",
			Severity: "Critical",
			Field:    "host_network",
			Value:    "true",
		})
	}

	if spec.HostPID && !allowHostPID {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Host PID namespace access is not allowed by policy",
			Severity: "Critical",
			Field:    "host_pid",
			Value:    "true",
		})
	}

	if spec.HostIPC && !allowHostIPC {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Host IPC namespace access is not allowed by policy",
			Severity: "Critical",
			Field:    "host_ipc",
			Value:    "true",
		})
	}

	return violations
}

// evaluateVolumeType checks volume type restrictions
func (e *Evaluator) evaluateVolumeType(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	allowedTypes, _ := rule.Config["allowed_types"].([]interface{})
	deniedTypes, _ := rule.Config["denied_types"].([]interface{})

	for _, volume := range spec.Volumes {
		// Check denied types
		for _, deniedInterface := range deniedTypes {
			if denied, ok := deniedInterface.(string); ok {
				if volume.Type == denied {
					violations = append(violations, Violation{
						RuleName: rule.Name,
						Message:  fmt.Sprintf("Volume type '%s' is denied by policy", volume.Type),
						Severity: "High",
						Field:    "volumes",
						Value:    volume.Type,
					})
				}
			}
		}

		// Check allowed types if specified
		if len(allowedTypes) > 0 {
			allowed := false
			for _, allowedInterface := range allowedTypes {
				if allowedType, ok := allowedInterface.(string); ok {
					if volume.Type == allowedType {
						allowed = true
						break
					}
				}
			}

			if !allowed {
				violations = append(violations, Violation{
					RuleName: rule.Name,
					Message:  fmt.Sprintf("Volume type '%s' is not in allowed list", volume.Type),
					Severity: "Medium",
					Field:    "volumes",
					Value:    volume.Type,
				})
			}
		}
	}

	return violations
}

// extractRegistry extracts the registry from an image reference
func (e *Evaluator) extractRegistry(image string) string {
	// Handle images like:
	// - docker.io/library/nginx:latest
	// - gcr.io/project/image:tag
	// - nginx:latest (defaults to docker.io)
	// - nginx (defaults to docker.io with latest tag)

	parts := strings.SplitN(image, "/", 2)
	if len(parts) == 1 {
		// No registry specified, default to docker.io
		return "docker.io"
	}

	// Check if first part looks like a registry (contains . or :)
	if strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":") {
		return parts[0]
	}

	// Otherwise it's a docker.io image like "library/nginx"
	return "docker.io"
}

// matchesPattern checks if a value matches a pattern (supports * wildcard)
func (e *Evaluator) matchesPattern(value, pattern string) bool {
	// Convert glob pattern to regex
	// * matches any characters
	// ? matches single character

	// Lowercase both value and pattern for case-insensitive matching (per DNS spec)
	value = strings.ToLower(value)
	pattern = strings.ToLower(pattern)

	regexPattern := "^" + regexp.QuoteMeta(pattern) + "$"
	regexPattern = strings.ReplaceAll(regexPattern, "\\*", ".*")
	regexPattern = strings.ReplaceAll(regexPattern, "\\?", ".")

	matched, err := regexp.MatchString(regexPattern, value)
	if err != nil {
		e.logger.Warn("Invalid pattern", zap.String("pattern", pattern), zap.Error(err))
		return false
	}

	return matched
}

// ============================================================================
// Security Context Evaluation Functions (CLD-REQ-062)
// ============================================================================

// evaluateSecurityContext checks comprehensive security context requirements
func (e *Evaluator) evaluateSecurityContext(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	requireSecurityContext, _ := rule.Config["require_security_context"].(bool)
	if requireSecurityContext && spec.SecurityContext == nil {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Security context is required by policy but not specified",
			Severity: "High",
			Field:    "security_context",
			Value:    "nil",
		})
		return violations
	}

	if spec.SecurityContext == nil {
		return violations
	}

	// Check if privileged is forbidden
	forbidPrivileged, _ := rule.Config["forbid_privileged"].(bool)
	if forbidPrivileged && spec.SecurityContext.Privileged {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Privileged containers are forbidden by security policy",
			Severity: "Critical",
			Field:    "security_context.privileged",
			Value:    "true",
		})
	}

	// Check RunAsNonRoot requirement
	requireNonRoot, _ := rule.Config["require_non_root"].(bool)
	if requireNonRoot {
		if spec.SecurityContext.RunAsNonRoot == nil || !*spec.SecurityContext.RunAsNonRoot {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  "RunAsNonRoot must be set to true",
				Severity: "High",
				Field:    "security_context.run_as_non_root",
				Value:    "false or unset",
			})
		}
	}

	// Check ReadOnlyRootFilesystem requirement
	requireReadOnlyRoot, _ := rule.Config["require_read_only_root"].(bool)
	if requireReadOnlyRoot && !spec.SecurityContext.ReadOnlyRootFilesystem {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Read-only root filesystem is required by policy",
			Severity: "Medium",
			Field:    "security_context.read_only_root_filesystem",
			Value:    "false",
		})
	}

	return violations
}

// evaluateSeccompProfile checks seccomp profile requirements
func (e *Evaluator) evaluateSeccompProfile(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	requireSeccomp, _ := rule.Config["require_seccomp"].(bool)
	allowedTypes, _ := rule.Config["allowed_types"].([]interface{})
	forbidUnconfined, _ := rule.Config["forbid_unconfined"].(bool)

	if spec.SecurityContext == nil || spec.SecurityContext.Linux == nil || spec.SecurityContext.Linux.SeccompProfile == nil {
		if requireSeccomp {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  "Seccomp profile is required by policy but not specified",
				Severity: "High",
				Field:    "security_context.linux.seccomp_profile",
				Value:    "nil",
			})
		}
		return violations
	}

	profile := spec.SecurityContext.Linux.SeccompProfile

	// Check if unconfined is forbidden
	if forbidUnconfined && profile.Type == "Unconfined" {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Unconfined seccomp profile is not allowed by policy",
			Severity: "Critical",
			Field:    "security_context.linux.seccomp_profile.type",
			Value:    "Unconfined",
		})
	}

	// Check allowed types
	if len(allowedTypes) > 0 {
		allowed := false
		for _, typeInterface := range allowedTypes {
			if allowedType, ok := typeInterface.(string); ok {
				if string(profile.Type) == allowedType {
					allowed = true
					break
				}
			}
		}

		if !allowed {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  fmt.Sprintf("Seccomp profile type '%s' is not in allowed list", profile.Type),
				Severity: "High",
				Field:    "security_context.linux.seccomp_profile.type",
				Value:    string(profile.Type),
			})
		}
	}

	return violations
}

// evaluateAppArmorProfile checks AppArmor profile requirements
func (e *Evaluator) evaluateAppArmorProfile(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	requireAppArmor, _ := rule.Config["require_apparmor"].(bool)
	allowedTypes, _ := rule.Config["allowed_types"].([]interface{})
	forbidUnconfined, _ := rule.Config["forbid_unconfined"].(bool)

	if spec.SecurityContext == nil || spec.SecurityContext.Linux == nil || spec.SecurityContext.Linux.AppArmorProfile == nil {
		if requireAppArmor {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  "AppArmor profile is required by policy but not specified",
				Severity: "High",
				Field:    "security_context.linux.apparmor_profile",
				Value:    "nil",
			})
		}
		return violations
	}

	profile := spec.SecurityContext.Linux.AppArmorProfile

	// Check if unconfined is forbidden
	if forbidUnconfined && profile.Type == "Unconfined" {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Unconfined AppArmor profile is not allowed by policy",
			Severity: "Critical",
			Field:    "security_context.linux.apparmor_profile.type",
			Value:    "Unconfined",
		})
	}

	// Check allowed types
	if len(allowedTypes) > 0 {
		allowed := false
		for _, typeInterface := range allowedTypes {
			if allowedType, ok := typeInterface.(string); ok {
				if string(profile.Type) == allowedType {
					allowed = true
					break
				}
			}
		}

		if !allowed {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  fmt.Sprintf("AppArmor profile type '%s' is not in allowed list", profile.Type),
				Severity: "High",
				Field:    "security_context.linux.apparmor_profile.type",
				Value:    string(profile.Type),
			})
		}
	}

	return violations
}

// evaluateSELinuxOptions checks SELinux context requirements
func (e *Evaluator) evaluateSELinuxOptions(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	requireSELinux, _ := rule.Config["require_selinux"].(bool)
	requiredType, _ := rule.Config["required_type"].(string)
	requiredLevel, _ := rule.Config["required_level"].(string)
	allowedTypes, _ := rule.Config["allowed_types"].([]interface{})

	if spec.SecurityContext == nil || spec.SecurityContext.Linux == nil || spec.SecurityContext.Linux.SELinuxOptions == nil {
		if requireSELinux {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  "SELinux options are required by policy but not specified",
				Severity: "Medium",
				Field:    "security_context.linux.selinux_options",
				Value:    "nil",
			})
		}
		return violations
	}

	options := spec.SecurityContext.Linux.SELinuxOptions

	// Check allowed types
	if len(allowedTypes) > 0 {
		allowed := false
		for _, typeInterface := range allowedTypes {
			if allowedType, ok := typeInterface.(string); ok {
				if options.Type == allowedType {
					allowed = true
					break
				}
			}
		}

		if !allowed {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  fmt.Sprintf("SELinux type '%s' is not in allowed list", options.Type),
				Severity: "High",
				Field:    "security_context.linux.selinux_options.type",
				Value:    options.Type,
			})
		}
	}

	// Check required type
	if requiredType != "" && options.Type != requiredType {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  fmt.Sprintf("SELinux type must be '%s', got '%s'", requiredType, options.Type),
			Severity: "High",
			Field:    "security_context.linux.selinux_options.type",
			Value:    options.Type,
		})
	}

	// Check required level
	if requiredLevel != "" && options.Level != requiredLevel {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  fmt.Sprintf("SELinux level must be '%s', got '%s'", requiredLevel, options.Level),
			Severity: "Medium",
			Field:    "security_context.linux.selinux_options.level",
			Value:    options.Level,
		})
	}

	return violations
}

// evaluateRuntimeClass checks runtime class restrictions
func (e *Evaluator) evaluateRuntimeClass(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	allowedClasses, _ := rule.Config["allowed_classes"].([]interface{})
	allowedRuntimeClasses, _ := rule.Config["allowed_runtime_classes"].([]interface{})
	forbiddenClasses, _ := rule.Config["forbidden_classes"].([]interface{})
	requireRuntimeClass, _ := rule.Config["require_runtime_class"].(bool)
	requireSandboxed, _ := rule.Config["require_sandboxed"].(bool)

	// Support both "allowed_classes" and "allowed_runtime_classes"
	if len(allowedRuntimeClasses) > 0 {
		allowedClasses = allowedRuntimeClasses
	}

	runtimeClass := ""
	if spec.SecurityContext != nil {
		runtimeClass = spec.SecurityContext.RuntimeClassName
	}

	// Check if runtime class is required
	if requireRuntimeClass && runtimeClass == "" {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Runtime class is required by policy but not specified",
			Severity: "Medium",
			Field:    "security_context.runtime_class_name",
			Value:    "empty",
		})
		return violations
	}

	// Check if sandboxed runtime is required
	sandboxedRuntimes := map[string]bool{
		"gvisor":      true,
		"kata":        true,
		"firecracker": true,
	}

	if requireSandboxed {
		if runtimeClass == "" || runtimeClass == "runc" || !sandboxedRuntimes[runtimeClass] {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  fmt.Sprintf("Sandboxed runtime required but got '%s'", runtimeClass),
				Severity: "High",
				Field:    "security_context.runtime_class_name",
				Value:    runtimeClass,
			})
		}
	}

	// Check forbidden classes
	for _, forbiddenInterface := range forbiddenClasses {
		if forbidden, ok := forbiddenInterface.(string); ok {
			if runtimeClass == forbidden {
				violations = append(violations, Violation{
					RuleName: rule.Name,
					Message:  fmt.Sprintf("Runtime class '%s' is forbidden by policy", runtimeClass),
					Severity: "High",
					Field:    "security_context.runtime_class_name",
					Value:    runtimeClass,
				})
			}
		}
	}

	// Check allowed classes
	if len(allowedClasses) > 0 && runtimeClass != "" {
		allowed := false
		for _, allowedInterface := range allowedClasses {
			if allowedClass, ok := allowedInterface.(string); ok {
				if runtimeClass == allowedClass {
					allowed = true
					break
				}
			}
		}

		if !allowed {
			violations = append(violations, Violation{
				RuleName: rule.Name,
				Message:  fmt.Sprintf("Runtime class '%s' is not in allowed list", runtimeClass),
				Severity: "High",
				Field:    "security_context.runtime_class_name",
				Value:    runtimeClass,
			})
		}
	}

	return violations
}

// evaluateRunAsNonRoot enforces non-root user requirement
func (e *Evaluator) evaluateRunAsNonRoot(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	enforceNonRoot, _ := rule.Config["enforce"].(bool)
	if !enforceNonRoot {
		return violations
	}

	if spec.SecurityContext == nil {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Security context is required to enforce RunAsNonRoot",
			Severity: "High",
			Field:    "security_context",
			Value:    "nil",
		})
		return violations
	}

	// Check if non-root is satisfied by either RunAsNonRoot field OR RunAsUser > 0
	hasRunAsNonRoot := spec.SecurityContext.RunAsNonRoot != nil && *spec.SecurityContext.RunAsNonRoot
	hasNonRootUser := spec.SecurityContext.RunAsUser != nil && *spec.SecurityContext.RunAsUser > 0
	hasRootUser := spec.SecurityContext.RunAsUser != nil && *spec.SecurityContext.RunAsUser == 0

	// If RunAsUser is explicitly set to root, that's a violation regardless of RunAsNonRoot
	if hasRootUser {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "RunAsUser cannot be 0 (root) when RunAsNonRoot is required",
			Severity: "Critical",
			Field:    "security_context.run_as_user",
			Value:    "0",
		})
		return violations
	}

	// If neither RunAsNonRoot nor a non-root RunAsUser is set, that's a violation
	if !hasRunAsNonRoot && !hasNonRootUser {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "RunAsNonRoot must be explicitly set to true",
			Severity: "High",
			Field:    "security_context.run_as_non_root",
			Value:    "false or unset",
		})
	}

	return violations
}

// evaluateReadOnlyRootFS enforces read-only root filesystem requirement
func (e *Evaluator) evaluateReadOnlyRootFS(rule Rule, spec WorkloadSpec) []Violation {
	var violations []Violation

	enforceReadOnly, _ := rule.Config["enforce"].(bool)
	if !enforceReadOnly {
		return violations
	}

	exceptions, _ := rule.Config["exceptions"].([]interface{})

	// Check if workload is in exceptions list
	for _, exceptionInterface := range exceptions {
		if exception, ok := exceptionInterface.(string); ok {
			if spec.Name == exception {
				// Workload is in exceptions list, skip enforcement
				return violations
			}
		}
	}

	if spec.SecurityContext == nil {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Security context is required to enforce ReadOnlyRootFilesystem",
			Severity: "Medium",
			Field:    "security_context",
			Value:    "nil",
		})
		return violations
	}

	if !spec.SecurityContext.ReadOnlyRootFilesystem {
		violations = append(violations, Violation{
			RuleName: rule.Name,
			Message:  "Read-only root filesystem is required by policy",
			Severity: "Medium",
			Field:    "security_context.read_only_root_filesystem",
			Value:    "false",
		})
	}

	return violations
}
