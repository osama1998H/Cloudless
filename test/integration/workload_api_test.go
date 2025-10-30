//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
	"github.com/cloudless/cloudless/pkg/coordinator"
	"github.com/cloudless/cloudless/pkg/scheduler"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestWorkloadFullLifecycle tests the complete workload lifecycle
// with real scheduler and state manager (integration test)
func TestWorkloadFullLifecycle(t *testing.T) {
	ctx := context.Background()
	coord := setupIntegrationCoordinator(t)

	// Step 1: Create workload
	t.Log("Step 1: Creating workload...")
	createResp, err := coord.CreateWorkload(ctx, &api.CreateWorkloadRequest{
		Namespace: "integration-test",
		Name:      "nginx-app",
		Spec: &api.WorkloadSpec{
			Image:    "nginx:alpine",
			Replicas: 3,
			Resources: &api.ResourceRequirements{
				Requests: &api.ResourceCapacity{
					CpuMillicores: 200,
					MemoryBytes:   268435456, // 256MB
				},
			},
			Rollout: &api.RolloutStrategy_Config{
				Strategy:     api.RolloutStrategy_ROLLING_UPDATE,
				MinAvailable: 2,
			},
		},
	})
	require.NoError(t, err)
	require.NotNil(t, createResp)
	assert.Equal(t, "nginx-app", createResp.Name)
	assert.Equal(t, "integration-test", createResp.Namespace)
	assert.Equal(t, api.WorkloadPhase_WORKLOAD_PHASE_SCHEDULING, createResp.Status.Phase)

	workloadID := createResp.Name

	// Step 2: Get workload status
	t.Log("Step 2: Getting workload status...")
	getResp, err := coord.GetWorkload(ctx, &api.GetWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
	})
	require.NoError(t, err)
	assert.Equal(t, "nginx:alpine", getResp.Spec.Image)
	assert.Equal(t, int32(3), getResp.Spec.Replicas)

	// Step 3: Scale workload up
	t.Log("Step 3: Scaling workload up (3 → 5)...")
	scaleUpResp, err := coord.ScaleWorkload(ctx, &api.ScaleWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
		Replicas:  5,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(5), scaleUpResp.Spec.Replicas)

	// Verify scale up
	getResp, err = coord.GetWorkload(ctx, &api.GetWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(5), getResp.Spec.Replicas)

	// Step 4: Update workload (trigger rollout)
	t.Log("Step 4: Updating workload image (nginx:alpine → nginx:1.21)...")
	updateResp, err := coord.UpdateWorkload(ctx, &api.UpdateWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
		Image:     "nginx:1.21",
	})
	require.NoError(t, err)
	assert.Equal(t, "nginx:1.21", updateResp.Spec.Image)

	// Step 5: Scale workload down
	t.Log("Step 5: Scaling workload down (5 → 2)...")
	scaleDownResp, err := coord.ScaleWorkload(ctx, &api.ScaleWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
		Replicas:  2,
	})
	require.NoError(t, err)
	assert.Equal(t, int32(2), scaleDownResp.Spec.Replicas)

	// Step 6: List workloads
	t.Log("Step 6: Listing workloads...")
	listResp, err := coord.ListWorkloads(ctx, &api.ListWorkloadsRequest{
		Namespace: "integration-test",
	})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(listResp.Workloads), 1)

	found := false
	for _, w := range listResp.Workloads {
		if w.Name == workloadID {
			found = true
			assert.Equal(t, int32(2), w.Spec.Replicas)
			assert.Equal(t, "nginx:1.21", w.Spec.Image)
			break
		}
	}
	assert.True(t, found, "workload should be in list")

	// Step 7: Delete workload
	t.Log("Step 7: Deleting workload...")
	_, err = coord.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{
		Namespace:       "integration-test",
		Name:            workloadID,
		GracePeriodSecs: 10,
	})
	require.NoError(t, err)

	// Verify deletion
	time.Sleep(100 * time.Millisecond) // Allow deletion to process

	_, err = coord.GetWorkload(ctx, &api.GetWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
	})
	assert.Error(t, err) // Should fail because workload is deleted

	t.Log("✅ Full lifecycle test completed successfully!")
}

// TestRolloutStrategies tests different rollout strategies
func TestRolloutStrategies(t *testing.T) {
	ctx := context.Background()
	coord := setupIntegrationCoordinator(t)

	tests := []struct {
		name         string
		strategy     api.RolloutStrategy
		minAvailable int32
	}{
		{
			name:         "RECREATE strategy (downtime allowed)",
			strategy:     api.RolloutStrategy_RECREATE,
			minAvailable: 0,
		},
		{
			name:         "ROLLING_UPDATE strategy (zero downtime)",
			strategy:     api.RolloutStrategy_ROLLING_UPDATE,
			minAvailable: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			workloadName := "rollout-test-" + tt.name

			// Create workload
			createResp, err := coord.CreateWorkload(ctx, &api.CreateWorkloadRequest{
				Namespace: "integration-test",
				Name:      workloadName,
				Spec: &api.WorkloadSpec{
					Image:    "nginx:1.20",
					Replicas: 3,
					Rollout: &api.RolloutStrategy_Config{
						Strategy:     tt.strategy,
						MinAvailable: tt.minAvailable,
					},
				},
			})
			require.NoError(t, err)
			assert.Equal(t, workloadName, createResp.Name)

			// Update to trigger rollout
			updateResp, err := coord.UpdateWorkload(ctx, &api.UpdateWorkloadRequest{
				Namespace: "integration-test",
				Name:      workloadName,
				Image:     "nginx:1.21",
			})
			require.NoError(t, err)
			assert.Equal(t, "nginx:1.21", updateResp.Spec.Image)

			// Clean up
			_, err = coord.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{
				Namespace: "integration-test",
				Name:      workloadName,
			})
			require.NoError(t, err)
		})
	}
}

// TestWorkloadFailureRecovery tests recovery from failures
func TestWorkloadFailureRecovery(t *testing.T) {
	ctx := context.Background()
	coord := setupIntegrationCoordinator(t)

	// Create workload
	createResp, err := coord.CreateWorkload(ctx, &api.CreateWorkloadRequest{
		Namespace: "integration-test",
		Name:      "recovery-test",
		Spec: &api.WorkloadSpec{
			Image:    "nginx:alpine",
			Replicas: 3,
		},
	})
	require.NoError(t, err)

	workloadID := createResp.Name

	// Simulate update failure (invalid image)
	_, err = coord.UpdateWorkload(ctx, &api.UpdateWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
		Image:     "nonexistent-registry.com/invalid:latest",
	})
	// Update should succeed (validation happens at runtime)
	require.NoError(t, err)

	// Verify workload can still be retrieved
	getResp, err := coord.GetWorkload(ctx, &api.GetWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
	})
	require.NoError(t, err)
	assert.NotNil(t, getResp)

	// Clean up
	_, err = coord.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
	})
	require.NoError(t, err)
}

// TestConcurrentWorkloadOperations tests concurrent access to workload APIs
func TestConcurrentWorkloadOperations(t *testing.T) {
	ctx := context.Background()
	coord := setupIntegrationCoordinator(t)

	// Create initial workload
	createResp, err := coord.CreateWorkload(ctx, &api.CreateWorkloadRequest{
		Namespace: "integration-test",
		Name:      "concurrent-test",
		Spec: &api.WorkloadSpec{
			Image:    "nginx:alpine",
			Replicas: 3,
		},
	})
	require.NoError(t, err)

	workloadID := createResp.Name
	done := make(chan bool, 4)

	// Concurrent scale operations
	go func() {
		defer func() { done <- true }()
		for i := 0; i < 5; i++ {
			coord.ScaleWorkload(ctx, &api.ScaleWorkloadRequest{
				Namespace: "integration-test",
				Name:      workloadID,
				Replicas:  int32(3 + i%3),
			})
			time.Sleep(20 * time.Millisecond)
		}
	}()

	// Concurrent get operations
	go func() {
		defer func() { done <- true }()
		for i := 0; i < 10; i++ {
			coord.GetWorkload(ctx, &api.GetWorkloadRequest{
				Namespace: "integration-test",
				Name:      workloadID,
			})
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Concurrent list operations
	go func() {
		defer func() { done <- true }()
		for i := 0; i < 8; i++ {
			coord.ListWorkloads(ctx, &api.ListWorkloadsRequest{
				Namespace: "integration-test",
			})
			time.Sleep(15 * time.Millisecond)
		}
	}()

	// Concurrent update operations
	go func() {
		defer func() { done <- true }()
		images := []string{"nginx:alpine", "nginx:1.20", "nginx:1.21"}
		for i := 0; i < 3; i++ {
			coord.UpdateWorkload(ctx, &api.UpdateWorkloadRequest{
				Namespace: "integration-test",
				Name:      workloadID,
				Image:     images[i],
			})
			time.Sleep(30 * time.Millisecond)
		}
	}()

	// Wait for all goroutines
	for i := 0; i < 4; i++ {
		<-done
	}

	// Verify workload is in consistent state
	finalWorkload, err := coord.GetWorkload(ctx, &api.GetWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
	})
	require.NoError(t, err)
	assert.NotNil(t, finalWorkload)
	assert.Greater(t, finalWorkload.Spec.Replicas, int32(0))

	// Clean up
	_, err = coord.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{
		Namespace: "integration-test",
		Name:      workloadID,
	})
	require.NoError(t, err)

	t.Log("✅ Concurrent operations test completed successfully!")
}

// TestWorkloadWithSecurityContext tests workloads with security constraints (CLD-REQ-062)
func TestWorkloadWithSecurityContext(t *testing.T) {
	ctx := context.Background()
	coord := setupIntegrationCoordinator(t)

	// Create workload with security context
	createResp, err := coord.CreateWorkload(ctx, &api.CreateWorkloadRequest{
		Namespace: "integration-test",
		Name:      "secure-app",
		Spec: &api.WorkloadSpec{
			Image:    "secure-app:v1",
			Replicas: 1,
			Resources: &api.ResourceRequirements{
				Requests: &api.ResourceCapacity{
					CpuMillicores: 100,
					MemoryBytes:   134217728, // 128MB
				},
			},
			SecurityContext: &api.SecurityContext{
				RunAsNonRoot:           true,
				RunAsUser:              10001,
				ReadOnlyRootFilesystem: true,
				CapabilitiesDrop:       []string{"ALL"},
				CapabilitiesAdd:        []string{"NET_BIND_SERVICE"},
				Linux: &api.LinuxSecurityContext{
					SeccompProfile: &api.SeccompProfile{
						Type: "RuntimeDefault",
					},
					AppArmorProfile: &api.AppArmorProfile{
						Type: "RuntimeDefault",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	assert.NotNil(t, createResp.Spec.SecurityContext)
	assert.True(t, createResp.Spec.SecurityContext.RunAsNonRoot)

	// Clean up
	_, err = coord.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{
		Namespace: "integration-test",
		Name:      "secure-app",
	})
	require.NoError(t, err)

	t.Log("✅ Security context test completed successfully!")
}

// TestMultipleNamespaces tests workload isolation across namespaces
func TestMultipleNamespaces(t *testing.T) {
	ctx := context.Background()
	coord := setupIntegrationCoordinator(t)

	namespaces := []string{"prod", "staging", "dev"}

	// Create workload in each namespace
	for _, ns := range namespaces {
		_, err := coord.CreateWorkload(ctx, &api.CreateWorkloadRequest{
			Namespace: ns,
			Name:      "app",
			Spec: &api.WorkloadSpec{
				Image:    "nginx:alpine",
				Replicas: 1,
			},
		})
		require.NoError(t, err)
	}

	// List workloads per namespace
	for _, ns := range namespaces {
		listResp, err := coord.ListWorkloads(ctx, &api.ListWorkloadsRequest{
			Namespace: ns,
		})
		require.NoError(t, err)
		assert.Len(t, listResp.Workloads, 1)
		assert.Equal(t, ns, listResp.Workloads[0].Namespace)
	}

	// Clean up
	for _, ns := range namespaces {
		_, err := coord.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{
			Namespace: ns,
			Name:      "app",
		})
		require.NoError(t, err)
	}

	t.Log("✅ Multiple namespaces test completed successfully!")
}

// setupIntegrationCoordinator creates a coordinator with real dependencies for integration testing
func setupIntegrationCoordinator(t *testing.T) *coordinator.Coordinator {
	logger := zap.NewNop()

	// Create real scheduler with mock node inventory
	sched := scheduler.NewScheduler(logger)

	// Register mock nodes for scheduling
	mockNodes := []*coordinator.Node{
		{
			ID:     "integration-node-1",
			Region: "test-region",
			Zone:   "zone-a",
			Capacity: &coordinator.ResourceCapacity{
				CPUMillicores: 4000,
				MemoryBytes:   8589934592, // 8GB
				StorageBytes:  107374182400, // 100GB
			},
			State: coordinator.NodeStateReady,
		},
		{
			ID:     "integration-node-2",
			Region: "test-region",
			Zone:   "zone-b",
			Capacity: &coordinator.ResourceCapacity{
				CPUMillicores: 2000,
				MemoryBytes:   4294967296, // 4GB
				StorageBytes:  53687091200, // 50GB
			},
			State: coordinator.NodeStateReady,
		},
	}

	// Create real state manager (in-memory)
	stateManager := coordinator.NewInMemoryWorkloadStateManager()

	// Create mock membership manager
	membershipMgr := &mockMembershipManager{
		nodes: mockNodes,
	}

	// Create coordinator
	coord := &coordinator.Coordinator{
		Logger:           logger,
		Scheduler:        sched,
		WorkloadStateMgr: stateManager,
		MembershipMgr:    membershipMgr,
		PolicyEngine:     &allowAllPolicyEngine{},
		IsLeader:         true,
	}

	return coord
}

// mockMembershipManager is a simple membership manager for integration tests
type mockMembershipManager struct {
	nodes []*coordinator.Node
}

func (m *mockMembershipManager) QueueAssignment(nodeID string, assignment *coordinator.Assignment) error {
	// No-op for integration tests (no real agents running)
	return nil
}

func (m *mockMembershipManager) GetNode(nodeID string) (*coordinator.Node, error) {
	for _, node := range m.nodes {
		if node.ID == nodeID {
			return node, nil
		}
	}
	return nil, coordinator.ErrNodeNotFound
}

func (m *mockMembershipManager) ListNodes() []*coordinator.Node {
	return m.nodes
}

// allowAllPolicyEngine allows all workloads (for integration testing)
type allowAllPolicyEngine struct{}

func (p *allowAllPolicyEngine) Evaluate(ctx context.Context, workload interface{}) (*coordinator.PolicyResult, error) {
	return &coordinator.PolicyResult{Allowed: true}, nil
}
