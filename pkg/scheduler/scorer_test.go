//go:build integration
// +build integration

package scheduler

import (
	"math"
	"testing"
)

func TestScoreNode(t *testing.T) {
	// Create scorer with default weights
	scorer := &Scorer{
		LocalityWeight:       0.3,
		ReliabilityWeight:    0.25,
		CostWeight:           0.15,
		UtilizationWeight:    0.2,
		NetworkPenaltyWeight: 0.1,
	}

	tests := []struct {
		name     string
		node     *NodeInfo
		req      *WorkloadRequest
		minScore float64
		maxScore float64
	}{
		{
			name: "ideal node - same zone, high reliability, low utilization",
			node: &NodeInfo{
				ID:               "node-1",
				Zone:             "us-east-1a",
				Region:           "us-east",
				CPUMillicores:    8000,
				MemoryBytes:      16 * 1024 * 1024 * 1024,
				UsedCPU:          1000,
				UsedMemory:       2 * 1024 * 1024 * 1024,
				ReliabilityScore: 0.95,
			},
			req: &WorkloadRequest{
				Zone:          "us-east-1a",
				Region:        "us-east",
				CPUMillicores: 500,
				MemoryBytes:   512 * 1024 * 1024,
			},
			minScore: 80.0,
			maxScore: 100.0,
		},
		{
			name: "different zone - lower score",
			node: &NodeInfo{
				ID:               "node-2",
				Zone:             "us-east-1b",
				Region:           "us-east",
				CPUMillicores:    8000,
				MemoryBytes:      16 * 1024 * 1024 * 1024,
				UsedCPU:          1000,
				UsedMemory:       2 * 1024 * 1024 * 1024,
				ReliabilityScore: 0.95,
			},
			req: &WorkloadRequest{
				Zone:          "us-east-1a",
				Region:        "us-east",
				CPUMillicores: 500,
				MemoryBytes:   512 * 1024 * 1024,
			},
			minScore: 60.0,
			maxScore: 85.0,
		},
		{
			name: "high utilization - lower score",
			node: &NodeInfo{
				ID:               "node-3",
				Zone:             "us-east-1a",
				Region:           "us-east",
				CPUMillicores:    8000,
				MemoryBytes:      16 * 1024 * 1024 * 1024,
				UsedCPU:          7000, // 87.5% utilized
				UsedMemory:       14 * 1024 * 1024 * 1024,
				ReliabilityScore: 0.95,
			},
			req: &WorkloadRequest{
				Zone:          "us-east-1a",
				Region:        "us-east",
				CPUMillicores: 500,
				MemoryBytes:   512 * 1024 * 1024,
			},
			minScore: 50.0,
			maxScore: 75.0,
		},
		{
			name: "low reliability - lower score",
			node: &NodeInfo{
				ID:               "node-4",
				Zone:             "us-east-1a",
				Region:           "us-east",
				CPUMillicores:    8000,
				MemoryBytes:      16 * 1024 * 1024 * 1024,
				UsedCPU:          1000,
				UsedMemory:       2 * 1024 * 1024 * 1024,
				ReliabilityScore: 0.5, // Low reliability
			},
			req: &WorkloadRequest{
				Zone:          "us-east-1a",
				Region:        "us-east",
				CPUMillicores: 500,
				MemoryBytes:   512 * 1024 * 1024,
			},
			minScore: 60.0,
			maxScore: 80.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scorer.ScoreNode(tt.node, tt.req)

			if score < tt.minScore || score > tt.maxScore {
				t.Errorf("Score %f not in expected range [%f, %f]", score, tt.minScore, tt.maxScore)
			}
		})
	}
}

func TestCalculateLocalityScore(t *testing.T) {
	scorer := &Scorer{}

	tests := []struct {
		name          string
		nodeZone      string
		nodeRegion    string
		reqZone       string
		reqRegion     string
		expectedScore float64
	}{
		{
			name:          "same zone",
			nodeZone:      "us-east-1a",
			nodeRegion:    "us-east",
			reqZone:       "us-east-1a",
			reqRegion:     "us-east",
			expectedScore: 100.0,
		},
		{
			name:          "same region, different zone",
			nodeZone:      "us-east-1b",
			nodeRegion:    "us-east",
			reqZone:       "us-east-1a",
			reqRegion:     "us-east",
			expectedScore: 70.0,
		},
		{
			name:          "different region",
			nodeZone:      "us-west-1a",
			nodeRegion:    "us-west",
			reqZone:       "us-east-1a",
			reqRegion:     "us-east",
			expectedScore: 30.0,
		},
		{
			name:          "no preference",
			nodeZone:      "us-east-1a",
			nodeRegion:    "us-east",
			reqZone:       "",
			reqRegion:     "",
			expectedScore: 50.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &NodeInfo{
				Zone:   tt.nodeZone,
				Region: tt.nodeRegion,
			}
			req := &WorkloadRequest{
				Zone:   tt.reqZone,
				Region: tt.reqRegion,
			}

			score := scorer.calculateLocalityScore(node, req)

			if math.Abs(score-tt.expectedScore) > 0.01 {
				t.Errorf("Expected score %f, got %f", tt.expectedScore, score)
			}
		})
	}
}

func TestCalculateUtilizationScore(t *testing.T) {
	scorer := &Scorer{}

	tests := []struct {
		name          string
		node          *NodeInfo
		expectedScore float64
		tolerance     float64
	}{
		{
			name: "low utilization (ideal)",
			node: &NodeInfo{
				CPUMillicores: 8000,
				MemoryBytes:   16 * 1024 * 1024 * 1024,
				UsedCPU:       2000,                   // 25% CPU
				UsedMemory:    4 * 1024 * 1024 * 1024, // 25% memory
			},
			expectedScore: 100.0,
			tolerance:     5.0,
		},
		{
			name: "medium utilization",
			node: &NodeInfo{
				CPUMillicores: 8000,
				MemoryBytes:   16 * 1024 * 1024 * 1024,
				UsedCPU:       4000,                   // 50% CPU
				UsedMemory:    8 * 1024 * 1024 * 1024, // 50% memory
			},
			expectedScore: 75.0,
			tolerance:     10.0,
		},
		{
			name: "high utilization",
			node: &NodeInfo{
				CPUMillicores: 8000,
				MemoryBytes:   16 * 1024 * 1024 * 1024,
				UsedCPU:       7000,                    // 87.5% CPU
				UsedMemory:    14 * 1024 * 1024 * 1024, // 87.5% memory
			},
			expectedScore: 30.0,
			tolerance:     15.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := scorer.calculateUtilizationScore(tt.node)

			if math.Abs(score-tt.expectedScore) > tt.tolerance {
				t.Errorf("Expected score ~%f, got %f", tt.expectedScore, score)
			}
		})
	}
}

func TestCalculateReliabilityScore(t *testing.T) {
	scorer := &Scorer{}

	tests := []struct {
		name             string
		reliabilityScore float64
		expectedScore    float64
	}{
		{
			name:             "perfect reliability",
			reliabilityScore: 1.0,
			expectedScore:    100.0,
		},
		{
			name:             "high reliability",
			reliabilityScore: 0.95,
			expectedScore:    95.0,
		},
		{
			name:             "medium reliability",
			reliabilityScore: 0.7,
			expectedScore:    70.0,
		},
		{
			name:             "low reliability",
			reliabilityScore: 0.3,
			expectedScore:    30.0,
		},
		{
			name:             "zero reliability",
			reliabilityScore: 0.0,
			expectedScore:    0.0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			node := &NodeInfo{
				ReliabilityScore: tt.reliabilityScore,
			}

			score := scorer.calculateReliabilityScore(node)

			if math.Abs(score-tt.expectedScore) > 0.01 {
				t.Errorf("Expected score %f, got %f", tt.expectedScore, score)
			}
		})
	}
}

func TestHasCapacity(t *testing.T) {
	tests := []struct {
		name        string
		node        *NodeInfo
		req         *WorkloadRequest
		hasCapacity bool
	}{
		{
			name: "sufficient capacity",
			node: &NodeInfo{
				CPUMillicores: 8000,
				MemoryBytes:   16 * 1024 * 1024 * 1024,
				UsedCPU:       1000,
				UsedMemory:    2 * 1024 * 1024 * 1024,
			},
			req: &WorkloadRequest{
				CPUMillicores: 500,
				MemoryBytes:   512 * 1024 * 1024,
			},
			hasCapacity: true,
		},
		{
			name: "insufficient CPU",
			node: &NodeInfo{
				CPUMillicores: 8000,
				MemoryBytes:   16 * 1024 * 1024 * 1024,
				UsedCPU:       7800,
				UsedMemory:    2 * 1024 * 1024 * 1024,
			},
			req: &WorkloadRequest{
				CPUMillicores: 500,
				MemoryBytes:   512 * 1024 * 1024,
			},
			hasCapacity: false,
		},
		{
			name: "insufficient memory",
			node: &NodeInfo{
				CPUMillicores: 8000,
				MemoryBytes:   16 * 1024 * 1024 * 1024,
				UsedCPU:       1000,
				UsedMemory:    15*1024*1024*1024 + 512*1024*1024,
			},
			req: &WorkloadRequest{
				CPUMillicores: 500,
				MemoryBytes:   512 * 1024 * 1024,
			},
			hasCapacity: false,
		},
		{
			name: "exact fit",
			node: &NodeInfo{
				CPUMillicores: 8000,
				MemoryBytes:   16 * 1024 * 1024 * 1024,
				UsedCPU:       7500,
				UsedMemory:    16*1024*1024*1024 - 512*1024*1024,
			},
			req: &WorkloadRequest{
				CPUMillicores: 500,
				MemoryBytes:   512 * 1024 * 1024,
			},
			hasCapacity: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := hasCapacity(tt.node, tt.req)

			if result != tt.hasCapacity {
				t.Errorf("Expected hasCapacity=%v, got %v", tt.hasCapacity, result)
			}
		})
	}
}

func TestWeightedScore(t *testing.T) {
	scorer := &Scorer{
		LocalityWeight:       0.3,
		ReliabilityWeight:    0.25,
		CostWeight:           0.15,
		UtilizationWeight:    0.2,
		NetworkPenaltyWeight: 0.1,
	}

	// Verify weights sum to 1.0
	totalWeight := scorer.LocalityWeight +
		scorer.ReliabilityWeight +
		scorer.CostWeight +
		scorer.UtilizationWeight +
		scorer.NetworkPenaltyWeight

	if math.Abs(totalWeight-1.0) > 0.01 {
		t.Errorf("Weights should sum to 1.0, got %f", totalWeight)
	}

	// Test that perfect scores give 100
	node := &NodeInfo{
		Zone:             "us-east-1a",
		Region:           "us-east",
		CPUMillicores:    8000,
		MemoryBytes:      16 * 1024 * 1024 * 1024,
		UsedCPU:          0,
		UsedMemory:       0,
		ReliabilityScore: 1.0,
	}

	req := &WorkloadRequest{
		Zone:          "us-east-1a",
		Region:        "us-east",
		CPUMillicores: 500,
		MemoryBytes:   512 * 1024 * 1024,
	}

	score := scorer.ScoreNode(node, req)

	// Score should be very high (close to 100)
	if score < 90.0 {
		t.Errorf("Perfect node should score >90, got %f", score)
	}
}

// Helper function that may be in the actual scorer implementation
func hasCapacity(node *NodeInfo, req *WorkloadRequest) bool {
	availableCPU := node.CPUMillicores - node.UsedCPU
	availableMemory := node.MemoryBytes - node.UsedMemory

	return availableCPU >= req.CPUMillicores && availableMemory >= req.MemoryBytes
}
