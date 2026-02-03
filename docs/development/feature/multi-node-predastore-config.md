# Multi-Node Predastore Configuration

## Overview

Predastore uses Reed-Solomon erasure coding RS(2,1) -- 2 data shards and 1 parity shard -- requiring a minimum of 3 distinct nodes. Each shard is placed on a separate node via a consistent hash ring (`GetClosestN(hash, 3)`), which means every node must know the full cluster topology at startup to make identical placement decisions.

This is fundamentally different from NATS, which uses gossip-based discovery and can add members dynamically. Predastore's hash ring is static -- if nodes disagree about membership, they'll compute different shard placements and data becomes unreachable.

## How It Works

### Init (leader node)

```
hive admin init --predastore-nodes "10.11.12.1,10.11.12.2,10.11.12.3" --bind 10.11.12.1 ...
```

1. The comma-separated IPs are parsed and validated. Each gets a sequential node ID (1, 2, 3).
2. The leader's `--bind` IP is matched against the list to determine its own node ID. If the bind IP isn't in the list, init fails.
3. `GenerateMultiNodePredastoreConfig` renders a single `predastore.toml` containing the full cluster topology -- all DB entries (port 6660), all shard entries (port 9991), RS config, auth, and buckets. Node ID 1 is marked as the Raft bootstrap leader.
4. The predastore.toml is written to `config/predastore/predastore.toml`.
5. The leader's node ID is written into `hive.toml` as `node_id = 1` under the `[predastore]` section.

### Join (follower nodes)

```
hive admin join --leader https://10.11.12.1:7777 --bind 10.11.12.2 ...
```

1. The follower contacts the leader's `/join` HTTP endpoint.
2. The leader reads its `predastore.toml` from disk and includes the full content in the join response (`PredastoreConfig` field).
3. The follower writes this verbatim to its own `config/predastore/predastore.toml`. Now all nodes have an identical copy.
4. `ParsePredastoreNodeIDFromConfig` unmarshals the TOML, extracts the `[[db]]` array, and matches the follower's `--bind` IP to find its node ID. If the IP isn't found, join fails with an error.
5. The detected node ID is written into the follower's `hive.toml` (e.g., `node_id = 2`).

### Startup

When `start-dev.sh` runs, it reads `node_id` from `hive.toml` using awk and exports it as `HIVE_PREDASTORE_NODE_ID`. Predastore reads this environment variable to know which DB and shard entries in the shared config belong to it.

## Key Design Decisions

### Shared config, per-node identity

Every node gets the **same** `predastore.toml` (the full cluster topology). The only difference per node is `node_id` in `hive.toml`. This separation exists because:

- Predastore needs all members known at startup for consistent hash ring agreement.
- The `predastore.toml` describes the cluster. The `node_id` in `hive.toml` says "I am this member."
- This makes config distribution simple: the leader just sends one file, and the follower figures out its own identity by matching its bind IP.

### Why IPs must be specified at init time

Unlike NATS (gossip discovery), Predastore requires static membership. The hash ring maps `GetClosestN(hash, 3)` to determine which 3 nodes store each object's shards. If a node joins without being in the ring, existing nodes won't route data to it. If a node is in the ring but missing, data placed on it becomes unavailable.

This means all Predastore node IPs must be known when the leader is initialized. The `--predastore-nodes` flag captures this.

### Node ID 1 is the Raft bootstrap leader

The generated config marks `leader = true` on the `[[db]]` entry with `id = 1`. This is the node that bootstraps the Raft cluster. It's always the first IP in the `--predastore-nodes` list, which is also the `--bind` IP of the init command (the leader node).

### Config distribution via join protocol

Rather than requiring operators to manually copy `predastore.toml` to each node, the existing HTTP join protocol distributes it. The leader's daemon reads its predastore.toml and includes it in the join response. This is the same pattern used for CA certificates.

The leader currently sends its predastore.toml unconditionally (even in single-node setups). The follower treats any non-empty `PredastoreConfig` in the response as "leader provided a multi-node config" and skips generating its own template-based config.

### Node ID detection uses proper TOML parsing

The follower determines its node ID by parsing the received predastore.toml with `go-toml/v2` and calling `FindNodeIDByIP` against the `[[db]]` entries. If parsing fails or the bind IP isn't found, join exits with an error. This mirrors the init path, which also exits on failure.

## Backward Compatibility

When `--predastore-nodes` is omitted from `hive admin init`:

- No multi-node predastore.toml is generated.
- `PredastoreNodeID` stays at 0, so `node_id` is not emitted in hive.toml.
- The default single-node predastore.toml template is used instead.
- `start-dev.sh` finds no `node_id` in hive.toml and leaves `HIVE_PREDASTORE_NODE_ID` empty.
- Predastore runs in its default single-node mode.

Existing single-node deployments are unaffected.

## Files Involved

| File | Role |
|------|------|
| `cmd/hive/cmd/admin.go` | CLI flag parsing, init/join orchestration |
| `hive/admin/admin.go` | `GenerateMultiNodePredastoreConfig`, `FindNodeIDByIP`, `ParsePredastoreNodeIDFromConfig`, `BaseConfigFiles` |
| `hive/config/config.go` | `PredastoreConfig.NodeID` field, `NodeJoinResponse.PredastoreConfig` field |
| `hive/daemon/daemon.go` | `/join` endpoint reads and sends predastore.toml |
| `cmd/hive/cmd/templates/hive.toml` | Conditional `node_id` rendering |
| `scripts/start-dev.sh` | Auto-detects `NODE_ID` from hive.toml at startup |

## Generated Config Example

For `--predastore-nodes "10.11.12.1,10.11.12.2,10.11.12.3"`:

```toml
version = "1.0"
region = "ap-southeast-2"

host = "0.0.0.0"
port = 8443

[rs]
data = 2
parity = 1

[[db]]
id = 1
host = "10.11.12.1"
port = 6660
path = "distributed/db/node-1/"
leader = true

[[db]]
id = 2
host = "10.11.12.2"
port = 6660
path = "distributed/db/node-2/"

[[db]]
id = 3
host = "10.11.12.3"
port = 6660
path = "distributed/db/node-3/"

[[nodes]]
id = 1
host = "10.11.12.1"
port = 9991
path = "distributed/nodes/node-1/"

[[nodes]]
id = 2
host = "10.11.12.2"
port = 9991
path = "distributed/nodes/node-2/"

[[nodes]]
id = 3
host = "10.11.12.3"
port = 9991
path = "distributed/nodes/node-3/"

[[buckets]]
name = "predastore"
region = "ap-southeast-2"
type = "distributed"

[[auth]]
access_key_id = "..."
secret_access_key = "..."
```

Each node receives this identical file. Node 1's `hive.toml` has `node_id = 1`, node 2 has `node_id = 2`, etc.
