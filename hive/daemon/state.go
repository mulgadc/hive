package daemon

import (
	"fmt"
	"log/slog"

	"github.com/mulgadc/hive/hive/vm"
)

// validTransitions defines the allowed state transitions for an instance.
var validTransitions = map[vm.InstanceState][]vm.InstanceState{
	vm.StateProvisioning: {vm.StateRunning, vm.StateError, vm.StateShuttingDown},
	vm.StatePending:      {vm.StateRunning, vm.StateError, vm.StateShuttingDown},
	vm.StateRunning:      {vm.StateStopping, vm.StateShuttingDown, vm.StateError},
	vm.StateStopping:     {vm.StateStopped, vm.StateError},
	vm.StateStopped:      {vm.StateRunning, vm.StateShuttingDown, vm.StateError},
	vm.StateShuttingDown: {vm.StateTerminated, vm.StateError},
	vm.StateError:        {vm.StateRunning, vm.StateShuttingDown},
}

// isValidTransition checks whether moving from current to target is allowed.
func isValidTransition(current, target vm.InstanceState) bool {
	allowed, ok := validTransitions[current]
	if !ok {
		return false
	}
	for _, s := range allowed {
		if s == target {
			return true
		}
	}
	return false
}

// TransitionState validates and applies a state transition on the given instance.
// It sets instance.Status, updates the EC2 state code/name, persists via WriteState, and logs.
// The caller must NOT hold d.Instances.Mu; this method acquires it internally.
func (d *Daemon) TransitionState(instance *vm.VM, target vm.InstanceState) error {
	d.Instances.Mu.Lock()
	current := instance.Status
	if !isValidTransition(current, target) {
		d.Instances.Mu.Unlock()
		return fmt.Errorf("invalid state transition: %s -> %s for instance %s", current, target, instance.ID)
	}

	instance.Status = target

	if instance.Instance != nil {
		if info, ok := vm.EC2StateCodes[target]; ok {
			instance.Instance.State.SetCode(info.Code)
			instance.Instance.State.SetName(info.Name)
		}
	}

	slog.Info("Instance state transition", "instanceId", instance.ID, "from", string(current), "to", string(target))

	// WriteState is called while holding the lock to prevent race conditions
	// where another transition could occur between state update and persistence.
	if err := d.WriteState(); err != nil {
		d.Instances.Mu.Unlock()
		slog.Error("Failed to persist state after transition", "instanceId", instance.ID, "err", err)
		return fmt.Errorf("state persisted in-memory but write failed: %w", err)
	}

	d.Instances.Mu.Unlock()
	return nil
}
