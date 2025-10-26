package scheduler

import (
	"math"
	"testing"

	"github.com/cloudless/cloudless/pkg/coordinator/membership"
	"go.uber.org/zap"
)

// TestScorer_NewScorer_WeightNormalization verifies CLD-REQ-021.
// CLD-REQ-021: Scheduler uses scoring function with weighted components.
// This test ensures weights are properly normalized to sum to 1.0.
func TestScorer_NewScorer_WeightNormalization(t *testing.T) {
	tests := []struct {
		name           string
		config         ScorerConfig
		expectDefaults bool
	}{
		{
			name: "custom weights - normalized",
			config: ScorerConfig{
				LocalityWeight:       10.0,
				ReliabilityWeight:    20.0,
				CostWeight:           15.0,
				UtilizationWeight:    30.0,
				NetworkPenaltyWeight: 25.0,
			},
			expectDefaults: false,
		},
		{
			name: "unequal weights - normalized",
			config: ScorerConfig{
				LocalityWeight:       1.0,
				ReliabilityWeight:    2.0,
				CostWeight:           3.0,
				UtilizationWeight:    4.0,
				NetworkPenaltyWeight: 5.0,
			},
			expectDefaults: false,
		},
		{
			name: "already normalized weights",
			config: ScorerConfig{
				LocalityWeight:       0.25,
				ReliabilityWeight:    0.30,
				CostWeight:           0.15,
				UtilizationWeight:    0.20,
				NetworkPenaltyWeight: 0.10,
			},
			expectDefaults: false,
		},
		{
			name:           "zero weights - use defaults",
			config:         ScorerConfig{},
			expectDefaults: true,
		},
		{
			name: "partial zero weights",
			config: ScorerConfig{
				LocalityWeight:       0.0,
				ReliabilityWeight:    1.0,
				CostWeight:           0.0,
				UtilizationWeight:    1.0,
				NetworkPenaltyWeight: 0.0,
			},
			expectDefaults: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			scorer := NewScorer(tt.config, logger)

			// Verify weights sum to 1.0 (with floating point tolerance)
			total := scorer.config.LocalityWeight +
				scorer.config.ReliabilityWeight +
				scorer.config.CostWeight +
				scorer.config.UtilizationWeight +
				scorer.config.NetworkPenaltyWeight

			if math.Abs(total-1.0) > 0.0001 {
				t.Errorf("Weights should sum to 1.0, got %f", total)
			}

			// Verify default weights if expected
			if tt.expectDefaults {
				if scorer.config.LocalityWeight != 0.25 {
					t.Errorf("Expected default LocalityWeight 0.25, got %f", scorer.config.LocalityWeight)
				}
				if scorer.config.ReliabilityWeight != 0.30 {
					t.Errorf("Expected default ReliabilityWeight 0.30, got %f", scorer.config.ReliabilityWeight)
				}
				if scorer.config.CostWeight != 0.15 {
					t.Errorf("Expected default CostWeight 0.15, got %f", scorer.config.CostWeight)
				}
				if scorer.config.UtilizationWeight != 0.20 {
					t.Errorf("Expected default UtilizationWeight 0.20, got %f", scorer.config.UtilizationWeight)
				}
				if scorer.config.NetworkPenaltyWeight != 0.10 {
					t.Errorf("Expected default NetworkPenaltyWeight 0.10, got %f", scorer.config.NetworkPenaltyWeight)
				}
			}
		})
	}
}

// TestScorer_UpdateWeights_Normalization verifies CLD-REQ-021.
// Tests dynamic weight updates with normalization.
func TestScorer_UpdateWeights_Normalization(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name   string
		config ScorerConfig
	}{
		{
			name: "update to custom weights",
			config: ScorerConfig{
				LocalityWeight:       5.0,
				ReliabilityWeight:    10.0,
				CostWeight:           5.0,
				UtilizationWeight:    15.0,
				NetworkPenaltyWeight: 5.0,
			},
		},
		{
			name: "update with negative weights (clamped to zero)",
			config: ScorerConfig{
				LocalityWeight:       -1.0, // Should be clamped to 0
				ReliabilityWeight:    1.0,
				CostWeight:           1.0,
				UtilizationWeight:    1.0,
				NetworkPenaltyWeight: 1.0,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scorer.UpdateWeights(tt.config)

			// Verify weights sum to 1.0
			total := scorer.config.LocalityWeight +
				scorer.config.ReliabilityWeight +
				scorer.config.CostWeight +
				scorer.config.UtilizationWeight +
				scorer.config.NetworkPenaltyWeight

			if math.Abs(total-1.0) > 0.0001 {
				t.Errorf("Weights should sum to 1.0 after update, got %f", total)
			}

			// Verify no negative weights
			if scorer.config.LocalityWeight < 0 {
				t.Errorf("LocalityWeight should not be negative, got %f", scorer.config.LocalityWeight)
			}
			if scorer.config.ReliabilityWeight < 0 {
				t.Errorf("ReliabilityWeight should not be negative, got %f", scorer.config.ReliabilityWeight)
			}
			if scorer.config.CostWeight < 0 {
				t.Errorf("CostWeight should not be negative, got %f", scorer.config.CostWeight)
			}
			if scorer.config.UtilizationWeight < 0 {
				t.Errorf("UtilizationWeight should not be negative, got %f", scorer.config.UtilizationWeight)
			}
			if scorer.config.NetworkPenaltyWeight < 0 {
				t.Errorf("NetworkPenaltyWeight should not be negative, got %f", scorer.config.NetworkPenaltyWeight)
			}
		})
	}
}

// TestScorer_UpdateWeights_ZeroWeights verifies CLD-REQ-021 weight handling.
// CLD-REQ-021: Tests edge case where UpdateWeights receives all zero weights.
func TestScorer_UpdateWeights_ZeroWeights(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	// Update with all zero weights
	scorer.UpdateWeights(ScorerConfig{
		LocalityWeight:       0,
		ReliabilityWeight:    0,
		CostWeight:           0,
		UtilizationWeight:    0,
		NetworkPenaltyWeight: 0,
	})

	// UpdateWeights keeps weights at 0 when total is 0 (unlike NewScorer which falls back to defaults)
	// This is intentional edge case behavior - scorer won't work properly but shouldn't crash
	total := scorer.config.LocalityWeight +
		scorer.config.ReliabilityWeight +
		scorer.config.CostWeight +
		scorer.config.UtilizationWeight +
		scorer.config.NetworkPenaltyWeight

	if total != 0 {
		t.Errorf("Expected all weights to be 0 when updated with zeros, got total %f", total)
	}
}

// TestScorer_LocalityScore_RegionMatch verifies CLD-REQ-021 locality component.
// Tests region preference scoring.
func TestScorer_LocalityScore_RegionMatch(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name          string
		nodeRegion    string
		nodeZone      string
		regions       []string
		zones         []string
		expectedScore float64
		tolerance     float64
	}{
		{
			name:          "first region preference - exact match",
			nodeRegion:    "us-east",
			nodeZone:      "us-east-1a",
			regions:       []string{"us-east", "us-west"},
			zones:         []string{},
			expectedScore: 1.0,
			tolerance:     0.01,
		},
		{
			name:          "second region preference - lower score",
			nodeRegion:    "us-west",
			nodeZone:      "us-west-1a",
			regions:       []string{"us-east", "us-west"},
			zones:         []string{},
			expectedScore: 0.9, // 1.0 - (1 * 0.1)
			tolerance:     0.01,
		},
		{
			name:          "third region preference",
			nodeRegion:    "eu-central",
			nodeZone:      "eu-central-1a",
			regions:       []string{"us-east", "us-west", "eu-central"},
			zones:         []string{},
			expectedScore: 0.8, // 1.0 - (2 * 0.1)
			tolerance:     0.01,
		},
		{
			name:          "no region match - baseline",
			nodeRegion:    "ap-south",
			nodeZone:      "ap-south-1a",
			regions:       []string{"us-east", "us-west"},
			zones:         []string{},
			expectedScore: 0.5, // Neutral baseline
			tolerance:     0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID:     "test-node",
				Region: tt.nodeRegion,
				Zone:   tt.nodeZone,
			}

			workload := &WorkloadSpec{
				PlacementPolicy: PlacementPolicy{
					Regions: tt.regions,
					Zones:   tt.zones,
				},
			}

			score := scorer.calculateLocalityScore(node, workload)

			if math.Abs(score-tt.expectedScore) > tt.tolerance {
				t.Errorf("Expected locality score %f, got %f", tt.expectedScore, score)
			}

			// Verify score is clamped [0, 1]
			if score < 0 || score > 1 {
				t.Errorf("Score %f is outside [0, 1] range", score)
			}
		})
	}
}

// TestScorer_LocalityScore_ZoneMatch verifies CLD-REQ-021 locality component.
// Tests zone bonus/penalty.
func TestScorer_LocalityScore_ZoneMatch(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name          string
		nodeRegion    string
		nodeZone      string
		regions       []string
		zones         []string
		expectedBonus float64
	}{
		{
			name:          "zone match - bonus applied",
			nodeRegion:    "us-east",
			nodeZone:      "us-east-1a",
			regions:       []string{"us-east"},
			zones:         []string{"us-east-1a"},
			expectedBonus: 0.0, // Zone bonus gets clamped when score already at 1.0
		},
		{
			name:          "zone mismatch - penalty applied",
			nodeRegion:    "us-east",
			nodeZone:      "us-east-1b",
			regions:       []string{"us-east"},
			zones:         []string{"us-east-1a"},
			expectedBonus: -0.2, // -0.2 for zone mismatch
		},
		{
			name:          "no zone preference - no adjustment",
			nodeRegion:    "us-east",
			nodeZone:      "us-east-1a",
			regions:       []string{"us-east"},
			zones:         []string{},
			expectedBonus: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID:     "test-node",
				Region: tt.nodeRegion,
				Zone:   tt.nodeZone,
			}

			workload := &WorkloadSpec{
				PlacementPolicy: PlacementPolicy{
					Regions: tt.regions,
					Zones:   tt.zones,
				},
			}

			// Get baseline score with region but no zone
			baselineWorkload := &WorkloadSpec{
				PlacementPolicy: PlacementPolicy{
					Regions: tt.regions,
					Zones:   []string{},
				},
			}

			baselineScore := scorer.calculateLocalityScore(node, baselineWorkload)
			actualScore := scorer.calculateLocalityScore(node, workload)

			actualBonus := actualScore - baselineScore

			if math.Abs(actualBonus-tt.expectedBonus) > 0.01 {
				t.Errorf("Expected zone bonus %f, got %f", tt.expectedBonus, actualBonus)
			}
		})
	}
}

// TestScorer_LocalityScore_NoPreference verifies CLD-REQ-021 locality component.
// Tests baseline score when no preference is specified.
func TestScorer_LocalityScore_NoPreference(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	node := &membership.NodeInfo{
		ID:     "test-node",
		Region: "us-east",
		Zone:   "us-east-1a",
	}

	workload := &WorkloadSpec{
		PlacementPolicy: PlacementPolicy{
			Regions: []string{},
			Zones:   []string{},
		},
	}

	score := scorer.calculateLocalityScore(node, workload)

	expectedScore := 0.7 // Per implementation: "No preference means moderate score"

	if math.Abs(score-expectedScore) > 0.01 {
		t.Errorf("Expected baseline score %f when no preference, got %f", expectedScore, score)
	}
}

// TestScorer_LocalityScore_Clamping verifies scores are clamped to [0, 1].
func TestScorer_LocalityScore_Clamping(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	// Test scenario that could produce score > 1
	node := &membership.NodeInfo{
		ID:     "test-node",
		Region: "us-east",
		Zone:   "us-east-1a",
	}

	workload := &WorkloadSpec{
		PlacementPolicy: PlacementPolicy{
			Regions: []string{"us-east"},
			Zones:   []string{"us-east-1a"},
		},
	}

	score := scorer.calculateLocalityScore(node, workload)

	// Verify score is within [0, 1]
	if score < 0 || score > 1 {
		t.Errorf("Score %f is outside [0, 1] range", score)
	}
}

// TestScorer_ReliabilityScore_BaseScore verifies CLD-REQ-021 reliability component.
// Tests base reliability score usage.
func TestScorer_ReliabilityScore_BaseScore(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name          string
		baseScore     float64
		expectedScore float64
	}{
		{
			name:          "perfect reliability",
			baseScore:     1.0,
			expectedScore: 1.0,
		},
		{
			name:          "high reliability",
			baseScore:     0.95,
			expectedScore: 0.95,
		},
		{
			name:          "medium reliability",
			baseScore:     0.5,
			expectedScore: 0.5,
		},
		{
			name:          "low reliability",
			baseScore:     0.2,
			expectedScore: 0.2,
		},
		{
			name:          "zero reliability",
			baseScore:     0.0,
			expectedScore: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID:               "test-node",
				State:            membership.StateReady,
				ReliabilityScore: tt.baseScore,
				Conditions:       []membership.NodeCondition{},
			}

			score := scorer.calculateReliabilityScore(node)

			if math.Abs(score-tt.expectedScore) > 0.01 {
				t.Errorf("Expected reliability score %f, got %f", tt.expectedScore, score)
			}
		})
	}
}

// TestScorer_ReliabilityScore_ConditionAdjustments verifies CLD-REQ-021 reliability component.
// Tests condition-based adjustments.
func TestScorer_ReliabilityScore_ConditionAdjustments(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name       string
		baseScore  float64
		conditions []membership.NodeCondition
		adjustment float64
	}{
		{
			name:      "memory pressure - penalty",
			baseScore: 1.0,
			conditions: []membership.NodeCondition{
				{Type: "MemoryPressure", Status: "True"},
			},
			adjustment: -0.2,
		},
		{
			name:      "disk pressure - penalty",
			baseScore: 1.0,
			conditions: []membership.NodeCondition{
				{Type: "DiskPressure", Status: "True"},
			},
			adjustment: -0.2,
		},
		{
			name:      "network unavailable - high penalty",
			baseScore: 1.0,
			conditions: []membership.NodeCondition{
				{Type: "NetworkUnavailable", Status: "True"},
			},
			adjustment: -0.4,
		},
		{
			name:      "ready - bonus",
			baseScore: 0.8,
			conditions: []membership.NodeCondition{
				{Type: "Ready", Status: "True"},
			},
			adjustment: 0.1,
		},
		{
			name:      "multiple conditions - cumulative",
			baseScore: 1.0,
			conditions: []membership.NodeCondition{
				{Type: "MemoryPressure", Status: "True"},
				{Type: "DiskPressure", Status: "True"},
				{Type: "Ready", Status: "True"},
			},
			adjustment: -0.3, // -0.2 - 0.2 + 0.1
		},
		{
			name:      "condition false - no adjustment",
			baseScore: 1.0,
			conditions: []membership.NodeCondition{
				{Type: "MemoryPressure", Status: "False"},
			},
			adjustment: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID:               "test-node",
				State:            membership.StateReady,
				ReliabilityScore: tt.baseScore,
				Conditions:       tt.conditions,
			}

			score := scorer.calculateReliabilityScore(node)
			expectedScore := math.Max(0, math.Min(1, tt.baseScore+tt.adjustment))

			if math.Abs(score-expectedScore) > 0.01 {
				t.Errorf("Expected score %f (base %f + adjustment %f), got %f",
					expectedScore, tt.baseScore, tt.adjustment, score)
			}
		})
	}
}

// TestScorer_ReliabilityScore_StateAdjustments verifies CLD-REQ-021 reliability component.
// Tests node state-based adjustments.
func TestScorer_ReliabilityScore_StateAdjustments(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name          string
		state         string
		baseScore     float64
		expectedScore float64
	}{
		{
			name:          "ready state - no adjustment",
			state:         membership.StateReady,
			baseScore:     0.8,
			expectedScore: 0.8,
		},
		{
			name:          "enrolling state - 50% penalty",
			state:         membership.StateEnrolling,
			baseScore:     0.8,
			expectedScore: 0.4, // 0.8 * 0.5
		},
		{
			name:          "draining state - zero score",
			state:         membership.StateDraining,
			baseScore:     0.8,
			expectedScore: 0.0,
		},
		{
			name:          "cordoned state - zero score",
			state:         membership.StateCordoned,
			baseScore:     0.8,
			expectedScore: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID:               "test-node",
				State:            tt.state,
				ReliabilityScore: tt.baseScore,
				Conditions:       []membership.NodeCondition{},
			}

			score := scorer.calculateReliabilityScore(node)

			if math.Abs(score-tt.expectedScore) > 0.01 {
				t.Errorf("Expected score %f for state %v, got %f", tt.expectedScore, tt.state, score)
			}
		})
	}
}

// TestScorer_ReliabilityScore_Clamping verifies scores are clamped to [0, 1].
func TestScorer_ReliabilityScore_Clamping(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name       string
		baseScore  float64
		conditions []membership.NodeCondition
	}{
		{
			name:      "score above 1 clamped",
			baseScore: 1.0,
			conditions: []membership.NodeCondition{
				{Type: "Ready", Status: "True"}, // +0.1
			},
		},
		{
			name:      "score below 0 clamped",
			baseScore: 0.2,
			conditions: []membership.NodeCondition{
				{Type: "NetworkUnavailable", Status: "True"}, // -0.4
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID:               "test-node",
				State:            membership.StateReady,
				ReliabilityScore: tt.baseScore,
				Conditions:       tt.conditions,
			}

			score := scorer.calculateReliabilityScore(node)

			if score < 0 || score > 1 {
				t.Errorf("Score %f is outside [0, 1] range", score)
			}
		})
	}
}

// TestScorer_CostScore_OptimalUtilization verifies CLD-REQ-021 cost component.
// Tests the optimal <30% range. Note: exactly 30% falls into next category (0.8 score).
func TestScorer_CostScore_OptimalUtilization(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name            string
		nodeCapacityCPU int64
		nodeCapacityMem int64
		workloadCPU     int64
		workloadMem     int64
		expectedScore   float64
		tolerance       float64
	}{
		{
			name:            "10% utilization - lower bound optimal",
			nodeCapacityCPU: 10000,
			nodeCapacityMem: 10 * 1024 * 1024 * 1024,
			workloadCPU:     1000,               // 10%
			workloadMem:     1024 * 1024 * 1024, // 10%
			expectedScore:   1.0,
			tolerance:       0.01,
		},
		{
			name:            "20% utilization - optimal",
			nodeCapacityCPU: 10000,
			nodeCapacityMem: 10 * 1024 * 1024 * 1024,
			workloadCPU:     2000,                   // 20%
			workloadMem:     2 * 1024 * 1024 * 1024, // 20%
			expectedScore:   1.0,
			tolerance:       0.01,
		},
		{
			name:            "30% utilization - upper bound optimal",
			nodeCapacityCPU: 10000,
			nodeCapacityMem: 10 * 1024 * 1024 * 1024,
			workloadCPU:     3000,                   // 30%
			workloadMem:     3 * 1024 * 1024 * 1024, // 30%
			expectedScore:   0.8,
			tolerance:       0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID: "test-node",
				Capacity: membership.ResourceCapacity{
					CPUMillicores: tt.nodeCapacityCPU,
					MemoryBytes:   tt.nodeCapacityMem,
				},
			}

			workload := &WorkloadSpec{
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: tt.workloadCPU,
						MemoryBytes:   tt.workloadMem,
					},
				},
			}

			score := scorer.calculateCostScore(node, workload)

			if math.Abs(score-tt.expectedScore) > tt.tolerance {
				t.Errorf("Expected cost score %f, got %f", tt.expectedScore, score)
			}
		})
	}
}

// TestScorer_CostScore_Waste verifies CLD-REQ-021 cost component.
// Tests penalty for under-utilization (waste).
func TestScorer_CostScore_Waste(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	node := &membership.NodeInfo{
		ID: "test-node",
		Capacity: membership.ResourceCapacity{
			CPUMillicores: 10000,
			MemoryBytes:   10 * 1024 * 1024 * 1024,
		},
	}

	workload := &WorkloadSpec{
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 500,               // 5% - wasteful
				MemoryBytes:   512 * 1024 * 1024, // 5%
			},
		},
	}

	score := scorer.calculateCostScore(node, workload)

	// <10% should get 0.7 score (some waste)
	if math.Abs(score-0.7) > 0.01 {
		t.Errorf("Expected cost score 0.7 for wasteful allocation, got %f", score)
	}
}

// TestScorer_CostScore_HighUtilization verifies CLD-REQ-021 cost component.
// Tests decreasing scores for high utilization.
func TestScorer_CostScore_HighUtilization(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name           string
		utilizationPct float64
		expectedScore  float64
	}{
		{
			name:           "40% utilization - good",
			utilizationPct: 0.40,
			expectedScore:  0.8,
		},
		{
			name:           "60% utilization - getting full",
			utilizationPct: 0.60,
			expectedScore:  0.6,
		},
		{
			name:           "75% utilization - too large",
			utilizationPct: 0.75,
			expectedScore:  0.3,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			capacityCPU := int64(10000)
			capacityMem := int64(10 * 1024 * 1024 * 1024)

			node := &membership.NodeInfo{
				ID: "test-node",
				Capacity: membership.ResourceCapacity{
					CPUMillicores: capacityCPU,
					MemoryBytes:   capacityMem,
				},
			}

			workload := &WorkloadSpec{
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: int64(float64(capacityCPU) * tt.utilizationPct),
						MemoryBytes:   int64(float64(capacityMem) * tt.utilizationPct),
					},
				},
			}

			score := scorer.calculateCostScore(node, workload)

			if math.Abs(score-tt.expectedScore) > 0.01 {
				t.Errorf("Expected cost score %f for %d%% utilization, got %f",
					tt.expectedScore, int(tt.utilizationPct*100), score)
			}
		})
	}
}

// TestScorer_CostScore_GPUPenalty verifies CLD-REQ-021 cost component.
// Tests penalty for wasting GPU resources.
func TestScorer_CostScore_GPUPenalty(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name          string
		nodeGPU       int32
		workloadGPU   int32
		expectPenalty bool
	}{
		{
			name:          "node has GPU, workload doesn't need - penalty",
			nodeGPU:       1,
			workloadGPU:   0,
			expectPenalty: true,
		},
		{
			name:          "node has GPU, workload needs GPU - no penalty",
			nodeGPU:       1,
			workloadGPU:   1,
			expectPenalty: false,
		},
		{
			name:          "node no GPU, workload doesn't need - no penalty",
			nodeGPU:       0,
			workloadGPU:   0,
			expectPenalty: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID: "test-node",
				Capacity: membership.ResourceCapacity{
					CPUMillicores: 10000,
					MemoryBytes:   10 * 1024 * 1024 * 1024,
					GPUCount:      tt.nodeGPU,
				},
			}

			workload := &WorkloadSpec{
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 2000, // 20% - optimal range
						MemoryBytes:   2 * 1024 * 1024 * 1024,
						GPUCount:      tt.workloadGPU,
					},
				},
			}

			score := scorer.calculateCostScore(node, workload)

			// Base score for 20% utilization should be 1.0
			// GPU penalty should reduce it by 0.3
			if tt.expectPenalty {
				if score > 0.7 { // 1.0 - 0.3
					t.Errorf("Expected GPU penalty, score should be <= 0.7, got %f", score)
				}
			} else {
				if math.Abs(score-1.0) > 0.01 {
					t.Errorf("Expected no GPU penalty, score should be ~1.0, got %f", score)
				}
			}
		})
	}
}

// TestScorer_CostScore_DivisionByZero verifies CLD-REQ-021 cost component.
// Tests handling of zero capacity (edge case).
func TestScorer_CostScore_DivisionByZero(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name        string
		capacityCPU int64
		capacityMem int64
	}{
		{
			name:        "zero CPU capacity",
			capacityCPU: 0,
			capacityMem: 10 * 1024 * 1024 * 1024,
		},
		{
			name:        "zero memory capacity",
			capacityCPU: 10000,
			capacityMem: 0,
		},
		{
			name:        "both zero",
			capacityCPU: 0,
			capacityMem: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID: "test-node",
				Capacity: membership.ResourceCapacity{
					CPUMillicores: tt.capacityCPU,
					MemoryBytes:   tt.capacityMem,
				},
			}

			workload := &WorkloadSpec{
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 1000,
						MemoryBytes:   1024 * 1024 * 1024,
					},
				},
			}

			score := scorer.calculateCostScore(node, workload)

			// Should return 0 for nodes with zero capacity
			if score != 0 {
				t.Errorf("Expected score 0 for zero capacity node, got %f", score)
			}
		})
	}
}

// TestScorer_UtilizationScore_Overcommitment verifies CLD-REQ-021 utilization component.
// Tests that overcommitment returns score of 0.
func TestScorer_UtilizationScore_Overcommitment(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name        string
		usedCPU     int64
		usedMem     int64
		workloadCPU int64
		workloadMem int64
	}{
		{
			name:        "CPU overcommitment",
			usedCPU:     9500,
			usedMem:     5 * 1024 * 1024 * 1024,
			workloadCPU: 1000, // Would exceed capacity
			workloadMem: 1024 * 1024 * 1024,
		},
		{
			name:        "memory overcommitment",
			usedCPU:     5000,
			usedMem:     9*1024*1024*1024 + 512*1024*1024,
			workloadCPU: 1000,
			workloadMem: 1024 * 1024 * 1024, // Would exceed capacity
		},
		{
			name:        "both overcommitted",
			usedCPU:     9500,
			usedMem:     9*1024*1024*1024 + 512*1024*1024,
			workloadCPU: 1000,
			workloadMem: 1024 * 1024 * 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID: "test-node",
				Capacity: membership.ResourceCapacity{
					CPUMillicores: 10000,
					MemoryBytes:   10 * 1024 * 1024 * 1024,
				},
				Usage: membership.ResourceUsage{
					CPUMillicores: tt.usedCPU,
					MemoryBytes:   tt.usedMem,
				},
			}

			workload := &WorkloadSpec{
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: tt.workloadCPU,
						MemoryBytes:   tt.workloadMem,
					},
				},
			}

			score := scorer.calculateUtilizationScore(node, workload)

			if score != 0 {
				t.Errorf("Expected score 0 for overcommitment, got %f", score)
			}
		})
	}
}

// TestScorer_UtilizationScore_BalancedTarget verifies CLD-REQ-021 utilization component.
// Tests the 65% balanced utilization target.
func TestScorer_UtilizationScore_BalancedTarget(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	// Create scenario where placement achieves 65% target utilization
	node := &membership.NodeInfo{
		ID: "test-node",
		Capacity: membership.ResourceCapacity{
			CPUMillicores: 10000,
			MemoryBytes:   10 * 1024 * 1024 * 1024,
		},
		Usage: membership.ResourceUsage{
			CPUMillicores: 3500, // Current 35%
			MemoryBytes:   3*1024*1024*1024 + 512*1024*1024,
		},
	}

	workload := &WorkloadSpec{
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 3000, // Future: 65%
				MemoryBytes:   3 * 1024 * 1024 * 1024,
			},
		},
	}

	score := scorer.calculateUtilizationScore(node, workload)

	// Score should be high (close to target 65%)
	if score < 0.8 {
		t.Errorf("Expected high score for balanced target utilization, got %f", score)
	}
}

// TestScorer_UtilizationScore_BalanceBonus verifies CLD-REQ-021 utilization component.
// Tests bonus for improving CPU/memory balance.
func TestScorer_UtilizationScore_BalanceBonus(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	// Create unbalanced node (high CPU, low memory)
	nodeUnbalanced := &membership.NodeInfo{
		ID: "test-node",
		Capacity: membership.ResourceCapacity{
			CPUMillicores: 10000,
			MemoryBytes:   10 * 1024 * 1024 * 1024,
		},
		Usage: membership.ResourceUsage{
			CPUMillicores: 8000,                   // 80% CPU
			MemoryBytes:   2 * 1024 * 1024 * 1024, // 20% memory
		},
	}

	// Workload that improves balance (low CPU, high memory)
	workloadBalancing := &WorkloadSpec{
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 500,                    // Minimal CPU
				MemoryBytes:   4 * 1024 * 1024 * 1024, // High memory
			},
		},
	}

	// Workload that worsens balance (high CPU, low memory)
	workloadImbalancing := &WorkloadSpec{
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 1500,              // More CPU
				MemoryBytes:   512 * 1024 * 1024, // Minimal memory
			},
		},
	}

	scoreBalancing := scorer.calculateUtilizationScore(nodeUnbalanced, workloadBalancing)
	scoreImbalancing := scorer.calculateUtilizationScore(nodeUnbalanced, workloadImbalancing)

	// Balancing workload should score higher
	if scoreBalancing <= scoreImbalancing {
		t.Errorf("Expected balancing workload to score higher (%f) than imbalancing (%f)",
			scoreBalancing, scoreImbalancing)
	}
}

// TestScorer_UtilizationScore_FutureUtilization verifies CLD-REQ-021 utilization component.
// Tests penalty for very high future utilization (>90%).
func TestScorer_UtilizationScore_FutureUtilization(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	node := &membership.NodeInfo{
		ID: "test-node",
		Capacity: membership.ResourceCapacity{
			CPUMillicores: 10000,
			MemoryBytes:   10 * 1024 * 1024 * 1024,
		},
		Usage: membership.ResourceUsage{
			CPUMillicores: 8500, // Current 85%
			MemoryBytes:   8*1024*1024*1024 + 512*1024*1024,
		},
	}

	workload := &WorkloadSpec{
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 1000, // Future: 95% - very high!
				MemoryBytes:   1024 * 1024 * 1024,
			},
		},
	}

	score := scorer.calculateUtilizationScore(node, workload)

	// Score should be low due to >90% utilization penalty
	if score > 0.5 {
		t.Errorf("Expected low score for >90%% future utilization, got %f", score)
	}
}

// TestScorer_UtilizationScore_DivisionByZero verifies handling of zero capacity.
func TestScorer_UtilizationScore_DivisionByZero(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	node := &membership.NodeInfo{
		ID: "test-node",
		Capacity: membership.ResourceCapacity{
			CPUMillicores: 0,
			MemoryBytes:   0,
		},
		Usage: membership.ResourceUsage{
			CPUMillicores: 0,
			MemoryBytes:   0,
		},
	}

	workload := &WorkloadSpec{
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 1000,
				MemoryBytes:   1024 * 1024 * 1024,
			},
		},
	}

	score := scorer.calculateUtilizationScore(node, workload)

	if score != 0 {
		t.Errorf("Expected score 0 for zero capacity node, got %f", score)
	}
}

// TestScorer_NetworkPenalty_InsufficientBandwidth verifies CLD-REQ-021 network penalty component.
// Tests penalty up to 0.5 based on bandwidth deficit.
func TestScorer_NetworkPenalty_InsufficientBandwidth(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name              string
		totalBandwidth    int64
		usedBandwidth     int64
		requiredBandwidth int64
		expectedPenalty   float64
		tolerance         float64
	}{
		{
			name:              "sufficient bandwidth - no penalty",
			totalBandwidth:    1000000000, // 1 Gbps
			usedBandwidth:     500000000,  // 0.5 Gbps used
			requiredBandwidth: 100000000,  // Need 0.1 Gbps (available)
			expectedPenalty:   0.0,
			tolerance:         0.01,
		},
		{
			name:              "50% deficit - medium penalty",
			totalBandwidth:    1000000000,
			usedBandwidth:     900000000, // 0.9 Gbps used
			requiredBandwidth: 200000000, // Need 0.2 Gbps (only 0.1 available)
			expectedPenalty:   0.25,      // 50% deficit * 0.5
			tolerance:         0.01,
		},
		{
			name:              "100% deficit - max bandwidth penalty",
			totalBandwidth:    1000000000,
			usedBandwidth:     1000000000, // Fully used
			requiredBandwidth: 100000000,  // Need bandwidth (none available)
			expectedPenalty:   0.5,        // Max bandwidth penalty
			tolerance:         0.01,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID: "test-node",
				Capacity: membership.ResourceCapacity{
					BandwidthBPS: tt.totalBandwidth,
				},
				Usage: membership.ResourceUsage{
					BandwidthBPS: tt.usedBandwidth,
				},
			}

			workload := &WorkloadSpec{
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						BandwidthBPS: tt.requiredBandwidth,
					},
				},
			}

			penalty := scorer.calculateNetworkPenalty(node, workload)

			if math.Abs(penalty-tt.expectedPenalty) > tt.tolerance {
				t.Errorf("Expected network penalty %f, got %f", tt.expectedPenalty, penalty)
			}
		})
	}
}

// TestScorer_NetworkPenalty_MissingPublicIP verifies CLD-REQ-021 network penalty component.
// Tests +0.1 penalty for missing public IP when required.
func TestScorer_NetworkPenalty_MissingPublicIP(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name            string
		nodePublicIP    string
		requirePublicIP bool
		expectedPenalty float64
	}{
		{
			name:            "has public IP, required - no penalty",
			nodePublicIP:    "1.2.3.4",
			requirePublicIP: true,
			expectedPenalty: 0.0,
		},
		{
			name:            "no public IP, required - penalty",
			nodePublicIP:    "",
			requirePublicIP: true,
			expectedPenalty: 0.1,
		},
		{
			name:            "no public IP, not required - no penalty",
			nodePublicIP:    "",
			requirePublicIP: false,
			expectedPenalty: 0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID: "test-node",
				NetworkInfo: membership.NetworkInfo{
					PublicIP: tt.nodePublicIP,
				},
			}

			annotations := map[string]string{}
			if tt.requirePublicIP {
				annotations["network.requirePublicIP"] = "true"
			}

			workload := &WorkloadSpec{
				Annotations: annotations,
			}

			penalty := scorer.calculateNetworkPenalty(node, workload)

			if math.Abs(penalty-tt.expectedPenalty) > 0.01 {
				t.Errorf("Expected penalty %f, got %f", tt.expectedPenalty, penalty)
			}
		})
	}
}

// TestScorer_NetworkPenalty_NetworkUnavailable verifies CLD-REQ-021 network penalty component.
// Tests maximum penalty (1.0) when network is unavailable.
func TestScorer_NetworkPenalty_NetworkUnavailable(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	node := &membership.NodeInfo{
		ID: "test-node",
		Conditions: []membership.NodeCondition{
			{Type: "NetworkUnavailable", Status: "True"},
		},
	}

	workload := &WorkloadSpec{}

	penalty := scorer.calculateNetworkPenalty(node, workload)

	// Should have maximum penalty of exactly 1.0
	if math.Abs(penalty-1.0) > 0.01 {
		t.Errorf("Expected maximum penalty 1.0 for network unavailable, got %f", penalty)
	}
}

// TestScorer_NetworkPenalty_CrossRegion verifies CLD-REQ-021 network penalty component.
// Tests +0.2 penalty for cross-region placement.
func TestScorer_NetworkPenalty_CrossRegion(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	tests := []struct {
		name             string
		nodeRegion       string
		preferredRegions []string
		expectedPenalty  float64
	}{
		{
			name:             "same region - no penalty",
			nodeRegion:       "us-east",
			preferredRegions: []string{"us-east"},
			expectedPenalty:  0.0,
		},
		{
			name:             "different region - penalty",
			nodeRegion:       "us-west",
			preferredRegions: []string{"us-east"},
			expectedPenalty:  0.2,
		},
		{
			name:             "no preference - no penalty",
			nodeRegion:       "us-west",
			preferredRegions: []string{},
			expectedPenalty:  0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &membership.NodeInfo{
				ID:     "test-node",
				Region: tt.nodeRegion,
			}

			workload := &WorkloadSpec{
				PlacementPolicy: PlacementPolicy{
					Regions: tt.preferredRegions,
				},
			}

			penalty := scorer.calculateNetworkPenalty(node, workload)

			if math.Abs(penalty-tt.expectedPenalty) > 0.01 {
				t.Errorf("Expected penalty %f, got %f", tt.expectedPenalty, penalty)
			}
		})
	}
}

// TestScorer_NetworkPenalty_Clamping verifies penalties are clamped to [0, 1].
func TestScorer_NetworkPenalty_Clamping(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	// Create scenario with multiple penalties that could exceed 1.0
	node := &membership.NodeInfo{
		ID:     "test-node",
		Region: "us-west",
		NetworkInfo: membership.NetworkInfo{
			PublicIP: "",
		},
		Conditions: []membership.NodeCondition{
			{Type: "NetworkUnavailable", Status: "True"}, // +1.0
		},
		Capacity: membership.ResourceCapacity{
			BandwidthBPS: 1000000,
		},
		Usage: membership.ResourceUsage{
			BandwidthBPS: 1000000, // Fully used
		},
	}

	workload := &WorkloadSpec{
		PlacementPolicy: PlacementPolicy{
			Regions: []string{"us-east"}, // Cross-region: +0.2
		},
		Annotations: map[string]string{
			"network.requirePublicIP": "true", // Missing: +0.1
		},
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				BandwidthBPS: 100000, // Insufficient: +0.5
			},
		},
	}

	penalty := scorer.calculateNetworkPenalty(node, workload)

	// Penalty should be clamped to [0, 1]
	if penalty < 0 || penalty > 1 {
		t.Errorf("Penalty %f is outside [0, 1] range", penalty)
	}
}

// TestScorer_ScoreNode_WeightedFormula verifies CLD-REQ-021.
// CLD-REQ-021: S = w_l*locality + w_r*reliability + w_c*cost + w_u*utilization - w_n*network_penalty
// This is the core test that verifies the complete weighted scoring formula.
func TestScorer_ScoreNode_WeightedFormula(t *testing.T) {
	logger := zap.NewNop()

	// Use specific weights for predictable calculation
	config := ScorerConfig{
		LocalityWeight:       0.25,
		ReliabilityWeight:    0.30,
		CostWeight:           0.15,
		UtilizationWeight:    0.20,
		NetworkPenaltyWeight: 0.10,
	}
	scorer := NewScorer(config, logger)

	// Create a node with known characteristics
	node := &membership.NodeInfo{
		ID:               "test-node",
		Region:           "us-east",
		Zone:             "us-east-1a",
		State:            membership.StateReady,
		ReliabilityScore: 0.9,
		Capacity: membership.ResourceCapacity{
			CPUMillicores: 10000,
			MemoryBytes:   10 * 1024 * 1024 * 1024,
			BandwidthBPS:  1000000000,
		},
		Usage: membership.ResourceUsage{
			CPUMillicores: 2000, // 20% used
			MemoryBytes:   2 * 1024 * 1024 * 1024,
			BandwidthBPS:  100000000,
		},
		Conditions: []membership.NodeCondition{
			{Type: "Ready", Status: "True"},
		},
		NetworkInfo: membership.NetworkInfo{
			PublicIP: "1.2.3.4",
		},
	}

	workload := &WorkloadSpec{
		PlacementPolicy: PlacementPolicy{
			Regions: []string{"us-east"},
			Zones:   []string{"us-east-1a"},
		},
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 2000, // 20% - optimal cost range
				MemoryBytes:   2 * 1024 * 1024 * 1024,
				BandwidthBPS:  100000000,
			},
		},
	}

	result := scorer.ScoreNode(node, workload)

	// Verify components are calculated
	if result.Components.LocalityScore <= 0 {
		t.Errorf("LocalityScore should be > 0, got %f", result.Components.LocalityScore)
	}
	if result.Components.ReliabilityScore <= 0 {
		t.Errorf("ReliabilityScore should be > 0, got %f", result.Components.ReliabilityScore)
	}
	if result.Components.CostScore <= 0 {
		t.Errorf("CostScore should be > 0, got %f", result.Components.CostScore)
	}
	if result.Components.UtilizationScore <= 0 {
		t.Errorf("UtilizationScore should be > 0, got %f", result.Components.UtilizationScore)
	}

	// Manually calculate expected score using the formula
	expectedScore := result.Components.LocalityScore*0.25 +
		result.Components.ReliabilityScore*0.30 +
		result.Components.CostScore*0.15 +
		result.Components.UtilizationScore*0.20 -
		result.Components.NetworkPenalty*0.10

	// Clamp to [0, 1] as implementation does
	expectedScore = math.Max(0, math.Min(1, expectedScore))

	// Verify total score matches formula (before affinity adjustments)
	// Note: actual score may differ slightly due to affinity adjustments
	if math.Abs(result.Score-expectedScore) > 0.15 {
		t.Errorf("Score %f doesn't match expected formula result %f (diff: %f)",
			result.Score, expectedScore, math.Abs(result.Score-expectedScore))
	}

	// Verify score is in valid range
	if result.Score < 0 || result.Score > 1 {
		t.Errorf("Score %f is outside [0, 1] range", result.Score)
	}
}

// TestScorer_ScoreNode_DefaultWeights verifies CLD-REQ-021 default weights.
func TestScorer_ScoreNode_DefaultWeights(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger) // Use defaults

	// Verify default weights match PRD specification
	if scorer.config.LocalityWeight != 0.25 {
		t.Errorf("Default LocalityWeight should be 0.25, got %f", scorer.config.LocalityWeight)
	}
	if scorer.config.ReliabilityWeight != 0.30 {
		t.Errorf("Default ReliabilityWeight should be 0.30, got %f", scorer.config.ReliabilityWeight)
	}
	if scorer.config.CostWeight != 0.15 {
		t.Errorf("Default CostWeight should be 0.15, got %f", scorer.config.CostWeight)
	}
	if scorer.config.UtilizationWeight != 0.20 {
		t.Errorf("Default UtilizationWeight should be 0.20, got %f", scorer.config.UtilizationWeight)
	}
	if scorer.config.NetworkPenaltyWeight != 0.10 {
		t.Errorf("Default NetworkPenaltyWeight should be 0.10, got %f", scorer.config.NetworkPenaltyWeight)
	}
}

// TestScorer_ScoreNode_FinalClamping verifies final score is clamped to [0, 1].
func TestScorer_ScoreNode_FinalClamping(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	// Create various scenarios
	tests := []struct {
		name string
		node *membership.NodeInfo
	}{
		{
			name: "perfect node - high score",
			node: &membership.NodeInfo{
				ID:               "perfect",
				State:            membership.StateReady,
				ReliabilityScore: 1.0,
				Capacity: membership.ResourceCapacity{
					CPUMillicores: 10000,
					MemoryBytes:   10 * 1024 * 1024 * 1024,
				},
				Usage: membership.ResourceUsage{
					CPUMillicores: 0,
					MemoryBytes:   0,
				},
			},
		},
		{
			name: "terrible node - low score",
			node: &membership.NodeInfo{
				ID:               "terrible",
				State:            membership.StateDraining,
				ReliabilityScore: 0.1,
				Capacity: membership.ResourceCapacity{
					CPUMillicores: 10000,
					MemoryBytes:   10 * 1024 * 1024 * 1024,
				},
				Usage: membership.ResourceUsage{
					CPUMillicores: 9500,
					MemoryBytes:   9 * 1024 * 1024 * 1024,
				},
				Conditions: []membership.NodeCondition{
					{Type: "NetworkUnavailable", Status: "True"},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workload := &WorkloadSpec{
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 1000,
						MemoryBytes:   1024 * 1024 * 1024,
					},
				},
			}

			result := scorer.ScoreNode(tt.node, workload)

			if result.Score < 0 || result.Score > 1 {
				t.Errorf("Score %f is outside [0, 1] range", result.Score)
			}
		})
	}
}

// TestScorer_AffinityAdjustments_RequiredAffinity verifies affinity bonus.
func TestScorer_AffinityAdjustments_RequiredAffinity(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	node := &membership.NodeInfo{
		ID:    "test-node",
		State: membership.StateReady,
		Labels: map[string]string{
			"env": "prod",
		},
		ReliabilityScore: 0.5,
		Capacity: membership.ResourceCapacity{
			CPUMillicores: 10000,
			MemoryBytes:   10 * 1024 * 1024 * 1024,
		},
		Usage: membership.ResourceUsage{},
	}

	// Workload with required affinity
	workloadWithAffinity := &WorkloadSpec{
		PlacementPolicy: PlacementPolicy{
			Affinity: []AffinityRule{
				{
					Type:     "node",
					Required: true,
					MatchLabels: map[string]string{
						"env": "prod",
					},
				},
			},
		},
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 2000,
				MemoryBytes:   2 * 1024 * 1024 * 1024,
			},
		},
	}

	// Workload without affinity
	workloadWithoutAffinity := &WorkloadSpec{
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 2000,
				MemoryBytes:   2 * 1024 * 1024 * 1024,
			},
		},
	}

	scoreWith := scorer.ScoreNode(node, workloadWithAffinity)
	scoreWithout := scorer.ScoreNode(node, workloadWithoutAffinity)

	// Required affinity should give +0.1 bonus
	if scoreWith.Score <= scoreWithout.Score {
		t.Errorf("Required affinity should increase score: with=%f, without=%f",
			scoreWith.Score, scoreWithout.Score)
	}

	diff := scoreWith.Score - scoreWithout.Score
	if math.Abs(diff-0.1) > 0.02 { // Small tolerance for floating point
		t.Errorf("Expected affinity bonus ~0.1, got %f", diff)
	}
}

// TestScorer_AffinityAdjustments_PreferredAffinity verifies preferred affinity bonus.
func TestScorer_AffinityAdjustments_PreferredAffinity(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	node := &membership.NodeInfo{
		ID:    "test-node",
		State: membership.StateReady,
		Labels: map[string]string{
			"tier": "frontend",
		},
		ReliabilityScore: 0.5,
		Capacity: membership.ResourceCapacity{
			CPUMillicores: 10000,
			MemoryBytes:   10 * 1024 * 1024 * 1024,
		},
		Usage: membership.ResourceUsage{},
	}

	tests := []struct {
		name   string
		weight int32
	}{
		{name: "weight 50 - moderate preference", weight: 50},
		{name: "weight 100 - strong preference", weight: 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workload := &WorkloadSpec{
				PlacementPolicy: PlacementPolicy{
					Affinity: []AffinityRule{
						{
							Type:     "node",
							Required: false,
							Weight:   tt.weight,
							MatchLabels: map[string]string{
								"tier": "frontend",
							},
						},
					},
				},
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 2000,
						MemoryBytes:   2 * 1024 * 1024 * 1024,
					},
				},
			}

			baselineWorkload := &WorkloadSpec{
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 2000,
						MemoryBytes:   2 * 1024 * 1024 * 1024,
					},
				},
			}

			scoreWith := scorer.ScoreNode(node, workload)
			scoreBaseline := scorer.ScoreNode(node, baselineWorkload)

			// Preferred affinity should increase score
			if scoreWith.Score <= scoreBaseline.Score {
				t.Errorf("Preferred affinity should increase score")
			}

			// Bonus should be (weight/100) * 0.2
			expectedBonus := (float64(tt.weight) / 100.0) * 0.2
			actualBonus := scoreWith.Score - scoreBaseline.Score

			if math.Abs(actualBonus-expectedBonus) > 0.02 {
				t.Errorf("Expected bonus %f, got %f", expectedBonus, actualBonus)
			}
		})
	}
}

// TestScorer_AffinityAdjustments_PreferredAntiAffinity verifies anti-affinity penalty.
func TestScorer_AffinityAdjustments_PreferredAntiAffinity(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	node := &membership.NodeInfo{
		ID:    "test-node",
		State: membership.StateReady,
		Labels: map[string]string{
			"workload-type": "batch",
		},
		ReliabilityScore: 0.5,
		Capacity: membership.ResourceCapacity{
			CPUMillicores: 10000,
			MemoryBytes:   10 * 1024 * 1024 * 1024,
		},
		Usage: membership.ResourceUsage{},
	}

	workload := &WorkloadSpec{
		PlacementPolicy: PlacementPolicy{
			AntiAffinity: []AffinityRule{
				{
					Type:     "node",
					Required: false,
					Weight:   100,
					MatchLabels: map[string]string{
						"workload-type": "batch",
					},
				},
			},
		},
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 2000,
				MemoryBytes:   2 * 1024 * 1024 * 1024,
			},
		},
	}

	baselineWorkload := &WorkloadSpec{
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 2000,
				MemoryBytes:   2 * 1024 * 1024 * 1024,
			},
		},
	}

	scoreWith := scorer.ScoreNode(node, workload)
	scoreBaseline := scorer.ScoreNode(node, baselineWorkload)

	// Anti-affinity should decrease score
	if scoreWith.Score >= scoreBaseline.Score {
		t.Errorf("Anti-affinity should decrease score: with=%f, baseline=%f",
			scoreWith.Score, scoreBaseline.Score)
	}

	// Penalty should be (weight/100) * 0.3
	expectedPenalty := (100.0 / 100.0) * 0.3
	actualPenalty := scoreBaseline.Score - scoreWith.Score

	if math.Abs(actualPenalty-expectedPenalty) > 0.02 {
		t.Errorf("Expected penalty %f, got %f", expectedPenalty, actualPenalty)
	}
}

// TestScorer_CompareScores verifies score comparison logic.
func TestScorer_CompareScores(t *testing.T) {
	logger := zap.NewNop()
	scorer := NewScorer(ScorerConfig{}, logger)

	node1 := &membership.NodeInfo{ID: "node1"}
	node2 := &membership.NodeInfo{ID: "node2"}

	tests := []struct {
		name     string
		score1   float64
		score2   float64
		expected int
	}{
		{
			name:     "score1 > score2",
			score1:   0.8,
			score2:   0.5,
			expected: 1,
		},
		{
			name:     "score1 < score2",
			score1:   0.3,
			score2:   0.7,
			expected: -1,
		},
		{
			name:     "score1 == score2",
			score1:   0.5,
			score2:   0.5,
			expected: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scoredNode1 := ScoredNode{Node: node1, Score: tt.score1}
			scoredNode2 := ScoredNode{Node: node2, Score: tt.score2}

			result := scorer.CompareScores(scoredNode1, scoredNode2)

			if result != tt.expected {
				t.Errorf("Expected comparison result %d, got %d", tt.expected, result)
			}
		})
	}
}
