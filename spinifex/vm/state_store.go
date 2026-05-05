package vm

// StateStore persists VM state across the three locations spinifex models:
// per-node "running" state, the cluster-shared "stopped" bucket, and the
// cluster-shared "terminated" bucket.
//
// The interface lives in the vm package; the live implementation is a thin
// adapter in the daemon package wrapping the JetStream-backed bucket.
type StateStore interface {
	// SaveRunningState writes the snapshot of VMs currently running on the
	// given node. The supplied map is owned by the caller and must not be
	// retained.
	SaveRunningState(nodeID string, snapshot map[string]*VM) error
	// LoadRunningState returns the VMs persisted for the given node. An
	// empty (non-nil) map is returned when no state exists.
	LoadRunningState(nodeID string) (map[string]*VM, error)

	WriteStoppedInstance(id string, v *VM) error
	LoadStoppedInstance(id string) (*VM, error)
	DeleteStoppedInstance(id string) error
	ListStoppedInstances() ([]*VM, error)

	WriteTerminatedInstance(id string, v *VM) error
	ListTerminatedInstances() ([]*VM, error)
}
