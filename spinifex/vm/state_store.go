package vm

// StateStore persists VM state to the per-node "running" bucket and to the
// cluster-shared "stopped" / "terminated" buckets via the manager.
//
// The interface lives in the vm package; the live implementation is a thin
// adapter in the daemon package wrapping the JetStream-backed bucket.
// Daemon-only callers that need read/list/delete on the stopped/terminated
// buckets reach into the JetStream manager directly.
type StateStore interface {
	// SaveRunningState writes the snapshot of VMs currently running on the
	// given node. The supplied map is owned by the caller and must not be
	// retained.
	SaveRunningState(nodeID string, snapshot map[string]*VM) error
	// LoadRunningState returns the VMs persisted for the given node. An
	// empty (non-nil) map is returned when no state exists.
	LoadRunningState(nodeID string) (map[string]*VM, error)

	WriteStoppedInstance(id string, v *VM) error
	WriteTerminatedInstance(id string, v *VM) error
}
