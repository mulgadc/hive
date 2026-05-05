package vm

import "log/slog"

// MigrateStoppedToSharedKV writes instance to the cluster-shared "stopped"
// KV bucket and removes it from the local running map. Returns true only
// when both the shared write succeeded AND the local entry was removed
// under this caller's ownership of the supplied *VM pointer.
//
// Returns false on KV write failure or when a concurrent handler reclaimed
// the slot (DeleteIf no longer matches the supplied pointer). Callers
// should fire OnInstanceDown / persist running-state only on true: a slot
// reclaim means the id now resolves to a different live instance, and
// firing the down hook would tear down the new instance's per-id
// resources (e.g. NATS subscriptions).
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
// MigrateTerminatedToKV. Returns true only when both the shared write
// succeeded and DeleteIf removed the local entry — see the public
// methods' doc comments for why slot-reclaim is reported as failure.
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
		return false
	}
	slog.Info("Migrated instance to KV", "instance", instance.ID, "bucket", label)
	return true
}
