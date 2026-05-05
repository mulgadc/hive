package vm

import "github.com/mulgadc/spinifex/spinifex/types"

// VolumeMounter mounts and unmounts the EBS volumes attached to a VM. The
// real implementation routes ebs.mount / ebs.unmount NATS requests; the
// abstraction keeps NATS out of the manager.
type VolumeMounter interface {
	// Mount mounts every attached volume in v.EBSRequests.Requests, recording
	// the resolved NBDURI back onto each request entry.
	Mount(v *VM) error
	// Unmount sends ebs.unmount for each attached volume. Errors are logged
	// per volume and aggregated; partial failure is tolerated.
	Unmount(v *VM) error

	// MountOne sends ebs.mount for a single request and writes the resolved
	// NBDURI back into req.NBDURI on success. Used by hot-attach.
	MountOne(req *types.EBSRequest) error
	// UnmountOne sends ebs.unmount for a single request. Best-effort: errors
	// are logged and tolerated.
	UnmountOne(req types.EBSRequest)
}
