# Viperblock Snapshot Feature

## Overview

Viperblock snapshots enable instant, zero-copy point-in-time captures of block storage volumes. A snapshot freezes the block-to-object mapping at a moment in time without copying any data. Both the snapshot and the live volume share the same immutable chunk files on S3. Volumes created from snapshots use a copy-on-write (COW) layered read path, reading unmodified blocks from the source volume's chunks and writing new data to their own chunks.

This is the foundation for AMI-backed instance launches -- `aws ec2 run-instances --image-id ami-ubuntu` will boot a VM that reads Ubuntu blocks on demand from the AMI's frozen snapshot, with zero image copying.

## Key Design Decisions

### 1. Snapshots are frozen block maps, not data copies

Viperblock's chunk files are immutable -- once `chunk.00000001.bin` is written, it never changes. New writes always produce new chunks with higher ObjectIDs. This means a snapshot only needs to capture the current `BlocksToObject` mapping (which block lives at which offset in which chunk file). The actual block data stays in place.

**Trade-off**: This makes snapshot creation O(mapping size) instead of O(data size), but it means the source volume's chunk files cannot be garbage collected while any snapshot references them.

### 2. Copy-on-write via layered block maps

A clone volume created from a snapshot has two block maps:

- **Own map** (`BlocksToObject`) -- blocks written since clone creation
- **Base map** (`BaseBlockMap`) -- frozen read-only map from the parent snapshot

The read path checks own map first, then falls back to the base map, then returns zeros:

```
Read block N:
  1. Own BlocksToObject / BlockStore  -> found? return data from own chunks
  2. BaseBlockMap                     -> found? return data from source volume's chunks via ReadFrom()
  3. Neither                          -> zero block (never written)
```

Writes always go to the clone's own chunks. Once block N is written in the clone, it shadows the snapshot's version permanently.

**Trade-off**: Reads of unmodified blocks require cross-volume I/O (reading from the source volume's S3 prefix). This is the same latency as a normal backend read but targets a different path. The BlockStore caches fetched base blocks so repeated reads are fast.

### 3. Backend interface extended with ReadFrom/WriteTo

The existing `Backend.Read()` and `Backend.Write()` methods construct S3 keys using the backend's configured volume name. Snapshots need to read/write across volume boundaries:

- `CreateSnapshot` writes checkpoint and config to `{snapshotID}/` prefix
- Clone volumes read chunk data from `{sourceVolumeName}/` prefix

Rather than temporarily swapping the backend's volume name (error-prone, not thread-safe), we added two new methods to the `Backend` interface:

```go
ReadFrom(volumeName string, fileType FileType, objectId uint64, offset uint32, length uint32) ([]byte, error)
WriteTo(volumeName string, fileType FileType, objectId uint64, headers *[]byte, data *[]byte) error
```

These are identical to `Read`/`Write` but use the provided volume name for path construction instead of the backend's configured name.

**Trade-off**: This adds two methods to an interface that all backends must implement. The alternative was adding a `volumeName` parameter to the existing methods (breaks all callers) or using a separate backend instance per snapshot operation (heavier). The `ReadFrom`/`WriteTo` approach is minimal and backward-compatible.

### 4. Snapshot state persisted in VBState

Two fields were added to `VBState` (the serialized volume config):

```go
SnapshotID       string  // ID of parent snapshot (empty for non-clones)
SourceVolumeName string  // Volume whose chunks the base map references
```

On `LoadState()`, if `SnapshotID` is set, `OpenFromSnapshot()` is called automatically to reload the base block map. This means a clone volume survives restarts -- the snapshot relationship is restored from the persisted config.

### 5. Both read paths modified (BlockStore and legacy)

Viperblock has two read paths: the optimized `readBlockStore()` (O(1) sharded index) and the legacy `read()` (map rebuilding). Both were modified identically:

- When a block is not found in the volume's own data structures, check `BaseBlockMap`
- Blocks found in the base map are collected into a separate `baseConsecutiveBlocks` list
- These are fetched via `fetchBaseBlocksFromBackend()` which uses `ReadFrom(sourceVolumeName, ...)`
- Fetched base blocks are cached in the BlockStore/LRU for subsequent reads

The two-list approach (own blocks vs base blocks) avoids mixing `Read()` and `ReadFrom()` calls in the same fetch loop.

### 6. Snapshot checkpoint reuses existing serialization

The snapshot's frozen block map is serialized in the same binary format as `SaveBlockState()` -- a header followed by 26-byte `BlockLookup` entries with CRC32 checksums. `LoadSnapshotBlockMap()` deserializes using the same `readBlockWalChunk()` function.

This means snapshot checkpoints are validated with the same integrity checks as regular checkpoints, and no new serialization code was needed.

## S3 Layout

```
vol-abc123/                              # Source volume
  config.json
  chunks/chunk.00000001.bin              # Shared -- snapshot references these
  chunks/chunk.00000002.bin
  checkpoints/blocks.00000001.bin

snap-xyz789/                             # Snapshot
  config.json                            # SnapshotState metadata
  checkpoints/blocks.00000000.bin        # Frozen block map at snapshot time

vol-newclone/                            # Clone volume
  config.json                            # VBState with SnapshotID set
  chunks/chunk.00000001.bin              # Only new writes after clone creation
  checkpoints/blocks.00000001.bin        # Only clone's own block mappings
```

## Files Changed (viperblock repo)

| File | Change |
|------|--------|
| `types/types.go` | Added `ReadFrom`, `WriteTo` to `Backend` interface |
| `viperblock/viperblock.go` | Added `BaseBlockMap`, `SourceVolumeName`, `SnapshotID` to VB struct. Added snapshot fields to `VBState`. Modified `readBlockStore()` and legacy `read()` for base map fallback. Added `fetchBaseBlocksFromBackend()`. Updated `SaveState`/`LoadState`. |
| `viperblock/snapshot.go` | New file: `SnapshotState` type, `CreateSnapshot()`, `LoadSnapshotBlockMap()`, `OpenFromSnapshot()`, `LookupBaseBlockToObject()` |
| `viperblock/backends/file/file.go` | Implemented `ReadFrom()`, `WriteTo()` |
| `viperblock/backends/s3/s3.go` | Implemented `ReadFrom()`, `WriteTo()` |
| `viperblock/backends/memory/memory.go` | Updated stub to satisfy new interface |
| `viperblock/snapshot_test.go` | 6 test cases across file and S3 backends |

## Test Coverage

| Test | What it validates |
|------|-------------------|
| `TestCreateSnapshot` | Flush + checkpoint + config written to backend; round-trip via LoadSnapshotBlockMap |
| `TestSnapshotReadFallback` | Clone reads unmodified blocks from source volume's chunks |
| `TestSnapshotCopyOnWrite` | Overwritten blocks return new data; unmodified blocks return snapshot data |
| `TestSnapshotZeroBlocks` | Blocks never written in snapshot or clone return zeros |
| `TestSnapshotMultiChunk` | Snapshot spanning multiple 4MB chunk files |
| `TestLoadFromSnapshot` | Clone survives save/reset/reload cycle with base map restored |

All tests run against 4 backend configurations: file, file_nocache, s3, s3_nocache.

## Hive API Integration

### Request Flow

```
AWS SDK (create-snapshot --volume-id vol-xxx)
  -> Gateway (port 9999): parse AWS query params, validate VolumeId
    -> NATS ec2.CreateSnapshot: routed to daemon via queue group
      -> Daemon handleEC2CreateSnapshot -> SnapshotServiceImpl.CreateSnapshot()
        1. Fetch volume config from Predastore (validate volume exists)
        2. NATS ebs.snapshot -> viperblockd: flush + freeze block map
        3. Write snapshot metadata to Predastore ({snapId}/metadata.json)
      <- NATS response: ec2.Snapshot JSON
    <- Gateway: wrap in XML
  <- AWS SDK: Snapshot response
```

### NATS Topics

| Topic | Direction | Purpose |
|-------|-----------|---------|
| `ec2.CreateSnapshot` | Gateway -> Daemon | AWS API entry point |
| `ebs.snapshot` | Daemon -> Viperblockd | Flush live VB instance and create frozen block map checkpoint |
| `ec2.DescribeSnapshots` | Gateway -> Daemon | List/filter snapshots |
| `ec2.DeleteSnapshot` | Gateway -> Daemon | Delete snapshot metadata and checkpoint |
| `ec2.CopySnapshot` | Gateway -> Daemon | Copy snapshot metadata (metadata-only) |

### Files Changed (hive repo)

| File | Change |
|------|--------|
| `hive/handlers/ec2/snapshot/service.go` | `SnapshotService` interface |
| `hive/handlers/ec2/snapshot/service_impl.go` | Direct implementation with S3 storage + NATS `ebs.snapshot` call |
| `hive/handlers/ec2/snapshot/service_nats.go` | NATS proxy (gateway -> daemon) |
| `hive/gateway/ec2/snapshot/CreateSnapshot.go` | Gateway validation + NATS dispatch |
| `hive/daemon/daemon_handlers.go` | `handleEC2CreateSnapshot` wiring |
| `hive/services/viperblockd/viperblockd.go` | `ebs.snapshot` subscription handler |
| `hive/config/config.go` | `EBSSnapshotRequest` / `EBSSnapshotResponse` types |
