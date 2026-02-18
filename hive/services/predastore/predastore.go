package predastore

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/predastore/s3"

	// Import backends to trigger their init() registration
	_ "github.com/mulgadc/predastore/backend/filesystem"
)

var serviceName = "predastore"

// Config holds the configuration for the predastore service
type Config struct {
	ConfigPath string
	Port       int
	Host       string
	Debug      bool
	BasePath   string
	TlsCert    string
	TlsKey     string

	Backend s3.BackendType
	NodeID  int

	// Profiling
	PprofEnabled    bool
	PprofOutputPath string
}

// Service wraps the predastore S3 server
type Service struct {
	Config *Config
	server *s3.Server
}

// New creates a new predastore service
func New(config any) (svc *Service, err error) {
	svc = &Service{
		Config: config.(*Config),
	}
	return svc, nil
}

// Start starts the predastore service
func (svc *Service) Start() (int, error) {
	if err := utils.WritePidFileTo(svc.Config.BasePath, serviceName, os.Getpid()); err != nil {
		slog.Error("Failed to write pid file", "err", err)
	}

	server, err := s3.NewServer(
		s3.WithConfigPath(svc.Config.ConfigPath),
		s3.WithAddress(svc.Config.Host, svc.Config.Port),
		s3.WithTLS(svc.Config.TlsCert, svc.Config.TlsKey),
		s3.WithBasePath(svc.Config.BasePath),
		s3.WithDebug(svc.Config.Debug),
		s3.WithBackend(svc.Config.Backend),
		s3.WithNodeID(svc.Config.NodeID),
		s3.WithPprof(svc.Config.PprofEnabled, svc.Config.PprofOutputPath),
	)
	if err != nil {
		slog.Error("Failed to create predastore server", "error", err)
		return 0, err
	}

	svc.server = server

	// Start server asynchronously
	if err := server.ListenAndServeAsync(); err != nil {
		slog.Error("Failed to start predastore server", "error", err)
		return 0, err
	}

	// Wait for shutdown signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	slog.Info("Shutting down predastore service")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		slog.Error("Error during shutdown", "error", err)
	}

	return os.Getpid(), nil
}

// Stop stops the predastore service
func (svc *Service) Stop() error {
	return utils.StopProcessAt(svc.Config.BasePath, serviceName)
}

// Status returns the status of the predastore service
func (svc *Service) Status() (string, error) {
	return "", nil
}

// Shutdown gracefully shuts down the predastore service
func (svc *Service) Shutdown() error {
	if svc.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return svc.server.Shutdown(ctx)
	}
	return svc.Stop()
}

// Reload reloads the predastore service configuration
func (svc *Service) Reload() error {
	return nil
}
