# Cluster Lifecycle: Heartbeats, Shutdown, Upgrades, Resilience, and VM Migration

## Phase 2 Roadmap

| Phase | Feature | Status | Description |
|-------|---------|--------|-------------|
| 2.1 | Heartbeats | **Complete** | Daemon heartbeats publish node status to KV every 10s |
| 2.4 | Coordinated Shutdown | **Complete** | Phased cluster-wide shutdown via NATS |
| 2.2 | Node Modes & Health Monitoring | **Planned** | Drain/maintenance/offline modes, stale heartbeat detection |
| 2.5 | Coordinated Rolling Upgrade | **Planned** | Cluster-wide binary upgrade without full downtime |
| 2.6 | Degraded Storage Writes | **Planned** | Predastore shard redirection when nodes are unavailable |
| 2.7 | VM Migration | **Planned** | Relocate running VMs to other nodes during drain/maintenance |
| 2.8 | [EC2 Health & Auto-Restart](ec2-health-restart.md) | **Planned** | QEMU crash detection, auto-restart, OOM protection, `DescribeInstanceStatus` API |

---

## Phase 2.1 — Heartbeats (Complete)

### Summary

Each daemon publishes its status to JetStream KV every 10 seconds — node name, epoch, running services, VM count, and resource allocation.

### Implementation

- `Heartbeat` struct in `jetstream.go` with node, epoch, services, VM count, resource allocation
- `WriteHeartbeat`/`ReadHeartbeat` KV methods on `JetStreamManager`
- `startHeartbeat()` goroutine in `heartbeat.go` — fires immediately, then every 10s
- `buildHeartbeat()` reads from `ResourceManager` and `Instances`

### Files Modified

| File | Action | Description |
|------|--------|-------------|
| `hive/daemon/jetstream.go` | Edit | `Heartbeat` struct + KV methods |
| `hive/daemon/heartbeat.go` | New | `startHeartbeat()`, `buildHeartbeat()` goroutine |
| `hive/daemon/daemon.go` | Edit | Call `startHeartbeat()` in `Start()` |
| `hive/daemon/heartbeat_test.go` | New | Heartbeat tests (build, allocation, KV round-trip) |

---

## Phase 2.4 — Coordinated Cluster Shutdown (Complete)

### Summary

`hive admin cluster shutdown` drives a phased shutdown across all nodes via NATS, replacing the error-prone per-node `stop-dev.sh` approach for multi-node clusters.

### Design Decisions

- **Option B chosen**: Daemons handle stopping local services internally via `utils.StopProcess()` when they receive NATS shutdown phase messages. The CLI coordinator just sends messages and collects ACKs.
- **Shutdown phases**: GATE → DRAIN → STORAGE → PERSIST → INFRA — ordered to prevent data loss
- **INFRA phase is fire-and-forget**: NATS is going down, no ACK possible
- **Scripts**: `stop-dev.sh` auto-detects multi-node and delegates to `hive admin cluster shutdown`; `HIVE_FORCE_LOCAL_STOP=1` overrides for per-service stop

### Implementation

- Five shutdown phase handlers in `shutdown.go`: GATE, DRAIN, STORAGE, PERSIST, INFRA
- `ShutdownRequest`, `ShutdownACK`, `ShutdownProgress` message types
- `shuttingDown atomic.Bool` on Daemon — set during GATE, checked by `setupShutdown()` to skip redundant VM stops
- NATS subscriptions for `hive.cluster.shutdown.{phase}` (fan-out, no queue group)
- Progress updates published during DRAIN phase
- CLI coordinator in `cmd/hive/cmd/cluster.go` with `--force`, `--timeout`, `--dry-run` flags
- `stop-dev.sh`: `is_multinode()` detection, delegates to coordinated shutdown (skipped when data-dir arg passed, e.g. E2E cleanup)
- `start-dev.sh`: `is_multinode()` detection, cluster health poll at end

### Files Modified

| File | Action | Description |
|------|--------|-------------|
| `hive/daemon/jetstream.go` | Edit | `ClusterShutdownState` struct + KV methods |
| `hive/daemon/shutdown.go` | New | Shutdown types + 5 phase handlers |
| `hive/daemon/daemon.go` | Edit | `shuttingDown atomic.Bool`, shutdown subscriptions, updated `setupShutdown()` |
| `cmd/hive/cmd/admin.go` | Edit | `clusterCmd` + `clusterShutdownCmd` registration |
| `cmd/hive/cmd/cluster.go` | New | `runClusterShutdown()` coordinator |
| `scripts/stop-dev.sh` | Edit | Multi-node detection + coordinated shutdown delegation |
| `scripts/start-dev.sh` | Edit | Multi-node detection + cluster health poll |
| `hive/daemon/shutdown_test.go` | New | Shutdown tests (marshal, KV round-trip, flag behavior) |

### Testing

- `make preflight` — all checks pass
- 9 new unit tests
- `make test-docker-multi` — full multi-node E2E passes (2 consecutive runs confirmed)
- `hive admin cluster shutdown --dry-run` prints phase plan
- `stop-dev.sh` on multi-node delegates to coordinated shutdown
- `HIVE_FORCE_LOCAL_STOP=1 stop-dev.sh` on multi-node uses per-service stop
- `stop-dev.sh` on single-node uses per-service stop (unchanged)

---

## Phase 2.2 — Node Modes & Health Monitoring (Planned)

### Problem Statement

Currently, nodes are either "running" or "not responding." There is no concept of a node being in a transitional state — draining VMs, undergoing maintenance, or recovering from a crash. The health endpoint returns hardcoded `"running"`, and `hive get nodes` shows `"Ready"` or `"NotReady"` with no in-between.

This means:
- No way to gracefully take a node offline for maintenance without a full cluster shutdown
- No distinction between a planned offline and a crash — rejoining is the same either way
- No stale heartbeat detection — if a node stops publishing heartbeats, nobody notices
- The scheduler keeps routing work to draining nodes until they're fully dead

### Design: Node Modes

```
              ┌─────────┐
              │ NORMAL  │ ← default, accepts all work
              └────┬────┘
        drain cmd  │  ▲  drain-cancel / rejoin
                   ▼  │
              ┌─────────┐
              │DRAINING │ ← rejects new VMs, existing VMs run until migrated/stopped
              └────┬────┘
        all VMs    │
        stopped    │
                   ▼
              ┌─────────┐
              │CORDONED │ ← no VMs, services still running, ready for maintenance
              └────┬────┘
      maintenance  │  ▲  uncordon
         cmd       ▼  │
              ┌─────────┐
              │MAINTENANCE│ ← services may be stopped, node offline
              └─────────┘
```

Add a `NodeMode` type to the Daemon struct (atomic for lock-free reads). Store in JetStream KV (`node.<name>.mode`) so it survives daemon restarts and is visible cluster-wide.

#### Mode Behaviors

| Mode | Accepts new VMs | Existing VMs | Services | Heartbeats |
|------|----------------|--------------|----------|------------|
| `normal` | Yes | Run | All running | Published |
| `draining` | No | Run (being migrated/stopped) | All running | Published with mode |
| `cordoned` | No | None | All running | Published with mode |
| `maintenance` | No | None | May be stopped | Published until NATS stops |

#### CLI Commands

```bash
hive node drain <node-name>          # NORMAL → DRAINING (triggers VM migration/stop)
hive node drain-cancel <node-name>   # DRAINING → NORMAL (abort drain)
hive node cordon <node-name>         # NORMAL/CORDONED (reject new VMs, don't touch existing)
hive node uncordon <node-name>       # CORDONED/DRAINING → NORMAL
hive node maintenance <node-name>    # CORDONED → MAINTENANCE
```

#### Implementation Details

**Heartbeat extension** — add `Mode` field to `Heartbeat` struct:
```go
type Heartbeat struct {
    // ... existing fields ...
    Mode string `json:"mode"` // "normal", "draining", "cordoned", "maintenance"
}
```

**Resource manager integration** — when mode != `normal`, unsubscribe from all `ec2.RunInstances.*` topics. The existing `updateInstanceSubscriptions()` already unsubscribes when capacity is 0; extend it to check mode.

**Stale heartbeat detection** — new goroutine `monitorPeerHeartbeats()`:
- Every 30s, read all `heartbeat.*` keys from KV
- If any peer's timestamp is >60s old (6 missed heartbeats), mark as `"suspect"`
- If >120s old, log warning and publish alert to `hive.cluster.alert`
- Display in `hive get nodes` as `"Unreachable"` status

**Graceful rejoin** — when a node starts and finds an existing `node.<name>.mode` key:
- If `maintenance`: transition to `normal` after services are healthy
- If `draining`: resume drain (check for remaining VMs)
- Publish heartbeat immediately on startup to signal liveness

### Proposed Changes

| File | Action | Description |
|------|--------|-------------|
| `hive/config/config.go` | Edit | Add `NodeMode` type and constants |
| `hive/daemon/daemon.go` | Edit | Add `nodeMode atomic.Value`, check mode in request handlers |
| `hive/daemon/heartbeat.go` | Edit | Add `Mode` to heartbeat, add `monitorPeerHeartbeats()` |
| `hive/daemon/jetstream.go` | Edit | Add `WriteNodeMode`/`ReadNodeMode` KV methods |
| `hive/daemon/daemon_handlers.go` | Edit | Check mode before accepting RunInstances |
| `cmd/hive/cmd/node.go` | New | `hive node drain/cordon/uncordon/maintenance` commands |
| `cmd/hive/cmd/get.go` | Edit | Show mode in `hive get nodes` output |

### Testing

- Unit tests for mode transitions, heartbeat staleness detection
- `make test-docker-multi`: drain a node, verify new VMs route elsewhere
- Remote cluster: `hive node drain node3`, verify VMs stop, verify `hive get nodes` shows "Draining"

---

## Phase 2.5 — Coordinated Rolling Upgrade (Planned)

### Problem Statement

Upgrading the Hive binary across a cluster currently requires either:
1. Full cluster shutdown → upgrade all nodes → restart (downtime)
2. Manual per-node SSH, stop, upgrade, restart (error-prone, no ordering guarantees)

Neither approach is acceptable for production clusters. We need a coordinated upgrade that takes nodes offline one at a time, upgrades them, and brings them back — while the cluster continues serving requests on the remaining nodes.

### Design: Rolling Upgrade via NATS Coordination

The cluster already has the building blocks: coordinated shutdown phases, heartbeats, and (with Phase 2.2) node modes. A rolling upgrade orchestrates these per-node:

```
For each node (one at a time):
  1. DRAIN node (Phase 2.2) — migrate/stop VMs
  2. CORDON node — no new work
  3. Stop services on node (reuse shutdown phases, targeted to single node)
  4. Upgrade binary
  5. Start services
  6. UNCORDON — resume normal operation
  7. Wait for health check before proceeding to next node
```

#### Upgrade Delivery Mechanism

**Option A: Binary push via Predastore (recommended)**

The upgrade coordinator (any node or external CLI) uploads the new `hive` binary to Predastore (S3-compatible), then each node downloads and replaces its own binary. No SSH needed, no inter-node trust beyond what NATS already provides.

```bash
# Operator uploads new binary
hive admin cluster upgrade --binary ./bin/hive

# Internally:
# 1. Upload binary to predastore: s3://hive-system/upgrades/hive-<version>
# 2. For each node:
#    a. Send NATS "hive.node.upgrade" to target node
#    b. Node downloads binary from predastore
#    c. Node replaces /usr/local/bin/hive (or wherever binary lives)
#    d. Node restarts services
#    e. Wait for heartbeat to confirm healthy
```

**Option B: Manual per-node (simpler, good enough for now)**

The operator runs `hive node drain <name>`, SSHes in, replaces the binary, and runs `hive node uncordon <name>`. The CLI provides the orchestration framework but the binary swap is manual.

**Option C: SSH-based push (rejected)**

Having Hive nodes SSH into each other introduces key management complexity, a lateral movement attack surface, and fragile dependency on SSH config. Not recommended.

#### Decision: Option A for automation, Option B as fallback

Option A (binary via Predastore) is the right long-term approach — the storage layer already exists, and nodes can self-serve the upgrade. Option B is the immediate path since it requires no new infrastructure.

### Implementation Plan

**Phase 2.5a: Manual rolling upgrade (Option B)**

CLI commands that coordinate the drain/upgrade/uncordon flow but leave the binary swap to the operator:

```bash
hive admin cluster upgrade --manual
# Prints step-by-step instructions per node:
#   1. Draining node1... done (0 VMs migrated)
#   2. SSH to node1 and run: sudo cp /path/to/new/hive /usr/local/bin/hive
#   3. Press Enter when done...
#   4. Restarting node1 services...
#   5. Waiting for node1 health... healthy
#   6. Proceeding to node2...
```

**Phase 2.5b: Automated rolling upgrade (Option A)**

Add binary upload to Predastore and NATS-triggered self-upgrade:

```bash
hive admin cluster upgrade --binary ./bin/hive [--version v1.2.3]
```

- New NATS handler `hive.node.upgrade` on each daemon
- Handler downloads binary from predastore, validates checksum, replaces in-place
- Daemon exec's itself to restart with new binary (or uses systemd restart)

### Proposed Changes

| File | Action | Description |
|------|--------|-------------|
| `cmd/hive/cmd/cluster.go` | Edit | Add `clusterUpgradeCmd` with `--manual`/`--binary` flags |
| `hive/daemon/daemon_handlers.go` | Edit | Add `hive.node.upgrade` handler (Phase 2.5b) |
| `hive/daemon/upgrade.go` | New | Binary download, validation, self-replace logic |

### Testing

- `make test-docker-multi`: run upgrade flow (swap binary, verify services restart)
- Remote cluster: full rolling upgrade with `hive admin cluster upgrade --manual`

---

## Phase 2.6 — Degraded Storage Writes (Planned)

### Problem Statement

**Current behavior**: Predastore uses Reed-Solomon RS(K, M) erasure coding (default RS(3,2) for 5 nodes, RS(2,1) for 3 nodes). When writing an object, it splits into K data shards + M parity shards and sends each to a deterministic node via consistent hashing. **If any shard destination is unreachable, the entire write fails immediately.** There is no retry, no alternate node selection, no degraded write mode.

This means a single node being drained or offline makes **all writes fail** — even though RS coding is designed to tolerate M node failures for reads.

**Desired behavior**: When a node is unavailable (draining, maintenance, or crashed), writes should succeed by redirecting shards to an available alternate node. The metadata should track where shards actually landed, and a background repair process should move shards back to their correct home when the node returns.

### Current Architecture

```
PUT /bucket/key
  → Hash object key → consistent hash ring → [node0, node1, node2] for RS(2,1)
  → Split into 2 data shards + 1 parity shard
  → QUIC Put to node0 (data shard 0)     ← fails if node0 is down
  → QUIC Put to node1 (data shard 1)
  → QUIC Put to node2 (parity shard 0)
  → Store ObjectToShardNodes metadata in s3db (Raft)
```

Key code: `putObjectViaQUIC()` in `predastore/backend/distributed/distributed.go:506`

### Design: Shard Redirection with Repair Queue

#### Write Path Changes

When a shard destination is unreachable:
1. Catch the QUIC dial/write error for the failing shard
2. Query the hash ring for the next available node (skip nodes marked as draining/maintenance/offline)
3. Write the shard to the alternate node
4. Update `ObjectToShardNodes` metadata to reflect actual placement
5. Enqueue a repair job: "shard X of object Y should be on node Z, currently on node W"

```
PUT with node0 down:
  → QUIC Put to node0 → FAIL
  → Redirect: QUIC Put to node3 (next on ring)
  → QUIC Put to node1 (data shard 1) → OK
  → QUIC Put to node2 (parity shard 0) → OK
  → Store metadata: DataShardNodes=[3, 1], ParityShardNodes=[2]
  → Enqueue repair: {object: key, shard: 0, expected: node0, actual: node3}
```

#### Node Health Awareness in Predastore

Predastore needs to know which nodes are available. Options:

**Option A: Heartbeat-based (NATS KV)** — Predastore reads hive daemon heartbeats from the `hive-cluster-state` KV bucket. Requires Predastore to connect to NATS (it currently doesn't).

**Option B: Predastore-internal health checks** — Each Predastore node pings peers via QUIC periodically. If a peer fails to respond after N attempts, mark it as unavailable locally. Simpler, self-contained.

**Option C: Configuration-driven** — The `hive node drain` CLI command updates predastore config (via NATS → daemon → predastore config reload). Predastore skips nodes marked as unavailable. Relies on the Hive daemon to propagate state.

**Recommendation**: Option B for crash detection (self-contained, no new dependencies), Option C for planned drain/maintenance (explicit, reliable). Both can coexist.

#### Repair Queue

When the unavailable node returns:
1. The returning node announces itself (heartbeat resumes or health check passes)
2. A background repair goroutine scans the repair queue
3. For each misplaced shard:
   - Read shard from alternate node
   - Write shard to correct node
   - Update `ObjectToShardNodes` metadata
   - Remove repair queue entry
4. Optionally delete the shard from the alternate node (after confirming the correct node has it)

Repair queue storage: JetStream stream (`HIVE-REPAIR`) or a dedicated KV bucket. Durable, replicated, survives restarts.

#### Write Consistency Guarantees

- **Minimum write quorum**: At least K data shards + M parity shards must be written for the PUT to succeed. Redirected shards count toward the quorum.
- **If fewer than K+M nodes are available**: Write fails (cannot satisfy RS requirements). For RS(2,1) with 3 nodes, at most 1 node can be down.
- **Metadata is always authoritative**: `ObjectToShardNodes` records where shards actually are, not where the hash ring says they should be. Reads use metadata, not the ring.

### Proposed Changes

| File | Action | Description |
|------|--------|-------------|
| `predastore/backend/distributed/distributed.go` | Edit | Shard redirection in `putObjectViaQUIC()`, alternate node selection |
| `predastore/backend/distributed/repair.go` | New | Repair queue, background repair goroutine |
| `predastore/backend/distributed/health.go` | New | Peer health checks, node availability tracking |
| `predastore/backend/distributed/distributed.go` | Edit | `ObjectToShardNodes` — ensure metadata reflects actual placement |

### Testing

- Unit tests: write with simulated node failure, verify redirect and metadata
- `make test-docker-multi`: stop one Predastore node, verify S3 PUTs still succeed, restart node, verify repair
- Remote cluster: drain a node, upload files via AWS CLI, verify objects readable

---

## Phase 2.7 — VM Migration (Planned)

### Problem Statement

When a node is drained for maintenance (Phase 2.2), its running VMs are currently stopped and terminated. The user's workload goes offline until they manually launch new instances on other nodes. For a platform aiming at AWS compatibility, this is unacceptable — VMs should be transparently relocated to other nodes with minimal or zero downtime.

### Background: Why Hive Is Well-Suited for Migration

Hive's architecture makes VM migration simpler than typical hypervisors:

1. **Storage is already shared** — all VM disks are backed by Viperblock/Predastore over the network (NBD). Both source and destination nodes can access the same volumes. No disk data transfer needed during migration.
2. **QMP is already implemented** — `hive/qmp/qmp.go` has a working QMP client over UNIX sockets. Migration commands are just more QMP messages.
3. **VM state is in JetStream KV** — instance metadata (`hive-instance-state` bucket) is already shared across the cluster. Cross-node handoff just updates the KV key.
4. **`ResetNodeLocalState()`** — `hive/vm/vm.go` already has a method that zeros out PID, PTS, QMPClient for cross-node handoff.

### Design: Two-Phase Migration Approach

#### Phase 2.7a: Cold Migration (Stop → Save → Restore)

Simplest path — stop the VM, save its memory state to a file, transfer the file, restore on destination. Total downtime: seconds to minutes depending on RAM size.

**When to use**: Planned maintenance, low-priority VMs, initial implementation.

```
Source Node                           Destination Node
────────────                          ────────────────
1. QMP: stop (pause vCPUs)
2. QMP: migrate "exec:cat > /tmp/state"
3. Wait for migration complete
4. Transfer state file ──────────────→ 5. Receive state file
                                       6. Start nbdkit for same volumes
                                       7. Launch QEMU with -incoming "exec:cat /tmp/state"
                                       8. VM resumes execution
9. Cleanup: stop nbdkit, remove state
10. Update KV: instance now on dest
```

**State file transfer options**:
- **Predastore (S3)**: Upload state to `s3://hive-system/migrations/<instance-id>.state`, destination downloads it. Reuses existing infrastructure, works across any network topology.
- **Direct TCP**: Pipe state directly via QEMU's `tcp:` transport. Faster (no intermediate storage), but requires direct connectivity between nodes.
- **NATS object store**: For smaller VMs (<1GB RAM), NATS JetStream object store could work. Built-in replication.

**Recommendation**: Start with Predastore for simplicity (upload/download pattern), add direct TCP later for performance.

#### Phase 2.7b: Live Migration (Pre-Copy)

Minimal downtime — VM runs on source while memory is copied to destination. Final switchover pauses VM for <500ms.

**When to use**: Production workloads, load balancing, zero-downtime maintenance.

```
Source Node                           Destination Node
────────────                          ────────────────
                                       1. Start nbdkit for same volumes
                                       2. Launch QEMU with -incoming defer
                                       3. QMP: migrate-incoming "tcp:0:PORT"
4. QMP: migrate-set-capabilities
   [auto-converge, multifd]
5. QMP: migrate "tcp:dest:PORT"
6. ── pre-copy: bulk RAM transfer ──→
7. ── iterative: dirty pages ──────→
8. ── converged: final switchover ──→  9. VM resumes on destination
10. Cleanup source
11. Update KV
```

**QMP commands** (existing `SendQMPCommand` infrastructure):

```go
// On source:
d.SendQMPCommand(qmpClient, QMPCommand{
    Execute: "migrate-set-capabilities",
    Arguments: map[string]any{"capabilities": []map[string]any{
        {"capability": "auto-converge", "state": true},
    }},
}, instanceID)

d.SendQMPCommand(qmpClient, QMPCommand{
    Execute: "migrate",
    Arguments: map[string]any{"uri": fmt.Sprintf("tcp:%s:%d", destHost, destPort)},
}, instanceID)

// Monitor progress:
resp := d.SendQMPCommand(qmpClient, QMPCommand{Execute: "query-migrate"}, instanceID)
// resp.Return.status: "active", "completed", "failed"
```

**Convergence**: For typical VMs (512MB-8GB RAM) on 10GbE, pre-copy converges in 5-30 seconds with <300ms switchover. `auto-converge` throttles guest vCPUs if dirty page rate exceeds transfer rate.

**TLS for migration streams**: Use the cluster's existing CA (generated by `dev-setup.sh` / `hive admin init`). Each node gets a dual-role TLS cert for QEMU migration. QEMU's `-object tls-creds-x509` handles the TLS handshake natively.

### Migration Coordination Flow (NATS)

```
CLI: hive node drain node3
  → NATS "hive.node.setmode" {node: "node3", mode: "draining"}

Daemon (node3): mode → draining
  → Unsubscribe from ec2.RunInstances.* topics
  → For each running VM:
      1. Select destination: pick node with most available resources + mode=normal
      2. NATS Request "hive.node.prepare-migration" → destination daemon
         Payload: {instanceID, volumes, vmConfig, migrationType: "live"|"cold"}
      3. Destination prepares: start nbdkit, launch QEMU with -incoming
      4. Destination responds: {ready: true, migrationEndpoint: "tcp:10.1.3.170:49152"}
      5. Source initiates QEMU migration
      6. Monitor progress, publish to "hive.cluster.migration.progress"
      7. On completion:
         - Source: cleanup local QEMU, nbdkit, update KV
         - Destination: update KV with new ownership
      8. Move to next VM

When all VMs migrated:
  → mode → cordoned (ready for maintenance)
```

### Destination VM Preparation

The destination daemon must reconstruct an identical QEMU environment:

1. **Mount volumes**: Send `ebs.mount` NATS requests for each of the VM's volumes. Viperblock starts nbdkit processes pointing at the same underlying data.
2. **Build QEMU command**: Same CPU, memory, machine type, devices, network config as source. Add `-incoming defer` flag.
3. **Network setup**: Same MAC address (preserved in migration stream), same port forwarding rules.
4. **Start QEMU**: Process starts but vCPUs are paused, waiting for migration data.

Key reuse point: the VM struct's `Config.Execute()` method in `hive/vm/vm.go` already builds the QEMU command. A variant with `-incoming` is straightforward.

### Post-Migration Cleanup

On the source node after successful migration:
1. Kill the source QEMU process (should already be exited after migration completes)
2. Unmount volumes (send `ebs.unmount` for each volume)
3. Remove local state (PID file, QMP socket, serial PTS)
4. Update JetStream KV: move instance from `node.<source>.instances` to `node.<dest>.instances`

The existing `ResetNodeLocalState()` method zeros out PID, PTS, QMPClient — exactly what's needed for the handoff.

### Failure Handling

| Failure | During Cold Migration | During Live Migration |
|---------|----------------------|----------------------|
| Source dies mid-migration | State file may be incomplete; VM is lost. Restart from stopped state on any node. | Migration aborts. If source QEMU still running, VM continues on source. If not, VM lost. |
| Destination dies mid-migration | Source VM is paused; resume on source. Retry with different destination. | Migration aborts. VM continues running on source (pre-switchover). |
| Network partition | State file transfer fails; resume on source. | Pre-copy: migration stalls, eventually times out, VM stays on source. Post-switchover: VM on destination, source can't clean up (handled on reconnect). |
| Storage failure | Both sides fail equally (shared storage). | Same — storage is shared, not part of migration stream. |

### Proposed Changes

| File | Action | Description |
|------|--------|-------------|
| `hive/daemon/migration.go` | New | Migration coordinator: prepare, execute (cold/live), cleanup |
| `hive/daemon/daemon_handlers.go` | Edit | Add `hive.node.prepare-migration` NATS handler |
| `hive/qmp/qmp.go` | Edit | Add migration QMP command types and helpers |
| `hive/vm/vm.go` | Edit | Add `IncomingConfig()` variant for destination QEMU launch |
| `cmd/hive/cmd/node.go` | Edit | `hive node drain` triggers migration flow |

### Testing

- Unit tests: migration state machine, QMP command construction
- `make test-docker-multi`: cold migration between nodes (Docker E2E doesn't have KVM so live migration won't work there — use remote cluster)
- Remote cluster (`scripts/iac/hive-test.sh`): full live migration test
  1. Launch VM on node3
  2. SSH into VM, start a long-running process
  3. `hive node drain node3`
  4. Verify VM appears on another node
  5. SSH into VM again, verify process still running (live) or VM rebooted cleanly (cold)

---

## Implementation Priority & Dependencies

```
Phase 2.8 A-B-D1 (Crash Detection, Auto-Restart, OOM Scores)
  │         ↑ no external dependencies, can start immediately
  │
Phase 2.2 (Node Modes) ◄── Phase 2.8 C+E (QMP Health, DescribeInstanceStatus API)
  │                          ↑ shares health state infrastructure with 2.2
  │
  ├──→ Phase 2.7a (Cold Migration) — requires drain mode + crash detection (2.8 A)
  │      │
  │      └──→ Phase 2.7b (Live Migration) — builds on cold migration
  │
  ├──→ Phase 2.5 (Rolling Upgrade) — requires drain/uncordon
  │
  ├──→ Phase 2.6 (Degraded Writes) — requires node health awareness
  │         ↑ benefits from memory pressure signals (2.8 D2-D3)
  │
  └──→ Phase 2.8 D2-D3-D1b (Resource Reservation, Pressure Monitoring, cgroups)
```

**Recommended order**: 2.8(A,B,D1) → 2.2 + 2.8(C,E) → 2.7a → 2.5 → 2.6 → 2.7b → 2.8(D2,D3,D1b)

Rationale:
- **2.8 A,B,D1 first**: Zero external dependencies. Fixes the silent QEMU crash problem immediately. OOM scores are 2 lines of code for massive protection. See [ec2-health-restart.md](ec2-health-restart.md) for details.
- **2.2 + 2.8 C,E together**: Node modes and health monitoring share the same state infrastructure. DescribeInstanceStatus API consumes the health data that 2.2's `monitorPeerHeartbeats()` and 2.8 C's QMP health check produce.
- **2.7a third**: Cold migration provides immediate value — VMs survive maintenance windows. Uses crash detection (2.8 A) for failure cases.
- **2.5 fourth**: Rolling upgrades become practical once drain + cold migration work.
- **2.6 fifth**: Degraded writes are important but require changes to Predastore (separate repo). Can be done in parallel with 2.7a.
- **2.7b sixth**: Live migration is the polish — sub-second downtime instead of seconds. Only worth the complexity after cold migration is proven.
- **2.8 D2,D3,D1b last**: Operational polish. Resource reservation and cgroups prevent OOM at the source but aren't blocking for other features.

---

## Key Existing Code to Reuse

| Function / Type | File | Relevance |
|----------------|------|-----------|
| `SendQMPCommand()` | `hive/daemon/daemon.go` | QMP dispatch — add migration commands |
| `Config.Execute()` | `hive/vm/vm.go` | QEMU command builder — add `-incoming` variant |
| `ResetNodeLocalState()` | `hive/vm/vm.go` | Zeros PID/PTS/QMP for cross-node handoff |
| `MountVolumes()` | `hive/daemon/daemon.go` | NBD volume setup — reuse on destination |
| `stopInstance()` | `hive/daemon/daemon.go` | QMP shutdown + cleanup — reuse for source cleanup |
| `collectResponses()` | `cmd/hive/cmd/get.go` | NATS fan-out pattern — reuse for node queries |
| `loadConfigAndConnect()` | `cmd/hive/cmd/get.go` | CLI NATS connection — reuse for new CLI commands |
| `utils.StopProcess()` | `hive/utils/utils.go` | PID-based service stop — reuse in drain/upgrade |
| `putObjectViaQUIC()` | `predastore/backend/distributed/distributed.go` | Write path — modify for shard redirection |
| `ObjectToShardNodes` | `predastore/backend/distributed/distributed.go` | Shard metadata — extend for actual placement |
| `hashRing.GetClosestN()` | `predastore/backend/distributed/distributed.go` | Node selection — extend to skip unavailable nodes |

---

## Verification Strategy

All phases will be verified with:
1. `make preflight` — format, vet, security, all unit tests
2. `make test-docker-multi` — multi-node E2E in Docker
3. Remote Proxmox cluster — real hardware validation via `scripts/iac/hive-test.sh`

The remote test cluster is critical for Phase 2.7b (live migration) since Docker E2E doesn't support nested KVM for QEMU-to-QEMU migration.
