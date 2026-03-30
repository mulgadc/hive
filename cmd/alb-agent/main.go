// alb-agent runs inside ALB VMs and manages HAProxy configuration.
//
// It starts an HTTP server that accepts config pushes from the daemon,
// responds to health pings, and reports HAProxy backend health.
//
// Usage:
//
//	alb-agent --lb-id=lb-xxxxx --listen=:8405
//
// The LB ID defaults to the hostname (set by cloud-init to the LB ID).
// The listen address defaults to :8405.
package main

import (
	"flag"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/mulgadc/spinifex/spinifex/albagent"
)

func main() {
	var (
		lbID       string
		listenAddr string
	)

	hostname, _ := os.Hostname()

	flag.StringVar(&lbID, "lb-id", hostname, "Load balancer ID (defaults to hostname)")
	flag.StringVar(&listenAddr, "listen", ":8405", "HTTP listen address")
	flag.Parse()

	if lbID == "" {
		slog.Error("--lb-id is required (or set hostname to the LB ID)")
		os.Exit(1)
	}

	slog.Info("Starting ALB agent", "lbId", lbID, "listen", listenAddr)

	agent, err := albagent.New(lbID, listenAddr)
	if err != nil {
		slog.Error("Failed to create agent", "err", err)
		os.Exit(1)
	}

	// Start HTTP server in a goroutine — it blocks until shutdown.
	errCh := make(chan error, 1)
	go func() {
		if err := agent.Start(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	// Wait for shutdown signal or server error.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	select {
	case sig := <-sigCh:
		slog.Info("Received signal, shutting down", "signal", sig)
	case err := <-errCh:
		slog.Error("Server error", "err", err)
	}

	agent.Stop()
}
