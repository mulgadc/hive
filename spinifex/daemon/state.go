package daemon

import (
	"fmt"
	"log/slog"

	"github.com/mulgadc/spinifex/spinifex/vm"
)

// TransitionState validates and applies a state transition on the given instance.
// It sets instance.Status, persists via WriteState, and logs.
// On validation failure, the instance status is unchanged and a wrapped
// vm.ErrInvalidTransition is returned so callers can map it to the AWS
// IncorrectInstanceState error code via errors.Is.
// If WriteState fails, the in-memory status retains the new value (the VM has
// physically changed state regardless) and an error is returned.
func (d *Daemon) TransitionState(instance *vm.VM, target vm.InstanceState) error {
	var (
		current vm.InstanceState
		invalid bool
	)
	// Inspect (not UpdateState): MarkFailed may invoke this for an instance
	// that is no longer in the running map.
	d.vmMgr.Inspect(instance, func(v *vm.VM) {
		current = v.Status
		if !vm.IsValidTransition(current, target) {
			invalid = true
			return
		}
		v.Status = target
	})
	if invalid {
		return fmt.Errorf("%w: %s -> %s for instance %s",
			vm.ErrInvalidTransition, current, target, instance.ID)
	}

	slog.Info("Instance state transition", "instanceId", instance.ID, "from", string(current), "to", string(target))

	if err := d.WriteState(); err != nil {
		slog.Error("Failed to persist state after transition", "instanceId", instance.ID,
			"from", string(current), "to", string(target), "err", err)
		return fmt.Errorf("state transition applied but write failed: %w", err)
	}
	return nil
}
