package config

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"sync"

	"github.com/nats-io/nats.go"
	"github.com/spf13/viper"
)

type ClusterConfig struct {
	Epoch   uint64            `mapstructure:"epoch"`   // bump when leader commits changes
	Node    string            `mapstructure:"node"`    // my node name
	Version string            `mapstructure:"version"` // hive version
	Nodes   map[string]Config `mapstructure:"nodes"`   // full config for every node
}

// Config holds all configuration for the application
type Config struct {
	// Node config
	Node    string `mapstructure:"node"`
	Host    string `mapstructure:"host"` // Unique hostname or IP of this node
	Region  string `mapstructure:"region"`
	AZ      string `mapstructure:"az"`
	DataDir string `mapstructure:"data_dir"`

	Daemon     DaemonConfig     `mapstructure:"daemon"`
	NATS       NATSConfig       `mapstructure:"nats"`
	Predastore PredastoreConfig `mapstructure:"predastore"`
	AWSGW      AWSGWConfig      `mapstructure:"awsgw"`

	// Authentication
	// TODO: Move to more appropriate setting above
	AccessKey string `mapstructure:"accesskey"`
	SecretKey string `mapstructure:"secretkey"`
	BaseDir   string `mapstructure:"base_dir"`
	WalDir    string `mapstructure:"wal_dir"`
}

type AWSGWConfig struct {
	Host    string `mapstructure:"host"`
	TLSKey  string `mapstructure:"tlskey"`
	TLSCert string `mapstructure:"tlscert"`
	Config  string `mapstructure:"config"`

	Debug         bool `mapstructure:"debug"`
	ExpectedNodes int  `mapstructure:"expected_nodes"` // TODO: Replace with root cluster config
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
	Host    string `mapstructure:"host"`
	TLSKey  string `mapstructure:"tlskey"`
	TLSCert string `mapstructure:"tlscert"`
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

type EBSRequests struct {
	Requests []EBSRequest `mapstructure:"ebs_requests"`
	Mu       sync.Mutex   `json:"-"`
}

type EBSRequest struct {
	Name                string
	VolType             string
	Boot                bool
	EFI                 bool
	CloudInit           bool
	DeleteOnTermination bool
	NBDURI              string // NBD URI - socket path (nbd:unix:/path.sock) or TCP (nbd://host:port)
	DeviceName          string // Device name (e.g. /dev/sdf) for hot-plugged volumes
}

// NBDTransport defines the transport type for NBD connections
type NBDTransport string

const (
	// NBDTransportSocket uses Unix domain sockets (faster, local only)
	NBDTransportSocket NBDTransport = "socket"
	// NBDTransportTCP uses TCP connections (required for remote/DPU scenarios)
	NBDTransportTCP NBDTransport = "tcp"
)

type EBSMountResponse struct {
	URI     string
	Mounted bool
	Error   string
}

type EBSUnMountResponse struct {
	Volume  string
	Mounted bool
	Error   string
}

type EBSSyncRequest struct {
	Volume string
}

type EBSSyncResponse struct {
	Volume string
	Synced bool
	Error  string
}

type EBSDeleteRequest struct {
	Volume string
}

type EBSDeleteResponse struct {
	Volume  string
	Success bool
	Error   string
}

type EBSSnapshotRequest struct {
	Volume     string
	SnapshotID string
}

type EBSSnapshotResponse struct {
	SnapshotID string
	Success    bool
	Error      string
}

// EC2, TODO: Move to vm.go or more applicable place
type EC2StartInstancesRequest struct {
	InstanceID string
}

type EC2StartInstancesResponse struct {
	InstanceID string
	Status     string
	Error      string
}

// TODO: Make a generic function for the response
func (ec2StartInstanceResponse EC2StartInstancesResponse) Respond(msg *nats.Msg) {

	response, err := json.Marshal(ec2StartInstanceResponse)
	if err != nil {
		slog.Error("Failed to marshal response", "err", err)
		return
	}

	if err := msg.Respond(response); err != nil {
		slog.Error("Failed to respond to NATS request", "err", err)
	}

}

// Cluster manager API types
type NodeJoinRequest struct {
	Node       string `json:"node"`
	Region     string `json:"region"`
	AZ         string `json:"az"`
	DataDir    string `json:"data_dir"`
	DaemonHost string `json:"daemon_host"` // Host:port where this node's daemon is accessible
}

// SharedClusterData contains only the shared cluster information (no node-specific top-level fields)
type SharedClusterData struct {
	Epoch   uint64            `json:"epoch" toml:"epoch"`
	Version string            `json:"version" toml:"version"`
	Nodes   map[string]Config `json:"nodes" toml:"nodes"`
}

type NodeJoinResponse struct {
	Success     bool               `json:"success"`
	Message     string             `json:"message"`
	SharedData  *SharedClusterData `json:"shared_data,omitempty"`
	ConfigHash  string             `json:"config_hash,omitempty"`
	JoiningNode string             `json:"joining_node,omitempty"` // The node name that is joining
	// CA distribution for per-node certificate generation
	CACert string `json:"ca_cert,omitempty"` // PEM-encoded CA certificate
	CAKey  string `json:"ca_key,omitempty"`  // PEM-encoded CA private key
	// Predastore config distribution for multi-node clusters
	PredastoreConfig string `json:"predastore_config,omitempty"` // Full predastore.toml content
}

type NodeHealthResponse struct {
	Node       string `json:"node"`
	Status     string `json:"status"`
	ConfigHash string `json:"config_hash"`
	Epoch      uint64 `json:"epoch"`
	Uptime     int64  `json:"uptime"`
}

// LoadConfig loads the configuration from file and environment variables
func LoadConfig(configPath string) (*ClusterConfig, error) {
	// Set environment variable prefix
	viper.SetEnvPrefix("HIVE")
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
			fmt.Fprintf(os.Stderr, "Using config file: %s\n", viper.ConfigFileUsed())
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
