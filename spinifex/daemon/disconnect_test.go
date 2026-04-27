package daemon

import (
	"net"
	"testing"
	"time"

	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDaemonModeDefaultStandalone — Mode() returns standalone before Start().
func TestDaemonModeDefaultStandalone(t *testing.T) {
	clusterCfg := &config.ClusterConfig{
		Node:  "node-1",
		Nodes: map[string]config.Config{"node-1": {}},
	}
	d, err := NewDaemon(clusterCfg)
	require.NoError(t, err)

	assert.Equal(t, DaemonModeStandalone, d.Mode())
	assert.Equal(t, int64(0), d.NATSRetryCount())
}

// TestDaemonModeFlipsOnConnectAndDisconnect — connecting flips to cluster,
// killing NATS flips back to standalone.
func TestDaemonModeFlipsOnConnectAndDisconnect(t *testing.T) {
	port := freePortForTest(t)
	ns := startTestNATSOnPortForTest(t, port)

	clusterCfg := &config.ClusterConfig{
		Node:  "node-1",
		Nodes: map[string]config.Config{"node-1": {NATS: config.NATSConfig{Host: ns.ClientURL()}}},
	}
	d, err := NewDaemon(clusterCfg)
	require.NoError(t, err)

	require.NoError(t, d.connectNATS())
	defer d.natsConn.Close()
	assert.Equal(t, DaemonModeCluster, d.Mode())

	ns.Shutdown()
	require.Eventually(t, func() bool { return d.Mode() == DaemonModeStandalone }, 3*time.Second, 50*time.Millisecond,
		"mode should flip to standalone after NATS shutdown")
}

// TestDaemonReconnectBumpsRetryCount — reconnect callback fires, count++.
func TestDaemonReconnectBumpsRetryCount(t *testing.T) {
	port := freePortForTest(t)
	ns := startTestNATSOnPortForTest(t, port)

	clusterCfg := &config.ClusterConfig{
		Node:  "node-1",
		Nodes: map[string]config.Config{"node-1": {NATS: config.NATSConfig{Host: ns.ClientURL()}}},
	}
	d, err := NewDaemon(clusterCfg)
	require.NoError(t, err)

	require.NoError(t, d.connectNATS())
	defer d.natsConn.Close()
	require.Equal(t, int64(0), d.NATSRetryCount())

	ns.Shutdown()
	require.Eventually(t, func() bool { return !d.natsConn.IsConnected() }, 3*time.Second, 50*time.Millisecond)

	startTestNATSOnPortForTest(t, port)
	require.Eventually(t, func() bool { return d.Mode() == DaemonModeCluster }, 5*time.Second, 50*time.Millisecond,
		"mode should flip back to cluster on reconnect")
	assert.GreaterOrEqual(t, d.NATSRetryCount(), int64(1))
}

func freePortForTest(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr, ok := l.Addr().(*net.TCPAddr)
	require.True(t, ok)
	port := addr.Port
	require.NoError(t, l.Close())
	return port
}

func startTestNATSOnPortForTest(t *testing.T, port int) *server.Server {
	t.Helper()
	opts := &server.Options{Host: "127.0.0.1", Port: port, NoLog: true, NoSigs: true}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second))
	t.Cleanup(func() { ns.Shutdown() })
	return ns
}
