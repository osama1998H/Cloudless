package commands

import (
	"context"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"
	"gopkg.in/yaml.v3"

	"github.com/cloudless/cloudless/cmd/cloudlessctl/config"
	"github.com/cloudless/cloudless/pkg/api"
)

// NewWorkloadCommand creates the workload command
func NewWorkloadCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "workload",
		Aliases: []string{"wl", "workloads"},
		Short:   "Manage workloads",
		Long:    "Manage workloads including creating, updating, deleting, and scaling containerized applications",
	}

	cmd.AddCommand(newWorkloadCreateCommand())
	cmd.AddCommand(newWorkloadListCommand())
	cmd.AddCommand(newWorkloadGetCommand())
	cmd.AddCommand(newWorkloadDeleteCommand())
	cmd.AddCommand(newWorkloadScaleCommand())
	cmd.AddCommand(newWorkloadLogsCommand())
	cmd.AddCommand(newWorkloadExecCommand())

	return cmd
}

// newWorkloadCreateCommand creates the workload create command
func newWorkloadCreateCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "create",
		Short: "Create a workload",
		Long:  "Create a workload from a YAML/JSON manifest file or command-line flags",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkloadCreate(cmd)
		},
	}

	cmd.Flags().StringP("file", "f", "", "Manifest file (YAML or JSON)")
	cmd.Flags().String("name", "", "Workload name")
	cmd.Flags().String("namespace", "default", "Workload namespace")
	cmd.Flags().String("image", "", "Container image")
	cmd.Flags().Int32("replicas", 1, "Number of replicas")
	cmd.Flags().StringSlice("env", nil, "Environment variables (KEY=VALUE)")

	return cmd
}

func runWorkloadCreate(cmd *cobra.Command, args []string) error {
	// Load config
	cfg, err := config.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create client
	client, conn, err := cfg.NewCoordinatorClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Parse workload from file or flags
	var workload *api.Workload
	manifestFile, _ := cmd.Flags().GetString("file")
	if manifestFile != "" {
		workload, err = parseWorkloadManifest(manifestFile)
		if err != nil {
			return err
		}
	} else {
		workload, err = parseWorkloadFlags(cmd)
		if err != nil {
			return err
		}
	}

	// Call API
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	created, err := client.CreateWorkload(ctx, &api.CreateWorkloadRequest{
		Workload: workload,
	})
	if err != nil {
		return fmt.Errorf("failed to create workload: %w", err)
	}

	fmt.Printf("Workload %s created successfully (ID: %s)\n", created.Name, created.Id)
	return nil
}

func parseWorkloadManifest(filename string) (*api.Workload, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read manifest: %w", err)
	}

	var workload api.Workload
	if err := yaml.Unmarshal(data, &workload); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	return &workload, nil
}

func parseWorkloadFlags(cmd *cobra.Command) (*api.Workload, error) {
	name, _ := cmd.Flags().GetString("name")
	namespace, _ := cmd.Flags().GetString("namespace")
	image, _ := cmd.Flags().GetString("image")
	replicas, _ := cmd.Flags().GetInt32("replicas")
	envVars, _ := cmd.Flags().GetStringSlice("env")

	if name == "" || image == "" {
		return nil, fmt.Errorf("name and image are required")
	}

	// Parse environment variables
	env := make(map[string]string)
	for _, e := range envVars {
		// Parse KEY=VALUE
		key := ""
		value := ""
		for i, c := range e {
			if c == '=' {
				key = e[:i]
				value = e[i+1:]
				break
			}
		}
		if key != "" {
			env[key] = value
		}
	}

	workload := &api.Workload{
		Name:      name,
		Namespace: namespace,
		Spec: &api.WorkloadSpec{
			Image:    image,
			Replicas: replicas,
			Env:      env,
		},
	}

	return workload, nil
}

// newWorkloadListCommand creates the workload list command
func newWorkloadListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List workloads",
		Long:  "List all workloads in the cluster or a specific namespace",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkloadList(cmd)
		},
	}

	cmd.Flags().StringP("namespace", "n", "", "Namespace to list (empty for all)")
	cmd.Flags().StringToString("labels", nil, "Filter by labels (key=value)")

	return cmd
}

func runWorkloadList(cmd *cobra.Command) error {
	// Load config
	cfg, err := config.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create client
	client, conn, err := cfg.NewCoordinatorClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Get flags
	namespace, _ := cmd.Flags().GetString("namespace")
	labels, _ := cmd.Flags().GetStringToString("labels")

	// Build request
	req := &api.ListWorkloadsRequest{
		Namespace:     namespace,
		LabelSelector: labels,
	}

	// Call API
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListWorkloads(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to list workloads: %w", err)
	}

	// Output results
	output, _ := cmd.Flags().GetString("output")
	out := config.NewOutputter(output)

	if out.GetFormat() == config.OutputTable {
		return printWorkloadTable(out, resp.Workloads)
	}

	return out.Print(resp.Workloads)
}

func printWorkloadTable(out *config.Outputter, workloads []*api.Workload) error {
	headers := []string{"NAME", "NAMESPACE", "IMAGE", "REPLICAS", "READY", "STATUS", "AGE"}
	rows := make([][]string, 0, len(workloads))

	for _, wl := range workloads {
		age := time.Since(wl.CreatedAt.AsTime()).Round(time.Second).String()
		replicas := fmt.Sprintf("%d/%d",
			wl.Status.ReadyReplicas,
			wl.Status.DesiredReplicas)

		rows = append(rows, []string{
			wl.Name,
			wl.Namespace,
			wl.Spec.Image,
			fmt.Sprintf("%d", wl.Spec.Replicas),
			replicas,
			wl.Status.Phase.String(),
			age,
		})
	}

	out.PrintTable(headers, rows)
	fmt.Printf("\nTotal: %d workloads\n", len(workloads))
	return nil
}

// newWorkloadGetCommand creates the workload get command
func newWorkloadGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get WORKLOAD_ID",
		Short: "Get workload details",
		Long:  "Get detailed information about a specific workload",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkloadGet(cmd, args[0])
		},
	}
}

func runWorkloadGet(cmd *cobra.Command, workloadID string) error {
	// Load config
	cfg, err := config.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create client
	client, conn, err := cfg.NewCoordinatorClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Call API
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	workload, err := client.GetWorkload(ctx, &api.GetWorkloadRequest{
		WorkloadId: workloadID,
	})
	if err != nil {
		return fmt.Errorf("failed to get workload: %w", err)
	}

	// Output result
	output, _ := cmd.Flags().GetString("output")
	out := config.NewOutputter(output)

	return out.Print(workload)
}

// newWorkloadDeleteCommand creates the workload delete command
func newWorkloadDeleteCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "delete WORKLOAD_ID",
		Short: "Delete a workload",
		Long:  "Delete a workload and all its replicas",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkloadDelete(cmd, args[0])
		},
	}

	cmd.Flags().Bool("graceful", true, "Gracefully terminate workload")
	cmd.Flags().Duration("timeout", 2*time.Minute, "Timeout for deletion")

	return cmd
}

func runWorkloadDelete(cmd *cobra.Command, workloadID string) error {
	// Load config
	cfg, err := config.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create client
	client, conn, err := cfg.NewCoordinatorClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Get flags
	graceful, _ := cmd.Flags().GetBool("graceful")
	timeout, _ := cmd.Flags().GetDuration("timeout")

	// Call API
	ctx, cancel := context.WithTimeout(context.Background(), timeout+10*time.Second)
	defer cancel()

	_, err = client.DeleteWorkload(ctx, &api.DeleteWorkloadRequest{
		WorkloadId: workloadID,
		Graceful:   graceful,
		Timeout:    durationpb.New(timeout),
	})
	if err != nil {
		return fmt.Errorf("failed to delete workload: %w", err)
	}

	fmt.Printf("Workload %s deleted successfully\n", workloadID)
	return nil
}

// newWorkloadScaleCommand creates the workload scale command
func newWorkloadScaleCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scale WORKLOAD_ID --replicas N",
		Short: "Scale a workload",
		Long:  "Scale a workload to the specified number of replicas",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkloadScale(cmd, args[0])
		},
	}

	cmd.Flags().Int32("replicas", 0, "Number of replicas (required)")
	cmd.MarkFlagRequired("replicas")

	return cmd
}

func runWorkloadScale(cmd *cobra.Command, workloadID string) error {
	// Load config
	cfg, err := config.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create client
	client, conn, err := cfg.NewCoordinatorClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	// Get replicas
	replicas, _ := cmd.Flags().GetInt32("replicas")

	// Call API
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	workload, err := client.ScaleWorkload(ctx, &api.ScaleWorkloadRequest{
		WorkloadId: workloadID,
		Replicas:   replicas,
	})
	if err != nil {
		return fmt.Errorf("failed to scale workload: %w", err)
	}

	fmt.Printf("Workload %s scaled to %d replicas\n", workload.Name, replicas)
	return nil
}

// newWorkloadLogsCommand creates the workload logs command
func newWorkloadLogsCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "logs CONTAINER_ID",
		Short: "Stream workload logs",
		Long:  "Stream logs from a workload container",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWorkloadLogs(cmd, args[0])
		},
	}

	cmd.Flags().BoolP("follow", "f", false, "Follow log output")
	cmd.Flags().Int32("tail", 100, "Number of lines to tail")

	return cmd
}

func runWorkloadLogs(cmd *cobra.Command, containerID string) error {
	// Load config
	cfg, err := config.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create client (Note: logs are served by agent, not coordinator)
	// For MVP, we'll connect through coordinator which proxies to agent
	client, conn, err := cfg.NewGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	agentClient := api.NewAgentServiceClient(conn)

	// Get flags
	follow, _ := cmd.Flags().GetBool("follow")
	tail, _ := cmd.Flags().GetInt32("tail")

	// Call API
	ctx := context.Background()
	stream, err := agentClient.StreamLogs(ctx, &api.StreamLogsRequest{
		ContainerId: containerID,
		Follow:      follow,
		TailLines:   tail,
	})
	if err != nil {
		return fmt.Errorf("failed to stream logs: %w", err)
	}

	// Stream logs to stdout
	for {
		entry, err := stream.Recv()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error receiving logs: %w", err)
		}

		fmt.Printf("[%s] %s", entry.Stream, entry.Line)
	}

	return nil
}

// newWorkloadExecCommand creates the workload exec command
func newWorkloadExecCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "exec CONTAINER_ID -- COMMAND [args...]",
		Short: "Execute command in container",
		Long:  "Execute a command inside a running container",
		Args:  cobra.MinimumNArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			containerID := args[0]
			command := args[1:]
			return runWorkloadExec(cmd, containerID, command)
		},
	}

	cmd.Flags().BoolP("tty", "t", false, "Allocate a pseudo-TTY")

	return cmd
}

func runWorkloadExec(cmd *cobra.Command, containerID string, command []string) error {
	// Load config
	cfg, err := config.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	// Create client
	client, conn, err := cfg.NewGRPCClient()
	if err != nil {
		return err
	}
	defer conn.Close()

	agentClient := api.NewAgentServiceClient(conn)

	// Get flags
	tty, _ := cmd.Flags().GetBool("tty")

	// Call API
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	resp, err := agentClient.ExecCommand(ctx, &api.ExecCommandRequest{
		ContainerId: containerID,
		Command:     command,
		Tty:         tty,
	})
	if err != nil {
		return fmt.Errorf("failed to exec command: %w", err)
	}

	// Output results
	if len(resp.Stdout) > 0 {
		fmt.Print(string(resp.Stdout))
	}
	if len(resp.Stderr) > 0 {
		fmt.Fprint(os.Stderr, string(resp.Stderr))
	}

	if resp.ExitCode != 0 {
		return fmt.Errorf("command exited with code %d", resp.ExitCode)
	}

	return nil
}
