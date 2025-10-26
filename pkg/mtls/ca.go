package mtls

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"
)

// CA represents a Certificate Authority for the Cloudless platform
type CA struct {
	cert       *x509.Certificate
	privateKey *rsa.PrivateKey
	certPEM    []byte
	keyPEM     []byte
	dataDir    string
	logger     *zap.Logger
	mu         sync.RWMutex
}

// CAConfig contains configuration for the Certificate Authority
type CAConfig struct {
	DataDir      string
	Organization string
	Country      string
	Logger       *zap.Logger
}

// NewCA creates a new Certificate Authority
func NewCA(config CAConfig) (*CA, error) {
	if config.DataDir == "" {
		return nil, fmt.Errorf("data directory is required")
	}
	if config.Organization == "" {
		config.Organization = "Cloudless"
	}
	if config.Country == "" {
		config.Country = "US"
	}
	if config.Logger == nil {
		return nil, fmt.Errorf("logger is required")
	}

	ca := &CA{
		dataDir: config.DataDir,
		logger:  config.Logger,
	}

	// Create CA directory if it doesn't exist
	caDir := filepath.Join(config.DataDir, "ca")
	if err := os.MkdirAll(caDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create CA directory: %w", err)
	}

	// Try to load existing CA certificate and key
	certPath := filepath.Join(caDir, "ca.crt")
	keyPath := filepath.Join(caDir, "ca.key")

	if err := ca.loadFromDisk(certPath, keyPath); err != nil {
		config.Logger.Info("Creating new CA certificate", zap.Error(err))
		// Generate new CA certificate
		if err := ca.generateCA(config.Organization, config.Country); err != nil {
			return nil, fmt.Errorf("failed to generate CA: %w", err)
		}
		// Save to disk
		if err := ca.saveToDisk(certPath, keyPath); err != nil {
			return nil, fmt.Errorf("failed to save CA: %w", err)
		}
	} else {
		config.Logger.Info("Loaded existing CA certificate",
			zap.String("subject", ca.cert.Subject.String()),
			zap.Time("not_after", ca.cert.NotAfter),
		)
	}

	return ca, nil
}

// generateCA generates a new self-signed CA certificate
func (ca *CA) generateCA(organization, country string) error {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	// Generate RSA key pair
	privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	// Create certificate template
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{organization},
			Country:      []string{country},
			CommonName:   "Cloudless CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour), // 1 year validity
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature | x509.KeyUsageCRLSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		MaxPathLen:            2,
		IPAddresses:           []net.IP{net.IPv4(127, 0, 0, 1)},
	}

	// Self-sign the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &privateKey.PublicKey, privateKey)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	// Parse the certificate
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Encode certificate
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	// Encode private key
	keyPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(privateKey),
	})

	ca.cert = cert
	ca.privateKey = privateKey
	ca.certPEM = certPEM
	ca.keyPEM = keyPEM

	ca.logger.Info("Generated new CA certificate",
		zap.String("subject", cert.Subject.String()),
		zap.Time("not_before", cert.NotBefore),
		zap.Time("not_after", cert.NotAfter),
	)

	return nil
}

// loadFromDisk loads the CA certificate and key from disk
func (ca *CA) loadFromDisk(certPath, keyPath string) error {
	ca.mu.Lock()
	defer ca.mu.Unlock()

	// Read certificate file
	certPEM, err := os.ReadFile(certPath)
	if err != nil {
		return fmt.Errorf("failed to read certificate file: %w", err)
	}

	// Read private key file
	keyPEM, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("failed to read private key file: %w", err)
	}

	// Parse certificate
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("failed to decode certificate PEM")
	}

	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return fmt.Errorf("failed to parse certificate: %w", err)
	}

	// Parse private key
	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("failed to decode private key PEM")
	}

	privateKey, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		// Try PKCS8 format
		keyInterface, err := x509.ParsePKCS8PrivateKey(keyBlock.Bytes)
		if err != nil {
			return fmt.Errorf("failed to parse private key: %w", err)
		}
		var ok bool
		privateKey, ok = keyInterface.(*rsa.PrivateKey)
		if !ok {
			return fmt.Errorf("private key is not RSA")
		}
	}

	// Check certificate validity
	if time.Now().After(cert.NotAfter) {
		return fmt.Errorf("CA certificate has expired")
	}

	ca.cert = cert
	ca.privateKey = privateKey
	ca.certPEM = certPEM
	ca.keyPEM = keyPEM

	return nil
}

// saveToDisk saves the CA certificate and key to disk
func (ca *CA) saveToDisk(certPath, keyPath string) error {
	ca.mu.RLock()
	defer ca.mu.RUnlock()

	// Write certificate
	if err := os.WriteFile(certPath, ca.certPEM, 0644); err != nil {
		return fmt.Errorf("failed to write certificate: %w", err)
	}

	// Write private key (with restricted permissions)
	if err := os.WriteFile(keyPath, ca.keyPEM, 0600); err != nil {
		return fmt.Errorf("failed to write private key: %w", err)
	}

	ca.logger.Info("Saved CA certificate to disk",
		zap.String("cert_path", certPath),
		zap.String("key_path", keyPath),
	)

	return nil
}

// SignCertificate signs a certificate signing request
func (ca *CA) SignCertificate(csr *x509.CertificateRequest, validityDays int) (*x509.Certificate, []byte, error) {
	ca.mu.RLock()
	defer ca.mu.RUnlock()

	if ca.cert == nil || ca.privateKey == nil {
		return nil, nil, fmt.Errorf("CA not initialized")
	}

	// Generate serial number
	serialNumber, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, nil, fmt.Errorf("failed to generate serial number: %w", err)
	}

	// Create certificate template from CSR
	template := &x509.Certificate{
		SerialNumber:          serialNumber,
		Subject:               csr.Subject,
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Duration(validityDays) * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		DNSNames:              csr.DNSNames,
		IPAddresses:           csr.IPAddresses,
		URIs:                  csr.URIs,
	}

	// Sign the certificate
	certDER, err := x509.CreateCertificate(rand.Reader, template, ca.cert, csr.PublicKey, ca.privateKey)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to sign certificate: %w", err)
	}

	// Parse the certificate
	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to parse signed certificate: %w", err)
	}

	// Encode to PEM
	certPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: certDER,
	})

	ca.logger.Info("Signed certificate",
		zap.String("subject", cert.Subject.String()),
		zap.String("serial", cert.SerialNumber.String()),
		zap.Time("not_after", cert.NotAfter),
	)

	return cert, certPEM, nil
}

// GetCACertificate returns the CA certificate in PEM format
func (ca *CA) GetCACertificate() []byte {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	return ca.certPEM
}

// GetCAPrivateKey returns the CA private key in PEM format (use with caution)
func (ca *CA) GetCAPrivateKey() []byte {
	ca.mu.RLock()
	defer ca.mu.RUnlock()
	return ca.keyPEM
}

// VerifyCertificate verifies if a certificate was signed by this CA
func (ca *CA) VerifyCertificate(cert *x509.Certificate) error {
	ca.mu.RLock()
	defer ca.mu.RUnlock()

	if ca.cert == nil {
		return fmt.Errorf("CA not initialized")
	}

	// Create certificate pool with CA cert
	roots := x509.NewCertPool()
	roots.AddCert(ca.cert)

	// Verify certificate
	opts := x509.VerifyOptions{
		Roots:     roots,
		KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth},
	}

	if _, err := cert.Verify(opts); err != nil {
		return fmt.Errorf("certificate verification failed: %w", err)
	}

	return nil
}

// RotateCA generates a new CA certificate (careful: invalidates all existing certificates)
func (ca *CA) RotateCA(organization, country string) error {
	ca.logger.Warn("Rotating CA certificate - all existing certificates will be invalidated")

	// Generate new CA
	if err := ca.generateCA(organization, country); err != nil {
		return fmt.Errorf("failed to generate new CA: %w", err)
	}

	// Save to disk
	certPath := filepath.Join(ca.dataDir, "ca", "ca.crt")
	keyPath := filepath.Join(ca.dataDir, "ca", "ca.key")
	if err := ca.saveToDisk(certPath, keyPath); err != nil {
		return fmt.Errorf("failed to save new CA: %w", err)
	}

	ca.logger.Info("CA certificate rotated successfully")
	return nil
}
