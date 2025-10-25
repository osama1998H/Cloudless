package scheduler

import (
	"math"

	"github.com/cloudless/cloudless/pkg/coordinator/membership"
	"go.uber.org/zap"
)

// Scorer calculates scores for node placement
type Scorer struct {
	config ScorerConfig
	logger *zap.Logger
}

// ScorerConfig contains scoring weights
type ScorerConfig struct {
	LocalityWeight    float64
	ReliabilityWeight float64
	CostWeight        float64
	UtilizationWeight float64
	NetworkWeight     float64
}

// ScoredNode represents a node with its score
type ScoredNode struct {
	Node  *membership.NodeInfo
	Score float64
	Components ScoreComponents
}

// ScoreComponents breaks down the total score
type ScoreComponents struct {
	LocalityScore    float64
	ReliabilityScore float64
	CostScore        float64
	UtilizationScore float64
	NetworkScore     float64
}

// NewScorer creates a new scorer
func NewScorer(config ScorerConfig, logger *zap.Logger) *Scorer {
	// Normalize weights
	total := config.LocalityWeight + config.ReliabilityWeight +
			config.CostWeight + config.UtilizationWeight + config.NetworkWeight

	if total > 0 {
		config.LocalityWeight /= total
		config.ReliabilityWeight /= total
		config.CostWeight /= total
		config.UtilizationWeight /= total
		config.NetworkWeight /= total
	} else {
		// Default weights if not specified
		config.LocalityWeight = 0.25
		config.ReliabilityWeight = 0.30
		config.CostWeight = 0.15
		config.UtilizationWeight = 0.20
		config.NetworkWeight = 0.10
	}

	return &Scorer{
		config: config,
		logger: logger,
	}
}

// ScoreNode calculates the placement score for a node
func (s *Scorer) ScoreNode(node *membership.NodeInfo, workload *WorkloadSpec) ScoredNode {
	components := ScoreComponents{
		LocalityScore:    s.calculateLocalityScore(node, workload),
		ReliabilityScore: s.calculateReliabilityScore(node),
		CostScore:        s.calculateCostScore(node, workload),
		UtilizationScore: s.calculateUtilizationScore(node, workload),
		NetworkScore:     s.calculateNetworkScore(node, workload),
	}

	// Calculate weighted total score
	totalScore := components.LocalityScore*s.config.LocalityWeight +
		components.ReliabilityScore*s.config.ReliabilityWeight +
		components.CostScore*s.config.CostWeight +
		components.UtilizationScore*s.config.UtilizationWeight +
		components.NetworkScore*s.config.NetworkWeight

	// Apply affinity bonuses/penalties
	totalScore = s.applyAffinityAdjustments(totalScore, node, workload)

	// Ensure score is between 0 and 1
	totalScore = math.Max(0, math.Min(1, totalScore))

	s.logger.Debug("Scored node",
		zap.String("node_id", node.ID),
		zap.Float64("total_score", totalScore),
		zap.Float64("locality", components.LocalityScore),
		zap.Float64("reliability", components.ReliabilityScore),
		zap.Float64("cost", components.CostScore),
		zap.Float64("utilization", components.UtilizationScore),
		zap.Float64("network", components.NetworkScore),
	)

	return ScoredNode{
		Node:       node,
		Score:      totalScore,
		Components: components,
	}
}

// calculateLocalityScore scores based on locality preferences
func (s *Scorer) calculateLocalityScore(node *membership.NodeInfo, workload *WorkloadSpec) float64 {
	score := 0.5 // Neutral baseline

	// Check region preference
	if len(workload.PlacementPolicy.Regions) > 0 {
		for i, region := range workload.PlacementPolicy.Regions {
			if node.Region == region {
				// Higher score for earlier in preference list
				score = 1.0 - (float64(i) * 0.1)
				break
			}
		}
	} else {
		score = 0.7 // No preference means moderate score
	}

	// Check zone preference
	if len(workload.PlacementPolicy.Zones) > 0 {
		zoneMatch := false
		for _, zone := range workload.PlacementPolicy.Zones {
			if node.Zone == zone {
				zoneMatch = true
				score += 0.2
				break
			}
		}
		if !zoneMatch {
			score -= 0.2
		}
	}

	// Bonus for nodes in same region/zone as existing replicas (for data locality)
	// This would require checking existing placements in production
	// For now, simplified

	return math.Max(0, math.Min(1, score))
}

// calculateReliabilityScore scores based on node reliability
func (s *Scorer) calculateReliabilityScore(node *membership.NodeInfo) float64 {
	// Use the reliability score from the node
	baseScore := node.ReliabilityScore

	// Adjust based on current conditions
	for _, condition := range node.Conditions {
		if condition.Status == "True" {
			switch condition.Type {
			case "MemoryPressure":
				baseScore -= 0.2
			case "DiskPressure":
				baseScore -= 0.2
			case "NetworkUnavailable":
				baseScore -= 0.4
			case "Ready":
				baseScore += 0.1
			}
		}
	}

	// Consider node state
	switch node.State {
	case membership.StateReady:
		// No adjustment
	case membership.StateEnrolling:
		baseScore *= 0.5 // New nodes are less reliable
	case membership.StateDraining, membership.StateCordoned:
		baseScore = 0 // Should not schedule here
	default:
		baseScore = 0
	}

	return math.Max(0, math.Min(1, baseScore))
}

// calculateCostScore scores based on resource cost
func (s *Scorer) calculateCostScore(node *membership.NodeInfo, workload *WorkloadSpec) float64 {
	// In a real system, this would consider:
	// - Spot vs on-demand pricing
	// - Region-specific costs
	// - Resource type costs (GPU more expensive)
	// - Network egress costs

	// For now, use a simple model based on resource efficiency

	// Prefer nodes with resources that closely match requirements
	cpuRatio := float64(workload.Resources.Requests.CPUMillicores) / float64(node.Capacity.CPUMillicores)
	memRatio := float64(workload.Resources.Requests.MemoryBytes) / float64(node.Capacity.MemoryBytes)

	// Best score when workload uses 10-30% of node capacity
	// This avoids both waste and overcommitment
	var score float64
	avgRatio := (cpuRatio + memRatio) / 2

	switch {
	case avgRatio < 0.1:
		score = 0.7 // Too small, some waste
	case avgRatio < 0.3:
		score = 1.0 // Optimal
	case avgRatio < 0.5:
		score = 0.8 // Good
	case avgRatio < 0.7:
		score = 0.6 // Getting full
	default:
		score = 0.3 // Too large for node
	}

	// Penalty for GPU if not needed
	if node.Capacity.GPUCount > 0 && workload.Resources.Requests.GPUCount == 0 {
		score -= 0.3 // Wasting expensive GPU resources
	}

	return math.Max(0, math.Min(1, score))
}

// calculateUtilizationScore scores based on resource utilization balance
func (s *Scorer) calculateUtilizationScore(node *membership.NodeInfo, workload *WorkloadSpec) float64 {
	// Calculate current utilization
	currentCPUUtil := float64(node.Usage.CPUMillicores) / float64(node.Capacity.CPUMillicores)
	currentMemUtil := float64(node.Usage.MemoryBytes) / float64(node.Capacity.MemoryBytes)

	// Calculate utilization after placement
	futureCPUUtil := (float64(node.Usage.CPUMillicores) + float64(workload.Resources.Requests.CPUMillicores)) /
					float64(node.Capacity.CPUMillicores)
	futureMemUtil := (float64(node.Usage.MemoryBytes) + float64(workload.Resources.Requests.MemoryBytes)) /
					float64(node.Capacity.MemoryBytes)

	// Check if placement would overcommit
	if futureCPUUtil > 1.0 || futureMemUtil > 1.0 {
		return 0 // Cannot fit
	}

	// Prefer balanced utilization (around 60-70%)
	targetUtil := 0.65
	cpuDiff := math.Abs(futureCPUUtil - targetUtil)
	memDiff := math.Abs(futureMemUtil - targetUtil)
	avgDiff := (cpuDiff + memDiff) / 2

	// Convert difference to score
	score := 1.0 - avgDiff

	// Bonus for improving balance
	currentBalance := math.Abs(currentCPUUtil - currentMemUtil)
	futureBalance := math.Abs(futureCPUUtil - futureMemUtil)
	if futureBalance < currentBalance {
		score += 0.1 // Improves resource balance
	}

	// Penalty for very high utilization
	if futureCPUUtil > 0.9 || futureMemUtil > 0.9 {
		score -= 0.3
	}

	return math.Max(0, math.Min(1, score))
}

// calculateNetworkScore scores based on network characteristics
func (s *Scorer) calculateNetworkScore(node *membership.NodeInfo, workload *WorkloadSpec) float64 {
	score := 0.5 // Baseline

	// Check bandwidth availability
	if node.Capacity.BandwidthBPS > 0 {
		availableBandwidth := node.Capacity.BandwidthBPS - node.Usage.BandwidthBPS
		requiredBandwidth := workload.Resources.Requests.BandwidthBPS

		if requiredBandwidth > 0 {
			if availableBandwidth >= requiredBandwidth {
				score = 0.8
				// Bonus for excess bandwidth
				excessRatio := float64(availableBandwidth) / float64(requiredBandwidth)
				if excessRatio > 2 {
					score = 1.0
				}
			} else {
				// Insufficient bandwidth
				deficitRatio := float64(availableBandwidth) / float64(requiredBandwidth)
				score = deficitRatio * 0.5
			}
		} else {
			score = 0.9 // No specific bandwidth requirement
		}
	}

	// Network location factors
	if node.NetworkInfo.PublicIP != "" {
		score += 0.1 // Has public IP
	}

	// Check for network-related conditions
	for _, condition := range node.Conditions {
		if condition.Type == "NetworkUnavailable" && condition.Status == "True" {
			score = 0 // Network issues
		}
	}

	// Consider network features
	hasRequiredFeatures := true
	for _, required := range workload.PlacementPolicy.NodeSelector {
		found := false
		for _, feature := range node.Capabilities.NetworkFeatures {
			if feature == required {
				found = true
				break
			}
		}
		if !found {
			hasRequiredFeatures = false
			break
		}
	}
	if !hasRequiredFeatures {
		score *= 0.5
	}

	return math.Max(0, math.Min(1, score))
}

// applyAffinityAdjustments applies affinity and anti-affinity adjustments
func (s *Scorer) applyAffinityAdjustments(score float64, node *membership.NodeInfo, workload *WorkloadSpec) float64 {
	// Apply affinity bonuses
	for _, affinity := range workload.PlacementPolicy.Affinity {
		if s.nodeMatchesAffinity(node, affinity) {
			if affinity.Required {
				// Required affinity already handled in filtering
				score += 0.1
			} else {
				// Preferred affinity
				weight := float64(affinity.Weight) / 100.0
				score += weight * 0.2
			}
		}
	}

	// Apply anti-affinity penalties
	for _, antiAffinity := range workload.PlacementPolicy.AntiAffinity {
		if s.nodeMatchesAffinity(node, antiAffinity) {
			if antiAffinity.Required {
				// Required anti-affinity should have filtered this node
				score = 0
			} else {
				// Preferred anti-affinity
				weight := float64(antiAffinity.Weight) / 100.0
				score -= weight * 0.3
			}
		}
	}

	return score
}

// nodeMatchesAffinity checks if a node matches an affinity rule
func (s *Scorer) nodeMatchesAffinity(node *membership.NodeInfo, affinity AffinityRule) bool {
	if affinity.Type == "node" {
		// Check node labels
		for key, value := range affinity.MatchLabels {
			if nodeValue, exists := node.Labels[key]; !exists || nodeValue != value {
				return false
			}
		}

		// Check topology
		if affinity.TopologyKey != "" {
			switch affinity.TopologyKey {
			case "region":
				// Check if in same region as other workloads
				// Simplified for now
			case "zone":
				// Check if in same zone as other workloads
				// Simplified for now
			}
		}

		return true
	}

	// Workload affinity would check existing workloads
	return false
}

// UpdateWeights updates the scoring weights
func (s *Scorer) UpdateWeights(config ScorerConfig) {
	// Normalize weights
	total := config.LocalityWeight + config.ReliabilityWeight +
			config.CostWeight + config.UtilizationWeight + config.NetworkWeight

	if total > 0 {
		config.LocalityWeight /= total
		config.ReliabilityWeight /= total
		config.CostWeight /= total
		config.UtilizationWeight /= total
		config.NetworkWeight /= total
	}

	s.config = config

	s.logger.Info("Updated scoring weights",
		zap.Float64("locality", config.LocalityWeight),
		zap.Float64("reliability", config.ReliabilityWeight),
		zap.Float64("cost", config.CostWeight),
		zap.Float64("utilization", config.UtilizationWeight),
		zap.Float64("network", config.NetworkWeight),
	)
}

// CompareScores compares two scored nodes
func (s *Scorer) CompareScores(a, b ScoredNode) int {
	if a.Score > b.Score {
		return 1
	} else if a.Score < b.Score {
		return -1
	}
	return 0
}