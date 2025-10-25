package commands

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
	"google.golang.org/protobuf/types/known/durationpb"

	"github.com/cloudless/cloudless/cmd/cloudlessctl/config"
	"github.com/cloudless/cloudless/pkg/api"
)

// NewNodeCommand creates the node command
func NewNodeCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "node",
		Short: "Manage cluster nodes",
		Long:  "Manage nodes in the Cloudless cluster including listing, inspecting, draining, and cordoning",
	}

	cmd.AddCommand(newNodeListCommand())
	cmd.AddCommand(newNodeGetCommand())
	cmd.AddCommand(newNodeDescribeCommand())
	cmd.AddCommand(newNodeDrainCommand())
	cmd.AddCommand(newNodeUncordonCommand())

	return cmd
}

// newNodeListCommand creates the node list command
func newNodeListCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List all nodes",
		Long:  "List all nodes in the cluster with their status and resource information",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNodeList(cmd)
		},
	}

	cmd.Flags().StringP("region", "r", "", "Filter by region")
	cmd.Flags().StringP("zone", "z", "", "Filter by zone")
	cmd.Flags().StringToString("labels", nil, "Filter by labels (key=value)")

	return cmd
}

func runNodeList(cmd *cobra.Command) error {
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
	region, _ := cmd.Flags().GetString("region")
	zone, _ := cmd.Flags().GetString("zone")
	labels, _ := cmd.Flags().GetStringToString("labels")

	// Build request
	req := &api.ListNodesRequest{
		LabelSelector: labels,
	}
	if region != "" {
		req.Regions = []string{region}
	}
	if zone != "" {
		req.Zones = []string{zone}
	}

	// Call API
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resp, err := client.ListNodes(ctx, req)
	if err != nil {
		return fmt.Errorf("failed to list nodes: %w", err)
	}

	// Output results
	output, _ := cmd.Flags().GetString("output")
	out := config.NewOutputter(output)

	if out.GetFormat() == config.OutputTable {
		return printNodeTable(out, resp.Nodes)
	}

	return out.Print(resp.Nodes)
}

func printNodeTable(out *config.Outputter, nodes []*api.Node) error {
	headers := []string{"ID", "NAME", "REGION", "ZONE", "STATUS", "CPU", "MEMORY", "RELIABILITY"}
	rows := make([][]string, 0, len(nodes))

	for _, node := range nodes {
		status := node.Status.Phase.String()
		cpu := fmt.Sprintf("%dm/%dm",
			node.Usage.CpuMillicores,
			node.Capacity.CpuMillicores)
		memory := fmt.Sprintf("%dMi/%dMi",
			node.Usage.MemoryBytes/(1024*1024),
			node.Capacity.MemoryBytes/(1024*1024))
		reliability := fmt.Sprintf("%.2f", node.ReliabilityScore)

		rows = append(rows, []string{
			node.Id,
			node.Name,
			node.Region,
			node.Zone,
			status,
			cpu,
			memory,
			reliability,
		})
	}

	out.PrintTable(headers, rows)
	fmt.Printf("\nTotal: %d nodes\n", len(nodes))
	return nil
}

// newNodeGetCommand creates the node get command
func newNodeGetCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "get NODE_ID",
		Short: "Get node details",
		Long:  "Get detailed information about a specific node",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNodeGet(cmd, args[0])
		},
	}
}

func runNodeGet(cmd *cobra.Command, nodeID string) error {
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

	node, err := client.GetNode(ctx, &api.GetNodeRequest{
		NodeId: nodeID,
	})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	// Output result
	output, _ := cmd.Flags().GetString("output")
	out := config.NewOutputter(output)

	return out.Print(node)
}

// newNodeDescribeCommand creates the node describe command
func newNodeDescribeCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "describe NODE_ID",
		Short: "Describe node in detail",
		Long:  "Show detailed information about a node including capabilities, conditions, and taints",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNodeDescribe(cmd, args[0])
		},
	}
}

func runNodeDescribe(cmd *cobra.Command, nodeID string) error {
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

	node, err := client.GetNode(ctx, &api.GetNodeRequest{
		NodeId: nodeID,
	})
	if err != nil {
		return fmt.Errorf("failed to get node: %w", err)
	}

	// Print detailed description
	printNodeDescription(node)
	return nil
}

func printNodeDescription(node *api.Node) {
	fmt.Printf("Name:                %s\n", node.Name)
	fmt.Printf("ID:                  %s\n", node.Id)
	fmt.Printf("Region:              %s\n", node.Region)
	fmt.Printf("Zone:                %s\n", node.Zone)
	fmt.Printf("Status:              %s\n", node.Status.Phase.String())
	fmt.Printf("Reliability Score:   %.2f\n", node.ReliabilityScore)
	fmt.Printf("Last Seen:           %s\n", node.LastSeen.AsTime().Format(time.RFC3339))

	fmt.Printf("\nCapacity:\n")
	fmt.Printf("  CPU:               %dm\n", node.Capacity.CpuMillicores)
	fmt.Printf("  Memory:            %dMi\n", node.Capacity.MemoryBytes/(1024*1024))
	fmt.Printf("  Storage:           %dGi\n", node.Capacity.StorageBytes/(1024*1024*1024))
	fmt.Printf("  Bandwidth:         %dMbps\n", node.Capacity.BandwidthBps/(1024*1024))
	if node.Capacity.GpuCount > 0 {
		fmt.Printf("  GPUs:              %d\n", node.Capacity.GpuCount)
	}

	fmt.Printf("\nUsage:\n")
	fmt.Printf("  CPU:               %dm (%.1f%%)\n",
		node.Usage.CpuMillicores,
		float64(node.Usage.CpuMillicores)/float64(node.Capacity.CpuMillicores)*100)
	fmt.Printf("  Memory:            %dMi (%.1f%%)\n",
		node.Usage.MemoryBytes/(1024*1024),
		float64(node.Usage.MemoryBytes)/float64(node.Capacity.MemoryBytes)*100)
	fmt.Printf("  Storage:           %dGi (%.1f%%)\n",
		node.Usage.StorageBytes/(1024*1024*1024),
		float64(node.Usage.StorageBytes)/float64(node.Capacity.StorageBytes)*100)

	if node.Capabilities != nil {
		fmt.Printf("\nCapabilities:\n")
		fmt.Printf("  Container Runtimes: %v\n", node.Capabilities.ContainerRuntimes)
		fmt.Printf("  Supports GPU:      %v\n", node.Capabilities.SupportsGpu)
		fmt.Printf("  Supports ARM:      %v\n", node.Capabilities.SupportsArm)
		fmt.Printf("  Supports x86:      %v\n", node.Capabilities.SupportsX86)
		if len(node.Capabilities.NetworkFeatures) > 0 {
			fmt.Printf("  Network Features:  %v\n", node.Capabilities.NetworkFeatures)
		}
		if len(node.Capabilities.StorageClasses) > 0 {
			fmt.Printf("  Storage Classes:   %v\n", node.Capabilities.StorageClasses)
		}
	}

	if len(node.Labels) > 0 {
		fmt.Printf("\nLabels:\n")
		for k, v := range node.Labels {
			fmt.Printf("  %s: %s\n", k, v)
		}
	}

	if len(node.Taints) > 0 {
		fmt.Printf("\nTaints:\n")
		for _, taint := range node.Taints {
			fmt.Printf("  %s=%s:%s\n", taint.Key, taint.Value, taint.Effect)
		}
	}

	if len(node.Status.Conditions) > 0 {
		fmt.Printf("\nConditions:\n")
		for _, cond := range node.Status.Conditions {
			fmt.Printf("  %s: %s\n", cond.Type, cond.Status)
			if cond.Reason != "" {
				fmt.Printf("    Reason: %s\n", cond.Reason)
			}
			if cond.Message != "" {
				fmt.Printf("    Message: %s\n", cond.Message)
			}
		}
	}
}

// newNodeDrainCommand creates the node drain command
func newNodeDrainCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "drain NODE_ID",
		Short: "Drain node",
		Long:  "Drain a node by evicting workloads and preventing new workloads from being scheduled",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNodeDrain(cmd, args[0])
		},
	}

	cmd.Flags().Bool("graceful", true, "Gracefully drain the node")
	cmd.Flags().Duration("timeout", 5*time.Minute, "Timeout for drain operation")

	return cmd
}

func runNodeDrain(cmd *cobra.Command, nodeID string) error {
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

	_, err = client.DrainNode(ctx, &api.DrainNodeRequest{
		NodeId:   nodeID,
		Graceful: graceful,
		Timeout:  durationpb.New(timeout),
	})
	if err != nil {
		return fmt.Errorf("failed to drain node: %w", err)
	}

	fmt.Printf("Node %s drained successfully\n", nodeID)
	return nil
}

// newNodeUncordonCommand creates the node uncordon command
func newNodeUncordonCommand() *cobra.Command {
	return &cobra.Command{
		Use:   "uncordon NODE_ID",
		Short: "Uncordon node",
		Long:  "Mark a node as schedulable again after draining or cordoning",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runNodeUncordon(cmd, args[0])
		},
	}
}

func runNodeUncordon(cmd *cobra.Command, nodeID string) error {
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

	_, err = client.UncordonNode(ctx, &api.UncordonNodeRequest{
		NodeId: nodeID,
	})
	if err != nil {
		return fmt.Errorf("failed to uncordon node: %w", err)
	}

	fmt.Printf("Node %s uncordoned successfully\n", nodeID)
	return nil
}
