package vpcd

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/mulgadc/hive/hive/utils"
)

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

func launchService(cfg *Config) error {
	slog.Info("Starting vpcd service",
		"ovn_nb_addr", cfg.OVNNBAddr,
		"nats_host", cfg.NatsHost,
	)

	// Connect to NATS
	nc, err := utils.ConnectNATS(cfg.NatsHost, cfg.NatsToken)
	if err != nil {
		slog.Error("Failed to connect to NATS", "err", err)
		return err
	}
	defer nc.Close()

	// Connect to OVN NB DB
	var ovn OVNClient
	if cfg.OVNNBAddr != "" {
		liveClient := NewLiveOVNClient(cfg.OVNNBAddr)
		ctx := context.Background()
		if err := liveClient.Connect(ctx); err != nil {
			slog.Warn("Failed to connect to OVN NB DB, operating without OVN",
				"endpoint", cfg.OVNNBAddr, "err", err)
		} else {
			ovn = liveClient
			defer liveClient.Close()
		}
	} else {
		slog.Warn("No OVN NB DB address configured, vpcd will not manage OVN resources")
	}

	if ovn != nil {
		slog.Info("Connected to OVN NB DB, ready for VPC lifecycle events")
	}

	// Subscribe to VPC lifecycle topics for OVN topology translation
	topo := NewTopologyHandler(ovn)
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
