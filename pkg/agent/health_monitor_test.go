package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/osama1998H/Cloudless/pkg/runtime"
	"go.uber.org/zap"
)

// Mock Runtime for testing
type mockRuntime struct {
	mu              sync.Mutex
	stopCalls       []stopCall
	startCalls      []string
	stopError       error
	startError      error
	stopDelay       time.Duration
	containerStates map[string]string // containerID -> state
}

type stopCall struct {
	containerID string
	timeout     time.Duration
}

func newMockRuntime() *mockRuntime {
	return &mockRuntime{
		containerStates: make(map[string]string),
	}
}

func (m *mockRuntime) StopContainer(ctx context.Context, containerID string, timeout time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.stopDelay > 0 {
		time.Sleep(m.stopDelay)
	}

	m.stopCalls = append(m.stopCalls, stopCall{containerID, timeout})
	if m.stopError != nil {
		return m.stopError
	}

	m.containerStates[containerID] = "stopped"
	return nil
}

func (m *mockRuntime) StartContainer(ctx context.Context, containerID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.startCalls = append(m.startCalls, containerID)
	if m.startError != nil {
		return m.startError
	}

	m.containerStates[containerID] = "running"
	return nil
}

func (m *mockRuntime) getStopCallCount(containerID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, call := range m.stopCalls {
		if call.containerID == containerID {
			count++
		}
	}
	return count
}

func (m *mockRuntime) getStartCallCount(containerID string) int {
	m.mu.Lock()
	defer m.mu.Unlock()

	count := 0
	for _, cid := range m.startCalls {
		if cid == containerID {
			count++
		}
	}
	return count
}

// Not used in tests but required to implement Runtime interface
func (m *mockRuntime) CreateContainer(ctx context.Context, spec runtime.ContainerSpec) (*runtime.Container, error) {
	return nil, errors.New("not implemented")
}
func (m *mockRuntime) DeleteContainer(ctx context.Context, containerID string) error {
	return errors.New("not implemented")
}
func (m *mockRuntime) GetContainer(ctx context.Context, containerID string) (*runtime.Container, error) {
	return nil, errors.New("not implemented")
}
func (m *mockRuntime) ListContainers(ctx context.Context) ([]*runtime.Container, error) {
	return nil, errors.New("not implemented")
}
func (m *mockRuntime) GetContainerLogs(ctx context.Context, containerID string, follow bool) (<-chan runtime.LogEntry, error) {
	return nil, errors.New("not implemented")
}
func (m *mockRuntime) GetContainerStats(ctx context.Context, containerID string) (*runtime.ContainerStats, error) {
	return nil, errors.New("not implemented")
}
func (m *mockRuntime) ExecContainer(ctx context.Context, containerID string, config runtime.ExecConfig) (*runtime.ExecResult, error) {
	return nil, errors.New("not implemented")
}
func (m *mockRuntime) PullImage(ctx context.Context, image string) error {
	return errors.New("not implemented")
}
func (m *mockRuntime) ListImages(ctx context.Context) ([]*runtime.Image, error) {
	return nil, errors.New("not implemented")
}
func (m *mockRuntime) DeleteImage(ctx context.Context, image string) error {
	return errors.New("not implemented")
}
func (m *mockRuntime) Close() error {
	return nil
}

// Mock ProbeExecutor for testing
type mockProbeExecutor struct {
	mu           sync.RWMutex
	probeResults map[string]*runtime.ProbeResult
}

func newMockProbeExecutor() *mockProbeExecutor {
	return &mockProbeExecutor{
		probeResults: make(map[string]*runtime.ProbeResult),
	}
}

func (m *mockProbeExecutor) GetAllProbeResults() map[string]*runtime.ProbeResult {
	m.mu.RLock()
	defer m.mu.RUnlock()

	// Return a copy to avoid concurrent modification
	results := make(map[string]*runtime.ProbeResult)
	for k, v := range m.probeResults {
		resultCopy := *v
		results[k] = &resultCopy
	}
	return results
}

func (m *mockProbeExecutor) setProbeResult(containerID, probeType string, result *runtime.ProbeResult) {
	m.mu.Lock()
	defer m.mu.Unlock()

	key := fmt.Sprintf("%s:%s", containerID, probeType)
	m.probeResults[key] = result
}

// TestHealthMonitor_LivenessRestart tests automatic restart on liveness failure
func TestHealthMonitor_LivenessRestart(t *testing.T) {
	tests := []struct {
		name                string
		containerID         string
		consecutiveFailures int
		failureThreshold    int
		healthy             bool
		expectRestart       bool
	}{
		{
			name:                "healthy container - no restart",
			containerID:         "container-1",
			consecutiveFailures: 0,
			failureThreshold:    3,
			healthy:             true,
			expectRestart:       false,
		},
		{
			name:                "unhealthy but below threshold - no restart",
			containerID:         "container-2",
			consecutiveFailures: 2,
			failureThreshold:    3,
			healthy:             false,
			expectRestart:       false,
		},
		{
			name:                "unhealthy at threshold - restart",
			containerID:         "container-3",
			consecutiveFailures: 3,
			failureThreshold:    3,
			healthy:             false,
			expectRestart:       true,
		},
		{
			name:                "unhealthy above threshold - restart",
			containerID:         "container-4",
			consecutiveFailures: 5,
			failureThreshold:    3,
			healthy:             false,
			expectRestart:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			mockRT := newMockRuntime()
			mockProbe := newMockProbeExecutor()
			logger := zap.NewNop()

			hm := NewHealthMonitor(mockProbe, mockRT, logger)

			// Set probe result
			mockProbe.setProbeResult(tt.containerID, "liveness", &runtime.ProbeResult{
				Healthy:             tt.healthy,
				ConsecutiveFailures: tt.consecutiveFailures,
				FailureThreshold:    tt.failureThreshold,
				LastCheckTime:       time.Now(),
			})

			// Execute
			ctx := context.Background()
			hm.checkProbeHealth(ctx)

			// Verify
			stopCount := mockRT.getStopCallCount(tt.containerID)
			startCount := mockRT.getStartCallCount(tt.containerID)

			if tt.expectRestart {
				if stopCount != 1 {
					t.Errorf("Expected 1 stop call, got %d", stopCount)
				}
				if startCount != 1 {
					t.Errorf("Expected 1 start call, got %d", startCount)
				}
			} else {
				if stopCount != 0 {
					t.Errorf("Expected 0 stop calls, got %d", stopCount)
				}
				if startCount != 0 {
					t.Errorf("Expected 0 start calls, got %d", startCount)
				}
			}
		})
	}
}

// TestHealthMonitor_RestartRateLimit tests restart rate limiting
func TestHealthMonitor_RestartRateLimit(t *testing.T) {
	// Setup
	mockRT := newMockRuntime()
	mockProbe := newMockProbeExecutor()
	logger := zap.NewNop()

	hm := NewHealthMonitor(mockProbe, mockRT, logger)
	hm.SetMaxRestarts(3)
	hm.SetRestartWindow(1 * time.Minute)

	containerID := "container-rate-limit"
	ctx := context.Background()

	// Set unhealthy probe result
	mockProbe.setProbeResult(containerID, "liveness", &runtime.ProbeResult{
		Healthy:             false,
		ConsecutiveFailures: 5,
		FailureThreshold:    3,
		LastCheckTime:       time.Now(),
	})

	// Trigger multiple restarts
	for i := 0; i < 5; i++ {
		hm.checkProbeHealth(ctx)
	}

	// Verify restart count is limited to maxRestarts
	stopCount := mockRT.getStopCallCount(containerID)
	if stopCount != 3 {
		t.Errorf("Expected 3 restarts (rate limited), got %d", stopCount)
	}

	// Verify restart count tracking
	restartCount := hm.getRestartCount(containerID)
	if restartCount != 3 {
		t.Errorf("Expected restart count 3, got %d", restartCount)
	}
}

// TestHealthMonitor_RestartRateLimitReset tests restart counter reset after window
func TestHealthMonitor_RestartRateLimitReset(t *testing.T) {
	// Setup
	mockRT := newMockRuntime()
	mockProbe := newMockProbeExecutor()
	logger := zap.NewNop()

	hm := NewHealthMonitor(mockProbe, mockRT, logger)
	hm.SetMaxRestarts(2)
	hm.SetRestartWindow(100 * time.Millisecond) // Short window for test

	containerID := "container-reset"
	ctx := context.Background()

	// Set unhealthy probe result
	mockProbe.setProbeResult(containerID, "liveness", &runtime.ProbeResult{
		Healthy:             false,
		ConsecutiveFailures: 5,
		FailureThreshold:    3,
		LastCheckTime:       time.Now(),
	})

	// Trigger restarts up to limit
	hm.checkProbeHealth(ctx)
	hm.checkProbeHealth(ctx)

	stopCount1 := mockRT.getStopCallCount(containerID)
	if stopCount1 != 2 {
		t.Errorf("Expected 2 restarts before limit, got %d", stopCount1)
	}

	// Wait for window to expire
	time.Sleep(150 * time.Millisecond)

	// Trigger another restart - should succeed after window reset
	hm.checkProbeHealth(ctx)

	stopCount2 := mockRT.getStopCallCount(containerID)
	if stopCount2 != 3 {
		t.Errorf("Expected 3 restarts total after window reset, got %d", stopCount2)
	}
}

// TestHealthMonitor_ReadinessChange tests readiness change detection
func TestHealthMonitor_ReadinessChange(t *testing.T) {
	// Setup
	mockRT := newMockRuntime()
	mockProbe := newMockProbeExecutor()
	logger := zap.NewNop()

	hm := NewHealthMonitor(mockProbe, mockRT, logger)

	containerID := "container-readiness"
	ctx := context.Background()

	// Track callback invocations
	var callbackMu sync.Mutex
	var callbackCalls []struct {
		containerID string
		healthy     bool
	}

	hm.SetReadinessChangeCallback(func(cid string, healthy bool) {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		callbackCalls = append(callbackCalls, struct {
			containerID string
			healthy     bool
		}{cid, healthy})
	})

	// Initial readiness: healthy
	mockProbe.setProbeResult(containerID, "readiness", &runtime.ProbeResult{
		Healthy:             true,
		ConsecutiveFailures: 0,
		FailureThreshold:    3,
		LastCheckTime:       time.Now(),
	})

	hm.checkProbeHealth(ctx)

	// Verify initial callback
	callbackMu.Lock()
	if len(callbackCalls) != 1 {
		t.Errorf("Expected 1 initial callback, got %d", len(callbackCalls))
	}
	if callbackCalls[0].healthy != true {
		t.Errorf("Expected initial callback with healthy=true")
	}
	callbackMu.Unlock()

	// Change to unhealthy
	mockProbe.setProbeResult(containerID, "readiness", &runtime.ProbeResult{
		Healthy:             false,
		ConsecutiveFailures: 1,
		FailureThreshold:    3,
		LastCheckTime:       time.Now(),
	})

	hm.checkProbeHealth(ctx)

	// Verify change callback
	callbackMu.Lock()
	if len(callbackCalls) != 2 {
		t.Errorf("Expected 2 total callbacks, got %d", len(callbackCalls))
	}
	if callbackCalls[1].healthy != false {
		t.Errorf("Expected change callback with healthy=false")
	}
	callbackMu.Unlock()

	// No change - should not trigger callback
	hm.checkProbeHealth(ctx)

	callbackMu.Lock()
	if len(callbackCalls) != 2 {
		t.Errorf("Expected no additional callback for unchanged status, got %d total", len(callbackCalls))
	}
	callbackMu.Unlock()
}

// TestHealthMonitor_LivenessFailureCallback tests liveness failure callback
func TestHealthMonitor_LivenessFailureCallback(t *testing.T) {
	// Setup
	mockRT := newMockRuntime()
	mockProbe := newMockProbeExecutor()
	logger := zap.NewNop()

	hm := NewHealthMonitor(mockProbe, mockRT, logger)

	containerID := "container-callback"
	ctx := context.Background()

	// Track callback invocations
	var callbackMu sync.Mutex
	var callbackCalls []struct {
		containerID         string
		consecutiveFailures int
	}

	hm.SetLivenessFailureCallback(func(cid string, failures int) {
		callbackMu.Lock()
		defer callbackMu.Unlock()
		callbackCalls = append(callbackCalls, struct {
			containerID         string
			consecutiveFailures int
		}{cid, failures})
	})

	// Set unhealthy probe result
	mockProbe.setProbeResult(containerID, "liveness", &runtime.ProbeResult{
		Healthy:             false,
		ConsecutiveFailures: 5,
		FailureThreshold:    3,
		LastCheckTime:       time.Now(),
	})

	hm.checkProbeHealth(ctx)

	// Verify callback was invoked with correct parameters
	callbackMu.Lock()
	if len(callbackCalls) != 1 {
		t.Errorf("Expected 1 callback, got %d", len(callbackCalls))
	}
	if callbackCalls[0].containerID != containerID {
		t.Errorf("Expected containerID %s, got %s", containerID, callbackCalls[0].containerID)
	}
	if callbackCalls[0].consecutiveFailures != 5 {
		t.Errorf("Expected consecutiveFailures 5, got %d", callbackCalls[0].consecutiveFailures)
	}
	callbackMu.Unlock()
}

// TestHealthMonitor_RestartError tests handling of restart errors
func TestHealthMonitor_RestartError(t *testing.T) {
	tests := []struct {
		name       string
		stopError  error
		startError error
	}{
		{
			name:      "stop error",
			stopError: errors.New("stop failed"),
		},
		{
			name:       "start error",
			startError: errors.New("start failed"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup
			mockRT := newMockRuntime()
			mockRT.stopError = tt.stopError
			mockRT.startError = tt.startError

			mockProbe := newMockProbeExecutor()
			logger := zap.NewNop()

			hm := NewHealthMonitor(mockProbe, mockRT, logger)

			containerID := "container-error"
			ctx := context.Background()

			// Set unhealthy probe result
			mockProbe.setProbeResult(containerID, "liveness", &runtime.ProbeResult{
				Healthy:             false,
				ConsecutiveFailures: 5,
				FailureThreshold:    3,
				LastCheckTime:       time.Now(),
			})

			// Execute - should not panic on error
			hm.checkProbeHealth(ctx)

			// Verify restart was attempted
			if tt.stopError != nil {
				stopCount := mockRT.getStopCallCount(containerID)
				if stopCount != 1 {
					t.Errorf("Expected 1 stop attempt despite error, got %d", stopCount)
				}
			}
		})
	}
}

// TestHealthMonitor_ClearRestartHistory tests clearing restart history
func TestHealthMonitor_ClearRestartHistory(t *testing.T) {
	// Setup
	mockRT := newMockRuntime()
	mockProbe := newMockProbeExecutor()
	logger := zap.NewNop()

	hm := NewHealthMonitor(mockProbe, mockRT, logger)

	containerID := "container-clear"

	// Record some restarts
	hm.recordRestart(containerID)
	hm.recordRestart(containerID)

	restartCount := hm.getRestartCount(containerID)
	if restartCount != 2 {
		t.Errorf("Expected restart count 2 before clear, got %d", restartCount)
	}

	// Clear history
	hm.ClearRestartHistory(containerID)

	restartCount = hm.getRestartCount(containerID)
	if restartCount != 0 {
		t.Errorf("Expected restart count 0 after clear, got %d", restartCount)
	}
}

// TestHealthMonitor_GetHealthStatus tests health status retrieval
func TestHealthMonitor_GetHealthStatus(t *testing.T) {
	// Setup
	mockRT := newMockRuntime()
	mockProbe := newMockProbeExecutor()
	logger := zap.NewNop()

	hm := NewHealthMonitor(mockProbe, mockRT, logger)

	containerID := "container-status"

	// Set probe results
	mockProbe.setProbeResult(containerID, "liveness", &runtime.ProbeResult{
		Healthy:             true,
		ConsecutiveFailures: 0,
		FailureThreshold:    3,
		LastCheckTime:       time.Now(),
	})

	mockProbe.setProbeResult(containerID, "readiness", &runtime.ProbeResult{
		Healthy:             false,
		ConsecutiveFailures: 2,
		FailureThreshold:    3,
		LastCheckTime:       time.Now(),
	})

	// Record restart
	hm.recordRestart(containerID)

	// Get status
	liveness, readiness, restartCount := hm.GetHealthStatus(containerID)

	// Verify
	if !liveness {
		t.Errorf("Expected liveness true, got false")
	}
	if readiness {
		t.Errorf("Expected readiness false, got true")
	}
	if restartCount != 1 {
		t.Errorf("Expected restart count 1, got %d", restartCount)
	}
}

// TestHealthMonitor_parseProbeKey tests probe key parsing
func TestHealthMonitor_parseProbeKey(t *testing.T) {
	tests := []struct {
		name              string
		key               string
		expectedContainer string
		expectedProbeType string
	}{
		{
			name:              "valid liveness key",
			key:               "container-123:liveness",
			expectedContainer: "container-123",
			expectedProbeType: "liveness",
		},
		{
			name:              "valid readiness key",
			key:               "container-456:readiness",
			expectedContainer: "container-456",
			expectedProbeType: "readiness",
		},
		{
			name:              "invalid key format",
			key:               "invalid-key-format",
			expectedContainer: "invalid-key-format",
			expectedProbeType: "unknown",
		},
		{
			name:              "key with multiple colons",
			key:               "container:with:colons:liveness",
			expectedContainer: "container",
			expectedProbeType: "with:colons:liveness",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			containerID, probeType := parseProbeKey(tt.key)

			if containerID != tt.expectedContainer {
				t.Errorf("Expected containerID %s, got %s", tt.expectedContainer, containerID)
			}
			if probeType != tt.expectedProbeType {
				t.Errorf("Expected probeType %s, got %s", tt.expectedProbeType, probeType)
			}
		})
	}
}

// TestHealthMonitor_ConcurrentAccess tests thread safety
func TestHealthMonitor_ConcurrentAccess(t *testing.T) {
	// Setup
	mockRT := newMockRuntime()
	mockProbe := newMockProbeExecutor()
	logger := zap.NewNop()

	hm := NewHealthMonitor(mockProbe, mockRT, logger)

	ctx := context.Background()
	const numGoroutines = 10
	const numOperations = 100

	// Run concurrent operations
	var wg sync.WaitGroup
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			containerID := fmt.Sprintf("container-%d", id)

			for j := 0; j < numOperations; j++ {
				// Concurrent reads and writes
				hm.recordRestart(containerID)
				_ = hm.getRestartCount(containerID)
				_ = hm.shouldRestart(containerID)

				// Set probe results
				mockProbe.setProbeResult(containerID, "liveness", &runtime.ProbeResult{
					Healthy:             j%2 == 0,
					ConsecutiveFailures: j,
					FailureThreshold:    3,
					LastCheckTime:       time.Now(),
				})

				// Check health
				hm.checkProbeHealth(ctx)
			}
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions detected (run with -race flag)
}
