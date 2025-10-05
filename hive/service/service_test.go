package service

import (
	"testing"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/services/nats"
	"github.com/mulgadc/hive/hive/services/predastore"
	"github.com/mulgadc/hive/hive/services/viperblockd"
	"github.com/stretchr/testify/assert"
)

func TestNew(t *testing.T) {
	// Test known service types
	services := []string{"nats", "predastore", "viperblock", "hive", "awsgw"}

	for _, s := range services {

		var svc Service
		var err error

		switch s {

		// TODO: Standardize service config handling (use config.Config for all?)
		case "nats":
			svc, err = New(s, &nats.Config{})
		case "predastore":
			svc, err = New(s, &predastore.Config{})
			// No special setup needed
		case "viperblock":
			svc, err = New(s, &viperblockd.Config{})
		case "hive":
			svc, err = New(s, &config.Config{})
		case "awsgw":
			svc, err = New(s, &config.Config{})
			// No special setup needed

		}

		assert.NoError(t, err)
		assert.NotNil(t, svc)
	}

	// Test unknown service type
	svc, err := New("unknownservice", nil)
	assert.Error(t, err)
	assert.Nil(t, svc)
}
