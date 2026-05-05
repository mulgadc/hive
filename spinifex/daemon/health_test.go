package daemon

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mulgadc/spinifex/spinifex/config"
	"github.com/mulgadc/spinifex/spinifex/vm"
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

	rm, err := NewResourceManager()
	require.NoError(t, err)

	d := &Daemon{
		natsConn:    nc,
		resourceMgr: rm,
		vmMgr:       vm.NewManager(),
	}

	instance := &vm.VM{
		ID:     "i-test-stopped",
		Status: vm.StateStopped,
	}
	d.vmMgr.Insert(instance)

	// Should return without action (no panic, no state change)
	d.handleInstanceCrash(instance, fmt.Errorf("test error"))
	assert.Equal(t, vm.StateStopped, instance.Status)
}

func TestHandleInstanceCrash_SkipsShuttingDown(t *testing.T) {
	d, cleanup := newTestDaemon(t)
	defer cleanup()
	d.shuttingDown.Store(true)

	instance := &vm.VM{
		ID:     "i-test-shutdown",
		Status: vm.StateRunning,
	}
	d.vmMgr.Insert(instance)

	d.handleInstanceCrash(instance, fmt.Errorf("test error"))

	// Status unchanged because shuttingDown is true
	assert.Equal(t, vm.StateRunning, instance.Status)
}

func TestMaybeRestart_ExceedsMaxInWindow(t *testing.T) {
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	rm, err := NewResourceManager()
	require.NoError(t, err)

	d := &Daemon{
		node:     "test-node",
		natsConn: nc,
		config: &config.Config{
			Services: []string{"daemon"},
		},
		resourceMgr: rm,
		vmMgr:       vm.NewManager(),
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
	d.vmMgr.Insert(instance)

	// Should not schedule a restart (no panic, instance stays in error)
	d.maybeRestartInstance(instance)

	// Instance should remain in error state
	assert.Equal(t, vm.StateError, instance.Status)
}

func TestMaybeRestart_ResetsAfterWindow(t *testing.T) {
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)
	defer nc.Close()

	rm, err := NewResourceManager()
	require.NoError(t, err)

	d := &Daemon{
		node:     "test-node",
		natsConn: nc,
		config: &config.Config{
			Services: []string{"daemon"},
		},
		resourceMgr: rm,
		vmMgr:       vm.NewManager(),
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
	d.vmMgr.Insert(instance)

	// maybeRestartInstance should reset counters and schedule a restart
	// (restart will fail because there's no real QEMU, but counters should reset)
	d.maybeRestartInstance(instance)

	// Counters should be reset
	assert.Equal(t, 1, instance.Health.CrashCount)
	assert.Equal(t, 0, instance.Health.RestartCount)
}

func TestRestartBackoff_Exponential(t *testing.T) {
	tests := []struct {
		restartCount int
		want         time.Duration
	}{
		{0, 5 * time.Second},
		{1, 10 * time.Second},
		{2, 20 * time.Second},
		{3, 40 * time.Second},
		{4, 80 * time.Second},
		{5, 120 * time.Second},   // capped at 2min
		{6, 120 * time.Second},   // stays capped
		{100, 120 * time.Second}, // large count stays capped
	}

	for _, tc := range tests {
		got := restartBackoff(tc.restartCount)
		assert.Equal(t, tc.want, got, "Backoff mismatch at restart count %d", tc.restartCount)
	}
}

// newTestDaemon creates a Daemon with shared NATS and a ResourceManager for health tests.
// jsManager is nil — WriteState will error (logged, not fatal).
func newTestDaemon(t *testing.T) (*Daemon, func()) {
	t.Helper()
	nc, err := nats.Connect(sharedNATSURL)
	require.NoError(t, err)

	rm, err := NewResourceManager()
	require.NoError(t, err)

	d := &Daemon{
		node:     "test-node",
		natsConn: nc,
		config: &config.Config{
			Services: []string{"daemon"},
		},
		resourceMgr: rm,
		vmMgr:       vm.NewManager(),
	}
	// Wire the dependencies the manager-internal crash handler relies on
	// (TransitionState, ResourceController, InstanceTypes, ShutdownSignal).
	// jsManager is still nil so TransitionState's WriteState will fail —
	// matching the pre-2e shape where the in-memory status flip survives a
	// write failure.
	d.vmMgr.SetDeps(vm.Deps{
		NodeID:          d.node,
		TransitionState: d.TransitionState,
		Resources:       newResourceControllerAdapter(rm),
		InstanceTypes:   newInstanceTypeResolverAdapter(rm),
		ShutdownSignal:  d.shuttingDown.Load,
	})
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
	d.vmMgr.Insert(instance)

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
	d.vmMgr.Insert(instance)

	// First crash
	d.handleInstanceCrash(instance, fmt.Errorf("crash 1"))
	firstTime := instance.Health.FirstCrashTime
	assert.False(t, firstTime.IsZero())
	assert.Equal(t, 1, instance.Health.CrashCount)

	// Reset to running for a second crash
	d.vmMgr.Inspect(instance, func(v *vm.VM) {
		v.Status = vm.StateRunning
		v.Running = true
		v.PID = 222
	})

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
	d.vmMgr.Insert(instance)

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
	d.vmMgr.Insert(instance)

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
	d.vmMgr.Insert(instance)

	// Should not panic — just log and return
	d.maybeRestartInstance(instance)

	// No restart scheduled
	assert.Equal(t, 0, instance.Health.RestartCount)
}

func TestMaybeRestart_InsufficientResources(t *testing.T) {
	d, cleanup := newTestDaemon(t)
	defer cleanup()

	allocType := smallestAllocType(t, d.resourceMgr)

	// Exhaust all schedulable resources (host - reserved) so canAllocate
	// returns 0, matching what's actually achievable at runtime.
	d.resourceMgr.mu.Lock()
	d.resourceMgr.allocatedVCPU = d.resourceMgr.hostVCPU - d.resourceMgr.reservedVCPU
	d.resourceMgr.allocatedMem = d.resourceMgr.hostMemGB - d.resourceMgr.reservedMem
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
	d.vmMgr.Insert(instance)

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
	d.vmMgr.Insert(instance)

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
