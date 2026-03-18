package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigDir_Dev(t *testing.T) {
	// Non-root user without /etc/spinifex should get dev paths
	if os.Getuid() == 0 {
		t.Skip("test requires non-root user")
	}

	homeDir, _ := os.UserHomeDir()
	expected := filepath.Join(homeDir, "spinifex", "config")
	got := DefaultConfigDir()
	if got != expected {
		t.Errorf("DefaultConfigDir() = %q, want %q", got, expected)
	}
}

func TestDefaultDataDir_Dev(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root user")
	}

	homeDir, _ := os.UserHomeDir()
	expected := filepath.Join(homeDir, "spinifex")
	got := DefaultDataDir()
	if got != expected {
		t.Errorf("DefaultDataDir() = %q, want %q", got, expected)
	}
}

func TestDefaultConfigFile(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root user")
	}

	got := DefaultConfigFile()
	homeDir, _ := os.UserHomeDir()
	expected := filepath.Join(homeDir, "spinifex", "config", "spinifex.toml")
	if got != expected {
		t.Errorf("DefaultConfigFile() = %q, want %q", got, expected)
	}
}

func TestIsProductionLayout_NonRoot(t *testing.T) {
	if os.Getuid() == 0 {
		t.Skip("test requires non-root user")
	}

	if isProductionLayout() {
		t.Error("isProductionLayout() = true for non-root user, want false")
	}
}
