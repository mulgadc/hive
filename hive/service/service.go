package service

import (
	"fmt"

	"github.com/mulgadc/hive/hive/services/awsgw"
	"github.com/mulgadc/hive/hive/services/hive"
	"github.com/mulgadc/hive/hive/services/hiveui"
	"github.com/mulgadc/hive/hive/services/nats"
	"github.com/mulgadc/hive/hive/services/predastore"
	"github.com/mulgadc/hive/hive/services/viperblockd"
	"github.com/mulgadc/hive/hive/services/vpcd"
)

type Service interface {
	Start() (int, error)
	Stop() error
	Status() (string, error)
	Shutdown() error
	Reload() error
}

func New(btype string, config any) (Service, error) {

	switch btype {
	case "nats":
		return nats.New(config)

	case "predastore":
		return predastore.New(config)

	case "viperblock":
		return viperblockd.New(config)

	case "hive":
		return hive.New(config)

	case "awsgw":
		return awsgw.New(config)

	case "hive-ui":
		return hiveui.New(config)

	case "vpcd":
		return vpcd.New(config)

	}

	return nil, fmt.Errorf("unknown service type: %s", btype)
}
