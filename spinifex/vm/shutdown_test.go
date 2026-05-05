package vm

import (
	"sync"
	"testing"
)

// recordingCleaner records every InstanceCleaner call. Tests assert against
// the per-method slices rather than booleans so duplicate invocations from a
// future regression are observable.
type recordingCleaner struct {
	mu       sync.Mutex
	delete   []string
	mgmt     []string
	pubip    []string
	eni      []string
	pg       []string
	releases []string
}

func (c *recordingCleaner) DeleteVolumes(v *VM) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.delete = append(c.delete, v.ID)
}

func (c *recordingCleaner) CleanupMgmtNetwork(v *VM) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.mgmt = append(c.mgmt, v.ID)
}

func (c *recordingCleaner) ReleasePublicIP(v *VM) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pubip = append(c.pubip, v.ID)
}

func (c *recordingCleaner) DetachAndDeleteENI(v *VM) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.eni = append(c.eni, v.ID)
}

func (c *recordingCleaner) RemoveFromPlacementGroup(v *VM) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.pg = append(c.pg, v.ID)
}

func (c *recordingCleaner) ReleaseGPU(v *VM) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.releases = append(c.releases, v.ID)
}

var _ InstanceCleaner = (*recordingCleaner)(nil)

func TestStopCleanup_InvokesReleaseGPU(t *testing.T) {
	cleaner := &recordingCleaner{}
	m := NewManagerWithDeps(Deps{InstanceCleaner: cleaner})
	instance := &VM{ID: "i-stop", GPUPCIAddress: "0000:01:00.0"}

	m.stopCleanup(instance)

	if got := cleaner.releases; len(got) != 1 || got[0] != "i-stop" {
		t.Fatalf("ReleaseGPU on stopCleanup: got %v, want [i-stop]", got)
	}
	// Stop must not touch the terminate-only resources.
	if len(cleaner.delete) != 0 || len(cleaner.pubip) != 0 || len(cleaner.eni) != 0 || len(cleaner.pg) != 0 {
		t.Fatalf("stopCleanup leaked terminate-only calls: delete=%v pubip=%v eni=%v pg=%v",
			cleaner.delete, cleaner.pubip, cleaner.eni, cleaner.pg)
	}
}

func TestTerminateCleanup_InvokesReleaseGPU(t *testing.T) {
	cleaner := &recordingCleaner{}
	m := NewManagerWithDeps(Deps{InstanceCleaner: cleaner})
	instance := &VM{ID: "i-term", GPUPCIAddress: "0000:01:00.0"}

	m.terminateCleanup(instance)

	if got := cleaner.releases; len(got) != 1 || got[0] != "i-term" {
		t.Fatalf("ReleaseGPU on terminateCleanup: got %v, want [i-term]", got)
	}
	if got := cleaner.delete; len(got) != 1 || got[0] != "i-term" {
		t.Fatalf("DeleteVolumes on terminateCleanup: got %v, want [i-term]", got)
	}
}

func TestCleanup_NoGPU_StillInvokesReleaseGPU(t *testing.T) {
	// The adapter no-ops for GPU-less instances; the manager must still
	// invoke the method so the adapter owns that decision rather than the
	// manager second-guessing it.
	cleaner := &recordingCleaner{}
	m := NewManagerWithDeps(Deps{InstanceCleaner: cleaner})
	instance := &VM{ID: "i-cpu"}

	m.stopCleanup(instance)
	m.terminateCleanup(instance)

	if got := len(cleaner.releases); got != 2 {
		t.Fatalf("ReleaseGPU calls across stop+terminate: got %d, want 2", got)
	}
}
