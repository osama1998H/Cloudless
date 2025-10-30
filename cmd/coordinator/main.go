package main

import (
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/cloudless/cloudless/pkg/coordinator"
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
	"google.golang.org/grpc/reflection"
)

var (
	// Build information (set via ldflags)
	Version   = "dev"
	BuildTime = "unknown"
	GitCommit = "unknown"

	logger *zap.Logger

	rootCmd = &cobra.Command{
		Use:   "coordinator",
		Short: "Cloudless Coordinator - Control plane for distributed compute",
		Long: `The Cloudless Coordinator manages the control plane for the distributed
compute platform, handling node membership, workload scheduling, and cluster state.`,
		RunE: run,
	}
)

func init() {
	// Set up flags
	rootCmd.PersistentFlags().String("config", "", "Config file path")
	rootCmd.PersistentFlags().String("data-dir", "/var/lib/cloudless", "Data directory for persistent storage")
	rootCmd.PersistentFlags().String("bind-addr", "0.0.0.0:8080", "gRPC server bind address")
	rootCmd.PersistentFlags().String("metrics-addr", "0.0.0.0:9090", "Metrics server bind address")
	rootCmd.PersistentFlags().String("raft-addr", "", "RAFT consensus bind address")
	rootCmd.PersistentFlags().String("raft-id", "", "RAFT node ID")
	rootCmd.PersistentFlags().Bool("raft-bootstrap", false, "Bootstrap new RAFT cluster")
	rootCmd.PersistentFlags().String("log-level", "info", "Log level (debug, info, warn, error)")
	rootCmd.PersistentFlags().Bool("tls-enabled", true, "Enable TLS for gRPC")
	rootCmd.PersistentFlags().String("tls-cert", "", "TLS certificate file")
	rootCmd.PersistentFlags().String("tls-key", "", "TLS key file")
	rootCmd.PersistentFlags().String("tls-ca", "", "TLS CA certificate file")

	// Secrets management flags (CLD-REQ-063)
	rootCmd.PersistentFlags().Bool("secrets-enabled", false, "Enable secrets management service")
	rootCmd.PersistentFlags().String("secrets-master-key", "", "Base64-encoded master encryption key (32 bytes)")
	rootCmd.PersistentFlags().String("secrets-master-key-id", "", "Master key identifier")
	rootCmd.PersistentFlags().String("secrets-token-signing-key", "", "Base64-encoded JWT signing key")
	rootCmd.PersistentFlags().Duration("secrets-token-ttl", 1*time.Hour, "Default TTL for secret access tokens")
	rootCmd.PersistentFlags().Bool("secrets-rotation-enabled", false, "Enable automatic master key rotation")
	rootCmd.PersistentFlags().Duration("secrets-rotation-interval", 90*24*time.Hour, "Master key rotation interval")

	// Bind flags to viper
	viper.BindPFlag("config", rootCmd.PersistentFlags().Lookup("config"))
	viper.BindPFlag("data_dir", rootCmd.PersistentFlags().Lookup("data-dir"))
	viper.BindPFlag("bind_addr", rootCmd.PersistentFlags().Lookup("bind-addr"))
	viper.BindPFlag("metrics_addr", rootCmd.PersistentFlags().Lookup("metrics-addr"))
	viper.BindPFlag("raft.addr", rootCmd.PersistentFlags().Lookup("raft-addr"))
	viper.BindPFlag("raft.id", rootCmd.PersistentFlags().Lookup("raft-id"))
	viper.BindPFlag("raft.bootstrap", rootCmd.PersistentFlags().Lookup("raft-bootstrap"))
	viper.BindPFlag("log_level", rootCmd.PersistentFlags().Lookup("log-level"))
	viper.BindPFlag("tls.enabled", rootCmd.PersistentFlags().Lookup("tls-enabled"))
	viper.BindPFlag("tls.cert", rootCmd.PersistentFlags().Lookup("tls-cert"))
	viper.BindPFlag("tls.key", rootCmd.PersistentFlags().Lookup("tls-key"))
	viper.BindPFlag("tls.ca", rootCmd.PersistentFlags().Lookup("tls-ca"))
	viper.BindPFlag("secrets.enabled", rootCmd.PersistentFlags().Lookup("secrets-enabled"))
	viper.BindPFlag("secrets.master_key", rootCmd.PersistentFlags().Lookup("secrets-master-key"))
	viper.BindPFlag("secrets.master_key_id", rootCmd.PersistentFlags().Lookup("secrets-master-key-id"))
	viper.BindPFlag("secrets.token_signing_key", rootCmd.PersistentFlags().Lookup("secrets-token-signing-key"))
	viper.BindPFlag("secrets.token_ttl", rootCmd.PersistentFlags().Lookup("secrets-token-ttl"))
	viper.BindPFlag("secrets.rotation_enabled", rootCmd.PersistentFlags().Lookup("secrets-rotation-enabled"))
	viper.BindPFlag("secrets.rotation_interval", rootCmd.PersistentFlags().Lookup("secrets-rotation-interval"))

	// Set up environment variable binding
	viper.SetEnvPrefix("CLOUDLESS")
	viper.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	viper.AutomaticEnv()

	// Add version command
	rootCmd.AddCommand(&cobra.Command{
		Use:   "version",
		Short: "Print version information",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("Cloudless Coordinator\n")
			fmt.Printf("  Version:    %s\n", Version)
			fmt.Printf("  Build Time: %s\n", BuildTime)
			fmt.Printf("  Git Commit: %s\n", GitCommit)
			fmt.Printf("  Go Version: %s\n", os.Getenv("GO_VERSION"))
		},
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

	logger.Info("Starting Cloudless Coordinator",
		zap.String("version", Version),
		zap.String("build_time", BuildTime),
		zap.String("git_commit", GitCommit),
	)

	// Create context with cancellation
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Decode secrets keys if provided
	var secretsMasterKey []byte
	var secretsTokenSigningKey []byte
	if masterKeyB64 := viper.GetString("secrets.master_key"); masterKeyB64 != "" {
		var err error
		secretsMasterKey, err = base64.StdEncoding.DecodeString(masterKeyB64)
		if err != nil {
			logger.Warn("Failed to decode secrets master key", zap.Error(err))
		}
	}
	if tokenKeyB64 := viper.GetString("secrets.token_signing_key"); tokenKeyB64 != "" {
		var err error
		secretsTokenSigningKey, err = base64.StdEncoding.DecodeString(tokenKeyB64)
		if err != nil {
			logger.Warn("Failed to decode secrets token signing key", zap.Error(err))
		}
	}

	// Initialize coordinator configuration
	config := &coordinator.Config{
		DataDir:       viper.GetString("data_dir"),
		BindAddr:      viper.GetString("bind_addr"),
		MetricsAddr:   viper.GetString("metrics_addr"),
		RaftAddr:      viper.GetString("raft.addr"),
		RaftID:        viper.GetString("raft.id"),
		RaftBootstrap: viper.GetBool("raft.bootstrap"),
		Logger:        logger,

		// Secrets management configuration (CLD-REQ-063)
		SecretsEnabled:          viper.GetBool("secrets.enabled"),
		SecretsMasterKey:        secretsMasterKey,
		SecretsMasterKeyID:      viper.GetString("secrets.master_key_id"),
		SecretsTokenSigningKey:  secretsTokenSigningKey,
		SecretsTokenTTL:         viper.GetDuration("secrets.token_ttl"),
		SecretsRotationEnabled:  viper.GetBool("secrets.rotation_enabled"),
		SecretsRotationInterval: viper.GetDuration("secrets.rotation_interval"),
	}

	// Validate configuration
	if err := config.Validate(); err != nil {
		return fmt.Errorf("invalid configuration: %w", err)
	}

	// Create coordinator instance
	coord, err := coordinator.New(config)
	if err != nil {
		return fmt.Errorf("failed to create coordinator: %w", err)
	}

	// Generate bootstrap tokens for development/testing (only if explicitly enabled)
	// SECURITY WARNING: Only enable in development environments, never in production
	if os.Getenv("CLOUDLESS_GENERATE_BOOTSTRAP_TOKENS") == "1" {
		logger.Warn("Bootstrap token auto-generation enabled",
			zap.String("security_warning", "DEVELOPMENT ONLY - Never enable in production"),
			zap.String("env_var", "CLOUDLESS_GENERATE_BOOTSTRAP_TOKENS=1"),
		)
		if err := generateBootstrapTokens(coord, logger); err != nil {
			logger.Warn("Failed to generate bootstrap tokens", zap.Error(err))
		}
	}

	// Start metrics server
	metricsServer := startMetricsServer(config.MetricsAddr, logger)

	// Set up gRPC server
	grpcServer, listener, err := setupGRPCServer(config, coord, logger)
	if err != nil {
		return fmt.Errorf("failed to set up gRPC server: %w", err)
	}

	// Start coordinator
	if err := coord.Start(ctx); err != nil {
		return fmt.Errorf("failed to start coordinator: %w", err)
	}

	// Start gRPC server in goroutine
	go func() {
		logger.Info("Starting gRPC server", zap.String("addr", config.BindAddr))
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

	// Stop coordinator
	if err := coord.Stop(shutdownCtx); err != nil {
		logger.Error("Error stopping coordinator", zap.Error(err))
	}

	// Stop metrics server
	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("Error stopping metrics server", zap.Error(err))
	}

	logger.Info("Shutdown complete")
	return nil
}

func setupGRPCServer(config *coordinator.Config, coord *coordinator.Coordinator, logger *zap.Logger) (*grpc.Server, net.Listener, error) {
	// Parse bind address
	listener, err := net.Listen("tcp", config.BindAddr)
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
	coord.RegisterServices(grpcServer)

	// Register health check service
	healthServer := health.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, healthServer)
	healthServer.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)

	// Register reflection service for debugging
	reflection.Register(grpcServer)

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

// generateBootstrapTokens generates bootstrap tokens for development/testing
func generateBootstrapTokens(coord *coordinator.Coordinator, logger *zap.Logger) error {
	tokenMgr := coord.GetTokenManager()
	if tokenMgr == nil {
		return fmt.Errorf("token manager not initialized")
	}

	// Define agents that need tokens
	agents := []struct {
		NodeID   string
		NodeName string
		Region   string
		Zone     string
	}{
		{"agent-1", "agent-1", "local", "zone-a"},
		{"agent-2", "agent-2", "local", "zone-b"},
		{"agent-3", "agent-3", "local", "zone-a"},
	}

	// Generate tokens for each agent
	for _, agent := range agents {
		token, err := tokenMgr.GenerateToken(
			agent.NodeID,
			agent.NodeName,
			agent.Region,
			agent.Zone,
			365*24*time.Hour, // 1 year validity
			999,              // max uses
		)
		if err != nil {
			logger.Warn("Failed to generate bootstrap token",
				zap.String("node_id", agent.NodeID),
				zap.Error(err),
			)
			continue
		}

		logger.Info("Generated bootstrap token",
			zap.String("node_id", agent.NodeID),
			zap.String("node_name", agent.NodeName),
			zap.String("token_id", token.ID),
			zap.Time("expires_at", token.ExpiresAt),
		)
	}

	return nil
}
