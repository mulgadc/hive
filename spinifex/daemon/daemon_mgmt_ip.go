package daemon

import (
	"fmt"
	"log/slog"
	"net"
	"sync"

	"github.com/mulgadc/spinifex/spinifex/vm"
)

// MgmtIPAllocator manages static IP allocation for management NICs on system instances.
// IPs are derived from the host's management bridge subnet (.10–.249 range).
// Thread-safe via its own mutex — LaunchSystemInstance does not hold a global lock.
type MgmtIPAllocator struct {
	mu        sync.Mutex
	allocated map[string]string // instanceID → IP
	baseIP    net.IP            // network base (e.g. 10.15.8.0)
	rangeMin  byte              // first host byte (10)
	rangeMax  byte              // last host byte (249)
}

// NewMgmtIPAllocator creates an allocator for the /24 subnet of bridgeIP.
// Allocates from .10 to .249 (240 addresses).
func NewMgmtIPAllocator(bridgeIP string) (*MgmtIPAllocator, error) {
	ip := net.ParseIP(bridgeIP)
	if ip == nil {
		return nil, fmt.Errorf("invalid bridge IP: %s", bridgeIP)
	}
	ip4 := ip.To4()
	if ip4 == nil {
		return nil, fmt.Errorf("not an IPv4 address: %s", bridgeIP)
	}

	// Base is the /24 network: zero out the last octet
	base := make(net.IP, 4)
	copy(base, ip4)
	base[3] = 0

	return &MgmtIPAllocator{
		allocated: make(map[string]string),
		baseIP:    base,
		rangeMin:  10,
		rangeMax:  249,
	}, nil
}

// Allocate assigns a management IP to the given instance.
// Returns the allocated IP string (e.g. "10.15.8.10").
func (a *MgmtIPAllocator) Allocate(instanceID string) (string, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Check if already allocated
	if ip, ok := a.allocated[instanceID]; ok {
		return ip, nil
	}

	// Build set of used IPs for fast lookup
	used := make(map[string]struct{}, len(a.allocated))
	for _, ip := range a.allocated {
		used[ip] = struct{}{}
	}

	// Find first free IP in range
	for i := a.rangeMin; i <= a.rangeMax; i++ {
		candidate := fmt.Sprintf("%d.%d.%d.%d", a.baseIP[0], a.baseIP[1], a.baseIP[2], i)
		if _, taken := used[candidate]; !taken {
			a.allocated[instanceID] = candidate
			slog.Debug("Management IP allocated", "instance", instanceID, "ip", candidate)
			return candidate, nil
		}
	}

	return "", fmt.Errorf("management IP range exhausted (%d.%d.%d.%d–%d.%d.%d.%d)",
		a.baseIP[0], a.baseIP[1], a.baseIP[2], a.rangeMin,
		a.baseIP[0], a.baseIP[1], a.baseIP[2], a.rangeMax)
}

// Release frees the management IP for the given instance.
func (a *MgmtIPAllocator) Release(instanceID string) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if ip, ok := a.allocated[instanceID]; ok {
		delete(a.allocated, instanceID)
		slog.Debug("Management IP released", "instance", instanceID, "ip", ip)
	}
}

// Rebuild reconstructs the allocated set from existing VMs.
// Called on daemon startup after loading VMs from KV store.
func (a *MgmtIPAllocator) Rebuild(vms map[string]*vm.VM) {
	a.mu.Lock()
	defer a.mu.Unlock()

	for id, v := range vms {
		if v.MgmtIP != "" {
			a.allocated[id] = v.MgmtIP
		}
	}

	slog.Info("Management IP allocator rebuilt", "count", len(a.allocated))
}

// AllocatedCount returns the number of currently allocated IPs.
func (a *MgmtIPAllocator) AllocatedCount() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return len(a.allocated)
}
