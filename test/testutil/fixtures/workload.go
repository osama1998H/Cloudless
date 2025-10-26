package fixtures

import (
	"github.com/cloudless/cloudless/pkg/api"
)

// NewTestWorkload creates a basic workload for testing
func NewTestWorkload(name, namespace string) *api.Workload {
	return &api.Workload{
		Id:        "test-workload-id",
		Name:      name,
		Namespace: namespace,
		Labels: map[string]string{
			"app": "test",
		},
		Annotations: map[string]string{
			"test": "true",
		},
		Spec: &api.WorkloadSpec{
			Image:   "nginx:latest",
			Command: []string{"/bin/sh"},
			Args:    []string{"-c", "echo hello"},
			Env: map[string]string{
				"ENV": "test",
			},
			Resources: &api.ResourceRequirements{
				Requests: &api.ResourceCapacity{
					CpuMillicores: 100,
					MemoryBytes:   128 * 1024 * 1024, // 128MB
				},
				Limits: &api.ResourceCapacity{
					CpuMillicores: 500,
					MemoryBytes:   512 * 1024 * 1024, // 512MB
				},
			},
		},
	}
}

// NewTestWorkloadWithResources creates a workload with custom resources
func NewTestWorkloadWithResources(name, namespace string, cpuMillis, memBytes int64) *api.Workload {
	workload := NewTestWorkload(name, namespace)
	workload.Spec.Resources = &api.ResourceRequirements{
		Requests: &api.ResourceCapacity{
			CpuMillicores: cpuMillis,
			MemoryBytes:   memBytes,
		},
		Limits: &api.ResourceCapacity{
			CpuMillicores: cpuMillis * 2,
			MemoryBytes:   memBytes * 2,
		},
	}
	return workload
}

// NewTestNode creates a basic node for testing
func NewTestNode(id, name, zone string) *api.Node {
	return &api.Node{
		Id:     id,
		Name:   name,
		Zone:   zone,
		Region: "us-west",
		Capacity: &api.ResourceCapacity{
			CpuMillicores: 4000,
			MemoryBytes:   4 * 1024 * 1024 * 1024, // 4GB
		},
		Usage: &api.ResourceUsage{
			CpuMillicores: 1000,
			MemoryBytes:   1 * 1024 * 1024 * 1024, // 1GB used
		},
		Labels: map[string]string{
			"zone": zone,
		},
	}
}
