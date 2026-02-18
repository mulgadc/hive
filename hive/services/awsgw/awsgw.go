package awsgw

import (
	"fmt"
	"log/slog"
	"os"
	"strings"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/gateway"
	"github.com/mulgadc/hive/hive/utils"
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

func (svc *Service) nodeBaseDir() string {
	if svc.Config == nil || svc.Config.Node == "" {
		return ""
	}
	if node, ok := svc.Config.Nodes[svc.Config.Node]; ok {
		return node.BaseDir
	}
	return ""
}

func (svc *Service) Start() (int, error) {
	if err := utils.WritePidFileTo(svc.nodeBaseDir(), serviceName, os.Getpid()); err != nil {
		slog.Error("Failed to write pid file", "err", err)
	}
	err := launchService(svc.Config)
	if err != nil {
		return 0, err
	}

	return os.Getpid(), nil
}

func (svc *Service) Stop() (err error) {
	return utils.StopProcessAt(svc.nodeBaseDir(), serviceName)
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
	natsConn, err := utils.ConnectNATS(nodeConfig.NATS.Host, nodeConfig.NATS.ACL.Token)
	if err != nil {
		slog.Error("Failed to connect to NATS", "err", err)
		return err
	}
	defer natsConn.Close()

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
