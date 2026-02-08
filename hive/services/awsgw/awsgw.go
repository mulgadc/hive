package awsgw

import (
	"fmt"
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
	Config *config.ClusterConfig
}

func New(cfg any) (svc *Service, err error) {
	svc = &Service{
		Config: cfg.(*config.ClusterConfig),
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

func launchService(config *config.ClusterConfig) (err error) {

	nodeConfig := config.Nodes[config.Node]

	// Connect to NATS for service communication
	opts := []nats.Option{
		nats.Token(nodeConfig.NATS.ACL.Token),
		nats.ReconnectWait(time.Second),
		nats.MaxReconnects(-1), // Infinite reconnects
		nats.DisconnectErrHandler(func(nc *nats.Conn, err error) {
			slog.Warn("NATS disconnected", "err", err)
		}),
		nats.ReconnectHandler(func(nc *nats.Conn) {
			slog.Info("NATS reconnected", "url", nc.ConnectedUrl())
		}),
	}

	natsConn, err := nats.Connect(nodeConfig.NATS.Host, opts...)
	if err != nil {
		slog.Error("Failed to connect to NATS", "err", err)
		return err
	}
	defer natsConn.Close()

	slog.Info("Connected to NATS server", "host", nodeConfig.NATS.Host)

	// Append Base dir if config has no leading path
	if nodeConfig.BaseDir != "" && !strings.HasPrefix(nodeConfig.AWSGW.Config, "/") {
		nodeConfig.AWSGW.Config = fmt.Sprintf("%s/%s", nodeConfig.BaseDir, nodeConfig.AWSGW.Config)
	}

	// Create gateway with NATS connection
	gw := gateway.GatewayConfig{
		Debug:          nodeConfig.AWSGW.Debug,
		DisableLogging: false,
		NATSConn:       natsConn,
		Config:         nodeConfig.AWSGW.Config,
		ExpectedNodes:  len(config.Nodes),
		Region:         nodeConfig.Region,
		AZ:             nodeConfig.AZ,
		AccessKey:      nodeConfig.AccessKey,
		SecretKey:      nodeConfig.SecretKey,
	}

	app := gw.SetupRoutes()

	if err != nil {
		slog.Warn("Failed to setup gateway routes", "err", err)
		return err
	}

	if err := app.ListenTLS(nodeConfig.AWSGW.Host, nodeConfig.AWSGW.TLSCert, nodeConfig.AWSGW.TLSKey); err != nil {
		slog.Error("Failed to start TLS listener", "err", err)
		os.Exit(1)
	}

	return nil

}
