package membership

import (
	"math"
	"time"

	"go.uber.org/zap"
)

// ScoringEngine calculates reliability scores for nodes
type ScoringEngine struct {
	logger *zap.Logger

	// Weights for different factors
	uptimeWeight       float64
	responseTimeWeight float64
	successRateWeight  float64
	capacityWeight     float64
	stabilityWeight    float64
}

// NewScoringEngine creates a new scoring engine
func NewScoringEngine(logger *zap.Logger) *ScoringEngine {
	return &ScoringEngine{
		logger:             logger,
		uptimeWeight:       0.3,
		responseTimeWeight: 0.2,
		successRateWeight:  0.2,
		capacityWeight:     0.15,
		stabilityWeight:    0.15,
	}
}

// CalculateReliability calculates the reliability score for a node
func (s *ScoringEngine) CalculateReliability(node *NodeInfo) float64 {
	if node == nil {
		return 0
	}

	// Calculate individual scores
	uptimeScore := s.calculateUptimeScore(node)
	responseScore := s.calculateResponseScore(node)
	successScore := s.calculateSuccessScore(node)
	capacityScore := s.calculateCapacityScore(node)
	stabilityScore := s.calculateStabilityScore(node)

	// Weighted average
	totalScore := uptimeScore*s.uptimeWeight +
		responseScore*s.responseTimeWeight +
		successScore*s.successRateWeight +
		capacityScore*s.capacityWeight +
		stabilityScore*s.stabilityWeight

	// Apply penalties
	totalScore = s.applyPenalties(node, totalScore)

	// Ensure score is between 0 and 1
	if totalScore < 0 {
		totalScore = 0
	} else if totalScore > 1 {
		totalScore = 1
	}

	s.logger.Debug("Calculated reliability score",
		zap.String("node_id", node.ID),
		zap.Float64("score", totalScore),
		zap.Float64("uptime", uptimeScore),
		zap.Float64("response", responseScore),
		zap.Float64("success", successScore),
		zap.Float64("capacity", capacityScore),
		zap.Float64("stability", stabilityScore),
	)

	return totalScore
}

// calculateUptimeScore calculates score based on node uptime
func (s *ScoringEngine) calculateUptimeScore(node *NodeInfo) float64 {
	now := time.Now()
	uptime := now.Sub(node.EnrolledAt)

	// No score if node is very new (less than 1 hour)
	if uptime < time.Hour {
		return 0.5 // Neutral score for new nodes
	}

	// Calculate time since last heartbeat
	timeSinceHeartbeat := now.Sub(node.LastHeartbeat)

	// Penalize based on heartbeat delay
	var score float64
	switch {
	case timeSinceHeartbeat < 15*time.Second:
		score = 1.0
	case timeSinceHeartbeat < 30*time.Second:
		score = 0.9
	case timeSinceHeartbeat < 60*time.Second:
		score = 0.7
	case timeSinceHeartbeat < 5*time.Minute:
		score = 0.5
	default:
		score = 0.1
	}

	// Bonus for long-running nodes
	if uptime > 24*time.Hour {
		score += 0.1
	}
	if uptime > 7*24*time.Hour {
		score += 0.1
	}

	return math.Min(score, 1.0)
}

// calculateResponseScore calculates score based on response times
func (s *ScoringEngine) calculateResponseScore(node *NodeInfo) float64 {
	// For now, use heartbeat regularity as a proxy
	// In production, would track actual response times to requests

	timeSinceHeartbeat := time.Since(node.LastHeartbeat)
	expectedInterval := node.HeartbeatInterval

	if timeSinceHeartbeat <= expectedInterval {
		return 1.0
	} else if timeSinceHeartbeat <= expectedInterval*2 {
		return 0.8
	} else if timeSinceHeartbeat <= expectedInterval*3 {
		return 0.6
	}
	return 0.3
}

// calculateSuccessScore calculates score based on task success rate
func (s *ScoringEngine) calculateSuccessScore(node *NodeInfo) float64 {
	// Count successful vs failed containers
	if len(node.Containers) == 0 {
		return 1.0 // No failures if no containers
	}

	successful := 0
	failed := 0

	for _, container := range node.Containers {
		switch container.State {
		case "running", "succeeded":
			successful++
		case "failed", "error", "crashed":
			failed++
		}
	}

	total := successful + failed
	if total == 0 {
		return 1.0
	}

	return float64(successful) / float64(total)
}

// calculateCapacityScore calculates score based on available capacity
func (s *ScoringEngine) calculateCapacityScore(node *NodeInfo) float64 {
	if node.Capacity.CPUMillicores == 0 || node.Capacity.MemoryBytes == 0 {
		return 0 // No capacity means no score
	}

	// Calculate utilization percentages
	cpuUtilization := float64(node.Usage.CPUMillicores) / float64(node.Capacity.CPUMillicores)
	memUtilization := float64(node.Usage.MemoryBytes) / float64(node.Capacity.MemoryBytes)

	// Score higher for moderate utilization (not too empty, not too full)
	cpuScore := s.utilizationToScore(cpuUtilization)
	memScore := s.utilizationToScore(memUtilization)

	// Average of CPU and memory scores
	return (cpuScore + memScore) / 2
}

// calculateStabilityScore calculates score based on node stability
func (s *ScoringEngine) calculateStabilityScore(node *NodeInfo) float64 {
	score := 1.0

	// Check for concerning conditions
	for _, condition := range node.Conditions {
		if condition.Status == "True" {
			switch condition.Type {
			case "MemoryPressure":
				score -= 0.3
			case "DiskPressure":
				score -= 0.3
			case "NetworkUnavailable":
				score -= 0.5
			case "CPUPressure":
				score -= 0.2
			}
		}
	}

	// Check node state
	switch node.State {
	case StateReady:
		// No penalty
	case StateEnrolling:
		score -= 0.2 // Still stabilizing
	case StateDraining, StateCordoned:
		score -= 0.5 // Administratively unavailable
	case StateOffline, StateFailed:
		score = 0 // Not available
	}

	return math.Max(score, 0)
}

// utilizationToScore converts utilization percentage to a score
func (s *ScoringEngine) utilizationToScore(utilization float64) float64 {
	// Optimal utilization is around 50-70%
	// Too low = wasted resources, too high = no headroom
	switch {
	case utilization < 0.3:
		return 0.7 // Underutilized
	case utilization < 0.5:
		return 0.9 // Good headroom
	case utilization < 0.7:
		return 1.0 // Optimal
	case utilization < 0.85:
		return 0.8 // Getting full
	case utilization < 0.95:
		return 0.5 // Very full
	default:
		return 0.2 // Overloaded
	}
}

// applyPenalties applies additional penalties based on node characteristics
func (s *ScoringEngine) applyPenalties(node *NodeInfo, score float64) float64 {
	// Penalty for nodes with taints
	taintPenalty := float64(len(node.Taints)) * 0.1
	score -= taintPenalty

	// Penalty for unreliable network
	// This would be based on actual network metrics in production
	if node.NetworkInfo.PublicIP == "" {
		score -= 0.1 // No public IP might indicate NAT issues
	}

	// Penalty for very new nodes (less than 10 minutes)
	nodeAge := time.Since(node.EnrolledAt)
	if nodeAge < 10*time.Minute {
		score *= 0.8 // 20% penalty for brand new nodes
	}

	return score
}

// CompareNodes compares two nodes and returns which is more reliable
func (s *ScoringEngine) CompareNodes(a, b *NodeInfo) int {
	scoreA := s.CalculateReliability(a)
	scoreB := s.CalculateReliability(b)

	if scoreA > scoreB {
		return 1
	} else if scoreA < scoreB {
		return -1
	}
	return 0
}

// GetRecommendation provides a recommendation based on the score
func (s *ScoringEngine) GetRecommendation(score float64) string {
	switch {
	case score >= 0.9:
		return "Excellent - Ideal for critical workloads"
	case score >= 0.75:
		return "Good - Suitable for most workloads"
	case score >= 0.6:
		return "Fair - Suitable for non-critical workloads"
	case score >= 0.4:
		return "Poor - Use only for testing or low-priority tasks"
	default:
		return "Unreliable - Not recommended for workloads"
	}
}

// AdjustWeights allows dynamic adjustment of scoring weights
func (s *ScoringEngine) AdjustWeights(uptime, response, success, capacity, stability float64) {
	total := uptime + response + success + capacity + stability

	// Normalize weights to sum to 1
	s.uptimeWeight = uptime / total
	s.responseTimeWeight = response / total
	s.successRateWeight = success / total
	s.capacityWeight = capacity / total
	s.stabilityWeight = stability / total

	s.logger.Info("Adjusted scoring weights",
		zap.Float64("uptime", s.uptimeWeight),
		zap.Float64("response", s.responseTimeWeight),
		zap.Float64("success", s.successRateWeight),
		zap.Float64("capacity", s.capacityWeight),
		zap.Float64("stability", s.stabilityWeight),
	)
}
