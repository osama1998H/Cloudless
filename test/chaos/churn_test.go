// +build chaos

package chaos

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/osama1998H/Cloudless/pkg/api"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// NodeChurnScenario tests the system's resilience to nodes joining and leaving
type NodeChurnScenario struct {
	coordinatorAddr string
	duration        time.Duration
	churnRate       float64
	client          api.CoordinatorServiceClient
	conn            *grpc.ClientConn
	logger          *zap.Logger
	collector       *MetricsCollector

	// Track nodes we add for cleanup
	addedNodes []string
}

func NewNodeChurnScenario(coordinatorAddr string, duration time.Duration, churnRate float64, logger *zap.Logger) *NodeChurnScenario {
	return &NodeChurnScenario{
		coordinatorAddr: coordinatorAddr,
		duration:        duration,
		churnRate:       churnRate,
		logger:          logger,
		collector:       NewMetricsCollector(logger),
	}
}

func (ncs *NodeChurnScenario) Name() string {
	return "NodeChurn"
}

func (ncs *NodeChurnScenario) Setup(ctx context.Context) error {
	// Connect to coordinator
	conn, err := grpc.Dial(ncs.coordinatorAddr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	if err != nil {
		return fmt.Errorf("failed to connect to coordinator: %w", err)
	}

	ncs.conn = conn
	ncs.client = api.NewCoordinatorServiceClient(conn)
	ncs.addedNodes = make([]string, 0)

	return nil
}

func (ncs *NodeChurnScenario) Execute(ctx context.Context) error {
	ncs.logger.Info("Starting node churn",
		zap.Duration("duration", ncs.duration),
		zap.Float64("churn_rate", ncs.churnRate),
	)

	// Calculate interval between churn events
	interval := time.Duration(float64(time.Minute) / ncs.churnRate)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	deadline := time.Now().Add(ncs.duration)

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return nil
			}

			// Randomly add or remove nodes
			if len(ncs.addedNodes) == 0 || rand.Intn(2) == 0 {
				// Add node
				if err := ncs.addNode(ctx); err != nil {
					ncs.logger.Error("Failed to add node", zap.Error(err))
				}
			} else {
				// Remove node
				if err := ncs.removeNode(ctx); err != nil {
					ncs.logger.Error("Failed to remove node", zap.Error(err))
				}
			}
		}
	}
}

func (ncs *NodeChurnScenario) addNode(ctx context.Context) error {
	nodeName := fmt.Sprintf("chaos-node-%d", time.Now().UnixNano())

	req := &api.EnrollNodeRequest{
		Token:    "chaos-test-token",
		NodeName: nodeName,
		Region:   "chaos-region",
		Zone:     "chaos-zone",
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

	resp, err := ncs.client.EnrollNode(ctx, req)
	if err != nil {
		return err
	}

	ncs.addedNodes = append(ncs.addedNodes, resp.NodeId)
	ncs.collector.RecordNodeAffected()

	ncs.logger.Info("Added chaos node",
		zap.String("node_id", resp.NodeId),
		zap.String("node_name", nodeName),
	)

	return nil
}

func (ncs *NodeChurnScenario) removeNode(ctx context.Context) error {
	if len(ncs.addedNodes) == 0 {
		return nil
	}

	// Pick a random node to remove
	idx := rand.Intn(len(ncs.addedNodes))
	nodeID := ncs.addedNodes[idx]

	// Drain the node
	_, err := ncs.client.DrainNode(ctx, &api.DrainNodeRequest{
		NodeId:   nodeID,
		Graceful: false, // Force drain for chaos
	})
	if err != nil {
		return err
	}

	// Remove from tracking
	ncs.addedNodes = append(ncs.addedNodes[:idx], ncs.addedNodes[idx+1:]...)
	ncs.collector.RecordNodeAffected()

	ncs.logger.Info("Removed chaos node",
		zap.String("node_id", nodeID),
	)

	return nil
}

func (ncs *NodeChurnScenario) Verify(ctx context.Context) error {
	invariants := []SystemInvariant{
		{
			Name:        "ClusterAvailable",
			Description: "Cluster should still be available",
			Check: func(ctx context.Context) error {
				_, err := ncs.client.ListNodes(ctx, &api.ListNodesRequest{})
				return err
			},
		},
		{
			Name:        "WorkloadsHealthy",
			Description: "Existing workloads should still be healthy",
			Check: func(ctx context.Context) error {
				resp, err := ncs.client.ListWorkloads(ctx, &api.ListWorkloadsRequest{})
				if err != nil {
					return err
				}

				for _, wl := range resp.Workloads {
					if wl.Status.Phase == api.WorkloadStatus_FAILED {
						return fmt.Errorf("workload %s is in failed state", wl.Id)
					}
				}

				return nil
			},
		},
	}

	return VerifyInvariants(ctx, invariants, ncs.logger)
}

func (ncs *NodeChurnScenario) Teardown(ctx context.Context) error {
	// Cleanup any remaining added nodes
	for _, nodeID := range ncs.addedNodes {
		ncs.client.DrainNode(ctx, &api.DrainNodeRequest{
			NodeId:   nodeID,
			Graceful: true,
		})
	}

	if ncs.conn != nil {
		ncs.conn.Close()
	}

	// Print metrics report
	ncs.logger.Info(ncs.collector.Report())

	return nil
}

// TestNodeChurn tests node churn resilience
func TestNodeChurn(t *testing.T) {
	logger, _ := zap.NewDevelopment()

	config := ChaosConfig{
		NodeChurnRate:    2.0, // 2 nodes per minute
		ChurnDuration:    5 * time.Minute,
		VerificationWait: 30 * time.Second,
		RandomSeed:       time.Now().UnixNano(),
	}

	runner := NewChaosRunner(config, logger)

	scenario := NewNodeChurnScenario(
		"localhost:8443",
		config.ChurnDuration,
		config.NodeChurnRate,
		logger,
	)

	ctx := context.Background()
	if err := runner.RunScenario(ctx, scenario); err != nil {
		t.Fatalf("Chaos scenario failed: %v", err)
	}
}

// TestHighChurn tests the system under very high churn
func TestHighChurn(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping high churn test in short mode")
	}

	logger, _ := zap.NewDevelopment()

	config := ChaosConfig{
		NodeChurnRate:    10.0, // 10 nodes per minute - very high!
		ChurnDuration:    10 * time.Minute,
		VerificationWait: 1 * time.Minute,
		RandomSeed:       time.Now().UnixNano(),
	}

	runner := NewChaosRunner(config, logger)

	scenario := NewNodeChurnScenario(
		"localhost:8443",
		config.ChurnDuration,
		config.NodeChurnRate,
		logger,
	)

	ctx := context.Background()
	if err := runner.RunScenario(ctx, scenario); err != nil {
		t.Fatalf("High churn scenario failed: %v", err)
	}
}
