package hive

import (
	"log/slog"
	"os"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/daemon"
	"github.com/mulgadc/hive/hive/utils"
)

var serviceName = "hive"

type Service struct {
	Config     *config.ClusterConfig
	ConfigPath string
}

func New(cfg any) (svc *Service, err error) {
	svc = &Service{
		Config: cfg.(*config.ClusterConfig),
	}
	return svc, nil
}

func (svc *Service) SetConfigPath(path string) {
	svc.ConfigPath = path
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
	err := launchService(svc.Config, svc.ConfigPath)
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

func launchService(config *config.ClusterConfig, configPath string) (err error) {

	d := daemon.NewDaemon(config)
	d.SetConfigPath(configPath)
	slog.Info("Starting Hive daemon ...")
	err = d.Start()

	if err != nil {
		slog.Warn("Failed to start Hive daemon", "err", err)
		return err
	}

	return nil

}
