package utils

import (
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// GenerateSocketFile generates a socket file path for the given name.
// Deprecated: Use GenerateUniqueSocketFile for new code to ensure uniqueness.
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

// GenerateUniqueSocketFile generates a unique socket file path with unix nano timestamp.
// Format: nbd-{volname}-{unixnano}.sock
// This ensures each mount operation gets a unique socket path.
func GenerateUniqueSocketFile(volname string) (string, error) {
	if volname == "" {
		return "", errors.New("volume name is required")
	}

	dir := NBDSocketDir()
	if dir == "" {
		return "", errors.New("nbd socket directory is empty")
	}

	timestamp := time.Now().UnixNano()
	filename := fmt.Sprintf("nbd-%s-%d.sock", volname, timestamp)
	return filepath.Join(dir, filename), nil
}

// NBDSocketDir returns the directory for NBD Unix sockets.
// Under systemd installs, this is /run/spinifex/nbd/.
// In dev mode (no /run/spinifex/nbd/), falls back to pidPath().
func NBDSocketDir() string {
	const systemdNBDDir = "/run/spinifex/nbd"
	if dirExists(systemdNBDDir) {
		return systemdNBDDir
	}
	return pidPath()
}

// IsSocketURI returns true if the NBD URI is a Unix socket path.
// Socket URIs end with ".sock" or contain "unix:".
func IsSocketURI(nbdURI string) bool {
	return strings.HasSuffix(nbdURI, ".sock") || strings.Contains(nbdURI, "unix:")
}

// FormatNBDSocketURI formats a socket path as an NBD URI for QEMU.
// Returns format: nbd:unix:/path/to/socket.sock
func FormatNBDSocketURI(socketPath string) string {
	return fmt.Sprintf("nbd:unix:%s", socketPath)
}

// FormatNBDTCPURI formats a host:port as an NBD URI for QEMU.
// Returns format: nbd://host:port
func FormatNBDTCPURI(host string, port int) string {
	return "nbd://" + net.JoinHostPort(host, strconv.Itoa(port))
}

// ParseNBDURI parses an NBD URI into its components for QMP blockdev-add.
// Supported formats:
//   - "nbd:unix:/path/to/socket.sock" → serverType="unix", path="/path/to/socket.sock"
//   - "nbd://host:port"               → serverType="inet", host="host", port=<port>
func ParseNBDURI(nbdURI string) (serverType, path, host string, port int, err error) {
	if after, ok := strings.CutPrefix(nbdURI, "nbd:unix:"); ok {
		path = after
		if path == "" {
			return "", "", "", 0, fmt.Errorf("empty socket path in NBD URI: %s", nbdURI)
		}
		return "unix", path, "", 0, nil
	}

	if after, ok := strings.CutPrefix(nbdURI, "nbd://"); ok {
		hostPort := after
		colonIdx := strings.LastIndex(hostPort, ":")
		if colonIdx < 0 {
			return "", "", "", 0, fmt.Errorf("missing port in NBD TCP URI: %s", nbdURI)
		}
		host = hostPort[:colonIdx]
		port, err = strconv.Atoi(hostPort[colonIdx+1:])
		if err != nil {
			return "", "", "", 0, fmt.Errorf("invalid port in NBD URI: %s", nbdURI)
		}
		return "inet", "", host, port, nil
	}

	return "", "", "", 0, fmt.Errorf("unsupported NBD URI format: %s", nbdURI)
}
