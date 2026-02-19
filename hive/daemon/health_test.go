package daemon

import (
	"fmt"
	"os/exec"
	"testing"
	"time"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/vm"
	"github.com/nats-io/nats.go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestClassifyCrashReason_CleanExit(t *testing.T) {
	assert.Equal(t, "clean-exit", classifyCrashReason(nil))
}

func TestClassifyCrashReason_OOMKill(t *testing.T) {
	// Start a process and kill it with SIGKILL to get a real ExitError
	cmd := exec.Command("sleep", "60")
	require.NoError(t, cmd.Start())

	require.NoError(t, cmd.Process.Kill()) // sends SIGKILL
	waitErr := cmd.Wait()
	require.Error(t, waitErr)

	assert.Equal(t, "oom-killed", classifyCrashReason(waitErr))
}

func TestClassifyCrashReason_ExitCode(t *testing.T) {
	// Run a command that exits with a non-zero code
	cmd := exec.Command("sh", "-c", "exit 42")
	waitErr := cmd.Run()
	require.Error(t, waitErr)

	assert.Equal(t, "exit-42", classifyCrashReason(waitErr))
}

func TestClassifyCrashReason_Unknown(t *testing.T) {
	// A non-ExitError error
	err := fmt.Errorf("some random error")
	assert.Equal(t, "unknown", classifyCrashReason(err))
}

func TestHandleInstanceCrash_SkipsNonRunning(t *testing.T) {
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	d := &Daemon{
		natsConn:    nc,
		resourceMgr: NewResourceManager(),
		Instances:   vm.Instances{VMS: make(map[string]*vm.VM)},
	}

	instance := &vm.VM{
		ID:     "i-test-stopped",
		Status: vm.StateStopped,
	}
	d.Instances.VMS[instance.ID] = instance

	// Should return without action (no panic, no state change)
	d.handleInstanceCrash(instance, fmt.Errorf("test error"))
	assert.Equal(t, vm.StateStopped, instance.Status)
}

func TestHandleInstanceCrash_SkipsShuttingDown(t *testing.T) {
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	d := &Daemon{
		natsConn:    nc,
		resourceMgr: NewResourceManager(),
		Instances:   vm.Instances{VMS: make(map[string]*vm.VM)},
	}
	d.shuttingDown.Store(true)

	instance := &vm.VM{
		ID:     "i-test-shutdown",
		Status: vm.StateRunning,
	}
	d.Instances.VMS[instance.ID] = instance

	d.handleInstanceCrash(instance, fmt.Errorf("test error"))

	// Status unchanged because shuttingDown is true
	assert.Equal(t, vm.StateRunning, instance.Status)
}

func TestMaybeRestart_ExceedsMaxInWindow(t *testing.T) {
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	d := &Daemon{
		node:     "test-node",
		natsConn: nc,
		config: &config.Config{
			Services: []string{"daemon"},
		},
		resourceMgr: NewResourceManager(),
		Instances:   vm.Instances{VMS: make(map[string]*vm.VM)},
	}

	// Find an allocatable instance type
	var allocType string
	for typeName := range d.resourceMgr.instanceTypes {
		allocType = typeName
		break
	}
	require.NotEmpty(t, allocType)

	instance := &vm.VM{
		ID:           "i-test-maxcrash",
		Status:       vm.StateError,
		InstanceType: allocType,
		Health: vm.InstanceHealthState{
			CrashCount:     maxRestartsInWindow + 1, // exceeds limit
			FirstCrashTime: time.Now(),
			RestartCount:   maxRestartsInWindow,
		},
	}
	d.Instances.VMS[instance.ID] = instance

	// Should not schedule a restart (no panic, instance stays in error)
	d.maybeRestartInstance(instance)

	// Instance should remain in error state
	assert.Equal(t, vm.StateError, instance.Status)
}

func TestMaybeRestart_ResetsAfterWindow(t *testing.T) {
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	d := &Daemon{
		node:     "test-node",
		natsConn: nc,
		config: &config.Config{
			Services: []string{"daemon"},
		},
		resourceMgr: NewResourceManager(),
		Instances:   vm.Instances{VMS: make(map[string]*vm.VM)},
	}

	// Find an allocatable instance type
	var allocType string
	for typeName := range d.resourceMgr.instanceTypes {
		allocType = typeName
		break
	}
	require.NotEmpty(t, allocType)

	instance := &vm.VM{
		ID:           "i-test-windowreset",
		Status:       vm.StateError,
		InstanceType: allocType,
		Health: vm.InstanceHealthState{
			CrashCount:     5,
			FirstCrashTime: time.Now().Add(-restartWindow - time.Minute), // outside window
			RestartCount:   5,
		},
	}
	d.Instances.VMS[instance.ID] = instance

	// maybeRestartInstance should reset counters and schedule a restart
	// (restart will fail because there's no real QEMU, but counters should reset)
	d.maybeRestartInstance(instance)

	// Counters should be reset
	assert.Equal(t, 1, instance.Health.CrashCount)
	assert.Equal(t, 0, instance.Health.RestartCount)
}

func TestRestartBackoff_Exponential(t *testing.T) {
	// Verify the backoff calculation logic
	expected := []time.Duration{
		5 * time.Second,   // restart 0
		10 * time.Second,  // restart 1
		20 * time.Second,  // restart 2
		40 * time.Second,  // restart 3
		80 * time.Second,  // restart 4
		120 * time.Second, // restart 5 (capped at 2min)
		120 * time.Second, // restart 6 (capped at 2min)
	}

	for i, want := range expected {
		delay := restartBackoffBase
		for range i {
			delay *= 2
			if delay > restartBackoffMax {
				delay = restartBackoffMax
				break
			}
		}
		assert.Equal(t, want, delay, "Backoff mismatch at restart count %d", i)
	}
}
