package scheduler

import (
	"sort"

	"github.com/cloudless/cloudless/pkg/coordinator/membership"
	"go.uber.org/zap"
)

// BinPacker implements bin packing algorithms for container placement
type BinPacker struct {
	logger *zap.Logger
}

// Bin represents a node with available resources
type Bin struct {
	NodeID           string
	NodeName         string
	AvailableCPU     int64
	AvailableMemory  int64
	AvailableStorage int64
	Score            float64
}

// Item represents a workload to be packed
type Item struct {
	ID              string
	RequiredCPU     int64
	RequiredMemory  int64
	RequiredStorage int64
}

// Placement represents a placement decision
type Placement struct {
	NodeID   string
	NodeName string
	ItemID   string
	Score    float64
}

// NewBinPacker creates a new bin packer
func NewBinPacker(logger *zap.Logger) *BinPacker {
	return &BinPacker{
		logger: logger,
	}
}

// Pack implements first-fit decreasing bin packing
func (bp *BinPacker) Pack(nodes []ScoredNode, resources ResourceSpec, replicas int) []Placement {
	// Convert nodes to bins
	bins := bp.createBins(nodes)

	// Sort bins by score (best first)
	sort.Slice(bins, func(i, j int) bool {
		return bins[i].Score > bins[j].Score
	})

	// Create items for replicas
	items := []Item{}
	for i := 0; i < replicas; i++ {
		items = append(items, Item{
			ID:              string(rune(i)),
			RequiredCPU:     resources.CPUMillicores,
			RequiredMemory:  resources.MemoryBytes,
			RequiredStorage: resources.StorageBytes,
		})
	}

	// Sort items by size (largest first - First Fit Decreasing)
	sort.Slice(items, func(i, j int) bool {
		sizeI := items[i].RequiredCPU + items[i].RequiredMemory/1000000 // Convert to comparable units
		sizeJ := items[j].RequiredCPU + items[j].RequiredMemory/1000000
		return sizeI > sizeJ
	})

	// Pack items into bins
	placements := []Placement{}

	for _, item := range items {
		placed := false

		for i, bin := range bins {
			if bp.fits(item, bin) {
				// Place item in bin
				placement := Placement{
					NodeID:   bin.NodeID,
					NodeName: bin.NodeName,
					ItemID:   item.ID,
					Score:    bin.Score,
				}
				placements = append(placements, placement)

				// Update bin capacity
				bins[i].AvailableCPU -= item.RequiredCPU
				bins[i].AvailableMemory -= item.RequiredMemory
				bins[i].AvailableStorage -= item.RequiredStorage

				placed = true
				bp.logger.Debug("Placed item in bin",
					zap.String("item_id", item.ID),
					zap.String("node_id", bin.NodeID),
					zap.Int64("cpu_remaining", bins[i].AvailableCPU),
					zap.Int64("memory_remaining", bins[i].AvailableMemory),
				)
				break
			}
		}

		if !placed {
			bp.logger.Warn("Could not place item",
				zap.String("item_id", item.ID),
				zap.Int64("required_cpu", item.RequiredCPU),
				zap.Int64("required_memory", item.RequiredMemory),
			)
		}
	}

	return placements
}

// PackWithSpread implements bin packing with spread constraints
func (bp *BinPacker) PackWithSpread(nodes []ScoredNode, resources ResourceSpec, replicas int, spreadKey string) []Placement {
	// Group nodes by spread key (e.g., zone, region)
	nodeGroups := bp.groupNodes(nodes, spreadKey)

	placements := []Placement{}
	replicasPlaced := 0

	// First pass: place one replica per group
	for groupKey, groupNodes := range nodeGroups {
		if replicasPlaced >= replicas {
			break
		}

		if len(groupNodes) > 0 {
			// Pick the best node in the group
			bestNode := bp.selectBestNode(groupNodes, resources)
			if bestNode != nil {
				placement := Placement{
					NodeID:   bestNode.Node.ID,
					NodeName: bestNode.Node.Name,
					ItemID:   string(rune(replicasPlaced)),
					Score:    bestNode.Score,
				}
				placements = append(placements, placement)
				replicasPlaced++

				bp.logger.Debug("Placed replica with spread",
					zap.Int("replica", replicasPlaced),
					zap.String("group", groupKey),
					zap.String("node_id", bestNode.Node.ID),
				)
			}
		}
	}

	// Second pass: place remaining replicas
	if replicasPlaced < replicas {
		// Flatten all nodes and sort by score
		allNodes := []ScoredNode{}
		for _, groupNodes := range nodeGroups {
			allNodes = append(allNodes, groupNodes...)
		}

		sort.Slice(allNodes, func(i, j int) bool {
			return allNodes[i].Score > allNodes[j].Score
		})

		// Place remaining replicas
		for _, node := range allNodes {
			if replicasPlaced >= replicas {
				break
			}

			// Check if node already has a replica
			hasReplica := false
			for _, p := range placements {
				if p.NodeID == node.Node.ID {
					hasReplica = true
					break
				}
			}

			if !hasReplica && bp.nodeHasCapacity(node.Node, resources) {
				placement := Placement{
					NodeID:   node.Node.ID,
					NodeName: node.Node.Name,
					ItemID:   string(rune(replicasPlaced)),
					Score:    node.Score,
				}
				placements = append(placements, placement)
				replicasPlaced++
			}
		}
	}

	return placements
}

// PackBestFit implements best-fit bin packing
func (bp *BinPacker) PackBestFit(nodes []ScoredNode, resources ResourceSpec, replicas int) []Placement {
	bins := bp.createBins(nodes)
	placements := []Placement{}

	for i := 0; i < replicas; i++ {
		item := Item{
			ID:              string(rune(i)),
			RequiredCPU:     resources.CPUMillicores,
			RequiredMemory:  resources.MemoryBytes,
			RequiredStorage: resources.StorageBytes,
		}

		// Find the best fit bin (least waste)
		bestBin := -1
		minWaste := int64(1<<63 - 1) // Max int64

		for j, bin := range bins {
			if bp.fits(item, bin) {
				waste := bp.calculateWaste(item, bin)
				if waste < minWaste {
					minWaste = waste
					bestBin = j
				}
			}
		}

		if bestBin >= 0 {
			placement := Placement{
				NodeID:   bins[bestBin].NodeID,
				NodeName: bins[bestBin].NodeName,
				ItemID:   item.ID,
				Score:    bins[bestBin].Score,
			}
			placements = append(placements, placement)

			// Update bin capacity
			bins[bestBin].AvailableCPU -= item.RequiredCPU
			bins[bestBin].AvailableMemory -= item.RequiredMemory
			bins[bestBin].AvailableStorage -= item.RequiredStorage

			bp.logger.Debug("Best-fit placement",
				zap.String("item_id", item.ID),
				zap.String("node_id", bins[bestBin].NodeID),
				zap.Int64("waste", minWaste),
			)
		}
	}

	return placements
}

// createBins converts nodes to bins
func (bp *BinPacker) createBins(nodes []ScoredNode) []Bin {
	bins := []Bin{}

	for _, node := range nodes {
		bin := Bin{
			NodeID:           node.Node.ID,
			NodeName:         node.Node.Name,
			AvailableCPU:     node.Node.Capacity.CPUMillicores - node.Node.Usage.CPUMillicores,
			AvailableMemory:  node.Node.Capacity.MemoryBytes - node.Node.Usage.MemoryBytes,
			AvailableStorage: node.Node.Capacity.StorageBytes - node.Node.Usage.StorageBytes,
			Score:            node.Score,
		}
		bins = append(bins, bin)
	}

	return bins
}

// fits checks if an item fits in a bin
func (bp *BinPacker) fits(item Item, bin Bin) bool {
	return item.RequiredCPU <= bin.AvailableCPU &&
		item.RequiredMemory <= bin.AvailableMemory &&
		item.RequiredStorage <= bin.AvailableStorage
}

// calculateWaste calculates the waste if item is placed in bin
func (bp *BinPacker) calculateWaste(item Item, bin Bin) int64 {
	cpuWaste := bin.AvailableCPU - item.RequiredCPU
	memoryWaste := (bin.AvailableMemory - item.RequiredMemory) / 1000000       // Convert to MB for comparison
	storageWaste := (bin.AvailableStorage - item.RequiredStorage) / 1000000000 // Convert to GB

	// Weighted sum of waste
	return cpuWaste + memoryWaste + storageWaste
}

// groupNodes groups nodes by a spread key
func (bp *BinPacker) groupNodes(nodes []ScoredNode, spreadKey string) map[string][]ScoredNode {
	groups := make(map[string][]ScoredNode)

	for _, node := range nodes {
		var key string
		switch spreadKey {
		case "zone":
			key = node.Node.Zone
		case "region":
			key = node.Node.Region
		case "node":
			key = node.Node.ID
		default:
			key = "default"
		}

		groups[key] = append(groups[key], node)
	}

	// Sort each group by score
	for _, group := range groups {
		sort.Slice(group, func(i, j int) bool {
			return group[i].Score > group[j].Score
		})
	}

	return groups
}

// selectBestNode selects the best node from a group that has capacity
func (bp *BinPacker) selectBestNode(nodes []ScoredNode, resources ResourceSpec) *ScoredNode {
	for _, node := range nodes {
		if bp.nodeHasCapacity(node.Node, resources) {
			return &node
		}
	}
	return nil
}

// nodeHasCapacity checks if a node has enough capacity for resources
func (bp *BinPacker) nodeHasCapacity(node *membership.NodeInfo, resources ResourceSpec) bool {
	availableCPU := node.Capacity.CPUMillicores - node.Usage.CPUMillicores
	availableMemory := node.Capacity.MemoryBytes - node.Usage.MemoryBytes
	availableStorage := node.Capacity.StorageBytes - node.Usage.StorageBytes

	return availableCPU >= resources.CPUMillicores &&
		availableMemory >= resources.MemoryBytes &&
		availableStorage >= resources.StorageBytes
}

// OptimizePacking optimizes the packing of existing placements
func (bp *BinPacker) OptimizePacking(currentPlacements []Placement, nodes []ScoredNode, resources ResourceSpec) []Placement {
	// This would implement defragmentation/rebalancing logic
	// For now, return current placements
	return currentPlacements
}

// EstimateFragmentation estimates the fragmentation level
func (bp *BinPacker) EstimateFragmentation(nodes []ScoredNode) float64 {
	totalCapacityCPU := int64(0)
	totalUsedCPU := int64(0)
	totalCapacityMem := int64(0)
	totalUsedMem := int64(0)

	fragmentedCPU := int64(0)
	fragmentedMem := int64(0)

	for _, node := range nodes {
		totalCapacityCPU += node.Node.Capacity.CPUMillicores
		totalUsedCPU += node.Node.Usage.CPUMillicores
		totalCapacityMem += node.Node.Capacity.MemoryBytes
		totalUsedMem += node.Node.Usage.MemoryBytes

		// Calculate fragmentation (unused but too small to fit standard workloads)
		availableCPU := node.Node.Capacity.CPUMillicores - node.Node.Usage.CPUMillicores
		availableMem := node.Node.Capacity.MemoryBytes - node.Node.Usage.MemoryBytes

		// Consider fragmented if less than 250m CPU or 256Mi memory available
		if availableCPU > 0 && availableCPU < 250 {
			fragmentedCPU += availableCPU
		}
		if availableMem > 0 && availableMem < 256*1024*1024 {
			fragmentedMem += availableMem
		}
	}

	// Calculate fragmentation percentage
	totalAvailableCPU := totalCapacityCPU - totalUsedCPU
	totalAvailableMem := totalCapacityMem - totalUsedMem

	cpuFragmentation := float64(0)
	if totalAvailableCPU > 0 {
		cpuFragmentation = float64(fragmentedCPU) / float64(totalAvailableCPU)
	}

	memFragmentation := float64(0)
	if totalAvailableMem > 0 {
		memFragmentation = float64(fragmentedMem) / float64(totalAvailableMem)
	}

	// Return average fragmentation
	return (cpuFragmentation + memFragmentation) / 2
}
