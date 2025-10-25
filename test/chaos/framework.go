// +build chaos

package chaos

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"go.uber.org/zap"
)

// ChaosScenario represents a chaos testing scenario
type ChaosScenario interface {
	Name() string
	Setup(ctx context.Context) error
	Execute(ctx context.Context) error
	Verify(ctx context.Context) error
	Teardown(ctx context.Context) error
}

// ChaosConfig holds configuration for chaos testing
type ChaosConfig struct {
	// Churn settings
	NodeChurnRate    float64       // Nodes per minute to add/remove
	ChurnDuration    time.Duration // How long to run churn

	// Network settings
	PacketLossRate   float64       // Percentage of packets to drop (0-1)
	NetworkLatency   time.Duration // Additional latency to inject
	PartitionDuration time.Duration // How long to maintain partition

	// Bandwidth settings
	BandwidthLimitKbps int // Bandwidth limit in Kbps

	// General settings
	RandomSeed       int64
	VerificationWait time.Duration // Time to wait before verification
}

// ChaosRunner runs chaos scenarios
type ChaosRunner struct {
	config ChaosConfig
	logger *zap.Logger
	rand   *rand.Rand
}

// NewChaosRunner creates a new chaos runner
func NewChaosRunner(config ChaosConfig, logger *zap.Logger) *ChaosRunner {
	seed := config.RandomSeed
	if seed == 0 {
		seed = time.Now().UnixNano()
	}

	return &ChaosRunner{
		config: config,
		logger: logger,
		rand:   rand.New(rand.NewSource(seed)),
	}
}

// RunScenario runs a chaos scenario
func (cr *ChaosRunner) RunScenario(ctx context.Context, scenario ChaosScenario) error {
	cr.logger.Info("Starting chaos scenario",
		zap.String("scenario", scenario.Name()),
	)

	// Setup
	cr.logger.Info("Setting up scenario")
	if err := scenario.Setup(ctx); err != nil {
		return fmt.Errorf("setup failed: %w", err)
	}

	// Execute chaos
	cr.logger.Info("Executing chaos")
	if err := scenario.Execute(ctx); err != nil {
		cr.logger.Error("Chaos execution failed", zap.Error(err))
		// Continue to verification even if chaos fails
	}

	// Wait for system to stabilize
	if cr.config.VerificationWait > 0 {
		cr.logger.Info("Waiting for system stabilization",
			zap.Duration("wait", cr.config.VerificationWait),
		)
		time.Sleep(cr.config.VerificationWait)
	}

	// Verify system invariants
	cr.logger.Info("Verifying system invariants")
	if err := scenario.Verify(ctx); err != nil {
		// Don't return early - still need to teardown
		cr.logger.Error("Verification failed", zap.Error(err))
		// Teardown
		if teardownErr := scenario.Teardown(ctx); teardownErr != nil {
			cr.logger.Error("Teardown failed", zap.Error(teardownErr))
		}
		return fmt.Errorf("verification failed: %w", err)
	}

	// Teardown
	cr.logger.Info("Tearing down scenario")
	if err := scenario.Teardown(ctx); err != nil {
		return fmt.Errorf("teardown failed: %w", err)
	}

	cr.logger.Info("Chaos scenario completed successfully",
		zap.String("scenario", scenario.Name()),
	)

	return nil
}

// SelectRandomNodes selects N random nodes from a list
func (cr *ChaosRunner) SelectRandomNodes(nodes []string, count int) []string {
	if count >= len(nodes) {
		return nodes
	}

	// Shuffle and take first N
	shuffled := make([]string, len(nodes))
	copy(shuffled, nodes)

	cr.rand.Shuffle(len(shuffled), func(i, j int) {
		shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
	})

	return shuffled[:count]
}

// RandomDuration returns a random duration between min and max
func (cr *ChaosRunner) RandomDuration(min, max time.Duration) time.Duration {
	diff := int64(max - min)
	if diff <= 0 {
		return min
	}
	return min + time.Duration(cr.rand.Int63n(diff))
}

// RandomInt returns a random integer between min and max (inclusive)
func (cr *ChaosRunner) RandomInt(min, max int) int {
	if min >= max {
		return min
	}
	return min + cr.rand.Intn(max-min+1)
}

// RandomFloat returns a random float between 0 and 1
func (cr *ChaosRunner) RandomFloat() float64 {
	return cr.rand.Float64()
}

// SystemInvariant represents a system property that should always hold
type SystemInvariant struct {
	Name        string
	Description string
	Check       func(ctx context.Context) error
}

// VerifyInvariants checks a list of system invariants
func VerifyInvariants(ctx context.Context, invariants []SystemInvariant, logger *zap.Logger) error {
	failed := false

	for _, inv := range invariants {
		logger.Info("Checking invariant",
			zap.String("name", inv.Name),
			zap.String("description", inv.Description),
		)

		if err := inv.Check(ctx); err != nil {
			logger.Error("Invariant check failed",
				zap.String("name", inv.Name),
				zap.Error(err),
			)
			failed = true
		} else {
			logger.Info("Invariant check passed",
				zap.String("name", inv.Name),
			)
		}
	}

	if failed {
		return fmt.Errorf("one or more invariants failed")
	}

	return nil
}

// ChaosMetrics tracks metrics during chaos testing
type ChaosMetrics struct {
	StartTime          time.Time
	EndTime            time.Time
	NodesAffected      int
	WorkloadsAffected  int
	RescheduleCount    int
	FailoverTime       time.Duration
	RecoveryTime       time.Duration
	DataLoss           bool
	AvailabilityDrop   float64 // Percentage
}

// MetricsCollector collects metrics during chaos tests
type MetricsCollector struct {
	metrics ChaosMetrics
	logger  *zap.Logger
}

// NewMetricsCollector creates a new metrics collector
func NewMetricsCollector(logger *zap.Logger) *MetricsCollector {
	return &MetricsCollector{
		metrics: ChaosMetrics{
			StartTime: time.Now(),
		},
		logger: logger,
	}
}

// RecordNodeAffected records that a node was affected
func (mc *MetricsCollector) RecordNodeAffected() {
	mc.metrics.NodesAffected++
}

// RecordWorkloadAffected records that a workload was affected
func (mc *MetricsCollector) RecordWorkloadAffected() {
	mc.metrics.WorkloadsAffected++
}

// RecordReschedule records that a reschedule occurred
func (mc *MetricsCollector) RecordReschedule() {
	mc.metrics.RescheduleCount++
}

// RecordFailoverTime records the time taken for failover
func (mc *MetricsCollector) RecordFailoverTime(duration time.Duration) {
	mc.metrics.FailoverTime = duration
}

// RecordRecoveryTime records the time taken for recovery
func (mc *MetricsCollector) RecordRecoveryTime(duration time.Duration) {
	mc.metrics.RecoveryTime = duration
}

// RecordDataLoss records that data loss occurred
func (mc *MetricsCollector) RecordDataLoss() {
	mc.metrics.DataLoss = true
}

// RecordAvailabilityDrop records the availability drop percentage
func (mc *MetricsCollector) RecordAvailabilityDrop(percentage float64) {
	mc.metrics.AvailabilityDrop = percentage
}

// Finalize finalizes the metrics collection
func (mc *MetricsCollector) Finalize() ChaosMetrics {
	mc.metrics.EndTime = time.Now()
	return mc.metrics
}

// Report generates a human-readable report of the metrics
func (mc *MetricsCollector) Report() string {
	m := mc.metrics
	duration := m.EndTime.Sub(m.StartTime)

	report := fmt.Sprintf(`
Chaos Test Metrics Report
=========================
Duration: %v
Nodes Affected: %d
Workloads Affected: %d
Reschedule Count: %d
Failover Time: %v
Recovery Time: %v
Data Loss: %v
Availability Drop: %.2f%%
`,
		duration,
		m.NodesAffected,
		m.WorkloadsAffected,
		m.RescheduleCount,
		m.FailoverTime,
		m.RecoveryTime,
		m.DataLoss,
		m.AvailabilityDrop,
	)

	return report
}
