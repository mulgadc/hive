# EC2 DescribeVolumeStatus

Report the health and status of EBS volumes, implementing the AWS `ec2 describe-volume-status` API. Returns IO-enabled status and volume health for all or specific volumes.

## Request Flow

```
AWS SDK (describe-volume-status)
  -> Gateway (port 9999): parse AWS query params, validate vol- prefix
    -> NATS ec2.DescribeVolumeStatus: generic topic (no instance affinity)
      -> Daemon DescribeVolumeStatus handler
        Fast path (VolumeIds provided):
          1. Parallel GetVolumeConfig for each volume ID
          2. Build VolumeStatusItem per volume
        Slow path (no VolumeIds):
          1. S3 ListObjects to find vol-* prefixes
          2. Skip internal sub-volumes (-efi, -cloudinit)
          3. GetVolumeConfig + build VolumeStatusItem per volume
      <- NATS response: DescribeVolumeStatusOutput JSON
    <- Gateway: wrap in XML as DescribeVolumeStatusResponse
  <- AWS SDK: DescribeVolumeStatusOutput
```

## Files Created

### Gateway Handler: `hive/gateway/ec2/volume/DescribeVolumeStatus.go`

`ValidateDescribeVolumeStatusInput` validates the vol- prefix on all VolumeIds. Follows the same pattern as `ValidateDescribeVolumesInput` — nil input is valid (means "all volumes"), nil entries in the VolumeIds slice are skipped, any non-`vol-` prefixed ID returns `InvalidVolume.Malformed`.

`DescribeVolumeStatus` validates input, creates a `NATSVolumeService`, and delegates to `volumeService.DescribeVolumeStatus(input)`.

### Gateway Tests: `hive/gateway/ec2/volume/DescribeVolumeStatus_test.go`

9 table-driven test cases mirroring `DescribeVolumes_test.go`: nil input, empty input, valid single ID, valid multiple IDs, no prefix, wrong prefix, empty string, mixed valid/invalid, nil in list.

## Files Modified

### Gateway Switch: `hive/gateway/ec2.go`

Added `"DescribeVolumeStatus"` case to the EC2 action switch. Parses `ec2.DescribeVolumeStatusInput`, calls the gateway handler, wraps result as `"DescribeVolumeStatusResponse"` XML.

### Volume Service Interface: `hive/handlers/ec2/volume/service.go`

Added `DescribeVolumeStatus(input *ec2.DescribeVolumeStatusInput) (*ec2.DescribeVolumeStatusOutput, error)` to the `VolumeService` interface. This is an AWS-facing operation so it belongs on the interface (unlike internal helpers like `GetVolumeConfig`).

### NATS Client: `hive/handlers/ec2/volume/service_nats.go`

Added `DescribeVolumeStatus` method following the same marshal-request-validate-unmarshal pattern as `DescribeVolumes`. Sends to `ec2.DescribeVolumeStatus` topic with 30s timeout.

### Service Implementation: `hive/handlers/ec2/volume/service_impl.go`

Three new methods:

**`DescribeVolumeStatus`** — Entry point. Defaults nil input to empty. Takes the fast path (parallel by-ID lookup) when VolumeIds are provided, or the slow path (S3 ListObjects with vol-* filtering) when no IDs are specified. Same dual-path pattern as `DescribeVolumes`.

**`fetchVolumeStatusByIDs`** — Parallel goroutine fan-out with mutex-protected slice append, matching the `fetchVolumesByIDs` pattern. Volumes that fail to load are logged and skipped.

**`getVolumeStatusByID`** — Reads volume config via the existing `GetVolumeConfig` and builds an `ec2.VolumeStatusItem` with:
- `VolumeId` and `AvailabilityZone` from volume metadata
- `VolumeStatus`: status `"ok"` with two details entries
- `Actions`: empty slice
- `Events`: empty slice

### Service Implementation Tests: `hive/handlers/ec2/volume/service_impl_test.go`

Added `TestDescribeVolumeStatus_NilInputDefaults` — verifies that nil input is correctly defaulted to an empty input (the nil guard runs) before reaching the S3 layer.

## Volume Status Details

Every volume currently returns the same static status:

| Detail | Value | Reason |
|--------|-------|--------|
| `io-enabled` | `passed` | Viperblock doesn't have degraded-IO states |
| `io-performance` | `not-applicable` | Viperblock doesn't track this metric |
| Status | `ok` | No health degradation model yet |
| Actions | empty | No pending actions |
| Events | empty | No scheduled events |

This matches AWS behavior for healthy volumes. When viperblock adds health monitoring, these values can be derived from actual backend state.

## Error Codes

| Condition | AWS Error Code |
|-----------|---------------|
| Invalid volume ID format (no vol- prefix) | `InvalidVolume.Malformed` |
| S3 listing failure | `InternalError` |
| Volume not found | Silently skipped (matching DescribeVolumes behavior) |

## Design Decisions

**Why reuse the DescribeVolumes patterns?** DescribeVolumeStatus has the same data access pattern: optionally filter by volume IDs, otherwise list all. Reusing `GetVolumeConfig`, the parallel fan-out helper, and the S3 listing/filtering logic keeps the implementation consistent and avoids duplication.

**Why static status values?** Viperblock currently has no concept of degraded IO or volume health checks. Returning `"ok"` / `"passed"` is accurate — if a volume's config is readable from S3, it is healthy. When viperblock adds health monitoring, `getVolumeStatusByID` is the single point to update.

**Why empty Actions and Events?** AWS uses these for scheduled maintenance events (e.g., `enable-volume-io`, `retire-volume`). Hive doesn't have scheduled maintenance, so these are always empty.

**Why no Filters/MaxResults/NextToken?** Consistent with the existing DescribeVolumes implementation. These can be added in a future iteration across all Describe* endpoints.

## Testing

```sh
# All volumes
aws --endpoint-url https://localhost:9999 ec2 describe-volume-status

# Specific volumes
aws --endpoint-url https://localhost:9999 ec2 describe-volume-status --volume-ids vol-abc123
```

## Known Limitations

- Filters, MaxResults, and NextToken are accepted but ignored.
- Volume status is always `"ok"` regardless of actual backend health.
- The slow path (list all volumes) is sequential per-volume after the S3 list call, unlike the fast path which is parallel.
