package mtls

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net/url"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/peer"
)

// TestNewNodeIdentity tests node identity creation
func TestNewNodeIdentity(t *testing.T) {
	tests := []struct {
		name           string
		nodeID         string
		expectedURI    string
		expectedType   IdentityType
		expectedDomain string
	}{
		{
			name:           "Valid node ID",
			nodeID:         "node-123",
			expectedURI:    "spiffe://cloudless/node/node-123",
			expectedType:   NodeIdentity,
			expectedDomain: TrustDomain,
		},
		{
			name:           "Node with UUID",
			nodeID:         "550e8400-e29b-41d4-a716-446655440000",
			expectedURI:    "spiffe://cloudless/node/550e8400-e29b-41d4-a716-446655440000",
			expectedType:   NodeIdentity,
			expectedDomain: TrustDomain,
		},
		{
			name:           "Node with special characters",
			nodeID:         "node-test-01",
			expectedURI:    "spiffe://cloudless/node/node-test-01",
			expectedType:   NodeIdentity,
			expectedDomain: TrustDomain,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := NewNodeIdentity(tt.nodeID)

			if identity == nil {
				t.Fatal("Expected non-nil identity")
			}

			if identity.ID != tt.nodeID {
				t.Errorf("Expected ID %s, got %s", tt.nodeID, identity.ID)
			}

			if identity.Type != tt.expectedType {
				t.Errorf("Expected type %s, got %s", tt.expectedType, identity.Type)
			}

			if identity.TrustDomain != tt.expectedDomain {
				t.Errorf("Expected trust domain %s, got %s", tt.expectedDomain, identity.TrustDomain)
			}

			if identity.URI.String() != tt.expectedURI {
				t.Errorf("Expected URI %s, got %s", tt.expectedURI, identity.URI.String())
			}

			// Verify identity is valid
			if err := identity.Validate(); err != nil {
				t.Errorf("Identity should be valid, got error: %v", err)
			}
		})
	}
}

// TestNewWorkloadIdentity tests workload identity creation
func TestNewWorkloadIdentity(t *testing.T) {
	tests := []struct {
		name         string
		workloadID   string
		expectedURI  string
		expectedType IdentityType
	}{
		{
			name:         "Valid workload ID",
			workloadID:   "workload-abc",
			expectedURI:  "spiffe://cloudless/workload/workload-abc",
			expectedType: WorkloadIdentity,
		},
		{
			name:         "Workload with namespace",
			workloadID:   "default.app-server",
			expectedURI:  "spiffe://cloudless/workload/default.app-server",
			expectedType: WorkloadIdentity,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity := NewWorkloadIdentity(tt.workloadID)

			if identity == nil {
				t.Fatal("Expected non-nil identity")
			}

			if identity.ID != tt.workloadID {
				t.Errorf("Expected ID %s, got %s", tt.workloadID, identity.ID)
			}

			if identity.Type != tt.expectedType {
				t.Errorf("Expected type %s, got %s", tt.expectedType, identity.Type)
			}

			if identity.URI.String() != tt.expectedURI {
				t.Errorf("Expected URI %s, got %s", tt.expectedURI, identity.URI.String())
			}
		})
	}
}

// TestNewServiceIdentity tests service identity creation
func TestNewServiceIdentity(t *testing.T) {
	serviceID := "coordinator"
	identity := NewServiceIdentity(serviceID)

	if identity == nil {
		t.Fatal("Expected non-nil identity")
	}

	expectedURI := "spiffe://cloudless/service/coordinator"
	if identity.URI.String() != expectedURI {
		t.Errorf("Expected URI %s, got %s", expectedURI, identity.URI.String())
	}

	if identity.Type != ServiceIdentity {
		t.Errorf("Expected type %s, got %s", ServiceIdentity, identity.Type)
	}
}

// TestNewControllerIdentity tests controller identity creation
func TestNewControllerIdentity(t *testing.T) {
	controllerID := "controller-01"
	identity := NewControllerIdentity(controllerID)

	if identity == nil {
		t.Fatal("Expected non-nil identity")
	}

	expectedURI := "spiffe://cloudless/controller/controller-01"
	if identity.URI.String() != expectedURI {
		t.Errorf("Expected URI %s, got %s", expectedURI, identity.URI.String())
	}

	if identity.Type != ControllerIdentity {
		t.Errorf("Expected type %s, got %s", ControllerIdentity, identity.Type)
	}
}

// TestParseSPIFFEID_ValidCases tests parsing valid SPIFFE IDs
func TestParseSPIFFEID_ValidCases(t *testing.T) {
	tests := []struct {
		name           string
		uri            string
		expectedType   IdentityType
		expectedID     string
		expectedDomain string
	}{
		{
			name:           "Node identity",
			uri:            "spiffe://cloudless/node/node-123",
			expectedType:   NodeIdentity,
			expectedID:     "node-123",
			expectedDomain: TrustDomain,
		},
		{
			name:           "Workload identity",
			uri:            "spiffe://cloudless/workload/app-server",
			expectedType:   WorkloadIdentity,
			expectedID:     "app-server",
			expectedDomain: TrustDomain,
		},
		{
			name:           "Service identity",
			uri:            "spiffe://cloudless/service/api-gateway",
			expectedType:   ServiceIdentity,
			expectedID:     "api-gateway",
			expectedDomain: TrustDomain,
		},
		{
			name:           "Controller identity",
			uri:            "spiffe://cloudless/controller/ctrl-001",
			expectedType:   ControllerIdentity,
			expectedID:     "ctrl-001",
			expectedDomain: TrustDomain,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity, err := ParseSPIFFEID(tt.uri)
			if err != nil {
				t.Fatalf("Expected no error, got: %v", err)
			}

			if identity.Type != tt.expectedType {
				t.Errorf("Expected type %s, got %s", tt.expectedType, identity.Type)
			}

			if identity.ID != tt.expectedID {
				t.Errorf("Expected ID %s, got %s", tt.expectedID, identity.ID)
			}

			if identity.TrustDomain != tt.expectedDomain {
				t.Errorf("Expected trust domain %s, got %s", tt.expectedDomain, identity.TrustDomain)
			}

			// Verify identity validates
			if err := identity.Validate(); err != nil {
				t.Errorf("Parsed identity should be valid, got: %v", err)
			}
		})
	}
}

// TestParseSPIFFEID_InvalidCases tests parsing invalid SPIFFE IDs
func TestParseSPIFFEID_InvalidCases(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		expectedErr string
	}{
		{
			name:        "Invalid scheme",
			uri:         "https://cloudless/node/node-123",
			expectedErr: "invalid SPIFFE URI scheme",
		},
		{
			name:        "Wrong trust domain",
			uri:         "spiffe://wrong-domain/node/node-123",
			expectedErr: "invalid trust domain",
		},
		{
			name:        "Invalid path - too short",
			uri:         "spiffe://cloudless/node",
			expectedErr: "invalid SPIFFE ID path",
		},
		{
			name:        "Invalid path - too many parts",
			uri:         "spiffe://cloudless/node/123/extra",
			expectedErr: "invalid SPIFFE ID path",
		},
		{
			name:        "Unknown identity type",
			uri:         "spiffe://cloudless/unknown/id-123",
			expectedErr: "unknown identity type",
		},
		{
			name:        "Malformed URI with empty scheme",
			uri:         "://bad-uri",
			expectedErr: "missing protocol scheme",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			identity, err := ParseSPIFFEID(tt.uri)
			if err == nil {
				t.Fatalf("Expected error containing '%s', got nil", tt.expectedErr)
			}

			if identity != nil {
				t.Errorf("Expected nil identity on error, got: %v", identity)
			}

			// Check error message contains expected text
			if err.Error() == "" || !strings.Contains(err.Error(), tt.expectedErr) {
				t.Errorf("Expected error containing '%s', got: %v", tt.expectedErr, err)
			}
		})
	}
}

// TestSPIFFEIdentity_String tests string representation
func TestSPIFFEIdentity_String(t *testing.T) {
	tests := []struct {
		name        string
		identity    *SPIFFEIdentity
		expectedStr string
	}{
		{
			name:        "Node identity with URI",
			identity:    NewNodeIdentity("node-123"),
			expectedStr: "spiffe://cloudless/node/node-123",
		},
		{
			name:        "Workload identity with URI",
			identity:    NewWorkloadIdentity("workload-abc"),
			expectedStr: "spiffe://cloudless/workload/workload-abc",
		},
		{
			name: "Identity without URI",
			identity: &SPIFFEIdentity{
				TrustDomain: TrustDomain,
				Type:        NodeIdentity,
				ID:          "manual-node",
				URI:         nil,
			},
			expectedStr: "spiffe://cloudless/node/manual-node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			str := tt.identity.String()
			if str != tt.expectedStr {
				t.Errorf("Expected string %s, got %s", tt.expectedStr, str)
			}
		})
	}
}

// TestSPIFFEIdentity_Validate tests identity validation
func TestSPIFFEIdentity_Validate(t *testing.T) {
	tests := []struct {
		name        string
		identity    *SPIFFEIdentity
		expectError bool
		errorText   string
	}{
		{
			name:        "Valid identity",
			identity:    NewNodeIdentity("node-123"),
			expectError: false,
		},
		{
			name: "Missing trust domain",
			identity: &SPIFFEIdentity{
				TrustDomain: "",
				Type:        NodeIdentity,
				ID:          "node-123",
				URI:         &url.URL{},
			},
			expectError: true,
			errorText:   "trust domain is required",
		},
		{
			name: "Missing identity type",
			identity: &SPIFFEIdentity{
				TrustDomain: TrustDomain,
				Type:        "",
				ID:          "node-123",
				URI:         &url.URL{},
			},
			expectError: true,
			errorText:   "identity type is required",
		},
		{
			name: "Missing ID",
			identity: &SPIFFEIdentity{
				TrustDomain: TrustDomain,
				Type:        NodeIdentity,
				ID:          "",
				URI:         &url.URL{},
			},
			expectError: true,
			errorText:   "identity ID is required",
		},
		{
			name: "Missing URI",
			identity: &SPIFFEIdentity{
				TrustDomain: TrustDomain,
				Type:        NodeIdentity,
				ID:          "node-123",
				URI:         nil,
			},
			expectError: true,
			errorText:   "URI is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.identity.Validate()

			if tt.expectError && err == nil {
				t.Errorf("Expected error, got nil")
			}

			if !tt.expectError && err != nil {
				t.Errorf("Expected no error, got: %v", err)
			}

			if tt.expectError && err != nil {
				if !strings.Contains(err.Error(), tt.errorText) {
					t.Errorf("Expected error containing '%s', got: %v", tt.errorText, err)
				}
			}
		})
	}
}

// TestExtractSPIFFEIDFromCert tests extracting SPIFFE ID from certificates
func TestExtractSPIFFEIDFromCert(t *testing.T) {
	// Generate test certificate with SPIFFE URI
	nodeIdentity := NewNodeIdentity("test-node")

	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("Failed to generate key: %v", err)
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-node",
		},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
		URIs:        []*url.URL{nodeIdentity.URI},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		t.Fatalf("Failed to create certificate: %v", err)
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		t.Fatalf("Failed to parse certificate: %v", err)
	}

	t.Run("Extract from valid certificate", func(t *testing.T) {
		identity, err := ExtractSPIFFEIDFromCert(cert)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if identity.ID != "test-node" {
			t.Errorf("Expected ID test-node, got %s", identity.ID)
		}

		if identity.Type != NodeIdentity {
			t.Errorf("Expected type %s, got %s", NodeIdentity, identity.Type)
		}
	})

	t.Run("Nil certificate", func(t *testing.T) {
		_, err := ExtractSPIFFEIDFromCert(nil)
		if err == nil {
			t.Error("Expected error for nil certificate")
		}
	})

	t.Run("Certificate without SPIFFE URI", func(t *testing.T) {
		// Certificate without SPIFFE URI
		templateNoSPIFFE := &x509.Certificate{
			SerialNumber: big.NewInt(2),
			Subject: pkix.Name{
				CommonName: "no-spiffe",
			},
			NotBefore:   time.Now(),
			NotAfter:    time.Now().Add(time.Hour),
			KeyUsage:    x509.KeyUsageDigitalSignature,
			ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
			URIs:        []*url.URL{},
		}

		certNoSPIFFE, err := x509.CreateCertificate(rand.Reader, templateNoSPIFFE, templateNoSPIFFE, &privateKey.PublicKey, privateKey)
		if err != nil {
			t.Fatalf("Failed to create certificate: %v", err)
		}

		parsedCert, err := x509.ParseCertificate(certNoSPIFFE)
		if err != nil {
			t.Fatalf("Failed to parse certificate: %v", err)
		}

		_, err = ExtractSPIFFEIDFromCert(parsedCert)
		if err == nil {
			t.Error("Expected error for certificate without SPIFFE URI")
		}
	})
}

// TestSPIFFEVerifier_VerifyIdentity tests identity verification
func TestSPIFFEVerifier_VerifyIdentity(t *testing.T) {
	logger := zap.NewNop()

	t.Run("Verify allowed identity", func(t *testing.T) {
		verifier := NewSPIFFEVerifier(
			logger,
			[]IdentityType{NodeIdentity, WorkloadIdentity},
			[]string{"node-123", "node-456"},
		)

		identity := NewNodeIdentity("node-123")
		err := verifier.VerifyIdentity(identity)
		if err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
	})

	t.Run("Reject nil identity", func(t *testing.T) {
		verifier := NewSPIFFEVerifier(logger, []IdentityType{NodeIdentity}, nil)
		err := verifier.VerifyIdentity(nil)
		if err == nil {
			t.Error("Expected error for nil identity")
		}
	})

	t.Run("Reject wrong trust domain", func(t *testing.T) {
		verifier := NewSPIFFEVerifier(logger, []IdentityType{NodeIdentity}, nil)

		wrongDomainIdentity := &SPIFFEIdentity{
			TrustDomain: "wrong-domain",
			Type:        NodeIdentity,
			ID:          "node-123",
			URI:         &url.URL{Scheme: "spiffe", Host: "wrong-domain", Path: "/node/node-123"},
		}

		err := verifier.VerifyIdentity(wrongDomainIdentity)
		if err == nil {
			t.Error("Expected error for wrong trust domain")
		}
		if !strings.Contains(err.Error(), "untrusted domain") {
			t.Errorf("Expected 'untrusted domain' error, got: %v", err)
		}
	})

	t.Run("Reject disallowed identity type", func(t *testing.T) {
		verifier := NewSPIFFEVerifier(logger, []IdentityType{NodeIdentity}, nil)

		workloadIdentity := NewWorkloadIdentity("workload-123")
		err := verifier.VerifyIdentity(workloadIdentity)
		if err == nil {
			t.Error("Expected error for disallowed identity type")
		}
		if !strings.Contains(err.Error(), "identity type not allowed") {
			t.Errorf("Expected 'identity type not allowed' error, got: %v", err)
		}
	})

	t.Run("Reject disallowed ID", func(t *testing.T) {
		verifier := NewSPIFFEVerifier(logger, []IdentityType{NodeIdentity}, []string{"node-123"})

		identity := NewNodeIdentity("node-456")
		err := verifier.VerifyIdentity(identity)
		if err == nil {
			t.Error("Expected error for disallowed ID")
		}
		if !strings.Contains(err.Error(), "identity ID not allowed") {
			t.Errorf("Expected 'identity ID not allowed' error, got: %v", err)
		}
	})

	t.Run("Allow any ID when allowedIDs is empty", func(t *testing.T) {
		verifier := NewSPIFFEVerifier(logger, []IdentityType{NodeIdentity}, []string{})

		identity1 := NewNodeIdentity("node-123")
		identity2 := NewNodeIdentity("node-456")

		if err := verifier.VerifyIdentity(identity1); err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}

		if err := verifier.VerifyIdentity(identity2); err != nil {
			t.Errorf("Expected no error, got: %v", err)
		}
	})
}

// TestExtractSPIFFEIDFromContext tests extracting SPIFFE ID from gRPC context
func TestExtractSPIFFEIDFromContext(t *testing.T) {
	// Create test certificate with SPIFFE URI
	nodeIdentity := NewNodeIdentity("context-node")
	privateKey, _ := rsa.GenerateKey(rand.Reader, 2048)

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "context-node"},
		NotBefore:    time.Now(),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		URIs:         []*url.URL{nodeIdentity.URI},
	}

	certDER, _ := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	cert, _ := x509.ParseCertificate(certDER)

	t.Run("Extract from context with TLS peer", func(t *testing.T) {
		// Create TLS connection state
		tlsState := credentials.TLSInfo{
			State: tls.ConnectionState{
				PeerCertificates: []*x509.Certificate{cert},
			},
		}

		ctx := peer.NewContext(context.Background(), &peer.Peer{
			AuthInfo: tlsState,
		})

		identity, err := ExtractSPIFFEIDFromContext(ctx)
		if err != nil {
			t.Fatalf("Expected no error, got: %v", err)
		}

		if identity.ID != "context-node" {
			t.Errorf("Expected ID context-node, got %s", identity.ID)
		}
	})

	t.Run("No peer in context", func(t *testing.T) {
		ctx := context.Background()
		_, err := ExtractSPIFFEIDFromContext(ctx)
		if err == nil {
			t.Error("Expected error for context without peer")
		}
		if !strings.Contains(err.Error(), "no peer info") {
			t.Errorf("Expected 'no peer info' error, got: %v", err)
		}
	})
}

// TestSPIFFEAuthorizer tests authorization based on SPIFFE identities
func TestSPIFFEAuthorizer(t *testing.T) {
	logger := zap.NewNop()

	t.Run("Authorize with matching policy", func(t *testing.T) {
		authorizer := NewSPIFFEAuthorizer(logger)

		policy := &AccessPolicy{
			AllowedTypes: []IdentityType{NodeIdentity},
			AllowedIDs:   []string{"node-123"},
			Resources:    []string{"/api/*"},
			Actions:      []string{"read", "write"},
		}
		authorizer.AddPolicy("test-policy", policy)

		identity := NewNodeIdentity("node-123")
		err := authorizer.Authorize(identity, "/api/workloads", "read")
		if err != nil {
			t.Errorf("Expected authorization to succeed, got: %v", err)
		}
	})

	t.Run("Deny with no matching policy", func(t *testing.T) {
		authorizer := NewSPIFFEAuthorizer(logger)

		policy := &AccessPolicy{
			AllowedTypes: []IdentityType{NodeIdentity},
			AllowedIDs:   []string{"node-123"},
			Resources:    []string{"/api/*"},
			Actions:      []string{"read"},
		}
		authorizer.AddPolicy("test-policy", policy)

		identity := NewNodeIdentity("node-456")
		err := authorizer.Authorize(identity, "/api/workloads", "read")
		if err == nil {
			t.Error("Expected authorization to fail")
		}
	})

	t.Run("Deny nil identity", func(t *testing.T) {
		authorizer := NewSPIFFEAuthorizer(logger)
		err := authorizer.Authorize(nil, "/api/test", "read")
		if err == nil {
			t.Error("Expected error for nil identity")
		}
	})

	t.Run("Wildcard ID matching", func(t *testing.T) {
		authorizer := NewSPIFFEAuthorizer(logger)

		policy := &AccessPolicy{
			AllowedTypes: []IdentityType{NodeIdentity},
			AllowedIDs:   []string{"*"},
			Resources:    []string{"/metrics"},
			Actions:      []string{"read"},
		}
		authorizer.AddPolicy("metrics-policy", policy)

		identity1 := NewNodeIdentity("node-123")
		identity2 := NewNodeIdentity("node-456")

		if err := authorizer.Authorize(identity1, "/metrics", "read"); err != nil {
			t.Errorf("Expected authorization to succeed for node-123, got: %v", err)
		}

		if err := authorizer.Authorize(identity2, "/metrics", "read"); err != nil {
			t.Errorf("Expected authorization to succeed for node-456, got: %v", err)
		}
	})

	t.Run("Wildcard action matching", func(t *testing.T) {
		authorizer := NewSPIFFEAuthorizer(logger)

		policy := &AccessPolicy{
			AllowedTypes: []IdentityType{ControllerIdentity},
			AllowedIDs:   []string{"ctrl-001"},
			Resources:    []string{"/admin/*"},
			Actions:      []string{"*"},
		}
		authorizer.AddPolicy("admin-policy", policy)

		identity := NewControllerIdentity("ctrl-001")

		for _, action := range []string{"read", "write", "delete", "execute"} {
			if err := authorizer.Authorize(identity, "/admin/users", action); err != nil {
				t.Errorf("Expected authorization to succeed for action %s, got: %v", action, err)
			}
		}
	})

	t.Run("Resource pattern matching", func(t *testing.T) {
		authorizer := NewSPIFFEAuthorizer(logger)

		policy := &AccessPolicy{
			AllowedTypes: []IdentityType{ServiceIdentity},
			Resources:    []string{"/api/v1/*", "/health"},
			Actions:      []string{"read"},
		}
		authorizer.AddPolicy("api-policy", policy)

		identity := NewServiceIdentity("api-gateway")

		// Should match /api/v1/*
		if err := authorizer.Authorize(identity, "/api/v1/workloads", "read"); err != nil {
			t.Errorf("Expected authorization to succeed, got: %v", err)
		}

		// Should match exact /health
		if err := authorizer.Authorize(identity, "/health", "read"); err != nil {
			t.Errorf("Expected authorization to succeed for /health, got: %v", err)
		}

		// Should NOT match /api/v2/*
		if err := authorizer.Authorize(identity, "/api/v2/workloads", "read"); err == nil {
			t.Error("Expected authorization to fail for /api/v2/*")
		}
	})
}

// TestMatchPattern tests pattern matching helper function
func TestMatchPattern(t *testing.T) {
	tests := []struct {
		name     string
		str      string
		pattern  string
		expected bool
	}{
		{
			name:     "Exact match",
			str:      "/api/workloads",
			pattern:  "/api/workloads",
			expected: true,
		},
		{
			name:     "Wildcard match all",
			str:      "/anything",
			pattern:  "*",
			expected: true,
		},
		{
			name:     "Prefix wildcard match",
			str:      "/api/v1/workloads",
			pattern:  "/api/v1/*",
			expected: true,
		},
		{
			name:     "Prefix wildcard no match",
			str:      "/other/path",
			pattern:  "/api/*",
			expected: false,
		},
		{
			name:     "No match",
			str:      "/api/workloads",
			pattern:  "/api/nodes",
			expected: false,
		},
		{
			name:     "Empty string with wildcard",
			str:      "",
			pattern:  "*",
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := matchPattern(tt.str, tt.pattern)
			if result != tt.expected {
				t.Errorf("matchPattern(%q, %q) = %v, expected %v", tt.str, tt.pattern, result, tt.expected)
			}
		})
	}
}
