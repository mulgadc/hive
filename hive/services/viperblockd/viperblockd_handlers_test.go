package viperblockd

// Tests for NATS message handlers: ebs.delete, ebs.sync, ebs.unmount (socket cleanup)
// These extend the existing integration tests to cover untested handler paths.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mulgadc/hive/hive/config"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- ebs.delete handler tests ---

func TestIntegration_EBSDeleteMountedVolume(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ns, natsURL := setupEmbeddedNATS(t)
	defer ns.Shutdown()

	// Create a temp socket file to verify cleanup
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "vol-del-test.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("fake-socket"), 0600))

	// Connect a client and create a subscription to use as SnapshotSub
	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	snapSub, err := nc.Subscribe("ebs.snapshot.vol-del-test", func(msg *nats.Msg) {})
	require.NoError(t, err)

	cfg := setupTestConfig(t, natsURL)
	cfg.MountedVolumes = []MountedVolume{
		{
			Name:        "vol-del-test",
			Port:        10809,
			Socket:      socketPath,
			PID:         99999, // Fake PID
			SnapshotSub: snapSub,
		},
	}

	go func() { launchService(cfg) }()
	time.Sleep(500 * time.Millisecond)

	reqData, _ := json.Marshal(config.EBSDeleteRequest{Volume: "vol-del-test"})
	msg, err := nc.Request("ebs.delete", reqData, 3*time.Second)
	require.NoError(t, err)

	var resp config.EBSDeleteResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))

	assert.Equal(t, "vol-del-test", resp.Volume)
	assert.True(t, resp.Success)
	assert.Empty(t, resp.Error)

	// Verify volume removed from config
	cfg.mu.Lock()
	assert.Len(t, cfg.MountedVolumes, 0)
	cfg.mu.Unlock()

	// Verify socket file deleted
	assert.False(t, fileExistsCheck(socketPath))

	// Verify snapshot subscription was unsubscribed
	assert.False(t, snapSub.IsValid())
}

func TestIntegration_EBSDeleteUnmountedVolume(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ns, natsURL := setupEmbeddedNATS(t)
	defer ns.Shutdown()

	cfg := setupTestConfig(t, natsURL)
	// No mounted volumes

	go func() { launchService(cfg) }()
	time.Sleep(500 * time.Millisecond)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	reqData, _ := json.Marshal(config.EBSDeleteRequest{Volume: "vol-not-mounted"})
	msg, err := nc.Request("ebs.delete", reqData, 3*time.Second)
	require.NoError(t, err)

	var resp config.EBSDeleteResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.True(t, resp.Success)
}

func TestIntegration_EBSDeleteInvalidJSON(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ns, natsURL := setupEmbeddedNATS(t)
	defer ns.Shutdown()

	cfg := setupTestConfig(t, natsURL)

	go func() { launchService(cfg) }()
	time.Sleep(500 * time.Millisecond)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	msg, err := nc.Request("ebs.delete", []byte("not json {{{"), 3*time.Second)
	require.NoError(t, err)

	var resp config.EBSDeleteResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.Contains(t, resp.Error, "bad request:")
}

// --- ebs.sync handler tests ---

func TestIntegration_EBSSyncVolumeNotMounted(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ns, natsURL := setupEmbeddedNATS(t)
	defer ns.Shutdown()

	cfg := setupTestConfig(t, natsURL)

	go func() { launchService(cfg) }()
	time.Sleep(500 * time.Millisecond)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	reqData, _ := json.Marshal(config.EBSSyncRequest{Volume: "vol-not-here"})
	msg, err := nc.Request("ebs.sync", reqData, 3*time.Second)
	require.NoError(t, err)

	var resp config.EBSSyncResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.False(t, resp.Synced)
	assert.Contains(t, resp.Error, "not mounted")
}

func TestIntegration_EBSSyncVolumeNoVBInstance(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ns, natsURL := setupEmbeddedNATS(t)
	defer ns.Shutdown()

	cfg := setupTestConfig(t, natsURL)
	cfg.MountedVolumes = []MountedVolume{
		{Name: "vol-no-vb", VB: nil}, // Volume exists but no VB instance
	}

	go func() { launchService(cfg) }()
	time.Sleep(500 * time.Millisecond)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	reqData, _ := json.Marshal(config.EBSSyncRequest{Volume: "vol-no-vb"})
	msg, err := nc.Request("ebs.sync", reqData, 3*time.Second)
	require.NoError(t, err)

	var resp config.EBSSyncResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.False(t, resp.Synced)
	assert.Contains(t, resp.Error, "not mounted")
}

func TestIntegration_EBSSyncInvalidJSON(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ns, natsURL := setupEmbeddedNATS(t)
	defer ns.Shutdown()

	cfg := setupTestConfig(t, natsURL)

	go func() { launchService(cfg) }()
	time.Sleep(500 * time.Millisecond)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	msg, err := nc.Request("ebs.sync", []byte("garbage"), 3*time.Second)
	require.NoError(t, err)

	var resp config.EBSSyncResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.Contains(t, resp.Error, "bad request:")
}

// --- ebs.unmount socket cleanup ---

func TestIntegration_EBSUnmountRemovesSocket(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ns, natsURL := setupEmbeddedNATS(t)
	defer ns.Shutdown()

	// Create an actual temp file as the socket
	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "vol-unmount-socket.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("fake"), 0600))

	cfg := setupTestConfig(t, natsURL)
	cfg.MountedVolumes = []MountedVolume{
		{
			Name:   "vol-unmount-socket",
			Socket: socketPath,
			PID:    99999,
		},
	}

	go func() { launchService(cfg) }()
	time.Sleep(500 * time.Millisecond)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	reqData, _ := json.Marshal(config.EBSRequest{Name: "vol-unmount-socket"})
	msg, err := nc.Request("ebs.test-node.unmount", reqData, 3*time.Second)
	require.NoError(t, err)

	var resp config.EBSUnMountResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.Empty(t, resp.Error)

	// Verify socket file was removed
	assert.False(t, fileExistsCheck(socketPath))
}

// --- ebs.delete removes socket ---

func TestIntegration_EBSDeleteRemovesSocket(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	ns, natsURL := setupEmbeddedNATS(t)
	defer ns.Shutdown()

	tmpDir := t.TempDir()
	socketPath := filepath.Join(tmpDir, "vol-del-socket.sock")
	require.NoError(t, os.WriteFile(socketPath, []byte("fake"), 0600))

	cfg := setupTestConfig(t, natsURL)
	cfg.MountedVolumes = []MountedVolume{
		{Name: "vol-del-socket", Socket: socketPath, PID: 99999},
	}

	go func() { launchService(cfg) }()
	time.Sleep(500 * time.Millisecond)

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	defer nc.Close()

	reqData, _ := json.Marshal(config.EBSDeleteRequest{Volume: "vol-del-socket"})
	msg, err := nc.Request("ebs.delete", reqData, 3*time.Second)
	require.NoError(t, err)

	var resp config.EBSDeleteResponse
	require.NoError(t, json.Unmarshal(msg.Data, &resp))
	assert.True(t, resp.Success)
	assert.False(t, fileExistsCheck(socketPath))
}

// fileExistsCheck is a helper to check if a file exists on disk.
func fileExistsCheck(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
