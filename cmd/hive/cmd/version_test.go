package cmd

import (
	"testing"
)

func TestVersionVarsExist(t *testing.T) {
	// Verify the version variables are set (defaults when not injected via ldflags)
	if Version == "" {
		t.Error("Version should not be empty")
	}
	if Commit == "" {
		t.Error("Commit should not be empty")
	}
}

func TestVersionCommand(t *testing.T) {
	// Verify the version command is registered
	found := false
	for _, cmd := range rootCmd.Commands() {
		if cmd.Use == "version" {
			found = true
			break
		}
	}
	if !found {
		t.Error("version command not registered on rootCmd")
	}
}
