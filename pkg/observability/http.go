package observability

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

// MetricsServer serves Prometheus metrics over HTTP
type MetricsServer struct {
	addr   string
	logger *zap.Logger
	server *http.Server
}

// NewMetricsServer creates a new metrics server
func NewMetricsServer(addr string, logger *zap.Logger) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", healthHandler)
	mux.HandleFunc("/ready", readyHandler)

	return &MetricsServer{
		addr:   addr,
		logger: logger,
		server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
}

// Start starts the metrics server
func (ms *MetricsServer) Start() error {
	ms.logger.Info("Starting metrics server",
		zap.String("address", ms.addr),
	)

	// Start server in a goroutine
	go func() {
		if err := ms.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			ms.logger.Error("Metrics server error",
				zap.Error(err),
			)
		}
	}()

	return nil
}

// Stop stops the metrics server gracefully
func (ms *MetricsServer) Stop(ctx context.Context) error {
	ms.logger.Info("Stopping metrics server")

	shutdownCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	if err := ms.server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("failed to shutdown metrics server: %w", err)
	}

	return nil
}

// healthHandler handles health check requests
func healthHandler(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// readyHandler handles readiness check requests
func readyHandler(w http.ResponseWriter, r *http.Request) {
	// In production, would check if all subsystems are ready
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("READY"))
}
