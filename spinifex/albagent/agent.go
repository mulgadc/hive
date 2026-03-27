// Package albagent implements the NATS config agent that runs inside ALB VMs.
// It subscribes to config updates, writes HAProxy configuration, and reloads
// the HAProxy process.
package albagent

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
)

const (
	// Default paths for HAProxy config and PID file.
	DefaultConfigPath = "/etc/haproxy/haproxy.cfg"
	DefaultPIDPath    = "/run/haproxy.pid"

	// NATS topic patterns.
	configTopicPrefix    = "elbv2.alb."
	configTopicSuffix    = ".config"
	pingTopicSuffix      = ".ping"
	healthTopicSuffix    = ".health"
	healthReportInterval = 10 * time.Second
)

// Agent manages HAProxy configuration inside an ALB VM.
type Agent struct {
	lbID       string
	natsURL    string
	configPath string
	pidPath    string
	socketPath string // HAProxy stats socket

	nc   *nats.Conn
	subs []*nats.Subscription
	mu   sync.Mutex
	stop chan struct{}

	// For testing: override the reload function.
	reloadFn func(configPath, pidPath string) error
	// For testing: override the stats query function.
	statsFn func(socketPath string) ([]ServerStatus, error)
}

// New creates a new ALB agent for the given load balancer.
func New(lbID, natsURL string) (*Agent, error) {
	if lbID == "" {
		return nil, fmt.Errorf("lbID is required")
	}
	if natsURL == "" {
		natsURL = "nats://127.0.0.1:4222"
	}

	return &Agent{
		lbID:       lbID,
		natsURL:    natsURL,
		configPath: DefaultConfigPath,
		pidPath:    DefaultPIDPath,
		socketPath: fmt.Sprintf("/tmp/spinifex-haproxy/alb-%s.sock", lbID),
		reloadFn:   reloadHAProxy,
		statsFn:    queryHAProxyStats,
		stop:       make(chan struct{}),
	}, nil
}

// ConfigTopic returns the NATS topic for config updates.
func ConfigTopic(lbID string) string {
	return configTopicPrefix + lbID + configTopicSuffix
}

// PingTopic returns the NATS topic for health pings.
func PingTopic(lbID string) string {
	return configTopicPrefix + lbID + pingTopicSuffix
}

// HealthTopic returns the NATS topic for target health reports.
func HealthTopic(lbID string) string {
	return configTopicPrefix + lbID + healthTopicSuffix
}

// Start connects to NATS and subscribes to config and ping topics.
func (a *Agent) Start() error {
	nc, err := nats.Connect(a.natsURL,
		nats.Name("alb-agent-"+a.lbID),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			slog.Warn("NATS disconnected", "err", err)
		}),
		nats.ReconnectHandler(func(_ *nats.Conn) {
			slog.Info("NATS reconnected")
		}),
	)
	if err != nil {
		return fmt.Errorf("connect to NATS at %s: %w", a.natsURL, err)
	}
	a.nc = nc

	// Subscribe to config updates
	configSub, err := nc.Subscribe(ConfigTopic(a.lbID), a.handleConfig)
	if err != nil {
		nc.Close()
		return fmt.Errorf("subscribe to config topic: %w", err)
	}

	// Subscribe to health pings (request/reply)
	pingSub, err := nc.Subscribe(PingTopic(a.lbID), a.handlePing)
	if err != nil {
		nc.Close()
		return fmt.Errorf("subscribe to ping topic: %w", err)
	}

	a.mu.Lock()
	a.subs = append(a.subs, configSub, pingSub)
	a.mu.Unlock()

	// Flush to ensure the server has processed our subscriptions before
	// we report that the agent is started and ready.
	if err := nc.Flush(); err != nil {
		nc.Close()
		return fmt.Errorf("flush subscriptions: %w", err)
	}

	// Start background goroutine that periodically queries HAProxy stats
	// and publishes target health to the control plane via NATS.
	go a.reportHealth()

	slog.Info("Agent started",
		"lbId", a.lbID,
		"configTopic", ConfigTopic(a.lbID),
		"pingTopic", PingTopic(a.lbID),
		"healthTopic", HealthTopic(a.lbID),
	)
	return nil
}

// Stop gracefully shuts down the agent.
func (a *Agent) Stop() {
	close(a.stop)

	a.mu.Lock()
	defer a.mu.Unlock()

	for _, sub := range a.subs {
		if err := sub.Unsubscribe(); err != nil {
			slog.Warn("Failed to unsubscribe", "err", err)
		}
	}
	a.subs = nil

	if a.nc != nil {
		a.nc.Close()
		a.nc = nil
	}

	slog.Info("Agent stopped", "lbId", a.lbID)
}

// handleConfig processes a config update message. The message payload is the
// raw HAProxy config text. It writes the config to disk and reloads HAProxy.
func (a *Agent) handleConfig(msg *nats.Msg) {
	config := string(msg.Data)
	if config == "" {
		slog.Warn("Received empty config, ignoring")
		a.respond(msg, "error", "empty config")
		return
	}

	slog.Info("Received config update", "lbId", a.lbID, "size", len(config))

	// Write config to disk
	if err := WriteConfig(a.configPath, config); err != nil {
		slog.Error("Failed to write config", "path", a.configPath, "err", err)
		a.respond(msg, "error", err.Error())
		return
	}

	// Reload HAProxy
	if err := a.reloadFn(a.configPath, a.pidPath); err != nil {
		slog.Error("Failed to reload HAProxy", "err", err)
		a.respond(msg, "error", err.Error())
		return
	}

	slog.Info("Config applied and HAProxy reloaded", "lbId", a.lbID)
	a.respond(msg, "ok", "")
}

// PingResponse is the JSON response to a health ping.
type PingResponse struct {
	Status    string `json:"status"`
	LBID      string `json:"lb_id"`
	HAProxy   bool   `json:"haproxy_running"`
	ConfigAge int64  `json:"config_age_seconds"`
}

// handlePing responds to health check requests.
func (a *Agent) handlePing(msg *nats.Msg) {
	haproxyRunning := isHAProxyRunning(a.pidPath)
	configAge := configAgeSeconds(a.configPath)

	resp := PingResponse{
		Status:    "ok",
		LBID:      a.lbID,
		HAProxy:   haproxyRunning,
		ConfigAge: configAge,
	}

	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("Failed to marshal ping response", "err", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		slog.Warn("Failed to respond to ping", "err", err)
	}
}

// respond sends a JSON reply to a NATS message if it has a reply subject.
func (a *Agent) respond(msg *nats.Msg, status, errMsg string) {
	if msg.Reply == "" {
		return
	}
	resp := map[string]string{"status": status}
	if errMsg != "" {
		resp["error"] = errMsg
	}
	data, err := json.Marshal(resp)
	if err != nil {
		slog.Error("Failed to marshal response", "err", err)
		return
	}
	if err := msg.Respond(data); err != nil {
		slog.Warn("Failed to respond", "err", err)
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
