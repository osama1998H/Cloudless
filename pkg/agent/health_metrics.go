package agent

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// CLD-REQ-032: Prometheus metrics for health probe monitoring
//
// These metrics provide observability into health probe execution,
// container restarts, and replica health status.
//
// Metrics follow Prometheus naming conventions (per GO_ENGINEERING_SOP.md §9.3):
// - Namespace: cloudless
// - Subsystem: agent
// - Counter suffix: _total
// - Histogram suffix: _seconds

var (
	// Liveness probe execution metrics
	livenessProbesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudless",
			Subsystem: "agent",
			Name:      "liveness_probes_total",
			Help:      "Total number of liveness probe executions",
		},
		[]string{"result"}, // success, failure
	)

	// Readiness probe execution metrics
	readinessProbesTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudless",
			Subsystem: "agent",
			Name:      "readiness_probes_total",
			Help:      "Total number of readiness probe executions",
		},
		[]string{"result"}, // success, failure
	)

	// Container restart metrics
	containerRestartsTotal = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Namespace: "cloudless",
			Subsystem: "agent",
			Name:      "container_restarts_total",
			Help:      "Total number of container restarts due to health probe failures",
		},
		[]string{"reason"}, // liveness_failure, manual, policy
	)

	// Probe execution duration
	probeDurationSeconds = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Namespace: "cloudless",
			Subsystem: "agent",
			Name:      "probe_duration_seconds",
			Help:      "Duration of health probe execution",
			Buckets:   prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~1s
		},
		[]string{"probe_type", "result"}, // probe_type: liveness|readiness, result: success|failure
	)

	// Current health status gauge
	containerHealthStatus = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "cloudless",
			Subsystem: "agent",
			Name:      "container_health_status",
			Help:      "Current health status of containers (1=healthy, 0=unhealthy)",
		},
		[]string{"container_id", "probe_type"}, // probe_type: liveness|readiness
	)

	// Consecutive failures gauge
	consecutiveFailuresGauge = promauto.NewGaugeVec(
		prometheus.GaugeOpts{
			Namespace: "cloudless",
			Subsystem: "agent",
			Name:      "probe_consecutive_failures",
			Help:      "Number of consecutive probe failures",
		},
		[]string{"container_id", "probe_type"}, // probe_type: liveness|readiness
	)

	// Restart rate limiting metrics
	restartRateLimitHits = promauto.NewCounter(
		prometheus.CounterOpts{
			Namespace: "cloudless",
			Subsystem: "agent",
			Name:      "restart_rate_limit_hits_total",
			Help:      "Total number of times restart rate limit was hit",
		},
	)
)

// RecordLivenessProbe records a liveness probe execution
func RecordLivenessProbe(success bool, duration float64) {
	result := "success"
	if !success {
		result = "failure"
	}
	livenessProbesTotal.WithLabelValues(result).Inc()
	probeDurationSeconds.WithLabelValues("liveness", result).Observe(duration)
}

// RecordReadinessProbe records a readiness probe execution
func RecordReadinessProbe(success bool, duration float64) {
	result := "success"
	if !success {
		result = "failure"
	}
	readinessProbesTotal.WithLabelValues(result).Inc()
	probeDurationSeconds.WithLabelValues("readiness", result).Observe(duration)
}

// RecordContainerRestart records a container restart
func RecordContainerRestart(reason string) {
	containerRestartsTotal.WithLabelValues(reason).Inc()
}

// RecordRestartRateLimitHit records when restart rate limit is exceeded
func RecordRestartRateLimitHit() {
	restartRateLimitHits.Inc()
}

// UpdateContainerHealthStatus updates the current health status gauge
func UpdateContainerHealthStatus(containerID, probeType string, healthy bool) {
	value := 0.0
	if healthy {
		value = 1.0
	}
	containerHealthStatus.WithLabelValues(containerID, probeType).Set(value)
}

// UpdateConsecutiveFailures updates the consecutive failures gauge
func UpdateConsecutiveFailures(containerID, probeType string, failures int) {
	consecutiveFailuresGauge.WithLabelValues(containerID, probeType).Set(float64(failures))
}
