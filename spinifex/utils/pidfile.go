package utils

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

func ReadPidFile(name string) (int, error) {

	pidPath := pidPath()

	pidFile, err := os.ReadFile(filepath.Join(pidPath, fmt.Sprintf("%s.pid", name)))

	if err != nil {
		return 0, err
	}

	// Strip whitespace and /r or /n
	pidFile = bytes.TrimSpace(pidFile)

	return strconv.Atoi(string(pidFile))
}

func GeneratePidFile(name string) (string, error) {

	if name == "" {
		return "", errors.New("name is required")
	}

	pidPath := pidPath()

	if pidPath == "" {
		return "", errors.New("pid path is empty")
	}

	return filepath.Join(pidPath, fmt.Sprintf("%s.pid", name)), nil
}

func WritePidFile(name string, pid int) error {

	// Write PID to file, check XDG, otherwise user home directory ~/spinifex/
	pidFilename, err := GeneratePidFile(name)

	if err != nil {
		return err
	}

	pidFile, err := os.Create(pidFilename)

	if err != nil {
		return err
	}

	defer pidFile.Close()
	_, err = pidFile.WriteString(fmt.Sprintf("%d", pid))
	if err != nil {
		return err
	}

	return nil
}

// WritePidFileTo writes a PID file to a specific directory. If dir is empty,
// falls back to the default pidPath(). Used by services that know their own
// data directory (e.g. predastore's BasePath) to avoid PID file collisions
// when multiple nodes run on the same host.
func WritePidFileTo(dir string, name string, pid int) error {
	if dir == "" {
		return WritePidFile(name, pid)
	}

	pidFilename := filepath.Join(dir, fmt.Sprintf("%s.pid", name))

	pidFile, err := os.Create(pidFilename)
	if err != nil {
		return err
	}

	defer pidFile.Close()
	_, err = pidFile.WriteString(fmt.Sprintf("%d", pid))
	return err
}

// ReadPidFileFrom reads a PID from a file in a specific directory. If dir is
// empty, falls back to the default pidPath().
func ReadPidFileFrom(dir string, name string) (int, error) {
	if dir == "" {
		return ReadPidFile(name)
	}

	data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("%s.pid", name)))
	if err != nil {
		return 0, err
	}

	data = bytes.TrimSpace(data)
	return strconv.Atoi(string(data))
}

// RemovePidFileAt removes a PID file from a specific directory. If dir is
// empty, falls back to the default pidPath().
func RemovePidFileAt(dir string, name string) error {
	if dir == "" {
		return RemovePidFile(name)
	}
	return os.Remove(filepath.Join(dir, fmt.Sprintf("%s.pid", name)))
}

// StopProcessAt stops a process using a PID file in a specific directory.
// If dir is empty, falls back to the default pidPath(). The PID file is
// always removed, even if the process is already dead, to prevent stale
// PID files from accumulating across restarts.
func StopProcessAt(dir string, name string) error {
	pid, err := ReadPidFileFrom(dir, name)
	if err != nil {
		return err
	}

	killErr := KillProcess(pid)

	// Always remove the PID file to avoid stale entries. If the process is
	// already dead, the PID file is stale and must be cleaned up.
	if removeErr := RemovePidFileAt(dir, name); removeErr != nil && killErr == nil {
		return removeErr
	}

	return killErr
}

func RemovePidFile(serviceName string) error {

	pidPath := pidPath()

	err := os.Remove(filepath.Join(pidPath, fmt.Sprintf("%s.pid", serviceName)))
	if err != nil {
		return err
	}

	return nil
}

// RuntimeDir returns the runtime directory used for PID files, sockets, and logs.
func RuntimeDir() string {
	return pidPath()
}

func pidPath() string {
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		return os.Getenv("XDG_RUNTIME_DIR")
	}
	if dirExists(fmt.Sprintf("%s/%s", os.Getenv("HOME"), "spinifex")) {
		return filepath.Join(os.Getenv("HOME"), "spinifex")
	}
	return os.TempDir()
}

// WaitForProcessExit polls until the given PID is no longer alive or the
// timeout expires. Unlike WaitForPidFileRemoval, this checks the process
// itself via kill(pid, 0), so it works after SIGKILL where the target
// cannot clean up its own PID file.
func WaitForProcessExit(pid int, timeout time.Duration) error {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timer.C:
			return fmt.Errorf("timeout waiting for process %d to exit", pid)
		case <-ticker.C:
			proc, err := os.FindProcess(pid)
			if err != nil {
				return nil // process gone
			}
			if proc.Signal(syscall.Signal(0)) != nil {
				return nil // process no longer alive
			}
		}
	}
}

func WaitForPidFileRemoval(instanceID string, timeout time.Duration) error {
	timeoutCh := time.After(timeout)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-timeoutCh:
			return fmt.Errorf("timeout waiting for PID file to be removed for instance %s", instanceID)
		case <-ticker.C:
			_, err := ReadPidFile(instanceID)
			if err != nil {
				// PID file no longer exists
				return nil
			}
		}
	}
}
