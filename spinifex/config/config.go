package config

import (
	"fmt"
	"log/slog"
	"os"
	"slices"

	"github.com/spf13/viper"
)

type ClusterConfig struct {
	Epoch   uint64            `mapstructure:"epoch"`   // bump when leader commits changes
	Node    string            `mapstructure:"node"`    // my node name
	Version string            `mapstructure:"version"` // spinifex version
	Nodes   map[string]Config `mapstructure:"nodes"`   // full config for every node
}

// Config holds all configuration for the application
type Config struct {
	// Node config
	Node     string   `mapstructure:"node"`
	Host     string   `mapstructure:"host"` // Unique hostname or IP of this node
	Region   string   `mapstructure:"region"`
	AZ       string   `mapstructure:"az"`
	DataDir  string   `mapstructure:"data_dir"`
	Services []string `mapstructure:"services"` // Which services this node runs locally

	Daemon     DaemonConfig     `mapstructure:"daemon"`
	NATS       NATSConfig       `mapstructure:"nats"`
	Predastore PredastoreConfig `mapstructure:"predastore"`
	Viperblock ViperblockConfig `mapstructure:"viperblock"`
	AWSGW      AWSGWConfig      `mapstructure:"awsgw"`
	VPCD       VPCDConfig       `mapstructure:"vpcd"`

	BaseDir string `mapstructure:"base_dir"`
	WalDir  string `mapstructure:"wal_dir"`
}

type AWSGWConfig struct {
	Host    string `mapstructure:"host"`
	TLSKey  string `mapstructure:"tlskey"`
	TLSCert string `mapstructure:"tlscert"`
	Config  string `mapstructure:"config"`

	Debug         bool `mapstructure:"debug"`
	ExpectedNodes int  `mapstructure:"expected_nodes"` // TODO: Replace with root cluster config
}

type ViperblockConfig struct {
	ShardWAL *bool `mapstructure:"shardwal"` // Enable sharded WAL (default false when nil)
}

// VPCDConfig holds the VPC daemon (vpcd) configuration.
type VPCDConfig struct {
	OVNNBAddr string `mapstructure:"ovn_nb_addr"` // OVN Northbound DB address (e.g., "tcp:127.0.0.1:6641")
	OVNSBAddr string `mapstructure:"ovn_sb_addr"` // OVN Southbound DB address (e.g., "tcp:127.0.0.1:6642")
}

type PredastoreConfig struct {
	Host      string `mapstructure:"host"`
	Bucket    string `mapstructure:"bucket"`
	Region    string `mapstructure:"region"`
	AccessKey string `mapstructure:"accesskey"`
	SecretKey string `mapstructure:"secretkey"`
	BaseDir   string `mapstructure:"base_dir"`
	NodeID    int    `mapstructure:"node_id"`
}

// DaemonConfig holds the daemon configuration
type DaemonConfig struct {
	Host          string `mapstructure:"host"`
	TLSKey        string `mapstructure:"tlskey"`
	TLSCert       string `mapstructure:"tlscert"`
	DevNetworking bool   `mapstructure:"dev_networking"` // VPC instances get both TAP + hostfwd for SSH dev access
}

// NATSConfig holds the NATS configuration
type NATSConfig struct {
	Host string  `mapstructure:"host"`
	ACL  NATSACL `mapstructure:"acl"`
	Sub  NATSSub `mapstructure:"sub"`
}

// NATSACL holds the NATS ACL configuration
type NATSACL struct {
	Token string `mapstructure:"token"`
}

// NATSSub holds the NATS subscription configuration
type NATSSub struct {
	Subject string `mapstructure:"subject"`
}

// NodeBaseDir returns the BaseDir for the current node, or "" if the config
// is nil, the node name is unset, or the node is not found in the Nodes map.
func (cc *ClusterConfig) NodeBaseDir() string {
	if cc == nil || cc.Node == "" {
		slog.Warn("NodeBaseDir: no config or node name set, using global PID path")
		return ""
	}
	node, ok := cc.Nodes[cc.Node]
	if !ok {
		slog.Error("NodeBaseDir: node not found in config", "node", cc.Node)
		return ""
	}
	if node.BaseDir == "" {
		slog.Warn("NodeBaseDir: BaseDir is empty for node, using global PID path", "node", cc.Node)
	}
	return node.BaseDir
}

// AllServices is the default service list when Services is empty (backward compat).
var AllServices = []string{"nats", "predastore", "viperblock", "daemon", "awsgw", "vpcd", "ui"}

// HasService reports whether the node runs the named service.
// An empty Services list means all services (backward compat).
func (c Config) HasService(name string) bool {
	services := c.Services
	if len(services) == 0 {
		services = AllServices
	}
	return slices.Contains(services, name)
}

// GetServices returns the configured service list, defaulting to AllServices.
func (c Config) GetServices() []string {
	if len(c.Services) == 0 {
		return AllServices
	}
	return c.Services
}

// LoadConfig loads the configuration from file and environment variables
func LoadConfig(configPath string) (*ClusterConfig, error) {
	// Set environment variable prefix
	viper.SetEnvPrefix("SPINIFEX")
	viper.AutomaticEnv()

	// Try to load config file if it exists
	if configPath != "" {
		// Check if file exists
		if _, err := os.Stat(configPath); err == nil {
			viper.SetConfigFile(configPath)
			viper.SetConfigType("toml")

			if err := viper.ReadInConfig(); err != nil {
				return nil, fmt.Errorf("error reading config file: %w", err)
			}
			//fmt.Fprintf(os.Stderr, "Using config file: %s\n", viper.ConfigFileUsed())

		} else {
			fmt.Fprintf(os.Stderr, "Config file not found: %s, using environment variables and defaults\n", configPath)
		}
	}

	// Create config struct
	var config ClusterConfig
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	return &config, nil
}
