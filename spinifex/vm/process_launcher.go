package vm

import "os/exec"

// ProcessLauncher resolves a Config into an *exec.Cmd ready to Start(). The
// real implementation is a 1-line wrapper over Config.Execute(); the seam
// exists so tests can substitute scripted commands without spawning QEMU.
type ProcessLauncher interface {
	Launch(cfg *Config) (*exec.Cmd, error)
}
