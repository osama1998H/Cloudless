package overlay

import (
	"fmt"
	"hash/fnv"
	"sync"
	"sync/atomic"
	"time"

	"go.uber.org/zap"
)

// L4LoadBalancer implements a Layer 4 load balancer
type L4LoadBalancer struct {
	config   LoadBalancerConfig
	registry *ServiceRegistry
	logger   *zap.Logger

	stats         sync.Map // serviceName -> *LoadBalancerStats
	endpointStats sync.Map // endpointID -> *EndpointStats
	sessionCache  sync.Map // clientAddr+serviceName -> endpointID (for session affinity)

	// Round-robin state
	rrCounters sync.Map // serviceName -> uint64

	mu sync.RWMutex
}

// NewL4LoadBalancer creates a new L4 load balancer
func NewL4LoadBalancer(config LoadBalancerConfig, registry *ServiceRegistry, logger *zap.Logger) *L4LoadBalancer {
	return &L4LoadBalancer{
		config:   config,
		registry: registry,
		logger:   logger,
	}
}

// SelectEndpoint selects an endpoint for a service based on the configured algorithm
func (lb *L4LoadBalancer) SelectEndpoint(service *Service, clientAddr string) (*Endpoint, error) {
	// Check session affinity first
	if lb.config.SessionAffinity {
		if endpointID := lb.getSessionEndpoint(service.Name, clientAddr); endpointID != "" {
			endpoints, err := lb.registry.GetHealthyEndpoints(service.Name, service.Namespace)
			if err == nil {
				for i := range endpoints {
					if endpoints[i].ID == endpointID {
						return &endpoints[i], nil
					}
				}
			}
		}
	}

	// Get healthy endpoints
	endpoints, err := lb.registry.GetHealthyEndpoints(service.Name, service.Namespace)
	if err != nil {
		return nil, err
	}

	if len(endpoints) == 0 {
		return nil, fmt.Errorf("no healthy endpoints available for service: %s", service.Name)
	}

	// Select endpoint based on algorithm
	var selected *Endpoint
	switch lb.config.Algorithm {
	case AlgorithmRoundRobin:
		selected = lb.selectRoundRobin(service.Name, endpoints)
	case AlgorithmWeightedRoundRobin:
		selected = lb.selectWeightedRoundRobin(service.Name, endpoints)
	case AlgorithmLeastConnections:
		selected = lb.selectLeastConnections(endpoints)
	case AlgorithmLocalityAware:
		selected = lb.selectLocalityAware(endpoints, clientAddr)
	default:
		selected = lb.selectRoundRobin(service.Name, endpoints)
	}

	if selected == nil {
		return nil, fmt.Errorf("failed to select endpoint")
	}

	// Update session cache if session affinity is enabled
	if lb.config.SessionAffinity {
		lb.setSessionEndpoint(service.Name, clientAddr, selected.ID)
	}

	// Update statistics
	lb.updateRequestStats(service.Name, selected.ID)

	return selected, nil
}

// selectRoundRobin selects an endpoint using round-robin algorithm
func (lb *L4LoadBalancer) selectRoundRobin(serviceName string, endpoints []Endpoint) *Endpoint {
	if len(endpoints) == 0 {
		return nil
	}

	// Get or create counter for this service
	var counter uint64
	if c, ok := lb.rrCounters.Load(serviceName); ok {
		counter = c.(uint64)
	}

	// Increment counter
	next := atomic.AddUint64(&counter, 1)
	lb.rrCounters.Store(serviceName, next)

	// Select endpoint
	index := int((next - 1) % uint64(len(endpoints)))
	return &endpoints[index]
}

// selectWeightedRoundRobin selects an endpoint using weighted round-robin
func (lb *L4LoadBalancer) selectWeightedRoundRobin(serviceName string, endpoints []Endpoint) *Endpoint {
	if len(endpoints) == 0 {
		return nil
	}

	// Calculate total weight
	totalWeight := 0
	for i := range endpoints {
		if endpoints[i].Weight <= 0 {
			endpoints[i].Weight = 1
		}
		totalWeight += endpoints[i].Weight
	}

	if totalWeight == 0 {
		return lb.selectRoundRobin(serviceName, endpoints)
	}

	// Get or create counter
	var counter uint64
	if c, ok := lb.rrCounters.Load(serviceName); ok {
		counter = c.(uint64)
	}

	// Increment counter
	next := atomic.AddUint64(&counter, 1)
	lb.rrCounters.Store(serviceName, next)

	// Select weighted endpoint
	target := int((next - 1) % uint64(totalWeight))
	current := 0

	for i := range endpoints {
		current += endpoints[i].Weight
		if target < current {
			return &endpoints[i]
		}
	}

	return &endpoints[0]
}

// selectLeastConnections selects the endpoint with the least active connections
func (lb *L4LoadBalancer) selectLeastConnections(endpoints []Endpoint) *Endpoint {
	if len(endpoints) == 0 {
		return nil
	}

	var best *Endpoint
	minConnections := int32(^uint32(0) >> 1) // Max int32

	for i := range endpoints {
		stats := lb.getEndpointStats(endpoints[i].ID)
		activeConns := atomic.LoadInt32(&stats.ActiveConnections)
		if activeConns < minConnections {
			minConnections = activeConns
			best = &endpoints[i]
		}
	}

	return best
}

// selectLocalityAware selects an endpoint based on locality (region/zone)
func (lb *L4LoadBalancer) selectLocalityAware(endpoints []Endpoint, clientAddr string) *Endpoint {
	if len(endpoints) == 0 {
		return nil
	}

	// Extract client locality (this is simplified - in production, would use GeoIP or node metadata)
	clientRegion, clientZone := lb.extractClientLocality(clientAddr)

	// Try to find endpoint in same zone
	for i := range endpoints {
		if endpoints[i].Zone == clientZone && endpoints[i].Region == clientRegion {
			return &endpoints[i]
		}
	}

	// Try to find endpoint in same region
	for i := range endpoints {
		if endpoints[i].Region == clientRegion {
			return &endpoints[i]
		}
	}

	// Fall back to round-robin if no locality match
	return lb.selectRoundRobin("locality-fallback", endpoints)
}

// extractClientLocality extracts region and zone from client address
func (lb *L4LoadBalancer) extractClientLocality(clientAddr string) (region, zone string) {
	// In a production system, this would use GeoIP lookup or node metadata
	// For now, return empty strings which will match any endpoint
	return "", ""
}

// UpdateEndpoints updates the endpoints for a service
func (lb *L4LoadBalancer) UpdateEndpoints(serviceName string, endpoints []Endpoint) error {
	return lb.registry.UpdateEndpoints(serviceName, "default", endpoints...)
}

// GetStats returns load balancing statistics for a service
// Thread-safe: Returns a deep copy with atomic loads to prevent external mutation
func (lb *L4LoadBalancer) GetStats(serviceName string) (*LoadBalancerStats, error) {
	if s, ok := lb.stats.Load(serviceName); ok {
		stats := s.(*LoadBalancerStats)

		// Create a deep copy to prevent external mutation
		statsCopy := &LoadBalancerStats{
			ServiceName:   stats.ServiceName,
			TotalRequests: atomic.LoadUint64(&stats.TotalRequests),
			EndpointStats: make(map[string]*EndpointStats),
			LastUpdated:   stats.LastUpdated, // Safe: time.Time is immutable
		}

		// Copy endpoint stats with atomic loads
		for epID, epStats := range stats.EndpointStats {
			statsCopy.EndpointStats[epID] = &EndpointStats{
				EndpointID:        epStats.EndpointID,
				Requests:          atomic.LoadUint64(&epStats.Requests),
				Failures:          atomic.LoadUint64(&epStats.Failures),
				AverageLatency:    atomic.LoadInt64(&epStats.AverageLatency),
				ActiveConnections: atomic.LoadInt32(&epStats.ActiveConnections),
			}
		}

		return statsCopy, nil
	}

	// Return empty stats if service not found
	return &LoadBalancerStats{
		ServiceName:   serviceName,
		TotalRequests: 0,
		EndpointStats: make(map[string]*EndpointStats),
		LastUpdated:   time.Now(),
	}, nil
}

// updateRequestStats updates request statistics
// Thread-safe: Uses copy-on-write pattern for LoadBalancerStats to avoid races on LastUpdated field
func (lb *L4LoadBalancer) updateRequestStats(serviceName, endpointID string) {
	// Create or update service stats with copy-on-write pattern
	var newStats *LoadBalancerStats
	if s, ok := lb.stats.Load(serviceName); ok {
		oldStats := s.(*LoadBalancerStats)
		// Create new stats struct with updated values
		newStats = &LoadBalancerStats{
			ServiceName:   oldStats.ServiceName,
			TotalRequests: atomic.LoadUint64(&oldStats.TotalRequests),
			EndpointStats: oldStats.EndpointStats, // Share map reference (immutable from this function's perspective)
			LastUpdated:   time.Now(),             // Safe: new struct not yet shared with other goroutines
		}
	} else {
		newStats = &LoadBalancerStats{
			ServiceName:   serviceName,
			TotalRequests: 0,
			EndpointStats: make(map[string]*EndpointStats),
			LastUpdated:   time.Now(),
		}
	}

	// Increment total requests atomically
	atomic.AddUint64(&newStats.TotalRequests, 1)

	// Store the new stats struct atomically (replaces entire pointer)
	lb.stats.Store(serviceName, newStats)

	// Update endpoint stats atomically
	epStats := lb.getEndpointStats(endpointID)
	atomic.AddUint64(&epStats.Requests, 1)
}

// RecordFailure records a failure for an endpoint
func (lb *L4LoadBalancer) RecordFailure(serviceName, endpointID string) {
	epStats := lb.getEndpointStats(endpointID)
	atomic.AddUint64(&epStats.Failures, 1)
	lb.endpointStats.Store(endpointID, epStats)

	// Mark endpoint as unhealthy if health checking is enabled
	if lb.config.HealthCheckEnabled {
		lb.registry.UpdateEndpointHealth(serviceName, "default", endpointID, HealthStatusUnhealthy)
	}
}

// RecordLatency records latency for an endpoint
// Thread-safe: Uses atomic CompareAndSwap loop for exponential moving average calculation
func (lb *L4LoadBalancer) RecordLatency(endpointID string, latency time.Duration) {
	epStats := lb.getEndpointStats(endpointID)

	requests := atomic.LoadUint64(&epStats.Requests)

	if requests == 0 {
		// First request, just store the latency
		atomic.StoreInt64(&epStats.AverageLatency, int64(latency))
		return
	}

	// Exponential moving average with atomic CAS loop
	const alpha = 0.1
	for {
		currentAvgNanos := atomic.LoadInt64(&epStats.AverageLatency)
		// Calculate new average: newAvg = currentAvg * (1-alpha) + latency * alpha
		newAvgNanos := int64(float64(currentAvgNanos)*(1-alpha) + float64(latency)*alpha)

		// Attempt to update atomically
		if atomic.CompareAndSwapInt64(&epStats.AverageLatency, currentAvgNanos, newAvgNanos) {
			return // Successfully updated
		}
		// CAS failed (another goroutine updated the value), retry with new current value
	}
}

// IncrementActiveConnections increments the active connection count for an endpoint
// Thread-safe: Uses atomic.AddInt32
func (lb *L4LoadBalancer) IncrementActiveConnections(endpointID string) {
	epStats := lb.getEndpointStats(endpointID)
	atomic.AddInt32(&epStats.ActiveConnections, 1)
}

// DecrementActiveConnections decrements the active connection count for an endpoint
// Thread-safe: Uses atomic CompareAndSwap loop to safely decrement without going negative
func (lb *L4LoadBalancer) DecrementActiveConnections(endpointID string) {
	epStats := lb.getEndpointStats(endpointID)

	// Use CAS loop to safely decrement without going negative
	for {
		current := atomic.LoadInt32(&epStats.ActiveConnections)
		if current <= 0 {
			return // Already at zero, nothing to decrement
		}
		// Attempt to decrement atomically
		if atomic.CompareAndSwapInt32(&epStats.ActiveConnections, current, current-1) {
			return // Successfully decremented
		}
		// CAS failed (another goroutine modified the value), retry
	}
}

// getEndpointStats gets or creates endpoint statistics
func (lb *L4LoadBalancer) getEndpointStats(endpointID string) *EndpointStats {
	if s, ok := lb.endpointStats.Load(endpointID); ok {
		return s.(*EndpointStats)
	}

	stats := &EndpointStats{
		EndpointID: endpointID,
	}
	lb.endpointStats.Store(endpointID, stats)
	return stats
}

// getSessionEndpoint retrieves the endpoint for a session
func (lb *L4LoadBalancer) getSessionEndpoint(serviceName, clientAddr string) string {
	key := lb.getSessionKey(serviceName, clientAddr)
	if entry, ok := lb.sessionCache.Load(key); ok {
		session := entry.(*sessionEntry)
		if time.Since(session.createdAt) < lb.config.SessionAffinityTTL {
			return session.endpointID
		}
		// Session expired
		lb.sessionCache.Delete(key)
	}
	return ""
}

// setSessionEndpoint sets the endpoint for a session
func (lb *L4LoadBalancer) setSessionEndpoint(serviceName, clientAddr, endpointID string) {
	key := lb.getSessionKey(serviceName, clientAddr)
	session := &sessionEntry{
		endpointID: endpointID,
		createdAt:  time.Now(),
	}
	lb.sessionCache.Store(key, session)
}

// getSessionKey generates a session key
func (lb *L4LoadBalancer) getSessionKey(serviceName, clientAddr string) string {
	h := fnv.New64a()
	h.Write([]byte(serviceName + ":" + clientAddr))
	return fmt.Sprintf("%x", h.Sum64())
}

// sessionEntry represents a session affinity entry
type sessionEntry struct {
	endpointID string
	createdAt  time.Time
}

// UpdateEndpoints updates endpoints for the load balancer
func (sr *ServiceRegistry) UpdateEndpoints(serviceName, namespace string, endpoints ...Endpoint) error {
	fullName := sr.getFullServiceName(serviceName, namespace)

	sr.mu.Lock()
	defer sr.mu.Unlock()

	// Replace all endpoints
	sr.endpoints.Store(fullName, endpoints)

	// Update service timestamp
	if s, ok := sr.services.Load(fullName); ok {
		service := s.(*Service)
		service.UpdatedAt = time.Now()
		service.Endpoints = endpoints
		sr.services.Store(fullName, service)
	}

	return nil
}
