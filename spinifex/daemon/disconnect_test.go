package daemon

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/mulgadc/spinifex/spinifex/vm"
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

// TestMode_NilStored_ReturnsStandalone — bare Daemon with no mode stored returns standalone.
// Covers the d.mode.Load() == nil branch in Mode().
func TestMode_NilStored_ReturnsStandalone(t *testing.T) {
	d := &Daemon{}
	assert.Equal(t, DaemonModeStandalone, d.Mode())
}

// TestMode_WrongTypeStored_ReturnsStandalone — non-string in atomic.Value falls
// back to standalone instead of panicking. Covers the type-assertion !ok branch.
func TestMode_WrongTypeStored_ReturnsStandalone(t *testing.T) {
	d := &Daemon{}
	d.mode.Store(struct{ x string }{x: "not a string"})
	assert.Equal(t, DaemonModeStandalone, d.Mode())
}

// TestOnNATSDisconnect_FlipsMode — direct unit test of the disconnect callback
// without needing an actual NATS disconnect roundtrip.
func TestOnNATSDisconnect_FlipsMode(t *testing.T) {
	d := &Daemon{}
	d.mode.Store(DaemonModeCluster)
	d.onNATSDisconnect(nil, nil)
	assert.Equal(t, DaemonModeStandalone, d.Mode())
}

// TestOnNATSReconnect_NoJetStreamManager_DoesNotPanic — reconnect with
// jsManager nil flips mode + bumps counter but skips the goroutine WriteState.
func TestOnNATSReconnect_NoJetStreamManager_DoesNotPanic(t *testing.T) {
	d := &Daemon{}
	d.mode.Store(DaemonModeStandalone)

	d.onNATSReconnect(nil)

	assert.Equal(t, DaemonModeCluster, d.Mode())
	assert.Equal(t, int64(1), d.NATSRetryCount())
}

// TestLocalStatePath_NilConfig_UsesDefault — defensive path used by callers
// that build a Daemon without populating Config (older test fixtures, etc).
func TestLocalStatePath_NilConfig_UsesDefault(t *testing.T) {
	d := &Daemon{}
	assert.Equal(t, "/var/lib/spinifex/state/instance-state.json", d.localStatePath())
}

// TestLocalStatePath_RootedAtDataDir — verifies 1a's DataDir-rooted layout.
func TestLocalStatePath_RootedAtDataDir(t *testing.T) {
	d := &Daemon{config: &config.Config{DataDir: "/var/lib/spinifex/n1"}}
	assert.Equal(t, "/var/lib/spinifex/n1/state/instance-state.json", d.localStatePath())
}

// TestDaemonWriteState_LocalFile — WriteState persists to the configured DataDir
// even with a nil jsManager (best-effort KV is the second half; local file
// must succeed standalone).
func TestDaemonWriteState_LocalFile(t *testing.T) {
	dataDir := t.TempDir()
	d := &Daemon{
		config: &config.Config{DataDir: dataDir},
		vmMgr:  vm.NewManager(),
	}
	d.vmMgr.Insert(&vm.VM{ID: "i-w1", InstanceType: "t3.micro"})

	require.NoError(t, d.WriteState())

	state, err := ReadLocalState(filepath.Join(dataDir, "state", "instance-state.json"))
	require.NoError(t, err)
	require.NotNil(t, state)
	assert.Equal(t, "t3.micro", state.VMS["i-w1"].InstanceType)
}

// TestDaemonLoadState_MissingFile — fresh-install path: empty map, no error.
func TestDaemonLoadState_MissingFile(t *testing.T) {
	d := &Daemon{
		config: &config.Config{DataDir: t.TempDir()},
		vmMgr:  vm.NewManager(),
	}
	require.NoError(t, d.LoadState())
	assert.Equal(t, 0, d.vmMgr.Count())
}

// TestDaemonLoadState_RoundTrip — write, then read back into a fresh daemon
// rooted at the same DataDir.
func TestDaemonLoadState_RoundTrip(t *testing.T) {
	dataDir := t.TempDir()
	writer := &Daemon{
		config: &config.Config{DataDir: dataDir},
		vmMgr:  vm.NewManager(),
	}
	writer.vmMgr.Insert(&vm.VM{ID: "i-rt1", InstanceType: "m5.large"})
	require.NoError(t, writer.WriteState())

	reader := &Daemon{
		config: &config.Config{DataDir: dataDir},
		vmMgr:  vm.NewManager(),
	}
	require.NoError(t, reader.LoadState())
	assert.Equal(t, 1, reader.vmMgr.Count())
	got, ok := reader.vmMgr.Get("i-rt1")
	require.True(t, ok)
	assert.Equal(t, "m5.large", got.InstanceType)
}

// TestDaemonLoadState_CorruptFile — corruption is fatal, daemon refuses start.
func TestDaemonLoadState_CorruptFile(t *testing.T) {
	dataDir := t.TempDir()
	d := &Daemon{config: &config.Config{DataDir: dataDir}}

	path := d.localStatePath()
	require.NoError(t, writeCorruptStateFile(path))

	err := d.LoadState()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "read local state")
}

func writeCorruptStateFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return os.WriteFile(path, []byte("{not json"), 0o600)
}
