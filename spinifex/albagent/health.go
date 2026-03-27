package albagent

import (
	"bufio"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"strings"
	"time"
)

// ServerStatus represents a backend server's health as reported by HAProxy.
type ServerStatus struct {
	Backend string `json:"backend"`
	Server  string `json:"server"`
	Status  string `json:"status"` // "UP", "DOWN", "MAINT", etc.
}

// HealthReport is published to NATS so the ELBv2 service can update target state.
type HealthReport struct {
	LBID    string         `json:"lb_id"`
	Servers []ServerStatus `json:"servers"`
}

// reportHealth periodically queries the HAProxy stats socket and publishes
// a health report via NATS. Follows the heartbeat ticker pattern.
func (a *Agent) reportHealth() {
	// Wait for HAProxy to start before first check.
	select {
	case <-time.After(5 * time.Second):
	case <-a.stop:
		return
	}

	ticker := time.NewTicker(healthReportInterval)
	defer ticker.Stop()

	for {
		a.publishHealth()

		select {
		case <-a.stop:
			return
		case <-ticker.C:
		}
	}
}

// publishHealth queries HAProxy and publishes server health to NATS.
func (a *Agent) publishHealth() {
	servers, err := a.statsFn(a.socketPath)
	if err != nil {
		slog.Debug("Failed to query HAProxy stats", "err", err)
		return
	}

	report := HealthReport{
		LBID:    a.lbID,
		Servers: servers,
	}

	data, err := json.Marshal(report)
	if err != nil {
		slog.Error("Failed to marshal health report", "err", err)
		return
	}

	if a.nc == nil {
		return
	}
	if err := a.nc.Publish(HealthTopic(a.lbID), data); err != nil {
		slog.Warn("Failed to publish health report", "err", err)
	} else {
		slog.Debug("Published health report", "servers", len(servers))
	}
}

// queryHAProxyStats connects to the HAProxy stats socket, runs "show stat",
// and parses the CSV output to extract backend server health status.
//
// HAProxy CSV format: pxname,svname,... fields. We need:
//   - Column 0: pxname (backend name)
//   - Column 1: svname (server name)
//   - Column 17: status (UP, DOWN, etc.)
//
// Rows where svname is "FRONTEND" or "BACKEND" are aggregates — skip them.
func queryHAProxyStats(socketPath string) ([]ServerStatus, error) {
	conn, err := net.DialTimeout("unix", socketPath, 2*time.Second)
	if err != nil {
		return nil, fmt.Errorf("connect to stats socket: %w", err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		return nil, fmt.Errorf("set deadline: %w", err)
	}

	if _, err := fmt.Fprintf(conn, "show stat\n"); err != nil {
		return nil, fmt.Errorf("send command: %w", err)
	}

	var servers []ServerStatus
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		fields := strings.Split(line, ",")
		if len(fields) < 18 {
			continue
		}

		svname := fields[1]
		if svname == "FRONTEND" || svname == "BACKEND" {
			continue
		}

		servers = append(servers, ServerStatus{
			Backend: fields[0],
			Server:  svname,
			Status:  fields[17],
		})
	}

	return servers, scanner.Err()
}
