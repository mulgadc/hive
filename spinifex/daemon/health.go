package daemon

import (
	"time"

	"github.com/mulgadc/spinifex/spinifex/vm"
)

// classifyCrashReason is preserved as a daemon-side shim so the existing
// classify-crash-reason tests keep their bare-function call shape while
// the implementation moved into the vm package alongside the manager.
//
// Phase 2f cleanup: migrate TestClassifyCrashReason_* into vm/ and delete.
func classifyCrashReason(waitErr error) string {
	return vm.ClassifyCrashReason(waitErr)
}

// Daemon-side aliases for vm package crash-recovery constants. Tests in
// health_test.go reference these by their pre-2e bare names. Phase 2f
// cleanup migrates those tests into vm/ and removes the aliases.
const (
	maxRestartsInWindow = vm.MaxRestartsInWindow
	restartWindow       = vm.RestartWindow
)

// restartBackoff is a daemon-side shim mirroring classifyCrashReason; the
// pure implementation lives on vm so the manager's MaybeRestart can use
// it without a daemon import.
func restartBackoff(restartCount int) time.Duration {
	return vm.RestartBackoff(restartCount)
}

// handleInstanceCrash and maybeRestartInstance are daemon-side shims
// around the manager methods that own crash recovery. Pre-2f tests still
// invoke them through the daemon receiver; the bodies live in
// vm/crash_recovery.go.
//
// Phase 2f cleanup: migrate the daemon health_test.go cases into vm/ and
// delete the shims.
func (d *Daemon) handleInstanceCrash(instance *vm.VM, waitErr error) {
	d.vmMgr.HandleCrash(instance, waitErr)
}

func (d *Daemon) maybeRestartInstance(instance *vm.VM) {
	d.vmMgr.MaybeRestart(instance)
}
