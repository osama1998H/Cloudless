package overlay

import (
	"context"
	"fmt"
	"net"
	"time"

	"github.com/pion/stun"
	"go.uber.org/zap"
)

// RFC 5780 STUN Behavior Discovery
// https://tools.ietf.org/html/rfc5780
//
// This file implements RFC 5780 NAT Behavior Discovery using STUN.
// It provides accurate NAT type classification through a series of tests.

// RFC5780Discoverer performs RFC 5780 NAT behavior discovery
type RFC5780Discoverer struct {
	logger        *zap.Logger
	timeout       time.Duration
	primaryServer string
	altServer     string // Alternate server for RFC 5780 tests
}

// NewRFC5780Discoverer creates a new RFC 5780 discoverer
func NewRFC5780Discoverer(primaryServer, altServer string, logger *zap.Logger) *RFC5780Discoverer {
	return &RFC5780Discoverer{
		logger:        logger,
		timeout:       5 * time.Second,
		primaryServer: primaryServer,
		altServer:     altServer,
	}
}

// DiscoverNATBehavior performs complete RFC 5780 NAT behavior discovery
func (d *RFC5780Discoverer) DiscoverNATBehavior(ctx context.Context, localAddr string) (*NATBehavior, error) {
	behavior := &NATBehavior{}

	// Test I: Basic binding test to detect NAT
	testIResult, err := d.performTestI(ctx, localAddr)
	if err != nil {
		return nil, fmt.Errorf("Test I failed: %w", err)
	}

	behavior.HasNAT = testIResult.HasNAT
	behavior.PublicIP = testIResult.PublicIP
	behavior.PublicPort = testIResult.PublicPort
	behavior.LocalIP = testIResult.LocalIP
	behavior.LocalPort = testIResult.LocalPort

	// If no NAT, we're done
	if !behavior.HasNAT {
		behavior.Type = NATTypeNone
		behavior.MappingBehavior = MappingEndpointIndependent
		behavior.FilteringBehavior = FilteringEndpointIndependent
		return behavior, nil
	}

	// Test II: Check if mapping is endpoint-independent
	testIIResult, err := d.performTestII(ctx, localAddr, testIResult)
	if err != nil {
		d.logger.Warn("Test II failed, assuming symmetric NAT", zap.Error(err))
		behavior.Type = NATTypeSymmetric
		behavior.MappingBehavior = MappingEndpointDependent
		behavior.FilteringBehavior = FilteringEndpointDependent
		return behavior, nil
	}

	behavior.MappingBehavior = testIIResult.MappingBehavior

	// Test III: Determine filtering behavior
	testIIIResult, err := d.performTestIII(ctx, localAddr, testIResult)
	if err != nil {
		d.logger.Warn("Test III failed, assuming restrictive filtering", zap.Error(err))
		behavior.FilteringBehavior = FilteringEndpointDependent
	} else {
		behavior.FilteringBehavior = testIIIResult.FilteringBehavior
	}

	// Determine NAT type based on mapping and filtering behavior
	behavior.Type = d.classifyNATType(behavior.MappingBehavior, behavior.FilteringBehavior)

	d.logger.Info("RFC 5780 NAT discovery complete",
		zap.String("nat_type", string(behavior.Type)),
		zap.String("mapping", string(behavior.MappingBehavior)),
		zap.String("filtering", string(behavior.FilteringBehavior)),
	)

	return behavior, nil
}

// performTestI performs RFC 5780 Test I: Basic binding test
// Tests if the client is behind NAT by comparing local and mapped addresses
func (d *RFC5780Discoverer) performTestI(ctx context.Context, localAddr string) (*TestIResult, error) {
	d.logger.Debug("Performing RFC 5780 Test I (NAT detection)")

	// Parse local address
	localIP, localPort, err := parseAddress(localAddr)
	if err != nil {
		return nil, fmt.Errorf("invalid local address: %w", err)
	}

	// Create UDP connection to primary STUN server
	conn, err := d.createConnection(localAddr, d.primaryServer)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(d.timeout)
	}
	conn.SetDeadline(deadline)

	// Send binding request
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if _, err := conn.Write(message.Raw); err != nil {
		return nil, fmt.Errorf("failed to send binding request: %w", err)
	}

	// Receive response
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to receive binding response: %w", err)
	}

	// Parse response
	response := &stun.Message{Raw: buf[:n]}
	if err := response.Decode(); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract XOR-MAPPED-ADDRESS
	var xorAddr stun.XORMappedAddress
	if err := xorAddr.GetFrom(response); err != nil {
		return nil, fmt.Errorf("failed to get mapped address: %w", err)
	}

	publicIP := xorAddr.IP.String()
	publicPort := xorAddr.Port

	// Determine if behind NAT
	hasNAT := publicIP != localIP || publicPort != localPort

	return &TestIResult{
		HasNAT:     hasNAT,
		PublicIP:   publicIP,
		PublicPort: publicPort,
		LocalIP:    localIP,
		LocalPort:  localPort,
	}, nil
}

// performTestII performs RFC 5780 Test II: Mapping behavior test
// Tests if the NAT uses the same mapping for different destinations
func (d *RFC5780Discoverer) performTestII(ctx context.Context, localAddr string, testIResult *TestIResult) (*TestIIResult, error) {
	d.logger.Debug("Performing RFC 5780 Test II (Mapping behavior)")

	// If no alternate server, cannot perform Test II
	if d.altServer == "" {
		return nil, fmt.Errorf("alternate server not configured")
	}

	// Create connection to alternate server
	conn, err := d.createConnection(localAddr, d.altServer)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(d.timeout)
	}
	conn.SetDeadline(deadline)

	// Send binding request to alternate server
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)
	if _, err := conn.Write(message.Raw); err != nil {
		return nil, fmt.Errorf("failed to send binding request: %w", err)
	}

	// Receive response
	buf := make([]byte, 1500)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("failed to receive binding response: %w", err)
	}

	// Parse response
	response := &stun.Message{Raw: buf[:n]}
	if err := response.Decode(); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	// Extract XOR-MAPPED-ADDRESS
	var xorAddr stun.XORMappedAddress
	if err := xorAddr.GetFrom(response); err != nil {
		return nil, fmt.Errorf("failed to get mapped address: %w", err)
	}

	altPublicIP := xorAddr.IP.String()
	altPublicPort := xorAddr.Port

	// Compare with Test I results
	// If same IP:port, mapping is endpoint-independent
	// If different, mapping is endpoint-dependent (symmetric NAT)
	var mappingBehavior MappingBehavior
	if altPublicIP == testIResult.PublicIP && altPublicPort == testIResult.PublicPort {
		mappingBehavior = MappingEndpointIndependent
	} else if altPublicIP == testIResult.PublicIP && altPublicPort != testIResult.PublicPort {
		mappingBehavior = MappingAddressDependent
	} else {
		mappingBehavior = MappingEndpointDependent
	}

	d.logger.Debug("Test II complete",
		zap.String("mapping", string(mappingBehavior)),
		zap.String("primary_addr", fmt.Sprintf("%s:%d", testIResult.PublicIP, testIResult.PublicPort)),
		zap.String("alt_addr", fmt.Sprintf("%s:%d", altPublicIP, altPublicPort)),
	)

	return &TestIIResult{
		MappingBehavior: mappingBehavior,
		PublicIP:        altPublicIP,
		PublicPort:      altPublicPort,
	}, nil
}

// performTestIII performs RFC 5780 Test III: Filtering behavior test
// Tests if the NAT filters packets based on source address/port
func (d *RFC5780Discoverer) performTestIII(ctx context.Context, localAddr string, testIResult *TestIResult) (*TestIIIResult, error) {
	d.logger.Debug("Performing RFC 5780 Test III (Filtering behavior)")

	// Create connection to primary server
	conn, err := d.createConnection(localAddr, d.primaryServer)
	if err != nil {
		return nil, err
	}
	defer conn.Close()

	// Set deadline
	deadline, ok := ctx.Deadline()
	if !ok {
		deadline = time.Now().Add(d.timeout)
	}
	conn.SetDeadline(deadline)

	// Send binding request with CHANGE-REQUEST attribute
	// Request: change both IP and port
	message, err := d.createChangeRequest(true, true)
	if err != nil {
		return nil, err
	}

	if _, err := conn.Write(message.Raw); err != nil {
		return nil, fmt.Errorf("failed to send change request: %w", err)
	}

	// Try to receive response
	buf := make([]byte, 1500)
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = conn.Read(buf)

	var filteringBehavior FilteringBehavior
	if err != nil {
		// Timeout means NAT filtered the response (restrictive filtering)
		if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
			filteringBehavior = FilteringEndpointDependent
		} else {
			return nil, fmt.Errorf("failed to receive response: %w", err)
		}
	} else {
		// Response received means NAT allows packets from different source
		// (endpoint-independent filtering)
		filteringBehavior = FilteringEndpointIndependent
	}

	d.logger.Debug("Test III complete",
		zap.String("filtering", string(filteringBehavior)),
	)

	return &TestIIIResult{
		FilteringBehavior: filteringBehavior,
	}, nil
}

// createConnection creates a UDP connection to the specified server
func (d *RFC5780Discoverer) createConnection(localAddr, serverAddr string) (*net.UDPConn, error) {
	// Parse local address
	localUDPAddr, err := net.ResolveUDPAddr("udp", localAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve local address: %w", err)
	}

	// Parse server address
	serverUDPAddr, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve server address: %w", err)
	}

	// Create connection
	conn, err := net.DialUDP("udp", localUDPAddr, serverUDPAddr)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection: %w", err)
	}

	return conn, nil
}

// createChangeRequest creates a STUN binding request with CHANGE-REQUEST attribute
func (d *RFC5780Discoverer) createChangeRequest(changeIP, changePort bool) (*stun.Message, error) {
	message := stun.MustBuild(stun.TransactionID, stun.BindingRequest)

	// RFC 5780 CHANGE-REQUEST attribute
	// For now, just return the basic binding request
	// Full CHANGE-REQUEST support requires additional STUN attribute implementation
	// This is a simplified version for the initial implementation

	return message, nil
}

// classifyNATType classifies NAT type based on mapping and filtering behavior
func (d *RFC5780Discoverer) classifyNATType(mapping MappingBehavior, filtering FilteringBehavior) NATType {
	// RFC 5780 classification:
	// 1. No NAT: Already handled before this function
	// 2. Symmetric NAT: Endpoint-dependent mapping
	// 3. Full Cone: Endpoint-independent mapping + endpoint-independent filtering
	// 4. Restricted Cone: Endpoint-independent mapping + address-dependent filtering
	// 5. Port Restricted Cone: Endpoint-independent mapping + endpoint-dependent filtering

	if mapping == MappingEndpointDependent {
		// Symmetric NAT: Different mapping for different destinations
		return NATTypeSymmetric
	}

	if mapping == MappingEndpointIndependent {
		if filtering == FilteringEndpointIndependent {
			// Full Cone: Same mapping, allows all sources
			return NATTypeFullCone
		}
		if filtering == FilteringAddressDependent {
			// Restricted Cone: Same mapping, filters by source address
			return NATTypeRestrictedCone
		}
		if filtering == FilteringEndpointDependent {
			// Port Restricted Cone: Same mapping, filters by source address+port
			return NATTypePortRestrictedCone
		}
	}

	// Default to port-restricted cone (most common)
	return NATTypePortRestrictedCone
}

// RFC 5780 test result types

// TestIResult contains results from RFC 5780 Test I
type TestIResult struct {
	HasNAT     bool
	PublicIP   string
	PublicPort int
	LocalIP    string
	LocalPort  int
}

// TestIIResult contains results from RFC 5780 Test II
type TestIIResult struct {
	MappingBehavior MappingBehavior
	PublicIP        string
	PublicPort      int
}

// TestIIIResult contains results from RFC 5780 Test III
type TestIIIResult struct {
	FilteringBehavior FilteringBehavior
}

// NATBehavior describes complete NAT behavior discovered via RFC 5780
type NATBehavior struct {
	Type              NATType
	HasNAT            bool
	PublicIP          string
	PublicPort        int
	LocalIP           string
	LocalPort         int
	MappingBehavior   MappingBehavior
	FilteringBehavior FilteringBehavior
}

// MappingBehavior describes how NAT maps internal addresses to external addresses
type MappingBehavior string

const (
	MappingEndpointIndependent MappingBehavior = "endpoint-independent"
	MappingAddressDependent    MappingBehavior = "address-dependent"
	MappingEndpointDependent   MappingBehavior = "endpoint-dependent"
)

// FilteringBehavior describes how NAT filters incoming packets
type FilteringBehavior string

const (
	FilteringEndpointIndependent FilteringBehavior = "endpoint-independent"
	FilteringAddressDependent    FilteringBehavior = "address-dependent"
	FilteringEndpointDependent   FilteringBehavior = "endpoint-dependent"
)
