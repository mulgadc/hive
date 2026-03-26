package albagent

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natstest "github.com/nats-io/nats-server/v2/test"
	"github.com/nats-io/nats.go"
)

func startTestNATS(t *testing.T) *natsserver.Server {
	t.Helper()
	opts := natstest.DefaultTestOptions
	opts.Port = -1 // random port
	ns := natstest.RunServer(&opts)
	t.Cleanup(ns.Shutdown)
	return ns
}

func TestNew(t *testing.T) {
	agent, err := New("lb-test123", "nats://localhost:4222")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if agent.lbID != "lb-test123" {
		t.Errorf("lbID = %q, want %q", agent.lbID, "lb-test123")
	}
}

func TestNew_EmptyLBID(t *testing.T) {
	_, err := New("", "nats://localhost:4222")
	if err == nil {
		t.Fatal("expected error for empty lbID")
	}
}

func TestNew_DefaultNATSURL(t *testing.T) {
	agent, err := New("lb-test", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if agent.natsURL != "nats://127.0.0.1:4222" {
		t.Errorf("natsURL = %q, want default", agent.natsURL)
	}
}

func TestConfigTopic(t *testing.T) {
	topic := ConfigTopic("lb-abc123")
	if topic != "elbv2.alb.lb-abc123.config" {
		t.Errorf("ConfigTopic = %q, want %q", topic, "elbv2.alb.lb-abc123.config")
	}
}

func TestPingTopic(t *testing.T) {
	topic := PingTopic("lb-abc123")
	if topic != "elbv2.alb.lb-abc123.ping" {
		t.Errorf("PingTopic = %q, want %q", topic, "elbv2.alb.lb-abc123.ping")
	}
}

func TestWriteConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "haproxy.cfg")

	content := "global\n  log stdout\n"
	if err := WriteConfig(path, content); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != content {
		t.Errorf("config = %q, want %q", string(data), content)
	}
}

func TestWriteConfig_CreatesDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "subdir", "haproxy.cfg")

	if err := WriteConfig(path, "test"); err != nil {
		t.Fatalf("WriteConfig: %v", err)
	}

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("config file not created: %v", err)
	}
}

func TestWriteConfig_Atomic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "haproxy.cfg")

	// Write initial config
	if err := WriteConfig(path, "initial"); err != nil {
		t.Fatalf("WriteConfig initial: %v", err)
	}

	// Overwrite with new config
	if err := WriteConfig(path, "updated"); err != nil {
		t.Fatalf("WriteConfig updated: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "updated" {
		t.Errorf("config = %q, want %q", string(data), "updated")
	}

	// No temp file left behind
	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should not exist after successful write")
	}
}

func TestConfigAgeSeconds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.cfg")

	// Non-existent file
	if age := configAgeSeconds(path); age != -1 {
		t.Errorf("age of non-existent file = %d, want -1", age)
	}

	// Write file and check age
	os.WriteFile(path, []byte("test"), 0o644)
	age := configAgeSeconds(path)
	if age < 0 || age > 2 {
		t.Errorf("age = %d, expected 0-2 seconds", age)
	}
}

func TestIsHAProxyRunning_NoPIDFile(t *testing.T) {
	if isHAProxyRunning("/nonexistent/haproxy.pid") {
		t.Error("expected false for non-existent PID file")
	}
}

func TestReadPID_InvalidContent(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "haproxy.pid")

	os.WriteFile(pidFile, []byte("not-a-number\n"), 0o644)
	if pid := readPID(pidFile); pid != 0 {
		t.Errorf("readPID = %d, want 0 for invalid content", pid)
	}
}

func TestReadPID_DeadProcess(t *testing.T) {
	dir := t.TempDir()
	pidFile := filepath.Join(dir, "haproxy.pid")

	// Use a very high PID that's unlikely to be running
	os.WriteFile(pidFile, []byte("999999999\n"), 0o644)
	if pid := readPID(pidFile); pid != 0 {
		t.Errorf("readPID = %d, want 0 for dead process", pid)
	}
}

func TestAgent_StartStop(t *testing.T) {
	ns := startTestNATS(t)

	agent, err := New("lb-test123", ns.ClientURL())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := agent.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Verify agent is connected
	if agent.nc == nil || !agent.nc.IsConnected() {
		t.Fatal("agent not connected to NATS")
	}

	agent.Stop()

	if agent.nc != nil {
		t.Error("nc should be nil after Stop")
	}
}

func TestAgent_HandleConfig(t *testing.T) {
	ns := startTestNATS(t)
	dir := t.TempDir()

	agent, _ := New("lb-cfg123", ns.ClientURL())
	agent.configPath = filepath.Join(dir, "haproxy.cfg")
	agent.pidPath = filepath.Join(dir, "haproxy.pid")

	// Mock reload to avoid needing real HAProxy
	reloadCalled := false
	agent.reloadFn = func(configPath, pidPath string) error {
		reloadCalled = true
		return nil
	}

	if err := agent.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop()

	// Send a config update via a separate NATS connection
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()

	haproxyConfig := "global\n  log stdout\ndefaults\n  mode http\n"
	reply, err := nc.Request(ConfigTopic("lb-cfg123"), []byte(haproxyConfig), 2*time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	// Check response
	var resp map[string]string
	json.Unmarshal(reply.Data, &resp)
	if resp["status"] != "ok" {
		t.Errorf("response status = %q, want %q", resp["status"], "ok")
	}

	// Verify config was written
	data, err := os.ReadFile(agent.configPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != haproxyConfig {
		t.Errorf("written config = %q, want %q", string(data), haproxyConfig)
	}

	if !reloadCalled {
		t.Error("reload function was not called")
	}
}

func TestAgent_HandleConfig_Empty(t *testing.T) {
	ns := startTestNATS(t)

	agent, _ := New("lb-empty", ns.ClientURL())
	agent.reloadFn = func(_, _ string) error { return nil }

	if err := agent.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop()

	nc, _ := nats.Connect(ns.ClientURL())
	defer nc.Close()

	reply, err := nc.Request(ConfigTopic("lb-empty"), []byte(""), 2*time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	var resp map[string]string
	json.Unmarshal(reply.Data, &resp)
	if resp["status"] != "error" {
		t.Errorf("response status = %q, want %q for empty config", resp["status"], "error")
	}
}

func TestAgent_HandlePing(t *testing.T) {
	ns := startTestNATS(t)
	dir := t.TempDir()

	agent, _ := New("lb-ping123", ns.ClientURL())
	agent.configPath = filepath.Join(dir, "haproxy.cfg")
	agent.pidPath = filepath.Join(dir, "haproxy.pid")

	if err := agent.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop()

	nc, _ := nats.Connect(ns.ClientURL())
	defer nc.Close()

	reply, err := nc.Request(PingTopic("lb-ping123"), nil, 2*time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	var resp PingResponse
	if err := json.Unmarshal(reply.Data, &resp); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	if resp.Status != "ok" {
		t.Errorf("status = %q, want %q", resp.Status, "ok")
	}
	if resp.LBID != "lb-ping123" {
		t.Errorf("lb_id = %q, want %q", resp.LBID, "lb-ping123")
	}
	if resp.HAProxy {
		t.Error("haproxy_running should be false (no PID file)")
	}
	if resp.ConfigAge != -1 {
		t.Errorf("config_age = %d, want -1 (no config file)", resp.ConfigAge)
	}
}

func TestAgent_HandleConfig_ReloadError(t *testing.T) {
	ns := startTestNATS(t)
	dir := t.TempDir()

	agent, _ := New("lb-err", ns.ClientURL())
	agent.configPath = filepath.Join(dir, "haproxy.cfg")
	agent.pidPath = filepath.Join(dir, "haproxy.pid")
	agent.reloadFn = func(_, _ string) error {
		return fmt.Errorf("haproxy binary not found")
	}

	if err := agent.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop()

	nc, _ := nats.Connect(ns.ClientURL())
	defer nc.Close()

	reply, err := nc.Request(ConfigTopic("lb-err"), []byte("some config"), 2*time.Second)
	if err != nil {
		t.Fatalf("Request: %v", err)
	}

	var resp map[string]string
	json.Unmarshal(reply.Data, &resp)
	if resp["status"] != "error" {
		t.Errorf("response status = %q, want %q", resp["status"], "error")
	}
	if resp["error"] == "" {
		t.Error("expected error message in response")
	}
}
