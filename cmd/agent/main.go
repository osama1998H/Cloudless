package main

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/cloudless/cloudless/pkg/agent"
	"github.com/cloudless/cloudless/pkg/observability"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/keepalive"
)

var (
	// Build information (set via ldflags)
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"

	logger *zap.Logger

	rootCmd = &cobra.Command{
		Use:   "agent",
		Short: "Cloudless Agent - Node agent for distributed compute",
		Long: `The Cloudless Agent runs on each node in the distributed compute platform,
managing local resources, running containers, and reporting status to the coordinator.`,
		RunE: run,
	}
)

func init() {
	// Set up flags
	rootCmd.PersistentFlags().String("config", "", "Config file path")
	rootCmd.PersistentFlags().String("data-dir", "/var/lib/cloudless-agent", "Data directory for local storage")
	rootCmd.PersistentFlags().String("coordinator-addr", "localhost:8080", "Coordinator address")
	rootCmd.PersistentFlags().String("agent-addr", "0.0.0.0:8090", "Agent gRPC server bind address")
	rootCmd.PersistentFlags().String("metrics-addr", "0.0.0.0:9090", "Metrics server bind address")
	rootCmd.PersistentFlags().String("node-id", "", "Unique node identifier")
	rootCmd.PersistentFlags().String("node-name", "", "Node display name")
	rootCmd.PersistentFlags().String("region", "", "Node region")
	rootCmd.PersistentFlags().String("zone", "", "Node availability zone")
	rootCmd.PersistentFlags().String("join-token", "", "Token for enrolling with coordinator")
	rootCmd.PersistentFlags().String("log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().Bool("tls-enabled", true, "Enable TLS for gRPC")
	rootCmd.PersistentFlags().String("tls-cert", "", "TLS certificate file")
	rootCmd.PersistentFlags().String("tls-key", "", "TLS key file")
	rootCmd.PersistentFlags().String("tls-ca", "", "TLS CA certificate file")
	rootCmd.PersistentFlags().String("container-runtime", "containerd", "Container runtime (containerd, docker)")
	rootCmd.PersistentFlags().String("container-socket", "/run/containerd/containerd.sock", "Container runtime socket path")
	rootCmd.PersistentFlags().Duration("heartbeat-interval", 10*time.Second, "Heartbeat interval")
	rootCmd.PersistentFlags().Int("cpu-cores", 0, "Number of CPU cores (0 for auto-detect)")
	rootCmd.PersistentFlags().Int("memory-mb", 0, "Memory in MB (0 for auto-detect)")
	rootCmd.PersistentFlags().Int("storage-gb", 0, "Storage in GB (0 for auto-detect)")
	rootCmd.PersistentFlags().Int("bandwidth-mbps", 0, "Bandwidth in Mbps (0 for auto-detect)")

	// Bind flags to viper
	viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config"))
	viper.BindPFlag("data_dir", rootCmd.PersistentFlags().Lookup("data-dir"))
	viper.BindPFlag("coordinator_addr", rootCmd.PersistentFlags().Lookup("coordinator-addr"))
	viper.BindPFlag("agent_addr", rootCmd.PersistentFlags().Lookup("agent-addr"))
	viper.BindPFlag("metrics_addr", rootCmd.PersistentFlags().Lookup("metrics-addr"))
	viper.BindPFlag("node_id", rootCmd.PersistentFlags().Lookup("node-id"))
	viper.BindPFlag("node_name", rootCmd.PersistentFlags().Lookup("node-name"))
	viper.BindPFlag("region", rootCmd.PersistentFlags().Lookup("region"))
	viper.BindPFlag("zone", rootCmd.PersistentFlags().Lookup("zone"))
	viper.BindPFlag("join_token", rootCmd.PersistentFlags().Lookup("join-token"))
	viper.BindPFlag("log_level", rootCmd.PersistentFlags().Lookup("log-level"))
	viper.BindPFlag("tls.enabled", rootCmd.PersistentFlags().Lookup("tls-enabled"))
	viper.BindPFlag("tls.cert", rootCmd.PersistentFlags().Lookup("tls-cert"))
	viper.BindPFlag("tls.key", rootCmd.PersistentFlags().Lookup("tls-key"))
	viper.BindPFlag("tls.ca", rootCmd.PersistentFlags().Lookup("tls-ca"))
	viper.BindPFlag("container.runtime", rootCmd.PersistentFlags().Lookup("container-runtime"))
	viper.BindPFlag("container.socket", rootCmd.PersistentFlags().Lookup("container-socket"))
	viper.BindPFlag("heartbeat_interval", rootCmd.PersistentFlags().Lookup("heartbeat-interval"))
	viper.BindPFlag("resources.cpu_cores", rootCmd.PersistentFlags().Lookup("cpu-cores"))
	viper.BindPFlag("resources.memory_mb", rootCmd.PersistentFlags().Lookup("memory-mb"))
	viper.BindPFlag("resources.storage_gb", rootCmd.PersistentFlags().Lookup("storage-gb"))
	viper.BindPFlag("resources.bandwidth_mbps", rootCmd.PersistentFlags().Lookup("bandwidth-mbps"))

	// Set up environment variable binding
	viper.SetEnvPrefix("CLOUDLESS")
	viper.AutomaticEnv()

	// Add version command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Cloudless Agent\n")
			fmt.Printf("  Version:    %s\n", Version)
			fmt.Printf("  Build Time: %s\n", BuildTime)
			fmt.Printf("  Git Commit: %s\n", GitCommit)
			fmt.Printf("  Go Version: %s\n", runtime.Version())
			fmt.Printf("  OS/Arch:    %s/%s\n", runtime.GOOS, runtime.GOARCH)
		},
	})

	// Add inspect command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "inspect",
		Short: "Inspect node capabilities and resources",
		RunE:  inspect,
	})
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run(cmd *cobra.Command, args []string) error {
	// Load configuration
	if configFile := viper.GetString("config"); configFile != "" {
		viper.SetConfigFile(configFile)
		if err := viper.ReadInConfig(); err != nil {
			return fmt.Errorf("failed to read config file: %w", err)
		}
	}

	// Initialize logger
	var err error
	logger, err = observability.NewLogger(viper.GetString("log_level"))
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer logger.Sync()

	logger.Info("Starting Cloudless Agent",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("git_commit", GitCommit),
		zap.String("os", runtime.GOOS),
		zap.String("arch", runtime.GOARCH),
	)

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Auto-detect resources if not specified
	resources := detectResources(logger)

	// Initialize agent configuration
	config := &agent.Config{
		DataDir:          viper.GetString("data_dir"),
		CoordinatorAddr:  viper.GetString("coordinator_addr"),
		AgentAddr:        viper.GetString("agent_addr"),
		MetricsAddr:      viper.GetString("metrics_addr"),
		NodeID:           viper.GetString("node_id"),
		NodeName:         viper.GetString("node_name"),
		Region:           viper.GetString("region"),
		Zone:             viper.GetString("zone"),
		JoinToken:        viper.GetString("join_token"),
		HeartbeatInterval: viper.GetDuration("heartbeat_interval"),
		ContainerRuntime: viper.GetString("container.runtime"),
		ContainerSocket:  viper.GetString("container.socket"),
		Resources:        resources,
		Logger:           logger,
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Create agent instance
	agentInstance, err := agent.New(config)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	// Start metrics server
	metricsServer := startMetricsServer(config.MetricsAddr, logger)

	// Set up gRPC server for agent API
	grpcServer, listener, err := setupGRPCServer(config, agentInstance, logger)
	if err != nil {
		return fmt.Errorf("failed to set up gRPC server: %w", err)
	}

	// Start agent (connects to coordinator)
	if err := agentInstance.Start(ctx); err != nil {
		return fmt.Errorf("failed to start agent: %w", err)
	}

	// Start gRPC server in goroutine
	go func() {
		logger.Info("Starting agent gRPC server", zap.String("addr", config.AgentAddr))
		if err := grpcServer.Serve(listener); err != nil {
			logger.Fatal("Failed to serve gRPC", zap.Error(err))
		}
	}()

	// Wait for shutdown signal
	select {
	case <-sigChan:
		logger.Info("Received shutdown signal")
	case <-ctx.Done():
		logger.Info("Context cancelled")
	}

	// Graceful shutdown
	logger.Info("Starting graceful shutdown...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	// Stop accepting new connections
	grpcServer.GracefulStop()

	// Stop agent
	if err := agentInstance.Stop(shutdownCtx); err != nil {
		logger.Error("Error stopping agent", zap.Error(err))
	}

	// Stop metrics server
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("Error stopping metrics server", zap.Error(err))
	}

	logger.Info("Shutdown complete")
	return nil
}

func setupGRPCServer(config *agent.Config, agentInstance *agent.Agent, logger *zap.Logger) (*grpc.Server, net.Listener, error) {
	// Parse bind address
	listener, err := net.Listen("tcp", config.AgentAddr)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to listen: %w", err)
	}

	// Set up gRPC server options
	opts := []grpc.ServerOption{
		grpc.KeepaliveParams(keepalive.ServerParameters{
			Time:    30 * time.Second,
			Timeout: 10 * time.Second,
		}),
		grpc.KeepaliveEnforcementPolicy(keepalive.EnforcementPolicy{
			MinTime:             5 * time.Second,
			PermitWithoutStream: true,
		}),
	}

	// Add TLS if enabled
	if viper.GetBool("tls.enabled") {
		certFile := viper.GetString("tls.cert")
		keyFile := viper.GetString("tls.key")

		if certFile == "" || keyFile == "" {
			return nil, nil, fmt.Errorf("TLS enabled but cert/key not provided")
		}

		creds, err := credentials.NewServerTLSFromFile(certFile, keyFile)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		opts = append(opts, grpc.Creds(creds))
	}

	// Add interceptors
	opts = append(opts,
		grpc.ChainUnaryInterceptor(
			observability.UnaryServerInterceptor(logger),
			observability.UnaryMetricsInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			observability.StreamServerInterceptor(logger),
			observability.StreamMetricsInterceptor(),
		),
	)

	// Create gRPC server
	grpcServer := grpc.NewServer(opts...)

	// Register services
	agentInstance.RegisterServices(grpcServer)

	// Register health check service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	return grpcServer, listener, nil
}

func startMetricsServer(addr string, logger *zap.Logger) *http.Server {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})

	server := &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		logger.Info("Starting metrics server", zap.String("addr", addr))
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Metrics server error", zap.Error(err))
		}
	}()

	return server
}

func detectResources(logger *zap.Logger) agent.Resources {
	resources := agent.Resources{}

	// CPU cores detection
	if cpuCores := viper.GetInt("resources.cpu_cores"); cpuCores > 0 {
		resources.CPUCores = cpuCores
	} else {
		resources.CPUCores = runtime.NumCPU()
	}

	// Memory detection (simplified - would use more sophisticated methods in production)
	if memoryMB := viper.GetInt("resources.memory_mb"); memoryMB > 0 {
		resources.MemoryMB = memoryMB
	} else {
		// Default to 4GB for now, would use syscall or /proc/meminfo in production
		resources.MemoryMB = 4096
	}

	// Storage detection
	if storageGB := viper.GetInt("resources.storage_gb"); storageGB > 0 {
		resources.StorageGB = storageGB
	} else {
		// Default to 100GB for now, would use df or statfs in production
		resources.StorageGB = 100
	}

	// Bandwidth detection
	if bandwidthMbps := viper.GetInt("resources.bandwidth_mbps"); bandwidthMbps > 0 {
		resources.BandwidthMbps = bandwidthMbps
	} else {
		// Default to 100Mbps for now, would use network speed test in production
		resources.BandwidthMbps = 100
	}

	logger.Info("Detected resources",
		zap.Int("cpu_cores", resources.CPUCores),
		zap.Int("memory_mb", resources.MemoryMB),
		zap.Int("storage_gb", resources.StorageGB),
		zap.Int("bandwidth_mbps", resources.BandwidthMbps),
	)

	return resources
}

func inspect(cmd *cobra.Command, args []string) error {
	// Initialize logger
	logger, err := observability.NewLogger("info")
	if err != nil {
		return fmt.Errorf("failed to initialize logger: %w", err)
	}
	defer logger.Sync()

	// Detect resources
	resources := detectResources(logger)

	// Print system information
	fmt.Println("Node Inspection Report")
	fmt.Println("======================")
	fmt.Printf("Operating System: %s\n", runtime.GOOS)
	fmt.Printf("Architecture: %s\n", runtime.GOARCH)
	fmt.Printf("Go Version: %s\n", runtime.Version())
	fmt.Println("\nDetected Resources:")
	fmt.Printf("  CPU Cores: %d\n", resources.CPUCores)
	fmt.Printf("  Memory: %d MB\n", resources.MemoryMB)
	fmt.Printf("  Storage: %d GB\n", resources.StorageGB)
	fmt.Printf("  Bandwidth: %d Mbps\n", resources.BandwidthMbps)

	// Check container runtime
	fmt.Println("\nContainer Runtime:")
	runtime := viper.GetString("container.runtime")
	socket := viper.GetString("container.socket")
	fmt.Printf("  Runtime: %s\n", runtime)
	fmt.Printf("  Socket: %s\n", socket)

	if _, err := os.Stat(socket); os.IsNotExist(err) {
		fmt.Printf("  Status: NOT FOUND (socket does not exist)\n")
	} else {
		fmt.Printf("  Status: Found\n")
	}

	return nil
}