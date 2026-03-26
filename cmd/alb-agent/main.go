// alb-agent runs inside ALB VMs and manages HAProxy configuration.
//
// It connects to NATS, subscribes to config updates for its load balancer,
// writes HAProxy configs to disk, and reloads HAProxy on changes. It also
// responds to health pings so the daemon can verify the agent is alive.
//
// Usage:
//
//	alb-agent --lb-id=lb-xxxxx --nats=nats://10.0.0.1:4222
//
// The LB ID defaults to the hostname (set by cloud-init to the LB ID).
// The NATS URL defaults to nats://127.0.0.1:4222.
package main

import (
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mulgadc/spinifex/spinifex/albagent"
)

func main() {
	var (
		lbID    string
		natsURL string
	)

	hostname, _ := os.Hostname()

	flag.StringVar(&lbID, "lb-id", hostname, "Load balancer ID (defaults to hostname)")
	flag.StringVar(&natsURL, "nats", "nats://127.0.0.1:4222", "NATS server URL")
	flag.Parse()

	if lbID == "" {
		slog.Error("--lb-id is required (or set hostname to the LB ID)")
		os.Exit(1)
	}

	slog.Info("Starting ALB agent", "lbId", lbID, "nats", natsURL)

	agent, err := albagent.New(lbID, natsURL)
	if err != nil {
		slog.Error("Failed to create agent", "err", err)
		os.Exit(1)
	}

	if err := agent.Start(); err != nil {
		slog.Error("Failed to start agent", "err", err)
		os.Exit(1)
	}

	// Wait for shutdown signal
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	sig := <-sigCh

	slog.Info("Received signal, shutting down", "signal", sig)
	agent.Stop()
}
