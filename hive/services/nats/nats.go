package nats

import (
	"log"
	"os"

	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats-server/v2/server"
	"go.uber.org/automaxprocs/maxprocs"
)

var serviceName = "nats"

type Config struct {
	Port      int    `json:"port"`
	Host      string `json:"host"`
	Debug     bool   `json:"debug"`
	LogFile   string `json:"log_file"`
	DataDir   string `json:"data_dir"`
	JetStream bool   `json:"jetstream"`
}

type Service struct {
	Config *Config
}

func New(config interface{}) (svc *Service, err error) {
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
	// Create proper server options
	opts := &server.Options{
		Port:      config.Port,
		Host:      config.Host,
		Debug:     config.Debug,
		LogFile:   config.LogFile,
		JetStream: config.JetStream,
	}

	//fmt.Println("opts", opts)

	// Set defaults if not provided
	if opts.Port == 0 {
		opts.Port = 4222
	}
	if opts.Host == "" {
		opts.Host = "0.0.0.0"
	}

	// Initialize new server with options
	ns, err := server.NewServer(opts)
	if err != nil {
		log.Fatalf("Failed to create NATS server: %v", err)
	}

	// Configure the logger based on the flags.
	ns.ConfigureLogger()

	// Start things up. Block here until done.
	if err := server.Run(ns); err != nil {
		server.PrintAndDie(err.Error())
	}

	// Adjust MAXPROCS if running under linux/cgroups quotas.
	undo, err := maxprocs.Set(maxprocs.Logger(ns.Debugf))
	if err != nil {
		ns.Warnf("Failed to set GOMAXPROCS: %v", err)
	} else {
		defer undo()
	}

	ns.WaitForShutdown()

	/*
		ns.ConfigureLogger()

		server.Run(ns)

		// Start the server
		go ns.Start()

		// Wait for server to be ready for connections
		if !ns.ReadyForConnections(4 * time.Second) {
			log.Fatal("NATS server not ready for connections")
		}

		log.Printf("NATS server started on %s:%d", opts.Host, opts.Port)

		// Create a channel to receive shutdown signals
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

		// Wait for shutdown signal
		<-sigChan
		log.Println("Shutting down NATS server gracefully...")

		// Graceful shutdown
		//ns.Shutdown()
		log.Println("NATS server stopped")
	*/

}
