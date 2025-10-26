package coordinator

import (
	"context"
	"fmt"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/coordinator/membership"
	"github.com/cloudless/cloudless/pkg/scheduler"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// EnrollNode handles node enrollment requests
func (c *Coordinator) EnrollNode(ctx context.Context, req *api.EnrollNodeRequest) (*api.EnrollNodeResponse, error) {
	c.logger.Info("Received enrollment request",
		zap.String("node_name", req.NodeName),
		zap.String("region", req.Region),
		zap.String("zone", req.Zone),
	)

	// Only leader can enroll nodes
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	// Validate enrollment token
	token, err := c.tokenManager.ValidateToken(req.Token)
	if err != nil {
		c.logger.Warn("Invalid enrollment token",
			zap.String("node_name", req.NodeName),
			zap.Error(err),
		)
		return nil, fmt.Errorf("invalid enrollment token: %w", err)
	}

	c.logger.Debug("Token validated", zap.Any("token", token))

	// Use membership manager to enroll the node directly with the API request
	response, err := c.membershipMgr.EnrollNode(ctx, req)
	if err != nil {
		c.logger.Error("Failed to enroll node",
			zap.String("node_name", req.NodeName),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to enroll node: %w", err)
	}

	c.logger.Info("Node enrolled successfully",
		zap.String("node_id", response.NodeId),
		zap.String("node_name", req.NodeName),
	)

	return response, nil
}

// Heartbeat processes heartbeat messages from nodes
func (c *Coordinator) Heartbeat(ctx context.Context, req *api.HeartbeatRequest) (*api.HeartbeatResponse, error) {
	// Process heartbeat through membership manager
	resp, err := c.membershipMgr.ProcessHeartbeat(ctx, req)
	if err != nil {
		c.logger.Debug("Heartbeat processing failed",
			zap.String("node_id", req.NodeId),
			zap.Error(err),
		)
		return nil, err
	}

	// CLD-REQ-032: Process container health data
	if len(req.ContainerHealth) > 0 {
		c.logger.Debug("Processing container health data",
			zap.String("node_id", req.NodeId),
			zap.Int("container_count", len(req.ContainerHealth)),
		)

		for _, health := range req.ContainerHealth {
			// Update replica health in state manager
			if err := c.workloadStateMgr.UpdateReplicaHealth(
				ctx,
				health.ContainerId,
				health.LivenessHealthy,
				health.ReadinessHealthy,
				health.LivenessConsecutiveFailures,
				health.ReadinessConsecutiveFailures,
			); err != nil {
				c.logger.Warn("Failed to update replica health",
					zap.String("container_id", health.ContainerId),
					zap.Error(err),
				)
				// Continue processing other containers
			}
		}
	}

	return resp, nil
}

// GetNode retrieves information about a specific node
func (c *Coordinator) GetNode(ctx context.Context, req *api.GetNodeRequest) (*api.Node, error) {
	node, err := c.membershipMgr.GetNode(req.NodeId)
	if err != nil {
		return nil, fmt.Errorf("node not found: %s", req.NodeId)
	}

	// Convert membership.NodeInfo to api.Node
	return convertNodeInfoToProto(node), nil
}

// ListNodes lists all nodes in the cluster
func (c *Coordinator) ListNodes(ctx context.Context, req *api.ListNodesRequest) (*api.ListNodesResponse, error) {
	// Build filter map if state is provided
	filters := make(map[string]string)

	nodes, err := c.membershipMgr.ListNodes(filters)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}

	apiNodes := make([]*api.Node, 0, len(nodes))
	for _, node := range nodes {
		apiNodes = append(apiNodes, convertNodeInfoToProto(node))
	}

	return &api.ListNodesResponse{
		Nodes: apiNodes,
	}, nil
}

// convertNodeInfoToProto converts membership.NodeInfo to api.Node
func convertNodeInfoToProto(node *membership.NodeInfo) *api.Node {
	return &api.Node{
		Id:     node.ID,
		Name:   node.Name,
		Region: node.Region,
		Zone:   node.Zone,
		Capabilities: &api.NodeCapabilities{
			ContainerRuntimes: node.Capabilities.ContainerRuntimes,
			SupportsGpu:       node.Capabilities.SupportsGPU,
			SupportsArm:       node.Capabilities.SupportsARM,
			SupportsX86:       node.Capabilities.SupportsX86,
			NetworkFeatures:   node.Capabilities.NetworkFeatures,
			StorageClasses:    node.Capabilities.StorageClasses,
		},
		Capacity: &api.ResourceCapacity{
			CpuMillicores: node.Capacity.CPUMillicores,
			MemoryBytes:   node.Capacity.MemoryBytes,
			StorageBytes:  node.Capacity.StorageBytes,
			BandwidthBps:  node.Capacity.BandwidthBPS,
			GpuCount:      node.Capacity.GPUCount,
		},
		Usage: &api.ResourceUsage{
			CpuMillicores: node.Usage.CPUMillicores,
			MemoryBytes:   node.Usage.MemoryBytes,
			StorageBytes:  node.Usage.StorageBytes,
			BandwidthBps:  node.Usage.BandwidthBPS,
			GpuCount:      node.Usage.GPUCount,
		},
		Status: &api.NodeStatus{
			Phase: convertNodeStateToPhase(node.State),
		},
		ReliabilityScore: node.ReliabilityScore,
		LastSeen:         nil, // TODO: add timestamp if available
	}
}

// convertNodeStateToPhase converts membership state to api phase
func convertNodeStateToPhase(state string) api.NodeStatus_Phase {
	switch state {
	case "enrolling":
		return api.NodeStatus_ENROLLING
	case "ready":
		return api.NodeStatus_READY
	case "draining":
		return api.NodeStatus_DRAINING
	case "cordoned":
		return api.NodeStatus_CORDONED
	case "offline":
		return api.NodeStatus_OFFLINE
	case "failed":
		return api.NodeStatus_FAILED
	default:
		return api.NodeStatus_UNKNOWN
	}
}

// convertRestartPolicyToString converts proto enum to string
func convertRestartPolicyToString(policy api.RestartPolicy_Policy) string {
	switch policy {
	case api.RestartPolicy_ALWAYS:
		return "Always"
	case api.RestartPolicy_ON_FAILURE:
		return "OnFailure"
	case api.RestartPolicy_NEVER:
		return "Never"
	default:
		return "Always"
	}
}

// convertRolloutStrategyToString converts proto enum to string
func convertRolloutStrategyToString(strategy api.RolloutStrategy_Strategy) string {
	switch strategy {
	case api.RolloutStrategy_ROLLING_UPDATE:
		return "RollingUpdate"
	case api.RolloutStrategy_RECREATE:
		return "Recreate"
	case api.RolloutStrategy_BLUE_GREEN:
		return "BlueGreen"
	default:
		return "RollingUpdate"
	}
}

// convertWorkloadStatusToPhase converts WorkloadStatus string to proto enum
func convertWorkloadStatusToPhase(status WorkloadStatus) api.WorkloadStatus_Phase {
	switch status {
	case WorkloadStatusPending:
		return api.WorkloadStatus_PENDING
	case WorkloadStatusScheduling:
		return api.WorkloadStatus_SCHEDULING
	case WorkloadStatusRunning:
		return api.WorkloadStatus_RUNNING
	case WorkloadStatusUpdating:
		return api.WorkloadStatus_UPDATING
	case WorkloadStatusFailed:
		return api.WorkloadStatus_FAILED
	case WorkloadStatusScaling:
		return api.WorkloadStatus_RUNNING // Scaling is a sub-state of running
	default:
		return api.WorkloadStatus_UNKNOWN
	}
}

// DrainNode marks a node for draining
func (c *Coordinator) DrainNode(ctx context.Context, req *api.DrainNodeRequest) (*emptypb.Empty, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	// DrainNode requires graceful bool and timeout duration
	graceful := true
	timeout := 5 * time.Minute
	if err := c.membershipMgr.DrainNode(req.NodeId, graceful, timeout); err != nil {
		c.logger.Error("Failed to drain node",
			zap.String("node_id", req.NodeId),
			zap.Error(err),
		)
		return nil, err
	}

	c.logger.Info("Node marked for draining",
		zap.String("node_id", req.NodeId),
	)

	return &emptypb.Empty{}, nil
}

// UncordonNode marks a drained node as ready again
func (c *Coordinator) UncordonNode(ctx context.Context, req *api.UncordonNodeRequest) (*emptypb.Empty, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	if err := c.membershipMgr.UncordonNode(req.NodeId); err != nil {
		c.logger.Error("Failed to uncordon node",
			zap.String("node_id", req.NodeId),
			zap.Error(err),
		)
		return nil, err
	}

	c.logger.Info("Node uncordoned",
		zap.String("node_id", req.NodeId),
	)

	return &emptypb.Empty{}, nil
}

// CreateWorkload creates a new workload
func (c *Coordinator) CreateWorkload(ctx context.Context, req *api.CreateWorkloadRequest) (*api.Workload, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	workload := req.GetWorkload()
	if workload == nil {
		return nil, fmt.Errorf("workload spec is required")
	}
	spec := workload.GetSpec()
	if spec == nil {
		return nil, fmt.Errorf("workload spec is required")
	}

	c.logger.Info("Creating workload",
		zap.String("name", workload.Name),
		zap.String("namespace", workload.Namespace),
		zap.Int32("replicas", spec.Replicas),
	)

	// Generate workload ID
	workloadID := workload.Id
	if workloadID == "" {
		workloadID = fmt.Sprintf("%s-%s", workload.Namespace, workload.Name)
	}
	// Persist the generated ID back to the workload
	workload.Id = workloadID

	// Convert API volumes to state manager format
	volumes := make([]VolumeMount, 0, len(spec.Volumes))
	for _, v := range spec.Volumes {
		volumes = append(volumes, VolumeMount{
			Name:      v.Name,
			MountPath: v.MountPath,
			ReadOnly:  v.ReadOnly,
		})
	}

	// Convert API ports to state manager format
	ports := make([]PortMapping, 0, len(spec.Ports))
	for _, p := range spec.Ports {
		ports = append(ports, PortMapping{
			Name:          p.Name,
			ContainerPort: p.ContainerPort,
			Protocol:      p.Protocol,
		})
	}

	// Convert resources
	resources := scheduler.ResourceRequirements{
		Requests: scheduler.ResourceSpec{
			CPUMillicores: int64(spec.Resources.Requests.CpuMillicores),
			MemoryBytes:   spec.Resources.Requests.MemoryBytes,
			StorageBytes:  spec.Resources.Requests.StorageBytes,
		},
		Limits: scheduler.ResourceSpec{
			CPUMillicores: int64(spec.Resources.Limits.CpuMillicores),
			MemoryBytes:   spec.Resources.Limits.MemoryBytes,
			StorageBytes:  spec.Resources.Limits.StorageBytes,
		},
	}

	// Convert placement policy
	var placement scheduler.PlacementPolicy
	if spec.Placement != nil {
		placement = scheduler.PlacementPolicy{
			Regions:      spec.Placement.Regions,
			Zones:        spec.Placement.Zones,
			NodeSelector: spec.Placement.NodeSelector,
		}
	}

	// Convert restart policy
	var restartPolicy scheduler.RestartPolicy
	if spec.RestartPolicy != nil {
		restartPolicy = scheduler.RestartPolicy{
			Policy:     convertRestartPolicyToString(spec.RestartPolicy.Policy),
			MaxRetries: int(spec.RestartPolicy.MaxRetries),
		}
	} else {
		restartPolicy = scheduler.RestartPolicy{
			Policy:     "always",
			MaxRetries: 0,
		}
	}

	// Convert rollout strategy
	var rolloutStrategy scheduler.RolloutStrategy
	if spec.Rollout != nil {
		rolloutStrategy = scheduler.RolloutStrategy{
			Strategy:       convertRolloutStrategyToString(spec.Rollout.Strategy),
			MaxSurge:       int(spec.Rollout.MaxSurge),
			MaxUnavailable: int(spec.Rollout.MaxUnavailable),
		}
	} else {
		rolloutStrategy = scheduler.RolloutStrategy{
			Strategy:       "rolling",
			MaxSurge:       1,
			MaxUnavailable: 0,
		}
	}

	// Create workload state
	workloadState := &WorkloadState{
		ID:              workloadID,
		Name:            workload.Name,
		Namespace:       workload.Namespace,
		Image:           spec.Image,
		Command:         spec.Command,
		Args:            spec.Args,
		Env:             spec.Env,
		Volumes:         volumes,
		Ports:           ports,
		Resources:       resources,
		Placement:       placement,
		Restart:         restartPolicy,
		Rollout:         rolloutStrategy,
		DesiredReplicas: spec.Replicas,
		Status:          WorkloadStatusPending,
		Labels:          workload.Labels,
		Annotations:     workload.Annotations,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	// Policy-based admission control
	if err := c.AdmitWorkload(ctx, workload); err != nil {
		c.logger.Warn("Workload admission denied",
			zap.String("workload", workload.Name),
			zap.String("namespace", workload.Namespace),
			zap.Error(err),
		)
		return nil, err
	}

	// Save workload state to RAFT
	if err := c.workloadStateMgr.SaveWorkload(ctx, workloadState); err != nil {
		c.logger.Error("Failed to save workload state",
			zap.String("workload_id", workloadID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to save workload state: %w", err)
	}

	// Update status to scheduling
	workloadState.Status = WorkloadStatusScheduling
	if err := c.workloadStateMgr.SaveWorkload(ctx, workloadState); err != nil {
		c.logger.Warn("Failed to update workload status",
			zap.String("workload_id", workloadID),
			zap.Error(err),
		)
	}

	// Convert to scheduler workload spec for scheduling
	schedulerSpec := scheduler.WorkloadSpec{
		ID:              workloadID,
		Name:            workload.Name,
		Namespace:       workload.Namespace,
		Replicas:        int(spec.Replicas),
		Resources:       resources,
		PlacementPolicy: placement,
	}

	// Schedule the workload
	decisions, err := c.ScheduleWorkload(ctx, schedulerSpec)
	if err != nil {
		c.logger.Error("Failed to schedule workload",
			zap.String("workload_id", workloadID),
			zap.Error(err),
		)

		// Update state to failed
		workloadState.Status = WorkloadStatusFailed
		c.workloadStateMgr.SaveWorkload(ctx, workloadState)

		return nil, fmt.Errorf("failed to schedule workload: %w", err)
	}

	// Ensure workload ID is set (defensive check)
	if workload.Id == "" {
		workload.Id = workloadID
	}

	// Queue assignments for each node
	for _, decision := range decisions {
		assignment := &api.WorkloadAssignment{
			FragmentId: decision.FragmentID,
			Workload:   workload,
			Action:     "run",
		}
		if err := c.membershipMgr.QueueAssignment(decision.NodeID, assignment); err != nil {
			c.logger.Error("Failed to queue assignment",
				zap.String("node_id", decision.NodeID),
				zap.Error(err),
			)
		}
	}

	// Update workload state with scheduled status
	workloadState.Status = WorkloadStatusRunning
	if err := c.workloadStateMgr.SaveWorkload(ctx, workloadState); err != nil {
		c.logger.Warn("Failed to update workload status",
			zap.String("workload_id", workloadID),
			zap.Error(err),
		)
	}

	c.logger.Info("Workload created and scheduled",
		zap.String("workload_id", workloadID),
		zap.Int("placements", len(decisions)),
	)

	return &api.Workload{
		Id:          workloadID,
		Name:        workload.Name,
		Namespace:   workload.Namespace,
		Spec:        spec,
		Labels:      workload.Labels,
		Annotations: workload.Annotations,
	}, nil
}

// UpdateWorkload updates an existing workload
func (c *Coordinator) UpdateWorkload(ctx context.Context, req *api.UpdateWorkloadRequest) (*api.Workload, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	workload := req.GetWorkload()
	if workload == nil {
		return nil, fmt.Errorf("workload is required")
	}

	workloadID := workload.Id
	if workloadID == "" {
		return nil, fmt.Errorf("workload ID is required")
	}

	c.logger.Info("Updating workload",
		zap.String("workload_id", workloadID),
	)

	// Get existing workload state
	existingState, err := c.workloadStateMgr.GetWorkload(ctx, workloadID)
	if err != nil {
		return nil, fmt.Errorf("failed to get existing workload: %w", err)
	}

	spec := workload.GetSpec()
	if spec != nil {
		// Update status to updating
		existingState.Status = WorkloadStatusUpdating

		// Update spec fields
		if spec.Image != "" {
			existingState.Image = spec.Image
		}
		if len(spec.Command) > 0 {
			existingState.Command = spec.Command
		}
		if len(spec.Args) > 0 {
			existingState.Args = spec.Args
		}
		if len(spec.Env) > 0 {
			existingState.Env = spec.Env
		}

		// Update volumes
		if len(spec.Volumes) > 0 {
			volumes := make([]VolumeMount, 0, len(spec.Volumes))
			for _, v := range spec.Volumes {
				volumes = append(volumes, VolumeMount{
					Name:      v.Name,
					MountPath: v.MountPath,
					ReadOnly:  v.ReadOnly,
				})
			}
			existingState.Volumes = volumes
		}

		// Update ports
		if len(spec.Ports) > 0 {
			ports := make([]PortMapping, 0, len(spec.Ports))
			for _, p := range spec.Ports {
				ports = append(ports, PortMapping{
					Name:          p.Name,
					ContainerPort: p.ContainerPort,
					Protocol:      p.Protocol,
				})
			}
			existingState.Ports = ports
		}

		// Update resources if provided
		if spec.Resources != nil {
			existingState.Resources = scheduler.ResourceRequirements{
				Requests: scheduler.ResourceSpec{
					CPUMillicores: int64(spec.Resources.Requests.CpuMillicores),
					MemoryBytes:   spec.Resources.Requests.MemoryBytes,
					StorageBytes:  spec.Resources.Requests.StorageBytes,
				},
				Limits: scheduler.ResourceSpec{
					CPUMillicores: int64(spec.Resources.Limits.CpuMillicores),
					MemoryBytes:   spec.Resources.Limits.MemoryBytes,
					StorageBytes:  spec.Resources.Limits.StorageBytes,
				},
			}
		}

		// Update desired replicas
		if spec.Replicas > 0 {
			existingState.DesiredReplicas = spec.Replicas
		}
	}

	// Update labels and annotations
	if len(workload.Labels) > 0 {
		existingState.Labels = workload.Labels
	}
	if len(workload.Annotations) > 0 {
		existingState.Annotations = workload.Annotations
	}

	// Policy-based admission control for updated workload
	// Re-validate the workload spec to prevent policy circumvention
	if err := c.AdmitWorkload(ctx, workload); err != nil {
		c.logger.Warn("Workload update admission denied",
			zap.String("workload_id", workloadID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("policy admission failed: %w", err)
	}

	// Save updated workload state
	if err := c.workloadStateMgr.SaveWorkload(ctx, existingState); err != nil {
		c.logger.Error("Failed to save updated workload",
			zap.String("workload_id", workloadID),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to save updated workload: %w", err)
	}

	// Queue update commands to affected nodes
	for _, replica := range existingState.Replicas {
		if replica.Status == ReplicaStatusRunning {
			assignment := &api.WorkloadAssignment{
				FragmentId: replica.FragmentID,
				Workload:   workload,
				Action:     "update",
			}
			if err := c.membershipMgr.QueueAssignment(replica.NodeID, assignment); err != nil {
				c.logger.Error("Failed to queue update assignment",
					zap.String("node_id", replica.NodeID),
					zap.String("replica_id", replica.ID),
					zap.Error(err),
				)
			}
		}
	}

	c.logger.Info("Workload updated",
		zap.String("workload_id", workloadID),
	)

	return &api.Workload{
		Id:          workloadID,
		Name:        existingState.Name,
		Namespace:   existingState.Namespace,
		Spec:        spec,
		Labels:      existingState.Labels,
		Annotations: existingState.Annotations,
	}, nil
}

// DeleteWorkload deletes a workload
func (c *Coordinator) DeleteWorkload(ctx context.Context, req *api.DeleteWorkloadRequest) (*emptypb.Empty, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	c.logger.Info("Deleting workload",
		zap.String("workload_id", req.WorkloadId),
	)

	// Get workload state
	workloadState, err := c.workloadStateMgr.GetWorkload(ctx, req.WorkloadId)
	if err != nil {
		return nil, fmt.Errorf("failed to get workload: %w", err)
	}

	// Mark workload as deleting (soft delete)
	if err := c.workloadStateMgr.DeleteWorkload(ctx, req.WorkloadId); err != nil {
		c.logger.Error("Failed to mark workload as deleted",
			zap.String("workload_id", req.WorkloadId),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to mark workload as deleted: %w", err)
	}

	// Send stop commands to all nodes running replicas
	for _, replica := range workloadState.Replicas {
		if replica.Status != ReplicaStatusStopped && replica.Status != ReplicaStatusFailed {
			assignment := &api.WorkloadAssignment{
				FragmentId: replica.FragmentID,
				Workload:   &api.Workload{Id: req.WorkloadId},
				Action:     "stop",
			}
			if err := c.membershipMgr.QueueAssignment(replica.NodeID, assignment); err != nil {
				c.logger.Error("Failed to queue stop assignment",
					zap.String("node_id", replica.NodeID),
					zap.String("replica_id", replica.ID),
					zap.Error(err),
				)
			}
		}
	}

	c.logger.Info("Workload deletion initiated",
		zap.String("workload_id", req.WorkloadId),
		zap.Int("replicas_to_stop", len(workloadState.Replicas)),
	)

	// If graceful deletion requested, wait for cleanup
	// Otherwise, purge immediately
	if !req.Graceful {
		// Purge workload from RAFT (hard delete)
		if err := c.workloadStateMgr.PurgeWorkload(ctx, req.WorkloadId); err != nil {
			c.logger.Warn("Failed to purge workload",
				zap.String("workload_id", req.WorkloadId),
				zap.Error(err),
			)
		}
	}

	return &emptypb.Empty{}, nil
}

// GetWorkload retrieves information about a workload
func (c *Coordinator) GetWorkload(ctx context.Context, req *api.GetWorkloadRequest) (*api.Workload, error) {
	c.logger.Debug("Getting workload",
		zap.String("workload_id", req.WorkloadId),
	)

	// Retrieve workload from state manager
	workload, err := c.workloadStateMgr.GetWorkload(ctx, req.WorkloadId)
	if err != nil {
		return nil, fmt.Errorf("failed to get workload: %w", err)
	}

	// Convert to API format
	apiWorkload := &api.Workload{
		Id:        workload.ID,
		Name:      workload.Name,
		Namespace: workload.Namespace,
		Spec: &api.WorkloadSpec{
			Image:    workload.Image,
			Command:  workload.Command,
			Args:     workload.Args,
			Env:      workload.Env,
			Replicas: workload.DesiredReplicas,
			Resources: &api.ResourceRequirements{
				Requests: &api.ResourceCapacity{
					CpuMillicores: workload.Resources.Requests.CPUMillicores,
					MemoryBytes:   workload.Resources.Requests.MemoryBytes,
					StorageBytes:  workload.Resources.Requests.StorageBytes,
				},
				Limits: &api.ResourceCapacity{
					CpuMillicores: workload.Resources.Limits.CPUMillicores,
					MemoryBytes:   workload.Resources.Limits.MemoryBytes,
					StorageBytes:  workload.Resources.Limits.StorageBytes,
				},
			},
		},
		Status: &api.WorkloadStatus{
			Phase:           convertWorkloadStatusToPhase(workload.Status),
			ReadyReplicas:   workload.ReadyReplicas,
			CurrentReplicas: workload.CurrentReplicas,
			DesiredReplicas: workload.DesiredReplicas,
		},
		CreatedAt: timestamppb.New(workload.CreatedAt),
		UpdatedAt: timestamppb.New(workload.UpdatedAt),
		Labels:    workload.Labels,
	}

	return apiWorkload, nil
}

// ListWorkloads lists all workloads
func (c *Coordinator) ListWorkloads(ctx context.Context, req *api.ListWorkloadsRequest) (*api.ListWorkloadsResponse, error) {
	c.logger.Debug("Listing workloads",
		zap.String("namespace", req.Namespace),
	)

	// List workloads from state manager with filtering
	workloads, err := c.workloadStateMgr.ListWorkloads(ctx, req.Namespace, req.LabelSelector)
	if err != nil {
		return nil, fmt.Errorf("failed to list workloads: %w", err)
	}

	// Convert to API format
	apiWorkloads := make([]*api.Workload, 0, len(workloads))
	for _, workload := range workloads {
		apiWorkloads = append(apiWorkloads, &api.Workload{
			Id:        workload.ID,
			Name:      workload.Name,
			Namespace: workload.Namespace,
			Spec: &api.WorkloadSpec{
				Image:    workload.Image,
				Command:  workload.Command,
				Args:     workload.Args,
				Replicas: workload.DesiredReplicas,
				Resources: &api.ResourceRequirements{
					Requests: &api.ResourceCapacity{
						CpuMillicores: workload.Resources.Requests.CPUMillicores,
						MemoryBytes:   workload.Resources.Requests.MemoryBytes,
						StorageBytes:  workload.Resources.Requests.StorageBytes,
					},
					Limits: &api.ResourceCapacity{
						CpuMillicores: workload.Resources.Limits.CPUMillicores,
						MemoryBytes:   workload.Resources.Limits.MemoryBytes,
						StorageBytes:  workload.Resources.Limits.StorageBytes,
					},
				},
			},
			Status: &api.WorkloadStatus{
				Phase:           convertWorkloadStatusToPhase(workload.Status),
				ReadyReplicas:   workload.ReadyReplicas,
				CurrentReplicas: workload.CurrentReplicas,
				DesiredReplicas: workload.DesiredReplicas,
			},
			CreatedAt: timestamppb.New(workload.CreatedAt),
			UpdatedAt: timestamppb.New(workload.UpdatedAt),
			Labels:    workload.Labels,
		})
	}

	return &api.ListWorkloadsResponse{
		Workloads: apiWorkloads,
	}, nil
}

// ScaleWorkload scales a workload to the desired number of replicas
func (c *Coordinator) ScaleWorkload(ctx context.Context, req *api.ScaleWorkloadRequest) (*api.Workload, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	c.logger.Info("Scaling workload",
		zap.String("workload_id", req.WorkloadId),
		zap.Int32("replicas", req.Replicas),
	)

	// Get workload state
	workloadState, err := c.workloadStateMgr.GetWorkload(ctx, req.WorkloadId)
	if err != nil {
		return nil, fmt.Errorf("failed to get workload: %w", err)
	}

	currentReplicas := workloadState.CurrentReplicas
	desiredReplicas := req.Replicas

	c.logger.Info("Scaling workload",
		zap.String("workload_id", req.WorkloadId),
		zap.Int32("current_replicas", currentReplicas),
		zap.Int32("desired_replicas", desiredReplicas),
	)

	// Update desired replicas
	workloadState.DesiredReplicas = desiredReplicas
	workloadState.Status = WorkloadStatusScaling

	if err := c.workloadStateMgr.SaveWorkload(ctx, workloadState); err != nil {
		return nil, fmt.Errorf("failed to save workload state: %w", err)
	}

	if desiredReplicas > currentReplicas {
		// Scale up: schedule new replicas
		replicasToAdd := int(desiredReplicas - currentReplicas)
		c.logger.Info("Scaling up",
			zap.String("workload_id", req.WorkloadId),
			zap.Int("replicas_to_add", replicasToAdd),
		)

		// Create scheduler spec for new replicas
		schedulerSpec := scheduler.WorkloadSpec{
			ID:              workloadState.ID,
			Name:            workloadState.Name,
			Namespace:       workloadState.Namespace,
			Replicas:        replicasToAdd,
			Resources:       workloadState.Resources,
			PlacementPolicy: workloadState.Placement,
		}

		// Schedule new replicas
		decisions, err := c.ScheduleWorkload(ctx, schedulerSpec)
		if err != nil {
			c.logger.Error("Failed to schedule new replicas",
				zap.String("workload_id", req.WorkloadId),
				zap.Error(err),
			)
			return nil, fmt.Errorf("failed to schedule new replicas: %w", err)
		}

		// Queue assignments for new replicas
		for _, decision := range decisions {
			assignment := &api.WorkloadAssignment{
				FragmentId: decision.FragmentID,
				Workload: &api.Workload{
					Id:        workloadState.ID,
					Name:      workloadState.Name,
					Namespace: workloadState.Namespace,
					Spec: &api.WorkloadSpec{
						Image:    workloadState.Image,
						Command:  workloadState.Command,
						Args:     workloadState.Args,
						Env:      workloadState.Env,
						Replicas: desiredReplicas,
					},
				},
				Action: "run",
			}
			if err := c.membershipMgr.QueueAssignment(decision.NodeID, assignment); err != nil {
				c.logger.Error("Failed to queue scale-up assignment",
					zap.String("node_id", decision.NodeID),
					zap.Error(err),
				)
			}
		}

	} else if desiredReplicas < currentReplicas {
		// Scale down: stop excess replicas
		replicasToRemove := int(currentReplicas - desiredReplicas)
		c.logger.Info("Scaling down",
			zap.String("workload_id", req.WorkloadId),
			zap.Int("replicas_to_remove", replicasToRemove),
		)

		// Select replicas to remove (prioritize failed/stopped ones first)
		replicasRemoved := 0
		for i := len(workloadState.Replicas) - 1; i >= 0 && replicasRemoved < replicasToRemove; i-- {
			replica := workloadState.Replicas[i]

			// Send stop command
			assignment := &api.WorkloadAssignment{
				FragmentId: replica.FragmentID,
				Workload:   &api.Workload{Id: req.WorkloadId},
				Action:     "stop",
			}
			if err := c.membershipMgr.QueueAssignment(replica.NodeID, assignment); err != nil {
				c.logger.Error("Failed to queue scale-down assignment",
					zap.String("node_id", replica.NodeID),
					zap.String("replica_id", replica.ID),
					zap.Error(err),
				)
			} else {
				replicasRemoved++
			}
		}
	}

	c.logger.Info("Workload scaling initiated",
		zap.String("workload_id", req.WorkloadId),
		zap.Int32("current", currentReplicas),
		zap.Int32("desired", desiredReplicas),
	)

	return &api.Workload{
		Id:        workloadState.ID,
		Name:      workloadState.Name,
		Namespace: workloadState.Namespace,
		Spec: &api.WorkloadSpec{
			Image:    workloadState.Image,
			Replicas: desiredReplicas,
		},
		Labels:      workloadState.Labels,
		Annotations: workloadState.Annotations,
	}, nil
}

// Schedule performs scheduling for a workload
func (c *Coordinator) Schedule(ctx context.Context, req *api.ScheduleRequest) (*api.ScheduleResponse, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	c.logger.Debug("Scheduling request",
		zap.String("workload_id", req.Workload.Id),
	)

	// Extract spec from workload
	workload := req.Workload
	if workload == nil || workload.Spec == nil {
		return nil, fmt.Errorf("workload and spec are required")
	}

	// Convert API request to scheduler spec
	spec := scheduler.WorkloadSpec{
		ID:        workload.Id,
		Name:      workload.Name,
		Namespace: workload.Namespace,
		Replicas:  int(workload.Spec.Replicas),
		Resources: scheduler.ResourceRequirements{
			Requests: scheduler.ResourceSpec{
				CPUMillicores: workload.Spec.Resources.Requests.CpuMillicores,
				MemoryBytes:   workload.Spec.Resources.Requests.MemoryBytes,
				StorageBytes:  workload.Spec.Resources.Requests.StorageBytes,
			},
		},
	}

	// Get ready nodes with filter
	filters := map[string]string{"state": "ready"}
	nodes, err := c.membershipMgr.ListNodes(filters)
	if err != nil {
		return nil, fmt.Errorf("failed to list nodes: %w", err)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no ready nodes available")
	}

	// Perform scheduling - Schedule returns ScheduleResult
	result, err := c.scheduler.Schedule(ctx, &spec)
	if err != nil {
		return nil, fmt.Errorf("scheduling failed: %w", err)
	}

	decisions := result.Decisions

	// Convert decisions to API format
	apiDecisions := make([]*api.ScheduleDecision, 0, len(decisions))
	for _, decision := range decisions {
		apiDecisions = append(apiDecisions, &api.ScheduleDecision{
			ReplicaId:  decision.ReplicaID,
			NodeId:     decision.NodeID,
			FragmentId: decision.FragmentID,
			Score:      decision.Score,
		})
	}

	return &api.ScheduleResponse{
		Decisions: apiDecisions,
	}, nil
}

// Reschedule performs rescheduling for a workload
// Implements CLD-REQ-030: Validates minAvailable constraint before rescheduling
func (c *Coordinator) Reschedule(ctx context.Context, req *api.RescheduleRequest) (*api.RescheduleResponse, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	c.logger.Info("Rescheduling workload",
		zap.String("workload_id", req.WorkloadId),
		zap.String("reason", req.Reason),
	)

	// Get workload state
	workloadState, err := c.workloadStateMgr.GetWorkload(ctx, req.WorkloadId)
	if err != nil {
		return nil, fmt.Errorf("failed to get workload: %w", err)
	}

	// Extract failed nodes from workload state
	// Collect node IDs that have failed replicas or are themselves failed
	failedNodesMap := make(map[string]bool)
	for _, replica := range workloadState.Replicas {
		if replica.Status == ReplicaStatusFailed {
			failedNodesMap[replica.NodeID] = true
		}
	}
	failedNodes := make([]string, 0, len(failedNodesMap))
	for nodeID := range failedNodesMap {
		failedNodes = append(failedNodes, nodeID)
	}

	c.logger.Info("Identified failed nodes for rescheduling",
		zap.Int("failed_nodes", len(failedNodes)),
		zap.Strings("node_ids", failedNodes),
	)

	// CLD-REQ-030: Check minAvailable constraint before rescheduling
	minAvailable := workloadState.Rollout.MinAvailable
	if minAvailable > 0 {
		if workloadState.ReadyReplicas < int32(minAvailable) {
			c.logger.Error("Cannot reschedule: minAvailable constraint violated",
				zap.String("workload_id", req.WorkloadId),
				zap.Int32("ready_replicas", workloadState.ReadyReplicas),
				zap.Int("min_available", minAvailable),
			)
			return nil, fmt.Errorf("cannot reschedule: current ready replicas (%d) below minAvailable (%d) - device failure would violate availability constraint",
				workloadState.ReadyReplicas, minAvailable)
		}

		c.logger.Info("MinAvailable constraint validated for rescheduling",
			zap.Int32("ready_replicas", workloadState.ReadyReplicas),
			zap.Int("min_available", minAvailable),
		)
	}

	// Build scheduler spec with RolloutStrategy
	schedulerSpec := scheduler.WorkloadSpec{
		ID:              workloadState.ID,
		Name:            workloadState.Name,
		Namespace:       workloadState.Namespace,
		Replicas:        int(workloadState.DesiredReplicas),
		Resources:       workloadState.Resources,
		PlacementPolicy: workloadState.Placement,
		RolloutStrategy: workloadState.Rollout, // CLD-REQ-030: Include rollout strategy
		RestartPolicy:   workloadState.Restart,
		Priority:        int(workloadState.Priority),
		Labels:          workloadState.Labels,
		Annotations:     workloadState.Annotations,
	}

	// Call scheduler's Reschedule method with failed nodes and current ready replicas
	result, err := c.scheduler.Reschedule(ctx, &schedulerSpec, failedNodes, int(workloadState.ReadyReplicas))
	if err != nil {
		c.logger.Error("Failed to reschedule workload",
			zap.String("workload_id", req.WorkloadId),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to reschedule workload: %w", err)
	}

	decisions := result.Decisions

	// Stop old replicas and start new ones
	oldDecisions := make([]*api.ScheduleDecision, 0, len(workloadState.Replicas))
	newDecisions := make([]*api.ScheduleDecision, 0, len(decisions))

	// Stop old replicas
	for _, replica := range workloadState.Replicas {
		if replica.Status == ReplicaStatusRunning {
			oldDecisions = append(oldDecisions, &api.ScheduleDecision{
				ReplicaId:  replica.ID,
				NodeId:     replica.NodeID,
				FragmentId: replica.FragmentID,
			})

			// Queue stop command
			assignment := &api.WorkloadAssignment{
				FragmentId: replica.FragmentID,
				Workload:   &api.Workload{Id: req.WorkloadId},
				Action:     "stop",
			}
			if err := c.membershipMgr.QueueAssignment(replica.NodeID, assignment); err != nil {
				c.logger.Error("Failed to queue stop for reschedule",
					zap.String("node_id", replica.NodeID),
					zap.String("replica_id", replica.ID),
					zap.Error(err),
				)
			}
		}
	}

	// Start new replicas on new nodes
	for _, decision := range decisions {
		newDecisions = append(newDecisions, &api.ScheduleDecision{
			NodeId:     decision.NodeID,
			FragmentId: decision.FragmentID,
			Score:      decision.Score,
		})

		// Queue run command
		assignment := &api.WorkloadAssignment{
			FragmentId: decision.FragmentID,
			Workload: &api.Workload{
				Id:        workloadState.ID,
				Name:      workloadState.Name,
				Namespace: workloadState.Namespace,
				Spec: &api.WorkloadSpec{
					Image:    workloadState.Image,
					Command:  workloadState.Command,
					Args:     workloadState.Args,
					Env:      workloadState.Env,
					Replicas: workloadState.DesiredReplicas,
				},
			},
			Action: "run",
		}
		if err := c.membershipMgr.QueueAssignment(decision.NodeID, assignment); err != nil {
			c.logger.Error("Failed to queue run for reschedule",
				zap.String("node_id", decision.NodeID),
				zap.Error(err),
			)
		}
	}

	c.logger.Info("Workload rescheduled",
		zap.String("workload_id", req.WorkloadId),
		zap.Int("old_replicas", len(oldDecisions)),
		zap.Int("new_replicas", len(newDecisions)),
	)

	return &api.RescheduleResponse{
		OldDecisions: oldDecisions,
		NewDecisions: newDecisions,
	}, nil
}
