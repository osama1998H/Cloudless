# Cloudless — Go Engineering SOP v1.0

**Document Version:** 1.0
**Last Updated:** 2025-10-26
**Applies To:** Cloudless Platform v0.x
**Owner:** Engineering Leadership

---

## Table of Contents

1. [Purpose and Scope](#1-purpose-and-scope)
2. [Repository Layout and Ownership](#2-repository-layout-and-ownership)
3. [Coding Standards](#3-coding-standards)
4. [Error Handling Policy](#4-error-handling-policy)
5. [Concurrency and Resource Policy](#5-concurrency-and-resource-policy)
6. [Networking Standards](#6-networking-standards)
7. [Storage Standards](#7-storage-standards)
8. [Security Requirements](#8-security-requirements)
9. [Observability](#9-observability)
10. [Configuration](#10-configuration)
11. [API and Compatibility](#11-api-and-compatibility)
12. [Testing Policy](#12-testing-policy)
13. [Performance and Scalability](#13-performance-and-scalability)
14. [CI/CD Pipeline](#14-cicd-pipeline-reference)
15. [Code Review and PR Discipline](#15-code-review-and-pr-discipline)
16. [Design Control](#16-design-control)
17. [Release Management](#17-release-management)
18. [Operational Runbooks](#18-operational-runbooks)
19. [Security Hygiene](#19-security-hygiene)
20. [Feature Flags and Experiments](#20-feature-flags-and-experiments)
21. [Data and Compatibility Contracts](#21-data-and-compatibility-contracts)
22. [Mapping to Requirements](#22-mapping-to-requirements)
23. [Local Development](#23-local-development)
24. [Triage and On-call](#24-triage-and-on-call)
25. [Decommission and Deprecation](#25-decommission-and-deprecation)
26. [Checklists](#26-checklists)
27. [References and Tooling](#27-references-and-tooling)

---

## 1. Purpose and Scope

### 1.1 Purpose

This Standard Operating Procedure (SOP) defines engineering practices for Go development on the **Cloudless distributed compute platform**. Cloudless is a system-level infrastructure project that aggregates heterogeneous devices into an elastic compute fabric, requiring:

- **High reliability** in the face of node churn and network partitions
- **Strong security** with mTLS everywhere and policy-based admission control
- **Predictable performance** (scheduler decisions in 200ms P50, 800ms P95)
- **Operational simplicity** for distributed systems operators

### 1.2 Scope

**Applies to:**
- All Go code in the Cloudless repository (`github.com/cloudless/cloudless`)
- Control plane: Coordinator, Scheduler, RAFT metadata store
- Data plane: Agent, Runtime, Overlay networking, Storage engine
- Observability: Metrics, logging, tracing, event streams
- CLI and gRPC APIs

**Does NOT apply to:**
- Third-party dependencies (follow their standards when contributing upstream)
- Experimental prototypes in personal forks (until merged to main)
- Documentation-only changes (follow separate docs style guide)

### 1.3 Audience

- **Go Engineers** implementing features and fixing bugs
- **Tech Leads** reviewing designs and code
- **SREs** debugging production issues and writing runbooks
- **New Contributors** ramping up on the codebase

### 1.4 Authority

This SOP is **mandatory** for all production code. Deviations require:
1. Explicit approval from a Tech Lead
2. Documentation in commit message with justification
3. Follow-up issue to align with SOP or update SOP

### 1.5 Versioning

- SOP follows semantic versioning (MAJOR.MINOR.PATCH)
- Breaking changes to standards increment MAJOR
- New sections or clarifications increment MINOR
- Typo fixes and formatting increment PATCH

---

## 2. Repository Layout and Ownership

### 2.1 Directory Structure

```
Cloudless/
├── cmd/                      # Binaries
│   ├── coordinator/          # Control plane entry point
│   ├── agent/                # Node agent entry point
│   └── cloudlessctl/         # CLI tool
├── pkg/                      # Shared libraries
│   ├── api/                  # Protobuf/gRPC definitions
│   ├── coordinator/          # Coordinator implementation
│   │   └── membership/       # RAFT-based cluster membership
│   ├── agent/                # Agent implementation
│   ├── scheduler/            # Scheduling logic
│   ├── overlay/              # QUIC network overlay
│   ├── storage/              # Distributed object store
│   ├── raft/                 # RAFT consensus wrapper
│   ├── runtime/              # Container runtime integration
│   ├── mtls/                 # Certificate management
│   ├── policy/               # Security policies
│   ├── secrets/              # Secrets management
│   └── observability/        # Metrics, logging, tracing
├── test/                     # Test infrastructure
│   ├── testutil/             # Shared test utilities
│   │   ├── mocks/            # Mock implementations
│   │   └── fixtures/         # Test data
│   ├── integration/          # Integration tests
│   ├── chaos/                # Chaos engineering tests
│   └── e2e/                  # End-to-end tests
├── deployments/              # Deployment configurations
│   └── docker/               # Docker Compose for local dev
├── config/                   # Example configurations
├── examples/                 # Example workloads
├── scripts/                  # Build and utility scripts
├── docs/                     # Technical documentation
├── go.mod                    # Go module definition
├── Makefile                  # Build automation
├── CLAUDE.md                 # Developer context for AI assistance
├── GO_ENGINEERING_SOP.md     # This document
└── README.md                 # User-facing documentation
```

### 2.2 Ownership Model

**CODEOWNERS File:**
- Each `pkg/` directory MUST have designated owners
- Minimum 2 owners per critical path component
- Owners responsible for: design review, code quality, on-call rotation

**Example:**
```
# CODEOWNERS
/pkg/scheduler/        @alice @bob
/pkg/raft/             @charlie @dave
/pkg/storage/          @eve @frank
/pkg/overlay/          @alice @eve
```

**Ownership Responsibilities:**
1. **Design Review** - Review RFCs for owned components
2. **Code Quality** - Ensure tests, docs, and standards compliance
3. **On-call** - Respond to incidents affecting owned components
4. **Knowledge Transfer** - Mentor new contributors

### 2.3 Package Naming and Hierarchy

**Rules:**
1. Package names are **lowercase, singular, no underscores** (`scheduler`, not `schedulers` or `scheduler_utils`)
2. Package path reflects logical grouping, not team structure
3. Avoid generic names (`util`, `common`, `helpers`) - be specific
4. Internal packages use `internal/` to restrict visibility

**Good:**
```go
pkg/scheduler/         # Scheduling logic
pkg/scheduler/binpacker/   # Bin packing strategies (sub-package)
```

**Bad:**
```go
pkg/sched_utils/       # Underscore, vague
pkg/helpers/           # Too generic
```

### 2.4 Module Boundaries

- Root `go.mod` at repository root
- No nested modules without approval
- All internal packages share version
- External APIs (e.g., `cloudlessctl`) may have separate module in future

---

## 3. Coding Standards

### 3.1 Go Version

**Required:** Go 1.24+ (as specified in `go.mod`)

**Justification:**
- Improved sync/atomic package structure
- Better generics support
- Performance improvements in runtime

**Update Policy:**
- Track latest stable Go release within 3 months
- Update CI and `go.mod` in lockstep
- Document breaking changes in release notes

### 3.2 Formatting and Linting

**Mandatory Tools:**

1. **`gofmt`** - All code MUST pass `gofmt -s` (simplify)
   ```bash
   gofmt -s -w .
   ```

2. **`goimports`** - Organize imports
   ```bash
   goimports -w .
   ```

3. **`golangci-lint`** v1.62.2+ - Comprehensive linting
   ```bash
   golangci-lint run
   ```

**Enabled Linters** (`.golangci.yml`):
- `errcheck` - Check unchecked errors
- `gosimple` - Suggest code simplifications
- `govet` - Standard Go vet checks
- `ineffassign` - Detect ineffectual assignments
- `staticcheck` - Advanced static analysis
- `unused` - Find unused code
- `misspell` - Catch common typos
- `gocyclo` - Flag high cyclomatic complexity (threshold: 15)
- `dupl` - Find duplicated code
- `gosec` - Security-focused linting

**Pre-commit Hook:**
```bash
#!/bin/bash
make fmt lint
```

### 3.3 Naming Conventions

**Exported Identifiers:**
- Use **PascalCase** for types, functions, constants
- Use clear, descriptive names (avoid abbreviations unless domain-standard)

```go
// Good
type SchedulingDecision struct { ... }
func NewScheduler(...) *Scheduler { ... }
const MaxReplicasPerWorkload = 100

// Bad
type SchedDecision struct { ... }  // Unclear abbreviation
func NewSched(...) *Scheduler { ... }
```

**Unexported Identifiers:**
- Use **camelCase**
- Prefix private fields with purpose, not `_`

```go
// Good
type scheduler struct {
    logger *zap.Logger
    nodeCache map[string]*Node
}

// Bad
type scheduler struct {
    _logger *zap.Logger  // Don't use underscore
    nc map[string]*Node  // Unclear abbreviation
}
```

**Acronyms:**
- Treat as words in PascalCase/camelCase positions
- Uppercase when at start or standalone

```go
// Good
type HTTPServer struct { ... }
type gRPCClient struct { ... }
func (c *Client) ServeHTTP(...) { ... }

// Bad
type HttpServer struct { ... }
type GrpcClient struct { ... }
```

**Interface Names:**
- Use `-er` suffix for single-method interfaces
- Use descriptive nouns for multi-method interfaces

```go
// Good
type Scheduler interface {
    Schedule(ctx context.Context, workload *Workload) (*Placement, error)
}

type StorageBackend interface {
    Put(ctx context.Context, key string, value []byte) error
    Get(ctx context.Context, key string) ([]byte, error)
    Delete(ctx context.Context, key string) error
}

// Bad
type SchedulerInterface interface { ... }  // Redundant suffix
type IScheduler interface { ... }          // Don't prefix with I
```

### 3.4 Comments and Documentation

**Package-level Comments:**
- Every package MUST have a doc comment describing its purpose
- Use complete sentences

```go
// Package scheduler implements the Cloudless workload placement engine.
//
// The scheduler uses a multi-criteria scoring function to select optimal
// nodes for workload replicas, balancing locality, reliability, cost,
// and utilization. See the Cloudless PRD (CLD-REQ-020) for details.
package scheduler
```

**Exported Type/Function Comments:**
- Start with the type/function name
- Explain what, not how (code shows how)
- Document edge cases, invariants, and concurrency safety

```go
// Scheduler selects nodes for workload placement using weighted scoring.
//
// Scheduler is safe for concurrent use by multiple goroutines. The scoring
// weights can be adjusted dynamically via UpdateWeights.
type Scheduler struct { ... }

// Schedule selects a node for the given workload replica.
//
// Returns ErrInsufficientCapacity if no nodes meet the resource requirements,
// or ErrPolicyViolation if the workload violates admission policies.
//
// Schedule is blocking and may take up to 200ms under normal load. Callers
// should set appropriate context deadlines.
func (s *Scheduler) Schedule(ctx context.Context, workload *Workload) (*Placement, error) {
    ...
}
```

**TODO Comments:**
- Use `TODO(username): description` format
- Create GitHub issue and link in comment
- Remove TODOs before merging to main (or document in issue)

```go
// TODO(alice): Implement priority-based preemption. See issue #123.
// Current implementation only handles best-effort scheduling.
```

### 3.5 Import Organization

**Order:**
1. Standard library
2. Third-party packages (alphabetical)
3. Local packages (alphabetical)

**Use `goimports` to enforce:**
```go
import (
    "context"
    "fmt"
    "time"

    "github.com/prometheus/client_golang/prometheus"
    "go.uber.org/zap"
    "google.golang.org/grpc"

    "github.com/cloudless/cloudless/pkg/api"
    "github.com/cloudless/cloudless/pkg/scheduler"
)
```

**Blank Imports:**
- Only for side effects (e.g., driver registration)
- Add explanatory comment

```go
import (
    _ "github.com/lib/pq"  // PostgreSQL driver for database/sql
)
```

### 3.6 Code Structure

**Function Length:**
- Target: **< 50 lines** per function
- Hard limit: **< 100 lines** (triggers linter warning)
- Extract helper functions for clarity

**Cognitive Complexity:**
- Target: **< 10** per function
- Use early returns to reduce nesting

```go
// Good - Early returns reduce nesting
func (s *Scheduler) filterNodes(ctx context.Context, workload *Workload) ([]*Node, error) {
    if workload == nil {
        return nil, ErrNilWorkload
    }

    nodes := s.getAvailableNodes(ctx)
    if len(nodes) == 0 {
        return nil, ErrNoNodesAvailable
    }

    filtered := make([]*Node, 0, len(nodes))
    for _, node := range nodes {
        if !s.meetsResourceRequirements(node, workload) {
            continue
        }
        filtered = append(filtered, node)
    }

    return filtered, nil
}
```

**Variable Scope:**
- Declare variables in the smallest scope possible
- Initialize variables at declaration

```go
// Good
func process(data []byte) error {
    if len(data) == 0 {
        return ErrEmptyData
    }

    checksum := calculateChecksum(data)
    if !verifyChecksum(checksum) {
        return ErrInvalidChecksum
    }

    return store(data, checksum)
}

// Bad - checksum declared too early
func process(data []byte) error {
    var checksum string

    if len(data) == 0 {
        return ErrEmptyData
    }

    checksum = calculateChecksum(data)
    ...
}
```

---

## 4. Error Handling Policy

### 4.1 Error Philosophy

**Principles:**
1. **Errors are values** - Handle them explicitly, don't ignore
2. **Context matters** - Wrap errors with additional context
3. **Errors are for humans** - Write actionable error messages
4. **Sentinel errors for control flow** - Use typed errors for decisions

### 4.2 Error Creation

**Use `fmt.Errorf` with `%w` for wrapping:**
```go
if err := db.Query(ctx, query); err != nil {
    return fmt.Errorf("failed to query database: %w", err)
}
```

**Define sentinel errors for expected conditions:**
```go
package scheduler

var (
    ErrInsufficientCapacity = errors.New("insufficient cluster capacity")
    ErrPolicyViolation      = errors.New("workload violates admission policy")
    ErrNodeNotFound         = errors.New("node not found in inventory")
)
```

**Use custom error types for structured data:**
```go
type SchedulingError struct {
    WorkloadID string
    Reason     string
    Violations []PolicyViolation
}

func (e *SchedulingError) Error() string {
    return fmt.Sprintf("scheduling failed for workload %s: %s (%d violations)",
        e.WorkloadID, e.Reason, len(e.Violations))
}
```

### 4.3 Error Handling Patterns

**Check errors immediately:**
```go
// Good
data, err := readFile(path)
if err != nil {
    return fmt.Errorf("reading config file: %w", err)
}
processData(data)

// Bad - deferred error check
data, err := readFile(path)
processData(data)
if err != nil {  // Too late!
    return err
}
```

**Handle or propagate - never ignore:**
```go
// Good - Explicit handling
if err := cleanup(); err != nil {
    logger.Warn("Cleanup failed", zap.Error(err))
}

// Bad - Silent ignore
_ = cleanup()  // What if this fails critically?
```

**Use `errors.Is` for sentinel errors:**
```go
if errors.Is(err, ErrInsufficientCapacity) {
    metrics.InsufficientCapacityCount.Inc()
    return scheduleToReservePool(workload)
}
```

**Use `errors.As` for typed errors:**
```go
var schedErr *SchedulingError
if errors.As(err, &schedErr) {
    logger.Error("Scheduling failed",
        zap.String("workload", schedErr.WorkloadID),
        zap.Int("violations", len(schedErr.Violations)),
    )
}
```

### 4.4 Error Context

**Add context when wrapping:**
```go
func (s *Scheduler) Schedule(ctx context.Context, workload *Workload) (*Placement, error) {
    nodes, err := s.filterNodes(ctx, workload)
    if err != nil {
        return nil, fmt.Errorf("filtering nodes for workload %s: %w",
            workload.ID, err)
    }

    placement, err := s.selectNode(ctx, nodes, workload)
    if err != nil {
        return nil, fmt.Errorf("selecting node for workload %s: %w",
            workload.ID, err)
    }

    return placement, nil
}
```

**Include actionable information:**
```go
// Good - Tells user what to do
return fmt.Errorf("node %s does not have %d CPU millicores available (has %d): increase cluster capacity or reduce workload requests",
    node.ID, required, available)

// Bad - Unclear
return fmt.Errorf("node resource check failed")
```

### 4.5 Panic Policy

**NEVER panic in library code** - Return errors instead

**Panic ONLY for:**
1. **Programmer errors** during initialization (e.g., nil required dependency)
2. **Invariant violations** that indicate critical bugs

```go
// Acceptable - Initialization validation
func NewScheduler(config SchedulerConfig, logger *zap.Logger) *Scheduler {
    if logger == nil {
        panic("scheduler: logger is required")
    }
    ...
}

// Bad - Don't panic in request handling
func (s *Scheduler) Schedule(ctx context.Context, workload *Workload) (*Placement, error) {
    if workload == nil {
        panic("nil workload")  // Should return error!
    }
    ...
}
```

**Recover from panics in goroutines:**
```go
func (s *Scheduler) worker() {
    defer func() {
        if r := recover(); r != nil {
            s.logger.Error("Worker panic",
                zap.Any("panic", r),
                zap.Stack("stack"),
            )
            // Restart worker or mark unhealthy
        }
    }()

    for task := range s.taskQueue {
        s.processTask(task)
    }
}
```

---

## 5. Concurrency and Resource Policy

### 5.1 Goroutine Lifecycle

**NEVER launch unbounded goroutines** - Always have:
1. A way to **stop** the goroutine (context, channel, done signal)
2. A way to **detect** goroutine leaks (tests)
3. A **bounded** number of concurrent goroutines

**Pattern: Context-based cancellation**
```go
func (s *Scheduler) Start(ctx context.Context) error {
    // Worker goroutine
    go func() {
        ticker := time.NewTicker(10 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ctx.Done():
                s.logger.Info("Scheduler worker stopping")
                return
            case <-ticker.C:
                s.performMaintenance()
            }
        }
    }()

    return nil
}
```

**Pattern: Worker pool**
```go
func (s *Scheduler) processWorkloads(ctx context.Context, workloads <-chan *Workload) {
    // Bounded worker pool
    const maxWorkers = 10
    sem := make(chan struct{}, maxWorkers)

    for {
        select {
        case <-ctx.Done():
            return
        case workload, ok := <-workloads:
            if !ok {
                return  // Channel closed
            }

            sem <- struct{}{}  // Acquire semaphore
            go func(w *Workload) {
                defer func() { <-sem }()  // Release semaphore
                s.scheduleWorkload(ctx, w)
            }(workload)
        }
    }
}
```

### 5.2 Channel Discipline

**Rule: The sender closes the channel**
```go
func producer(ctx context.Context) <-chan int {
    ch := make(chan int, 10)

    go func() {
        defer close(ch)  // Producer owns the channel

        for i := 0; i < 100; i++ {
            select {
            case <-ctx.Done():
                return
            case ch <- i:
            }
        }
    }()

    return ch
}

func consumer(ctx context.Context, ch <-chan int) {
    for {
        select {
        case <-ctx.Done():
            return
        case val, ok := <-ch:
            if !ok {
                return  // Channel closed by producer
            }
            process(val)
        }
    }
}
```

**Buffered vs. Unbuffered:**
- **Unbuffered** (default) - Use for synchronization
- **Buffered** - Use for asynchronous work queues (choose size carefully)

```go
// Unbuffered - Synchronous handoff
done := make(chan struct{})
go func() {
    performWork()
    close(done)
}()
<-done  // Wait for completion

// Buffered - Work queue
const queueSize = 100
tasks := make(chan Task, queueSize)
```

### 5.3 Mutex and RWMutex

**Prefer `sync.RWMutex` for read-heavy workloads:**
```go
type NodeCache struct {
    mu    sync.RWMutex
    nodes map[string]*Node
}

func (c *NodeCache) Get(id string) (*Node, bool) {
    c.mu.RLock()
    defer c.mu.RUnlock()

    node, ok := c.nodes[id]
    return node, ok
}

func (c *NodeCache) Set(id string, node *Node) {
    c.mu.Lock()
    defer c.mu.Unlock()

    c.nodes[id] = node
}
```

**Lock Granularity:**
- Keep critical sections **small**
- Don't hold locks across I/O or blocking calls

```go
// Bad - Lock held during I/O
func (c *NodeCache) Update(id string) error {
    c.mu.Lock()
    defer c.mu.Unlock()

    node, err := c.fetchFromDatabase(id)  // I/O under lock!
    if err != nil {
        return err
    }
    c.nodes[id] = node
    return nil
}

// Good - Lock only for mutation
func (c *NodeCache) Update(id string) error {
    node, err := c.fetchFromDatabase(id)
    if err != nil {
        return err
    }

    c.mu.Lock()
    c.nodes[id] = node
    c.mu.Unlock()

    return nil
}
```

### 5.4 sync.Map Usage

**Use `sync.Map` for:**
- Mostly-read workloads with rare writes
- Many concurrent readers and writers
- Key sets that grow dynamically

**Don't use `sync.Map` for:**
- Type-safe access (no generics support)
- Complex value types requiring serialization

```go
// Good use case - Node registry
type NodeRegistry struct {
    nodes sync.Map  // nodeID (string) -> *Node
}

func (r *NodeRegistry) Register(node *Node) {
    r.nodes.Store(node.ID, node)
}

func (r *NodeRegistry) Get(id string) (*Node, bool) {
    val, ok := r.nodes.Load(id)
    if !ok {
        return nil, false
    }
    return val.(*Node), true
}
```

### 5.5 Context Propagation

**ALWAYS accept `context.Context` as first parameter:**
```go
func (s *Scheduler) Schedule(ctx context.Context, workload *Workload) (*Placement, error) {
    ...
}
```

**Respect context cancellation:**
```go
func (s *Scheduler) filterNodes(ctx context.Context, nodes []*Node) []*Node {
    filtered := make([]*Node, 0, len(nodes))

    for _, node := range nodes {
        select {
        case <-ctx.Done():
            s.logger.Warn("Filter operation cancelled", zap.Error(ctx.Err()))
            return filtered  // Return partial results
        default:
        }

        if s.meetsCriteria(node) {
            filtered = append(filtered, node)
        }
    }

    return filtered
}
```

**Set timeouts for outbound calls:**
```go
func (c *CoordinatorClient) Heartbeat(node *Node) error {
    ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
    defer cancel()

    _, err := c.client.Heartbeat(ctx, &api.HeartbeatRequest{
        NodeId: node.ID,
    })

    return err
}
```

### 5.6 Resource Cleanup

**Use `defer` for cleanup:**
```go
func processFile(path string) error {
    f, err := os.Open(path)
    if err != nil {
        return err
    }
    defer f.Close()  // Always closed, even on error

    return parseFile(f)
}
```

**Cleanup order matters:**
```go
func setupConnection() error {
    conn, err := dial()
    if err != nil {
        return err
    }
    defer conn.Close()  // Second: Close connection

    tx, err := conn.Begin()
    if err != nil {
        return err
    }
    defer tx.Rollback()  // First: Rollback transaction (if not committed)

    if err := executeQueries(tx); err != nil {
        return err  // Rollback happens, then conn closes
    }

    return tx.Commit()
}
```

### 5.7 Deadlock Prevention

**Lock Ordering:**
- Establish global lock order (document in package comments)
- Always acquire locks in the same order

```go
// Package scheduler uses the following lock ordering:
//   1. Scheduler.stateMu
//   2. Node.mu
//   3. Workload.mu

func (s *Scheduler) updateNodeAndWorkload(nodeID, workloadID string) error {
    s.stateMu.Lock()
    defer s.stateMu.Unlock()

    node := s.nodes[nodeID]
    node.mu.Lock()
    defer node.mu.Unlock()

    workload := s.workloads[workloadID]
    workload.mu.Lock()
    defer workload.mu.Unlock()

    // Safe - locks acquired in order
    return s.assignWorkloadToNode(workload, node)
}
```

**Avoid holding multiple locks:**
```go
// Bad - Potential deadlock
func transfer(from, to *Account, amount int) error {
    from.mu.Lock()
    defer from.mu.Unlock()

    to.mu.Lock()
    defer to.mu.Unlock()

    from.balance -= amount
    to.balance += amount
    return nil
}

// Good - Lock ordering by ID
func transfer(from, to *Account, amount int) error {
    first, second := from, to
    if from.ID > to.ID {
        first, second = to, from
    }

    first.mu.Lock()
    defer first.mu.Unlock()

    second.mu.Lock()
    defer second.mu.Unlock()

    from.balance -= amount
    to.balance += amount
    return nil
}
```

---

## 6. Networking Standards

### 6.1 gRPC Services

**All RPCs MUST:**
1. Accept `context.Context` as first parameter
2. Respect context cancellation and deadlines
3. Return structured errors with gRPC status codes
4. Include request/response tracing

**Example:**
```go
func (s *CoordinatorServer) EnrollNode(ctx context.Context, req *api.EnrollNodeRequest) (*api.EnrollNodeResponse, error) {
    // 1. Validate request
    if req.Token == "" {
        return nil, status.Errorf(codes.InvalidArgument, "enrollment token is required")
    }

    // 2. Check context deadline
    deadline, ok := ctx.Deadline()
    if ok && time.Until(deadline) < 1*time.Second {
        return nil, status.Errorf(codes.DeadlineExceeded, "insufficient time to process request")
    }

    // 3. Process with tracing
    span := trace.SpanFromContext(ctx)
    span.SetAttributes(attribute.String("node.region", req.Region))

    node, err := s.enrollNode(ctx, req)
    if err != nil {
        return nil, status.Errorf(codes.Internal, "enrollment failed: %v", err)
    }

    // 4. Return response
    return &api.EnrollNodeResponse{
        NodeId:      node.ID,
        Certificate: node.Certificate,
    }, nil
}
```

### 6.2 Error Mapping

**Map internal errors to gRPC codes:**
```go
func toGRPCError(err error) error {
    switch {
    case errors.Is(err, ErrNotFound):
        return status.Error(codes.NotFound, err.Error())
    case errors.Is(err, ErrInsufficientCapacity):
        return status.Error(codes.ResourceExhausted, err.Error())
    case errors.Is(err, ErrPolicyViolation):
        return status.Error(codes.PermissionDenied, err.Error())
    case errors.Is(err, context.Canceled):
        return status.Error(codes.Canceled, "request cancelled")
    case errors.Is(err, context.DeadlineExceeded):
        return status.Error(codes.DeadlineExceeded, "request timeout")
    default:
        return status.Error(codes.Internal, err.Error())
    }
}
```

**gRPC Status Codes:**
- `codes.InvalidArgument` - Invalid input (e.g., missing required field)
- `codes.NotFound` - Resource doesn't exist
- `codes.AlreadyExists` - Resource already exists
- `codes.PermissionDenied` - Authorization failure
- `codes.ResourceExhausted` - Quota or rate limit exceeded
- `codes.Unauthenticated` - Authentication failure
- `codes.Unavailable` - Service temporarily unavailable
- `codes.Internal` - Internal server error

### 6.3 Retry and Backoff

**Client-side retries with exponential backoff:**
```go
import "github.com/cenkalti/backoff/v4"

func (c *Client) callWithRetry(ctx context.Context, fn func() error) error {
    b := backoff.NewExponentialBackOff()
    b.InitialInterval = 100 * time.Millisecond
    b.MaxInterval = 10 * time.Second
    b.MaxElapsedTime = 1 * time.Minute

    return backoff.Retry(fn, backoff.WithContext(b, ctx))
}
```

**Idempotency:**
- All mutations MUST be idempotent
- Use idempotency keys for create operations

```go
message CreateWorkloadRequest {
    string idempotency_key = 1;  // Client-generated UUID
    WorkloadSpec spec = 2;
}

func (s *Server) CreateWorkload(ctx context.Context, req *CreateWorkloadRequest) (*Workload, error) {
    // Check if already processed
    if existing, ok := s.idempotencyCache.Get(req.IdempotencyKey); ok {
        return existing, nil  // Return cached result
    }

    workload, err := s.createWorkload(ctx, req.Spec)
    if err != nil {
        return nil, err
    }

    s.idempotencyCache.Set(req.IdempotencyKey, workload, 24*time.Hour)
    return workload, nil
}
```

### 6.4 mTLS Configuration

**All network communication MUST use mTLS:**
```go
import "crypto/tls"

func (c *Coordinator) setupTLSConfig() (*tls.Config, error) {
    cert, err := tls.LoadX509KeyPair(c.config.CertFile, c.config.KeyFile)
    if err != nil {
        return nil, fmt.Errorf("loading certificate: %w", err)
    }

    caCert, err := os.ReadFile(c.config.CAFile)
    if err != nil {
        return nil, fmt.Errorf("loading CA certificate: %w", err)
    }

    caPool := x509.NewCertPool()
    if !caPool.AppendCertsFromPEM(caCert) {
        return nil, errors.New("failed to parse CA certificate")
    }

    return &tls.Config{
        Certificates: []tls.Certificate{cert},
        ClientCAs:    caPool,
        ClientAuth:   tls.RequireAndVerifyClientCert,
        MinVersion:   tls.VersionTLS13,
    }, nil
}
```

### 6.5 Rate Limiting

**Implement per-client rate limiting:**
```go
import "golang.org/x/time/rate"

type rateLimitInterceptor struct {
    limiters sync.Map  // clientID -> *rate.Limiter
}

func (r *rateLimitInterceptor) getLimiter(clientID string) *rate.Limiter {
    if limiter, ok := r.limiters.Load(clientID); ok {
        return limiter.(*rate.Limiter)
    }

    limiter := rate.NewLimiter(rate.Limit(100), 200)  // 100 req/s, burst 200
    r.limiters.Store(clientID, limiter)
    return limiter
}

func (r *rateLimitInterceptor) Unary() grpc.UnaryServerInterceptor {
    return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
        clientID := getClientID(ctx)
        limiter := r.getLimiter(clientID)

        if !limiter.Allow() {
            return nil, status.Errorf(codes.ResourceExhausted, "rate limit exceeded")
        }

        return handler(ctx, req)
    }
}
```

---

## 7. Storage Standards

### 7.1 Data Durability

**All writes MUST:**
1. Use `fsync` or equivalent for durability
2. Validate checksums on read
3. Replicate across `R` nodes (default R=3)

**Example:**
```go
func (s *ChunkStore) WriteChunk(data []byte) (*Chunk, error) {
    checksum := s.calculateChecksum(data)

    // Write to temp file first
    tmpPath := filepath.Join(s.tmpDir, checksum+".tmp")
    if err := os.WriteFile(tmpPath, data, 0644); err != nil {
        return nil, fmt.Errorf("writing temp file: %w", err)
    }

    // Sync to disk
    f, err := os.Open(tmpPath)
    if err != nil {
        return nil, err
    }
    if err := f.Sync(); err != nil {
        f.Close()
        return nil, fmt.Errorf("syncing temp file: %w", err)
    }
    f.Close()

    // Atomic rename
    finalPath := s.getChunkPath(checksum)
    if err := os.Rename(tmpPath, finalPath); err != nil {
        return nil, fmt.Errorf("renaming to final path: %w", err)
    }

    return &Chunk{
        ID:       checksum,
        Size:     int64(len(data)),
        Checksum: checksum,
    }, nil
}
```

### 7.2 Content Addressing

**Use SHA-256 for content addressing:**
```go
import "crypto/sha256"

func calculateChecksum(data []byte) string {
    hash := sha256.Sum256(data)
    return hex.EncodeToString(hash[:])
}
```

**Deduplication:**
```go
func (s *ChunkStore) WriteChunk(data []byte) (*Chunk, error) {
    checksum := calculateChecksum(data)

    // Check if chunk already exists
    if chunk, exists := s.GetChunk(checksum); exists {
        chunk.RefCount++
        return chunk, nil  // Deduplication
    }

    // Write new chunk
    return s.writeNewChunk(checksum, data)
}
```

### 7.3 Garbage Collection

**Reference Counting:**
```go
func (s *ChunkStore) DeleteChunk(chunkID string) error {
    chunk, exists := s.GetChunk(chunkID)
    if !exists {
        return ErrChunkNotFound
    }

    s.mu.Lock()
    defer s.mu.Unlock()

    chunk.RefCount--
    if chunk.RefCount <= 0 {
        // Remove from disk
        if err := os.Remove(s.getChunkPath(chunkID)); err != nil {
            return fmt.Errorf("removing chunk file: %w", err)
        }
        delete(s.chunks, chunkID)
    }

    return nil
}
```

**Periodic GC:**
```go
func (s *ChunkStore) GarbageCollect() (int, error) {
    s.logger.Info("Starting garbage collection")

    orphaned := s.findOrphanedChunks()

    for _, chunkID := range orphaned {
        if err := s.DeleteChunk(chunkID); err != nil {
            s.logger.Error("Failed to delete orphaned chunk",
                zap.String("chunk_id", chunkID),
                zap.Error(err),
            )
        }
    }

    s.logger.Info("Garbage collection completed",
        zap.Int("chunks_removed", len(orphaned)),
    )

    return len(orphaned), nil
}
```

### 7.4 Anti-Entropy Repair

**Background repair process:**
```go
func (s *StorageManager) repairLoop(ctx context.Context) {
    ticker := time.NewTicker(1 * time.Hour)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case <-ticker.C:
            if err := s.performRepair(ctx); err != nil {
                s.logger.Error("Repair failed", zap.Error(err))
            }
        }
    }
}

func (s *StorageManager) performRepair(ctx context.Context) error {
    objects := s.listAllObjects(ctx)

    for _, obj := range objects {
        replicas := s.getReplicaLocations(obj.ID)
        if len(replicas) < s.config.ReplicationFactor {
            s.logger.Warn("Under-replicated object",
                zap.String("object_id", obj.ID),
                zap.Int("replicas", len(replicas)),
                zap.Int("target", s.config.ReplicationFactor),
            )

            if err := s.repairObject(ctx, obj); err != nil {
                s.logger.Error("Object repair failed",
                    zap.String("object_id", obj.ID),
                    zap.Error(err),
                )
            }
        }
    }

    return nil
}
```

---

## 8. Security Requirements

### 8.1 mTLS Everywhere

**NO plaintext communication:**
```go
// Bad - Insecure
conn, err := net.Dial("tcp", "coordinator:8080")

// Good - mTLS
tlsConfig, err := loadTLSConfig()
if err != nil {
    return err
}
conn, err := tls.Dial("tcp", "coordinator:8080", tlsConfig)
```

### 8.2 Secrets Management

**NEVER hardcode secrets:**
```go
// Bad
const apiKey = "sk_live_abc123"

// Good - Load from environment or secrets manager
apiKey := os.Getenv("CLOUDLESS_API_KEY")
if apiKey == "" {
    return errors.New("CLOUDLESS_API_KEY not set")
}
```

**Secrets at rest:**
```go
import "crypto/aes"
import "crypto/cipher"

func (s *SecretStore) encryptSecret(plaintext []byte) ([]byte, error) {
    block, err := aes.NewCipher(s.encryptionKey)
    if err != nil {
        return nil, err
    }

    gcm, err := cipher.NewGCM(block)
    if err != nil {
        return nil, err
    }

    nonce := make([]byte, gcm.NonceSize())
    if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
        return nil, err
    }

    return gcm.Seal(nonce, nonce, plaintext, nil), nil
}
```

### 8.3 Input Validation

**Validate ALL external input:**
```go
func (s *Server) CreateWorkload(ctx context.Context, req *api.CreateWorkloadRequest) (*api.Workload, error) {
    // Validate name
    if req.Name == "" {
        return nil, status.Error(codes.InvalidArgument, "workload name is required")
    }
    if len(req.Name) > 253 {
        return nil, status.Error(codes.InvalidArgument, "workload name exceeds 253 characters")
    }
    if !isValidDNSName(req.Name) {
        return nil, status.Error(codes.InvalidArgument, "workload name must be valid DNS-1123 subdomain")
    }

    // Validate resources
    if req.Spec.Resources.Requests.CpuMillicores <= 0 {
        return nil, status.Error(codes.InvalidArgument, "CPU request must be positive")
    }
    if req.Spec.Resources.Requests.MemoryBytes <= 0 {
        return nil, status.Error(codes.InvalidArgument, "memory request must be positive")
    }

    // Validate image
    if req.Spec.Image == "" {
        return nil, status.Error(codes.InvalidArgument, "container image is required")
    }
    if !isAllowedImage(req.Spec.Image) {
        return nil, status.Error(codes.PermissionDenied, "image not in allowed registry")
    }

    return s.createWorkload(ctx, req)
}
```

### 8.4 Policy Enforcement

**Use OPA-style policy engine:**
```go
func (c *Coordinator) AdmitWorkload(ctx context.Context, workload *api.Workload) error {
    if c.policyEngine == nil {
        return nil  // Policy engine optional in dev
    }

    result, err := c.policyEngine.Evaluate(ctx, workload)
    if err != nil {
        return fmt.Errorf("policy evaluation failed: %w", err)
    }

    if !result.Allowed {
        c.logger.Warn("Workload denied by policy",
            zap.String("workload", workload.Name),
            zap.String("reason", result.Reason),
            zap.Any("violations", result.Violations),
        )
        return status.Errorf(codes.PermissionDenied, "workload denied: %s", result.Reason)
    }

    return nil
}
```

### 8.5 Least Privilege

**Run with minimum required permissions:**
```go
// Drop capabilities after binding privileged port
import "golang.org/x/sys/unix"

func dropPrivileges() error {
    // Drop all capabilities except CAP_NET_BIND_SERVICE
    caps := &unix.CapUserHeader{Version: unix.LINUX_CAPABILITY_VERSION_3}
    data := &unix.CapUserData{}

    if err := unix.Capget(caps, data); err != nil {
        return fmt.Errorf("capget: %w", err)
    }

    data.Effective = 0
    data.Permitted = 0
    data.Inheritable = 0

    if err := unix.Capset(caps, data); err != nil {
        return fmt.Errorf("capset: %w", err)
    }

    return nil
}
```

---

## 9. Observability

### 9.1 Structured Logging

**Use `zap` for all logging:**
```go
import "go.uber.org/zap"

func NewScheduler(logger *zap.Logger) *Scheduler {
    return &Scheduler{
        logger: logger.Named("scheduler"),
    }
}

func (s *Scheduler) Schedule(ctx context.Context, workload *Workload) (*Placement, error) {
    s.logger.Info("Scheduling workload",
        zap.String("workload_id", workload.ID),
        zap.String("namespace", workload.Namespace),
        zap.Int64("cpu_millicores", workload.Spec.Resources.Requests.CpuMillicores),
        zap.Int64("memory_bytes", workload.Spec.Resources.Requests.MemoryBytes),
    )

    placement, err := s.selectNode(ctx, workload)
    if err != nil {
        s.logger.Error("Scheduling failed",
            zap.String("workload_id", workload.ID),
            zap.Error(err),
        )
        return nil, err
    }

    s.logger.Info("Workload scheduled",
        zap.String("workload_id", workload.ID),
        zap.String("node_id", placement.NodeID),
        zap.Float64("score", placement.Score),
    )

    return placement, nil
}
```

**Log Levels:**
- `DEBUG` - Detailed information for debugging (disabled in production)
- `INFO` - Normal operational events (node joins, workload scheduled)
- `WARN` - Unexpected but handled conditions (retry after failure)
- `ERROR` - Failures requiring attention (scheduling error, I/O failure)
- `FATAL` - Unrecoverable errors (process must exit)

### 9.2 Metrics with Prometheus

**Define metrics as package-level variables:**
```go
import "github.com/prometheus/client_golang/prometheus"

var (
    schedulingDecisions = prometheus.NewCounterVec(
        prometheus.CounterOpts{
            Namespace: "cloudless",
            Subsystem: "scheduler",
            Name:      "decisions_total",
            Help:      "Total number of scheduling decisions",
        },
        []string{"result"},  // success, failure
    )

    schedulingLatency = prometheus.NewHistogramVec(
        prometheus.HistogramOpts{
            Namespace: "cloudless",
            Subsystem: "scheduler",
            Name:      "decision_latency_seconds",
            Help:      "Time to make scheduling decision",
            Buckets:   prometheus.ExponentialBuckets(0.001, 2, 10),  // 1ms to ~1s
        },
        []string{"workload_type"},
    )

    nodeCapacity = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: "cloudless",
            Subsystem: "scheduler",
            Name:      "node_capacity_millicores",
            Help:      "Total CPU capacity per node",
        },
        []string{"node_id", "zone"},
    )
)

func init() {
    prometheus.MustRegister(schedulingDecisions)
    prometheus.MustRegister(schedulingLatency)
    prometheus.MustRegister(nodeCapacity)
}
```

**Record metrics:**
```go
func (s *Scheduler) Schedule(ctx context.Context, workload *Workload) (*Placement, error) {
    start := time.Now()

    placement, err := s.scheduleInternal(ctx, workload)

    duration := time.Since(start).Seconds()
    schedulingLatency.WithLabelValues(workload.Type).Observe(duration)

    if err != nil {
        schedulingDecisions.WithLabelValues("failure").Inc()
        return nil, err
    }

    schedulingDecisions.WithLabelValues("success").Inc()
    return placement, nil
}
```

**Metric Naming:**
- Use `<namespace>_<subsystem>_<name>_<unit>` format
- Units: `_seconds`, `_bytes`, `_ratio`, `_total`
- Labels for dimensions, NOT for high-cardinality values

### 9.3 Distributed Tracing

**Use OpenTelemetry:**
```go
import (
    "go.opentelemetry.io/otel"
    "go.opentelemetry.io/otel/attribute"
    "go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("cloudless/scheduler")

func (s *Scheduler) Schedule(ctx context.Context, workload *Workload) (*Placement, error) {
    ctx, span := tracer.Start(ctx, "Schedule",
        trace.WithAttributes(
            attribute.String("workload.id", workload.ID),
            attribute.String("workload.namespace", workload.Namespace),
        ),
    )
    defer span.End()

    nodes, err := s.filterNodes(ctx, workload)
    if err != nil {
        span.RecordError(err)
        return nil, err
    }
    span.SetAttributes(attribute.Int("nodes.filtered", len(nodes)))

    placement, err := s.selectNode(ctx, nodes, workload)
    if err != nil {
        span.RecordError(err)
        return nil, err
    }
    span.SetAttributes(attribute.String("placement.node", placement.NodeID))

    return placement, nil
}
```

**Propagate trace context across RPCs:**
```go
import "go.opentelemetry.io/contrib/instrumentation/google.golang.org/grpc/otelgrpc"

func setupGRPCServer() *grpc.Server {
    return grpc.NewServer(
        grpc.UnaryInterceptor(otelgrpc.UnaryServerInterceptor()),
        grpc.StreamInterceptor(otelgrpc.StreamServerInterceptor()),
    )
}

func setupGRPCClient() *grpc.ClientConn {
    return grpc.Dial(
        "coordinator:8080",
        grpc.WithUnaryInterceptor(otelgrpc.UnaryClientInterceptor()),
        grpc.WithStreamInterceptor(otelgrpc.StreamClientInterceptor()),
    )
}
```

---

## 10. Configuration

### 10.1 Configuration Sources

**Priority order (highest to lowest):**
1. Command-line flags
2. Environment variables
3. Config file
4. Defaults

```go
import (
    "flag"
    "os"

    "gopkg.in/yaml.v3"
)

type Config struct {
    ListenAddr string        `yaml:"listen_addr"`
    LogLevel   string        `yaml:"log_level"`
    DataDir    string        `yaml:"data_dir"`
    TLS        TLSConfig     `yaml:"tls"`
}

func LoadConfig(configPath string) (*Config, error) {
    // 1. Load defaults
    config := &Config{
        ListenAddr: ":8080",
        LogLevel:   "info",
        DataDir:    "/var/lib/cloudless",
    }

    // 2. Load from file
    if configPath != "" {
        data, err := os.ReadFile(configPath)
        if err != nil {
            return nil, fmt.Errorf("reading config file: %w", err)
        }

        if err := yaml.Unmarshal(data, config); err != nil {
            return nil, fmt.Errorf("parsing config file: %w", err)
        }
    }

    // 3. Override with environment variables
    if addr := os.Getenv("CLOUDLESS_LISTEN_ADDR"); addr != "" {
        config.ListenAddr = addr
    }
    if level := os.Getenv("CLOUDLESS_LOG_LEVEL"); level != "" {
        config.LogLevel = level
    }

    // 4. Override with flags (parsed elsewhere)
    if *flagListenAddr != "" {
        config.ListenAddr = *flagListenAddr
    }

    return config, nil
}
```

### 10.2 Validation

**Validate configuration at startup:**
```go
func (c *Config) Validate() error {
    if c.ListenAddr == "" {
        return errors.New("listen_addr is required")
    }

    if c.DataDir == "" {
        return errors.New("data_dir is required")
    }

    if _, err := os.Stat(c.DataDir); os.IsNotExist(err) {
        return fmt.Errorf("data_dir does not exist: %s", c.DataDir)
    }

    validLevels := map[string]bool{"debug": true, "info": true, "warn": true, "error": true}
    if !validLevels[c.LogLevel] {
        return fmt.Errorf("invalid log_level: %s (must be debug, info, warn, or error)", c.LogLevel)
    }

    if err := c.TLS.Validate(); err != nil {
        return fmt.Errorf("tls config: %w", err)
    }

    return nil
}
```

### 10.3 Sensitive Data

**Mask secrets in logs and debug output:**
```go
func (c *Config) String() string {
    masked := *c
    if masked.APIKey != "" {
        masked.APIKey = "***REDACTED***"
    }
    return fmt.Sprintf("%+v", masked)
}
```

---

## 11. API and Compatibility

### 11.1 Protobuf Versioning

**NEVER break backward compatibility:**
```protobuf
// Good - Add new fields with unique numbers
message WorkloadSpec {
    string image = 1;
    repeated string command = 2;
    ResourceRequirements resources = 3;

    // Added in v0.2
    RestartPolicy restart_policy = 4;
}

// Bad - Don't reuse field numbers or change types
message WorkloadSpec {
    string image = 1;
    repeated string command = 2;
    int32 resources = 3;  // Changed from ResourceRequirements - BREAKING!
}
```

**Deprecation:**
```protobuf
message WorkloadSpec {
    string image = 1;
    repeated string command = 2;

    // Deprecated: Use resources instead
    reserved 3;
    reserved "cpu_millicores";

    ResourceRequirements resources = 4;
}
```

### 11.2 API Evolution

**Use API versions:**
```protobuf
syntax = "proto3";

package cloudless.api.v1;

option go_package = "github.com/cloudless/cloudless/pkg/api;api";
```

**Introduce v2 API alongside v1:**
```
pkg/api/
├── v1/
│   └── cloudless.proto
└── v2/
    └── cloudless.proto
```

### 11.3 REST/JSON Compatibility

**gRPC-Gateway for HTTP/JSON:**
```go
import "github.com/grpc-ecosystem/grpc-gateway/v2/runtime"

func registerHTTPGateway(ctx context.Context, mux *runtime.ServixMux, endpoint string) error {
    opts := []grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}

    if err := api.RegisterCoordinatorServiceHandlerFromEndpoint(ctx, mux, endpoint, opts); err != nil {
        return err
    }

    return nil
}
```

---

## 12. Testing Policy

### 12.1 Test Structure

**Follow existing test organization:**
```
pkg/scheduler/
├── scheduler.go
├── scheduler_test.go         # Unit tests (co-located)
└── scheduler_bench_test.go   # Benchmarks (build tag: benchmark)

test/
├── testutil/
│   ├── helpers.go             # Shared test utilities
│   ├── mocks/                 # Mock implementations
│   └── fixtures/              # Test data generators
├── integration/               # Integration tests (build tag: integration)
├── chaos/                     # Chaos tests (build tag: chaos)
└── e2e/                       # End-to-end tests
```

### 12.2 Unit Tests

**Coverage requirements:**
- **Minimum 70%** statement coverage per package
- **Minimum 60%** branch coverage
- **100%** coverage for critical paths (security, data durability)

**Test naming:**
```go
func TestScheduler_Schedule_InsufficientCapacity(t *testing.T) {
    // Test name format: Test<Type>_<Method>_<Scenario>
}
```

**Table-driven tests:**
```go
func TestScheduler_FilterNodes(t *testing.T) {
    tests := []struct {
        name          string
        nodes         []*Node
        workload      *Workload
        wantFiltered  int
        wantErr       bool
    }{
        {
            name: "sufficient capacity",
            nodes: []*Node{
                {ID: "node-1", Capacity: &ResourceCapacity{CpuMillicores: 2000}},
                {ID: "node-2", Capacity: &ResourceCapacity{CpuMillicores: 1000}},
            },
            workload: &Workload{
                Spec: &WorkloadSpec{
                    Resources: &ResourceRequirements{
                        Requests: &ResourceCapacity{CpuMillicores: 500},
                    },
                },
            },
            wantFiltered: 2,
        },
        {
            name: "insufficient capacity",
            nodes: []*Node{
                {ID: "node-1", Capacity: &ResourceCapacity{CpuMillicores: 100}},
            },
            workload: &Workload{
                Spec: &WorkloadSpec{
                    Resources: &ResourceRequirements{
                        Requests: &ResourceCapacity{CpuMillicores: 500},
                    },
                },
            },
            wantFiltered: 0,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            s := NewScheduler(zap.NewNop())

            filtered := s.filterNodes(context.Background(), tt.nodes, tt.workload)

            if len(filtered) != tt.wantFiltered {
                t.Errorf("filterNodes() = %d nodes, want %d", len(filtered), tt.wantFiltered)
            }
        })
    }
}
```

### 12.3 Race Detection

**Run tests with race detector:**
```bash
go test -race ./...
```

**CI MUST fail on race conditions**

### 12.4 Integration Tests

**Use build tags:**
```go
//go:build integration

package integration

import "testing"

func TestClusterStartup(t *testing.T) {
    // Requires running coordinator and agents
}
```

**Run integration tests:**
```bash
go test -tags=integration ./test/integration/...
```

### 12.5 Benchmark Tests

**Use build tags for incomplete benchmarks:**
```go
//go:build benchmark

package scheduler

import "testing"

func BenchmarkScheduler_SelectNode(b *testing.B) {
    s := setupScheduler()
    workload := generateWorkload()

    b.ResetTimer()
    for i := 0; i < b.N; i++ {
        s.selectNode(context.Background(), workload)
    }
}
```

**Run benchmarks:**
```bash
go test -tags=benchmark -bench=. ./pkg/scheduler/...
```

### 12.6 Chaos Tests

**Test failure scenarios:**
```go
//go:build chaos

package chaos

func TestScheduler_NodeFailureRescheduling(t *testing.T) {
    cluster := setupTestCluster(t, 5)
    defer cluster.Teardown()

    // Deploy workload
    workload := deployWorkload(t, cluster)

    // Kill random node
    cluster.KillRandomNode()

    // Verify replicas rescheduled within SLO
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    if !waitForHealthyReplicas(ctx, workload, workload.Spec.Replicas) {
        t.Errorf("Replicas not rescheduled within 10s")
    }
}
```

---

## 13. Performance and Scalability

### 13.1 Performance Requirements

**From PRD (NFR-P1):**
- Scheduler decisions: **200ms P50, 800ms P95** under 5k nodes
- Membership convergence: **5s P50, 15s P95** after node join/leave
- Failed replica rescheduling: **3s P50, 10s P95**

### 13.2 Profiling

**Enable pprof in coordinator/agent:**
```go
import _ "net/http/pprof"

func enableProfiling(addr string) {
    go func() {
        log.Println("Starting pprof server on", addr)
        log.Println(http.ListenAndServe(addr, nil))
    }()
}

func main() {
    if os.Getenv("ENABLE_PPROF") == "true" {
        enableProfiling(":6060")
    }
    // ... rest of main
}
```

**Collect profiles:**
```bash
# CPU profile
go tool pprof http://localhost:6060/debug/pprof/profile?seconds=30

# Memory profile
go tool pprof http://localhost:6060/debug/pprof/heap

# Goroutine profile (check for leaks)
go tool pprof http://localhost:6060/debug/pprof/goroutine
```

### 13.3 Memory Management

**Avoid allocations in hot paths:**
```go
// Bad - Allocates on every call
func (s *Scheduler) scoreNode(node *Node) float64 {
    factors := []float64{
        s.localityScore(node),
        s.reliabilityScore(node),
        s.costScore(node),
    }

    var total float64
    for _, f := range factors {
        total += f
    }
    return total
}

// Good - No allocations
func (s *Scheduler) scoreNode(node *Node) float64 {
    return s.localityScore(node) +
           s.reliabilityScore(node) +
           s.costScore(node)
}
```

**Pool expensive objects:**
```go
var bufferPool = sync.Pool{
    New: func() interface{} {
        return new(bytes.Buffer)
    },
}

func processData(data []byte) ([]byte, error) {
    buf := bufferPool.Get().(*bytes.Buffer)
    defer func() {
        buf.Reset()
        bufferPool.Put(buf)
    }()

    buf.Write(data)
    return compress(buf.Bytes())
}
```

### 13.4 Benchmarking

**Track performance over time:**
```bash
# Run benchmarks and save results
go test -bench=. -benchmem ./pkg/scheduler/ > current.txt

# Compare with baseline
benchstat baseline.txt current.txt
```

**CI MUST fail on regressions > 10%**

---

## 14. CI/CD Pipeline (Reference)

### 14.1 CI Gates

**ALL of the following MUST pass:**

1. **Formatting** - `gofmt -s`, `goimports`
2. **Linting** - `golangci-lint run`
3. **Tests** - `go test -race -cover ./...`
4. **Build** - `make build` for all platforms
5. **Security Scan** - `gosec`, dependency vulnerability check
6. **License Check** - All dependencies compatible with project license

### 14.2 GitHub Actions Workflow

**See `.github/workflows/ci.yml`** for full pipeline

**Key jobs:**
- `lint` - Run all linters
- `test` - Run unit tests with race detector
- `test-integration` - Run integration tests (conditional)
- `build` - Build for linux/amd64 and linux/arm64
- `security` - Run gosec and trivy scans

**Required Go version:** 1.24 (matches `go.mod`)

### 14.3 Pre-merge Checklist

- [ ] All CI checks green
- [ ] Code reviewed by 2 owners
- [ ] Tests added for new code
- [ ] Documentation updated
- [ ] CHANGELOG entry added (for user-visible changes)

---

## 15. Code Review and PR Discipline

### 15.1 Pull Request Size

**Target: < 400 lines changed** (excluding generated code)

**For large changes:**
1. Split into logical commits
2. Submit as a series of PRs
3. Use feature branches

### 15.2 PR Description

**Required sections:**
```markdown
## What
Brief description of the change

## Why
Business or technical justification

## How
High-level implementation approach

## Testing
How was this tested?

## Checklist
- [ ] Tests added
- [ ] Documentation updated
- [ ] Metrics/logging added
- [ ] Security implications considered
```

### 15.3 Review Focus Areas

**Reviewers MUST check:**
1. **Correctness** - Does the code do what it claims?
2. **Error handling** - Are errors handled properly?
3. **Concurrency** - Are goroutines and channels used correctly?
4. **Security** - Any security implications?
5. **Performance** - Any obvious performance issues?
6. **Tests** - Are tests adequate?
7. **Style** - Does it follow this SOP?

### 15.4 Approval Requirements

**Minimum:**
- 2 approvals from package owners
- All CI checks passing
- No unresolved comments

**For sensitive areas (security, RAFT, storage):**
- 3 approvals including domain expert

---

## 16. Design Control

### 16.1 When Design Review is Required

**MUST have design review for:**
- New user-facing APIs
- Changes to storage formats or protocols
- New distributed algorithms
- Performance-critical paths
- Security-sensitive components

### 16.2 Design Document Template

**See `docs/design/TEMPLATE.md`**

**Required sections:**
1. Problem Statement
2. Goals and Non-Goals
3. Design Overview
4. Detailed Design
5. Alternatives Considered
6. Performance Impact
7. Security Considerations
8. Testing Plan
9. Rollout Plan

### 16.3 Review Process

1. Author creates design doc as PR
2. Notify relevant owners and stakeholders
3. Review period: 3-5 business days
4. Address comments
5. Approval from 2+ owners
6. Merge design doc
7. Implementation PRs reference design doc

---

## 17. Release Management

### 17.1 Versioning

**Semantic Versioning (SemVer):**
- `MAJOR.MINOR.PATCH` (e.g., `0.3.1`)
- `MAJOR` = incompatible API changes
- `MINOR` = backward-compatible features
- `PATCH` = backward-compatible bug fixes

**Pre-release:**
- `0.3.0-alpha.1` - Alpha releases
- `0.3.0-beta.2` - Beta releases
- `0.3.0-rc.1` - Release candidates

### 17.2 Release Process

1. **Create release branch:** `release/v0.3.0`
2. **Update CHANGELOG:** Document all changes
3. **Version bump:** Update version in code
4. **Tag release:** `git tag v0.3.0`
5. **Build artifacts:** Create binaries and container images
6. **Run full test suite:** Including integration and chaos tests
7. **Canary deployment:** Deploy to 1% of dev cluster
8. **Monitor for 24h:** Check metrics, logs, errors
9. **Gradual rollout:** 10%, 50%, 100%
10. **Publish release:** GitHub release with notes and SBOM

### 17.3 Hotfix Process

**For critical bugs in production:**
1. Create hotfix branch from release tag: `hotfix/v0.3.1`
2. Fix bug with minimal changes
3. Add regression test
4. Fast-track review (1 owner + tech lead)
5. Tag and release
6. Backport to main branch

---

## 18. Operational Runbooks

### 18.1 Debugging Checklist

**When investigating an issue:**

1. **Gather context**
   - What changed recently? (check git log, deployments)
   - When did it start? (check metrics/logs timeline)
   - Is it affecting all users or specific subset?

2. **Check observability**
   - Grafana dashboards for metrics
   - Loki for logs (filter by component, error level)
   - Jaeger for traces (find slow requests)

3. **Reproduce locally**
   - Can you reproduce with `make compose-up`?
   - Collect logs: `make compose-logs`

4. **Hypothesis testing**
   - Form hypothesis based on data
   - Test with minimal changes
   - Document findings

5. **Root cause analysis**
   - Identify root cause
   - Document in incident report
   - Create follow-up issues

### 18.2 Common Issues

**Issue: Scheduler slow to make decisions**

Symptoms:
- `cloudless_scheduler_decision_latency_seconds` P95 > 1s
- Workloads stuck in Pending state

Checks:
1. Check node count: `curl http://coordinator:8081/metrics | grep node_count`
2. Check CPU usage: `top -p $(pidof coordinator)`
3. Check for lock contention: `go tool pprof http://coordinator:6060/debug/pprof/mutex`

Resolution:
- If many nodes (> 5k), increase scheduler workers
- If high CPU, check for inefficient filtering logic
- If lock contention, review critical sections

**Issue: Storage replication lag**

Symptoms:
- `cloudless_storage_replication_lag_seconds` > 60s
- Objects under-replicated

Checks:
1. Check network connectivity between nodes
2. Check disk I/O: `iostat -x 5`
3. Check replication workers: `curl http://node:9092/metrics | grep replication_workers`

Resolution:
- Increase replication workers if CPU available
- Check network bandwidth and latency
- Verify disk not at capacity

---

## 19. Security Hygiene

### 19.1 Dependency Management

**Keep dependencies updated:**
```bash
# Check for updates
go list -u -m all

# Update specific dependency
go get -u github.com/some/package@latest
go mod tidy
```

**Run vulnerability scanner:**
```bash
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck ./...
```

### 19.2 Secrets Scanning

**Pre-commit hook:**
```bash
#!/bin/bash
# .git/hooks/pre-commit

if git diff --cached --name-only | xargs grep -E '(sk_live|api_key|password|secret)' ; then
    echo "Error: Potential secret detected in commit"
    exit 1
fi
```

**Use `gitleaks` in CI:**
```bash
gitleaks detect --source . --verbose
```

### 19.3 Security Audits

**Quarterly security review:**
- Review CODEOWNERS for sensitive packages
- Check dependency vulnerabilities
- Audit access controls and policies
- Review certificate rotation procedures
- Test incident response playbook

---

## 20. Feature Flags and Experiments

### 20.1 Feature Flag System

**Use feature flags for gradual rollouts:**
```go
type FeatureFlags struct {
    EnableNewScheduler   bool `yaml:"enable_new_scheduler"`
    EnableErasureCoding  bool `yaml:"enable_erasure_coding"`
    MaxReplicasPerNode   int  `yaml:"max_replicas_per_node"`
}

func (s *Scheduler) selectNode(ctx context.Context, nodes []*Node) (*Node, error) {
    if s.flags.EnableNewScheduler {
        return s.selectNodeV2(ctx, nodes)
    }
    return s.selectNodeV1(ctx, nodes)
}
```

### 20.2 A/B Testing

**Percentage-based rollout:**
```go
func (s *Scheduler) shouldUseNewAlgorithm(workloadID string) bool {
    hash := hashString(workloadID)
    return (hash % 100) < s.flags.NewAlgorithmRolloutPercent
}
```

---

## 21. Data and Compatibility Contracts

### 21.1 Storage Format Versioning

**All persisted data MUST include version:**
```go
type ChunkMetadata struct {
    Version   int    `json:"version"`  // Schema version
    ID        string `json:"id"`
    Size      int64  `json:"size"`
    Checksum  string `json:"checksum"`
    CreatedAt string `json:"created_at"`
}

func (s *ChunkStore) readMetadata(path string) (*ChunkMetadata, error) {
    data, err := os.ReadFile(path)
    if err != nil {
        return nil, err
    }

    var meta ChunkMetadata
    if err := json.Unmarshal(data, &meta); err != nil {
        return nil, err
    }

    // Migrate old versions
    if meta.Version < currentMetadataVersion {
        meta = s.migrateMetadata(meta)
    }

    return &meta, nil
}
```

### 21.2 Database Migrations

**Use versioned migrations:**
```
migrations/
├── 001_initial_schema.sql
├── 002_add_zone_column.sql
└── 003_add_reliability_index.sql
```

**Apply migrations on startup:**
```go
func (c *Coordinator) applyMigrations() error {
    currentVersion := c.getSchemaVersion()

    for version := currentVersion + 1; version <= latestSchemaVersion; version++ {
        if err := c.applyMigration(version); err != nil {
            return fmt.Errorf("migration %d failed: %w", version, err)
        }
    }

    return nil
}
```

---

## 22. Mapping to Requirements

### 22.1 Traceability

**Link code to PRD requirements:**
```go
// Schedule implements the workload placement algorithm.
//
// Satisfies requirements:
//   - CLD-REQ-020: Workload scheduling with multi-criteria scoring
//   - CLD-REQ-021: Scoring function with locality, reliability, cost, utilization
//   - NFR-P1: Scheduling decisions within 200ms P50, 800ms P95
func (s *Scheduler) Schedule(ctx context.Context, workload *Workload) (*Placement, error) {
    ...
}
```

### 22.2 Requirement Coverage

**Track in tests:**
```go
// TestScheduler_MultiCriteriaScoring verifies CLD-REQ-021.
func TestScheduler_MultiCriteriaScoring(t *testing.T) {
    // Test that scoring function uses all weighted factors
}

// BenchmarkScheduler_SchedulingLatency verifies NFR-P1.
func BenchmarkScheduler_SchedulingLatency(b *testing.B) {
    // Measure that 95% of decisions complete within 800ms
}
```

---

## 23. Local Development

### 23.1 Getting Started

**Initial setup:**
```bash
# Clone repository
git clone https://github.com/cloudless/cloudless.git
cd cloudless

# Install dependencies
make mod

# Generate protobuf code
make proto

# Build binaries
make build

# Run tests
make test

# Start local cluster
make compose-up

# View logs
make compose-logs

# Stop cluster
make compose-down
```

### 23.2 Development Workflow

**Typical workflow:**
1. Create feature branch: `git checkout -b feat/my-feature`
2. Make changes
3. Run tests locally: `make test`
4. Run linters: `make lint`
5. Format code: `make fmt`
6. Commit changes: `git commit -m "feat: add my feature"`
7. Push and create PR: `git push origin feat/my-feature`

### 23.3 Debugging with Delve

**Debug coordinator:**
```bash
dlv debug ./cmd/coordinator -- --config config/coordinator.yaml
```

**Attach to running process:**
```bash
dlv attach $(pidof coordinator)
```

---

## 24. Triage and On-call

### 24.1 Incident Severity

**SEV-1 (Critical):**
- Control plane completely down
- Data loss
- Security breach
- Response: Immediate, all hands

**SEV-2 (High):**
- Degraded service (> 5% error rate)
- Key features unavailable
- Response: Within 1 hour

**SEV-3 (Medium):**
- Minor functionality impaired
- Performance degradation
- Response: Next business day

**SEV-4 (Low):**
- Cosmetic issues
- Feature requests
- Response: Backlog

### 24.2 On-call Rotation

**Primary on-call:**
- Respond to pages within 15 minutes
- Triage and escalate
- Update incident status

**Secondary on-call:**
- Backup for primary
- Available for escalations

**On-call shifts:**
- 1 week rotation
- Handoff on Monday mornings
- Document ongoing incidents

### 24.3 Postmortem Process

**For SEV-1 and SEV-2 incidents:**

1. **Create postmortem doc** within 24h
2. **Timeline** - What happened when
3. **Root cause** - Why it happened
4. **Impact** - Who was affected, duration
5. **Resolution** - How it was fixed
6. **Action items** - Prevent recurrence
7. **Review meeting** - Within 1 week
8. **Follow-ups** - Track to completion

**Blameless culture:**
- Focus on systems, not individuals
- Document what we learned
- Improve processes

---

## 25. Decommission and Deprecation

### 25.1 Deprecation Policy

**For public APIs:**
1. **Announce deprecation** - In release notes and documentation
2. **Deprecation period** - Minimum 2 minor versions
3. **Migration guide** - Document alternative approach
4. **Warnings** - Log warnings when deprecated APIs used
5. **Removal** - In next major version

**Example:**
```go
// Deprecated: Use ScheduleWithConstraints instead.
// Will be removed in v1.0.0.
func (s *Scheduler) Schedule(workload *Workload) (*Placement, error) {
    log.Warn("Schedule is deprecated, use ScheduleWithConstraints")
    return s.ScheduleWithConstraints(context.Background(), workload, nil)
}
```

### 25.2 Feature Removal

**Process:**
1. Mark as deprecated for 2 releases
2. Update all internal usage
3. Notify users via blog post
4. Remove in major version bump
5. Update CHANGELOG

---

## 26. Checklists

### 26.1 New Package Checklist

When creating a new package:

- [ ] **Owners set** - Add to CODEOWNERS file
- [ ] **README** - Document purpose, dependencies, usage
- [ ] **Package doc** - Add package-level doc comment
- [ ] **API documented** - Godoc for all exported types/functions
- [ ] **Unit tests** - With race detector (`go test -race`)
- [ ] **Coverage** - Minimum 70% statement coverage
- [ ] **Metrics** - Add Prometheus metrics for key operations
- [ ] **Logging** - Structured logging with zap
- [ ] **Tracing** - OpenTelemetry spans for distributed operations
- [ ] **Error handling** - Proper error wrapping and types
- [ ] **Concurrency** - Document concurrency safety
- [ ] **Security review** - If touches network, secrets, or policies

### 26.2 New RPC Checklist

When adding a new gRPC RPC:

- [ ] **Backward compatible** - .proto changes don't break existing clients
- [ ] **Context parameter** - First parameter is `context.Context`
- [ ] **Deadline handling** - Respect context deadline
- [ ] **Validation** - Validate all request fields
- [ ] **Idempotency** - Safe to retry (use idempotency keys if needed)
- [ ] **Error codes** - Return appropriate gRPC status codes
- [ ] **Metrics** - Counter for requests, histogram for latency
- [ ] **Tracing** - Propagate trace context
- [ ] **Logging** - Log important events (start, end, errors)
- [ ] **Rate limiting** - Protect against abuse
- [ ] **Documentation** - Proto comments explain purpose and usage
- [ ] **Tests** - Unit tests for handler logic
- [ ] **Integration test** - End-to-end RPC test

### 26.3 Concurrency Checklist

When adding concurrent code:

- [ ] **No unbounded goroutines** - Use worker pools or semaphores
- [ ] **Context cancellation** - Respect context.Done()
- [ ] **Channel ownership** - Sender closes, documented clearly
- [ ] **Deadlines** - Set timeouts for blocking operations
- [ ] **Backoff** - Use exponential backoff for retries
- [ ] **Leak test** - Test for goroutine leaks
- [ ] **Race detector** - Run tests with `-race`
- [ ] **Mutex scope** - Keep critical sections small
- [ ] **Lock ordering** - Document and follow lock order
- [ ] **Resource cleanup** - Use defer for cleanup
- [ ] **Error propagation** - Handle errors in goroutines
- [ ] **Documentation** - Document concurrency safety

### 26.4 Release Checklist

Before releasing a new version:

- [ ] **All CI gates green** - No failing tests or linters
- [ ] **Version bump** - Update version in code and docs
- [ ] **CHANGELOG** - Document all changes since last release
- [ ] **Dependencies** - All dependencies up to date
- [ ] **Vulnerability scan** - No known vulnerabilities
- [ ] **Build artifacts** - Binaries for all platforms
- [ ] **Container images** - Tagged and pushed to registry
- [ ] **Documentation** - User docs updated
- [ ] **Migration guide** - If breaking changes
- [ ] **Canary deployment** - Deploy to 1% of dev cluster
- [ ] **Monitoring** - Watch metrics for 24h
- [ ] **Gradual rollout** - 10%, 50%, 100%
- [ ] **Rollback plan** - Tested rollback procedure
- [ ] **Release notes** - Published to GitHub
- [ ] **SBOM** - Software Bill of Materials attached
- [ ] **Announce** - Blog post, Slack, email

---

## 27. References and Tooling

### 27.1 Go Resources

**Official:**
- [Go Documentation](https://go.dev/doc/)
- [Effective Go](https://go.dev/doc/effective_go)
- [Go Code Review Comments](https://github.com/golang/go/wiki/CodeReviewComments)

**Books:**
- "The Go Programming Language" - Donovan & Kernighan
- "Concurrency in Go" - Katherine Cox-Buday
- "100 Go Mistakes and How to Avoid Them" - Teiva Harsanyi

### 27.2 Required Tools

**Development:**
- Go 1.24+ - https://go.dev/dl/
- golangci-lint v1.62.2+ - https://golangci-lint.run/
- protoc + protoc-gen-go - https://grpc.io/docs/languages/go/quickstart/
- Docker + Docker Compose - https://docs.docker.com/

**Optional:**
- Delve (debugger) - https://github.com/go-delve/delve
- pprof (profiler) - Built into Go
- grpcurl (gRPC CLI) - https://github.com/fullstorydev/grpcurl

### 27.3 Cloudless-Specific Docs

**In this repository:**
- `README.md` - User-facing quick start
- `CLAUDE.md` - Developer context for AI assistance
- `GO_ENGINEERING_SOP.md` - This document
- `Cloudless.md` - Product Requirements Document (PRD)
- `docs/architecture/` - Architecture diagrams and decisions
- `docs/design/` - Design documents for major features
- `docs/runbooks/` - Operational procedures

### 27.4 External Services

**Observability:**
- Prometheus - https://prometheus.io/
- Grafana - https://grafana.com/
- Loki - https://grafana.com/oss/loki/
- Jaeger - https://www.jaegertracing.io/

**Infrastructure:**
- containerd - https://containerd.io/
- QUIC - https://datatracker.ietf.org/doc/html/rfc9000
- RAFT - https://raft.github.io/

---

## Appendix A: Performance Targets Summary

| Metric | Target | Source |
|--------|--------|--------|
| Scheduler decision latency | 200ms P50, 800ms P95 | NFR-P1 |
| Membership convergence | 5s P50, 15s P95 | CLD-REQ-002 |
| Failed replica rescheduling | 3s P50, 10s P95 | CLD-REQ-031 |
| Coordinator availability | 99.9% monthly | NFR-A1 |

## Appendix B: Error Code Reference

| Internal Error | gRPC Code | HTTP Status |
|----------------|-----------|-------------|
| ErrNotFound | codes.NotFound | 404 |
| ErrInsufficientCapacity | codes.ResourceExhausted | 429 |
| ErrPolicyViolation | codes.PermissionDenied | 403 |
| ErrInvalidRequest | codes.InvalidArgument | 400 |
| ErrUnauthenticated | codes.Unauthenticated | 401 |
| ErrUnavailable | codes.Unavailable | 503 |
| (default) | codes.Internal | 500 |

## Appendix C: Metric Naming Conventions

| Type | Suffix | Example |
|------|--------|---------|
| Counter | `_total` | `scheduler_decisions_total` |
| Gauge | (none) | `node_count` |
| Histogram | `_seconds`, `_bytes` | `scheduling_latency_seconds` |
| Summary | `_seconds`, `_bytes` | `request_duration_seconds` |

---

**End of Document**

**Questions or Suggestions?**
Open an issue or submit a PR to improve this SOP.

**Last Updated:** 2025-10-26
**Version:** 1.0
