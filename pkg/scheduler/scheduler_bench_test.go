//go:build benchmark
// +build benchmark

package scheduler

import (
	"context"
	"fmt"
	"math/rand"
	"testing"
	"time"

	"github.com/cloudless/cloudless/pkg/api"
)

// BenchmarkScheduleWorkload benchmarks the complete scheduling decision path
func BenchmarkScheduleWorkload(b *testing.B) {
	ctx := context.Background()

	tests := []struct {
		name      string
		nodeCount int
		replicas  int32
	}{
		{"10_nodes_1_replica", 10, 1},
		{"50_nodes_1_replica", 50, 1},
		{"100_nodes_1_replica", 100, 1},
		{"100_nodes_3_replicas", 100, 3},
		{"500_nodes_1_replica", 500, 1},
		{"1000_nodes_1_replica", 1000, 1},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			scheduler := createBenchScheduler(tt.nodeCount)
			workload := createBenchWorkload(tt.replicas)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, err := scheduler.ScheduleWorkload(ctx, workload)
				if err != nil {
					b.Fatalf("Scheduling failed: %v", err)
				}
			}
			b.StopTimer()

			// Report latency percentiles
			opsPerSec := float64(b.N) / b.Elapsed().Seconds()
			avgLatencyMs := b.Elapsed().Milliseconds() / int64(b.N)
			b.ReportMetric(float64(avgLatencyMs), "ms/op")
			b.ReportMetric(opsPerSec, "ops/sec")
		})
	}
}

// BenchmarkNodeScoring benchmarks the node scoring algorithm
func BenchmarkNodeScoring(b *testing.B) {
	scorer := &Scorer{
		LocalityWeight:     0.3,
		ReliabilityWeight:  0.25,
		CostWeight:         0.15,
		UtilizationWeight:  0.2,
		NetworkPenaltyWeight: 0.1,
	}

	tests := []struct {
		name      string
		nodeCount int
	}{
		{"10_nodes", 10},
		{"50_nodes", 50},
		{"100_nodes", 100},
		{"500_nodes", 500},
		{"1000_nodes", 1000},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			nodes := generateBenchNodes(tt.nodeCount)
			req := &WorkloadRequest{
				CPUMillicores: 1000,
				MemoryBytes:   2 * 1024 * 1024 * 1024,
				PreferredZone: "us-east-1a",
			}

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, node := range nodes {
					_ = scorer.ScoreNode(node, req)
				}
			}
			b.StopTimer()

			nodesPerSec := float64(tt.nodeCount*b.N) / b.Elapsed().Seconds()
			b.ReportMetric(nodesPerSec, "nodes/sec")
		})
	}
}

// BenchmarkFilterNodes benchmarks node filtering by constraints
func BenchmarkFilterNodes(b *testing.B) {
	tests := []struct {
		name        string
		nodeCount   int
		constraints *api.PlacementPolicy
	}{
		{
			name:      "no_constraints_100",
			nodeCount: 100,
			constraints: nil,
		},
		{
			name:      "zone_constraint_100",
			nodeCount: 100,
			constraints: &api.PlacementPolicy{
				Zones: []string{"us-east-1a"},
			},
		},
		{
			name:      "label_constraint_100",
			nodeCount: 100,
			constraints: &api.PlacementPolicy{
				NodeSelector: map[string]string{
					"tier": "premium",
				},
			},
		},
		{
			name:      "multiple_constraints_1000",
			nodeCount: 1000,
			constraints: &api.PlacementPolicy{
				Zones:        []string{"us-east-1a", "us-east-1b"},
				Regions:      []string{"us-east"},
				NodeSelector: map[string]string{
					"tier":     "premium",
					"workload": "api",
				},
			},
		},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			scheduler := createBenchScheduler(tt.nodeCount)
			workload := createBenchWorkload(1)
			workload.Spec.Placement = tt.constraints

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				nodes := scheduler.state.GetAvailableNodes()
				_ = scheduler.filterNodesByConstraints(nodes, workload)
			}
		})
	}
}

// BenchmarkPlacementDecision benchmarks the complete placement algorithm
func BenchmarkPlacementDecision(b *testing.B) {
	tests := []struct {
		name      string
		nodeCount int
		replicas  int32
	}{
		{"single_replica_100", 100, 1},
		{"multi_replica_100", 100, 5},
		{"multi_replica_500", 500, 10},
		{"multi_replica_1000", 1000, 20},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			scheduler := createBenchScheduler(tt.nodeCount)
			workload := createBenchWorkload(tt.replicas)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				candidates := scheduler.state.GetAvailableNodes()
				filtered := scheduler.filterNodesByConstraints(candidates, workload)
				_ = scheduler.selectBestNodes(filtered, workload, int(tt.replicas))
			}
		})
	}
}

// BenchmarkRescheduling benchmarks workload rescheduling after node failure
func BenchmarkRescheduling(b *testing.B) {
	ctx := context.Background()
	scheduler := createBenchScheduler(100)

	// Create initial workload
	workload := createBenchWorkload(3)
	placement, err := scheduler.ScheduleWorkload(ctx, workload)
	if err != nil {
		b.Fatalf("Initial scheduling failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate node failure
		failedNodeID := placement.Assignments[0].NodeID

		// Reschedule
		_, err := scheduler.RescheduleWorkload(ctx, workload, failedNodeID)
		if err != nil {
			b.Fatalf("Rescheduling failed: %v", err)
		}
	}
}

// BenchmarkConcurrentScheduling benchmarks concurrent scheduling requests
func BenchmarkConcurrentScheduling(b *testing.B) {
	ctx := context.Background()
	scheduler := createBenchScheduler(500)

	b.RunParallel(func(pb *testing.PB) {
		workload := createBenchWorkload(1)
		for pb.Next() {
			_, err := scheduler.ScheduleWorkload(ctx, workload)
			if err != nil {
				b.Errorf("Concurrent scheduling failed: %v", err)
			}
		}
	})
}

// BenchmarkSchedulerStateUpdate benchmarks updating scheduler state
func BenchmarkSchedulerStateUpdate(b *testing.B) {
	tests := []struct {
		name      string
		nodeCount int
	}{
		{"10_nodes", 10},
		{"100_nodes", 100},
		{"500_nodes", 500},
		{"1000_nodes", 1000},
	}

	for _, tt := range tests {
		b.Run(tt.name, func(b *testing.B) {
			scheduler := createBenchScheduler(tt.nodeCount)
			nodes := generateBenchNodes(tt.nodeCount)

			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				for _, node := range nodes {
					// Simulate heartbeat update
					node.Usage.CpuMillicores = rand.Int63n(node.Capacity.CpuMillicores)
					node.Usage.MemoryBytes = rand.Int63n(node.Capacity.MemoryBytes)
					scheduler.state.UpdateNode(node)
				}
			}
		})
	}
}

// Helper functions for benchmarks

func createBenchScheduler(nodeCount int) *Scheduler {
	state := NewSchedulerState()

	// Add nodes to state
	nodes := generateBenchNodes(nodeCount)
	for _, node := range nodes {
		state.AddNode(node)
	}

	scorer := &Scorer{
		LocalityWeight:        0.3,
		ReliabilityWeight:     0.25,
		CostWeight:            0.15,
		UtilizationWeight:     0.2,
		NetworkPenaltyWeight:  0.1,
	}

	return &Scheduler{
		state:  state,
		scorer: scorer,
	}
}

func generateBenchNodes(count int) []*NodeInfo {
	nodes := make([]*NodeInfo, count)
	zones := []string{"us-east-1a", "us-east-1b", "us-east-1c", "us-west-1a", "us-west-1b"}
	regions := []string{"us-east", "us-west"}

	for i := 0; i < count; i++ {
		zone := zones[i%len(zones)]
		region := regions[i%len(regions)]

		nodes[i] = &NodeInfo{
			ID:     fmt.Sprintf("node-%d", i),
			Name:   fmt.Sprintf("bench-node-%d", i),
			Zone:   zone,
			Region: region,
			Status: api.NodeStatus_READY,
			Capacity: api.ResourceCapacity{
				CpuMillicores: 16000,
				MemoryBytes:   32 * 1024 * 1024 * 1024,
				StorageBytes:  500 * 1024 * 1024 * 1024,
				BandwidthBps:  10 * 1024 * 1024 * 1024,
			},
			Usage: api.ResourceCapacity{
				CpuMillicores: rand.Int63n(8000),
				MemoryBytes:   rand.Int63n(16 * 1024 * 1024 * 1024),
				StorageBytes:  rand.Int63n(250 * 1024 * 1024 * 1024),
				BandwidthBps:  rand.Int63n(5 * 1024 * 1024 * 1024),
			},
			ReliabilityScore: 0.8 + rand.Float64()*0.2,
			CostPerHour:      0.10 + rand.Float64()*0.50,
			Labels: map[string]string{
				"tier":     []string{"premium", "standard", "basic"}[i%3],
				"workload": []string{"api", "batch", "ml"}[i%3],
			},
			LastHeartbeat: time.Now(),
		}
	}

	return nodes
}

func createBenchWorkload(replicas int32) *api.Workload {
	return &api.Workload{
		Id:        fmt.Sprintf("bench-workload-%d", rand.Int63()),
		Name:      "benchmark-workload",
		Namespace: "default",
		Spec: &api.WorkloadSpec{
			Image:    "nginx:latest",
			Replicas: replicas,
			Resources: &api.ResourceRequirements{
				Requests: &api.ResourceCapacity{
					CpuMillicores: 1000,
					MemoryBytes:   2 * 1024 * 1024 * 1024,
					StorageBytes:  10 * 1024 * 1024 * 1024,
				},
				Limits: &api.ResourceCapacity{
					CpuMillicores: 2000,
					MemoryBytes:   4 * 1024 * 1024 * 1024,
					StorageBytes:  20 * 1024 * 1024 * 1024,
				},
			},
		},
	}
}
