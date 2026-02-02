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
	vm.StateStopping:     {vm.StateStopped, vm.StateShuttingDown, vm.StateError},
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

	// Save previous state for rollback if persistence fails
	prevStatus := instance.Status
	var prevCode int64
	var prevName string
	if instance.Instance != nil && instance.Instance.State != nil {
		if instance.Instance.State.Code != nil {
			prevCode = *instance.Instance.State.Code
		}
		if instance.Instance.State.Name != nil {
			prevName = *instance.Instance.State.Name
		}
	}

	instance.Status = target

	if instance.Instance != nil {
		if info, ok := vm.EC2StateCodes[target]; ok {
			instance.Instance.State.SetCode(info.Code)
			instance.Instance.State.SetName(info.Name)
		}
	}

	slog.Info("Instance state transition", "instanceId", instance.ID, "from", string(current), "to", string(target))

	// writeStateLocked is used because we already hold d.Instances.Mu.
	// This prevents race conditions where another transition could occur
	// between state update and persistence.
	if err := d.writeStateLocked(); err != nil {
		// Roll back in-memory state to maintain consistency with persisted state
		instance.Status = prevStatus
		if instance.Instance != nil && instance.Instance.State != nil {
			instance.Instance.State.SetCode(prevCode)
			instance.Instance.State.SetName(prevName)
		}
		d.Instances.Mu.Unlock()
		slog.Error("Failed to persist state after transition, rolled back", "instanceId", instance.ID, "err", err)
		return fmt.Errorf("state transition rolled back, write failed: %w", err)
	}

	d.Instances.Mu.Unlock()
	return nil
}
