package scheduler

import (
	"context"
	"fmt"
	"math"
	"sort"
	"sync"
	"time"

	"github.com/cloudless/cloudless/pkg/coordinator/membership"
	"go.uber.org/zap"
)

// Scheduler handles workload placement decisions
type Scheduler struct {
	membershipManager *membership.Manager
	scorer            *Scorer
	binPacker         *BinPacker
	logger            *zap.Logger
	config            *SchedulerConfig
	mu                sync.RWMutex
}

// SchedulerConfig contains scheduler configuration
type SchedulerConfig struct {
	// Scoring weights
	LocalityWeight     float64
	ReliabilityWeight  float64
	CostWeight         float64
	UtilizationWeight  float64
	NetworkWeight      float64

	// Scheduling policies
	MaxRetries         int
	RetryBackoff       time.Duration
	SpreadPolicy       string // "zone", "region", "node"
	PackingStrategy    string // "binpack", "spread", "random"
	PreferredLocality  string // preferred region/zone
	EnforcePlacement   bool   // strictly enforce placement constraints
}

// WorkloadSpec defines workload requirements
type WorkloadSpec struct {
	ID                string
	Name              string
	Namespace         string
	Replicas          int
	Resources         ResourceRequirements
	PlacementPolicy   PlacementPolicy
	RestartPolicy     RestartPolicy
	RolloutStrategy   RolloutStrategy
	Priority          int
	Labels            map[string]string
	Annotations       map[string]string
}

// ResourceRequirements defines resource needs
type ResourceRequirements struct {
	Requests ResourceSpec
	Limits   ResourceSpec
}

// ResourceSpec defines specific resource amounts
type ResourceSpec struct {
	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64
	BandwidthBPS  int64
	GPUCount      int32
}

// PlacementPolicy controls where workloads run
type PlacementPolicy struct {
	Regions       []string
	Zones         []string
	NodeSelector  map[string]string
	Affinity      []AffinityRule
	AntiAffinity  []AffinityRule
	Tolerations   []Toleration
	SpreadTopology string
}

// AffinityRule defines affinity constraints
type AffinityRule struct {
	Type        string // "node", "workload"
	MatchLabels map[string]string
	TopologyKey string
	Required    bool
	Weight      int32
}

// Toleration allows workload on tainted nodes
type Toleration struct {
	Key      string
	Value    string
	Operator string // "Equal", "Exists"
	Effect   string // "NoSchedule", "PreferNoSchedule", "NoExecute"
}

// RestartPolicy defines restart behavior
type RestartPolicy struct {
	Policy     string // "Always", "OnFailure", "Never"
	MaxRetries int
	Backoff    time.Duration
}

// RolloutStrategy controls updates
type RolloutStrategy struct {
	Strategy        string // "RollingUpdate", "Recreate", "BlueGreen"
	MaxSurge        int
	MaxUnavailable  int
	PauseDuration   time.Duration
}

// ScheduleDecision represents a placement decision
type ScheduleDecision struct {
	ReplicaID   string
	NodeID      string
	NodeName    string
	FragmentID  string
	Score       float64
	Reasons     []string
}

// ScheduleResult contains scheduling results
type ScheduleResult struct {
	Decisions   []ScheduleDecision
	Success     bool
	Message     string
	UnscheduledReplicas int
}

// Fragment represents a resource allocation unit
type Fragment struct {
	ID        string
	NodeID    string
	Resources ResourceSpec
	State     string // "available", "reserved", "allocated", "releasing"
	WorkloadID string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// DefaultSchedulerConfig returns default scheduler configuration
func DefaultSchedulerConfig() *SchedulerConfig {
	return &SchedulerConfig{
		LocalityWeight:    0.25,
		ReliabilityWeight: 0.30,
		CostWeight:        0.15,
		UtilizationWeight: 0.20,
		NetworkWeight:     0.10,
		MaxRetries:        3,
		RetryBackoff:      time.Second,
		SpreadPolicy:      "zone",
		PackingStrategy:   "binpack",
		EnforcePlacement:  false,
	}
}

// NewScheduler creates a new scheduler
func NewScheduler(membershipManager *membership.Manager, config *SchedulerConfig, logger *zap.Logger) (*Scheduler, error) {
	if membershipManager == nil {
		return nil, fmt.Errorf("membership manager is required")
	}
	if config == nil {
		config = DefaultSchedulerConfig()
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	s := &Scheduler{
		membershipManager: membershipManager,
		config:            config,
		logger:            logger,
	}

	// Initialize scorer
	s.scorer = NewScorer(ScorerConfig{
		LocalityWeight:    config.LocalityWeight,
		ReliabilityWeight: config.ReliabilityWeight,
		CostWeight:        config.CostWeight,
		UtilizationWeight: config.UtilizationWeight,
		NetworkWeight:     config.NetworkWeight,
	}, logger)

	// Initialize bin packer
	s.binPacker = NewBinPacker(logger)

	return s, nil
}

// Schedule finds placement for a workload
func (s *Scheduler) Schedule(ctx context.Context, workload *WorkloadSpec) (*ScheduleResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.logger.Info("Scheduling workload",
		zap.String("workload_id", workload.ID),
		zap.String("name", workload.Name),
		zap.Int("replicas", workload.Replicas),
	)

	// Get available nodes
	nodes, err := s.membershipManager.ListNodes(nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	// Filter nodes based on requirements
	candidateNodes := s.filterNodes(nodes, workload)
	if len(candidateNodes) == 0 {
		return &ScheduleResult{
			Success:             false,
			Message:             "No nodes match requirements",
			UnscheduledReplicas: workload.Replicas,
		}, nil
	}

	// Score and rank nodes
	scoredNodes := s.scoreNodes(candidateNodes, workload)

	// Sort nodes by score
	sort.Slice(scoredNodes, func(i, j int) bool {
		return scoredNodes[i].Score > scoredNodes[j].Score
	})

	// Make placement decisions
	decisions := []ScheduleDecision{}
	scheduled := 0

	// Apply packing strategy
	switch s.config.PackingStrategy {
	case "binpack":
		decisions = s.binPackSchedule(scoredNodes, workload)
	case "spread":
		decisions = s.spreadSchedule(scoredNodes, workload)
	default:
		decisions = s.defaultSchedule(scoredNodes, workload)
	}

	scheduled = len(decisions)

	result := &ScheduleResult{
		Decisions:           decisions,
		Success:             scheduled == workload.Replicas,
		UnscheduledReplicas: workload.Replicas - scheduled,
	}

	if result.Success {
		result.Message = fmt.Sprintf("Successfully scheduled %d replicas", scheduled)
		s.logger.Info("Workload scheduled successfully",
			zap.String("workload_id", workload.ID),
			zap.Int("scheduled", scheduled),
		)
	} else {
		result.Message = fmt.Sprintf("Only scheduled %d of %d replicas", scheduled, workload.Replicas)
		s.logger.Warn("Partial scheduling",
			zap.String("workload_id", workload.ID),
			zap.Int("scheduled", scheduled),
			zap.Int("requested", workload.Replicas),
		)
	}

	return result, nil
}

// filterNodes filters nodes based on workload requirements
func (s *Scheduler) filterNodes(nodes []*membership.NodeInfo, workload *WorkloadSpec) []*membership.NodeInfo {
	filtered := []*membership.NodeInfo{}

	for _, node := range nodes {
		// Check node state
		if node.State != membership.StateReady {
			s.logger.Debug("Node filtered: not ready",
				zap.String("node_id", node.ID),
				zap.String("state", node.State),
			)
			continue
		}

		// Check resource availability
		if !s.hasEnoughResources(node, workload.Resources.Requests) {
			s.logger.Debug("Node filtered: insufficient resources",
				zap.String("node_id", node.ID),
			)
			continue
		}

		// Check placement policy
		if !s.matchesPlacementPolicy(node, workload.PlacementPolicy) {
			s.logger.Debug("Node filtered: placement policy mismatch",
				zap.String("node_id", node.ID),
			)
			continue
		}

		// Check tolerations for taints
		if !s.toleratesTaints(node, workload.PlacementPolicy.Tolerations) {
			s.logger.Debug("Node filtered: taints not tolerated",
				zap.String("node_id", node.ID),
			)
			continue
		}

		filtered = append(filtered, node)
	}

	s.logger.Debug("Filtered candidate nodes",
		zap.Int("total", len(nodes)),
		zap.Int("candidates", len(filtered)),
	)

	return filtered
}

// hasEnoughResources checks if node has enough resources
func (s *Scheduler) hasEnoughResources(node *membership.NodeInfo, requests ResourceSpec) bool {
	availableCPU := node.Capacity.CPUMillicores - node.Usage.CPUMillicores
	availableMemory := node.Capacity.MemoryBytes - node.Usage.MemoryBytes
	availableStorage := node.Capacity.StorageBytes - node.Usage.StorageBytes

	return availableCPU >= requests.CPUMillicores &&
		availableMemory >= requests.MemoryBytes &&
		availableStorage >= requests.StorageBytes
}

// matchesPlacementPolicy checks if node matches placement policy
func (s *Scheduler) matchesPlacementPolicy(node *membership.NodeInfo, policy PlacementPolicy) bool {
	// Check regions
	if len(policy.Regions) > 0 {
		regionMatch := false
		for _, region := range policy.Regions {
			if node.Region == region {
				regionMatch = true
				break
			}
		}
		if !regionMatch {
			return false
		}
	}

	// Check zones
	if len(policy.Zones) > 0 {
		zoneMatch := false
		for _, zone := range policy.Zones {
			if node.Zone == zone {
				zoneMatch = true
				break
			}
		}
		if !zoneMatch {
			return false
		}
	}

	// Check node selector
	for key, value := range policy.NodeSelector {
		if nodeValue, exists := node.Labels[key]; !exists || nodeValue != value {
			return false
		}
	}

	// Check required affinity rules
	for _, affinity := range policy.Affinity {
		if affinity.Required && !s.matchesAffinity(node, affinity) {
			return false
		}
	}

	// Check anti-affinity rules
	for _, antiAffinity := range policy.AntiAffinity {
		if antiAffinity.Required && s.matchesAffinity(node, antiAffinity) {
			return false // Node matches anti-affinity, so exclude it
		}
	}

	return true
}

// matchesAffinity checks if node matches affinity rule
func (s *Scheduler) matchesAffinity(node *membership.NodeInfo, affinity AffinityRule) bool {
	if affinity.Type == "node" {
		// Check if node labels match
		for key, value := range affinity.MatchLabels {
			if nodeValue, exists := node.Labels[key]; !exists || nodeValue != value {
				return false
			}
		}
		return true
	}

	// For workload affinity, would need to check existing workloads on the node
	// This is simplified for now
	return false
}

// toleratesTaints checks if workload tolerates node taints
func (s *Scheduler) toleratesTaints(node *membership.NodeInfo, tolerations []Toleration) bool {
	for _, taint := range node.Taints {
		tolerated := false
		for _, toleration := range tolerations {
			if s.matchesToleration(taint, toleration) {
				tolerated = true
				break
			}
		}
		if !tolerated && taint.Effect == "NoSchedule" {
			return false
		}
	}
	return true
}

// matchesToleration checks if a taint matches a toleration
func (s *Scheduler) matchesToleration(taint membership.Taint, toleration Toleration) bool {
	if toleration.Key != taint.Key {
		return false
	}

	if toleration.Operator == "Exists" {
		return true
	}

	if toleration.Operator == "Equal" && toleration.Value == taint.Value {
		if toleration.Effect == "" || toleration.Effect == taint.Effect {
			return true
		}
	}

	return false
}

// scoreNodes scores nodes for workload placement
func (s *Scheduler) scoreNodes(nodes []*membership.NodeInfo, workload *WorkloadSpec) []ScoredNode {
	scored := []ScoredNode{}

	for _, node := range nodes {
		score := s.scorer.ScoreNode(node, workload)
		scored = append(scored, score)
	}

	return scored
}

// binPackSchedule implements bin-packing scheduling strategy
func (s *Scheduler) binPackSchedule(nodes []ScoredNode, workload *WorkloadSpec) []ScheduleDecision {
	decisions := []ScheduleDecision{}
	scheduled := 0

	// Use bin packer to find optimal placement
	packing := s.binPacker.Pack(nodes, workload.Resources.Requests, workload.Replicas)

	for _, placement := range packing {
		fragmentID := s.generateFragmentID()
		decision := ScheduleDecision{
			ReplicaID:  fmt.Sprintf("%s-%d", workload.ID, scheduled),
			NodeID:     placement.NodeID,
			NodeName:   placement.NodeName,
			FragmentID: fragmentID,
			Score:      placement.Score,
			Reasons:    []string{"bin-packed"},
		}
		decisions = append(decisions, decision)
		scheduled++

		if scheduled >= workload.Replicas {
			break
		}
	}

	return decisions
}

// spreadSchedule implements spread scheduling strategy
func (s *Scheduler) spreadSchedule(nodes []ScoredNode, workload *WorkloadSpec) []ScheduleDecision {
	decisions := []ScheduleDecision{}
	scheduled := 0

	// Group nodes by spread topology
	nodeGroups := s.groupNodesByTopology(nodes, s.config.SpreadPolicy)

	// Distribute replicas across groups
	for _, group := range nodeGroups {
		if scheduled >= workload.Replicas {
			break
		}

		// Pick best node from group
		if len(group) > 0 {
			fragmentID := s.generateFragmentID()
			decision := ScheduleDecision{
				ReplicaID:  fmt.Sprintf("%s-%d", workload.ID, scheduled),
				NodeID:     group[0].Node.ID,
				NodeName:   group[0].Node.Name,
				FragmentID: fragmentID,
				Score:      group[0].Score,
				Reasons:    []string{fmt.Sprintf("spread across %s", s.config.SpreadPolicy)},
			}
			decisions = append(decisions, decision)
			scheduled++
		}
	}

	// If not enough groups, use remaining nodes
	for _, node := range nodes {
		if scheduled >= workload.Replicas {
			break
		}

		// Check if already scheduled
		alreadyScheduled := false
		for _, decision := range decisions {
			if decision.NodeID == node.Node.ID {
				alreadyScheduled = true
				break
			}
		}

		if !alreadyScheduled {
			fragmentID := s.generateFragmentID()
			decision := ScheduleDecision{
				ReplicaID:  fmt.Sprintf("%s-%d", workload.ID, scheduled),
				NodeID:     node.Node.ID,
				NodeName:   node.Node.Name,
				FragmentID: fragmentID,
				Score:      node.Score,
				Reasons:    []string{"spread overflow"},
			}
			decisions = append(decisions, decision)
			scheduled++
		}
	}

	return decisions
}

// defaultSchedule implements default scheduling strategy
func (s *Scheduler) defaultSchedule(nodes []ScoredNode, workload *WorkloadSpec) []ScheduleDecision {
	decisions := []ScheduleDecision{}

	for i := 0; i < workload.Replicas && i < len(nodes); i++ {
		fragmentID := s.generateFragmentID()
		decision := ScheduleDecision{
			ReplicaID:  fmt.Sprintf("%s-%d", workload.ID, i),
			NodeID:     nodes[i].Node.ID,
			NodeName:   nodes[i].Node.Name,
			FragmentID: fragmentID,
			Score:      nodes[i].Score,
			Reasons:    []string{"default placement"},
		}
		decisions = append(decisions, decision)
	}

	return decisions
}

// groupNodesByTopology groups nodes by topology key
func (s *Scheduler) groupNodesByTopology(nodes []ScoredNode, topology string) map[string][]ScoredNode {
	groups := make(map[string][]ScoredNode)

	for _, node := range nodes {
		var key string
		switch topology {
		case "zone":
			key = node.Node.Zone
		case "region":
			key = node.Node.Region
		case "node":
			key = node.Node.ID
		default:
			key = "default"
		}

		groups[key] = append(groups[key], node)
	}

	// Sort each group by score
	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			return group[i].Score > group[j].Score
		})
	}

	return groups
}

// generateFragmentID generates a unique fragment ID
func (s *Scheduler) generateFragmentID() string {
	return fmt.Sprintf("fragment-%d-%d", time.Now().Unix(), time.Now().Nanosecond())
}

// Reschedule handles rescheduling of failed replicas
func (s *Scheduler) Reschedule(ctx context.Context, workload *WorkloadSpec, failedNodes []string) (*ScheduleResult, error) {
	s.logger.Info("Rescheduling workload",
		zap.String("workload_id", workload.ID),
		zap.Int("failed_nodes", len(failedNodes)),
	)

	// Mark failed nodes as unavailable temporarily
	// Then call Schedule with updated constraints
	return s.Schedule(ctx, workload)
}

// PreemptWorkloads handles preemption for higher priority workloads
func (s *Scheduler) PreemptWorkloads(ctx context.Context, workload *WorkloadSpec) ([]string, error) {
	// Find lower priority workloads that can be preempted
	// This is a simplified implementation
	preempted := []string{}

	if workload.Priority > 100 { // High priority threshold
		s.logger.Info("Considering preemption for high-priority workload",
			zap.String("workload_id", workload.ID),
			zap.Int("priority", workload.Priority),
		)
		// TODO: Implement preemption logic
	}

	return preempted, nil
}

// UpdateSchedulerConfig updates the scheduler configuration
func (s *Scheduler) UpdateSchedulerConfig(config *SchedulerConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.config = config

	// Update scorer weights
	s.scorer.UpdateWeights(ScorerConfig{
		LocalityWeight:    config.LocalityWeight,
		ReliabilityWeight: config.ReliabilityWeight,
		CostWeight:        config.CostWeight,
		UtilizationWeight: config.UtilizationWeight,
		NetworkWeight:     config.NetworkWeight,
	})

	s.logger.Info("Updated scheduler configuration",
		zap.String("packing_strategy", config.PackingStrategy),
		zap.String("spread_policy", config.SpreadPolicy),
	)
}