package nats

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats-server/v2/server"
	"go.uber.org/automaxprocs/maxprocs"
)

var serviceName = "nats"

type Config struct {
	ConfigFile string `json:"config_file"`
	Port       int    `json:"port"`
	Host       string `json:"host"`
	Debug      bool   `json:"debug"`
	LogFile    string `json:"log_file"`
	DataDir    string `json:"data_dir"`
	JetStream  bool   `json:"jetstream"`
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

func launchService(config *Config) (err error) {
	// Create proper server options
	opts := &server.Options{}

	// If configFile set use, otherwise set defaults
	if config.ConfigFile != "" {

		opts, err = server.ProcessConfigFile(config.ConfigFile)

		if err != nil {
			slog.Error("Failed to process NATS config file", "err", err)
			return err
		}

	} else {

		opts = &server.Options{
			ConfigFile: config.ConfigFile,
			Port:       config.Port,
			Host:       config.Host,
			Debug:      config.Debug,
			LogFile:    config.LogFile,
			JetStream:  config.JetStream,
		}

		// Set defaults if not provided
		if opts.Port == 0 {
			opts.Port = 4222
		}
		if opts.Host == "" {
			opts.Host = "0.0.0.0"
		}

	}

	fmt.Println(opts)

	// Initialize new server with options
	ns, err := server.NewServer(opts)
	if err != nil {
		slog.Error("Failed to create NATS server", "err", err)
		return err
	}

	// Configure the logger based on the flags.
	ns.ConfigureLogger()

	// Start things up. Block here until done.
	if err := server.Run(ns); err != nil {
		// Will exit() here
		server.PrintAndDie(err.Error())
	}

	// Adjust MAXPROCS if running under linux/cgroups quotas.
	undo, err := maxprocs.Set(maxprocs.Logger(ns.Debugf))
	if err != nil {
		slog.Warn("Failed to set GOMAXPROCS", "err", err)
	} else {
		defer undo()
	}

	ns.WaitForShutdown()

	return nil

}
