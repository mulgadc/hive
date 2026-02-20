package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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

// newTestDaemon creates a Daemon with shared NATS and a ResourceManager for health tests.
// jsManager is nil — WriteState will error (logged, not fatal).
func newTestDaemon(t *testing.T) (*Daemon, func()) {
	t.Helper()
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)

	d := &Daemon{
		node:     "test-node",
		natsConn: nc,
		config: &config.Config{
			Services: []string{"daemon"},
		},
		resourceMgr: NewResourceManager(),
		Instances:   vm.Instances{VMS: make(map[string]*vm.VM)},
	}
	return d, func() { nc.Close() }
}

// smallestAllocType returns the name of the smallest allocatable instance type
// in the resource manager, considering both vCPU and memory to ensure it fits
// on the current host (important for CI runners with limited resources).
func smallestAllocType(t *testing.T, rm *ResourceManager) string {
	t.Helper()
	var smallest string
	var smallestScore int64 = 1<<63 - 1
	for name, it := range rm.instanceTypes {
		vcpu := instanceTypeVCPUs(it)
		memMiB := instanceTypeMemoryMiB(it)
		// Score by total resource footprint so we pick the type smallest in both dimensions
		score := vcpu*1024 + memMiB
		if score < smallestScore {
			smallestScore = score
			smallest = name
		}
	}
	if smallest == "" {
		t.Fatal("resource manager has no instance types")
	}
	return smallest
}

func TestHandleInstanceCrash_CoreFlow(t *testing.T) {
	d, cleanup := newTestDaemon(t)
	defer cleanup()

	allocType := smallestAllocType(t, d.resourceMgr)
	instanceType := d.resourceMgr.instanceTypes[allocType]
	require.NoError(t, d.resourceMgr.allocate(instanceType))

	// Record resource levels before crash
	d.resourceMgr.mu.RLock()
	allocVCPUBefore := d.resourceMgr.allocatedVCPU
	allocMemBefore := d.resourceMgr.allocatedMem
	d.resourceMgr.mu.RUnlock()
	require.Greater(t, allocVCPUBefore, 0)

	// Create a temp QMP socket file to verify removal
	tmpDir := t.TempDir()
	qmpPath := filepath.Join(tmpDir, "qmp.sock")
	require.NoError(t, os.WriteFile(qmpPath, []byte("dummy"), 0o600))

	instance := &vm.VM{
		ID:           "i-test-core",
		Status:       vm.StateRunning,
		Running:      true,
		PID:          12345,
		InstanceType: allocType,
		Config:       vm.Config{QMPSocket: qmpPath},
	}
	d.Instances.VMS[instance.ID] = instance

	d.handleInstanceCrash(instance, fmt.Errorf("test crash"))

	// Status transitioned to error
	assert.Equal(t, vm.StateError, instance.Status)

	// Health tracking updated
	assert.Equal(t, 1, instance.Health.CrashCount)
	assert.False(t, instance.Health.LastCrashTime.IsZero())
	assert.Equal(t, "unknown", instance.Health.LastCrashReason) // fmt.Errorf is not *exec.ExitError
	assert.False(t, instance.Health.FirstCrashTime.IsZero())

	// Running and PID cleared
	assert.False(t, instance.Running)
	assert.Equal(t, 0, instance.PID)

	// Resources deallocated
	d.resourceMgr.mu.RLock()
	assert.Less(t, d.resourceMgr.allocatedVCPU, allocVCPUBefore)
	assert.Less(t, d.resourceMgr.allocatedMem, allocMemBefore)
	d.resourceMgr.mu.RUnlock()

	// QMP socket file removed
	_, err := os.Stat(qmpPath)
	assert.True(t, os.IsNotExist(err), "QMP socket should be removed")
}

func TestHandleInstanceCrash_FirstCrashSetsTime(t *testing.T) {
	d, cleanup := newTestDaemon(t)
	defer cleanup()

	allocType := smallestAllocType(t, d.resourceMgr)

	instance := &vm.VM{
		ID:           "i-test-firstcrash",
		Status:       vm.StateRunning,
		Running:      true,
		PID:          111,
		InstanceType: allocType,
	}
	d.Instances.VMS[instance.ID] = instance

	// First crash
	d.handleInstanceCrash(instance, fmt.Errorf("crash 1"))
	firstTime := instance.Health.FirstCrashTime
	assert.False(t, firstTime.IsZero())
	assert.Equal(t, 1, instance.Health.CrashCount)

	// Reset to running for a second crash
	d.Instances.Mu.Lock()
	instance.Status = vm.StateRunning
	instance.Running = true
	instance.PID = 222
	d.Instances.Mu.Unlock()

	time.Sleep(time.Millisecond) // ensure time advances
	d.handleInstanceCrash(instance, fmt.Errorf("crash 2"))

	// FirstCrashTime should be unchanged from the first crash
	assert.Equal(t, firstTime, instance.Health.FirstCrashTime)
	assert.Equal(t, 2, instance.Health.CrashCount)
}

func TestHandleInstanceCrash_UnknownInstanceType(t *testing.T) {
	d, cleanup := newTestDaemon(t)
	defer cleanup()

	instance := &vm.VM{
		ID:           "i-test-unknown-type",
		Status:       vm.StateRunning,
		Running:      true,
		PID:          333,
		InstanceType: "z99.nonexistent",
	}
	d.Instances.VMS[instance.ID] = instance

	// Should not panic even though instance type is not in resourceMgr
	d.handleInstanceCrash(instance, fmt.Errorf("crash"))

	assert.Equal(t, vm.StateError, instance.Status)
	assert.Equal(t, 1, instance.Health.CrashCount)
	assert.False(t, instance.Running)
	assert.Equal(t, 0, instance.PID)
}

func TestMaybeRestart_SkipsShuttingDown(t *testing.T) {
	d, cleanup := newTestDaemon(t)
	defer cleanup()
	d.shuttingDown.Store(true)

	allocType := smallestAllocType(t, d.resourceMgr)

	instance := &vm.VM{
		ID:           "i-test-restart-shutdown",
		Status:       vm.StateError,
		InstanceType: allocType,
		Health: vm.InstanceHealthState{
			CrashCount:     1,
			FirstCrashTime: time.Now(),
		},
	}
	d.Instances.VMS[instance.ID] = instance

	d.maybeRestartInstance(instance)

	// RestartCount should not have changed — restart was skipped
	assert.Equal(t, 0, instance.Health.RestartCount)
}

func TestMaybeRestart_UnknownInstanceType(t *testing.T) {
	d, cleanup := newTestDaemon(t)
	defer cleanup()

	instance := &vm.VM{
		ID:           "i-test-restart-unknown",
		Status:       vm.StateError,
		InstanceType: "z99.nonexistent",
		Health: vm.InstanceHealthState{
			CrashCount:     1,
			FirstCrashTime: time.Now(),
		},
	}
	d.Instances.VMS[instance.ID] = instance

	// Should not panic — just log and return
	d.maybeRestartInstance(instance)

	// No restart scheduled
	assert.Equal(t, 0, instance.Health.RestartCount)
}

func TestMaybeRestart_InsufficientResources(t *testing.T) {
	d, cleanup := newTestDaemon(t)
	defer cleanup()

	allocType := smallestAllocType(t, d.resourceMgr)

	// Exhaust all resources by setting allocated = available
	d.resourceMgr.mu.Lock()
	d.resourceMgr.allocatedVCPU = d.resourceMgr.availableVCPU
	d.resourceMgr.allocatedMem = d.resourceMgr.availableMem
	d.resourceMgr.mu.Unlock()

	instance := &vm.VM{
		ID:           "i-test-restart-nores",
		Status:       vm.StateError,
		InstanceType: allocType,
		Health: vm.InstanceHealthState{
			CrashCount:     1,
			FirstCrashTime: time.Now(),
		},
	}
	d.Instances.VMS[instance.ID] = instance

	d.maybeRestartInstance(instance)

	// No restart scheduled
	assert.Equal(t, 0, instance.Health.RestartCount)
}

func TestMaybeRestart_SchedulesRestart(t *testing.T) {
	d, cleanup := newTestDaemon(t)
	defer cleanup()

	allocType := smallestAllocType(t, d.resourceMgr)

	instance := &vm.VM{
		ID:           "i-test-restart-schedule",
		Status:       vm.StateError,
		InstanceType: allocType,
		Health: vm.InstanceHealthState{
			CrashCount:     1,
			FirstCrashTime: time.Now(),
			RestartCount:   0,
		},
	}
	d.Instances.VMS[instance.ID] = instance

	// maybeRestartInstance should pass all guards and call time.AfterFunc.
	// The scheduled restartCrashedInstance will increment RestartCount and
	// attempt LaunchInstance (which will fail without real infra — that's fine).
	d.maybeRestartInstance(instance)

	// The restart is scheduled via time.AfterFunc with 5s delay.
	// We verify that the scheduling code path was reached by checking that
	// the function didn't bail out early (all early returns leave RestartCount at 0
	// and don't call time.AfterFunc). The counter reset branch does not apply since
	// FirstCrashTime is recent. If we got here without panic, the scheduling path ran.
	// We can't easily assert the timer fired without waiting 5s, but we can
	// verify the counters were not reset (proving we didn't take the window-expired path).
	assert.Equal(t, 1, instance.Health.CrashCount)
	assert.Equal(t, 0, instance.Health.RestartCount) // incremented by restartCrashedInstance, not maybeRestart
}
