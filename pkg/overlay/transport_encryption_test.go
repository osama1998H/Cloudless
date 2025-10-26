package overlay

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/tls"
	"encoding/pem"
	"fmt"
	"io"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"
)

// TestQUICTransport_EncryptedStreamCommunication verifies CLD-REQ-040: encrypted communication
func TestQUICTransport_EncryptedStreamCommunication(t *testing.T) {
	logger := zap.NewNop()

	// Setup mTLS certificates
	caCert, caKey, caPool := createTestCA(t)
	serverCert, serverKey := createTestCertificate(t, caCert, caKey, "server", false)
	serverTLSCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Raw}),
		encodePrivateKey(serverKey),
	)
	require.NoError(t, err)

	clientCert, clientKey := createTestCertificate(t, caCert, caKey, "client", false)
	clientTLSCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCert.Raw}),
		encodePrivateKey(clientKey),
	)
	require.NoError(t, err)

	// Configure server
	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{"cloudless-overlay"},
		MinVersion:   tls.VersionTLS13,
	}

	serverConfig := TransportConfig{
		ListenAddress:     "127.0.0.1:0",
		MaxIdleTimeout:    30 * time.Second,
		MaxStreamsPerConn: 100,
		KeepAliveInterval: 10 * time.Second,
	}

	serverTransport := NewQUICTransport(serverConfig, serverTLSConfig, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = serverTransport.Listen(ctx, "")
	require.NoError(t, err)
	defer serverTransport.Close()

	// Server will accept connections via background acceptLoop
	// We need to manually handle incoming streams since there's no application-level handler
	serverReady := make(chan struct{})
	streamReady := make(chan Stream, 1)

	go func() {
		// Wait for client to connect (acceptLoop stores it in connections map)
		time.Sleep(100 * time.Millisecond)
		close(serverReady)

		// The connection is already accepted by the background acceptLoop
		// We need to get it from the connections map by iterating
		var serverConn Connection
		serverTransport.connections.Range(func(key, value interface{}) bool {
			serverConn = value.(Connection)
			return false // stop after first
		})

		if serverConn == nil {
			t.Logf("No server connection found")
			return
		}

		stream, err := serverConn.AcceptStream(ctx)
		if err != nil {
			t.Logf("Server accept stream error: %v", err)
			return
		}
		streamReady <- stream

		// Echo received data back
		buf := make([]byte, 4096)
		n, err := stream.Read(buf)
		if err != nil {
			t.Logf("Server read error: %v", err)
			stream.Close()
			return
		}

		_, err = stream.Write(buf[:n])
		if err != nil {
			t.Logf("Server write error: %v", err)
		}
		stream.Close()
	}()

	// Configure client
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

	// Connect to server
	conn, err := clientTransport.Connect(ctx, "server", serverTransport.LocalAddr())
	require.NoError(t, err)
	defer conn.Close()

	// Open encrypted stream
	stream, err := conn.OpenStream(ctx)
	require.NoError(t, err)
	defer stream.Close()

	// Test data (should be encrypted over the wire)
	testData := []byte("This is sensitive data that must be encrypted over QUIC")

	// Write data
	_, err = stream.Write(testData)
	assert.NoError(t, err, "Should write encrypted data successfully")

	// Read echo
	buf := make([]byte, len(testData))
	n, err := io.ReadFull(stream, buf)
	assert.NoError(t, err, "Should read encrypted data successfully")
	assert.Equal(t, len(testData), n, "Should receive all data")
	assert.Equal(t, testData, buf, "Data should be intact after encryption/decryption")

	// Verify connection state shows encryption
	if qconn, ok := conn.(*QUICConnection); ok {
		connState := qconn.conn.ConnectionState()
		assert.True(t, connState.TLS.HandshakeComplete, "TLS handshake should be complete")
		assert.Equal(t, uint16(tls.VersionTLS13), connState.TLS.Version, "Should use TLS 1.3")
		assert.NotEmpty(t, connState.TLS.CipherSuite, "Should have cipher suite")
	}

	// Wait for server stream to be ready and closed
	select {
	case s := <-streamReady:
		// Stream was accepted, wait a bit for echo to complete
		time.Sleep(100 * time.Millisecond)
		s.Close()
	case <-time.After(5 * time.Second):
		t.Error("Timeout waiting for server stream")
	}
}

// TestQUICTransport_DataIntegrity verifies data integrity over encrypted channel
func TestQUICTransport_DataIntegrity(t *testing.T) {
	tests := []struct {
		name     string
		dataSize int
	}{
		{"small payload", 100},
		{"medium payload", 1024},
		{"large payload", 64 * 1024},
		{"very large payload", 512 * 1024},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger := zap.NewNop()

			// Setup
			caCert, caKey, caPool := createTestCA(t)
			serverCert, serverKey := createTestCertificate(t, caCert, caKey, "server", false)
			serverTLSCert, err := tls.X509KeyPair(
				pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Raw}),
				encodePrivateKey(serverKey),
			)
			require.NoError(t, err)

			serverTLSConfig := &tls.Config{
				Certificates: []tls.Certificate{serverTLSCert},
				ClientCAs:    caPool,
				ClientAuth:   tls.RequireAndVerifyClientCert,
				NextProtos:   []string{"cloudless-overlay"},
				MinVersion:   tls.VersionTLS13,
			}

			serverConfig := TransportConfig{
				ListenAddress:     "127.0.0.1:0",
				MaxIdleTimeout:    30 * time.Second,
				MaxStreamsPerConn: 100,
			}

			serverTransport := NewQUICTransport(serverConfig, serverTLSConfig, logger)

			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()

			err = serverTransport.Listen(ctx, "")
			require.NoError(t, err)
			defer serverTransport.Close()

			// Server echo handler - use background acceptLoop pattern
			go func() {
				// Wait for connection to be established
				time.Sleep(100 * time.Millisecond)

				var serverConn Connection
				serverTransport.connections.Range(func(key, value interface{}) bool {
					serverConn = value.(Connection)
					return false
				})

				if serverConn == nil {
					return
				}

				stream, err := serverConn.AcceptStream(ctx)
				if err != nil {
					return
				}
				defer stream.Close()

				// Echo all data - read and write back
				buf := make([]byte, 1024*1024) // 1MB buffer for large payloads
				for {
					n, err := stream.Read(buf)
					if err != nil {
						if err == io.EOF {
							break
						}
						return
					}
					if n > 0 {
						_, err = stream.Write(buf[:n])
						if err != nil {
							return
						}
					}
				}
			}()

			// Client setup
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
				MaxIdleTimeout:    30 * time.Second,
				MaxStreamsPerConn: 100,
			}

			clientTransport := NewQUICTransport(clientConfig, clientTLSConfig, logger)
			defer clientTransport.Close()

			conn, err := clientTransport.Connect(ctx, "server", serverTransport.LocalAddr())
			require.NoError(t, err)
			defer conn.Close()

			stream, err := conn.OpenStream(ctx)
			require.NoError(t, err)
			defer stream.Close()

			// Generate random test data
			testData := make([]byte, tt.dataSize)
			_, err = rand.Read(testData)
			require.NoError(t, err)

			// Write data
			n, err := stream.Write(testData)
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)

			// Read back and verify integrity
			received := make([]byte, len(testData))
			n, err = io.ReadFull(stream, received)
			assert.NoError(t, err)
			assert.Equal(t, len(testData), n)
			assert.True(t, bytes.Equal(testData, received), "Data integrity must be preserved")
		})
	}
}

// TestQUICTransport_EncryptedDatagrams verifies datagram encryption
func TestQUICTransport_EncryptedDatagrams(t *testing.T) {
	logger := zap.NewNop()

	// Setup certificates
	caCert, caKey, caPool := createTestCA(t)
	serverCert, serverKey := createTestCertificate(t, caCert, caKey, "server", false)
	serverTLSCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Raw}),
		encodePrivateKey(serverKey),
	)
	require.NoError(t, err)

	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{"cloudless-overlay"},
		MinVersion:   tls.VersionTLS13,
	}

	serverConfig := TransportConfig{
		ListenAddress:     "127.0.0.1:0",
		MaxIdleTimeout:    30 * time.Second,
		MaxStreamsPerConn: 100,
	}

	serverTransport := NewQUICTransport(serverConfig, serverTLSConfig, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = serverTransport.Listen(ctx, "")
	require.NoError(t, err)
	defer serverTransport.Close()

	// Server datagram receiver - use background acceptLoop pattern
	receivedDatagram := make(chan []byte, 1)
	go func() {
		// Wait for connection to be established
		time.Sleep(100 * time.Millisecond)

		var serverConn Connection
		serverTransport.connections.Range(func(key, value interface{}) bool {
			serverConn = value.(Connection)
			return false
		})

		if serverConn == nil {
			return
		}

		if qconn, ok := serverConn.(*QUICConnection); ok {
			data, err := qconn.ReceiveDatagram(ctx)
			if err == nil {
				receivedDatagram <- data
			}
		}
	}()

	// Client setup
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
		MaxIdleTimeout:    30 * time.Second,
		MaxStreamsPerConn: 100,
	}

	clientTransport := NewQUICTransport(clientConfig, clientTLSConfig, logger)
	defer clientTransport.Close()

	conn, err := clientTransport.Connect(ctx, "server", serverTransport.LocalAddr())
	require.NoError(t, err)
	defer conn.Close()

	// Send encrypted datagram
	testData := []byte("Encrypted datagram payload")
	if qconn, ok := conn.(*QUICConnection); ok {
		err = qconn.SendDatagram(testData)
		assert.NoError(t, err, "Should send encrypted datagram")

		// Verify received datagram
		select {
		case received := <-receivedDatagram:
			assert.Equal(t, testData, received, "Datagram data should be intact")
		case <-time.After(5 * time.Second):
			t.Error("Timeout waiting for datagram")
		}
	}
}

// TestQUICTransport_ConcurrentEncryptedStreams verifies concurrent encrypted streams
func TestQUICTransport_ConcurrentEncryptedStreams(t *testing.T) {
	logger := zap.NewNop()

	// Setup
	caCert, caKey, caPool := createTestCA(t)
	serverCert, serverKey := createTestCertificate(t, caCert, caKey, "server", false)
	serverTLSCert, err := tls.X509KeyPair(
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCert.Raw}),
		encodePrivateKey(serverKey),
	)
	require.NoError(t, err)

	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverTLSCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
		NextProtos:   []string{"cloudless-overlay"},
		MinVersion:   tls.VersionTLS13,
	}

	serverConfig := TransportConfig{
		ListenAddress:     "127.0.0.1:0",
		MaxIdleTimeout:    30 * time.Second,
		MaxStreamsPerConn: 100,
	}

	serverTransport := NewQUICTransport(serverConfig, serverTLSConfig, logger)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	err = serverTransport.Listen(ctx, "")
	require.NoError(t, err)
	defer serverTransport.Close()

	// Server handles multiple streams - use background acceptLoop pattern
	go func() {
		// Wait for connection to be established
		time.Sleep(100 * time.Millisecond)

		var serverConn Connection
		serverTransport.connections.Range(func(key, value interface{}) bool {
			serverConn = value.(Connection)
			return false
		})

		if serverConn == nil {
			return
		}

		for {
			stream, err := serverConn.AcceptStream(ctx)
			if err != nil {
				return
			}

			go func(s Stream) {
				defer s.Close()
				// Echo data properly
				buf := make([]byte, 4096)
				for {
					n, err := s.Read(buf)
					if err != nil {
						return
					}
					if n > 0 {
						_, err = s.Write(buf[:n])
						if err != nil {
							return
						}
					}
				}
			}(stream)
		}
	}()

	// Client setup
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
		MaxIdleTimeout:    30 * time.Second,
		MaxStreamsPerConn: 100,
	}

	clientTransport := NewQUICTransport(clientConfig, clientTLSConfig, logger)
	defer clientTransport.Close()

	conn, err := clientTransport.Connect(ctx, "server", serverTransport.LocalAddr())
	require.NoError(t, err)
	defer conn.Close()

	// Open multiple concurrent streams
	const numStreams = 10
	errors := make(chan error, numStreams)

	for i := 0; i < numStreams; i++ {
		go func(streamID int) {
			stream, err := conn.OpenStream(ctx)
			if err != nil {
				errors <- err
				return
			}
			defer stream.Close()

			testData := []byte(fmt.Sprintf("Stream %d data", streamID))
			_, err = stream.Write(testData)
			if err != nil {
				errors <- err
				return
			}

			buf := make([]byte, len(testData))
			_, err = io.ReadFull(stream, buf)
			if err != nil {
				errors <- err
				return
			}

			if !bytes.Equal(testData, buf) {
				errors <- fmt.Errorf("stream %d: data mismatch", streamID)
				return
			}

			errors <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < numStreams; i++ {
		select {
		case err := <-errors:
			assert.NoError(t, err, "All concurrent streams should succeed")
		case <-time.After(30 * time.Second):
			t.Fatalf("Timeout waiting for stream %d", i)
		}
	}
}
