package vm

import "log/slog"

// MigrateStoppedToSharedKV writes instance to the cluster-shared "stopped"
// KV bucket and removes it from the local running map. Returns true on
// success. The migration is a normal-operation state transition (not
// one-time data movement): instances move between local memory, the
// shared "stopped" bucket, and the shared "terminated" bucket as part of
// their lifecycle.
//
// The slot is reclaimed by another handler if the local entry no longer
// matches instance — that's treated as success because the shared write
// has already happened.
func (m *Manager) MigrateStoppedToSharedKV(instance *VM) bool {
	if m.deps.StateStore == nil {
		return false
	}
	return m.migrateInstanceToKV(instance, m.deps.StateStore.WriteStoppedInstance, "stopped")
}

// MigrateTerminatedToKV writes instance to the cluster-shared "terminated"
// KV bucket and removes it from the local running map. Same semantics as
// MigrateStoppedToSharedKV but for the terminated bucket.
func (m *Manager) MigrateTerminatedToKV(instance *VM) bool {
	if m.deps.StateStore == nil {
		return false
	}
	return m.migrateInstanceToKV(instance, m.deps.StateStore.WriteTerminatedInstance, "terminated")
}

// migrateInstanceToKV is the shared body of MigrateStoppedToSharedKV and
// MigrateTerminatedToKV. It stamps LastNode, calls the bucket-specific
// write function, then guards the local delete so a concurrent claim on
// the same id (e.g. a start handler reusing the slot) is not clobbered.
func (m *Manager) migrateInstanceToKV(instance *VM, writeFn func(string, *VM) error, label string) bool {
	instance.LastNode = m.deps.NodeID
	if err := writeFn(instance.ID, instance); err != nil {
		slog.Error("Failed to migrate instance to KV",
			"instance", instance.ID, "bucket", label, "err", err)
		return false
	}
	if !m.DeleteIf(instance.ID, instance) {
		slog.Info("Slot reclaimed by another handler during migration; skipping local delete",
			"instance", instance.ID, "bucket", label)
		return true
	}
	slog.Info("Migrated instance to KV", "instance", instance.ID, "bucket", label)
	return true
}
