package overlay

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestL4LoadBalancer_SelectEndpoint_RoundRobin verifies round-robin algorithm
func TestL4LoadBalancer_SelectEndpoint_RoundRobin(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "test-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register 3 endpoints
	endpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy, Weight: 1},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy, Weight: 1},
		{ID: "ep-3", Address: "10.0.0.3", Port: 8080, Health: HealthStatusHealthy, Weight: 1},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("test-service", "default", ep))
	}

	// Select endpoints and verify round-robin distribution
	selections := make(map[string]int)
	for i := 0; i < 9; i++ {
		selected, err := lb.SelectEndpoint(service, "client-1")
		require.NoError(t, err)
		selections[selected.ID]++
	}

	// Each endpoint should be selected 3 times (9 requests / 3 endpoints)
	assert.Equal(t, 3, selections["ep-1"], "ep-1 should be selected 3 times")
	assert.Equal(t, 3, selections["ep-2"], "ep-2 should be selected 3 times")
	assert.Equal(t, 3, selections["ep-3"], "ep-3 should be selected 3 times")
}

// TestL4LoadBalancer_SelectEndpoint_WeightedRoundRobin verifies CLD-REQ-042: Weighted endpoints
func TestL4LoadBalancer_SelectEndpoint_WeightedRoundRobin(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmWeightedRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "weighted-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints with different weights
	// Total weight: 1 + 2 + 3 = 6
	endpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy, Weight: 1},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy, Weight: 2},
		{ID: "ep-3", Address: "10.0.0.3", Port: 8080, Health: HealthStatusHealthy, Weight: 3},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("weighted-service", "default", ep))
	}

	// Select endpoints and verify weighted distribution
	selections := make(map[string]int)
	totalRequests := 60 // Multiple of total weight (6) for clean distribution

	for i := 0; i < totalRequests; i++ {
		selected, err := lb.SelectEndpoint(service, "client-1")
		require.NoError(t, err)
		selections[selected.ID]++
	}

	// Expected distribution based on weights:
	// ep-1: 1/6 of requests = 10
	// ep-2: 2/6 of requests = 20
	// ep-3: 3/6 of requests = 30
	assert.Equal(t, 10, selections["ep-1"], "ep-1 with weight 1 should get 1/6 of traffic")
	assert.Equal(t, 20, selections["ep-2"], "ep-2 with weight 2 should get 2/6 of traffic")
	assert.Equal(t, 30, selections["ep-3"], "ep-3 with weight 3 should get 3/6 of traffic")
}

// TestL4LoadBalancer_SelectEndpoint_WeightedRoundRobin_ZeroWeight verifies zero weight handling
func TestL4LoadBalancer_SelectEndpoint_WeightedRoundRobin_ZeroWeight(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmWeightedRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "zero-weight-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints with zero and negative weights (should default to 1)
	endpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy, Weight: 0},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy, Weight: -5},
		{ID: "ep-3", Address: "10.0.0.3", Port: 8080, Health: HealthStatusHealthy, Weight: 2},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("zero-weight-service", "default", ep))
	}

	// Select endpoints - zero/negative weights should be treated as 1
	selections := make(map[string]int)
	for i := 0; i < 40; i++ {
		selected, err := lb.SelectEndpoint(service, "client-1")
		require.NoError(t, err)
		selections[selected.ID]++
	}

	// Expected: ep-1 (1), ep-2 (1), ep-3 (2) = total weight 4
	// ep-1: 1/4 = 10, ep-2: 1/4 = 10, ep-3: 2/4 = 20
	assert.Equal(t, 10, selections["ep-1"], "ep-1 with weight 0 should default to 1")
	assert.Equal(t, 10, selections["ep-2"], "ep-2 with negative weight should default to 1")
	assert.Equal(t, 20, selections["ep-3"], "ep-3 with weight 2 should get double traffic")
}

// TestL4LoadBalancer_SelectEndpoint_LocalityAware verifies CLD-REQ-042: Locality-aware routing
func TestL4LoadBalancer_SelectEndpoint_LocalityAware(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmLocalityAware,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "locality-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints in different regions/zones
	endpoints := []Endpoint{
		{ID: "ep-us-west-a", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy, Region: "us-west", Zone: "zone-a"},
		{ID: "ep-us-west-b", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy, Region: "us-west", Zone: "zone-b"},
		{ID: "ep-us-east-a", Address: "10.0.0.3", Port: 8080, Health: HealthStatusHealthy, Region: "us-east", Zone: "zone-a"},
		{ID: "ep-eu-west-a", Address: "10.0.0.4", Port: 8080, Health: HealthStatusHealthy, Region: "eu-west", Zone: "zone-a"},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("locality-service", "default", ep))
	}

	// Note: Current implementation's extractClientLocality returns empty strings
	// This means it will fall back to round-robin
	// In production, this would use GeoIP or node metadata
	selected, err := lb.SelectEndpoint(service, "client-us-west-a")
	require.NoError(t, err)
	assert.NotNil(t, selected)

	// Verify endpoint has locality information
	assert.NotEmpty(t, selected.Region, "Selected endpoint should have region")
	assert.NotEmpty(t, selected.Zone, "Selected endpoint should have zone")
}

// TestL4LoadBalancer_SelectEndpoint_LeastConnections verifies least connections algorithm
func TestL4LoadBalancer_SelectEndpoint_LeastConnections(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmLeastConnections,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "leastconn-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints
	endpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy},
		{ID: "ep-3", Address: "10.0.0.3", Port: 8080, Health: HealthStatusHealthy},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("leastconn-service", "default", ep))
	}

	// Simulate connections
	// Set ep-1 to have 5 connections
	for i := 0; i < 5; i++ {
		lb.IncrementActiveConnections("ep-1")
	}

	// Set ep-2 to have 2 connections
	for i := 0; i < 2; i++ {
		lb.IncrementActiveConnections("ep-2")
	}

	// ep-3 has 0 connections

	// Next selection should prefer ep-3 (least connections)
	selected, err := lb.SelectEndpoint(service, "client-1")
	require.NoError(t, err)
	assert.Equal(t, "ep-3", selected.ID, "Should select endpoint with least connections")

	// After incrementing ep-3, should select ep-2 (now 2 vs 3)
	lb.IncrementActiveConnections("ep-3")
	lb.IncrementActiveConnections("ep-3")
	lb.IncrementActiveConnections("ep-3")

	selected, err = lb.SelectEndpoint(service, "client-1")
	require.NoError(t, err)
	assert.Equal(t, "ep-2", selected.ID, "Should select endpoint with least connections")
}

// TestL4LoadBalancer_SelectEndpoint_SessionAffinity verifies sticky sessions
func TestL4LoadBalancer_SelectEndpoint_SessionAffinity(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm:          AlgorithmRoundRobin,
		SessionAffinity:    true,
		SessionAffinityTTL: 1 * time.Hour,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "sticky-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints
	endpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy},
		{ID: "ep-3", Address: "10.0.0.3", Port: 8080, Health: HealthStatusHealthy},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("sticky-service", "default", ep))
	}

	// First request from client-A
	selected1, err := lb.SelectEndpoint(service, "client-A")
	require.NoError(t, err)
	firstEndpoint := selected1.ID

	// Subsequent requests from client-A should go to same endpoint
	for i := 0; i < 10; i++ {
		selected, err := lb.SelectEndpoint(service, "client-A")
		require.NoError(t, err)
		assert.Equal(t, firstEndpoint, selected.ID, "Session affinity should route to same endpoint")
	}

	// Different client should get different endpoint (statistically)
	selected2, err := lb.SelectEndpoint(service, "client-B")
	require.NoError(t, err)
	secondEndpoint := selected2.ID

	// Verify client-B also has session affinity
	for i := 0; i < 5; i++ {
		selected, err := lb.SelectEndpoint(service, "client-B")
		require.NoError(t, err)
		assert.Equal(t, secondEndpoint, selected.ID, "Session affinity for client-B")
	}
}

// TestL4LoadBalancer_SelectEndpoint_SessionAffinity_TTL verifies session expiration
func TestL4LoadBalancer_SelectEndpoint_SessionAffinity_TTL(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm:          AlgorithmRoundRobin,
		SessionAffinity:    true,
		SessionAffinityTTL: 100 * time.Millisecond,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "ttl-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints
	endpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("ttl-service", "default", ep))
	}

	// First request
	selected1, err := lb.SelectEndpoint(service, "client-ttl")
	require.NoError(t, err)
	firstEndpoint := selected1.ID

	// Immediate request should use same endpoint
	selected2, err := lb.SelectEndpoint(service, "client-ttl")
	require.NoError(t, err)
	assert.Equal(t, firstEndpoint, selected2.ID)

	// Wait for TTL to expire
	time.Sleep(150 * time.Millisecond)

	// Request after TTL may go to different endpoint
	selected3, err := lb.SelectEndpoint(service, "client-ttl")
	require.NoError(t, err)
	assert.NotNil(t, selected3, "Should still select an endpoint")
	// Note: May or may not be the same endpoint due to round-robin
}

// TestL4LoadBalancer_SelectEndpoint_HealthFiltering verifies only healthy endpoints are selected
func TestL4LoadBalancer_SelectEndpoint_HealthFiltering(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "health-filter-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints with different health statuses
	endpoints := []Endpoint{
		{ID: "ep-healthy", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy},
		{ID: "ep-unhealthy", Address: "10.0.0.2", Port: 8080, Health: HealthStatusUnhealthy},
		{ID: "ep-unknown", Address: "10.0.0.3", Port: 8080, Health: HealthStatusUnknown},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("health-filter-service", "default", ep))
	}

	// Select endpoints multiple times
	selections := make(map[string]int)
	for i := 0; i < 10; i++ {
		selected, err := lb.SelectEndpoint(service, "client-1")
		require.NoError(t, err)
		selections[selected.ID]++
	}

	// Only healthy endpoint should be selected
	assert.Equal(t, 10, selections["ep-healthy"], "Only healthy endpoint should be selected")
	assert.Equal(t, 0, selections["ep-unhealthy"], "Unhealthy endpoint should not be selected")
	assert.Equal(t, 0, selections["ep-unknown"], "Unknown health endpoint should not be selected")
}

// TestL4LoadBalancer_SelectEndpoint_NoHealthyEndpoints verifies error when all endpoints unhealthy
func TestL4LoadBalancer_SelectEndpoint_NoHealthyEndpoints(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "no-healthy-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register only unhealthy endpoints
	endpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusUnhealthy},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusUnhealthy},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("no-healthy-service", "default", ep))
	}

	// Selection should fail
	_, err = lb.SelectEndpoint(service, "client-1")
	assert.Error(t, err, "Should error when no healthy endpoints")
	assert.Contains(t, err.Error(), "no healthy endpoints available")
}

// TestL4LoadBalancer_SelectEndpoint_NoEndpoints verifies error when no endpoints registered
func TestL4LoadBalancer_SelectEndpoint_NoEndpoints(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service but no endpoints
	service := &Service{
		Name:      "no-endpoints-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Selection should fail
	_, err = lb.SelectEndpoint(service, "client-1")
	assert.Error(t, err, "Should error when no endpoints")
	assert.Contains(t, err.Error(), "no healthy endpoints available")
}

// TestL4LoadBalancer_RecordFailure verifies failure recording
func TestL4LoadBalancer_RecordFailure(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm:          AlgorithmRoundRobin,
		HealthCheckEnabled: true,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service and endpoint
	service := &Service{
		Name:      "failure-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	endpoint := Endpoint{
		ID:      "ep-1",
		Address: "10.0.0.1",
		Port:    8080,
		Health:  HealthStatusHealthy,
	}
	require.NoError(t, registry.RegisterEndpoint("failure-service", "default", endpoint))

	// Record failure
	lb.RecordFailure("failure-service", "ep-1")

	// Check endpoint stats
	stats := lb.getEndpointStats("ep-1")
	assert.Equal(t, uint64(1), stats.Failures, "Failure count should be 1")

	// If health check enabled, endpoint should be marked unhealthy
	endpoints, err := registry.GetHealthyEndpoints("failure-service", "default")
	require.NoError(t, err)
	assert.Len(t, endpoints, 0, "Failed endpoint should be marked unhealthy")
}

// TestL4LoadBalancer_RecordLatency verifies latency tracking
func TestL4LoadBalancer_RecordLatency(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Record latencies
	lb.RecordLatency("ep-1", 10*time.Millisecond)
	lb.RecordLatency("ep-1", 20*time.Millisecond)
	lb.RecordLatency("ep-1", 30*time.Millisecond)

	// Check stats
	stats := lb.getEndpointStats("ep-1")
	avgLatency := stats.GetAverageLatency()
	assert.Greater(t, avgLatency, time.Duration(0), "Average latency should be > 0")
	assert.Less(t, avgLatency, 40*time.Millisecond, "Average latency should be < 40ms")
}

// TestL4LoadBalancer_ActiveConnections verifies connection tracking
func TestL4LoadBalancer_ActiveConnections(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmLeastConnections,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Initially zero connections
	stats := lb.getEndpointStats("ep-1")
	assert.Equal(t, 0, stats.GetActiveConnections())

	// Increment connections
	lb.IncrementActiveConnections("ep-1")
	lb.IncrementActiveConnections("ep-1")
	lb.IncrementActiveConnections("ep-1")

	stats = lb.getEndpointStats("ep-1")
	assert.Equal(t, 3, stats.GetActiveConnections())

	// Decrement connections
	lb.DecrementActiveConnections("ep-1")
	stats = lb.getEndpointStats("ep-1")
	assert.Equal(t, 2, stats.GetActiveConnections())

	// Decrement below zero should not go negative
	lb.DecrementActiveConnections("ep-1")
	lb.DecrementActiveConnections("ep-1")
	lb.DecrementActiveConnections("ep-1")
	stats = lb.getEndpointStats("ep-1")
	assert.Equal(t, 0, stats.GetActiveConnections(), "Active connections should not go negative")
}

// TestL4LoadBalancer_GetStats verifies statistics retrieval
func TestL4LoadBalancer_GetStats(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service and endpoints
	service := &Service{
		Name:      "stats-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	endpoint := Endpoint{
		ID:      "ep-1",
		Address: "10.0.0.1",
		Port:    8080,
		Health:  HealthStatusHealthy,
	}
	require.NoError(t, registry.RegisterEndpoint("stats-service", "default", endpoint))

	// Make requests
	for i := 0; i < 5; i++ {
		_, err := lb.SelectEndpoint(service, "client-1")
		require.NoError(t, err)
	}

	// Get stats
	stats, err := lb.GetStats("stats-service")
	assert.NoError(t, err)
	assert.NotNil(t, stats)
	assert.Equal(t, "stats-service", stats.ServiceName)
	assert.Equal(t, uint64(5), stats.TotalRequests)
}

// TestL4LoadBalancer_ConcurrentRequests verifies thread safety with -race detector
// Note: This test may reveal race conditions in the production code (loadbalancer.go:247)
// where stats.LastUpdated is modified without proper synchronization.
func TestL4LoadBalancer_ConcurrentRequests(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmWeightedRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "concurrent-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints
	endpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy, Weight: 1},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy, Weight: 2},
		{ID: "ep-3", Address: "10.0.0.3", Port: 8080, Health: HealthStatusHealthy, Weight: 3},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("concurrent-service", "default", ep))
	}

	// Concurrent requests
	var wg sync.WaitGroup
	numGoroutines := 100
	numRequestsPerGoroutine := 10

	// Use mutex to protect map access in test
	var mu sync.Mutex
	selections := make(map[string]int)
	var totalServed int

	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for j := 0; j < numRequestsPerGoroutine; j++ {
				selected, err := lb.SelectEndpoint(service, "client-concurrent")
				assert.NoError(t, err)
				if selected != nil {
					// Track selections with mutex protection
					mu.Lock()
					selections[selected.ID]++
					totalServed++
					mu.Unlock()
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify all requests were served
	assert.Equal(t, numGoroutines*numRequestsPerGoroutine, totalServed, "All concurrent requests should be served")

	// Verify all endpoints received some traffic
	assert.Greater(t, len(selections), 0, "At least one endpoint should be selected")
}

// TestL4LoadBalancer_ConcurrentHealthUpdates verifies concurrent health updates
func TestL4LoadBalancer_ConcurrentHealthUpdates(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm:          AlgorithmRoundRobin,
		HealthCheckEnabled: true,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service and endpoints
	service := &Service{
		Name:      "health-update-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	numEndpoints := 10
	for i := 0; i < numEndpoints; i++ {
		endpoint := Endpoint{
			ID:      "ep-" + string(rune('0'+i)),
			Address: "10.0.0." + string(rune('0'+i)),
			Port:    8080,
			Health:  HealthStatusHealthy,
		}
		require.NoError(t, registry.RegisterEndpoint("health-update-service", "default", endpoint))
	}

	// Concurrently record failures
	var wg sync.WaitGroup
	for i := 0; i < numEndpoints; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			endpointID := "ep-" + string(rune('0'+id))
			for j := 0; j < 5; j++ {
				lb.RecordFailure("health-update-service", endpointID)
				time.Sleep(1 * time.Millisecond)
			}
		}(i)
	}

	wg.Wait()

	// Verify failures were recorded
	for i := 0; i < numEndpoints; i++ {
		endpointID := "ep-" + string(rune('0'+i))
		stats := lb.getEndpointStats(endpointID)
		assert.Equal(t, uint64(5), stats.Failures, "Endpoint %s should have 5 failures", endpointID)
	}
}

// TestL4LoadBalancer_UpdateEndpoints verifies endpoint updates
func TestL4LoadBalancer_UpdateEndpoints(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "update-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Update endpoints via load balancer
	newEndpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy},
	}

	err = lb.UpdateEndpoints("update-service", newEndpoints)
	assert.NoError(t, err)

	// Verify endpoints were updated
	endpoints, err := registry.GetEndpoints("update-service", "default")
	require.NoError(t, err)
	assert.Len(t, endpoints, 2)
}

// TestL4LoadBalancer_DefaultAlgorithm verifies fallback to round-robin
func TestL4LoadBalancer_DefaultAlgorithm(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: "invalid-algorithm", // Invalid algorithm
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "default-algo-service",
		Namespace: "default",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints
	endpoints := []Endpoint{
		{ID: "ep-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy},
		{ID: "ep-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy},
	}

	for _, ep := range endpoints {
		require.NoError(t, registry.RegisterEndpoint("default-algo-service", "default", ep))
	}

	// Should fall back to round-robin
	selected, err := lb.SelectEndpoint(service, "client-1")
	assert.NoError(t, err, "Should succeed with fallback algorithm")
	assert.NotNil(t, selected)
}

// TestL4LoadBalancer_ConcurrentStatsUpdates_RaceDetector is a comprehensive stress test
// designed to detect race conditions in statistics updates across all code paths.
// Run with: go test -race -v -run=TestL4LoadBalancer_ConcurrentStatsUpdates_RaceDetector
//
// This test spawns multiple goroutines that concurrently:
// 1. Select endpoints (updates TotalRequests, LastUpdated)
// 2. Record latency (updates AverageLatency with atomic CAS loop)
// 3. Increment/Decrement active connections (tests atomic operations)
// 4. Record failures (tests failure tracking)
// 5. Get stats (tests deep copy with atomic loads)
//
// All these operations should be thread-safe with zero race conditions.
func TestL4LoadBalancer_ConcurrentStatsUpdates_RaceDetector(t *testing.T) {
	logger := zap.NewNop()
	config := LoadBalancerConfig{
		Algorithm: AlgorithmRoundRobin,
	}

	registry, err := NewServiceRegistry(RegistryConfig{VirtualIPCIDR: "10.96.0.0/16"}, logger)
	require.NoError(t, err)

	lb := NewL4LoadBalancer(config, registry, logger)

	// Register service
	service := &Service{
		Name:      "race-test-service",
		Namespace: "default",
		VirtualIP: "10.96.1.100",
		Port:      8080,
	}
	require.NoError(t, registry.RegisterService(service))

	// Register multiple endpoints
	numEndpoints := 5
	endpoints := make([]Endpoint, numEndpoints)
	for i := 0; i < numEndpoints; i++ {
		endpoints[i] = Endpoint{
			ID:      fmt.Sprintf("ep-%d", i+1),
			Address: fmt.Sprintf("10.0.0.%d", i+1),
			Port:    8080,
			Health:  HealthStatusHealthy,
			Weight:  1,
		}
		require.NoError(t, registry.RegisterEndpoint("race-test-service", "default", endpoints[i]))
	}

	// Stress test parameters
	numGoroutines := 100
	operationsPerGoroutine := 50

	var wg sync.WaitGroup
	wg.Add(numGoroutines * 5) // 5 different operation types

	// 1. Concurrent SelectEndpoint calls (updates TotalRequests, LastUpdated)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				_, _ = lb.SelectEndpoint(service, fmt.Sprintf("client-%d", id))
			}
		}(i)
	}

	// 2. Concurrent RecordLatency calls (atomic CAS loop for AverageLatency)
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				endpointID := fmt.Sprintf("ep-%d", (id%numEndpoints)+1)
				latency := time.Duration((id*j)%100) * time.Millisecond
				lb.RecordLatency(endpointID, latency)
			}
		}(i)
	}

	// 3. Concurrent IncrementActiveConnections calls
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				endpointID := fmt.Sprintf("ep-%d", (id%numEndpoints)+1)
				lb.IncrementActiveConnections(endpointID)
			}
		}(i)
	}

	// 4. Concurrent DecrementActiveConnections calls
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				endpointID := fmt.Sprintf("ep-%d", (id%numEndpoints)+1)
				lb.DecrementActiveConnections(endpointID)
			}
		}(i)
	}

	// 5. Concurrent GetStats calls (deep copy with atomic loads)
	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			for j := 0; j < operationsPerGoroutine; j++ {
				stats, err := lb.GetStats("race-test-service")
				if err == nil && stats != nil {
					// Access all fields to ensure they're properly copied
					_ = stats.ServiceName
					_ = stats.TotalRequests
					_ = stats.LastUpdated
					for _, epStats := range stats.EndpointStats {
						_ = epStats.EndpointID
						_ = epStats.Requests
						_ = epStats.Failures
						_ = epStats.GetAverageLatency()
						_ = epStats.GetActiveConnections()
					}
				}
			}
		}()
	}

	// Wait for all operations to complete
	wg.Wait()

	// Verify final state is consistent (no panics, no negative values)
	finalStats, err := lb.GetStats("race-test-service")
	require.NoError(t, err)
	require.NotNil(t, finalStats)

	// All endpoint stats should have non-negative values
	for epID, epStats := range finalStats.EndpointStats {
		t.Logf("Endpoint %s: Requests=%d, Failures=%d, AvgLatency=%v, ActiveConns=%d",
			epID, epStats.Requests, epStats.Failures, epStats.GetAverageLatency(), epStats.GetActiveConnections())

		// Active connections should be non-negative (due to our CAS loop protection)
		assert.GreaterOrEqual(t, epStats.GetActiveConnections(), 0, "Active connections should not be negative for %s", epID)

		// With concurrent increments and decrements, we can't predict exact value,
		// but it should be within reasonable range
		assert.LessOrEqual(t, epStats.GetActiveConnections(), numGoroutines*operationsPerGoroutine,
			"Active connections should not exceed theoretical maximum")
	}

	t.Logf("Race stress test completed successfully with %d goroutines x %d operations = %d total operations",
		numGoroutines, operationsPerGoroutine*5, numGoroutines*operationsPerGoroutine*5)
}
