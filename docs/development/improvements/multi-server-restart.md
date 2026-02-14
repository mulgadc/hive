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

These changes enable daemons to coordinate lifecycle events — both failure recovery and graceful cluster-wide shutdown.

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

#### 2.4 Coordinated cluster shutdown

##### Problem

The current `stop-dev.sh` operates per-node: each node independently shuts down its own services in reverse startup order. In a multi-node cluster, this creates ordering hazards:

1. **Storage pulled from under running VMs**: If node1 stops Predastore before node2's VMs have finished flushing writes through Viperblock, data can be lost or corrupted.
2. **Cascade failures during rolling stop**: Stopping NATS on one node can break JetStream quorum before other nodes have persisted their state.
3. **Manual coordination required**: An operator must SSH into each node and run `stop-dev.sh` in the right order — infrastructure nodes last, compute nodes first. This is error-prone and doesn't scale.
4. **No cluster-wide drain**: There's no way to stop accepting new API requests across all gateways simultaneously. A request arriving at node2's AWSGW during node1's shutdown could try to launch a VM on a node that's mid-teardown.
5. **Orphaned nbdkit processes**: If a daemon exits before cleanly unmounting all volumes, nbdkit processes may be left running with no parent to clean them up. The current `stop-dev.sh` handles this via `pgrep`/`pkill` but only for the local node.

##### Design inspiration

The coordinated shutdown draws from several distributed systems patterns:

| Source | Pattern | Application to Hive |
|--------|---------|---------------------|
| Ceph | Pre-shutdown flags (`noout`, `norecover`, `nobackfill`) set before maintenance; monitors shut down last | Write cluster shutdown marker before any service stops; NATS shuts down last |
| Ceph | Ordered daemon shutdown: RGW → MDS → OSD → MON | AWSGW → Daemon/VMs → Viperblock → Predastore → NATS |
| Kubernetes | Graceful node shutdown (KEP-2000): kubelet uses systemd inhibitor locks, pods sorted by priority class, `terminationGracePeriodSeconds` per pod | Per-VM QMP `system_powerdown` with configurable timeout, force kill after grace period |
| Kubernetes | Pod termination lifecycle: preStop hook → SIGTERM → grace period → SIGKILL | Phase-based shutdown with ACK between phases; no phase proceeds until prior phase completes |
| Kubernetes | `kubectl drain` cordons a node (marks unschedulable) before evicting pods | GATE phase stops AWSGW to prevent new work before draining VMs |
| Rancher/Harvester | Ordered HCI shutdown: cordon node → migrate VMs → drain workloads → power off; fleet-managed lifecycle for multi-cluster | Coordinator-driven phased shutdown across all nodes; progress tracking via NATS |
| RKE2 | `rke2-killall.sh` handles both graceful and forceful modes with explicit cleanup of iptables, mounts, and network namespaces | `--force` flag for immediate shutdown; explicit nbdkit/QEMU cleanup in DRAIN phase |
| etcd | Learner removal before shutdown; leader transfer to minimize disruption | Coordinator is last to stop NATS; non-coordinator nodes stop NATS first |

##### Command

```bash
# Graceful shutdown — waits for VMs to power down cleanly
./bin/hive admin cluster shutdown

# Force shutdown — SIGKILL VMs immediately, skip grace periods
./bin/hive admin cluster shutdown --force

# Custom timeout for VM drain phase (default: 120s)
./bin/hive admin cluster shutdown --timeout 300s

# Dry run — show what would happen without executing
./bin/hive admin cluster shutdown --dry-run
```

The command can be run from **any node** in the cluster. The issuing node becomes the shutdown coordinator. If the coordinator crashes mid-shutdown, any surviving node can detect the `cluster.shutdown` KV marker and resume coordination (similar to the recovery leader election pattern from 2.3).

##### Shutdown protocol

The protocol uses five phases, each gated by acknowledgements from all participating nodes. The coordinator drives transitions and aggregates progress. NATS request-reply ensures reliable delivery with timeouts per phase.

```
Phase 1: GATE ──────► Phase 2: DRAIN ──────► Phase 3: STORAGE ──────► Phase 4: PERSIST ──────► Phase 5: INFRA
(stop new work)        (stop VMs)             (stop block I/O)         (stop object storage)    (stop NATS)
```

**Phase 1: GATE** — stop accepting new work

Purpose: Ensure no new API requests enter the system while shutdown proceeds. Analogous to Ceph's `noout`/`norecover` flags and Kubernetes' node cordon.

1. Coordinator writes `cluster.shutdown` marker to `hive-cluster-state` KV:
   ```
   Key:   cluster.shutdown
   Value: {"initiator": "node1", "phase": "gate", "started": "2024-...", "timeout": "120s", "force": false}
   ```
2. Coordinator publishes `hive.cluster.shutdown.gate` (NATS request, expecting N replies)
3. Each node on receiving the gate request:
   - Stops AWSGW service (no new API requests accepted)
   - Stops hive-ui service
   - Sets internal `shuttingDown` flag to reject any in-flight NATS work requests
   - ACKs to coordinator: `{"node": "node2", "phase": "gate", "stopped": ["awsgw", "ui"]}`
4. Coordinator waits for all ACKs (timeout: 30s per node)
5. On timeout: log which nodes didn't respond, proceed anyway (they may be already down)

**Phase 2: DRAIN** — gracefully terminate all VMs

Purpose: Ensure all VMs are cleanly shut down and their volumes unmounted before storage services stop. This is the critical data-safety phase. Mirrors Kubernetes' pod termination lifecycle: preStop → SIGTERM → grace period → SIGKILL.

1. Coordinator publishes `hive.cluster.shutdown.drain`
2. Each daemon node:
   - Sends QMP `system_powerdown` to each running QEMU instance (same as `setupShutdown()` today)
   - Waits for QEMU processes to exit, polling PID files
   - Reports progress via `hive.cluster.shutdown.progress`:
     ```json
     {"node": "node2", "phase": "drain", "total": 4, "remaining": 2, "vms": ["i-abc123", "i-def456"]}
     ```
   - After per-VM grace period (default 60s), sends SIGTERM then SIGKILL to stubborn QEMU processes
   - Unmounts all EBS volumes via `ebs.unmount` NATS requests (viperblock still running)
   - Cleans up any orphaned nbdkit processes attached to terminated VMs
   - Writes instance state to JetStream KV (same as `WriteState()` in `setupShutdown()`)
   - Writes per-node shutdown marker (reuses Phase 1's `WriteShutdownMarker()`)
   - ACKs when all VMs have exited and volumes are unmounted
3. Coordinator aggregates progress, prints live status
4. `--force` mode: skip QMP `system_powerdown`, immediately SIGKILL all QEMU processes
5. On timeout: coordinator logs remaining VMs, force-kills them, proceeds

**Phase 3: STORAGE** — stop block storage

Purpose: Shut down viperblock and clean up all nbdkit processes. Safe to do now because all VMs are terminated and volumes unmounted.

1. Coordinator publishes `hive.cluster.shutdown.storage`
2. Each node with viperblock service:
   - Stops viperblock service
   - Scans for and kills any orphaned nbdkit processes (`pkill -f nbdkit`)
   - Verifies no NBD block devices remain active
   - ACKs when complete
3. Nodes without viperblock: ACK immediately
4. Coordinator waits for all ACKs (timeout: 30s)

**Phase 4: PERSIST** — stop object storage

Purpose: Shut down Predastore cleanly. Safe because no viperblock or AWSGW requests remain. Predastore may still have internal replication traffic between nodes — the coordinator ensures all nodes stop together.

1. Coordinator publishes `hive.cluster.shutdown.persist`
2. Each node with predastore service:
   - Stops predastore service
   - ACKs when complete
3. Coordinator waits for all ACKs (timeout: 30s)

**Phase 5: INFRA** — stop NATS (coordinator last)

Purpose: Shut down the coordination layer itself. This is fire-and-forget because the communication channel is being torn down.

1. Coordinator publishes `hive.cluster.shutdown.infra` to all **non-coordinator** nodes
2. Non-coordinator nodes: stop local NATS and exit the shutdown handler
3. Coordinator waits 5s for NATS cluster to stabilize with fewer members
4. Coordinator stops its own NATS
5. Coordinator logs "Cluster shutdown complete" and exits

##### Coordinator election and failure handling

The coordinator is simply the node that runs the `hive admin cluster shutdown` command. If the coordinator crashes mid-shutdown:

1. The `cluster.shutdown` KV marker persists (1-hour TTL, long enough for any shutdown)
2. The marker records the current phase and which nodes have ACKed
3. Any surviving daemon detects the marker via `kv.Watch("cluster.shutdown")`
4. After a coordinator timeout (30s without progress updates), a surviving daemon takes over using the same CAS pattern as recovery leader election (2.3)
5. The new coordinator resumes from the last completed phase

If a node is unreachable during any phase:
- Coordinator logs a warning and continues after the per-phase timeout
- Unreachable nodes may have already shut down (e.g., power loss)
- If they come back later, they'll see the `cluster.shutdown` marker and refuse to start services until the marker is cleared

##### Status reporting

The coordinator prints live progress to stdout:

```
Cluster shutdown initiated from node1 (3 nodes)

Phase 1/5: GATE — Stopping API gateways and UI...
  node1: awsgw stopped, ui stopped               [ok]
  node2: awsgw stopped, ui stopped               [ok]
  node3: awsgw stopped, ui stopped               [ok]

Phase 2/5: DRAIN — Stopping virtual machines...
  node1: 4/4 VMs stopped                         [ok]     12.3s
  node2: 2/2 VMs stopped                         [ok]      8.7s
  node3: 1/1 VMs stopped                         [ok]      6.1s

Phase 3/5: STORAGE — Stopping block storage...
  node1: viperblock stopped, 0 nbdkit cleaned    [ok]
  node2: viperblock stopped, 0 nbdkit cleaned    [ok]
  node3: viperblock stopped, 0 nbdkit cleaned    [ok]

Phase 4/5: PERSIST — Stopping object storage...
  node1: predastore stopped                      [ok]
  node2: predastore stopped                      [ok]
  node3: predastore stopped                      [ok]

Phase 5/5: INFRA — Stopping NATS...
  node2: nats stopped
  node3: nats stopped
  node1: nats stopped (coordinator, last)

Cluster shutdown complete (47.3s)
```

##### NATS topics

| Topic | Pattern | Purpose |
|-------|---------|---------|
| `hive.cluster.shutdown.gate` | Request-Reply | Phase 1: stop AWSGW + UI, await ACK |
| `hive.cluster.shutdown.drain` | Request-Reply | Phase 2: stop VMs, await ACK |
| `hive.cluster.shutdown.progress` | Publish (broadcast) | Progress updates during drain (VM counts) |
| `hive.cluster.shutdown.storage` | Request-Reply | Phase 3: stop viperblock + nbdkit, await ACK |
| `hive.cluster.shutdown.persist` | Request-Reply | Phase 4: stop predastore, await ACK |
| `hive.cluster.shutdown.infra` | Publish (fire-and-forget) | Phase 5: stop NATS on non-coordinator nodes |

##### Cluster shutdown KV state

```
Bucket:  hive-cluster-state
Key:     cluster.shutdown
Value:   {
    "initiator": "node1",
    "phase": "drain",
    "started": "2024-01-15T10:30:00Z",
    "timeout": "120s",
    "force": false,
    "nodes_total": 3,
    "nodes_acked": {
        "node1": {"phase": "gate", "at": "..."},
        "node2": {"phase": "gate", "at": "..."},
        "node3": {"phase": "gate", "at": "..."}
    }
}
```

##### Integration with existing code

- **Reuses `stopInstance()`**: The daemon's existing `stopInstance()` function handles QMP shutdown, PID polling, volume unmount, and force kill. The DRAIN phase calls this same code path rather than reimplementing it.
- **Reuses `WriteShutdownMarker()`**: Each node writes its per-node shutdown marker during DRAIN, so on next startup, `restoreInstances()` sees a clean shutdown.
- **Reuses `./bin/hive service <name> stop`**: For stopping AWSGW, UI, viperblock, predastore, and NATS, the coordinator tells each daemon to invoke the same service stop mechanism used by `stop-dev.sh`.
- **`setupShutdown()` becomes cluster-aware**: When a daemon receives a cluster shutdown request, it skips independent shutdown and instead participates in the coordinated protocol. If a SIGTERM arrives while a coordinated shutdown is in progress, the daemon ACKs the current phase and exits.
- **`stop-dev.sh` remains for single-node dev**: The script continues to work for local development. For multi-node clusters, `hive admin cluster shutdown` is the preferred approach.

##### Comparison with per-node `stop-dev.sh`

| Aspect | `stop-dev.sh` (current) | `hive admin cluster shutdown` (proposed) |
|--------|------------------------|------------------------------------------|
| Scope | Single node | Entire cluster from any node |
| Ordering | Per-node reverse order | Cluster-wide phased ordering |
| VM safety | Daemon exits, then waits for QEMU via `pgrep` | Daemon actively monitors each VM's QMP shutdown |
| Storage safety | Hopes VMs exited before predastore stop | Guarantees all VMs stopped before storage phases |
| nbdkit cleanup | `pgrep`/`pkill` after the fact | Explicit unmount + cleanup during DRAIN |
| Progress | Per-service echo messages | Cluster-wide live progress with VM counts |
| Failure handling | None — operator must check each node | Automatic coordinator failover, timeout handling |
| NATS safety | Stops NATS whenever it gets there | NATS is always last, coordinator node last of all |

**Files:**
- `hive/daemon/daemon.go` — shutdown handler that participates in coordinated protocol, `shuttingDown` flag to reject new work
- `hive/daemon/shutdown.go` — **new file**, coordinator logic: phase management, ACK aggregation, progress reporting, coordinator failover
- `hive/daemon/jetstream.go` — `WriteClusterShutdown()` / `ReadClusterShutdown()` / `DeleteClusterShutdown()` / `WatchClusterShutdown()`
- `cmd/hive/cmd/admin.go` — new `cluster shutdown` subcommand with `--force`, `--timeout`, `--dry-run` flags
- `hive/daemon/daemon_handlers.go` — NATS handlers for `hive.cluster.shutdown.*` topics

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
- Works with coordinated shutdown: the coordinator sets maintenance mode on all nodes before Phase 1, clears it after Phase 5

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

| File | Changes | Phase |
|------|---------|-------|
| `hive/daemon/jetstream.go` | Heartbeat, recovery lease, cluster shutdown KV, maintenance mode | 2.1–2.4, 3.1 |
| `hive/daemon/recovery.go` | **New file** — recovery coordinator: leader election, capability-aware VM assignment, peer monitoring | 2.3 |
| `hive/daemon/shutdown.go` | **New file** — coordinated shutdown coordinator: phase management, ACK aggregation, progress reporting, failover | 2.4 |
| `hive/daemon/daemon.go` | Heartbeat/recovery integration, cluster shutdown handler, `shuttingDown` flag | 2.1–2.4 |
| `hive/daemon/daemon_handlers.go` | NATS handlers for `hive.cluster.shutdown.*` topics | 2.4 |
| `cmd/hive/cmd/admin.go` | `cluster shutdown`, `maintenance`, `status` subcommands | 2.4, 3.1, 3.3 |

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

**Phase 2** — cross-node coordination — **Planned**:
10. Heartbeat publishing (with capabilities)
11. Peer failure detection
12. Leader-elected recovery coordinator (capability-aware placement)
13. Coordinated cluster shutdown (`hive admin cluster shutdown`)

**Phase 3** — operational tooling — **Planned**:
14. Maintenance mode flag
15. Recovery status endpoints
16. Admin cluster status command

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
- Coordinated shutdown: test phase transitions, ACK aggregation, timeout handling, coordinator failover
- Shutdown protocol: test `cluster.shutdown` KV marker lifecycle (write, read, resume, clear)

#### Integration tests (multi-node Docker)
- Start 3-node cluster, launch 4 VMs, stop all, restart — verify all 4 VMs recover exactly once
- Start 3+1 cluster (3 full + 1 compute-only), verify compute-only connects to remote NATS/Predastore
- Kill one node (SIGKILL), verify surviving nodes recover its VMs after grace period
- Rolling restart: stop/start nodes one at a time, verify zero duplicate launches
- Coordinated shutdown: launch VMs across 3 nodes, run `hive admin cluster shutdown` from node2, verify phase ordering (AWSGW stops before VMs, VMs stop before viperblock, viperblock before predastore, NATS last)
- Coordinated shutdown with `--force`: verify VMs are killed immediately, no grace period
- Shutdown coordinator failure: start cluster shutdown from node1, kill node1 mid-drain, verify another node resumes coordination
- Post-shutdown restart: after coordinated shutdown, restart all nodes, verify clean shutdown markers present and VMs restore correctly
- No orphaned processes: after coordinated shutdown, verify zero remaining QEMU/nbdkit processes on all nodes

#### Manual verification
- Deploy to 3-node test cluster (10.1.3.170-172), run stop/start cycle, check logs for race conditions
- Add a 4th compute-only node, verify it joins and accepts workloads
- Run `hive admin cluster shutdown` from each of the 3 nodes in turn, verify it works from any node
- Run `hive admin cluster shutdown --dry-run` and verify output matches expected phase plan

## Future Work

- **Coordinated cluster startup** (`hive admin cluster start`): Complement to `cluster shutdown`. Coordinator starts services across all nodes in dependency order (NATS → Predastore → Viperblock → Daemon → AWSGW → UI), with health checks between phases. Would replace per-node `start-dev.sh` for multi-node clusters.
- **Persistent VM pinning**: Optional affinity rules to prefer restarting VMs on their original node
- **Live migration**: Move running VMs between nodes without stopping them (requires QEMU live migration support). Would enable `cluster shutdown` to migrate VMs to surviving nodes instead of stopping them.
- **Automatic scaling**: Detect sustained capacity pressure and suggest adding nodes
- **Recovery metrics**: Prometheus-compatible metrics for recovery duration, failures, leader elections, shutdown phase timings
- **Dynamic service reconfiguration**: Add/remove services from a running node without full restart
- **Storage-only node health**: Extend recovery to detect failed storage nodes and reroute Viperblock/Predastore traffic
- **Rolling cluster upgrade**: Coordinated restart that drains nodes one at a time, upgrades binaries, and restarts — maintaining cluster availability throughout
