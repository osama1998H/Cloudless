package mtls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// CertificateManager manages certificates for nodes and workloads
type CertificateManager struct {
	ca               *CA
	dataDir          string
	logger           *zap.Logger
	rotationInterval time.Duration
	certificates     map[string]*Certificate // nodeID/workloadID -> cert
	mu               sync.RWMutex
	stopCh           chan struct{}
}

// Certificate represents a certificate with its metadata
type Certificate struct {
	Cert       *x509.Certificate
	PrivateKey *rsa.PrivateKey
	CertPEM    []byte
	KeyPEM     []byte
	IssuedAt   time.Time
	ExpiresAt  time.Time
	Subject    string
	Type       CertificateType
}

// CertificateType represents the type of certificate
type CertificateType string

const (
	NodeCertificate     CertificateType = "node"
	WorkloadCertificate CertificateType = "workload"
	ServiceCertificate  CertificateType = "service"
)

// CertificateManagerConfig contains configuration for the certificate manager
type CertificateManagerConfig struct {
	CA               *CA
	DataDir          string
	Logger           *zap.Logger
	RotationInterval time.Duration
}

// NewCertificateManager creates a new certificate manager
func NewCertificateManager(config CertificateManagerConfig) (*CertificateManager, error) {
	if config.CA == nil {
		return nil, fmt.Errorf("CA is required")
	}
	if config.DataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if config.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}
	if config.RotationInterval <= 0 {
		config.RotationInterval = 24 * time.Hour // Default to 24 hours
	}

	// Create certificates directory
	certsDir := filepath.Join(config.DataDir, "certificates")
	if err := os.MkdirAll(certsDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create certificates directory: %w", err)
	}

	cm := &CertificateManager{
		ca:               config.CA,
		dataDir:          config.DataDir,
		logger:           config.Logger,
		rotationInterval: config.RotationInterval,
		certificates:     make(map[string]*Certificate),
		stopCh:           make(chan struct{}),
	}

	// Load existing certificates from disk
	if err := cm.loadCertificates(); err != nil {
		config.Logger.Warn("Failed to load existing certificates", zap.Error(err))
	}

	return cm, nil
}

// GenerateNodeCertificate generates a certificate for a node
func (cm *CertificateManager) GenerateNodeCertificate(nodeID, nodeName string, ips []net.IP, dnsNames []string, validityDays int) (*Certificate, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.logger.Info("Generating node certificate",
		zap.String("node_id", nodeID),
		zap.String("node_name", nodeName),
	)

	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate request
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   nodeName,
			Organization: []string{"Cloudless"},
			Country:      []string{"US"},
			SerialNumber: nodeID,
		},
		IPAddresses: ips,
		DNSNames:    dnsNames,
	}

	// Add SPIFFE URI for the node
	spiffeURI, _ := url.Parse(fmt.Sprintf("spiffe://cloudless/node/%s", nodeID))
	template.URIs = []*url.URL{spiffeURI}

	// Create CSR
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate request: %w", err)
	}

	// Sign with CA
	cert, certPEM, err := cm.ca.SignCertificate(csr, validityDays)
	if err != nil {
		return nil, fmt.Errorf("failed to sign certificate: %w", err)
	}

	// Encode private key
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// Create certificate object
	certificate := &Certificate{
		Cert:       cert,
		PrivateKey: privateKey,
		CertPEM:    certPEM,
		KeyPEM:     keyPEM,
		IssuedAt:   cert.NotBefore,
		ExpiresAt:  cert.NotAfter,
		Subject:    nodeID,
		Type:       NodeCertificate,
	}

	// Store in memory
	cm.certificates[nodeID] = certificate

	// Save to disk
	if err := cm.saveCertificate(nodeID, certificate); err != nil {
		cm.logger.Error("Failed to save certificate to disk", zap.Error(err))
	}

	return certificate, nil
}

// GenerateWorkloadCertificate generates a certificate for a workload
func (cm *CertificateManager) GenerateWorkloadCertificate(workloadID, workloadName string, validityDays int) (*Certificate, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cm.logger.Info("Generating workload certificate",
		zap.String("workload_id", workloadID),
		zap.String("workload_name", workloadName),
	)

	// Generate private key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate request
	template := &x509.CertificateRequest{
		Subject: pkix.Name{
			CommonName:   workloadName,
			Organization: []string{"Cloudless"},
			Country:      []string{"US"},
			SerialNumber: workloadID,
		},
	}

	// Add SPIFFE URI for the workload
	spiffeURI, _ := url.Parse(fmt.Sprintf("spiffe://cloudless/workload/%s", workloadID))
	template.URIs = []*url.URL{spiffeURI}

	// Create CSR
	csrDER, err := x509.CreateCertificateRequest(rand.Reader, template, privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to create certificate request: %w", err)
	}

	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate request: %w", err)
	}

	// Sign with CA
	cert, certPEM, err := cm.ca.SignCertificate(csr, validityDays)
	if err != nil {
		return nil, fmt.Errorf("failed to sign certificate: %w", err)
	}

	// Encode private key
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	// Create certificate object
	certificate := &Certificate{
		Cert:       cert,
		PrivateKey: privateKey,
		CertPEM:    certPEM,
		KeyPEM:     keyPEM,
		IssuedAt:   cert.NotBefore,
		ExpiresAt:  cert.NotAfter,
		Subject:    workloadID,
		Type:       WorkloadCertificate,
	}

	// Store in memory
	cm.certificates[workloadID] = certificate

	// Save to disk
	if err := cm.saveCertificate(workloadID, certificate); err != nil {
		cm.logger.Error("Failed to save certificate to disk", zap.Error(err))
	}

	return certificate, nil
}

// GetCertificate retrieves a certificate by ID
func (cm *CertificateManager) GetCertificate(id string) (*Certificate, error) {
	cm.mu.RLock()
	defer cm.mu.RUnlock()

	cert, exists := cm.certificates[id]
	if !exists {
		return nil, fmt.Errorf("certificate not found for ID: %s", id)
	}

	// Check if certificate is expired
	if time.Now().After(cert.ExpiresAt) {
		return nil, fmt.Errorf("certificate for %s has expired", id)
	}

	return cert, nil
}

// RotateCertificate rotates a certificate
func (cm *CertificateManager) RotateCertificate(id string) (*Certificate, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	oldCert, exists := cm.certificates[id]
	if !exists {
		return nil, fmt.Errorf("certificate not found for ID: %s", id)
	}

	cm.logger.Info("Rotating certificate",
		zap.String("id", id),
		zap.String("type", string(oldCert.Type)),
	)

	// Generate new certificate based on type
	var newCert *Certificate
	var err error

	switch oldCert.Type {
	case NodeCertificate:
		// Extract information from old certificate
		ips := oldCert.Cert.IPAddresses
		dnsNames := oldCert.Cert.DNSNames
		cm.mu.Unlock() // Unlock before calling GenerateNodeCertificate
		newCert, err = cm.GenerateNodeCertificate(id, oldCert.Cert.Subject.CommonName, ips, dnsNames, 30)
		cm.mu.Lock() // Re-lock
	case WorkloadCertificate:
		cm.mu.Unlock() // Unlock before calling GenerateWorkloadCertificate
		newCert, err = cm.GenerateWorkloadCertificate(id, oldCert.Cert.Subject.CommonName, 7)
		cm.mu.Lock() // Re-lock
	default:
		return nil, fmt.Errorf("unknown certificate type: %s", oldCert.Type)
	}

	if err != nil {
		return nil, fmt.Errorf("failed to generate new certificate: %w", err)
	}

	// Replace in memory
	cm.certificates[id] = newCert

	// Save to disk
	if err := cm.saveCertificate(id, newCert); err != nil {
		cm.logger.Error("Failed to save rotated certificate", zap.Error(err))
	}

	cm.logger.Info("Certificate rotated successfully",
		zap.String("id", id),
		zap.Time("expires_at", newCert.ExpiresAt),
	)

	return newCert, nil
}

// StartRotation starts automatic certificate rotation
func (cm *CertificateManager) StartRotation() {
	cm.logger.Info("Starting automatic certificate rotation",
		zap.Duration("interval", cm.rotationInterval),
	)

	ticker := time.NewTicker(cm.rotationInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			cm.rotateExpiredCertificates()
		case <-cm.stopCh:
			cm.logger.Info("Stopping certificate rotation")
			return
		}
	}
}

// StopRotation stops automatic certificate rotation
func (cm *CertificateManager) StopRotation() {
	close(cm.stopCh)
}

// GetCACertificate returns the CA certificate PEM
func (cm *CertificateManager) GetCACertificate() []byte {
	return cm.ca.GetCACertificate()
}

// rotateExpiredCertificates checks and rotates expired certificates
func (cm *CertificateManager) rotateExpiredCertificates() {
	cm.mu.RLock()
	ids := make([]string, 0, len(cm.certificates))
	for id := range cm.certificates {
		ids = append(ids, id)
	}
	cm.mu.RUnlock()

	rotated := 0
	failed := 0

	for _, id := range ids {
		cert, err := cm.GetCertificate(id)
		if err != nil {
			continue // Already expired or not found
		}

		// Rotate if certificate will expire within 1 hour
		if time.Until(cert.ExpiresAt) < time.Hour {
			if _, err := cm.RotateCertificate(id); err != nil {
				cm.logger.Error("Failed to rotate certificate",
					zap.String("id", id),
					zap.Error(err),
				)
				failed++
			} else {
				rotated++
			}
		}
	}

	if rotated > 0 || failed > 0 {
		cm.logger.Info("Certificate rotation cycle completed",
			zap.Int("rotated", rotated),
			zap.Int("failed", failed),
		)
	}
}

// saveCertificate saves a certificate to disk
func (cm *CertificateManager) saveCertificate(id string, cert *Certificate) error {
	certsDir := filepath.Join(cm.dataDir, "certificates", id)
	if err := os.MkdirAll(certsDir, 0700); err != nil {
		return fmt.Errorf("failed to create certificate directory: %w", err)
	}

	// Save certificate
	certPath := filepath.Join(certsDir, "cert.pem")
	if err := os.WriteFile(certPath, cert.CertPEM, 0644); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Save private key
	keyPath := filepath.Join(certsDir, "key.pem")
	if err := os.WriteFile(keyPath, cert.KeyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	// Save metadata
	metaPath := filepath.Join(certsDir, "metadata")
	metadata := fmt.Sprintf("type=%s\nissued=%s\nexpires=%s\n",
		cert.Type,
		cert.IssuedAt.Format(time.RFC3339),
		cert.ExpiresAt.Format(time.RFC3339),
	)
	if err := os.WriteFile(metaPath, []byte(metadata), 0644); err != nil {
		return fmt.Errorf("failed to write metadata: %w", err)
	}

	return nil
}

// loadCertificates loads certificates from disk
func (cm *CertificateManager) loadCertificates() error {
	certsDir := filepath.Join(cm.dataDir, "certificates")

	entries, err := os.ReadDir(certsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No certificates yet
		}
		return fmt.Errorf("failed to read certificates directory: %w", err)
	}

	loaded := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		id := entry.Name()
		certPath := filepath.Join(certsDir, id, "cert.pem")
		keyPath := filepath.Join(certsDir, id, "key.pem")

		// Load certificate
		if cert, err := cm.loadCertificate(id, certPath, keyPath); err != nil {
			cm.logger.Warn("Failed to load certificate",
				zap.String("id", id),
				zap.Error(err),
			)
		} else {
			cm.certificates[id] = cert
			loaded++
		}
	}

	cm.logger.Info("Loaded certificates from disk", zap.Int("count", loaded))
	return nil
}

// loadCertificate loads a single certificate from disk
func (cm *CertificateManager) loadCertificate(id, certPath, keyPath string) (*Certificate, error) {
	// Read certificate
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read certificate: %w", err)
	}

	// Read private key
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read private key: %w", err)
	}

	// Parse certificate
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, fmt.Errorf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Parse private key
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return nil, fmt.Errorf("failed to decode private key PEM")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	// Determine certificate type from metadata or URIs
	certType := NodeCertificate
	if len(cert.URIs) > 0 {
		uri := cert.URIs[0].String()
		if contains(uri, "/workload/") {
			certType = WorkloadCertificate
		} else if contains(uri, "/service/") {
			certType = ServiceCertificate
		}
	}

	return &Certificate{
		Cert:       cert,
		PrivateKey: privateKey,
		CertPEM:    certPEM,
		KeyPEM:     keyPEM,
		IssuedAt:   cert.NotBefore,
		ExpiresAt:  cert.NotAfter,
		Subject:    id,
		Type:       certType,
	}, nil
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[len(s)-len(substr):] == substr
}
