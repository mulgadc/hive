package daemon

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/mulgadc/spinifex/spinifex/vm"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newLocalAPITestDaemon returns a Daemon with just enough wiring to exercise
// the read-only /local/* handlers: a vm.Manager, a node name, and a Config so
// any path-resolving code (revision bump via WriteState) has a temp DataDir.
func newLocalAPITestDaemon(t *testing.T) *Daemon {
	t.Helper()
	d := &Daemon{
		node:   "node-1",
		vmMgr:  vm.NewManager(),
		config: &config.Config{DataDir: t.TempDir()},
	}
	d.mode.Store(DaemonModeStandalone)
	return d
}

func newLocalAPIRouter(d *Daemon) chi.Router {
	r := chi.NewRouter()
	d.registerLocalRoutes(r)
	return r
}

func doGET(t *testing.T, r chi.Router, path string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, req)
	return rec
}

func TestLocalAPI_Instances_Empty(t *testing.T) {
	d := newLocalAPITestDaemon(t)
	rec := doGET(t, newLocalAPIRouter(d), "/local/instances")
	require.Equal(t, http.StatusOK, rec.Code)

	var got []LocalInstance
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, []LocalInstance{}, got)
}

func TestLocalAPI_Instances_ListsVMs(t *testing.T) {
	d := newLocalAPITestDaemon(t)
	d.vmMgr.Insert(&vm.VM{ID: "i-b", Status: vm.StateRunning, PID: 4242})
	d.vmMgr.Insert(&vm.VM{ID: "i-a", Status: vm.StateStopped})

	rec := doGET(t, newLocalAPIRouter(d), "/local/instances")
	require.Equal(t, http.StatusOK, rec.Code)

	var got []LocalInstance
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	require.Len(t, got, 2)
	// Sorted by InstanceID for stable output.
	assert.Equal(t, "i-a", got[0].InstanceID)
	assert.Equal(t, "stopped", got[0].State)
	assert.Zero(t, got[0].PID)
	assert.Equal(t, "i-b", got[1].InstanceID)
	assert.Equal(t, "running", got[1].State)
	assert.Equal(t, 4242, got[1].PID)
}

func TestLocalAPI_Instance_Found(t *testing.T) {
	d := newLocalAPITestDaemon(t)
	d.vmMgr.Insert(&vm.VM{ID: "i-find", Status: vm.StateRunning, PID: 7})

	rec := doGET(t, newLocalAPIRouter(d), "/local/instances/i-find")
	require.Equal(t, http.StatusOK, rec.Code)

	var got LocalInstance
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, LocalInstance{InstanceID: "i-find", State: "running", PID: 7}, got)
}

func TestLocalAPI_Instance_NotFound(t *testing.T) {
	d := newLocalAPITestDaemon(t)
	rec := doGET(t, newLocalAPIRouter(d), "/local/instances/i-missing")
	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestLocalAPI_Status_Standalone_NoNATS(t *testing.T) {
	d := newLocalAPITestDaemon(t)

	rec := doGET(t, newLocalAPIRouter(d), "/local/status")
	require.Equal(t, http.StatusOK, rec.Code)

	var got LocalStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, LocalStatus{
		Node:           "node-1",
		Mode:           DaemonModeStandalone,
		NATS:           natsDisconnected,
		NATSRetryCount: 0,
		Revision:       0,
	}, got)
}

func TestLocalAPI_Status_RetryCountAndRevisionPropagate(t *testing.T) {
	d := newLocalAPITestDaemon(t)
	d.mode.Store(DaemonModeCluster)
	d.natsRetryCount.Store(3)
	require.NoError(t, d.WriteState())
	require.NoError(t, d.WriteState())

	rec := doGET(t, newLocalAPIRouter(d), "/local/status")
	require.Equal(t, http.StatusOK, rec.Code)

	var got LocalStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
	assert.Equal(t, DaemonModeCluster, got.Mode)
	assert.Equal(t, int64(3), got.NATSRetryCount)
	assert.Equal(t, uint64(2), got.Revision)
}

func TestLocalAPI_Status_ContentType(t *testing.T) {
	d := newLocalAPITestDaemon(t)
	rec := doGET(t, newLocalAPIRouter(d), "/local/status")
	assert.Equal(t, "application/json", rec.Header().Get("Content-Type"))
}
