package overlay

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/osama1998H/Cloudless/pkg/raft"
	"go.uber.org/zap"
)

// PersistentServiceRegistry is a RAFT-backed service registry with durability
//
// Architecture:
//   - In-memory ServiceRegistry for fast reads
//   - Write-through cache to RAFT for persistence
//   - Automatic recovery from RAFT on startup
//
// Usage:
//
//	registry, err := NewPersistentServiceRegistry(ctx, raftStore, config, logger)
//	registry.RegisterService(service) // Persisted to RAFT
type PersistentServiceRegistry struct {
	// Embedded in-memory registry for fast reads
	*ServiceRegistry

	// RAFT backend for persistence
	store  *raft.Store
	logger *zap.Logger
	mu     sync.RWMutex
}

// ServicePersistentState represents the state persisted to RAFT
type ServicePersistentState struct {
	Service   *Service   `json:"service"`
	Endpoints []Endpoint `json:"endpoints"`
	CreatedAt time.Time  `json:"created_at"`
	UpdatedAt time.Time  `json:"updated_at"`
	DeletedAt *time.Time `json:"deleted_at,omitempty"`
}

// NewPersistentServiceRegistry creates a new RAFT-backed service registry
func NewPersistentServiceRegistry(
	ctx context.Context,
	store *raft.Store,
	config RegistryConfig,
	logger *zap.Logger,
) (*PersistentServiceRegistry, error) {
	if store == nil {
		return nil, fmt.Errorf("raft store is required")
	}
	if logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	// Create in-memory registry
	memRegistry, err := NewServiceRegistry(config, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create in-memory registry: %w", err)
	}

	psr := &PersistentServiceRegistry{
		ServiceRegistry: memRegistry,
		store:           store,
		logger:          logger,
	}

	// Load existing services from RAFT on startup
	if err := psr.loadFromRAFT(ctx); err != nil {
		logger.Warn("Failed to load services from RAFT", zap.Error(err))
		// Continue anyway - RAFT might be empty on first start
	}

	return psr, nil
}

// RegisterService persists service to RAFT with quorum write
func (psr *PersistentServiceRegistry) RegisterService(service *Service) error {
	// First register in memory (handles virtual IP allocation)
	if err := psr.ServiceRegistry.RegisterService(service); err != nil {
		return err
	}

	// Then persist to RAFT
	return psr.persistService(service)
}

// DeregisterService removes service from memory and marks as deleted in RAFT
func (psr *PersistentServiceRegistry) DeregisterService(name, namespace string) error {
	// First deregister from memory
	if err := psr.ServiceRegistry.DeregisterService(name, namespace); err != nil {
		return err
	}

	// Mark as deleted in RAFT (soft delete)
	return psr.markServiceDeleted(name, namespace)
}

// RegisterEndpoint persists endpoint to RAFT
func (psr *PersistentServiceRegistry) RegisterEndpoint(serviceName, namespace string, endpoint Endpoint) error {
	// First register in memory
	if err := psr.ServiceRegistry.RegisterEndpoint(serviceName, namespace, endpoint); err != nil {
		return err
	}

	// Then persist to RAFT
	service, err := psr.ServiceRegistry.GetService(serviceName, namespace)
	if err != nil {
		return err
	}

	return psr.persistService(service)
}

// DeregisterEndpoint removes endpoint from memory and persists to RAFT
func (psr *PersistentServiceRegistry) DeregisterEndpoint(serviceName, namespace, endpointID string) error {
	// First deregister from memory
	if err := psr.ServiceRegistry.DeregisterEndpoint(serviceName, namespace, endpointID); err != nil {
		return err
	}

	// Then persist to RAFT
	service, err := psr.ServiceRegistry.GetService(serviceName, namespace)
	if err != nil {
		return err
	}

	return psr.persistService(service)
}

// UpdateEndpointHealth updates health status in memory and persists to RAFT
func (psr *PersistentServiceRegistry) UpdateEndpointHealth(serviceName, namespace, endpointID string, health HealthStatus) error {
	// First update in memory
	if err := psr.ServiceRegistry.UpdateEndpointHealth(serviceName, namespace, endpointID, health); err != nil {
		return err
	}

	// Then persist to RAFT
	service, err := psr.ServiceRegistry.GetService(serviceName, namespace)
	if err != nil {
		return err
	}

	return psr.persistService(service)
}

// PurgeService permanently removes a service from RAFT (hard delete)
func (psr *PersistentServiceRegistry) PurgeService(ctx context.Context, name, namespace string) error {
	psr.mu.Lock()
	defer psr.mu.Unlock()

	key := psr.getServiceKey(name, namespace)
	if err := psr.store.Delete(key); err != nil {
		return fmt.Errorf("failed to delete from RAFT: %w", err)
	}

	psr.logger.Info("Service purged from RAFT",
		zap.String("name", name),
		zap.String("namespace", namespace),
	)

	return nil
}

// persistService writes service and its endpoints to RAFT
func (psr *PersistentServiceRegistry) persistService(service *Service) error {
	psr.mu.Lock()
	defer psr.mu.Unlock()

	// Get current endpoints
	endpoints, _ := psr.ServiceRegistry.GetEndpoints(service.Name, service.Namespace)

	// Create persistent state
	state := ServicePersistentState{
		Service:   service,
		Endpoints: endpoints,
		CreatedAt: service.CreatedAt,
		UpdatedAt: time.Now(),
	}

	// Serialize to JSON
	data, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal service: %w", err)
	}

	// Write to RAFT
	key := psr.getServiceKey(service.Name, service.Namespace)
	if err := psr.store.Set(key, data); err != nil {
		return fmt.Errorf("failed to write to RAFT: %w", err)
	}

	psr.logger.Debug("Service persisted to RAFT",
		zap.String("name", service.Name),
		zap.String("namespace", service.Namespace),
		zap.String("virtual_ip", service.VirtualIP),
		zap.Int("endpoints", len(endpoints)),
	)

	return nil
}

// markServiceDeleted marks a service as soft deleted in RAFT
func (psr *PersistentServiceRegistry) markServiceDeleted(name, namespace string) error {
	psr.mu.Lock()
	defer psr.mu.Unlock()

	// Read current state from RAFT
	key := psr.getServiceKey(name, namespace)
	data, err := psr.store.Get(key)
	if err != nil {
		return fmt.Errorf("failed to read from RAFT: %w", err)
	}
	if data == nil {
		// Already deleted or never existed
		return nil
	}

	var state ServicePersistentState
	if err := json.Unmarshal(data, &state); err != nil {
		return fmt.Errorf("failed to unmarshal service: %w", err)
	}

	// Mark as deleted
	now := time.Now()
	state.DeletedAt = &now
	state.UpdatedAt = now

	// Write back to RAFT
	updatedData, err := json.Marshal(state)
	if err != nil {
		return fmt.Errorf("failed to marshal service: %w", err)
	}

	if err := psr.store.Set(key, updatedData); err != nil {
		return fmt.Errorf("failed to write to RAFT: %w", err)
	}

	psr.logger.Info("Service marked as deleted in RAFT",
		zap.String("name", name),
		zap.String("namespace", namespace),
	)

	return nil
}

// loadFromRAFT loads all services from RAFT into in-memory registry
func (psr *PersistentServiceRegistry) loadFromRAFT(ctx context.Context) error {
	// Get all keys with service prefix
	keys, err := psr.store.ListKeys("service:")
	if err != nil {
		return fmt.Errorf("failed to list keys: %w", err)
	}

	psr.logger.Info("Loading services from RAFT",
		zap.Int("count", len(keys)),
	)

	psr.mu.Lock()
	defer psr.mu.Unlock()

	loaded := 0
	skipped := 0

	for _, key := range keys {
		data, err := psr.store.Get(key)
		if err != nil {
			psr.logger.Warn("Failed to load service from RAFT",
				zap.String("key", key),
				zap.Error(err),
			)
			continue
		}

		var state ServicePersistentState
		if err := json.Unmarshal(data, &state); err != nil {
			psr.logger.Warn("Failed to unmarshal service",
				zap.String("key", key),
				zap.Error(err),
			)
			continue
		}

		// Skip deleted services
		if state.DeletedAt != nil {
			skipped++
			continue
		}

		// Register service in memory (this will allocate virtual IP if needed)
		if err := psr.ServiceRegistry.RegisterService(state.Service); err != nil {
			psr.logger.Warn("Failed to register service from RAFT",
				zap.String("name", state.Service.Name),
				zap.String("namespace", state.Service.Namespace),
				zap.Error(err),
			)
			continue
		}

		// Register endpoints
		for _, endpoint := range state.Endpoints {
			if err := psr.ServiceRegistry.RegisterEndpoint(state.Service.Name, state.Service.Namespace, endpoint); err != nil {
				psr.logger.Warn("Failed to register endpoint from RAFT",
					zap.String("service", state.Service.Name),
					zap.String("endpoint_id", endpoint.ID),
					zap.Error(err),
				)
			}
		}

		loaded++
	}

	psr.logger.Info("Loaded services from RAFT",
		zap.Int("loaded", loaded),
		zap.Int("skipped", skipped),
		zap.Int("failed", len(keys)-loaded-skipped),
	)

	return nil
}

// getServiceKey generates a RAFT key for a service
func (psr *PersistentServiceRegistry) getServiceKey(name, namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return fmt.Sprintf("service:%s.%s", name, namespace)
}
