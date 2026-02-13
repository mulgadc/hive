# Multi-Server Cluster Formation

## Problem

The current multi-server configuration has a fundamental design flaw: cluster formation and cluster operation are entangled. The `/join` endpoint lives inside the Hive daemon, which requires NATS to be running. This creates a chicken-and-egg problem — nodes cannot join until services are running, but services cannot be correctly configured until all nodes have joined.

### Current bugs

1. **NATS config missing all cluster routes** — each node's `nats.conf` only contains the leader's route, not routes to all other nodes. NATS cannot form a full mesh cluster.
2. **NATS cluster name hardcoded to `C1`** — should be configurable per-cluster.
3. **Predastore "Distributed mode requires -node flag"** — NODE_ID detection fails in multi-node setups.
4. **Predastore "no known peers" on joining nodes** — Raft starts with `servers=[]`, cannot form consensus.
5. **Sequence dependency** — node 1 must run `./scripts/start-dev.sh` before node 2 can `hive admin join`, because the `/join` endpoint is inside the daemon.
6. **Certs not installed to system CA store** — joining nodes receive the CA cert but don't install it, causing TLS failures.

### Root cause

All bugs trace back to one issue: configs are generated per-node during init/join with incomplete information (only the leader IP is known). The correct approach is to generate configs AFTER all nodes have registered, using complete cluster information.

## Design

Separate cluster **formation** (lightweight HTTP) from cluster **operation** (NATS, Predastore, etc).

### Formation flow

```
                    ┌─────────────────────────────────┐
                    │   hive admin init --nodes 3      │
                    │   Starts formation HTTP server    │
                    │   Registers self as node 1        │
                    │   Waits for 2 more nodes...       │
                    └──────────┬──────────────────────┘
                               │ :4432
              ┌────────────────┼────────────────┐
              │                │                │
    ┌─────────▼──────┐  ┌─────▼────────┐  ┌───▼──────────┐
    │ hive admin join │  │  (waiting)   │  │  (waiting)   │
    │ POST /join      │  │              │  │              │
    │ polls /status   │  │              │  │              │
    └────────────────┘  └──────────────┘  └──────────────┘
              │                │                │
              └────────────────┼────────────────┘
                               │ All 3 nodes joined
                    ┌──────────▼──────────────────────┐
                    │   Formation complete              │
                    │   Generate configs with ALL IPs   │
                    │   Distribute CA, credentials      │
                    │   All processes exit               │
                    └──────────┬──────────────────────┘
                               │
              ┌────────────────┼────────────────┐
              │                │                │
    ┌─────────▼──────┐  ┌─────▼────────┐  ┌───▼──────────┐
    │ start-dev.sh   │  │ start-dev.sh │  │ start-dev.sh │
    │ Node 1         │  │ Node 2       │  │ Node 3       │
    └────────────────┘  └──────────────┘  └──────────────┘
```

### Phase 1: Formation (init + join)

**Node 1** starts a lightweight HTTP formation server (no NATS dependency):

```sh
./bin/hive admin init \
  --node node1 \
  --nodes 3 \
  --bind 10.11.12.1 \
  --cluster-bind 10.11.12.1 \
  --port 4432 \
  --hive-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a
```

This generates credentials (AWS keys, NATS token, CA certs) and starts the formation HTTP server on `10.11.12.1:4432`. It registers itself as the first node and waits for 2 more.

**Node 2 and Node 3** join while init is running:

```sh
./bin/hive admin join \
  --node node2 \
  --bind 10.11.12.2 \
  --cluster-bind 10.11.12.2 \
  --host 10.11.12.1:4432 \
  --data-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a
```

Each joining node POSTs to the formation server, then polls for completion.

Once all 3 nodes register, the formation server:
1. Computes NATS cluster routes from ALL node IPs (full mesh)
2. Builds predastore node configs from ALL node IPs (with correct node IDs)
3. Returns complete config package (credentials, CA, node list) to each polling node
4. Each node generates its own configs locally with complete cluster data
5. CA cert is installed to system trust store on each node
6. All processes (init + joins) display cluster summary and exit

**Key improvements over current design:**
- `--predastore-nodes` is now **optional** — derived automatically from joined nodes
- `--cluster-routes` on join is now **optional** — derived from formation data
- No sequence dependency — init starts the formation server directly (no NATS needed)
- All configs have complete cluster data (all IPs, all routes)

### Phase 2: Service startup

After formation completes, run on each node:

```sh
./scripts/start-dev.sh
```

Services start in order: NATS → Predastore → Viperblock → Daemon → AWS Gateway → UI.

Since all configs already have the full cluster topology, NATS forms a mesh immediately and Predastore Raft finds all peers.

## Network recommendations

It is recommended that at least two network links are used by Hive:

1. **Management link** — cluster sync, NATS, Predastore Raft, and daemon communication. This requires low latency (< 5ms) and dedicated bandwidth.
2. **Data/VM link** — EC2 instances, VPC traffic (Geneve/Open vSwitch), and user-facing services. Isolated from management to prevent contention.

This follows the same model as Proxmox VE, which recommends a physically separated NIC for cluster (corosync) traffic: "The network should not be used heavily by other members, as while corosync does not use much bandwidth it is sensitive to latency jitters."

## Proxmox Cluster Manager comparison

Reviewed [Proxmox Cluster Manager](https://pve.proxmox.com/wiki/Cluster_Manager) for best practices.

**What we adopt from Proxmox:**
- Formation-first workflow (`pvecm create` / `pvecm add` completes before services use the cluster)
- Shared CA model (joining node's cert is replaced with one signed by cluster CA)
- Dedicated cluster network recommendation (separate NIC for management traffic)
- Hostname/IP finalized before formation (cannot change post-creation)

**Why native Go over Corosync:**

Corosync is a C group communication library providing reliable messaging, quorum, and membership. Hive already has equivalent capabilities:

| Capability | Corosync | Hive |
|---|---|---|
| Message bus | Totem protocol | NATS |
| State replication | pmxcfs (replicated filesystem) | NATS JetStream KV |
| Consensus/quorum | Corosync votequorum | Predastore Raft |
| Config distribution | pmxcfs | Formation server + hive.toml |

Adding Corosync would be a redundant external C dependency providing overlapping functionality. The native Go implementation gives us:
- Single binary deployment (no `corosync` daemon to install/manage)
- Direct integration with NATS and Predastore
- Full control over the join protocol
- No CGo or external library dependency

## Implementation

### New package: `hive/formation/`

Lightweight HTTP formation server using `net/http`. Zero dependency on NATS or any operational service.

**`formation.go`** — core server:
- `FormationServer` struct: tracks expected nodes, joined nodes, shared credentials, CA cert/key
- `POST /formation/join` — register a node, return progress
- `GET /formation/status` — poll completion; returns full config data when all nodes joined
- `GET /formation/health` — liveness check
- `WaitForCompletion()` — blocks until all nodes join or timeout (default 10 minutes)

**`helpers.go`** — config builders:
- `BuildClusterRoutes(nodes)` — returns all node cluster-bind IPs as `IP:4248` routes
- `BuildPredastoreNodes(nodes)` — returns `[]PredastoreNodeConfig` with 1-based IDs

### Changes to `cmd/hive/cmd/admin.go`

**`runAdminInit`:**
- `--nodes 1` (or no cluster flags): existing single-node behaviour, unchanged
- `--nodes >= 2`: start formation server, register self, wait for joins, generate configs with complete data
- `--predastore-nodes` becomes optional (derived from formation when not specified)
- New flag: `--formation-timeout` (default "10m")

**`runAdminJoin`:**
- POST to `http://leader:port/formation/join` (not the daemon `/join`)
- Poll `GET /formation/status` every 2s until complete
- Extract full node list, CA, credentials from response
- Generate all configs locally with complete cluster data
- Install CA to system trust store

### Changes to templates

**`templates/nats.conf`:** Change `name: C1` to `name: {{ .ClusterName }}`

**`hive/admin/admin.go`:** Add `ClusterName string` to `ConfigSettings`

### Changes to `scripts/start-dev.sh`

- Remove awk-based NODE_ID auto-detection (formation now writes correct node_id into hive.toml)
- Keep `HIVE_PREDASTORE_NODE_ID` env var override for manual use

### Daemon `/join` endpoint (no changes)

The existing `/join` endpoint in `hive/daemon/daemon.go` stays as-is for future "hot-add node to running cluster" scenarios. Formation handles initial bootstrap; daemon handles runtime expansion.

### Graceful cluster shutdown

The current `stop-dev.sh` sends SIGTERM to each service sequentially with no cluster coordination. In a multi-node cluster this causes cascading failures — if the Raft leader is stopped first, remaining predastore nodes enter an election storm trying to contact dead peers, goroutines block, and the cluster hangs instead of shutting down cleanly.

#### Two shutdown modes

**1. Cluster-wide shutdown** (`hive admin shutdown`):

Coordinated shutdown of the entire cluster. The initiating node broadcasts a shutdown intent, all nodes agree, and services stop in a safe order across the cluster.

```sh
# On any node — broadcasts to entire cluster
./bin/hive admin shutdown
```

**2. Single-node shutdown** (`stop-dev.sh` or `hive admin stop`):

Stops services on one node only. The node announces its departure so peers can adjust (e.g., Raft leader transfer) rather than treating it as a crash.

```sh
# On the node being stopped
./scripts/stop-dev.sh
```

#### Cluster-wide shutdown flow

```
Operator runs: hive admin shutdown (on any node)
              │
              ▼
┌──────────────────────────────────┐
│ 1. Connect to local daemon       │
│ 2. POST /cluster/shutdown        │
│ 3. Daemon broadcasts             │
│    hive.cluster.shutdown via NATS │
└──────────────┬───────────────────┘
               │
    ┌──────────┼──────────┐
    ▼          ▼          ▼
  Node 1     Node 2     Node 3
    │          │          │
    │  All nodes enter "shutting_down" state
    │  Stop accepting new work (API returns 503)
    │          │          │
    ▼          ▼          ▼
  Phase 1: Stop VMs (system_powerdown, wait, write state to JetStream)
    │          │          │
    ▼          ▼          ▼
  Phase 2: Stop daemon + gateway (unsubscribe NATS, close connections)
    │          │          │
    ▼          ▼          ▼
  Phase 3: Stop storage (predastore Raft shutdown, viperblock)
    │          │          │
    ▼          ▼          ▼
  Phase 4: Stop NATS (last — needed for coordination until this point)
```

#### Shutdown protocol

1. **Initiation** — any node's daemon receives `POST /cluster/shutdown` (from `hive admin shutdown` CLI)
2. **Broadcast** — daemon publishes `hive.cluster.shutdown` on NATS with initiator node ID and epoch
3. **Acknowledgement** — each daemon responds on `hive.cluster.shutdown.ack` with its node ID. Initiator waits up to 10s for all known nodes to ACK (proceeds anyway if some nodes are unreachable)
4. **Phase 1: VM drain** — all daemons stop accepting new `ec2.RunInstances` requests, send `system_powerdown` to running VMs, write instance state to JetStream, wait up to 60s for VMs to power off
5. **Phase 2: Daemon stop** — daemons unsubscribe from all NATS topics, shut down the cluster HTTP server and AWS Gateway
6. **Phase 3: Storage stop** — predastore Raft leader (if local) calls `raft.Shutdown()` first, then followers shut down Raft; viperblock stops. Because all nodes are shutting down simultaneously, there's no election storm — every node's Raft transport is closed before heartbeat timeouts fire
7. **Phase 4: NATS stop** — NATS server is the last to stop (it was needed for the shutdown broadcast and ACKs)
8. **Cleanup** — each node's `stop-dev.sh` waits for all service PIDs to exit, force-kills any stragglers after 120s

#### Predastore Raft shutdown handling

The core problem: when a Raft leader disappears without warning, followers detect missing heartbeats and start elections. With 2-of-3 nodes gone, elections can never succeed and goroutines spin indefinitely.

**Solution for cluster-wide shutdown:**
- All predastore nodes receive the shutdown broadcast via NATS (Phase 3)
- Each node closes the Raft transport first (`transport.Close()`), which prevents any new RPCs
- Then calls `raft.Shutdown()` which drains in-flight operations
- Because all nodes close transport near-simultaneously, no node has time to start an election
- The 5-second Raft shutdown timeout in predastore (`s3db/raft.go`) is sufficient since no elections are triggered

**Solution for single-node shutdown:**
- The departing node publishes `hive.cluster.node-leaving` with its node ID
- If the departing node is the Raft leader, it calls `raft.LeadershipTransfer()` before shutting down — this hands leadership to a follower cleanly
- If it's a follower, it calls `raft.Shutdown()` directly — the leader and remaining followers continue normally
- Remaining nodes log the departure and continue operating (Raft maintains quorum with N-1 nodes if majority survives)

#### Single-node stop flow

When `stop-dev.sh` runs on one node in a multi-node cluster:

1. Daemon receives SIGTERM
2. Daemon publishes `hive.cluster.node-leaving` with its node ID on NATS
3. If this node is the predastore Raft leader: call `raft.LeadershipTransfer()`, wait up to 5s
4. Stop VMs: `system_powerdown` → wait → write state to JetStream
5. Unsubscribe from NATS topics, shut down cluster HTTP server
6. Stop predastore (Raft transport close, then `raft.Shutdown()`)
7. Stop viperblock
8. Close NATS connection
9. Stop NATS server

The remaining cluster nodes see the `node-leaving` message and:
- Remove the node from active heartbeat tracking (no false alarms)
- Predastore Raft has already transferred leadership if needed
- AWS Gateway's next `hive.nodes.discover` broadcast gets fewer responses (expected)
- No config changes — the node is still in `hive.toml` but simply offline

#### Changes to `scripts/stop-dev.sh`

- Before stopping services, check if this is a multi-node cluster (read `hive.toml` node count)
- If multi-node: send `hive.cluster.node-leaving` via the daemon before stopping it
- If daemon is unreachable (already crashed): proceed with direct SIGTERM as today (best effort)
- Keep the existing service stop order but add a brief pause after daemon stop to allow the `node-leaving` broadcast to propagate

#### New CLI command: `hive admin shutdown`

```sh
./bin/hive admin shutdown [--force] [--timeout 120s]
```

- Connects to local daemon's cluster HTTP endpoint
- `POST /cluster/shutdown` triggers the coordinated shutdown flow
- `--force` skips VM drain and ACK wait (immediate SIGTERM to all services)
- `--timeout` controls max wait for VM powerdown (default 120s)
- Prints progress: "Waiting for ACKs... 3/3", "Draining VMs... 2 remaining", "Stopping storage...", "Shutdown complete"

## Files

| File | Action |
|---|---|
| `hive/formation/formation.go` | New — formation HTTP server |
| `hive/formation/helpers.go` | New — route and node config builders |
| `hive/formation/formation_test.go` | New — tests |
| `hive/admin/admin.go` | Modify — add ClusterName to ConfigSettings |
| `cmd/hive/cmd/admin.go` | Modify — rewrite init multi-node path + join, add `shutdown` subcommand |
| `cmd/hive/cmd/templates/nats.conf` | Modify — C1 → {{ .ClusterName }} |
| `scripts/start-dev.sh` | Modify — simplify NODE_ID detection |
| `scripts/stop-dev.sh` | Modify — check cluster mode, send `node-leaving` before stopping services |
| `hive/daemon/daemon.go` | Modify — add shutdown broadcast handler, `node-leaving` publish on SIGTERM, Raft leader transfer coordination |

## Testing

### Unit tests

- Formation server: all nodes join, duplicate rejection, timeout, status polling
- Helper functions: route building, predastore node building
- NATS template: renders cluster name and all routes correctly
- Shutdown broadcast: all nodes ACK, timeout when node unreachable, phase ordering
- Node-leaving: Raft leader transfer triggered when departing node is leader

### Integration test

Note: `--config-dir ~/$NODE/config/` needs to be specified for each node, so not to conflict when testing locally to simulate 3 nodes.

1. `hive admin init --nodes 3 --bind 127.0.0.1 --node node1` in terminal 1
2. `hive admin join --node node2 --host 127.0.0.1:4432 --bind 127.0.0.2` in terminal 2
3. `hive admin join --node node3 --host 127.0.0.1:4432 --bind 127.0.0.3` in terminal 3
4. Verify: all 3 exit with cluster summary
5. Verify: each `nats.conf` has routes to ALL other nodes
6. Verify: each `predastore.toml` has all 3 nodes
7. Verify: each `hive.toml` has all 3 nodes in `[nodes.*]`
8. Run `./scripts/start-dev.sh` on all 3, verify NATS mesh and Predastore Raft form correctly

### Shutdown integration test

1. Start 3-node cluster (from formation integration test above)
2. Run `hive admin shutdown` on node1
3. Verify: all 3 nodes receive shutdown broadcast (check logs for ACK)
4. Verify: all predastore Raft nodes shut down cleanly — no election storms in logs
5. Verify: all NATS servers stop last
6. Verify: all service PIDs are gone on all 3 nodes

### Single-node stop integration test

1. Start 3-node cluster
2. Run `./scripts/stop-dev.sh` on node3 (a Raft follower)
3. Verify: node3 publishes `node-leaving`, services stop cleanly
4. Verify: nodes 1 and 2 continue operating, Predastore Raft maintains quorum (2/3)
5. Run `./scripts/stop-dev.sh` on node1 (if node1 is Raft leader)
6. Verify: Raft leadership transfers to node2 before node1's predastore stops
7. Verify: node2 continues operating as single-node (degraded but functional)

---

## Stage 2: Runtime Node Expansion

### Overview

After the initial cluster is formed (Stage 1) and services are running, operators need the ability to add new nodes without stopping the cluster. This covers "day 2" operations — e.g., a 3-node cluster formed on Monday, with a 4th node added on Tuesday.

The daemon takes over formation duties once the cluster is running. The existing `/join` endpoint in `hive/daemon/daemon.go` is replaced by the formation server operating in "Stage 2 mode" — same formation package, different context.

### Key difference from Stage 1

Stage 1 formation happens BEFORE any services start — it's a clean-room config generation step where all processes exit after writing configs. Stage 2 happens WHILE services are running, so each service must be notified of topology changes and reconfigure itself live.

### Expansion flow

```
Existing running cluster (3 nodes)                  New node
┌──────────┐  ┌──────────┐  ┌──────────┐
│  Node 1   │  │  Node 2   │  │  Node 3   │
│  daemon   │  │  daemon   │  │  daemon   │
│  :4432    │  │  :4432    │  │  :4432    │
└─────┬─────┘  └─────┬─────┘  └─────┬─────┘
      │  NATS mesh    │              │
      └───────┬───────┘              │
              │                      │         ┌─────────────────┐
              │                      │         │ hive admin join  │
              │                      │         │   --host node1   │
              │                      │         │   --node node4   │
              │                      │         │   --capability   │
              │                      │         │   nats,viperblock│
              │                      │         │   ,hive,awsgw    │
              │                      │         └────────┬────────┘
              │                      │                  │
              │                      │       POST /formation/join
              │                      │                  │
        ┌─────▼──────┐              │                  │
        │ Node 1      │◄─────────────┼──────────────────┘
        │ validates    │             │
        │ updates toml │             │
        │ returns creds│─────────────┘
        │ broadcasts   │──► hive.cluster.config-update (NATS)
        └─────┬───────┘
              │
              ▼
   All existing nodes receive broadcast:
   - Regenerate nats.conf → SIGHUP nats-server
   - Update predastore.toml if needed → restart predastore
   - Update hive.toml with new node entry
              │
              ▼
   New node generates configs, starts services:
   ./scripts/start-dev.sh  (on node4 only)
```

### Node capabilities

Not all nodes need to run all services. The `--capability` flag controls which services a node provides:

```sh
./bin/hive admin join \
  --node node4 \
  --bind 10.11.12.4 \
  --cluster-bind 10.11.12.4 \
  --host 10.11.12.1:4432 \
  --data-dir ~/hive/ \
  --config-dir ~/hive/config/ \
  --region ap-southeast-2 \
  --az ap-southeast-2a \
  --capability nats,viperblock,hive,awsgw
```

Available capabilities:

| Capability | Service | Notes |
|---|---|---|
| `nats` | NATS server | Cluster messaging — required for all nodes |
| `predastore` | Predastore storage node | Object shard storage via QUIC |
| `predastoredb` | Predastore DB node | Raft-replicated metadata database |
| `viperblock` | Viperblock | Local NVMe WAL for block storage — required if running VMs |
| `hive` | Hive daemon | VM orchestration and scheduling |
| `awsgw` | AWS Gateway | TLS API endpoint for AWS SDK compatibility |
| `hive-ui` | Hive UI | Web management interface |

Default: all capabilities enabled.

**Example: compute-only node** (runs VMs, no object storage):
```sh
--capability nats,viperblock,hive,awsgw
```

**Example: storage-only node** (object storage, no VMs):
```sh
--capability nats,predastore
```

**Predastore DB scaling note:** The `predastoredb` capability controls whether a node participates in the Predastore Raft metadata cluster. For small clusters (3-5 nodes), every predastore node should also run predastoredb. For larger clusters (10+ nodes), Raft only needs 3-5 voters for consensus — additional predastore nodes store object shards via the hash ring but don't replicate the metadata database. This avoids replicating the KV store 25 times in a 25-node cluster.

The `--capability` flag also applies to Stage 1 `hive admin join` for initial formation. Stage 1 `hive admin init` always has all capabilities (it's the bootstrap node).

### Join protocol (Stage 2)

The `hive admin join` command detects whether it's joining a formation (Stage 1) or a running cluster (Stage 2) based on the response from the leader.

1. New node runs `hive admin join --host leader:4432 --node node4 --capability ...`
2. New node POSTs to `http://leader:4432/formation/join` with node info + capabilities
3. Leader daemon validates:
   - Node name not already in cluster (409 Conflict if duplicate)
   - Bind IP not already in use by another node
   - Capabilities are valid strings
4. Leader updates local `hive.toml` with new node entry, increments epoch
5. Leader returns: CA cert/key, credentials (AWS access/secret key, NATS token), full node list including the new node
6. New node writes CA cert/key, generates server cert signed by cluster CA
7. New node generates local configs (nats.conf, hive.toml, predastore.toml, etc.) using complete cluster data, filtered by its capabilities
8. New node installs CA cert to system trust store
9. Leader broadcasts config update to all existing nodes via NATS (`hive.cluster.config-update`)
10. Existing nodes receive update and reconfigure affected services (see below)
11. `hive admin join` exits with cluster summary — operator then runs `start-dev.sh` on the new node

### Service reconfiguration on existing nodes

When a new node joins a running cluster, the leader broadcasts a `hive.cluster.config-update` message via NATS containing the updated node list, capabilities, and epoch. Each existing daemon handles the update:

**NATS:**
1. Regenerate `nats.conf` with new route added (only if new node has `nats` capability)
2. Send `SIGHUP` to the `nats-server` process — NATS supports config reload via signal, including adding new cluster routes
3. NATS discovers and connects to the new node once it starts
4. If SIGHUP reload fails, log error and flag for manual restart

**Predastore storage (`predastore` capability):**
- If the new node has `predastore` capability: regenerate `predastore.toml` with new `[[nodes]]` entry and restart the predastore process (predastore does not support hot config reload)
- The consistent hash ring updates on restart — new PUTs distribute across all nodes including the new one
- Existing objects are unaffected (see "Hash ring rebalancing" below)

**Predastore DB (`predastoredb` capability):**
- If the new node has `predastoredb` capability: the leader calls `raft.AddVoter()` to add the new node to the Raft cluster
- No restart needed on existing DB nodes — Raft handles membership changes natively via joint consensus
- If the cluster already has sufficient DB voters (e.g., 5), the new node is not added as a voter even if it has the `predastoredb` capability — log a warning

**Viperblock:**
- No reconfiguration needed on existing nodes — viperblock is local to each node
- New node starts its own viperblock instance independently

**JetStream:**
- Call `UpdateReplicas()` to match new cluster size (already implemented in `jetstream.go`)
- Capped at the NATS cluster size (JetStream replicas cannot exceed the number of NATS nodes)

**AWS Gateway:**
- No reconfiguration needed — uses dynamic NATS node discovery (`hive.nodes.discover` fan-out with 500ms timeout)
- New node's daemon responds to discovery automatically once it's connected to NATS

**Hive UI:**
- No reconfiguration needed — UI queries the AWS Gateway, which discovers nodes dynamically

### Predastore hash ring rebalancing

When a new predastore storage node joins, the consistent hash ring (xxhash + `buraksezer/consistent`) changes. Shard placement for new writes shifts, but existing data is safe.

**Immediate behaviour (no rebalancing — Stage 2 scope):**
- Existing objects stay on their original nodes. Shard locations are recorded per-object in `ObjectToShardNodes` in the Raft-replicated s3db — GETs look up this metadata, not the ring
- New objects (PUTs) use the updated ring, distributing across all nodes including the new one
- DELETEs use stored metadata to find and remove shards from the correct nodes
- The cluster is fully functional without rebalancing — the only downside is uneven storage distribution until a rebalance runs

**Why this is safe:** The hash ring is only used at write time to decide where to place shards. Once placed, the mapping `object_hash → [node_ids]` is persisted in s3db. All subsequent reads and deletes use this stored mapping, not the ring. Adding a node to the ring does not invalidate any existing mappings.

**Background rebalancing (future — not in Stage 2 scope):**
A future `hive admin rebalance` command would:
1. Scan all objects in s3db
2. For each object, compute new ring placement with current topology
3. If placement changed, migrate shards from old nodes to new node via QUIC
4. Update s3db metadata with new shard locations (atomic Raft write)
5. Delete old shard copies after successful migration

This is a significant feature (data migration, progress tracking, failure recovery, bandwidth throttling) and is deferred to a dedicated implementation.

### Config sync protocol

All nodes must converge on the same cluster state. The protocol uses epoch-based versioning:

1. **Epoch counter** — every config change (node join, capability change) increments the epoch on the leader
2. **Broadcast on change** — leader publishes `hive.cluster.config-update` via NATS with full node list + epoch
3. **Periodic heartbeat** — each daemon periodically publishes its epoch on `hive.cluster.heartbeat`; if a node detects it has a stale epoch (e.g., it was temporarily disconnected when the broadcast happened), it requests the latest config from the leader via `hive.cluster.config-request`
4. **Config hash** — SHA256 of shared cluster data (already implemented via `computeConfigHash()`) included in `/health` endpoint response; monitoring can detect inconsistencies
5. **Idempotent updates** — if a node receives a config update with the same or older epoch, it ignores it

### New node startup sequence

After `hive admin join` completes on the new node:

```sh
./scripts/start-dev.sh  # On the new node only
```

`start-dev.sh` reads the node's capabilities from `hive.toml` and starts only matching services:

1. **NATS** (if `nats` capability) — joins existing mesh via routes in nats.conf
2. **Predastore** (if `predastore` capability) — starts storage node; existing nodes have already been restarted with updated config
3. **Predastore DB** (if `predastoredb` capability) — starts DB node; leader has already called `AddVoter()` so Raft accepts it
4. **Viperblock** (if `viperblock` capability) — starts local instance
5. **Daemon** (if `hive` capability) — connects to NATS, subscribes to topics, announces via heartbeat
6. **AWS Gateway** (if `awsgw` capability) — starts and discovers nodes via NATS fan-out
7. **UI** (if `hive-ui` capability) — starts web interface

### Stage 2 implementation

**Changes to `hive/formation/formation.go`:**
- Add `Stage2Join()` handler — validates node, updates config, returns credentials + full cluster data
- Add `BroadcastConfigUpdate(natsConn, clusterConfig)` — publishes update to `hive.cluster.config-update` NATS topic
- `FormationServer` gains `mode` field: `ModeFormation` (Stage 1) vs `ModeRunning` (Stage 2)
- In Stage 2 mode, the server has access to the NATS connection for broadcasting

**Changes to `hive/daemon/daemon.go`:**
- On startup, start formation server in Stage 2 mode (replaces the existing `/join` handler)
- Remove old `/join` endpoint handler (lines 814-931) — replaced by formation server
- Add `handleConfigUpdate()` — NATS subscription on `hive.cluster.config-update`, applies received config
- Add `reloadNATS()` — regenerates nats.conf from current cluster config, sends SIGHUP to nats-server PID
- Add `reloadPredastore()` — regenerates predastore.toml, restarts predastore process
- Add `handleClusterHeartbeat()` — periodic epoch broadcast + stale detection

**Changes to `hive/config/config.go`:**
- Add `Capabilities []string` to per-node `Config` struct
- Add `Capabilities []string` to `NodeJoinRequest`
- Add `HasCapability(cap string) bool` method on `Config`

**Changes to `cmd/hive/cmd/admin.go`:**
- `runAdminJoin`: new `--capability` flag (comma-separated string, default all capabilities)
- Join detects Stage 1 vs Stage 2: if formation server responds with `mode: "formation"`, poll for completion (Stage 1); if `mode: "running"`, configs are returned immediately (Stage 2)

**Changes to `hive/formation/helpers.go`:**
- `BuildClusterRoutes(nodes)` → `BuildClusterRoutes(nodes, capabilityFilter)` — only include nodes with `nats` capability
- `BuildPredastoreNodes(nodes)` → filter by `predastore` capability
- New: `BuildPredastoreDBNodes(nodes)` — filter by `predastoredb` capability
- New: `FilterNodesByCapability(nodes map[string]Config, cap string) []Config`

**Changes to config templates:**
- `nats.conf` — routes only include nodes with `nats` capability
- `predastore-multinode.toml` — `[[nodes]]` only include `predastore` capable nodes; `[[db]]` only include `predastoredb` capable nodes

**Changes to `scripts/start-dev.sh`:**
- Read capabilities from `hive.toml` for the local node
- Only start services matching the node's capabilities
- Skip predastore/predastoredb/viperblock/awsgw/hive-ui if not in capability list

### Stage 2 files

| File | Action |
|---|---|
| `hive/formation/formation.go` | Modify — add Stage 2 mode, Stage2Join handler, broadcast |
| `hive/formation/helpers.go` | Modify — capability-aware filtering for all config builders |
| `hive/formation/formation_test.go` | Modify — add Stage 2 tests |
| `hive/daemon/daemon.go` | Modify — replace `/join` with formation Stage 2, add config update + service reload handlers |
| `hive/config/config.go` | Modify — add Capabilities field to Config and NodeJoinRequest |
| `cmd/hive/cmd/admin.go` | Modify — add `--capability` flag, Stage 1 vs Stage 2 detection logic |
| `cmd/hive/cmd/templates/nats.conf` | Modify — capability-filtered routes |
| `cmd/hive/cmd/templates/predastore-multinode.toml` | Modify — capability-filtered `[[nodes]]` and `[[db]]` sections |
| `scripts/start-dev.sh` | Modify — capability-aware service startup |
| `hive/daemon/jetstream.go` | No changes (UpdateReplicas already handles dynamic scaling) |

### Stage 2 testing

**Unit tests:**
- Stage 2 join: validates capabilities, rejects duplicate node names and IPs, increments epoch
- Capability filtering: correct nodes appear in NATS routes and predastore config
- Config broadcast: serialization/deserialization of `hive.cluster.config-update` messages
- Service reload: NATS config regeneration produces valid config, predastore config includes correct node subsets
- Epoch-based sync: stale epoch detection triggers config request

**Integration test:**
1. Form 3-node cluster via Stage 1 (all capabilities)
2. Start all 3 nodes, verify NATS mesh + Predastore Raft healthy
3. Run `hive admin join --node node4 --host 127.0.0.1:4432 --bind 127.0.0.4 --capability nats,viperblock,hive,awsgw`
4. Verify: node4 added to all nodes' `hive.toml` with correct capabilities
5. Verify: all `nats.conf` files updated with node4 route
6. Verify: `predastore.toml` NOT updated (node4 has no `predastore` capability)
7. Start node4 with `start-dev.sh`, verify it joins NATS mesh
8. Verify: AWS Gateway discovers node4 via `hive.nodes.discover`
9. Verify: predastore cluster unchanged (still 3 nodes)
10. Run `hive admin join --node node5 --host 127.0.0.1:4432 --bind 127.0.0.5 --capability nats,predastore`
11. Verify: `predastore.toml` updated on all nodes with node5 as storage node
12. Verify: predastore `[[db]]` section unchanged (node5 has no `predastoredb` capability)
13. Start node5, write new object via S3 API, verify shards distributed across 4 predastore nodes

## Remote testing

Integration tests above use localhost IPs for local simulation. For real multi-server testing, a 3-node cluster environment is available — see `docs/INTERNAL.md` for SSH access, deployment workflow (git diff → scp → git apply → make build), and cluster init/join commands with real IPs.

The remote test flow:
1. Deploy code patches to all 3 nodes (INTERNAL.md: "Deploying code changes")
2. Fresh install or patch-only depending on scope (INTERNAL.md: "Testing workflows")
3. Run `hive admin init` on node 1, `hive admin join` on nodes 2 and 3 (INTERNAL.md: "Cluster init and join")
4. Start services on all nodes, verify NATS mesh, Predastore Raft, and daemon health
5. Run AWS CLI smoke test against the cluster endpoint
