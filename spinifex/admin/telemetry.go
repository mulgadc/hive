package admin

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"
)

const defaultTelemetryURL = "https://install.mulgadc.com/install"

// TelemetryPayload is the JSON body sent to the telemetry endpoint.
type TelemetryPayload struct {
	MachineID    string `json:"machine_id"`
	Event        string `json:"event"` // "init" or "join"
	Region       string `json:"region"`
	AZ           string `json:"az"`
	Node         string `json:"node"`
	Nodes        int    `json:"nodes"`
	BindIP       string `json:"bind_ip"`
	Version      string `json:"version"`
	Arch         string `json:"arch"`
	OS           string `json:"os"`
	ExternalMode string `json:"external_mode,omitempty"`
	Timestamp    string `json:"timestamp"`

	// URL overrides the telemetry endpoint (for testing only). Not serialized.
	URL string `json:"-"`
}

// SendTelemetry posts the payload to the telemetry endpoint.
// It respects the context deadline and logs at Debug level only.
func SendTelemetry(ctx context.Context, payload TelemetryPayload) {
	if payload.Arch == "" {
		payload.Arch = runtime.GOARCH
	}
	if payload.OS == "" {
		payload.OS = runtime.GOOS
	}
	if payload.Timestamp == "" {
		payload.Timestamp = time.Now().UTC().Format(time.RFC3339)
	}

	url := payload.URL
	if url == "" {
		url = defaultTelemetryURL
	}

	body, err := json.Marshal(payload)
	if err != nil {
		slog.Debug("telemetry: failed to marshal payload", "error", err)
		return
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		slog.Debug("telemetry: failed to create request", "error", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		slog.Debug("telemetry: request failed", "error", err)
		return
	}
	defer resp.Body.Close()

	slog.Debug("telemetry: sent", "status", resp.StatusCode, "event", payload.Event)
}

// ReadMachineID returns a stable anonymous identifier for this machine.
// Reads /etc/machine-id (standard Linux UUID). Falls back to a SHA-256
// hash of hostname + first non-loopback MAC address.
func ReadMachineID() string {
	data, err := os.ReadFile("/etc/machine-id")
	if err == nil {
		id := strings.TrimSpace(string(data))
		if id != "" {
			return id
		}
	}

	// Fallback: hash hostname + MAC
	hostname, _ := os.Hostname()
	mac := firstMAC()
	hash := sha256.Sum256([]byte(hostname + mac))
	return fmt.Sprintf("%x", hash[:16])
}

// firstMAC returns the MAC address of the first non-loopback, up interface.
func firstMAC() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, iface := range ifaces {
		if iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
			continue
		}
		if mac := iface.HardwareAddr.String(); mac != "" {
			return mac
		}
	}
	return ""
}
