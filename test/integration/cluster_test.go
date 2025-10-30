// +build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/osama1998H/Cloudless/pkg/api"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TestClusterStartup tests that a cluster starts up correctly
func TestClusterStartup(t *testing.T) {
	// Connect to coordinator
	conn, err := grpc.Dial("localhost:8443",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(30*time.Second),
	)
	if err != nil {
		t.Fatalf("Failed to connect to coordinator: %v", err)
	}
	defer conn.Close()

	client := api.NewCoordinatorServiceClient(conn)

	// List nodes
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListNodes(ctx, &api.ListNodesRequest{})
	if err != nil {
		t.Fatalf("Failed to list nodes: %v", err)
	}

	// Should have at least the coordinator node
	if len(resp.Nodes) == 0 {
		t.Error("Expected at least one node in the cluster")
	}

	t.Logf("Found %d nodes in cluster", len(resp.Nodes))
}

// TestNodeEnrollment tests node enrollment process
func TestNodeEnrollment(t *testing.T) {
	conn, err := grpc.Dial("localhost:8443",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("Failed to connect to coordinator: %v", err)
	}
	defer conn.Close()

	client := api.NewCoordinatorServiceClient(conn)

	// Enroll a new node
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	enrollReq := &api.EnrollNodeRequest{
		Token:    "test-token",
		NodeName: "test-node",
		Region:   "test-region",
		Zone:     "test-zone",
		Capabilities: &api.NodeCapabilities{
			ContainerRuntimes: []string{"containerd"},
			SupportsX86:       true,
		},
		Capacity: &api.ResourceCapacity{
			CpuMillicores: 4000,
			MemoryBytes:   8 * 1024 * 1024 * 1024,
			StorageBytes:  100 * 1024 * 1024 * 1024,
			BandwidthBps:  1000 * 1024 * 1024,
		},
	}

	enrollResp, err := client.EnrollNode(ctx, enrollReq)
	if err != nil {
		t.Fatalf("Failed to enroll node: %v", err)
	}

	if enrollResp.NodeId == "" {
		t.Error("Expected node ID in enrollment response")
	}

	t.Logf("Node enrolled with ID: %s", enrollResp.NodeId)

	// Verify node appears in list
	listResp, err := client.ListNodes(ctx, &api.ListNodesRequest{})
	if err != nil {
		t.Fatalf("Failed to list nodes: %v", err)
	}

	found := false
	for _, node := range listResp.Nodes {
		if node.Id == enrollResp.NodeId {
			found = true
			break
		}
	}

	if !found {
		t.Error("Enrolled node not found in node list")
	}
}

// TestWorkloadLifecycle tests complete workload lifecycle
func TestWorkloadLifecycle(t *testing.T) {
	conn, err := grpc.Dial("localhost:8443",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("Failed to connect to coordinator: %v", err)
	}
	defer conn.Close()

	client := api.NewCoordinatorServiceClient(conn)
	ctx := context.Background()

	// Create workload
	workload := &api.Workload{
		Name:      "test-workload",
		Namespace: "default",
		Spec: &api.WorkloadSpec{
			Image:    "nginx:latest",
			Replicas: 1,
			Resources: &api.ResourceRequirements{
				Requests: &api.ResourceCapacity{
					CpuMillicores: 100,
					MemoryBytes:   128 * 1024 * 1024,
				},
			},
		},
	}

	createResp, err := client.CreateWorkload(ctx, &api.CreateWorkloadRequest{
		Workload: workload,
	})
	if err != nil {
		t.Fatalf("Failed to create workload: %v", err)
	}

	workloadID := createResp.Id
	t.Logf("Created workload with ID: %s", workloadID)

	// Wait for workload to be scheduled
	time.Sleep(5 * time.Second)

	// Get workload status
	getResp, err := client.GetWorkload(ctx, &api.GetWorkloadRequest{
		WorkloadId: workloadID,
	})
	if err != nil {
		t.Fatalf("Failed to get workload: %v", err)
	}

	if getResp.Status.Phase == api.WorkloadStatus_UNKNOWN || getResp.Status.Phase == api.WorkloadStatus_FAILED {
		t.Errorf("Workload in unexpected state: %s", getResp.Status.Phase)
	}

	t.Logf("Workload status: %s", getResp.Status.Phase)

	// Scale workload
	scaleResp, err := client.ScaleWorkload(ctx, &api.ScaleWorkloadRequest{
		WorkloadId: workloadID,
		Replicas:   3,
	})
	if err != nil {
		t.Fatalf("Failed to scale workload: %v", err)
	}

	if scaleResp.Spec.Replicas != 3 {
		t.Errorf("Expected 3 replicas, got %d", scaleResp.Spec.Replicas)
	}

	t.Log("Scaled workload to 3 replicas")

	// Delete workload
	_, err = client.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{
		WorkloadId: workloadID,
		Graceful:   true,
	})
	if err != nil {
		t.Fatalf("Failed to delete workload: %v", err)
	}

	t.Log("Workload deleted successfully")
}

// TestNodeFailover tests node failure and workload rescheduling
func TestNodeFailover(t *testing.T) {
	t.Skip("Requires multi-node cluster setup")

	conn, err := grpc.Dial("localhost:8443",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("Failed to connect to coordinator: %v", err)
	}
	defer conn.Close()

	client := api.NewCoordinatorServiceClient(conn)
	ctx := context.Background()

	// Create workload with 2 replicas
	workload := &api.Workload{
		Name:      "failover-test",
		Namespace: "default",
		Spec: &api.WorkloadSpec{
			Image:    "nginx:latest",
			Replicas: 2,
		},
	}

	createResp, err := client.CreateWorkload(ctx, &api.CreateWorkloadRequest{
		Workload: workload,
	})
	if err != nil {
		t.Fatalf("Failed to create workload: %v", err)
	}

	workloadID := createResp.Id

	// Wait for workload to be running
	time.Sleep(10 * time.Second)

	// Get workload and find node
	getResp, err := client.GetWorkload(ctx, &api.GetWorkloadRequest{
		WorkloadId: workloadID,
	})
	if err != nil {
		t.Fatalf("Failed to get workload: %v", err)
	}

	if len(getResp.Status.Replicas) == 0 {
		t.Fatal("No replicas found")
	}

	nodeID := getResp.Status.Replicas[0].NodeId

	// Drain the node
	_, err = client.DrainNode(ctx, &api.DrainNodeRequest{
		NodeId:   nodeID,
		Graceful: true,
	})
	if err != nil {
		t.Fatalf("Failed to drain node: %v", err)
	}

	t.Logf("Drained node %s", nodeID)

	// Wait for rescheduling
	time.Sleep(15 * time.Second)

	// Verify replicas were rescheduled
	getResp, err = client.GetWorkload(ctx, &api.GetWorkloadRequest{
		WorkloadId: workloadID,
	})
	if err != nil {
		t.Fatalf("Failed to get workload: %v", err)
	}

	if getResp.Status.ReadyReplicas < 2 {
		t.Errorf("Expected 2 ready replicas after failover, got %d", getResp.Status.ReadyReplicas)
	}

	// Cleanup
	client.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{WorkloadId: workloadID})
	client.UncordonNode(ctx, &api.UncordonNodeRequest{NodeId: nodeID})
}

// TestSchedulingConstraints tests workload placement with constraints
func TestSchedulingConstraints(t *testing.T) {
	conn, err := grpc.Dial("localhost:8443",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		t.Fatalf("Failed to connect to coordinator: %v", err)
	}
	defer conn.Close()

	client := api.NewCoordinatorServiceClient(conn)
	ctx := context.Background()

	// Create workload with zone affinity
	workload := &api.Workload{
		Name:      "constrained-workload",
		Namespace: "default",
		Spec: &api.WorkloadSpec{
			Image:    "nginx:latest",
			Replicas: 1,
			Placement: &api.PlacementPolicy{
				Zones: []string{"us-east-1a"},
			},
		},
	}

	createResp, err := client.CreateWorkload(ctx, &api.CreateWorkloadRequest{
		Workload: workload,
	})
	if err != nil {
		t.Fatalf("Failed to create workload: %v", err)
	}

	workloadID := createResp.Id

	// Wait for scheduling
	time.Sleep(5 * time.Second)

	// Verify workload was scheduled to correct zone
	getResp, err := client.GetWorkload(ctx, &api.GetWorkloadRequest{
		WorkloadId: workloadID,
	})
	if err != nil {
		t.Fatalf("Failed to get workload: %v", err)
	}

	if len(getResp.Status.Replicas) == 0 {
		t.Error("No replicas scheduled")
	} else {
		nodeID := getResp.Status.Replicas[0].NodeId

		// Get node details
		node, err := client.GetNode(ctx, &api.GetNodeRequest{NodeId: nodeID})
		if err != nil {
			t.Fatalf("Failed to get node: %v", err)
		}

		if node.Zone != "us-east-1a" {
			t.Errorf("Expected workload to be scheduled in zone us-east-1a, got %s", node.Zone)
		}
	}

	// Cleanup
	client.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{WorkloadId: workloadID})
}
