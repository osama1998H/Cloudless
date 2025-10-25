package overlay

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/pion/logging"
	"github.com/pion/turn/v2"
	"go.uber.org/zap"
)

// TURNClient handles TURN protocol for relayed connections
type TURNClient struct {
	servers []TURNServer
	logger  *zap.Logger
	client  *turn.Client
	timeout time.Duration
}

// NewTURNClient creates a new TURN client
func NewTURNClient(servers []TURNServer, logger *zap.Logger) *TURNClient {
	return &TURNClient{
		servers: servers,
		logger:  logger,
		timeout: 10 * time.Second,
	}
}

// AllocateRelay allocates a relay address on the TURN server
func (tc *TURNClient) AllocateRelay(ctx context.Context, localAddr string) (*RelayAllocation, error) {
	// Try each TURN server
	for _, server := range tc.servers {
		tc.logger.Debug("Attempting TURN allocation",
			zap.String("server", server.Address),
		)

		allocation, err := tc.performTURNAllocation(ctx, server, localAddr)
		if err != nil {
			tc.logger.Warn("TURN allocation failed",
				zap.String("server", server.Address),
				zap.Error(err),
			)
			continue
		}

		return allocation, nil
	}

	return nil, fmt.Errorf("all TURN servers failed")
}

// performTURNAllocation performs TURN allocation on a specific server
func (tc *TURNClient) performTURNAllocation(ctx context.Context, server TURNServer, localAddr string) (*RelayAllocation, error) {
	// Parse local address
	udpAddr, err := net.ResolveUDPAddr("udp", localAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local address: %w", err)
	}

	// Create UDP connection
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create UDP connection: %w", err)
	}

	// Parse TURN server address
	turnAddr, err := net.ResolveUDPAddr("udp", server.Address)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to resolve TURN server: %w", err)
	}

	// Create TURN client configuration
	cfg := &turn.ClientConfig{
		STUNServerAddr: turnAddr.String(),
		TURNServerAddr: turnAddr.String(),
		Username:       server.Username,
		Password:       server.Password,
		Conn:           conn,
		LoggerFactory:  NewPionLoggerFactory(tc.logger),
	}

	// Create TURN client
	client, err := turn.NewClient(cfg)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create TURN client: %w", err)
	}

	tc.client = client

	// Start client
	if err := client.Listen(); err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to start TURN client: %w", err)
	}

	// Allocate relay
	relayConn, err := client.Allocate()
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to allocate relay: %w", err)
	}

	relayAddr := relayConn.LocalAddr().(*net.UDPAddr)

	allocation := &RelayAllocation{
		RelayAddr:  relayAddr.String(),
		LocalAddr:  localAddr,
		ServerAddr: server.Address,
		Conn:       relayConn,
		Client:     client,
	}

	tc.logger.Info("TURN allocation successful",
		zap.String("relay_addr", relayAddr.String()),
		zap.String("server", server.Address),
	)

	return allocation, nil
}

// CreatePermission creates a permission for a peer on the TURN server
func (tc *TURNClient) CreatePermission(ctx context.Context, allocation *RelayAllocation, peerAddr string) error {
	if allocation == nil || allocation.Client == nil {
		return fmt.Errorf("invalid allocation or client")
	}

	// Parse peer address
	udpAddr, err := net.ResolveUDPAddr("udp", peerAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve peer address: %w", err)
	}

	// Create permission using the allocation's client
	if err := allocation.Client.CreatePermission(udpAddr); err != nil {
		return fmt.Errorf("failed to create permission: %w", err)
	}

	tc.logger.Debug("Created TURN permission",
		zap.String("peer_addr", peerAddr),
	)

	return nil
}

// SendIndication sends a data indication through the TURN server
func (tc *TURNClient) SendIndication(allocation *RelayAllocation, peerAddr string, data []byte) error {
	if allocation == nil || allocation.Conn == nil {
		return fmt.Errorf("invalid relay allocation")
	}

	// Parse peer address
	udpAddr, err := net.ResolveUDPAddr("udp", peerAddr)
	if err != nil {
		return fmt.Errorf("failed to resolve peer address: %w", err)
	}

	// Write to relay connection
	_, err = allocation.Conn.WriteTo(data, udpAddr)
	if err != nil {
		return fmt.Errorf("failed to send data: %w", err)
	}

	return nil
}

// ReceiveData receives data from the relay connection
func (tc *TURNClient) ReceiveData(allocation *RelayAllocation, timeout time.Duration) ([]byte, string, error) {
	if allocation == nil || allocation.Conn == nil {
		return nil, "", fmt.Errorf("invalid relay allocation")
	}

	// Set read deadline
	if err := allocation.Conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, "", fmt.Errorf("failed to set deadline: %w", err)
	}

	// Read from connection
	buf := make([]byte, 1500)
	n, addr, err := allocation.Conn.ReadFrom(buf)
	if err != nil {
		return nil, "", fmt.Errorf("failed to receive data: %w", err)
	}

	return buf[:n], addr.String(), nil
}

// Refresh refreshes the TURN allocation
func (tc *TURNClient) Refresh(allocation *RelayAllocation) error {
	if allocation == nil || allocation.Client == nil {
		return fmt.Errorf("invalid allocation or client")
	}

	// TURN client doesn't have a direct Refresh method
	// The allocation is kept alive by the client automatically
	// In production, would implement periodic refresh if needed

	tc.logger.Debug("TURN allocation kept alive",
		zap.String("relay_addr", allocation.RelayAddr),
	)

	return nil
}

// Close closes the TURN client and releases the allocation
func (tc *TURNClient) Close() error {
	if tc.client != nil {
		tc.client.Close()
		tc.client = nil
	}

	return nil
}

// RelayAllocation represents a TURN relay allocation
type RelayAllocation struct {
	RelayAddr  string
	LocalAddr  string
	ServerAddr string
	Conn       net.PacketConn
	Client     *turn.Client
}

// Close closes the relay allocation
func (ra *RelayAllocation) Close() error {
	if ra.Conn != nil {
		if err := ra.Conn.Close(); err != nil {
			return err
		}
		ra.Conn = nil
	}

	if ra.Client != nil {
		ra.Client.Close()
		ra.Client = nil
	}

	return nil
}

// PionLoggerFactory implements pion's LoggerFactory interface
type PionLoggerFactory struct {
	logger *zap.Logger
}

// NewPionLoggerFactory creates a new pion logger factory
func NewPionLoggerFactory(logger *zap.Logger) *PionLoggerFactory {
	return &PionLoggerFactory{logger: logger}
}

// NewLogger creates a new logger with the given scope
func (f *PionLoggerFactory) NewLogger(scope string) logging.LeveledLogger {
	return &PionLogger{
		logger: f.logger.With(zap.String("pion_scope", scope)),
	}
}

// PionLogger implements pion's Logger interface
type PionLogger struct {
	logger *zap.Logger
}

// Trace logs a trace message
func (l *PionLogger) Trace(msg string) {
	l.logger.Debug(msg)
}

// Tracef logs a formatted trace message
func (l *PionLogger) Tracef(format string, args ...interface{}) {
	l.logger.Sugar().Debugf(format, args...)
}

// Debug logs a debug message
func (l *PionLogger) Debug(msg string) {
	l.logger.Debug(msg)
}

// Debugf logs a formatted debug message
func (l *PionLogger) Debugf(format string, args ...interface{}) {
	l.logger.Sugar().Debugf(format, args...)
}

// Info logs an info message
func (l *PionLogger) Info(msg string) {
	l.logger.Info(msg)
}

// Infof logs a formatted info message
func (l *PionLogger) Infof(format string, args ...interface{}) {
	l.logger.Sugar().Infof(format, args...)
}

// Warn logs a warning message
func (l *PionLogger) Warn(msg string) {
	l.logger.Warn(msg)
}

// Warnf logs a formatted warning message
func (l *PionLogger) Warnf(format string, args ...interface{}) {
	l.logger.Sugar().Warnf(format, args...)
}

// Error logs an error message
func (l *PionLogger) Error(msg string) {
	l.logger.Error(msg)
}

// Errorf logs a formatted error message
func (l *PionLogger) Errorf(format string, args ...interface{}) {
	l.logger.Sugar().Errorf(format, args...)
}
