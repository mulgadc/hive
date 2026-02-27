package awsgw

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/gateway"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
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

func (svc *Service) Start() (int, error) {
	if err := utils.WritePidFileTo(svc.Config.NodeBaseDir(), serviceName, os.Getpid()); err != nil {
		slog.Error("Failed to write pid file", "err", err)
	}
	err := launchService(svc.Config)
	if err != nil {
		return 0, err
	}

	return os.Getpid(), nil
}

func (svc *Service) Stop() (err error) {
	return utils.StopProcessAt(svc.Config.NodeBaseDir(), serviceName)
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

func launchService(config *config.ClusterConfig) error {

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

	// Load IAM master key from disk (required for all authenticated requests)
	masterKeyPath := filepath.Join(nodeConfig.BaseDir, "config", "master.key")
	masterKey, err := handlers_iam.LoadMasterKey(masterKeyPath)
	if err != nil {
		return fmt.Errorf("load IAM master key from %s: %w", masterKeyPath, err)
	}

	// Initialize IAM service with NATS KV backend (required for auth)
	iamService, err := handlers_iam.NewIAMServiceImpl(natsConn, masterKey)
	if err != nil {
		return fmt.Errorf("initialize IAM service: %w", err)
	}

	// First boot: consume bootstrap.json → seed root user into NATS KV → delete file
	bootstrapPath := filepath.Join(nodeConfig.BaseDir, "config", "bootstrap.json")
	if data, err := handlers_iam.LoadBootstrapData(bootstrapPath); err == nil {
		slog.Info("Bootstrap file found, seeding root IAM user")
		if err := iamService.SeedRootUser(data); err != nil {
			return fmt.Errorf("seed root user from bootstrap.json: %w", err)
		}
		if err := os.Remove(bootstrapPath); err != nil {
			slog.Warn("Failed to delete bootstrap file", "path", bootstrapPath, "err", err)
		}
		slog.Info("Bootstrap complete, bootstrap.json deleted")
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
		IAMService:     iamService,
		IAMMasterKey:   masterKey,
	}

	app := gw.SetupRoutes()

	if err := app.ListenTLS(nodeConfig.AWSGW.Host, nodeConfig.AWSGW.TLSCert, nodeConfig.AWSGW.TLSKey); err != nil {
		slog.Error("Failed to start TLS listener", "err", err)
		os.Exit(1)
	}

	return nil

}
