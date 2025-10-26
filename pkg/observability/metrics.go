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
