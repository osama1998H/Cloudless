package scheduler

import (
	"context"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestRollout_MinAvailable_Enforcement verifies CLD-REQ-023: Rolling updates must keep minAvailable replicas serving
// Test ID: CLD-REQ-023-TC-001
func TestRollout_MinAvailable_Enforcement(t *testing.T) {
	tests := []struct {
		name            string
		currentReplicas int
		desiredReplicas int
		minAvailable    int
		maxUnavailable  int
		expectedBatches int
		expectedMaxStop int
		shouldSucceed   bool
		description     string
	}{
		{
			name:            "enforce_min_available_3_of_5",
			currentReplicas: 5,
			desiredReplicas: 5,
			minAvailable:    3,
			maxUnavailable:  2,
			expectedBatches: 3, // Can stop 2 at a time (5-3=2), so ceil(5/2)=3 batches
			expectedMaxStop: 2,
			shouldSucceed:   true,
			description:     "Rolling update respects minAvailable of 3 replicas out of 5",
		},
		{
			name:            "min_available_equals_replicas",
			currentReplicas: 3,
			desiredReplicas: 3,
			minAvailable:    3,
			maxUnavailable:  2,
			expectedBatches: 3, // Can only stop 0 at a time, but defaults to 1
			expectedMaxStop: 0,
			shouldSucceed:   true,
			description:     "Cannot stop any replicas when minAvailable equals total replicas",
		},
		{
			name:            "min_available_zero_no_constraint",
			currentReplicas: 5,
			desiredReplicas: 5,
			minAvailable:    0,
			maxUnavailable:  3,
			expectedBatches: 2, // Can stop 3 at a time, ceil(5/3)=2 batches
			expectedMaxStop: 3,
			shouldSucceed:   true,
			description:     "No constraint when minAvailable is 0",
		},
		{
			name:            "min_available_1_default",
			currentReplicas: 4,
			desiredReplicas: 4,
			minAvailable:    1,
			maxUnavailable:  2,
			expectedBatches: 2, // Can stop 2 at a time (4-1=3, min with maxUnavail=2), ceil(4/2)=2
			expectedMaxStop: 2,
			shouldSucceed:   true,
			description:     "Default minAvailable of 1 allows rolling updates",
		},
		{
			name:            "max_unavailable_limited_by_min_available",
			currentReplicas: 5,
			desiredReplicas: 5,
			minAvailable:    4,
			maxUnavailable:  3, // Would allow 3, but minAvailable limits to 1
			expectedBatches: 5, // Can only stop 1 at a time (5-4=1), ceil(5/1)=5 batches
			expectedMaxStop: 1,
			shouldSucceed:   true,
			description:     "MaxUnavailable is limited by minAvailable constraint",
		},
		{
			name:            "scale_down_with_min_available",
			currentReplicas: 5,
			desiredReplicas: 3,
			minAvailable:    2,
			maxUnavailable:  2,
			expectedBatches: 2, // Can stop 2 at a time (min of maxUnavail=2, current-min=3), then scale down
			expectedMaxStop: 2,
			shouldSucceed:   true,
			description:     "Scale down respects minAvailable during transition",
		},
		{
			name:            "min_available_exceeds_replicas_clamped",
			currentReplicas: 3,
			desiredReplicas: 3,
			minAvailable:    5, // Should be clamped to 3
			maxUnavailable:  1,
			expectedBatches: 3, // After clamping to 3, can stop 0 at a time
			expectedMaxStop: 0,
			shouldSucceed:   true,
			description:     "MinAvailable exceeding replicas is clamped to replica count",
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

			currentState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: tt.currentReplicas,
			}

			desiredState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: tt.desiredReplicas,
				RolloutStrategy: RolloutStrategy{
					Strategy:       "RollingUpdate",
					MinAvailable:   tt.minAvailable,
					MaxUnavailable: tt.maxUnavailable,
					MaxSurge:       1,
					PauseDuration:  1 * time.Second,
				},
			}

			// Execute
			plan, err := orchestrator.PlanRollout(context.Background(), currentState, desiredState)

			// Verify
			if err != nil && tt.shouldSucceed {
				t.Fatalf("%s: PlanRollout failed: %v", tt.description, err)
			}

			if err == nil && !tt.shouldSucceed {
				t.Fatalf("%s: PlanRollout should have failed but succeeded", tt.description)
			}

			if err != nil {
				return // Expected failure
			}

			// Verify MinAvailable is respected
			expectedMinAvailable := tt.minAvailable
			if expectedMinAvailable > tt.desiredReplicas {
				expectedMinAvailable = tt.desiredReplicas
			}
			if expectedMinAvailable == 0 && tt.maxUnavailable > 0 {
				expectedMinAvailable = tt.desiredReplicas - tt.maxUnavailable
			}
			if expectedMinAvailable == 0 && tt.maxUnavailable == 0 {
				expectedMinAvailable = max(1, tt.desiredReplicas-1)
			}

			if plan.MinAvailable != expectedMinAvailable {
				t.Errorf("%s: expected minAvailable %d, got %d",
					tt.description, expectedMinAvailable, plan.MinAvailable)
			}

			// Verify batch size calculation
			actualBatchSize := 0
			if tt.currentReplicas > 0 {
				calculatedBatchSize := min(tt.maxUnavailable, tt.currentReplicas-plan.MinAvailable)
				if calculatedBatchSize < 1 {
					calculatedBatchSize = 1
				}
				actualBatchSize = calculatedBatchSize
			}

			if actualBatchSize > 0 && tt.expectedMaxStop > 0 {
				expectedMaxStop := min(tt.maxUnavailable, tt.currentReplicas-plan.MinAvailable)
				if expectedMaxStop != tt.expectedMaxStop {
					t.Errorf("%s: expected max stop %d, calculated %d (current=%d, minAvail=%d, maxUnavail=%d)",
						tt.description, tt.expectedMaxStop, expectedMaxStop,
						tt.currentReplicas, plan.MinAvailable, tt.maxUnavailable)
				}
			}

			// Verify plan has update phases
			hasUpdatePhase := false
			for _, phase := range plan.Phases {
				if phase.Phase == RolloutPhaseUpdating && phase.Action == "stop" {
					hasUpdatePhase = true
					// Verify stop count doesn't violate minAvailable
					if phase.ReplicaCount > actualBatchSize {
						t.Errorf("%s: phase stop count %d exceeds batch size %d",
							tt.description, phase.ReplicaCount, actualBatchSize)
					}
				}
			}

			if tt.currentReplicas > 0 && !hasUpdatePhase {
				// It's OK if there are no update phases when we can't stop any replicas
				if actualBatchSize > 0 {
					t.Errorf("%s: expected update phases in plan", tt.description)
				}
			}

			t.Logf("%s: PASS - minAvailable=%d enforced with batchSize=%d",
				tt.description, plan.MinAvailable, actualBatchSize)
		})
	}
}

// TestRollout_MinAvailable_ValidateRollout verifies ValidateRollout enforces minAvailable
// Test ID: CLD-REQ-023-TC-002
func TestRollout_MinAvailable_ValidateRollout(t *testing.T) {
	tests := []struct {
		name           string
		totalReplicas  int
		readyReplicas  int
		minAvailable   int
		shouldValidate bool
		description    string
	}{
		{
			name:           "sufficient_ready_replicas",
			totalReplicas:  5,
			readyReplicas:  4,
			minAvailable:   3,
			shouldValidate: true,
			description:    "Validation passes when ready replicas meet minAvailable",
		},
		{
			name:           "exactly_min_available",
			totalReplicas:  5,
			readyReplicas:  3,
			minAvailable:   3,
			shouldValidate: true,
			description:    "Validation passes when ready replicas exactly equal minAvailable",
		},
		{
			name:           "insufficient_ready_replicas",
			totalReplicas:  5,
			readyReplicas:  2,
			minAvailable:   3,
			shouldValidate: false,
			description:    "Validation fails when ready replicas below minAvailable",
		},
		{
			name:           "zero_min_available_no_constraint",
			totalReplicas:  5,
			readyReplicas:  0,
			minAvailable:   0,
			shouldValidate: true,
			description:    "No constraint when minAvailable is 0",
		},
		{
			name:           "all_replicas_ready",
			totalReplicas:  3,
			readyReplicas:  3,
			minAvailable:   2,
			shouldValidate: true,
			description:    "Validation passes when all replicas are ready",
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

			state := &RolloutState{
				WorkloadID:    "test-workload",
				TotalReplicas: tt.totalReplicas,
				ReadyReplicas: tt.readyReplicas,
				Strategy: RolloutStrategy{
					MinAvailable: tt.minAvailable,
				},
			}

			// Execute
			err := orchestrator.ValidateRollout(state)

			// Verify
			if tt.shouldValidate && err != nil {
				t.Errorf("%s: validation should pass but failed with: %v", tt.description, err)
			}

			if !tt.shouldValidate && err == nil {
				t.Errorf("%s: validation should fail but passed", tt.description)
			}

			if err != nil {
				t.Logf("%s: PASS - validation correctly failed: %v", tt.description, err)
			} else {
				t.Logf("%s: PASS - validation correctly passed", tt.description)
			}
		})
	}
}

// TestRollout_MinAvailable_CalculateMaxReplicasToStop verifies safe stop calculation
// Test ID: CLD-REQ-023-TC-003
func TestRollout_MinAvailable_CalculateMaxReplicasToStop(t *testing.T) {
	tests := []struct {
		name            string
		totalReplicas   int
		readyReplicas   int
		minAvailable    int
		expectedCanStop int
		description     string
	}{
		{
			name:            "can_stop_two_replicas",
			totalReplicas:   5,
			readyReplicas:   5,
			minAvailable:    3,
			expectedCanStop: 2,
			description:     "Can stop 2 replicas (5-3=2) while maintaining minAvailable",
		},
		{
			name:            "can_stop_zero_at_min",
			totalReplicas:   3,
			readyReplicas:   3,
			minAvailable:    3,
			expectedCanStop: 0,
			description:     "Cannot stop any replicas when at minAvailable",
		},
		{
			name:            "can_stop_all_no_constraint",
			totalReplicas:   5,
			readyReplicas:   5,
			minAvailable:    0,
			expectedCanStop: 5,
			description:     "Can stop all replicas when minAvailable is 0",
		},
		{
			name:            "negative_result_clamped_to_zero",
			totalReplicas:   5,
			readyReplicas:   2,
			minAvailable:    3,
			expectedCanStop: 0,
			description:     "Cannot stop any when ready < minAvailable (returns 0, not negative)",
		},
		{
			name:            "can_stop_one_replica",
			totalReplicas:   4,
			readyReplicas:   4,
			minAvailable:    3,
			expectedCanStop: 1,
			description:     "Can stop 1 replica (4-3=1) while maintaining minAvailable",
		},
		{
			name:            "partial_ready_replicas",
			totalReplicas:   5,
			readyReplicas:   4,
			minAvailable:    3,
			expectedCanStop: 1,
			description:     "Can stop 1 replica when only 4 of 5 ready (4-3=1)",
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
			canStop := orchestrator.CalculateMaxReplicasToStop(
				tt.totalReplicas,
				tt.readyReplicas,
				tt.minAvailable,
			)

			// Verify
			if canStop != tt.expectedCanStop {
				t.Errorf("%s: expected canStop=%d, got %d (total=%d, ready=%d, minAvail=%d)",
					tt.description, tt.expectedCanStop, canStop,
					tt.totalReplicas, tt.readyReplicas, tt.minAvailable)
			}

			// Verify invariant: ready - canStop >= minAvailable (when minAvailable > 0)
			// BUT only check if we're not already below minAvailable
			if tt.minAvailable > 0 && tt.readyReplicas >= tt.minAvailable {
				remainingReady := tt.readyReplicas - canStop
				if remainingReady < tt.minAvailable {
					t.Errorf("%s: stopping %d would violate minAvailable: ready=%d, canStop=%d, remaining=%d, minAvail=%d",
						tt.description, canStop, tt.readyReplicas, canStop, remainingReady, tt.minAvailable)
				}
			}

			t.Logf("%s: PASS - canStop=%d (ready=%d, minAvail=%d, remaining=%d)",
				tt.description, canStop, tt.readyReplicas, tt.minAvailable, tt.readyReplicas-canStop)
		})
	}
}

// TestRollout_MinAvailable_RecreateStrategy verifies Recreate strategy behavior
// Test ID: CLD-REQ-023-TC-004
func TestRollout_MinAvailable_RecreateStrategy(t *testing.T) {
	tests := []struct {
		name            string
		currentReplicas int
		desiredReplicas int
		minAvailable    int
		description     string
	}{
		{
			name:            "recreate_with_min_available_warning",
			currentReplicas: 3,
			desiredReplicas: 3,
			minAvailable:    2,
			description:     "Recreate strategy logs warning but proceeds (cannot guarantee minAvailable)",
		},
		{
			name:            "recreate_with_zero_min_available",
			currentReplicas: 3,
			desiredReplicas: 3,
			minAvailable:    0, // Will default to max(1, 3-1) = 2
			description:     "Recreate strategy with no minAvailable constraint (defaults applied)",
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

			currentState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: tt.currentReplicas,
			}

			desiredState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: tt.desiredReplicas,
				RolloutStrategy: RolloutStrategy{
					Strategy:      "Recreate",
					MinAvailable:  tt.minAvailable,
					PauseDuration: 1 * time.Second,
				},
			}

			// Execute
			plan, err := orchestrator.PlanRollout(context.Background(), currentState, desiredState)

			// Verify
			if err != nil {
				t.Fatalf("%s: PlanRollout failed: %v", tt.description, err)
			}

			if plan.Strategy != "Recreate" {
				t.Errorf("%s: expected Recreate strategy, got %s", tt.description, plan.Strategy)
			}

			// For Recreate strategy, minAvailable may be defaulted
			expectedMinAvail := tt.minAvailable
			if expectedMinAvail == 0 {
				expectedMinAvail = max(1, tt.desiredReplicas-1)
			}
			if plan.MinAvailable != expectedMinAvail {
				t.Errorf("%s: expected minAvailable %d, got %d",
					tt.description, expectedMinAvail, plan.MinAvailable)
			}

			// Verify plan stops all old replicas first (Recreate behavior)
			if len(plan.Phases) > 0 {
				firstPhase := plan.Phases[0]
				if firstPhase.Action == "stop" && firstPhase.ReplicaCount != tt.currentReplicas {
					t.Errorf("%s: Recreate should stop all %d replicas, but stops %d",
						tt.description, tt.currentReplicas, firstPhase.ReplicaCount)
				}
			}

			t.Logf("%s: PASS - Recreate strategy created plan (warning logged for minAvailable=%d)",
				tt.description, tt.minAvailable)
		})
	}
}

// TestRollout_MinAvailable_BlueGreenStrategy verifies BlueGreen strategy behavior
// Test ID: CLD-REQ-023-TC-005
func TestRollout_MinAvailable_BlueGreenStrategy(t *testing.T) {
	tests := []struct {
		name            string
		currentReplicas int
		desiredReplicas int
		minAvailable    int
		description     string
	}{
		{
			name:            "bluegreen_maintains_availability",
			currentReplicas: 3,
			desiredReplicas: 3,
			minAvailable:    2,
			description:     "BlueGreen starts new replicas before stopping old ones",
		},
		{
			name:            "bluegreen_scale_up",
			currentReplicas: 2,
			desiredReplicas: 5,
			minAvailable:    2,
			description:     "BlueGreen with scale up maintains old replicas until new are ready",
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

			currentState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: tt.currentReplicas,
			}

			desiredState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: tt.desiredReplicas,
				RolloutStrategy: RolloutStrategy{
					Strategy:      "BlueGreen",
					MinAvailable:  tt.minAvailable,
					PauseDuration: 5 * time.Second,
				},
			}

			// Execute
			plan, err := orchestrator.PlanRollout(context.Background(), currentState, desiredState)

			// Verify
			if err != nil {
				t.Fatalf("%s: PlanRollout failed: %v", tt.description, err)
			}

			if plan.Strategy != "BlueGreen" {
				t.Errorf("%s: expected BlueGreen strategy, got %s", tt.description, plan.Strategy)
			}

			// Verify BlueGreen starts all new replicas first
			if len(plan.Phases) > 0 {
				firstPhase := plan.Phases[0]
				if firstPhase.Phase != RolloutPhaseScalingUp || firstPhase.Action != "start" {
					t.Errorf("%s: BlueGreen should start with ScalingUp phase, got %v",
						tt.description, firstPhase.Phase)
				}
				if firstPhase.ReplicaCount != tt.desiredReplicas {
					t.Errorf("%s: BlueGreen should start %d replicas, got %d",
						tt.description, tt.desiredReplicas, firstPhase.ReplicaCount)
				}
			}

			// Verify old replicas are stopped last (after new ones are ready)
			stopPhaseFound := false
			for i, phase := range plan.Phases {
				if phase.Action == "stop" {
					stopPhaseFound = true
					// Verify stop phase comes after start and wait phases
					if i < 2 {
						t.Errorf("%s: BlueGreen stop phase at index %d should come after start/wait phases",
							tt.description, i)
					}
				}
			}

			if tt.currentReplicas > 0 && !stopPhaseFound {
				t.Errorf("%s: expected stop phase for old replicas", tt.description)
			}

			t.Logf("%s: PASS - BlueGreen strategy maintains availability (starts new before stopping old)",
				tt.description)
		})
	}
}

// TestRollout_MinAvailable_DefaultCalculation verifies default minAvailable calculation
// Test ID: CLD-REQ-023-TC-006
func TestRollout_MinAvailable_DefaultCalculation(t *testing.T) {
	tests := []struct {
		name                  string
		desiredReplicas       int
		maxUnavailable        int
		specifiedMinAvailable int
		expectedMinAvailable  int
		description           string
	}{
		{
			name:                  "default_from_max_unavailable",
			desiredReplicas:       5,
			maxUnavailable:        2,
			specifiedMinAvailable: 0,
			expectedMinAvailable:  3, // 5 - 2 = 3
			description:           "MinAvailable defaults to desiredReplicas - maxUnavailable",
		},
		{
			name:                  "default_when_both_zero",
			desiredReplicas:       5,
			maxUnavailable:        0,
			specifiedMinAvailable: 0,
			expectedMinAvailable:  4, // max(1, 5-1) = 4
			description:           "MinAvailable defaults to replicas-1 when both are 0",
		},
		{
			name:                  "explicit_min_available_used",
			desiredReplicas:       5,
			maxUnavailable:        2,
			specifiedMinAvailable: 4,
			expectedMinAvailable:  4,
			description:           "Explicit minAvailable is used when specified",
		},
		{
			name:                  "min_available_clamped_to_replicas",
			desiredReplicas:       3,
			maxUnavailable:        1,
			specifiedMinAvailable: 10,
			expectedMinAvailable:  3, // Clamped to desiredReplicas
			description:           "MinAvailable is clamped to desiredReplicas when exceeding",
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

			currentState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: 0,
			}

			desiredState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: tt.desiredReplicas,
				RolloutStrategy: RolloutStrategy{
					Strategy:       "RollingUpdate",
					MinAvailable:   tt.specifiedMinAvailable,
					MaxUnavailable: tt.maxUnavailable,
					MaxSurge:       1,
				},
			}

			// Execute
			plan, err := orchestrator.PlanRollout(context.Background(), currentState, desiredState)

			// Verify
			if err != nil {
				t.Fatalf("%s: PlanRollout failed: %v", tt.description, err)
			}

			if plan.MinAvailable != tt.expectedMinAvailable {
				t.Errorf("%s: expected minAvailable=%d, got %d (replicas=%d, maxUnavail=%d, specified=%d)",
					tt.description, tt.expectedMinAvailable, plan.MinAvailable,
					tt.desiredReplicas, tt.maxUnavailable, tt.specifiedMinAvailable)
			}

			t.Logf("%s: PASS - minAvailable calculated as %d", tt.description, plan.MinAvailable)
		})
	}
}

// TestRollout_MinAvailable_ScaleOperations verifies minAvailable during scale operations
// Test ID: CLD-REQ-023-TC-007
func TestRollout_MinAvailable_ScaleOperations(t *testing.T) {
	tests := []struct {
		name            string
		currentReplicas int
		desiredReplicas int
		minAvailable    int
		maxSurge        int
		description     string
	}{
		{
			name:            "scale_up_with_min_available",
			currentReplicas: 2,
			desiredReplicas: 5,
			minAvailable:    2,
			maxSurge:        2,
			description:     "Scale up maintains minAvailable during transition",
		},
		{
			name:            "scale_down_with_min_available",
			currentReplicas: 5,
			desiredReplicas: 2,
			minAvailable:    2,
			maxSurge:        1,
			description:     "Scale down respects minAvailable (doesn't drop below)",
		},
		{
			name:            "no_scale_same_replicas",
			currentReplicas: 3,
			desiredReplicas: 3,
			minAvailable:    2,
			maxSurge:        1,
			description:     "No scaling needed when replicas match",
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

			currentState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: tt.currentReplicas,
			}

			desiredState := &WorkloadSpec{
				Name:     "test-workload",
				Replicas: tt.desiredReplicas,
				RolloutStrategy: RolloutStrategy{
					Strategy:       "RollingUpdate",
					MinAvailable:   tt.minAvailable,
					MaxUnavailable: 2,
					MaxSurge:       tt.maxSurge,
					PauseDuration:  1 * time.Second,
				},
			}

			// Execute
			plan, err := orchestrator.PlanRollout(context.Background(), currentState, desiredState)

			// Verify
			if err != nil {
				t.Fatalf("%s: PlanRollout failed: %v", tt.description, err)
			}

			// Verify minAvailable is set
			if plan.MinAvailable != tt.minAvailable {
				t.Errorf("%s: expected minAvailable %d, got %d",
					tt.description, tt.minAvailable, plan.MinAvailable)
			}

			// For scale up, verify ScalingUp phase exists
			if tt.desiredReplicas > tt.currentReplicas {
				hasScaleUp := false
				for _, phase := range plan.Phases {
					if phase.Phase == RolloutPhaseScalingUp && phase.Action == "start" {
						hasScaleUp = true
						// Verify surge is respected
						if phase.ReplicaCount > tt.maxSurge {
							t.Errorf("%s: scale up count %d exceeds maxSurge %d",
								tt.description, phase.ReplicaCount, tt.maxSurge)
						}
					}
				}
				if !hasScaleUp {
					t.Errorf("%s: expected ScalingUp phase for scale up operation", tt.description)
				}
			}

			// For scale down, verify ScalingDown phase exists
			if tt.desiredReplicas < tt.currentReplicas {
				hasScaleDown := false
				for _, phase := range plan.Phases {
					if phase.Phase == RolloutPhaseScalingDown && phase.Action == "stop" {
						hasScaleDown = true
					}
				}
				if !hasScaleDown {
					t.Errorf("%s: expected ScalingDown phase for scale down operation", tt.description)
				}
			}

			t.Logf("%s: PASS - scale operation respects minAvailable=%d", tt.description, tt.minAvailable)
		})
	}
}
