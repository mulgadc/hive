# Phase 2.1 Heartbeats + Phase 2.4 Coordinated Cluster Shutdown

**Status: Complete**

## Summary

Implemented daemon heartbeats (Phase 2.1) and coordinated cluster shutdown (Phase 2.4). Heartbeats publish node status to KV every 10s. Coordinated shutdown drives a phased shutdown across all nodes via NATS, replacing the error-prone per-node approach for multi-node clusters.

## Context / Problem Statement

- No cluster-wide health awareness — nodes don't know peer status
- Multi-node shutdown required manual per-node `stop-dev.sh`, which is error-prone and doesn't guarantee correct phase ordering (VMs must stop before storage, storage before NATS)
- Phase 0+1 (capabilities, recovery throttling, shutdown markers) were already complete

## Design Decisions

- **Option B chosen**: Daemons handle stopping local services internally via `utils.StopProcess()` when they receive NATS shutdown phase messages. The CLI coordinator just sends messages and collects ACKs.
- **Heartbeat interval**: 10 seconds — fast enough for health monitoring, low overhead
- **Shutdown phases**: GATE → DRAIN → STORAGE → PERSIST → INFRA — ordered to prevent data loss
- **INFRA phase is fire-and-forget**: NATS is going down, no ACK possible
- **Scripts**: `stop-dev.sh` auto-detects multi-node and delegates to `hive admin cluster shutdown`; `HIVE_FORCE_LOCAL_STOP=1` overrides for per-service stop

## Proposed Changes (Implemented)

### Heartbeat System
- `Heartbeat` struct in `jetstream.go` with node, epoch, services, VM count, resource allocation
- `WriteHeartbeat`/`ReadHeartbeat` KV methods on `JetStreamManager`
- `startHeartbeat()` goroutine in `heartbeat.go` — fires immediately, then every 10s
- `buildHeartbeat()` reads from `ResourceManager` and `Instances`

### Coordinated Shutdown
- Five shutdown phase handlers in `shutdown.go`: GATE, DRAIN, STORAGE, PERSIST, INFRA
- `ShutdownRequest`, `ShutdownACK`, `ShutdownProgress` message types
- `shuttingDown atomic.Bool` on Daemon — set during GATE, checked by `setupShutdown()` to skip redundant VM stops
- NATS subscriptions for `hive.cluster.shutdown.{phase}` (fan-out, no queue group)
- Progress updates published during DRAIN phase

### CLI Coordinator
- `hive admin cluster shutdown` command in `cluster.go`
- Flags: `--force`, `--timeout`, `--dry-run`
- Executes phases sequentially, collecting ACKs with timeout
- Writes `ClusterShutdownState` to KV for observability
- Live progress display during DRAIN phase

### Script Updates
- `stop-dev.sh`: `is_multinode()` detection, delegates to coordinated shutdown
- `start-dev.sh`: `is_multinode()` detection, cluster health poll at end

## Files Modified

| File | Action | Description |
|------|--------|-------------|
| `hive/daemon/jetstream.go` | Edit | `Heartbeat`, `ClusterShutdownState` structs + KV methods |
| `hive/daemon/heartbeat.go` | New | `startHeartbeat()`, `buildHeartbeat()` goroutine |
| `hive/daemon/shutdown.go` | New | Shutdown types + 5 phase handlers |
| `hive/daemon/daemon.go` | Edit | `shuttingDown atomic.Bool`, shutdown subscriptions, `startHeartbeat()`, updated `setupShutdown()` |
| `cmd/hive/cmd/admin.go` | Edit | `clusterCmd` + `clusterShutdownCmd` registration |
| `cmd/hive/cmd/cluster.go` | New | `runClusterShutdown()` coordinator |
| `scripts/stop-dev.sh` | Edit | Multi-node detection + coordinated shutdown delegation |
| `scripts/start-dev.sh` | Edit | Multi-node detection + cluster health poll |
| `hive/daemon/heartbeat_test.go` | New | Heartbeat tests (build, allocation, KV round-trip) |
| `hive/daemon/shutdown_test.go` | New | Shutdown tests (marshal, KV round-trip, flag behavior) |

## Testing

- `make preflight` — all checks pass (gofmt, go vet, gosec, staticcheck, govulncheck, all tests)
- 9 new unit tests covering heartbeat building, resource reflection, KV round-trips, shutdown message marshaling, and shutdown state management
- `hive admin cluster shutdown --dry-run` prints phase plan
- `stop-dev.sh` on multi-node delegates to coordinated shutdown
- `HIVE_FORCE_LOCAL_STOP=1 stop-dev.sh` on multi-node uses per-service stop
- `stop-dev.sh` on single-node uses per-service stop (unchanged behavior)

## Future Work

- Phase 2.2: Heartbeat-driven health monitoring (stale heartbeat detection, node failure alerts)
- Phase 2.3: Automatic VM migration on node failure
- Integration with `hive top` to display heartbeat data
- Cluster shutdown progress bar in UI
