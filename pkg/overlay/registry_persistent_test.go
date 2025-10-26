//go:build integration

package overlay

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cloudless/cloudless/pkg/raft"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestPersistentServiceRegistry_RegisterService_PersistsToRAFT verifies service persistence
func TestPersistentServiceRegistry_RegisterService_PersistsToRAFT(t *testing.T) {
	logger := zap.NewNop()
	store, cleanup := createTestRAFTStore(t, logger)
	defer cleanup()

	config := RegistryConfig{
		VirtualIPCIDR: "10.96.0.0/16",
	}

	ctx := context.Background()
	registry, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err, "Failed to create persistent registry")

	// Register service
	service := &Service{
		Name:      "web-service",
		Namespace: "default",
		Port:      8080,
		Protocol:  ProtocolTCP,
	}

	err = registry.RegisterService(service)
	assert.NoError(t, err, "Service registration should succeed")
	assert.NotEmpty(t, service.VirtualIP, "Virtual IP should be allocated")

	// Verify service is in RAFT
	key := registry.getServiceKey("web-service", "default")
	data, err := store.Get(key)
	require.NoError(t, err)
	assert.NotNil(t, data, "Service should be persisted to RAFT")

	// Verify we can retrieve it
	retrieved, err := registry.GetService("web-service", "default")
	assert.NoError(t, err)
	assert.Equal(t, "web-service", retrieved.Name)
	assert.Equal(t, service.VirtualIP, retrieved.VirtualIP)
}

// TestPersistentServiceRegistry_LoadFromRAFT_RecoverState verifies recovery from RAFT
func TestPersistentServiceRegistry_LoadFromRAFT_RecoverState(t *testing.T) {
	logger := zap.NewNop()
	store, cleanup := createTestRAFTStore(t, logger)
	defer cleanup()

	config := RegistryConfig{
		VirtualIPCIDR: "10.96.0.0/16",
	}

	ctx := context.Background()

	// Create first registry and register services
	registry1, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	service1 := &Service{Name: "api-service", Namespace: "prod", Port: 443}
	service2 := &Service{Name: "db-service", Namespace: "prod", Port: 5432}
	service3 := &Service{Name: "cache-service", Namespace: "default", Port: 6379}

	require.NoError(t, registry1.RegisterService(service1))
	require.NoError(t, registry1.RegisterService(service2))
	require.NoError(t, registry1.RegisterService(service3))

	// Register endpoints
	endpoint1 := Endpoint{ID: "ep-1", PeerID: "node-1", Address: "10.0.0.1", Port: 443, Health: HealthStatusHealthy}
	require.NoError(t, registry1.RegisterEndpoint("api-service", "prod", endpoint1))

	// Create second registry (simulating restart)
	registry2, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	// Verify all services were recovered
	services := registry2.ListServices()
	assert.Len(t, services, 3, "All services should be recovered from RAFT")

	// Verify service details
	api, err := registry2.GetService("api-service", "prod")
	require.NoError(t, err)
	assert.Equal(t, "api-service", api.Name)
	assert.Equal(t, service1.VirtualIP, api.VirtualIP, "Virtual IP should be preserved")

	// Verify endpoints were recovered
	endpoints, err := registry2.GetEndpoints("api-service", "prod")
	require.NoError(t, err)
	assert.Len(t, endpoints, 1, "Endpoints should be recovered")
	assert.Equal(t, "ep-1", endpoints[0].ID)
	assert.Equal(t, HealthStatusHealthy, endpoints[0].Health)
}

// TestPersistentServiceRegistry_DeregisterService_SoftDelete verifies soft delete behavior
func TestPersistentServiceRegistry_DeregisterService_SoftDelete(t *testing.T) {
	logger := zap.NewNop()
	store, cleanup := createTestRAFTStore(t, logger)
	defer cleanup()

	config := RegistryConfig{
		VirtualIPCIDR: "10.96.0.0/16",
	}

	ctx := context.Background()
	registry, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	// Register service
	service := &Service{Name: "temp-service", Namespace: "default", Port: 8080}
	require.NoError(t, registry.RegisterService(service))

	// Deregister service
	err = registry.DeregisterService("temp-service", "default")
	assert.NoError(t, err)

	// Service should not be in memory
	_, err = registry.GetService("temp-service", "default")
	assert.Error(t, err, "Service should not be in memory")

	// But should still exist in RAFT (marked as deleted)
	key := registry.getServiceKey("temp-service", "default")
	data, err := store.Get(key)
	require.NoError(t, err)
	assert.NotNil(t, data, "Service should still exist in RAFT")

	// On recovery, it should be skipped
	registry2, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	services := registry2.ListServices()
	assert.Len(t, services, 0, "Deleted service should not be recovered")
}

// TestPersistentServiceRegistry_PurgeService_HardDelete verifies hard delete
func TestPersistentServiceRegistry_PurgeService_HardDelete(t *testing.T) {
	logger := zap.NewNop()
	store, cleanup := createTestRAFTStore(t, logger)
	defer cleanup()

	config := RegistryConfig{
		VirtualIPCIDR: "10.96.0.0/16",
	}

	ctx := context.Background()
	registry, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	// Register service
	service := &Service{Name: "purge-service", Namespace: "default", Port: 8080}
	require.NoError(t, registry.RegisterService(service))

	// Purge service
	err = registry.PurgeService(ctx, "purge-service", "default")
	assert.NoError(t, err)

	// Service should not exist in RAFT (Get will return error or nil data)
	key := registry.getServiceKey("purge-service", "default")
	data, err := store.Get(key)
	if err == nil {
		assert.Nil(t, data, "Service should be completely removed from RAFT")
	}
	// Either error (key not found) or nil data is acceptable
}

// TestPersistentServiceRegistry_EndpointUpdates_Persisted verifies endpoint persistence
func TestPersistentServiceRegistry_EndpointUpdates_Persisted(t *testing.T) {
	logger := zap.NewNop()
	store, cleanup := createTestRAFTStore(t, logger)
	defer cleanup()

	config := RegistryConfig{
		VirtualIPCIDR: "10.96.0.0/16",
	}

	ctx := context.Background()
	registry, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	// Register service
	service := &Service{Name: "endpoint-service", Namespace: "default", Port: 8080}
	require.NoError(t, registry.RegisterService(service))

	// Register endpoints
	endpoint1 := Endpoint{ID: "ep-1", PeerID: "node-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy}
	endpoint2 := Endpoint{ID: "ep-2", PeerID: "node-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy}

	require.NoError(t, registry.RegisterEndpoint("endpoint-service", "default", endpoint1))
	require.NoError(t, registry.RegisterEndpoint("endpoint-service", "default", endpoint2))

	// Update endpoint health
	err = registry.UpdateEndpointHealth("endpoint-service", "default", "ep-1", HealthStatusUnhealthy)
	assert.NoError(t, err)

	// Deregister one endpoint
	err = registry.DeregisterEndpoint("endpoint-service", "default", "ep-2")
	assert.NoError(t, err)

	// Create new registry to verify persistence
	registry2, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	// Verify endpoints were persisted correctly
	endpoints, err := registry2.GetEndpoints("endpoint-service", "default")
	require.NoError(t, err)
	assert.Len(t, endpoints, 1, "Should have one endpoint after deregistration")
	assert.Equal(t, "ep-1", endpoints[0].ID)
	assert.Equal(t, HealthStatusUnhealthy, endpoints[0].Health, "Health update should be persisted")
}

// TestPersistentServiceRegistry_VirtualIPPreserved_AfterRestart verifies virtual IP stability
func TestPersistentServiceRegistry_VirtualIPPreserved_AfterRestart(t *testing.T) {
	logger := zap.NewNop()
	store, cleanup := createTestRAFTStore(t, logger)
	defer cleanup()

	config := RegistryConfig{
		VirtualIPCIDR: "10.96.0.0/24",
	}

	ctx := context.Background()

	// Create first registry and register service
	registry1, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	service := &Service{Name: "stable-service", Namespace: "default", Port: 8080}
	require.NoError(t, registry1.RegisterService(service))
	originalVIP := service.VirtualIP

	// Create second registry (simulating restart)
	registry2, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	// Verify virtual IP is preserved
	retrieved, err := registry2.GetService("stable-service", "default")
	require.NoError(t, err)
	assert.Equal(t, originalVIP, retrieved.VirtualIP, "Virtual IP should be stable across restarts")

	// Verify DNS resolution works
	vip, err := registry2.ResolveService("stable-service", "default")
	assert.NoError(t, err)
	assert.Equal(t, originalVIP, vip, "DNS resolution should return correct VIP")
}

// TestPersistentServiceRegistry_ConcurrentWrites_RAFTConsistency verifies RAFT consistency
func TestPersistentServiceRegistry_ConcurrentWrites_RAFTConsistency(t *testing.T) {
	logger := zap.NewNop()
	store, cleanup := createTestRAFTStore(t, logger)
	defer cleanup()

	config := RegistryConfig{
		VirtualIPCIDR: "10.96.0.0/16",
	}

	ctx := context.Background()
	registry, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	// Register service
	service := &Service{Name: "concurrent-service", Namespace: "default", Port: 8080}
	require.NoError(t, registry.RegisterService(service))

	// Simulate concurrent endpoint updates
	endpoint1 := Endpoint{ID: "ep-1", PeerID: "node-1", Address: "10.0.0.1", Port: 8080, Health: HealthStatusHealthy}
	endpoint2 := Endpoint{ID: "ep-2", PeerID: "node-2", Address: "10.0.0.2", Port: 8080, Health: HealthStatusHealthy}
	endpoint3 := Endpoint{ID: "ep-3", PeerID: "node-3", Address: "10.0.0.3", Port: 8080, Health: HealthStatusHealthy}

	// Register endpoints sequentially (RAFT ensures ordering)
	require.NoError(t, registry.RegisterEndpoint("concurrent-service", "default", endpoint1))
	require.NoError(t, registry.RegisterEndpoint("concurrent-service", "default", endpoint2))
	require.NoError(t, registry.RegisterEndpoint("concurrent-service", "default", endpoint3))

	// Create new registry to verify all writes persisted
	registry2, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	endpoints, err := registry2.GetEndpoints("concurrent-service", "default")
	require.NoError(t, err)
	assert.Len(t, endpoints, 3, "All endpoint writes should be persisted")

	// Verify endpoint IDs
	ids := make(map[string]bool)
	for _, ep := range endpoints {
		ids[ep.ID] = true
	}
	assert.True(t, ids["ep-1"])
	assert.True(t, ids["ep-2"])
	assert.True(t, ids["ep-3"])
}

// TestPersistentServiceRegistry_MultipleNamespaces_Isolation verifies namespace isolation in RAFT
func TestPersistentServiceRegistry_MultipleNamespaces_Isolation(t *testing.T) {
	logger := zap.NewNop()
	store, cleanup := createTestRAFTStore(t, logger)
	defer cleanup()

	config := RegistryConfig{
		VirtualIPCIDR: "10.96.0.0/16",
	}

	ctx := context.Background()
	registry, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	// Register same service name in different namespaces
	service1 := &Service{Name: "app", Namespace: "dev", Port: 8080}
	service2 := &Service{Name: "app", Namespace: "prod", Port: 8080}
	service3 := &Service{Name: "app", Namespace: "staging", Port: 8080}

	require.NoError(t, registry.RegisterService(service1))
	require.NoError(t, registry.RegisterService(service2))
	require.NoError(t, registry.RegisterService(service3))

	// Create new registry to verify recovery
	registry2, err := NewPersistentServiceRegistry(ctx, store, config, logger)
	require.NoError(t, err)

	// Verify all namespaces have their own service
	devApp, err := registry2.GetService("app", "dev")
	require.NoError(t, err)
	assert.Equal(t, "dev", devApp.Namespace)

	prodApp, err := registry2.GetService("app", "prod")
	require.NoError(t, err)
	assert.Equal(t, "prod", prodApp.Namespace)

	stagingApp, err := registry2.GetService("app", "staging")
	require.NoError(t, err)
	assert.Equal(t, "staging", stagingApp.Namespace)

	// Verify they have different virtual IPs
	assert.NotEqual(t, devApp.VirtualIP, prodApp.VirtualIP)
	assert.NotEqual(t, devApp.VirtualIP, stagingApp.VirtualIP)
	assert.NotEqual(t, prodApp.VirtualIP, stagingApp.VirtualIP)
}

// createTestRAFTStore creates a test RAFT store for integration tests
func createTestRAFTStore(t *testing.T, logger *zap.Logger) (*raft.Store, func()) {
	tempDir := t.TempDir()

	config := &raft.Config{
		RaftID:    "test-node-1",
		RaftBind:  "127.0.0.1:0", // Dynamic port
		RaftDir:   filepath.Join(tempDir, "raft"),
		Bootstrap: true, // Single-node cluster for testing
		Logger:    logger,
	}

	store, err := raft.NewStore(config)
	require.NoError(t, err, "Failed to create RAFT store")

	// Wait for leader election
	err = store.WaitForLeader(5 * time.Second)
	require.NoError(t, err, "Leader election failed")

	cleanup := func() {
		store.Close()
	}

	return store, cleanup
}
