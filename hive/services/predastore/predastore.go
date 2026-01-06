package predastore

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mulgadc/hive/hive/utils"
	"github.com/mulgadc/predastore/s3"
	"go.uber.org/automaxprocs/maxprocs"
)

var serviceName = "predastore"

type Config struct {
	ConfigPath string
	Port       int
	Host       string
	Debug      bool
	BasePath   string
	TlsCert    string
	TlsKey     string
}

type Service struct {
	Config *Config
}

func New(config any) (svc *Service, err error) {
	svc = &Service{
		Config: config.(*Config),
	}

	return svc, nil
}

func (svc *Service) Start() (int, error) {

	utils.WritePidFile(serviceName, os.Getpid())
	launchService(svc.Config)

	return os.Getpid(), nil
}

func (svc *Service) Stop() (err error) {

	err = utils.StopProcess(serviceName)
	return err
}

func (svc *Service) Status() (string, error) {
	return "", nil
}

func (svc *Service) Shutdown() (err error) {
	return svc.Stop()
}

func (svc *Service) Reload() (err error) {
	return nil
}

func launchService(config *Config) {

	s3 := s3.New(&s3.Config{
		ConfigPath: config.ConfigPath,
		Port:       config.Port,
		Host:       config.Host,
		Debug:      config.Debug,
		BasePath:   config.BasePath,
	})

	// Adjust MAXPROCS if running under linux/cgroups quotas.
	undo, err := maxprocs.Set(maxprocs.Logger(log.Printf))
	if err != nil {
		log.Printf("Failed to set GOMAXPROCS: %v", err)
	} else {
		defer undo()
	}

	err = s3.ReadConfig()

	if err != nil {
		slog.Warn("Error reading config file", "error", err)
		os.Exit(-1)
	}

	app := s3.SetupRoutes()

	go func() {
		log.Fatal(app.ListenTLS(fmt.Sprintf("%s:%d", config.Host, config.Port), config.TlsCert, config.TlsKey))
	}()

	// Create a channel to receive shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down gracefully...")

	// Graceful shutdown with timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := app.ShutdownWithContext(ctx); err != nil {
		log.Printf("Error during shutdown: %v", err)
	}

	log.Println("Server stopped")

}
