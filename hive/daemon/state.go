package daemon

import (
	"fmt"
	"log/slog"

	"github.com/mulgadc/hive/hive/vm"
)

// TransitionState validates and applies a state transition on the given instance.
// It sets instance.Status, persists via WriteState, and logs.
// The caller must NOT hold d.Instances.Mu; this method acquires it internally.
func (d *Daemon) TransitionState(instance *vm.VM, target vm.InstanceState) error {
	d.Instances.Mu.Lock()
	current := instance.Status
	if !vm.IsValidTransition(current, target) {
		d.Instances.Mu.Unlock()
		return fmt.Errorf("invalid state transition: %s -> %s for instance %s", current, target, instance.ID)
	}
	instance.Status = target
	d.Instances.Mu.Unlock()

	slog.Info("Instance state transition", "instanceId", instance.ID, "from", string(current), "to", string(target))

	if err := d.WriteState(); err != nil {
		slog.Error("Failed to persist state after transition", "instanceId", instance.ID, "err", err)
		return fmt.Errorf("state persisted in memory but write failed: %w", err)
	}
	return nil
}
