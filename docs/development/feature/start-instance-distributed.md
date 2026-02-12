# Distributed StartInstances

Decouple stopped EC2 instances from their original daemon node, allowing any available daemon to handle `start-instances` requests.

## Problem

Previously, stopped instances remained bound to the daemon that ran them. The daemon kept an active `ec2.cmd.{instanceID}` NATS subscription after stop, so `start-instances` always routed back to the same node. In multi-node deployments, this meant a stopped instance could not be started if its original daemon was down or at capacity.

## Request Flow

```
AWS SDK (start-instances)
  -> Gateway (port 9999): parse AWS query params
    -> NATS ec2.start (queue group: hive-workers): any daemon can pick up
      -> Daemon handleEC2StartStoppedInstance:
        1. Load instance from shared NATS KV (key: instance.{instanceID})
        2. Validate state is "stopped"
        3. Re-initialize non-serialized fields (QMPClient, EBSRequests.Mu)
        4. Allocate resources (CPU, memory)
        5. LaunchInstance (mount volumes, start QEMU, subscribe to ec2.cmd.{instanceID})
        6. Delete from shared KV
      <- NATS response: {"status":"running","instanceId":"..."}
    <- Gateway: build InstanceStateChange XML
  <- AWS SDK: StartInstancesOutput
```

## How Ownership Release Works

When an instance is stopped (not terminated), the daemon releases ownership in this order:

1. Set `inst.LastNode = d.node` (record which daemon last ran it)
2. Write instance to shared KV at key `instance.{instanceID}`
3. Unsubscribe from `ec2.cmd.{instanceID}` NATS topic
4. Remove instance from local `d.Instances.VMS` map
5. Persist local state via `d.WriteState()` (now without the stopped instance)

The order is deliberate: shared KV is written **first**, then local state is cleaned up. If the daemon crashes between steps 2 and 5, `restoreInstances` on restart detects the overlap and completes the migration.

## Shared KV Storage

Stopped instances are stored in the existing `hive-instance-state` NATS JetStream KV bucket using the key prefix `instance.` (distinct from the per-node prefix `node.`).

Four methods on `JetStreamManager`:
- `WriteStoppedInstance(instanceID, *vm.VM)` — serialize and write to `instance.{instanceID}`
- `LoadStoppedInstance(instanceID)` — returns `nil, nil` if not found (not an error)
- `DeleteStoppedInstance(instanceID)` — idempotent (ignores `ErrKeyNotFound`)
- `ListStoppedInstances()` — iterates all `instance.*` keys, returns `[]*vm.VM`

## DescribeInstances Integration

`DescribeInstances` now includes stopped instances from shared KV. After the existing fan-out to all daemons (which returns running/pending instances from each node's local map), the gateway sends an additional NATS request to `ec2.DescribeStoppedInstances` (queue group, 3s timeout). The response contains reservations for all stopped instances in shared KV. These are merged into the final output.

If the stopped-instance query fails (timeout, no responders), it is logged and skipped — running instances are still returned. This matches the existing pattern where individual daemon fan-out failures are non-fatal.

## Migration on Restart

`restoreInstances` migrates pre-existing stopped instances from per-node state to shared KV:

- Instances in `StateStopped`: set `LastNode`, write to shared KV, remove from local map
- Instances in `StateStopping`: transition to `StateStopped`, then migrate
- `WriteState()` is called at the end to persist the removals

If migration fails for an instance (KV write error), the instance stays in local state as a fallback and will be retried on next restart.

## Files Changed

### VM struct: `hive/vm/vm.go`

Added `LastNode string` field (json: `last_node,omitempty`). Records which daemon node last ran the instance. Set during ownership release on stop. Used for diagnostics and future WAL recovery.

### KV methods: `hive/daemon/jetstream.go`

Added `StoppedInstancePrefix = "instance."` constant and four CRUD methods for stopped instance state. These reuse the `hive-instance-state` bucket — no new bucket required.

### Stop flow: `hive/daemon/daemon_handlers.go`

Extended the background goroutine in `handleStopOrTerminateInstance`. After `TransitionState(inst, StateStopped)`, the stop-only path (not terminate) releases ownership to shared KV. The terminate path is unchanged.

### New handlers: `hive/daemon/daemon_handlers.go`

`handleEC2StartStoppedInstance` — loads from shared KV, validates stopped state, re-initializes non-serialized fields, allocates resources, launches instance, removes from shared KV. On failure, rolls back resource allocation and local map insertion.

`handleEC2DescribeStoppedInstances` — lists all stopped instances from shared KV, applies instance ID filters from request payload, groups by reservation ID, returns `DescribeInstancesOutput`.

### Subscriptions: `hive/daemon/daemon.go`

Added to `subscribeAll()`:
- `ec2.start` with queue group `hive-workers` — load-balanced start requests
- `ec2.DescribeStoppedInstances` with queue group `hive-workers` — only one daemon needs to read shared KV

### Gateway: `hive/gateway/ec2/instance/StartInstances.go`

Rewritten to send `{"instance_id":"..."}` to `ec2.start` topic instead of constructing QMP commands and routing to `ec2.cmd.{instanceID}`. Removed `qmp` import.

### Gateway: `hive/gateway/ec2/instance/DescribeInstances.go`

Added a NATS request to `ec2.DescribeStoppedInstances` after the fan-out loop. Merges stopped instance reservations into the response.

### Restore: `hive/daemon/daemon.go`

`restoreInstances` now migrates stopped instances from per-node state to shared KV instead of re-subscribing to per-instance NATS topics.

## Design Decisions

**Why queue group for `ec2.start`?** NATS queue groups deliver each message to exactly one subscriber. This prevents two daemons from simultaneously trying to start the same instance, without needing distributed locks.

**Why shared KV instead of a database?** The `hive-instance-state` KV bucket already exists for per-node state. Reusing it avoids new infrastructure. NATS JetStream KV provides consistency guarantees sufficient for this use case (single writer per key during normal operation).

**Why write shared KV before removing local state?** Crash safety. If the daemon crashes after the KV write but before local cleanup, the instance exists in both locations. `restoreInstances` detects this overlap and completes the migration. The reverse order (remove local first, then write KV) would risk losing the instance entirely on crash.

**Why `LastNode` instead of re-routing to the original daemon?** The original daemon may be down, at capacity, or decommissioned. Recording which node last ran an instance is useful for debugging and future WAL recovery (Phase 2), but the start request goes to whichever daemon is available.

**Why not a dedicated KV bucket for stopped instances?** The `instance.` prefix provides sufficient namespace separation from `node.` prefix entries. A separate bucket would require additional JetStream configuration for no practical benefit.

## Error Codes

| Condition | AWS Error Code |
|-----------|---------------|
| No instance IDs provided | `InvalidParameterValue` (gateway) |
| Instance not found in shared KV | `InvalidInstanceID.NotFound` |
| Instance not in stopped state | `IncorrectInstanceState` |
| Resource allocation failure | `InsufficientInstanceCapacity` |
| LaunchInstance failure | `InternalError` |
| NATS timeout (no daemons available) | State reported as still stopped |

## Testing

**JetStream KV methods** (`jetstream_test.go`): 6 tests covering write/load round-trip, missing key returns nil, delete (including non-existent), list multiple instances, no interference with per-node state, nil KV bucket handling.

**Handler error paths** (`daemon_handlers_test.go`): 5 tests covering missing instance ID, instance not found in KV, non-stopped state, describe returns stopped instances, describe with instance ID filter.

**Gateway** (`StartInstances_test.go`): 6 tests covering single instance success, multiple instances, empty input, nil IDs skipped, NATS failure (reports stopped state), mixed success and failure.

**State restore** (`state_test.go`): 5 tests updated to verify stopped instances are migrated to shared KV during `restoreInstances` instead of kept in local state.

## Known Limitations

- **All daemons must be updated together.** Old daemons don't subscribe to `ec2.start` and won't release stopped instances to shared KV.
- **No WAL flush before ownership release.** Phase 1 relies on `ebs.unmount` during stop to flush volume data. Phase 2 will add explicit WAL flush to Predastore before releasing ownership.
- **No capacity-aware scheduling.** `ec2.start` uses simple NATS queue group round-robin. A daemon might accept a start request even if it lacks resources, failing at allocation time. The gateway reports this as "still stopped" to the caller.
- **TOCTOU on shared KV.** Between loading an instance from KV and deleting it after successful start, another daemon could theoretically load the same key. In practice this doesn't happen because NATS queue group delivers each `ec2.start` message to exactly one subscriber, and the instance ID in the message maps to a single KV key.
