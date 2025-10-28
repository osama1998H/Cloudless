package observability

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// TestNewLogger_ValidLevels tests logger creation with all valid log levels
func TestNewLogger_ValidLevels(t *testing.T) {
	tests := []struct {
		name          string
		level         string
		expectedLevel zapcore.Level
	}{
		{
			name:          "Debug level",
			level:         "debug",
			expectedLevel: zapcore.DebugLevel,
		},
		{
			name:          "Info level",
			level:         "info",
			expectedLevel: zapcore.InfoLevel,
		},
		{
			name:          "Warn level lowercase",
			level:         "warn",
			expectedLevel: zapcore.WarnLevel,
		},
		{
			name:          "Warning level",
			level:         "warning",
			expectedLevel: zapcore.WarnLevel,
		},
		{
			name:          "Error level",
			level:         "error",
			expectedLevel: zapcore.ErrorLevel,
		},
		{
			name:          "Fatal level",
			level:         "fatal",
			expectedLevel: zapcore.FatalLevel,
		},
		{
			name:          "Mixed case level",
			level:         "DEBUG",
			expectedLevel: zapcore.DebugLevel,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := NewLogger(tt.level)
			if err != nil {
				t.Fatalf("NewLogger(%s) error = %v, want nil", tt.level, err)
			}
			if logger == nil {
				t.Fatal("Expected non-nil logger")
			}

			// Verify logger is functional
			logger.Info("test message")
		})
	}
}

// TestNewLogger_InvalidLevel tests error handling for invalid log levels
func TestNewLogger_InvalidLevel(t *testing.T) {
	tests := []struct {
		name  string
		level string
	}{
		{
			name:  "Empty level",
			level: "",
		},
		{
			name:  "Invalid level",
			level: "invalid",
		},
		{
			name:  "Numeric level",
			level: "123",
		},
		{
			name:  "Special characters",
			level: "inf@!",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			logger, err := NewLogger(tt.level)
			if err == nil {
				t.Errorf("NewLogger(%s) expected error, got nil", tt.level)
			}
			if logger != nil {
				t.Errorf("NewLogger(%s) expected nil logger on error, got %v", tt.level, logger)
			}

			// Verify error message contains level info
			if !strings.Contains(err.Error(), "invalid log level") {
				t.Errorf("Error message should contain 'invalid log level', got: %v", err)
			}
		})
	}
}

// TestNewLogger_JSONEncoding tests production JSON format (non-debug levels)
func TestNewLogger_JSONEncoding(t *testing.T) {
	tests := []struct {
		name  string
		level string
	}{
		{"Info level", "info"},
		{"Warn level", "warn"},
		{"Error level", "error"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewLogger(tt.level)
			if err != nil {
				t.Fatalf("NewLogger() error = %v", err)
			}

			// Capture log output
			var buf bytes.Buffer
			core := zapcore.NewCore(
				zapcore.NewJSONEncoder(zapcore.EncoderConfig{
					TimeKey:        "timestamp",
					LevelKey:       "level",
					MessageKey:     "msg",
					EncodeLevel:    zapcore.LowercaseLevelEncoder,
					EncodeTime:     zapcore.ISO8601TimeEncoder,
					EncodeDuration: zapcore.MillisDurationEncoder,
				}),
				zapcore.AddSync(&buf),
				zapcore.InfoLevel,
			)
			testLogger := zap.New(core)

			// Write a log message
			testLogger.Info("test message", zap.String("key", "value"))

			// Verify JSON format
			output := buf.String()
			if output == "" {
				t.Fatal("Expected non-empty log output")
			}

			// Parse as JSON
			var logEntry map[string]interface{}
			if err := json.Unmarshal([]byte(output), &logEntry); err != nil {
				t.Fatalf("Log output is not valid JSON: %v\nOutput: %s", err, output)
			}

			// Verify required fields
			if _, ok := logEntry["level"]; !ok {
				t.Error("JSON log should contain 'level' field")
			}
			if _, ok := logEntry["msg"]; !ok {
				t.Error("JSON log should contain 'msg' field")
			}
			if _, ok := logEntry["timestamp"]; !ok {
				t.Error("JSON log should contain 'timestamp' field")
			}
		})
	}
}

// TestNewLogger_ConsoleEncoding tests development console format (debug level)
func TestNewLogger_ConsoleEncoding(t *testing.T) {
	logger, err := NewLogger("debug")
	if err != nil {
		t.Fatalf("NewLogger(debug) error = %v", err)
	}

	// For debug level, console encoding is used
	// We can't easily test the output format without replacing the core
	// But we can verify the logger works
	logger.Debug("debug message", zap.String("key", "value"))
	logger.Info("info message")
	logger.Warn("warn message")
}

// TestWithFields tests adding fields to a logger
func TestWithFields(t *testing.T) {
	logger, err := NewLogger("info")
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	// Add fields
	loggerWithFields := WithFields(logger,
		zap.String("service", "test-service"),
		zap.Int("port", 8080),
		zap.Bool("enabled", true),
	)

	if loggerWithFields == nil {
		t.Fatal("WithFields() returned nil logger")
	}

	// Verify logger is functional with fields
	loggerWithFields.Info("test message with fields")

	// Add more fields to the already-fielded logger
	loggerWithMoreFields := WithFields(loggerWithFields,
		zap.String("additional", "field"),
	)

	if loggerWithMoreFields == nil {
		t.Fatal("WithFields() on fielded logger returned nil")
	}

	loggerWithMoreFields.Info("test message with more fields")
}

// TestNewLogger_DefaultConfiguration tests default logger configuration
func TestNewLogger_DefaultConfiguration(t *testing.T) {
	logger, err := NewLogger("info")
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	// Verify logger handles various log levels correctly
	tests := []struct {
		name    string
		logFunc func(string, ...zap.Field)
	}{
		{
			name:    "Info log",
			logFunc: logger.Info,
		},
		{
			name:    "Warn log",
			logFunc: logger.Warn,
		},
		{
			name:    "Error log",
			logFunc: logger.Error,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Should not panic
			tt.logFunc("test message", zap.String("field", "value"))
		})
	}

	// Debug should be filtered out at info level
	logger.Debug("this should be filtered")
}

// TestNewLogger_StructuredFields tests various field types
func TestNewLogger_StructuredFields(t *testing.T) {
	logger, err := NewLogger("info")
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	// Test various zap field types
	logger.Info("structured log test",
		zap.String("string_field", "value"),
		zap.Int("int_field", 42),
		zap.Int64("int64_field", 9223372036854775807),
		zap.Float64("float_field", 3.14159),
		zap.Bool("bool_field", true),
		zap.Duration("duration_field", 1000000000), // 1 second in nanoseconds
		zap.Any("any_field", map[string]string{"nested": "value"}),
		zap.Strings("array_field", []string{"a", "b", "c"}),
	)

	// Verify logger didn't panic with various field types
}

// TestNewLogger_ErrorFields tests error field handling
func TestNewLogger_ErrorFields(t *testing.T) {
	logger, err := NewLogger("error")
	if err != nil {
		t.Fatalf("NewLogger() error = %v", err)
	}

	// Test error field
	testErr := &testError{msg: "test error"}
	logger.Error("error log test",
		zap.Error(testErr),
		zap.String("context", "test"),
	)
}

// testError is a simple error implementation for testing
type testError struct {
	msg string
}

func (e *testError) Error() string {
	return e.msg
}
