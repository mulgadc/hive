package daemon

import (
	"testing"
	"time"

	"github.com/mulgadc/spinifex/spinifex/vm"
)

func TestWaitForSystemInstance_AlreadyRunning(t *testing.T) {
	d := &Daemon{
		Instances: vm.Instances{
			VMS: map[string]*vm.VM{
				"i-test1": {ID: "i-test1", Status: vm.StateRunning},
			},
		},
	}

	err := d.WaitForSystemInstance("i-test1", 1*time.Second)
	if err != nil {
		t.Fatalf("expected no error for running instance, got: %v", err)
	}
}

func TestWaitForSystemInstance_NotFound(t *testing.T) {
	d := &Daemon{
		Instances: vm.Instances{
			VMS: map[string]*vm.VM{},
		},
	}

	err := d.WaitForSystemInstance("i-nonexistent", 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for missing instance")
	}
}

func TestWaitForSystemInstance_ErrorState(t *testing.T) {
	d := &Daemon{
		Instances: vm.Instances{
			VMS: map[string]*vm.VM{
				"i-failed": {ID: "i-failed", Status: vm.StateError},
			},
		},
	}

	err := d.WaitForSystemInstance("i-failed", 1*time.Second)
	if err == nil {
		t.Fatal("expected error for failed instance")
	}
}

func TestWaitForSystemInstance_TransitionsToRunning(t *testing.T) {
	inst := &vm.VM{ID: "i-pending", Status: vm.StateProvisioning}
	d := &Daemon{
		Instances: vm.Instances{
			VMS: map[string]*vm.VM{
				"i-pending": inst,
			},
		},
	}

	// Transition to running after a short delay
	go func() {
		time.Sleep(200 * time.Millisecond)
		d.Instances.Mu.Lock()
		inst.Status = vm.StateRunning
		d.Instances.Mu.Unlock()
	}()

	err := d.WaitForSystemInstance("i-pending", 2*time.Second)
	if err != nil {
		t.Fatalf("expected instance to reach running, got: %v", err)
	}
}

func TestWaitForSystemInstance_Timeout(t *testing.T) {
	d := &Daemon{
		Instances: vm.Instances{
			VMS: map[string]*vm.VM{
				"i-stuck": {ID: "i-stuck", Status: vm.StateProvisioning},
			},
		},
	}

	err := d.WaitForSystemInstance("i-stuck", 600*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
