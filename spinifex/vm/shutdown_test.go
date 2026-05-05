package vm

import (
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go/service/ec2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shutdownTestManager wires the dependencies needed to exercise Stop /
// StopAll / Terminate / MarkFailed without standing up the daemon. The
// returned cleaner records every method invocation so tests can assert
// what cleanup ran (and what didn't).
func shutdownTestManager(t *testing.T) (m *Manager, store *fakeStateStore, mounter *fakeVolumeMounter, cleaner *recordingInstanceCleaner, rt *recordedTransitions) {
	t.Helper()
	store = newFakeStateStore()
	mounter = &fakeVolumeMounter{}
	cleaner = &recordingInstanceCleaner{}
	rt = &recordedTransitions{}
	m = NewManager()
	rt.bind(m)
	m.SetDeps(Deps{
		NodeID:          "test-node",
		StateStore:      store,
		VolumeMounter:   mounter,
		InstanceCleaner: cleaner,
		TransitionState: rt.apply,
		ShutdownSignal:  func() bool { return false },
	})
	return m, store, mounter, cleaner, rt
}

// TestMarkFailed_TransitionsToTerminated verifies the synchronous +
// asynchronous parts of MarkFailed: the synchronous transition to
// ShuttingDown plus the StateReason mutation, and the goroutine driving
// the instance through terminateCleanup → Terminated.
func TestMarkFailed_TransitionsToTerminated(t *testing.T) {
	m, _, _, _, rt := shutdownTestManager(t)

	instance := &VM{
		ID:        "i-mark-failed",
		Status:    StatePending,
		AccountID: "111122223333",
		Instance:  &ec2.Instance{},
	}
	m.Insert(instance)

	m.MarkFailed(instance, "volume_preparation_failed")

	require.NotNil(t, instance.Instance.StateReason,
		"MarkFailed must populate StateReason synchronously")
	assert.Equal(t, "Server.InternalError", *instance.Instance.StateReason.Code)
	assert.Equal(t, "volume_preparation_failed", *instance.Instance.StateReason.Message)

	require.Eventually(t, func() bool {
		return m.Status(instance) == StateTerminated
	}, 2*time.Second, 5*time.Millisecond, "cleanup goroutine must reach Terminated")

	targets := rt.targets("i-mark-failed")
	require.NotEmpty(t, targets)
	assert.Equal(t, StateShuttingDown, targets[0],
		"first transition must be ShuttingDown")
	assert.Contains(t, targets, StateTerminated,
		"terminal transition must reach Terminated")
}

// TestMarkFailed_NilInstance verifies MarkFailed tolerates a VM with no
// embedded ec2.Instance (Instance == nil) without panicking.
func TestMarkFailed_NilInstance(t *testing.T) {
	m, _, _, _, _ := shutdownTestManager(t)

	instance := &VM{
		ID:        "i-mark-failed-nil",
		Status:    StatePending,
		AccountID: "111122223333",
		Instance:  nil,
	}
	m.Insert(instance)

	require.NotPanics(t, func() {
		m.MarkFailed(instance, "test_failure")
	})

	require.Eventually(t, func() bool {
		return m.Status(instance) == StateTerminated
	}, 2*time.Second, 5*time.Millisecond, "cleanup goroutine must reach Terminated")
}

// TestMarkFailed_AlreadyShuttingDown_NoOp verifies MarkFailed skips its
// work when the instance is already past pending — the existing cleanup
// goroutine owns the transition.
func TestMarkFailed_AlreadyShuttingDown_NoOp(t *testing.T) {
	m, _, _, cleaner, rt := shutdownTestManager(t)

	instance := &VM{
		ID:       "i-already-down",
		Status:   StateShuttingDown,
		Instance: &ec2.Instance{},
	}
	m.Insert(instance)

	m.MarkFailed(instance, "duplicate_call")

	assert.Empty(t, rt.snapshot(),
		"MarkFailed must not transition an already-shutting-down instance")
	assert.Nil(t, instance.Instance.StateReason,
		"MarkFailed must not overwrite StateReason on already-shutting-down instance")
	assert.Zero(t, cleaner.deleteVolumesCount(),
		"MarkFailed must not run cleanup on already-shutting-down instance")
}

func TestMarkFailed_AlreadyTerminated_NoOp(t *testing.T) {
	m, _, _, _, rt := shutdownTestManager(t)

	instance := &VM{
		ID:       "i-terminated",
		Status:   StateTerminated,
		Instance: &ec2.Instance{},
	}
	m.Insert(instance)

	m.MarkFailed(instance, "duplicate_call")

	assert.Empty(t, rt.snapshot(),
		"MarkFailed must not transition an already-terminated instance")
}

// TestStop_DoesNotCallDeleteVolumes locks down the architectural
// invariant that Stop must never delete volumes — a regression here
// would silently destroy user data on every stop. Phase 2c removed
// TestStopInstance_NoDelete_OnStop from daemon_test.go without a
// vm-package replacement; this is that replacement.
func TestStop_DoesNotCallDeleteVolumes(t *testing.T) {
	m, _, _, cleaner, _ := shutdownTestManager(t)

	instance := &VM{
		ID:           "i-stop-nodelete",
		Status:       StateRunning,
		InstanceType: "t3.micro",
		Instance:     &ec2.Instance{},
	}
	m.Insert(instance)

	require.NoError(t, m.Stop(instance.ID))

	assert.Zero(t, cleaner.deleteVolumesCount(),
		"Manager.Stop must never invoke InstanceCleaner.DeleteVolumes")
}

// TestStopAll_DoesNotCallDeleteVolumes mirrors the Stop invariant for the
// fan-out path used by coordinated shutdown / SIGTERM.
func TestStopAll_DoesNotCallDeleteVolumes(t *testing.T) {
	m, _, _, cleaner, _ := shutdownTestManager(t)

	for i := range 3 {
		m.Insert(&VM{
			ID:           string(rune('a' + i)),
			Status:       StateRunning,
			InstanceType: "t3.micro",
			Instance:     &ec2.Instance{},
		})
	}

	require.NoError(t, m.StopAll())

	assert.Zero(t, cleaner.deleteVolumesCount(),
		"Manager.StopAll must never invoke InstanceCleaner.DeleteVolumes")
}

func TestStopAll_EmptyMap_FastPath(t *testing.T) {
	m, _, mounter, cleaner, rt := shutdownTestManager(t)

	require.NoError(t, m.StopAll())

	assert.Empty(t, rt.snapshot(), "empty StopAll must not invoke transitions")
	assert.Empty(t, mounter.unmounted, "empty StopAll must not invoke unmount")
	assert.Empty(t, cleaner.cleanupMgmt, "empty StopAll must not invoke cleaner")
}

// TestStopAll_DoesNotFireOnInstanceDown verifies StopAll's fan-out
// shutdown path — used by coordinated DRAIN and SIGTERM — leaves the
// hook contract alone. Per the plan's hook contract, Stop fires
// OnInstanceDown but StopAll does not (it leaves instances in the
// running map for restoreInstances to pick up on next boot).
func TestStopAll_DoesNotFireOnInstanceDown(t *testing.T) {
	m, _, _, _, _ := shutdownTestManager(t)
	var down atomic.Int64
	rt := (&recordedTransitions{}).bind(m)
	m.SetDeps(Deps{
		NodeID:          "test-node",
		TransitionState: rt.apply,
		ShutdownSignal:  func() bool { return false },
		Hooks: ManagerHooks{
			OnInstanceDown: func(string) { down.Add(1) },
		},
	})

	for _, id := range []string{"i-1", "i-2", "i-3"} {
		m.Insert(&VM{
			ID:           id,
			Status:       StateRunning,
			InstanceType: "t3.micro",
			Instance:     &ec2.Instance{},
		})
	}

	require.NoError(t, m.StopAll())

	assert.Zero(t, down.Load(),
		"StopAll must not fire OnInstanceDown — instances stay in the running map")
	assert.Equal(t, 3, m.Count(),
		"StopAll must leave instances in the local map for restoreInstances")
}

// TestMigrateStoppedToSharedKV_SlotReclaim covers the DeleteIf-mismatch
// branch: another handler reclaimed the slot under the same id while the
// shared write was in flight, so the local delete must not happen and
// the function must report false.
func TestMigrateStoppedToSharedKV_SlotReclaim(t *testing.T) {
	m, store, _, _, _ := shutdownTestManager(t)

	original := &VM{ID: "i-reclaim", Status: StateStopped}
	reclaimed := &VM{ID: "i-reclaim", Status: StateRunning}
	m.Insert(original)
	// Replace the slot under the same id — DeleteIf(original) must miss.
	m.Insert(reclaimed)

	got := m.MigrateStoppedToSharedKV(original)
	require.False(t, got, "MigrateStoppedToSharedKV must return false when the slot was reclaimed")

	// The shared KV write still happened (writeFn fires before DeleteIf).
	stored, _ := store.LoadStoppedInstance("i-reclaim")
	assert.NotNil(t, stored, "shared KV write must precede the slot check")

	// The reclaimed instance must remain in the local map.
	v, ok := m.Get("i-reclaim")
	require.True(t, ok, "reclaimed slot must remain in the local map")
	assert.Same(t, reclaimed, v, "Get must return the reclaimed pointer, not the original")
}

func TestMigrateStoppedToSharedKV_KVWriteFailure(t *testing.T) {
	mounter := &fakeVolumeMounter{}
	cleaner := &recordingInstanceCleaner{}
	store := failOnWriteStoppedStore{newFakeStateStore(), errors.New("kv unreachable")}
	m := NewManagerWithDeps(Deps{
		NodeID:          "test-node",
		StateStore:      store,
		VolumeMounter:   mounter,
		InstanceCleaner: cleaner,
		ShutdownSignal:  func() bool { return false },
	})

	v := &VM{ID: "i-kv-fail", Status: StateStopped}
	m.Insert(v)

	got := m.MigrateStoppedToSharedKV(v)
	assert.False(t, got, "MigrateStoppedToSharedKV must return false on KV write failure")

	// Local map entry must remain so restoreInstances can retry on boot.
	_, ok := m.Get("i-kv-fail")
	assert.True(t, ok, "KV write failure must leave the instance in the local map")
}

// TestMigrateStoppedToSharedKV_NoStateStore covers the no-deps fallback
// where StateStore is nil — used by Manager built via NewManager() with
// no Deps wired (e.g. early-boot daemon construction).
func TestMigrateStoppedToSharedKV_NoStateStore(t *testing.T) {
	m := NewManager()
	v := &VM{ID: "i-no-store", Status: StateStopped}
	m.Insert(v)

	got := m.MigrateStoppedToSharedKV(v)
	assert.False(t, got, "missing StateStore must report false (no migration possible)")
	_, ok := m.Get("i-no-store")
	assert.True(t, ok, "missing StateStore must leave local map untouched")
}

// failOnWriteStoppedStore wraps fakeStateStore and forces the
// WriteStoppedInstance path to error so the slot-reclaim contract can be
// verified in isolation from the happy path.
type failOnWriteStoppedStore struct {
	*fakeStateStore

	err error
}

func (f failOnWriteStoppedStore) WriteStoppedInstance(string, *VM) error {
	return f.err
}

// TestStop_FiresOnInstanceDownExactlyOnce verifies the success-path hook
// contract: Stop fires OnInstanceDown once after a successful transition
// to Stopped + shared-KV migration.
func TestStop_FiresOnInstanceDownExactlyOnce(t *testing.T) {
	m, _, _, _, _ := shutdownTestManager(t)
	var down atomic.Int64
	var downIDs []string
	var mu sync.Mutex
	rt := (&recordedTransitions{}).bind(m)
	m.SetDeps(Deps{
		NodeID:          "test-node",
		StateStore:      newFakeStateStore(),
		VolumeMounter:   &fakeVolumeMounter{},
		InstanceCleaner: &recordingInstanceCleaner{},
		TransitionState: rt.apply,
		ShutdownSignal:  func() bool { return false },
		Hooks: ManagerHooks{
			OnInstanceDown: func(id string) {
				down.Add(1)
				mu.Lock()
				downIDs = append(downIDs, id)
				mu.Unlock()
			},
		},
	})

	v := &VM{
		ID:           "i-stop-hook",
		Status:       StateRunning,
		InstanceType: "t3.micro",
		Instance:     &ec2.Instance{},
	}
	m.Insert(v)

	require.NoError(t, m.Stop(v.ID))

	assert.Equal(t, int64(1), down.Load(), "Stop must fire OnInstanceDown exactly once")
	mu.Lock()
	defer mu.Unlock()
	assert.Equal(t, []string{"i-stop-hook"}, downIDs)
}

// TestStop_DoesNotFireOnInstanceDown_OnSlotReclaim verifies the slot-
// reclaim branch in Stop: when MigrateStoppedToSharedKV returns false
// because a concurrent handler took the slot, OnInstanceDown must not
// fire (firing it would tear down the new instance's NATS subs).
func TestStop_DoesNotFireOnInstanceDown_OnSlotReclaim(t *testing.T) {
	var down atomic.Int64
	store := &reclaimingStateStore{fakeStateStore: newFakeStateStore()}
	m := NewManager()
	rt := (&recordedTransitions{}).bind(m)
	m.SetDeps(Deps{
		NodeID:          "test-node",
		StateStore:      store,
		VolumeMounter:   &fakeVolumeMounter{},
		InstanceCleaner: &recordingInstanceCleaner{},
		TransitionState: rt.apply,
		ShutdownSignal:  func() bool { return false },
		Hooks: ManagerHooks{
			OnInstanceDown: func(string) { down.Add(1) },
		},
	})
	store.m = m

	v := &VM{
		ID:           "i-reclaim-no-hook",
		Status:       StateRunning,
		InstanceType: "t3.micro",
		Instance:     &ec2.Instance{},
	}
	m.Insert(v)

	require.NoError(t, m.Stop(v.ID))

	assert.Zero(t, down.Load(),
		"slot-reclaim branch must not fire OnInstanceDown")
}

// reclaimingStateStore reclaims the slot under the same id during the
// shared-KV write callback, so the subsequent DeleteIf in
// migrateInstanceToKV finds a different pointer and returns false.
type reclaimingStateStore struct {
	*fakeStateStore

	m *Manager
}

func (r *reclaimingStateStore) WriteStoppedInstance(id string, v *VM) error {
	if err := r.fakeStateStore.WriteStoppedInstance(id, v); err != nil {
		return err
	}
	// Mid-flight slot reclaim: a concurrent start handler installs a new
	// pointer under the same id between the write and DeleteIf.
	r.m.Insert(&VM{ID: id, Status: StateRunning})
	return nil
}

func TestStopCleanup_InvokesReleaseGPU(t *testing.T) {
	cleaner := &recordingInstanceCleaner{}
	m := NewManagerWithDeps(Deps{InstanceCleaner: cleaner})
	instance := &VM{ID: "i-stop", GPUPCIAddress: "0000:01:00.0"}

	m.stopCleanup(instance)

	if got := cleaner.releaseGPU; len(got) != 1 || got[0] != "i-stop" {
		t.Fatalf("ReleaseGPU on stopCleanup: got %v, want [i-stop]", got)
	}
	if len(cleaner.deleteVolumes) != 0 || len(cleaner.releasePublicIP) != 0 || len(cleaner.detachAndDeleteENI) != 0 || len(cleaner.removeFromPlacement) != 0 {
		t.Fatalf("stopCleanup leaked terminate-only calls: delete=%v pubip=%v eni=%v pg=%v",
			cleaner.deleteVolumes, cleaner.releasePublicIP, cleaner.detachAndDeleteENI, cleaner.removeFromPlacement)
	}
}

func TestTerminateCleanup_InvokesReleaseGPU(t *testing.T) {
	cleaner := &recordingInstanceCleaner{}
	m := NewManagerWithDeps(Deps{InstanceCleaner: cleaner})
	instance := &VM{ID: "i-term", GPUPCIAddress: "0000:01:00.0"}

	m.terminateCleanup(instance)

	if got := cleaner.releaseGPU; len(got) != 1 || got[0] != "i-term" {
		t.Fatalf("ReleaseGPU on terminateCleanup: got %v, want [i-term]", got)
	}
	if got := cleaner.deleteVolumes; len(got) != 1 || got[0] != "i-term" {
		t.Fatalf("DeleteVolumes on terminateCleanup: got %v, want [i-term]", got)
	}
}

func TestCleanup_NoGPU_StillInvokesReleaseGPU(t *testing.T) {
	// The adapter no-ops for GPU-less instances; the manager must still
	// invoke the method so the adapter owns that decision rather than the
	// manager second-guessing it.
	cleaner := &recordingInstanceCleaner{}
	m := NewManagerWithDeps(Deps{InstanceCleaner: cleaner})
	instance := &VM{ID: "i-cpu"}

	m.stopCleanup(instance)
	m.terminateCleanup(instance)

	if got := len(cleaner.releaseGPU); got != 2 {
		t.Fatalf("ReleaseGPU calls across stop+terminate: got %d, want 2", got)
	}
}
