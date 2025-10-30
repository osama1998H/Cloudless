package overlay

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/osama1998H/Cloudless/pkg/observability"
	"go.uber.org/zap"
)

// NATTraversal handles NAT traversal using STUN/TURN
type NATTraversal struct {
	config NATConfig
	logger *zap.Logger

	stunClient *STUNClient
	turnClient *TURNClient

	natInfo      *NATInfo
	natInfoMu    sync.RWMutex
	allocation   *RelayAllocation
	allocationMu sync.RWMutex

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewNATTraversal creates a new NAT traversal handler
func NewNATTraversal(config NATConfig, logger *zap.Logger) *NATTraversal {
	ctx, cancel := context.WithCancel(context.Background())

	return &NATTraversal{
		config:     config,
		logger:     logger,
		stunClient: NewSTUNClient(config.STUNServers, logger),
		turnClient: NewTURNClient(config.TURNServers, logger),
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Start starts NAT traversal
func (nt *NATTraversal) Start(localAddr string) error {
	nt.logger.Info("Starting NAT traversal",
		zap.String("local_addr", localAddr),
	)

	// Discover NAT type using STUN
	if err := nt.discoverNAT(localAddr); err != nil {
		nt.logger.Warn("STUN discovery failed, will try TURN", zap.Error(err))
	}

	// Start refresh loop for TURN allocations
	nt.wg.Add(1)
	go nt.refreshLoop()

	return nil
}

// Stop stops NAT traversal
func (nt *NATTraversal) Stop() error {
	nt.logger.Info("Stopping NAT traversal")

	nt.cancel()
	nt.wg.Wait()

	// Close TURN allocation if exists
	nt.allocationMu.Lock()
	if nt.allocation != nil {
		nt.allocation.Close()
		nt.allocation = nil
	}
	nt.allocationMu.Unlock()

	// Close TURN client
	if err := nt.turnClient.Close(); err != nil {
		nt.logger.Error("Failed to close TURN client", zap.Error(err))
	}

	nt.logger.Info("NAT traversal stopped")
	return nil
}

// discoverNAT discovers NAT information
func (nt *NATTraversal) discoverNAT(localAddr string) error {
	ctx, cancel := context.WithTimeout(nt.ctx, 10*time.Second)
	defer cancel()

	natInfo, err := nt.stunClient.DiscoverNATInfo(ctx, localAddr)
	if err != nil {
		return fmt.Errorf("failed to discover NAT: %w", err)
	}

	nt.natInfoMu.Lock()
	nt.natInfo = natInfo
	nt.natInfoMu.Unlock()

	nt.logger.Info("NAT discovered",
		zap.String("type", string(natInfo.Type)),
		zap.String("public_ip", natInfo.PublicIP),
		zap.Int("public_port", natInfo.PublicPort),
	)

	return nil
}

// GetNATInfo returns current NAT information
func (nt *NATTraversal) GetNATInfo() *NATInfo {
	nt.natInfoMu.RLock()
	defer nt.natInfoMu.RUnlock()

	if nt.natInfo == nil {
		return nil
	}

	// Return a copy
	info := *nt.natInfo
	return &info
}

// GetPublicEndpoint returns the public endpoint
func (nt *NATTraversal) GetPublicEndpoint() (string, error) {
	natInfo := nt.GetNATInfo()
	if natInfo == nil {
		return "", fmt.Errorf("NAT info not available")
	}

	return fmt.Sprintf("%s:%d", natInfo.PublicIP, natInfo.PublicPort), nil
}

// EstablishConnection establishes a connection to a peer through NAT
func (nt *NATTraversal) EstablishConnection(ctx context.Context, localAddr, peerPublicAddr string, peerNATType NATType) (*ConnectionInfo, error) {
	nt.logger.Info("Establishing NAT traversal connection",
		zap.String("peer_addr", peerPublicAddr),
		zap.String("peer_nat_type", string(peerNATType)),
	)

	natInfo := nt.GetNATInfo()
	if natInfo == nil {
		return nil, fmt.Errorf("NAT info not available")
	}

	// Determine connection strategy based on NAT types
	strategy := nt.determineStrategy(natInfo.Type, peerNATType)

	nt.logger.Debug("Using NAT traversal strategy",
		zap.String("strategy", string(strategy)),
	)

	switch strategy {
	case StrategyDirect:
		return nt.establishDirectConnection(ctx, localAddr, peerPublicAddr)

	case StrategyHolePunch:
		return nt.establishHolePunchConnection(ctx, localAddr, peerPublicAddr)

	case StrategyRelay:
		return nt.establishRelayConnection(ctx, localAddr, peerPublicAddr)

	default:
		return nil, fmt.Errorf("unknown NAT traversal strategy: %s", strategy)
	}
}

// determineStrategy determines the NAT traversal strategy
func (nt *NATTraversal) determineStrategy(localNAT, peerNAT NATType) NATTraversalStrategy {
	// No NAT - direct connection
	if localNAT == NATTypeNone && peerNAT == NATTypeNone {
		return StrategyDirect
	}

	// CLD-REQ-003: Symmetric NAT always requires relay (check first, takes priority)
	// Symmetric NAT cannot do hole punching regardless of peer NAT type
	if localNAT == NATTypeSymmetric || peerNAT == NATTypeSymmetric {
		return StrategyRelay
	}

	// Full cone NAT - can do hole punching
	if localNAT == NATTypeFullCone || peerNAT == NATTypeFullCone {
		return StrategyHolePunch
	}

	// Port-restricted cone - try hole punching
	if localNAT == NATTypePortRestrictedCone || peerNAT == NATTypePortRestrictedCone {
		return StrategyHolePunch
	}

	// Default to relay for safety
	return StrategyRelay
}

// establishDirectConnection establishes a direct connection
func (nt *NATTraversal) establishDirectConnection(ctx context.Context, localAddr, peerAddr string) (*ConnectionInfo, error) {
	nt.logger.Debug("Establishing direct connection",
		zap.String("peer_addr", peerAddr),
	)

	// Test connectivity
	if err := nt.stunClient.TestConnectivity(ctx, localAddr, peerAddr); err != nil {
		// CLD-REQ-003: Emit metric for direct connection failure
		observability.NATTraversalAttempts.WithLabelValues("direct", "failure").Inc()
		return nil, fmt.Errorf("direct connection failed: %w", err)
	}

	// CLD-REQ-003: Emit metric for direct connection success
	observability.NATTraversalAttempts.WithLabelValues("direct", "success").Inc()
	return &ConnectionInfo{
		Strategy:   StrategyDirect,
		LocalAddr:  localAddr,
		RemoteAddr: peerAddr,
	}, nil
}

// establishHolePunchConnection establishes a connection via UDP hole punching
func (nt *NATTraversal) establishHolePunchConnection(ctx context.Context, localAddr, peerAddr string) (*ConnectionInfo, error) {
	nt.logger.Debug("Establishing hole punch connection",
		zap.String("peer_addr", peerAddr),
	)

	// Perform hole punching
	if err := nt.stunClient.PerformHolePunch(ctx, localAddr, peerAddr); err != nil {
		// CLD-REQ-003: Emit metric for hole punch failure
		observability.NATTraversalAttempts.WithLabelValues("holepunch", "failure").Inc()
		// Hole punching failed, fallback to relay
		nt.logger.Warn("Hole punching failed, falling back to relay", zap.Error(err))
		return nt.establishRelayConnection(ctx, localAddr, peerAddr)
	}

	// CLD-REQ-003: Emit metric for hole punch success
	observability.NATTraversalAttempts.WithLabelValues("holepunch", "success").Inc()
	return &ConnectionInfo{
		Strategy:   StrategyHolePunch,
		LocalAddr:  localAddr,
		RemoteAddr: peerAddr,
	}, nil
}

// establishRelayConnection establishes a relayed connection via TURN
func (nt *NATTraversal) establishRelayConnection(ctx context.Context, localAddr, peerAddr string) (*ConnectionInfo, error) {
	nt.logger.Debug("Establishing relay connection",
		zap.String("peer_addr", peerAddr),
	)

	// Get or create TURN allocation
	allocation, err := nt.getOrCreateAllocation(ctx, localAddr)
	if err != nil {
		// CLD-REQ-003: Emit metric for relay failure
		observability.NATTraversalAttempts.WithLabelValues("relay", "failure").Inc()
		return nil, fmt.Errorf("failed to get TURN allocation: %w", err)
	}

	// Create permission for peer
	if err := nt.turnClient.CreatePermission(ctx, allocation, peerAddr); err != nil {
		// CLD-REQ-003: Emit metric for relay failure
		observability.NATTraversalAttempts.WithLabelValues("relay", "failure").Inc()
		return nil, fmt.Errorf("failed to create TURN permission: %w", err)
	}

	// CLD-REQ-003: Emit metric for relay success
	observability.NATTraversalAttempts.WithLabelValues("relay", "success").Inc()
	return &ConnectionInfo{
		Strategy:   StrategyRelay,
		LocalAddr:  localAddr,
		RemoteAddr: peerAddr,
		RelayAddr:  allocation.RelayAddr,
		Allocation: allocation,
	}, nil
}

// getOrCreateAllocation gets or creates a TURN allocation
func (nt *NATTraversal) getOrCreateAllocation(ctx context.Context, localAddr string) (*RelayAllocation, error) {
	nt.allocationMu.Lock()
	defer nt.allocationMu.Unlock()

	// Return existing allocation if valid
	if nt.allocation != nil {
		return nt.allocation, nil
	}

	// Create new allocation
	allocation, err := nt.turnClient.AllocateRelay(ctx, localAddr)
	if err != nil {
		return nil, err
	}

	nt.allocation = allocation
	return allocation, nil
}

// refreshLoop periodically refreshes TURN allocations
func (nt *NATTraversal) refreshLoop() {
	defer nt.wg.Done()

	ticker := time.NewTicker(5 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-nt.ctx.Done():
			return
		case <-ticker.C:
			nt.refreshAllocations()
		}
	}
}

// refreshAllocations refreshes TURN allocations
func (nt *NATTraversal) refreshAllocations() {
	nt.allocationMu.RLock()
	allocation := nt.allocation
	nt.allocationMu.RUnlock()

	if allocation != nil {
		if err := nt.turnClient.Refresh(allocation); err != nil {
			nt.logger.Error("Failed to refresh TURN allocation", zap.Error(err))

			// Mark allocation as invalid
			nt.allocationMu.Lock()
			nt.allocation = nil
			nt.allocationMu.Unlock()
		}
	}
}

// SendThroughRelay sends data through TURN relay
func (nt *NATTraversal) SendThroughRelay(peerAddr string, data []byte) error {
	nt.allocationMu.RLock()
	allocation := nt.allocation
	nt.allocationMu.RUnlock()

	if allocation == nil {
		return fmt.Errorf("no active TURN allocation")
	}

	return nt.turnClient.SendIndication(allocation, peerAddr, data)
}

// ReceiveFromRelay receives data from TURN relay
func (nt *NATTraversal) ReceiveFromRelay(timeout time.Duration) ([]byte, string, error) {
	nt.allocationMu.RLock()
	allocation := nt.allocation
	nt.allocationMu.RUnlock()

	if allocation == nil {
		return nil, "", fmt.Errorf("no active TURN allocation")
	}

	return nt.turnClient.ReceiveData(allocation, timeout)
}

// NATTraversalStrategy represents a NAT traversal strategy
type NATTraversalStrategy string

const (
	StrategyDirect    NATTraversalStrategy = "direct"
	StrategyHolePunch NATTraversalStrategy = "hole-punch"
	StrategyRelay     NATTraversalStrategy = "relay"
)

// ConnectionInfo contains information about an established connection
type ConnectionInfo struct {
	Strategy   NATTraversalStrategy
	LocalAddr  string
	RemoteAddr string
	RelayAddr  string
	Allocation *RelayAllocation
}

// IsRelayed returns whether the connection is relayed
func (ci *ConnectionInfo) IsRelayed() bool {
	return ci.Strategy == StrategyRelay
}

// GetEffectiveAddr returns the effective address to use for this connection
func (ci *ConnectionInfo) GetEffectiveAddr() string {
	if ci.IsRelayed() && ci.RelayAddr != "" {
		return ci.RelayAddr
	}
	return ci.RemoteAddr
}
