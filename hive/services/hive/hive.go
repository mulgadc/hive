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

	d := daemon.NewDaemon(config)
	slog.Info("Starting Hive daemon ...")
	err = d.Start()

	if err != nil {
		slog.Warn("Failed to start Hive daemon: %v", err)
		return err
	}

	return nil

}
