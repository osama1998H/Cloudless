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
	minConnections := int(^uint(0) >> 1) // Max int

	for i := range endpoints {
		stats := lb.getEndpointStats(endpoints[i].ID)
		if stats.ActiveConnections < minConnections {
			minConnections = stats.ActiveConnections
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
func (lb *L4LoadBalancer) GetStats(serviceName string) (*LoadBalancerStats, error) {
	if s, ok := lb.stats.Load(serviceName); ok {
		stats := s.(*LoadBalancerStats)
		return stats, nil
	}

	// Return empty stats if service not found
	return &LoadBalancerStats{
		ServiceName:   serviceName,
		EndpointStats: make(map[string]*EndpointStats),
		LastUpdated:   time.Now(),
	}, nil
}

// updateRequestStats updates request statistics
func (lb *L4LoadBalancer) updateRequestStats(serviceName, endpointID string) {
	// Update service stats
	var stats *LoadBalancerStats
	if s, ok := lb.stats.Load(serviceName); ok {
		stats = s.(*LoadBalancerStats)
	} else {
		stats = &LoadBalancerStats{
			ServiceName:   serviceName,
			EndpointStats: make(map[string]*EndpointStats),
			LastUpdated:   time.Now(),
		}
	}

	atomic.AddUint64(&stats.TotalRequests, 1)
	stats.LastUpdated = time.Now()
	lb.stats.Store(serviceName, stats)

	// Update endpoint stats
	epStats := lb.getEndpointStats(endpointID)
	atomic.AddUint64(&epStats.Requests, 1)
	lb.endpointStats.Store(endpointID, epStats)
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
func (lb *L4LoadBalancer) RecordLatency(endpointID string, latency time.Duration) {
	epStats := lb.getEndpointStats(endpointID)

	// Calculate running average (simplified)
	currentAvg := epStats.AverageLatency
	requests := atomic.LoadUint64(&epStats.Requests)

	if requests == 0 {
		epStats.AverageLatency = latency
	} else {
		// Exponential moving average
		alpha := 0.1
		newAvg := time.Duration(float64(currentAvg)*(1-alpha) + float64(latency)*alpha)
		epStats.AverageLatency = newAvg
	}

	lb.endpointStats.Store(endpointID, epStats)
}

// IncrementActiveConnections increments the active connection count for an endpoint
func (lb *L4LoadBalancer) IncrementActiveConnections(endpointID string) {
	epStats := lb.getEndpointStats(endpointID)
	epStats.ActiveConnections++
	lb.endpointStats.Store(endpointID, epStats)
}

// DecrementActiveConnections decrements the active connection count for an endpoint
func (lb *L4LoadBalancer) DecrementActiveConnections(endpointID string) {
	epStats := lb.getEndpointStats(endpointID)
	if epStats.ActiveConnections > 0 {
		epStats.ActiveConnections--
	}
	lb.endpointStats.Store(endpointID, epStats)
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
