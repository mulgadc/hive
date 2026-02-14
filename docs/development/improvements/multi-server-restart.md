# Multi-Server Cluster Restart

**Status: In Progress** — Phase 0 and Phase 1 complete. Phase 2 and 3 planned.

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

### Phase 0: Node capabilities and service topology — Complete

Foundation for everything else — the cluster must know what each node runs.

#### 0.1 Capabilities in config — Done

Added `Services []string` field to the `Config` struct (`config/config.go:29`) and two helper methods:
- `HasService(name string) bool` — checks if a service is in the list (or returns true for all if list is empty, for backward compat)
- `GetServices() []string` — returns the services list, defaulting to `AllServices` when empty

The `AllServices` package variable defines the canonical list: `["nats", "predastore", "viperblock", "daemon", "awsgw", "ui"]`.

The originally-planned role helpers (`IsComputeNode`, `IsStorageNode`, `IsInfraNode`) were deferred — `HasService()` is sufficient and more flexible.

Updated `NodeHealthResponse` to include `Services []string` and `ServiceHealth map[string]string`.

Unit tests added in `hive/config/config_test.go`: `TestHasService_ExplicitList`, `TestHasService_EmptyListDefaultsAll`, `TestHasService_UnknownService`, `TestGetServices_DefaultsToAll`, `TestGetServices_ExplicitList`.

**Files modified:**
- `hive/config/config.go` — `Services` field, `AllServices`, `HasService()`, `GetServices()`, updated `NodeHealthResponse`
- `hive/config/config_test.go` — 5 new tests

#### 0.2 Formation carries capabilities — Done

Added `Services []string` to `NodeInfo` in `hive/formation/formation.go`. Since `JoinRequest` embeds `NodeInfo`, it automatically gets the field.

Updated `BuildClusterRoutes()` to filter nodes by `"nats"` service — only nodes running NATS are included in cluster routes. Updated `BuildPredastoreNodes()` to filter by `"predastore"` service. Added `hasService()` helper with backward-compat behavior (empty services list = all services).

Unit tests added in `hive/formation/helpers_test.go`: `TestHasService_EmptyListMeansAll`, `TestHasService_ExplicitList`, `TestBuildClusterRoutes_FiltersNonNATSNodes`, `TestBuildClusterRoutes_EmptyServicesIncludesAll`, `TestBuildPredastoreNodes_FiltersByPredastore`, `TestBuildPredastoreNodes_EmptyServicesIncludesAll`, `TestBuildClusterRoutes_MixedServicesAndEmpty`.

**Files modified:**
- `hive/formation/formation.go` — `Services` field on `NodeInfo`
- `hive/formation/helpers.go` — `hasService()` helper, filtered `BuildClusterRoutes()` and `BuildPredastoreNodes()`
- `hive/formation/helpers_test.go` — 7 new tests

#### 0.3 Config template and CLI flags — Done

Added `Services []string` to `ConfigSettings` in `hive/admin/admin.go`.

Added `services` template rendering in `cmd/hive/cmd/templates/hive.toml` (after the `az` line).

Added `--services` StringSlice flag to both `adminInitCmd` and `adminJoinCmd` in `cmd/hive/cmd/admin.go`. The flag is read in `runAdminInit`, `runAdminInitMultiNode`, and `runAdminJoin`, and passed through to `ConfigSettings`.

**Files modified:**
- `hive/admin/admin.go` — `Services` field on `ConfigSettings`
- `cmd/hive/cmd/templates/hive.toml` — `services` template line
- `cmd/hive/cmd/admin.go` — `--services` flag on init/join, wiring to `ConfigSettings`

#### 0.4 Capability-aware start/stop scripts — Done

Added `parse_services()` and `has_service()` bash functions to both `scripts/start-dev.sh` and `scripts/stop-dev.sh`.

`parse_services()` reads the `services` line from `hive.toml` and defaults to all services if not set. All 6 service start/stop blocks are wrapped in `has_service` conditionals with skip messages for non-local services.

The stop script maps between process names and config service names (e.g., `hive` → `daemon`, `hive-ui` → `ui`).

**Files modified:**
- `scripts/start-dev.sh` — `parse_services()`, `has_service()`, conditional service startup
- `scripts/stop-dev.sh` — `parse_services()`, `has_service()` (with name mapping), conditional service stop

#### 0.5 Health endpoint and cluster state KV — Done

**Health endpoint**: Updated the `/health` HTTP handler in `daemon.go` to include `Services` (from config) and `ServiceHealth` (per-service status map). For local services, status is `"ok"`. For remote NATS dependency (when nats is not a local service), checks `natsConn.IsConnected()` and reports `"remote_ok"` or `"remote_unreachable"`.

**Cluster state KV bucket**: Added `hive-cluster-state` bucket alongside the existing `hive-instance-state` bucket. New `clusterKV` field on `JetStreamManager`. `InitClusterStateBucket()` creates the bucket with 1-hour TTL and history of 1. Called from `initJetStream()` after `InitKVBucket()`.

**Service manifest**: `WriteServiceManifest()` writes `node.{nodeID}.services` to the cluster state KV on daemon startup, containing the node's services, NATS host, predastore host, and timestamp.

`UpdateReplicas()` updated to also update the cluster-state bucket replicas alongside the instance-state bucket.

**Files modified:**
- `hive/daemon/daemon.go` — health endpoint with services/serviceHealth, `InitClusterStateBucket()` call in `initJetStream()`, `WriteServiceManifest()` call in `Start()`
- `hive/daemon/jetstream.go` — `ClusterStateBucket` const, `clusterKV` field, `InitClusterStateBucket()`, `WriteServiceManifest()`, updated `UpdateReplicas()`

### Phase 1: Self-recovery improvements (single-node correctness) — Complete

These changes fix the immediate restart bugs without requiring cross-node coordination.

#### 1.1 Cluster readiness gate — Done

Added `waitForClusterReady()` method to `Daemon`, called in `Start()` just before `restoreInstances()`. Uses a 2-minute max wait with 2-second polling interval.

Two checks run in sequence:
1. **Viperblock** — `checkViperblockReady()` verifies NATS connection is live. Initially implemented as a NATS request to `ebs.{node}.health`, but viperblock has no health NATS topic — this caused a 2-minute hang during E2E tests. Fixed to simply check `d.natsConn.IsConnected()` since viperblock runs alongside the daemon and subscribes to NATS.
2. **Predastore** — `checkPredastoreReady()` does a TCP dial to the predastore host:port from config with a 3-second timeout. Skipped if no predastore host is configured.

If all checks pass, recovery proceeds immediately. If the 2-minute timeout expires, recovery proceeds anyway with a warning log.

**Finding**: Viperblock does not expose a health endpoint via NATS. Any future viperblock health check should use TCP connectivity or an HTTP endpoint, not NATS request-reply.

**Files modified:**
- `hive/daemon/daemon.go` — `waitForClusterReady()`, `checkViperblockReady()`, `checkPredastoreReady()`, added `"net"` import

#### 1.2 Recovery throttling with semaphore — Done

Refactored `restoreInstances()` into two phases:

**Phase 1 (sequential)**: Iterates all instances from KV state. For each instance:
- Reconnects to still-running QEMU processes (checks PID file, instant, no I/O)
- Finalizes transitional states (stopping → stopped, shutting-down → terminated)
- Collects instances that need relaunching into a `toLaunch` list

**Phase 2 (throttled)**: Launches crashed VMs using a semaphore channel with `maxConcurrentRecovery = 2`. Each launch runs in a goroutine that acquires the semaphore before proceeding and releases on completion. A `sync.WaitGroup` ensures all launches complete before `restoreInstances()` returns.

This prevents thundering herd on storage during recovery while keeping the lightweight reconnect/finalize steps sequential.

**Files modified:**
- `hive/daemon/daemon.go` — refactored `restoreInstances()` with two-phase approach and semaphore

#### 1.3 Clean shutdown marker — Done

Three new methods on `JetStreamManager`:
- `WriteShutdownMarker(nodeID)` — writes `shutdown.{nodeID}` to `hive-cluster-state` KV with node name and timestamp
- `ReadShutdownMarker(nodeID)` — checks if a shutdown marker exists for the node
- `DeleteShutdownMarker(nodeID)` — deletes the marker (tolerates not-found)

**Write**: In `setupShutdown()`, the shutdown marker is written to cluster state KV before `WriteState()` persists instance state. This ensures the marker is present if the state write also succeeds.

**Read**: At the start of `restoreInstances()`, the marker is checked. If present: log "Clean shutdown marker found, trusting KV state", delete the marker, proceed normally. If absent: log a warning about possible crash recovery, sleep 3 seconds to let orphaned QEMU processes finish exiting, then proceed with careful PID validation.

**Files modified:**
- `hive/daemon/jetstream.go` — `WriteShutdownMarker()`, `ReadShutdownMarker()`, `DeleteShutdownMarker()`
- `hive/daemon/daemon.go` — marker write in `setupShutdown()`, marker check/delete in `restoreInstances()`

#### 1.4 Service manifest on startup — Done

Added `WriteServiceManifest()` to `JetStreamManager`, which writes `node.{nodeID}.services` to the cluster state KV containing the node's service list, NATS host, predastore host, and timestamp.

Called in `Start()` after `initJetStream()` succeeds. Failure is non-fatal (logged as warning).

**Files modified:**
- `hive/daemon/jetstream.go` — `WriteServiceManifest()`
- `hive/daemon/daemon.go` — call in `Start()`

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

## Files Modified (Phase 0 + 1)

| File | Changes | Phase |
|------|---------|-------|
| `hive/config/config.go` | Added `Services` field, `AllServices`, `HasService()`, `GetServices()`, updated `NodeHealthResponse` with `Services` and `ServiceHealth` | 0.1, 0.5 |
| `hive/config/config_test.go` | 5 new tests for `HasService` and `GetServices` | 0.1 |
| `hive/admin/admin.go` | Added `Services []string` to `ConfigSettings` | 0.3 |
| `cmd/hive/cmd/templates/hive.toml` | Added `services` template rendering | 0.3 |
| `cmd/hive/cmd/admin.go` | Added `--services` flag to init/join, wired to `ConfigSettings` | 0.3 |
| `hive/formation/formation.go` | Added `Services` to `NodeInfo` | 0.2 |
| `hive/formation/helpers.go` | Added `hasService()`, filtered `BuildClusterRoutes()` by nats, `BuildPredastoreNodes()` by predastore | 0.2 |
| `hive/formation/helpers_test.go` | 7 new tests for capability-filtered routing and predastore node building | 0.2 |
| `scripts/start-dev.sh` | Added `parse_services()`, `has_service()`, conditional service startup | 0.4 |
| `scripts/stop-dev.sh` | Added `parse_services()`, `has_service()` (with name mapping), conditional stop | 0.4 |
| `hive/daemon/daemon.go` | Updated health endpoint, `waitForClusterReady()`, `checkViperblockReady()`, `checkPredastoreReady()`, refactored `restoreInstances()` with semaphore, shutdown marker in `setupShutdown()`, service manifest write in `Start()` | 0.5, 1.1–1.4 |
| `hive/daemon/jetstream.go` | `ClusterStateBucket`, `clusterKV` field, `InitClusterStateBucket()`, `WriteShutdownMarker()`, `ReadShutdownMarker()`, `DeleteShutdownMarker()`, `WriteServiceManifest()`, updated `UpdateReplicas()` | 0.5, 1.3, 1.4 |

## Files to Modify (Phase 2 + 3)

| File | Changes |
|------|---------|
| `hive/daemon/jetstream.go` | Heartbeat, recovery lease, maintenance mode |
| `hive/daemon/recovery.go` | **New file** — recovery coordinator: leader election, capability-aware VM assignment, peer monitoring |
| `hive/daemon/daemon.go` | Heartbeat/recovery integration |
| `cmd/hive/cmd/admin.go` | `maintenance` and `status` subcommands |

## Implementation Order

Phase 0 is the prerequisite — all subsequent phases depend on nodes knowing their capabilities. Phase 1 fixes immediate restart bugs. Phases 2-3 add cross-node resilience and tooling.

**Phase 0** — node capabilities (foundation) — **Complete**:
1. [x] `Services` field in Config + helpers
2. [x] Formation carries capabilities
3. [x] Config template and CLI flags
4. [x] Capability-aware start/stop scripts
5. [x] Health endpoint includes capabilities + cluster state KV bucket + service manifest

**Phase 1** — self-recovery (fixes immediate restart bugs) — **Complete**:
6. [x] Cluster readiness gate
7. [x] Recovery throttling (semaphore in `restoreInstances`)
8. [x] Clean shutdown marker
9. [x] Service manifest on startup

**Phase 2** — cross-node failure recovery — **Planned**:
10. Heartbeat publishing (with capabilities)
11. Peer failure detection
12. Leader-elected recovery coordinator (capability-aware placement)

**Phase 3** — operational tooling — **Planned**:
13. Maintenance mode flag
14. Recovery status endpoints
15. Admin cluster status command

## Testing

### Phase 0 + 1 verification — Passed

| Check | Result |
|-------|--------|
| `make test` (all unit tests) | Passed |
| `make preflight` (format + vet + security + tests) | Passed |
| `make test-docker-single` (single-node E2E) | Passed — all 9 phases |
| `make test-docker-multi` (multi-node E2E, 3 nodes) | Passed — all phases including cluster health, cross-node operations |
| Backward compatibility (no `services` field in config) | Verified — defaults to all services |

**New unit tests added:**
- `hive/config/config_test.go` — `HasService` (explicit, empty/default, unknown), `GetServices` (default, explicit)
- `hive/formation/helpers_test.go` — `hasService` (empty=all, explicit), `BuildClusterRoutes` (filters by nats, empty=all, mixed), `BuildPredastoreNodes` (filters by predastore, empty=all)

**Bug found during E2E**: `checkViperblockReady()` initially sent a NATS request to `ebs.{node}.health`, but viperblock does not subscribe to any health NATS topic. This caused the daemon to block in `waitForClusterReady()` for the full 2-minute timeout on every startup. Fixed by checking `natsConn.IsConnected()` instead.

### Phase 2 + 3 tests (planned)

#### Unit tests
- Recovery coordinator: test leader election, capability-filtered VM assignment, epoch-based fencing
- Heartbeat: test staleness detection thresholds

#### Integration tests (multi-node Docker)
- Start 3-node cluster, launch 4 VMs, stop all, restart — verify all 4 VMs recover exactly once
- Start 3+1 cluster (3 full + 1 compute-only), verify compute-only connects to remote NATS/Predastore
- Kill one node (SIGKILL), verify surviving nodes recover its VMs after grace period
- Rolling restart: stop/start nodes one at a time, verify zero duplicate launches

#### Manual verification
- Deploy to 3-node test cluster (10.1.3.170-172), run stop/start cycle, check logs for race conditions
- Add a 4th compute-only node, verify it joins and accepts workloads

## Future Work

- **Persistent VM pinning**: Optional affinity rules to prefer restarting VMs on their original node
- **Live migration**: Move running VMs between nodes without stopping them (requires QEMU live migration support)
- **Automatic scaling**: Detect sustained capacity pressure and suggest adding nodes
- **Recovery metrics**: Prometheus-compatible metrics for recovery duration, failures, leader elections
- **Dynamic service reconfiguration**: Add/remove services from a running node without full restart
- **Storage-only node health**: Extend recovery to detect failed storage nodes and reroute Viperblock/Predastore traffic
