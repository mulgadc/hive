package daemon

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"sort"

	"github.com/go-chi/chi/v5"
	"github.com/mulgadc/spinifex/spinifex/vm"
)

// NATS connectivity strings reported by /local/status.
const (
	natsConnected    = "connected"
	natsDisconnected = "disconnected"
)

// LocalInstance is one entry of GET /local/instances. Mirrors the harness shape
// in spinifex/tests/e2e/ddil/harness/daemon_client.go.
type LocalInstance struct {
	InstanceID string `json:"instance_id"`
	State      string `json:"state"`
	PID        int    `json:"pid,omitempty"`
}

// LocalStatus is the response shape for GET /local/status. Reports the
// daemon's connectivity mode plus enough context for an operator to detect
// flapping (nats_retry_count) or stale state (revision).
type LocalStatus struct {
	Node           string `json:"node"`
	Mode           string `json:"mode"`
	NATS           string `json:"nats"`
	NATSRetryCount int64  `json:"nats_retry_count"`
	Revision       uint64 `json:"revision"`
}

// registerLocalRoutes wires the read-only /local/* endpoints onto r. Called
// from ClusterManager so the routes share the existing TLS listener and stay
// reachable while NATS is down.
func (d *Daemon) registerLocalRoutes(r chi.Router) {
	r.Get("/local/instances", d.handleLocalInstances)
	r.Get("/local/instances/{id}", d.handleLocalInstance)
	r.Get("/local/status", d.handleLocalStatus)
}

func (d *Daemon) handleLocalInstances(w http.ResponseWriter, _ *http.Request) {
	out := []LocalInstance{}
	d.vmMgr.View(func(vms map[string]*vm.VM) {
		for _, v := range vms {
			out = append(out, vmToLocalInstance(v))
		}
	})
	sort.Slice(out, func(i, j int) bool { return out[i].InstanceID < out[j].InstanceID })
	writeJSON(w, http.StatusOK, out)
}

func (d *Daemon) handleLocalInstance(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	v, ok := d.vmMgr.Get(id)
	if !ok {
		writeJSONError(w, http.StatusNotFound, "instance not found")
		return
	}
	var li LocalInstance
	d.vmMgr.Inspect(v, func(v *vm.VM) {
		li = vmToLocalInstance(v)
	})
	writeJSON(w, http.StatusOK, li)
}

func (d *Daemon) handleLocalStatus(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, LocalStatus{
		Node:           d.node,
		Mode:           d.Mode(),
		NATS:           d.natsConnectivity(),
		NATSRetryCount: d.NATSRetryCount(),
		Revision:       d.Revision(),
	})
}

// natsConnectivity reports the live NATS link state. Mirrors the check used by
// the /health handler so /local/status agrees with /health on the same node.
func (d *Daemon) natsConnectivity() string {
	if d.natsConn != nil && d.natsConn.IsConnected() {
		return natsConnected
	}
	return natsDisconnected
}

func vmToLocalInstance(v *vm.VM) LocalInstance {
	return LocalInstance{
		InstanceID: v.ID,
		State:      string(v.Status),
		PID:        v.PID,
	}
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("local API encode failed", "error", err)
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
