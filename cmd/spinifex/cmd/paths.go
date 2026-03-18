package cmd

import (
	"os"
	"path/filepath"
)

// DefaultConfigDir returns the default configuration directory.
// Production: /etc/spinifex (when running as root or /etc/spinifex exists)
// Development: ~/spinifex/config
func DefaultConfigDir() string {
	if isProductionLayout() {
		return "/etc/spinifex"
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, "spinifex", "config")
}

// DefaultDataDir returns the default data directory.
// Production: /var/lib/spinifex
// Development: ~/spinifex
func DefaultDataDir() string {
	if isProductionLayout() {
		return "/var/lib/spinifex"
	}
	homeDir, _ := os.UserHomeDir()
	return filepath.Join(homeDir, "spinifex")
}

// DefaultConfigFile returns the default path to spinifex.toml.
func DefaultConfigFile() string {
	return filepath.Join(DefaultConfigDir(), "spinifex.toml")
}

// isProductionLayout returns true when running in a production install.
// Detected by: running as root, or /etc/spinifex directory exists.
func isProductionLayout() bool {
	if os.Getuid() == 0 {
		if info, err := os.Stat("/etc/spinifex"); err == nil && info.IsDir() {
			return true
		}
	}
	return false
}
