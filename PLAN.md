# Plan: JetStream KV for Volume→Snapshot Dependency Tracking

## Context

Currently, `DeleteVolume` checks for snapshot dependencies by scanning **all** `snap-*` prefixes in S3 and reading each snapshot's metadata to see if it references the volume (`checkVolumeHasNoSnapshots` in `service_impl.go:848`). This is O(n) in total snapshots and makes multiple S3 round-trips per delete.

We'll replace this with a JetStream KV index keyed by volume ID, giving O(1) lookups on the delete path. The KV is written **before** the snapshot is persisted to S3, so the failure mode is safe: a phantom KV entry blocks deletion of a volume that could technically be deleted, rather than allowing deletion of a volume with live dependents.

## Design

**Bucket:** `hive-volume-snapshots` (new KV bucket, follows existing naming: `hive-ec2-account-settings`, `hive-eigw`)

**Key:** volume ID (e.g., `vol-abc123`)
**Value:** JSON array of snapshot IDs (e.g., `["snap-001","snap-002"]`)

**Write ordering (safe direction):**
1. CreateSnapshot: add snapshot ID to KV **before** writing snapshot to S3
2. DeleteSnapshot: remove snapshot ID from KV **after** deleting snapshot from S3
3. CopySnapshot: add new snapshot ID to KV **before** writing snapshot config to S3

**Read path:** `DeleteVolume` does a single `kv.Get(volumeID)` — if key exists and array is non-empty, return `VolumeInUse`.

## Changes

### 1. Add KV field to SnapshotServiceImpl

**File:** `hive/handlers/ec2/snapshot/service_impl.go`

- Add `snapshotKV nats.KeyValue` field to `SnapshotServiceImpl` struct
- Add `const KVBucketVolumeSnapshots = "hive-volume-snapshots"`
- Add new constructor `NewSnapshotServiceImplWithNATS(cfg, natsConn)` that initializes the KV bucket using the existing `getOrCreateKVBucket` pattern (from `hive/handlers/ec2/account/service_impl.go:63`)
- Reuse `getOrCreateKVBucket` — move it to a shared location or duplicate it (it's 8 lines). Since `account` and `eigw` both have their own copies already, duplicate into this package too for consistency.

### 2. Add KV helper methods to SnapshotServiceImpl

**File:** `hive/handlers/ec2/snapshot/service_impl.go`

Three helpers:

```go
// addSnapshotRef adds snapshotID to the volume's snapshot list in KV.
func (s *SnapshotServiceImpl) addSnapshotRef(volumeID, snapshotID string) error

// removeSnapshotRef removes snapshotID from the volume's snapshot list in KV.
// Deletes the key if the list becomes empty.
func (s *SnapshotServiceImpl) removeSnapshotRef(volumeID, snapshotID string) error

// VolumeHasSnapshots returns true if the volume has any snapshots in KV.
func (s *SnapshotServiceImpl) VolumeHasSnapshots(volumeID string) (bool, error)
```

- `addSnapshotRef`: Get current value (or empty array if `ErrKeyNotFound`), append snapshot ID, Put back
- `removeSnapshotRef`: Get current value, filter out snapshot ID, Put back (or Delete key if empty)
- `VolumeHasSnapshots`: Get key, unmarshal, return `len > 0`. Return `false, nil` on `ErrKeyNotFound`.

All methods are no-ops (log warning, return nil/false) if `snapshotKV` is nil, to support the fallback case where JetStream isn't available.

### 3. Hook into CreateSnapshot

**File:** `hive/handlers/ec2/snapshot/service_impl.go` — `CreateSnapshot()` (line ~153)

After the viperblock NATS snapshot succeeds but **before** `putSnapshotConfig` (the S3 write), call `addSnapshotRef(volumeID, snapshotID)`. If the KV write fails, fail the entire CreateSnapshot and return an error — this prevents creating a snapshot that isn't tracked.

### 4. Hook into DeleteSnapshot

**File:** `hive/handlers/ec2/snapshot/service_impl.go` — `DeleteSnapshot()` (line ~311)

Read the snapshot config first (already done at line 321) to get `volumeID`. **After** all S3 objects are deleted successfully, call `removeSnapshotRef(cfg.VolumeID, snapshotID)`. If KV removal fails, log a warning but don't fail the delete — the safe direction is a phantom entry.

### 5. Hook into CopySnapshot

**File:** `hive/handlers/ec2/snapshot/service_impl.go` — `CopySnapshot()` (line ~355)

CopySnapshot creates a new snapshot referencing the same source volume. **Before** `putSnapshotConfig`, call `addSnapshotRef(sourceCfg.VolumeID, newSnapshotID)`. Fail if KV write fails (same reasoning as CreateSnapshot).

### 6. Replace checkVolumeHasNoSnapshots in VolumeServiceImpl

**File:** `hive/handlers/ec2/volume/service_impl.go`

The volume service doesn't currently have access to the snapshot KV. Rather than giving it a direct KV handle (crossing service boundaries), use a NATS request to a new topic.

Add a new NATS topic `ec2.VolumeHasSnapshots` handled by the daemon. The handler calls `snapshotService.VolumeHasSnapshots(volumeID)` and returns a simple bool response.

**In `DeleteVolume` (line ~711):** Replace the `checkVolumeHasNoSnapshots` S3 scan with a NATS request to `ec2.VolumeHasSnapshots`. Keep the old `checkVolumeHasNoSnapshots` method as a **fallback** if the NATS request fails (e.g., no responders), so the system degrades gracefully.

### 7. Wire up in daemon

**File:** `hive/daemon/daemon.go` (line ~521)

Change `NewSnapshotServiceImpl` → `NewSnapshotServiceImplWithNATS` (with fallback like eigw/account patterns).

**File:** `hive/daemon/daemon.go` — subscriptions

Add `ec2.VolumeHasSnapshots` subscription with queue group `hive-workers`.

**File:** `hive/daemon/daemon_handlers.go`

Add `handleEC2VolumeHasSnapshots` handler:
- Unmarshal volume ID from request
- Call `d.snapshotService.VolumeHasSnapshots(volumeID)`
- Respond with result

### 8. Tests

**File:** `hive/handlers/ec2/snapshot/service_impl_test.go`

- Test `addSnapshotRef` / `removeSnapshotRef` / `VolumeHasSnapshots` with a real JetStream test server
- Test CreateSnapshot writes KV entry
- Test DeleteSnapshot removes KV entry
- Test CopySnapshot adds KV entry for same volume
- Test KV nil fallback (no-op behavior)

**File:** `hive/handlers/ec2/volume/service_impl_test.go`

- Test that DeleteVolume is blocked when VolumeHasSnapshots returns true via NATS
- Test fallback to S3 scan when NATS is unavailable

## Files Modified

| File | Change |
|------|--------|
| `hive/handlers/ec2/snapshot/service_impl.go` | KV field, constructor, helpers, hook into Create/Delete/Copy |
| `hive/handlers/ec2/volume/service_impl.go` | Replace S3 scan with NATS call + fallback |
| `hive/daemon/daemon.go` | Wire `NewSnapshotServiceImplWithNATS`, add subscription |
| `hive/daemon/daemon_handlers.go` | Add `handleEC2VolumeHasSnapshots` handler |
| `hive/handlers/ec2/snapshot/service_impl_test.go` | KV helper tests |
| `hive/handlers/ec2/volume/service_impl_test.go` | Updated DeleteVolume tests |

## Verification

1. `make test` — all existing + new tests pass
2. `make build` — compiles cleanly
3. Manual e2e: create volume → create snapshot → attempt delete volume (should fail) → delete snapshot → delete volume (should succeed)
