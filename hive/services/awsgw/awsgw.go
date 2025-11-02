package awsgw

import (
	"fmt"
	"log"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/gateway"
	"github.com/mulgadc/hive/hive/utils"
	"github.com/nats-io/nats.go"
)

var serviceName = "awsgw"

type Service struct {
	Config *config.Config
}

func New(cfg interface{}) (svc *Service, err error) {
	svc = &Service{
		Config: cfg.(*config.Config),
	}
	return svc, nil
}

func (svc *Service) Start() (int, error) {
	utils.WritePidFile(serviceName, os.Getpid())
	err := launchService(svc.Config)
	if err != nil {
		return 0, err
	}

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

func launchService(config *config.Config) (err error) {

	// Connect to NATS for service communication
	opts := []nats.Option{
		nats.Token(config.NATS.ACL.Token),
		nats.ReconnectWait(time.Second),
		nats.MaxReconnects(-1), // Infinite reconnects
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			log.Printf("NATS disconnected: %v", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			log.Printf("NATS reconnected to %s", nc.ConnectedUrl())
		}),
	}

	natsConn, err := nats.Connect(config.NATS.Host, opts...)
	if err != nil {
		slog.Error("Failed to connect to NATS", "err", err)
		return err
	}
	defer natsConn.Close()

	slog.Info("Connected to NATS server", "host", config.NATS.Host)

	// Append Base dir if config has no leading path
	if config.BaseDir != "" && !strings.HasPrefix(config.AWSGW.Config, "/") {
		config.AWSGW.Config = fmt.Sprintf("%s/%s", config.BaseDir, config.AWSGW.Config)
	}

	// Create gateway with NATS connection
	gw := gateway.GatewayConfig{
		Debug:          config.AWSGW.Debug,
		DisableLogging: false,
		NATSConn:       natsConn,
		Config:         config.AWSGW.Config,
	}

	app := gw.SetupRoutes()

	if err != nil {
		slog.Warn("Failed to setup gateway routes", "err", err)
		return err
	}

	log.Fatal(app.ListenTLS(config.AWSGW.Host, config.AWSGW.TLSCert, config.AWSGW.TLSKey))

	return nil

}
