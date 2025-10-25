package policy

import (
	"time"
)

// PolicyEngine evaluates security policies for workload admission
type PolicyEngine interface {
	// Evaluate checks if a workload spec complies with policies
	Evaluate(ctx PolicyContext, spec WorkloadSpec) (*EvaluationResult, error)

	// AddPolicy adds or updates a policy
	AddPolicy(policy *Policy) error

	// RemovePolicy removes a policy by name
	RemovePolicy(name string) error

	// ListPolicies returns all active policies
	ListPolicies() []*Policy

	// GetPolicy retrieves a policy by name
	GetPolicy(name string) (*Policy, error)
}

// Policy represents a security policy
type Policy struct {
	// Metadata
	Name        string            `json:"name"`
	Namespace   string            `json:"namespace"`
	Description string            `json:"description"`
	Enabled     bool              `json:"enabled"`
	Priority    int               `json:"priority"` // Higher priority evaluated first
	Labels      map[string]string `json:"labels,omitempty"`

	// Policy rules
	Rules []Rule `json:"rules"`

	// Action to take when policy matches
	Action PolicyAction `json:"action"` // Allow, Deny, Audit

	// Timestamps
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// PolicyAction defines what happens when a policy matches
type PolicyAction string

const (
	// PolicyActionAllow explicitly allows the request
	PolicyActionAllow PolicyAction = "Allow"
	// PolicyActionDeny blocks the request
	PolicyActionDeny PolicyAction = "Deny"
	// PolicyActionAudit logs the request but allows it
	PolicyActionAudit PolicyAction = "Audit"
)

// Rule represents a single policy rule
type Rule struct {
	// Rule identification
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// Rule type determines which evaluator to use
	Type RuleType `json:"type"`

	// Rule configuration (type-specific)
	Config map[string]interface{} `json:"config"`

	// Conditions that must be met for rule to apply
	Conditions []Condition `json:"conditions,omitempty"`
}

// RuleType defines the category of rule
type RuleType string

const (
	// RuleTypeImageRegistry restricts allowed image registries
	RuleTypeImageRegistry RuleType = "ImageRegistry"

	// RuleTypeCapabilities restricts container capabilities
	RuleTypeCapabilities RuleType = "Capabilities"

	// RuleTypeEgressDestination restricts network egress
	RuleTypeEgressDestination RuleType = "EgressDestination"

	// RuleTypeResourceLimit enforces resource constraints
	RuleTypeResourceLimit RuleType = "ResourceLimit"

	// RuleTypeNodeSelector restricts node placement
	RuleTypeNodeSelector RuleType = "NodeSelector"

	// RuleTypePrivilegedMode restricts privileged containers
	RuleTypePrivilegedMode RuleType = "PrivilegedMode"

	// RuleTypeHostNetwork restricts host network access
	RuleTypeHostNetwork RuleType = "HostNetwork"

	// RuleTypeVolumeType restricts volume types
	RuleTypeVolumeType RuleType = "VolumeType"
)

// Condition represents a condition for rule application
type Condition struct {
	// Field to check (e.g., "namespace", "labels.env")
	Field string `json:"field"`

	// Operator for comparison
	Operator ConditionOperator `json:"operator"`

	// Value(s) to compare against
	Value interface{} `json:"value"`
}

// ConditionOperator defines how to compare values
type ConditionOperator string

const (
	OperatorEqual          ConditionOperator = "Equal"
	OperatorNotEqual       ConditionOperator = "NotEqual"
	OperatorIn             ConditionOperator = "In"
	OperatorNotIn          ConditionOperator = "NotIn"
	OperatorExists         ConditionOperator = "Exists"
	OperatorDoesNotExist   ConditionOperator = "DoesNotExist"
	OperatorMatches        ConditionOperator = "Matches"        // Regex match
	OperatorDoesNotMatch   ConditionOperator = "DoesNotMatch"   // Regex not match
)

// WorkloadSpec represents the workload specification to evaluate
type WorkloadSpec struct {
	Name        string
	Namespace   string
	Image       string
	Command     []string
	Args        []string
	Env         map[string]string
	Labels      map[string]string
	Annotations map[string]string

	// Security context
	Privileged      bool
	Capabilities    []string
	HostNetwork     bool
	HostPID         bool
	HostIPC         bool
	RunAsUser       *int64
	RunAsGroup      *int64
	FSGroup         *int64
	ReadOnlyRootFS  bool

	// Resources
	CPURequest    int64
	MemoryRequest int64
	CPULimit      int64
	MemoryLimit   int64

	// Volumes
	Volumes []VolumeSpec

	// Network
	Ports           []PortSpec
	EgressDomains   []string

	// Placement
	NodeSelector map[string]string
	Region       string
	Zone         string
}

// VolumeSpec represents a volume specification
type VolumeSpec struct {
	Name      string
	Type      string // "emptyDir", "hostPath", "configMap", "secret", "persistentVolume"
	Source    string
	MountPath string
	ReadOnly  bool
}

// PortSpec represents a port specification
type PortSpec struct {
	Name          string
	Port          int32
	Protocol      string // "TCP", "UDP"
	HostPort      int32
}

// PolicyContext provides context for policy evaluation
type PolicyContext struct {
	// Request metadata
	RequestID   string
	User        string
	Groups      []string
	ServiceAccount string

	// Cluster context
	Namespace   string
	ClusterName string

	// Additional context
	Timestamp time.Time
	Metadata  map[string]interface{}
}

// EvaluationResult contains the result of policy evaluation
type EvaluationResult struct {
	// Overall result
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`

	// Matched policies
	MatchedPolicies []string `json:"matched_policies,omitempty"`

	// Violations (for denied requests)
	Violations []Violation `json:"violations,omitempty"`

	// Audit entries (for audited requests)
	AuditEntries []AuditEntry `json:"audit_entries,omitempty"`

	// Evaluation metadata
	EvaluationTime time.Duration `json:"evaluation_time"`
	PolicyCount    int           `json:"policy_count"`
}

// Violation represents a policy violation
type Violation struct {
	PolicyName  string `json:"policy_name"`
	RuleName    string `json:"rule_name"`
	Message     string `json:"message"`
	Severity    string `json:"severity"` // "Critical", "High", "Medium", "Low"
	Field       string `json:"field,omitempty"`
	Value       string `json:"value,omitempty"`
}

// AuditEntry represents an audit log entry
type AuditEntry struct {
	PolicyName string    `json:"policy_name"`
	RuleName   string    `json:"rule_name"`
	Message    string    `json:"message"`
	Timestamp  time.Time `json:"timestamp"`
	Action     string    `json:"action"`
}

// EngineConfig contains policy engine configuration
type EngineConfig struct {
	// DefaultAction when no policies match
	DefaultAction PolicyAction

	// EnableAuditLogging enables audit logging
	EnableAuditLogging bool

	// MaxEvaluationTime is the timeout for policy evaluation
	MaxEvaluationTime time.Duration

	// CacheEnabled enables policy result caching
	CacheEnabled bool

	// CacheTTL is the cache TTL for policy results
	CacheTTL time.Duration
}

// DefaultEngineConfig returns default engine configuration
func DefaultEngineConfig() EngineConfig {
	return EngineConfig{
		DefaultAction:      PolicyActionDeny, // Secure by default
		EnableAuditLogging: true,
		MaxEvaluationTime:  5 * time.Second,
		CacheEnabled:       true,
		CacheTTL:           5 * time.Minute,
	}
}

// RegistryRule represents image registry restriction rule
type RegistryRule struct {
	// AllowedRegistries is a list of allowed registry patterns
	AllowedRegistries []string `json:"allowed_registries"`

	// DeniedRegistries is a list of denied registry patterns
	DeniedRegistries []string `json:"denied_registries"`

	// RequireSignedImages requires image signatures
	RequireSignedImages bool `json:"require_signed_images,omitempty"`
}

// CapabilitiesRule represents capabilities restriction rule
type CapabilitiesRule struct {
	// AllowedCapabilities is a list of allowed capabilities
	AllowedCapabilities []string `json:"allowed_capabilities"`

	// DeniedCapabilities is a list of denied capabilities
	DeniedCapabilities []string `json:"denied_capabilities"`

	// RequireDropAll requires dropping all capabilities first
	RequireDropAll bool `json:"require_drop_all,omitempty"`
}

// EgressRule represents egress destination restriction rule
type EgressRule struct {
	// AllowedDestinations is a list of allowed CIDR/domain patterns
	AllowedDestinations []string `json:"allowed_destinations"`

	// DeniedDestinations is a list of denied CIDR/domain patterns
	DeniedDestinations []string `json:"denied_destinations"`

	// AllowPrivateIPs allows private IP ranges
	AllowPrivateIPs bool `json:"allow_private_ips,omitempty"`
}

// ResourceLimitRule represents resource limit enforcement rule
type ResourceLimitRule struct {
	// MaxCPUMillicores is the maximum CPU request
	MaxCPUMillicores int64 `json:"max_cpu_millicores,omitempty"`

	// MaxMemoryBytes is the maximum memory request
	MaxMemoryBytes int64 `json:"max_memory_bytes,omitempty"`

	// MaxStorageBytes is the maximum storage request
	MaxStorageBytes int64 `json:"max_storage_bytes,omitempty"`

	// RequireLimits requires resource limits to be set
	RequireLimits bool `json:"require_limits,omitempty"`

	// MaxLimitToRequestRatio is the max ratio of limit to request
	MaxLimitToRequestRatio float64 `json:"max_limit_to_request_ratio,omitempty"`
}
