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

	"github.com/osama1998H/Cloudless/cmd/cloudlessctl/config"
	"github.com/osama1998H/Cloudless/pkg/api"
)

// YAML manifest structures for Kubernetes-style workload definitions
// These mirror the YAML structure and are converted to protobuf messages

type WorkloadManifest struct {
	APIVersion string           `yaml:"apiVersion"`
	Kind       string           `yaml:"kind"`
	Metadata   ManifestMetadata `yaml:"metadata"`
	Spec       ManifestSpec     `yaml:"spec"`
}

type ManifestMetadata struct {
	Name        string            `yaml:"name"`
	Namespace   string            `yaml:"namespace"`
	Labels      map[string]string `yaml:"labels"`
	Annotations map[string]string `yaml:"annotations"`
}

type ManifestSpec struct {
	Image            string                        `yaml:"image"`
	Command          []string                      `yaml:"command"`
	Args             []string                      `yaml:"args"`
	Env              map[string]string             `yaml:"env"`
	Resources        *ManifestResourceRequirements `yaml:"resources"`
	Replicas         int32                         `yaml:"replicas"`
	Placement        *ManifestPlacement            `yaml:"placement"`
	RestartPolicy    *ManifestRestartPolicy        `yaml:"restart_policy"`
	Volumes          []ManifestVolume              `yaml:"volumes"`
	Ports            []ManifestPort                `yaml:"ports"`
	LivenessProbe    *ManifestHealthCheck          `yaml:"liveness_probe"`
	ReadinessProbe   *ManifestHealthCheck          `yaml:"readiness_probe"`
	ImagePullSecrets []string                      `yaml:"image_pull_secrets"`
}

type ManifestResourceRequirements struct {
	Requests *ManifestResourceCapacity `yaml:"requests"`
	Limits   *ManifestResourceCapacity `yaml:"limits"`
}

type ManifestResourceCapacity struct {
	CPUMillicores int64 `yaml:"cpu_millicores"`
	MemoryBytes   int64 `yaml:"memory_bytes"`
	StorageBytes  int64 `yaml:"storage_bytes"`
}

type ManifestPlacement struct {
	Regions        []string           `yaml:"regions"`
	Zones          []string           `yaml:"zones"`
	NodeSelector   map[string]string  `yaml:"node_selector"`
	Affinity       []ManifestAffinity `yaml:"affinity"`
	AntiAffinity   []ManifestAffinity `yaml:"anti_affinity"`
	SpreadTopology string             `yaml:"spread_topology"`
}

type ManifestAffinity struct {
	Type        string            `yaml:"type"`
	MatchLabels map[string]string `yaml:"match_labels"`
	TopologyKey string            `yaml:"topology_key"`
	Required    bool              `yaml:"required"`
}

type ManifestRestartPolicy struct {
	Policy     string `yaml:"policy"`
	MaxRetries int32  `yaml:"max_retries"`
	Backoff    string `yaml:"backoff"` // Duration string like "30s"
}

type ManifestVolume struct {
	Name      string            `yaml:"name"`
	Source    string            `yaml:"source"`
	MountPath string            `yaml:"mount_path"`
	ReadOnly  bool              `yaml:"read_only"`
	Options   map[string]string `yaml:"options"`
}

type ManifestPort struct {
	Name          string `yaml:"name"`
	ContainerPort int32  `yaml:"container_port"`
	HostPort      int32  `yaml:"host_port"`
	Protocol      string `yaml:"protocol"`
}

type ManifestHealthCheck struct {
	HTTP             *ManifestHTTPProbe `yaml:"http"`
	TCP              *ManifestTCPProbe  `yaml:"tcp"`
	Exec             *ManifestExecProbe `yaml:"exec"`
	InitialDelay     string             `yaml:"initial_delay"` // Duration string like "15s"
	Period           string             `yaml:"period"`        // Duration string like "10s"
	Timeout          string             `yaml:"timeout"`       // Duration string like "3s"
	SuccessThreshold int32              `yaml:"success_threshold"`
	FailureThreshold int32              `yaml:"failure_threshold"`
}

type ManifestHTTPProbe struct {
	Path    string               `yaml:"path"`
	Port    int32                `yaml:"port"`
	Scheme  string               `yaml:"scheme"` // HTTP or HTTPS
	Headers []ManifestHTTPHeader `yaml:"headers"`
}

type ManifestHTTPHeader struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type ManifestTCPProbe struct {
	Port int32 `yaml:"port"`
}

type ManifestExecProbe struct {
	Command []string `yaml:"command"`
}

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
			return runWorkloadCreate(cmd, args)
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

	// Parse YAML into WorkloadManifest structure
	var manifest WorkloadManifest
	if err := yaml.Unmarshal(data, &manifest); err != nil {
		return nil, fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Convert to protobuf Workload
	workload, err := manifest.ToWorkload()
	if err != nil {
		return nil, fmt.Errorf("failed to convert manifest: %w", err)
	}

	return workload, nil
}

// Helper functions for converting YAML manifest to protobuf

// parseDuration converts a duration string (like "15s", "10m") to durationpb.Duration
func parseDuration(s string) (*durationpb.Duration, error) {
	if s == "" {
		return nil, nil
	}
	d, err := time.ParseDuration(s)
	if err != nil {
		return nil, fmt.Errorf("invalid duration %q: %w", s, err)
	}
	return durationpb.New(d), nil
}

// parseRestartPolicy converts a policy string to protobuf enum
func parseRestartPolicy(s string) (api.RestartPolicy_Policy, error) {
	switch s {
	case "ALWAYS":
		return api.RestartPolicy_ALWAYS, nil
	case "ON_FAILURE":
		return api.RestartPolicy_ON_FAILURE, nil
	case "NEVER":
		return api.RestartPolicy_NEVER, nil
	default:
		return api.RestartPolicy_ALWAYS, fmt.Errorf("invalid restart policy %q (must be ALWAYS, ON_FAILURE, or NEVER)", s)
	}
}

// parseHTTPScheme validates and returns the HTTP scheme string
func parseHTTPScheme(s string) (string, error) {
	switch s {
	case "", "HTTP":
		return "HTTP", nil
	case "HTTPS":
		return "HTTPS", nil
	default:
		return "HTTP", fmt.Errorf("invalid HTTP scheme %q (must be HTTP or HTTPS)", s)
	}
}

// parseAffinityType converts an affinity type string to protobuf enum
func parseAffinityType(s string) (api.Affinity_Type, error) {
	switch s {
	case "NODE":
		return api.Affinity_NODE, nil
	case "WORKLOAD":
		return api.Affinity_WORKLOAD, nil
	default:
		return api.Affinity_NODE, fmt.Errorf("invalid affinity type %q (must be NODE or WORKLOAD)", s)
	}
}

// ToWorkload converts WorkloadManifest to api.Workload protobuf message
func (m *WorkloadManifest) ToWorkload() (*api.Workload, error) {
	// Build WorkloadSpec
	spec := &api.WorkloadSpec{
		Image:            m.Spec.Image,
		Command:          m.Spec.Command,
		Args:             m.Spec.Args,
		Env:              m.Spec.Env,
		Replicas:         m.Spec.Replicas,
		ImagePullSecrets: m.Spec.ImagePullSecrets,
	}

	// Convert resources
	if m.Spec.Resources != nil {
		spec.Resources = &api.ResourceRequirements{}
		if m.Spec.Resources.Requests != nil {
			spec.Resources.Requests = &api.ResourceCapacity{
				CpuMillicores: m.Spec.Resources.Requests.CPUMillicores,
				MemoryBytes:   m.Spec.Resources.Requests.MemoryBytes,
				StorageBytes:  m.Spec.Resources.Requests.StorageBytes,
			}
		}
		if m.Spec.Resources.Limits != nil {
			spec.Resources.Limits = &api.ResourceCapacity{
				CpuMillicores: m.Spec.Resources.Limits.CPUMillicores,
				MemoryBytes:   m.Spec.Resources.Limits.MemoryBytes,
				StorageBytes:  m.Spec.Resources.Limits.StorageBytes,
			}
		}
	}

	// Convert restart policy
	if m.Spec.RestartPolicy != nil {
		policy, err := parseRestartPolicy(m.Spec.RestartPolicy.Policy)
		if err != nil {
			return nil, err
		}
		backoff, err := parseDuration(m.Spec.RestartPolicy.Backoff)
		if err != nil {
			return nil, fmt.Errorf("restart_policy.backoff: %w", err)
		}
		spec.RestartPolicy = &api.RestartPolicy{
			Policy:     policy,
			MaxRetries: m.Spec.RestartPolicy.MaxRetries,
			Backoff:    backoff,
		}
	}

	// Convert placement
	if m.Spec.Placement != nil {
		spec.Placement = &api.PlacementPolicy{
			Regions:        m.Spec.Placement.Regions,
			Zones:          m.Spec.Placement.Zones,
			NodeSelector:   m.Spec.Placement.NodeSelector,
			SpreadTopology: m.Spec.Placement.SpreadTopology,
		}

		// Convert affinity rules
		for _, aff := range m.Spec.Placement.Affinity {
			affType, err := parseAffinityType(aff.Type)
			if err != nil {
				return nil, err
			}
			spec.Placement.Affinity = append(spec.Placement.Affinity, &api.Affinity{
				Type:        affType,
				MatchLabels: aff.MatchLabels,
				TopologyKey: aff.TopologyKey,
				Required:    aff.Required,
			})
		}

		// Convert anti-affinity rules
		for _, aff := range m.Spec.Placement.AntiAffinity {
			affType, err := parseAffinityType(aff.Type)
			if err != nil {
				return nil, err
			}
			spec.Placement.AntiAffinity = append(spec.Placement.AntiAffinity, &api.Affinity{
				Type:        affType,
				MatchLabels: aff.MatchLabels,
				TopologyKey: aff.TopologyKey,
				Required:    aff.Required,
			})
		}
	}

	// Convert volumes
	for _, vol := range m.Spec.Volumes {
		spec.Volumes = append(spec.Volumes, &api.Volume{
			Name:      vol.Name,
			Source:    vol.Source,
			MountPath: vol.MountPath,
			ReadOnly:  vol.ReadOnly,
			Options:   vol.Options,
		})
	}

	// Convert ports
	for _, port := range m.Spec.Ports {
		spec.Ports = append(spec.Ports, &api.Port{
			Name:          port.Name,
			ContainerPort: port.ContainerPort,
			HostPort:      port.HostPort,
			Protocol:      port.Protocol,
		})
	}

	// Convert liveness probe
	if m.Spec.LivenessProbe != nil {
		probe, err := m.Spec.LivenessProbe.ToHealthCheck()
		if err != nil {
			return nil, fmt.Errorf("liveness_probe: %w", err)
		}
		spec.LivenessProbe = probe
	}

	// Convert readiness probe
	if m.Spec.ReadinessProbe != nil {
		probe, err := m.Spec.ReadinessProbe.ToHealthCheck()
		if err != nil {
			return nil, fmt.Errorf("readiness_probe: %w", err)
		}
		spec.ReadinessProbe = probe
	}

	// Build Workload
	workload := &api.Workload{
		Name:        m.Metadata.Name,
		Namespace:   m.Metadata.Namespace,
		Labels:      m.Metadata.Labels,
		Annotations: m.Metadata.Annotations,
		Spec:        spec,
	}

	return workload, nil
}

// ToHealthCheck converts ManifestHealthCheck to api.HealthCheck
func (h *ManifestHealthCheck) ToHealthCheck() (*api.HealthCheck, error) {
	check := &api.HealthCheck{
		SuccessThreshold: h.SuccessThreshold,
		FailureThreshold: h.FailureThreshold,
	}

	// Parse durations
	var err error
	check.InitialDelay, err = parseDuration(h.InitialDelay)
	if err != nil {
		return nil, fmt.Errorf("initial_delay: %w", err)
	}
	check.Period, err = parseDuration(h.Period)
	if err != nil {
		return nil, fmt.Errorf("period: %w", err)
	}
	check.Timeout, err = parseDuration(h.Timeout)
	if err != nil {
		return nil, fmt.Errorf("timeout: %w", err)
	}

	// Determine probe type and convert
	if h.HTTP != nil {
		scheme, err := parseHTTPScheme(h.HTTP.Scheme)
		if err != nil {
			return nil, err
		}
		httpProbe := &api.HTTPProbe{
			Path:   h.HTTP.Path,
			Port:   h.HTTP.Port,
			Scheme: scheme,
		}
		for _, hdr := range h.HTTP.Headers {
			httpProbe.Headers = append(httpProbe.Headers, &api.HTTPHeader{
				Name:  hdr.Name,
				Value: hdr.Value,
			})
		}
		check.Check = &api.HealthCheck_Http{Http: httpProbe}
	} else if h.TCP != nil {
		check.Check = &api.HealthCheck_Tcp{
			Tcp: &api.TCPProbe{
				Port: h.TCP.Port,
			},
		}
	} else if h.Exec != nil {
		check.Check = &api.HealthCheck_Exec{
			Exec: &api.ExecProbe{
				Command: h.Exec.Command,
			},
		}
	} else {
		return nil, fmt.Errorf("health check must specify one of: http, tcp, or exec")
	}

	return check, nil
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
	conn, err := cfg.NewGRPCClient()
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
	conn, err := cfg.NewGRPCClient()
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
