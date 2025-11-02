# Cloudless High Availability (HA) Cluster Setup

This document describes the 3-coordinator RAFT cluster configuration for high availability testing.

## Architecture

The HA cluster consists of:
- **3 Coordinators**: RAFT consensus cluster (1 leader + 2 followers)
- **3 Agents**: Distributed compute nodes
- **Observability Stack**: Prometheus, Grafana, Jaeger, Loki

### Network Topology

```
Coordinators (RAFT Cluster)
├── coordinator-1 (172.28.0.10) - Bootstrap node
│   ├── gRPC API: localhost:8080
│   ├── HTTP API: localhost:8081
│   ├── Metrics: localhost:9090
│   └── RAFT: localhost:3000
├── coordinator-2 (172.28.0.11)
│   ├── gRPC API: localhost:8082
│   ├── HTTP API: localhost:8083
│   ├── Metrics: localhost:9091
│   └── RAFT: localhost:3001
└── coordinator-3 (172.28.0.12)
    ├── gRPC API: localhost:8084
    ├── HTTP API: localhost:8085
    ├── Metrics: localhost:9092
    └── RAFT: localhost:3002

Agents
├── agent-1 (172.28.0.21) - zone-a, 2 cores, 2GB RAM
├── agent-2 (172.28.0.22) - zone-b, 4 cores, 4GB RAM
└── agent-3 (172.28.0.23) - zone-a, 1 core, 1GB RAM (edge)

Observability
├── Prometheus: localhost:9093
├── Grafana: localhost:3001 (admin/admin)
├── Jaeger UI: localhost:16686
└── Loki: localhost:3100
```

## Quick Start

### Option 1: Automated Testing (Recommended)

Run the comprehensive test script:

```bash
cd /Users/osamamuhammed/Cloudless/deployments/docker
./test-ha-cluster.sh
```

This script will:
1. Stop any existing clusters
2. Deploy the HA cluster
3. Verify RAFT cluster formation
4. Test leader election
5. Test leader failover
6. Verify agent enrollment
7. Generate comprehensive test report

### Option 2: Manual Deployment

```bash
cd /Users/osamamuhammed/Cloudless/deployments/docker

# Stop existing cluster
docker-compose -f docker-compose.yml down -v

# Start HA cluster
docker-compose -f docker-compose-ha.yml up -d

# Wait for services to stabilize
sleep 60

# Check all containers are running
docker-compose -f docker-compose-ha.yml ps
```

## RAFT Cluster Formation

### Current Implementation Status

The current implementation has a **bootstrap-only** RAFT configuration. Here's what this means:

**Working:**
- ✅ Coordinator-1 bootstraps as a single-node RAFT cluster
- ✅ Coordinator-1 becomes the leader
- ✅ Basic RAFT consensus works for coordinator-1

**Not Implemented Yet:**
- ❌ Coordinator-2 and Coordinator-3 automatic join
- ❌ gRPC/HTTP endpoint for RAFT cluster join
- ❌ Service discovery for follower nodes

### Why Only One Node Works

The RAFT store code (pkg/raft/store.go) includes:
1. `Join(nodeID, addr)` method - for adding nodes to cluster
2. `JoinAddr` config field - for specifying bootstrap node

However:
- There's no exposed API endpoint for calling `Join()`
- The auto-join logic (lines 88-102) only logs a warning
- Coordinators 2 and 3 will bootstrap their own single-node clusters

**Result**: You'll have 3 independent single-node RAFT clusters instead of 1 three-node cluster.

### Manual Cluster Formation (Workaround)

To form a proper 3-node RAFT cluster, you would need to:

1. **Add HTTP/gRPC endpoint** for RAFT join:
   ```go
   // In pkg/api/cloudless.proto
   service CoordinatorService {
       rpc JoinRaftCluster(JoinRaftRequest) returns (JoinRaftResponse);
   }

   // In pkg/coordinator/coordinator.go
   func (c *Coordinator) JoinRaftCluster(ctx context.Context, req *api.JoinRaftRequest) (*api.JoinRaftResponse, error) {
       return c.raftStore.Join(req.NodeId, req.RaftAddr)
   }
   ```

2. **Implement auto-join on startup**:
   ```go
   // In pkg/raft/store.go (NewStore)
   if config.JoinAddr != "" && !config.Bootstrap {
       // Connect to leader and call JoinRaftCluster RPC
       conn, _ := grpc.Dial(config.JoinAddr)
       client := api.NewCoordinatorServiceClient(conn)
       client.JoinRaftCluster(ctx, &api.JoinRaftRequest{
           NodeId: config.RaftID,
           RaftAddr: config.RaftBind,
       })
   }
   ```

3. **Update docker-compose-ha.yml**:
   ```yaml
   coordinator-2:
       environment:
           - CLOUDLESS_RAFT_JOIN_ADDR=coordinator-1:8080
   ```

### Current Behavior Verification

Check the RAFT cluster state:

```bash
# Check leader election (should show 3 leaders - one per single-node cluster)
curl -s http://localhost:9090/metrics | grep cloudless_coordinator_is_leader
curl -s http://localhost:9091/metrics | grep cloudless_coordinator_is_leader
curl -s http://localhost:9092/metrics | grep cloudless_coordinator_is_leader

# Check RAFT logs
docker-compose -f docker-compose-ha.yml logs coordinator-1 | grep -i "raft\|leader"
docker-compose -f docker-compose-ha.yml logs coordinator-2 | grep -i "raft\|leader"
docker-compose -f docker-compose-ha.yml logs coordinator-3 | grep -i "raft\|leader"
```

Expected output:
- Each coordinator will show `cloudless_coordinator_is_leader 1`
- Each coordinator will log "Bootstrapped Raft cluster" (3 separate clusters)

## Testing Leader Failover

Even though we have 3 separate RAFT clusters, you can test leader behavior:

```bash
# Stop coordinator-1
docker-compose -f docker-compose-ha.yml stop coordinator-1

# Check coordinator-1 is no longer leader (metrics unavailable)
curl -s http://localhost:9090/metrics | grep cloudless_coordinator_is_leader

# Restart coordinator-1
docker-compose -f docker-compose-ha.yml start coordinator-1

# Verify it becomes leader again (single-node cluster)
sleep 10
curl -s http://localhost:9090/metrics | grep cloudless_coordinator_is_leader
```

## Agent Enrollment

Agents can connect to any coordinator:
- agent-1 → coordinator-1:8080
- agent-2 → coordinator-2:8080
- agent-3 → coordinator-3:8080

Since each coordinator has its own RAFT cluster, agents enrolled on different coordinators won't see each other's workloads.

**For production HA**, agents should:
1. Use a load balancer or service mesh
2. Implement client-side load balancing across all coordinators
3. Retry on different coordinators if one fails

## Monitoring

### Prometheus Metrics

Key metrics for RAFT cluster health:

```bash
# Leader status (1 = leader, 0 = follower)
cloudless_coordinator_is_leader

# RAFT state (0=Follower, 1=Candidate, 2=Leader, 3=Shutdown)
raft_state

# Applied index (should be consistent across cluster)
raft_applied_index

# Commit index (should advance over time)
raft_commit_index
```

### Grafana Dashboards

Access Grafana at http://localhost:3001 (admin/admin)

Pre-configured dashboards:
- RAFT Consensus Health
- Coordinator Metrics
- Agent Metrics

### Logs

View logs for all services:
```bash
docker-compose -f docker-compose-ha.yml logs -f
```

View specific service:
```bash
docker-compose -f docker-compose-ha.yml logs -f coordinator-1
```

## Cleanup

Stop and remove all containers:
```bash
docker-compose -f docker-compose-ha.yml down -v
```

## Implementation Roadmap for Full HA

To achieve proper 3-node RAFT consensus, implement:

1. **Phase 1**: Add RaftJoin gRPC endpoint (CLD-REQ-012)
   - [ ] Define JoinRaftCluster RPC in cloudless.proto
   - [ ] Implement gRPC handler in coordinator
   - [ ] Add HTTP endpoint for HTTP-based join

2. **Phase 2**: Implement auto-join on startup
   - [ ] Update pkg/raft/store.go NewStore()
   - [ ] Add CLOUDLESS_RAFT_JOIN_ADDR env var support
   - [ ] Implement retry logic for join failures

3. **Phase 3**: Service discovery
   - [ ] Implement peer discovery (gossip or DNS)
   - [ ] Add dynamic cluster reconfiguration
   - [ ] Support adding/removing nodes dynamically

4. **Phase 4**: Client-side failover
   - [ ] Update agent to connect to multiple coordinators
   - [ ] Implement automatic failover on connection loss
   - [ ] Add leader forwarding for writes

5. **Phase 5**: Operational tooling
   - [ ] CLI commands for cluster management
   - [ ] Health checks for RAFT cluster
   - [ ] Alerting rules for split-brain scenarios

## Known Limitations

Current docker-compose-ha.yml limitations:

1. **No true RAFT consensus** - 3 separate single-node clusters
2. **No automatic failover** - agents connected to failed coordinator lose connectivity
3. **No state replication** - workloads on coordinator-1 not visible to coordinator-2/3
4. **Manual join required** - no automated cluster formation

These limitations will be addressed in future development iterations.

## References

- RAFT Consensus: https://raft.github.io/
- HashiCorp RAFT Library: https://github.com/hashicorp/raft
- Cloudless RAFT Implementation: pkg/raft/
- Design Document: CLD-REQ-012 (RAFT Consensus)

## Support

For issues or questions:
1. Check logs: `/tmp/cloudless-ha-test-logs.txt`
2. Review test report: `/tmp/cloudless-ha-test-results.txt`
3. File an issue on GitHub with logs attached
