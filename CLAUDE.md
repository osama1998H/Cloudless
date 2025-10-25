# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

Cloudless is a distributed compute platform that unifies heterogeneous devices (phones, laptops, PCs, IoT gateways, VPS) into a single elastic compute fabric. The platform provisions virtual resources (CPU, memory, storage, bandwidth) to run containerized applications with high availability and graceful churn handling.

## Architecture Components

The system is divided into three main planes:

1. **Control Plane**: Coordinator, Scheduler, Metadata Store (RAFT), API Gateway
2. **Data Plane**: Node Agent, Resource Monitor, Overlay Networking, Storage Engine
3. **Observability Plane**: Metrics, logs, traces, event stream

## Development Setup

### Project Structure (Implemented)
```
/cmd/agent         - Node agent entry point
/cmd/coordinator   - Coordinator service entry point
/pkg/api          - API type definitions (placeholder for protobuf)
/pkg/scheduler    - Workload scheduling with multi-factor scoring
/pkg/runtime      - Container runtime integration (containerd)
/pkg/agent        - Agent implementation with resource monitoring
/pkg/coordinator  - Coordinator and membership management
/pkg/mtls         - Certificate Authority and security
/pkg/raft         - RAFT consensus implementation
/pkg/overlay      - Network overlay implementation
/pkg/storage      - Distributed object store
/pkg/policy       - Security policy engine
/pkg/observability - Metrics, logging, tracing
/pkg/common       - Shared utilities
```

### Build Commands
```bash
# Build all binaries
make build

# Build specific components
make build-agent
make build-coordinator

# Run tests
make test

# Run with race detector
make test-race

# Run integration tests
make test-integration

# Run chaos tests
make test-chaos

# Generate coverage report
make coverage

# Run linting
make lint

# Format code
make fmt

# Generate protobuf/gRPC code (if protobuf file exists)
make proto

# Cross-compile for Linux AMD64/ARM64
make build-linux-amd64
make build-linux-arm64

# Download and tidy dependencies
make mod

# Full CI pipeline
make ci
```

### Development Requirements
- Go 1.23+
- containerd v1.7+ for container runtime
- Protocol Buffers compiler (protoc) for gRPC code generation
- Docker and Docker Compose for local testing

### Running Locally
```bash
# Start local development cluster with Docker Compose
make compose-up

# View logs
make compose-logs

# Stop cluster
make compose-down

# Or run individual components:
make run-coordinator
make run-agent
```

## Implemented Components (v0.1)

### Phase 1: Core Infrastructure ✅
- ✅ mTLS-based secure node enrollment
- ✅ Certificate Authority with auto-rotation
- ✅ SPIFFE-like workload identities
- ✅ Bootstrap tokens for authentication
- ✅ RAFT consensus for coordinator clustering
- ✅ Heartbeat and membership management
- ✅ Node state tracking and lifecycle

### Phase 2: Scheduling ✅
- ✅ Multi-factor resource scoring:
  - Locality (region/zone preferences)
  - Reliability (uptime, conditions, success rate)
  - Cost (resource efficiency)
  - Utilization (balanced placement)
  - Network (bandwidth, features)
- ✅ Bin-packing algorithms:
  - First-fit decreasing
  - Best-fit
  - Spread-aware placement
- ✅ Affinity/anti-affinity rules
- ✅ Workload filtering and validation

### Phase 3: Runtime Integration ✅
- ✅ containerd runtime integration
- ✅ Container lifecycle management
- ✅ Image pull and management
- ✅ Container stats collection
- ✅ Log streaming
- ✅ Resource monitoring (CPU, memory, disk, network)

### Phase 4: Networking & Storage (Planned)
- Encrypted overlay (QUIC streams)
- Service discovery with stable virtual IPs
- Replicated object store with R=2 (MVP) or R=3 (default)

## Architecture Deep Dive

### Coordinator Integration
The coordinator (`pkg/coordinator/coordinator.go`) wires together multiple subsystems:

**Initialization Order (Critical):**
1. Create data directories (raft, ca)
2. Initialize Certificate Authority
3. Initialize Token Manager
4. Initialize Membership Manager
5. Initialize Scheduler
6. Initialize RAFT Store

**Startup Order (Critical):**
1. Open RAFT store and wait for leader election (30s timeout)
2. Bootstrap cluster if `RaftBootstrap=true`
3. Start Membership Manager
4. Ready to accept requests

**Leader Operations:**
- Only the RAFT leader can schedule workloads
- Non-leaders redirect requests with leader address
- Leader election uses `raftStore.IsLeader()` and `raftStore.Leader()`

### Agent Integration
The agent (`pkg/agent/agent.go`) integrates:

**Components:**
- containerd runtime for container execution
- Resource monitor for system metrics (gopsutil)
- gRPC connection to coordinator
- Heartbeat loop for status updates

**Initialization:**
1. Create data directories
2. Initialize containerd runtime (default socket: `/run/containerd/containerd.sock`)
3. Initialize resource monitor (5s polling interval)
4. Connect to coordinator via gRPC

**Heartbeat Flow:**
1. Collect resource snapshot (CPU, memory, disk, network)
2. List running containers from containerd
3. Send heartbeat to coordinator
4. Process response for new workload assignments

### Scheduler Scoring System
The scheduler (`pkg/scheduler/scorer.go`) uses a configurable multi-factor scoring algorithm:

**Scoring Formula:**
```
TotalScore = LocalityScore × LocalityWeight +
             ReliabilityScore × ReliabilityWeight +
             CostScore × CostWeight +
             UtilizationScore × UtilizationWeight +
             NetworkScore × NetworkWeight +
             AffinityAdjustments
```

**Default Weights (normalized to sum to 1.0):**
- Locality: 0.25
- Reliability: 0.30
- Cost: 0.15
- Utilization: 0.20
- Network: 0.10

**Adding New Scoring Factors:**
1. Add score calculation method to `Scorer` struct
2. Add field to `ScorerConfig` for the weight
3. Update `NewScorer()` normalization logic
4. Include in `ScoreNode()` total calculation
5. Add to `ScoreComponents` struct for debugging

**Bin Packing Algorithms:**
- `Pack()`: First-fit decreasing (sorts bins by score, items by size)
- `PackBestFit()`: Minimizes waste per placement
- `PackWithSpread()`: Spreads replicas across topology keys (zone/region/node)

### Membership Management
The membership manager (`pkg/coordinator/membership/manager.go`) tracks node lifecycle:

**Node States:**
- `enrolling`: Initial state during join
- `ready`: Node is healthy and schedulable
- `draining`: Node is being gracefully drained
- `cordoned`: Node is marked unschedulable
- `offline`: Node missed heartbeats
- `failed`: Node has failed and needs attention

**Enrollment Flow:**
1. Agent sends EnrollNodeRequest with join token
2. Manager validates token
3. CA issues node certificate
4. Node added to membership with `enrolling` state
5. First successful heartbeat transitions to `ready`

**Heartbeat Processing:**
1. Update `LastHeartbeat` timestamp
2. Update resource capacity and usage
3. Update container states
4. Update node conditions (MemoryPressure, DiskPressure, etc.)
5. Check for workload assignments
6. Return next heartbeat interval

**Reliability Scoring:**
- Uptime score (30% weight)
- Response time score (20% weight)
- Success rate score (20% weight)
- Capacity score (15% weight)
- Stability score (15% weight)

### RAFT Consensus
The RAFT store (`pkg/raft/store.go` and `fsm.go`) provides distributed consensus:

**Operations:**
- `Apply()`: Submit command to FSM (leader only)
- `Join()`: Add new coordinator to cluster
- `Remove()`: Remove coordinator from cluster
- `Bootstrap()`: Initialize single-node cluster

**FSM State Management:**
- Node registrations stored in-memory
- Workload assignments tracked
- Snapshots taken every 1024 commands
- BoltDB used for log storage

### Container Runtime
The runtime abstraction (`pkg/runtime/`) provides a unified interface:

**Interface Methods:**
- `CreateContainer()`: Creates container from spec
- `StartContainer()`: Starts a created container
- `StopContainer()`: Gracefully stops (SIGTERM then SIGKILL after timeout)
- `DeleteContainer()`: Removes container and snapshot
- `GetContainer()`: Gets container status
- `ListContainers()`: Lists all containers in namespace
- `PullImage()`: Pulls image from registry
- `GetContainerStats()`: Retrieves resource usage
- `GetContainerLogs()`: Streams container logs

**containerd Integration:**
- Uses namespace "cloudless" by default
- Default socket: `/run/containerd/containerd.sock`
- Snapshots managed per container
- OCI spec constructed from ContainerSpec

### Security (mTLS)
The mTLS subsystem (`pkg/mtls/`) handles all security:

**Certificate Authority:**
- RSA 4096-bit root CA
- 10-year validity for CA cert
- 1-year validity for node/workload certs
- Automatic rotation before expiry

**Certificate Types:**
- Node certificates: Issued during enrollment
- Workload certificates: Issued per container
- Service certificates: For gRPC services

**SPIFFE Integration:**
- SPIFFE IDs: `spiffe://cloudless/node/{nodeID}` and `spiffe://cloudless/workload/{workloadID}`
- Embedded in certificate SAN

**Token Management:**
- JWT-based bootstrap tokens
- 24-hour default expiry
- bcrypt-hashed secrets
- One-time use enforced

## Performance Targets
- Scheduler decisions: 200ms P50, 800ms P95 (5k nodes)
- Membership convergence: 5s P50, 15s P95
- Failed replica rescheduling: 3s P50, 10s P95
- Coordinator availability: 99.9% monthly

## Important Implementation Constraints

### Module Structure
- Go module: `github.com/cloudless/cloudless`
- All internal imports use full path (no relative imports)
- CGO_ENABLED=0 for cross-compilation

### Dependency Versions
- Go 1.23+
- containerd v1.7.x (not v2.x due to API changes)
- HashiCorp RAFT v1.6+
- gopsutil v3.23+

### Build Flags
- Static binaries with `-ldflags="-s -w"` for size reduction
- Version info injected at build time
- Race detector available via `make test-race`

### Container Runtime Requirements
- containerd must be installed on agent nodes
- containerd socket must be accessible
- Namespace isolation per cluster
- Snapshotter support required