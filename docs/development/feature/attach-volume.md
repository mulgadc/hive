# EC2 AttachVolume

Hot-plug an EBS volume to a running instance via QMP, implementing the AWS `ec2 attach-volume` API.

## Request Flow

```
AWS SDK (attach-volume)
  -> Gateway (port 9999): parse AWS query params, validate input
    -> NATS ec2.cmd.{instanceId}: per-instance topic routing
      -> Daemon handleEC2Events: owns the instance's QEMU process
        1. Validate volume is "available" (Predastore/S3 lookup)
        2. ebs.mount via NATS -> viperblock starts NBD server
        3. QMP blockdev-add -> register NBD block device in QEMU
        4. QMP device_add -> attach as virtio-blk-pci to guest
        5. Update instance metadata + volume metadata
      <- NATS response: ec2.VolumeAttachment JSON
    <- Gateway: wrap in XML
  <- AWS SDK: VolumeAttachment response
```

## Why Per-Instance NATS Routing

AttachVolume uses `ec2.cmd.{instanceId}` instead of a generic `ec2.AttachVolume` topic. This is because the command must reach the specific daemon process that owns the QEMU instance. Each daemon subscribes to `ec2.cmd.{instanceId}` for every instance it manages. The same pattern is used by Start, Stop, and Terminate. If no daemon owns the instance, NATS returns `ErrNoResponders`, which the gateway maps to `InvalidInstanceID.NotFound`.

Other volume operations (CreateVolume, DeleteVolume, DescribeVolumes) use generic topics like `ec2.CreateVolume` because they don't require access to a specific QEMU process.

## Files Changed

### Wire Format: `hive/qmp/qmp.go`

Added `AttachVolume bool` to `Attributes` and a new `AttachVolumeData` struct carried as an optional pointer on `Command`. The pointer-with-omitempty pattern means the field is absent from JSON for non-attach commands, keeping the wire format clean. The `Attributes` struct uses boolean flags for dispatch (matching the existing Start/Stop/Terminate pattern).

### Gateway Handler: `hive/gateway/ec2/volume/AttachVolume.go`

Validates `VolumeId` and `InstanceId` are present, constructs a `qmp.Command` with `AttachVolume: true` and `AttachVolumeData`, sends it to `ec2.cmd.{instanceId}` via NATS request-response (30s timeout). The `Device` field is optional per the AWS API spec.

NATS errors are differentiated: `ErrNoResponders` returns `InvalidInstanceID.NotFound` (no daemon owns this instance), while timeouts and connection errors return `InternalError` to avoid misleading users about non-existent instances when the real problem is infrastructure.

### Gateway Switch: `hive/gateway/ec2.go`

Added `"AttachVolume"` case to the EC2 action switch statement.

### Daemon Handler: `hive/daemon/daemon_handlers.go`

The core logic, placed as the first branch in `handleEC2Events` (before Start/Stop/Terminate). Implements a three-phase commit with reverse-order rollback:

**Phase 1 - EBS Mount:** Sends `ebs.mount` via NATS to viperblock, which starts an NBD server and returns a URI (unix socket or TCP). If this fails, no cleanup is needed since nothing was mounted.

**Phase 2 - QMP blockdev-add:** Registers the NBD block device in QEMU. Uses `nbd-{volumeId}` as the node-name for predictable identification during future detach. The `export` field is set to `""` per viperblock convention (default export). If this fails, Phase 1 is rolled back via `ebs.unmount`.

**Phase 3 - QMP device_add:** Attaches the block device to the guest as `virtio-blk-pci` with id `vdisk-{volumeId}`. If this fails, Phase 2 is rolled back via `blockdev-del`, then Phase 1 via `ebs.unmount`. The `blockdev-del` rollback error is checked: if it fails, the EBS unmount is skipped to avoid tearing down storage that QEMU still references (which could crash the VM).

After all three phases succeed, the handler updates in-memory state (`EBSRequests`, `BlockDeviceMappings`), persists to Predastore (volume metadata) and JetStream (instance state), then responds with `ec2.VolumeAttachment` JSON.

### Volume Service: `hive/handlers/ec2/volume/service_impl.go`

`getVolumeConfig` was renamed to `GetVolumeConfig` (exported directly instead of using a wrapper) so the daemon can validate that a volume exists and check its state before attempting attachment.

`UpdateVolumeState` was extended with a `deviceName` parameter rather than creating a separate `UpdateVolumeAttachment` method. The existing callers pass `""` for deviceName since boot volumes don't track device names this way. This avoids near-duplicate methods.

### Volume Service Interface: `hive/handlers/ec2/volume/service.go`

The `VolumeService` interface is kept to AWS-facing operations only (CreateVolume, DescribeVolumes, ModifyVolume, DeleteVolume). `GetVolumeConfig` and `UpdateVolumeState` are methods on the concrete `VolumeServiceImpl` type, not the interface, because they are internal operations used by the daemon rather than AWS API endpoints. The daemon holds a `*VolumeServiceImpl` directly so it can call these methods without polluting the interface that the gateway-side `NATSVolumeService` also implements.

### Device Name Assignment: `hive/daemon/daemon.go`

`nextAvailableDevice` scans both `EBSRequests` and `BlockDeviceMappings` to find the next available `/dev/sd[f-p]` slot. Both collections are checked because there's a window during attachment where a volume appears in `EBSRequests` but not yet in `BlockDeviceMappings`. Returns `""` when all 11 slots are exhausted, which the handler maps to `AttachmentLimitExceeded`.

The `/dev/sd[f-p]` range follows the AWS convention. The guest OS sees these as `/dev/vd*` when using virtio drivers, but the metadata device name is the `/dev/sd*` form per AWS API compatibility.

### Rollback Helper: `hive/daemon/daemon.go`

`rollbackEBSMount` sends `ebs.unmount` via NATS and verifies the response: checks both the `Error` field and the `Mounted` boolean. Rollback failures are logged at Error level but not propagated to callers since rollback is best-effort cleanup during an already-failed operation. This is a deliberate tradeoff: propagating rollback errors would complicate the handler without changing the user-visible outcome (the attach already failed).

### Config: `hive/config/config.go`

Added `DeviceName` field to `EBSRequest`. This tracks which `/dev/sd*` device name is assigned to a hot-plugged volume. Boot-time volumes don't use this field since their device assignment is handled by QEMU command-line args.

### NBD URI Parsing: `hive/utils/utils.go`

`ParseNBDURI` is the inverse of the existing `FormatNBDSocketURI`/`FormatNBDTCPURI` functions. It extracts the components needed for QMP's `blockdev-add` server argument:
- `nbd:unix:/path/to/socket.sock` -> `type=unix, path=/path/to/socket.sock`
- `nbd://host:port` -> `type=inet, host=host, port=port`

## Design Decisions

**Why QMP hot-plug instead of restarting the VM?** Hot-plug via `blockdev-add` + `device_add` attaches the volume without downtime. This matches AWS behavior where `attach-volume` works on running instances immediately.

**Why `nbd-{volumeId}` and `vdisk-{volumeId}` naming?** These are predictable identifiers derived from the volume ID. This makes future `detach-volume` straightforward: the daemon can construct the node-name and device-id from the volume ID without maintaining a separate mapping.

**Why `export: ""`?** Viperblock's NBD server uses a blank export name as the default. This is a viperblock convention, not a universal NBD convention.

**Why rollback skips EBS unmount when blockdev-del fails?** If `blockdev-del` fails, QEMU still has a reference to the NBD backend. Unmounting the EBS volume would tear down the NBD server while QEMU still tries to do I/O through it, potentially crashing or hanging the VM. Leaving the mount in place is the safer failure mode: the volume is leaked but the VM stays healthy.

**Why `DeleteOnTermination` defaults to false?** This matches AWS behavior: hot-attached volumes default to `DeleteOnTermination: false`. Boot volumes set this to true. Users can change it via `modify-instance-attribute`.

## Error Codes

| Condition | AWS Error Code |
|-----------|---------------|
| Missing VolumeId/InstanceId | `InvalidParameterValue` |
| Volume not found in Predastore | `InvalidVolume.NotFound` |
| Volume already in-use | `VolumeInUse` |
| Instance not found (no NATS responder) | `InvalidInstanceID.NotFound` |
| Instance not running | `IncorrectInstanceState` |
| All device slots full | `AttachmentLimitExceeded` |
| Internal failures (mount, QMP, etc.) | `InternalError` |

## Testing

**Gateway validation** (`AttachVolume_test.go`): Table-driven tests for nil input, empty input, missing fields, empty strings, valid inputs with/without device. Follows the pattern from `CreateVolume_test.go`.

**Device name assignment** (`daemon_test.go`): 6 scenarios covering empty instance, existing block device mappings, existing EBS requests, all devices used, nil instance, mixed sources.

**Handler error paths** (`daemon_test.go`): Tests for missing attach volume data, instance not running, and volume not found. Uses a real embedded NATS server (not mocks). The happy path is not currently testable because `volumeService` is a concrete type — testing the full flow would require introducing interface-based dependency injection for the volume service and QMP client.

**NBD URI parsing** (`utils_test.go`): 8 cases covering both formats, empty paths, missing ports, invalid ports, unsupported formats.

## Known Limitations

- No validation of user-supplied device names against the `/dev/sd[f-p]` range or conflict checking.
- TOCTOU gap between checking instance status/volume state and performing the actual QMP operations. A concurrent stop could race with an attach.
- The `Attributes` struct uses boolean flags for mutually exclusive operations. A string-based action field would be structurally safer but is a breaking wire-format change.
- Volume metadata and JetStream state persistence failures after a successful attach are logged but do not cause the response to indicate failure, since the volume is already physically attached to the VM.
