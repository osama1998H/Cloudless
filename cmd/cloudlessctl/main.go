package main

import (
	"fmt"
	"os"

	"github.com/osama1998H/Cloudless/cmd/cloudlessctl/commands"
	"github.com/spf13/cobra"
)

var (
	// Version information (set by build flags)
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"
)

func main() {
	if err := newRootCommand().Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func newRootCommand() *cobra.Command {
	rootCmd := &cobra.Command{
		Use:   "cloudlessctl",
		Short: "Cloudless Platform CLI",
		Long: `cloudlessctl is the command-line interface for the Cloudless distributed compute platform.

It provides commands to manage nodes, workloads, storage, and networking across
your Cloudless cluster.`,
		Version: fmt.Sprintf("%s (commit: %s, built: %s)", Version, GitCommit, BuildTime),
	}

	// Global flags
	rootCmd.PersistentFlags().String("coordinator", "", "Coordinator address (default from config)")
	rootCmd.PersistentFlags().String("config", "", "Config file path (default: $HOME/.cloudless/config.yaml)")
	rootCmd.PersistentFlags().String("output", "table", "Output format: table, json, yaml")
	rootCmd.PersistentFlags().Bool("insecure", false, "Skip TLS certificate verification")

	// Add subcommands
	rootCmd.AddCommand(commands.NewNodeCommand())
	rootCmd.AddCommand(commands.NewWorkloadCommand())
	rootCmd.AddCommand(commands.NewStorageCommand())
	rootCmd.AddCommand(commands.NewNetworkCommand())
	rootCmd.AddCommand(commands.NewVersionCommand(Version, BuildTime, GitCommit))

	return rootCmd
}
