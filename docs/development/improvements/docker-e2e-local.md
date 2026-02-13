# Fix Docker E2E Tests and Enable Local Reproduction

**Status: Complete**

## Summary

Both CI E2E tests (single-node and multi-node) are failing after recent multi-server changes. This improvement fixes the root causes and adds a local runner script so developers can reproduce E2E tests without pushing to CI.

## Context / Problem Statement

### Single-Node E2E Failure

**Symptom:** `DescribeInstanceTypes` returns empty — logs show "nats: no responders available for request".

**Root cause:** Race condition. The E2E script waits for the gateway HTTP endpoint (`curl -s https://localhost:9999`), but this only confirms the gateway process is listening — not that the daemon has connected to NATS and subscribed to topics.

Daemon startup path: `connectNATS()` -> `ClusterManager()` -> `initJetStream()` -> create services -> `restoreInstances()` -> `subscribeAll()`. If the gateway accepts requests before the daemon finishes `subscribeAll()`, NATS publishes find no subscribers.

Note: `DescribeRegions` and `DescribeAvailabilityZones` succeed because they're handled locally in the gateway (`gateway/ec2.go`) without NATS. `DescribeInstanceTypes` is the first action that requires daemon NATS subscriptions.

### Multi-Node E2E Failure

**Symptom:** Formation server waits 10 minutes for nodes to join, then times out (1/3 nodes joined).

**Root cause:** `init_leader_node()` calls `hive admin init` without explicit `--nodes` flag. The default is `--nodes 3` (`admin.go:156`). Combined with `--bind 10.11.12.1` (not `0.0.0.0`), this triggers the multi-node formation path:

```go
isMultiNode := nodes >= 2 && bindIP != "0.0.0.0"
```

The formation server starts and blocks at `WaitForCompletion(formationTimeout)` waiting for 2 more nodes. But the E2E script is sequential — `join_follower_node 2` and `3` run AFTER `init_leader_node` returns, creating a deadlock.

Additionally, `hive admin join` also blocks — it polls `/formation/status` until `complete: true` (all nodes joined). So even if init were backgrounded, running join_2 then join_3 sequentially would deadlock because join_2 blocks waiting for node_3.

### Timing of CA cert and config files

The `runAdminInit` flow (`admin.go`):
1. **Cert generation** (`admin.GenerateCertificatesIfNeeded`) — happens BEFORE multi-node check
2. **Multi-node path** starts formation server, blocks at `WaitForCompletion`
3. **Config generation** happens AFTER all nodes join

This means:
- `~/node1/config/ca.pem` — available as soon as formation server starts (generated beforehand)
- `~/node1/config/hive.toml` and other configs — only available after ALL nodes join

## Proposed Changes

### 1. Single-node fix: daemon readiness check

Added `wait_for_daemon_ready()` helper in `multinode-helpers.sh` that polls `describe-instance-types` until it returns a non-empty result. Called after gateway health check in both E2E scripts.

### 2. Multi-node fix: concurrent formation orchestration

Restructured `init_leader_node()` to background the init process and poll for formation server readiness. Restructured Phase 2 of `run-multinode-e2e.sh` to:
1. Background init (starts formation server, generates certs first)
2. Trust CA cert (exists before formation completes)
3. Background BOTH joins concurrently (they poll `/formation/status`)
4. Wait for all three processes to finish
5. Start services after formation completes (configs now exist)

This mirrors production where init and joins run on separate machines simultaneously.

### 3. Local E2E runner script

Created `scripts/run-e2e-local.sh` that mirrors the CI workflow (`e2e.yml`) for local Docker E2E testing. Supports running single-node, multi-node, or both suites.

## Files to Modify

| File | Change |
|---|---|
| `tests/e2e/lib/multinode-helpers.sh` | Added `wait_for_daemon_ready()`, restructured `init_leader_node()` to background init and poll formation health |
| `tests/e2e/run-e2e.sh` | Added `wait_for_daemon_ready` call after gateway wait |
| `tests/e2e/run-multinode-e2e.sh` | Restructured Phase 2 for concurrent formation, added `wait_for_daemon_ready` |
| `scripts/run-e2e-local.sh` | New local Docker E2E runner script |
| `.claude/CLAUDE.md` | Added git history guidance to Development Process section |

## Testing

1. **Multi-node fix:** Run `./scripts/run-e2e-local.sh multi` — formation server starts, all 3 joins complete concurrently, configs generated, services start, tests pass
2. **Single-node fix:** Run `./scripts/run-e2e-local.sh single` — daemon readiness check passes, `describe-instance-types` returns instance types
3. **Full local E2E:** Run `./scripts/run-e2e-local.sh` — both suites pass
4. **CI validation:** Push and verify GitHub Actions E2E workflow passes
