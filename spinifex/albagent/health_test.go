package albagent

import (
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
)

func TestQueryHAProxyStats(t *testing.T) {
	// Create a fake Unix socket that responds with HAProxy CSV stats
	dir := t.TempDir()
	sock := filepath.Join(dir, "haproxy.sock")

	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	// Respond to "show stat\n" with CSV data
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()

		buf := make([]byte, 256)
		conn.Read(buf)

		// HAProxy CSV format: pxname,svname,...,status (col 17),...
		// Header + FRONTEND + BACKEND + 2 servers
		csv := "# pxname,svname,qcur,qmax,scur,smax,slim,stot,bin,bout,dreq,dresp,ereq,econ,eresp,wretr,wredis,status,weight\n"
		csv += "bk_tg1,FRONTEND,0,0,0,0,0,0,0,0,0,0,0,0,0,0,0,OPEN,\n"
		csv += "bk_tg1,srv_i-aaa111,0,0,0,0,0,5,0,0,0,0,0,0,0,0,0,UP,1\n"
		csv += "bk_tg1,srv_i-bbb222,0,0,0,0,0,3,0,0,0,0,0,0,0,0,0,DOWN,1\n"
		csv += "bk_tg1,BACKEND,0,0,0,0,0,8,0,0,0,0,0,0,0,0,0,UP,2\n"
		fmt.Fprint(conn, csv)
	}()

	servers, err := queryHAProxyStats(sock)
	if err != nil {
		t.Fatalf("queryHAProxyStats: %v", err)
	}

	if len(servers) != 2 {
		t.Fatalf("got %d servers, want 2", len(servers))
	}

	if servers[0].Server != "srv_i-aaa111" || servers[0].Status != "UP" {
		t.Errorf("server[0] = %+v, want srv_i-aaa111/UP", servers[0])
	}
	if servers[1].Server != "srv_i-bbb222" || servers[1].Status != "DOWN" {
		t.Errorf("server[1] = %+v, want srv_i-bbb222/DOWN", servers[1])
	}
}

func TestQueryHAProxyStats_SocketNotFound(t *testing.T) {
	_, err := queryHAProxyStats("/nonexistent/haproxy.sock")
	if err == nil {
		t.Error("expected error for non-existent socket")
	}
}

func TestPublishHealth(t *testing.T) {
	ns := startTestNATS(t)

	// Subscribe BEFORE starting agent to avoid race
	nc, err := nats.Connect(ns.ClientURL())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer nc.Close()

	sub, err := nc.SubscribeSync(HealthTopic("lb-health1"))
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	nc.Flush()

	agent, err := New("lb-health1", ns.ClientURL())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	// Override statsFn to return fake stats
	agent.statsFn = func(_ string) ([]ServerStatus, error) {
		return []ServerStatus{
			{Backend: "bk_tg1", Server: "srv_i-web1", Status: "UP"},
			{Backend: "bk_tg1", Server: "srv_i-web2", Status: "DOWN"},
		}, nil
	}

	if err := agent.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop()

	// Trigger a publish directly (don't wait for ticker)
	agent.publishHealth()
	agent.nc.Flush()

	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatalf("NextMsg: %v", err)
	}

	var report HealthReport
	if err := json.Unmarshal(msg.Data, &report); err != nil {
		t.Fatalf("Unmarshal: %v", err)
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
}

func TestPublishHealth_StatsError(t *testing.T) {
	ns := startTestNATS(t)

	agent, err := New("lb-health-err", ns.ClientURL())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	agent.statsFn = func(_ string) ([]ServerStatus, error) {
		return nil, fmt.Errorf("socket not found")
	}

	if err := agent.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer agent.Stop()

	// Subscribe and verify no message is published on stats error
	nc, _ := nats.Connect(ns.ClientURL())
	defer nc.Close()

	sub, _ := nc.SubscribeSync(HealthTopic("lb-health-err"))

	agent.publishHealth()

	_, err = sub.NextMsg(200 * time.Millisecond)
	if err == nil {
		t.Error("expected no message when stats query fails")
	}
}

func TestHealthTopic(t *testing.T) {
	topic := HealthTopic("lb-abc123")
	if topic != "elbv2.alb.lb-abc123.health" {
		t.Errorf("HealthTopic = %q, want %q", topic, "elbv2.alb.lb-abc123.health")
	}
}

// Verify the socketPath is set correctly from the LB ID
func TestNew_SocketPath(t *testing.T) {
	agent, err := New("lb-sock123", "nats://localhost:4222")
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	expected := "/tmp/spinifex-haproxy/alb-lb-sock123.sock"
	if agent.socketPath != expected {
		t.Errorf("socketPath = %q, want %q", agent.socketPath, expected)
	}
}

func TestReportHealth_StopsOnClose(t *testing.T) {
	ns := startTestNATS(t)

	agent, err := New("lb-report1", ns.ClientURL())
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	called := 0
	agent.statsFn = func(_ string) ([]ServerStatus, error) {
		called++
		return nil, nil
	}

	if err := agent.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	// Give the reportHealth goroutine time to do the initial 5s wait
	// then stop it
	time.Sleep(100 * time.Millisecond)
	agent.Stop()

	// The goroutine should have exited during the initial sleep
	// since we stopped quickly
	if called > 1 {
		t.Errorf("expected at most 1 call to statsFn, got %d", called)
	}
}
