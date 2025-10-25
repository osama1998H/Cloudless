Cloudless Platform Implementation Plan

     Based on your detailed product requirements, I'll build the Cloudless distributed compute platform following this structured 
     approach:

     Phase 0: Project Bootstrap & Foundation (Week 1)

     1. Initialize Go Project Structure
       - Create module structure with go.mod (Go 1.23+)
       - Set up directory hierarchy:
       /cmd/agent, /cmd/coordinator, /pkg/api, /pkg/scheduler,
     /pkg/overlay, /pkg/storage, /pkg/policy, /pkg/raft, 
     /pkg/observability, /pkg/common
     2. Development Environment
       - Create Makefile with build targets (build, test, lint, proto)
       - Set up Docker Compose for local multi-node testing
       - Configure GitHub Actions CI/CD pipeline
       - Add golangci-lint configuration
       - Set up protobuf/gRPC tooling
     3. Core Dependencies
       - containerd client libraries for container runtime
       - hashicorp/raft for consensus
       - quic-go for QUIC-based networking (best for NAT traversal & churn handling)
       - grpc-go for control plane communication
       - prometheus client for metrics
       - zap for structured logging

     Phase 1: Security & Membership Foundation (Week 2-3)

     1. mTLS Infrastructure
       - Certificate generation and rotation system
       - SPIFFE-inspired workload identity framework
       - Bootstrap token mechanism for node enrollment
       - Mutual authentication for all control/data paths
     2. RAFT Consensus Layer
       - Implement distributed metadata store
       - Leader election and log replication
       - Snapshot and recovery mechanisms
       - Integration tests for split-brain scenarios
     3. Node Membership
       - Agent enrollment protocol
       - Heartbeat mechanism (5s P50, 15s P95 convergence)
       - Node capability reporting (CPU, RAM, storage, bandwidth)
       - Reliability scoring system

     Phase 2: Core Control Plane (Week 4-5)

     1. Coordinator Service
       - gRPC API server with mTLS
       - Node inventory management
       - Workload lifecycle management
       - Event streaming system
     2. Basic Scheduler
       - Resource matching and bin-packing algorithm
       - Scoring function implementation:
       S = w_l*locality + w_r*reliability + w_c*cost + 
         w_u*utilization_balance - w_n*network_penalty
       - Fragment allocation tracking
       - Health probe framework
     3. Agent Core
       - containerd integration for container management
       - Resource monitoring and reporting
       - cgroups enforcement for fragments
       - Local workload state management

     Phase 3: Data Plane & Networking (Week 6-7)

     1. QUIC Overlay Network
       - Peer-to-peer mesh with QUIC streams
       - NAT traversal with STUN/TURN fallback
       - Encrypted transport by default
       - Service discovery and DNS integration
     2. Load Balancing
       - L4 load balancer implementation
       - Weighted endpoint routing
       - Locality-aware traffic distribution
       - Health-based endpoint selection

     Phase 4: Storage System (Week 8-9)

     1. Distributed Object Store
       - Content-addressed storage with checksums
       - Replication with configurable R factor (default R=3)
       - Anti-entropy repair mechanism
       - Bucket and object management APIs
     2. Storage Classes
       - Ephemeral node-local volumes
       - Hot/cold data placement
       - IOPS-based fragment allocation

     Phase 5: Operations & Polish (Week 10-11)

     1. CLI Tool (cloudlessctl)
       - Command structure: node, workload, storage, network
       - YAML/JSON manifest support
       - Interactive exec and logs
       - Formatted output options
     2. Observability
       - Prometheus metrics endpoint
       - OpenTelemetry tracing
       - Structured logging with correlation IDs
       - Event stream for audit
     3. Testing Suite
       - Unit tests with race detection
       - Integration test harness
       - Chaos testing framework (node churn, network partitions)
       - Performance benchmarks

     Implementation Priorities

     Based on your preferences, I'll implement these components in order:
     1. RAFT consensus - Foundation for distributed state
     2. mTLS authentication - Security from the start
     3. Basic scheduler - Core workload placement
     4. Container runtime - Actually run workloads

     Technical Decisions

     - Container Runtime: containerd (mature, Kubernetes-compatible)
     - Networking: QUIC streams (optimal for NAT traversal and churn tolerance)
     - Testing: Docker Compose for local multi-node simulation
     - Build: Static Go binaries with CGO disabled

     MVP Deliverables (v0.1)

     1. Functional coordinator on single VPS anchor
     2. Linux x86_64/ARM64 agents with secure enrollment
     3. Basic workload scheduling and rescheduling
     4. Encrypted overlay networking
     5. Simple replicated storage (R=2)
     6. CLI and gRPC API
     7. Basic metrics and logs

     Files to Create

     go.mod                          # Go module definition
     Makefile                        # Build automation
     docker-compose.yml              # Local testing environment
     .github/workflows/ci.yml        # CI/CD pipeline
     /cmd/coordinator/main.go        # Coordinator entry point
     /cmd/agent/main.go             # Agent entry point
     /pkg/api/cloudless.proto       # gRPC service definitions
     /pkg/raft/consensus.go         # RAFT implementation
     /pkg/mtls/auth.go             # mTLS authentication
     /pkg/scheduler/scheduler.go    # Scheduling logic
     /pkg/overlay/quic.go          # QUIC networking
     /pkg/agent/runtime.go         # containerd integration
     /config/                      # Configuration examples
     /examples/                    # Sample workload manifests
     /test/                       # Integration tests

     This plan follows your P0-P6 phases while prioritizing the components you selected. The implementation will be incremental, with 
     each phase building on the previous one to create a robust distributed compute platform.



refactoring plan 
  ⎿  Cloudless Refactoring Plan - PRD Alignment

     After analyzing the codebase against Cloudless.MD requirements, I've identified 6 major refactoring opportunities that will bring the 
     implementation into full alignment with the Product Requirements Document.

     ---
     1. Policy Engine Implementation [HIGH PRIORITY]

     Status: pkg/policy/ exists but is EMPTYPRD Reference: CLD-REQ-061, Section 12

     Gap: The PRD requires an OPA-style policy engine embedded in the Coordinator to enforce:
     - Image registry allow/deny lists
     - Container capabilities restrictions
     - Egress destination filtering
     - Custom security policies

     Refactoring Tasks:
     - Create pkg/policy/engine.go - Core policy evaluation engine
     - Create pkg/policy/rules.go - Rule definition and parsing
     - Create pkg/policy/evaluator.go - Policy evaluation against workload specs
     - Integrate with coordinator's workload admission pipeline
     - Add policy violation events to event stream

     Files to Create/Modify:
     - pkg/policy/*.go (new package implementation)
     - pkg/coordinator/coordinator.go (integrate policy checks)
     - pkg/observability/events.go (EventPolicyViolation already defined ✓)

     ---
     2. Secrets Management System [HIGH PRIORITY]

     Status: NOT IMPLEMENTEDPRD Reference: CLD-REQ-063, Section 12

     Gap: No secrets management found in codebase. PRD requires:
     - Envelope encryption for secrets at rest
     - Delivery over mTLS with audience and TTL constraints
     - Short-lived token mechanism

     Refactoring Tasks:
     - Create pkg/secrets/ package
     - Implement envelope encryption (data key + master key pattern)
     - Add secrets storage in RAFT store
     - Create gRPC API for secrets (CreateSecret, GetSecret, RotateSecret)
     - Integrate with agent for secure secret delivery to workloads

     Files to Create:
     - pkg/secrets/manager.go - Secret lifecycle management
     - pkg/secrets/encryption.go - Envelope encryption
     - pkg/secrets/store.go - RAFT-backed persistence
     - pkg/api/cloudless.proto - Add SecretsService RPCs
     - pkg/coordinator/secrets_handler.go - gRPC handlers

     ---
     3. Health Probes Runtime Implementation [MEDIUM PRIORITY]

     Status: Defined in proto, NOT implemented in runtimePRD Reference: CLD-REQ-032

     Gap: Health probes (liveness/readiness) are defined in proto/examples but not executed by the runtime. PRD requires probes to drive:
     - Automated container restart (liveness)
     - Traffic gating (readiness)
     - Replica rescheduling on persistent failures

     Refactoring Tasks:
     - Create pkg/runtime/health.go - Health check executor (HTTP, TCP, Exec)
     - Add probe configuration to container spec
     - Implement periodic probe execution in agent
     - Report probe results in heartbeat to coordinator
     - Trigger replica rescheduling on failed liveness probes

     Files to Create/Modify:
     - pkg/runtime/health.go (new)
     - pkg/agent/agent.go (integrate probe executor)
     - pkg/coordinator/membership/health_monitor.go (react to probe failures)

     ---
     4. MinAvailable Constraint Enforcement [MEDIUM PRIORITY]

     Status: NOT IMPLEMENTEDPRD Reference: CLD-REQ-023, CLD-REQ-030

     Gap: Rolling updates and failure handling don't respect minAvailable replicas. PRD requires:
     - Rollouts keep ≥ minAvailable replicas serving during updates
     - Failed nodes don't reduce service below minAvailable
     - Admission control rejects scale-down that violates minAvailable

     Refactoring Tasks:
     - Add MinAvailable field to RolloutStrategy struct
     - Implement rollout orchestrator that respects minAvailable during updates
     - Add minAvailable check to scale operations
     - Enhance replica rescheduling to maintain minAvailable during node failures

     Files to Modify:
     - pkg/scheduler/scheduler.go - Add RolloutStrategy.MinAvailable field
     - pkg/coordinator/state_manager.go - Add minAvailable validation
     - pkg/coordinator/grpc_handlers.go - Implement rollout orchestration
     - pkg/api/cloudless.proto - Add min_available to RolloutStrategy message

     ---
     5. Scheduler Scoring Function Alignment [LOW PRIORITY]

     Status: Partially aligned, needs refinementPRD Reference: CLD-REQ-021

     Gap: PRD specifies scoring function with negative network penalty:
     S = w_l*locality + w_r*reliability + w_c*cost + w_u*utilization_balance − w_n*network_penalty

     Current implementation treats all weights as positive. Also naming inconsistency: NetworkWeight vs NetworkPenaltyWeight.

     Refactoring Tasks:
     - Rename NetworkWeight → NetworkPenaltyWeight in ScorerConfig
     - Implement network penalty as subtraction (negative contribution to score)
     - Update default weights to match PRD recommendations
     - Add scoring formula documentation in scorer.go

     Files to Modify:
     - pkg/scheduler/scorer.go - Implement negative penalty, rename field
     - pkg/scheduler/scorer_test.go - Update tests for negative penalty
     - pkg/coordinator/coordinator.go - Update default config

     ---
     6. Storage Erasure Coding Support [LOW PRIORITY - Post-MVP]

     Status: NOT IMPLEMENTEDPRD Reference: CLD-REQ-053

     Gap: PRD allows optional erasure coding for cold data when node count ≥ 6. Currently only replication is implemented.

     Refactoring Tasks:
     - Create pkg/storage/erasure.go - Reed-Solomon erasure coding
     - Add EC configuration to StorageConfig (k data + m parity shards)
     - Implement EC encoding/decoding for cold storage class
     - Add node count check (≥ 6) before enabling EC
     - Maintain backward compatibility with replication-only mode

     Files to Create/Modify:
     - pkg/storage/erasure.go (new - Reed-Solomon implementation)
     - pkg/storage/types.go - Add EC config fields
     - pkg/storage/placement.go - Integrate EC into placement decisions
     - pkg/storage/repair.go - Add EC repair logic

     ---
     Summary

     High Priority (MVP Gaps):
     1. Policy Engine - Empty package, core security requirement
     2. Secrets Management - Completely missing

     Medium Priority (Functional Gaps):
     3. Health Probes - Defined but not executed
     4. MinAvailable Constraint - Critical for HA rollouts

     Low Priority (Optimizations):
     5. Scoring Function - Minor alignment issue
     6. Erasure Coding - Post-MVP feature

     Recommendation: Start with items #1-4 as they represent core PRD requirements (CLD-REQ-*) that are missing or incomplete.


     The image pull error is expected - agents are using a dummy container runtime in this test environment. The important part is that scheduling
  works correctly


    Note on Issue #8 (RAFT Initialization Order)

  This issue has a structural dependency conflict: The Membership Manager constructor requires a Store interface (line 158-160 validation in
  membership/manager.go), which is satisfied by RAFT Store. Moving RAFT initialization after Membership Manager would require architectural changes to use
  dependency injection or a two-phase initialization pattern. This should be addressed in a separate architectural refactoring PR.