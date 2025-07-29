package service

import (
	"fmt"

	"github.com/mulgadc/hive/hive/services/nats"
	"github.com/mulgadc/hive/hive/services/predastore"
	"github.com/mulgadc/hive/hive/services/viperblockd"
)

type Service interface {
	Start() (int, error)
	Stop() error
	Status() (string, error)
	Shutdown() error
	Reload() error
}

func New(btype string, config interface{}) (Service, error) {

	switch btype {
	case "predastore":
		return predastore.New(config)

	case "nats":
		return nats.New(config)

	case "viperblock":
		return viperblockd.New(config)

	}

	return nil, fmt.Errorf("unknown service type: %s", btype)
}
