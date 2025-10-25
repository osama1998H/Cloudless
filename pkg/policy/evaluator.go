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
