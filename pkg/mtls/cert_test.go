package mtls

import (
	"crypto/x509"
	"encoding/pem"
	"net"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestCertificateManager_Creation verifies CLD-REQ-001 mTLS setup.
// CLD-REQ-001: Certificate manager must be properly initialized with CA.
func TestCertificateManager_Creation(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	// Create CA
	ca, err := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("Failed to create CA: %v", err)
	}

	// Create certificate manager
	certManager, err := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})
	if err != nil {
		t.Fatalf("Failed to create certificate manager: %v", err)
	}

	if certManager == nil {
		t.Fatal("Certificate manager should not be nil")
	}
	if certManager.ca != ca {
		t.Error("Certificate manager should reference provided CA")
	}
}

// TestCertificateManager_GenerateNodeCertificate verifies CLD-REQ-001 node certificates.
// CLD-REQ-001: Enrollment must issue node certificates for mTLS handshake.
func TestCertificateManager_GenerateNodeCertificate(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, _ := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	tests := []struct {
		name         string
		nodeID       string
		nodeName     string
		ips          []net.IP
		dnsNames     []string
		validityDays int
	}{
		{
			name:         "basic certificate",
			nodeID:       "node-123",
			nodeName:     "test-node",
			ips:          nil,
			dnsNames:     nil,
			validityDays: 30,
		},
		{
			name:         "certificate with IPs",
			nodeID:       "node-456",
			nodeName:     "test-node-2",
			ips:          []net.IP{net.ParseIP("192.168.1.1"), net.ParseIP("10.0.0.1")},
			dnsNames:     nil,
			validityDays: 30,
		},
		{
			name:         "certificate with DNS names",
			nodeID:       "node-789",
			nodeName:     "test-node-3",
			ips:          nil,
			dnsNames:     []string{"node.example.com", "node.local"},
			validityDays: 30,
		},
		{
			name:         "certificate with IPs and DNS",
			nodeID:       "node-abc",
			nodeName:     "test-node-4",
			ips:          []net.IP{net.ParseIP("172.16.0.1")},
			dnsNames:     []string{"node4.example.com"},
			validityDays: 90,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cert, err := certManager.GenerateNodeCertificate(
				tt.nodeID,
				tt.nodeName,
				tt.ips,
				tt.dnsNames,
				tt.validityDays,
			)
			if err != nil {
				t.Fatalf("Failed to generate certificate: %v", err)
			}

			// Verify certificate metadata
			if cert == nil {
				t.Fatal("Certificate should not be nil")
			}
			if cert.Type != NodeCertificate {
				t.Errorf("Expected type %s, got %s", NodeCertificate, cert.Type)
			}
			// Subject is set to nodeID (line 161 in cert.go)
			if cert.Subject != tt.nodeID {
				t.Errorf("Expected subject %s, got %s", tt.nodeID, cert.Subject)
			}

			// Verify PEM encoding
			if len(cert.CertPEM) == 0 {
				t.Error("Certificate PEM should not be empty")
			}
			if len(cert.KeyPEM) == 0 {
				t.Error("Key PEM should not be empty")
			}

			// Verify PEM format
			if !strings.HasPrefix(string(cert.CertPEM), "-----BEGIN CERTIFICATE-----") {
				t.Error("Certificate should be PEM encoded")
			}
			if !strings.HasPrefix(string(cert.KeyPEM), "-----BEGIN RSA PRIVATE KEY-----") {
				t.Error("Private key should be PEM encoded")
			}

			// Verify X.509 certificate
			if cert.Cert == nil {
				t.Fatal("X.509 certificate should not be nil")
			}

			// Verify SPIFFE URI
			expectedURI := "spiffe://cloudless/node/" + tt.nodeID
			found := false
			for _, uri := range cert.Cert.URIs {
				if uri.String() == expectedURI {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("Expected SPIFFE URI %s not found in certificate", expectedURI)
			}

			// Verify IPs
			if len(tt.ips) > 0 {
				if len(cert.Cert.IPAddresses) != len(tt.ips) {
					t.Errorf("Expected %d IP addresses, got %d", len(tt.ips), len(cert.Cert.IPAddresses))
				}
			}

			// Verify DNS names
			if len(tt.dnsNames) > 0 {
				if len(cert.Cert.DNSNames) != len(tt.dnsNames) {
					t.Errorf("Expected %d DNS names, got %d", len(tt.dnsNames), len(cert.Cert.DNSNames))
				}
			}

			// Verify validity period
			now := time.Now()
			if cert.Cert.NotBefore.After(now) {
				t.Error("Certificate should be valid now")
			}
			expectedExpiry := now.AddDate(0, 0, tt.validityDays)
			// Allow 1 hour tolerance for test execution time
			if cert.Cert.NotAfter.Before(expectedExpiry.Add(-1*time.Hour)) || cert.Cert.NotAfter.After(expectedExpiry.Add(1*time.Hour)) {
				t.Errorf("Expected expiry around %v, got %v", expectedExpiry, cert.Cert.NotAfter)
			}
		})
	}
}

// TestCertificateManager_GenerateWorkloadCertificate verifies CLD-REQ-001 workload certificates.
// CLD-REQ-001: Workloads must have certificates for secure communication.
func TestCertificateManager_GenerateWorkloadCertificate(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, _ := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	cert, err := certManager.GenerateWorkloadCertificate("workload-123", "test-workload", 30)
	if err != nil {
		t.Fatalf("Failed to generate workload certificate: %v", err)
	}

	// Verify certificate type
	if cert.Type != WorkloadCertificate {
		t.Errorf("Expected type %s, got %s", WorkloadCertificate, cert.Type)
	}

	// Verify SPIFFE URI for workload
	expectedURI := "spiffe://cloudless/workload/workload-123"
	found := false
	for _, uri := range cert.Cert.URIs {
		if uri.String() == expectedURI {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("Expected SPIFFE URI %s not found in certificate", expectedURI)
	}
}

// TestCertificateManager_PEMEncoding verifies CLD-REQ-001 certificate format.
// CLD-REQ-001: Certificates must be properly encoded for transmission.
func TestCertificateManager_PEMEncoding(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, _ := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	cert, _ := certManager.GenerateNodeCertificate("node-test", "test-node", nil, nil, 30)

	// Decode certificate PEM
	certBlock, _ := pem.Decode(cert.CertPEM)
	if certBlock == nil {
		t.Fatal("Failed to decode certificate PEM")
	}
	if certBlock.Type != "CERTIFICATE" {
		t.Errorf("Expected PEM type CERTIFICATE, got %s", certBlock.Type)
	}

	// Parse X.509 certificate from PEM
	parsedCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse certificate: %v", err)
	}
	if parsedCert == nil {
		t.Fatal("Parsed certificate should not be nil")
	}

	// Decode private key PEM
	keyBlock, _ := pem.Decode(cert.KeyPEM)
	if keyBlock == nil {
		t.Fatal("Failed to decode private key PEM")
	}
	if keyBlock.Type != "RSA PRIVATE KEY" {
		t.Errorf("Expected PEM type RSA PRIVATE KEY, got %s", keyBlock.Type)
	}
}

// TestCertificateManager_GetCertificate verifies CLD-REQ-001 certificate retrieval.
// CLD-REQ-001: Certificates must be retrievable by ID.
func TestCertificateManager_GetCertificate(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, _ := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	// Generate certificate
	nodeID := "node-get-test"
	cert, err := certManager.GenerateNodeCertificate(nodeID, "test-node", nil, nil, 30)
	if err != nil {
		t.Fatalf("Failed to generate certificate: %v", err)
	}

	// Retrieve certificate
	retrieved, err := certManager.GetCertificate(nodeID)
	if err != nil {
		t.Fatalf("Failed to get certificate: %v", err)
	}

	if retrieved == nil {
		t.Fatal("Retrieved certificate should not be nil")
	}
	if retrieved.Subject != cert.Subject {
		t.Errorf("Expected subject %s, got %s", cert.Subject, retrieved.Subject)
	}

	// Try to get non-existent certificate
	_, err = certManager.GetCertificate("non-existent")
	if err == nil {
		t.Error("Expected error for non-existent certificate")
	}
}

// TestCertificateManager_GetCACertificate verifies CLD-REQ-001 CA distribution.
// CLD-REQ-001: Nodes must receive CA certificate for validation.
func TestCertificateManager_GetCACertificate(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, _ := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	caCertPEM := certManager.GetCACertificate()
	if len(caCertPEM) == 0 {
		t.Fatal("CA certificate should not be empty")
	}

	// Verify PEM encoding
	if !strings.HasPrefix(string(caCertPEM), "-----BEGIN CERTIFICATE-----") {
		t.Error("CA certificate should be PEM encoded")
	}

	// Decode and verify
	certBlock, _ := pem.Decode(caCertPEM)
	if certBlock == nil {
		t.Fatal("Failed to decode CA certificate PEM")
	}

	caCert, err := x509.ParseCertificate(certBlock.Bytes)
	if err != nil {
		t.Fatalf("Failed to parse CA certificate: %v", err)
	}

	// Verify it's a CA certificate
	if !caCert.IsCA {
		t.Error("Certificate should be marked as CA")
	}
}

// TestCertificateManager_RotateCertificate verifies CLD-REQ-001 certificate renewal.
// CLD-REQ-001: Certificates must support rotation for security.
func TestCertificateManager_RotateCertificate(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, _ := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	// Generate initial certificate
	nodeID := "node-rotate"
	originalCert, _ := certManager.GenerateNodeCertificate(nodeID, "test-node", nil, nil, 30)

	// Wait a bit to ensure different timestamps
	time.Sleep(10 * time.Millisecond)

	// Rotate certificate
	newCert, err := certManager.RotateCertificate(nodeID)
	if err != nil {
		t.Fatalf("Failed to rotate certificate: %v", err)
	}

	// Verify new certificate is different
	if string(newCert.CertPEM) == string(originalCert.CertPEM) {
		t.Error("Rotated certificate should be different from original")
	}

	// Verify subject remains the same
	if newCert.Subject != originalCert.Subject {
		t.Error("Subject should remain the same after rotation")
	}

	// Verify type remains the same
	if newCert.Type != originalCert.Type {
		t.Error("Certificate type should remain the same after rotation")
	}

	// Verify private key is different (new key generated)
	if string(newCert.KeyPEM) == string(originalCert.KeyPEM) {
		t.Error("Rotated certificate should have new private key")
	}
}

// TestCertificateManager_Persistence verifies CLD-REQ-001 certificate storage.
// CLD-REQ-001: Certificates must be persisted for recovery.
func TestCertificateManager_Persistence(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, _ := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	// Create first manager and generate certificate
	certManager1, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	nodeID := "node-persist"
	cert1, _ := certManager1.GenerateNodeCertificate(nodeID, "test-node", nil, nil, 30)

	// Create second manager with same data dir (simulating restart)
	certManager2, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	// Retrieve certificate from second manager
	cert2, err := certManager2.GetCertificate(nodeID)
	if err != nil {
		t.Fatalf("Failed to retrieve certificate after restart: %v", err)
	}

	// Verify certificates match
	if string(cert2.CertPEM) != string(cert1.CertPEM) {
		t.Error("Persisted certificate should match original")
	}
}

// TestCertificateManager_ConcurrentGeneration verifies CLD-REQ-001 thread safety.
// CLD-REQ-001: Certificate generation must be thread-safe.
func TestCertificateManager_ConcurrentGeneration(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, _ := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	certManager, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	// Generate certificates concurrently
	numGoroutines := 10
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	errors := make(chan error, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			defer wg.Done()
			nodeID := "node-concurrent-" + string(rune('0'+id))
			_, err := certManager.GenerateNodeCertificate(nodeID, "test-node", nil, nil, 30)
			if err != nil {
				errors <- err
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent generation error: %v", err)
	}

	// Verify all certificates were created
	certManager.mu.RLock()
	count := len(certManager.certificates)
	certManager.mu.RUnlock()

	if count != numGoroutines {
		t.Errorf("Expected %d certificates, got %d", numGoroutines, count)
	}
}

// TestCA_Creation verifies CLD-REQ-001 CA initialization.
// CLD-REQ-001: CA must be properly initialized for certificate signing.
func TestCA_Creation(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, err := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("Failed to create CA: %v", err)
	}

	if ca == nil {
		t.Fatal("CA should not be nil")
	}
	if ca.cert == nil {
		t.Fatal("CA certificate should not be nil")
	}
	if ca.privateKey == nil {
		t.Fatal("CA private key should not be nil")
	}
	if len(ca.certPEM) == 0 {
		t.Error("CA certificate PEM should not be empty")
	}
}

// TestCA_Persistence verifies CLD-REQ-001 CA certificate persistence.
// CLD-REQ-001: CA must persist and reload certificates across restarts.
func TestCA_Persistence(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()
	caDir := filepath.Join(tempDir, "ca")

	// Create first CA
	ca1, _ := NewCA(CAConfig{
		DataDir:      caDir,
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	cert1PEM := ca1.certPEM

	// Create second CA with same data dir (simulating restart)
	ca2, err := NewCA(CAConfig{
		DataDir:      caDir,
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})
	if err != nil {
		t.Fatalf("Failed to reload CA: %v", err)
	}

	// Verify CA certificate matches
	if string(ca2.certPEM) != string(cert1PEM) {
		t.Error("Reloaded CA certificate should match original")
	}
}

// TestCA_SignCertificate verifies CLD-REQ-001 certificate signing.
// CLD-REQ-001: CA must sign certificate requests properly.
func TestCA_SignCertificate(t *testing.T) {
	logger := zap.NewNop()
	tempDir := t.TempDir()

	ca, _ := NewCA(CAConfig{
		DataDir:      filepath.Join(tempDir, "ca"),
		Organization: "Test Org",
		Country:      "US",
		Logger:       logger,
	})

	// Create a certificate request (CSR)
	// This is tested indirectly through GenerateNodeCertificate,
	// but we verify the CA can sign properly
	certManager, _ := NewCertificateManager(CertificateManagerConfig{
		CA:      ca,
		DataDir: filepath.Join(tempDir, "certs"),
		Logger:  logger,
	})

	cert, err := certManager.GenerateNodeCertificate("test-node", "node", nil, nil, 30)
	if err != nil {
		t.Fatalf("Failed to generate signed certificate: %v", err)
	}

	// Verify certificate is signed by CA
	certBlock, _ := pem.Decode(cert.CertPEM)
	parsedCert, _ := x509.ParseCertificate(certBlock.Bytes)

	// Create certificate pool with CA
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)

	// Verify certificate chain
	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}

	_, err = parsedCert.Verify(opts)
	if err != nil {
		t.Errorf("Certificate verification failed: %v", err)
	}
}
