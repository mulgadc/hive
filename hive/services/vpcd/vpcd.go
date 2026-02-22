package vpcd

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"syscall"

	"github.com/mulgadc/hive/hive/utils"
)

// sudoCommand wraps exec.Command with sudo when running as non-root.
// OVS/OVN commands require elevated privileges; when running as root
// (Docker, production) no wrapper is needed.
func sudoCommand(name string, args ...string) *exec.Cmd {
	if os.Getuid() == 0 {
		return exec.Command(name, args...)
	}
	return exec.Command("sudo", append([]string{name}, args...)...)
}

var serviceName = "vpcd"

// Config holds the vpcd service configuration.
type Config struct {
	// NatsHost is the NATS server address (host:port).
	NatsHost string
	// NatsToken is the NATS authentication token.
	NatsToken string
	// OVNNBAddr is the OVN Northbound DB address (e.g., "tcp:127.0.0.1:6641").
	OVNNBAddr string
	// OVNSBAddr is the OVN Southbound DB address (e.g., "tcp:127.0.0.1:6642"), used for monitoring.
	OVNSBAddr string
	// BaseDir is the base directory for PID files and state.
	BaseDir string
	// Debug enables debug logging.
	Debug bool
}

// Service implements the Hive service interface for vpcd.
type Service struct {
	Config *Config
}

// New creates a new vpcd Service.
func New(config any) (*Service, error) {
	return &Service{
		Config: config.(*Config),
	}, nil
}

// Start starts the vpcd service.
func (svc *Service) Start() (int, error) {
	if err := utils.WritePidFileTo(svc.Config.BaseDir, serviceName, os.Getpid()); err != nil {
		slog.Error("Failed to write pid file", "err", err)
	}

	err := launchService(svc.Config)
	if err != nil {
		slog.Error("Failed to launch vpcd service", "err", err)
		return 0, err
	}

	return os.Getpid(), nil
}

// Stop stops the vpcd service.
func (svc *Service) Stop() error {
	return utils.StopProcessAt(svc.Config.BaseDir, serviceName)
}

// Status returns the vpcd service status.
func (svc *Service) Status() (string, error) {
	return "", nil
}

// Shutdown gracefully shuts down the vpcd service.
func (svc *Service) Shutdown() error {
	return svc.Stop()
}

// Reload reloads the vpcd service configuration.
func (svc *Service) Reload() error {
	return nil
}

// checkBrInt verifies the OVS integration bridge (br-int) exists.
// This is the bridge that all VM TAP devices connect to.
var checkBrInt = func() error {
	cmd := sudoCommand("ovs-vsctl", "br-exists", "br-int")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("br-int does not exist: run ./scripts/setup-ovn.sh --management")
	}
	return nil
}

// checkOVNController verifies that ovn-controller is running on this host.
var checkOVNController = func() error {
	cmd := sudoCommand("ovs-appctl", "-t", "ovn-controller", "version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ovn-controller is not running: run ./scripts/setup-ovn.sh --management")
	}
	return nil
}

// preflightOVN runs all OVN preflight checks and returns the first failure.
func preflightOVN() error {
	if err := checkBrInt(); err != nil {
		return fmt.Errorf("OVN preflight failed: %w", err)
	}
	if err := checkOVNController(); err != nil {
		return fmt.Errorf("OVN preflight failed: %w", err)
	}
	return nil
}

func launchService(cfg *Config) error {
	slog.Info("Starting vpcd service",
		"ovn_nb_addr", cfg.OVNNBAddr,
		"nats_host", cfg.NatsHost,
	)

	// OVN preflight: verify br-int and ovn-controller before proceeding
	if err := preflightOVN(); err != nil {
		slog.Error("OVN preflight check failed — vpcd cannot start without OVN", "err", err)
		return err
	}
	slog.Info("OVN preflight passed (br-int exists, ovn-controller running)")

	// Connect to NATS
	nc, err := utils.ConnectNATS(cfg.NatsHost, cfg.NatsToken)
	if err != nil {
		slog.Error("Failed to connect to NATS", "err", err)
		return err
	}
	defer nc.Close()

	// Connect to OVN NB DB (required — vpcd is useless without it)
	if cfg.OVNNBAddr == "" {
		return fmt.Errorf("OVN NB DB address not configured (ovn_nb_addr is empty)")
	}

	liveClient := NewLiveOVNClient(cfg.OVNNBAddr)
	ctx := context.Background()
	if err := liveClient.Connect(ctx); err != nil {
		slog.Error("Failed to connect to OVN NB DB", "endpoint", cfg.OVNNBAddr, "err", err)
		return fmt.Errorf("connect OVN NB DB: %w", err)
	}
	defer liveClient.Close()
	slog.Info("Connected to OVN NB DB", "endpoint", cfg.OVNNBAddr)

	// Subscribe to VPC lifecycle topics for OVN topology translation
	topo := NewTopologyHandler(liveClient)
	subs, err := topo.Subscribe(nc)
	if err != nil {
		slog.Error("Failed to subscribe to VPC topics", "err", err)
		return err
	}
	defer func() {
		for _, s := range subs {
			_ = s.Unsubscribe()
		}
	}()

	slog.Info("vpcd service started, waiting for VPC lifecycle events", "subscriptions", len(subs))

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("vpcd service shutting down")
	return nil
}
