package testutil

import (
	"os"
	"testing"

	"go.uber.org/zap"
	"go.uber.org/zap/zaptest"
)

// NewTestLogger creates a logger suitable for testing that outputs to the test log
func NewTestLogger(t *testing.T) *zap.Logger {
	return zaptest.NewLogger(t)
}

// NewTempDir creates a temporary directory for testing and returns cleanup function
func NewTempDir(t *testing.T, pattern string) (string, func()) {
	t.Helper()

	tmpDir, err := os.MkdirTemp("", pattern)
	if err != nil {
		t.Fatalf("Failed to create temp dir: %v", err)
	}

	cleanup := func() {
		os.RemoveAll(tmpDir)
	}

	return tmpDir, cleanup
}

// AssertNoError fails the test if err is not nil
func AssertNoError(t *testing.T, err error, msg string) {
	t.Helper()
	if err != nil {
		t.Fatalf("%s: %v", msg, err)
	}
}

// AssertError fails the test if err is nil
func AssertError(t *testing.T, err error, msg string) {
	t.Helper()
	if err == nil {
		t.Fatalf("%s: expected error but got nil", msg)
	}
}

// AssertEqual fails the test if got != want
func AssertEqual(t *testing.T, got, want interface{}, msg string) {
	t.Helper()
	if got != want {
		t.Fatalf("%s: got %v, want %v", msg, got, want)
	}
}

// AssertTrue fails the test if condition is false
func AssertTrue(t *testing.T, condition bool, msg string) {
	t.Helper()
	if !condition {
		t.Fatalf("%s: condition is false", msg)
	}
}

// AssertFalse fails the test if condition is true
func AssertFalse(t *testing.T, condition bool, msg string) {
	t.Helper()
	if condition {
		t.Fatalf("%s: condition is true", msg)
	}
}
