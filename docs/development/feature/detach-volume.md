# EC2 DetachVolume

Hot-unplug an EBS volume from a running instance via QMP, implementing the AWS `ec2 detach-volume` API. This is the inverse of `attach-volume`.

## Request Flow

```
AWS SDK (detach-volume)
  -> Gateway (port 9999): parse AWS query params, validate input
    -> NATS ec2.cmd.{instanceId}: per-instance topic routing
      -> Daemon handleEC2Events: owns the instance's QEMU process
        1. Validate volume is attached to instance
        2. Reject boot/EFI/CloudInit volumes
        3. QMP device_del -> remove virtio-blk-pci from guest
        4. QMP blockdev-del -> remove NBD block device from QEMU
        5. ebs.unmount via NATS -> viperblock stops NBD server
        6. Update instance metadata + volume metadata
      <- NATS response: ec2.VolumeAttachment JSON
    <- Gateway: wrap in XML
  <- AWS SDK: VolumeAttachment response
```

## Instance ID Resolution

Unlike AttachVolume, DetachVolume does not require an InstanceId parameter (per the AWS API spec). If InstanceId is omitted, the gateway resolves it by calling DescribeVolumes via NATS to look up the volume's current attachment. If the volume has no attachments, it returns `IncorrectState`.

## Files Changed

### Wire Format: `hive/qmp/qmp.go`

Added `DetachVolume bool` to `Attributes` and a new `DetachVolumeData` struct with `VolumeID`, `Device` (optional), and `Force` (optional). Carried as a pointer with omitempty on `Command`, matching the AttachVolumeData pattern.

### Gateway Handler: `hive/gateway/ec2/volume/DetachVolume.go`

Validates `VolumeId` is present (InstanceId, Device, Force are optional). If InstanceId is not provided, resolves it via `NewNATSVolumeService.DescribeVolumes()`. Constructs `qmp.Command` with `DetachVolume: true` and sends to `ec2.cmd.{instanceId}`.

### Gateway Switch: `hive/gateway/ec2.go`

Added `"DetachVolume"` case to the EC2 action switch statement.

### Daemon Handler: `hive/daemon/daemon_handlers.go`

The core logic, placed after AttachVolume in `handleEC2Events`. Implements a three-phase hot-unplug (reverse order of attach):

**Validation:**
- Volume must be in instance's `EBSRequests`
- Boot, EFI, and CloudInit volumes cannot be detached
- Optional device name cross-check against stored device name

**Phase 1 - QMP device_del:** Removes the virtio-blk-pci device from the guest. Uses `vdisk-{volumeId}` as the device ID (set during attach). If this fails and Force is false, the operation aborts. With Force=true, a warning is logged and execution continues.

**Phase 2 - QMP blockdev-del:** Removes the NBD block device node from QEMU. Uses `nbd-{volumeId}` as the node-name. If this fails, Phase 3 is skipped because the block node is still referenced by QEMU — unmounting would crash the VM.

**Phase 3 - ebs.unmount:** Sends `ebs.unmount` via NATS to viperblock to stop the NBD server. Only executed if Phase 2 succeeded.

**State cleanup (always runs after QMP phases):**
- Remove volume from `instance.EBSRequests.Requests`
- Remove matching entry from `instance.Instance.BlockDeviceMappings`
- `UpdateVolumeState(volumeID, "available", "", "")` — clears attachment in S3
- `WriteState()` — persist instance to JetStream

**Response:** `ec2.VolumeAttachment` with `State: "detaching"`

## Error Codes

| Condition | AWS Error Code |
|-----------|---------------|
| Missing VolumeId | `InvalidParameterValue` |
| Volume not found (gateway lookup) | `InvalidVolume.NotFound` |
| Volume not attached | `IncorrectState` |
| Instance not found (no NATS responder) | `InvalidInstanceID.NotFound` |
| Instance not running | `IncorrectInstanceState` |
| Boot/EFI/CloudInit volume | `OperationNotPermitted` |
| Device mismatch | `InvalidParameterValue` |
| Internal failures | `ServerInternal` |

## Design Decisions

**Why three phases in reverse order?** The attach order is mount -> blockdev-add -> device_add. Detach reverses this: device_del -> blockdev-del -> unmount. The guest device must be removed before the block node, and the block node before the NBD server, to avoid I/O errors or VM crashes.

**Why skip unmount when blockdev-del fails?** If QEMU still references the block node, tearing down the NBD server would cause I/O errors in the VM. The volume is leaked but the VM stays healthy. This matches the same safety rationale used in attach rollback.

**Why the 1-second sleep between device_del and blockdev-del?** The guest OS needs time to acknowledge the PCI device removal. Without this pause, blockdev-del may fail because the device still has a reference to the block node.

**Why allow Force=true to continue past device_del failure?** This matches AWS `--force` behavior for stuck volumes. The device may already be partially removed or the guest may be unresponsive.

**Why state cleanup runs even when QMP phases partially fail?** The metadata should reflect the detach intent. If the QMP commands partially succeeded, the volume is in an inconsistent state from QEMU's perspective but the metadata should be cleaned up so the volume can be re-attached or managed.

## Testing

**Gateway validation** (`DetachVolume_test.go`): Table-driven tests for nil input, empty input, missing VolumeId, empty VolumeId, valid inputs with various optional field combinations.

**Daemon handler tests** (`daemon_test.go`): Tests for missing DetachVolumeData, instance not running, volume not attached, and boot volume protection.

## Known Limitations

- TOCTOU gap between finding the volume in EBSRequests and performing QMP operations.
- No timeout on the 1-second sleep between device_del and blockdev-del.
- Force mode continues past device_del failure but does not retry.
