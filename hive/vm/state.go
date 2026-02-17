package vm

import "slices"

// InstanceState represents a typed VM lifecycle state.
type InstanceState string

const (
	StateProvisioning InstanceState = "provisioning"
	StatePending      InstanceState = "pending"
	StateRunning      InstanceState = "running"
	StateStopping     InstanceState = "stopping"
	StateStopped      InstanceState = "stopped"
	StateShuttingDown InstanceState = "shutting-down"
	StateTerminated   InstanceState = "terminated"
	StateError        InstanceState = "error"
)

// EC2StateInfo holds the EC2 API code and name for a given instance state.
type EC2StateInfo struct {
	Code int64
	Name string
}

// EC2StateCodes maps each InstanceState to its EC2 API code and name.
// Note: StateError and StateProvisioning are Hive-specific states with no direct
// AWS EC2 equivalent. Their Code/Name values are best-effort mappings.
var EC2StateCodes = map[InstanceState]EC2StateInfo{
	StateProvisioning: {Code: 0, Name: "pending"},
	StatePending:      {Code: 0, Name: "pending"},
	StateRunning:      {Code: 16, Name: "running"},
	StateStopping:     {Code: 64, Name: "stopping"},
	StateStopped:      {Code: 80, Name: "stopped"},
	StateShuttingDown: {Code: 32, Name: "shutting-down"},
	StateTerminated:   {Code: 48, Name: "terminated"},
	StateError:        {Code: 0, Name: "error"},
}

// ValidTransitions defines the allowed state transitions for an instance.
// StateTerminated is intentionally absent — it is a terminal state with no valid transitions.
var ValidTransitions = map[InstanceState][]InstanceState{
	StateProvisioning: {StateRunning, StateError, StateShuttingDown},
	StatePending:      {StateRunning, StateError, StateShuttingDown},
	StateRunning:      {StateStopping, StateShuttingDown, StateError},
	StateStopping:     {StateStopped, StateShuttingDown, StateError},
	StateStopped:      {StateRunning, StateShuttingDown, StateError},
	StateShuttingDown: {StateTerminated, StateError},
	StateError:        {StatePending, StateRunning, StateShuttingDown},
}

// IsValidTransition checks whether moving from current to target is allowed.
func IsValidTransition(current, target InstanceState) bool {
	allowed, ok := ValidTransitions[current]
	if !ok {
		return false
	}
	return slices.Contains(allowed, target)
}
