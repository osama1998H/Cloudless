package overlay

import (
	"fmt"
	"net"
	"sync"
	"time"

	"go.uber.org/zap"
)

// ServiceRegistry manages service registration and discovery
type ServiceRegistry struct {
	config RegistryConfig
	logger *zap.Logger

	services  sync.Map // serviceName -> *Service
	endpoints sync.Map // serviceName -> []Endpoint
	virtualIPs sync.Map // serviceName -> virtualIP
	ipPool    *IPPool

	mu sync.RWMutex
}

// NewServiceRegistry creates a new service registry
func NewServiceRegistry(config RegistryConfig, logger *zap.Logger) (*ServiceRegistry, error) {
	ipPool, err := NewIPPool(config.VirtualIPCIDR)
	if err != nil {
		return nil, fmt.Errorf("failed to create IP pool: %w", err)
	}

	return &ServiceRegistry{
		config: config,
		logger: logger,
		ipPool: ipPool,
	}, nil
}

// RegisterService registers a new service
func (sr *ServiceRegistry) RegisterService(service *Service) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	sr.logger.Info("Registering service",
		zap.String("name", service.Name),
		zap.String("namespace", service.Namespace),
	)

	// Allocate virtual IP if not provided
	if service.VirtualIP == "" {
		ip, err := sr.ipPool.Allocate()
		if err != nil {
			return fmt.Errorf("failed to allocate virtual IP: %w", err)
		}
		service.VirtualIP = ip
	}

	service.CreatedAt = time.Now()
	service.UpdatedAt = time.Now()

	fullName := sr.getFullServiceName(service.Name, service.Namespace)
	sr.services.Store(fullName, service)
	sr.virtualIPs.Store(fullName, service.VirtualIP)

	return nil
}

// DeregisterService removes a service
func (sr *ServiceRegistry) DeregisterService(name, namespace string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	fullName := sr.getFullServiceName(name, namespace)

	sr.logger.Info("Deregistering service",
		zap.String("name", name),
		zap.String("namespace", namespace),
	)

	// Release virtual IP
	if vip, ok := sr.virtualIPs.Load(fullName); ok {
		sr.ipPool.Release(vip.(string))
		sr.virtualIPs.Delete(fullName)
	}

	sr.services.Delete(fullName)
	sr.endpoints.Delete(fullName)

	return nil
}

// GetService retrieves a service by name
func (sr *ServiceRegistry) GetService(name, namespace string) (*Service, error) {
	fullName := sr.getFullServiceName(name, namespace)

	if s, ok := sr.services.Load(fullName); ok {
		service := s.(*Service)
		return service, nil
	}

	return nil, fmt.Errorf("service not found: %s/%s", namespace, name)
}

// ListServices returns all services
func (sr *ServiceRegistry) ListServices() []*Service {
	var services []*Service

	sr.services.Range(func(key, value interface{}) bool {
		service := value.(*Service)
		services = append(services, service)
		return true
	})

	return services
}

// RegisterEndpoint registers an endpoint for a service
func (sr *ServiceRegistry) RegisterEndpoint(serviceName, namespace string, endpoint Endpoint) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	fullName := sr.getFullServiceName(serviceName, namespace)

	sr.logger.Debug("Registering endpoint",
		zap.String("service", serviceName),
		zap.String("namespace", namespace),
		zap.String("endpoint_id", endpoint.ID),
	)

	// Get existing endpoints
	var endpoints []Endpoint
	if e, ok := sr.endpoints.Load(fullName); ok {
		endpoints = e.([]Endpoint)
	}

	// Check if endpoint already exists
	for i, ep := range endpoints {
		if ep.ID == endpoint.ID {
			// Update existing endpoint
			endpoints[i] = endpoint
			sr.endpoints.Store(fullName, endpoints)
			return nil
		}
	}

	// Add new endpoint
	endpoints = append(endpoints, endpoint)
	sr.endpoints.Store(fullName, endpoints)

	// Update service timestamp
	if s, ok := sr.services.Load(fullName); ok {
		service := s.(*Service)
		service.UpdatedAt = time.Now()
		sr.services.Store(fullName, service)
	}

	return nil
}

// DeregisterEndpoint removes an endpoint from a service
func (sr *ServiceRegistry) DeregisterEndpoint(serviceName, namespace, endpointID string) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	fullName := sr.getFullServiceName(serviceName, namespace)

	sr.logger.Debug("Deregistering endpoint",
		zap.String("service", serviceName),
		zap.String("namespace", namespace),
		zap.String("endpoint_id", endpointID),
	)

	if e, ok := sr.endpoints.Load(fullName); ok {
		endpoints := e.([]Endpoint)
		newEndpoints := make([]Endpoint, 0, len(endpoints))

		for _, ep := range endpoints {
			if ep.ID != endpointID {
				newEndpoints = append(newEndpoints, ep)
			}
		}

		if len(newEndpoints) > 0 {
			sr.endpoints.Store(fullName, newEndpoints)
		} else {
			sr.endpoints.Delete(fullName)
		}
	}

	return nil
}

// GetEndpoints retrieves all endpoints for a service
func (sr *ServiceRegistry) GetEndpoints(serviceName, namespace string) ([]Endpoint, error) {
	fullName := sr.getFullServiceName(serviceName, namespace)

	if e, ok := sr.endpoints.Load(fullName); ok {
		endpoints := e.([]Endpoint)
		return endpoints, nil
	}

	return []Endpoint{}, nil
}

// GetHealthyEndpoints retrieves all healthy endpoints for a service
func (sr *ServiceRegistry) GetHealthyEndpoints(serviceName, namespace string) ([]Endpoint, error) {
	endpoints, err := sr.GetEndpoints(serviceName, namespace)
	if err != nil {
		return nil, err
	}

	healthy := make([]Endpoint, 0, len(endpoints))
	for _, ep := range endpoints {
		if ep.Health == HealthStatusHealthy {
			healthy = append(healthy, ep)
		}
	}

	return healthy, nil
}

// UpdateEndpointHealth updates the health status of an endpoint
func (sr *ServiceRegistry) UpdateEndpointHealth(serviceName, namespace, endpointID string, health HealthStatus) error {
	sr.mu.Lock()
	defer sr.mu.Unlock()

	fullName := sr.getFullServiceName(serviceName, namespace)

	if e, ok := sr.endpoints.Load(fullName); ok {
		endpoints := e.([]Endpoint)

		for i, ep := range endpoints {
			if ep.ID == endpointID {
				endpoints[i].Health = health
				sr.endpoints.Store(fullName, endpoints)
				return nil
			}
		}
	}

	return fmt.Errorf("endpoint not found: %s", endpointID)
}

// ResolveService resolves a service name to a virtual IP
func (sr *ServiceRegistry) ResolveService(serviceName, namespace string) (string, error) {
	fullName := sr.getFullServiceName(serviceName, namespace)

	if vip, ok := sr.virtualIPs.Load(fullName); ok {
		return vip.(string), nil
	}

	return "", fmt.Errorf("service not found: %s/%s", namespace, serviceName)
}

// ReverseLookup performs a reverse lookup from virtual IP to service name
func (sr *ServiceRegistry) ReverseLookup(virtualIP string) (*Service, error) {
	var foundService *Service

	sr.virtualIPs.Range(func(key, value interface{}) bool {
		vip := value.(string)
		if vip == virtualIP {
			fullName := key.(string)
			if s, ok := sr.services.Load(fullName); ok {
				foundService = s.(*Service)
				return false // Stop iteration
			}
		}
		return true
	})

	if foundService != nil {
		return foundService, nil
	}

	return nil, fmt.Errorf("service not found for IP: %s", virtualIP)
}

// getFullServiceName returns the full service name including namespace
func (sr *ServiceRegistry) getFullServiceName(name, namespace string) string {
	if namespace == "" {
		namespace = "default"
	}
	return fmt.Sprintf("%s.%s", name, namespace)
}

// IPPool manages a pool of virtual IPs
type IPPool struct {
	cidr       string
	network    *net.IPNet
	allocated  map[string]bool
	nextOffset uint32
	mu         sync.Mutex
}

// NewIPPool creates a new IP pool
func NewIPPool(cidr string) (*IPPool, error) {
	if cidr == "" {
		cidr = "10.96.0.0/12" // Default Kubernetes service CIDR
	}

	_, network, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, fmt.Errorf("invalid CIDR: %w", err)
	}

	return &IPPool{
		cidr:       cidr,
		network:    network,
		allocated:  make(map[string]bool),
		nextOffset: 1, // Skip network address
	}, nil
}

// Allocate allocates a new IP address
func (pool *IPPool) Allocate() (string, error) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	// Convert network to uint32
	networkIP := pool.network.IP.To4()
	if networkIP == nil {
		return "", fmt.Errorf("only IPv4 is supported")
	}

	networkUint := ipToUint32(networkIP)
	ones, bits := pool.network.Mask.Size()
	maxHosts := uint32(1<<uint(bits-ones)) - 2 // Subtract network and broadcast

	// Find next available IP
	for i := uint32(0); i < maxHosts; i++ {
		offset := (pool.nextOffset + i) % maxHosts
		if offset == 0 {
			offset = 1 // Skip network address
		}

		ip := uint32ToIP(networkUint + offset)
		ipStr := ip.String()

		if !pool.allocated[ipStr] {
			pool.allocated[ipStr] = true
			pool.nextOffset = offset + 1
			return ipStr, nil
		}
	}

	return "", fmt.Errorf("IP pool exhausted")
}

// Release releases an IP address back to the pool
func (pool *IPPool) Release(ip string) {
	pool.mu.Lock()
	defer pool.mu.Unlock()

	delete(pool.allocated, ip)
}

// ipToUint32 converts an IP address to uint32
func ipToUint32(ip net.IP) uint32 {
	ip = ip.To4()
	return uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
}

// uint32ToIP converts a uint32 to an IP address
func uint32ToIP(n uint32) net.IP {
	return net.IPv4(byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
}
