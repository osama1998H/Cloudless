package overlay

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestMeshManager_RouteDiscovery verifies CLD-REQ-040: L3 routing between workloads
func TestMeshManager_RouteDiscovery(t *testing.T) {
	logger := zap.NewNop()

	// Create peer manager with test configuration
	config := MeshConfig{
		MeshType: MeshTypeFull,
		MaxPeers: 10,
	}

	peerManager := NewPeerManager("node-0", nil, config, logger)

	// Add test peers directly
	peerManager.peers.Store("peer-1", &Peer{ID: "peer-1", Status: PeerStatusConnected})
	peerManager.peers.Store("peer-2", &Peer{ID: "peer-2", Status: PeerStatusConnected})
	peerManager.peers.Store("peer-3", &Peer{ID: "peer-3", Status: PeerStatusDisconnected})

	mm := NewMeshManager("node-0", config, peerManager, logger)

	// Discover routes
	mm.discoverRoutes()

	// Verify direct routes were created for connected peers
	route1, err := mm.GetRoute("peer-1")
	assert.NoError(t, err, "Should find route to connected peer-1")
	assert.Equal(t, "peer-1", route1.Destination)
	assert.Equal(t, "peer-1", route1.NextHop, "Direct connection should use peer as next hop")
	assert.Equal(t, 1, route1.Metric, "Direct route should have metric 1")
	assert.Equal(t, "direct", route1.Interface)

	route2, err := mm.GetRoute("peer-2")
	assert.NoError(t, err, "Should find route to connected peer-2")
	assert.Equal(t, "peer-2", route2.Destination)
	assert.Equal(t, "peer-2", route2.NextHop)

	// Verify no route exists for disconnected peer
	_, err = mm.GetRoute("peer-3")
	assert.Error(t, err, "Should not have route to disconnected peer-3")

	// Verify route count
	routes := mm.GetRoutes()
	assert.Len(t, routes, 2, "Should have 2 direct routes")
}

// TestMeshManager_RouteHealing verifies route healing when next hop becomes unavailable
func TestMeshManager_RouteHealing(t *testing.T) {
	logger := zap.NewNop()

	config := MeshConfig{
		MeshType: MeshTypePartial,
		MaxPeers: 10,
	}

	peerManager := NewPeerManager("node-0", nil, config, logger)
	peer1 := &Peer{ID: "peer-1", Status: PeerStatusConnected}
	peer2 := &Peer{ID: "peer-2", Status: PeerStatusConnected}
	peerManager.peers.Store("peer-1", peer1)
	peerManager.peers.Store("peer-2", peer2)

	mm := NewMeshManager("node-0", config, peerManager, logger)

	// Create initial routes
	mm.discoverRoutes()

	// Verify initial route
	route, err := mm.GetRoute("peer-1")
	require.NoError(t, err)
	assert.Equal(t, "peer-1", route.NextHop)

	// Simulate peer-1 becoming unreachable
	peer1.Status = PeerStatusUnreachable
	peerManager.peers.Store("peer-1", peer1)

	// Add alternate route to cache (simulating route learned from peer-2)
	// The cached route has metric 2, but findAlternateRoute will increment it to 3
	alternateRoute := Route{
		Destination: "peer-1",
		NextHop:     "peer-2",
		Metric:      2,
		Interface:   "direct",
	}
	mm.cacheRoute("peer-1", alternateRoute)

	// Trigger healing
	mm.healMesh()

	// Verify alternate route was selected
	healedRoute, err := mm.GetRoute("peer-1")
	require.NoError(t, err)
	assert.Equal(t, "peer-2", healedRoute.NextHop, "Should use alternate route through peer-2")
	assert.Equal(t, 3, healedRoute.Metric, "findAlternateRoute increments metric: 2+1=3")
	assert.Equal(t, "indirect", healedRoute.Interface)
}

// TestMeshManager_IndirectRouting verifies routing through intermediate nodes
func TestMeshManager_IndirectRouting(t *testing.T) {
	logger := zap.NewNop()

	config := MeshConfig{
		MeshType: MeshTypePartial,
		MaxPeers: 10,
	}

	peerManager := NewPeerManager("node-0", nil, config, logger)
	peerManager.peers.Store("peer-1", &Peer{ID: "peer-1", Status: PeerStatusConnected})
	peerManager.peers.Store("peer-2", &Peer{ID: "peer-2", Status: PeerStatusDisconnected})

	mm := NewMeshManager("node-0", config, peerManager, logger)

	// Cache a route to peer-2 through peer-1
	indirectRoute := Route{
		Destination: "peer-2",
		NextHop:     "peer-1",
		Metric:      1,
		Interface:   "direct",
	}
	mm.cacheRoute("peer-2", indirectRoute)

	// Find alternate route to peer-2
	route := mm.findAlternateRoute("peer-2")

	require.NotNil(t, route, "Should find indirect route")
	assert.Equal(t, "peer-2", route.Destination)
	assert.Equal(t, "peer-1", route.NextHop, "Should route through peer-1")
	assert.Equal(t, 2, route.Metric, "Metric should be incremented for indirect route")
}

// TestMeshManager_GetRoutes verifies retrieving all active routes
func TestMeshManager_GetRoutes(t *testing.T) {
	logger := zap.NewNop()

	config := MeshConfig{
		MeshType: MeshTypeFull,
		MaxPeers: 10,
	}

	peerManager := NewPeerManager("node-0", nil, config, logger)
	peerManager.peers.Store("peer-1", &Peer{ID: "peer-1", Status: PeerStatusConnected})
	peerManager.peers.Store("peer-2", &Peer{ID: "peer-2", Status: PeerStatusConnected})
	peerManager.peers.Store("peer-3", &Peer{ID: "peer-3", Status: PeerStatusConnected})

	mm := NewMeshManager("node-0", config, peerManager, logger)
	mm.discoverRoutes()

	routes := mm.GetRoutes()

	assert.Len(t, routes, 3, "Should have routes to all 3 connected peers")

	// Verify each route destination is unique
	destinations := make(map[string]bool)
	for _, route := range routes {
		destinations[route.Destination] = true
		assert.Equal(t, 1, route.Metric, "All direct routes should have metric 1")
		assert.Equal(t, "direct", route.Interface)
	}

	assert.Len(t, destinations, 3, "Should have unique destinations")
	assert.True(t, destinations["peer-1"])
	assert.True(t, destinations["peer-2"])
	assert.True(t, destinations["peer-3"])
}

// TestMeshManager_TopologyTracking verifies mesh topology tracking
func TestMeshManager_TopologyTracking(t *testing.T) {
	logger := zap.NewNop()

	config := MeshConfig{
		MeshType: MeshTypeFull,
		MaxPeers: 10,
	}

	peerManager := NewPeerManager("node-0", nil, config, logger)
	peerManager.peers.Store("peer-1", &Peer{ID: "peer-1", Status: PeerStatusConnected})
	peerManager.peers.Store("peer-2", &Peer{ID: "peer-2", Status: PeerStatusConnected})

	mm := NewMeshManager("node-0", config, peerManager, logger)

	// Sync topology
	mm.syncTopology()

	// Verify local topology
	topology := mm.GetTopology()
	assert.Contains(t, topology, "node-0")

	localPeers := topology["node-0"]
	assert.Len(t, localPeers, 2, "Should have 2 connected peers in topology")
	assert.Contains(t, localPeers, "peer-1")
	assert.Contains(t, localPeers, "peer-2")

	// Update peer topology
	mm.UpdatePeerTopology("peer-1", []string{"node-0", "peer-3"})

	// Verify peer topology was stored
	topology = mm.GetTopology()
	assert.Contains(t, topology, "peer-1")
	peerTopology := topology["peer-1"]
	assert.Len(t, peerTopology, 2)
	assert.Contains(t, peerTopology, "node-0")
	assert.Contains(t, peerTopology, "peer-3")
}

// TestMeshManager_MeshStats verifies mesh statistics
func TestMeshManager_MeshStats(t *testing.T) {
	logger := zap.NewNop()

	config := MeshConfig{
		MeshType: MeshTypePartial,
		MaxPeers: 5,
	}

	peerManager := NewPeerManager("node-0", nil, config, logger)
	peerManager.peers.Store("peer-1", &Peer{ID: "peer-1", Status: PeerStatusConnected})
	peerManager.peers.Store("peer-2", &Peer{ID: "peer-2", Status: PeerStatusConnected})
	peerManager.peers.Store("peer-3", &Peer{ID: "peer-3", Status: PeerStatusDisconnected})

	mm := NewMeshManager("node-0", config, peerManager, logger)
	mm.discoverRoutes()

	stats := mm.GetMeshStats()

	assert.Equal(t, 3, stats.TotalPeers, "Should count all peers")
	assert.Equal(t, 2, stats.ConnectedPeers, "Should count only connected peers")
	assert.Equal(t, 2, stats.TotalRoutes, "Should count routes to connected peers")
	assert.Equal(t, MeshTypePartial, stats.MeshType)
	assert.Equal(t, 5, stats.MaxPeers)
}

// TestMeshManager_PeerScoring verifies peer scoring for optimal mesh topology
func TestMeshManager_PeerScoring(t *testing.T) {
	logger := zap.NewNop()

	config := MeshConfig{
		MeshType: MeshTypePartial,
		MaxPeers: 2,
	}

	mm := NewMeshManager("node-0", config, nil, logger)

	tests := []struct {
		name          string
		peer          *Peer
		expectedScore float64
		description   string
	}{
		{
			name: "high quality peer",
			peer: &Peer{
				ID:        "peer-1",
				Latency:   10 * time.Millisecond,
				Bandwidth: 100 * 1024 * 1024, // 100 MB/s
				LastSeen:  time.Now(),
			},
			expectedScore: 101.0, // Base 100 - (10ms * 0.1) + (100MB/s * 0.01) = 100 - 1 + 1 = 100
			description:   "Low latency, high bandwidth, recently seen",
		},
		{
			name: "high latency peer",
			peer: &Peer{
				ID:       "peer-2",
				Latency:  500 * time.Millisecond,
				LastSeen: time.Now(),
			},
			expectedScore: 50.0, // Base 100 - (500ms * 0.1) = 100 - 50 = 50
			description:   "High latency should reduce score",
		},
		{
			name: "stale peer",
			peer: &Peer{
				ID:       "peer-3",
				Latency:  10 * time.Millisecond,
				LastSeen: time.Now().Add(-10 * time.Minute),
			},
			expectedScore: 89.0, // Base 100 - (10ms * 0.1) - 10 (stale penalty) = 100 - 1 - 10 = 89
			description:   "Stale peer should be penalized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			score := mm.scorePeer(tt.peer)
			assert.InDelta(t, tt.expectedScore, score, 1.0, tt.description)
		})
	}
}

// TestMeshManager_RouteCache verifies route caching functionality
func TestMeshManager_RouteCache(t *testing.T) {
	logger := zap.NewNop()

	config := MeshConfig{
		MeshType: MeshTypeFull,
		MaxPeers: 10,
	}

	mm := NewMeshManager("node-0", config, nil, logger)

	// Cache multiple routes to same destination
	route1 := Route{
		Destination: "peer-1",
		NextHop:     "direct-hop",
		Metric:      1,
		Interface:   "direct",
	}
	mm.cacheRoute("peer-1", route1)

	route2 := Route{
		Destination: "peer-1",
		NextHop:     "indirect-hop",
		Metric:      2,
		Interface:   "indirect",
	}
	mm.cacheRoute("peer-1", route2)

	// Verify both routes were cached
	cached, ok := mm.routeCache.Load("peer-1")
	require.True(t, ok, "Routes should be cached")

	routes := cached.([]Route)
	assert.Len(t, routes, 2, "Should cache multiple routes to same destination")

	// Verify duplicate routes are not added
	mm.cacheRoute("peer-1", route1) // Try to add route1 again

	cached, _ = mm.routeCache.Load("peer-1")
	routes = cached.([]Route)
	assert.Len(t, routes, 2, "Should not add duplicate routes")
}
