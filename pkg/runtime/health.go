package runtime

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ProbeType represents the type of health probe
type ProbeType string

const (
	ProbeTypeHTTP ProbeType = "http"
	ProbeTypeTCP  ProbeType = "tcp"
	ProbeTypeExec ProbeType = "exec"
)

// ProbeConfig represents health probe configuration
type ProbeConfig struct {
	Type ProbeType

	// HTTP probe config
	HTTPGet *HTTPProbe

	// TCP probe config
	TCPSocket *TCPProbe

	// Exec probe config
	Exec *ExecProbe

	// Timing configuration
	InitialDelaySeconds int32
	PeriodSeconds       int32
	TimeoutSeconds      int32
	SuccessThreshold    int32
	FailureThreshold    int32
}

// HTTPProbe represents an HTTP health check
type HTTPProbe struct {
	Path   string
	Port   int32
	Scheme string // HTTP or HTTPS
	Headers map[string]string
}

// TCPProbe represents a TCP health check
type TCPProbe struct {
	Port int32
}

// ExecProbe represents an exec health check
type ExecProbe struct {
	Command []string
}

// ProbeResult represents the result of a health probe execution
type ProbeResult struct {
	Success         bool
	Message         string
	LastProbeTime   time.Time
	ConsecutiveSuccesses int32
	ConsecutiveFailures  int32
}

// ProbeExecutor executes health probes for containers
type ProbeExecutor struct {
	runtime         Runtime
	logger          *zap.Logger
	probeResults    map[string]*ProbeResult // containerID -> result
	mu              sync.RWMutex
	ctx             context.Context
	cancel          context.CancelFunc
	probeExecutions chan *ProbeExecution
}

// ProbeExecution represents a scheduled probe execution
type ProbeExecution struct {
	ContainerID   string
	ContainerIP   string
	ProbeType     string // "liveness" or "readiness"
	Config        *ProbeConfig
	ResultChannel chan *ProbeResult
}

// NewProbeExecutor creates a new health probe executor
func NewProbeExecutor(runtime Runtime, logger *zap.Logger) *ProbeExecutor {
	ctx, cancel := context.WithCancel(context.Background())

	executor := &ProbeExecutor{
		runtime:         runtime,
		logger:          logger,
		probeResults:    make(map[string]*ProbeResult),
		ctx:             ctx,
		cancel:          cancel,
		probeExecutions: make(chan *ProbeExecution, 100),
	}

	// Start probe execution worker
	go executor.probeWorker()

	return executor
}

// ExecuteProbe schedules a probe execution
func (pe *ProbeExecutor) ExecuteProbe(containerID, containerIP, probeType string, config *ProbeConfig) (*ProbeResult, error) {
	if config == nil {
		return nil, fmt.Errorf("probe config is nil")
	}

	resultChan := make(chan *ProbeResult, 1)

	execution := &ProbeExecution{
		ContainerID:   containerID,
		ContainerIP:   containerIP,
		ProbeType:     probeType,
		Config:        config,
		ResultChannel: resultChan,
	}

	// Send to worker
	select {
	case pe.probeExecutions <- execution:
	case <-time.After(5 * time.Second):
		return nil, fmt.Errorf("probe execution queue is full")
	}

	// Wait for result with timeout
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 3 * time.Second
	}

	select {
	case result := <-resultChan:
		return result, nil
	case <-time.After(timeout + 1*time.Second):
		return nil, fmt.Errorf("probe execution timed out")
	}
}

// probeWorker processes probe executions from the queue
func (pe *ProbeExecutor) probeWorker() {
	for {
		select {
		case <-pe.ctx.Done():
			return
		case execution := <-pe.probeExecutions:
			result := pe.executeProbeInternal(execution)
			execution.ResultChannel <- result
		}
	}
}

// executeProbeInternal executes a single probe
func (pe *ProbeExecutor) executeProbeInternal(execution *ProbeExecution) *ProbeResult {
	config := execution.Config

	// Get previous result
	pe.mu.RLock()
	key := fmt.Sprintf("%s:%s", execution.ContainerID, execution.ProbeType)
	prevResult, exists := pe.probeResults[key]
	pe.mu.RUnlock()

	if !exists {
		prevResult = &ProbeResult{
			ConsecutiveSuccesses: 0,
			ConsecutiveFailures:  0,
		}
	}

	// Create timeout context
	timeout := time.Duration(config.TimeoutSeconds) * time.Second
	if timeout == 0 {
		timeout = 3 * time.Second
	}
	ctx, cancel := context.WithTimeout(pe.ctx, timeout)
	defer cancel()

	// Execute probe based on type
	var success bool
	var message string

	switch config.Type {
	case ProbeTypeHTTP:
		success, message = pe.executeHTTPProbe(ctx, execution.ContainerIP, config.HTTPGet)
	case ProbeTypeTCP:
		success, message = pe.executeTCPProbe(ctx, execution.ContainerIP, config.TCPSocket)
	case ProbeTypeExec:
		success, message = pe.executeExecProbe(ctx, execution.ContainerID, config.Exec)
	default:
		success = false
		message = fmt.Sprintf("unknown probe type: %s", config.Type)
	}

	// Update result counters
	result := &ProbeResult{
		Success:       success,
		Message:       message,
		LastProbeTime: time.Now(),
	}

	if success {
		result.ConsecutiveSuccesses = prevResult.ConsecutiveSuccesses + 1
		result.ConsecutiveFailures = 0
	} else {
		result.ConsecutiveSuccesses = 0
		result.ConsecutiveFailures = prevResult.ConsecutiveFailures + 1
	}

	// Store result
	pe.mu.Lock()
	pe.probeResults[key] = result
	pe.mu.Unlock()

	pe.logger.Debug("Probe executed",
		zap.String("container_id", execution.ContainerID),
		zap.String("probe_type", execution.ProbeType),
		zap.Bool("success", success),
		zap.String("message", message),
		zap.Int32("consecutive_successes", result.ConsecutiveSuccesses),
		zap.Int32("consecutive_failures", result.ConsecutiveFailures),
	)

	return result
}

// executeHTTPProbe executes an HTTP health probe
func (pe *ProbeExecutor) executeHTTPProbe(ctx context.Context, containerIP string, probe *HTTPProbe) (bool, string) {
	if probe == nil {
		return false, "HTTP probe config is nil"
	}

	scheme := probe.Scheme
	if scheme == "" {
		scheme = "http"
	}

	url := fmt.Sprintf("%s://%s:%d%s", scheme, containerIP, probe.Port, probe.Path)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false, fmt.Sprintf("failed to create request: %v", err)
	}

	// Add custom headers
	for key, value := range probe.Headers {
		req.Header.Set(key, value)
	}

	client := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	resp, err := client.Do(req)
	if err != nil {
		return false, fmt.Sprintf("HTTP request failed: %v", err)
	}
	defer resp.Body.Close()

	// Success if status code is 2xx or 3xx
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return true, fmt.Sprintf("HTTP %d", resp.StatusCode)
	}

	return false, fmt.Sprintf("HTTP %d (expected 2xx-3xx)", resp.StatusCode)
}

// executeTCPProbe executes a TCP health probe
func (pe *ProbeExecutor) executeTCPProbe(ctx context.Context, containerIP string, probe *TCPProbe) (bool, string) {
	if probe == nil {
		return false, "TCP probe config is nil"
	}

	address := fmt.Sprintf("%s:%d", containerIP, probe.Port)

	dialer := net.Dialer{
		Timeout: 3 * time.Second,
	}

	conn, err := dialer.DialContext(ctx, "tcp", address)
	if err != nil {
		return false, fmt.Sprintf("TCP connection failed: %v", err)
	}
	defer conn.Close()

	return true, "TCP connection successful"
}

// executeExecProbe executes an exec health probe inside the container
func (pe *ProbeExecutor) executeExecProbe(ctx context.Context, containerID string, probe *ExecProbe) (bool, string) {
	if probe == nil {
		return false, "Exec probe config is nil"
	}

	if len(probe.Command) == 0 {
		return false, "Exec probe command is empty"
	}

	// Create exec config
	execConfig := ExecConfig{
		Command: probe.Command,
		Stdout:  true,
		Stderr:  true,
	}

	// Execute command in container
	result, err := pe.runtime.ExecContainer(ctx, containerID, execConfig)
	if err != nil {
		return false, fmt.Sprintf("exec failed: %v", err)
	}

	// Success if exit code is 0
	if result.ExitCode == 0 {
		return true, "exec command succeeded"
	}

	message := fmt.Sprintf("exec command failed with exit code %d", result.ExitCode)
	if len(result.Stderr) > 0 {
		message += fmt.Sprintf(": %s", string(result.Stderr))
	}

	return false, message
}

// GetProbeResult returns the latest probe result for a container
func (pe *ProbeExecutor) GetProbeResult(containerID, probeType string) (*ProbeResult, bool) {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	key := fmt.Sprintf("%s:%s", containerID, probeType)
	result, exists := pe.probeResults[key]
	return result, exists
}

// IsHealthy checks if a container is healthy based on probe thresholds
func (pe *ProbeExecutor) IsHealthy(containerID, probeType string, config *ProbeConfig) bool {
	result, exists := pe.GetProbeResult(containerID, probeType)
	if !exists {
		// No probe result yet - assume healthy during initial delay
		return true
	}

	// Check success threshold
	successThreshold := config.SuccessThreshold
	if successThreshold == 0 {
		successThreshold = 1
	}

	// Check failure threshold
	failureThreshold := config.FailureThreshold
	if failureThreshold == 0 {
		failureThreshold = 3
	}

	// Healthy if consecutive successes meet threshold
	if result.ConsecutiveSuccesses >= successThreshold {
		return true
	}

	// Unhealthy if consecutive failures meet threshold
	if result.ConsecutiveFailures >= failureThreshold {
		return false
	}

	// In transition state - use previous success status
	return result.Success
}

// ClearProbeResults removes probe results for a container
func (pe *ProbeExecutor) ClearProbeResults(containerID string) {
	pe.mu.Lock()
	defer pe.mu.Unlock()

	// Remove all probe results for this container
	for key := range pe.probeResults {
		if len(key) > len(containerID) && key[:len(containerID)] == containerID {
			delete(pe.probeResults, key)
		}
	}

	pe.logger.Debug("Cleared probe results", zap.String("container_id", containerID))
}

// StartProbing starts periodic health probes for a container
func (pe *ProbeExecutor) StartProbing(containerID, containerIP string, livenessProbe, readinessProbe *ProbeConfig) {
	// Start liveness probe goroutine
	if livenessProbe != nil {
		go pe.periodicProbe(containerID, containerIP, "liveness", livenessProbe)
	}

	// Start readiness probe goroutine
	if readinessProbe != nil {
		go pe.periodicProbe(containerID, containerIP, "readiness", readinessProbe)
	}
}

// periodicProbe runs a probe periodically
func (pe *ProbeExecutor) periodicProbe(containerID, containerIP, probeType string, config *ProbeConfig) {
	// Wait for initial delay
	initialDelay := time.Duration(config.InitialDelaySeconds) * time.Second
	if initialDelay == 0 {
		initialDelay = 10 * time.Second
	}

	select {
	case <-pe.ctx.Done():
		return
	case <-time.After(initialDelay):
	}

	// Calculate period
	period := time.Duration(config.PeriodSeconds) * time.Second
	if period == 0 {
		period = 10 * time.Second
	}

	ticker := time.NewTicker(period)
	defer ticker.Stop()

	for {
		select {
		case <-pe.ctx.Done():
			return
		case <-ticker.C:
			// Execute probe (async)
			_, err := pe.ExecuteProbe(containerID, containerIP, probeType, config)
			if err != nil {
				pe.logger.Warn("Failed to execute periodic probe",
					zap.String("container_id", containerID),
					zap.String("probe_type", probeType),
					zap.Error(err),
				)
			}
		}
	}
}

// Stop stops the probe executor
func (pe *ProbeExecutor) Stop() {
	pe.cancel()
	close(pe.probeExecutions)
}

// GetStats returns statistics about probe execution
func (pe *ProbeExecutor) GetStats() map[string]interface{} {
	pe.mu.RLock()
	defer pe.mu.RUnlock()

	healthy := 0
	unhealthy := 0

	for _, result := range pe.probeResults {
		if result.Success {
			healthy++
		} else {
			unhealthy++
		}
	}

	return map[string]interface{}{
		"total_probes": len(pe.probeResults),
		"healthy":      healthy,
		"unhealthy":    unhealthy,
	}
}
