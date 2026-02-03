# DeleteOnTermination for EBS Volumes

## Overview

When an EC2 instance is terminated, attached EBS volumes with `DeleteOnTermination=true` are automatically deleted. Volumes with `DeleteOnTermination=false` (the default) remain in "available" state. Internal volumes (EFI, cloud-init) are always cleaned up on termination regardless of the flag.

This matches the AWS EC2 behavior where the root volume's lifecycle can be tied to its instance.

## How It Works

### Volume Types During Termination

Each instance has three volume categories in `EBSRequests.Requests`:

| Volume Type | Identified By | Behavior on Terminate |
|---|---|---|
| Root (user-visible) | `Boot=true` | Respects `DeleteOnTermination` flag |
| EFI partition | `EFI=true` | Always cleaned up via `ebs.delete` NATS |
| Cloud-init ISO | `CloudInit=true` | Always cleaned up via `ebs.delete` NATS |

### Data Flow

```
RunInstances request
  → BlockDeviceMappings[0].Ebs.DeleteOnTermination parsed in GenerateVolumes()
  → Stored on config.EBSRequest.DeleteOnTermination (per-volume)
  → Persisted in viperblock VolumeMetadata.DeleteOnTermination (S3 config.json)
  → Persisted in JetStream VM state (EBSRequests serialized with the instance)

TerminateInstances request
  → handleEC2Events sets deleteVolume=true
  → stopInstance() iterates instance.EBSRequests.Requests:
      1. All volumes: unmounted via ebs.unmount NATS
      2. EFI/cloud-init: ebs.delete NATS sent to stop viperblockd processes
      3. Root with DeleteOnTermination=true: volumeService.DeleteVolume() called
         → Sends ebs.delete NATS to viperblockd
         → Deletes S3 prefixes: vol-xxx/, vol-xxx-efi/, vol-xxx-cloudinit/
      4. Root with DeleteOnTermination=false: skipped, remains "available"
```

### Stop vs Terminate

- **Stop** (`deleteVolume=false`): Volumes are unmounted only. No deletion occurs regardless of the `DeleteOnTermination` flag. The volume state is set to "available".
- **Terminate** (`deleteVolume=true`): The full deletion logic above runs.

## Design Decisions

### Why store the flag on EBSRequest rather than reading it from VolumeMetadata at termination time?

`EBSRequest` is the struct available on the instance at termination time (`instance.EBSRequests.Requests`). Reading from S3 (`VolumeMetadata`) would add a network round-trip during termination and could fail if predastore is degraded. The flag is set once during `GenerateVolumes()` and carried with the instance state in JetStream.

### Why do internal volumes use ebs.delete directly instead of DeleteVolume?

EFI and cloud-init volumes are sub-prefixes of the root volume in S3 (`vol-xxx-efi/`, `vol-xxx-cloudinit/`). When `DeleteVolume` is called on the root volume, it already deletes these sub-prefixes. The direct `ebs.delete` NATS message to viperblockd is only needed to stop the running nbdkit/WAL syncer processes for those volumes. This avoids double-deletion of S3 data.

### Why do errors not block termination?

Following AWS behavior, termination is a best-effort cleanup. If a volume deletion fails (e.g., predastore temporarily unavailable), the instance still transitions to terminated state. The volume may remain as orphaned S3 data that can be cleaned up later. This prevents a broken storage backend from making instances un-terminable.

### Why is DeleteOnTermination=false the default?

This matches AWS EC2 defaults. When no `BlockDeviceMappings` are specified in `RunInstances`, volumes are preserved after termination. This is the safer default since accidental data loss is worse than orphaned volumes.

## Files Changed

| File | Change |
|---|---|
| `hive/config/config.go` | Added `DeleteOnTermination bool` to `EBSRequest` |
| `hive/handlers/ec2/instance/service_impl.go` | Wired flag through `prepareRootVolume`, set on `EBSRequest` and `VolumeMetadata` |
| `hive/daemon/daemon.go` | Implemented deletion logic in `stopInstance()` |
| `hive/handlers/ec2/volume/service_impl.go` | `DescribeVolumes` reads `DeleteOnTermination` from `VolumeMetadata` instead of hardcoded `false` |
| `viperblock/viperblock/viperblock.go` | Added `DeleteOnTermination bool` to `VolumeMetadata` |
| `hive/daemon/daemon_test.go` | Tests for flag propagation, termination deletion, stop-not-delete, and JSON round-trip |

## Testing

### Unit Tests

Run the DeleteOnTermination-specific tests:

```bash
go test -v -run "TestEBSRequest|TestGenerateVolumes_DeleteOnTermination|TestStopInstance_DeleteOnTermination|TestStopInstance_NoDelete|TestEBSRequest_JSON" ./hive/daemon/
```

### Manual Testing

```bash
# Launch with DeleteOnTermination=true
aws --endpoint-url https://localhost:9999 ec2 run-instances \
  --image-id ami-<id> --instance-type t3.micro --key-name <key> \
  --block-device-mappings '[{"DeviceName":"/dev/xvda","Ebs":{"DeleteOnTermination":true,"VolumeSize":4}}]' \
  --no-verify-ssl

# Terminate and verify volume is gone
aws --endpoint-url https://localhost:9999 ec2 terminate-instances --instance-ids <id> --no-verify-ssl
aws --endpoint-url https://localhost:9999 ec2 describe-volumes --volume-ids <vol-id> --no-verify-ssl

# Launch with default (DeleteOnTermination=false)
# Terminate and verify volume remains in "available" state
```

### Log Messages to Watch

- `"Volume has DeleteOnTermination=false, skipping deletion"` - volume preserved
- `"Deleting volume with DeleteOnTermination=true"` - deletion triggered
- `"Sent ebs.delete for internal volume"` - EFI/cloud-init process cleanup
- `"Failed to delete volume on termination"` - deletion failed (non-blocking)
