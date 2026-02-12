# Instance State Management

## Problem Statement

The current implementation tightly couples EC2 instances to specific daemon nodes throughout their entire lifecycle. When an instance is stopped, the daemon retains ownership and NATS subscriptions, preventing instances from being started on different nodes. This creates issues in multi-node deployments and prevents proper recovery from ungraceful shutdowns.

## Goals

1. Enable stopped instances to be started on any available node
2. Handle ungraceful shutdown scenarios (QEMU killed, viperblock/nbdkit crashes)
3. Ensure data consistency when instances migrate between nodes
4. Improve test coverage for edge cases and failure scenarios

## Current Behavior

- Daemon owns an instance throughout its entire lifecycle (running and stopped)
- `ec2.cmd.$instance` NATS subscription remains on the original node even when stopped
- StartInstance commands route directly to the original daemon
- This works correctly for single-node deployments but breaks multi-node scenarios

## Proposed Architecture

### Instance Ownership Model

**Running Instance:**
- Daemon owns the instance and subscribes to `ec2.cmd.$instance`
- All commands (stop, reboot, terminate, etc.) route directly to the owning daemon

**Stopped Instance:**
- Daemon releases the `ec2.cmd.$instance` subscription
- Instance state persists in NATS KV (no daemon ownership)
- Gateway handles StartInstance, routing to any available daemon

### State Transitions

```
[Stopped] --StartInstance--> Gateway routes to available daemon --> [Running]
[Running] --StopInstance-->  Daemon releases subscription       --> [Stopped]
[Running] --Crash/Kill-->    Daemon detects, handles cleanup    --> [Requires Recovery]
```

### Viperblock WAL Synchronization

Viperblock maintains a local WAL on NVMe for performance. On ungraceful shutdown, the WAL may contain blocks not yet pushed to Predastore (S3).

**Graceful Shutdown:**
1. Stop QEMU
2. Flush viperblock WAL to Predastore
3. Release `ec2.cmd.$instance` subscription
4. Instance can start on any node

**Ungraceful Shutdown (crash, kill, hardware failure):**
1. Instance marked as `requires-recovery` in NATS KV
2. WAL data remains on original node's NVMe
3. Instance MUST restart on same node OR original node must sync WAL first

**Recovery Sequence:**
1. StartInstance received for instance in `requires-recovery` state
2. Gateway queries original node: "Can you sync WAL for instance X?"
3. Original node syncs WAL to Predastore
4. Instance state updated to `stopped`
5. Any available node can now start the instance

### Edge Case: Original Node at Capacity

If the original node has no resources available and holds unsync'd WAL data:
1. Original node still syncs WAL to Predastore (sync doesn't require running the instance)
2. After sync completes, instance can start on any other available node

## Implementation Plan

### Phase 1: Refactor Instance Ownership

1. **Modify StopInstance handler** - Release `ec2.cmd.$instance` subscription after graceful stop
2. **Add Gateway StartInstance handler** - Route to available daemon via queue group
3. **Update daemon availability tracking** - Only listen for new instances when resources available
4. **Update NATS KV schema** - Track instance state and last-known node

### Phase 2: Graceful Shutdown Flow

1. **Implement WAL sync on stop** - Ensure viperblock flushes to Predastore before releasing ownership
2. **Add sync confirmation** - Daemon confirms WAL sync complete before releasing subscription
3. **Update instance metadata** - Store `wal_synced: true` in NATS KV

### Phase 3: Crash Detection and Recovery

1. **Detect ungraceful shutdown** - Monitor QEMU, nbdkit, viperblock processes
2. **Mark instances for recovery** - Set `requires_recovery: true` and `last_node: $node_id`
3. **Implement recovery endpoint** - `POST /recover` triggers WAL sync from original node
4. **Add recovery check to StartInstance** - Block start until WAL synced

### Phase 4: Testing

**Unit Tests:**
- Instance state transitions (running → stopped → running)
- Ownership release on stop
- Ownership acquisition on start
- WAL sync completion tracking

**Integration Tests:**
- Multi-node instance migration
- Graceful shutdown and restart on different node
- Concurrent StartInstance requests

**Edge Case Tests:**
- Kill QEMU process mid-operation
- Kill viperblock/nbdkit processes
- Corrupt NATS KV state (instance shows wrong state)
- Node failure during WAL sync
- Start instance when original node unavailable
- Start instance when original node at capacity

**E2E Tests:**
- Full instance lifecycle across multiple nodes
- Recovery from simulated hardware failure

## Technical Notes

### Predastore Replication

All instance data is stored in Predastore, which uses Reed-Solomon erasure coding (e.g., RS(3,2) = 3 data shards, 2 parity shards). Objects are distributed across nodes via consistent hashing. If a data node is unavailable, parity shards can reconstruct the data. See [predastore/DESIGN.md](https://github.com/mulgadc/predastore/blob/main/DESIGN.md).

### NATS Topics

- `ec2.cmd.$instance` - Commands for running instances (daemon subscribes)
- `ec2.start` - StartInstance requests (queue group, any available daemon)
- `ec2.recovery.$node` - WAL sync requests for specific node
