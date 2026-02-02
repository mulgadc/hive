package vm

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestIsValidTransition(t *testing.T) {
	assert.True(t, IsValidTransition(StateRunning, StateStopping))
	assert.True(t, IsValidTransition(StateRunning, StateShuttingDown))
	assert.True(t, IsValidTransition(StateProvisioning, StateRunning))
	assert.True(t, IsValidTransition(StateStopped, StateRunning))
	assert.True(t, IsValidTransition(StateShuttingDown, StateTerminated))
	assert.True(t, IsValidTransition(StateError, StateRunning))

	assert.False(t, IsValidTransition(StateRunning, StateTerminated))
	assert.False(t, IsValidTransition(StateTerminated, StateRunning))
	assert.False(t, IsValidTransition(StateRunning, StateRunning))
	assert.False(t, IsValidTransition(StateStopped, StateStopping))
}

func TestEC2StateCodes_AllStatesHaveMapping(t *testing.T) {
	allStates := []InstanceState{
		StateProvisioning,
		StatePending,
		StateRunning,
		StateStopping,
		StateStopped,
		StateShuttingDown,
		StateTerminated,
		StateError,
	}

	for _, s := range allStates {
		info, ok := EC2StateCodes[s]
		assert.True(t, ok, "State %s should have an EC2 mapping", s)
		assert.NotEmpty(t, info.Name, "State %s EC2 name should not be empty", s)
	}
}

func TestEC2StateCodes_CorrectValues(t *testing.T) {
	expected := map[InstanceState]EC2StateInfo{
		StateProvisioning: {Code: 0, Name: "pending"},
		StatePending:      {Code: 0, Name: "pending"},
		StateRunning:      {Code: 16, Name: "running"},
		StateStopping:     {Code: 64, Name: "stopping"},
		StateStopped:      {Code: 80, Name: "stopped"},
		StateShuttingDown: {Code: 32, Name: "shutting-down"},
		StateTerminated:   {Code: 48, Name: "terminated"},
		StateError:        {Code: 0, Name: "error"},
	}

	for state, exp := range expected {
		actual := EC2StateCodes[state]
		assert.Equal(t, exp.Code, actual.Code, "State %s code mismatch", state)
		assert.Equal(t, exp.Name, actual.Name, "State %s name mismatch", state)
	}
}
