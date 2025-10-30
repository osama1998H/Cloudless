package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/osama1998H/Cloudless/pkg/api"
)

// Config holds CLI configuration
type Config struct {
	Coordinator string `mapstructure:"coordinator"`
	CertFile    string `mapstructure:"cert_file"`
	KeyFile     string `mapstructure:"key_file"`
	CAFile      string `mapstructure:"ca_file"`
	Insecure    bool   `mapstructure:"insecure"`
}

// LoadConfig loads configuration from file and flags
func LoadConfig(cmd *cobra.Command) (*Config, error) {
	cfg := &Config{}

	// Get config file path
	configFile, _ := cmd.Flags().GetString("config")
	if configFile == "" {
		// Default to $HOME/.cloudless/config.yaml
		home, err := os.UserHomeDir()
		if err == nil {
			configFile = filepath.Join(home, ".cloudless", "config.yaml")
		}
	}

	// Set up viper
	viper.SetConfigFile(configFile)
	viper.SetConfigType("yaml")
	viper.AutomaticEnv()
	viper.SetEnvPrefix("CLOUDLESS")

	// Read config file if it exists
	if configFile != "" {
		if err := viper.ReadInConfig(); err != nil {
			if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
				return nil, fmt.Errorf("failed to read config file: %w", err)
			}
		}
	}

	// Unmarshal config
	if err := viper.Unmarshal(cfg); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Override with flags
	if coordinator, _ := cmd.Flags().GetString("coordinator"); coordinator != "" {
		cfg.Coordinator = coordinator
	}
	if insecure, _ := cmd.Flags().GetBool("insecure"); insecure {
		cfg.Insecure = true
	}

	// Set default coordinator if not specified
	if cfg.Coordinator == "" {
		cfg.Coordinator = "localhost:8443"
	}

	return cfg, nil
}

// NewGRPCClient creates a new gRPC client connection
func (c *Config) NewGRPCClient() (*grpc.ClientConn, error) {
	var opts []grpc.DialOption

	if c.Insecure {
		// Use insecure connection (for development)
		opts = append(opts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	} else {
		// Use mTLS
		tlsConfig, err := c.loadTLSConfig()
		if err != nil {
			return nil, fmt.Errorf("failed to load TLS config: %w", err)
		}
		opts = append(opts, grpc.WithTransportCredentials(credentials.NewTLS(tlsConfig)))
	}

	conn, err := grpc.Dial(c.Coordinator, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to coordinator: %w", err)
	}

	return conn, nil
}

// NewCoordinatorClient creates a new coordinator service client
func (c *Config) NewCoordinatorClient() (api.CoordinatorServiceClient, *grpc.ClientConn, error) {
	conn, err := c.NewGRPCClient()
	if err != nil {
		return nil, nil, err
	}

	client := api.NewCoordinatorServiceClient(conn)
	return client, conn, nil
}

// NewStorageClient creates a new storage service client
func (c *Config) NewStorageClient() (api.StorageServiceClient, *grpc.ClientConn, error) {
	conn, err := c.NewGRPCClient()
	if err != nil {
		return nil, nil, err
	}

	client := api.NewStorageServiceClient(conn)
	return client, conn, nil
}

// NewNetworkClient creates a new network service client
func (c *Config) NewNetworkClient() (api.NetworkServiceClient, *grpc.ClientConn, error) {
	conn, err := c.NewGRPCClient()
	if err != nil {
		return nil, nil, err
	}

	client := api.NewNetworkServiceClient(conn)
	return client, conn, nil
}

// loadTLSConfig loads TLS configuration for mTLS
func (c *Config) loadTLSConfig() (*tls.Config, error) {
	// Load client cert
	cert, err := tls.LoadX509KeyPair(c.CertFile, c.KeyFile)
	if err != nil {
		return nil, fmt.Errorf("failed to load client cert: %w", err)
	}

	// Load CA cert
	caCert, err := os.ReadFile(c.CAFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read CA cert: %w", err)
	}

	caCertPool := x509.NewCertPool()
	if !caCertPool.AppendCertsFromPEM(caCert) {
		return nil, fmt.Errorf("failed to append CA cert")
	}

	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
		RootCAs:      caCertPool,
		MinVersion:   tls.VersionTLS13,
	}

	return tlsConfig, nil
}
