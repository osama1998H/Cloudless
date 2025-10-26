// Package agent implements the Cloudless node agent
package agent

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cloudless/cloudless/pkg/runtime"
	"go.uber.org/zap"
)

// HealthMonitor monitors probe results and triggers actions (restart, reporting)
//
// This implements CLD-REQ-032: Health probes drive automated restart and traffic gating.
// The monitor runs a background loop that:
// - Checks liveness probe failures and triggers container restarts
// - Detects readiness changes and reports them for traffic gating
// - Enforces restart rate limiting to prevent flapping
//
// Concurrency Safety: All methods are safe for concurrent use.
type HealthMonitor struct {
	probeExecutor *runtime.ProbeExecutor
	runtime       runtime.Runtime
	logger        *zap.Logger

	// Container tracking with probe configs
	mu                sync.RWMutex
	containers        map[string]*containerProbeConfig // containerID -> probe configs
	restartAttempts   map[string]int                   // containerID -> restart count
	lastRestartTime   map[string]time.Time             // containerID -> last restart time
	previousReadiness map[string]bool                  // containerID -> previous readiness status

	// Configuration
	maxRestarts   int
	restartWindow time.Duration
	checkInterval time.Duration

	// Callbacks for reporting health events
	onLivenessFailure func(containerID string, consecutiveFailures int)
	onReadinessChange func(containerID string, healthy bool)
}

// containerProbeConfig stores probe configurations for a container
type containerProbeConfig struct {
	containerID     string
	livenessConfig  *runtime.ProbeConfig
	readinessConfig *runtime.ProbeConfig
}

// NewHealthMonitor creates a new health monitor
//
// Parameters:
//   - executor: ProbeExecutor that performs health checks
//   - rt: Container runtime for restart operations
//   - logger: Structured logger
//
// Returns a HealthMonitor with default configuration:
//   - maxRestarts: 5 restarts per restart window
//   - restartWindow: 5 minutes
//   - checkInterval: 5 seconds between health checks
func NewHealthMonitor(
	executor *runtime.ProbeExecutor,
	rt runtime.Runtime,
	logger *zap.Logger,
) *HealthMonitor {
	return &HealthMonitor{
		probeExecutor:     executor,
		runtime:           rt,
		logger:            logger.Named("health_monitor"),
		containers:        make(map[string]*containerProbeConfig),
		restartAttempts:   make(map[string]int),
		lastRestartTime:   make(map[string]time.Time),
		previousReadiness: make(map[string]bool),
		maxRestarts:       5,
		restartWindow:     5 * time.Minute,
		checkInterval:     5 * time.Second,
	}
}

// Start begins monitoring health probes
//
// This method blocks until the context is cancelled. It should be run in a goroutine.
// The monitor periodically checks probe results and takes action on failures.
//
// Context Handling: Respects ctx.Done() for graceful shutdown (per SOP §5.5)
func (hm *HealthMonitor) Start(ctx context.Context) {
	hm.logger.Info("Starting health monitor",
		zap.Duration("check_interval", hm.checkInterval),
		zap.Int("max_restarts", hm.maxRestarts),
		zap.Duration("restart_window", hm.restartWindow),
	)

	ticker := time.NewTicker(hm.checkInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			hm.logger.Info("Health monitor stopping", zap.Error(ctx.Err()))
			return

		case <-ticker.C:
			hm.checkProbeHealth(ctx)
		}
	}
}

// checkProbeHealth evaluates all probe results and takes action
//
// This is the main monitoring loop that:
// 1. Fetches probe results for each tracked container
// 2. Handles liveness failures (triggers restart)
// 3. Handles readiness changes (triggers callback for traffic gating)
func (hm *HealthMonitor) checkProbeHealth(ctx context.Context) {
	hm.mu.RLock()
	containersCopy := make([]*containerProbeConfig, 0, len(hm.containers))
	for _, cfg := range hm.containers {
		containersCopy = append(containersCopy, cfg)
	}
	hm.mu.RUnlock()

	// Check each container's health probes
	for _, cfg := range containersCopy {
		// Check liveness probe
		if cfg.livenessConfig != nil {
			if result, ok := hm.probeExecutor.GetProbeResult(cfg.containerID, "liveness"); ok {
				RecordLivenessProbe(result.Success, 0) // Duration tracked in ProbeExecutor
				hm.handleLivenessResult(ctx, cfg.containerID, result, cfg.livenessConfig)
			}
		}

		// Check readiness probe
		if cfg.readinessConfig != nil {
			if result, ok := hm.probeExecutor.GetProbeResult(cfg.containerID, "readiness"); ok {
				RecordReadinessProbe(result.Success, 0) // Duration tracked in ProbeExecutor
				hm.handleReadinessResult(ctx, cfg.containerID, result)
			}
		}
	}
}

// handleLivenessResult checks if liveness failures should trigger restart
//
// Restart Criteria:
// - Container is unhealthy (success = false)
// - Consecutive failures >= failure threshold
// - Restart rate limit not exceeded
//
// Restart Rate Limiting (per SOP §5 Concurrency):
// - Max 5 restarts per 5-minute window (configurable)
// - Prevents restart loops on persistently failing containers
func (hm *HealthMonitor) handleLivenessResult(
	ctx context.Context,
	containerID string,
	result *runtime.ProbeResult,
	config *runtime.ProbeConfig,
) {
	// CLD-REQ-032: Update health metrics
	UpdateContainerHealthStatus(containerID, "liveness", result.Success)
	UpdateConsecutiveFailures(containerID, "liveness", int(result.ConsecutiveFailures))

	if result.Success {
		return // Container healthy, no action needed
	}

	// Get failure threshold from config (default to 3 if not set)
	failureThreshold := config.FailureThreshold
	if failureThreshold == 0 {
		failureThreshold = 3
	}

	// Container unhealthy - check if restart needed
	if result.ConsecutiveFailures >= failureThreshold {
		hm.logger.Warn("Container liveness check failed, considering restart",
			zap.String("container_id", containerID),
			zap.Int32("consecutive_failures", result.ConsecutiveFailures),
			zap.Int32("threshold", failureThreshold),
		)

		// Check restart limits
		if hm.shouldRestart(containerID) {
			if err := hm.restartContainer(ctx, containerID); err != nil {
				hm.logger.Error("Failed to restart unhealthy container",
					zap.String("container_id", containerID),
					zap.Error(err),
				)
			} else {
				hm.recordRestart(containerID)

				// CLD-REQ-032: Record restart metric
				RecordContainerRestart("liveness_failure")

				// Callback for agent to report restart
				if hm.onLivenessFailure != nil {
					hm.onLivenessFailure(containerID, int(result.ConsecutiveFailures))
				}
			}
		} else {
			// CLD-REQ-032: Record rate limit hit
			RecordRestartRateLimitHit()

			hm.logger.Error("Container restart limit exceeded, marking as failed",
				zap.String("container_id", containerID),
				zap.Int("restart_count", hm.getRestartCount(containerID)),
				zap.Int("max_restarts", hm.maxRestarts),
				zap.Duration("restart_window", hm.restartWindow),
			)

			// Callback to report persistent failure (for rescheduling)
			if hm.onLivenessFailure != nil {
				hm.onLivenessFailure(containerID, int(result.ConsecutiveFailures))
			}
		}
	}
}

// handleReadinessResult detects readiness changes and reports them
//
// Readiness changes affect traffic routing:
// - healthy -> unhealthy: Remove endpoint from load balancer
// - unhealthy -> healthy: Add endpoint back to load balancer
//
// Change detection uses previous state tracking to avoid redundant callbacks.
func (hm *HealthMonitor) handleReadinessResult(
	ctx context.Context,
	containerID string,
	result *runtime.ProbeResult,
) {
	hm.mu.Lock()
	previousHealthy, exists := hm.previousReadiness[containerID]
	currentHealthy := result.Success

	// Update tracked state
	hm.previousReadiness[containerID] = currentHealthy
	hm.mu.Unlock()

	// CLD-REQ-032: Update readiness metrics
	UpdateContainerHealthStatus(containerID, "readiness", currentHealthy)
	UpdateConsecutiveFailures(containerID, "readiness", int(result.ConsecutiveFailures))

	// Detect changes
	if !exists || previousHealthy != currentHealthy {
		hm.logger.Info("Container readiness changed",
			zap.String("container_id", containerID),
			zap.Bool("previous", previousHealthy),
			zap.Bool("current", currentHealthy),
			zap.Int32("consecutive_failures", result.ConsecutiveFailures),
		)

		// Report change for traffic gating
		if hm.onReadinessChange != nil {
			hm.onReadinessChange(containerID, currentHealthy)
		}
	}
}

// shouldRestart checks if container can be restarted (rate limiting)
//
// Rate limiting prevents restart loops:
// - Counts restarts within a time window
// - Resets counter if outside window
// - Blocks restart if limit exceeded
//
// Thread Safety: Uses RLock for read access (per SOP §5.3)
func (hm *HealthMonitor) shouldRestart(containerID string) bool {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	restarts := hm.restartAttempts[containerID]
	lastRestart, exists := hm.lastRestartTime[containerID]

	if !exists {
		return true // First restart
	}

	// Reset counter if outside restart window
	if time.Since(lastRestart) > hm.restartWindow {
		return true
	}

	return restarts < hm.maxRestarts
}

// recordRestart records a container restart
//
// Tracks restart count and timestamp for rate limiting.
//
// Thread Safety: Uses Lock for write access (per SOP §5.3)
func (hm *HealthMonitor) recordRestart(containerID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	lastRestart, exists := hm.lastRestartTime[containerID]

	if !exists || time.Since(lastRestart) > hm.restartWindow {
		// First restart or outside window, reset counter
		hm.restartAttempts[containerID] = 1
	} else {
		hm.restartAttempts[containerID]++
	}

	hm.lastRestartTime[containerID] = time.Now()
}

// getRestartCount returns restart count for container
//
// Thread Safety: Uses RLock for read access (per SOP §5.3)
func (hm *HealthMonitor) getRestartCount(containerID string) int {
	hm.mu.RLock()
	defer hm.mu.RUnlock()
	return hm.restartAttempts[containerID]
}

// restartContainer restarts a container
//
// Restart Procedure:
// 1. Stop container with 10-second grace period
// 2. Start container
// 3. Log success/failure
//
// Error Handling: Returns error but logs details (per SOP §4)
func (hm *HealthMonitor) restartContainer(ctx context.Context, containerID string) error {
	hm.logger.Info("Restarting container due to liveness failure",
		zap.String("container_id", containerID),
	)

	// Stop container
	if err := hm.runtime.StopContainer(ctx, containerID, 10*time.Second); err != nil {
		return fmt.Errorf("stopping container: %w", err)
	}

	// Start container
	if err := hm.runtime.StartContainer(ctx, containerID); err != nil {
		return fmt.Errorf("starting container: %w", err)
	}

	hm.logger.Info("Container restarted successfully",
		zap.String("container_id", containerID),
	)

	return nil
}

// SetLivenessFailureCallback sets callback for liveness failures
//
// The callback is invoked when:
// - Liveness probe failure threshold exceeded
// - Container restart attempted
// - Restart limit exceeded (persistent failure)
//
// Callback Parameters:
//   - containerID: Container that failed liveness checks
//   - consecutiveFailures: Number of consecutive failures
//
// Use Case: Agent reports liveness failures in heartbeat for coordinator tracking
func (hm *HealthMonitor) SetLivenessFailureCallback(fn func(string, int)) {
	hm.onLivenessFailure = fn
}

// SetReadinessChangeCallback sets callback for readiness changes
//
// The callback is invoked when:
// - Container transitions healthy <-> unhealthy
// - Initial readiness status established
//
// Callback Parameters:
//   - containerID: Container with readiness change
//   - healthy: New readiness status
//
// Use Case: Agent reports readiness in heartbeat for traffic gating
func (hm *HealthMonitor) SetReadinessChangeCallback(fn func(string, bool)) {
	hm.onReadinessChange = fn
}

// SetMaxRestarts sets the maximum restarts allowed in the restart window
func (hm *HealthMonitor) SetMaxRestarts(max int) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.maxRestarts = max
}

// SetRestartWindow sets the time window for restart rate limiting
func (hm *HealthMonitor) SetRestartWindow(window time.Duration) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.restartWindow = window
}

// SetCheckInterval sets the health check polling interval
func (hm *HealthMonitor) SetCheckInterval(interval time.Duration) {
	hm.mu.Lock()
	defer hm.mu.Unlock()
	hm.checkInterval = interval
}

// ClearRestartHistory clears restart history for a container
//
// Use Case: Reset restart counter when container is manually restarted
// or after successful prolonged operation.
func (hm *HealthMonitor) ClearRestartHistory(containerID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	delete(hm.restartAttempts, containerID)
	delete(hm.lastRestartTime, containerID)

	hm.logger.Info("Cleared restart history",
		zap.String("container_id", containerID),
	)
}

// GetHealthStatus returns current health status for a container
//
// Returns liveness healthy, readiness healthy, and restart count.
// Returns false, false, 0 if container not found.
func (hm *HealthMonitor) GetHealthStatus(containerID string) (livenessHealthy, readinessHealthy bool, restartCount int) {
	hm.mu.RLock()
	defer hm.mu.RUnlock()

	// Get liveness result
	if result, ok := hm.probeExecutor.GetProbeResult(containerID, "liveness"); ok {
		livenessHealthy = result.Success
	}

	// Get readiness result
	if result, ok := hm.probeExecutor.GetProbeResult(containerID, "readiness"); ok {
		readinessHealthy = result.Success
	}

	// Get restart count
	restartCount = hm.restartAttempts[containerID]

	return livenessHealthy, readinessHealthy, restartCount
}

// RegisterContainer registers a container with its probe configurations
//
// This must be called when starting health probes for a container.
// The health monitor uses this to track which containers to monitor.
func (hm *HealthMonitor) RegisterContainer(containerID string, livenessConfig, readinessConfig *runtime.ProbeConfig) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	hm.containers[containerID] = &containerProbeConfig{
		containerID:     containerID,
		livenessConfig:  livenessConfig,
		readinessConfig: readinessConfig,
	}

	hm.logger.Info("Registered container for health monitoring",
		zap.String("container_id", containerID),
		zap.Bool("has_liveness", livenessConfig != nil),
		zap.Bool("has_readiness", readinessConfig != nil),
	)
}

// UnregisterContainer removes a container from health monitoring
//
// Call this when a container is stopped or removed.
func (hm *HealthMonitor) UnregisterContainer(containerID string) {
	hm.mu.Lock()
	defer hm.mu.Unlock()

	delete(hm.containers, containerID)
	delete(hm.previousReadiness, containerID)

	hm.logger.Info("Unregistered container from health monitoring",
		zap.String("container_id", containerID),
	)
}

// parseProbeKey parses "containerID:probeType" key
//
// Returns containerID and probeType. If key format is invalid,
// returns the full key as containerID and "unknown" as probeType.
func parseProbeKey(key string) (containerID, probeType string) {
	parts := strings.Split(key, ":")
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return key, "unknown"
}
