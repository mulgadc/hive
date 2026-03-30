package albagent

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNew(t *testing.T) {
	agent, err := New("lb-test123", ":8405")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if agent.lbID != "lb-test123" {
		t.Errorf("lbID = %q, want %q", agent.lbID, "lb-test123")
	}
}

func TestNew_EmptyLBID(t *testing.T) {
	_, err := New("", ":8405")
	if err == nil {
		t.Fatal("expected error for empty lbID")
	}
}

func TestNew_DefaultListenAddr(t *testing.T) {
	agent, err := New("lb-test", "")
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if agent.listenAddr != ":8405" {
		t.Errorf("listenAddr = %q, want %q", agent.listenAddr, ":8405")
	}
}

func TestNew_SocketPath(t *testing.T) {
	agent, err := New("lb-sock123", ":8405")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	expected := "/tmp/spinifex-haproxy/alb-lb-sock123.sock"
	if agent.socketPath != expected {
		t.Errorf("socketPath = %q, want %q", agent.socketPath, expected)
	}
}

// newTestAgent creates an agent and returns it along with an httptest.Server
// serving its handlers.
func newTestAgent(t *testing.T, lbID string) (*Agent, *httptest.Server) {
	t.Helper()
	agent, err := New(lbID, ":0")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /config", agent.handleConfig)
	mux.HandleFunc("GET /ping", agent.handlePing)
	mux.HandleFunc("GET /health", agent.handleHealth)

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return agent, ts
}

func TestHandleConfig(t *testing.T) {
	dir := t.TempDir()
	agent, ts := newTestAgent(t, "lb-cfg123")
	agent.configPath = filepath.Join(dir, "haproxy.cfg")
	agent.pidPath = filepath.Join(dir, "haproxy.pid")

	reloadCalled := false
	agent.reloadFn = func(configPath, pidPath string) error {
		reloadCalled = true
		return nil
	}

	haproxyConfig := "global\n  log stdout\ndefaults\n  mode http\n"
	resp, err := http.Post(ts.URL+"/config", "text/plain", strings.NewReader(haproxyConfig))
	if err != nil {
		t.Fatalf("POST /config: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("response status = %q, want %q", body["status"], "ok")
	}

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

func TestHandleConfig_Empty(t *testing.T) {
	agent, ts := newTestAgent(t, "lb-empty")
	agent.reloadFn = func(_, _ string) error { return nil }

	resp, err := http.Post(ts.URL+"/config", "text/plain", strings.NewReader(""))
	if err != nil {
		t.Fatalf("POST /config: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusBadRequest)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "error" {
		t.Errorf("response status = %q, want %q", body["status"], "error")
	}
}

func TestHandleConfig_ReloadError(t *testing.T) {
	dir := t.TempDir()
	agent, ts := newTestAgent(t, "lb-err")
	agent.configPath = filepath.Join(dir, "haproxy.cfg")
	agent.pidPath = filepath.Join(dir, "haproxy.pid")
	agent.reloadFn = func(_, _ string) error {
		return fmt.Errorf("haproxy binary not found")
	}

	resp, err := http.Post(ts.URL+"/config", "text/plain", strings.NewReader("some config"))
	if err != nil {
		t.Fatalf("POST /config: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusInternalServerError {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusInternalServerError)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "error" {
		t.Errorf("response status = %q, want %q", body["status"], "error")
	}
	if body["error"] == "" {
		t.Error("expected error message in response")
	}
}

func TestHandleConfig_WrongMethod(t *testing.T) {
	_, ts := newTestAgent(t, "lb-method")

	resp, err := http.Get(ts.URL + "/config")
	if err != nil {
		t.Fatalf("GET /config: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMethodNotAllowed {
		// Go 1.22+ ServeMux returns 405 for method mismatch
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusMethodNotAllowed)
	}
}

func TestHandlePing(t *testing.T) {
	dir := t.TempDir()
	agent, ts := newTestAgent(t, "lb-ping123")
	agent.configPath = filepath.Join(dir, "haproxy.cfg")
	agent.pidPath = filepath.Join(dir, "haproxy.pid")

	resp, err := http.Get(ts.URL + "/ping")
	if err != nil {
		t.Fatalf("GET /ping: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var ping PingResponse
	if err := json.NewDecoder(resp.Body).Decode(&ping); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if ping.Status != "ok" {
		t.Errorf("status = %q, want %q", ping.Status, "ok")
	}
	if ping.LBID != "lb-ping123" {
		t.Errorf("lb_id = %q, want %q", ping.LBID, "lb-ping123")
	}
	if ping.HAProxy {
		t.Error("haproxy_running should be false (no PID file)")
	}
	if ping.ConfigAge != -1 {
		t.Errorf("config_age = %d, want -1 (no config file)", ping.ConfigAge)
	}
}

func TestHandleHealth(t *testing.T) {
	agent, ts := newTestAgent(t, "lb-health1")
	agent.statsFn = func(_ string) ([]ServerStatus, error) {
		return []ServerStatus{
			{Backend: "bk_tg1", Server: "srv_i-web1", Status: "UP"},
			{Backend: "bk_tg1", Server: "srv_i-web2", Status: "DOWN"},
		}, nil
	}

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	var report HealthReport
	if err := json.NewDecoder(resp.Body).Decode(&report); err != nil {
		t.Fatalf("Decode: %v", err)
	}

	if report.LBID != "lb-health1" {
		t.Errorf("LBID = %q, want %q", report.LBID, "lb-health1")
	}
	if len(report.Servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(report.Servers))
	}
	if report.Servers[0].Status != "UP" {
		t.Errorf("server[0].Status = %q, want UP", report.Servers[0].Status)
	}
	if report.Servers[1].Status != "DOWN" {
		t.Errorf("server[1].Status = %q, want DOWN", report.Servers[1].Status)
	}
}

func TestHandleHealth_StatsError(t *testing.T) {
	agent, ts := newTestAgent(t, "lb-health-err")
	agent.statsFn = func(_ string) ([]ServerStatus, error) {
		return nil, fmt.Errorf("socket not found")
	}

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusServiceUnavailable)
	}

	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "error" {
		t.Errorf("response status = %q, want %q", body["status"], "error")
	}
}

func TestHandleHealth_EmptyServers(t *testing.T) {
	agent, ts := newTestAgent(t, "lb-health-empty")
	agent.statsFn = func(_ string) ([]ServerStatus, error) {
		return nil, nil
	}

	resp, err := http.Get(ts.URL + "/health")
	if err != nil {
		t.Fatalf("GET /health: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, _ := io.ReadAll(resp.Body)
	var report HealthReport
	json.Unmarshal(body, &report)
	if report.LBID != "lb-health-empty" {
		t.Errorf("LBID = %q, want %q", report.LBID, "lb-health-empty")
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

	if err := WriteConfig(path, "initial"); err != nil {
		t.Fatalf("WriteConfig initial: %v", err)
	}

	if err := WriteConfig(path, "updated"); err != nil {
		t.Fatalf("WriteConfig updated: %v", err)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "updated" {
		t.Errorf("config = %q, want %q", string(data), "updated")
	}

	if _, err := os.Stat(path + ".tmp"); !os.IsNotExist(err) {
		t.Error("temp file should not exist after successful write")
	}
}

func TestConfigAgeSeconds(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.cfg")

	if age := configAgeSeconds(path); age != -1 {
		t.Errorf("age of non-existent file = %d, want -1", age)
	}

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

	os.WriteFile(pidFile, []byte("999999999\n"), 0o644)
	if pid := readPID(pidFile); pid != 0 {
		t.Errorf("readPID = %d, want 0 for dead process", pid)
	}
}
