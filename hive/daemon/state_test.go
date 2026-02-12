package daemon

import (
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/mulgadc/hive/hive/config"
	"github.com/mulgadc/hive/hive/qmp"
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
		name string
		from vm.InstanceState
		to   vm.InstanceState
	}{
		{"provisioning->running", vm.StateProvisioning, vm.StateRunning},
		{"provisioning->error", vm.StateProvisioning, vm.StateError},
		{"provisioning->shutting-down", vm.StateProvisioning, vm.StateShuttingDown},
		{"pending->running", vm.StatePending, vm.StateRunning},
		{"pending->error", vm.StatePending, vm.StateError},
		{"pending->shutting-down", vm.StatePending, vm.StateShuttingDown},
		{"running->stopping", vm.StateRunning, vm.StateStopping},
		{"running->shutting-down", vm.StateRunning, vm.StateShuttingDown},
		{"running->error", vm.StateRunning, vm.StateError},
		{"stopping->stopped", vm.StateStopping, vm.StateStopped},
		{"stopping->shutting-down", vm.StateStopping, vm.StateShuttingDown},
		{"stopping->error", vm.StateStopping, vm.StateError},
		{"stopped->running", vm.StateStopped, vm.StateRunning},
		{"stopped->shutting-down", vm.StateStopped, vm.StateShuttingDown},
		{"stopped->error", vm.StateStopped, vm.StateError},
		{"shutting-down->terminated", vm.StateShuttingDown, vm.StateTerminated},
		{"shutting-down->error", vm.StateShuttingDown, vm.StateError},
		{"error->running", vm.StateError, vm.StateRunning},
		{"error->shutting-down", vm.StateError, vm.StateShuttingDown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instance := &vm.VM{
				ID:     "i-test-valid",
				Status: tt.from,
			}

			daemon.Instances.Mu.Lock()
			daemon.Instances.VMS[instance.ID] = instance
			daemon.Instances.Mu.Unlock()

			err := daemon.TransitionState(instance, tt.to)
			require.NoError(t, err)

			assert.Equal(t, tt.to, instance.Status)
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
				ID:     "i-test-invalid",
				Status: tt.from,
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

// allStates is the canonical list of every InstanceState in the system.
var allStates = []vm.InstanceState{
	vm.StateProvisioning,
	vm.StatePending,
	vm.StateRunning,
	vm.StateStopping,
	vm.StateStopped,
	vm.StateShuttingDown,
	vm.StateTerminated,
	vm.StateError,
}

// stableStates are states where an instance can rest indefinitely.
var stableStates = []vm.InstanceState{
	vm.StateRunning,
	vm.StateStopped,
	vm.StateTerminated,
	vm.StateError,
}

// transitionalStates are states that must resolve to a stable state.
var transitionalStates = []vm.InstanceState{
	vm.StateProvisioning,
	vm.StatePending,
	vm.StateStopping,
	vm.StateShuttingDown,
}

// TestValidTransitions_MapCoversAllNonTerminalStates verifies that every state
// except StateTerminated has at least one valid outgoing transition. A missing
// entry would mean an instance could get permanently stuck.
func TestValidTransitions_MapCoversAllNonTerminalStates(t *testing.T) {
	for _, state := range allStates {
		if state == vm.StateTerminated {
			continue
		}
		targets, ok := vm.ValidTransitions[state]
		assert.True(t, ok, "state %q missing from ValidTransitions map", state)
		assert.NotEmpty(t, targets, "state %q has no valid transitions — instances would get stuck", state)
	}
}

// TestTerminatedState_HasNoOutgoingTransitions ensures the terminal state is
// truly terminal — no valid transitions out.
func TestTerminatedState_HasNoOutgoingTransitions(t *testing.T) {
	targets, ok := vm.ValidTransitions[vm.StateTerminated]
	assert.False(t, ok || len(targets) > 0,
		"StateTerminated should have no valid transitions, got %v", targets)
}

// TestTransitionalStates_CanReachStableState verifies that every transitional
// state has at least one direct transition to a stable state. This is the
// structural guarantee that prevents the "stuck in stopping" bug.
func TestTransitionalStates_CanReachStableState(t *testing.T) {
	stableSet := map[vm.InstanceState]bool{}
	for _, s := range stableStates {
		stableSet[s] = true
	}

	for _, state := range transitionalStates {
		targets := vm.ValidTransitions[state]
		hasStableTarget := false
		for _, target := range targets {
			if stableSet[target] {
				hasStableTarget = true
				break
			}
		}
		assert.True(t, hasStableTarget,
			"transitional state %q has no direct path to a stable state (targets: %v)", state, targets)
	}
}

// TestEveryState_CanReachTerminated does a BFS from each non-terminated state
// to confirm there is always a path to StateTerminated. An unreachable terminal
// state means instances can never be fully cleaned up.
func TestEveryState_CanReachTerminated(t *testing.T) {
	for _, start := range allStates {
		if start == vm.StateTerminated {
			continue
		}

		visited := map[vm.InstanceState]bool{start: true}
		queue := []vm.InstanceState{start}
		found := false

		for len(queue) > 0 {
			current := queue[0]
			queue = queue[1:]
			if current == vm.StateTerminated {
				found = true
				break
			}
			for _, next := range vm.ValidTransitions[current] {
				if !visited[next] {
					visited[next] = true
					queue = append(queue, next)
				}
			}
		}

		assert.True(t, found,
			"state %q has no path to StateTerminated — instances starting here can never be cleaned up", start)
	}
}

// TestStopLifecycle_CompletesFullChain walks the full stop lifecycle:
// provisioning → running → stopping → stopped, then restart and terminate:
// stopped → running → shutting-down → terminated.
func TestStopLifecycle_CompletesFullChain(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	instance := &vm.VM{ID: "i-lifecycle-stop", Status: vm.StateProvisioning}
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[instance.ID] = instance
	daemon.Instances.Mu.Unlock()

	chain := []vm.InstanceState{
		vm.StateRunning,      // launch
		vm.StateStopping,     // stop requested
		vm.StateStopped,      // stop completed
		vm.StateRunning,      // restart
		vm.StateShuttingDown, // terminate requested
		vm.StateTerminated,   // terminate completed
	}

	for _, target := range chain {
		err := daemon.TransitionState(instance, target)
		require.NoError(t, err, "transition to %s failed (current: %s)", target, instance.Status)
	}

	assert.Equal(t, vm.StateTerminated, instance.Status)
}

// TestStopLifecycle_ErrorRecovery verifies that an instance that hits an error
// during stop can recover: running → stopping → error → shutting-down → terminated.
func TestStopLifecycle_ErrorRecovery(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	instance := &vm.VM{ID: "i-lifecycle-err", Status: vm.StateRunning}
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[instance.ID] = instance
	daemon.Instances.Mu.Unlock()

	chain := []vm.InstanceState{
		vm.StateStopping,
		vm.StateError,        // stop failed
		vm.StateShuttingDown, // force terminate from error
		vm.StateTerminated,
	}

	for _, target := range chain {
		err := daemon.TransitionState(instance, target)
		require.NoError(t, err, "transition to %s failed (current: %s)", target, instance.Status)
	}

	assert.Equal(t, vm.StateTerminated, instance.Status)
}

// TestShuttingDown_ErrorRecovery verifies: shutting-down → error → shutting-down → terminated.
func TestShuttingDown_ErrorRecovery(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	instance := &vm.VM{ID: "i-shutdown-err", Status: vm.StateShuttingDown}
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[instance.ID] = instance
	daemon.Instances.Mu.Unlock()

	chain := []vm.InstanceState{
		vm.StateError,        // terminate failed
		vm.StateShuttingDown, // retry terminate
		vm.StateTerminated,   // success
	}

	for _, target := range chain {
		err := daemon.TransitionState(instance, target)
		require.NoError(t, err, "transition to %s failed (current: %s)", target, instance.Status)
	}

	assert.Equal(t, vm.StateTerminated, instance.Status)
}

// TestTransitionState_ConcurrentTransitions hammers the same instance with
// concurrent transitions to verify the mutex protects against races and
// the instance always ends in a valid state.
func TestTransitionState_ConcurrentTransitions(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	instance := &vm.VM{ID: "i-concurrent", Status: vm.StateRunning}
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[instance.ID] = instance
	daemon.Instances.Mu.Unlock()

	// Multiple goroutines race to transition from running.
	// After one succeeds, the others may chain from the new state
	// (e.g., running→error, then error→shutting-down), which is correct.
	targets := []vm.InstanceState{vm.StateStopping, vm.StateShuttingDown, vm.StateError}

	var wg sync.WaitGroup
	results := make([]error, len(targets))

	for i, target := range targets {
		wg.Add(1)
		go func(idx int, tgt vm.InstanceState) {
			defer wg.Done()
			results[idx] = daemon.TransitionState(instance, tgt)
		}(i, target)
	}
	wg.Wait()

	// At least one transition must succeed.
	successes := 0
	for _, err := range results {
		if err == nil {
			successes++
		}
	}
	assert.GreaterOrEqual(t, successes, 1, "at least one concurrent transition should succeed")

	// The final state must be reachable from running via valid transitions.
	finalState := instance.Status
	reachable := reachableStates(vm.StateRunning)
	assert.True(t, reachable[finalState],
		"instance ended in state %q which is not reachable from running", finalState)
}

// reachableStates returns all states reachable from start via BFS.
func reachableStates(start vm.InstanceState) map[vm.InstanceState]bool {
	visited := map[vm.InstanceState]bool{start: true}
	queue := []vm.InstanceState{start}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range vm.ValidTransitions[current] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return visited
}

// TestTransitionState_DoubleTransitionSameTarget verifies that transitioning
// to the same state twice is rejected (no self-loops).
func TestTransitionState_DoubleTransitionSameTarget(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	for _, state := range allStates {
		t.Run(string(state), func(t *testing.T) {
			instance := &vm.VM{ID: fmt.Sprintf("i-double-%s", state), Status: state}
			daemon.Instances.Mu.Lock()
			daemon.Instances.VMS[instance.ID] = instance
			daemon.Instances.Mu.Unlock()

			err := daemon.TransitionState(instance, state)
			assert.Error(t, err, "self-transition %s -> %s should be rejected", state, state)
			assert.Equal(t, state, instance.Status, "state should be unchanged after rejected self-transition")
		})
	}
}

// TestTransitionState_InvalidFromTerminated exhaustively checks that
// terminated cannot transition to any state.
func TestTransitionState_InvalidFromTerminated(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	for _, target := range allStates {
		t.Run(fmt.Sprintf("terminated->%s", target), func(t *testing.T) {
			instance := &vm.VM{ID: fmt.Sprintf("i-term-%s", target), Status: vm.StateTerminated}
			daemon.Instances.Mu.Lock()
			daemon.Instances.VMS[instance.ID] = instance
			daemon.Instances.Mu.Unlock()

			err := daemon.TransitionState(instance, target)
			assert.Error(t, err, "transition from terminated to %s should be rejected", target)
			assert.Equal(t, vm.StateTerminated, instance.Status)
		})
	}
}

// TestTransitionState_StoppingMustReachStoppedOrError is the specific
// regression test for the bug where stopping never reached stopped.
// It verifies both the happy path and error path from stopping.
func TestTransitionState_StoppingMustReachStoppedOrError(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	t.Run("happy_path", func(t *testing.T) {
		instance := &vm.VM{ID: "i-stop-happy", Status: vm.StateStopping}
		daemon.Instances.Mu.Lock()
		daemon.Instances.VMS[instance.ID] = instance
		daemon.Instances.Mu.Unlock()

		err := daemon.TransitionState(instance, vm.StateStopped)
		require.NoError(t, err)
		assert.Equal(t, vm.StateStopped, instance.Status)
	})

	t.Run("error_path", func(t *testing.T) {
		instance := &vm.VM{ID: "i-stop-error", Status: vm.StateStopping}
		daemon.Instances.Mu.Lock()
		daemon.Instances.VMS[instance.ID] = instance
		daemon.Instances.Mu.Unlock()

		err := daemon.TransitionState(instance, vm.StateError)
		require.NoError(t, err)
		assert.Equal(t, vm.StateError, instance.Status)
	})

	t.Run("force_terminate_path", func(t *testing.T) {
		instance := &vm.VM{ID: "i-stop-term", Status: vm.StateStopping}
		daemon.Instances.Mu.Lock()
		daemon.Instances.VMS[instance.ID] = instance
		daemon.Instances.Mu.Unlock()

		err := daemon.TransitionState(instance, vm.StateShuttingDown)
		require.NoError(t, err)
		assert.Equal(t, vm.StateShuttingDown, instance.Status)

		err = daemon.TransitionState(instance, vm.StateTerminated)
		require.NoError(t, err)
		assert.Equal(t, vm.StateTerminated, instance.Status)
	})
}

// TestTransitionState_ShuttingDownMustReachTerminatedOrError is the parallel
// regression test for the terminate flow.
func TestTransitionState_ShuttingDownMustReachTerminatedOrError(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	t.Run("happy_path", func(t *testing.T) {
		instance := &vm.VM{ID: "i-shut-happy", Status: vm.StateShuttingDown}
		daemon.Instances.Mu.Lock()
		daemon.Instances.VMS[instance.ID] = instance
		daemon.Instances.Mu.Unlock()

		err := daemon.TransitionState(instance, vm.StateTerminated)
		require.NoError(t, err)
		assert.Equal(t, vm.StateTerminated, instance.Status)
	})

	t.Run("error_path", func(t *testing.T) {
		instance := &vm.VM{ID: "i-shut-error", Status: vm.StateShuttingDown}
		daemon.Instances.Mu.Lock()
		daemon.Instances.VMS[instance.ID] = instance
		daemon.Instances.Mu.Unlock()

		err := daemon.TransitionState(instance, vm.StateError)
		require.NoError(t, err)
		assert.Equal(t, vm.StateError, instance.Status)
	})
}

// TestValidTransitions_NoCyclesWithoutStableState ensures there is no cycle
// of only transitional states. Every cycle in the state graph must pass
// through at least one stable state.
func TestValidTransitions_NoCyclesWithoutStableState(t *testing.T) {
	stableSet := map[vm.InstanceState]bool{}
	for _, s := range stableStates {
		stableSet[s] = true
	}

	// DFS from each transitional state — if we revisit a transitional state
	// without going through a stable state, that's a problem.
	for _, start := range transitionalStates {
		visited := map[vm.InstanceState]bool{}
		var hasCycle bool

		var dfs func(vm.InstanceState)
		dfs = func(current vm.InstanceState) {
			if stableSet[current] {
				return // reached a stable state, stop this path
			}
			if visited[current] {
				hasCycle = true
				return
			}
			visited[current] = true
			for _, next := range vm.ValidTransitions[current] {
				dfs(next)
			}
		}
		dfs(start)

		assert.False(t, hasCycle,
			"found a cycle of only transitional states starting from %q", start)
	}
}

// TestTransitionState_MultipleInstancesIndependent verifies that transitioning
// one instance does not affect another instance's state.
func TestTransitionState_MultipleInstancesIndependent(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	inst1 := &vm.VM{ID: "i-multi-1", Status: vm.StateRunning}
	inst2 := &vm.VM{ID: "i-multi-2", Status: vm.StateRunning}
	daemon.Instances.Mu.Lock()
	daemon.Instances.VMS[inst1.ID] = inst1
	daemon.Instances.VMS[inst2.ID] = inst2
	daemon.Instances.Mu.Unlock()

	err := daemon.TransitionState(inst1, vm.StateStopping)
	require.NoError(t, err)

	assert.Equal(t, vm.StateStopping, inst1.Status)
	assert.Equal(t, vm.StateRunning, inst2.Status, "inst2 should be unaffected by inst1's transition")
}

// --- Recovery / restoreInstances tests ---
//
// These tests simulate a daemon restart by writing instance state to JetStream,
// then calling restoreInstances on a fresh daemon. Since no QEMU process is
// running, isInstanceProcessRunning returns false for all instances, which
// exercises the recovery state resolution logic.

// TestRestoreInstances_StoppingFinalizedToStopped verifies that an instance
// stuck in StateStopping when the daemon died gets finalized to StateStopped
// and migrated to shared KV.
func TestRestoreInstances_StoppingFinalizedToStopped(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	// Simulate: daemon was running, instance was in StateStopping, daemon crashed.
	daemon.Instances.VMS["i-restore-stop"] = &vm.VM{
		ID:     "i-restore-stop",
		Status: vm.StateStopping,
	}
	require.NoError(t, daemon.WriteState())

	// Clear in-memory state to simulate fresh daemon startup.
	daemon.Instances.VMS = make(map[string]*vm.VM)

	// restoreInstances loads from JetStream and resolves transitional states.
	daemon.restoreInstances()

	// Stopped instances are migrated to shared KV and removed from local map
	_, ok := daemon.Instances.VMS["i-restore-stop"]
	assert.False(t, ok, "stopping→stopped instance should be migrated to shared KV, not in local map")

	stoppedInst, err := daemon.jsManager.LoadStoppedInstance("i-restore-stop")
	require.NoError(t, err)
	require.NotNil(t, stoppedInst, "stopping→stopped instance should exist in shared KV")
	assert.Equal(t, vm.StateStopped, stoppedInst.Status,
		"stopping instance should be finalized to stopped on recovery")
}

// TestRestoreInstances_ShuttingDownFinalizedToTerminated verifies that an
// instance stuck in StateShuttingDown gets finalized to StateTerminated.
func TestRestoreInstances_ShuttingDownFinalizedToTerminated(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	daemon.Instances.VMS["i-restore-shut"] = &vm.VM{
		ID:     "i-restore-shut",
		Status: vm.StateShuttingDown,
	}
	require.NoError(t, daemon.WriteState())

	daemon.Instances.VMS = make(map[string]*vm.VM)
	daemon.restoreInstances()

	instance, ok := daemon.Instances.VMS["i-restore-shut"]
	require.True(t, ok, "instance should be loaded from state")
	assert.Equal(t, vm.StateTerminated, instance.Status,
		"shutting-down instance should be finalized to terminated on recovery")
}

// TestRestoreInstances_TerminatedSkipped verifies that terminated instances
// are loaded but not relaunched or modified.
func TestRestoreInstances_TerminatedSkipped(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	daemon.Instances.VMS["i-restore-term"] = &vm.VM{
		ID:     "i-restore-term",
		Status: vm.StateTerminated,
	}
	require.NoError(t, daemon.WriteState())

	daemon.Instances.VMS = make(map[string]*vm.VM)
	daemon.restoreInstances()

	instance, ok := daemon.Instances.VMS["i-restore-term"]
	require.True(t, ok, "terminated instance should still be loaded")
	assert.Equal(t, vm.StateTerminated, instance.Status,
		"terminated instance should remain terminated")
}

// TestRestoreInstances_UserStoppedMigratedToSharedKV verifies that instances
// flagged as user-stopped are migrated to shared KV and not relaunched.
func TestRestoreInstances_UserStoppedMigratedToSharedKV(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	daemon.Instances.VMS["i-restore-userstop"] = &vm.VM{
		ID:     "i-restore-userstop",
		Status: vm.StateStopped,
		Attributes: qmp.Attributes{
			StopInstance: true,
		},
	}
	require.NoError(t, daemon.WriteState())

	daemon.Instances.VMS = make(map[string]*vm.VM)
	daemon.restoreInstances()

	// Stopped instances should be migrated to shared KV
	_, ok := daemon.Instances.VMS["i-restore-userstop"]
	assert.False(t, ok, "user-stopped instance should be migrated to shared KV, not in local map")

	stoppedInst, err := daemon.jsManager.LoadStoppedInstance("i-restore-userstop")
	require.NoError(t, err)
	require.NotNil(t, stoppedInst, "user-stopped instance should exist in shared KV")
	assert.Equal(t, vm.StateStopped, stoppedInst.Status,
		"user-stopped instance should remain stopped in shared KV")
}

// TestRestoreInstances_RunningResetToPending verifies that a running instance
// whose QEMU process is dead gets reset to pending for relaunch.
// LaunchInstance will fail (no QEMU available in tests), but the state
// should be set to pending before the attempt.
func TestRestoreInstances_RunningResetToPending(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	daemon.Instances.VMS["i-restore-run"] = &vm.VM{
		ID:     "i-restore-run",
		Status: vm.StateRunning,
	}
	require.NoError(t, daemon.WriteState())

	daemon.Instances.VMS = make(map[string]*vm.VM)
	daemon.restoreInstances()

	instance, ok := daemon.Instances.VMS["i-restore-run"]
	require.True(t, ok, "running instance should still be loaded")
	// LaunchInstance fails (no QEMU), so it stays pending or gets marked failed.
	// Either way it should NOT remain "running" — that would be a lie.
	assert.NotEqual(t, vm.StateRunning, instance.Status,
		"instance should not remain running when QEMU process is dead")
}

// TestRestoreInstances_MixedStates verifies recovery with multiple instances
// in different states, ensuring each is handled correctly and independently.
// Stopped instances (including stopping→stopped transitions) are migrated to shared KV.
func TestRestoreInstances_MixedStates(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	daemon.Instances.VMS["i-mix-stopping"] = &vm.VM{
		ID: "i-mix-stopping", Status: vm.StateStopping,
	}
	daemon.Instances.VMS["i-mix-shutting"] = &vm.VM{
		ID: "i-mix-shutting", Status: vm.StateShuttingDown,
	}
	daemon.Instances.VMS["i-mix-terminated"] = &vm.VM{
		ID: "i-mix-terminated", Status: vm.StateTerminated,
	}
	daemon.Instances.VMS["i-mix-stopped"] = &vm.VM{
		ID: "i-mix-stopped", Status: vm.StateStopped,
		Attributes: qmp.Attributes{StopInstance: true},
	}
	require.NoError(t, daemon.WriteState())

	daemon.Instances.VMS = make(map[string]*vm.VM)
	daemon.restoreInstances()

	// Stopped instances are migrated to shared KV and removed from local map
	assert.Nil(t, daemon.Instances.VMS["i-mix-stopping"],
		"stopping→stopped should be migrated to shared KV")
	stoppedFromStopping, err := daemon.jsManager.LoadStoppedInstance("i-mix-stopping")
	require.NoError(t, err)
	require.NotNil(t, stoppedFromStopping, "stopping→stopped should exist in shared KV")
	assert.Equal(t, vm.StateStopped, stoppedFromStopping.Status)

	assert.Equal(t, vm.StateTerminated, daemon.Instances.VMS["i-mix-shutting"].Status,
		"shutting-down should finalize to terminated")
	assert.Equal(t, vm.StateTerminated, daemon.Instances.VMS["i-mix-terminated"].Status,
		"terminated should remain terminated")

	assert.Nil(t, daemon.Instances.VMS["i-mix-stopped"],
		"user-stopped should be migrated to shared KV")
	stoppedFromUser, err := daemon.jsManager.LoadStoppedInstance("i-mix-stopped")
	require.NoError(t, err)
	require.NotNil(t, stoppedFromUser, "user-stopped should exist in shared KV")
	assert.Equal(t, vm.StateStopped, stoppedFromUser.Status)
}

// TestRestoreInstances_StatePersistsAfterRecovery verifies that the finalized
// state is actually persisted to JetStream, so a second restart doesn't
// re-process the same transitional state.
func TestRestoreInstances_StatePersistsAfterRecovery(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	daemon.Instances.VMS["i-persist-stop"] = &vm.VM{
		ID: "i-persist-stop", Status: vm.StateStopping,
	}
	daemon.Instances.VMS["i-persist-shut"] = &vm.VM{
		ID: "i-persist-shut", Status: vm.StateShuttingDown,
	}
	require.NoError(t, daemon.WriteState())

	// First restart: finalize transitional states.
	daemon.Instances.VMS = make(map[string]*vm.VM)
	daemon.restoreInstances()

	// stopping→stopped migrates to shared KV
	assert.Nil(t, daemon.Instances.VMS["i-persist-stop"],
		"stopping→stopped should be migrated to shared KV")
	stoppedInst, err := daemon.jsManager.LoadStoppedInstance("i-persist-stop")
	require.NoError(t, err)
	require.NotNil(t, stoppedInst)
	assert.Equal(t, vm.StateStopped, stoppedInst.Status)

	assert.Equal(t, vm.StateTerminated, daemon.Instances.VMS["i-persist-shut"].Status)

	// Second restart: stopped instance is in shared KV (not per-node state),
	// so local map won't have it. Terminated instance persists in per-node state.
	daemon.Instances.VMS = make(map[string]*vm.VM)
	daemon.restoreInstances()

	// Stopped instance should still be in shared KV
	stoppedInst2, err := daemon.jsManager.LoadStoppedInstance("i-persist-stop")
	require.NoError(t, err)
	require.NotNil(t, stoppedInst2, "stopped instance should persist in shared KV through second restart")
	assert.Equal(t, vm.StateStopped, stoppedInst2.Status)

	assert.Equal(t, vm.StateTerminated, daemon.Instances.VMS["i-persist-shut"].Status,
		"terminated state should persist through second restart")
}

// TestStatePersistence_RoundTrip verifies that all instance fields survive
// a write-load cycle through JetStream, simulating a daemon restart.
func TestStatePersistence_RoundTrip(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	original := &vm.VM{
		ID:           "i-roundtrip",
		Status:       vm.StateRunning,
		InstanceType: "t3.micro",
		Attributes: qmp.Attributes{
			StopInstance:      false,
			TerminateInstance: false,
		},
	}
	daemon.Instances.VMS[original.ID] = original
	require.NoError(t, daemon.WriteState())

	// Simulate restart: clear and reload.
	daemon.Instances.VMS = make(map[string]*vm.VM)
	require.NoError(t, daemon.LoadState())

	loaded := daemon.Instances.VMS["i-roundtrip"]
	require.NotNil(t, loaded)
	assert.Equal(t, original.ID, loaded.ID)
	assert.Equal(t, original.Status, loaded.Status)
	assert.Equal(t, original.InstanceType, loaded.InstanceType)
	assert.Equal(t, original.Attributes.StopInstance, loaded.Attributes.StopInstance)
}

// TestRestoreInstances_StoppedInstanceMigratedToSharedKV verifies that after
// a daemon restart, a stopped instance is migrated to shared KV and can be
// retrieved by the ec2.start handler.
func TestRestoreInstances_StoppedInstanceMigratedToSharedKV(t *testing.T) {
	daemon := createDaemonWithJetStream(t)

	instanceID := "i-restore-start"
	daemon.Instances.VMS[instanceID] = &vm.VM{
		ID:           instanceID,
		InstanceType: "t3.micro",
		Status:       vm.StateStopped,
		Attributes:   qmp.Attributes{StopInstance: true},
	}
	require.NoError(t, daemon.WriteState())

	// Simulate daemon restart: clear in-memory state and restore from JetStream.
	daemon.Instances.VMS = make(map[string]*vm.VM)
	daemon.restoreInstances()

	// Instance should not be in local map (migrated to shared KV).
	_, ok := daemon.Instances.VMS[instanceID]
	assert.False(t, ok, "stopped instance should be migrated to shared KV, not in local map")

	// Instance should be in shared KV
	stoppedInst, err := daemon.jsManager.LoadStoppedInstance(instanceID)
	require.NoError(t, err)
	require.NotNil(t, stoppedInst, "stopped instance should exist in shared KV")
	assert.Equal(t, vm.StateStopped, stoppedInst.Status)
	assert.Equal(t, "node-1", stoppedInst.LastNode,
		"stopped instance should record last node")
}
