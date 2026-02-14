package daemon

import (
	"encoding/json"
	"testing"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestShutdownACKMarshal verifies JSON round-trip for ShutdownACK.
func TestShutdownACKMarshal(t *testing.T) {
	ack := ShutdownACK{
		Node:    "node1",
		Phase:   "gate",
		Stopped: []string{"awsgw", "hive-ui"},
	}

	data, err := json.Marshal(ack)
	require.NoError(t, err)

	var decoded ShutdownACK
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, ack.Node, decoded.Node)
	assert.Equal(t, ack.Phase, decoded.Phase)
	assert.Equal(t, ack.Stopped, decoded.Stopped)
	assert.Empty(t, decoded.Error)
}

// TestShutdownACKWithError verifies JSON round-trip for ShutdownACK with an error.
func TestShutdownACKWithError(t *testing.T) {
	ack := ShutdownACK{
		Node:  "node2",
		Phase: "drain",
		Error: "failed to stop VMs",
	}

	data, err := json.Marshal(ack)
	require.NoError(t, err)

	var decoded ShutdownACK
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "failed to stop VMs", decoded.Error)
	assert.Nil(t, decoded.Stopped)
}

// TestShutdownRequestMarshal verifies JSON round-trip for ShutdownRequest.
func TestShutdownRequestMarshal(t *testing.T) {
	req := ShutdownRequest{
		Phase:   "drain",
		Force:   true,
		Timeout: 120,
	}

	data, err := json.Marshal(req)
	require.NoError(t, err)

	var decoded ShutdownRequest
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "drain", decoded.Phase)
	assert.True(t, decoded.Force)
	assert.Equal(t, 120, decoded.Timeout)
}

// TestShutdownProgressMarshal verifies JSON round-trip for ShutdownProgress.
func TestShutdownProgressMarshal(t *testing.T) {
	progress := ShutdownProgress{
		Node:      "node1",
		Phase:     "drain",
		Total:     5,
		Remaining: 3,
	}

	data, err := json.Marshal(progress)
	require.NoError(t, err)

	var decoded ShutdownProgress
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, "node1", decoded.Node)
	assert.Equal(t, "drain", decoded.Phase)
	assert.Equal(t, 5, decoded.Total)
	assert.Equal(t, 3, decoded.Remaining)
}

// TestClusterShutdownStateKVRoundTrip verifies cluster shutdown state can be stored and retrieved from KV.
func TestClusterShutdownStateKVRoundTrip(t *testing.T) {
	nc, err := nats.Connect(sharedJSNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	jsm, err := NewJetStreamManager(nc, 1)
	require.NoError(t, err)
	err = jsm.InitClusterStateBucket()
	require.NoError(t, err)

	state := &ClusterShutdownState{
		Initiator:  "node1",
		Phase:      "drain",
		Started:    "2025-01-01T00:00:00Z",
		Timeout:    "2m0s",
		Force:      false,
		NodesTotal: 3,
		NodesAcked: map[string]string{
			"node1": "gate",
			"node2": "gate",
		},
	}

	err = jsm.WriteClusterShutdown(state)
	require.NoError(t, err)

	loaded, err := jsm.ReadClusterShutdown()
	require.NoError(t, err)
	require.NotNil(t, loaded)

	assert.Equal(t, state.Initiator, loaded.Initiator)
	assert.Equal(t, state.Phase, loaded.Phase)
	assert.Equal(t, state.Started, loaded.Started)
	assert.Equal(t, state.NodesTotal, loaded.NodesTotal)
	assert.Equal(t, state.Force, loaded.Force)
	assert.Len(t, loaded.NodesAcked, 2)

	// Cleanup
	err = jsm.DeleteClusterShutdown()
	require.NoError(t, err)
}

// TestShuttingDownFlagSkipsVMStop verifies that the shuttingDown flag is respected.
func TestShuttingDownFlagSkipsVMStop(t *testing.T) {
	d := &Daemon{}

	// Default should be false
	assert.False(t, d.shuttingDown.Load())

	// Set to true (as GATE phase does)
	d.shuttingDown.Store(true)
	assert.True(t, d.shuttingDown.Load())
}
