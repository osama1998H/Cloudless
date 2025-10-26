# Design Document: Health Probe Integration (CLD-REQ-032)

**Status:** Draft
**Author:** Engineering Team
**Created:** 2025-10-26
**PRD Reference:** CLD-REQ-032, CLD-REQ-031
**Related Issues:** Health probes defined but not integrated

---

## 1. Problem Statement

Cloudless has a complete health probe execution infrastructure (`pkg/runtime/health.go`) and protobuf definitions for liveness and readiness probes, but **these probes are never executed** in the workload lifecycle. This violates CLD-REQ-032:

> "Health probes (liveness/readiness) drive automated restart and traffic gating."

### Current State

- **Probe Executor:** ✅ Fully implemented with HTTP, TCP, and Exec probe support
- **Protobuf API:** ✅ Complete definitions in `WorkloadSpec`
- **Integration:** ❌ Probes not started, results not used
- **Automated Restart:** ❌ Liveness failures don't restart containers
- **Traffic Gating:** ❌ Readiness status doesn't affect load balancing

### Impact

- Workloads with failing containers continue running (no liveness restart)
- Traffic sent to unhealthy endpoints (no readiness gating)
- Replica monitoring incomplete (doesn't detect unhealthy state)
- PRD requirement CLD-REQ-032 not satisfied

---

## 2. Goals and Non-Goals

### 2.1 Goals

**G1. Liveness Probe Automation**
- Execute liveness probes for running containers
- Restart containers after exceeding failure threshold
- Report restart events to coordinator

**G2. Readiness Probe Traffic Gating**
- Execute readiness probes for running containers
- Update service registry endpoint health based on readiness
- Load balancer excludes unhealthy endpoints from rotation

**G3. Coordinator Integration**
- Extend heartbeat protocol to report probe health status
- Track replica health state in coordinator
- Integrate with ReplicaMonitor for rescheduling unhealthy replicas

**G4. Backward Compatibility**
- Workloads without health probes continue working unchanged
- Optional feature with graceful degradation
- No breaking changes to existing APIs

**G5. Observability**
- Prometheus metrics for probe success/failure rates
- Structured logging for probe lifecycle events
- Distributed tracing for health check operations

### 2.2 Non-Goals

**NG1. Advanced Probe Types**
- No gRPC probe support in v0.x (HTTP/TCP/Exec sufficient)
- No custom probe plugins

**NG2. Multi-Container Health**
- Init containers, sidecars out of scope (future work)
- Single container per workload assumption

**NG3. External Health Endpoints**
- No integration with external monitoring systems (Datadog, etc.)
- Self-contained health checking only

**NG4. Startup Probes**
- Kubernetes has startup probes distinct from liveness
- We'll use liveness with `initial_delay_seconds` instead

---

## 3. Design Overview

### 3.1 Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ AGENT NODE                                                       │
│                                                                  │
│  ┌──────────────┐     Probe Configs      ┌──────────────────┐  │
│  │   Workload   │ ────────────────────▶   │  Probe Executor  │  │
│  │   Lifecycle  │                         │  (existing)      │  │
│  └──────────────┘                         └────────┬─────────┘  │
│         │                                           │            │
│         │ Start Container                           │ Results    │
│         ▼                                           ▼            │
│  ┌──────────────┐                         ┌──────────────────┐  │
│  │   Container  │                         │  Health Monitor  │  │
│  │   Runtime    │ ◀───── Restart ───────  │    (NEW)         │  │
│  └──────────────┘                         └────────┬─────────┘  │
│                                                     │            │
│                                            Health Status         │
│                                                     │            │
│                                                     ▼            │
│                                           ┌──────────────────┐  │
│                                           │    Heartbeat     │  │
│                                           │    Reporter      │  │
│                                           └────────┬─────────┘  │
└────────────────────────────────────────────────────┼────────────┘
                                                     │
                                          Health Data (gRPC)
                                                     │
                                                     ▼
┌────────────────────────────────────────────────────┼────────────┐
│ COORDINATOR                                        │             │
│                                                    ▼             │
│                                          ┌──────────────────┐   │
│                                          │    Heartbeat     │   │
│                                          │    Handler       │   │
│                                          └────────┬─────────┘   │
│                                                   │             │
│                                    ┌──────────────┴─────────┐   │
│                                    │                        │   │
│                                    ▼                        ▼   │
│                          ┌──────────────────┐    ┌──────────────────┐
│                          │  State Manager   │    │ Service Registry │
│                          │  (Replica Health)│    │ (Endpoint Health)│
│                          └────────┬─────────┘    └──────────────────┘
│                                   │                        │
│                                   │                        │
│                                   ▼                        ▼
│                          ┌──────────────────┐    ┌──────────────────┐
│                          │ ReplicaMonitor   │    │  Load Balancer   │
│                          │  (Reschedule)    │    │ (Traffic Gating) │
│                          └──────────────────┘    └──────────────────┘
│                                                                      │
└──────────────────────────────────────────────────────────────────────┘
```

### 3.2 Data Flow

**Liveness Probe Flow:**
1. Agent extracts liveness probe config from `WorkloadSpec`
2. ProbeExecutor starts periodic health checks
3. HealthMonitor watches probe results
4. Failure threshold exceeded → restart container
5. Restart reported in next heartbeat
6. Coordinator updates replica state
7. ReplicaMonitor detects persistent failures → reschedule

**Readiness Probe Flow:**
1. Agent extracts readiness probe config from `WorkloadSpec`
2. ProbeExecutor starts periodic health checks
3. Readiness status changes (healthy ↔ unhealthy)
4. Agent reports readiness in heartbeat
5. Coordinator updates service registry endpoint health
6. Load balancer excludes unhealthy endpoints from traffic

---

## 4. Detailed Design

### 4.1 Protobuf API Extensions

**File:** `pkg/api/cloudless.proto`

```protobuf
// HeartbeatRequest extension
message HeartbeatRequest {
  string node_id = 1;
  repeated ContainerStatus containers = 2;
  ResourceUsage usage = 3;
  int64 timestamp = 4;
  string region = 5;

  // NEW: Health probe status for containers
  repeated ContainerHealth container_health = 6;
}

// NEW: Health status for a single container
message ContainerHealth {
  string container_id = 1;

  // Liveness probe status
  bool liveness_healthy = 2;
  int32 liveness_consecutive_failures = 3;
  int64 liveness_last_check_time = 4;

  // Readiness probe status
  bool readiness_healthy = 5;
  int32 readiness_consecutive_failures = 6;
  int64 readiness_last_check_time = 7;
}
```

**Backward Compatibility:**
- `container_health` is optional (protobuf3 default: empty repeated field)
- Old coordinators ignore unknown fields
- Old agents don't populate this field

### 4.2 Agent: Probe Configuration Extraction

**File:** `pkg/agent/agent.go`

```go
// extractProbeConfigs converts WorkloadSpec health probes to runtime probe configs
func (a *Agent) extractProbeConfigs(
    spec *api.WorkloadSpec,
    containerID string,
    containerIP string,
) (liveness, readiness *runtime.ProbeConfig, err error) {

    // Extract liveness probe
    if spec.LivenessProbe != nil {
        liveness, err = a.convertHealthCheck(
            spec.LivenessProbe,
            containerID,
            containerIP,
            "liveness",
        )
        if err != nil {
            return nil, nil, fmt.Errorf("invalid liveness probe: %w", err)
        }
    }

    // Extract readiness probe
    if spec.ReadinessProbe != nil {
        readiness, err = a.convertHealthCheck(
            spec.ReadinessProbe,
            containerID,
            containerIP,
            "readiness",
        )
        if err != nil {
            return nil, nil, fmt.Errorf("invalid readiness probe: %w", err)
        }
    }

    return liveness, readiness, nil
}

// convertHealthCheck converts protobuf HealthCheck to runtime ProbeConfig
func (a *Agent) convertHealthCheck(
    hc *api.HealthCheck,
    containerID string,
    containerIP string,
    probeType string,
) (*runtime.ProbeConfig, error) {

    if hc == nil {
        return nil, nil
    }

    config := &runtime.ProbeConfig{
        ContainerID:      containerID,
        ProbeType:        probeType,
        InitialDelay:     time.Duration(hc.InitialDelaySeconds) * time.Second,
        PeriodSeconds:    time.Duration(hc.PeriodSeconds) * time.Second,
        TimeoutSeconds:   time.Duration(hc.TimeoutSeconds) * time.Second,
        SuccessThreshold: int(hc.SuccessThreshold),
        FailureThreshold: int(hc.FailureThreshold),
    }

    // Convert probe handler
    switch handler := hc.Handler.(type) {
    case *api.HealthCheck_Http:
        if containerIP == "" {
            return nil, errors.New("container IP required for HTTP probe")
        }
        config.HTTPGet = &runtime.HTTPProbe{
            Host:   containerIP,
            Port:   int(handler.Http.Port),
            Path:   handler.Http.Path,
            Scheme: handler.Http.Scheme,
        }

    case *api.HealthCheck_Tcp:
        if containerIP == "" {
            return nil, errors.New("container IP required for TCP probe")
        }
        config.TCPSocket = &runtime.TCPProbe{
            Host: containerIP,
            Port: int(handler.Tcp.Port),
        }

    case *api.HealthCheck_Exec:
        config.Exec = &runtime.ExecProbe{
            Command: handler.Exec.Command,
        }

    default:
        return nil, errors.New("unknown probe handler type")
    }

    return config, nil
}
```

### 4.3 Agent: Probe Lifecycle Management

**Modification:** `pkg/agent/agent.go` - `runWorkload()` function

```go
func (a *Agent) runWorkload(ctx context.Context, workload *api.Workload) error {
    // ... existing container creation code ...

    containerID := resp.ID

    // NEW: Get container IP for network probes
    containerIP, err := a.runtime.GetContainerIP(ctx, containerID)
    if err != nil {
        a.logger.Warn("Failed to get container IP, network probes will be skipped",
            zap.String("container_id", containerID),
            zap.Error(err),
        )
        containerIP = "" // Graceful degradation
    }

    // NEW: Extract and start health probes
    if a.config.EnableHealthProbes {
        livenessConfig, readinessConfig, err := a.extractProbeConfigs(
            workload.Spec,
            containerID,
            containerIP,
        )
        if err != nil {
            return fmt.Errorf("extracting probe configs: %w", err)
        }

        // Start liveness probe
        if livenessConfig != nil {
            if err := a.probeExecutor.StartProbing(ctx, livenessConfig); err != nil {
                a.logger.Error("Failed to start liveness probe",
                    zap.String("container_id", containerID),
                    zap.Error(err),
                )
            } else {
                a.logger.Info("Started liveness probe",
                    zap.String("container_id", containerID),
                    zap.Duration("period", livenessConfig.PeriodSeconds),
                )
            }
        }

        // Start readiness probe
        if readinessConfig != nil {
            if err := a.probeExecutor.StartProbing(ctx, readinessConfig); err != nil {
                a.logger.Error("Failed to start readiness probe",
                    zap.String("container_id", containerID),
                    zap.Error(err),
                )
            } else {
                a.logger.Info("Started readiness probe",
                    zap.String("container_id", containerID),
                    zap.Duration("period", readinessConfig.PeriodSeconds),
                )
            }
        }
    }

    // ... rest of function ...
}
```

### 4.4 Agent: Health Monitor (NEW)

**File:** `pkg/agent/health_monitor.go`

```go
package agent

import (
    "context"
    "sync"
    "time"

    "github.com/cloudless/cloudless/pkg/runtime"
    "go.uber.org/zap"
)

// HealthMonitor monitors probe results and triggers actions (restart, reporting)
type HealthMonitor struct {
    probeExecutor *runtime.ProbeExecutor
    runtime       runtime.Runtime
    logger        *zap.Logger

    // Container restart tracking
    mu                sync.RWMutex
    restartAttempts   map[string]int       // containerID -> restart count
    lastRestartTime   map[string]time.Time // containerID -> last restart

    // Configuration
    maxRestarts       int
    restartWindow     time.Duration

    // Callbacks
    onLivenessFailure func(containerID string, consecutiveFailures int)
    onReadinessChange func(containerID string, healthy bool)
}

// NewHealthMonitor creates a new health monitor
func NewHealthMonitor(
    executor *runtime.ProbeExecutor,
    rt runtime.Runtime,
    logger *zap.Logger,
) *HealthMonitor {
    return &HealthMonitor{
        probeExecutor:   executor,
        runtime:         rt,
        logger:          logger.Named("health_monitor"),
        restartAttempts: make(map[string]int),
        lastRestartTime: make(map[string]time.Time),
        maxRestarts:     5,
        restartWindow:   5 * time.Minute,
    }
}

// Start begins monitoring health probes
func (hm *HealthMonitor) Start(ctx context.Context) {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            hm.logger.Info("Health monitor stopping")
            return

        case <-ticker.C:
            hm.checkProbeHealth(ctx)
        }
    }
}

// checkProbeHealth evaluates all probe results and takes action
func (hm *HealthMonitor) checkProbeHealth(ctx context.Context) {
    // Get all probe results from executor
    results := hm.probeExecutor.GetAllProbeResults()

    for key, result := range results {
        // Parse key: "containerID:probeType"
        containerID, probeType := parseProbeKey(key)

        if probeType == "liveness" {
            hm.handleLivenessResult(ctx, containerID, result)
        } else if probeType == "readiness" {
            hm.handleReadinessResult(ctx, containerID, result)
        }
    }
}

// handleLivenessResult checks if liveness failures should trigger restart
func (hm *HealthMonitor) handleLivenessResult(
    ctx context.Context,
    containerID string,
    result *runtime.ProbeResult,
) {
    if result.Healthy {
        return // Container healthy, no action needed
    }

    // Container unhealthy - check if restart needed
    if result.ConsecutiveFailures >= result.FailureThreshold {
        hm.logger.Warn("Container liveness check failed, restarting",
            zap.String("container_id", containerID),
            zap.Int("consecutive_failures", result.ConsecutiveFailures),
            zap.Int("threshold", result.FailureThreshold),
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

                // Callback for agent to report restart
                if hm.onLivenessFailure != nil {
                    hm.onLivenessFailure(containerID, result.ConsecutiveFailures)
                }
            }
        } else {
            hm.logger.Error("Container restart limit exceeded",
                zap.String("container_id", containerID),
                zap.Int("restart_count", hm.getRestartCount(containerID)),
                zap.Int("max_restarts", hm.maxRestarts),
            )
        }
    }
}

// handleReadinessResult detects readiness changes and reports them
func (hm *HealthMonitor) handleReadinessResult(
    ctx context.Context,
    containerID string,
    result *runtime.ProbeResult,
) {
    // Track previous state to detect changes
    // (In real implementation, store previous state)

    if hm.onReadinessChange != nil {
        hm.onReadinessChange(containerID, result.Healthy)
    }
}

// shouldRestart checks if container can be restarted (rate limiting)
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
func (hm *HealthMonitor) getRestartCount(containerID string) int {
    hm.mu.RLock()
    defer hm.mu.RUnlock()
    return hm.restartAttempts[containerID]
}

// restartContainer restarts a container
func (hm *HealthMonitor) restartContainer(ctx context.Context, containerID string) error {
    hm.logger.Info("Restarting container",
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
func (hm *HealthMonitor) SetLivenessFailureCallback(fn func(string, int)) {
    hm.onLivenessFailure = fn
}

// SetReadinessChangeCallback sets callback for readiness changes
func (hm *HealthMonitor) SetReadinessChangeCallback(fn func(string, bool)) {
    hm.onReadinessChange = fn
}

// parseProbeKey parses "containerID:probeType" key
func parseProbeKey(key string) (containerID, probeType string) {
    parts := strings.Split(key, ":")
    if len(parts) == 2 {
        return parts[0], parts[1]
    }
    return key, "unknown"
}
```

### 4.5 Agent: Heartbeat Extension

**Modification:** `pkg/agent/agent.go` - `sendHeartbeat()` function

```go
func (a *Agent) sendHeartbeat(ctx context.Context) error {
    // ... existing heartbeat code ...

    req := &api.HeartbeatRequest{
        NodeId:     a.config.NodeID,
        Containers: containerStatuses,
        Usage:      usage,
        Timestamp:  time.Now().Unix(),
        Region:     a.config.Region,
    }

    // NEW: Add container health status
    if a.config.EnableHealthProbes {
        req.ContainerHealth = a.collectContainerHealth()
    }

    // ... send request ...
}

// collectContainerHealth gathers health probe results
func (a *Agent) collectContainerHealth() []*api.ContainerHealth {
    results := a.probeExecutor.GetAllProbeResults()

    // Group by container ID
    healthMap := make(map[string]*api.ContainerHealth)

    for key, result := range results {
        containerID, probeType := parseProbeKey(key)

        if _, exists := healthMap[containerID]; !exists {
            healthMap[containerID] = &api.ContainerHealth{
                ContainerId: containerID,
            }
        }

        health := healthMap[containerID]

        if probeType == "liveness" {
            health.LivenessHealthy = result.Healthy
            health.LivenessConsecutiveFailures = int32(result.ConsecutiveFailures)
            health.LivenessLastCheckTime = result.LastCheckTime.Unix()
        } else if probeType == "readiness" {
            health.ReadinessHealthy = result.Healthy
            health.ReadinessConsecutiveFailures = int32(result.ConsecutiveFailures)
            health.ReadinessLastCheckTime = result.LastCheckTime.Unix()
        }
    }

    // Convert map to slice
    healthList := make([]*api.ContainerHealth, 0, len(healthMap))
    for _, health := range healthMap {
        healthList = append(healthList, health)
    }

    return healthList
}
```

### 4.6 Coordinator: Heartbeat Processing

**Modification:** `pkg/coordinator/coordinator.go` - `Heartbeat()` RPC

```go
func (c *Coordinator) Heartbeat(
    ctx context.Context,
    req *api.HeartbeatRequest,
) (*api.HeartbeatResponse, error) {

    // ... existing heartbeat processing ...

    // NEW: Process container health updates
    if len(req.ContainerHealth) > 0 {
        if err := c.processContainerHealth(ctx, req.NodeId, req.ContainerHealth); err != nil {
            c.logger.Error("Failed to process container health",
                zap.String("node_id", req.NodeId),
                zap.Error(err),
            )
            // Don't fail heartbeat on health processing errors
        }
    }

    // ... rest of function ...
}

// processContainerHealth updates replica health state
func (c *Coordinator) processContainerHealth(
    ctx context.Context,
    nodeID string,
    containerHealth []*api.ContainerHealth,
) error {

    for _, health := range containerHealth {
        // Find replica by container ID
        replica, err := c.stateManager.GetReplicaByContainerID(health.ContainerId)
        if err != nil {
            c.logger.Warn("Container not found in state",
                zap.String("container_id", health.ContainerId),
                zap.Error(err),
            )
            continue
        }

        // Update replica health
        update := &ReplicaHealthUpdate{
            ReplicaID:                    replica.ID,
            WorkloadID:                   replica.WorkloadID,
            LivenessHealthy:              health.LivenessHealthy,
            LivenessConsecutiveFailures:  int(health.LivenessConsecutiveFailures),
            ReadinessHealthy:             health.ReadinessHealthy,
            ReadinessConsecutiveFailures: int(health.ReadinessConsecutiveFailures),
            Timestamp:                    time.Now(),
        }

        if err := c.stateManager.UpdateReplicaHealth(update); err != nil {
            return fmt.Errorf("updating replica health: %w", err)
        }

        // Update service registry for readiness changes
        if replica.PreviousReadiness != health.ReadinessHealthy {
            c.updateServiceEndpointHealth(
                replica.WorkloadID,
                replica.ID,
                health.ReadinessHealthy,
            )
        }
    }

    return nil
}

// updateServiceEndpointHealth updates overlay registry
func (c *Coordinator) updateServiceEndpointHealth(
    workloadID string,
    replicaID string,
    healthy bool,
) {
    // Find service name from workload
    workload, err := c.stateManager.GetWorkload(workloadID)
    if err != nil {
        c.logger.Error("Workload not found for health update",
            zap.String("workload_id", workloadID),
            zap.Error(err),
        )
        return
    }

    serviceName := workload.Name

    // Update registry
    if err := c.serviceRegistry.UpdateEndpointHealth(serviceName, replicaID, healthy); err != nil {
        c.logger.Error("Failed to update endpoint health",
            zap.String("service", serviceName),
            zap.String("replica_id", replicaID),
            zap.Bool("healthy", healthy),
            zap.Error(err),
        )
    } else {
        c.logger.Info("Updated endpoint health",
            zap.String("service", serviceName),
            zap.String("replica_id", replicaID),
            zap.Bool("healthy", healthy),
        )
    }
}
```

### 4.7 Coordinator: State Manager Extensions

**Modification:** `pkg/coordinator/state_manager.go`

```go
// ReplicaHealthUpdate contains health probe status
type ReplicaHealthUpdate struct {
    ReplicaID                    string
    WorkloadID                   string
    LivenessHealthy              bool
    LivenessConsecutiveFailures  int
    ReadinessHealthy             bool
    ReadinessConsecutiveFailures int
    Timestamp                    time.Time
}

// UpdateReplicaHealth updates replica health state
func (sm *StateManager) UpdateReplicaHealth(update *ReplicaHealthUpdate) error {
    sm.mu.Lock()
    defer sm.mu.Unlock()

    replica, exists := sm.replicas[update.ReplicaID]
    if !exists {
        return fmt.Errorf("replica %s not found", update.ReplicaID)
    }

    // Store previous readiness for change detection
    replica.PreviousReadiness = replica.ReadinessHealthy

    // Update health fields
    replica.LivenessHealthy = update.LivenessHealthy
    replica.LivenessConsecutiveFailures = update.LivenessConsecutiveFailures
    replica.ReadinessHealthy = update.ReadinessHealthy
    replica.ReadinessConsecutiveFailures = update.ReadinessConsecutiveFailures
    replica.LastHealthUpdate = update.Timestamp

    // If liveness persistently failing, mark replica as failed
    if !update.LivenessHealthy && update.LivenessConsecutiveFailures >= 3 {
        replica.Status = ReplicaStatusFailed
        replica.Message = fmt.Sprintf(
            "Liveness probe failed %d consecutive times",
            update.LivenessConsecutiveFailures,
        )

        sm.logger.Warn("Replica marked as failed due to liveness failures",
            zap.String("replica_id", update.ReplicaID),
            zap.String("workload_id", update.WorkloadID),
            zap.Int("failures", update.LivenessConsecutiveFailures),
        )
    }

    return nil
}

// GetUnhealthyReplicas returns replicas with health issues
func (sm *StateManager) GetUnhealthyReplicas() []*Replica {
    sm.mu.RLock()
    defer sm.mu.RUnlock()

    unhealthy := make([]*Replica, 0)

    for _, replica := range sm.replicas {
        if !replica.LivenessHealthy || !replica.ReadinessHealthy {
            unhealthy = append(unhealthy, replica)
        }
    }

    return unhealthy
}

// GetReplicaByContainerID finds replica by container ID
func (sm *StateManager) GetReplicaByContainerID(containerID string) (*Replica, error) {
    sm.mu.RLock()
    defer sm.mu.RUnlock()

    for _, replica := range sm.replicas {
        if replica.ContainerID == containerID {
            return replica, nil
        }
    }

    return nil, fmt.Errorf("no replica found with container_id %s", containerID)
}
```

### 4.8 Coordinator: ReplicaMonitor Integration

**Modification:** `pkg/coordinator/replica_monitor.go`

The existing ReplicaMonitor already handles `ReplicaStatusFailed` replicas. Since we now mark persistently unhealthy replicas as failed in `UpdateReplicaHealth()`, the monitor will automatically reschedule them.

**Optional Enhancement:** Add specific metrics for health-driven rescheduling:

```go
var (
    replicaLivenessFailures = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "cloudless",
            Subsystem: "coordinator",
            Name:      "replica_liveness_failures_total",
            Help:      "Total number of replica liveness probe failures",
        },
        []string{"workload_id"},
    )
)
```

---

## 5. Alternatives Considered

### 5.1 Coordinator-Side Probe Execution

**Approach:** Coordinator executes probes directly against container endpoints.

**Pros:**
- Centralized health view
- Simpler agent code

**Cons:**
- Network overhead (all probes traverse overlay network)
- Coordinator becomes bottleneck
- Doesn't scale to 5k nodes (NFR-P1)
- Higher latency for exec probes

**Decision:** Agent-side execution chosen for performance and scalability.

### 5.2 Pull-Based Health Reporting

**Approach:** Coordinator polls agents for health status.

**Pros:**
- Coordinator controls polling rate
- Simpler retry logic

**Cons:**
- More network calls (poll + heartbeat)
- Delayed health updates
- Violates "push on change" pattern

**Decision:** Push-based reporting via heartbeat chosen for consistency with existing design.

### 5.3 Separate Health Update RPC

**Approach:** New `UpdateHealth()` RPC instead of extending heartbeat.

**Pros:**
- Cleaner API separation
- Health updates independent of heartbeat interval

**Cons:**
- More gRPC connections
- Duplicate node metadata in requests
- Inconsistent with current architecture

**Decision:** Extend heartbeat to leverage existing communication channel.

---

## 6. Performance Impact

### 6.1 Agent Overhead

**Probe Execution:**
- HTTP/TCP probes: 1-10ms per probe
- Exec probes: 10-100ms (depends on command)
- Concurrent execution via worker pool (limit: 100 workers)

**Health Monitor:**
- 5-second check interval
- O(N) complexity where N = number of containers
- Minimal CPU impact (< 1% for 100 containers)

**Heartbeat Payload:**
- Current: ~200 bytes per heartbeat
- With health: ~400 bytes (adds 8 fields × 25 bytes per container)
- Negligible impact on network (heartbeat every 10s)

### 6.2 Coordinator Overhead

**Heartbeat Processing:**
- Additional O(M) work where M = containers per node
- Typical: 1-10 containers per node
- Processing time: < 5ms additional latency

**State Updates:**
- RWMutex contention on `stateManager.mu`
- Mitigated by short critical sections
- Health updates don't block scheduler

**Estimated Impact on NFR-P1:**
- Scheduler decisions: +10ms P50, +20ms P95
- Well within 200ms P50 / 800ms P95 target

---

## 7. Security Considerations

### 7.1 Probe Credential Management

**Issue:** HTTP probes may need authentication headers.

**Mitigation:**
- Store probe credentials in secrets engine
- Deliver via mTLS with TTL
- Never log credentials

**Future Work:** Add `httpHeaders` field to `HTTPProbe` (CLD-REQ-063)

### 7.2 SSRF Prevention

**Issue:** User-controlled HTTP probes could target internal services.

**Mitigation:**
- Validate probe URLs in admission policy
- Block private IP ranges (10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16)
- Limit probe destinations to container IP only

**Implementation:**
```go
func validateHTTPProbe(probe *api.HTTPProbe) error {
    if probe.Host != containerIP {
        return errors.New("HTTP probe host must be container IP")
    }
    return nil
}
```

### 7.3 Exec Probe Command Injection

**Issue:** Malicious workload specs could inject commands.

**Mitigation:**
- Policy engine validates allowed commands
- No shell execution (direct command array)
- Run with container's security context (no privilege escalation)

---

## 8. Testing Plan

### 8.1 Unit Tests

**pkg/agent/health_monitor_test.go:**
- Test liveness failure detection
- Test restart rate limiting
- Test readiness change detection
- Mock ProbeExecutor and Runtime

**pkg/coordinator/state_manager_test.go:**
- Test replica health updates
- Test unhealthy replica queries
- Test concurrent updates (race detector)

**Coverage Target:** 70% statement coverage (per SOP §12.2)

### 8.2 Integration Tests

**test/integration/health_probes_test.go:**

Test scenarios:
1. **Liveness Restart:** Deploy workload with failing liveness probe, verify restart
2. **Readiness Gating:** Deploy workload, fail readiness, verify traffic excluded
3. **Coordinator Integration:** Verify health status in coordinator state
4. **Persistent Failure:** Verify ReplicaMonitor reschedules after multiple restarts

**Environment:** Docker Compose local cluster

### 8.3 Chaos Tests

**test/chaos/health_probes_chaos_test.go:**

Scenarios:
1. **Flapping Probes:** Rapid healthy ↔ unhealthy transitions
2. **Network Partition:** Agent can't reach coordinator during probe failure
3. **Coordinator Failure:** Health updates during coordinator downtime
4. **Mass Failures:** 50% of replicas fail liveness simultaneously

### 8.4 Performance Tests

**Benchmarks:**
- Probe execution throughput
- Heartbeat processing with 100 containers
- State manager update contention

**Target:** No regression in scheduler latency (200ms P50)

---

## 9. Rollout Plan

### 9.1 Feature Flag

**Configuration:**
```yaml
# agent.yaml
enable_health_probes: false  # Default: off
```

**Phased Rollout:**
1. Week 1: Merge code with flag OFF
2. Week 2: Enable in dev cluster, monitor metrics
3. Week 3: Enable for 10% of workloads in staging
4. Week 4: Enable for 50% of workloads
5. Week 5: Enable globally
6. v0.4.0: Remove flag, always enabled

### 9.2 Monitoring During Rollout

**Key Metrics:**
- `cloudless_agent_liveness_probes_total` - Probe success/failure rate
- `cloudless_agent_container_restarts_total{reason="liveness_failure"}` - Restart count
- `cloudless_coordinator_replica_liveness_failures_total` - Coordinator-side tracking
- `cloudless_scheduler_decision_latency_seconds` - Ensure no regression

**Alerts:**
- Restart rate > 10 per minute (potential flapping)
- Probe failure rate > 50% (configuration issues)
- Scheduler latency P95 > 1s (performance regression)

### 9.3 Rollback Plan

If issues detected:
1. Disable feature flag via config update
2. Rolling restart agents (config reload)
3. Workloads continue running without probes
4. Investigate root cause
5. Fix and re-rollout

---

## 10. Open Questions

**Q1:** Should exec probes run in container namespace or host?
**A:** Container namespace (requires nsenter or CRI exec). Document in runbook.

**Q2:** How to handle probe timeout during container restart?
**A:** Stop probes before restart, restart after container running.

**Q3:** Should readiness affect scheduler placement?
**A:** Not in v0.x. Scheduler uses node-level capacity, not replica-level health. Future work.

**Q4:** Metrics for individual probe results or aggregated?
**A:** Aggregated by result (success/failure). Individual probe results in logs.

---

## 11. Success Metrics

### 11.1 Functional Validation

- ✅ Liveness probe failure triggers container restart
- ✅ Readiness probe failure removes endpoint from load balancer
- ✅ Health status visible in `cloudlessctl get workload`
- ✅ ReplicaMonitor reschedules unhealthy replicas

### 11.2 Performance Validation

- ✅ Scheduler latency P50 < 200ms (no regression from baseline)
- ✅ Heartbeat processing < 10ms additional latency
- ✅ Probe execution < 100ms P95

### 11.3 Reliability Validation

- ✅ No race conditions (tested with `-race`)
- ✅ No goroutine leaks (tested with pprof)
- ✅ Graceful degradation when probes misconfigured

---

## 12. Related Work

- **CLD-REQ-031:** Failed replica rescheduling (ReplicaMonitor integration)
- **CLD-REQ-041:** Service registry and stable virtual IPs (traffic gating)
- **CLD-REQ-062:** Least privilege workload execution (exec probe security)
- **CLD-REQ-071:** Event stream for lifecycle events (health events)

---

## 13. References

- Cloudless PRD: `Cloudless.MD` (CLD-REQ-032, lines 82-86)
- Go Engineering SOP: `GO_ENGINEERING_SOP.md`
- Kubernetes Health Checks: https://kubernetes.io/docs/tasks/configure-pod-container/configure-liveness-readiness-startup-probes/
- Existing Implementation: `pkg/runtime/health.go`

---

## Appendix A: Replica State Transitions

```
                    ┌─────────────┐
                    │   Pending   │
                    └──────┬──────┘
                           │
                    Container Created
                           │
                           ▼
                    ┌─────────────┐
              ┌─────┤   Running   ├─────┐
              │     └──────┬──────┘     │
              │            │            │
    Liveness Failed   Readiness    Readiness
        (restart)       Failed      Recovered
              │            │            │
              ▼            ▼            ▼
    ┌─────────────┐ ┌─────────────┐ ┌─────────────┐
    │ Restarting  │ │ Not Ready   │ │   Ready     │
    └──────┬──────┘ └─────────────┘ └─────────────┘
           │
      Restart OK
           │
           ▼
    ┌─────────────┐
    │   Running   │
    └─────────────┘
           │
    Persistent Failure
     (3+ restarts)
           │
           ▼
    ┌─────────────┐
    │   Failed    │ ───▶ ReplicaMonitor reschedules
    └─────────────┘
```

---

**End of Design Document**

**Next Steps:**
1. Review and approval from 2+ package owners
2. Implementation following phased plan
3. Testing at each phase
4. Documentation updates
5. Gradual rollout with monitoring
