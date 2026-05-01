package daemon

import (
	"fmt"
	"sync"
	"testing"

	"github.com/mulgadc/spinifex/spinifex/vm"
)

func TestNewMgmtIPAllocator(t *testing.T) {
	tests := []struct {
		name      string
		bridgeIP  string
		wantErr   bool
		wantBase3 byte // expected 4th octet of base (always 0)
	}{
		{"valid", "10.15.8.1", false, 0},
		{"different subnet", "192.168.1.33", false, 0},
		{"invalid", "not-an-ip", true, 0},
		{"ipv6", "::1", true, 0},
		{"empty", "", true, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewMgmtIPAllocator(tt.bridgeIP)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if a.baseIP[3] != tt.wantBase3 {
				t.Errorf("base[3] = %d, want %d", a.baseIP[3], tt.wantBase3)
			}
		})
	}
}

func TestMgmtIPAllocator_Allocate(t *testing.T) {
	tests := []struct {
		name     string
		bridgeIP string
		firstIP  string
		secondIP string
	}{
		{"primary subnet", "10.15.8.1", "10.15.8.10", "10.15.8.11"},
		{"different subnet", "192.168.1.33", "192.168.1.10", "192.168.1.11"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a, err := NewMgmtIPAllocator(tt.bridgeIP)
			if err != nil {
				t.Fatal(err)
			}

			ip, err := a.Allocate("i-first")
			if err != nil {
				t.Fatal(err)
			}
			if ip != tt.firstIP {
				t.Errorf("first IP = %q, want %q", ip, tt.firstIP)
			}

			ip, err = a.Allocate("i-second")
			if err != nil {
				t.Fatal(err)
			}
			if ip != tt.secondIP {
				t.Errorf("second IP = %q, want %q", ip, tt.secondIP)
			}

			// Re-allocating same instance returns same IP
			ip, err = a.Allocate("i-first")
			if err != nil {
				t.Fatal(err)
			}
			if ip != tt.firstIP {
				t.Errorf("re-allocate = %q, want %q", ip, tt.firstIP)
			}

			if a.AllocatedCount() != 2 {
				t.Errorf("count = %d, want 2", a.AllocatedCount())
			}
		})
	}
}

func TestMgmtIPAllocator_Release(t *testing.T) {
	a, err := NewMgmtIPAllocator("10.15.8.1")
	if err != nil {
		t.Fatal(err)
	}

	a.Allocate("i-one")
	a.Allocate("i-two")
	a.Release("i-one")

	if a.AllocatedCount() != 1 {
		t.Errorf("count after release = %d, want 1", a.AllocatedCount())
	}

	// Released IP should be reused
	ip, err := a.Allocate("i-three")
	if err != nil {
		t.Fatal(err)
	}
	if ip != "10.15.8.10" {
		t.Errorf("reused IP = %q, want 10.15.8.10", ip)
	}
}

func TestMgmtIPAllocator_ReleaseNonexistent(t *testing.T) {
	a, err := NewMgmtIPAllocator("10.15.8.1")
	if err != nil {
		t.Fatal(err)
	}
	// Should not panic
	a.Release("i-nonexistent")
}

func TestMgmtIPAllocator_Exhaustion(t *testing.T) {
	a, err := NewMgmtIPAllocator("10.15.8.1")
	if err != nil {
		t.Fatal(err)
	}

	// Fill all 240 slots (.10–.249)
	for i := range 240 {
		_, err := a.Allocate(fmt.Sprintf("i-%d", i))
		if err != nil {
			t.Fatalf("allocation %d failed: %v", i, err)
		}
	}

	// Next should fail
	_, err = a.Allocate("i-overflow")
	if err == nil {
		t.Fatal("expected exhaustion error")
	}

	// Release one, then it should work
	a.Release("i-0")
	ip, err := a.Allocate("i-overflow")
	if err != nil {
		t.Fatalf("after release: %v", err)
	}
	if ip != "10.15.8.10" {
		t.Errorf("reused IP = %q, want 10.15.8.10", ip)
	}
}

func TestMgmtIPAllocator_Rebuild(t *testing.T) {
	a, err := NewMgmtIPAllocator("10.15.8.1")
	if err != nil {
		t.Fatal(err)
	}

	vms := map[string]*vm.VM{
		"i-a": {MgmtIP: "10.15.8.10"},
		"i-b": {MgmtIP: "10.15.8.15"},
		"i-c": {MgmtIP: ""}, // no mgmt NIC
		"i-d": {MgmtIP: "10.15.8.20"},
	}

	a.Rebuild(vms)

	if a.AllocatedCount() != 3 {
		t.Errorf("count after rebuild = %d, want 3", a.AllocatedCount())
	}

	// Next allocation should skip the already-used IPs
	ip, err := a.Allocate("i-new")
	if err != nil {
		t.Fatal(err)
	}
	if ip != "10.15.8.11" {
		t.Errorf("next IP after rebuild = %q, want 10.15.8.11", ip)
	}
}

func TestMgmtIPAllocator_Concurrent(t *testing.T) {
	a, err := NewMgmtIPAllocator("10.15.8.1")
	if err != nil {
		t.Fatal(err)
	}

	var wg sync.WaitGroup
	results := make(map[string]string)
	var mu sync.Mutex
	errs := make([]error, 0)

	for i := range 50 {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("i-%d", n)
			ip, err := a.Allocate(id)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			results[id] = ip
		}(i)
	}
	wg.Wait()

	if len(errs) > 0 {
		t.Fatalf("allocation errors: %v", errs)
	}
	if len(results) != 50 {
		t.Errorf("got %d results, want 50", len(results))
	}

	// All IPs should be unique
	seen := make(map[string]bool)
	for id, ip := range results {
		if seen[ip] {
			t.Errorf("duplicate IP %s for %s", ip, id)
		}
		seen[ip] = true
	}
}
