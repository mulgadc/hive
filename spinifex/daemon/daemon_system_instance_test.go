package daemon

import (
	"testing"
	"time"

	"github.com/mulgadc/spinifex/spinifex/vm"
)

func newDaemonWithVMs(vms ...*vm.VM) *Daemon {
	d := &Daemon{vmMgr: vm.NewManager()}
	for _, v := range vms {
		d.vmMgr.Insert(v)
	}
	return d
}

func TestWaitForSystemInstance_AlreadyRunning(t *testing.T) {
	d := newDaemonWithVMs(&vm.VM{ID: "i-test1", Status: vm.StateRunning})

	err := d.WaitForSystemInstance("i-test1", 1*time.Second)
	if err != nil {
		t.Fatalf("expected no error for running instance, got: %v", err)
	}
}

func TestWaitForSystemInstance_NotFound(t *testing.T) {
	d := newDaemonWithVMs()

	err := d.WaitForSystemInstance("i-nonexistent", 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
}

func TestWaitForSystemInstance_ErrorState(t *testing.T) {
	d := newDaemonWithVMs(&vm.VM{ID: "i-failed", Status: vm.StateError})

	err := d.WaitForSystemInstance("i-failed", 1*time.Second)
	if err == nil {
		t.Fatal("expected error for failed instance")
	}
}

func TestWaitForSystemInstance_TransitionsToRunning(t *testing.T) {
	inst := &vm.VM{ID: "i-pending", Status: vm.StateProvisioning}
	d := newDaemonWithVMs(inst)

	// Transition to running after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.vmMgr.Inspect(inst, func(v *vm.VM) { v.Status = vm.StateRunning })
	}()

	err := d.WaitForSystemInstance("i-pending", 2*time.Second)
	if err != nil {
		t.Fatalf("expected instance to reach running, got: %v", err)
	}
}

func TestWaitForSystemInstance_Timeout(t *testing.T) {
	d := newDaemonWithVMs(&vm.VM{ID: "i-stuck", Status: vm.StateProvisioning})

	err := d.WaitForSystemInstance("i-stuck", 600*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
