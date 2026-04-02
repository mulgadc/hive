package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfigDir_Dev(t *testing.T) {
	// Without /etc/spinifex should get dev paths
	if _, err := os.Stat("/etc/spinifex"); err == nil {
		t.Skip("test requires /etc/spinifex to not exist")
	}

	homeDir, _ := os.UserHomeDir()
	expected := filepath.Join(homeDir, "spinifex", "config")
	got := DefaultConfigDir()
	if got != expected {
		t.Errorf("DefaultConfigDir() = %q, want %q", got, expected)
	}
}

func TestDefaultDataDir_Dev(t *testing.T) {
	if _, err := os.Stat("/etc/spinifex"); err == nil {
		t.Skip("test requires /etc/spinifex to not exist")
	}

	homeDir, _ := os.UserHomeDir()
	expected := filepath.Join(homeDir, "spinifex")
	got := DefaultDataDir()
	if got != expected {
		t.Errorf("DefaultDataDir() = %q, want %q", got, expected)
	}
}

func TestDefaultConfigFile(t *testing.T) {
	if _, err := os.Stat("/etc/spinifex"); err == nil {
		t.Skip("test requires /etc/spinifex to not exist")
	}

	got := DefaultConfigFile()
	homeDir, _ := os.UserHomeDir()
	expected := filepath.Join(homeDir, "spinifex", "config", "spinifex.toml")
	if got != expected {
		t.Errorf("DefaultConfigFile() = %q, want %q", got, expected)
	}
}

func TestIsProductionLayout_NoEtcSpinifex(t *testing.T) {
	if _, err := os.Stat("/etc/spinifex"); err == nil {
		t.Skip("test requires /etc/spinifex to not exist")
	}

	if isProductionLayout() {
		t.Error("isProductionLayout() = true without /etc/spinifex, want false")
	}
}
