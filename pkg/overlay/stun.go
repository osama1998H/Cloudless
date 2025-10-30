package overlay

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/osama1998H/Cloudless/pkg/observability"
	"github.com/pion/stun"
	"go.uber.org/zap"
)

// STUNClient handles STUN protocol for NAT discovery
type STUNClient struct {
	servers      []string
	logger       *zap.Logger
	timeout      time.Duration
	useRFC5780   bool   // Use RFC 5780 for accurate NAT detection
	altServer    string // Alternate server for RFC 5780 tests
	rfc5780Disco *RFC5780Discoverer
}

// NewSTUNClient creates a new STUN client
func NewSTUNClient(servers []string, logger *zap.Logger) *STUNClient {
	if len(servers) == 0 {
		// Default STUN servers
		servers = []string{
			"stun.l.google.com:19302",
			"stun1.l.google.com:19302",
			"stun2.l.google.com:19302",
		}
	}

	return &STUNClient{
		servers:    servers,
		logger:     logger,
		timeout:    5 * time.Second,
		useRFC5780: false,
	}
}

// EnableRFC5780 enables RFC 5780 NAT behavior discovery for accurate NAT detection
func (sc *STUNClient) EnableRFC5780(altServer string) {
	sc.useRFC5780 = true
	sc.altServer = altServer
	if sc.useRFC5780 && len(sc.servers) > 0 {
		sc.rfc5780Disco = NewRFC5780Discoverer(sc.servers[0], altServer, sc.logger)
	}
}

// DiscoverNATInfo discovers NAT information using STUN
func (sc *STUNClient) DiscoverNATInfo(ctx context.Context, localAddr string) (*NATInfo, error) {
	// Use RFC 5780 for accurate NAT detection if enabled
	if sc.useRFC5780 && sc.rfc5780Disco != nil {
		return sc.discoverWithRFC5780(ctx, localAddr)
	}

	// Parse local address
	localIP, localPort, err := parseAddress(localAddr)
	if err != nil {
		// CLD-REQ-003: Emit metric for STUN failure
		observability.NATTraversalAttempts.WithLabelValues("stun", "failure").Inc()
		return nil, fmt.Errorf("failed to parse local address: %w", err)
	}

	// Try each STUN server
	for _, server := range sc.servers {
		sc.logger.Debug("Attempting STUN discovery",
			zap.String("server", server),
		)

		natInfo, err := sc.performSTUNDiscovery(ctx, server, localIP, localPort)
		if err != nil {
			sc.logger.Warn("STUN discovery failed",
				zap.String("server", server),
				zap.Error(err),
			)
			continue
		}

		// CLD-REQ-003: Emit metric for STUN success
		observability.NATTraversalAttempts.WithLabelValues("stun", "success").Inc()
		return natInfo, nil
	}

	// CLD-REQ-003: Emit metric for STUN failure (all servers failed)
	observability.NATTraversalAttempts.WithLabelValues("stun", "failure").Inc()
	return nil, fmt.Errorf("all STUN servers failed")
}

// discoverWithRFC5780 performs NAT discovery using RFC 5780
func (sc *STUNClient) discoverWithRFC5780(ctx context.Context, localAddr string) (*NATInfo, error) {
	sc.logger.Info("Using RFC 5780 for NAT discovery")

	behavior, err := sc.rfc5780Disco.DiscoverNATBehavior(ctx, localAddr)
	if err != nil {
		// CLD-REQ-003: Emit metric for STUN failure
		observability.NATTraversalAttempts.WithLabelValues("stun", "failure").Inc()
		return nil, fmt.Errorf("RFC 5780 discovery failed: %w", err)
	}

	// Convert NATBehavior to NATInfo
	natInfo := &NATInfo{
		Type:       behavior.Type,
		PublicIP:   behavior.PublicIP,
		PublicPort: behavior.PublicPort,
		LocalIP:    behavior.LocalIP,
		LocalPort:  behavior.LocalPort,
		Mapped:     behavior.HasNAT,
	}

	// CLD-REQ-003: Emit metric for STUN success
	observability.NATTraversalAttempts.WithLabelValues("stun", "success").Inc()

	sc.logger.Info("RFC 5780 NAT discovery complete",
		zap.String("nat_type", string(natInfo.Type)),
		zap.String("mapping", string(behavior.MappingBehavior)),
		zap.String("filtering", string(behavior.FilteringBehavior)),
	)

	return natInfo, nil
}

// performSTUNDiscovery performs STUN discovery against a specific server
func (sc *STUNClient) performSTUNDiscovery(ctx context.Context, server, localIP string, localPort int) (*NATInfo, error) {
	// Create UDP connection
	// Use port 0 to let OS assign an available port (avoids conflict with QUIC transport)
	localAddr := &net.UDPAddr{
		IP:   net.ParseIP(localIP),
		Port: 0, // Dynamic port allocation
	}

	sc.logger.Debug("STUN using dynamic port allocation",
		zap.String("local_ip", localIP),
		zap.Int("original_port", localPort),
		zap.Int("new_port", localAddr.Port),
	)

	serverAddr, err := net.ResolveUDPAddr("udp", server)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve STUN server: %w", err)
	}

	conn, err := net.DialUDP("udp", localAddr, serverAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to STUN server: %w", err)
	}
	defer conn.Close()

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(sc.timeout)
	}
	conn.SetDeadline(deadline)

	// Create STUN binding request
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	// Send request
	if _, err := conn.Write(message.Raw); err != nil {
		return nil, fmt.Errorf("failed to send STUN request: %w", err)
	}

	// Receive response
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to receive STUN response: %w", err)
	}

	// Parse response
	response := &stun.Message{Raw: buf[:n]}
	if err := response.Decode(); err != nil {
		return nil, fmt.Errorf("failed to decode STUN response: %w", err)
	}

	// Extract mapped address
	var xorAddr stun.XORMappedAddress
	if err := xorAddr.GetFrom(response); err != nil {
		return nil, fmt.Errorf("failed to get mapped address: %w", err)
	}

	publicIP := xorAddr.IP.String()
	publicPort := xorAddr.Port

	// Determine NAT type
	natType := sc.detectNATType(localIP, localPort, publicIP, publicPort)

	natInfo := &NATInfo{
		Type:       natType,
		PublicIP:   publicIP,
		PublicPort: publicPort,
		LocalIP:    localIP,
		LocalPort:  localPort,
		Mapped:     true,
	}

	sc.logger.Info("STUN discovery successful",
		zap.String("public_ip", publicIP),
		zap.Int("public_port", publicPort),
		zap.String("nat_type", string(natType)),
	)

	return natInfo, nil
}

// detectNATType detects the type of NAT based on mapping behavior
func (sc *STUNClient) detectNATType(localIP string, localPort int, publicIP string, publicPort int) NATType {
	// If public IP matches local IP, no NAT
	if publicIP == localIP {
		return NATTypeNone
	}

	// If public port matches local port, likely full cone
	if publicPort == localPort {
		return NATTypeFullCone
	}

	// This is a simplified detection
	// Full detection would require multiple STUN requests to different servers
	// and analyzing port mapping patterns

	// For now, assume port-restricted cone NAT
	// In production, would perform RFC 5780 STUN behavior discovery
	return NATTypePortRestrictedCone
}

// TestConnectivity tests connectivity to a remote peer
func (sc *STUNClient) TestConnectivity(ctx context.Context, localAddr, remoteAddr string) error {
	conn, err := net.DialTimeout("udp", remoteAddr, sc.timeout)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(sc.timeout)
	}
	conn.SetDeadline(deadline)

	// Send ping
	if _, err := conn.Write([]byte("PING")); err != nil {
		return fmt.Errorf("failed to send ping: %w", err)
	}

	// Wait for response
	buf := make([]byte, 4)
	n, err := conn.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to receive pong: %w", err)
	}

	if n != 4 || string(buf[:n]) != "PONG" {
		return fmt.Errorf("invalid response")
	}

	return nil
}

// GetPublicEndpoint returns the public endpoint for this node
func (sc *STUNClient) GetPublicEndpoint(ctx context.Context, localAddr string) (string, error) {
	natInfo, err := sc.DiscoverNATInfo(ctx, localAddr)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("%s:%d", natInfo.PublicIP, natInfo.PublicPort), nil
}

// PerformHolePunch performs UDP hole punching to establish direct connection
func (sc *STUNClient) PerformHolePunch(ctx context.Context, localAddr, remotePublicAddr string) error {
	sc.logger.Info("Attempting UDP hole punch",
		zap.String("local", localAddr),
		zap.String("remote", remotePublicAddr),
	)

	// Parse addresses
	localUDPAddr, err := net.ResolveUDPAddr("udp", localAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve local address: %w", err)
	}

	remoteUDPAddr, err := net.ResolveUDPAddr("udp", remotePublicAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve remote address: %w", err)
	}

	// Create UDP connection
	// Use dynamic port to avoid conflict with QUIC transport
	// Note: This means hole punching won't use the same port as QUIC,
	// but avoids "address already in use" errors
	localUDPAddr.Port = 0
	conn, err := net.ListenUDP("udp", localUDPAddr)
	if err != nil {
		return fmt.Errorf("failed to create UDP connection: %w", err)
	}
	defer conn.Close()

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(sc.timeout)
	}
	conn.SetDeadline(deadline)

	// Send multiple packets to punch hole
	for i := 0; i < 5; i++ {
		message := []byte(fmt.Sprintf("PUNCH-%d", i))
		if _, err := conn.WriteToUDP(message, remoteUDPAddr); err != nil {
			sc.logger.Debug("Failed to send hole punch packet",
				zap.Int("attempt", i),
				zap.Error(err),
			)
		}

		// Small delay between packets
		time.Sleep(100 * time.Millisecond)
	}

	// Try to receive response
	buf := make([]byte, 1500)
	n, addr, err := conn.ReadFromUDP(buf)
	if err != nil {
		return fmt.Errorf("hole punch failed: %w", err)
	}

	sc.logger.Info("Hole punch successful",
		zap.String("remote", addr.String()),
		zap.Int("bytes", n),
	)

	return nil
}

// parseAddress parses an address string into IP and port
func parseAddress(addr string) (string, int, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return "", 0, err
	}

	// If host is empty, use 0.0.0.0
	if host == "" {
		host = "0.0.0.0"
	}

	port := 0
	if portStr != "" {
		_, err := fmt.Sscanf(portStr, "%d", &port)
		if err != nil {
			return "", 0, fmt.Errorf("invalid port: %w", err)
		}
	}

	return host, port, nil
}
