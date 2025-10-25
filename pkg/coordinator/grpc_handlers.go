package coordinator

import (
	"context"
	"fmt"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/coordinator/membership"
	"github.com/cloudless/cloudless/pkg/scheduler"
	"go.uber.org/zap"
	"google.golang.org/protobuf/types/known/emptypb"
)

// EnrollNode handles node enrollment requests
func (c *Coordinator) EnrollNode(ctx context.Context, req *api.EnrollNodeRequest) (*api.EnrollNodeResponse, error) {
	c.logger.Info("Received enrollment request",
		zap.String("node_id", req.NodeId),
		zap.String("address", req.Address),
	)

	// Only leader can enroll nodes
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	// Verify enrollment token
	claims, err := c.tokenManager.VerifyToken(req.Token)
	if err != nil {
		c.logger.Warn("Invalid enrollment token",
			zap.String("node_id", req.NodeId),
			zap.Error(err),
		)
		return nil, fmt.Errorf("invalid enrollment token: %w", err)
	}

	// Verify node ID matches token claims
	if claims.NodeID != req.NodeId {
		c.logger.Warn("Node ID mismatch",
			zap.String("request_node_id", req.NodeId),
			zap.String("token_node_id", claims.NodeID),
		)
		return nil, fmt.Errorf("node ID mismatch")
	}

	// Issue certificate for the node
	cert, err := c.ca.IssueCertificate(req.NodeId, []string{req.NodeId})
	if err != nil {
		c.logger.Error("Failed to issue certificate",
			zap.String("node_id", req.NodeId),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to issue certificate: %w", err)
	}

	// Serialize certificate
	certPEM, keyPEM, err := c.ca.SerializeCertificate(cert)
	if err != nil {
		return nil, fmt.Errorf("failed to serialize certificate: %w", err)
	}

	// Register node with membership manager
	node := &membership.Node{
		ID:      req.NodeId,
		Address: req.Address,
		State:   membership.StateReady,
		Labels:  req.Labels,
		Capabilities: membership.NodeCapabilities{
			CPUCores:     int(req.Resources.CpuCores),
			MemoryBytes:  req.Resources.MemoryBytes,
			StorageBytes: req.Resources.StorageBytes,
		},
	}

	if err := c.membershipMgr.RegisterNode(node); err != nil {
		c.logger.Error("Failed to register node",
			zap.String("node_id", req.NodeId),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to register node: %w", err)
	}

	c.logger.Info("Node enrolled successfully",
		zap.String("node_id", req.NodeId),
	)

	return &api.EnrollNodeResponse{
		Success:     true,
		Certificate: certPEM,
		PrivateKey:  keyPEM,
		CaCert:      c.ca.GetCACertPEM(),
	}, nil
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

	return resp, nil
}

// GetNode retrieves information about a specific node
func (c *Coordinator) GetNode(ctx context.Context, req *api.GetNodeRequest) (*api.Node, error) {
	node := c.membershipMgr.GetNode(req.NodeId)
	if node == nil {
		return nil, fmt.Errorf("node not found: %s", req.NodeId)
	}

	return &api.Node{
		Id:      node.ID,
		Address: node.Address,
		State:   string(node.State),
		Labels:  node.Labels,
		Resources: &api.ResourceSpec{
			CpuCores:     int32(node.Capabilities.CPUCores),
			MemoryBytes:  node.Capabilities.MemoryBytes,
			StorageBytes: node.Capabilities.StorageBytes,
		},
	}, nil
}

// ListNodes lists all nodes in the cluster
func (c *Coordinator) ListNodes(ctx context.Context, req *api.ListNodesRequest) (*api.ListNodesResponse, error) {
	var nodes []*membership.Node

	if req.State == "" {
		// List all nodes regardless of state
		nodes = c.membershipMgr.ListNodes("")
	} else {
		// List nodes with specific state
		nodes = c.membershipMgr.ListNodes(membership.NodeState(req.State))
	}

	apiNodes := make([]*api.Node, 0, len(nodes))
	for _, node := range nodes {
		apiNodes = append(apiNodes, &api.Node{
			Id:      node.ID,
			Address: node.Address,
			State:   string(node.State),
			Labels:  node.Labels,
			Resources: &api.ResourceSpec{
				CpuCores:     int32(node.Capabilities.CPUCores),
				MemoryBytes:  node.Capabilities.MemoryBytes,
				StorageBytes: node.Capabilities.StorageBytes,
			},
		})
	}

	return &api.ListNodesResponse{
		Nodes: apiNodes,
	}, nil
}

// DrainNode marks a node for draining
func (c *Coordinator) DrainNode(ctx context.Context, req *api.DrainNodeRequest) (*emptypb.Empty, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	if err := c.membershipMgr.DrainNode(req.NodeId); err != nil {
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

	c.logger.Info("Creating workload",
		zap.String("name", req.Name),
		zap.String("namespace", req.Namespace),
		zap.Int32("replicas", req.Replicas),
	)

	// Convert API workload spec to scheduler workload spec
	spec := scheduler.WorkloadSpec{
		ID:        fmt.Sprintf("%s-%s", req.Namespace, req.Name),
		Name:      req.Name,
		Namespace: req.Namespace,
		Replicas:  int(req.Replicas),
		Priority:  int(req.Priority),
		Resources: scheduler.ResourceRequirements{
			Requests: scheduler.ResourceSpec{
				CPUMillicores: int64(req.Resources.CpuMillicores),
				MemoryBytes:   req.Resources.MemoryBytes,
				StorageBytes:  req.Resources.StorageBytes,
			},
			Limits: scheduler.ResourceSpec{
				CPUMillicores: int64(req.Resources.CpuMillicores) * 2, // Default to 2x requests
				MemoryBytes:   req.Resources.MemoryBytes * 2,
				StorageBytes:  req.Resources.StorageBytes,
			},
		},
		// TODO: Map other fields (image, command, env, etc.)
	}

	// Schedule the workload
	decisions, err := c.ScheduleWorkload(ctx, spec)
	if err != nil {
		c.logger.Error("Failed to schedule workload",
			zap.String("name", req.Name),
			zap.Error(err),
		)
		return nil, fmt.Errorf("failed to schedule workload: %w", err)
	}

	// Queue assignments for each node
	for _, decision := range decisions {
		assignment := &api.WorkloadAssignment{
			WorkloadId: spec.ID,
			NodeId:     decision.NodeID,
			Resources: &api.ResourceAllocation{
				CpuMillicores: int32(decision.Resources.CPUMillicores),
				MemoryBytes:   decision.Resources.MemoryBytes,
				StorageBytes:  decision.Resources.StorageBytes,
			},
		}
		if err := c.membershipMgr.QueueAssignment(decision.NodeID, assignment); err != nil {
			c.logger.Error("Failed to queue assignment",
				zap.String("node_id", decision.NodeID),
				zap.Error(err),
			)
		}
	}

	c.logger.Info("Workload created and scheduled",
		zap.String("name", req.Name),
		zap.Int("placements", len(decisions)),
	)

	return &api.Workload{
		Id:        spec.ID,
		Name:      spec.Name,
		Namespace: spec.Namespace,
		Replicas:  req.Replicas,
		State:     "Scheduled",
	}, nil
}

// UpdateWorkload updates an existing workload
func (c *Coordinator) UpdateWorkload(ctx context.Context, req *api.UpdateWorkloadRequest) (*api.Workload, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	c.logger.Info("Updating workload",
		zap.String("workload_id", req.WorkloadId),
	)

	// TODO: Implement workload update logic
	// This would involve:
	// 1. Looking up existing workload state
	// 2. Computing diff between current and desired state
	// 3. Issuing update/migration commands to affected nodes
	// 4. Updating workload state in RAFT

	return nil, fmt.Errorf("workload update not yet implemented")
}

// DeleteWorkload deletes a workload
func (c *Coordinator) DeleteWorkload(ctx context.Context, req *api.DeleteWorkloadRequest) (*emptypb.Empty, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	c.logger.Info("Deleting workload",
		zap.String("workload_id", req.WorkloadId),
	)

	// TODO: Implement workload deletion logic
	// This would involve:
	// 1. Finding all nodes running this workload
	// 2. Sending stop commands to those nodes
	// 3. Removing workload state from RAFT

	return &emptypb.Empty{}, fmt.Errorf("workload deletion not yet implemented")
}

// GetWorkload retrieves information about a workload
func (c *Coordinator) GetWorkload(ctx context.Context, req *api.GetWorkloadRequest) (*api.Workload, error) {
	c.logger.Debug("Getting workload",
		zap.String("workload_id", req.WorkloadId),
	)

	// TODO: Implement workload lookup from RAFT state
	return nil, fmt.Errorf("workload get not yet implemented")
}

// ListWorkloads lists all workloads
func (c *Coordinator) ListWorkloads(ctx context.Context, req *api.ListWorkloadsRequest) (*api.ListWorkloadsResponse, error) {
	c.logger.Debug("Listing workloads",
		zap.String("namespace", req.Namespace),
	)

	// TODO: Implement workload listing from RAFT state
	return &api.ListWorkloadsResponse{
		Workloads: []*api.Workload{},
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

	// TODO: Implement workload scaling logic
	// This would involve:
	// 1. Looking up current workload state
	// 2. Computing scale up/down operations
	// 3. Scheduling new replicas or stopping existing ones
	// 4. Updating workload state in RAFT

	return nil, fmt.Errorf("workload scaling not yet implemented")
}

// Schedule performs scheduling for a workload
func (c *Coordinator) Schedule(ctx context.Context, req *api.ScheduleRequest) (*api.ScheduleResponse, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	c.logger.Debug("Scheduling request",
		zap.String("workload_id", req.WorkloadId),
	)

	// Convert API request to scheduler spec
	spec := scheduler.WorkloadSpec{
		ID:       req.WorkloadId,
		Replicas: int(req.Replicas),
		Resources: scheduler.ResourceRequirements{
			Requests: scheduler.ResourceSpec{
				CPUMillicores: int64(req.Resources.CpuMillicores),
				MemoryBytes:   req.Resources.MemoryBytes,
				StorageBytes:  req.Resources.StorageBytes,
			},
		},
	}

	// Get ready nodes
	nodes := c.membershipMgr.ListNodes(membership.StateReady)
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no ready nodes available")
	}

	// Perform scheduling
	decisions, err := c.scheduler.Schedule(ctx, spec, nodes)
	if err != nil {
		return nil, fmt.Errorf("scheduling failed: %w", err)
	}

	// Convert decisions to API format
	placements := make([]*api.Placement, 0, len(decisions))
	for _, decision := range decisions {
		placements = append(placements, &api.Placement{
			NodeId: decision.NodeID,
			Resources: &api.ResourceAllocation{
				CpuMillicores: int32(decision.Resources.CPUMillicores),
				MemoryBytes:   decision.Resources.MemoryBytes,
				StorageBytes:  decision.Resources.StorageBytes,
			},
			Score: decision.Score,
		})
	}

	return &api.ScheduleResponse{
		Placements: placements,
	}, nil
}

// Reschedule performs rescheduling for a workload
func (c *Coordinator) Reschedule(ctx context.Context, req *api.RescheduleRequest) (*api.RescheduleResponse, error) {
	if !c.IsLeader() {
		return nil, fmt.Errorf("not the leader, redirect to: %s", c.GetLeader())
	}

	c.logger.Info("Rescheduling workload",
		zap.String("workload_id", req.WorkloadId),
		zap.String("reason", req.Reason),
	)

	// TODO: Implement rescheduling logic
	// This would involve:
	// 1. Looking up current workload placements
	// 2. Determining which replicas need to be rescheduled
	// 3. Finding new nodes for those replicas
	// 4. Issuing migration commands

	return &api.RescheduleResponse{
		Success:    false,
		Placements: []*api.Placement{},
	}, fmt.Errorf("rescheduling not yet implemented")
}
