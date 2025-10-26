package agent

import (
	"context"
	"sync"
	"time"

	"go.uber.org/zap"
)

// MetricType represents the type of metric being stored
type MetricType string

const (
	MetricTypeCPU       MetricType = "cpu"
	MetricTypeMemory    MetricType = "memory"
	MetricTypeStorage   MetricType = "storage"
	MetricTypeBandwidth MetricType = "bandwidth"
	MetricTypeGPU       MetricType = "gpu"
)

// MetricDataPoint represents a single metric measurement
type MetricDataPoint struct {
	Timestamp time.Time
	Value     float64
	Labels    map[string]string
}

// MetricThreshold defines alert thresholds for metrics
type MetricThreshold struct {
	WarningPercent  float64 // Percentage threshold for warning
	CriticalPercent float64 // Percentage threshold for critical
	Limit           float64 // Maximum limit for the metric
}

// MetricsAlert represents an alert triggered by threshold breach
type MetricsAlert struct {
	Type      MetricType
	Level     string // "warning" or "critical"
	Threshold float64
	Current   float64
	Message   string
	Timestamp time.Time
}

// MetricsStorage stores time-series metrics data
type MetricsStorage struct {
	mu sync.RWMutex

	// Time-series storage for each metric type
	cpuMetrics       []MetricDataPoint
	memoryMetrics    []MetricDataPoint
	storageMetrics   []MetricDataPoint
	bandwidthMetrics []MetricDataPoint
	gpuMetrics       []MetricDataPoint

	// Thresholds for alerting
	thresholds map[MetricType]MetricThreshold

	// Alert handlers
	alertHandlers []AlertHandler

	// Configuration
	retentionDuration time.Duration // How long to keep metrics
	maxDataPoints     int           // Maximum data points per metric type

	logger *zap.Logger
}

// AlertHandler is a function that handles metric alerts
type AlertHandler func(alert MetricsAlert)

// MetricsStorageConfig contains configuration for metrics storage
type MetricsStorageConfig struct {
	// How long to retain metrics data
	RetentionDuration time.Duration

	// Maximum number of data points to keep per metric type
	MaxDataPoints int

	// Thresholds for each metric type
	Thresholds map[MetricType]MetricThreshold
}

// NewMetricsStorage creates a new metrics storage instance
func NewMetricsStorage(config MetricsStorageConfig, logger *zap.Logger) *MetricsStorage {
	if config.RetentionDuration == 0 {
		config.RetentionDuration = 24 * time.Hour // Default 24 hours
	}

	if config.MaxDataPoints == 0 {
		config.MaxDataPoints = 1000 // Default 1000 data points
	}

	if config.Thresholds == nil {
		// Set default thresholds
		config.Thresholds = map[MetricType]MetricThreshold{
			MetricTypeCPU: {
				WarningPercent:  70.0,
				CriticalPercent: 90.0,
			},
			MetricTypeMemory: {
				WarningPercent:  80.0,
				CriticalPercent: 95.0,
			},
			MetricTypeStorage: {
				WarningPercent:  85.0,
				CriticalPercent: 95.0,
			},
			MetricTypeBandwidth: {
				WarningPercent:  75.0,
				CriticalPercent: 90.0,
			},
			MetricTypeGPU: {
				WarningPercent:  80.0,
				CriticalPercent: 95.0,
			},
		}
	}

	ms := &MetricsStorage{
		cpuMetrics:        make([]MetricDataPoint, 0, config.MaxDataPoints),
		memoryMetrics:     make([]MetricDataPoint, 0, config.MaxDataPoints),
		storageMetrics:    make([]MetricDataPoint, 0, config.MaxDataPoints),
		bandwidthMetrics:  make([]MetricDataPoint, 0, config.MaxDataPoints),
		gpuMetrics:        make([]MetricDataPoint, 0, config.MaxDataPoints),
		thresholds:        config.Thresholds,
		alertHandlers:     make([]AlertHandler, 0),
		retentionDuration: config.RetentionDuration,
		maxDataPoints:     config.MaxDataPoints,
		logger:            logger,
	}

	return ms
}

// RecordMetric records a new metric data point
func (ms *MetricsStorage) RecordMetric(metricType MetricType, value float64, labels map[string]string) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	dataPoint := MetricDataPoint{
		Timestamp: time.Now(),
		Value:     value,
		Labels:    labels,
	}

	// Add to appropriate metric slice
	switch metricType {
	case MetricTypeCPU:
		ms.cpuMetrics = ms.addAndTrim(ms.cpuMetrics, dataPoint)
	case MetricTypeMemory:
		ms.memoryMetrics = ms.addAndTrim(ms.memoryMetrics, dataPoint)
	case MetricTypeStorage:
		ms.storageMetrics = ms.addAndTrim(ms.storageMetrics, dataPoint)
	case MetricTypeBandwidth:
		ms.bandwidthMetrics = ms.addAndTrim(ms.bandwidthMetrics, dataPoint)
	case MetricTypeGPU:
		ms.gpuMetrics = ms.addAndTrim(ms.gpuMetrics, dataPoint)
	}

	// Check thresholds and trigger alerts if necessary
	ms.checkThresholds(metricType, value)
}

// addAndTrim adds a data point and trims the slice if it exceeds max data points
func (ms *MetricsStorage) addAndTrim(metrics []MetricDataPoint, dataPoint MetricDataPoint) []MetricDataPoint {
	metrics = append(metrics, dataPoint)

	// Trim old data points if we exceed the limit
	if len(metrics) > ms.maxDataPoints {
		// Remove oldest data points
		metrics = metrics[len(metrics)-ms.maxDataPoints:]
	}

	return metrics
}

// checkThresholds checks if the metric value breaches any thresholds
func (ms *MetricsStorage) checkThresholds(metricType MetricType, value float64) {
	threshold, ok := ms.thresholds[metricType]
	if !ok {
		return
	}

	// Skip if no limit is set
	if threshold.Limit == 0 {
		return
	}

	percentage := (value / threshold.Limit) * 100

	var alert *MetricsAlert

	if percentage >= threshold.CriticalPercent {
		alert = &MetricsAlert{
			Type:      metricType,
			Level:     "critical",
			Threshold: threshold.CriticalPercent,
			Current:   percentage,
			Message:   "Critical threshold breached",
			Timestamp: time.Now(),
		}
	} else if percentage >= threshold.WarningPercent {
		alert = &MetricsAlert{
			Type:      metricType,
			Level:     "warning",
			Threshold: threshold.WarningPercent,
			Current:   percentage,
			Message:   "Warning threshold breached",
			Timestamp: time.Now(),
		}
	}

	if alert != nil {
		ms.logger.Warn("Metric threshold breached",
			zap.String("type", string(metricType)),
			zap.String("level", alert.Level),
			zap.Float64("threshold", alert.Threshold),
			zap.Float64("current", alert.Current),
		)

		// Trigger alert handlers
		for _, handler := range ms.alertHandlers {
			go handler(*alert)
		}
	}
}

// GetMetrics retrieves metrics for a specific type within a time range
func (ms *MetricsStorage) GetMetrics(metricType MetricType, start, end time.Time) []MetricDataPoint {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var metrics []MetricDataPoint

	switch metricType {
	case MetricTypeCPU:
		metrics = ms.cpuMetrics
	case MetricTypeMemory:
		metrics = ms.memoryMetrics
	case MetricTypeStorage:
		metrics = ms.storageMetrics
	case MetricTypeBandwidth:
		metrics = ms.bandwidthMetrics
	case MetricTypeGPU:
		metrics = ms.gpuMetrics
	default:
		return []MetricDataPoint{}
	}

	// Filter by time range
	result := make([]MetricDataPoint, 0)
	for _, m := range metrics {
		if m.Timestamp.After(start) && m.Timestamp.Before(end) {
			result = append(result, m)
		}
	}

	return result
}

// GetLatestMetric returns the most recent metric value
func (ms *MetricsStorage) GetLatestMetric(metricType MetricType) *MetricDataPoint {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	var metrics []MetricDataPoint

	switch metricType {
	case MetricTypeCPU:
		metrics = ms.cpuMetrics
	case MetricTypeMemory:
		metrics = ms.memoryMetrics
	case MetricTypeStorage:
		metrics = ms.storageMetrics
	case MetricTypeBandwidth:
		metrics = ms.bandwidthMetrics
	case MetricTypeGPU:
		metrics = ms.gpuMetrics
	default:
		return nil
	}

	if len(metrics) == 0 {
		return nil
	}

	latest := metrics[len(metrics)-1]
	return &latest
}

// GetAverageMetric calculates the average value for a metric within a time range
func (ms *MetricsStorage) GetAverageMetric(metricType MetricType, start, end time.Time) float64 {
	metrics := ms.GetMetrics(metricType, start, end)

	if len(metrics) == 0 {
		return 0
	}

	var sum float64
	for _, m := range metrics {
		sum += m.Value
	}

	return sum / float64(len(metrics))
}

// RegisterAlertHandler registers a function to handle metric alerts
func (ms *MetricsStorage) RegisterAlertHandler(handler AlertHandler) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.alertHandlers = append(ms.alertHandlers, handler)
}

// SetThreshold sets or updates the threshold for a metric type
func (ms *MetricsStorage) SetThreshold(metricType MetricType, threshold MetricThreshold) {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	ms.thresholds[metricType] = threshold
}

// PruneOldMetrics removes metrics older than the retention duration
func (ms *MetricsStorage) PruneOldMetrics() {
	ms.mu.Lock()
	defer ms.mu.Unlock()

	cutoffTime := time.Now().Add(-ms.retentionDuration)

	ms.cpuMetrics = ms.pruneMetrics(ms.cpuMetrics, cutoffTime)
	ms.memoryMetrics = ms.pruneMetrics(ms.memoryMetrics, cutoffTime)
	ms.storageMetrics = ms.pruneMetrics(ms.storageMetrics, cutoffTime)
	ms.bandwidthMetrics = ms.pruneMetrics(ms.bandwidthMetrics, cutoffTime)
	ms.gpuMetrics = ms.pruneMetrics(ms.gpuMetrics, cutoffTime)

	ms.logger.Debug("Pruned old metrics",
		zap.Time("cutoff_time", cutoffTime),
	)
}

// pruneMetrics removes data points older than the cutoff time
func (ms *MetricsStorage) pruneMetrics(metrics []MetricDataPoint, cutoffTime time.Time) []MetricDataPoint {
	result := make([]MetricDataPoint, 0, len(metrics))

	for _, m := range metrics {
		if m.Timestamp.After(cutoffTime) {
			result = append(result, m)
		}
	}

	return result
}

// StartPruningLoop starts a background goroutine that periodically prunes old metrics
func (ms *MetricsStorage) StartPruningLoop(ctx context.Context) {
	ticker := time.NewTicker(1 * time.Hour) // Prune every hour
	defer ticker.Stop()

	ms.logger.Info("Starting metrics pruning loop")

	for {
		select {
		case <-ctx.Done():
			ms.logger.Info("Stopping metrics pruning loop")
			return
		case <-ticker.C:
			ms.PruneOldMetrics()
		}
	}
}

// GetMetricsSnapshot returns a snapshot of current metrics
func (ms *MetricsStorage) GetMetricsSnapshot() MetricsSnapshot {
	ms.mu.RLock()
	defer ms.mu.RUnlock()

	return MetricsSnapshot{
		CPU:       ms.getLatestValue(ms.cpuMetrics),
		Memory:    ms.getLatestValue(ms.memoryMetrics),
		Storage:   ms.getLatestValue(ms.storageMetrics),
		Bandwidth: ms.getLatestValue(ms.bandwidthMetrics),
		GPU:       ms.getLatestValue(ms.gpuMetrics),
		Timestamp: time.Now(),
	}
}

// getLatestValue returns the latest value from a metrics slice
func (ms *MetricsStorage) getLatestValue(metrics []MetricDataPoint) float64 {
	if len(metrics) == 0 {
		return 0
	}
	return metrics[len(metrics)-1].Value
}

// MetricsSnapshot represents a point-in-time snapshot of all metrics
type MetricsSnapshot struct {
	CPU       float64
	Memory    float64
	Storage   float64
	Bandwidth float64
	GPU       float64
	Timestamp time.Time
}
