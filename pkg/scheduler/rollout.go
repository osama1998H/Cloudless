package scheduler

import (
	"context"
	"fmt"
	"time"

	"go.uber.org/zap"
)

// RolloutOrchestrator manages workload rollouts with minAvailable constraints
type RolloutOrchestrator struct {
	scheduler *Scheduler
	logger    *zap.Logger
}

// NewRolloutOrchestrator creates a new rollout orchestrator
func NewRolloutOrchestrator(scheduler *Scheduler, logger *zap.Logger) *RolloutOrchestrator {
	return &RolloutOrchestrator{
		scheduler: scheduler,
		logger:    logger,
	}
}

// RolloutState tracks the state of a rollout
type RolloutState struct {
	WorkloadID      string
	TotalReplicas   int
	ReadyReplicas   int
	UpdatedReplicas int
	OldReplicas     int
	Strategy        RolloutStrategy
	Phase           RolloutPhase
	StartTime       time.Time
}

// RolloutPhase represents the phase of a rollout
type RolloutPhase string

const (
	RolloutPhaseInitializing RolloutPhase = "Initializing"
	RolloutPhaseScalingUp    RolloutPhase = "ScalingUp"
	RolloutPhaseUpdating     RolloutPhase = "Updating"
	RolloutPhaseScalingDown  RolloutPhase = "ScalingDown"
	RolloutPhasePaused       RolloutPhase = "Paused"
	RolloutPhaseCompleted    RolloutPhase = "Completed"
	RolloutPhaseFailed       RolloutPhase = "Failed"
)

// PlanRollout creates a rollout plan that respects minAvailable constraints
func (ro *RolloutOrchestrator) PlanRollout(ctx context.Context, currentState, desiredState *WorkloadSpec) (*RolloutPlan, error) {
	strategy := desiredState.RolloutStrategy

	// Calculate minAvailable (default to desiredReplicas - maxUnavailable)
	minAvailable := strategy.MinAvailable
	if minAvailable == 0 {
		if strategy.MaxUnavailable > 0 {
			minAvailable = desiredState.Replicas - strategy.MaxUnavailable
		} else {
			// Default to keeping at least 1 replica available
			minAvailable = max(1, desiredState.Replicas-1)
		}
	}

	// Ensure minAvailable is within valid range
	minAvailable = max(0, min(minAvailable, desiredState.Replicas))

	ro.logger.Info("Planning rollout",
		zap.String("workload", desiredState.Name),
		zap.Int("current_replicas", currentState.Replicas),
		zap.Int("desired_replicas", desiredState.Replicas),
		zap.Int("min_available", minAvailable),
		zap.Int("max_surge", strategy.MaxSurge),
		zap.Int("max_unavailable", strategy.MaxUnavailable),
	)

	switch strategy.Strategy {
	case "RollingUpdate":
		return ro.planRollingUpdate(currentState, desiredState, minAvailable, strategy)
	case "Recreate":
		return ro.planRecreate(currentState, desiredState, minAvailable, strategy)
	case "BlueGreen":
		return ro.planBlueGreen(currentState, desiredState, minAvailable, strategy)
	default:
		return nil, fmt.Errorf("unsupported rollout strategy: %s", strategy.Strategy)
	}
}

// RolloutPlan represents a planned rollout with phases
type RolloutPlan struct {
	Strategy       string
	MinAvailable   int
	MaxSurge       int
	MaxUnavailable int
	PauseDuration  time.Duration
	Phases         []RolloutPhaseAction
}

// RolloutPhaseAction represents an action in a rollout phase
type RolloutPhaseAction struct {
	Phase          RolloutPhase
	Action         string // "start", "stop", "wait"
	ReplicaCount   int
	WaitDuration   time.Duration
	AllowedFailures int
}

// planRollingUpdate plans a rolling update rollout
func (ro *RolloutOrchestrator) planRollingUpdate(current, desired *WorkloadSpec, minAvailable int, strategy RolloutStrategy) (*RolloutPlan, error) {
	plan := &RolloutPlan{
		Strategy:       "RollingUpdate",
		MinAvailable:   minAvailable,
		MaxSurge:       strategy.MaxSurge,
		MaxUnavailable: strategy.MaxUnavailable,
		PauseDuration:  strategy.PauseDuration,
		Phases:         []RolloutPhaseAction{},
	}

	currentReplicas := current.Replicas
	desiredReplicas := desired.Replicas

	// Phase 1: Scale up if needed (respecting maxSurge and minAvailable)
	if desiredReplicas > currentReplicas {
		maxSurgeReplicas := strategy.MaxSurge
		if maxSurgeReplicas == 0 {
			maxSurgeReplicas = 1 // Default to 1
		}

		// Calculate how many new replicas we can start
		newReplicas := min(desiredReplicas-currentReplicas, maxSurgeReplicas)

		plan.Phases = append(plan.Phases, RolloutPhaseAction{
			Phase:        RolloutPhaseScalingUp,
			Action:       "start",
			ReplicaCount: newReplicas,
		})

		// Wait for new replicas to become ready
		plan.Phases = append(plan.Phases, RolloutPhaseAction{
			Phase:        RolloutPhaseScalingUp,
			Action:       "wait",
			WaitDuration: 30 * time.Second,
		})
	}

	// Phase 2: Rolling update (respecting minAvailable)
	if currentReplicas > 0 {
		maxUnavailable := strategy.MaxUnavailable
		if maxUnavailable == 0 {
			maxUnavailable = 1 // Default to 1
		}

		// Calculate how many old replicas we can stop at once
		// Ensure we never go below minAvailable
		batchSize := min(maxUnavailable, currentReplicas-minAvailable)
		if batchSize < 1 {
			batchSize = 1
		}

		batches := (currentReplicas + batchSize - 1) / batchSize

		for i := 0; i < batches; i++ {
			stopCount := min(batchSize, currentReplicas-(i*batchSize))

			// Stop old replicas
			plan.Phases = append(plan.Phases, RolloutPhaseAction{
				Phase:        RolloutPhaseUpdating,
				Action:       "stop",
				ReplicaCount: stopCount,
			})

			// Start new replicas
			plan.Phases = append(plan.Phases, RolloutPhaseAction{
				Phase:        RolloutPhaseUpdating,
				Action:       "start",
				ReplicaCount: stopCount,
			})

			// Wait for new replicas to become ready
			plan.Phases = append(plan.Phases, RolloutPhaseAction{
				Phase:        RolloutPhaseUpdating,
				Action:       "wait",
				WaitDuration: strategy.PauseDuration,
			})
		}
	}

	// Phase 3: Scale down if needed
	if desiredReplicas < currentReplicas {
		scaleDownCount := currentReplicas - desiredReplicas

		plan.Phases = append(plan.Phases, RolloutPhaseAction{
			Phase:        RolloutPhaseScalingDown,
			Action:       "stop",
			ReplicaCount: scaleDownCount,
		})
	}

	return plan, nil
}

// planRecreate plans a recreate rollout
func (ro *RolloutOrchestrator) planRecreate(current, desired *WorkloadSpec, minAvailable int, strategy RolloutStrategy) (*RolloutPlan, error) {
	plan := &RolloutPlan{
		Strategy:      "Recreate",
		MinAvailable:  minAvailable,
		PauseDuration: strategy.PauseDuration,
		Phases:        []RolloutPhaseAction{},
	}

	// WARNING: Recreate strategy cannot guarantee minAvailable
	// It stops all old replicas before starting new ones
	if minAvailable > 0 {
		ro.logger.Warn("Recreate strategy cannot guarantee minAvailable",
			zap.Int("min_available", minAvailable),
		)
	}

	// Phase 1: Stop all old replicas
	if current.Replicas > 0 {
		plan.Phases = append(plan.Phases, RolloutPhaseAction{
			Phase:        RolloutPhaseScalingDown,
			Action:       "stop",
			ReplicaCount: current.Replicas,
		})

		// Wait for old replicas to stop
		plan.Phases = append(plan.Phases, RolloutPhaseAction{
			Phase:        RolloutPhaseScalingDown,
			Action:       "wait",
			WaitDuration: 10 * time.Second,
		})
	}

	// Phase 2: Start all new replicas
	plan.Phases = append(plan.Phases, RolloutPhaseAction{
		Phase:        RolloutPhaseScalingUp,
		Action:       "start",
		ReplicaCount: desired.Replicas,
	})

	// Wait for new replicas to become ready
	plan.Phases = append(plan.Phases, RolloutPhaseAction{
		Phase:        RolloutPhaseScalingUp,
		Action:       "wait",
		WaitDuration: 30 * time.Second,
	})

	return plan, nil
}

// planBlueGreen plans a blue-green rollout
func (ro *RolloutOrchestrator) planBlueGreen(current, desired *WorkloadSpec, minAvailable int, strategy RolloutStrategy) (*RolloutPlan, error) {
	plan := &RolloutPlan{
		Strategy:      "BlueGreen",
		MinAvailable:  minAvailable,
		PauseDuration: strategy.PauseDuration,
		Phases:        []RolloutPhaseAction{},
	}

	// Phase 1: Start all new replicas (green deployment)
	plan.Phases = append(plan.Phases, RolloutPhaseAction{
		Phase:        RolloutPhaseScalingUp,
		Action:       "start",
		ReplicaCount: desired.Replicas,
	})

	// Wait for new replicas to become ready
	plan.Phases = append(plan.Phases, RolloutPhaseAction{
		Phase:        RolloutPhaseScalingUp,
		Action:       "wait",
		WaitDuration: 30 * time.Second,
	})

	// Phase 2: Pause for validation (optional)
	if strategy.PauseDuration > 0 {
		plan.Phases = append(plan.Phases, RolloutPhaseAction{
			Phase:        RolloutPhasePaused,
			Action:       "wait",
			WaitDuration: strategy.PauseDuration,
		})
	}

	// Phase 3: Stop all old replicas (blue deployment)
	if current.Replicas > 0 {
		plan.Phases = append(plan.Phases, RolloutPhaseAction{
			Phase:        RolloutPhaseScalingDown,
			Action:       "stop",
			ReplicaCount: current.Replicas,
		})
	}

	return plan, nil
}

// ValidateRollout validates that a rollout respects minAvailable constraints
func (ro *RolloutOrchestrator) ValidateRollout(state *RolloutState) error {
	minAvailable := state.Strategy.MinAvailable
	if minAvailable == 0 {
		return nil // No constraint
	}

	// Check if current ready replicas meet minAvailable
	if state.ReadyReplicas < minAvailable {
		return fmt.Errorf("rollout violates minAvailable constraint: ready=%d, min=%d",
			state.ReadyReplicas, minAvailable)
	}

	return nil
}

// CalculateMaxReplicasToStop calculates max replicas that can be stopped while respecting minAvailable
func (ro *RolloutOrchestrator) CalculateMaxReplicasToStop(totalReplicas, readyReplicas, minAvailable int) int {
	if minAvailable == 0 {
		return totalReplicas // No constraint
	}

	// Calculate how many replicas we can stop
	canStop := readyReplicas - minAvailable
	if canStop < 0 {
		return 0
	}

	return canStop
}

// Helper functions
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
