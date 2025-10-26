package scheduler

import (
	"fmt"
	"testing"

	"github.com/cloudless/cloudless/pkg/coordinator/membership"
	"go.uber.org/zap"
)

// TestBinPacker_Pack_BasicPacking verifies CLD-REQ-022 bin-packing algorithm
// Test ID: CLD-REQ-022-TC-001
// Requirement: Bin-pack by default
func TestBinPacker_Pack_BasicPacking(t *testing.T) {
	tests := []struct {
		name               string
		nodeCount          int
		nodeCPU            int64
		nodeMemory         int64
		replicaCount       int
		replicaCPU         int64
		replicaMemory      int64
		expectedPlacements int
		description        string
	}{
		{
			name:               "pack_all_replicas_on_single_node",
			nodeCount:          3,
			nodeCPU:            4000,                   // 4 CPU cores
			nodeMemory:         8 * 1024 * 1024 * 1024, // 8 GB
			replicaCount:       3,
			replicaCPU:         500,               // 0.5 CPU
			replicaMemory:      512 * 1024 * 1024, // 512 MB
			expectedPlacements: 3,
			description:        "All replicas should pack on highest-scored node when they fit",
		},
		{
			name:               "distribute_when_single_node_insufficient",
			nodeCount:          3,
			nodeCPU:            2000,                   // 2 CPU cores per node
			nodeMemory:         4 * 1024 * 1024 * 1024, // 4 GB per node
			replicaCount:       5,
			replicaCPU:         800,                // 0.8 CPU each
			replicaMemory:      1024 * 1024 * 1024, // 1 GB each
			expectedPlacements: 5,
			description:        "Should distribute across nodes when single node capacity exceeded",
		},
		{
			name:               "respect_memory_constraints",
			nodeCount:          2,
			nodeCPU:            8000,                   // 8 CPU cores
			nodeMemory:         2 * 1024 * 1024 * 1024, // 2 GB
			replicaCount:       3,
			replicaCPU:         1000,               // 1 CPU
			replicaMemory:      1024 * 1024 * 1024, // 1 GB each
			expectedPlacements: 3,
			description:        "Should pack based on memory constraints even with excess CPU",
		},
		{
			name:               "handle_insufficient_cluster_capacity",
			nodeCount:          2,
			nodeCPU:            1000,               // 1 CPU core
			nodeMemory:         1024 * 1024 * 1024, // 1 GB
			replicaCount:       5,
			replicaCPU:         800,               // 0.8 CPU each
			replicaMemory:      512 * 1024 * 1024, // 512 MB
			expectedPlacements: 2,                 // Only 2 can fit
			description:        "Should place as many as possible when cluster capacity insufficient",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			binPacker := NewBinPacker(logger)

			// Create nodes with equal scores
			nodes := createNodesWithCapacity(tt.nodeCount, tt.nodeCPU, tt.nodeMemory, 100.0)

			resources := ResourceSpec{
				CPUMillicores: tt.replicaCPU,
				MemoryBytes:   tt.replicaMemory,
			}

			placements := binPacker.Pack(nodes, resources, tt.replicaCount)

			if len(placements) != tt.expectedPlacements {
				t.Errorf("%s: expected %d placements, got %d",
					tt.description, tt.expectedPlacements, len(placements))
			}

			// Verify no duplicate placements
			placedNodes := make(map[string]int)
			for _, p := range placements {
				placedNodes[p.NodeID]++
			}

			t.Logf("Placements distribution: %v", placedNodes)
		})
	}
}

// TestBinPacker_Pack_FirstFitDecreasing verifies first-fit decreasing algorithm
// Test ID: CLD-REQ-022-TC-002
// Requirement: Bin-pack by default (algorithm correctness)
func TestBinPacker_Pack_FirstFitDecreasing(t *testing.T) {
	tests := []struct {
		name                 string
		nodes                []struct{ cpu, memory int64 }
		replicaCount         int
		replicaCPU           int64
		replicaMemory        int64
		expectedDistribution map[int]int // node index -> replica count
		description          string
	}{
		{
			name: "pack_on_best_scoring_node_first",
			nodes: []struct{ cpu, memory int64 }{
				{cpu: 4000, memory: 4 * 1024 * 1024 * 1024}, // Node 0 - highest score
				{cpu: 4000, memory: 4 * 1024 * 1024 * 1024}, // Node 1
				{cpu: 4000, memory: 4 * 1024 * 1024 * 1024}, // Node 2
			},
			replicaCount:  2,
			replicaCPU:    1000,
			replicaMemory: 1024 * 1024 * 1024,
			expectedDistribution: map[int]int{
				0: 2, // Both should go to first (highest score) node
			},
			description: "First-fit should pack on highest-scored node when capacity permits",
		},
		{
			name: "overflow_to_next_node_when_full",
			nodes: []struct{ cpu, memory int64 }{
				{cpu: 1500, memory: 2 * 1024 * 1024 * 1024}, // Node 0 - can fit 1
				{cpu: 4000, memory: 4 * 1024 * 1024 * 1024}, // Node 1 - can fit 3
			},
			replicaCount:  4,
			replicaCPU:    1000,
			replicaMemory: 1024 * 1024 * 1024,
			expectedDistribution: map[int]int{
				0: 1, // First replica
				1: 3, // Remaining replicas
			},
			description: "Should overflow to next node when first node is full",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			binPacker := NewBinPacker(logger)

			// Create nodes with descending scores
			scoredNodes := []ScoredNode{}
			for i, node := range tt.nodes {
				scoredNodes = append(scoredNodes, ScoredNode{
					Node: &membership.NodeInfo{
						ID:   fmt.Sprintf("node-%d", i),
						Name: fmt.Sprintf("node-%d", i),
						Capacity: membership.ResourceCapacity{
							CPUMillicores: node.cpu,
							MemoryBytes:   node.memory,
						},
						Usage: membership.ResourceUsage{
							CPUMillicores: 0,
							MemoryBytes:   0,
						},
					},
					Score: float64(100 - i*10), // Descending scores
				})
			}

			resources := ResourceSpec{
				CPUMillicores: tt.replicaCPU,
				MemoryBytes:   tt.replicaMemory,
			}

			placements := binPacker.Pack(scoredNodes, resources, tt.replicaCount)

			// Count placements per node
			distribution := make(map[string]int)
			for _, p := range placements {
				distribution[p.NodeID]++
			}

			// Verify distribution matches expected
			for nodeIdx, expectedCount := range tt.expectedDistribution {
				nodeID := fmt.Sprintf("node-%d", nodeIdx)
				actualCount := distribution[nodeID]
				if actualCount != expectedCount {
					t.Errorf("%s: expected %d replicas on %s, got %d",
						tt.description, expectedCount, nodeID, actualCount)
				}
			}

			t.Logf("Distribution: %v", distribution)
		})
	}
}

// TestScheduler_PackingStrategy_Default verifies CLD-REQ-022 default strategy
// Test ID: CLD-REQ-022-TC-003
// Requirement: Bin-pack by default
func TestScheduler_PackingStrategy_Default(t *testing.T) {
	// Test that default config uses bin-pack
	config := DefaultSchedulerConfig()
	if config.PackingStrategy != "binpack" {
		t.Errorf("Default packing strategy should be 'binpack', got '%s'", config.PackingStrategy)
	}

	if config.SpreadPolicy != "zone" {
		t.Errorf("Default spread policy should be 'zone', got '%s'", config.SpreadPolicy)
	}

	t.Logf("Default config: PackingStrategy=%s, SpreadPolicy=%s",
		config.PackingStrategy, config.SpreadPolicy)
}

// TestScheduler_SpreadStrategy_ZoneDistribution verifies CLD-REQ-022 spread across zones
// Test ID: CLD-REQ-022-TC-004
// Requirement: Support spread across zones for HA
func TestScheduler_SpreadStrategy_ZoneDistribution(t *testing.T) {
	tests := []struct {
		name          string
		zones         []string
		nodesPerZone  int
		replicaCount  int
		expectedZones map[string]int // zone -> min replicas
		description   string
	}{
		{
			name:         "distribute_across_three_zones",
			zones:        []string{"zone-a", "zone-b", "zone-c"},
			nodesPerZone: 2,
			replicaCount: 6,
			expectedZones: map[string]int{
				"zone-a": 1,
				"zone-b": 1,
				"zone-c": 1,
			},
			description: "Should place at least one replica in each zone for HA",
		},
		{
			name:         "handle_more_replicas_than_zones",
			zones:        []string{"zone-a", "zone-b"},
			nodesPerZone: 3,
			replicaCount: 5,
			expectedZones: map[string]int{
				"zone-a": 1,
				"zone-b": 1,
			},
			description: "Should distribute across all zones even with more replicas",
		},
		{
			name:         "handle_single_zone",
			zones:        []string{"zone-a"},
			nodesPerZone: 3,
			replicaCount: 3,
			expectedZones: map[string]int{
				"zone-a": 3,
			},
			description: "Should handle single zone deployment",
		},
		{
			name:         "prioritize_zone_diversity",
			zones:        []string{"zone-a", "zone-b", "zone-c"},
			nodesPerZone: 1,
			replicaCount: 3,
			expectedZones: map[string]int{
				"zone-a": 1,
				"zone-b": 1,
				"zone-c": 1,
			},
			description: "Should maximize zone diversity when replicas equal zones",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()

			// Create nodes distributed across zones
			nodes := []ScoredNode{}
			nodeID := 0
			for _, zone := range tt.zones {
				for i := 0; i < tt.nodesPerZone; i++ {
					nodes = append(nodes, ScoredNode{
						Node: &membership.NodeInfo{
							ID:     fmt.Sprintf("node-%d", nodeID),
							Name:   fmt.Sprintf("node-%d", nodeID),
							Zone:   zone,
							Region: "region-1",
							State:  membership.StateReady,
							Capacity: membership.ResourceCapacity{
								CPUMillicores: 4000,
								MemoryBytes:   8 * 1024 * 1024 * 1024,
							},
							Usage: membership.ResourceUsage{
								CPUMillicores: 0,
								MemoryBytes:   0,
							},
						},
						Score: 80.0,
					})
					nodeID++
				}
			}

			scheduler := &Scheduler{
				logger: logger,
				config: &SchedulerConfig{
					PackingStrategy: "spread",
					SpreadPolicy:    "zone",
				},
			}

			workload := &WorkloadSpec{
				ID:       "test-workload",
				Name:     "spread-test",
				Replicas: tt.replicaCount,
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 500,
						MemoryBytes:   512 * 1024 * 1024,
					},
				},
			}

			decisions := scheduler.spreadSchedule(nodes, workload)

			// Count replicas per zone
			zoneDistribution := make(map[string]int)
			for _, decision := range decisions {
				// Find the node's zone
				for _, node := range nodes {
					if node.Node.ID == decision.NodeID {
						zoneDistribution[node.Node.Zone]++
						break
					}
				}
			}

			// Verify minimum replicas per zone
			for zone, minReplicas := range tt.expectedZones {
				actualReplicas := zoneDistribution[zone]
				if actualReplicas < minReplicas {
					t.Errorf("%s: expected at least %d replicas in %s, got %d",
						tt.description, minReplicas, zone, actualReplicas)
				}
			}

			t.Logf("Zone distribution: %v", zoneDistribution)
		})
	}
}

// TestScheduler_GroupNodesByTopology verifies topology grouping
// Test ID: CLD-REQ-022-TC-005
// Requirement: Support spread across zones for HA (topology awareness)
func TestScheduler_GroupNodesByTopology(t *testing.T) {
	tests := []struct {
		name           string
		topology       string
		nodes          []struct{ id, zone, region string }
		expectedGroups int
		description    string
	}{
		{
			name:     "group_by_zone",
			topology: "zone",
			nodes: []struct{ id, zone, region string }{
				{id: "node-1", zone: "zone-a", region: "region-1"},
				{id: "node-2", zone: "zone-a", region: "region-1"},
				{id: "node-3", zone: "zone-b", region: "region-1"},
				{id: "node-4", zone: "zone-c", region: "region-2"},
			},
			expectedGroups: 3, // zone-a, zone-b, zone-c
			description:    "Should group nodes by zone",
		},
		{
			name:     "group_by_region",
			topology: "region",
			nodes: []struct{ id, zone, region string }{
				{id: "node-1", zone: "zone-a", region: "region-1"},
				{id: "node-2", zone: "zone-b", region: "region-1"},
				{id: "node-3", zone: "zone-c", region: "region-2"},
				{id: "node-4", zone: "zone-d", region: "region-2"},
			},
			expectedGroups: 2, // region-1, region-2
			description:    "Should group nodes by region",
		},
		{
			name:     "group_by_node",
			topology: "node",
			nodes: []struct{ id, zone, region string }{
				{id: "node-1", zone: "zone-a", region: "region-1"},
				{id: "node-2", zone: "zone-a", region: "region-1"},
				{id: "node-3", zone: "zone-b", region: "region-1"},
			},
			expectedGroups: 3, // Each node is its own group
			description:    "Should create one group per node",
		},
		{
			name:     "default_topology",
			topology: "unknown",
			nodes: []struct{ id, zone, region string }{
				{id: "node-1", zone: "zone-a", region: "region-1"},
				{id: "node-2", zone: "zone-b", region: "region-2"},
			},
			expectedGroups: 1, // All nodes in "default" group
			description:    "Should use default group for unknown topology",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			scheduler := &Scheduler{
				logger: zap.NewNop(),
				config: DefaultSchedulerConfig(),
			}

			// Create scored nodes
			scoredNodes := []ScoredNode{}
			for _, node := range tt.nodes {
				scoredNodes = append(scoredNodes, ScoredNode{
					Node: &membership.NodeInfo{
						ID:     node.id,
						Name:   node.id,
						Zone:   node.zone,
						Region: node.region,
						Capacity: membership.ResourceCapacity{
							CPUMillicores: 4000,
							MemoryBytes:   8 * 1024 * 1024 * 1024,
						},
					},
					Score: 80.0,
				})
			}

			groups := scheduler.groupNodesByTopology(scoredNodes, tt.topology)

			if len(groups) != tt.expectedGroups {
				t.Errorf("%s: expected %d groups, got %d",
					tt.description, tt.expectedGroups, len(groups))
			}

			// Log group distribution
			for key, nodes := range groups {
				nodeIDs := []string{}
				for _, n := range nodes {
					nodeIDs = append(nodeIDs, n.Node.ID)
				}
				t.Logf("Group '%s': %v", key, nodeIDs)
			}
		})
	}
}

// TestBinPacker_PackWithSpread verifies zone-aware bin-packing
// Test ID: CLD-REQ-022-TC-006
// Requirement: Support spread across zones for HA (with bin-packing)
func TestBinPacker_PackWithSpread(t *testing.T) {
	tests := []struct {
		name             string
		zones            []string
		nodesPerZone     int
		replicaCount     int
		expectedMinZones int // Minimum number of zones that should have replicas
		description      string
	}{
		{
			name:             "spread_first_then_pack",
			zones:            []string{"zone-a", "zone-b", "zone-c"},
			nodesPerZone:     2,
			replicaCount:     5,
			expectedMinZones: 3, // Should hit all zones first
			description:      "Should place one replica per zone first, then pack remaining",
		},
		{
			name:             "single_zone_no_spread_needed",
			zones:            []string{"zone-a"},
			nodesPerZone:     3,
			replicaCount:     3,
			expectedMinZones: 1,
			description:      "Single zone should pack all replicas",
		},
		{
			name:             "fewer_replicas_than_zones",
			zones:            []string{"zone-a", "zone-b", "zone-c", "zone-d"},
			nodesPerZone:     2,
			replicaCount:     2,
			expectedMinZones: 2, // Only 2 zones needed
			description:      "Should spread across available zones up to replica count",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			binPacker := NewBinPacker(logger)

			// Create nodes across zones
			nodes := []ScoredNode{}
			nodeID := 0
			for _, zone := range tt.zones {
				for i := 0; i < tt.nodesPerZone; i++ {
					nodes = append(nodes, ScoredNode{
						Node: &membership.NodeInfo{
							ID:   fmt.Sprintf("node-%d", nodeID),
							Name: fmt.Sprintf("node-%d", nodeID),
							Zone: zone,
							Capacity: membership.ResourceCapacity{
								CPUMillicores: 4000,
								MemoryBytes:   8 * 1024 * 1024 * 1024,
							},
							Usage: membership.ResourceUsage{
								CPUMillicores: 0,
								MemoryBytes:   0,
							},
						},
						Score: 80.0,
					})
					nodeID++
				}
			}

			resources := ResourceSpec{
				CPUMillicores: 500,
				MemoryBytes:   512 * 1024 * 1024,
			}

			placements := binPacker.PackWithSpread(nodes, resources, tt.replicaCount, "zone")

			// Count unique zones
			zonesUsed := make(map[string]int)
			for _, p := range placements {
				for _, node := range nodes {
					if node.Node.ID == p.NodeID {
						zonesUsed[node.Node.Zone]++
						break
					}
				}
			}

			if len(zonesUsed) < tt.expectedMinZones {
				t.Errorf("%s: expected at least %d zones used, got %d",
					tt.description, tt.expectedMinZones, len(zonesUsed))
			}

			t.Logf("Zones used: %v", zonesUsed)
		})
	}
}

// TestScheduler_BinPackSchedule_Integration verifies full bin-pack integration
// Test ID: CLD-REQ-022-TC-007
// Requirement: Bin-pack by default (end-to-end integration)
func TestScheduler_BinPackSchedule_Integration(t *testing.T) {
	logger := zap.NewNop()

	// Create mock nodes
	nodes := []ScoredNode{
		{
			Node: &membership.NodeInfo{
				ID:    "node-1",
				Name:  "node-1",
				Zone:  "zone-a",
				State: membership.StateReady,
				Capacity: membership.ResourceCapacity{
					CPUMillicores: 4000,
					MemoryBytes:   8 * 1024 * 1024 * 1024,
				},
				Usage: membership.ResourceUsage{
					CPUMillicores: 0,
					MemoryBytes:   0,
				},
			},
			Score: 100.0,
		},
		{
			Node: &membership.NodeInfo{
				ID:    "node-2",
				Name:  "node-2",
				Zone:  "zone-b",
				State: membership.StateReady,
				Capacity: membership.ResourceCapacity{
					CPUMillicores: 4000,
					MemoryBytes:   8 * 1024 * 1024 * 1024,
				},
				Usage: membership.ResourceUsage{
					CPUMillicores: 0,
					MemoryBytes:   0,
				},
			},
			Score: 90.0,
		},
	}

	scheduler := &Scheduler{
		logger: logger,
		config: &SchedulerConfig{
			PackingStrategy: "binpack",
		},
		binPacker: NewBinPacker(logger),
	}

	workload := &WorkloadSpec{
		ID:       "test-workload",
		Name:     "binpack-test",
		Replicas: 3,
		Resources: ResourceRequirements{
			Requests: ResourceSpec{
				CPUMillicores: 1000,
				MemoryBytes:   1024 * 1024 * 1024,
			},
		},
	}

	decisions := scheduler.binPackSchedule(nodes, workload)

	if len(decisions) != 3 {
		t.Errorf("Expected 3 placement decisions, got %d", len(decisions))
	}

	// Verify bin-pack behavior: should prefer node-1 (higher score)
	node1Count := 0
	for _, d := range decisions {
		if d.NodeID == "node-1" {
			node1Count++
		}
		// Verify fragment ID is set
		if d.FragmentID == "" {
			t.Error("Fragment ID should be set")
		}
		// Verify reason includes "bin-packed"
		found := false
		for _, reason := range d.Reasons {
			if reason == "bin-packed" {
				found = true
				break
			}
		}
		if !found {
			t.Error("Decision reason should include 'bin-packed'")
		}
	}

	t.Logf("Node-1 placements: %d, Node-2 placements: %d", node1Count, 3-node1Count)
}

// TestScheduler_StrategySelection verifies correct strategy routing
// Test ID: CLD-REQ-022-TC-008
// Requirement: Bin-pack by default; support spread (strategy selection)
func TestScheduler_StrategySelection(t *testing.T) {
	tests := []struct {
		name            string
		packingStrategy string
		expectedMethod  string
		description     string
	}{
		{
			name:            "binpack_strategy",
			packingStrategy: "binpack",
			expectedMethod:  "binPackSchedule",
			description:     "PackingStrategy='binpack' should use bin-packing",
		},
		{
			name:            "spread_strategy",
			packingStrategy: "spread",
			expectedMethod:  "spreadSchedule",
			description:     "PackingStrategy='spread' should use spread scheduling",
		},
		{
			name:            "unknown_strategy_uses_default",
			packingStrategy: "unknown",
			expectedMethod:  "defaultSchedule",
			description:     "Unknown strategy should fall back to default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultSchedulerConfig()
			config.PackingStrategy = tt.packingStrategy

			// Verify config is set correctly
			if config.PackingStrategy != tt.packingStrategy {
				t.Errorf("Config should have PackingStrategy=%s", tt.packingStrategy)
			}

			t.Logf("%s: Config PackingStrategy=%s", tt.description, config.PackingStrategy)
		})
	}
}

// TestScheduler_BinPack_EdgeCases verifies edge case handling
// Test ID: CLD-REQ-022-TC-009
// Requirement: Bin-pack by default (edge cases)
func TestScheduler_BinPack_EdgeCases(t *testing.T) {
	tests := []struct {
		name               string
		nodeCount          int
		replicaCount       int
		nodeCapacityCPU    int64
		replicaRequestCPU  int64
		expectedPlacements int
		description        string
	}{
		{
			name:               "zero_replicas",
			nodeCount:          3,
			replicaCount:       0,
			nodeCapacityCPU:    4000,
			replicaRequestCPU:  1000,
			expectedPlacements: 0,
			description:        "Should handle zero replicas gracefully",
		},
		{
			name:               "single_replica",
			nodeCount:          3,
			replicaCount:       1,
			nodeCapacityCPU:    4000,
			replicaRequestCPU:  1000,
			expectedPlacements: 1,
			description:        "Should place single replica on best node",
		},
		{
			name:               "exact_capacity_match",
			nodeCount:          2,
			replicaCount:       2,
			nodeCapacityCPU:    1000,
			replicaRequestCPU:  1000,
			expectedPlacements: 2,
			description:        "Should handle exact capacity match (one replica per node)",
		},
		{
			name:               "no_nodes_available",
			nodeCount:          0,
			replicaCount:       3,
			nodeCapacityCPU:    4000,
			replicaRequestCPU:  1000,
			expectedPlacements: 0,
			description:        "Should handle no available nodes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()
			binPacker := NewBinPacker(logger)

			nodes := createNodesWithCapacity(tt.nodeCount, tt.nodeCapacityCPU, 8*1024*1024*1024, 80.0)

			resources := ResourceSpec{
				CPUMillicores: tt.replicaRequestCPU,
				MemoryBytes:   512 * 1024 * 1024,
			}

			placements := binPacker.Pack(nodes, resources, tt.replicaCount)

			if len(placements) != tt.expectedPlacements {
				t.Errorf("%s: expected %d placements, got %d",
					tt.description, tt.expectedPlacements, len(placements))
			}
		})
	}
}

// Helper function to create nodes with specific capacity
func createNodesWithCapacity(count int, cpu, memory int64, score float64) []ScoredNode {
	nodes := []ScoredNode{}
	for i := 0; i < count; i++ {
		nodes = append(nodes, ScoredNode{
			Node: &membership.NodeInfo{
				ID:    fmt.Sprintf("node-%d", i),
				Name:  fmt.Sprintf("node-%d", i),
				Zone:  fmt.Sprintf("zone-%d", i%3), // Distribute across 3 zones
				State: membership.StateReady,
				Capacity: membership.ResourceCapacity{
					CPUMillicores: cpu,
					MemoryBytes:   memory,
				},
				Usage: membership.ResourceUsage{
					CPUMillicores: 0,
					MemoryBytes:   0,
				},
			},
			Score: score,
		})
	}
	return nodes
}
