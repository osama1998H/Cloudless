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

### Workload Sandboxing and Security (CLD-REQ-062)

Cloudless implements comprehensive workload sandboxing and least privilege controls for container security.

**Security Features**:

1. **Seccomp (Secure Computing Mode)** - Syscall filtering
   - Default profile: Whitelists ~300 common syscalls
   - Strict profile: Minimal syscall set for high-security workloads
   - Custom profiles: Load from `/etc/cloudless/seccomp/`
   - Embedded profiles: Default/strict profiles built into binary
   - Configuration: `security_context.linux.seccomp_profile.type` (RuntimeDefault, Localhost, Unconfined)

2. **AppArmor (Application Armor)** - Mandatory Access Control
   - Default profile: `cloudless-default` with file/network/capabilities restrictions
   - Custom profiles: Install to `/etc/apparmor.d/`
   - Feature detection: Gracefully handles systems without AppArmor
   - Configuration: `security_context.linux.apparmor_profile.type`

3. **SELinux (Security-Enhanced Linux)** - Type Enforcement
   - Default type: `cloudless_container_t`
   - MCS support: Multi-Category Security for container isolation
   - Policy module: `config/selinux/cloudless.te`
   - Auto-detection: Checks for enforcing/permissive/disabled mode
   - Configuration: `security_context.linux.selinux_options`

4. **RuntimeClass** - Alternative Container Runtimes
   - **runc** (default): Standard OCI runtime
   - **gVisor**: Userspace kernel providing strong isolation
   - **Kata Containers**: Lightweight VM per container
   - **Firecracker**: AWS microVM-based runtime
   - Configuration: `security_context.runtime_class_name`

5. **Least Privilege Controls**
   - **RunAsNonRoot**: Enforce non-root user execution
   - **RunAsUser/RunAsGroup**: Specify UID/GID
   - **ReadOnlyRootFilesystem**: Immutable root filesystem
   - **Capability Management**: Drop ALL, selectively add required caps
   - **Host Namespace Isolation**: Prevent host network/PID/IPC sharing

**Policy Enforcement**:

The policy engine supports 7 new rule types for security validation:
- `SecurityContext`: Comprehensive security context requirements
- `SeccompProfile`: Seccomp profile enforcement
- `AppArmorProfile`: AppArmor profile enforcement
- `SELinuxOptions`: SELinux context validation
- `RuntimeClass`: Runtime class restrictions
- `RunAsNonRoot`: Non-root user enforcement
- `ReadOnlyRootFS`: Read-only root filesystem enforcement

**Configuration Examples**:

Basic secure workload:
```yaml
security_context:
  run_as_non_root: true
  run_as_user: 10001
  read_only_root_filesystem: true
  capabilities_drop: [ALL]
  capabilities_add: [NET_BIND_SERVICE]
  linux:
    seccomp_profile:
      type: RuntimeDefault
  runtime_class_name: gvisor
```

Maximum security workload:
```yaml
security_context:
  privileged: false
  run_as_non_root: true
  run_as_user: 20000
  read_only_root_filesystem: true
  host_network: false
  host_pid: false
  host_ipc: false
  capabilities_drop: [ALL]
  linux:
    seccomp_profile:
      type: Localhost
      localhost_profile: /etc/cloudless/seccomp/strict.json
    apparmor_profile:
      type: Localhost
      localhost_profile: cloudless-strict
    selinux_options:
      user: system_u
      role: system_r
      type: cloudless_secure_t
      level: s0:c100,c200
  runtime_class_name: kata
```

**Key Files**:
- `pkg/runtime/seccomp.go` - Seccomp profile loading and application
- `pkg/runtime/apparmor.go` - AppArmor profile verification and application
- `pkg/runtime/selinux.go` - SELinux context management
- `pkg/runtime/runtimeclass.go` - Runtime class selection
- `pkg/runtime/container.go` - Security context integration
- `pkg/policy/evaluator.go` - Security policy evaluation
- `config/seccomp/` - Embedded seccomp profiles
- `config/apparmor/cloudless-default` - Default AppArmor profile
- `config/selinux/cloudless.te` - SELinux policy module
- `config/workload-secure-example.yaml` - Security configuration examples
- `config/policy-security-example.yaml` - Policy examples

**Testing**:
- Unit tests: `pkg/runtime/security_test.go` (31 tests)
- Policy tests: `pkg/policy/security_test.go` (45 tests covering all 7 rule types)
- Run with: `go test ./pkg/runtime ./pkg/policy -v -run Security`

**Platform Notes**:
- Seccomp: Available on all Linux kernels 3.5+
- AppArmor: Ubuntu/Debian default, optional on other distros
- SELinux: RHEL/CentOS/Fedora default, optional on other distros
- RuntimeClass: Requires containerd 1.2+ with appropriate runtime shims installed

**Graceful Degradation**:
All security features detect platform availability and log warnings rather than failing when features are unavailable. This allows development on macOS/Windows while enforcing security in production Linux environments.

### Secrets Management (CLD-REQ-063)

Cloudless implements enterprise-grade secrets management for secure storage and distribution of sensitive configuration data.

**Architecture**:

```
Coordinator (SecretsService)
    │
    ├── Envelope Encryption (AES-256-GCM)
    │   ├── Master Encryption Key (MEK) - 32 bytes
    │   └── Data Encryption Keys (DEK) - unique per secret
    │
    ├── Access Control
    │   ├── Audience-based restrictions
    │   └── Short-lived JWT access tokens
    │
    ├── Storage (BoltDB)
    │   ├── secrets/ - Encrypted secret data
    │   ├── tokens/ - Access token metadata
    │   └── audit/ - Audit log entries
    │
    └── Audit Logging
        └── All operations tracked (CREATE, ACCESS, UPDATE, DELETE)
            │
            ▼
    Agent (Secrets Cache)
        │
        ├── In-memory cache with TTL
        ├── Cache invalidation on updates
        └── Workload secret injection
            │
            ▼
    Workload Containers
        ├── Environment variables
        └── Mounted files (tmpfs)
```

**Core Features**:

1. **Envelope Encryption**
   - Master Key (MEK) encrypts Data Encryption Keys (DEK)
   - Each secret has unique DEK
   - Fast key rotation (only re-encrypt DEKs, not data)
   - Algorithm: AES-256-GCM with authenticated encryption

2. **Audience-Based Access Control**
   - Secrets declare allowed audiences at creation
   - Access tokens must match audience
   - Example: `["backend-api", "worker-pool"]`
   - Prevents cross-workload secret access

3. **Short-Lived Access Tokens**
   - JWT tokens with configurable TTL (default 1h, max 24h)
   - Usage limits (max_uses per token)
   - Automatic revocation on exhaustion/expiration
   - Signed with HMAC-SHA256

4. **Audit Logging**
   - All operations logged (CREATE, ACCESS, UPDATE, DELETE, GENERATE_TOKEN)
   - Tracks actor, timestamp, IP address, metadata
   - 90-day retention (configurable)
   - Compliance support (SOC 2, GDPR, HIPAA, PCI-DSS)

5. **Immutable Secrets**
   - Prevents updates to sensitive configs (TLS certs, signing keys)
   - Update attempts rejected with FailedPrecondition error

**gRPC API** (10 methods in `pkg/api/cloudless.proto`):

```protobuf
service SecretsService {
  // Lifecycle
  rpc CreateSecret(CreateSecretRequest) returns (CreateSecretResponse);
  rpc GetSecret(GetSecretRequest) returns (GetSecretResponse);
  rpc UpdateSecret(UpdateSecretRequest) returns (UpdateSecretResponse);
  rpc DeleteSecret(DeleteSecretRequest) returns (DeleteSecretResponse);
  rpc ListSecrets(ListSecretsRequest) returns (ListSecretsResponse);

  // Access control
  rpc GenerateAccessToken(GenerateAccessTokenRequest) returns (GenerateAccessTokenResponse);
  rpc RevokeAccessToken(RevokeAccessTokenRequest) returns (RevokeAccessTokenResponse);

  // Management
  rpc GetSecretAuditLog(GetSecretAuditLogRequest) returns (GetSecretAuditLogResponse);
  rpc GetMasterKeyInfo(GetMasterKeyInfoRequest) returns (GetMasterKeyInfoResponse);
  rpc RotateMasterKey(RotateMasterKeyRequest) returns (RotateMasterKeyResponse);
}
```

**Usage Example**:

```yaml
# Create secret via CLI
cloudlessctl secrets create production/postgres-creds \
  --data DB_HOST=postgres.prod.internal \
  --data DB_USER=app_user \
  --data DB_PASSWORD=secretPassword123 \
  --audience backend-api \
  --audience worker-pool \
  --ttl 90d

# Generate access token
TOKEN=$(cloudlessctl secrets token production/postgres-creds \
  --audience backend-api \
  --ttl 1h \
  --max-uses 100)

# Workload spec (runtime injection)
apiVersion: cloudless.dev/v1
kind: Workload
metadata:
  name: backend-api
spec:
  containers:
  - name: app
    image: myapp:v1.0
    env:
    - name: DB_PASSWORD
      valueFrom:
        secretKeyRef:
          namespace: production
          name: postgres-creds
          key: DB_PASSWORD
          audience: backend-api
```

**Agent-Side Caching**:

Agents cache decrypted secrets in memory to reduce coordinator load:
- Cache key: `namespace/name`
- TTL: min(token_ttl, secret_ttl)
- Invalidation: Coordinator broadcasts updates
- Storage: Memory only (never persisted to disk)

**Security Model**:

| **Threat** | **Mitigation** |
|------------|---------------|
| Secret exposure in logs | Never log plaintext, only metadata |
| Stolen access token | Short TTL (1h), usage limits, audience restriction |
| Compromised agent | Secrets cached with TTL, deleted on expiration |
| Network eavesdropping | mTLS for all coordinator-agent communication |
| Master key compromise | Key rotation (90-day interval), audit forensics |
| Privilege escalation | Audience-based access control, no wildcards |

**Configuration** (docker-compose.yml):

```yaml
coordinator:
  environment:
    # Enable secrets management
    - CLOUDLESS_SECRETS_ENABLED=true

    # Keys (base64-encoded, 32 bytes)
    # WARNING: Generate unique keys per deployment!
    # Use: scripts/generate-secrets-keys.sh
    - CLOUDLESS_SECRETS_MASTER_KEY=bqgAnIrXVpBU0cByc5mBvK+ELHNiyPp0A+d+kMkoKv0=
    - CLOUDLESS_SECRETS_MASTER_KEY_ID=dev-master-key-001
    - CLOUDLESS_SECRETS_TOKEN_SIGNING_KEY=85yjkBGBhaBxlboSdzcFIlF6uhUYqiwB5sv2qfDXU2A=

    # Token TTL (1 hour default)
    - CLOUDLESS_SECRETS_TOKEN_TTL=1h

    # Automatic key rotation (recommended in production)
    - CLOUDLESS_SECRETS_ROTATION_ENABLED=false  # Dev: disabled
    - CLOUDLESS_SECRETS_ROTATION_INTERVAL=2160h  # Prod: 90 days
```

**Key Files**:
- `pkg/secrets/manager.go` - Core secrets manager
- `pkg/secrets/encryption.go` - Envelope encryption logic
- `pkg/secrets/store.go` - BoltDB storage layer
- `pkg/secrets/tokens.go` - JWT access token management
- `pkg/secrets/audit.go` - Audit logging
- `pkg/api/secrets_service.go` - gRPC service implementation
- `pkg/runtime/secrets.go` - Runtime secret injection
- `scripts/generate-secrets-keys.sh` - Key generation utility
- `docs/design/CLD-REQ-063-SECRETS.md` - Comprehensive design doc
- `test/manual/secrets_demo.go` - Manual test program (10 test steps)
- `examples/workloads/secure-app.yaml` - Example workload with secrets

**Testing**:
```bash
# Unit tests (9 tests passing)
go test ./pkg/secrets -v

# Manual test (requires running cluster)
make compose-up
go run test/manual/secrets_demo.go --coordinator=localhost:8080

# Integration tests (planned)
go test -tags=integration ./test/integration/secrets_test.go
```

**Performance Targets**:
- CreateSecret: 200µs P50, 500µs P95
- GetSecret (cache hit): 50µs P50, 100µs P95
- GetSecret (cache miss): 180µs P50, 400µs P95
- GenerateAccessToken: 50µs P50, 100µs P95
- Coordinator throughput: 5,000+ requests/sec

**Production Best Practices**:
1. ✅ Generate unique master keys (never use dev keys)
2. ✅ Enable TLS/mTLS for all communication
3. ✅ Set short token TTL (1h recommended, max 24h)
4. ✅ Enable automatic key rotation (90-day interval)
5. ✅ Monitor audit logs for suspicious activity
6. ✅ Use mounted files (not environment variables) for injection
7. ✅ Store master keys in external secret manager (Vault, AWS Secrets Manager)
8. ❌ Never commit keys to version control
9. ❌ Never disable TLS in production
10. ❌ Never grant wildcard audience permissions

**Documentation**:
- Full design: `docs/design/CLD-REQ-063-SECRETS.md` (900+ lines)
- API reference: `pkg/api/cloudless.proto` (SecretsService)
- Examples: `examples/workloads/secure-app.yaml`
- Key generation: `scripts/generate-secrets-keys.sh`

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

## Specialized Agent Orchestration

Cloudless uses specialized Claude Code agents for different aspects of development. Each agent has its own context window, system prompt, and expertise area, ensuring focused and high-quality work.

### Available Agents

Six specialized agents are available in `.claude/agents/`:

| **Agent** | **File** | **Purpose** | **Key Expertise** |
|-----------|----------|-------------|-------------------|
| **Product Owner** | `product-owner.md` | Requirements validation, feature prioritization | CLD-REQ-* mapping, MVP scope, tenet alignment |
| **Architect** | `architect.md` | System design, architecture decisions | Three-plane architecture, performance, design docs |
| **Engineer** | `engineer.md` | Code implementation | GO_ENGINEERING_SOP.md compliance, 70%+ coverage |
| **Tech Lead** | `tech-lead.md` | Code review, quality enforcement | Performance, concurrency, security review |
| **Unit Tester** | `unit-tester.md` | Test code development | Table-driven tests, race detector, edge cases |
| **Documenter** | `documenter.md` | Documentation creation | Design docs, API reference, user guides |

### When to Use Each Agent

**Decision Tree**:

```
┌─────────────────────────────────────────────────────────────┐
│ NEW FEATURE REQUEST                                         │
│ ↓                                                           │
│ 1. Product Owner - Validate requirements                   │
│    • Map to CLD-REQ-* or validate new requirement          │
│    • Check MVP scope (P0-P6)                               │
│    • Verify tenet alignment (T1-T4)                        │
│    • Define acceptance criteria                            │
│                                                             │
│ If APPROVED → Continue                                      │
│ If REJECTED → Explain to user, suggest alternatives        │
└─────────────────────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────────────────────┐
│ 2. Architect - Design solution                             │
│    • Create design document (§16.2 template)               │
│    • Mermaid diagrams for architecture                     │
│    • Performance analysis (P50/P95/P99)                    │
│    • Security considerations                               │
│    • Alternatives considered                               │
│                                                             │
│ Output: Design document in docs/design/CLD-REQ-*.md       │
└─────────────────────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────────────────────┐
│ 3. Engineer - Implement code                               │
│    • Follow GO_ENGINEERING_SOP.md §3-§12                   │
│    • Error handling (§4)                                   │
│    • Concurrency patterns (§5)                             │
│    • Structured logging and metrics (§9)                   │
│    • Security compliance (§8)                              │
│                                                             │
│ Output: Implementation in pkg/                             │
└─────────────────────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────────────────────┐
│ 4. Unit Tester - Write tests                               │
│    • Table-driven tests                                    │
│    • 70%+ statement coverage                               │
│    • Race detector passes                                  │
│    • Edge cases and error paths                            │
│    • Appropriate build tags                                │
│                                                             │
│ Output: *_test.go files with comprehensive coverage        │
└─────────────────────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────────────────────┐
│ 5. Tech Lead - Review code                                 │
│    • Correctness and error handling                        │
│    • Concurrency safety (goroutines, channels)             │
│    • Performance (meets NFR-P1 targets)                    │
│    • Security (mTLS, validation, policy)                   │
│    • Test coverage and quality                             │
│    • Architecture alignment                                │
│                                                             │
│ Output: Approval or feedback for revision                  │
└─────────────────────────────────────────────────────────────┘
                    ↓
┌─────────────────────────────────────────────────────────────┐
│ 6. Documenter - Document implementation                    │
│    • Update design document to "Implemented"               │
│    • API reference (godoc, protobuf)                       │
│    • User guide section                                    │
│    • Runbook if operational complexity                     │
│    • Package README if new package                         │
│                                                             │
│ Output: Documentation in docs/, pkg/ godoc, examples/      │
└─────────────────────────────────────────────────────────────┘
                    ↓
                 COMPLETE
```

### Agent Invocation Guidelines

**Automatic Invocation** (Claude proactively uses agents):

Agents are invoked automatically when their description patterns match the task:

- **Product Owner**: User mentions requirements, features, CLD-REQ-*, MVP, scope
- **Architect**: Adding packages, changing protocols, architectural decisions
- **Engineer**: Implementing code, fixing bugs, adding functionality
- **Tech Lead**: Code review requested, PR feedback, quality concerns
- **Unit Tester**: Writing tests, improving coverage, test failures
- **Documenter**: Creating docs, updating design docs, API reference

**Manual Invocation** (User explicitly requests):

Users can explicitly request agents:
```bash
# User messages that trigger specific agents
"Product Owner: Is GPU scheduling in MVP scope?"
"Architect: Design a distributed object store"
"Engineer: Implement the scheduler scoring function"
"Tech Lead: Review the concurrent workload processing code"
"Unit Tester: Write tests for the secrets manager"
"Documenter: Create user guide for secrets management"
```

**Parallel Agent Execution**:

For complex tasks, multiple agents can work in parallel when there are no dependencies:

```
Example: "Implement GPU scheduling support"

SEQUENTIAL workflow (default):
  Product Owner → Architect → Engineer → Unit Tester → Tech Lead → Documenter

PARALLEL opportunities:
  1. Product Owner validates requirements
  2. [PARALLEL] Architect designs solution + Tech Lead reviews existing GPU code
  3. Engineer implements
  4. [PARALLEL] Unit Tester writes tests + Documenter drafts API reference
  5. Tech Lead reviews implementation
  6. Documenter finalizes documentation
```

### Agent Coordination Rules

**MANDATORY Sequential Ordering**:

1. **Product Owner ALWAYS validates first** - No implementation without requirement validation
2. **Architect designs before Engineer implements** - No code without design for complex features
3. **Unit Tester writes tests during/after Engineer implements** - Never after PR submission
4. **Tech Lead reviews after Engineer + Unit Tester complete** - Review with tests included
5. **Documenter documents after implementation stabilizes** - Don't document unstable APIs

**Collaboration Patterns**:

| **Pattern** | **Agents Involved** | **When to Use** |
|-------------|-------------------|----------------|
| **Quick Fix** | Engineer → Tech Lead | Bug fixes, small refactors (< 50 LOC) |
| **Feature Development** | Product Owner → Architect → Engineer → Unit Tester → Tech Lead → Documenter | New features, API changes, architectural changes |
| **Test Improvement** | Unit Tester → Tech Lead | Coverage gaps, test refactoring |
| **Documentation Update** | Documenter → Tech Lead | Docs-only changes, clarifications |
| **Design Review** | Architect → Tech Lead | Design validation before implementation |
| **Requirements Clarification** | Product Owner → Architect | Scope questions, MVP decisions |

### Workflow Examples

#### Example 1: New Feature - Distributed Caching Layer

**Task**: "Add distributed caching to reduce coordinator load"

**Workflow**:
```
1. Product Owner Agent
   - Validates against CLD-REQ-* (no existing requirement)
   - Checks MVP scope (post-MVP: P7)
   - Verifies tenet alignment (T3: graceful degradation ✅)
   - Defines acceptance criteria
   - Output: Feature approved for P7, maps to new CLD-REQ-090

2. Architect Agent
   - Designs caching architecture
   - Evaluates Redis vs memcached vs custom
   - Mermaid diagrams for data flow
   - Performance analysis (cache hit ratio, latency)
   - Security: mTLS for cache connections
   - Output: docs/design/CLD-REQ-090-DISTRIBUTED-CACHE.md

3. Engineer Agent
   - Implements pkg/cache/
   - Redis client wrapper with mTLS
   - Cache invalidation logic
   - Integration with coordinator
   - Structured logging and Prometheus metrics
   - Output: pkg/cache/*.go (500 LOC)

4. Unit Tester Agent
   - Table-driven tests for cache operations
   - Mock Redis for unit tests
   - Race detector tests for concurrent access
   - Error handling tests (Redis down, network partition)
   - Coverage: 82% statement coverage
   - Output: pkg/cache/*_test.go (600 LOC)

5. Tech Lead Agent
   - Reviews implementation
   - Checks: concurrency (✅), error handling (✅), performance (✅)
   - Verifies tests (✅ 82% coverage, race detector passes)
   - Approval with minor feedback: Add context timeout for Redis operations
   - Output: Approved with comments

6. Documenter Agent
   - Updates design doc to "Implemented"
   - Godoc for pkg/cache/
   - User guide: "Enabling Distributed Caching"
   - Configuration example in config/cache-example.yaml
   - Runbook: "Debugging Cache Issues"
   - Output: docs/guides/caching.md, runbook entry

Status: COMPLETE ✅
```

#### Example 2: Bug Fix - Race Condition in Scheduler

**Task**: "Fix race condition in scheduler node scoring"

**Workflow** (Fast Track):
```
1. Engineer Agent
   - Analyzes pkg/scheduler/scorer.go:145
   - Identifies: shared map access without mutex
   - Fix: Add sync.RWMutex for node score cache
   - Adds defer unlock pattern
   - Output: pkg/scheduler/scorer.go (10 LOC changed)

2. Unit Tester Agent (Parallel with Engineer)
   - Adds race detector test
   - Test: 100 concurrent score calculations
   - Verifies: No data races, correct results
   - Output: pkg/scheduler/scorer_test.go (50 LOC added)

3. Tech Lead Agent
   - Reviews fix: Mutex placement correct ✅
   - Runs: go test -race ./pkg/scheduler/... ✅
   - Checks performance: No regression ✅
   - Approval: Immediate approval
   - Output: LGTM

Status: COMPLETE ✅ (No Product Owner/Architect/Documenter needed for bug fix)
```

#### Example 3: Documentation - API Reference for Secrets

**Task**: "Document the SecretsService gRPC API"

**Workflow** (Docs-Only):
```
1. Documenter Agent
   - Reads pkg/api/cloudless.proto (SecretsService)
   - Generates API reference from protobuf
   - Adds usage examples for each RPC
   - Creates quickstart guide
   - Documents error codes and retry logic
   - Output: docs/api/secrets.md (400 lines)

2. Tech Lead Agent
   - Reviews documentation for accuracy
   - Verifies examples compile
   - Checks alignment with implementation
   - Approval: Minor typo fixes
   - Output: Approved

Status: COMPLETE ✅ (No Product Owner/Architect/Engineer/Unit Tester needed)
```

### Agent Communication

Agents communicate through:

1. **Shared Context**: All agents read CLAUDE.md, GO_ENGINEERING_SOP.md, Cloudless.MD
2. **Artifacts**: Design docs, code, tests, documentation
3. **Status Updates**: Each agent reports completion and artifacts produced
4. **Feedback Loops**: Tech Lead provides feedback → Engineer revises → Unit Tester updates tests

**Handoff Format**:

When one agent completes and hands off to the next:

```markdown
## [Agent Name] - COMPLETED ✅

**Task**: [Description]

**Artifacts Produced**:
- [File 1]: [Description]
- [File 2]: [Description]

**Key Decisions**:
- [Decision 1]: [Rationale]
- [Decision 2]: [Rationale]

**Next Agent**: [Name]
**Handoff Context**: [What the next agent needs to know]

**Open Questions** (if any):
- [Question 1]
- [Question 2]
```

### Task Complexity Assessment

Not all tasks require all agents. Use this guide:

| **Complexity** | **LOC** | **Agents Required** | **Example** |
|----------------|---------|-------------------|-------------|
| **Trivial** | < 20 | Engineer + Tech Lead | Fix typo, update comment |
| **Simple** | 20-100 | Engineer + Unit Tester + Tech Lead | Bug fix, small refactor |
| **Moderate** | 100-500 | Architect + Engineer + Unit Tester + Tech Lead | New API endpoint, feature extension |
| **Complex** | 500-2000 | Product Owner + Architect + Engineer + Unit Tester + Tech Lead + Documenter | New subsystem, architectural change |
| **Major** | > 2000 | Full workflow + multiple iterations | New storage engine, security model overhaul |

### Agent Standards Compliance

Each agent enforces specific GO_ENGINEERING_SOP.md sections:

| **Agent** | **SOP Sections Enforced** | **Validation** |
|-----------|---------------------------|----------------|
| **Product Owner** | §1 (Purpose), §22 (Requirements Mapping) | All features map to CLD-REQ-*, MVP scope validated |
| **Architect** | §2 (Repository Layout), §16 (Design Control) | Design docs follow §16.2 template, diagrams included |
| **Engineer** | §3-§12 (Coding Standards to Testing) | Code passes lint, 70%+ coverage, error handling correct |
| **Tech Lead** | §15 (Code Review), §13 (Performance) | Performance targets met, concurrency safe, security verified |
| **Unit Tester** | §12 (Testing Policy) | Table-driven tests, race detector, appropriate build tags |
| **Documenter** | §3.4 (Comments), §16.2 (Design Template) | Godoc complete, design docs updated, examples provided |

### Performance Considerations

Agent invocation overhead is minimal (< 1s per agent), but for optimal performance:

- **Batch related changes**: Don't invoke agents for each tiny change
- **Use fast-track workflows**: Simple bugs don't need Product Owner/Architect
- **Parallel execution**: Run independent agents (Unit Tester + Documenter) in parallel
- **Cache agent context**: Agents remember project context across invocations

### Troubleshooting Agent Issues

**Issue: Agent produces incorrect output**

1. Check agent has access to latest CLAUDE.md, GO_ENGINEERING_SOP.md
2. Verify agent description pattern matches task
3. Provide more explicit handoff context
4. Use manual invocation with specific instructions

**Issue: Agent misses requirements**

1. Product Owner agent should be invoked first
2. Ensure CLD-REQ-* mapping is explicit
3. Reference Cloudless.MD sections in task description

**Issue: Agent workflow feels slow**

1. Assess task complexity (see table above)
2. Use fast-track workflows for simple tasks
3. Consider parallel agent execution
4. Batch related changes

### Agent Updates

Agents are versioned with the project. If you need to update an agent:

1. Edit `.claude/agents/<agent-name>.md`
2. Update agent description or tools
3. Test with sample task
4. Document changes in `.claude/README.md`

For details on agent capabilities and usage, see `.claude/README.md`.

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
