# Multi-Server Cluster Restart

**Status: Planned**

## Summary

When a multi-node Hive cluster restarts (stop-dev.sh then start-dev.sh on each node), every daemon independently loads the full instance state from NATS JetStream KV and attempts to relaunch all VMs simultaneously. This creates race conditions, duplicate launch attempts, and resource exhaustion. We need a coordinated recovery mechanism inspired by Ceph, Kubernetes, and etcd patterns.

Additionally, the cluster assumes every node runs all services (NATS, Predastore, Viperblock, Daemon, AWSGW, UI). In practice, nodes should be able to run a subset — e.g., a compute-only node running just Daemon + Viperblock that connects to remote NATS and Predastore. The config, startup scripts, health checks, and recovery logic all need to be capability-aware.

## Problem Statement

### Current behavior

Each daemon's `restoreInstances()` (`daemon.go:634-719`) runs independently on startup:

1. Loads `node.{nodeID}` from JetStream KV — gets its own previous instances
2. For each instance that was `StateRunning` but whose QEMU exited, resets to `StatePending` and calls `LaunchInstance()`
3. All instances launch simultaneously with no throttling
4. No coordination with other daemons — no awareness of cluster readiness

### Race conditions and failure modes

1. **Thundering herd on storage**: All nodes launch all their VMs at once. Each VM requires Viperblock volume mounts, NBD connections, and AMI downloads from Predastore. This saturates storage I/O before services are fully ready.

2. **JetStream quorum unavailable**: In a 3-node cluster with 3 replicas, JetStream needs 2 NATS servers for Raft quorum. If node1 starts restoring instances before node2's NATS is up, KV writes may fail or block. The current 10-attempt retry in `initJetStream()` (`daemon.go:574-615`) helps but doesn't guarantee other services (Predastore, Viperblock) are ready.

3. **No cluster-wide readiness gate**: Daemons start restoring VMs as soon as their own JetStream init succeeds, regardless of whether Predastore, Viperblock, or peer daemons are healthy. Volume mounts fail because Viperblock isn't ready; AMI fetches fail because Predastore hasn't elected a leader.

4. **No recovery throttling**: A node with 10 previously-running VMs will attempt to launch all 10 simultaneously — flooding QEMU starts, volume mounts, and metadata server allocations.

5. **Stopped instance coordination**: Stopped instances live in shared KV (`instance.{id}`) and can be started by any node via `ec2.start` with the `hive-workers` queue group. On restart, the `handleEC2StartStoppedInstance` handler (`daemon_handlers.go:1363-1463`) uses `Put` (not CAS) to claim instances, so two nodes could theoretically race on the same start request.

6. **No clean vs crash shutdown distinction**: The daemon writes state to KV during graceful shutdown (`setupShutdown()` at `daemon.go:1224-1228`), but there's no marker distinguishing a clean shutdown from a crash. After a crash, the KV state may be stale (last write before crash), and QEMU processes may be orphaned.

### Heterogeneous node problems

7. **All-or-nothing service startup**: `start-dev.sh` unconditionally starts all 6 services (NATS, Predastore, Viperblock, Daemon, AWSGW, UI) on every node. A compute-only node that connects to remote NATS/Predastore will fail because it tries to start local instances of services it doesn't need.

8. **No capability model in config**: `Config` struct (`config/config.go:22-41`) has no field indicating which services a node runs. The `hive.toml` template always generates all service sections. There's no way to express "this node runs daemon + viperblock, but uses remote NATS and Predastore."

9. **Health checks assume local services**: The daemon's `waitForClusterReady()` (proposed in Phase 1) must distinguish between "wait for local Viperblock to start" vs "wait for remote Predastore at 10.1.3.170:8443 to be reachable" — currently there's no way to know which is which.

10. **Formation and heartbeats lack capability info**: `formation.NodeInfo` and `NodeHealthResponse` have no capability/role fields. The recovery coordinator can't make capacity-aware decisions without knowing which nodes can run VMs vs which are storage-only.

## Research: Distributed Systems Patterns

### Ceph OSD restart patterns

- **Backfill reservation**: Limits concurrent recovery operations per OSD via `osd_max_backfills` (default: 1). Each OSD has local and remote reservers — a semaphore-based system that prevents thundering herd. Priority ordering ensures degraded/critical recovery happens first.
- **Recovery delay**: `osd_recovery_delay_start` introduces a deliberate delay after OSD boot before recovery begins, allowing the cluster to stabilize (peering, Raft leader election).
- **`noout` flag**: Distinguishes planned restart (don't rebalance) from permanent failure (rebalance after grace period). Set before maintenance, unset after.
- **Peering and epochs**: Before serving I/O, an OSD must verify its cluster map epoch is current via `up_thru`. Prevents stale decisions based on outdated topology.

### Kubernetes node restart patterns

- **Node lease heartbeats**: Kubelets maintain a `Lease` object with `renewTime`. If not renewed within `leaseDurationSeconds` (40s), the node controller marks the node `NotReady`.
- **Taint-based eviction timeline**: T+0 miss heartbeat → T+40s mark NotReady → T+340s (5min grace) evict pods. Deliberately slow to tolerate transient failures. When a node recovers, taints auto-clear but evicted pods stay on their new nodes.
- **Pod Disruption Budgets**: Limit concurrent disruptions cluster-wide, ensuring minimum availability during rolling restarts.
- **Learner pattern** (from etcd): New members join as non-voting learners, catch up on state, then get promoted to voters. Prevents a partially-synced member from affecting quorum.
- **Node labels and taints**: Kubernetes uses labels (not hard-coded types) to describe node capabilities. Scheduling matches workload requirements to node labels via affinity rules. A node can have multiple roles (control-plane + worker) or be specialized.

### etcd cluster recovery

- **Quorum requirement**: Needs `(N/2)+1` members for reads/writes. In a 3-node cluster, losing 2 nodes makes the cluster read-only or unavailable.
- **Raft log replay**: On restart, members replay their WAL before accepting requests. No client traffic until replay complete.
- **Learner promotion**: Members start as learners, sync data, then promote to full members. Safe and reversible.

### Consul/Nomad server vs client pattern

- **Two-tier architecture**: Server nodes participate in Raft consensus; client nodes run workloads and forward RPCs to servers. A client is configured with `retry_join` — a list of server addresses.
- **Role advertising via gossip tags**: Serf gossip carries per-node metadata (role, datacenter, port). Any node can inspect peers' capabilities without a central registry.
- **Service registration**: Each node's local agent registers only the services it runs. Health checks are per-service, not per-node.

### Key takeaways for Hive

| Pattern | Source | Application to Hive |
|---------|--------|---------------------|
| Recovery delay | Ceph | Wait for cluster readiness before restoring VMs |
| Concurrency limit | Ceph | Max N concurrent VM launches per node during recovery |
| Heartbeat leases | K8s | Daemons publish heartbeats to JetStream KV for peer monitoring |
| Grace period | K8s | Don't reschedule VMs from a down node for 5 minutes |
| Quorum gate | etcd | Don't begin recovery until JetStream has quorum |
| Fencing tokens | Kleppmann | Use KV revision-based CAS to prevent duplicate VM claims |
| Clean shutdown marker | General | Distinguish crash from graceful stop for recovery decisions |
| Node labels | K8s | Capabilities as config-driven labels, not hard-coded types |
| Static bootstrap + dynamic registration | Consul/Ceph | Config lists remote service addresses; node advertises its own capabilities at runtime |
| Per-service health checks | Consul | Only health-check what the node actually runs |

## Proposed Changes

### Phase 0: Node capabilities and service topology

Foundation for everything else — the cluster must know what each node runs.

#### 0.1 Capabilities in config

Add a `services` list to the per-node `Config` struct. This declares which services the node runs locally:

```toml
# hive.toml — full infrastructure node (node1, node2, node3)
[nodes.node1]
node = "node1"
services = ["nats", "predastore", "viperblock", "daemon", "awsgw", "ui"]
# ... existing daemon, nats, predastore, awsgw sections ...

# hive.toml — compute-only node (node4)
[nodes.node4]
node = "node4"
services = ["daemon", "viperblock"]
# nats/predastore sections still present but point to REMOTE hosts
[nodes.node4.nats]
host = "10.1.3.170:4222"    # remote NATS cluster
[nodes.node4.predastore]
host = "10.1.3.170:8443"    # remote Predastore
```

The `services` field is a string slice of service names. Valid values: `nats`, `predastore`, `viperblock`, `daemon`, `awsgw`, `ui`. When omitted (backward compat), defaults to all services — existing configs work unchanged.

**Config struct change** (`config/config.go`):

```go
type Config struct {
    Node     string   `mapstructure:"node"`
    Host     string   `mapstructure:"host"`
    Region   string   `mapstructure:"region"`
    AZ       string   `mapstructure:"az"`
    DataDir  string   `mapstructure:"data_dir"`
    Services []string `mapstructure:"services"` // NEW: which services this node runs locally
    // ... rest unchanged
}
```

Helper methods on Config:

```go
func (c Config) HasService(name string) bool    // check if service is in the list
func (c Config) IsComputeNode() bool             // has "daemon"
func (c Config) IsStorageNode() bool             // has "predastore" or "viperblock"
func (c Config) IsInfraNode() bool               // has "nats"
```

**Files:**
- `hive/config/config.go` — add `Services` field and helper methods
- `cmd/hive/cmd/templates/hive.toml` — add `services` to template
- `cmd/hive/cmd/admin.go` — add `--services` flag to `hive admin init` and `hive admin join`

#### 0.2 Formation carries capabilities

Extend `formation.NodeInfo` to include the services list, so the formation server knows what each node provides:

```go
type NodeInfo struct {
    Name      string   `json:"name"`
    BindIP    string   `json:"bind_ip"`
    ClusterIP string   `json:"cluster_ip"`
    Region    string   `json:"region"`
    AZ        string   `json:"az"`
    Port      int      `json:"port"`
    Services  []string `json:"services"` // NEW
}
```

This allows `BuildClusterRoutes()` to only include nodes with `"nats"` in their services list for NATS cluster routes, and `BuildPredastoreNodes()` to only include nodes with `"predastore"`.

**Files:**
- `hive/formation/formation.go` — add `Services` to `NodeInfo` and `JoinRequest`
- `hive/formation/helpers.go` — filter by capability in `BuildClusterRoutes()` and `BuildPredastoreNodes()`

#### 0.3 Capability-aware start/stop scripts

Refactor `start-dev.sh` and `stop-dev.sh` to read the `services` list from `hive.toml` and only start/stop configured services.

**Service dependency graph** (start order):

```
nats         → (none, starts first)
predastore   → nats
viperblock   → nats
daemon       → nats, viperblock
awsgw        → nats, daemon
ui           → awsgw
```

For a compute-only node (`services = ["daemon", "viperblock"]`):
1. Skip NATS (not local) — but verify remote NATS is reachable
2. Skip Predastore (not local) — but verify remote Predastore is reachable
3. Start Viperblock (local)
4. Start Daemon (local)
5. Skip AWSGW (not local)
6. Skip UI (not local)

**Implementation in start-dev.sh:**

```bash
# Read services from hive.toml
SERVICES=$(parse_services_from_config "$CONFIG_DIR/hive.toml")
# Defaults to "nats predastore viperblock daemon awsgw ui" if not set

has_service() { echo "$SERVICES" | grep -qw "$1"; }

# Start local services in dependency order
if has_service "nats"; then
    start_service "nats" "$NATS_CMD"
else
    # Verify remote NATS is reachable
    NATS_HOST=$(parse_nats_host "$CONFIG_DIR/hive.toml")
    check_service "NATS (remote)" "${NATS_HOST%%:*}" "${NATS_HOST##*:}"
fi

if has_service "predastore"; then
    start_service "predastore" "$PREDASTORE_CMD"
else
    # Verify remote Predastore is reachable
    check_service "Predastore (remote)" "$PREDASTORE_HOST" "$PREDASTORE_PORT"
fi

# ... etc for each service
```

**stop-dev.sh** — reverse logic: only stop services that were started locally. Currently it calls `./bin/hive service <name> stop` for all services, which fails silently for services that aren't running — but it's noisy and slow.

**Files:**
- `scripts/start-dev.sh` — add config parsing, conditional service startup, remote dependency checks
- `scripts/stop-dev.sh` — add config parsing, only stop local services

#### 0.4 Health endpoint includes capabilities

Extend `NodeHealthResponse` to advertise what the node runs:

```go
type NodeHealthResponse struct {
    Node       string            `json:"node"`
    Status     string            `json:"status"`
    ConfigHash string            `json:"config_hash"`
    Epoch      uint64            `json:"epoch"`
    Uptime     int64             `json:"uptime"`
    Services   []string          `json:"services"`      // NEW: what this node runs
    ServiceHealth map[string]string `json:"service_health"` // NEW: per-service status
}
```

The `ServiceHealth` map only includes services this node runs:

```json
{
  "node": "node4",
  "status": "running",
  "services": ["daemon", "viperblock"],
  "service_health": {
    "daemon": "ok",
    "viperblock": "ok",
    "nats": "remote_ok",
    "predastore": "remote_ok"
  }
}
```

For local services, the daemon checks the process is running. For remote dependencies (NATS, Predastore), it checks connectivity. This gives operators and the recovery coordinator a complete picture of node health.

**Files:**
- `hive/config/config.go` — update `NodeHealthResponse` struct
- `hive/daemon/daemon.go` — update health handler to include capabilities and per-service health
- `hive/daemon/daemon_handlers.go` — update NATS health handler similarly

#### 0.5 Cluster service map in JetStream KV

To support runtime service discovery (not just config-time), each daemon writes its node's service manifest to JetStream KV on startup:

```
Bucket:  hive-cluster-state (new, separate from hive-instance-state)
Key:     node.{nodeID}.services
Value:   {"node": "node4", "services": ["daemon", "viperblock"], "nats_host": "10.1.3.170:4222", "predastore_host": "10.1.3.170:8443"}
TTL:     1 hour (auto-cleanup of dead nodes)
History: 1
```

This enables:
- Recovery coordinator to find which nodes can accept VMs (nodes with `"daemon"` service)
- Any node to discover where NATS/Predastore are running
- Operators to query cluster topology via `hive admin status`

**Design decisions on KV buckets:**

| Bucket | Purpose | Replicas | History | TTL |
|--------|---------|----------|---------|-----|
| `hive-instance-state` | VM state, stopped instances | cluster size | 1 | none |
| `hive-cluster-state` | Heartbeats, service maps, shutdown markers, recovery leases | cluster size | 1 | 1 hour |

Separating the buckets keeps ephemeral data (heartbeats, leases) from durable data (instance state). The 1-hour TTL on `hive-cluster-state` auto-cleans dead node heartbeats and stale service maps without manual intervention. At 100 nodes this bucket holds ~100 keys of ~300 bytes each — trivially small. The TTL prevents unbounded growth from nodes that leave the cluster permanently.

**Files:**
- `hive/daemon/jetstream.go` — new `InitClusterStateBucket()`, `WriteServiceManifest()`, `ListServiceManifests()`

### Phase 1: Self-recovery improvements (single-node correctness)

These changes fix the immediate restart bugs without requiring cross-node coordination.

#### 1.1 Cluster readiness gate (capability-aware)

Before `restoreInstances()`, add a readiness check that waits for dependent services. The check varies based on the node's capabilities:

**Full infrastructure node** (`services = ["nats", "predastore", "viperblock", "daemon", ...]`):
```
NATS connected (already done)
  → JetStream KV initialized (already done)
    → Local Viperblock healthy (health check with retry)
      → Local Predastore healthy (health check with retry)
        → Peer daemons discoverable (at least N-1 NATS routes connected)
          → Begin recovery
```

**Compute-only node** (`services = ["daemon", "viperblock"]`):
```
Remote NATS reachable (TCP connect with retry)
  → JetStream KV initialized (already done — connects to remote NATS)
    → Local Viperblock healthy (health check with retry)
      → Remote Predastore reachable (TCP connect with retry)
        → Begin recovery
```

The `waitForClusterReady()` method reads the node's `services` list and builds its dependency checks accordingly. Local services get health endpoint checks; remote services get TCP connectivity checks.

**Files:**
- `hive/daemon/daemon.go` — new `waitForClusterReady()`, called from `Start()` between lines 523-547

#### 1.2 Recovery throttling with semaphore

Replace the unbounded loop in `restoreInstances()` with a worker pool. Limit concurrent VM launches to `maxConcurrentRecovery` (default: 2 per node).

Recovery order priority:
1. Reconnect to still-running QEMU processes (instant, no I/O)
2. Finalize transitional states (stopping → stopped, shutting-down → terminated)
3. Relaunch crashed VMs (pending/running with dead QEMU) — these hit storage

**Files:**
- `hive/daemon/daemon.go` — refactor `restoreInstances()` to use a semaphore for step 3

#### 1.3 Clean shutdown marker

On graceful shutdown, write a `shutdown.{nodeID}` key to `hive-cluster-state` KV with timestamp and instance count. On startup, check for this marker:

- **Marker present**: Clean shutdown. Trust KV state. Delete marker and proceed.
- **Marker absent**: Crash or power loss. Log a warning, do extra PID file validation, wait slightly longer for QEMU processes to potentially still be exiting.

**Files:**
- `hive/daemon/jetstream.go` — new `WriteShutdownMarker()` / `ReadShutdownMarker()` / `DeleteShutdownMarker()`
- `hive/daemon/daemon.go` — write marker in `setupShutdown()`, read/delete in `restoreInstances()`

### Phase 2: Cross-node coordination

These changes enable daemons to coordinate recovery of failed nodes' VMs.

#### 2.1 Heartbeat publishing

Each daemon publishes a heartbeat to `hive-cluster-state` KV every 10 seconds:

```
Key:   heartbeat.{nodeID}
Value: {"node": "node1", "epoch": 1, "timestamp": "...", "services": ["daemon", "viperblock", ...], "vm_count": 4, "vcpu_avail": 8, "mem_avail_gb": 16.0}
```

Use `kv.Put()` — no need for CAS since only the owning daemon writes its own heartbeat. Other daemons use `kv.Watch("heartbeat.*")` for push-based notification of changes — no polling. The 1-hour TTL on the bucket auto-cleans dead node heartbeats.

Heartbeat publishing starts after self-recovery completes (after `restoreInstances()` returns), so the heartbeat reflects accurate resource state.

**Files:**
- `hive/daemon/jetstream.go` — new `WriteHeartbeat()` / `WatchHeartbeats()`
- `hive/daemon/daemon.go` — start heartbeat goroutine after `restoreInstances()`

#### 2.2 Peer failure detection

After self-recovery and heartbeat publishing begin, each daemon monitors peer heartbeats:

- **Stale threshold**: 60 seconds (6 missed heartbeats). Mark peer as `suspect`.
- **Failed threshold**: 300 seconds (5 minutes). Mark peer as `failed` — eligible for VM recovery.

These thresholds are deliberately conservative (matching Kubernetes' approach) to avoid false positives during rolling restarts where nodes come back quickly.

**Files:**
- `hive/daemon/daemon.go` — new `monitorPeerHealth()` goroutine, started alongside heartbeat publisher

#### 2.3 Leader-elected recovery coordinator (capability-aware)

When a peer is detected as `failed`, surviving daemons elect a recovery leader using JetStream KV CAS:

```
Key:   leader.recovery
Value: {"node": "node2", "epoch": 42, "acquired": "..."}
```

Acquisition uses `kv.Create()` (create-if-not-exists) for atomic leader election. The leader holds the lease for 60 seconds and must renew it. If the leader fails, another daemon acquires it after the lease expires.

The recovery leader:
1. Reads the failed node's state from `node.{failedNode}` in `hive-instance-state` KV
2. Filters to VMs that were `StateRunning` or `StatePending`
3. Assigns VMs to surviving **compute-capable** nodes (those with `"daemon"` in their services, from heartbeat data) based on resource availability
4. Writes recovery assignments to KV: `recovery.{instanceID} = {"target_node": "node2", "epoch": 42}`
5. Each target daemon watches for its assignments and launches them

**Capability-aware placement**: The leader only assigns VMs to nodes whose heartbeats include `"daemon"` in their `services` list and have sufficient `vcpu_avail` and `mem_avail_gb`. Storage-only nodes are never assigned VMs.

**Fencing**: Each assignment includes the recovery epoch. Before launching, the target daemon verifies the epoch is still current via CAS. If another leader took over with a higher epoch, the stale assignment is ignored.

**Files:**
- `hive/daemon/jetstream.go` — new `AcquireRecoveryLease()` / `RenewRecoveryLease()` / `WriteRecoveryAssignment()` / `WatchRecoveryAssignments()`
- `hive/daemon/recovery.go` — new file, recovery coordinator logic
- `hive/daemon/daemon.go` — integrate recovery coordinator startup

### Phase 3: Operational tooling

#### 3.1 Maintenance mode flag

Add a `maintenance.{nodeID}` KV key that daemons can set before planned restarts:

```bash
./bin/hive admin maintenance enable --node node1
```

When maintenance mode is set for a node:
- Peer daemons skip the 5-minute failure detection grace period for that node (they know it's planned)
- The node itself skips VM recovery on restart if the maintenance flag is still set (operator controls when VMs come back)
- Analogous to Ceph's `noout` flag

**Files:**
- `hive/daemon/jetstream.go` — new `SetMaintenanceMode()` / `ClearMaintenanceMode()` / `IsMaintenanceMode()`
- `cmd/hive/cmd/admin.go` — new `maintenance` subcommand

#### 3.2 Recovery status visibility

Add admin endpoints and NATS subjects for recovery observability:

- `GET /admin/recovery/status` — shows recovery state, assignments, peer health
- `GET /admin/cluster/topology` — shows all nodes, their capabilities, and service health
- `hive.admin.recovery.status` NATS subject — cluster-wide recovery state query

**Files:**
- `hive/daemon/daemon.go` — new cluster manager routes

#### 3.3 Admin cluster status command

Add a CLI command for operators to inspect the cluster topology:

```bash
$ ./bin/hive admin status
Cluster: hive (epoch 3)

NODE     SERVICES                                    STATUS   VMS  vCPU(free)  MEM(free)
node1    nats,predastore,viperblock,daemon,awsgw,ui  healthy  4    8/16        12.0/32.0
node2    nats,predastore,viperblock,daemon,awsgw     healthy  3    10/16       16.0/32.0
node3    nats,predastore,viperblock,daemon            healthy  2    12/16       20.0/32.0
node4    viperblock,daemon                            healthy  6    2/8         4.0/16.0
```

This reads from the `hive-cluster-state` KV bucket (heartbeats + service manifests) and fans out health checks via NATS.

**Files:**
- `cmd/hive/cmd/admin.go` — new `status` subcommand

## Files to Modify

| File | Changes |
|------|---------|
| `hive/config/config.go` | Add `Services` field to `Config`, helper methods (`HasService`, etc.), update `NodeHealthResponse` |
| `cmd/hive/cmd/templates/hive.toml` | Add `services` field to template |
| `cmd/hive/cmd/admin.go` | Add `--services` flag to init/join, add `maintenance` and `status` subcommands |
| `hive/formation/formation.go` | Add `Services` to `NodeInfo` |
| `hive/formation/helpers.go` | Filter `BuildClusterRoutes` and `BuildPredastoreNodes` by capability |
| `scripts/start-dev.sh` | Read services from config, conditional service startup, remote dependency checks |
| `scripts/stop-dev.sh` | Read services from config, only stop local services |
| `hive/daemon/daemon.go` | `waitForClusterReady()` (capability-aware), refactor `restoreInstances()` with semaphore, heartbeat/recovery integration |
| `hive/daemon/jetstream.go` | New `hive-cluster-state` bucket, shutdown marker, heartbeat, recovery lease, service manifest, maintenance mode |
| `hive/daemon/recovery.go` | **New file** — recovery coordinator: leader election, capability-aware VM assignment, peer monitoring |
| `hive/daemon/state.go` | Minor — shutdown marker integration with state transitions |
| `hive/daemon/daemon_handlers.go` | Update NATS health handler with capabilities and per-service health |

## Implementation Order

Phase 0 is the prerequisite — all subsequent phases depend on nodes knowing their capabilities. Phase 1 fixes immediate restart bugs. Phases 2-3 add cross-node resilience and tooling.

**Phase 0** — node capabilities (foundation):
1. `Services` field in Config + helpers
2. Formation carries capabilities
3. Capability-aware start/stop scripts
4. Health endpoint includes capabilities
5. Cluster service map in JetStream KV

**Phase 1** — self-recovery (fixes immediate restart bugs):
6. Cluster readiness gate (capability-aware)
7. Recovery throttling (semaphore in `restoreInstances`)
8. Clean shutdown marker

**Phase 2** — cross-node failure recovery:
9. Heartbeat publishing (with capabilities)
10. Peer failure detection
11. Leader-elected recovery coordinator (capability-aware placement)

**Phase 3** — operational tooling:
12. Maintenance mode flag
13. Recovery status endpoints
14. Admin cluster status command

## Testing

### Unit tests
- Config `HasService()` helper methods with various service lists
- Config defaults to all services when `Services` is empty (backward compat)
- `waitForClusterReady()` with different capability configurations
- `restoreInstances()` with mocked JetStream: verify semaphore limits concurrent launches
- Shutdown marker: write on shutdown, verify presence on clean restart, verify absence simulates crash
- Recovery coordinator: test leader election, capability-filtered VM assignment, epoch-based fencing
- Heartbeat: test staleness detection thresholds

### Integration tests (multi-node Docker)
- Start 3-node cluster (all services), launch 4 VMs, stop all nodes, restart — verify all 4 VMs recover exactly once
- Start 3+1 cluster (3 full + 1 compute-only), verify compute-only node connects to remote NATS/Predastore and launches VMs
- Kill one node (SIGKILL), verify surviving nodes eventually recover its VMs after grace period, respecting capabilities
- `start-dev.sh` on a compute-only node: verify it skips NATS/Predastore startup but checks remote connectivity
- Rolling restart: stop/start nodes one at a time, verify zero duplicate launches

### Manual verification
- `make test` passes after each phase
- Deploy to 3-node test cluster (10.1.3.170-172), run stop/start cycle, check `~/hive/logs/` for race conditions
- Add a 4th compute-only node, verify it joins and accepts workloads

## Future Work

- **Persistent VM pinning**: Optional affinity rules to prefer restarting VMs on their original node
- **Live migration**: Move running VMs between nodes without stopping them (requires QEMU live migration support)
- **Automatic scaling**: Detect sustained capacity pressure and suggest adding nodes
- **Recovery metrics**: Prometheus-compatible metrics for recovery duration, failures, leader elections
- **Dynamic service reconfiguration**: Add/remove services from a running node without full restart
- **Storage-only node health**: Extend recovery to detect failed storage nodes and reroute Viperblock/Predastore traffic
