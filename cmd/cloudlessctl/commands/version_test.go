package commands

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

// TestNewVersionCommand tests version command creation.
// Per GO_ENGINEERING_SOP.md §12.2 (unit tests).
func TestNewVersionCommand(t *testing.T) {
	version := "v0.1.0"
	buildTime := "2025-10-30T00:00:00Z"
	gitCommit := "abc123def456"

	cmd := NewVersionCommand(version, buildTime, gitCommit)

	if cmd == nil {
		t.Fatal("NewVersionCommand returned nil")
	}

	if cmd.Use != "version" {
		t.Errorf("Use = %q, want %q", cmd.Use, "version")
	}

	if cmd.Short == "" {
		t.Error("Short description should not be empty")
	}

	if cmd.Long == "" {
		t.Error("Long description should not be empty")
	}

	if cmd.Run == nil {
		t.Error("Run function should not be nil")
	}
}

// TestVersionCommand_Output tests version command output.
// Note: Version command uses fmt.Printf which writes to stdout directly,
// not cobra's output writer. This is intentional for simplicity.
func TestVersionCommand_Output(t *testing.T) {
	tests := []struct {
		name           string
		version        string
		buildTime      string
		gitCommit      string
	}{
		{
			name:       "standard version",
			version:    "v1.0.0",
			buildTime:  "2025-10-30T12:00:00Z",
			gitCommit:  "abc123def456",
		},
		{
			name:       "dev version",
			version:    "dev-dirty",
			buildTime:  "unknown",
			gitCommit:  "uncommitted",
		},
		{
			name:       "empty values",
			version:    "",
			buildTime:  "",
			gitCommit:  "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create command
			cmd := NewVersionCommand(tt.version, tt.buildTime, tt.gitCommit)

			// Execute command (output goes to stdout)
			// We're testing that it doesn't error, not capturing output
			// since version command intentionally uses fmt.Printf
			if err := cmd.Execute(); err != nil {
				t.Fatalf("Execute() error = %v", err)
			}

			// Test passes if Execute() succeeds without error
			t.Logf("Version command executed successfully with version=%q", tt.version)
		})
	}
}

// TestVersionCommand_NoArgs tests version command with no arguments.
func TestVersionCommand_NoArgs(t *testing.T) {
	cmd := NewVersionCommand("v1.0.0", "2025-10-30", "abc123")

	// Version command should not accept arguments
	cmd.Args = cobra.NoArgs

	// Should succeed with no args
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Errorf("Execute() with no args error = %v, want nil", err)
	}
}

// TestVersionCommand_Integration tests version command as part of root command.
func TestVersionCommand_Integration(t *testing.T) {
	// Create root command
	rootCmd := &cobra.Command{Use: "cloudlessctl"}

	// Add version command
	versionCmd := NewVersionCommand("v1.0.0", "2025-10-30", "abc123")
	rootCmd.AddCommand(versionCmd)

	// Execute version subcommand
	rootCmd.SetArgs([]string{"version"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// Test passes if version subcommand executes without error
	t.Log("Version subcommand integrated successfully with root command")
}

// BenchmarkVersionCommand benchmarks version command execution.
// Per GO_ENGINEERING_SOP.md §13.4.
func BenchmarkVersionCommand(b *testing.B) {
	cmd := NewVersionCommand("v1.0.0", "2025-10-30", "abc123")
	var buf bytes.Buffer
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		if err := cmd.Execute(); err != nil {
			b.Fatalf("Execute() error = %v", err)
		}
	}
}
