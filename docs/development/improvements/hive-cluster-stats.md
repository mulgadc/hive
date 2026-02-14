# Hive Cluster Stats CLI

**Status: Complete**

## Summary

Added `hive get {nodes,vms}` and `hive top nodes` commands to provide kubectl-style cluster visibility. Operators can now see node health, running VMs, and resource utilization across the cluster from a single command.

This also satisfies the `hive admin status` command proposed in `multi-server-restart.md` (Section 3.3) — `hive get nodes` and `hive top nodes` together provide the same information with better kubectl-style UX.

## Commands

### `hive get nodes`

Like `kubectl get nodes -o wide`. Shows all physical nodes in the cluster.

```
$ hive get nodes
NAME    STATUS    IP            REGION             AZ                  UPTIME   VMs   SERVICES
node1   Ready     10.11.12.1    ap-southeast-2     ap-southeast-2a     3d4h     2     nats,predastore,viperblock,daemon,awsgw,ui
node2   Ready     10.11.12.2    ap-southeast-2     ap-southeast-2a     3d4h     1     nats,predastore,viperblock,daemon
node3   NotReady  10.11.12.3    ap-southeast-2     ap-southeast-2b     -        -     nats,predastore,viperblock,daemon
```

- **Status values**: `Ready` (daemon responded), `NotReady` (no response within timeout)
- Nodes known from config but not responding are shown as `NotReady`
- `--timeout` flag controls response collection window (default 3s)

### `hive get vms` (alias: `hive get instances`)

Like `kubectl get pods -A -o wide`. Shows all VMs across the cluster.

```
$ hive get vms
INSTANCE             STATUS    TYPE       VCPU   MEM     NODE    IP            AGE
i-896fdcfd299a39193  running   t3.nano    2      512Mi   node1   10.11.12.1    0m
i-2b61dfa9fdb8c2b39  running   t3.nano    2      512Mi   node2   10.11.12.2    0m
i-b097988f78db6553d  running   t3.nano    2      512Mi   node2   10.11.12.2    0m
```

### `hive top nodes`

Like `kubectl top nodes`. Shows resource usage per node plus aggregated available instance types.

```
$ hive top nodes
NAME    CPU (used/total)   MEM (used/total)    VMs
node1   4/16               1.0Gi/30.6Gi        2
node2   2/16               512Mi/30.6Gi        1
node3   -                  -                   -

INSTANCE TYPE   AVAILABLE   VCPU   MEMORY
t3.nano         22          2      512Mi
t3.micro        22          2      1.0Gi
t3.small        16          2      2.0Gi
t3.medium       11          2      4.0Gi
t3.large        5           2      8.0Gi
t3.xlarge       2           4      16.0Gi
```

Bottom table aggregates free capacity across all Ready nodes.

## Design

### Config as Source of Truth

During multi-node formation, each node's `hive.toml` includes ALL cluster nodes — the local node with full config (daemon, nats, predastore subsections) and remote nodes with basic info (host, region, AZ, services). This makes config the authoritative list of expected cluster members.

The CLI builds its node list from the union of config nodes + NATS responders:
- **Config nodes that respond** → shown as `Ready` with live data from NATS
- **Config nodes that don't respond** → shown as `NotReady` with host/region/AZ from config
- **NATS responders not in config** → shown as `Ready` (safety net for dynamic additions)

### NATS Topics

Two fan-out topics (no queue group — all daemons respond):

| Topic | Purpose | Response Type |
|-------|---------|---------------|
| `hive.node.status` | Node stats + resource usage | `NodeStatusResponse` |
| `hive.node.vms` | Running VMs on this node | `NodeVMsResponse` |

### NATS Fan-Out Collection Pattern

CLI connects to NATS using the config's token, publishes to a fan-out topic with an inbox reply address, and collects all responses within the timeout window:

```go
inbox := nats.NewInbox()
sub, _ := nc.SubscribeSync(inbox)
nc.PublishRequest("hive.node.status", inbox, nil)
for {
    msg, err := sub.NextMsg(remaining)
    if err != nil { break }
    // unmarshal and append
}
```

### ResourceManager.GetResourceStats()

New method that returns total/allocated vCPU and memory plus per-instance-type available capacity in a single lock acquisition. All data was already tracked — no new system calls needed.

## Files Modified

| File | Change |
|------|--------|
| `hive/config/config.go` | Added `NodeStatusResponse`, `InstanceTypeCap`, `VMInfo`, `NodeVMsResponse` types |
| `hive/admin/admin.go` | Added `RemoteNode` type and `RemoteNodes` field to `ConfigSettings` |
| `hive/daemon/daemon_handlers.go` | Added `daemonIP()`, `handleNodeStatus()`, `handleNodeVMs()` handlers |
| `hive/daemon/daemon.go` | Added `hive.node.status` and `hive.node.vms` subscriptions in `subscribeAll()`, added `GetResourceStats()` method to `ResourceManager` |
| `cmd/hive/cmd/admin.go` | Added `buildRemoteNodes()` helper; populate `RemoteNodes` in init and join flows |
| `cmd/hive/cmd/templates/hive.toml` | Added `host` to local node, template loop for remote nodes |
| `cmd/hive/cmd/get.go` | **New file** — `get nodes`, `get vms` commands with pterm table output |
| `cmd/hive/cmd/top.go` | **New file** — `top nodes` command with node resource + instance type capacity tables |
| `tests/e2e/run-e2e.sh` | Added Phase 1b (get nodes, top nodes, get vms empty) and Phase 5a-pre (get vms with running VM) |
| `tests/e2e/run-multinode-e2e.sh` | Added Phase 3b (multi-node get/top) and post-launch get vms verification |

## Testing

- `make preflight` — all checks pass (gofmt, vet, gosec, staticcheck, govulncheck, unit tests)
- `make test-docker-single` — single-node E2E passes with new CLI test phases
- `make test-docker-multi` — 3-node E2E passes, `get vms` correctly shows instances across nodes with correct IPs

### E2E test coverage added

- `hive get nodes` shows Ready nodes with correct IP, region, AZ, services
- `hive top nodes` shows CPU/MEM stats and instance type capacity table
- `hive get vms` returns "No VMs found" when empty
- `hive get vms` shows running instances with correct node placement after launch
- Multi-node: VMs distributed across nodes are all visible in `get vms` output
