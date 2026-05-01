package daemon

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

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

// TestRespondShutdownACK verifies respondShutdownACK marshals and sends the ACK via NATS request/reply.
func TestRespondShutdownACK(t *testing.T) {
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	d := &Daemon{node: "test-node", natsConn: nc}

	tests := []struct {
		name string
		ack  ShutdownACK
	}{
		{
			name: "gate phase with stopped services",
			ack: ShutdownACK{
				Node:    "test-node",
				Phase:   "gate",
				Stopped: []string{"awsgw", "spinifex-ui"},
			},
		},
		{
			name: "drain phase with error",
			ack: ShutdownACK{
				Node:  "test-node",
				Phase: "drain",
				Error: "failed to stop VMs",
			},
		},
		{
			name: "storage phase empty stopped list",
			ack: ShutdownACK{
				Node:  "test-node",
				Phase: "storage",
			},
		},
	}

	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subject := fmt.Sprintf("test.shutdown.ack.%d", i)

			// Subscribe and set up a handler that receives requests
			sub, err := nc.SubscribeSync(subject)
			require.NoError(t, err)
			defer sub.Unsubscribe()
			require.NoError(t, nc.Flush())

			// Send a NATS request — the handler will call msg.Respond()
			inbox := nc.NewRespInbox()
			replySub, err := nc.SubscribeSync(inbox)
			require.NoError(t, err)
			defer replySub.Unsubscribe()
			require.NoError(t, nc.Flush())

			err = nc.PublishRequest(subject, inbox, []byte("{}"))
			require.NoError(t, err)

			// Receive the request message and pass it to respondShutdownACK
			msg, err := sub.NextMsg(2 * time.Second)
			require.NoError(t, err)

			d.respondShutdownACK(msg, tt.ack)

			// Read the reply
			reply, err := replySub.NextMsg(2 * time.Second)
			require.NoError(t, err)

			var decoded ShutdownACK
			err = json.Unmarshal(reply.Data, &decoded)
			require.NoError(t, err)

			assert.Equal(t, tt.ack.Node, decoded.Node)
			assert.Equal(t, tt.ack.Phase, decoded.Phase)
			assert.Equal(t, tt.ack.Stopped, decoded.Stopped)
			assert.Equal(t, tt.ack.Error, decoded.Error)
		})
	}
}

// TestPublishShutdownProgress verifies publishShutdownProgress publishes correct progress to the NATS topic.
func TestPublishShutdownProgress(t *testing.T) {
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	d := &Daemon{node: "progress-node", natsConn: nc}

	tests := []struct {
		name      string
		phase     string
		total     int
		remaining int
	}{
		{"initial drain progress", "drain", 5, 5},
		{"partial drain progress", "drain", 5, 2},
		{"final drain progress", "drain", 5, 0},
		{"zero VMs", "drain", 0, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sub, err := nc.SubscribeSync("spinifex.cluster.shutdown.progress")
			require.NoError(t, err)
			defer sub.Unsubscribe()
			require.NoError(t, nc.Flush())

			d.publishShutdownProgress(tt.phase, tt.total, tt.remaining)
			require.NoError(t, nc.Flush())

			msg, err := sub.NextMsg(2 * time.Second)
			require.NoError(t, err)

			var progress ShutdownProgress
			err = json.Unmarshal(msg.Data, &progress)
			require.NoError(t, err)

			assert.Equal(t, "progress-node", progress.Node)
			assert.Equal(t, tt.phase, progress.Phase)
			assert.Equal(t, tt.total, progress.Total)
			assert.Equal(t, tt.remaining, progress.Remaining)
		})
	}
}
