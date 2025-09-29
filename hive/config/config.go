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

// Config holds all configuration for the application
type Config struct {
	Daemon     DaemonConfig     `mapstructure:"daemon"`
	NATS       NATSConfig       `mapstructure:"nats"`
	Predastore PredastoreConfig `mapstructure:"predastore"`

	// Authentication
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Host      string `mapstructure:"host"`
	BaseDir   string `mapstructure:"base_dir"`
	WalDir    string `mapstructure:"wal_dir"`
}

type PredastoreConfig struct {
	Host      string `mapstructure:"host"`
	Bucket    string `mapstructure:"bucket"`
	Region    string `mapstructure:"region"`
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	BaseDir   string `mapstructure:"base_dir"`
}

// DaemonConfig holds the daemon configuration
type DaemonConfig struct {
	Host    string `mapstructure:"host"`
	SSLKey  string `mapstructure:"sslkey"`
	SSLCert string `mapstructure:"sslcert"`
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
	Name      string
	VolType   string
	Boot      bool
	EFI       bool
	CloudInit bool
	NBDURI    string
}

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

type EBSDeleteRequest struct {
	Volume string
}

type EBSDeleteResponse struct {
	Volume  string
	Success bool
	Error   string
}

// EC2, TODO: Move to vm.go or more applicable place
type EC2Response struct {
	InstanceID string
	Hostname   string
	Success    bool
	Status     string
	Error      string
}

type EC2DescribeRequest struct {
	InstanceID string
}

type EC2DescribeResponse struct {
	InstanceID string
	Hostname   string
	Status     string
	Error      string
}

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
		slog.Error("Failed to marshal response: %v", err)
		return
	}

	msg.Respond(response)

}
func (ec2DescribeResponse EC2DescribeResponse) Respond(msg *nats.Msg) {

	response, err := json.Marshal(ec2DescribeResponse)
	if err != nil {
		slog.Error("Failed to marshal response: %v", err)
		return
	}

	msg.Respond(response)

}

func (ec2Response EC2Response) Respond(msg *nats.Msg) {

	response, err := json.Marshal(ec2Response)
	if err != nil {
		slog.Error("Failed to marshal response: %v", err)
		return
	}

	msg.Respond(response)

}

// LoadConfig loads the configuration from file and environment variables
func LoadConfig(configPath string) (*Config, error) {
	// Set environment variable prefix
	viper.SetEnvPrefix("HIVE")
	viper.AutomaticEnv()

	// Set default values
	//viper.SetDefault("host", "https://localhost:8443/")
	//viper.SetDefault("base_dir", "/tmp/vb/")

	//viper.SetDefault("daemon.host", "0.0.0.0:4432")
	//viper.SetDefault("nats.host", "0.0.0.0:4222")
	//viper.SetDefault("nats.sub.subject", "ec2.>")

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
	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Validate required fields
	if config.NATS.Host == "" {
		return nil, fmt.Errorf("NATS host is required")
	}

	if config.AccessKey == "" {
		return nil, fmt.Errorf("access key is required")
	}
	if config.SecretKey == "" {
		return nil, fmt.Errorf("secret key is required")
	}

	//	fmt.Println("Config: ", config)

	return &config, nil
}
