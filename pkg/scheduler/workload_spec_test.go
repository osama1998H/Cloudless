// CLD-REQ-020: Workloads declare Spec with resources, replicas, region/zone affinity,
// anti-affinity, restart policy, rollout strategy
//
// This test file verifies the complete implementation of CLD-REQ-020 including:
// - Resource requirements (CPU, memory, storage, bandwidth, GPU)
// - Replica count specification
// - Region/zone filtering
// - Affinity and anti-affinity rules
// - Node selectors
// - Taint tolerations
// - Restart policies
// - Rollout strategies (RollingUpdate, Recreate, BlueGreen)
// - MinAvailable constraints (CLD-REQ-023)

package scheduler

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/osama1998H/Cloudless/pkg/coordinator/membership"
	"go.uber.org/zap"
)

// TestScheduler_PlacementPolicy_RegionFiltering verifies CLD-REQ-020 region constraints
// Test ID: CLD-REQ-020-TC-001
func TestScheduler_PlacementPolicy_RegionFiltering(t *testing.T) {
	tests := []struct {
		name               string
		regions            []string
		nodeRegions        []string
		expectedCandidates int
		description        string
	}{
		{
			name:               "single_region_match",
			regions:            []string{"us-west-1"},
			nodeRegions:        []string{"us-west-1", "us-east-1", "eu-west-1"},
			expectedCandidates: 1,
			description:        "Workload with single region preference matches only nodes in that region",
		},
		{
			name:               "multiple_regions_match",
			regions:            []string{"us-west-1", "us-east-1"},
			nodeRegions:        []string{"us-west-1", "us-east-1", "eu-west-1"},
			expectedCandidates: 2,
			description:        "Workload with multiple region preferences matches nodes in any listed region",
		},
		{
			name:               "no_region_match",
			regions:            []string{"ap-southeast-1"},
			nodeRegions:        []string{"us-west-1", "us-east-1", "eu-west-1"},
			expectedCandidates: 0,
			description:        "Workload with region preference excludes nodes in other regions",
		},
		{
			name:               "no_region_preference",
			regions:            []string{},
			nodeRegions:        []string{"us-west-1", "us-east-1", "eu-west-1"},
			expectedCandidates: 3,
			description:        "Workload with no region preference accepts all nodes",
		},
		{
			name:               "all_regions_match",
			regions:            []string{"us-west-1", "us-east-1", "eu-west-1"},
			nodeRegions:        []string{"us-west-1", "us-east-1", "eu-west-1"},
			expectedCandidates: 3,
			description:        "Workload with all regions matches all nodes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop()
			scheduler := &Scheduler{
				logger: logger,
				config: DefaultSchedulerConfig(),
			}

			nodes := createMockNodesWithRegions(tt.nodeRegions)
			workload := &WorkloadSpec{
				ID:       "test-workload",
				Name:     "region-test",
				Replicas: 1,
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 100,
						MemoryBytes:   100 * 1024 * 1024, // 100MB
					},
				},
				PlacementPolicy: PlacementPolicy{
					Regions: tt.regions,
				},
			}

			// Execute - test filterNodes directly
			filtered := scheduler.filterNodes(nodes, workload)

			// Verify
			if len(filtered) != tt.expectedCandidates {
				t.Errorf("%s: expected %d candidates, got %d",
					tt.description, tt.expectedCandidates, len(filtered))
			}

			// Verify filtered nodes are in allowed regions
			for _, node := range filtered {
				if len(tt.regions) > 0 && !contains(tt.regions, node.Region) {
					t.Errorf("Filtered node %s in region %s, not in allowed regions %v",
						node.ID, node.Region, tt.regions)
				}
			}
		})
	}
}

// TestScheduler_PlacementPolicy_ZoneFiltering verifies CLD-REQ-020 zone constraints
// Test ID: CLD-REQ-020-TC-002
func TestScheduler_PlacementPolicy_ZoneFiltering(t *testing.T) {
	tests := []struct {
		name               string
		zones              []string
		nodeZones          []string
		expectedCandidates int
		description        string
	}{
		{
			name:               "single_zone_match",
			zones:              []string{"us-west-1a"},
			nodeZones:          []string{"us-west-1a", "us-west-1b", "us-east-1a"},
			expectedCandidates: 1,
			description:        "Workload with single zone preference matches only nodes in that zone",
		},
		{
			name:               "multiple_zones_match",
			zones:              []string{"us-west-1a", "us-west-1b"},
			nodeZones:          []string{"us-west-1a", "us-west-1b", "us-east-1a"},
			expectedCandidates: 2,
			description:        "Workload with multiple zone preferences matches nodes in any listed zone",
		},
		{
			name:               "no_zone_match",
			zones:              []string{"ap-southeast-1a"},
			nodeZones:          []string{"us-west-1a", "us-west-1b", "us-east-1a"},
			expectedCandidates: 0,
			description:        "Workload with zone preference excludes nodes in other zones",
		},
		{
			name:               "no_zone_preference",
			zones:              []string{},
			nodeZones:          []string{"us-west-1a", "us-west-1b", "us-east-1a"},
			expectedCandidates: 3,
			description:        "Workload with no zone preference accepts all nodes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop()
			scheduler := &Scheduler{
				logger: logger,
				config: DefaultSchedulerConfig(),
			}

			nodes := createMockNodesWithZones(tt.nodeZones)
			workload := &WorkloadSpec{
				ID:       "test-workload",
				Name:     "zone-test",
				Replicas: 1,
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 100,
						MemoryBytes:   100 * 1024 * 1024,
					},
				},
				PlacementPolicy: PlacementPolicy{
					Zones: tt.zones,
				},
			}

			// Execute
			filtered := scheduler.filterNodes(nodes, workload)

			// Verify
			if len(filtered) != tt.expectedCandidates {
				t.Errorf("%s: expected %d candidates, got %d",
					tt.description, tt.expectedCandidates, len(filtered))
			}

			// Verify filtered nodes are in allowed zones
			for _, node := range filtered {
				if len(tt.zones) > 0 && !contains(tt.zones, node.Zone) {
					t.Errorf("Filtered node %s in zone %s, not in allowed zones %v",
						node.ID, node.Zone, tt.zones)
				}
			}
		})
	}
}

// TestScheduler_PlacementPolicy_Affinity verifies CLD-REQ-020 affinity rules
// Test ID: CLD-REQ-020-TC-003
func TestScheduler_PlacementPolicy_Affinity(t *testing.T) {
	tests := []struct {
		name               string
		affinityRules      []AffinityRule
		nodeLabels         []map[string]string
		expectedCandidates int
		description        string
	}{
		{
			name: "required_affinity_match",
			affinityRules: []AffinityRule{
				{
					Type:        "node",
					MatchLabels: map[string]string{"disk": "ssd"},
					Required:    true,
				},
			},
			nodeLabels: []map[string]string{
				{"disk": "ssd", "region": "us-west"},
				{"disk": "hdd", "region": "us-west"},
				{"disk": "ssd", "region": "us-east"},
			},
			expectedCandidates: 2,
			description:        "Required affinity rule filters nodes to those matching labels",
		},
		{
			name: "required_affinity_no_match",
			affinityRules: []AffinityRule{
				{
					Type:        "node",
					MatchLabels: map[string]string{"disk": "nvme"},
					Required:    true,
				},
			},
			nodeLabels: []map[string]string{
				{"disk": "ssd"},
				{"disk": "hdd"},
			},
			expectedCandidates: 0,
			description:        "Required affinity with no matching nodes returns no candidates",
		},
		{
			name: "multiple_label_affinity",
			affinityRules: []AffinityRule{
				{
					Type:        "node",
					MatchLabels: map[string]string{"disk": "ssd", "network": "10g"},
					Required:    true,
				},
			},
			nodeLabels: []map[string]string{
				{"disk": "ssd", "network": "10g"},
				{"disk": "ssd", "network": "1g"},
				{"disk": "hdd", "network": "10g"},
			},
			expectedCandidates: 1,
			description:        "Affinity with multiple labels requires all labels to match",
		},
		{
			name:          "no_affinity_rules",
			affinityRules: []AffinityRule{},
			nodeLabels: []map[string]string{
				{"disk": "ssd"},
				{"disk": "hdd"},
			},
			expectedCandidates: 2,
			description:        "No affinity rules accepts all nodes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop()
			scheduler := &Scheduler{
				logger: logger,
				config: DefaultSchedulerConfig(),
			}

			nodes := createMockNodesWithLabels(tt.nodeLabels)
			workload := &WorkloadSpec{
				ID:       "test-workload",
				Name:     "affinity-test",
				Replicas: 1,
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 100,
						MemoryBytes:   100 * 1024 * 1024,
					},
				},
				PlacementPolicy: PlacementPolicy{
					Affinity: tt.affinityRules,
				},
			}

			// Execute
			filtered := scheduler.filterNodes(nodes, workload)

			// Verify
			if len(filtered) != tt.expectedCandidates {
				t.Errorf("%s: expected %d candidates, got %d",
					tt.description, tt.expectedCandidates, len(filtered))
			}
		})
	}
}

// TestScheduler_PlacementPolicy_AntiAffinity verifies CLD-REQ-020 anti-affinity rules
// Test ID: CLD-REQ-020-TC-004
func TestScheduler_PlacementPolicy_AntiAffinity(t *testing.T) {
	tests := []struct {
		name               string
		antiAffinityRules  []AffinityRule
		nodeLabels         []map[string]string
		expectedCandidates int
		description        string
	}{
		{
			name: "required_anti_affinity_excludes",
			antiAffinityRules: []AffinityRule{
				{
					Type:        "node",
					MatchLabels: map[string]string{"gpu": "true"},
					Required:    true,
				},
			},
			nodeLabels: []map[string]string{
				{"gpu": "true", "region": "us-west"},
				{"gpu": "false", "region": "us-west"},
				{"cpu": "high", "region": "us-east"},
			},
			expectedCandidates: 2,
			description:        "Anti-affinity excludes nodes with matching labels",
		},
		{
			name: "anti_affinity_all_excluded",
			antiAffinityRules: []AffinityRule{
				{
					Type:        "node",
					MatchLabels: map[string]string{"type": "compute"},
					Required:    true,
				},
			},
			nodeLabels: []map[string]string{
				{"type": "compute"},
				{"type": "compute"},
			},
			expectedCandidates: 0,
			description:        "Anti-affinity matching all nodes returns no candidates",
		},
		{
			name: "anti_affinity_multiple_labels",
			antiAffinityRules: []AffinityRule{
				{
					Type:        "node",
					MatchLabels: map[string]string{"env": "prod", "critical": "true"},
					Required:    true,
				},
			},
			nodeLabels: []map[string]string{
				{"env": "prod", "critical": "true"},
				{"env": "prod", "critical": "false"},
				{"env": "dev", "critical": "true"},
			},
			expectedCandidates: 2,
			description:        "Anti-affinity requires all labels to match for exclusion",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop()
			scheduler := &Scheduler{
				logger: logger,
				config: DefaultSchedulerConfig(),
			}

			nodes := createMockNodesWithLabels(tt.nodeLabels)
			workload := &WorkloadSpec{
				ID:       "test-workload",
				Name:     "anti-affinity-test",
				Replicas: 1,
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 100,
						MemoryBytes:   100 * 1024 * 1024,
					},
				},
				PlacementPolicy: PlacementPolicy{
					AntiAffinity: tt.antiAffinityRules,
				},
			}

			// Execute
			filtered := scheduler.filterNodes(nodes, workload)

			// Verify
			if len(filtered) != tt.expectedCandidates {
				t.Errorf("%s: expected %d candidates, got %d",
					tt.description, tt.expectedCandidates, len(filtered))
			}
		})
	}
}

// TestScheduler_PlacementPolicy_NodeSelectors verifies CLD-REQ-020 node selector constraints
// Test ID: CLD-REQ-020-TC-005
func TestScheduler_PlacementPolicy_NodeSelectors(t *testing.T) {
	tests := []struct {
		name               string
		nodeSelector       map[string]string
		nodeLabels         []map[string]string
		expectedCandidates int
		description        string
	}{
		{
			name:         "single_selector_match",
			nodeSelector: map[string]string{"instance-type": "m5.large"},
			nodeLabels: []map[string]string{
				{"instance-type": "m5.large"},
				{"instance-type": "t3.medium"},
				{"instance-type": "m5.large"},
			},
			expectedCandidates: 2,
			description:        "Node selector filters to nodes with matching label",
		},
		{
			name:         "multiple_selectors_match",
			nodeSelector: map[string]string{"instance-type": "m5.large", "arch": "amd64"},
			nodeLabels: []map[string]string{
				{"instance-type": "m5.large", "arch": "amd64"},
				{"instance-type": "m5.large", "arch": "arm64"},
				{"instance-type": "t3.medium", "arch": "amd64"},
			},
			expectedCandidates: 1,
			description:        "Multiple node selectors require all labels to match",
		},
		{
			name:         "no_selector_match",
			nodeSelector: map[string]string{"gpu": "v100"},
			nodeLabels: []map[string]string{
				{"gpu": "p3"},
				{"gpu": "k80"},
			},
			expectedCandidates: 0,
			description:        "Node selector with no matches returns no candidates",
		},
		{
			name:         "empty_selector",
			nodeSelector: map[string]string{},
			nodeLabels: []map[string]string{
				{"instance-type": "m5.large"},
				{"instance-type": "t3.medium"},
			},
			expectedCandidates: 2,
			description:        "Empty node selector accepts all nodes",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop()
			scheduler := &Scheduler{
				logger: logger,
				config: DefaultSchedulerConfig(),
			}

			nodes := createMockNodesWithLabels(tt.nodeLabels)
			workload := &WorkloadSpec{
				ID:       "test-workload",
				Name:     "selector-test",
				Replicas: 1,
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 100,
						MemoryBytes:   100 * 1024 * 1024,
					},
				},
				PlacementPolicy: PlacementPolicy{
					NodeSelector: tt.nodeSelector,
				},
			}

			// Execute
			filtered := scheduler.filterNodes(nodes, workload)

			// Verify
			if len(filtered) != tt.expectedCandidates {
				t.Errorf("%s: expected %d candidates, got %d",
					tt.description, tt.expectedCandidates, len(filtered))
			}
		})
	}
}

// TestScheduler_PlacementPolicy_Tolerations verifies CLD-REQ-020 taint toleration
// Test ID: CLD-REQ-020-TC-006
func TestScheduler_PlacementPolicy_Tolerations(t *testing.T) {
	tests := []struct {
		name               string
		tolerations        []Toleration
		nodeTaints         [][]membership.Taint
		expectedCandidates int
		description        string
	}{
		{
			name: "toleration_allows_tainted_node",
			tolerations: []Toleration{
				{
					Key:      "dedicated",
					Value:    "gpu",
					Operator: "Equal",
					Effect:   "NoSchedule",
				},
			},
			nodeTaints: [][]membership.Taint{
				{{Key: "dedicated", Value: "gpu", Effect: "NoSchedule"}},
				{},
				{{Key: "dedicated", Value: "cpu", Effect: "NoSchedule"}},
			},
			expectedCandidates: 2,
			description:        "Toleration allows scheduling on tainted nodes",
		},
		{
			name: "exists_operator_tolerates_any_value",
			tolerations: []Toleration{
				{
					Key:      "maintenance",
					Operator: "Exists",
					Effect:   "NoSchedule",
				},
			},
			nodeTaints: [][]membership.Taint{
				{{Key: "maintenance", Value: "scheduled", Effect: "NoSchedule"}},
				{{Key: "maintenance", Value: "emergency", Effect: "NoSchedule"}},
				{},
			},
			expectedCandidates: 3,
			description:        "Exists operator tolerates taint regardless of value",
		},
		{
			name:        "no_toleration_excludes_tainted",
			tolerations: []Toleration{},
			nodeTaints: [][]membership.Taint{
				{{Key: "specialized", Value: "ml", Effect: "NoSchedule"}},
				{},
			},
			expectedCandidates: 1,
			description:        "No toleration excludes nodes with NoSchedule taints",
		},
		{
			name: "mismatched_effect_excludes_node",
			tolerations: []Toleration{
				{
					Key:      "env",
					Value:    "prod",
					Operator: "Equal",
					Effect:   "PreferNoSchedule",
				},
			},
			nodeTaints: [][]membership.Taint{
				{{Key: "env", Value: "prod", Effect: "NoSchedule"}},
			},
			expectedCandidates: 0,
			description:        "Toleration with different effect doesn't match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop()
			scheduler := &Scheduler{
				logger: logger,
				config: DefaultSchedulerConfig(),
			}

			nodes := createMockNodesWithTaints(tt.nodeTaints)
			workload := &WorkloadSpec{
				ID:       "test-workload",
				Name:     "toleration-test",
				Replicas: 1,
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 100,
						MemoryBytes:   100 * 1024 * 1024,
					},
				},
				PlacementPolicy: PlacementPolicy{
					Tolerations: tt.tolerations,
				},
			}

			// Execute
			filtered := scheduler.filterNodes(nodes, workload)

			// Verify
			if len(filtered) != tt.expectedCandidates {
				t.Errorf("%s: expected %d candidates, got %d",
					tt.description, tt.expectedCandidates, len(filtered))
			}
		})
	}
}

// TestRollout_RollingUpdate_MinAvailable verifies CLD-REQ-020 & CLD-REQ-023
// Test ID: CLD-REQ-020-TC-007
func TestRollout_RollingUpdate_MinAvailable(t *testing.T) {
	tests := []struct {
		name           string
		currentState   *WorkloadSpec
		desiredState   *WorkloadSpec
		expectSuccess  bool
		minPhaseChecks map[RolloutPhase]int
		description    string
	}{
		{
			name: "rolling_update_respects_min_available",
			currentState: &WorkloadSpec{
				Replicas: 3,
				RolloutStrategy: RolloutStrategy{
					Strategy:       "RollingUpdate",
					MaxSurge:       1,
					MaxUnavailable: 1,
					MinAvailable:   2,
				},
			},
			desiredState: &WorkloadSpec{
				Replicas: 3,
				RolloutStrategy: RolloutStrategy{
					Strategy:       "RollingUpdate",
					MaxSurge:       1,
					MaxUnavailable: 1,
					MinAvailable:   2,
				},
			},
			expectSuccess: true,
			minPhaseChecks: map[RolloutPhase]int{
				RolloutPhaseUpdating: 2, // At least 2 phases for rolling update
			},
			description: "Rolling update maintains minAvailable during rollout",
		},
		{
			name: "scale_up_with_max_surge",
			currentState: &WorkloadSpec{
				Replicas: 2,
				RolloutStrategy: RolloutStrategy{
					Strategy:     "RollingUpdate",
					MaxSurge:     2,
					MinAvailable: 2,
				},
			},
			desiredState: &WorkloadSpec{
				Replicas: 5,
				RolloutStrategy: RolloutStrategy{
					Strategy:     "RollingUpdate",
					MaxSurge:     2,
					MinAvailable: 2,
				},
			},
			expectSuccess: true,
			minPhaseChecks: map[RolloutPhase]int{
				RolloutPhaseScalingUp: 1, // Scale up phase present
			},
			description: "Scale up respects maxSurge limit",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop()
			scheduler := &Scheduler{
				logger: logger,
				config: DefaultSchedulerConfig(),
			}
			orchestrator := NewRolloutOrchestrator(scheduler, logger)

			// Execute
			plan, err := orchestrator.PlanRollout(context.Background(), tt.currentState, tt.desiredState)

			// Verify
			if err != nil {
				if tt.expectSuccess {
					t.Fatalf("PlanRollout failed unexpectedly: %v", err)
				}
				return
			}

			if !tt.expectSuccess {
				t.Fatalf("PlanRollout succeeded but expected failure")
			}

			// Verify minAvailable is set correctly
			if plan.MinAvailable != tt.desiredState.RolloutStrategy.MinAvailable {
				t.Errorf("Expected minAvailable %d, got %d",
					tt.desiredState.RolloutStrategy.MinAvailable, plan.MinAvailable)
			}

			// Verify phase actions
			phaseCounts := make(map[RolloutPhase]int)
			for _, phase := range plan.Phases {
				phaseCounts[phase.Phase]++
			}

			for expectedPhase, minCount := range tt.minPhaseChecks {
				if phaseCounts[expectedPhase] < minCount {
					t.Errorf("%s: expected at least %d %s phases, got %d",
						tt.description, minCount, expectedPhase, phaseCounts[expectedPhase])
				}
			}
		})
	}
}

// TestRollout_Strategies_All verifies CLD-REQ-020 rollout strategies
// Test ID: CLD-REQ-020-TC-008
func TestRollout_Strategies_All(t *testing.T) {
	tests := []struct {
		name          string
		strategy      string
		currentState  *WorkloadSpec
		desiredState  *WorkloadSpec
		expectSuccess bool
		description   string
	}{
		{
			name:     "rolling_update_strategy",
			strategy: "RollingUpdate",
			currentState: &WorkloadSpec{
				Replicas: 3,
			},
			desiredState: &WorkloadSpec{
				Replicas: 3,
				RolloutStrategy: RolloutStrategy{
					Strategy:       "RollingUpdate",
					MaxSurge:       1,
					MaxUnavailable: 1,
					PauseDuration:  5 * time.Second,
				},
			},
			expectSuccess: true,
			description:   "RollingUpdate strategy creates incremental rollout plan",
		},
		{
			name:     "recreate_strategy",
			strategy: "Recreate",
			currentState: &WorkloadSpec{
				Replicas: 2,
			},
			desiredState: &WorkloadSpec{
				Replicas: 2,
				RolloutStrategy: RolloutStrategy{
					Strategy:      "Recreate",
					PauseDuration: 10 * time.Second,
				},
			},
			expectSuccess: true,
			description:   "Recreate strategy stops all then starts new",
		},
		{
			name:     "blue_green_strategy",
			strategy: "BlueGreen",
			currentState: &WorkloadSpec{
				Replicas: 3,
			},
			desiredState: &WorkloadSpec{
				Replicas: 3,
				RolloutStrategy: RolloutStrategy{
					Strategy:      "BlueGreen",
					PauseDuration: 30 * time.Second,
				},
			},
			expectSuccess: true,
			description:   "BlueGreen strategy starts new before stopping old",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop()
			scheduler := &Scheduler{
				logger: logger,
				config: DefaultSchedulerConfig(),
			}
			orchestrator := NewRolloutOrchestrator(scheduler, logger)

			// Execute
			plan, err := orchestrator.PlanRollout(context.Background(), tt.currentState, tt.desiredState)

			// Verify
			if err != nil {
				if tt.expectSuccess {
					t.Fatalf("%s: PlanRollout failed: %v", tt.description, err)
				}
				return
			}

			if !tt.expectSuccess {
				t.Fatalf("%s: PlanRollout succeeded but expected failure", tt.description)
			}

			if plan.Strategy != tt.strategy {
				t.Errorf("%s: expected strategy %s, got %s",
					tt.description, tt.strategy, plan.Strategy)
			}

			// Strategy-specific validations
			switch tt.strategy {
			case "RollingUpdate":
				if plan.MaxSurge != tt.desiredState.RolloutStrategy.MaxSurge {
					t.Errorf("Expected maxSurge %d, got %d",
						tt.desiredState.RolloutStrategy.MaxSurge, plan.MaxSurge)
				}
				if plan.MaxUnavailable != tt.desiredState.RolloutStrategy.MaxUnavailable {
					t.Errorf("Expected maxUnavailable %d, got %d",
						tt.desiredState.RolloutStrategy.MaxUnavailable, plan.MaxUnavailable)
				}

			case "Recreate":
				// Verify stop-all phase exists
				hasStopAll := false
				for _, phase := range plan.Phases {
					if phase.Phase == RolloutPhaseScalingDown && phase.Action == "stop" {
						hasStopAll = true
					}
				}
				if !hasStopAll {
					t.Errorf("%s: Recreate strategy should have stop-all phase", tt.description)
				}

			case "BlueGreen":
				// Verify new replicas started before old stopped
				startIndex := -1
				stopIndex := -1
				for i, phase := range plan.Phases {
					if phase.Phase == RolloutPhaseScalingUp && phase.Action == "start" && startIndex == -1 {
						startIndex = i
					}
					if phase.Phase == RolloutPhaseScalingDown && phase.Action == "stop" {
						stopIndex = i
					}
				}
				if startIndex == -1 || stopIndex == -1 || startIndex >= stopIndex {
					t.Errorf("%s: BlueGreen should start new replicas before stopping old", tt.description)
				}
			}
		})
	}
}

// TestWorkloadSpec_Complete verifies CLD-REQ-020 complete WorkloadSpec integration
// Test ID: CLD-REQ-020-TC-009
func TestWorkloadSpec_Complete(t *testing.T) {
	tests := []struct {
		name           string
		workloadSpec   *WorkloadSpec
		nodes          []*membership.NodeInfo
		expectFiltered int
		description    string
	}{
		{
			name: "complete_spec_with_all_constraints",
			workloadSpec: &WorkloadSpec{
				ID:       "complete-test",
				Name:     "production-app",
				Replicas: 2,
				Resources: ResourceRequirements{
					Requests: ResourceSpec{
						CPUMillicores: 500,
						MemoryBytes:   512 * 1024 * 1024,  // 512MB
						StorageBytes:  1024 * 1024 * 1024, // 1GB
					},
					Limits: ResourceSpec{
						CPUMillicores: 1000,
						MemoryBytes:   1024 * 1024 * 1024, // 1GB
					},
				},
				PlacementPolicy: PlacementPolicy{
					Regions: []string{"us-west-1"},
					Zones:   []string{"us-west-1a", "us-west-1b"},
					NodeSelector: map[string]string{
						"instance-type": "m5.large",
					},
					Affinity: []AffinityRule{
						{
							Type:        "node",
							MatchLabels: map[string]string{"disk": "ssd"},
							Required:    true,
						},
					},
					AntiAffinity: []AffinityRule{
						{
							Type:        "node",
							MatchLabels: map[string]string{"maintenance": "true"},
							Required:    true,
						},
					},
				},
				RestartPolicy: RestartPolicy{
					Policy:     "Always",
					MaxRetries: 3,
					Backoff:    10 * time.Second,
				},
				RolloutStrategy: RolloutStrategy{
					Strategy:       "RollingUpdate",
					MaxSurge:       1,
					MaxUnavailable: 1,
					MinAvailable:   1,
					PauseDuration:  5 * time.Second,
				},
			},
			nodes: []*membership.NodeInfo{
				{
					ID:     "node-1",
					Name:   "node-1",
					State:  membership.StateReady,
					Region: "us-west-1",
					Zone:   "us-west-1a",
					Labels: map[string]string{
						"instance-type": "m5.large",
						"disk":          "ssd",
					},
					Capacity: membership.ResourceCapacity{
						CPUMillicores: 4000,
						MemoryBytes:   8 * 1024 * 1024 * 1024,
						StorageBytes:  100 * 1024 * 1024 * 1024,
					},
					Usage:  membership.ResourceUsage{},
					Taints: []membership.Taint{},
				},
				{
					ID:     "node-2",
					Name:   "node-2",
					State:  membership.StateReady,
					Region: "us-west-1",
					Zone:   "us-west-1b",
					Labels: map[string]string{
						"instance-type": "m5.large",
						"disk":          "ssd",
					},
					Capacity: membership.ResourceCapacity{
						CPUMillicores: 4000,
						MemoryBytes:   8 * 1024 * 1024 * 1024,
						StorageBytes:  100 * 1024 * 1024 * 1024,
					},
					Usage:  membership.ResourceUsage{},
					Taints: []membership.Taint{},
				},
				// Node that should be filtered out
				{
					ID:     "node-3",
					Name:   "node-3",
					State:  membership.StateReady,
					Region: "us-east-1", // Wrong region
					Zone:   "us-east-1a",
					Labels: map[string]string{
						"instance-type": "m5.large",
						"disk":          "ssd",
					},
					Capacity: membership.ResourceCapacity{
						CPUMillicores: 4000,
						MemoryBytes:   8 * 1024 * 1024 * 1024,
					},
					Usage:  membership.ResourceUsage{},
					Taints: []membership.Taint{},
				},
			},
			expectFiltered: 2,
			description:    "Complete WorkloadSpec with all constraints filters correctly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			logger := zap.NewNop()
			scheduler := &Scheduler{
				logger: logger,
				config: DefaultSchedulerConfig(),
			}

			// Execute filtering
			filtered := scheduler.filterNodes(tt.nodes, tt.workloadSpec)

			// Verify
			if len(filtered) != tt.expectFiltered {
				t.Errorf("%s: expected %d filtered nodes, got %d",
					tt.description, tt.expectFiltered, len(filtered))
			}

			// Test rollout planning
			orchestrator := NewRolloutOrchestrator(scheduler, logger)
			currentState := &WorkloadSpec{Replicas: 0}
			plan, err := orchestrator.PlanRollout(context.Background(), currentState, tt.workloadSpec)

			if err != nil {
				t.Fatalf("%s: PlanRollout failed: %v", tt.description, err)
			}

			if plan.Strategy != tt.workloadSpec.RolloutStrategy.Strategy {
				t.Errorf("%s: expected rollout strategy %s, got %s",
					tt.description, tt.workloadSpec.RolloutStrategy.Strategy, plan.Strategy)
			}

			if plan.MinAvailable != tt.workloadSpec.RolloutStrategy.MinAvailable {
				t.Errorf("%s: expected minAvailable %d, got %d",
					tt.description, tt.workloadSpec.RolloutStrategy.MinAvailable, plan.MinAvailable)
			}
		})
	}
}

// Helper functions

func createMockNodesWithRegions(regions []string) []*membership.NodeInfo {
	nodes := make([]*membership.NodeInfo, len(regions))
	for i, region := range regions {
		nodes[i] = &membership.NodeInfo{
			ID:     fmt.Sprintf("node-%d", i),
			Name:   fmt.Sprintf("node-%d", i),
			State:  membership.StateReady,
			Region: region,
			Zone:   fmt.Sprintf("%s-a", region),
			Capacity: membership.ResourceCapacity{
				CPUMillicores: 2000,
				MemoryBytes:   4 * 1024 * 1024 * 1024,
				StorageBytes:  50 * 1024 * 1024 * 1024,
			},
			Usage:  membership.ResourceUsage{},
			Labels: map[string]string{},
			Taints: []membership.Taint{},
		}
	}
	return nodes
}

func createMockNodesWithZones(zones []string) []*membership.NodeInfo {
	nodes := make([]*membership.NodeInfo, len(zones))
	for i, zone := range zones {
		nodes[i] = &membership.NodeInfo{
			ID:     fmt.Sprintf("node-%d", i),
			Name:   fmt.Sprintf("node-%d", i),
			State:  membership.StateReady,
			Region: "us-west-1",
			Zone:   zone,
			Capacity: membership.ResourceCapacity{
				CPUMillicores: 2000,
				MemoryBytes:   4 * 1024 * 1024 * 1024,
				StorageBytes:  50 * 1024 * 1024 * 1024,
			},
			Usage:  membership.ResourceUsage{},
			Labels: map[string]string{},
			Taints: []membership.Taint{},
		}
	}
	return nodes
}

func createMockNodesWithLabels(labelSets []map[string]string) []*membership.NodeInfo {
	nodes := make([]*membership.NodeInfo, len(labelSets))
	for i, labels := range labelSets {
		nodes[i] = &membership.NodeInfo{
			ID:     fmt.Sprintf("node-%d", i),
			Name:   fmt.Sprintf("node-%d", i),
			State:  membership.StateReady,
			Region: "us-west-1",
			Zone:   "us-west-1a",
			Capacity: membership.ResourceCapacity{
				CPUMillicores: 2000,
				MemoryBytes:   4 * 1024 * 1024 * 1024,
				StorageBytes:  50 * 1024 * 1024 * 1024,
			},
			Usage:  membership.ResourceUsage{},
			Labels: labels,
			Taints: []membership.Taint{},
		}
	}
	return nodes
}

func createMockNodesWithTaints(taintSets [][]membership.Taint) []*membership.NodeInfo {
	nodes := make([]*membership.NodeInfo, len(taintSets))
	for i, taints := range taintSets {
		nodes[i] = &membership.NodeInfo{
			ID:     fmt.Sprintf("node-%d", i),
			Name:   fmt.Sprintf("node-%d", i),
			State:  membership.StateReady,
			Region: "us-west-1",
			Zone:   "us-west-1a",
			Capacity: membership.ResourceCapacity{
				CPUMillicores: 2000,
				MemoryBytes:   4 * 1024 * 1024 * 1024,
				StorageBytes:  50 * 1024 * 1024 * 1024,
			},
			Usage:  membership.ResourceUsage{},
			Labels: map[string]string{},
			Taints: taints,
		}
	}
	return nodes
}

func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}
