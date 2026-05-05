package vm

import (
	"errors"
	"maps"
	"sync"
	"testing"

	"github.com/mulgadc/spinifex/spinifex/types"
)

// fakeStateStore is a minimal in-memory StateStore used to verify Deps wiring.
type fakeStateStore struct {
	saved      map[string]map[string]*VM
	stopped    map[string]*VM
	terminated map[string]*VM
}

func newFakeStateStore() *fakeStateStore {
	return &fakeStateStore{
		saved:      map[string]map[string]*VM{},
		stopped:    map[string]*VM{},
		terminated: map[string]*VM{},
	}
}

func (f *fakeStateStore) SaveRunningState(nodeID string, snap map[string]*VM) error {
	cp := make(map[string]*VM, len(snap))
	maps.Copy(cp, snap)
	f.saved[nodeID] = cp
	return nil
}

func (f *fakeStateStore) LoadRunningState(nodeID string) (map[string]*VM, error) {
	if v, ok := f.saved[nodeID]; ok {
		return v, nil
	}
	return map[string]*VM{}, nil
}

func (f *fakeStateStore) WriteStoppedInstance(id string, v *VM) error {
	f.stopped[id] = v
	return nil
}

func (f *fakeStateStore) LoadStoppedInstance(id string) (*VM, error) {
	if v, ok := f.stopped[id]; ok {
		return v, nil
	}
	return nil, nil
}

func (f *fakeStateStore) DeleteStoppedInstance(id string) error {
	delete(f.stopped, id)
	return nil
}

func (f *fakeStateStore) ListStoppedInstances() ([]*VM, error) {
	out := make([]*VM, 0, len(f.stopped))
	for _, v := range f.stopped {
		out = append(out, v)
	}
	return out, nil
}

func (f *fakeStateStore) WriteTerminatedInstance(id string, v *VM) error {
	f.terminated[id] = v
	return nil
}

func (f *fakeStateStore) ListTerminatedInstances() ([]*VM, error) {
	out := make([]*VM, 0, len(f.terminated))
	for _, v := range f.terminated {
		out = append(out, v)
	}
	return out, nil
}

var _ StateStore = (*fakeStateStore)(nil)

func TestNewManagerWithDeps_StoresDeps(t *testing.T) {
	store := newFakeStateStore()
	deps := Deps{
		NodeID:     "node-a",
		StateStore: store,
		ShutdownSignal: func() bool {
			return true
		},
	}
	m := NewManagerWithDeps(deps)

	if m.NodeID() != "node-a" {
		t.Fatalf("NodeID: got %q, want %q", m.NodeID(), "node-a")
	}
	if m.deps.StateStore == nil {
		t.Fatal("deps.StateStore: got nil after construction")
	}
	if !m.deps.ShutdownSignal() {
		t.Fatal("ShutdownSignal callback: not preserved")
	}

	// Sanity: the manager's map is independent of any future Deps state.
	if m.Count() != 0 {
		t.Fatalf("Count on fresh manager: got %d, want 0", m.Count())
	}
}

func TestSetDeps_OverridesDeps(t *testing.T) {
	m := NewManager()
	if m.NodeID() != "" {
		t.Fatalf("NodeID on default manager: got %q, want empty", m.NodeID())
	}

	deps := Deps{NodeID: "node-b"}
	m.SetDeps(deps)
	if m.NodeID() != "node-b" {
		t.Fatalf("NodeID after SetDeps: got %q, want %q", m.NodeID(), "node-b")
	}

	// Replacing deps fully overwrites prior deps, not merges.
	m.SetDeps(Deps{NodeID: "node-c"})
	if m.NodeID() != "node-c" {
		t.Fatalf("NodeID after second SetDeps: got %q, want %q", m.NodeID(), "node-c")
	}
}

func TestManagerHooks_InitiallyNil(t *testing.T) {
	// A manager constructed without hooks must expose nil hook fields so
	// call sites can no-op rather than panicking.
	m := NewManager()
	if m.deps.Hooks.OnInstanceUp != nil {
		t.Fatal("OnInstanceUp: got non-nil on default manager")
	}
	if m.deps.Hooks.OnInstanceDown != nil {
		t.Fatal("OnInstanceDown: got non-nil on default manager")
	}
}

// fakeVolumeMounter records every call so lifecycle tests can assert ordering.
// The mutex covers the recording slices so StopAll's per-instance fan-out
// goroutines stay race-free.
type fakeVolumeMounter struct {
	mu                       sync.Mutex
	mounted, unmounted       []string
	mountedOne, unmountedOne []string
	mountErr                 error
	mountOneErr              error
	mountOneURI              string
}

func (f *fakeVolumeMounter) Mount(v *VM) error {
	f.mu.Lock()
	f.mounted = append(f.mounted, v.ID)
	err := f.mountErr
	f.mu.Unlock()
	return err
}

func (f *fakeVolumeMounter) Unmount(v *VM) error {
	f.mu.Lock()
	f.unmounted = append(f.unmounted, v.ID)
	f.mu.Unlock()
	return nil
}

func (f *fakeVolumeMounter) MountOne(req *types.EBSRequest) error {
	f.mu.Lock()
	f.mountedOne = append(f.mountedOne, req.Name)
	mountOneErr := f.mountOneErr
	uri := f.mountOneURI
	f.mu.Unlock()
	if mountOneErr != nil {
		return mountOneErr
	}
	if uri != "" {
		req.NBDURI = uri
	}
	return nil
}

func (f *fakeVolumeMounter) UnmountOne(req types.EBSRequest) {
	f.mu.Lock()
	f.unmountedOne = append(f.unmountedOne, req.Name)
	f.mu.Unlock()
}

var _ VolumeMounter = (*fakeVolumeMounter)(nil)

func TestDeps_VolumeMounterSatisfiesInterface(t *testing.T) {
	mounter := &fakeVolumeMounter{}
	deps := Deps{VolumeMounter: mounter}
	m := NewManagerWithDeps(deps)

	if err := m.deps.VolumeMounter.Mount(&VM{ID: "i-1"}); err != nil {
		t.Fatalf("Mount: %v", err)
	}
	if err := m.deps.VolumeMounter.Unmount(&VM{ID: "i-1"}); err != nil {
		t.Fatalf("Unmount: %v", err)
	}
	if len(mounter.mounted) != 1 || mounter.mounted[0] != "i-1" {
		t.Fatalf("mounted: got %v, want [i-1]", mounter.mounted)
	}
	if len(mounter.unmounted) != 1 || mounter.unmounted[0] != "i-1" {
		t.Fatalf("unmounted: got %v, want [i-1]", mounter.unmounted)
	}
}

func TestDeps_StateStoreSatisfiesInterface(t *testing.T) {
	store := newFakeStateStore()
	deps := Deps{NodeID: "n", StateStore: store}
	m := NewManagerWithDeps(deps)

	if err := m.deps.StateStore.WriteStoppedInstance("i-1", &VM{ID: "i-1"}); err != nil {
		t.Fatalf("WriteStoppedInstance: %v", err)
	}
	got, err := m.deps.StateStore.LoadStoppedInstance("i-1")
	if err != nil {
		t.Fatalf("LoadStoppedInstance: %v", err)
	}
	if got == nil || got.ID != "i-1" {
		t.Fatalf("LoadStoppedInstance: got %v, want VM{ID:i-1}", got)
	}

	if err := m.deps.StateStore.SaveRunningState("n", map[string]*VM{"i-1": {ID: "i-1"}}); err != nil {
		t.Fatalf("SaveRunningState: %v", err)
	}
	loaded, err := m.deps.StateStore.LoadRunningState("n")
	if err != nil {
		t.Fatalf("LoadRunningState: %v", err)
	}
	if len(loaded) != 1 {
		t.Fatalf("LoadRunningState: got %d entries, want 1", len(loaded))
	}
}

// failStateStore returns errors so lifecycle tests can verify propagation
// when the manager wires through StateStore.
type failStateStore struct{ *fakeStateStore }

func (failStateStore) SaveRunningState(string, map[string]*VM) error {
	return errors.New("save failed")
}

func TestDeps_StateStoreErrorPropagates(t *testing.T) {
	deps := Deps{NodeID: "n", StateStore: failStateStore{newFakeStateStore()}}
	m := NewManagerWithDeps(deps)

	err := m.deps.StateStore.SaveRunningState("n", nil)
	if err == nil {
		t.Fatal("SaveRunningState: expected error from failStateStore, got nil")
	}
}
