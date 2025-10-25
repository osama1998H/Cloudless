package overlay

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// MeshManager manages the mesh topology
type MeshManager struct {
	nodeID      string
	config      MeshConfig
	peerManager *PeerManager
	logger      *zap.Logger

	routes      sync.Map // destination -> Route
	topology    sync.Map // peerID -> []string (connected peers)
	routeCache  sync.Map // destination -> []Route (all known routes)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewMeshManager creates a new mesh manager
func NewMeshManager(nodeID string, config MeshConfig, peerManager *PeerManager, logger *zap.Logger) *MeshManager {
	ctx, cancel := context.WithCancel(context.Background())

	return &MeshManager{
		nodeID:      nodeID,
		config:      config,
		peerManager: peerManager,
		logger:      logger,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Start starts the mesh manager
func (mm *MeshManager) Start() error {
	mm.logger.Info("Starting mesh manager",
		zap.String("node_id", mm.nodeID),
		zap.String("mesh_type", string(mm.config.MeshType)),
		zap.Int("max_peers", mm.config.MaxPeers),
	)

	// Start topology maintenance loop
	mm.wg.Add(1)
	go mm.topologyMaintenanceLoop()

	// Start route discovery loop
	mm.wg.Add(1)
	go mm.routeDiscoveryLoop()

	// Start topology sync loop
	mm.wg.Add(1)
	go mm.topologySyncLoop()

	return nil
}

// Stop stops the mesh manager
func (mm *MeshManager) Stop() error {
	mm.logger.Info("Stopping mesh manager")

	mm.cancel()
	mm.wg.Wait()

	mm.logger.Info("Mesh manager stopped")
	return nil
}

// topologyMaintenanceLoop maintains the mesh topology
func (mm *MeshManager) topologyMaintenanceLoop() {
	defer mm.wg.Done()

	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-mm.ctx.Done():
			return
		case <-ticker.C:
			mm.maintainTopology()
		}
	}
}

// maintainTopology ensures the mesh topology is healthy
func (mm *MeshManager) maintainTopology() {
	peers := mm.peerManager.ListPeers()
	connectedCount := mm.peerManager.GetConnectedPeerCount()

	mm.logger.Debug("Maintaining topology",
		zap.Int("total_peers", len(peers)),
		zap.Int("connected_peers", connectedCount),
	)

	switch mm.config.MeshType {
	case MeshTypeFull:
		mm.maintainFullMesh(peers)
	case MeshTypePartial:
		mm.maintainPartialMesh(peers)
	}

	// Heal broken connections
	mm.healMesh()
}

// maintainFullMesh ensures we're connected to all peers
func (mm *MeshManager) maintainFullMesh(peers []*Peer) {
	for _, peer := range peers {
		if peer.Status != PeerStatusConnected && peer.Status != PeerStatusConnecting {
			mm.logger.Debug("Peer not connected in full mesh",
				zap.String("peer_id", peer.ID),
				zap.String("status", string(peer.Status)),
			)
			// PeerManager will handle reconnection
		}
	}
}

// maintainPartialMesh ensures we're connected to optimal subset of peers
func (mm *MeshManager) maintainPartialMesh(peers []*Peer) {
	connectedCount := mm.peerManager.GetConnectedPeerCount()

	// If below max peers, try to connect to more
	if connectedCount < mm.config.MaxPeers && len(peers) > connectedCount {
		// Find best candidates based on latency, region, etc.
		candidates := mm.findBestPeerCandidates(peers, mm.config.MaxPeers-connectedCount)

		for _, peer := range candidates {
			if peer.Status != PeerStatusConnected && peer.Status != PeerStatusConnecting {
				mm.logger.Info("Connecting to peer in partial mesh",
					zap.String("peer_id", peer.ID),
				)
				mm.peerManager.AddPeer(peer)
			}
		}
	}

	// If above max peers, consider disconnecting less optimal peers
	if connectedCount > mm.config.MaxPeers {
		mm.pruneExcessPeers(peers, connectedCount-mm.config.MaxPeers)
	}
}

// findBestPeerCandidates finds the best peers to connect to
func (mm *MeshManager) findBestPeerCandidates(peers []*Peer, count int) []*Peer {
	// Score peers based on latency, region, and connectivity
	type scoredPeer struct {
		peer  *Peer
		score float64
	}

	var scored []scoredPeer
	for _, peer := range peers {
		if peer.Status == PeerStatusConnected || peer.Status == PeerStatusConnecting {
			continue
		}

		score := mm.scorePeer(peer)
		scored = append(scored, scoredPeer{peer: peer, score: score})
	}

	// Sort by score (higher is better)
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score > scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	// Return top candidates
	var candidates []*Peer
	for i := 0; i < len(scored) && i < count; i++ {
		candidates = append(candidates, scored[i].peer)
	}

	return candidates
}

// scorePeer scores a peer for connection priority
func (mm *MeshManager) scorePeer(peer *Peer) float64 {
	score := 100.0

	// Prefer low latency
	if peer.Latency > 0 {
		latencyMs := float64(peer.Latency.Milliseconds())
		score -= latencyMs * 0.1 // Subtract 0.1 points per ms
	}

	// Prefer peers in same region
	if peer.Region != "" {
		// Would compare with local node's region
		// For now, no penalty
	}

	// Prefer recently seen peers
	timeSinceLastSeen := time.Since(peer.LastSeen)
	if timeSinceLastSeen > 5*time.Minute {
		score -= 10.0
	}

	// Prefer high bandwidth peers
	if peer.Bandwidth > 0 {
		bandwidthMbps := float64(peer.Bandwidth) / (1024 * 1024)
		score += bandwidthMbps * 0.01
	}

	return score
}

// pruneExcessPeers disconnects less optimal peers
func (mm *MeshManager) pruneExcessPeers(peers []*Peer, count int) {
	// Find connected peers with lowest scores
	var connectedPeers []*Peer
	for _, peer := range peers {
		if peer.Status == PeerStatusConnected {
			connectedPeers = append(connectedPeers, peer)
		}
	}

	// Score and sort
	type scoredPeer struct {
		peer  *Peer
		score float64
	}

	var scored []scoredPeer
	for _, peer := range connectedPeers {
		score := mm.scorePeer(peer)
		scored = append(scored, scoredPeer{peer: peer, score: score})
	}

	// Sort by score (lower is worse)
	for i := 0; i < len(scored); i++ {
		for j := i + 1; j < len(scored); j++ {
			if scored[j].score < scored[i].score {
				scored[i], scored[j] = scored[j], scored[i]
			}
		}
	}

	// Disconnect worst peers
	for i := 0; i < len(scored) && i < count; i++ {
		mm.logger.Info("Pruning excess peer",
			zap.String("peer_id", scored[i].peer.ID),
			zap.Float64("score", scored[i].score),
		)
		mm.peerManager.RemovePeer(scored[i].peer.ID)
	}
}

// healMesh attempts to heal broken mesh connections
func (mm *MeshManager) healMesh() {
	// Check for unreachable peers and try alternate routes
	mm.routes.Range(func(key, value interface{}) bool {
		dest := key.(string)
		route := value.(Route)

		// Check if next hop is reachable
		peer, err := mm.peerManager.GetPeer(route.NextHop)
		if err != nil || peer.Status != PeerStatusConnected {
			mm.logger.Debug("Route broken, finding alternate",
				zap.String("destination", dest),
				zap.String("next_hop", route.NextHop),
			)

			// Try to find alternate route
			if alternateRoute := mm.findAlternateRoute(dest); alternateRoute != nil {
				mm.routes.Store(dest, *alternateRoute)
				mm.logger.Info("Found alternate route",
					zap.String("destination", dest),
					zap.String("new_next_hop", alternateRoute.NextHop),
				)
			}
		}

		return true
	})
}

// routeDiscoveryLoop discovers and updates routes
func (mm *MeshManager) routeDiscoveryLoop() {
	defer mm.wg.Done()

	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-mm.ctx.Done():
			return
		case <-ticker.C:
			mm.discoverRoutes()
		}
	}
}

// discoverRoutes discovers routes to all peers
func (mm *MeshManager) discoverRoutes() {
	peers := mm.peerManager.ListPeers()

	for _, peer := range peers {
		if peer.Status == PeerStatusConnected {
			// Direct connection - create direct route
			route := Route{
				Destination: peer.ID,
				NextHop:     peer.ID,
				Metric:      1,
				Interface:   "direct",
			}
			mm.routes.Store(peer.ID, route)

			// Cache all known routes to this peer
			mm.cacheRoute(peer.ID, route)
		} else {
			// No direct connection - try to find indirect route
			if route := mm.findAlternateRoute(peer.ID); route != nil {
				mm.routes.Store(peer.ID, *route)
			}
		}
	}

	mm.logger.Debug("Route discovery completed",
		zap.Int("total_routes", mm.countRoutes()),
	)
}

// findAlternateRoute finds an alternate route to a destination
func (mm *MeshManager) findAlternateRoute(dest string) *Route {
	// Try to find a path through connected peers
	// This is a simplified implementation - would use proper pathfinding in production

	connectedPeers := mm.getConnectedPeers()

	for _, peerID := range connectedPeers {
		// Check if this peer has a route to destination
		if routes, ok := mm.routeCache.Load(dest); ok {
			routeList := routes.([]Route)
			for _, route := range routeList {
				if route.NextHop == peerID {
					// Found a route through this peer
					return &Route{
						Destination: dest,
						NextHop:     peerID,
						Metric:      route.Metric + 1,
						Interface:   "indirect",
					}
				}
			}
		}
	}

	return nil
}

// cacheRoute caches a route for future reference
func (mm *MeshManager) cacheRoute(dest string, route Route) {
	var routes []Route
	if r, ok := mm.routeCache.Load(dest); ok {
		routes = r.([]Route)
	}

	// Add route if not already cached
	found := false
	for _, r := range routes {
		if r.NextHop == route.NextHop && r.Metric == route.Metric {
			found = true
			break
		}
	}

	if !found {
		routes = append(routes, route)
		mm.routeCache.Store(dest, routes)
	}
}

// topologySyncLoop syncs topology information with peers
func (mm *MeshManager) topologySyncLoop() {
	defer mm.wg.Done()

	ticker := time.NewTicker(45 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-mm.ctx.Done():
			return
		case <-ticker.C:
			mm.syncTopology()
		}
	}
}

// syncTopology syncs topology information with connected peers
func (mm *MeshManager) syncTopology() {
	// Get current topology
	topology := mm.getCurrentTopology()

	// Store local topology
	mm.topology.Store(mm.nodeID, topology)

	// In a full implementation, would send topology updates to peers
	// For now, just log
	mm.logger.Debug("Topology sync",
		zap.Int("connected_peers", len(topology)),
	)
}

// getCurrentTopology returns the current list of connected peers
func (mm *MeshManager) getCurrentTopology() []string {
	var connected []string

	peers := mm.peerManager.ListPeers()
	for _, peer := range peers {
		if peer.Status == PeerStatusConnected {
			connected = append(connected, peer.ID)
		}
	}

	return connected
}

// getConnectedPeers returns list of connected peer IDs
func (mm *MeshManager) getConnectedPeers() []string {
	return mm.getCurrentTopology()
}

// countRoutes counts the number of active routes
func (mm *MeshManager) countRoutes() int {
	count := 0
	mm.routes.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// GetRoute returns the route to a destination
func (mm *MeshManager) GetRoute(dest string) (*Route, error) {
	if r, ok := mm.routes.Load(dest); ok {
		route := r.(Route)
		return &route, nil
	}

	return nil, fmt.Errorf("no route to destination: %s", dest)
}

// GetRoutes returns all active routes
func (mm *MeshManager) GetRoutes() []Route {
	var routes []Route

	mm.routes.Range(func(key, value interface{}) bool {
		route := value.(Route)
		routes = append(routes, route)
		return true
	})

	return routes
}

// GetTopology returns the current mesh topology
func (mm *MeshManager) GetTopology() map[string][]string {
	topology := make(map[string][]string)

	mm.topology.Range(func(key, value interface{}) bool {
		nodeID := key.(string)
		peers := value.([]string)
		topology[nodeID] = peers
		return true
	})

	return topology
}

// UpdatePeerTopology updates topology information from a peer
func (mm *MeshManager) UpdatePeerTopology(peerID string, connectedPeers []string) {
	mm.topology.Store(peerID, connectedPeers)

	mm.logger.Debug("Updated peer topology",
		zap.String("peer_id", peerID),
		zap.Int("peer_connections", len(connectedPeers)),
	)

	// Trigger route recalculation
	go mm.discoverRoutes()
}

// GetMeshStats returns mesh statistics
func (mm *MeshManager) GetMeshStats() MeshStats {
	peers := mm.peerManager.ListPeers()
	connectedCount := mm.peerManager.GetConnectedPeerCount()

	return MeshStats{
		TotalPeers:     len(peers),
		ConnectedPeers: connectedCount,
		TotalRoutes:    mm.countRoutes(),
		MeshType:       mm.config.MeshType,
		MaxPeers:       mm.config.MaxPeers,
	}
}

// MeshStats contains mesh statistics
type MeshStats struct {
	TotalPeers     int
	ConnectedPeers int
	TotalRoutes    int
	MeshType       MeshType
	MaxPeers       int
}
