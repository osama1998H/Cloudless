package storage

import (
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"
)

// PlacementStrategy determines where to place chunks and replicas
type PlacementStrategy struct {
	config StorageConfig
	logger *zap.Logger
	nodes  sync.Map // nodeID -> *StorageNode
	mu     sync.RWMutex
	rand   *rand.Rand
}

// NewPlacementStrategy creates a new placement strategy
func NewPlacementStrategy(config StorageConfig, logger *zap.Logger) *PlacementStrategy {
	return &PlacementStrategy{
		config: config,
		logger: logger,
		rand:   rand.New(rand.NewSource(time.Now().UnixNano())),
	}
}

// RegisterNode registers a storage node
func (ps *PlacementStrategy) RegisterNode(node *StorageNode) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.nodes.Store(node.ID, node)

	ps.logger.Info("Registered storage node",
		zap.String("node_id", node.ID),
		zap.String("zone", node.Zone),
		zap.Int64("capacity", node.Capacity),
		zap.String("iops_class", string(node.IOPSClass)),
	)

	return nil
}

// UnregisterNode removes a storage node
func (ps *PlacementStrategy) UnregisterNode(nodeID string) error {
	ps.mu.Lock()
	defer ps.mu.Unlock()

	ps.nodes.Delete(nodeID)

	ps.logger.Info("Unregistered storage node",
		zap.String("node_id", nodeID),
	)

	return nil
}

// UpdateNodeCapacity updates a node's capacity information
func (ps *PlacementStrategy) UpdateNodeCapacity(nodeID string, used, capacity int64) error {
	n, ok := ps.nodes.Load(nodeID)
	if !ok {
		return fmt.Errorf("node not found: %s", nodeID)
	}

	node := n.(*StorageNode)
	node.Used = used
	node.Capacity = capacity
	node.Available = capacity - used
	node.LastUpdated = time.Now()

	ps.nodes.Store(nodeID, node)

	return nil
}

// SelectNodes selects nodes for chunk placement
func (ps *PlacementStrategy) SelectNodes(req *PlacementRequest) ([]string, error) {
	ps.logger.Debug("Selecting nodes for placement",
		zap.String("storage_class", string(req.StorageClass)),
		zap.String("iops_class", string(req.IOPSClass)),
		zap.Int("replica_count", req.ReplicaCount),
		zap.Int64("size", req.Size),
	)

	// Get eligible nodes
	eligibleNodes := ps.getEligibleNodes(req)
	if len(eligibleNodes) == 0 {
		return nil, fmt.Errorf("no eligible nodes available")
	}

	// Check if we have enough nodes
	if len(eligibleNodes) < req.ReplicaCount {
		return nil, fmt.Errorf("insufficient nodes: need %d, got %d",
			req.ReplicaCount, len(eligibleNodes))
	}

	// Select nodes based on strategy
	selectedNodes, err := ps.selectBestNodes(eligibleNodes, req)
	if err != nil {
		return nil, err
	}

	ps.logger.Debug("Selected nodes for placement",
		zap.Strings("node_ids", selectedNodes),
	)

	return selectedNodes, nil
}

// SelectPrimaryNode selects the best node to read from
func (ps *PlacementStrategy) SelectPrimaryNode(nodeIDs []string, preferredZone string) (string, error) {
	if len(nodeIDs) == 0 {
		return "", fmt.Errorf("no nodes provided")
	}

	// Try to select node in preferred zone
	if preferredZone != "" {
		for _, nodeID := range nodeIDs {
			if n, ok := ps.nodes.Load(nodeID); ok {
				node := n.(*StorageNode)
				if node.Zone == preferredZone && node.State == NodeStateHealthy {
					return nodeID, nil
				}
			}
		}
	}

	// Select node with lowest load
	var bestNode string
	var lowestLoad float64 = 1.0

	for _, nodeID := range nodeIDs {
		if n, ok := ps.nodes.Load(nodeID); ok {
			node := n.(*StorageNode)
			if node.State != NodeStateHealthy {
				continue
			}

			load := ps.calculateNodeLoad(node)
			if bestNode == "" || load < lowestLoad {
				bestNode = nodeID
				lowestLoad = load
			}
		}
	}

	if bestNode == "" {
		// Fallback to random selection
		return nodeIDs[ps.rand.Intn(len(nodeIDs))], nil
	}

	return bestNode, nil
}

// GetNodesByIOPS returns nodes matching an IOPS class
func (ps *PlacementStrategy) GetNodesByIOPS(iopsClass IOPSClass) []*StorageNode {
	var nodes []*StorageNode

	ps.nodes.Range(func(key, value interface{}) bool {
		node := value.(*StorageNode)
		if node.IOPSClass == iopsClass && node.State == NodeStateHealthy {
			nodes = append(nodes, node)
		}
		return true
	})

	return nodes
}

// GetNodesByZone returns nodes in a specific zone
func (ps *PlacementStrategy) GetNodesByZone(zone string) []*StorageNode {
	var nodes []*StorageNode

	ps.nodes.Range(func(key, value interface{}) bool {
		node := value.(*StorageNode)
		if node.Zone == zone && node.State == NodeStateHealthy {
			nodes = append(nodes, node)
		}
		return true
	})

	return nodes
}

// RebalanceRecommendations suggests rebalancing operations
func (ps *PlacementStrategy) RebalanceRecommendations() []RebalanceOperation {
	var operations []RebalanceOperation

	// Get all nodes
	var nodes []*StorageNode
	ps.nodes.Range(func(key, value interface{}) bool {
		node := value.(*StorageNode)
		if node.State == NodeStateHealthy {
			nodes = append(nodes, node)
		}
		return true
	})

	if len(nodes) < 2 {
		return operations
	}

	// Calculate average load
	var totalLoad float64
	for _, node := range nodes {
		totalLoad += ps.calculateNodeLoad(node)
	}
	avgLoad := totalLoad / float64(len(nodes))

	// Find overloaded and underloaded nodes
	threshold := 0.15 // 15% deviation threshold

	for _, node := range nodes {
		load := ps.calculateNodeLoad(node)
		deviation := (load - avgLoad) / avgLoad

		if deviation > threshold {
			// Node is overloaded - recommend moving data off
			for _, underloadedNode := range nodes {
				if underloadedNode.ID == node.ID {
					continue
				}

				underloadedLoad := ps.calculateNodeLoad(underloadedNode)
				underloadedDeviation := (underloadedLoad - avgLoad) / avgLoad

				if underloadedDeviation < -threshold {
					operations = append(operations, RebalanceOperation{
						Type:         RebalanceTypeMove,
						SourceNodeID: node.ID,
						TargetNodeID: underloadedNode.ID,
						Reason:       "Load balancing",
						Priority:     ps.calculateRebalancePriority(deviation),
					})
					break
				}
			}
		}
	}

	ps.logger.Debug("Generated rebalance recommendations",
		zap.Int("operation_count", len(operations)),
	)

	return operations
}

// GetClusterCapacity returns cluster-wide capacity statistics
func (ps *PlacementStrategy) GetClusterCapacity() ClusterCapacity {
	capacity := ClusterCapacity{
		LastUpdated: time.Now(),
	}

	ps.nodes.Range(func(key, value interface{}) bool {
		node := value.(*StorageNode)
		capacity.TotalNodes++

		if node.State == NodeStateHealthy {
			capacity.HealthyNodes++
		}

		capacity.TotalCapacity += node.Capacity
		capacity.TotalUsed += node.Used
		capacity.TotalAvailable += node.Available

		// Count by IOPS class
		switch node.IOPSClass {
		case IOPSClassHigh:
			capacity.HighIOPSNodes++
			capacity.HighIOPSCapacity += node.Capacity
		case IOPSClassMedium:
			capacity.MediumIOPSNodes++
			capacity.MediumIOPSCapacity += node.Capacity
		case IOPSClassLow:
			capacity.LowIOPSNodes++
			capacity.LowIOPSCapacity += node.Capacity
		}

		// Count by zone
		if _, exists := capacity.ZoneCapacity[node.Zone]; !exists {
			capacity.ZoneCapacity = make(map[string]int64)
		}
		capacity.ZoneCapacity[node.Zone] += node.Capacity

		return true
	})

	if capacity.TotalCapacity > 0 {
		capacity.UsagePercent = float64(capacity.TotalUsed) / float64(capacity.TotalCapacity) * 100
	}

	return capacity
}

// Helper Methods

// getEligibleNodes returns nodes that meet the placement requirements
func (ps *PlacementStrategy) getEligibleNodes(req *PlacementRequest) []*StorageNode {
	var eligible []*StorageNode

	ps.nodes.Range(func(key, value interface{}) bool {
		node := value.(*StorageNode)

		// Check node health
		if node.State != NodeStateHealthy {
			return true
		}

		// Check capacity
		if node.Available < req.Size {
			return true
		}

		// Check IOPS class for non-ephemeral storage
		if req.StorageClass != StorageClassEphemeral {
			if req.IOPSClass != "" && node.IOPSClass != req.IOPSClass {
				return true
			}
		}

		// Check storage class compatibility
		if !ps.isNodeCompatible(node, req.StorageClass) {
			return true
		}

		// Check excluded nodes
		if ps.isExcluded(node.ID, req.ExcludeNodes) {
			return true
		}

		eligible = append(eligible, node)
		return true
	})

	return eligible
}

// selectBestNodes selects the best nodes from eligible candidates
func (ps *PlacementStrategy) selectBestNodes(eligible []*StorageNode, req *PlacementRequest) ([]string, error) {
	// Score each node
	type scoredNode struct {
		node  *StorageNode
		score float64
	}

	var scored []scoredNode
	for _, node := range eligible {
		score := ps.calculateNodeScore(node, req)
		scored = append(scored, scoredNode{node: node, score: score})
	}

	// Sort by score (higher is better)
	sort.Slice(scored, func(i, j int) bool {
		return scored[i].score > scored[j].score
	})

	// Select top N nodes with zone diversity
	selected := make([]string, 0, req.ReplicaCount)
	usedZones := make(map[string]bool)

	// First pass: select best nodes from different zones
	for _, s := range scored {
		if len(selected) >= req.ReplicaCount {
			break
		}

		if !usedZones[s.node.Zone] || len(usedZones) >= req.ReplicaCount {
			selected = append(selected, s.node.ID)
			usedZones[s.node.Zone] = true
		}
	}

	// Second pass: fill remaining slots if needed
	for _, s := range scored {
		if len(selected) >= req.ReplicaCount {
			break
		}

		alreadySelected := false
		for _, id := range selected {
			if id == s.node.ID {
				alreadySelected = true
				break
			}
		}

		if !alreadySelected {
			selected = append(selected, s.node.ID)
		}
	}

	if len(selected) < req.ReplicaCount {
		return nil, fmt.Errorf("could not select enough nodes: got %d, need %d",
			len(selected), req.ReplicaCount)
	}

	return selected, nil
}

// calculateNodeScore calculates a placement score for a node
func (ps *PlacementStrategy) calculateNodeScore(node *StorageNode, req *PlacementRequest) float64 {
	score := 0.0

	// Available capacity (0-40 points)
	capacityRatio := float64(node.Available) / float64(node.Capacity)
	score += capacityRatio * 40

	// Load factor (0-30 points)
	load := ps.calculateNodeLoad(node)
	score += (1.0 - load) * 30

	// IOPS match (0-20 points)
	if req.IOPSClass != "" && node.IOPSClass == req.IOPSClass {
		score += 20
	}

	// Zone preference (0-10 points)
	if req.PreferredZone != "" && node.Zone == req.PreferredZone {
		score += 10
	}

	return score
}

// calculateNodeLoad calculates the load on a node (0.0 = idle, 1.0 = full)
func (ps *PlacementStrategy) calculateNodeLoad(node *StorageNode) float64 {
	if node.Capacity == 0 {
		return 1.0
	}

	return float64(node.Used) / float64(node.Capacity)
}

// calculateRebalancePriority calculates priority for rebalancing
func (ps *PlacementStrategy) calculateRebalancePriority(deviation float64) int {
	// Higher deviation = higher priority
	if deviation > 0.5 {
		return 1 // High priority
	} else if deviation > 0.3 {
		return 2 // Medium priority
	}
	return 3 // Low priority
}

// isNodeCompatible checks if a node is compatible with a storage class
func (ps *PlacementStrategy) isNodeCompatible(node *StorageNode, class StorageClass) bool {
	switch class {
	case StorageClassHot:
		// Hot storage requires high or medium IOPS
		return node.IOPSClass == IOPSClassHigh || node.IOPSClass == IOPSClassMedium
	case StorageClassCold:
		// Cold storage can use any IOPS class
		return true
	case StorageClassEphemeral:
		// Ephemeral can use any node
		return true
	default:
		return true
	}
}

// isExcluded checks if a node is in the exclusion list
func (ps *PlacementStrategy) isExcluded(nodeID string, excludeList []string) bool {
	for _, excluded := range excludeList {
		if nodeID == excluded {
			return true
		}
	}
	return false
}

// StorageNode represents a storage node in the cluster
type StorageNode struct {
	ID        string
	Zone      string
	Region    string
	Capacity  int64
	Used      int64
	Available int64
	IOPSClass IOPSClass
	State     NodeState

	// Performance metrics
	ReadLatencyMs  float64
	WriteLatencyMs float64
	ReadIOPS       int64
	WriteIOPS      int64

	// Metadata
	Labels      map[string]string
	LastUpdated time.Time
}

// NodeState represents the state of a storage node
type NodeState string

const (
	NodeStateHealthy     NodeState = "healthy"
	NodeStateDegraded    NodeState = "degraded"
	NodeStateUnavailable NodeState = "unavailable"
	NodeStateDraining    NodeState = "draining"
)

// PlacementRequest contains parameters for node selection
type PlacementRequest struct {
	StorageClass  StorageClass
	IOPSClass     IOPSClass
	ReplicaCount  int
	Size          int64
	PreferredZone string
	ExcludeNodes  []string
}

// RebalanceOperation represents a rebalancing operation
type RebalanceOperation struct {
	Type         RebalanceType
	SourceNodeID string
	TargetNodeID string
	ChunkID      string
	Reason       string
	Priority     int
}

// RebalanceType represents the type of rebalancing operation
type RebalanceType string

const (
	RebalanceTypeMove      RebalanceType = "move"
	RebalanceTypeCopy      RebalanceType = "copy"
	RebalanceTypeDelete    RebalanceType = "delete"
	RebalanceTypeReplicate RebalanceType = "replicate"
)

// ClusterCapacity contains cluster-wide capacity information
type ClusterCapacity struct {
	TotalNodes     int
	HealthyNodes   int
	TotalCapacity  int64
	TotalUsed      int64
	TotalAvailable int64
	UsagePercent   float64
	LastUpdated    time.Time

	// By IOPS class
	HighIOPSNodes      int
	MediumIOPSNodes    int
	LowIOPSNodes       int
	HighIOPSCapacity   int64
	MediumIOPSCapacity int64
	LowIOPSCapacity    int64

	// By zone
	ZoneCapacity map[string]int64
}
