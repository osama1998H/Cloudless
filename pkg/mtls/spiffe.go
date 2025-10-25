package mtls

import (
	"context"
	"crypto/x509"
	"fmt"
	"net/url"
	"strings"

	"go.uber.org/zap"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

const (
	// TrustDomain is the SPIFFE trust domain for Cloudless
	TrustDomain = "cloudless"

	// URIScheme is the URI scheme for SPIFFE identities
	URIScheme = "spiffe"
)

// SPIFFEIdentity represents a SPIFFE identity
type SPIFFEIdentity struct {
	TrustDomain string
	Type        IdentityType
	ID          string
	URI         *url.URL
}

// IdentityType represents the type of SPIFFE identity
type IdentityType string

const (
	NodeIdentity      IdentityType = "node"
	WorkloadIdentity  IdentityType = "workload"
	ServiceIdentity   IdentityType = "service"
	ControllerIdentity IdentityType = "controller"
)

// NewNodeIdentity creates a new node SPIFFE identity
func NewNodeIdentity(nodeID string) *SPIFFEIdentity {
	uri, _ := url.Parse(fmt.Sprintf("%s://%s/%s/%s", URIScheme, TrustDomain, NodeIdentity, nodeID))
	return &SPIFFEIdentity{
		TrustDomain: TrustDomain,
		Type:        NodeIdentity,
		ID:          nodeID,
		URI:         uri,
	}
}

// NewWorkloadIdentity creates a new workload SPIFFE identity
func NewWorkloadIdentity(workloadID string) *SPIFFEIdentity {
	uri, _ := url.Parse(fmt.Sprintf("%s://%s/%s/%s", URIScheme, TrustDomain, WorkloadIdentity, workloadID))
	return &SPIFFEIdentity{
		TrustDomain: TrustDomain,
		Type:        WorkloadIdentity,
		ID:          workloadID,
		URI:         uri,
	}
}

// NewServiceIdentity creates a new service SPIFFE identity
func NewServiceIdentity(serviceID string) *SPIFFEIdentity {
	uri, _ := url.Parse(fmt.Sprintf("%s://%s/%s/%s", URIScheme, TrustDomain, ServiceIdentity, serviceID))
	return &SPIFFEIdentity{
		TrustDomain: TrustDomain,
		Type:        ServiceIdentity,
		ID:          serviceID,
		URI:         uri,
	}
}

// NewControllerIdentity creates a new controller SPIFFE identity
func NewControllerIdentity(controllerID string) *SPIFFEIdentity {
	uri, _ := url.Parse(fmt.Sprintf("%s://%s/%s/%s", URIScheme, TrustDomain, ControllerIdentity, controllerID))
	return &SPIFFEIdentity{
		TrustDomain: TrustDomain,
		Type:        ControllerIdentity,
		ID:          controllerID,
		URI:         uri,
	}
}

// ParseSPIFFEID parses a SPIFFE ID from a URI string
func ParseSPIFFEID(uriStr string) (*SPIFFEIdentity, error) {
	uri, err := url.Parse(uriStr)
	if err != nil {
		return nil, fmt.Errorf("failed to parse URI: %w", err)
	}

	if uri.Scheme != URIScheme {
		return nil, fmt.Errorf("invalid SPIFFE URI scheme: %s", uri.Scheme)
	}

	if uri.Host != TrustDomain {
		return nil, fmt.Errorf("invalid trust domain: %s", uri.Host)
	}

	// Parse path: /type/id
	parts := strings.Split(strings.TrimPrefix(uri.Path, "/"), "/")
	if len(parts) != 2 {
		return nil, fmt.Errorf("invalid SPIFFE ID path: %s", uri.Path)
	}

	identityType := IdentityType(parts[0])
	id := parts[1]

	// Validate identity type
	switch identityType {
	case NodeIdentity, WorkloadIdentity, ServiceIdentity, ControllerIdentity:
		// Valid
	default:
		return nil, fmt.Errorf("unknown identity type: %s", identityType)
	}

	return &SPIFFEIdentity{
		TrustDomain: uri.Host,
		Type:        identityType,
		ID:          id,
		URI:         uri,
	}, nil
}

// ExtractSPIFFEIDFromCert extracts a SPIFFE ID from a certificate
func ExtractSPIFFEIDFromCert(cert *x509.Certificate) (*SPIFFEIdentity, error) {
	if cert == nil {
		return nil, fmt.Errorf("certificate is nil")
	}

	// Look for SPIFFE ID in URIs
	for _, uri := range cert.URIs {
		if uri.Scheme == URIScheme {
			return ParseSPIFFEID(uri.String())
		}
	}

	return nil, fmt.Errorf("no SPIFFE ID found in certificate")
}

// String returns the string representation of the SPIFFE identity
func (id *SPIFFEIdentity) String() string {
	if id.URI != nil {
		return id.URI.String()
	}
	return fmt.Sprintf("%s://%s/%s/%s", URIScheme, id.TrustDomain, id.Type, id.ID)
}

// Validate validates the SPIFFE identity
func (id *SPIFFEIdentity) Validate() error {
	if id.TrustDomain == "" {
		return fmt.Errorf("trust domain is required")
	}
	if id.Type == "" {
		return fmt.Errorf("identity type is required")
	}
	if id.ID == "" {
		return fmt.Errorf("identity ID is required")
	}
	if id.URI == nil {
		return fmt.Errorf("URI is required")
	}
	return nil
}

// SPIFFEAuthInfo provides SPIFFE authentication information
type SPIFFEAuthInfo struct {
	Identity *SPIFFEIdentity
	Cert     *x509.Certificate
}

// AuthType returns the authentication type
func (s SPIFFEAuthInfo) AuthType() string {
	return "spiffe"
}

// ExtractSPIFFEIDFromContext extracts SPIFFE ID from gRPC context
func ExtractSPIFFEIDFromContext(ctx context.Context) (*SPIFFEIdentity, error) {
	p, ok := peer.FromContext(ctx)
	if !ok {
		return nil, fmt.Errorf("no peer info in context")
	}

	tlsInfo, ok := p.AuthInfo.(credentials.TLSInfo)
	if !ok {
		return nil, fmt.Errorf("no TLS info in peer")
	}

	if len(tlsInfo.State.PeerCertificates) == 0 {
		return nil, fmt.Errorf("no peer certificates")
	}

	return ExtractSPIFFEIDFromCert(tlsInfo.State.PeerCertificates[0])
}

// SPIFFEVerifier verifies SPIFFE identities
type SPIFFEVerifier struct {
	logger          *zap.Logger
	allowedTypes    []IdentityType
	allowedIDs      map[string]bool
	trustDomain     string
}

// NewSPIFFEVerifier creates a new SPIFFE verifier
func NewSPIFFEVerifier(logger *zap.Logger, allowedTypes []IdentityType, allowedIDs []string) *SPIFFEVerifier {
	idMap := make(map[string]bool)
	for _, id := range allowedIDs {
		idMap[id] = true
	}

	return &SPIFFEVerifier{
		logger:       logger,
		allowedTypes: allowedTypes,
		allowedIDs:   idMap,
		trustDomain:  TrustDomain,
	}
}

// VerifyIdentity verifies a SPIFFE identity
func (v *SPIFFEVerifier) VerifyIdentity(id *SPIFFEIdentity) error {
	if id == nil {
		return fmt.Errorf("identity is nil")
	}

	// Validate identity
	if err := id.Validate(); err != nil {
		return fmt.Errorf("invalid identity: %w", err)
	}

	// Check trust domain
	if id.TrustDomain != v.trustDomain {
		return fmt.Errorf("untrusted domain: %s", id.TrustDomain)
	}

	// Check identity type
	typeAllowed := false
	for _, allowedType := range v.allowedTypes {
		if id.Type == allowedType {
			typeAllowed = true
			break
		}
	}
	if !typeAllowed {
		return fmt.Errorf("identity type not allowed: %s", id.Type)
	}

	// Check specific IDs if configured
	if len(v.allowedIDs) > 0 {
		if !v.allowedIDs[id.ID] {
			return fmt.Errorf("identity ID not allowed: %s", id.ID)
		}
	}

	v.logger.Debug("SPIFFE identity verified",
		zap.String("identity", id.String()),
		zap.String("type", string(id.Type)),
		zap.String("id", id.ID),
	)

	return nil
}

// VerifyCertificate verifies a certificate contains a valid SPIFFE identity
func (v *SPIFFEVerifier) VerifyCertificate(cert *x509.Certificate) (*SPIFFEIdentity, error) {
	if cert == nil {
		return nil, fmt.Errorf("certificate is nil")
	}

	// Extract SPIFFE ID
	id, err := ExtractSPIFFEIDFromCert(cert)
	if err != nil {
		return nil, fmt.Errorf("failed to extract SPIFFE ID: %w", err)
	}

	// Verify identity
	if err := v.VerifyIdentity(id); err != nil {
		return nil, fmt.Errorf("identity verification failed: %w", err)
	}

	return id, nil
}

// VerifyPeer verifies a peer's SPIFFE identity from gRPC context
func (v *SPIFFEVerifier) VerifyPeer(ctx context.Context) (*SPIFFEIdentity, error) {
	id, err := ExtractSPIFFEIDFromContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to extract SPIFFE ID from context: %w", err)
	}

	if err := v.VerifyIdentity(id); err != nil {
		return nil, fmt.Errorf("peer verification failed: %w", err)
	}

	return id, nil
}

// SPIFFEAuthorizer provides authorization based on SPIFFE identities
type SPIFFEAuthorizer struct {
	policies map[string]*AccessPolicy
	logger   *zap.Logger
}

// AccessPolicy defines access control for SPIFFE identities
type AccessPolicy struct {
	AllowedTypes []IdentityType
	AllowedIDs   []string
	Resources    []string
	Actions      []string
}

// NewSPIFFEAuthorizer creates a new SPIFFE authorizer
func NewSPIFFEAuthorizer(logger *zap.Logger) *SPIFFEAuthorizer {
	return &SPIFFEAuthorizer{
		policies: make(map[string]*AccessPolicy),
		logger:   logger,
	}
}

// AddPolicy adds an access policy
func (a *SPIFFEAuthorizer) AddPolicy(name string, policy *AccessPolicy) {
	a.policies[name] = policy
}

// Authorize checks if an identity is authorized for a resource and action
func (a *SPIFFEAuthorizer) Authorize(id *SPIFFEIdentity, resource, action string) error {
	if id == nil {
		return fmt.Errorf("identity is nil")
	}

	// Check each policy
	for name, policy := range a.policies {
		// Check if identity type is allowed
		typeAllowed := false
		for _, allowedType := range policy.AllowedTypes {
			if id.Type == allowedType {
				typeAllowed = true
				break
			}
		}
		if !typeAllowed {
			continue
		}

		// Check if specific ID is allowed (if specified)
		if len(policy.AllowedIDs) > 0 {
			idAllowed := false
			for _, allowedID := range policy.AllowedIDs {
				if id.ID == allowedID || allowedID == "*" {
					idAllowed = true
					break
				}
			}
			if !idAllowed {
				continue
			}
		}

		// Check resource
		resourceAllowed := false
		for _, allowedResource := range policy.Resources {
			if matchPattern(resource, allowedResource) {
				resourceAllowed = true
				break
			}
		}
		if !resourceAllowed {
			continue
		}

		// Check action
		actionAllowed := false
		for _, allowedAction := range policy.Actions {
			if action == allowedAction || allowedAction == "*" {
				actionAllowed = true
				break
			}
		}
		if actionAllowed {
			a.logger.Debug("Authorization granted",
				zap.String("identity", id.String()),
				zap.String("resource", resource),
				zap.String("action", action),
				zap.String("policy", name),
			)
			return nil
		}
	}

	return fmt.Errorf("authorization denied for %s to %s on %s", id.String(), action, resource)
}

// matchPattern checks if a string matches a pattern (supports * wildcard)
func matchPattern(s, pattern string) bool {
	if pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(s, prefix)
	}
	return s == pattern
}