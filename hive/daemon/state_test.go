package daemon

import (
	"os"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createDaemonWithJetStream creates a daemon backed by an in-process NATS+JetStream server
// so that TransitionState (which calls WriteState) works end-to-end.
func createDaemonWithJetStream(t *testing.T) *Daemon {
	t.Helper()

	jsTmpDir, err := os.MkdirTemp("", "nats-js-state-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(jsTmpDir) })

	opts := &server.Options{
		Host:      "127.0.0.1",
		Port:      -1,
		JetStream: true,
		StoreDir:  jsTmpDir,
		NoLog:     true,
		NoSigs:    true,
	}
	ns, err := server.NewServer(opts)
	require.NoError(t, err)
	go ns.Start()
	require.True(t, ns.ReadyForConnections(5*time.Second), "NATS server failed to start")
	t.Cleanup(func() { ns.Shutdown() })

	natsURL := ns.ClientURL()

	tmpDir, err := os.MkdirTemp("", "hive-state-test-*")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tmpDir) })

	clusterCfg := &config.ClusterConfig{
		Node:  "node-1",
		Nodes: map[string]config.Config{"node-1": {BaseDir: tmpDir}},
	}
	daemon := NewDaemon(clusterCfg)
	daemon.config = &config.Config{BaseDir: tmpDir}

	nc, err := nats.Connect(natsURL)
	require.NoError(t, err)
	t.Cleanup(func() { nc.Close() })

	daemon.natsConn = nc
	daemon.jsManager, err = NewJetStreamManager(nc, 1)
	require.NoError(t, err)
	require.NoError(t, daemon.jsManager.InitKVBucket())

	return daemon
}

func TestTransitionState_ValidTransitions(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	tests := []struct {
		name   string
		from   vm.InstanceState
		to     vm.InstanceState
		ec2    vm.EC2StateInfo
	}{
		{"provisioning->running", vm.StateProvisioning, vm.StateRunning, vm.EC2StateCodes[vm.StateRunning]},
		{"provisioning->error", vm.StateProvisioning, vm.StateError, vm.EC2StateCodes[vm.StateError]},
		{"provisioning->shutting-down", vm.StateProvisioning, vm.StateShuttingDown, vm.EC2StateCodes[vm.StateShuttingDown]},
		{"pending->running", vm.StatePending, vm.StateRunning, vm.EC2StateCodes[vm.StateRunning]},
		{"pending->error", vm.StatePending, vm.StateError, vm.EC2StateCodes[vm.StateError]},
		{"pending->shutting-down", vm.StatePending, vm.StateShuttingDown, vm.EC2StateCodes[vm.StateShuttingDown]},
		{"running->stopping", vm.StateRunning, vm.StateStopping, vm.EC2StateCodes[vm.StateStopping]},
		{"running->shutting-down", vm.StateRunning, vm.StateShuttingDown, vm.EC2StateCodes[vm.StateShuttingDown]},
		{"running->error", vm.StateRunning, vm.StateError, vm.EC2StateCodes[vm.StateError]},
		{"stopping->stopped", vm.StateStopping, vm.StateStopped, vm.EC2StateCodes[vm.StateStopped]},
		{"stopping->shutting-down", vm.StateStopping, vm.StateShuttingDown, vm.EC2StateCodes[vm.StateShuttingDown]},
		{"stopping->error", vm.StateStopping, vm.StateError, vm.EC2StateCodes[vm.StateError]},
		{"stopped->running", vm.StateStopped, vm.StateRunning, vm.EC2StateCodes[vm.StateRunning]},
		{"stopped->shutting-down", vm.StateStopped, vm.StateShuttingDown, vm.EC2StateCodes[vm.StateShuttingDown]},
		{"stopped->error", vm.StateStopped, vm.StateError, vm.EC2StateCodes[vm.StateError]},
		{"shutting-down->terminated", vm.StateShuttingDown, vm.StateTerminated, vm.EC2StateCodes[vm.StateTerminated]},
		{"shutting-down->error", vm.StateShuttingDown, vm.StateError, vm.EC2StateCodes[vm.StateError]},
		{"error->running", vm.StateError, vm.StateRunning, vm.EC2StateCodes[vm.StateRunning]},
		{"error->shutting-down", vm.StateError, vm.StateShuttingDown, vm.EC2StateCodes[vm.StateShuttingDown]},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &vm.VM{
				ID:       "i-test-valid",
				Status:   tt.from,
				Instance: &ec2.Instance{State: &ec2.InstanceState{}},
			}

			daemon.Instances.Mu.Lock()
			daemon.Instances.VMS[instance.ID] = instance
			daemon.Instances.Mu.Unlock()

			err := daemon.TransitionState(instance, tt.to)
			require.NoError(t, err)

			assert.Equal(t, tt.to, instance.Status)
			assert.Equal(t, tt.ec2.Code, *instance.Instance.State.Code)
			assert.Equal(t, tt.ec2.Name, *instance.Instance.State.Name)
		})
	}
}

func TestTransitionState_InvalidTransitions(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	tests := []struct {
		name string
		from vm.InstanceState
		to   vm.InstanceState
	}{
		{"running->running", vm.StateRunning, vm.StateRunning},
		{"running->pending", vm.StateRunning, vm.StatePending},
		{"running->stopped", vm.StateRunning, vm.StateStopped},
		{"running->terminated", vm.StateRunning, vm.StateTerminated},
		{"stopped->stopping", vm.StateStopped, vm.StateStopping},
		{"stopped->terminated", vm.StateStopped, vm.StateTerminated},
		{"terminated->running", vm.StateTerminated, vm.StateRunning},
		{"terminated->stopped", vm.StateTerminated, vm.StateStopped},
		{"stopping->running", vm.StateStopping, vm.StateRunning},
		{"shutting-down->running", vm.StateShuttingDown, vm.StateRunning},
		{"shutting-down->stopped", vm.StateShuttingDown, vm.StateStopped},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &vm.VM{
				ID:       "i-test-invalid",
				Status:   tt.from,
				Instance: &ec2.Instance{State: &ec2.InstanceState{}},
			}

			daemon.Instances.Mu.Lock()
			daemon.Instances.VMS[instance.ID] = instance
			daemon.Instances.Mu.Unlock()

			err := daemon.TransitionState(instance, tt.to)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "invalid state transition")

			// Status should remain unchanged
			assert.Equal(t, tt.from, instance.Status)
		})
	}
}

func TestTransitionState_NilEC2Instance(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	instance := &vm.VM{
		ID:       "i-test-nil-ec2",
		Status:   vm.StateProvisioning,
		Instance: nil, // no EC2 instance metadata
	}

	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[instance.ID] = instance
	daemon.Instances.Mu.Unlock()

	err := daemon.TransitionState(instance, vm.StateRunning)
	require.NoError(t, err)
	assert.Equal(t, vm.StateRunning, instance.Status)
}

func TestEC2StateCodes_AllStatesHaveMapping(t *testing.T) {
	allStates := []vm.InstanceState{
		vm.StateProvisioning,
		vm.StatePending,
		vm.StateRunning,
		vm.StateStopping,
		vm.StateStopped,
		vm.StateShuttingDown,
		vm.StateTerminated,
		vm.StateError,
	}

	for _, s := range allStates {
		info, ok := vm.EC2StateCodes[s]
		assert.True(t, ok, "State %s should have an EC2 mapping", s)
		assert.NotEmpty(t, info.Name, "State %s EC2 name should not be empty", s)
	}
}

func TestEC2StateCodes_CorrectValues(t *testing.T) {
	expected := map[vm.InstanceState]vm.EC2StateInfo{
		vm.StateProvisioning: {Code: 0, Name: "pending"},
		vm.StatePending:      {Code: 0, Name: "pending"},
		vm.StateRunning:      {Code: 16, Name: "running"},
		vm.StateStopping:     {Code: 64, Name: "stopping"},
		vm.StateStopped:      {Code: 80, Name: "stopped"},
		vm.StateShuttingDown: {Code: 32, Name: "shutting-down"},
		vm.StateTerminated:   {Code: 48, Name: "terminated"},
		vm.StateError:        {Code: 0, Name: "error"},
	}

	for state, exp := range expected {
		actual := vm.EC2StateCodes[state]
		assert.Equal(t, exp.Code, actual.Code, "State %s code mismatch", state)
		assert.Equal(t, exp.Name, actual.Name, "State %s name mismatch", state)
	}
}

func TestIsValidTransition(t *testing.T) {
	// Spot-check the exported-via-package function
	assert.True(t, isValidTransition(vm.StateRunning, vm.StateStopping))
	assert.True(t, isValidTransition(vm.StateRunning, vm.StateShuttingDown))
	assert.False(t, isValidTransition(vm.StateRunning, vm.StateTerminated))
	assert.False(t, isValidTransition(vm.StateTerminated, vm.StateRunning))
}
