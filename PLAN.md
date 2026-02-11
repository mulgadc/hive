# Plan: Implement CreateImage API

## Context

Users need the ability to create a new AMI from a running (or stopped) instance so they can launch new instances from snapshots of existing ones. This is the standard AWS `CreateImage` flow: snapshot the instance's root volume, then register a new AMI pointing at that snapshot. All the building blocks exist (CreateSnapshot, AMIMetadata, OpenFromSnapshot, cloneAMIToVolume) — they just need to be wired together.

## Flow

```
AWS CLI: create-image --instance-id i-xxx --name "my-image"
  → Gateway (ec2.go)
    → NATS "ec2.CreateImage"
      → Daemon handler
        → Finds instance → gets root volume ID
        → Calls snapshotService.CreateSnapshot(volumeID)
        → Creates new AMI viperblock config with AMIMetadata.SnapshotID
        → Stores as ami-xxx/config.json in S3
        → Returns ami-xxx
```

## Changes

### 1. Gateway: Add CreateImage action entry
**File**: `hive/gateway/ec2.go`
- Add `"CreateImage"` to the `ec2Actions` map, following the DescribeImages pattern

### 2. Gateway: CreateImage handler
**File**: `hive/gateway/ec2/image/CreateImage.go` (new)
- `ValidateCreateImageInput()` — validate InstanceId is present and starts with `i-`
- `CreateImage()` — create NATSImageService, call `svc.CreateImage(input)`
- Follow exact pattern of `gateway/ec2/snapshot/CreateSnapshot.go`

### 3. NATS service: Wire CreateImage to NATS
**File**: `hive/handlers/ec2/image/service_nats.go`
- Replace the `CreateImage` stub with a real NATS request to `"ec2.CreateImage"` topic
- Use `utils.NATSRequest[ec2.CreateImageOutput]` with 120s timeout (snapshot can be slow)

### 4. Daemon: Add NATS subscription and handler
**File**: `hive/daemon/daemon.go`
- Add `{"ec2.CreateImage", d.handleEC2CreateImage, "hive-workers"}` to subscriptions
- Update `NewImageServiceImpl(d.config)` → `NewImageServiceImpl(d.config, d.natsConn)` (~line 515)

**File**: `hive/daemon/daemon_handlers.go`
- Add `handleEC2CreateImage` — this is a **stateful handler** (NOT handleNATSRequest) because it needs instance state to find the root volume
- Steps:
  1. Unmarshal `ec2.CreateImageInput`
  2. Lock `d.Instances.Mu`, find instance in `d.Instances.VMS[*input.InstanceId]`, unlock
  3. Validate instance exists and is in running or stopped state
  4. Find root volume ID from `instance.Instance.BlockDeviceMappings` (first entry with `Ebs.VolumeId`)
  5. Get instance status (running vs stopped) — pass to service so it knows which snapshot path to use
  6. Get source AMI ID from `instance.Instance.ImageId` (for architecture, platform, etc.)
  7. Call `d.imageService.CreateImageFromInstance(params)` passing the extracted data
  8. Respond with JSON

### 5. Image service: Update interface and add CreateImageParams
**File**: `hive/handlers/ec2/image/service.go`
- Keep `CreateImage(input *ec2.CreateImageInput)` signature unchanged on the interface (gateway NATS side doesn't use extra params)

**File**: `hive/handlers/ec2/image/service_impl.go`
- Add a `CreateImageParams` struct for the daemon-side call:
  ```go
  type CreateImageParams struct {
      Input         *ec2.CreateImageInput
      RootVolumeID  string
      SourceImageID string
      IsRunning     bool // true = use ebs.snapshot NATS, false = snapshot from S3 state
  }
  ```
- The daemon handler calls `d.imageService.CreateImageFromInstance(params)` directly (not through the generic interface)

### 6. Image service: Implement CreateImageFromInstance
**File**: `hive/handlers/ec2/image/service_impl.go`

Add `natsConn *nats.Conn` to `ImageServiceImpl` (for running-instance NATS snapshot calls and offline viperblock instantiation for stopped instances). Update constructor.

Implement `CreateImageFromInstance(params CreateImageParams)`:
1. Generate AMI ID: `utils.GenerateResourceID("ami")`
2. Generate snapshot ID: `utils.GenerateResourceID("snap")`
3. **Snapshot the root volume** — two paths:
   - **Running instance**: NATS `ebs.snapshot.{volumeID}` — reuse the exact pattern from `snapshot/service_impl.go:191-218`:
     - Marshal `config.EBSSnapshotRequest{Volume: rootVolumeID, SnapshotID: snapshotID}`
     - Send NATS request to `ebs.snapshot.{rootVolumeID}` with 30s timeout
     - Validate response
   - **Stopped instance**: Volume is not mounted in viperblockd. Data is fully persisted in S3. Create snapshot offline:
     - Instantiate viperblock via `newViperblock(rootVolumeID, ...)` (reuse pattern from `instance/service_impl.go:238-258`)
     - `backend.Init()` + `LoadStateRequest("")` to load config + block map from S3
     - Call `vb.CreateSnapshot(snapshotID)` directly — the flush/WAL steps are no-ops on a freshly loaded instance, it just serializes the block map into the snapshot checkpoint
     - `vb.RemoveLocalFiles()` to clean up temp files
4. **Store snapshot metadata** in `{snapshotID}/metadata.json` — same pattern as `snapshot/service_impl.go:220-252`
5. **Read source AMI config** from S3 `{sourceImageID}/config.json` to get architecture, platform, etc.
6. **Read root volume config** from S3 `{rootVolumeID}/config.json` to get volume size
7. **Build AMI VolumeConfig** with populated `AMIMetadata`:
   - `ImageID`: new ami-xxx
   - `Name`: from input
   - `Description`: from input
   - `SnapshotID`: the snap-xxx just created
   - `Architecture`, `PlatformDetails`, `Virtualization`, etc.: from source AMI (or defaults)
   - `VolumeSizeGiB`: from root volume config
   - `CreationDate`: now
   - `RootDeviceType`: "ebs"
   - `ImageOwnerAlias`: "self"
8. **Store AMI config** to S3 as `{amiID}/config.json` — create a `VBState` with the VolumeConfig. This is what `DescribeImages` reads.
9. Return `&ec2.CreateImageOutput{ImageId: &amiID}`

### 7. Tests
- Add unit test for `CreateImage` in `hive/handlers/ec2/image/` with mock object store
- Add gateway validation test in `hive/gateway/ec2/image/`

## Key Files

| File | Change |
|------|--------|
| `hive/gateway/ec2.go` | Add CreateImage action |
| `hive/gateway/ec2/image/CreateImage.go` | New gateway handler |
| `hive/handlers/ec2/image/service.go` | Update interface |
| `hive/handlers/ec2/image/service_nats.go` | Wire NATS request |
| `hive/handlers/ec2/image/service_impl.go` | Core implementation |
| `hive/daemon/daemon.go` | Add subscription, update constructor |
| `hive/daemon/daemon_handlers.go` | Add stateful handler |

## Existing Code to Reuse

- `snapshot/service_impl.go:191-218` — EBS snapshot NATS pattern (`ebs.snapshot.{volumeID}`)
- `snapshot/service_impl.go:107-123` — `putSnapshotConfig` S3 storage pattern
- `image/service_impl.go:96-126` — Reading viperblock VBState from S3 (`config.json`)
- `instance/service_impl.go:238-258` — `newViperblock()` for offline viperblock instantiation
- `utils.GenerateResourceID()` — ID generation
- `utils.NATSRequest[T]()` — Generic NATS request helper
- `viperblock.VBState`, `viperblock.VolumeConfig`, `viperblock.AMIMetadata` — Existing types

## Verification

1. `make build` — confirm compilation
2. `make test` — all existing tests pass
3. Manual test:
   ```bash
   # Launch instance from existing AMI
   aws ec2 run-instances --image-id ami-xxx --instance-type t2.micro --key-name mykey
   # Create image from running instance
   aws ec2 create-image --instance-id i-xxx --name "test-snapshot-image"
   # Verify it appears
   aws ec2 describe-images --owners self
   # Launch new instance from the snapshot-based AMI
   aws ec2 run-instances --image-id ami-yyy --instance-type t2.micro --key-name mykey
   ```
