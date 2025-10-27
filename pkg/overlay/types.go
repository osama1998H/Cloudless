package overlay

import (
	"context"
	"net"
	"sync/atomic"
	"time"
)

// Transport defines the interface for overlay network transport
type Transport interface {
	// Listen starts listening for incoming connections
	Listen(ctx context.Context, addr string) error

	// Connect establishes a connection to a peer
	Connect(ctx context.Context, peerID string, addr string) (Connection, error)

	// Accept waits for and returns an incoming connection
	Accept(ctx context.Context) (Connection, error)

	// Close closes the transport
	Close() error
}

// Connection represents a connection to a peer
type Connection interface {
	// PeerID returns the peer's identifier
	PeerID() string

	// RemoteAddr returns the remote address
	RemoteAddr() net.Addr

	// LocalAddr returns the local address
	LocalAddr() net.Addr

	// OpenStream opens a new stream on the connection
	OpenStream(ctx context.Context) (Stream, error)

	// AcceptStream waits for and accepts an incoming stream
	AcceptStream(ctx context.Context) (Stream, error)

	// Close closes the connection
	Close() error

	// IsClosed returns whether the connection is closed
	IsClosed() bool
}

// Stream represents a bidirectional data stream
type Stream interface {
	// Read reads data from the stream
	Read(p []byte) (n int, err error)

	// Write writes data to the stream
	Write(p []byte) (n int, err error)

	// Close closes the stream
	Close() error

	// StreamID returns the stream identifier
	StreamID() uint64
}

// Peer represents a node in the overlay network
type Peer struct {
	ID        string
	Address   string
	PublicIP  string
	Port      int
	Region    string
	Zone      string
	Status    PeerStatus
	Latency   time.Duration
	Bandwidth uint64 // Bytes per second
	LastSeen  time.Time
	Metadata  map[string]string

	// NAT traversal information
	NATType NATType  // Detected NAT type for this peer
	NATInfo *NATInfo // Full NAT information if available
}

// PeerStatus represents the status of a peer
type PeerStatus string

const (
	PeerStatusConnected    PeerStatus = "connected"
	PeerStatusConnecting   PeerStatus = "connecting"
	PeerStatusDisconnected PeerStatus = "disconnected"
	PeerStatusUnreachable  PeerStatus = "unreachable"
)

// Service represents a service in the overlay network
type Service struct {
	Name      string
	Namespace string
	VirtualIP string
	Endpoints []Endpoint
	Protocol  Protocol
	Port      int
	Labels    map[string]string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Endpoint represents a service endpoint
type Endpoint struct {
	ID       string
	PeerID   string
	Address  string
	Port     int
	Weight   int
	Health   HealthStatus
	Region   string
	Zone     string
	Metadata map[string]string
}

// HealthStatus represents the health status of an endpoint
type HealthStatus string

const (
	HealthStatusHealthy   HealthStatus = "healthy"
	HealthStatusUnhealthy HealthStatus = "unhealthy"
	HealthStatusUnknown   HealthStatus = "unknown"
)

// Protocol represents a network protocol
type Protocol string

const (
	ProtocolTCP Protocol = "tcp"
	ProtocolUDP Protocol = "udp"
)

// LoadBalancer defines the interface for load balancing
type LoadBalancer interface {
	// SelectEndpoint selects an endpoint for a service
	SelectEndpoint(service *Service, clientAddr string) (*Endpoint, error)

	// UpdateEndpoints updates the endpoints for a service
	UpdateEndpoints(serviceName string, endpoints []Endpoint) error

	// GetStats returns load balancing statistics
	GetStats(serviceName string) (*LoadBalancerStats, error)
}

// LoadBalancerStats contains load balancing statistics
type LoadBalancerStats struct {
	ServiceName   string
	TotalRequests uint64
	EndpointStats map[string]*EndpointStats
	LastUpdated   time.Time
}

// EndpointStats contains statistics for an endpoint
// NOTE: AverageLatency and ActiveConnections use atomic-compatible types
// - AverageLatency is int64 (nanoseconds) for atomic.LoadInt64/StoreInt64/CompareAndSwapInt64
// - ActiveConnections is int32 for atomic.AddInt32/LoadInt32/CompareAndSwapInt32
// Use helper methods for type-safe access
type EndpointStats struct {
	EndpointID        string
	Requests          uint64 // Atomic via atomic.AddUint64/LoadUint64
	Failures          uint64 // Atomic via atomic.AddUint64/LoadUint64
	AverageLatency    int64  // Atomic int64 (nanoseconds) - use GetAverageLatency()/SetAverageLatency()
	ActiveConnections int32  // Atomic int32 - use atomic operations or GetActiveConnections()
}

// GetAverageLatency returns the average latency as a time.Duration
// Thread-safe: uses atomic.LoadInt64
func (es *EndpointStats) GetAverageLatency() time.Duration {
	return time.Duration(atomic.LoadInt64(&es.AverageLatency))
}

// SetAverageLatency sets the average latency from a time.Duration
// Thread-safe: uses atomic.StoreInt64
func (es *EndpointStats) SetAverageLatency(d time.Duration) {
	atomic.StoreInt64(&es.AverageLatency, int64(d))
}

// GetActiveConnections returns the active connection count as an int
// Thread-safe: uses atomic.LoadInt32
func (es *EndpointStats) GetActiveConnections() int {
	return int(atomic.LoadInt32(&es.ActiveConnections))
}

// LoadBalancingAlgorithm represents a load balancing algorithm
type LoadBalancingAlgorithm string

const (
	AlgorithmRoundRobin         LoadBalancingAlgorithm = "round-robin"
	AlgorithmWeightedRoundRobin LoadBalancingAlgorithm = "weighted-round-robin"
	AlgorithmLeastConnections   LoadBalancingAlgorithm = "least-connections"
	AlgorithmLocalityAware      LoadBalancingAlgorithm = "locality-aware"
)

// NATType represents the type of NAT detected
type NATType string

const (
	NATTypeNone               NATType = "none"
	NATTypeFullCone           NATType = "full-cone"
	NATTypeRestrictedCone     NATType = "restricted-cone"
	NATTypePortRestrictedCone NATType = "port-restricted-cone"
	NATTypeSymmetric          NATType = "symmetric"
	NATTypeUnknown            NATType = "unknown"
)

// NATInfo contains information about NAT traversal
type NATInfo struct {
	Type       NATType
	PublicIP   string
	PublicPort int
	LocalIP    string
	LocalPort  int
	Mapped     bool
}

// Route represents a route in the mesh
type Route struct {
	Destination string
	NextHop     string
	Metric      int
	Interface   string
}

// MeshConfig contains mesh configuration
type MeshConfig struct {
	// MeshType is full or partial mesh
	MeshType MeshType

	// MaxPeers is the maximum number of peers to connect to
	MaxPeers int

	// HealthCheckInterval is the interval for health checks
	HealthCheckInterval time.Duration

	// ReconnectInterval is the interval for reconnection attempts
	ReconnectInterval time.Duration

	// ConnectionTimeout is the timeout for connection attempts
	ConnectionTimeout time.Duration
}

// MeshType represents the type of mesh topology
type MeshType string

const (
	MeshTypeFull    MeshType = "full"
	MeshTypePartial MeshType = "partial"
)

// RegistryConfig contains service registry configuration
type RegistryConfig struct {
	// TTL is the time-to-live for registry entries
	TTL time.Duration

	// RefreshInterval is how often to refresh entries
	RefreshInterval time.Duration

	// VirtualIPCIDR is the CIDR for virtual IP allocation
	VirtualIPCIDR string
}

// LoadBalancerConfig contains load balancer configuration
type LoadBalancerConfig struct {
	// Algorithm is the load balancing algorithm to use
	Algorithm LoadBalancingAlgorithm

	// HealthCheckEnabled enables health checking
	HealthCheckEnabled bool

	// HealthCheckInterval is the interval for health checks
	HealthCheckInterval time.Duration

	// SessionAffinity enables session affinity
	SessionAffinity bool

	// SessionAffinityTTL is the TTL for session affinity
	SessionAffinityTTL time.Duration
}

// NATConfig contains NAT traversal configuration
type NATConfig struct {
	// STUNServers is a list of STUN servers
	STUNServers []string

	// TURNServers is a list of TURN servers
	TURNServers []TURNServer

	// EnableUPnP enables UPnP for port mapping
	EnableUPnP bool

	// EnableNATPMP enables NAT-PMP for port mapping
	EnableNATPMP bool
}

// TURNServer represents a TURN server configuration
type TURNServer struct {
	Address  string
	Username string
	Password string
}

// TransportConfig contains transport configuration
type TransportConfig struct {
	// ListenAddress is the address to listen on
	ListenAddress string

	// CertFile is the path to the TLS certificate
	CertFile string

	// KeyFile is the path to the TLS private key
	KeyFile string

	// MaxIdleTimeout is the maximum idle timeout for connections
	MaxIdleTimeout time.Duration

	// MaxStreamsPerConn is the maximum number of streams per connection
	MaxStreamsPerConn int

	// KeepAlive enables keep-alive
	KeepAlive bool

	// KeepAliveInterval is the keep-alive interval
	KeepAliveInterval time.Duration
}

// OverlayConfig is the main configuration for the overlay network
type OverlayConfig struct {
	NodeID       string
	Transport    TransportConfig
	Mesh         MeshConfig
	Registry     RegistryConfig
	LoadBalancer LoadBalancerConfig
	NAT          NATConfig
}
