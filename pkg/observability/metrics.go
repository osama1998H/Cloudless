package observability

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metric registries for different subsystems

// Scheduler Metrics
var (
	// Scheduling decision metrics
	SchedulingDecisionsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_scheduling_decisions_total",
			Help: "Total number of scheduling decisions made",
		},
		[]string{"result"}, // success, failure, no_nodes
	)

	SchedulingDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_scheduling_duration_seconds",
			Help:    "Duration of scheduling decisions in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.2, 0.5, 1.0, 2.0, 5.0},
		},
		[]string{"phase"}, // filter, score, bind
	)

	PlacementScoreDistribution = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_placement_score",
			Help:    "Distribution of node placement scores",
			Buckets: []float64{0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100},
		},
		[]string{"node_id"},
	)

	ReschedulingOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_rescheduling_operations_total",
			Help: "Total number of rescheduling operations",
		},
		[]string{"reason"}, // node_failure, rebalance, constraint_violation
	)

	// CLD-REQ-031: Failed replica rescheduling latency metrics
	RescheduleLatencySeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cloudless_scheduler_reschedule_latency_seconds",
			Help:    "Time from replica failure detection to successful rescheduling (CLD-REQ-031: P50 < 3s, P95 < 10s)",
			Buckets: []float64{0.5, 1.0, 2.0, 3.0, 5.0, 10.0, 15.0, 30.0, 60.0},
		},
	)

	RescheduleFailuresTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_scheduler_reschedule_failures_total",
			Help: "Total number of failed rescheduling attempts",
		},
		[]string{"reason"}, // insufficient_capacity, constraint_violation, scheduler_error
	)

	SchedulingQueueDepth = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_scheduling_queue_depth",
			Help: "Current depth of the scheduling queue",
		},
	)
)

// Storage Metrics
var (
	// Chunk metrics
	StorageChunksTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_storage_chunks_total",
			Help: "Total number of chunks in storage",
		},
		[]string{"node_id"},
	)

	StorageUsedBytes = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_storage_used_bytes",
			Help: "Storage space used in bytes",
		},
		[]string{"node_id", "storage_class"},
	)

	StorageCapacityBytes = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_storage_capacity_bytes",
			Help: "Total storage capacity in bytes",
		},
		[]string{"node_id", "storage_class"},
	)

	StorageOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_storage_operations_total",
			Help: "Total number of storage operations",
		},
		[]string{"operation", "result"}, // operation: read/write/delete, result: success/failure
	)

	StorageOperationDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_storage_operation_duration_seconds",
			Help:    "Duration of storage operations in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0},
		},
		[]string{"operation"}, // read, write, delete, replicate
	)

	ReplicationLagSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_storage_replication_lag_seconds",
			Help: "Replication lag in seconds",
		},
		[]string{"source_node", "target_node"},
	)

	RepairOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_storage_repair_operations_total",
			Help: "Total number of storage repair operations",
		},
		[]string{"type", "result"}, // type: read_repair/scrub/rebalance, result: success/failure
	)

	ObjectsTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_storage_objects_total",
			Help: "Total number of objects in storage",
		},
		[]string{"bucket", "storage_class"},
	)
)

// Node Metrics
var (
	// Node state metrics
	NodesByState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_nodes_by_state",
			Help: "Number of nodes in each state",
		},
		[]string{"state"}, // ready, draining, cordoned, offline, failed
	)

	NodeReliabilityScore = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_node_reliability_score",
			Help: "Reliability score of nodes (0-1)",
		},
		[]string{"node_id", "region", "zone"},
	)

	NodeResourceCapacity = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_node_resource_capacity",
			Help: "Total resource capacity of nodes",
		},
		[]string{"node_id", "resource"}, // resource: cpu_millicores, memory_bytes, storage_bytes, bandwidth_bps
	)

	NodeResourceUsage = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_node_resource_usage",
			Help: "Current resource usage of nodes",
		},
		[]string{"node_id", "resource"},
	)

	NodeFragmentUtilization = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_node_fragment_utilization",
			Help: "Fragment utilization rate (0-1)",
		},
		[]string{"node_id"},
	)

	NodeHeartbeatLatencySeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_node_heartbeat_latency_seconds",
			Help:    "Latency of node heartbeats in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
		},
		[]string{"node_id"},
	)

	NodeEnrollmentsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_node_enrollments_total",
			Help: "Total number of node enrollment attempts",
		},
		[]string{"result"}, // success, failure
	)

	// CLD-REQ-002: Membership convergence timing metrics
	MembershipJoinConvergenceSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cloudless_membership_join_convergence_seconds",
			Help:    "Time for a node join to converge cluster-wide (CLD-REQ-002: P50 < 5s, P95 < 15s)",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 15.0, 30.0, 60.0},
		},
	)

	MembershipLeaveConvergenceSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cloudless_membership_leave_convergence_seconds",
			Help:    "Time for a node leave to converge cluster-wide (CLD-REQ-002: P50 < 5s, P95 < 15s)",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 15.0, 30.0, 60.0},
		},
	)

	MembershipNodeRecoverySeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cloudless_membership_node_recovery_seconds",
			Help:    "Time for a node to recover from offline state and rejoin the cluster",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 15.0, 30.0, 60.0, 120.0, 300.0},
		},
	)
)

// Workload Metrics
var (
	// Workload state metrics
	WorkloadsByPhase = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_workloads_by_phase",
			Help: "Number of workloads in each phase",
		},
		[]string{"phase", "namespace"}, // pending, scheduling, running, updating, failed, succeeded, terminating
	)

	WorkloadReplicasDesired = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_workload_replicas_desired",
			Help: "Desired number of replicas for workloads",
		},
		[]string{"workload_id", "namespace"},
	)

	WorkloadReplicasReady = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_workload_replicas_ready",
			Help: "Number of ready replicas for workloads",
		},
		[]string{"workload_id", "namespace"},
	)

	WorkloadReplicaRestarts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_workload_replica_restarts_total",
			Help: "Total number of replica restarts",
		},
		[]string{"workload_id", "namespace", "reason"},
	)

	WorkloadHealthCheckFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_workload_health_check_failures_total",
			Help: "Total number of workload health check failures",
		},
		[]string{"workload_id", "namespace", "probe_type"}, // liveness, readiness
	)

	WorkloadCreationDurationSeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cloudless_workload_creation_duration_seconds",
			Help:    "Duration of workload creation from submission to running state",
			Buckets: []float64{1, 5, 10, 30, 60, 120, 300},
		},
	)

	WorkloadScaleOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_workload_scale_operations_total",
			Help: "Total number of workload scale operations",
		},
		[]string{"direction"}, // up, down
	)
)

// Container Runtime Metrics
var (
	ContainerOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_container_operations_total",
			Help: "Total number of container operations",
		},
		[]string{"operation", "result"}, // operation: create/start/stop/delete, result: success/failure
	)

	ContainerOperationDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_container_operation_duration_seconds",
			Help:    "Duration of container operations in seconds",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0},
		},
		[]string{"operation"},
	)

	RunningContainersTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_running_containers_total",
			Help: "Total number of running containers",
		},
		[]string{"node_id"},
	)
)

// Overlay Network Metrics
var (
	NetworkConnectionsTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_network_connections_total",
			Help: "Total number of active network connections",
		},
		[]string{"source_node", "target_node"},
	)

	NetworkBytesTransferred = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_network_bytes_transferred_total",
			Help: "Total bytes transferred over the network",
		},
		[]string{"source_node", "target_node", "direction"}, // direction: sent/received
	)

	NetworkLatencySeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_network_latency_seconds",
			Help:    "Network latency between nodes in seconds",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 5.0},
		},
		[]string{"source_node", "target_node"},
	)

	NATTraversalAttempts = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_nat_traversal_attempts_total",
			Help: "Total number of NAT traversal attempts",
		},
		[]string{"method", "result"}, // method: stun/turn/relay, result: success/failure
	)
)

// Coordinator Metrics
var (
	CoordinatorLeaderElections = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_coordinator_leader_elections_total",
			Help: "Total number of coordinator leader elections",
		},
		[]string{"result"}, // success, failure
	)

	CoordinatorIsLeader = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_coordinator_is_leader",
			Help: "Whether this coordinator instance is the leader (1=yes, 0=no)",
		},
	)

	CoordinatorRaftLogEntries = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_coordinator_raft_log_entries",
			Help: "Number of entries in the RAFT log",
		},
	)

	CoordinatorRaftAppliedIndex = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_coordinator_raft_applied_index",
			Help: "Last applied RAFT log index",
		},
	)
)

// RAFT Consensus Metrics (CLD-REQ-051: Strong consistency via RAFT)
var (
	// RAFT state tracking
	RaftNodeState = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_raft_node_state",
			Help: "RAFT node state (0=follower, 1=candidate, 2=leader)",
		},
		[]string{"node_id"},
	)

	RaftCommitIndex = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_raft_commit_index",
			Help: "Last committed RAFT log index",
		},
	)

	RaftLastLogIndex = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_raft_last_log_index",
			Help: "Index of the last RAFT log entry",
		},
	)

	RaftLastLogTerm = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_raft_last_log_term",
			Help: "Term of the last RAFT log entry",
		},
	)

	RaftApplyLatencySeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cloudless_raft_apply_latency_seconds",
			Help:    "Latency of applying RAFT log entries to FSM",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0},
		},
	)

	RaftCommitLatencySeconds = promauto.NewHistogram(
		prometheus.HistogramOpts{
			Name:    "cloudless_raft_commit_latency_seconds",
			Help:    "Latency from log append to commit (consensus latency)",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 2.0, 5.0},
		},
	)

	// Leader-specific metrics
	RaftLeaderLastContact = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_raft_leader_last_contact_seconds",
			Help: "Seconds since leader last heard from each follower",
		},
		[]string{"follower_id"},
	)

	RaftFollowerReplicationLag = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_raft_follower_replication_lag_entries",
			Help: "Number of log entries by which a follower is behind the leader",
		},
		[]string{"follower_id"},
	)

	// Snapshot metrics
	RaftSnapshotCreationTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_raft_snapshot_creation_total",
			Help: "Total number of RAFT snapshot creation operations",
		},
		[]string{"result"}, // success, failure
	)

	RaftSnapshotRestoreTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_raft_snapshot_restore_total",
			Help: "Total number of RAFT snapshot restore operations",
		},
		[]string{"result"}, // success, failure
	)

	RaftSnapshotDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_raft_snapshot_duration_seconds",
			Help:    "Duration of RAFT snapshot operations",
			Buckets: []float64{0.1, 0.5, 1.0, 2.0, 5.0, 10.0, 30.0, 60.0},
		},
		[]string{"operation"}, // create, restore
	)

	RaftSnapshotSizeBytes = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_raft_snapshot_size_bytes",
			Help: "Size of the last RAFT snapshot in bytes",
		},
	)

	// Peer connectivity
	RaftPeerConnections = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_raft_peer_connections",
			Help: "Number of active connections to RAFT peers (1=connected, 0=disconnected)",
		},
		[]string{"peer_id"},
	)

	RaftHeartbeatFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_raft_heartbeat_failures_total",
			Help: "Total number of failed heartbeats to RAFT peers",
		},
		[]string{"peer_id"},
	)
)

// Metadata Store Metrics (CLD-REQ-051: RAFT-backed metadata)
var (
	MetadataOperationsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_metadata_operations_total",
			Help: "Total number of metadata operations (CLD-REQ-051: RAFT-backed)",
		},
		[]string{"operation", "result"}, // operation: create_bucket/delete_bucket/put_object/delete_object, result: success/failure
	)

	MetadataOperationDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_metadata_operation_duration_seconds",
			Help:    "Duration of metadata operations including RAFT consensus (CLD-REQ-051)",
			Buckets: []float64{0.001, 0.005, 0.01, 0.05, 0.1, 0.5, 1.0, 2.0, 5.0},
		},
		[]string{"operation"},
	)

	MetadataRaftApplyErrors = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_metadata_raft_apply_errors_total",
			Help: "Total number of errors applying metadata operations to RAFT",
		},
		[]string{"operation", "error_type"}, // error_type: not_leader/timeout/apply_failed
	)

	MetadataBucketsTotal = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_metadata_buckets_total",
			Help: "Total number of buckets in metadata store",
		},
	)

	MetadataObjectsPerBucket = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_metadata_objects_per_bucket",
			Help: "Number of objects per bucket in metadata store",
		},
		[]string{"bucket"},
	)

	MetadataCacheHitRate = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_metadata_cache_hit_rate",
			Help: "Cache hit rate for metadata lookups (0-1)",
		},
		[]string{"operation"}, // get_bucket, get_object
	)
)

// Service Registry Metrics
var (
	// Service operations
	RegistryServicesTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_registry_services_total",
			Help: "Total number of registered services",
		},
		[]string{"namespace"},
	)

	RegistryServiceRegistrations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_registry_service_registrations_total",
			Help: "Total number of service registration operations",
		},
		[]string{"namespace", "result"}, // result: success, failure
	)

	RegistryServiceDeregistrations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_registry_service_deregistrations_total",
			Help: "Total number of service deregistration operations",
		},
		[]string{"namespace"},
	)

	// Endpoint operations
	RegistryEndpointsTotal = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_registry_endpoints_total",
			Help: "Total number of registered endpoints",
		},
		[]string{"service", "namespace", "health_status"}, // health_status: healthy, unhealthy, unknown
	)

	RegistryEndpointRegistrations = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_registry_endpoint_registrations_total",
			Help: "Total number of endpoint registration operations",
		},
		[]string{"service", "namespace"},
	)

	RegistryEndpointHealthTransitions = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "cloudless_registry_endpoint_health_transitions_total",
			Help: "Total number of endpoint health status transitions",
		},
		[]string{"from_status", "to_status"}, // healthy/unhealthy/unknown
	)

	// Virtual IP pool metrics
	RegistryVIPPoolSize = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_registry_vip_pool_size",
			Help: "Total number of IPs in the virtual IP pool",
		},
	)

	RegistryVIPPoolAllocated = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_registry_vip_pool_allocated",
			Help: "Number of currently allocated virtual IPs",
		},
	)

	RegistryVIPPoolUtilization = promauto.NewGauge(
		prometheus.GaugeOpts{
			Name: "cloudless_registry_vip_pool_utilization_ratio",
			Help: "Virtual IP pool utilization ratio (allocated/total)",
		},
	)

	// Performance metrics
	RegistryOperationDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "cloudless_registry_operation_duration_seconds",
			Help:    "Duration of registry operations in seconds",
			Buckets: []float64{0.0001, 0.0005, 0.001, 0.005, 0.01, 0.05, 0.1},
		},
		[]string{"operation"}, // register_service, deregister_service, register_endpoint, etc.
	)
)

// General System Metrics
var (
	SystemInfo = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_system_info",
			Help: "System information (version, build time, etc.)",
		},
		[]string{"version", "build_time", "git_commit"},
	)

	UptimeSeconds = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "cloudless_uptime_seconds",
			Help: "Uptime of the component in seconds",
		},
		[]string{"component"}, // coordinator, agent
	)
)
