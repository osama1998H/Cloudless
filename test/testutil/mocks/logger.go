package mocks

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// NewNoOpLogger creates a logger that discards all output
// Useful for tests that don't need logging
func NewNoOpLogger() *zap.Logger {
	return zap.New(zapcore.NewNopCore())
}

// NewDevelopmentLogger creates a development logger for debugging tests
func NewDevelopmentLogger() *zap.Logger {
	logger, _ := zap.NewDevelopment()
	return logger
}
