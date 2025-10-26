package overlay

import (
	"context"
	"fmt"
	"sync"
	"time"

	"go.uber.org/zap"
)

// PeerManager manages peers in the overlay network
type PeerManager struct {
	nodeID       string
	transport    *QUICTransport
	logger       *zap.Logger
	natTraversal *NATTraversal // NAT traversal handler

	peers       sync.Map // peerID -> *Peer
	connections sync.Map // peerID -> Connection

	healthCheckInterval time.Duration
	reconnectInterval   time.Duration
	connectionTimeout   time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewPeerManager creates a new peer manager
func NewPeerManager(nodeID string, transport *QUICTransport, config MeshConfig, logger *zap.Logger) *PeerManager {
	ctx, cancel := context.WithCancel(context.Background())

	// Set default intervals if not configured
	healthCheckInterval := config.HealthCheckInterval
	if healthCheckInterval == 0 {
		healthCheckInterval = 30 * time.Second
	}

	reconnectInterval := config.ReconnectInterval
	if reconnectInterval == 0 {
		reconnectInterval = 60 * time.Second
	}

	connectionTimeout := config.ConnectionTimeout
	if connectionTimeout == 0 {
		connectionTimeout = 10 * time.Second
	}

	return &PeerManager{
		nodeID:              nodeID,
		transport:           transport,
		logger:              logger,
		healthCheckInterval: healthCheckInterval,
		reconnectInterval:   reconnectInterval,
		connectionTimeout:   connectionTimeout,
		ctx:                 ctx,
		cancel:              cancel,
	}
}

// SetNATTraversal sets the NAT traversal handler for this peer manager
func (pm *PeerManager) SetNATTraversal(natTraversal *NATTraversal) {
	pm.natTraversal = natTraversal
	pm.logger.Info("NAT traversal enabled for peer manager")
}

// Start starts the peer manager
func (pm *PeerManager) Start() error {
	pm.logger.Info("Starting peer manager")

	// Start health check loop
	pm.wg.Add(1)
	go pm.healthCheckLoop()

	// Start reconnect loop
	pm.wg.Add(1)
	go pm.reconnectLoop()

	return nil
}

// Stop stops the peer manager
func (pm *PeerManager) Stop() error {
	pm.logger.Info("Stopping peer manager")

	pm.cancel()
	pm.wg.Wait()

	// Close all connections
	pm.connections.Range(func(key, value interface{}) bool {
		conn := value.(Connection)
		conn.Close()
		return true
	})

	pm.logger.Info("Peer manager stopped")
	return nil
}

// AddPeer adds a new peer
func (pm *PeerManager) AddPeer(peer *Peer) error {
	pm.logger.Info("Adding peer",
		zap.String("peer_id", peer.ID),
		zap.String("address", peer.Address),
	)

	peer.LastSeen = time.Now()
	pm.peers.Store(peer.ID, peer)

	// Attempt to connect to the peer
	go pm.connectToPeer(peer)

	return nil
}

// RemovePeer removes a peer
func (pm *PeerManager) RemovePeer(peerID string) error {
	pm.logger.Info("Removing peer", zap.String("peer_id", peerID))

	// Close connection if exists
	if conn, ok := pm.connections.Load(peerID); ok {
		conn.(Connection).Close()
		pm.connections.Delete(peerID)
	}

	pm.peers.Delete(peerID)

	return nil
}

// GetPeer retrieves a peer by ID
func (pm *PeerManager) GetPeer(peerID string) (*Peer, error) {
	if p, ok := pm.peers.Load(peerID); ok {
		peer := p.(*Peer)
		return peer, nil
	}
	return nil, fmt.Errorf("peer not found: %s", peerID)
}

// ListPeers returns all peers
func (pm *PeerManager) ListPeers() []*Peer {
	var peers []*Peer
	pm.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		peers = append(peers, peer)
		return true
	})
	return peers
}

// GetConnection retrieves a connection to a peer
func (pm *PeerManager) GetConnection(peerID string) (Connection, error) {
	if conn, ok := pm.connections.Load(peerID); ok {
		connection := conn.(Connection)
		if !connection.IsClosed() {
			return connection, nil
		}
		// Connection is closed, remove it
		pm.connections.Delete(peerID)
	}

	// Try to establish a new connection
	peer, err := pm.GetPeer(peerID)
	if err != nil {
		return nil, err
	}

	return pm.connectToPeerSync(peer)
}

// connectToPeer attempts to connect to a peer asynchronously
func (pm *PeerManager) connectToPeer(peer *Peer) {
	_, err := pm.connectToPeerSync(peer)
	if err != nil {
		pm.logger.Warn("Failed to connect to peer",
			zap.String("peer_id", peer.ID),
			zap.Error(err),
		)
		pm.updatePeerStatus(peer.ID, PeerStatusUnreachable)
	}
}

// connectToPeerSync attempts to connect to a peer synchronously
func (pm *PeerManager) connectToPeerSync(peer *Peer) (Connection, error) {
	// Check if already connected
	if conn, ok := pm.connections.Load(peer.ID); ok {
		connection := conn.(Connection)
		if !connection.IsClosed() {
			return connection, nil
		}
		pm.connections.Delete(peer.ID)
	}

	pm.updatePeerStatus(peer.ID, PeerStatusConnecting)

	ctx, cancel := context.WithTimeout(pm.ctx, pm.connectionTimeout)
	defer cancel()

	// Determine effective address to use for connection
	addr, strategy, err := pm.determineConnectionAddress(ctx, peer)
	if err != nil {
		pm.logger.Warn("Failed to determine connection address, using direct connection",
			zap.String("peer_id", peer.ID),
			zap.Error(err),
		)
		addr = fmt.Sprintf("%s:%d", peer.Address, peer.Port)
		strategy = "direct"
	}

	pm.logger.Debug("Connecting to peer",
		zap.String("peer_id", peer.ID),
		zap.String("address", addr),
		zap.String("strategy", strategy),
	)

	conn, err := pm.transport.Connect(ctx, peer.ID, addr)
	if err != nil {
		pm.updatePeerStatus(peer.ID, PeerStatusUnreachable)
		return nil, fmt.Errorf("failed to connect to peer %s: %w", peer.ID, err)
	}

	pm.connections.Store(peer.ID, conn)
	pm.updatePeerStatus(peer.ID, PeerStatusConnected)

	pm.logger.Info("Connected to peer",
		zap.String("peer_id", peer.ID),
		zap.String("address", addr),
		zap.String("strategy", strategy),
	)

	// Start monitoring connection
	pm.wg.Add(1)
	go pm.monitorConnection(peer.ID, conn)

	return conn, nil
}

// determineConnectionAddress determines the best address to use for connecting to a peer
// Returns: (address, strategy, error)
func (pm *PeerManager) determineConnectionAddress(ctx context.Context, peer *Peer) (string, string, error) {
	// If NAT traversal is not enabled, use direct connection
	if pm.natTraversal == nil {
		return fmt.Sprintf("%s:%d", peer.Address, peer.Port), "direct", nil
	}

	// Get local NAT info
	localNATInfo := pm.natTraversal.GetNATInfo()
	if localNATInfo == nil {
		pm.logger.Debug("Local NAT info not available, using direct connection")
		return fmt.Sprintf("%s:%d", peer.Address, peer.Port), "direct", nil
	}

	// Determine peer's public address
	peerPublicAddr := peer.PublicIP
	if peerPublicAddr == "" {
		peerPublicAddr = peer.Address
	}
	peerAddr := fmt.Sprintf("%s:%d", peerPublicAddr, peer.Port)

	// Determine peer NAT type (use stored info if available, otherwise assume port-restricted)
	peerNATType := peer.NATType
	if peerNATType == "" {
		peerNATType = NATTypePortRestrictedCone // Conservative default
	}

	pm.logger.Debug("Establishing NAT traversal connection",
		zap.String("peer_id", peer.ID),
		zap.String("local_nat", string(localNATInfo.Type)),
		zap.String("peer_nat", string(peerNATType)),
	)

	// Use NAT traversal to establish connection
	localAddr := pm.transport.LocalAddr()
	connInfo, err := pm.natTraversal.EstablishConnection(ctx, localAddr, peerAddr, peerNATType)
	if err != nil {
		return "", "", fmt.Errorf("NAT traversal failed: %w", err)
	}

	// Return the effective address based on NAT traversal strategy
	effectiveAddr := connInfo.GetEffectiveAddr()
	return effectiveAddr, string(connInfo.Strategy), nil
}

// monitorConnection monitors a connection and handles disconnections
func (pm *PeerManager) monitorConnection(peerID string, conn Connection) {
	defer pm.wg.Done()

	// Wait for connection to close
	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-time.After(5 * time.Second):
			if conn.IsClosed() {
				pm.logger.Info("Connection closed",
					zap.String("peer_id", peerID),
				)
				pm.connections.Delete(peerID)
				pm.updatePeerStatus(peerID, PeerStatusDisconnected)
				return
			}
		}
	}
}

// healthCheckLoop periodically checks the health of all peers
func (pm *PeerManager) healthCheckLoop() {
	defer pm.wg.Done()

	ticker := time.NewTicker(pm.healthCheckInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.performHealthChecks()
		}
	}
}

// performHealthChecks performs health checks on all connected peers
func (pm *PeerManager) performHealthChecks() {
	pm.connections.Range(func(key, value interface{}) bool {
		peerID := key.(string)
		conn := value.(Connection)

		if conn.IsClosed() {
			pm.connections.Delete(peerID)
			pm.updatePeerStatus(peerID, PeerStatusDisconnected)
			return true
		}

		// Perform ping
		go pm.pingPeer(peerID, conn)

		return true
	})
}

// pingPeer sends a ping to a peer and measures latency
func (pm *PeerManager) pingPeer(peerID string, conn Connection) {
	ctx, cancel := context.WithTimeout(pm.ctx, 5*time.Second)
	defer cancel()

	start := time.Now()

	// Open a stream and send a ping
	stream, err := conn.OpenStream(ctx)
	if err != nil {
		pm.logger.Debug("Failed to open stream for ping",
			zap.String("peer_id", peerID),
			zap.Error(err),
		)
		pm.updatePeerStatus(peerID, PeerStatusUnreachable)
		return
	}
	defer stream.Close()

	// Send ping
	if _, err := stream.Write([]byte("PING")); err != nil {
		pm.logger.Debug("Failed to send ping",
			zap.String("peer_id", peerID),
			zap.Error(err),
		)
		pm.updatePeerStatus(peerID, PeerStatusUnreachable)
		return
	}

	// Read response
	buf := make([]byte, 4)
	if _, err := stream.Read(buf); err != nil {
		pm.logger.Debug("Failed to read ping response",
			zap.String("peer_id", peerID),
			zap.Error(err),
		)
		pm.updatePeerStatus(peerID, PeerStatusUnreachable)
		return
	}

	latency := time.Since(start)

	// Update peer latency
	if p, ok := pm.peers.Load(peerID); ok {
		peer := p.(*Peer)
		peer.Latency = latency
		peer.LastSeen = time.Now()
		pm.peers.Store(peerID, peer)
	}

	pm.logger.Debug("Ping successful",
		zap.String("peer_id", peerID),
		zap.Duration("latency", latency),
	)
}

// reconnectLoop periodically attempts to reconnect to disconnected peers
func (pm *PeerManager) reconnectLoop() {
	defer pm.wg.Done()

	ticker := time.NewTicker(pm.reconnectInterval)
	defer ticker.Stop()

	for {
		select {
		case <-pm.ctx.Done():
			return
		case <-ticker.C:
			pm.attemptReconnections()
		}
	}
}

// attemptReconnections attempts to reconnect to disconnected peers
func (pm *PeerManager) attemptReconnections() {
	pm.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)

		// Skip if already connected
		if peer.Status == PeerStatusConnected || peer.Status == PeerStatusConnecting {
			return true
		}

		// Attempt reconnection
		go pm.connectToPeer(peer)

		return true
	})
}

// updatePeerStatus updates the status of a peer
func (pm *PeerManager) updatePeerStatus(peerID string, status PeerStatus) {
	if p, ok := pm.peers.Load(peerID); ok {
		peer := p.(*Peer)
		peer.Status = status
		peer.LastSeen = time.Now()
		pm.peers.Store(peerID, peer)

		pm.logger.Debug("Peer status updated",
			zap.String("peer_id", peerID),
			zap.String("status", string(status)),
		)
	}
}

// GetPeerCount returns the number of peers
func (pm *PeerManager) GetPeerCount() int {
	count := 0
	pm.peers.Range(func(key, value interface{}) bool {
		count++
		return true
	})
	return count
}

// GetConnectedPeerCount returns the number of connected peers
func (pm *PeerManager) GetConnectedPeerCount() int {
	count := 0
	pm.peers.Range(func(key, value interface{}) bool {
		peer := value.(*Peer)
		if peer.Status == PeerStatusConnected {
			count++
		}
		return true
	})
	return count
}

// UpdatePeerMetadata updates metadata for a peer
func (pm *PeerManager) UpdatePeerMetadata(peerID string, metadata map[string]string) error {
	peer, err := pm.GetPeer(peerID)
	if err != nil {
		return err
	}

	if peer.Metadata == nil {
		peer.Metadata = make(map[string]string)
	}

	for k, v := range metadata {
		peer.Metadata[k] = v
	}

	pm.peers.Store(peerID, peer)

	return nil
}
