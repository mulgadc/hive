package vm

import "maps"

import "sync"

// Manager owns the in-memory map of running VMs on this node.
type Manager struct {
	mu  sync.Mutex
	vms map[string]*VM
}

func NewManager() *Manager {
	return &Manager{vms: make(map[string]*VM)}
}

// Get returns the VM for id (and true) or (nil, false).
func (m *Manager) Get(id string) (*VM, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vms[id]
	return v, ok
}

// Insert unconditionally stores v under v.ID, overwriting any existing entry.
func (m *Manager) Insert(v *VM) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vms[v.ID] = v
}

// InsertIfAbsent stores v under v.ID only if no entry currently exists.
// Returns true if the insert happened, false if the slot was already occupied.
func (m *Manager) InsertIfAbsent(v *VM) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.vms[v.ID]; exists {
		return false
	}
	m.vms[v.ID] = v
	return true
}

// Delete removes the entry for id. No-op if absent.
func (m *Manager) Delete(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.vms, id)
}

// DeleteIf deletes the entry for id only if the stored pointer matches want.
// Returns true if the delete happened. Used by stop/terminate handlers to guard
// against the slot being reclaimed by a concurrent start handler.
func (m *Manager) DeleteIf(id string, want *VM) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	current, exists := m.vms[id]
	if !exists || current != want {
		return false
	}
	delete(m.vms, id)
	return true
}

// Count returns the current number of VMs.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.vms)
}

// ForEach calls fn for each VM under the manager lock. fn must not call back
// into Manager methods that take the lock — that will deadlock. For lock-free
// iteration, use Snapshot.
func (m *Manager) ForEach(fn func(*VM)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, v := range m.vms {
		fn(v)
	}
}

// Snapshot returns the current set of VMs as a slice. Callers must not mutate
// the slice's *VM entries' map-related fields; the underlying VM pointers are
// shared with the manager.
func (m *Manager) Snapshot() []*VM {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]*VM, 0, len(m.vms))
	for _, v := range m.vms {
		out = append(out, v)
	}
	return out
}

// SnapshotMap returns a copy of the id→VM map. Used by the state persistence
// adapter so serialization can happen without holding the manager lock.
func (m *Manager) SnapshotMap() map[string]*VM {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]*VM, len(m.vms))
	maps.Copy(out, m.vms)
	return out
}

// Filter returns the VMs for which pred returns true. pred runs under lock.
func (m *Manager) Filter(pred func(*VM) bool) []*VM {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []*VM
	for _, v := range m.vms {
		if pred(v) {
			out = append(out, v)
		}
	}
	return out
}

// UpdateState looks up id and, if found, runs fn(v) under lock. Used for
// atomic check-then-mutate or read-under-lock on a single VM. Returns true
// if the VM was found and fn was invoked.
func (m *Manager) UpdateState(id string, fn func(*VM)) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.vms[id]
	if !ok {
		return false
	}
	fn(v)
	return true
}

// Inspect runs fn(v) under the manager lock for an already-resolved VM
// pointer. Used by call sites that hold a *VM (e.g. from a NATS handler
// dispatch) and need to read or mutate its fields with the same memory-
// ordering guarantee as map-keyed access.
func (m *Manager) Inspect(v *VM, fn func(*VM)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(v)
}

// View runs fn with the live id→VM map under the manager lock. fn must not
// mutate the map nor retain references after returning. Used by serialization
// paths that need every VM's fields to be stable for the duration of the
// encode (e.g. JSON marshal during state persistence).
func (m *Manager) View(fn func(map[string]*VM)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	fn(m.vms)
}

// Replace bulk-resets the manager to the given set of VMs. The supplied map
// is copied; callers may mutate it after the call returns. Used by Restore
// after loading persisted state.
func (m *Manager) Replace(vms map[string]*VM) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vms = make(map[string]*VM, len(vms))
	maps.Copy(m.vms, vms)
}
