package awsgw

import (
	"log"
	"log/slog"
	"os"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/gateway"
	"github.com/mulgadc/hive/hive/utils"
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

	/*
		d := daemon.NewDaemon(config)
		slog.Info("Starting AWS-GW daemon ...")
		err = d.Start()
	*/

	gw := gateway.GatewayConfig{
		Debug:          config.AWSGW.Debug,
		DisableLogging: false,
	}

	app := gw.SetupRoutes()

	if err != nil {
		slog.Warn("Failed to start AWS-GW daemon", "err", err)
		return err
	}

	log.Fatal(app.ListenTLS(config.AWSGW.Host, config.AWSGW.TLSCert, config.AWSGW.TLSKey))

	return nil

}
