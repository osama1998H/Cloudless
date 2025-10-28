package observability

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

// TestNewMetricsServer tests metrics server creation
func TestNewMetricsServer(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name string
		addr string
	}{
		{
			name: "Standard port",
			addr: "localhost:9090",
		},
		{
			name: "Custom port",
			addr: "localhost:19090",
		},
		{
			name: "All interfaces",
			addr: "0.0.0.0:9091",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := NewMetricsServer(tt.addr, logger)

			if server == nil {
				t.Fatal("NewMetricsServer() returned nil")
			}
			if server.addr != tt.addr {
				t.Errorf("Expected addr %s, got %s", tt.addr, server.addr)
			}
			if server.logger == nil {
				t.Error("Expected non-nil logger")
			}
			if server.server == nil {
				t.Error("Expected non-nil HTTP server")
			}
		})
	}
}

// TestMetricsServer_Start tests server startup
func TestMetricsServer_Start(t *testing.T) {
	logger := zap.NewNop()

	// Use a unique port for this test
	addr := "localhost:19091"
	server := NewMetricsServer(addr, logger)

	// Start server
	err := server.Start()
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Verify server is responding
	resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatalf("Failed to connect to metrics server: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Stop server
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()

	if err := server.Stop(ctx); err != nil {
		t.Errorf("Stop() error = %v", err)
	}
}

// TestMetricsServer_Stop tests graceful shutdown
func TestMetricsServer_Stop(t *testing.T) {
	logger := zap.NewNop()

	tests := []struct {
		name    string
		timeout time.Duration
	}{
		{
			name:    "Normal shutdown",
			timeout: 5 * time.Second,
		},
		{
			name:    "Short timeout",
			timeout: 100 * time.Millisecond,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use unique port for each test
			addr := fmt.Sprintf("localhost:190%d", 92+len(tt.name)%10)
			server := NewMetricsServer(addr, logger)

			// Start server
			if err := server.Start(); err != nil {
				t.Fatalf("Start() error = %v", err)
			}

			// Give server time to start
			time.Sleep(50 * time.Millisecond)

			// Stop server with timeout
			ctx, cancel := context.WithTimeout(context.Background(), tt.timeout)
			defer cancel()

			err := server.Stop(ctx)
			if err != nil && ctx.Err() == context.DeadlineExceeded {
				// Timeout is acceptable for short timeout test
				t.Logf("Stop() timeout (expected for short timeout): %v", err)
			} else if err != nil {
				t.Errorf("Stop() error = %v", err)
			}
		})
	}
}

// TestMetricsEndpoint tests /metrics handler
func TestMetricsEndpoint(t *testing.T) {
	logger := zap.NewNop()
	addr := "localhost:19093"
	server := NewMetricsServer(addr, logger)

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		server.Stop(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Request metrics endpoint
	resp, err := http.Get(fmt.Sprintf("http://%s/metrics", addr))
	if err != nil {
		t.Fatalf("Failed to get /metrics: %v", err)
	}
	defer resp.Body.Close()

	// Verify status code
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Verify content type
	contentType := resp.Header.Get("Content-Type")
	if !strings.Contains(contentType, "text/plain") {
		// Prometheus metrics can be text/plain or application/openmetrics-text
		if !strings.Contains(contentType, "openmetrics") {
			t.Errorf("Expected text/plain or openmetrics content type, got %s", contentType)
		}
	}

	// Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	bodyStr := string(body)
	if len(bodyStr) == 0 {
		t.Error("Expected non-empty metrics response")
	}

	// Verify metrics format (basic check for Prometheus format)
	// Prometheus metrics should contain HELP and TYPE comments
	if !strings.Contains(bodyStr, "# HELP") && !strings.Contains(bodyStr, "# TYPE") {
		t.Logf("Warning: Response doesn't contain standard Prometheus metric format")
	}
}

// TestHealthEndpoint tests /health handler
func TestHealthEndpoint(t *testing.T) {
	logger := zap.NewNop()
	addr := "localhost:19094"
	server := NewMetricsServer(addr, logger)

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		server.Stop(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Request health endpoint
	resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
	if err != nil {
		t.Fatalf("Failed to get /health: %v", err)
	}
	defer resp.Body.Close()

	// Verify status code
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	// Verify response
	if string(body) != "OK" {
		t.Errorf("Expected body 'OK', got '%s'", string(body))
	}
}

// TestReadyEndpoint tests /ready handler
func TestReadyEndpoint(t *testing.T) {
	logger := zap.NewNop()
	addr := "localhost:19095"
	server := NewMetricsServer(addr, logger)

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		server.Stop(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Request ready endpoint
	resp, err := http.Get(fmt.Sprintf("http://%s/ready", addr))
	if err != nil {
		t.Fatalf("Failed to get /ready: %v", err)
	}
	defer resp.Body.Close()

	// Verify status code
	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status 200, got %d", resp.StatusCode)
	}

	// Read body
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("Failed to read response body: %v", err)
	}

	// Verify response
	if string(body) != "READY" {
		t.Errorf("Expected body 'READY', got '%s'", string(body))
	}
}

// TestMetricsServer_MultipleRequests tests server handles multiple concurrent requests
func TestMetricsServer_MultipleRequests(t *testing.T) {
	logger := zap.NewNop()
	addr := "localhost:19096"
	server := NewMetricsServer(addr, logger)

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		server.Stop(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Make multiple concurrent requests
	const numRequests = 10
	errors := make(chan error, numRequests)

	for i := 0; i < numRequests; i++ {
		go func(id int) {
			endpoint := "/health"
			if id%2 == 0 {
				endpoint = "/metrics"
			}

			resp, err := http.Get(fmt.Sprintf("http://%s%s", addr, endpoint))
			if err != nil {
				errors <- fmt.Errorf("request %d failed: %w", id, err)
				return
			}
			defer resp.Body.Close()

			if resp.StatusCode != http.StatusOK {
				errors <- fmt.Errorf("request %d got status %d", id, resp.StatusCode)
				return
			}

			errors <- nil
		}(i)
	}

	// Collect results
	for i := 0; i < numRequests; i++ {
		err := <-errors
		if err != nil {
			t.Errorf("Concurrent request error: %v", err)
		}
	}
}

// TestMetricsServer_StartStop_Multiple tests starting and stopping server multiple times
func TestMetricsServer_StartStop_Multiple(t *testing.T) {
	logger := zap.NewNop()
	addr := "localhost:19097"

	// Create server once
	server := NewMetricsServer(addr, logger)

	// Start and stop multiple times
	for i := 0; i < 3; i++ {
		t.Logf("Iteration %d", i+1)

		// Start
		if err := server.Start(); err != nil {
			t.Fatalf("Start() iteration %d error = %v", i+1, err)
		}

		// Give server time to start
		time.Sleep(50 * time.Millisecond)

		// Verify it's working
		resp, err := http.Get(fmt.Sprintf("http://%s/health", addr))
		if err != nil {
			t.Fatalf("Health check iteration %d failed: %v", i+1, err)
		}
		resp.Body.Close()

		// Stop
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		if err := server.Stop(ctx); err != nil {
			cancel()
			t.Fatalf("Stop() iteration %d error = %v", i+1, err)
		}
		cancel()

		// Wait for server to fully stop
		time.Sleep(100 * time.Millisecond)

		// Recreate server for next iteration
		server = NewMetricsServer(addr, logger)
	}
}

// TestMetricsServer_404 tests handling of unknown endpoints
func TestMetricsServer_404(t *testing.T) {
	logger := zap.NewNop()
	addr := "localhost:19098"
	server := NewMetricsServer(addr, logger)

	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		server.Stop(ctx)
	}()

	// Give server time to start
	time.Sleep(100 * time.Millisecond)

	// Request unknown endpoint
	resp, err := http.Get(fmt.Sprintf("http://%s/unknown", addr))
	if err != nil {
		t.Fatalf("Failed to get /unknown: %v", err)
	}
	defer resp.Body.Close()

	// Verify 404 status code
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("Expected status 404, got %d", resp.StatusCode)
	}
}
