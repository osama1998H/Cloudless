package policy

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// Engine is the OPA-style policy engine implementation
type Engine struct {
	config    EngineConfig
	logger    *zap.Logger
	policies  map[string]*Policy // policyName -> Policy
	evaluator *Evaluator
	mu        sync.RWMutex

	// Cache for evaluation results
	cache     map[string]*cachedResult
	cacheMu   sync.RWMutex
}

type cachedResult struct {
	result    *EvaluationResult
	expiresAt time.Time
}

// NewEngine creates a new policy engine
func NewEngine(config EngineConfig, logger *zap.Logger) *Engine {
	return &Engine{
		config:    config,
		logger:    logger,
		policies:  make(map[string]*Policy),
		evaluator: NewEvaluator(logger),
		cache:     make(map[string]*cachedResult),
	}
}

// Evaluate checks if a workload spec complies with all policies
func (e *Engine) Evaluate(ctx PolicyContext, spec WorkloadSpec) (*EvaluationResult, error) {
	startTime := time.Now()

	// Check cache if enabled
	if e.config.CacheEnabled {
		if cached := e.getCachedResult(spec); cached != nil {
			e.logger.Debug("Policy evaluation cache hit",
				zap.String("workload", spec.Name),
				zap.String("namespace", spec.Namespace),
			)
			return cached, nil
		}
	}

	// Create evaluation timeout context
	evalCtx, cancel := context.WithTimeout(context.Background(), e.config.MaxEvaluationTime)
	defer cancel()

	result := &EvaluationResult{
		Allowed:         true, // Start optimistic
		MatchedPolicies: []string{},
		Violations:      []Violation{},
		AuditEntries:    []AuditEntry{},
	}

	// Get sorted policies (by priority)
	policies := e.getSortedPolicies()
	result.PolicyCount = len(policies)

	// Evaluate each policy
	for _, policy := range policies {
		// Check if context deadline exceeded
		select {
		case <-evalCtx.Done():
			return nil, fmt.Errorf("policy evaluation timeout exceeded")
		default:
		}

		if !policy.Enabled {
			continue
		}

		// Check if policy applies to this workload
		if !e.policyApplies(ctx, policy, spec) {
			continue
		}

		// Evaluate policy rules
		policyResult := e.evaluatePolicy(policy, spec)

		if policyResult.Matched {
			result.MatchedPolicies = append(result.MatchedPolicies, policy.Name)

			switch policy.Action {
			case PolicyActionDeny:
				// Deny action - mark as not allowed and record violations
				result.Allowed = false
				result.Violations = append(result.Violations, policyResult.Violations...)

			case PolicyActionAllow:
				// Allow action - explicitly allowed (overrides default deny)
				// But don't override explicit denies from other policies
				if result.Allowed {
					result.Reason = fmt.Sprintf("Allowed by policy: %s", policy.Name)
				}

			case PolicyActionAudit:
				// Audit action - log but don't block
				result.AuditEntries = append(result.AuditEntries, AuditEntry{
					PolicyName: policy.Name,
					RuleName:   "audit",
					Message:    policyResult.Message,
					Timestamp:  time.Now(),
					Action:     "audit",
				})
			}
		}
	}

	// Apply default action if no policies matched
	if len(result.MatchedPolicies) == 0 {
		if e.config.DefaultAction == PolicyActionDeny {
			result.Allowed = false
			result.Reason = "No matching allow policy (default deny)"
		} else {
			result.Allowed = true
			result.Reason = "No matching deny policy (default allow)"
		}
	}

	// Set final reason if denied
	if !result.Allowed && result.Reason == "" {
		result.Reason = fmt.Sprintf("%d policy violation(s)", len(result.Violations))
	}

	result.EvaluationTime = time.Since(startTime)

	// Cache result if enabled
	if e.config.CacheEnabled {
		e.cacheResult(spec, result)
	}

	// Log audit entries
	if e.config.EnableAuditLogging && len(result.AuditEntries) > 0 {
		for _, entry := range result.AuditEntries {
			e.logger.Info("Policy audit",
				zap.String("policy", entry.PolicyName),
				zap.String("workload", spec.Name),
				zap.String("namespace", spec.Namespace),
				zap.String("message", entry.Message),
			)
		}
	}

	return result, nil
}

// AddPolicy adds or updates a policy
func (e *Engine) AddPolicy(policy *Policy) error {
	if policy.Name == "" {
		return fmt.Errorf("policy name is required")
	}

	e.mu.Lock()
	defer e.mu.Unlock()

	now := time.Now()
	if policy.CreatedAt.IsZero() {
		policy.CreatedAt = now
	}
	policy.UpdatedAt = now

	e.policies[policy.Name] = policy

	// Clear cache when policies change
	e.clearCache()

	e.logger.Info("Policy added",
		zap.String("name", policy.Name),
		zap.String("action", string(policy.Action)),
		zap.Int("rules", len(policy.Rules)),
		zap.Int("priority", policy.Priority),
	)

	return nil
}

// RemovePolicy removes a policy by name
func (e *Engine) RemovePolicy(name string) error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if _, exists := e.policies[name]; !exists {
		return fmt.Errorf("policy not found: %s", name)
	}

	delete(e.policies, name)

	// Clear cache when policies change
	e.clearCache()

	e.logger.Info("Policy removed", zap.String("name", name))

	return nil
}

// ListPolicies returns all active policies
func (e *Engine) ListPolicies() []*Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()

	policies := make([]*Policy, 0, len(e.policies))
	for _, policy := range e.policies {
		if policy.DeletedAt == nil {
			policies = append(policies, policy)
		}
	}

	return policies
}

// GetPolicy retrieves a policy by name
func (e *Engine) GetPolicy(name string) (*Policy, error) {
	e.mu.RLock()
	defer e.mu.RUnlock()

	policy, exists := e.policies[name]
	if !exists {
		return nil, fmt.Errorf("policy not found: %s", name)
	}

	return policy, nil
}

// policyApplies checks if a policy applies to the given workload
func (e *Engine) policyApplies(ctx PolicyContext, policy *Policy, spec WorkloadSpec) bool {
	// Check namespace match
	if policy.Namespace != "" && policy.Namespace != spec.Namespace && policy.Namespace != "*" {
		return false
	}

	// Check label selectors
	for labelKey, labelValue := range policy.Labels {
		if specValue, exists := spec.Labels[labelKey]; !exists || specValue != labelValue {
			return false
		}
	}

	return true
}

// evaluatePolicy evaluates a single policy against a workload spec
func (e *Engine) evaluatePolicy(policy *Policy, spec WorkloadSpec) *policyEvalResult {
	result := &policyEvalResult{
		Matched:    false,
		Violations: []Violation{},
	}

	// Allow policies with no rules automatically match (they apply to the namespace/labels)
	if len(policy.Rules) == 0 && policy.Action == PolicyActionAllow {
		result.Matched = true
		result.Message = fmt.Sprintf("Allow policy %s matched (no specific rules)", policy.Name)
		return result
	}

	// Evaluate each rule
	for _, rule := range policy.Rules {
		// Check if rule conditions are met
		if !e.evaluateConditions(rule.Conditions, spec) {
			continue
		}

		// Evaluate rule based on type
		ruleViolations := e.evaluator.EvaluateRule(rule, spec)

		if len(ruleViolations) > 0 {
			result.Matched = true
			result.Violations = append(result.Violations, ruleViolations...)
		}
	}

	if result.Matched {
		result.Message = fmt.Sprintf("Policy %s matched with %d violation(s)", policy.Name, len(result.Violations))
	}

	return result
}

// evaluateConditions checks if all conditions are met
func (e *Engine) evaluateConditions(conditions []Condition, spec WorkloadSpec) bool {
	for _, condition := range conditions {
		if !e.evaluateCondition(condition, spec) {
			return false
		}
	}
	return true
}

// evaluateCondition evaluates a single condition
func (e *Engine) evaluateCondition(condition Condition, spec WorkloadSpec) bool {
	// Get field value from spec
	fieldValue := e.getFieldValue(condition.Field, spec)

	switch condition.Operator {
	case OperatorEqual:
		return fieldValue == condition.Value

	case OperatorNotEqual:
		return fieldValue != condition.Value

	case OperatorIn:
		if values, ok := condition.Value.([]interface{}); ok {
			for _, v := range values {
				if fieldValue == v {
					return true
				}
			}
		}
		return false

	case OperatorNotIn:
		if values, ok := condition.Value.([]interface{}); ok {
			for _, v := range values {
				if fieldValue == v {
					return false
				}
			}
		}
		return true

	case OperatorExists:
		return fieldValue != nil

	case OperatorDoesNotExist:
		return fieldValue == nil

	default:
		e.logger.Warn("Unknown condition operator", zap.String("operator", string(condition.Operator)))
		return false
	}
}

// getFieldValue extracts a field value from a workload spec
func (e *Engine) getFieldValue(field string, spec WorkloadSpec) interface{} {
	switch field {
	case "namespace":
		return spec.Namespace
	case "name":
		return spec.Name
	case "image":
		return spec.Image
	case "privileged":
		return spec.Privileged
	case "hostNetwork":
		return spec.HostNetwork
	default:
		// Check labels
		if len(field) > 7 && field[:7] == "labels." {
			labelKey := field[7:]
			return spec.Labels[labelKey]
		}
		return nil
	}
}

// getSortedPolicies returns policies sorted by priority (descending)
func (e *Engine) getSortedPolicies() []*Policy {
	e.mu.RLock()
	defer e.mu.RUnlock()

	policies := make([]*Policy, 0, len(e.policies))
	for _, policy := range e.policies {
		if policy.DeletedAt == nil {
			policies = append(policies, policy)
		}
	}

	// Sort by priority (higher first)
	for i := 0; i < len(policies)-1; i++ {
		for j := i + 1; j < len(policies); j++ {
			if policies[i].Priority < policies[j].Priority {
				policies[i], policies[j] = policies[j], policies[i]
			}
		}
	}

	return policies
}

// getCachedResult retrieves a cached evaluation result
func (e *Engine) getCachedResult(spec WorkloadSpec) *EvaluationResult {
	e.cacheMu.RLock()
	defer e.cacheMu.RUnlock()

	cacheKey := e.getCacheKey(spec)
	if cached, exists := e.cache[cacheKey]; exists {
		if time.Now().Before(cached.expiresAt) {
			return cached.result
		}
		// Expired, will be cleaned up later
	}

	return nil
}

// cacheResult stores an evaluation result in cache
func (e *Engine) cacheResult(spec WorkloadSpec, result *EvaluationResult) {
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()

	cacheKey := e.getCacheKey(spec)
	e.cache[cacheKey] = &cachedResult{
		result:    result,
		expiresAt: time.Now().Add(e.config.CacheTTL),
	}
}

// getCacheKey generates a cache key for a workload spec
func (e *Engine) getCacheKey(spec WorkloadSpec) string {
	return fmt.Sprintf("%s/%s/%s", spec.Namespace, spec.Name, spec.Image)
}

// clearCache clears all cached results
func (e *Engine) clearCache() {
	e.cacheMu.Lock()
	defer e.cacheMu.Unlock()

	e.cache = make(map[string]*cachedResult)
}

// policyEvalResult is an internal result structure
type policyEvalResult struct {
	Matched    bool
	Violations []Violation
	Message    string
}
