package api

import (
	"time"
)

// Placeholder types for API
// TODO: Generate these from protobuf definitions

// EnrollNodeRequest represents a node enrollment request
type EnrollNodeRequest struct {
	NodeID       string
	NodeName     string
	Address      string
	Region       string
	Zone         string
	JoinToken    string
	Capabilities NodeCapabilities
	Resources    NodeResources
}

// EnrollNodeResponse represents a node enrollment response
type EnrollNodeResponse struct {
	Success           bool
	Message           string
	Certificate       []byte
	HeartbeatInterval time.Time
}

// HeartbeatRequest represents a heartbeat from a node
type HeartbeatRequest struct {
	NodeID     string
	Capacity   NodeResources
	Usage      NodeResources
	Containers []ContainerInfo
	Conditions []NodeCondition
}

// HeartbeatResponse represents a heartbeat response
type HeartbeatResponse struct {
	Assignments      []*WorkloadAssignment
	Commands         []string
	NextHeartbeat    time.Time
}

// WorkloadAssignment represents a workload assignment to a node
type WorkloadAssignment struct {
	WorkloadID string
	Image      string
	Resources  ResourceRequirements
}

// NodeCapabilities represents node capabilities
type NodeCapabilities struct {
	GPUCount        int
	AcceleratorType string
	NetworkFeatures []string
	StorageTypes    []string
}

// NodeResources represents node resource capacity or usage
type NodeResources struct {
	CPUMillicores int64
	MemoryBytes   int64
	StorageBytes  int64
	BandwidthBPS  int64
	GPUCount      int
}

// ContainerInfo represents container information
type ContainerInfo struct {
	ID    string
	Name  string
	State string
	Image string
}

// NodeCondition represents a node condition
type NodeCondition struct {
	Type    string
	Status  string
	Message string
}

// ResourceRequirements represents resource requests and limits
type ResourceRequirements struct {
	Requests NodeResources
	Limits   NodeResources
}
