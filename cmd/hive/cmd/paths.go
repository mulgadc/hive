package cmd

import (
	"os"
	"path/filepath"
)

// DefaultConfigDir returns the default configuration directory.
// Production: /etc/hive (when running as root or /etc/hive exists)
// Development: ~/hive/config
func DefaultConfigDir() string {
	if isProductionLayout() {
		return "/etc/hive"
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, "hive", "config")
}

// DefaultDataDir returns the default data directory.
// Production: /var/lib/hive
// Development: ~/hive
func DefaultDataDir() string {
	if isProductionLayout() {
		return "/var/lib/hive"
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, "hive")
}

// DefaultConfigFile returns the default path to hive.toml.
func DefaultConfigFile() string {
	return filepath.Join(DefaultConfigDir(), "hive.toml")
}

// isProductionLayout returns true when running in a production install.
// Detected by: running as root, or /etc/hive directory exists.
func isProductionLayout() bool {
	if os.Getuid() == 0 {
		if info, err := os.Stat("/etc/hive"); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
