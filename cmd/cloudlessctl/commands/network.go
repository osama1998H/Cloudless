package commands

import (
	"context"
	"fmt"
	"strings"

	cmdconfig "github.com/cloudless/cloudless/cmd/cloudlessctl/config"
	"github.com/cloudless/cloudless/pkg/api"
	"github.com/spf13/cobra"
)

// NewNetworkCommand creates the network command
func NewNetworkCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "network",
		Short: "Manage networking",
		Long:  "Manage networking including services, endpoints, and overlay configuration",
	}

	cmd.AddCommand(newNetworkServiceCommand())
	cmd.AddCommand(newNetworkEndpointCommand())

	return cmd
}

// newNetworkServiceCommand creates the service command
func newNetworkServiceCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Manage services",
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List services",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceList(cmd, args)
		},
	}
	listCmd.Flags().StringP("output", "o", "table", "Output format (table, json, yaml)")
	listCmd.Flags().String("namespace", "", "Filter by namespace")
	listCmd.Flags().StringSlice("labels", nil, "Filter by labels (key=value format)")
	cmd.AddCommand(listCmd)

	getCmd := &cobra.Command{
		Use:   "get SERVICE_ID",
		Short: "Get service details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return runServiceGet(cmd, args)
		},
	}
	getCmd.Flags().StringP("output", "o", "yaml", "Output format (json, yaml)")
	cmd.AddCommand(getCmd)

	return cmd
}

// newNetworkEndpointCommand creates the endpoint command
func newNetworkEndpointCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "endpoint",
		Short: "Manage endpoints",
	}

	listCmd := &cobra.Command{
		Use:   "list",
		Short: "List endpoints",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEndpointList(cmd, args)
		},
	}
	listCmd.Flags().StringP("output", "o", "table", "Output format (table, json, yaml)")
	listCmd.Flags().String("service", "", "Filter by service ID")
	listCmd.Flags().String("zone", "", "Filter by zone")
	listCmd.Flags().String("health", "", "Filter by health status (Healthy, Unhealthy, Unknown)")
	cmd.AddCommand(listCmd)

	return cmd
}

// Service command implementations

func runServiceList(cmd *cobra.Command, args []string) error {
	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewNetworkClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	namespace, _ := cmd.Flags().GetString("namespace")
	labels, _ := cmd.Flags().GetStringSlice("labels")

	// Parse label selector
	labelSelector := make(map[string]string)
	for _, label := range labels {
		parts := strings.SplitN(label, "=", 2)
		if len(parts) == 2 {
			labelSelector[parts[0]] = parts[1]
		}
	}

	ctx := context.Background()
	resp, err := client.ListServices(ctx, &api.ListServicesRequest{
		Namespace:     namespace,
		LabelSelector: labelSelector,
	})
	if err != nil {
		return fmt.Errorf("failed to list services: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	outputter := cmdconfig.NewOutputter(output)

	if output == "table" {
		if len(resp.Services) == 0 {
			fmt.Println("No services found")
			return nil
		}

		headers := []string{"ID", "NAME", "NAMESPACE", "TYPE", "PORTS", "CREATED"}
		var rows [][]string
		for _, svc := range resp.Services {
			// Format ports
			var ports []string
			for _, port := range svc.Spec.Ports {
				ports = append(ports, fmt.Sprintf("%d/%s", port.Port, port.Protocol))
			}
			portsStr := strings.Join(ports, ", ")
			if portsStr == "" {
				portsStr = "-"
			}

			rows = append(rows, []string{
				svc.Id,
				svc.Name,
				svc.Namespace,
				svc.Spec.Type,
				portsStr,
				svc.CreatedAt.AsTime().Format("2006-01-02 15:04:05"),
			})
		}
		outputter.PrintTable(headers, rows)
	} else {
		outputter.Print(resp.Services)
	}

	return nil
}

func runServiceGet(cmd *cobra.Command, args []string) error {
	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewNetworkClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	serviceID := args[0]

	ctx := context.Background()
	service, err := client.GetService(ctx, &api.GetServiceRequest{
		ServiceId: serviceID,
	})
	if err != nil {
		return fmt.Errorf("failed to get service: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	outputter := cmdconfig.NewOutputter(output)
	outputter.Print(service)

	return nil
}

// Endpoint command implementations

func runEndpointList(cmd *cobra.Command, args []string) error {
	cfg, err := cmdconfig.LoadConfig(cmd)
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	client, conn, err := cfg.NewNetworkClient()
	if err != nil {
		return fmt.Errorf("failed to create client: %w", err)
	}
	defer conn.Close()

	serviceID, _ := cmd.Flags().GetString("service")
	zone, _ := cmd.Flags().GetString("zone")
	health, _ := cmd.Flags().GetString("health")

	ctx := context.Background()
	resp, err := client.ListEndpoints(ctx, &api.ListEndpointsRequest{
		ServiceId:    serviceID,
		Zone:         zone,
		HealthStatus: health,
	})
	if err != nil {
		return fmt.Errorf("failed to list endpoints: %w", err)
	}

	output, _ := cmd.Flags().GetString("output")
	outputter := cmdconfig.NewOutputter(output)

	if output == "table" {
		if len(resp.Endpoints) == 0 {
			fmt.Println("No endpoints found")
			return nil
		}

		headers := []string{"ID", "SERVICE", "ADDRESS", "PORT", "NODE", "HEALTH", "WEIGHT", "ZONE"}
		var rows [][]string
		for _, ep := range resp.Endpoints {
			healthStatus := "Unknown"
			if ep.Health != nil {
				healthStatus = ep.Health.Status
			}

			rows = append(rows, []string{
				ep.Id,
				ep.ServiceId,
				ep.Address,
				fmt.Sprintf("%d", ep.Port),
				ep.NodeId,
				healthStatus,
				fmt.Sprintf("%d", ep.Weight),
				ep.Zone,
			})
		}
		outputter.PrintTable(headers, rows)
	} else {
		outputter.Print(resp.Endpoints)
	}

	return nil
}
