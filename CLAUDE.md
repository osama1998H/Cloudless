# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Overview

Cloudless is a distributed compute platform that aggregates heterogeneous devices (phones, laptops, PCs, IoT gateways, VPS) into a single elastic compute fabric. The platform runs containerized applications with high availability and graceful churn handling.

**Key Characteristics:**
- System-level distributed infrastructure (not a web app)
- High reliability requirements with frequent node churn
- mTLS everywhere - no plaintext communication
- Performance-critical: scheduler decisions in 200ms P50, 800ms P95
- Go 1.23+, uses Protocol Buffers/gRPC for APIs

## Build and Test Commands

### Building
```bash
# Build all binaries (coordinator, agent, CLI)
make build

# Build individual components
make build-coordinator
make build-agent
make build-cli

# Cross-compile for Linux AMD64 (for deployment)
make build-linux-amd64

# Generate protobuf/gRPC code (required after .proto changes)
make proto
```

### Testing
```bash
# Run unit tests with coverage
make test

# Run tests with race detector (IMPORTANT: always run before committing concurrent code)
make test-race

# Run integration tests (requires Docker)
make test-integration

# Run chaos tests (simulates failures)
make test-chaos

# Generate coverage report
make coverage

# Run benchmarks
make benchmark
make bench-scheduler   # Scheduler-specific benchmarks
make bench-storage     # Storage-specific benchmarks
```

### Local Development Cluster
```bash
# Start local 3-node cluster with Docker Compose
make compose-up

# View logs from all services
make compose-logs

# Stop cluster
make compose-down
```

**Note on Bootstrap Tokens:**
The local docker-compose environment automatically generates bootstrap tokens for node enrollment via the `CLOUDLESS_GENERATE_BOOTSTRAP_TOKENS=1` environment variable. This is ONLY for development convenience. Production deployments must NEVER enable this flag and should use the proper token management API with short-lived, scoped tokens (see Security section below).

### Code Quality
```bash
# Format code (MUST run before committing)
make fmt

# Run linters (MUST pass before PR)
make lint

# Run full CI pipeline locally
make ci
```

### Running Single Test
```bash
# Run a specific test by name
go test -v -run TestScheduler_Schedule ./pkg/scheduler/

# Run with race detector
go test -v -race -run TestScheduler_Schedule ./pkg/scheduler/

# Run tests in a specific package
go test -v ./pkg/scheduler/
```

## Architecture Overview

### Three-Plane Architecture

**Control Plane** (`pkg/coordinator/`, `cmd/coordinator/`)
- **Coordinator**: Main control plane service, handles node enrollment, workload management
- **Scheduler** (`pkg/scheduler/`): Multi-criteria workload placement using weighted scoring
- **RAFT Store** (`pkg/raft/`): Distributed consensus for metadata (wraps hashicorp/raft)
- **Membership** (`pkg/coordinator/membership/`): Node inventory, health tracking, failure detection

**Data Plane** (`pkg/agent/`, `cmd/agent/`)
- **Agent**: Runs on each node, executes containers, reports resources
- **Runtime** (`pkg/runtime/`): Container lifecycle management (containerd integration)
- **Storage** (`pkg/storage/`): Distributed object store with replication (R=2 default)
- **Overlay** (`pkg/overlay/`): QUIC-based peer-to-peer networking with NAT traversal

**Observability Plane** (`pkg/observability/`)
- Structured logging with Zap (NOT fmt.Println or log.Print)
- Prometheus metrics for all critical paths
- OpenTelemetry tracing for distributed requests
- Event streaming for audit logs

### Key Packages and Their Roles

**`pkg/api/cloudless.proto`**
- Single source of truth for all gRPC APIs
- Defines CoordinatorService, AgentService, StorageService, NetworkService
- NEVER break backward compatibility (append only, use field numbers carefully)

**`pkg/scheduler/`**
- **scheduler.go**: Main scheduling logic, orchestrates scoring and placement
- **scorer.go**: Multi-criteria scoring function (locality, reliability, cost, utilization)
- **binpack.go**: Bin-packing algorithm for efficient resource utilization
- **rollout.go**: Rolling update strategies (MinAvailable, MaxSurge)
- Performance target: 200ms P50, 800ms P95 for scheduling decisions

**`pkg/raft/`**
- Wraps hashicorp/raft for distributed consensus
- Used by coordinator for strongly consistent metadata (node inventory, workload state)
- Handles leader election, log replication, snapshots
- Critical for cluster coordination - test thoroughly

**`pkg/coordinator/membership/`**
- Manages node lifecycle: enrollment, heartbeats, failure detection
- Implements SWIM-style gossip for membership convergence
- Scoring nodes for scheduler (health, capacity, reliability metrics)
- Target: 5s P50, 15s P95 for membership convergence

**`pkg/storage/`**
- Content-addressed storage using SHA-256
- Replication factor R=2 (configurable)
- Chunk-based storage with deduplication
- Anti-entropy repair for under-replicated data
- Implements S3-compatible API

**`pkg/overlay/`**
- QUIC-based overlay network (UDP-based, encrypted)
- NAT traversal using STUN/TURN and hole-punching
- Service mesh with load balancing and health checks
- Handles high churn (nodes joining/leaving frequently)

**`pkg/mtls/`**
- Certificate Authority (CA) for mTLS certificates
- SPIFFE-like workload identities
- Certificate rotation and renewal
- ALL network communication uses mTLS (no exceptions)

**`pkg/secrets/`**
- Secrets management (CLD-REQ-063)
- AES-256-GCM encryption at rest
- JWT-based access tokens with TTL
- Key rotation support

**`pkg/policy/`**
- OPA-style policy engine for admission control
- Validates workload placement against security policies
- Enforces resource quotas and constraints

**`pkg/observability/`**
- Structured logging with Zap (use logger.Info/Error/Warn, not fmt.Println)
- Prometheus metrics (counters, histograms, gauges)
- OpenTelemetry tracing spans
- gRPC interceptors for automatic tracing

### Critical Design Patterns

**Error Handling**
- Always wrap errors with context: `fmt.Errorf("operation failed: %w", err)`
- Use sentinel errors for expected conditions: `var ErrNotFound = errors.New("not found")`
- Return structured errors for gRPC: `status.Errorf(codes.NotFound, "node %s not found", id)`
- NEVER ignore errors silently

**Concurrency**
- ALWAYS use context.Context for cancellation and timeouts
- NO unbounded goroutines - use worker pools or semaphores
- Channel ownership: sender closes the channel
- Run tests with `-race` flag before committing
- Mutex lock scope: keep critical sections SMALL (no I/O under locks)

**gRPC Services**
- First parameter is always `context.Context`
- Respect context cancellation and deadlines
- Validate ALL input fields
- Map internal errors to appropriate gRPC status codes
- Add tracing spans for distributed operations
- Return proper gRPC error codes (codes.NotFound, codes.InvalidArgument, etc.)

**Metrics and Observability**
- Add Prometheus metrics for all operations (success/failure counts, latency histograms)
- Use structured logging: `logger.Info("msg", zap.String("key", value))`
- Add tracing spans for multi-step operations
- Metric naming: `cloudless_<subsystem>_<name>_<unit>` (e.g., `cloudless_scheduler_decisions_total`)

**Storage and Persistence**
- Use fsync for durability guarantees
- Validate checksums on read
- Version all persisted data structures
- Implement anti-entropy repair for distributed data

**Security**
- NO plaintext communication - mTLS everywhere
- Validate ALL external input
- NEVER hardcode secrets - use environment variables or secrets manager
- NEVER enable bootstrap token auto-generation in production (`CLOUDLESS_GENERATE_BOOTSTRAP_TOKENS`)
- Run with least privilege
- Use policy engine for admission control

## Development Workflow

### Making Changes

1. **Create feature branch**: `git checkout -b feat/my-feature`
2. **Make changes** following the patterns in GO_ENGINEERING_SOP.md
3. **Generate proto** (if changed): `make proto`
4. **Run tests**: `make test-race` (race detector is critical)
5. **Format code**: `make fmt`
6. **Run linters**: `make lint`
7. **Verify local cluster**: `make compose-up && make compose-logs`
8. **Commit with conventional format**: `git commit -m "feat: add scheduler affinity rules"`
9. **Create PR** with proper description (see .github/PULL_REQUEST_TEMPLATE.md)

### Testing Philosophy

- **Unit tests**: Co-located with source (e.g., `scheduler_test.go`)
- **Minimum 70% coverage** per package (enforced by CI)
- **Table-driven tests** for multiple scenarios
- **Race detector**: MUST run before committing concurrent code
- **Integration tests**: Use build tag `//go:build integration`
- **Chaos tests**: Simulate failures, verify recovery

### Common Pitfalls

**DON'T:**
- Use `fmt.Println` or `log.Print` for logging (use zap logger)
- Launch unbounded goroutines without cancellation
- Hold mutex locks across I/O operations
- Ignore errors or use `_ = err`
- Panic in library code (only main or init)
- Break proto backward compatibility
- Skip race detector tests
- Use generic package names like `util`, `common`, `helpers`

**DO:**
- Use structured logging with Zap
- Always pass `context.Context` as first parameter
- Respect context cancellation
- Add metrics for all operations
- Write table-driven tests
- Run `make test-race` before committing
- Follow naming conventions from GO_ENGINEERING_SOP.md
- Add tracing spans for distributed operations

## Key Files to Reference

**Standards and Conventions:**
- `GO_ENGINEERING_SOP.md`: Comprehensive engineering standards (READ THIS FIRST)
- `.golangci.yml`: Linter configuration
- `.github/PULL_REQUEST_TEMPLATE.md`: PR template
- `.github/CICD.md`: CI/CD pipeline documentation

**Architecture and Design:**
- `README.md`: User-facing overview and quick start
- `pkg/api/cloudless.proto`: gRPC API definitions
- `docs/design/`: Design documents for major features
- `pkg/coordinator/membership/CLD-REQ-002-DESIGN.md`: Membership design doc
- `pkg/overlay/CLD-REQ-003-DESIGN.md`: Overlay network design doc

**Testing:**
- `test/testutil/`: Shared test utilities, mocks, fixtures
- `test/integration/`: Integration tests
- `test/chaos/`: Chaos engineering tests

## Working with Specific Components

### Scheduler Changes
- Update `pkg/scheduler/scorer.go` for scoring logic
- Benchmarks in `pkg/scheduler/scheduler_bench_test.go`
- Verify performance targets: 200ms P50, 800ms P95
- Test with high node counts (simulate 5k nodes)

### RAFT/Consensus Changes
- Modify `pkg/raft/store.go` for store operations
- Update `pkg/raft/fsm.go` for state machine operations
- Test leader election, log replication, snapshots
- CRITICAL: test failure scenarios thoroughly

### Storage Changes
- Update `pkg/storage/chunk.go` for chunk operations
- Modify `pkg/storage/replication.go` for replication logic
- Verify data durability with fsync
- Test anti-entropy repair

### Overlay Network Changes
- Update `pkg/overlay/mesh.go` for routing
- Modify `pkg/overlay/transport.go` for QUIC transport
- Test NAT traversal scenarios
- Verify encryption and authentication

### Adding New gRPC RPCs
1. Add to `pkg/api/cloudless.proto` (use next available field number)
2. Run `make proto` to generate Go code
3. Implement server handler (coordinator or agent)
4. Add client method if needed
5. Add metrics (request count, latency)
6. Add tracing span
7. Add unit tests and integration tests
8. Document in proto comments

## Configuration

**Coordinator** (`cmd/coordinator/main.go`):
- Flags: `--data-dir`, `--bind-addr`, `--raft-bootstrap`, `--tls-cert/key/ca`
- Config file: YAML format, loaded via Viper
- Environment variables: `CLOUDLESS_*` prefix

**Agent** (`cmd/agent/main.go`):
- Flags: `--coordinator-addr`, `--node-name`, `--region`, `--zone`
- Must have valid mTLS certificates to connect
- Reports resources via heartbeat

**CLI** (`cmd/cloudlessctl/main.go`):
- Uses config file at `~/.cloudless/config.yaml`
- Commands: `workload`, `node`, `storage`, `network`

## Performance Targets (NFRs)

From PRD - these are REQUIREMENTS, not suggestions:
- Scheduler decisions: **200ms P50, 800ms P95** (5k nodes)
- Membership convergence: **5s P50, 15s P95**
- Failed replica rescheduling: **3s P50, 10s P95**
- Coordinator availability: **99.9% monthly**

Verify with benchmarks and load tests.

## Important Notes

**Security:**
- This is a distributed systems platform with mTLS everywhere
- NEVER accept plaintext connections
- Validate ALL external input
- Use policy engine for admission control

**Reliability:**
- Expect nodes to join/leave frequently (churn)
- Design for partial failures
- Implement retries with exponential backoff
- Add circuit breakers for external calls

**Observability:**
- Add metrics for EVERYTHING important
- Use structured logging (Zap)
- Add tracing spans for distributed operations
- Monitor SLOs continuously

**Testing:**
- 70% minimum coverage (enforced)
- Race detector MUST pass
- Test failure scenarios (chaos tests)
- Benchmark critical paths

**Standards:**
Refer to GO_ENGINEERING_SOP.md for detailed coding standards, error handling, concurrency patterns, testing requirements, and operational procedures.
