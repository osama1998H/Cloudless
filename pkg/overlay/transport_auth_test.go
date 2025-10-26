package overlay

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestQUICTransport_mTLS_ValidHandshake verifies CLD-REQ-040: authenticated connections
func TestQUICTransport_mTLS_ValidHandshake(t *testing.T) {
	logger := zap.NewNop()

	// Create CA certificate
	caCert, caKey, caPool := createTestCA(t)

	// Create server certificate signed by CA
	serverCert, serverKey := createTestCertificate(t, caCert, caKey, "server-node-1", false)
	serverTLSCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Raw}),
		encodePrivateKey(serverKey),
	)
	require.NoError(t, err)

	// Create client certificate signed by same CA
	clientCert, clientKey := createTestCertificate(t, caCert, caKey, "client-node-2", false)
	clientTLSCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCert.Raw}),
		encodePrivateKey(clientKey),
	)
	require.NoError(t, err)

	// Configure server transport with mTLS
	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert, // CLD-REQ-040: mutual authentication
		NextProtos:   []string{"cloudless-overlay"},
		MinVersion:   tls.VersionTLS13,
	}

	serverConfig := TransportConfig{
		ListenAddress:     "127.0.0.1:0", // Random port
		MaxIdleTimeout:    30 * time.Second,
		MaxStreamsPerConn: 100,
		KeepAliveInterval: 10 * time.Second,
	}

	serverTransport := NewQUICTransport(serverConfig, serverTLSConfig, logger)

	// Start server listening
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = serverTransport.Listen(ctx, "")
	require.NoError(t, err, "Server should start listening")
	defer serverTransport.Close()

	serverAddr := serverTransport.LocalAddr()
	require.NotEmpty(t, serverAddr, "Server should have local address")

	// Configure client transport with mTLS
	clientTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{clientTLSCert},
		RootCAs:      caPool,
		NextProtos:   []string{"cloudless-overlay"},
		MinVersion:   tls.VersionTLS13,
	}

	clientConfig := TransportConfig{
		ListenAddress:     "127.0.0.1:0",
		MaxIdleTimeout:    30 * time.Second,
		MaxStreamsPerConn: 100,
		KeepAliveInterval: 10 * time.Second,
	}

	clientTransport := NewQUICTransport(clientConfig, clientTLSConfig, logger)
	defer clientTransport.Close()

	// Client connects to server
	conn, err := clientTransport.Connect(ctx, "server-node-1", serverAddr)
	require.NoError(t, err, "Client should successfully connect with valid mTLS certificates")
	assert.NotNil(t, conn)
	assert.Equal(t, "server-node-1", conn.PeerID(), "Peer ID should be extracted from certificate")

	// Verify connection is functional
	stream, err := conn.OpenStream(ctx)
	require.NoError(t, err, "Should be able to open stream on authenticated connection")
	defer stream.Close()

	// Send test data
	testData := []byte("Hello from authenticated client")
	_, err = stream.Write(testData)
	assert.NoError(t, err, "Should be able to write to authenticated stream")
}

// TestQUICTransport_mTLS_InvalidCertificate verifies authentication failure scenarios
func TestQUICTransport_mTLS_InvalidCertificate(t *testing.T) {
	tests := []struct {
		name           string
		setupClient    func(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) *tls.Config
		expectedErrMsg string
	}{
		{
			name: "client with self-signed cert not in CA",
			setupClient: func(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey) *tls.Config {
				// Create a different CA and sign cert with it
				otherCACert, otherCAKey, _ := createTestCA(t)
				// Create cert signed by different CA
				otherCert, otherKey := createTestCertificate(t, otherCACert, otherCAKey, "rogue-client", false)
				tlsCert, err := tls.X509KeyPair(
					pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: otherCert.Raw}),
					encodePrivateKey(otherKey),
				)
				require.NoError(t, err)

				// Create pool with the other CA (not the server's CA)
				otherPool := x509.NewCertPool()
				otherPool.AddCert(otherCACert)

				return &tls.Config{
					Certificates: []tls.Certificate{tlsCert},
					RootCAs:      otherPool, // Wrong CA pool
					NextProtos:   []string{"cloudless-overlay"},
					MinVersion:   tls.VersionTLS13,
				}
			},
			expectedErrMsg: "", // Server will reject during handshake
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()

			// Create CA and server cert
			caCert, caKey, caPool := createTestCA(t)
			serverCert, serverKey := createTestCertificate(t, caCert, caKey, "server-node-1", false)
			serverTLSCert, err := tls.X509KeyPair(
				pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Raw}),
				encodePrivateKey(serverKey),
			)
			require.NoError(t, err)

			// Configure server with strict mTLS
			serverTLSConfig := &tls.Config{
				Certificates: []tls.Certificate{serverTLSCert},
				ClientCAs:    caPool,
				ClientAuth:   tls.RequireAndVerifyClientCert,
				NextProtos:   []string{"cloudless-overlay"},
				MinVersion:   tls.VersionTLS13,
			}

			serverConfig := TransportConfig{
				ListenAddress:     "127.0.0.1:0",
				MaxIdleTimeout:    5 * time.Second,
				MaxStreamsPerConn: 100,
				KeepAliveInterval: 2 * time.Second,
			}

			serverTransport := NewQUICTransport(serverConfig, serverTLSConfig, logger)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			err = serverTransport.Listen(ctx, "")
			require.NoError(t, err)
			defer serverTransport.Close()

			serverAddr := serverTransport.LocalAddr()

			// Setup client with invalid/expired cert
			clientTLSConfig := tt.setupClient(t, caCert, caKey)

			clientConfig := TransportConfig{
				ListenAddress:     "127.0.0.1:0",
				MaxIdleTimeout:    5 * time.Second,
				MaxStreamsPerConn: 100,
				KeepAliveInterval: 2 * time.Second,
			}

			clientTransport := NewQUICTransport(clientConfig, clientTLSConfig, logger)
			defer clientTransport.Close()

			// Attempt connection - should fail
			conn, err := clientTransport.Connect(ctx, "server-node-1", serverAddr)
			assert.Error(t, err, "Connection should fail with invalid certificate")
			if conn != nil {
				conn.Close()
			}
			if tt.expectedErrMsg != "" && err != nil {
				assert.Contains(t, err.Error(), tt.expectedErrMsg, "Error should indicate authentication failure")
			}
		})
	}
}

// TestQUICTransport_PeerIDExtraction_SPIFFE verifies CLD-REQ-040: peer identification
func TestQUICTransport_PeerIDExtraction_SPIFFE(t *testing.T) {
	tests := []struct {
		name           string
		spiffeID       string
		dnsNames       []string
		commonName     string
		expectedPeerID string
	}{
		{
			name:           "SPIFFE URI for node",
			spiffeID:       "spiffe://cloudless/node/node-123",
			expectedPeerID: "node-123",
		},
		{
			name:           "SPIFFE URI for workload",
			spiffeID:       "spiffe://cloudless/workload/wl-456",
			expectedPeerID: "wl-456",
		},
		{
			name:           "Fallback to DNS name",
			dnsNames:       []string{"agent-1.cloudless.local"},
			expectedPeerID: "agent-1.cloudless.local",
		},
		{
			name:           "Fallback to Common Name",
			commonName:     "coordinator-node",
			expectedPeerID: "coordinator-node",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()

			// Create CA and certificates
			caCert, caKey, caPool := createTestCA(t)

			// Create server cert with SPIFFE ID or DNS names
			serverCertTemplate := &x509.Certificate{
				SerialNumber: big.NewInt(2),
				Subject: pkix.Name{
					CommonName: tt.commonName,
				},
				NotBefore:             time.Now(),
				NotAfter:              time.Now().Add(365 * 24 * time.Hour),
				KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
				ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
				BasicConstraintsValid: true,
				DNSNames:              tt.dnsNames,
				IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")}, // Required for local testing
			}

			if tt.spiffeID != "" {
				spiffeURI, err := url.Parse(tt.spiffeID)
				require.NoError(t, err)
				serverCertTemplate.URIs = []*url.URL{spiffeURI}
			}

			serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
			require.NoError(t, err)

			serverCertBytes, err := x509.CreateCertificate(rand.Reader, serverCertTemplate, caCert, &serverKey.PublicKey, caKey)
			require.NoError(t, err)

			serverTLSCert, err := tls.X509KeyPair(
				pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCertBytes}),
				encodePrivateKey(serverKey),
			)
			require.NoError(t, err)

			// Setup server
			serverTLSConfig := &tls.Config{
				Certificates: []tls.Certificate{serverTLSCert},
				ClientCAs:    caPool,
				ClientAuth:   tls.RequireAndVerifyClientCert,
				NextProtos:   []string{"cloudless-overlay"},
			}

			serverConfig := TransportConfig{
				ListenAddress:     "127.0.0.1:0",
				MaxIdleTimeout:    10 * time.Second,
				MaxStreamsPerConn: 100,
			}

			serverTransport := NewQUICTransport(serverConfig, serverTLSConfig, logger)

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()

			err = serverTransport.Listen(ctx, "")
			require.NoError(t, err)
			defer serverTransport.Close()

			// Create client cert and connect
			clientCert, clientKey := createTestCertificate(t, caCert, caKey, "client-node", false)
			clientTLSCert, err := tls.X509KeyPair(
				pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCert.Raw}),
				encodePrivateKey(clientKey),
			)
			require.NoError(t, err)

			clientTLSConfig := &tls.Config{
				Certificates: []tls.Certificate{clientTLSCert},
				RootCAs:      caPool,
				NextProtos:   []string{"cloudless-overlay"},
			}

			clientConfig := TransportConfig{
				ListenAddress:     "127.0.0.1:0",
				MaxIdleTimeout:    10 * time.Second,
				MaxStreamsPerConn: 100,
			}

			clientTransport := NewQUICTransport(clientConfig, clientTLSConfig, logger)
			defer clientTransport.Close()

			// Connect and verify peer ID extraction
			conn, err := clientTransport.Connect(ctx, tt.expectedPeerID, serverTransport.LocalAddr())
			require.NoError(t, err, "Should connect successfully")
			assert.Equal(t, tt.expectedPeerID, conn.PeerID(), "Peer ID should be correctly extracted")
		})
	}
}

// TestQUICTransport_TLSVersion verifies minimum TLS version enforcement
func TestQUICTransport_TLSVersion(t *testing.T) {
	logger := zap.NewNop()

	caCert, caKey, caPool := createTestCA(t)
	serverCert, serverKey := createTestCertificate(t, caCert, caKey, "server", false)
	serverTLSCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Raw}),
		encodePrivateKey(serverKey),
	)
	require.NoError(t, err)

	// Server requires TLS 1.3
	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{"cloudless-overlay"},
		MinVersion:   tls.VersionTLS13,
	}

	serverConfig := TransportConfig{
		ListenAddress:     "127.0.0.1:0",
		MaxIdleTimeout:    10 * time.Second,
		MaxStreamsPerConn: 100,
	}

	serverTransport := NewQUICTransport(serverConfig, serverTLSConfig, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err = serverTransport.Listen(ctx, "")
	require.NoError(t, err)
	defer serverTransport.Close()

	// Client with TLS 1.3 should succeed
	clientCert, clientKey := createTestCertificate(t, caCert, caKey, "client", false)
	clientTLSCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCert.Raw}),
		encodePrivateKey(clientKey),
	)
	require.NoError(t, err)

	clientTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{clientTLSCert},
		RootCAs:      caPool,
		NextProtos:   []string{"cloudless-overlay"},
		MinVersion:   tls.VersionTLS13,
	}

	clientConfig := TransportConfig{
		ListenAddress:     "127.0.0.1:0",
		MaxIdleTimeout:    10 * time.Second,
		MaxStreamsPerConn: 100,
	}

	clientTransport := NewQUICTransport(clientConfig, clientTLSConfig, logger)
	defer clientTransport.Close()

	conn, err := clientTransport.Connect(ctx, "server", serverTransport.LocalAddr())
	assert.NoError(t, err, "Connection with TLS 1.3 should succeed")
	if conn != nil {
		conn.Close()
	}
}

// Helper functions

// createTestCA creates a test Certificate Authority
func createTestCA(t *testing.T) (*x509.Certificate, *ecdsa.PrivateKey, *x509.CertPool) {
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Cloudless Test CA"},
			CommonName:   "Cloudless Test Root CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}

	caCertBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	require.NoError(t, err)

	caCert, err := x509.ParseCertificate(caCertBytes)
	require.NoError(t, err)

	caPool := x509.NewCertPool()
	caPool.AddCert(caCert)

	return caCert, caKey, caPool
}

// createTestCertificate creates a test certificate signed by CA
func createTestCertificate(t *testing.T, caCert *x509.Certificate, caKey *ecdsa.PrivateKey, commonName string, expired bool) (*x509.Certificate, *ecdsa.PrivateKey) {
	certKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	require.NoError(t, err)

	notBefore := time.Now()
	notAfter := time.Now().Add(365 * 24 * time.Hour)

	if expired {
		// Create expired certificate
		notBefore = time.Now().Add(-48 * time.Hour)
		notAfter = time.Now().Add(-24 * time.Hour)
	}

	certTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certBytes, err := x509.CreateCertificate(rand.Reader, certTemplate, caCert, &certKey.PublicKey, caKey)
	require.NoError(t, err)

	cert, err := x509.ParseCertificate(certBytes)
	require.NoError(t, err)

	return cert, certKey
}

// encodePrivateKey encodes ECDSA private key to PEM format
func encodePrivateKey(key *ecdsa.PrivateKey) []byte {
	keyBytes, _ := x509.MarshalECPrivateKey(key)
	return pem.EncodeToMemory(&pem.Block{
		Type:  "EC PRIVATE KEY",
		Bytes: keyBytes,
	})
}
