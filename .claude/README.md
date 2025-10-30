# Cloudless Specialized Agents

This directory contains specialized Claude Code agents designed to work on the Cloudless distributed container orchestration platform. Each agent has specific expertise, tools, and responsibilities aligned with GO_ENGINEERING_SOP.md and Cloudless.MD requirements.

## Overview

Cloudless uses a **multi-agent architecture** where specialized agents handle different aspects of software development:

- **Product Owner**: Requirements validation and feature prioritization
- **Architect**: System design and architecture decisions
- **Engineer**: Code implementation following standards
- **Tech Lead**: Code review and quality enforcement
- **Unit Tester**: Test code development
- **Documenter**: Documentation creation

Each agent operates with its own context window and system prompt, ensuring focused, high-quality work within its domain.

## Quick Start

### Automatic Agent Invocation

Claude automatically invokes agents when your task matches their description patterns:

```bash
# These requests automatically invoke the appropriate agents:
"Is GPU scheduling in MVP scope?"                    → Product Owner
"Design a distributed caching layer"                 → Architect
"Implement the scheduler scoring function"           → Engineer
"Review the concurrent workload processing code"     → Tech Lead
"Write tests for the secrets manager"                → Unit Tester
"Create user guide for secrets management"           → Documenter
```

### Manual Agent Invocation

You can explicitly request specific agents:

```bash
# Prefix your request with the agent name:
"Product Owner: Validate this feature against CLD-REQ requirements"
"Architect: Create a design document for distributed caching"
"Engineer: Implement CLD-REQ-063 secrets management"
"Tech Lead: Review pkg/scheduler/scorer.go for race conditions"
"Unit Tester: Write table-driven tests for pkg/cache/"
"Documenter: Generate API reference for SecretsService"
```

## Agent Capabilities

### Product Owner (`product-owner.md`)

**Role**: Guardian of product vision and MVP scope

**When to Use**:
- New feature requests
- Requirements validation
- MVP scope questions
- Feature prioritization
- Acceptance criteria definition

**Key Expertise**:
- CLD-REQ-* functional requirements (001-081)
- NFR-* non-functional requirements
- Product tenets (T1-T4: Predictability, Security, Graceful Degradation, Simplicity)
- MVP phases (P0-P6)

**Example Invocations**:
```
"Product Owner: Is GPU scheduling in scope for v0.1?"
"Product Owner: Map this feature to existing CLD-REQ requirements"
"Product Owner: Define acceptance criteria for distributed caching"
"Product Owner: Should we implement erasure coding in P4 or P7?"
```

**Tools**: Read, Grep, Glob, AskUserQuestion, Task

**Required Reading**:
- Cloudless.MD §1 (Vision), §3 (Goals/Non-Goals), §4 (Tenets), §7 (Requirements), §17 (MVP Scope)
- GO_ENGINEERING_SOP.md §1 (Purpose), §22 (Requirements Mapping)

### Architect (`architect.md`)

**Role**: Chief Architect for system design and technical decisions

**When to Use**:
- Designing new features
- Adding new packages
- Changing storage/protocol formats
- Distributed algorithms
- Performance-critical changes
- Security-sensitive architecture

**Key Expertise**:
- Three-Plane Architecture (Control, Data, Observability)
- Performance targets (200ms P50, 800ms P95 scheduler)
- Design document templates (§16.2)
- Mermaid diagrams for architecture

**Example Invocations**:
```
"Architect: Design a distributed caching layer for the coordinator"
"Architect: Create design doc for CLD-REQ-090 caching"
"Architect: Evaluate Redis vs memcached vs custom cache"
"Architect: Design overlay network protocol changes for NAT traversal"
```

**Tools**: Read, Write, Edit, Grep, Glob, Task, WebFetch, AskUserQuestion

**Required Reading**:
- GO_ENGINEERING_SOP.md §2 (Repository Layout), §16 (Design Control), §22 (Requirements Mapping)
- Cloudless.MD §6 (System Overview), §8 (NFRs)
- CLAUDE.md (Architecture Deep Dive)

### Engineer (`engineer.md`)

**Role**: Senior Software Engineer for production code implementation

**When to Use**:
- Implementing features
- Fixing bugs
- Refactoring code
- Adding new functionality
- Optimizing performance

**Key Expertise**:
- GO_ENGINEERING_SOP.md compliance (§3-§12)
- Error handling patterns (§4)
- Concurrency safety (§5)
- Structured logging and metrics (§9)
- Security compliance (§8)

**Example Invocations**:
```
"Engineer: Implement pkg/cache/ for distributed caching"
"Engineer: Fix race condition in pkg/scheduler/scorer.go"
"Engineer: Add mTLS support to pkg/cache/redis.go"
"Engineer: Refactor pkg/coordinator/membership/ for better testability"
```

**Tools**: Read, Write, Edit, Grep, Glob, Bash, Task

**Required Reading**:
- GO_ENGINEERING_SOP.md §3 (Coding Standards), §4 (Error Handling), §5 (Concurrency), §8 (Security), §9 (Observability), §12 (Testing)
- CLAUDE.md (Architecture sections for the component)

### Tech Lead (`tech-lead.md`)

**Role**: Technical Lead for code review and quality enforcement

**When to Use**:
- Code reviews
- PR feedback
- Performance concerns
- Security reviews
- Concurrency validation
- Architecture alignment checks

**Key Expertise**:
- Code review checklist (§15)
- Performance targets (NFR-P1)
- Concurrency patterns (§5)
- Security validation (§8)
- Test coverage requirements (70%+)

**Example Invocations**:
```
"Tech Lead: Review pkg/cache/*.go for correctness and safety"
"Tech Lead: Validate performance of scheduler changes"
"Tech Lead: Check pkg/secrets/ for security vulnerabilities"
"Tech Lead: Review test coverage for pkg/coordinator/"
```

**Tools**: Read, Grep, Glob, Bash, Task

**Required Reading**:
- GO_ENGINEERING_SOP.md §15 (Code Review), §13 (Performance), §12 (Testing), §5 (Concurrency), §8 (Security), §26 (Checklists)
- CLAUDE.md (Performance Targets, Architecture)

### Unit Tester (`unit-tester.md`)

**Role**: Test Engineer for comprehensive unit test development

**When to Use**:
- Writing new tests
- Improving test coverage
- Test failures investigation
- Race condition testing
- Edge case validation

**Key Expertise**:
- Table-driven tests
- 70%+ statement coverage requirement
- Race detector usage
- Build tags (integration, chaos, benchmark)
- Mock patterns

**Example Invocations**:
```
"Unit Tester: Write table-driven tests for pkg/cache/"
"Unit Tester: Add race detector tests for pkg/scheduler/scorer.go"
"Unit Tester: Improve coverage for pkg/secrets/ to 70%+"
"Unit Tester: Write integration tests for pkg/overlay/"
```

**Tools**: Read, Write, Edit, Grep, Glob, Bash, Task

**Required Reading**:
- GO_ENGINEERING_SOP.md §12 (Testing Policy)
- CLAUDE.md (Build Tags Strategy, Test Structure)

### Documenter (`documenter.md`)

**Role**: Technical Writer for comprehensive documentation

**When to Use**:
- Creating documentation
- Updating design docs
- Writing API references
- Creating user guides
- Writing runbooks
- Package README creation

**Key Expertise**:
- Design document template (§16.2)
- Godoc best practices (§3.4)
- Mermaid diagrams
- API reference generation
- User guide structure

**Example Invocations**:
```
"Documenter: Create API reference for SecretsService gRPC API"
"Documenter: Write user guide for distributed caching"
"Documenter: Update CLD-REQ-090 design doc to 'Implemented' status"
"Documenter: Create runbook for debugging cache issues"
```

**Tools**: Read, Write, Edit, Grep, Glob, Task, WebFetch

**Required Reading**:
- GO_ENGINEERING_SOP.md §3.4 (Comments), §16.2 (Design Template), §26.1 (Package README)
- CLAUDE.md (all sections for context)

## Workflow Patterns

### Pattern 1: Full Feature Development

**Use Case**: New feature requiring design, implementation, tests, and documentation

**Workflow**:
```
User Request → Product Owner → Architect → Engineer → Unit Tester → Tech Lead → Documenter
```

**Example**: "Add distributed caching to reduce coordinator load"

**Agents Involved**: All 6 agents

**Duration**: 3-5 iterations, 2-4 hours total

**Artifacts**:
- Design document: `docs/design/CLD-REQ-090-DISTRIBUTED-CACHE.md`
- Implementation: `pkg/cache/*.go`
- Tests: `pkg/cache/*_test.go`
- Documentation: `docs/guides/caching.md`

### Pattern 2: Quick Bug Fix

**Use Case**: Simple bug fix with minimal scope

**Workflow**:
```
User Request → Engineer → Unit Tester → Tech Lead
```

**Example**: "Fix race condition in scheduler node scoring"

**Agents Involved**: Engineer, Unit Tester, Tech Lead

**Duration**: 1-2 iterations, 30-60 minutes

**Artifacts**:
- Fix: `pkg/scheduler/scorer.go` (10 LOC changed)
- Tests: `pkg/scheduler/scorer_test.go` (50 LOC added)

### Pattern 3: Documentation-Only

**Use Case**: Creating or updating documentation without code changes

**Workflow**:
```
User Request → Documenter → Tech Lead
```

**Example**: "Document the SecretsService gRPC API"

**Agents Involved**: Documenter, Tech Lead

**Duration**: 1 iteration, 30-45 minutes

**Artifacts**:
- Documentation: `docs/api/secrets.md`

### Pattern 4: Design Review

**Use Case**: Architectural decision before implementation

**Workflow**:
```
User Request → Architect → Tech Lead → [Implementation]
```

**Example**: "Should we use Redis or memcached for distributed caching?"

**Agents Involved**: Architect, Tech Lead

**Duration**: 1 iteration, 20-30 minutes

**Artifacts**:
- Design decision: Added to design doc or CLAUDE.md

### Pattern 5: Test Improvement

**Use Case**: Improving test coverage or adding missing tests

**Workflow**:
```
User Request → Unit Tester → Tech Lead
```

**Example**: "Improve test coverage for pkg/secrets/ to 70%+"

**Agents Involved**: Unit Tester, Tech Lead

**Duration**: 1-2 iterations, 45-90 minutes

**Artifacts**:
- Tests: Additional test files and cases

## Agent Coordination Rules

### Mandatory Sequential Ordering

1. **Product Owner validates BEFORE Engineer implements**
   - Rationale: Prevents wasted effort on out-of-scope features
   - Exception: Obvious bug fixes (< 50 LOC)

2. **Architect designs BEFORE Engineer implements**
   - Rationale: Ensures design alignment and prevents rework
   - Exception: Simple features (< 100 LOC) with clear pattern

3. **Unit Tester writes tests DURING/AFTER Engineer implements**
   - Rationale: Tests are part of the deliverable, not an afterthought
   - Exception: None (tests are always required)

4. **Tech Lead reviews AFTER Engineer + Unit Tester complete**
   - Rationale: Review code and tests together
   - Exception: Design-only reviews (Architect → Tech Lead)

5. **Documenter documents AFTER implementation stabilizes**
   - Rationale: Don't document APIs that might change
   - Exception: Design docs can be drafted during implementation

### Parallel Execution Opportunities

When agents can work in parallel (no dependencies):

```
✅ PARALLEL: Architect designs + Tech Lead reviews existing code
✅ PARALLEL: Unit Tester writes tests + Documenter drafts API reference
✅ PARALLEL: Product Owner validates feature A + Architect designs feature B
❌ SEQUENTIAL: Engineer implements → Unit Tester writes tests (dependency)
❌ SEQUENTIAL: Product Owner validates → Architect designs (dependency)
```

## Task Complexity Guide

Not all tasks require all agents. Use this guide to determine which agents to involve:

| **Complexity** | **LOC** | **Agents** | **Example** |
|----------------|---------|-----------|-------------|
| **Trivial** | < 20 | Engineer + Tech Lead | Fix typo, update comment |
| **Simple** | 20-100 | Engineer + Unit Tester + Tech Lead | Bug fix, small refactor |
| **Moderate** | 100-500 | Architect + Engineer + Unit Tester + Tech Lead | New API endpoint |
| **Complex** | 500-2000 | Product Owner + Architect + Engineer + Unit Tester + Tech Lead + Documenter | New subsystem |
| **Major** | > 2000 | Full workflow + multiple iterations | New storage engine |

## Best Practices

### 1. Always Validate Requirements First

For any new feature, start with the Product Owner agent:

```bash
# ✅ Good
"Product Owner: Is distributed caching in MVP scope?"
# Then after approval:
"Architect: Design distributed caching"

# ❌ Bad
"Engineer: Implement distributed caching"
# (Might be out of scope or duplicate work)
```

### 2. Use Fast-Track Workflows for Simple Tasks

Don't over-engineer simple tasks:

```bash
# ✅ Good - Bug fix fast track
"Engineer: Fix typo in pkg/scheduler/scorer.go line 145"
# Tech Lead reviews automatically

# ❌ Bad - Unnecessary overhead
"Product Owner: Validate typo fix requirement"
"Architect: Design typo fix approach"
# (Too much overhead for 1 LOC change)
```

### 3. Provide Context for Handoffs

When one agent completes, provide context for the next:

```bash
# ✅ Good
"Product Owner approved distributed caching for P7 (CLD-REQ-090).
Architect: Design Redis-based caching with mTLS support."

# ❌ Bad
"Architect: Design caching"
# (Missing: Which requirement? What scope? What constraints?)
```

### 4. Batch Related Changes

Don't invoke agents for every tiny change:

```bash
# ✅ Good
"Engineer: Implement pkg/cache/ with Redis client, mTLS, and metrics"
# (One invocation, complete implementation)

# ❌ Bad
"Engineer: Create pkg/cache/ directory"
"Engineer: Add Redis client"
"Engineer: Add mTLS support"
"Engineer: Add metrics"
# (4 invocations for one feature)
```

### 5. Use Parallel Execution When Possible

Run independent agents in parallel:

```bash
# ✅ Good
"Unit Tester: Write tests for pkg/cache/ in parallel with
Documenter: Draft API reference for pkg/cache/"

# ❌ Bad
"Unit Tester: Write tests for pkg/cache/"
# Wait for completion...
"Documenter: Draft API reference for pkg/cache/"
# (Sequential when parallel is possible)
```

## Troubleshooting

### Agent Produces Incorrect Output

**Symptoms**:
- Agent misses requirements
- Agent generates non-compliant code
- Agent doesn't follow GO_ENGINEERING_SOP.md

**Solutions**:
1. Check agent has access to latest CLAUDE.md, GO_ENGINEERING_SOP.md, Cloudless.MD
2. Provide more explicit context in the request
3. Use manual invocation with specific instructions
4. Review agent's "Required Reading" sections

### Agent Workflow Feels Slow

**Symptoms**:
- Too many agents involved for simple tasks
- Sequential execution when parallel is possible

**Solutions**:
1. Assess task complexity (see table above)
2. Use fast-track workflows for simple tasks (< 100 LOC)
3. Invoke independent agents in parallel
4. Batch related changes

### Agent Misses MVP Scope Requirements

**Symptoms**:
- Feature implemented but out of scope
- Product Owner not consulted

**Solutions**:
1. Always invoke Product Owner first for new features
2. Reference CLD-REQ-* explicitly in requests
3. Check Cloudless.MD §17 (MVP Scope) before requesting features

### Agent Code Doesn't Meet Standards

**Symptoms**:
- Code fails linting
- Test coverage < 70%
- Race detector failures

**Solutions**:
1. Engineer agent should always run `make lint` and `make test-race`
2. Tech Lead agent should review before approval
3. Reference GO_ENGINEERING_SOP.md sections explicitly

## Agent Standards Compliance

Each agent enforces specific GO_ENGINEERING_SOP.md sections:

| **Agent** | **Enforces** | **Validation** |
|-----------|--------------|----------------|
| Product Owner | §1 (Purpose), §22 (Requirements Mapping) | CLD-REQ-* mapping, MVP scope |
| Architect | §2 (Repository Layout), §16 (Design Control) | §16.2 template, diagrams |
| Engineer | §3-§12 (Coding to Testing) | Lint passes, 70%+ coverage |
| Tech Lead | §15 (Code Review), §13 (Performance) | NFR-P1 targets, concurrency |
| Unit Tester | §12 (Testing Policy) | Table-driven, race detector |
| Documenter | §3.4 (Comments), §16.2 (Design Template) | Godoc complete, design docs |

## Performance Considerations

Agent invocation overhead is minimal (< 1s per agent), but consider:

- **Batching**: Don't invoke for every tiny change
- **Fast-track workflows**: Simple bugs don't need full workflow
- **Parallel execution**: Run independent agents concurrently
- **Context caching**: Agents remember project context

## Examples

### Example 1: Full Feature Development

**User Request**: "Add distributed caching to reduce coordinator load"

**Workflow**:
```
1. "Product Owner: Validate distributed caching feature"
   → Output: Approved for P7, maps to CLD-REQ-090

2. "Architect: Design Redis-based distributed caching"
   → Output: docs/design/CLD-REQ-090-DISTRIBUTED-CACHE.md

3. "Engineer: Implement pkg/cache/ with Redis, mTLS, metrics"
   → Output: pkg/cache/*.go (500 LOC)

4. "Unit Tester: Write table-driven tests for pkg/cache/"
   → Output: pkg/cache/*_test.go (600 LOC, 82% coverage)

5. "Tech Lead: Review pkg/cache/ implementation and tests"
   → Output: Approved with minor feedback

6. "Documenter: Create user guide and API reference for caching"
   → Output: docs/guides/caching.md, updated design doc
```

### Example 2: Quick Bug Fix

**User Request**: "Fix race condition in scheduler node scoring"

**Workflow**:
```
1. "Engineer: Fix race condition in pkg/scheduler/scorer.go"
   → Output: Added sync.RWMutex (10 LOC changed)

2. "Unit Tester: Add race detector test for concurrent scoring"
   → Output: pkg/scheduler/scorer_test.go (50 LOC added)

3. "Tech Lead: Review race condition fix"
   → Output: LGTM (Approved)
```

### Example 3: Documentation-Only

**User Request**: "Document the SecretsService gRPC API"

**Workflow**:
```
1. "Documenter: Generate API reference for SecretsService"
   → Output: docs/api/secrets.md (400 lines)

2. "Tech Lead: Review API documentation for accuracy"
   → Output: Approved with typo fixes
```

## Additional Resources

- **CLAUDE.md**: Architecture context, development setup, tips
- **GO_ENGINEERING_SOP.md**: Comprehensive engineering standards
- **Cloudless.MD**: Product requirements and vision
- **Agent Files**: `.claude/agents/*.md` for detailed agent specifications

## Support

If agents aren't working as expected:

1. Check agent has access to required documents (CLAUDE.md, GO_ENGINEERING_SOP.md, Cloudless.MD)
2. Review agent description patterns in `.claude/agents/*.md`
3. Provide more explicit context in requests
4. Consult this README for workflow guidance
5. Report issues to the Cloudless team

---

**Last Updated**: January 2025
**Agent System Version**: 1.0
**Compatible with**: Claude Code CLI
