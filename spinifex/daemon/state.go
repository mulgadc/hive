package daemon

import (
	"fmt"
	"log/slog"

	"github.com/mulgadc/spinifex/spinifex/vm"
)

// TransitionState validates and applies a state transition on the given instance.
// It sets instance.Status, persists via WriteState, and logs.
// On validation failure, the instance status is unchanged and an error is returned.
// If WriteState fails, the in-memory status retains the new value (the VM has
// physically changed state regardless) and an error is returned.
func (d *Daemon) TransitionState(instance *vm.VM, target vm.InstanceState) error {
	var (
		current vm.InstanceState
		invalid bool
	)
	d.vmMgr.Inspect(instance, func(v *vm.VM) {
		current = v.Status
		if !vm.IsValidTransition(current, target) {
			invalid = true
			return
		}
		v.Status = target
	})
	if invalid {
		return fmt.Errorf("invalid state transition: %s -> %s for instance %s", current, target, instance.ID)
	}

	slog.Info("Instance state transition", "instanceId", instance.ID, "from", string(current), "to", string(target))

	if err := d.WriteState(); err != nil {
		slog.Error("Failed to persist state after transition", "instanceId", instance.ID,
			"from", string(current), "to", string(target), "err", err)
		return fmt.Errorf("state transition applied but write failed: %w", err)
	}
	return nil
}
