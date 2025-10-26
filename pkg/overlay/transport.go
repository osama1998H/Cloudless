package overlay

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"

	"github.com/quic-go/quic-go"
	"go.uber.org/zap"
)

// QUICTransport implements the Transport interface using QUIC
type QUICTransport struct {
	config    TransportConfig
	tlsConfig *tls.Config
	logger    *zap.Logger

	listener    *quic.Listener
	connections sync.Map // peerID -> *QUICConnection

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

// NewQUICTransport creates a new QUIC transport
func NewQUICTransport(config TransportConfig, tlsConfig *tls.Config, logger *zap.Logger) *QUICTransport {
	ctx, cancel := context.WithCancel(context.Background())

	return &QUICTransport{
		config:    config,
		tlsConfig: tlsConfig,
		logger:    logger,
		ctx:       ctx,
		cancel:    cancel,
	}
}

// Listen starts listening for incoming QUIC connections
func (t *QUICTransport) Listen(ctx context.Context, addr string) error {
	if addr == "" {
		addr = t.config.ListenAddress
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return fmt.Errorf("failed to resolve UDP address: %w", err)
	}

	udpConn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return fmt.Errorf("failed to listen on UDP: %w", err)
	}

	quicConfig := &quic.Config{
		MaxIdleTimeout:        t.config.MaxIdleTimeout,
		MaxIncomingStreams:    int64(t.config.MaxStreamsPerConn),
		MaxIncomingUniStreams: int64(t.config.MaxStreamsPerConn),
		KeepAlivePeriod:       t.config.KeepAliveInterval,
		EnableDatagrams:       true,
		Allow0RTT:             false,
	}

	listener, err := quic.Listen(udpConn, t.tlsConfig, quicConfig)
	if err != nil {
		return fmt.Errorf("failed to create QUIC listener: %w", err)
	}

	t.listener = listener

	t.logger.Info("QUIC transport listening",
		zap.String("address", addr),
	)

	// Start accepting connections in the background
	t.wg.Add(1)
	go t.acceptLoop()

	return nil
}

// acceptLoop continuously accepts incoming connections
func (t *QUICTransport) acceptLoop() {
	defer t.wg.Done()

	for {
		select {
		case <-t.ctx.Done():
			return
		default:
			conn, err := t.listener.Accept(t.ctx)
			if err != nil {
				if t.ctx.Err() != nil {
					return // Context cancelled
				}
				t.logger.Error("Failed to accept connection", zap.Error(err))
				continue
			}

			// Handle connection in background
			t.wg.Add(1)
			go t.handleIncomingConnection(conn)
		}
	}
}

// handleIncomingConnection processes an incoming connection
func (t *QUICTransport) handleIncomingConnection(conn quic.Connection) {
	defer t.wg.Done()

	// Extract peer ID from TLS certificate
	peerID := extractPeerIDFromConn(conn)
	if peerID == "" {
		t.logger.Warn("Failed to extract peer ID from connection")
		conn.CloseWithError(1, "invalid peer ID")
		return
	}

	t.logger.Info("Accepted incoming connection",
		zap.String("peer_id", peerID),
		zap.String("remote_addr", conn.RemoteAddr().String()),
	)

	qconn := &QUICConnection{
		peerID:    peerID,
		conn:      conn,
		transport: t,
		logger:    t.logger,
	}

	t.connections.Store(peerID, qconn)

	// Keep connection alive until closed
	<-conn.Context().Done()
	t.connections.Delete(peerID)

	t.logger.Info("Connection closed",
		zap.String("peer_id", peerID),
	)
}

// Connect establishes a connection to a peer
func (t *QUICTransport) Connect(ctx context.Context, peerID string, addr string) (Connection, error) {
	// Check if already connected
	if existing, ok := t.connections.Load(peerID); ok {
		conn := existing.(*QUICConnection)
		if !conn.IsClosed() {
			return conn, nil
		}
		// Remove stale connection
		t.connections.Delete(peerID)
	}

	t.logger.Info("Connecting to peer",
		zap.String("peer_id", peerID),
		zap.String("address", addr),
	)

	quicConfig := &quic.Config{
		MaxIdleTimeout:        t.config.MaxIdleTimeout,
		MaxIncomingStreams:    int64(t.config.MaxStreamsPerConn),
		MaxIncomingUniStreams: int64(t.config.MaxStreamsPerConn),
		KeepAlivePeriod:       t.config.KeepAliveInterval,
		EnableDatagrams:       true,
		Allow0RTT:             false,
	}

	conn, err := quic.DialAddr(ctx, addr, t.tlsConfig, quicConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to dial peer: %w", err)
	}

	qconn := &QUICConnection{
		peerID:    peerID,
		conn:      conn,
		transport: t,
		logger:    t.logger,
	}

	t.connections.Store(peerID, qconn)

	t.logger.Info("Connected to peer",
		zap.String("peer_id", peerID),
		zap.String("address", addr),
	)

	return qconn, nil
}

// Accept waits for and returns an incoming connection
func (t *QUICTransport) Accept(ctx context.Context) (Connection, error) {
	if t.listener == nil {
		return nil, fmt.Errorf("transport not listening")
	}

	conn, err := t.listener.Accept(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to accept connection: %w", err)
	}

	peerID := extractPeerIDFromConn(conn)
	if peerID == "" {
		conn.CloseWithError(1, "invalid peer ID")
		return nil, fmt.Errorf("failed to extract peer ID")
	}

	qconn := &QUICConnection{
		peerID:    peerID,
		conn:      conn,
		transport: t,
		logger:    t.logger,
	}

	t.connections.Store(peerID, qconn)

	return qconn, nil
}

// Close closes the transport
func (t *QUICTransport) Close() error {
	t.logger.Info("Closing QUIC transport")

	t.cancel()

	// Close all connections
	t.connections.Range(func(key, value interface{}) bool {
		conn := value.(*QUICConnection)
		conn.Close()
		return true
	})

	if t.listener != nil {
		if err := t.listener.Close(); err != nil {
			t.logger.Error("Failed to close listener", zap.Error(err))
		}
	}

	t.wg.Wait()

	t.logger.Info("QUIC transport closed")
	return nil
}

// LocalAddr returns the local address that the transport is listening on
func (t *QUICTransport) LocalAddr() string {
	if t.listener != nil {
		return t.listener.Addr().String()
	}
	return ""
}

// QUICConnection implements the Connection interface
type QUICConnection struct {
	peerID    string
	conn      quic.Connection
	transport *QUICTransport
	logger    *zap.Logger
	closed    bool
	mu        sync.Mutex
}

// PeerID returns the peer's identifier
func (c *QUICConnection) PeerID() string {
	return c.peerID
}

// RemoteAddr returns the remote address
func (c *QUICConnection) RemoteAddr() net.Addr {
	return c.conn.RemoteAddr()
}

// LocalAddr returns the local address
func (c *QUICConnection) LocalAddr() net.Addr {
	return c.conn.LocalAddr()
}

// OpenStream opens a new stream on the connection
func (c *QUICConnection) OpenStream(ctx context.Context) (Stream, error) {
	stream, err := c.conn.OpenStreamSync(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to open stream: %w", err)
	}

	return &QUICStream{
		stream: stream,
		logger: c.logger,
	}, nil
}

// AcceptStream waits for and accepts an incoming stream
func (c *QUICConnection) AcceptStream(ctx context.Context) (Stream, error) {
	stream, err := c.conn.AcceptStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to accept stream: %w", err)
	}

	return &QUICStream{
		stream: stream,
		logger: c.logger,
	}, nil
}

// Close closes the connection
func (c *QUICConnection) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.closed {
		return nil
	}

	c.closed = true
	return c.conn.CloseWithError(0, "connection closed")
}

// IsClosed returns whether the connection is closed
func (c *QUICConnection) IsClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

// QUICStream implements the Stream interface
type QUICStream struct {
	stream quic.Stream
	logger *zap.Logger
}

// Read reads data from the stream
func (s *QUICStream) Read(p []byte) (n int, err error) {
	return s.stream.Read(p)
}

// Write writes data to the stream
func (s *QUICStream) Write(p []byte) (n int, err error) {
	return s.stream.Write(p)
}

// Close closes the stream
func (s *QUICStream) Close() error {
	return s.stream.Close()
}

// StreamID returns the stream identifier
func (s *QUICStream) StreamID() uint64 {
	return uint64(s.stream.StreamID())
}

// extractPeerIDFromConn extracts the peer ID from a QUIC connection's TLS certificate
func extractPeerIDFromConn(conn quic.Connection) string {
	// Extract peer ID from TLS certificate following priority:
	// 1. SPIFFE URI from SAN (most secure)
	// 2. DNS names from SAN
	// 3. Common Name from Subject
	// 4. Remote address (fallback)

	connState := conn.ConnectionState()
	if len(connState.TLS.PeerCertificates) > 0 {
		cert := connState.TLS.PeerCertificates[0]

		// Priority 1: Try SPIFFE URIs in SAN (SPIFFE-compatible workload identity)
		for _, uri := range cert.URIs {
			if uri.Scheme == "spiffe" {
				// Parse SPIFFE ID: spiffe://cloudless/node/{nodeID} or spiffe://cloudless/workload/{workloadID}
				// Extract the last path component as the ID
				path := uri.Path
				if len(path) > 0 {
					// Remove leading slash
					if path[0] == '/' {
						path = path[1:]
					}
					// For paths like "node/node-123" or "workload/wl-456", extract the last component
					parts := strings.Split(path, "/")
					if len(parts) > 0 {
						return parts[len(parts)-1]
					}
				}
			}
		}

		// Priority 2: Try DNS names from SAN
		if len(cert.DNSNames) > 0 {
			// Use the first DNS name
			return cert.DNSNames[0]
		}

		// Priority 3: Extract from Common Name
		if cert.Subject.CommonName != "" {
			return cert.Subject.CommonName
		}
	}

	// Priority 4: Fallback to remote address (least secure, for development only)
	return conn.RemoteAddr().String()
}

// SendDatagram sends a datagram over the connection
func (c *QUICConnection) SendDatagram(data []byte) error {
	return c.conn.SendDatagram(data)
}

// ReceiveDatagram receives a datagram from the connection
func (c *QUICConnection) ReceiveDatagram(ctx context.Context) ([]byte, error) {
	return c.conn.ReceiveDatagram(ctx)
}

// Ping sends a QUIC PING frame
func (c *QUICConnection) Ping(ctx context.Context) error {
	// Open a stream and immediately close it to trigger a PING
	stream, err := c.OpenStream(ctx)
	if err != nil {
		return err
	}
	defer stream.Close()

	// Write a single byte and read it back
	if _, err := stream.Write([]byte{0}); err != nil {
		return err
	}

	buf := make([]byte, 1)
	if _, err := io.ReadFull(stream, buf); err != nil {
		return err
	}

	return nil
}
