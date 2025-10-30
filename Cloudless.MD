# Cloudless Platform — Product Requirements & Architecture v0.1

## 1. Vision

Unify heterogeneous devices (phones, laptops, PCs, IoT gateways, VPS) into a single elastic compute fabric that provisions **virtual resources** (CPU, memory, storage, bandwidth) to run applications with high availability and graceful churn handling. Built in Go.

## 2. Problem Statement

Today, compute capacity on personal and edge devices sits idle. Cloudless aggregates this surplus, presents it as a dependable pool, and schedules workloads across it while tolerating node failures, mobility, and intermittent connectivity.

## 3. Goals and Non‑Goals

### 3.1 Goals

* G1. Pool CPU, RAM, storage, and network across many devices into a **Virtual Resource Network (VRN)**.
* G2. Deploy containerized applications across this VRN with **zero‑downtime failover**.
* G3. Support **graceful churn**: nodes join/leave without manual intervention.
* G4. Provide secure, mutually authenticated communication and isolation between tenants and workloads.
* G5. Offer simple **CLI** and **gRPC API** for developers and operators.
* G6. Region‑aware placement for latency and bandwidth efficiency.
* G7. Minimal operational footprint; single VPS can anchor coordination for Internet reachability.

### 3.2 Non‑Goals (v0.x)

* NG1. Full VM orchestration or live process migration.
* NG2. Stateful database sharding across churny nodes. Prefer managed or replicated stores with quorum.
* NG3. Multi‑tenant billing and marketplace economics.
* NG4. GPU virtualization beyond static passthrough where available.

## 4. Tenets

* T1. **Predictability over peak throughput.**
* T2. **Secure by default.** No plaintext, no unauthenticated control paths.
* T3. **Graceful degradation.** Partial networks must remain useful.
* T4. **Simple first.** Prefer primitives that compose.

## 5. Key Concepts and Glossary

* **Node:** Any device running the Cloudless Agent.
* **Coordinator:** Control‑plane service managing membership, placement, and state. Can run on a VPS anchor.
* **Fragment:** A reserved slice of resources on a node (e.g., 250mCPU, 256MiB RAM, 1GiB storage, 5Mbps). Workloads consume one or more fragments.
* **Workload:** A container image plus runtime policy. (v0.x supports OCI/Docker containers only.)
* **VRN:** The aggregated view of all fragments across nodes.
* **Region / Zone:** Operator‑defined locality labels (e.g., "iq-bgw", "eu-frankfurt").

## 6. System Overview

### 6.1 High‑Level Architecture

* **Control Plane:** Coordinator, Scheduler, Metadata Store (RAFT), API Gateway.
* **Data Plane:** Node Agent, Resource Monitor, Overlay Networking, Storage Engine.
* **Observability Plane:** Metrics, logs, traces, event stream.

### 6.2 Control/Data Separation

* Control messages flow via gRPC over mTLS (QUIC preferred). Data plane uses an encrypted overlay (WireGuard‑class or QUIC streams) with service discovery.

## 7. Functional Requirements

All requirements have IDs for traceability.

### 7.1 Membership and Discovery

* CLD‑REQ‑001: Nodes must enroll using bootstrap credentials and complete mTLS handshake with the Coordinator.
* CLD‑REQ‑002: Membership must converge within 5s P50 and 15s P95 after a node joins or leaves.
* CLD‑REQ‑003: NAT traversal must be supported via STUN; fallback to TURN or relay through the VPS anchor.

### 7.2 Resource Abstraction and Accounting

* CLD‑REQ‑010: Each node reports available CPU (millicores), RAM (MiB), storage (GiB, IOPS class), and egress bandwidth (Mbps).
* CLD‑REQ‑011: The Agent enforces cgroups/quotas to honor fragment reservations.
* CLD‑REQ‑012: The Coordinator maintains a consistent VRN inventory (RAFT, majority quorum).

### 7.3 Scheduling and Placement

* CLD‑REQ‑020: Workloads declare a **Spec** with: resources, replicas, region/zone affinity, anti‑affinity, restart policy, rollout strategy.
* CLD‑REQ‑021: Scheduler uses a scoring function `S = w_l*locality + w_r*reliability + w_c*cost + w_u*utilization_balance − w_n*network_penalty`.
* CLD‑REQ‑022: Bin‑pack by default; support spread across zones for HA.
* CLD‑REQ‑023: Rolling updates must keep `minAvailable` replicas serving during rollout.

### 7.4 Availability and Churn Handling

* CLD‑REQ‑030: Device failure must not reduce service below declared `minAvailable` when capacity exists in the VRN.
* CLD‑REQ‑031: Failed replicas are rescheduled within 3s P50, 10s P95.
* CLD‑REQ‑032: Health probes (liveness/readiness) drive automated restart and traffic gating.

### 7.5 Networking

* CLD‑REQ‑040: Mesh overlay provides authenticated, encrypted L3 between workloads.
* CLD‑REQ‑041: Built‑in service registry offers stable virtual IP/DNS names.
* CLD‑REQ‑042: Ingress supports per‑service L4 load balancing with weighted endpoints and locality.

### 7.6 Storage

* CLD‑REQ‑050: Provide a **Distributed Object Store (DOS)** for artifacts and app data with replication factor `R` (default 3) and eventual consistency.
* CLD‑REQ‑051: Metadata (buckets, manifests) is strongly consistent via RAFT.
* CLD‑REQ‑052: Node‑local ephemeral volumes are isolated per workload.
* CLD‑REQ‑053: Optional erasure coding for cold data when node count ≥ 6.

### 7.7 Security

* CLD‑REQ‑060: All control and data traffic uses mTLS with rotating certificates.
* CLD‑REQ‑061: Policy engine supports allow/deny for images, registries, capabilities, and egress destinations.
* CLD‑REQ‑062: Workloads run with least privilege and optional sandboxing (seccomp/AppArmor; gVisor‑class optional profile).
* CLD‑REQ‑063: Secrets are stored encrypted at rest and delivered via short‑lived tokens.

### 7.8 Observability and Operations

* CLD‑REQ‑070: System exports Prometheus‑compatible metrics, structured logs, and OpenTelemetry traces.
* CLD‑REQ‑071: Event stream records membership, scheduling, and security decisions.
* CLD‑REQ‑072: CLI supports `cloudless node|workload|storage|network` verbs with JSON output.

### 7.9 APIs and SDK

* CLD‑REQ‑080: Public gRPC API for workload lifecycle: Create/Update/Delete, Scale, Rollout, Status, Logs, Exec.
* CLD‑REQ‑081: Declarative spec in YAML/JSON with schema validation.

## 8. Non‑Functional Requirements

* NFR‑A1 Availability: Coordinator 99.9% monthly when anchored on a single VPS; data plane tolerant of coordinator outages for ≥ 15 minutes.
* NFR‑P1 Performance: Scheduler decisions within 200ms P50, 800ms P95 under 5k nodes.
* NFR‑S1 Security: No unauthenticated endpoints; pass CIS‑style baseline checks for Linux hosts.
* NFR‑R1 Reliability: No single point of failure other than the chosen anchor VPS in v0.x; document failover options.
* NFR‑C1 Compliance: Provide audit log and deterministic config for reproducibility.
* NFR‑M1 Maintainability: Go 1.24+, static binaries, minimal external deps.
* NFR‑U1 Usability: First app deployed within 10 minutes following the quickstart.

## 9. Data Model (Sketch)

* **Node:** id, region, zone, capabilities, capacity, status, reliabilityScore, lastSeen.
* **Fragment:** id, nodeId, cpu, memory, storage, bandwidth.
* **Workload:** id, image, resources, replicas, constraints, rollout, status.
* **Endpoint:** serviceName, addresses, weights, health.
* **Object:** bucket, key, replicas[], version, checksum.

## 10. Scheduler Details

* Inputs: WorkloadSpec, VRN inventory, live health, topology.
* Filters: resource fit, policy, taints/tolerations, zone rules.
* Score: locality (RTT percentile), reliability (node uptime, battery/mains), cost (operator weights), utilization (balance), network penalty (cross‑region).
* Actuation: allocate fragments, program Agent, update registry; observe readiness gates before traffic.

## 11. Failure Scenarios

* F1 Node sudden loss → replicas rescheduled; storage re‑replicates to maintain R.
* F2 Coordinator loss → agents continue serving; membership frozen; upon recovery, reconcile.
* F3 Network partition → Zones operate independently; cross‑zone traffic paused; reconcile after heal.
* F4 Image registry unavailable → use cache; degrade new deployments.

## 12. Security Architecture

* Bootstrap: one‑time join token signed by the Coordinator; rotated to node cert.
* mTLS: per‑node and per‑workload identities; SPIFFE‑like naming.
* Policy: OPA‑style policy engine embedded in Coordinator.
* Secrets: envelope encryption; deliver over mTLS with audience and TTL.

## 13. Networking Architecture

* Overlay: peer‑to‑peer where possible; relay via anchor when NAT‑blocked.
* Service Discovery: Coordinator maintains catalog; Agents cache with TTL and watch updates.
* Ingress: pluggable L4 with weighted, locality‑aware routing.

## 14. Storage Architecture

* Object Store: content‑addressed chunks; background repair; anti‑entropy gossip.
* Consistency: strong for metadata, eventual for data; read‑repair on access.
* Quotas and classes: hot vs cold placement; IOPS classes for fragments.

## 15. Management Interfaces

* CLI: `cloudlessctl` in Go; JSON/YAML I/O; kube‑like UX for familiarity.
* API: gRPC + HTTP/JSON gateway; unary RPCs with idempotency keys.
* UI (optional later): read‑only dashboard first; control follows.

## 16. Deployments and Topologies

* **Single Anchor VPS** (2 vCPU, 4GiB RAM, 20GiB disk) runs Coordinator.
* Edge nodes enroll from homes, offices, or DCs.
* Regions defined by operator; scheduler honors affinity.

## 17. MVP Scope (v0.1)

1. Coordinator + RAFT metadata on the anchor.
2. Agent on Linux x86_64/ARM64 with container runtime interface.
3. Secure membership and heartbeats.
4. Basic bin‑pack scheduling and replica management.
5. Encrypted overlay networking and service registry.
6. Simple replicated object store (R=2) without erasure coding.
7. CLI and minimal gRPC API; basic metrics.

## 18. Phase Plan

* **P0: Bootstrap (2–3 weeks)**: Repo, CI, lint, Go mod, protobufs, scaffold Agent/Coordinator, mTLS.
* **P1: Membership (2–3 weeks)**: Heartbeats, inventory, RAFT store.
* **P2: Scheduling (3–4 weeks)**: Placement, rollout, health gates, reschedule.
* **P3: Networking (3 weeks)**: Overlay, service discovery, ingress.
* **P4: Storage (4 weeks)**: Replicated object store, repair, quotas.
* **P5: Security & Ops (3 weeks)**: Policy, secrets, metrics, logs, CLI polish.
* **P6: Hardening (ongoing)**: Chaos tests, soak, docs, quickstart.

## 19. Testing Strategy

* Unit tests with race detector and fuzzing on parsers.
* Integration harness that spins ephemeral clusters.
* Chaos suite: random node churn, bandwidth caps, packet loss, clock skew.
* Performance tests: scheduler latency, control‑plane scalability, overlay throughput.
* Data durability tests: replica loss, checksum mismatch, repair time.

## 20. Risks and Mitigations

* R1 High churn reduces availability → Over‑provision replicas; prefer reliable nodes using reliabilityScore.
* R2 NAT traversal fails → Relay via anchor; document port requirements.
* R3 Data loss on small clusters → Enforce R≥2; warn on unsafe ops; periodic backups of metadata.
* R4 Security drift on heterogeneous hosts → Baseline checks; deny non‑compliant nodes.
* R5 Operator complexity → Opinionated defaults; one‑command install; clear runbooks.

## 21. Open Questions

* OQ1 Container runtime: containerd vs nerdctl vs embedded.
* OQ2 Overlay: QUIC streams vs WireGuard; portability constraints.
* OQ3 Node attestation: TPM/TEE support roadmap.
* OQ4 Energy‑aware scheduling for battery devices.
* OQ5 Windows/macOS Agent timelines.

## 22. Compliance and Governance (Future)

* Signing of images and manifests.
* Supply‑chain attestations (SLSA‑style).
* Per‑tenant namespaces and quotas.

## 23. Implementation Notes (Go)

* Go 1.24+. Modules: `/cmd/agent`, `/cmd/coordinator`, `/pkg/api` (protobuf/gRPC), `/pkg/scheduler`, `/pkg/overlay`, `/pkg/storage`, `/pkg/policy`, `/pkg/raft`, `/pkg/observability`.
* Build: static binaries, CGO disabled when possible.
* Cross‑compile for linux/amd64 and linux/arm64.

---

This document defines the initial scope. Use the IDs to drive issues and tests, and map P0–P6 to milestones.
