# Cloudless Platform - Claude Code Documentation

This document provides context for Claude Code instances working on the Cloudless distributed container orchestration platform.

## Project Overview

Cloudless is a distributed compute platform that unifies heterogeneous devices (phones, laptops, PCs, IoT gateways, VPS) into a single elastic compute fabric. It aggregates surplus compute capacity from personal and edge devices into a dependable pool for running containerized applications with high availability and graceful churn handling.

### Three-Plane Architecture

1. **Control Plane**
   - **Coordinator** (`pkg/coordinator/`): Manages cluster state, node enrollment, workload lifecycle
   - **Scheduler** (`pkg/scheduler/`): Makes workload placement decisions using weighted scoring
   - **RAFT Consensus** (`pkg/raft/`): Provides distributed coordination and metadata storage
   - **API Gateway**: gRPC API for client interactions

2. **Data Plane**
   - **Agent** (`pkg/agent/`): Runs on each node, manages local workloads
   - **Runtime** (`pkg/runtime/`): Container runtime integration (containerd)
   - **Overlay Network** (`pkg/overlay/`): QUIC-based mesh networking between nodes
   - **Storage Engine** (`pkg/storage/`): Distributed object store with replication

3. **Observability Plane**
   - **Prometheus**: Metrics collection (coordinator:9090, agents:9092)
   - **Loki**: Log aggregation (port 3100)
   - **Promtail**: Log collection from Docker containers
   - **Grafana**: Visualization and dashboards (port 3001)
   - **Jaeger**: Distributed tracing (UI on port 16686)
   - **OpenTelemetry**: Instrumentation for traces and metrics

### Key Technologies

- **RAFT Consensus**: HashiCorp Raft for coordinator high availability
- **QUIC Protocol**: Network overlay transport with built-in encryption
- **containerd**: Container runtime (CRI compatible)
- **mTLS**: Mutual TLS for all service-to-service communication
- **Protobuf/gRPC**: API definitions and RPC framework
- **JWT Tokens**: Secure node enrollment

## Development Setup

### Prerequisites

- **Go 1.24+** (IMPORTANT: CI requires exactly 1.24, not 1.23)
- **Docker and Docker Compose**
- **Protocol Buffers compiler (protoc)**
- **golangci-lint v1.62.2+** (for Go 1.24 support)

### Quick Start

```bash
# Install dependencies
make mod

# Generate protobuf code
make proto

# Build all binaries (coordinator, agent, cloudlessctl)
make build

# Run tests
make test

# Start local development cluster
make compose-up

# View logs
make compose-logs

# Access services:
# - Grafana: http://localhost:3001 (admin/admin)
# - Prometheus: http://localhost:9091
# - Jaeger: http://localhost:16686
# - Coordinator API: localhost:8080 (gRPC), localhost:8081 (HTTP)

# Stop cluster
make compose-down
```

## Build System

### Important Makefile Targets

```bash
# Building
make build                # Build coordinator, agent, and CLI
make build-linux-amd64    # Cross-compile for Linux AMD64
make build-all            # Build for all platforms

# Testing
make test                 # Run unit tests with coverage
make test-race            # Run with race detector
make test-integration     # Run integration tests (requires -tags=integration)
make test-chaos           # Run chaos tests (requires -tags=chaos)

# Benchmarks
make benchmark            # Run all benchmarks
make bench-scheduler      # Scheduler-specific benchmarks
make bench-storage        # Storage-specific benchmarks
make bench-report         # Save benchmark results to test/benchmarks/

# Code Quality
make lint                 # Run golangci-lint
make fmt                  # Format code
make vet                  # Run go vet
make ci                   # Full CI pipeline locally

# Docker
make docker               # Build coordinator and agent images
make docker-push          # Push images to registry

# Protobuf
make proto                # Regenerate gRPC code from pkg/api/cloudless.proto
```

### Build Tags Strategy

The codebase uses Go build tags to conditionally compile different test suites:

- **`//go:build benchmark`**: Benchmark tests that shouldn't run in CI (incomplete/WIP)
  - `pkg/scheduler/scheduler_bench_test.go`
  - `pkg/observability/observability_bench_test.go`

- **`//go:build integration`**: Integration tests requiring external services
  - `pkg/scheduler/scorer_test.go` (incomplete, excluded from normal runs)
  - Files in `test/integration/`

- **`//go:build chaos`**: Chaos engineering tests
  - Files in `test/chaos/`

**Why this matters**: Regular `make test` and CI runs exclude these tagged tests. This prevents incomplete benchmarks from breaking builds while allowing focused development.

## Architecture Deep Dive

### Scheduler Scoring Algorithm

The scheduler (`pkg/scheduler/scheduler.go`) uses weighted multi-criteria scoring to place workloads:

```go
type Scorer struct {
    LocalityWeight        float64  // 0.3 - Prefer same zone/region
    ReliabilityWeight     float64  // 0.25 - Node uptime/stability
    CostWeight            float64  // 0.15 - Resource pricing
    UtilizationWeight     float64  // 0.2 - Prefer low utilization
    NetworkPenaltyWeight  float64  // 0.1 - Network latency penalty
}
```

**Scoring Flow**:
1. Filter nodes with sufficient capacity (CPU, memory, storage)
2. Apply affinity/anti-affinity rules
3. Calculate composite score (0-100) using weighted factors
4. Sort nodes by score descending
5. Select top-ranked node for placement

**Key Files**:
- `pkg/scheduler/scheduler.go` - Main scheduling logic
- `pkg/scheduler/scorer.go` - Scoring implementation
- `pkg/scheduler/binpacker.go` - Bin packing strategies

### RAFT Consensus

The coordinator uses RAFT for:
- **Leader election**: One coordinator acts as scheduler
- **Log replication**: Workload state, node membership
- **Snapshot/restore**: Periodic state snapshots for recovery

**Configuration**:
- Bootstrap mode: `CLOUDLESS_BOOTSTRAP=true` for first coordinator
- Raft address: `CLOUDLESS_RAFT_ADDR=172.28.0.10:3000`
- Data directory: `/data/raft/` (persistent volume)

**Key Files**:
- `pkg/raft/raft.go` - RAFT wrapper
- `pkg/coordinator/membership/` - Cluster membership using RAFT

### QUIC Overlay Network

Nodes communicate via a QUIC-based mesh overlay:
- **Encryption**: Built into QUIC protocol
- **Connection multiplexing**: Multiple streams per connection
- **NAT traversal**: STUN/TURN support (`pkg/overlay/`)
- **Peer discovery**: Coordinator provides peer list

### Container Runtime Integration

Agents use containerd for container management:
- **Socket**: `/run/containerd/containerd.sock`
- **Namespaces**: Isolated per workload
- **Image pulling**: Multi-registry support
- **Resource limits**: CPU/memory cgroups

**Key Files**:
- `pkg/runtime/containerd.go` - containerd client wrapper
- `pkg/agent/agent.go` - Agent runtime lifecycle

### Security Model

**mTLS Everywhere**:
- Coordinator ↔ Agent: mTLS with client certificates
- Agent ↔ Agent: mTLS via overlay network
- Certificate rotation: Automatic renewal

**Node Enrollment**:
1. Admin generates JWT enrollment token
2. Agent presents token to coordinator
3. Coordinator validates token and issues mTLS certificate
4. Agent uses certificate for all future communication

**Key Files**:
- `pkg/mtls/` - Certificate management
- `pkg/coordinator/enrollment.go` - Token validation
- `deployments/docker/certs/` - Development certificates

## Observability Stack

### Loki Log Aggregation

**Components**:
- **Loki** (172.28.0.33:3100): Log storage with 7-day retention
- **Promtail** (172.28.0.34): Scrapes Docker container logs
- **Grafana**: Log visualization with volume charts

**Configuration**:
- Loki config: `deployments/docker/config/loki/loki-config.yaml`
  - `volume_enabled: true` - Required for Grafana log volume visualization
  - `auth_enabled: false` - Development only (MUST be true in production)
  - Retention: 168h (7 days)
  - Schema: BoltDB shipper with filesystem storage

- Promtail config: `deployments/docker/config/promtail/promtail-config.yaml`
  - Scrapes logs from Docker socket
  - Filters by compose project label
  - Parses JSON logs (level, timestamp, logger, message)
  - Positions file: `/var/lib/promtail/positions.yaml` (persistent volume)

**Why positions matter**: Promtail tracks read positions to avoid duplicate log ingestion on restart. The positions file MUST be on a persistent volume.

### Prometheus Metrics

**Scrape targets**:
- Coordinator: `172.28.0.10:9090`
- Agent-1: `172.28.0.21:9092`
- Agent-2: `172.28.0.22:9092`
- Agent-3: `172.28.0.23:9092`

**Key metrics**:
- `cloudless_scheduler_decisions_total` - Scheduling decisions
- `cloudless_node_cpu_utilization` - Node CPU usage
- `cloudless_workload_placement_latency_seconds` - Placement time

### OpenTelemetry Tracing

**TracerConfig** (as of latest fixes):
```go
type TracerConfig struct {
    Enabled        bool
    ServiceName    string
    ServiceVersion string
    Endpoint       string  // OTLP endpoint (was OTLPEndpoint)
    SampleRate     float64 // Sampling rate 0.0-1.0 (was SamplingRate)
    Insecure       bool    // Skip TLS verification
}
```

**IMPORTANT**: If working with `pkg/observability/observability_bench_test.go`, note that field names changed recently:
- `OTLPEndpoint` → `Endpoint`
- `SamplingRate` → `SampleRate`
- `Environment` → removed, added `Enabled` and `Insecure`

## Docker Compose Local Development

The local cluster (`deployments/docker/docker-compose.yml`) runs:

**Network**: 172.28.0.0/16 bridge network

**Services**:
| Service | IP | Ports | Purpose |
|---------|-----|-------|---------|
| coordinator | 172.28.0.10 | 8080 (gRPC), 8081 (HTTP), 9090 (metrics) | Control plane |
| agent-1 | 172.28.0.21 | - | 2 CPU, 2GB RAM, zone-a |
| agent-2 | 172.28.0.22 | - | 4 CPU, 4GB RAM, zone-b |
| agent-3 | 172.28.0.23 | - | 1 CPU, 1GB RAM, edge device |
| prometheus | 172.28.0.30 | 9091:9090 | Metrics storage |
| grafana | 172.28.0.31 | 3001:3000 | Dashboards |
| jaeger | 172.28.0.32 | 16686 (UI) | Distributed tracing |
| loki | 172.28.0.33 | 3100 | Log aggregation |
| promtail | 172.28.0.34 | - | Log collection |

**Persistent Volumes**:
- `coordinator-data` - RAFT state, certificates
- `agent{1,2,3}-data` - Container images, workload state
- `prometheus-data` - Time-series metrics
- `grafana-data` - Dashboard configs
- `loki-data` - Log storage (BoltDB + chunks)
- `promtail-data` - Log positions file

**Health Checks**:
- Coordinator: `wget http://localhost:8081/health`
- Loki: `wget http://localhost:3100/ready`
- Promtail: `wget http://localhost:9080/ready`

**Service Dependencies**:
```
grafana → [prometheus, loki]
promtail → loki
agents → coordinator
```

## CI/CD Context

### GitHub Actions Workflow

**Required Versions**:
- Go: `1.24` (NOT 1.23 - causes sync/atomic import errors)
- golangci-lint: `v1.62.2` (NOT v1.55.2 - doesn't support Go 1.24)
- actions/upload-artifact: `v4` (v3 deprecated)
- github/codeql-action/upload-sarif: `v3` (v2 deprecated)

**Why Go 1.24 specifically**:
- `go.mod` requires `go 1.24.0` with `toolchain go1.24.1`
- golangci-lint v1.55.2 doesn't support Go 1.24's sync/atomic changes
- This caused CI failures with "could not import sync/atomic (unsupported version: 2)"

### Common CI Failure Patterns

**1. Undefined package errors**:
- **Symptom**: `undefined: yaml`, `undefined: jwt`, `undefined: quic`
- **Cause**: Go version mismatch between CI and go.mod
- **Fix**: Update CI GO_VERSION to match go.mod

**2. Build tag exclusions**:
- **Symptom**: Incomplete benchmark/integration tests causing compilation errors
- **Cause**: Tests not excluded from normal runs
- **Fix**: Add `//go:build benchmark` or `//go:build integration` tags

**3. TracerConfig field errors**:
- **Symptom**: `unknown field OTLPEndpoint` in struct literal
- **Cause**: Observability API changed
- **Fix**: Use `Endpoint`, `SampleRate`, `Enabled`, `Insecure` fields

## Common Development Tasks

### Adding a New Workload Type

1. Define protobuf message in `pkg/api/cloudless.proto`
2. Run `make proto` to regenerate Go code
3. Add scheduling logic to `pkg/scheduler/`
4. Implement runtime handler in `pkg/runtime/`
5. Add metrics to `pkg/observability/`
6. Write tests with appropriate build tags

### Debugging Scheduler Decisions

```bash
# Enable debug logging
export CLOUDLESS_LOG_LEVEL=debug

# Query Prometheus for scoring metrics
curl 'http://localhost:9091/api/v1/query?query=cloudless_scheduler_node_score'

# Check Loki logs for scheduling decisions
curl 'http://localhost:3100/loki/api/v1/query_range' \
  --data-urlencode 'query={component="coordinator",logger="scheduler"}' \
  --data-urlencode 'start=1h' | jq

# Use Grafana Explore to correlate logs and metrics
```

### Testing Network Overlay

```bash
# Start cluster
make compose-up

# Exec into agent-1
docker exec -it cloudless-agent-1 /bin/sh

# Test QUIC connectivity to agent-2
# (Overlay network automatically establishes mesh)

# Check overlay metrics
curl http://localhost:9092/metrics | grep overlay
```

### Running Chaos Tests

```bash
# Requires -tags=chaos
make test-chaos

# Or manually:
go test -v -tags=chaos ./test/chaos/...

# Example chaos scenarios:
# - Coordinator failover (RAFT leader election)
# - Network partitions
# - Node churn (agents joining/leaving)
# - Resource exhaustion
```

## Recent Important Changes

### Loki Integration (October 2024)
- Added Loki and Promtail services to docker-compose
- Configured log volume visualization (`volume_enabled: true`)
- Fixed Promtail positions file path to use persistent volume
- Added health checks to ensure service readiness

### CI Fixes (October 2024)
- Migrated from Go 1.23 to Go 1.24
- Upgraded golangci-lint from v1.55.2 to v1.62.2
- Fixed TracerConfig field mismatches in benchmark tests
- Added build tags to exclude incomplete tests from CI
- Updated deprecated GitHub Actions (upload-artifact v3→v4, codeql-action v2→v3)

### Security Improvements
- Added documentation warnings for development-only configs
- Documented production requirements (auth_enabled: true, stream limits)
- Improved TLS certificate management

## Project Structure

```
.
├── cmd/
│   ├── coordinator/    # Coordinator entry point
│   ├── agent/          # Agent entry point
│   └── cloudlessctl/   # CLI tool
├── pkg/
│   ├── api/            # Protobuf/gRPC definitions
│   ├── coordinator/    # Coordinator implementation
│   │   └── membership/ # RAFT-based cluster membership
│   ├── agent/          # Agent implementation
│   ├── scheduler/      # Scheduling logic (scorer, binpacker)
│   ├── overlay/        # QUIC network overlay
│   ├── storage/        # Distributed object store
│   ├── raft/           # RAFT consensus wrapper
│   ├── runtime/        # Container runtime integration
│   ├── mtls/           # Certificate management
│   ├── policy/         # Security policies (OPA-style)
│   └── observability/  # Metrics, logging, tracing
├── deployments/
│   └── docker/         # Docker Compose configs
│       ├── config/     # Service configurations
│       │   ├── loki/
│       │   ├── promtail/
│       │   ├── prometheus/
│       │   └── grafana/
│       ├── certs/      # Development mTLS certificates
│       └── docker-compose.yml
├── config/             # Example configurations
├── examples/           # Example workloads
│   └── workloads/
├── test/
│   ├── integration/    # Integration tests (-tags=integration)
│   └── chaos/          # Chaos tests (-tags=chaos)
├── scripts/            # Utility scripts
├── go.mod              # Go 1.24.0 + toolchain go1.24.1
├── Makefile            # Build automation
├── README.md           # User-facing documentation
└── CLAUDE.md           # This file
```

## Tips for Claude Code Instances

1. **Always check go.mod**: This project requires Go 1.24+. Don't assume 1.23 works.

2. **Use build tags appropriately**: When writing tests, decide if they should run in CI:
   - Normal unit tests: No tag
   - Integration tests requiring services: `//go:build integration`
   - Benchmarks not ready for CI: `//go:build benchmark`
   - Chaos tests: `//go:build chaos`

3. **Regenerate proto after API changes**: Always run `make proto` after editing `pkg/api/cloudless.proto`

4. **Check TracerConfig fields**: The observability API changed recently. Use `Endpoint`, `SampleRate`, `Enabled`, `Insecure`.

5. **Loki log volume**: Grafana log visualization requires `volume_enabled: true` in Loki config.

6. **Positions file persistence**: Promtail needs persistent volume for `/var/lib/promtail/positions.yaml`

7. **Local development**: Use `make compose-up` for full stack, not individual Docker runs

8. **Debugging**: Set `CLOUDLESS_LOG_LEVEL=debug` and use Grafana Explore to correlate logs/metrics

9. **CI failures**: Check Go version, golangci-lint version, and build tags first

10. **Security warnings**: Never commit changes that remove security warnings in config files (auth_enabled, InsecureSkipVerify, etc.)

## Performance Targets

- Scheduler decisions: 200ms P50, 800ms P95 (5k nodes)
- Membership convergence: 5s P50, 15s P95
- Failed replica rescheduling: 3s P50, 10s P95
- Coordinator availability: 99.9% monthly

These targets drive architectural decisions (RAFT for HA, QUIC for low-latency networking, weighted scoring for fast decisions).

## Go Engineering Standards

### GO_ENGINEERING_SOP.md

**Location:** `/GO_ENGINEERING_SOP.md`

**Purpose:** Comprehensive Standard Operating Procedure for Go development on Cloudless

**When to Use:**
- 📋 **Before starting work** - Review relevant sections for the component you're working on
- 🔍 **During code review** - Reference standards for review feedback
- 🐛 **When debugging** - Consult debugging checklists and patterns
- ✅ **Before submitting PR** - Use checklists to ensure compliance
- 🚀 **Before release** - Follow release management procedures

**Key Sections by Task:**

| **Your Task** | **Consult These Sections** | **Why** |
|---------------|---------------------------|---------|
| Adding new package | §2 (Repository Layout), §26.1 (New Package Checklist) | Ownership, structure, documentation requirements |
| Writing concurrent code | §5 (Concurrency Policy), §26.3 (Concurrency Checklist) | Goroutine lifecycle, channel ownership, deadlock prevention |
| Adding gRPC RPC | §6 (Networking), §11 (API Compatibility), §26.2 (RPC Checklist) | Context handling, error codes, idempotency, tracing |
| Implementing storage | §7 (Storage Standards) | Durability, checksums, replication, garbage collection |
| Security-sensitive code | §8 (Security Requirements) | mTLS, secrets, input validation, policy enforcement |
| Adding metrics/logging | §9 (Observability) | Structured logging, Prometheus metrics, OpenTelemetry tracing |
| Writing tests | §12 (Testing Policy), test/ structure | Unit, integration, chaos, benchmark test guidelines |
| Performance optimization | §13 (Performance), profiling | Performance targets, profiling, memory management |
| Preparing for release | §17 (Release Management), §26.4 (Release Checklist) | Versioning, canary deployments, rollback procedures |
| On-call debugging | §18 (Runbooks), §24 (Triage) | Incident response, common issues, debugging workflows |

**CLAUDE.md vs GO_ENGINEERING_SOP.md:**

| Use **CLAUDE.md** when... | Use **GO_ENGINEERING_SOP.md** when... |
|--------------------------|--------------------------------------|
| Understanding architecture | Learning coding standards |
| Quick context for AI assistance | Writing production code |
| Finding where code lives | Understanding how to write code |
| Debugging specific components | Following development procedures |
| Understanding recent changes | Reviewing code quality requirements |
| Setting up local development | Understanding CI/CD pipeline |

**Critical Reminders from SOP:**

1. **Concurrency** (§5)
   - NO unbounded goroutines - use worker pools or semaphores
   - Context cancellation - respect `ctx.Done()`
   - Channels - sender closes, document ownership
   - Run tests with `-race` detector

2. **Error Handling** (§4)
   - Check errors immediately
   - Use `fmt.Errorf` with `%w` for wrapping
   - Define sentinel errors for expected conditions
   - NEVER panic in library code

3. **Security** (§8)
   - mTLS everywhere - NO plaintext communication
   - Validate ALL external input
   - NEVER hardcode secrets
   - Use policy engine for admission control

4. **Observability** (§9)
   - Structured logging with `zap`
   - Prometheus metrics for all operations
   - OpenTelemetry tracing for distributed calls
   - Follow metric naming conventions

5. **Testing** (§12)
   - Minimum 70% statement coverage
   - Use build tags: `//go:build benchmark|integration|chaos`
   - Table-driven tests for multiple scenarios
   - Run with race detector in CI

6. **Performance** (§13)
   - Scheduler: 200ms P50, 800ms P95
   - Membership: 5s P50, 15s P95
   - Profile with pprof before optimizing
   - Avoid allocations in hot paths

**Quick Decision Guide:**

```
┌─────────────────────────────────────────┐
│ Need to understand the codebase?       │
│ → Read CLAUDE.md                        │
└─────────────────────────────────────────┘
                    │
┌─────────────────────────────────────────┐
│ Need to write/review code?             │
│ → Follow GO_ENGINEERING_SOP.md          │
└─────────────────────────────────────────┘
                    │
┌─────────────────────────────────────────┐
│ Need product requirements?              │
│ → Read Cloudless.md (PRD)               │
└─────────────────────────────────────────┘
```

**Example Workflow:**

1. **Receive task:** "Add new scheduler plugin for GPU affinity"
2. **Read CLAUDE.md §Scheduler** → Understand architecture
3. **Read Cloudless.md CLD-REQ-020** → Understand requirements
4. **Read GO_ENGINEERING_SOP.md §26.1** → New package checklist
5. **Read GO_ENGINEERING_SOP.md §12** → Testing requirements
6. **Implement** following standards from SOP
7. **Review** using §15 (Code Review) checklist
8. **Submit PR** with all checklist items complete

**Test Structure Reference:**

As of the recent test restructuring (see test/ directory):
- **Unit tests:** Co-located with source (`pkg/*/\*_test.go`)
- **Shared utilities:** `test/testutil/` (helpers, mocks, fixtures)
- **Integration tests:** `test/integration/` (build tag: `integration`)
- **Chaos tests:** `test/chaos/` (build tag: `chaos`)
- **Benchmarks:** Tagged with `//go:build benchmark`

Run tests:
```bash
# Unit tests (default)
make test

# Integration tests
make test-integration  # or: go test -tags=integration ./test/integration/...

# Chaos tests
make test-chaos  # or: go test -tags=chaos ./test/chaos/...

# Benchmarks
make benchmark  # or: go test -tags=benchmark -bench=. ./pkg/...
```

**Enforcement:**

The SOP is enforced through:
- ✅ **CI pipeline** - Automated checks for formatting, linting, tests
- 👥 **Code review** - Reviewers use SOP as checklist
- 📊 **Metrics** - Track coverage, performance regressions
- 🔒 **Security scans** - gosec, dependency vulnerabilities
- 📝 **PR template** - Reminds contributors of requirements

**Updates:**

The SOP follows semantic versioning (currently v1.0). If you find:
- Outdated information
- Missing guidance
- Contradictions with current practices

Please submit a PR to update GO_ENGINEERING_SOP.md.

## Getting Help

- **GitHub Issues**: https://github.com/osama1998H/Cloudless/issues
- **README.md**: User-facing quick start and feature list
- **Makefile**: Run `make help` for all available commands
- **CLAUDE.md (this file)**: Architecture context and non-obvious patterns
- **GO_ENGINEERING_SOP.md**: Engineering standards and procedures
- **Cloudless.md**: Product requirements and design goals
