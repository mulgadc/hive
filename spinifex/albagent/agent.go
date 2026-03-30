// Package albagent implements the HTTP config agent that runs inside ALB VMs.
// It exposes an HTTP server for config pushes, health pings, and target health
// queries. The daemon communicates with the agent over the VPC network.
package albagent

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	// Default paths for HAProxy config and PID file.
	DefaultConfigPath = "/etc/haproxy/haproxy.cfg"
	DefaultPIDPath    = "/run/haproxy.pid"

	// maxConfigSize limits config POST bodies to 1 MiB.
	maxConfigSize = 1 << 20
)

// Agent manages HAProxy configuration inside an ALB VM.
type Agent struct {
	lbID       string
	listenAddr string
	configPath string
	pidPath    string
	socketPath string // HAProxy stats socket

	server *http.Server

	// For testing: override the reload function.
	reloadFn func(configPath, pidPath string) error
	// For testing: override the stats query function.
	statsFn func(socketPath string) ([]ServerStatus, error)
}

// New creates a new ALB agent for the given load balancer.
func New(lbID, listenAddr string) (*Agent, error) {
	if lbID == "" {
		return nil, fmt.Errorf("lbID is required")
	}
	if listenAddr == "" {
		listenAddr = ":8405"
	}

	return &Agent{
		lbID:       lbID,
		listenAddr: listenAddr,
		configPath: DefaultConfigPath,
		pidPath:    DefaultPIDPath,
		socketPath: fmt.Sprintf("/tmp/spinifex-haproxy/alb-%s.sock", lbID),
		reloadFn:   reloadHAProxy,
		statsFn:    queryHAProxyStats,
	}, nil
}

// Start starts the HTTP server. It blocks until the server is shut down.
func (a *Agent) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /config", a.handleConfig)
	mux.HandleFunc("GET /ping", a.handlePing)
	mux.HandleFunc("GET /health", a.handleHealth)

	a.server = &http.Server{
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	ln, err := net.Listen("tcp", a.listenAddr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", a.listenAddr, err)
	}

	slog.Info("Agent started", "lbId", a.lbID, "listen", ln.Addr().String())
	return a.server.Serve(ln)
}

// Stop gracefully shuts down the HTTP server.
func (a *Agent) Stop() {
	if a.server == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := a.server.Shutdown(ctx); err != nil {
		slog.Warn("HTTP server shutdown error", "err", err)
	}
	slog.Info("Agent stopped", "lbId", a.lbID)
}

// handleConfig processes a config push. The request body is the raw HAProxy
// config text. It writes the config to disk and reloads HAProxy.
func (a *Agent) handleConfig(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigSize))
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "read body: " + err.Error()})
		return
	}

	config := string(body)
	if config == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"status": "error", "error": "empty config"})
		return
	}

	slog.Info("Received config update", "lbId", a.lbID, "size", len(config))

	if err := WriteConfig(a.configPath, config); err != nil {
		slog.Error("Failed to write config", "path", a.configPath, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
		return
	}

	if err := a.reloadFn(a.configPath, a.pidPath); err != nil {
		slog.Error("Failed to reload HAProxy", "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"status": "error", "error": err.Error()})
		return
	}

	slog.Info("Config applied and HAProxy reloaded", "lbId", a.lbID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// PingResponse is the JSON response to a health ping.
type PingResponse struct {
	Status    string `json:"status"`
	LBID      string `json:"lb_id"`
	HAProxy   bool   `json:"haproxy_running"`
	ConfigAge int64  `json:"config_age_seconds"`
}

// handlePing responds to health check requests from the daemon.
func (a *Agent) handlePing(w http.ResponseWriter, _ *http.Request) {
	resp := PingResponse{
		Status:    "ok",
		LBID:      a.lbID,
		HAProxy:   isHAProxyRunning(a.pidPath),
		ConfigAge: configAgeSeconds(a.configPath),
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleHealth returns the current HAProxy backend server health by querying
// the stats socket. The daemon polls this endpoint instead of receiving pushes.
func (a *Agent) handleHealth(w http.ResponseWriter, _ *http.Request) {
	servers, err := a.statsFn(a.socketPath)
	if err != nil {
		slog.Debug("Failed to query HAProxy stats", "err", err)
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "error": err.Error()})
		return
	}

	report := HealthReport{
		LBID:    a.lbID,
		Servers: servers,
	}
	writeJSON(w, http.StatusOK, report)
}

// writeJSON marshals v as JSON and writes it to w.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Error("Failed to write JSON response", "err", err)
	}
}

// WriteConfig atomically writes an HAProxy config file.
// It writes to a temp file first, then renames for atomicity.
func WriteConfig(path, content string) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return fmt.Errorf("create config dir: %w", err)
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(content), 0o600); err != nil {
		return fmt.Errorf("write temp config: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename config: %w", err)
	}
	return nil
}

// reloadHAProxy starts or reloads the HAProxy process.
// If HAProxy is running (PID file exists and process alive), it does a
// graceful reload with -sf. Otherwise it starts a fresh instance.
func reloadHAProxy(configPath, pidPath string) error {
	// Ensure the stats socket directory exists (the config may reference
	// /tmp/spinifex-haproxy/ which doesn't exist on fresh Alpine VMs).
	_ = os.MkdirAll("/tmp/spinifex-haproxy", 0o750)

	oldPID := readPID(pidPath)

	var cmd *exec.Cmd
	if oldPID > 0 {
		// Graceful reload: new worker starts, old workers finish in-flight requests
		cmd = exec.Command("haproxy", "-f", configPath, "-p", pidPath, "-D", "-sf", strconv.Itoa(oldPID))
	} else {
		// Fresh start
		cmd = exec.Command("haproxy", "-f", configPath, "-p", pidPath, "-D")
	}

	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("haproxy: %s: %w", strings.TrimSpace(string(out)), err)
	}
	return nil
}

// readPID reads the HAProxy PID from the PID file. Returns 0 if unavailable.
func readPID(pidPath string) int {
	data, err := os.ReadFile(pidPath)
	if err != nil {
		return 0
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0
	}
	// Check if process is still alive
	proc, err := os.FindProcess(pid)
	if err != nil {
		return 0
	}
	if err := proc.Signal(syscall.Signal(0)); err != nil {
		return 0
	}
	return pid
}

// isHAProxyRunning checks if HAProxy is running via its PID file.
func isHAProxyRunning(pidPath string) bool {
	return readPID(pidPath) > 0
}

// configAgeSeconds returns how many seconds since the config file was last modified.
// Returns -1 if the file doesn't exist.
func configAgeSeconds(configPath string) int64 {
	info, err := os.Stat(configPath)
	if err != nil {
		return -1
	}
	return int64(time.Since(info.ModTime()).Seconds())
}
