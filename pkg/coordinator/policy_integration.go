package coordinator

import (
	"context"
	"fmt"

	"github.com/osama1998H/Cloudless/pkg/api"
	"github.com/osama1998H/Cloudless/pkg/observability"
	"github.com/osama1998H/Cloudless/pkg/policy"
	"go.uber.org/zap"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// AdmitWorkload performs policy-based admission control for a workload
func (c *Coordinator) AdmitWorkload(ctx context.Context, workload *api.Workload) error {
	if c.policyEngine == nil {
		// Policy engine not initialized, allow workload (for backward compatibility)
		c.logger.Debug("Policy engine not initialized, skipping admission control")
		return nil
	}

	// Convert API workload to policy workload spec
	policySpec := c.convertToPolicyWorkloadSpec(workload)

	// Create policy context
	policyCtx := policy.PolicyContext{
		RequestID:   fmt.Sprintf("workload-%s", workload.Id),
		Namespace:   workload.Namespace,
		ClusterName: "cloudless",
	}

	// Evaluate policies
	result, err := c.policyEngine.Evaluate(policyCtx, policySpec)
	if err != nil {
		c.logger.Error("Policy evaluation failed",
			zap.String("workload", workload.Name),
			zap.String("namespace", workload.Namespace),
			zap.Error(err),
		)
		return status.Errorf(codes.Internal, "policy evaluation failed: %v", err)
	}

	// Log evaluation result
	c.logger.Info("Policy evaluation completed",
		zap.String("workload", workload.Name),
		zap.String("namespace", workload.Namespace),
		zap.Bool("allowed", result.Allowed),
		zap.Int("violations", len(result.Violations)),
		zap.Duration("evaluation_time", result.EvaluationTime),
	)

	// CLD-REQ-071: Record policy enforcement events
	if result.Allowed {
		c.logger.Debug("Policy enforcement: workload admitted",
			zap.String("workload", workload.Name),
			zap.Strings("matched_policies", result.MatchedPolicies),
		)
	} else {
		c.logger.Warn("Policy enforcement: workload denied",
			zap.String("workload", workload.Name),
			zap.String("reason", result.Reason),
			zap.Int("violations", len(result.Violations)),
		)

		// Record policy violation event
		if c.eventStream != nil {
			// Determine actor ID (user or system)
			actorID := "user" // Default, could be extracted from context

			// Get primary policy name if available
			policyName := "admission_control"
			if len(result.MatchedPolicies) > 0 {
				policyName = result.MatchedPolicies[0]
			}

			event := observability.NewPolicyViolationEvent(
				actorID,
				"workload",
				workload.Id,
				policyName,
				result.Reason,
			)
			c.eventStream.RecordEvent(ctx, event)
		}
	}

	// Deny if not allowed
	if !result.Allowed {
		c.logger.Warn("Workload denied by policy",
			zap.String("workload", workload.Name),
			zap.String("namespace", workload.Namespace),
			zap.String("reason", result.Reason),
			zap.Any("violations", result.Violations),
		)

		// Build detailed error message
		errMsg := fmt.Sprintf("Workload denied by policy: %s\n\nViolations:\n", result.Reason)
		for i, violation := range result.Violations {
			errMsg += fmt.Sprintf("%d. [%s] %s: %s\n",
				i+1, violation.Severity, violation.RuleName, violation.Message)
		}

		return status.Errorf(codes.PermissionDenied, "%s", errMsg)
	}

	return nil
}

// convertToPolicyWorkloadSpec converts API workload to policy workload spec
// Note: Simplified to map only basic fields due to API structure differences
func (c *Coordinator) convertToPolicyWorkloadSpec(workload *api.Workload) policy.WorkloadSpec {
	spec := workload.Spec
	if spec == nil {
		return policy.WorkloadSpec{
			Name:      workload.Name,
			Namespace: workload.Namespace,
		}
	}

	policySpec := policy.WorkloadSpec{
		Name:        workload.Name,
		Namespace:   workload.Namespace,
		Image:       spec.Image,
		Command:     spec.Command,
		Args:        spec.Args,
		Env:         spec.Env,
		Labels:      workload.Labels,
		Annotations: workload.Annotations,
	}

	// Security context fields would be mapped here if they existed in WorkloadSpec
	// For now, policy evaluation will use default/safe values

	// Extract resource requirements
	if spec.Resources != nil {
		if spec.Resources.Requests != nil {
			policySpec.CPURequest = spec.Resources.Requests.CpuMillicores
			policySpec.MemoryRequest = spec.Resources.Requests.MemoryBytes
		}
		if spec.Resources.Limits != nil {
			policySpec.CPULimit = spec.Resources.Limits.CpuMillicores
			policySpec.MemoryLimit = spec.Resources.Limits.MemoryBytes
		}
	}

	// Extract volumes (simplified - Volume API structure differs from policy spec)
	for _, vol := range spec.Volumes {
		policySpec.Volumes = append(policySpec.Volumes, policy.VolumeSpec{
			Name:      vol.Name,
			Type:      vol.Source, // Using source field as type indicator
			Source:    vol.Source,
			MountPath: vol.MountPath,
			ReadOnly:  vol.ReadOnly,
		})
	}

	// Extract ports
	for _, port := range spec.Ports {
		policySpec.Ports = append(policySpec.Ports, policy.PortSpec{
			Name:     port.Name,
			Port:     port.ContainerPort,
			Protocol: port.Protocol,
			HostPort: port.HostPort,
		})
	}

	// Extract placement
	if spec.Placement != nil {
		policySpec.NodeSelector = spec.Placement.NodeSelector
		if len(spec.Placement.Regions) > 0 {
			policySpec.Region = spec.Placement.Regions[0]
		}
		if len(spec.Placement.Zones) > 0 {
			policySpec.Zone = spec.Placement.Zones[0]
		}
	}

	return policySpec
}

// InitializePolicyEngine initializes the policy engine with default policies
func (c *Coordinator) InitializePolicyEngine() error {
	// Create policy engine config
	engineConfig := policy.DefaultEngineConfig()

	// Create policy engine
	c.policyEngine = policy.NewEngine(engineConfig, c.logger)

	// Load default policies
	defaultPolicies := policy.DefaultPolicies()
	for _, pol := range defaultPolicies {
		if err := c.policyEngine.AddPolicy(pol); err != nil {
			c.logger.Warn("Failed to add default policy",
				zap.String("policy", pol.Name),
				zap.Error(err),
			)
		} else {
			c.logger.Info("Loaded default policy",
				zap.String("policy", pol.Name),
				zap.String("action", string(pol.Action)),
				zap.Int("priority", pol.Priority),
			)
		}
	}

	c.logger.Info("Policy engine initialized",
		zap.Int("default_policies", len(defaultPolicies)),
	)

	return nil
}

// AddPolicy adds a custom policy to the engine
func (c *Coordinator) AddPolicy(pol *policy.Policy) error {
	if c.policyEngine == nil {
		return fmt.Errorf("policy engine not initialized")
	}
	return c.policyEngine.AddPolicy(pol)
}

// RemovePolicy removes a policy from the engine
func (c *Coordinator) RemovePolicy(name string) error {
	if c.policyEngine == nil {
		return fmt.Errorf("policy engine not initialized")
	}
	return c.policyEngine.RemovePolicy(name)
}

// ListPolicies returns all active policies
func (c *Coordinator) ListPolicies() []*policy.Policy {
	if c.policyEngine == nil {
		return []*policy.Policy{}
	}
	return c.policyEngine.ListPolicies()
}

// GetPolicy retrieves a policy by name
func (c *Coordinator) GetPolicy(name string) (*policy.Policy, error) {
	if c.policyEngine == nil {
		return nil, fmt.Errorf("policy engine not initialized")
	}
	return c.policyEngine.GetPolicy(name)
}
