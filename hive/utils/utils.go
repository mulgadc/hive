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

func GenerateSocketFile(name string) (string, error) {

	if name == "" {
		return "", errors.New("name is required")
	}

	pidPath := pidPath()

	if pidPath == "" {
		return "", errors.New("pid path is empty")
	}

	return filepath.Join(pidPath, fmt.Sprintf("%s.sock", name)), nil
}

func WritePidFile(name string, pid int) error {

	// Write PID to file, check XDG, otherwise user home directory ~/hive/
	pidFilename, err := GeneratePidFile(name)

	if err != nil {
		return err
	}

	pidFile, err := os.Create(pidFilename)

	if err != nil {
		return err
	}

	defer pidFile.Close()
	pidFile.WriteString(fmt.Sprintf("%d", pid))

	return nil
}

func RemovePidFile(serviceName string) error {

	pidPath := pidPath()

	os.Remove(filepath.Join(pidPath, fmt.Sprintf("%s.pid", serviceName)))

	return nil
}

func pidPath() string {
	var pidPath string
	if os.Getenv("XDG_RUNTIME_DIR") != "" {
		pidPath = os.Getenv("XDG_RUNTIME_DIR")
	} else {
		pidPath = filepath.Join(os.Getenv("HOME"), "hive")
	}

	return pidPath
}

func StopProcess(serviceName string) error {
	pid, err := ReadPidFile(serviceName)
	if err != nil {
		return err
	}

	err = KillProcess(pid)
	if err != nil {
		return err
	}

	// Remove PID file
	RemovePidFile(serviceName)

	return nil
}

func KillProcess(pid int) error {

	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}

	// Send SIGTERM first (graceful)
	err = process.Signal(syscall.SIGTERM)
	if err != nil {
		return err
	}

	// Check process terminated

	checks := 0
	for {
		time.Sleep(1 * time.Second)
		process, err = os.FindProcess(pid)
		if err != nil {
			return err
		}

		err = process.Signal(syscall.Signal(0))

		if err != nil {
			// Process terminated, break
			break
		}

		checks++

		// If process is still running after 3 checks, force kill
		if checks > 3 {
			err = process.Kill() // SIGKILL

			if err != nil {
				return err
			}

			break
		}
	}

	return nil

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
