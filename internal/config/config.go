package config

import (
	"fmt"
	"os"

	"github.com/spf13/viper"
)

// Config holds all configuration for the application
type Config struct {
	Daemon DaemonConfig `mapstructure:"daemon"`
	NATS   NATSConfig   `mapstructure:"nats"`

	// Authentication
	AccessKey string `mapstructure:"access_key"`
	SecretKey string `mapstructure:"secret_key"`
	Host      string `mapstructure:"host"`
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

// LoadConfig loads the configuration from file and environment variables
func LoadConfig(configPath string) (*Config, error) {
	// Set environment variable prefix
	viper.SetEnvPrefix("HIVE")
	viper.AutomaticEnv()

	// Set default values
	viper.SetDefault("host", "https://localhost:8443/")
	viper.SetDefault("base_dir", "/tmp/vb/")

	viper.SetDefault("daemon.host", "0.0.0.0:4432")
	viper.SetDefault("nats.host", "0.0.0.0:4222")
	viper.SetDefault("nats.sub.subject", "ec2.>")

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

	return &config, nil
}
