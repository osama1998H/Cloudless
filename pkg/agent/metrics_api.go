package agent

import (
	"encoding/json"
	"net/http"
	"time"

	"go.uber.org/zap"
)

// MetricsAPI provides HTTP endpoints for querying metrics
type MetricsAPI struct {
	agent  *Agent
	logger *zap.Logger
}

// NewMetricsAPI creates a new metrics API handler
func NewMetricsAPI(agent *Agent, logger *zap.Logger) *MetricsAPI {
	return &MetricsAPI{
		agent:  agent,
		logger: logger,
	}
}

// ServeHTTP implements http.Handler
func (m *MetricsAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch r.URL.Path {
	case "/metrics/snapshot":
		m.handleSnapshot(w, r)
	case "/metrics/cpu":
		m.handleMetric(w, r, MetricTypeCPU)
	case "/metrics/memory":
		m.handleMetric(w, r, MetricTypeMemory)
	case "/metrics/storage":
		m.handleMetric(w, r, MetricTypeStorage)
	case "/metrics/bandwidth":
		m.handleMetric(w, r, MetricTypeBandwidth)
	case "/metrics/gpu":
		m.handleMetric(w, r, MetricTypeGPU)
	default:
		http.NotFound(w, r)
	}
}

// handleSnapshot returns a snapshot of current metrics
func (m *MetricsAPI) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	snapshot := m.agent.metricsStorage.GetMetricsSnapshot()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(snapshot); err != nil {
		m.logger.Error("Failed to encode snapshot", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// handleMetric returns time-series data for a specific metric
func (m *MetricsAPI) handleMetric(w http.ResponseWriter, r *http.Request, metricType MetricType) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	// Parse query parameters
	query := r.URL.Query()

	// Default to last hour
	end := time.Now()
	start := end.Add(-1 * time.Hour)

	// Override with query params if provided
	if startParam := query.Get("start"); startParam != "" {
		if t, err := time.Parse(time.RFC3339, startParam); err == nil {
			start = t
		}
	}

	if endParam := query.Get("end"); endParam != "" {
		if t, err := time.Parse(time.RFC3339, endParam); err == nil {
			end = t
		}
	}

	// Get metrics
	metrics := m.agent.metricsStorage.GetMetrics(metricType, start, end)

	// Calculate average if requested
	includeAverage := query.Get("average") == "true"

	response := map[string]interface{}{
		"type":   metricType,
		"start":  start,
		"end":    end,
		"count":  len(metrics),
		"points": metrics,
	}

	if includeAverage {
		avg := m.agent.metricsStorage.GetAverageMetric(metricType, start, end)
		response["average"] = avg
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		m.logger.Error("Failed to encode metrics", zap.Error(err))
		http.Error(w, "Internal server error", http.StatusInternalServerError)
	}
}

// RegisterMetricsEndpoints registers metrics endpoints with an HTTP mux
func RegisterMetricsEndpoints(mux *http.ServeMux, agent *Agent, logger *zap.Logger) {
	api := NewMetricsAPI(agent, logger)
	mux.Handle("/metrics/", api)
}
