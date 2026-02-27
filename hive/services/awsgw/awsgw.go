package awsgw

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/gateway"
	handlers_iam "github.com/mulgadc/hive/hive/handlers/iam"
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

	// Connect to NATS for service communication. On concurrent startup the
	// local NATS server may not be listening yet, so retry with backoff.
	natsConn, err := connectNATS(nodeConfig.NATS.Host, nodeConfig.NATS.ACL.Token)
	if err != nil {
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

	// Initialize IAM service with NATS KV backend (required for auth).
	// On multi-node clusters, JetStream KV requires cluster quorum which may
	// not be available yet if nodes start concurrently. Retry with backoff.
	iamService, err := initIAMService(natsConn, masterKey)
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

	// Migrate legacy IAM data to account-scoped keys (idempotent, runs every startup)
	if err := iamService.MigrateToAccountScoped(); err != nil {
		return fmt.Errorf("IAM account migration: %w", err)
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

// connectNATS establishes a connection to NATS with retry/backoff. On concurrent
// startup the local NATS server may not be listening yet.
func connectNATS(host, token string) (*nats.Conn, error) {
	const maxWait = 5 * time.Minute
	retryDelay := 500 * time.Millisecond
	start := time.Now()

	for {
		nc, err := utils.ConnectNATS(host, token)
		if err == nil {
			if time.Since(start) > time.Second {
				slog.Info("NATS connection established", "elapsed", time.Since(start).Round(time.Second))
			}
			return nc, nil
		}

		elapsed := time.Since(start)
		if elapsed >= maxWait {
			return nil, fmt.Errorf("NATS connect failed after %s: %w", elapsed.Round(time.Second), err)
		}

		slog.Warn("NATS not ready, retrying...", "error", err, "elapsed", elapsed.Round(time.Second), "retryIn", retryDelay)
		time.Sleep(retryDelay)
		retryDelay = min(retryDelay*2, 10*time.Second)
	}
}

// initIAMService initializes the IAM service with retry/backoff. On multi-node
// clusters, JetStream requires NATS cluster quorum before KV buckets can be
// created. This retries for up to 5 minutes to allow late-joining nodes.
func initIAMService(natsConn *nats.Conn, masterKey []byte) (*handlers_iam.IAMServiceImpl, error) {
	const maxWait = 5 * time.Minute
	retryDelay := 500 * time.Millisecond
	start := time.Now()
	attempt := 0

	for {
		attempt++
		svc, err := handlers_iam.NewIAMServiceImpl(natsConn, masterKey)
		if err == nil {
			if attempt > 1 {
				slog.Info("IAM service initialized after retry", "attempts", attempt, "elapsed", time.Since(start).Round(time.Second))
			}
			return svc, nil
		}

		elapsed := time.Since(start)
		if elapsed >= maxWait {
			return nil, fmt.Errorf("IAM service unavailable after %s (%d attempts): %w", elapsed.Round(time.Second), attempt, err)
		}

		slog.Warn("IAM service not ready (waiting for JetStream cluster quorum)", "error", err, "attempt", attempt, "elapsed", elapsed.Round(time.Second), "retryIn", retryDelay)
		time.Sleep(retryDelay)
		retryDelay = min(retryDelay*2, 10*time.Second)
	}
}
