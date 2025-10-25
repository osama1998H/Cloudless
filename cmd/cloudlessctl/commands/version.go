package commands

import (
	"fmt"

	"github.com/spf13/cobra"
)

// NewVersionCommand creates the version command
func NewVersionCommand(version, buildTime, gitCommit string) *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Long:  "Print the version, build time, and git commit of cloudlessctl",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("cloudlessctl version %s\n", version)
			fmt.Printf("Build time: %s\n", buildTime)
			fmt.Printf("Git commit: %s\n", gitCommit)
		},
	}
}
