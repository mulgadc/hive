# RunInstances capacity routing does not retry other nodes

## Problem

When `RunInstances` is called, the gateway publishes to NATS topic `ec2.RunInstances` with queue group `hive-workers`. NATS round-robins the request to one daemon. If that daemon has insufficient capacity, it returns `InsufficientInstanceCapacity` and the gateway passes the error to the client. No other node is tried.

This means a cluster with available capacity can reject instance launches if the randomly-selected node happens to be full.

### Observed behavior (3-node cluster)

Node 3 already runs a `t3.medium` (2 vCPUs, 4 GB) and has no remaining capacity:

```
# First attempt — NATS routes to node3 (full) → error
$ aws ec2 run-instances --image-id $HIVE_AMI --instance-type t3.medium ...
An error occurred (InsufficientInstanceCapacity) when calling the RunInstances operation: None

# Second attempt — NATS routes to node1 or node2 (free) → success
$ aws ec2 run-instances --image-id $HIVE_AMI --instance-type t3.medium ...
{ "Instances": [{ "InstanceId": "i-6c97c523f1ead0eef", "State": { "Name": "pending" } }] }
```

The user must retry manually until NATS happens to pick a node with capacity.

### Why this happens

The request flow is fire-and-forget from the gateway's perspective:

```
AWS SDK → Gateway → NATS "ec2.RunInstances" [queue: hive-workers] → ONE daemon
                                                                        ↓
                                                          canAllocate() fails
                                                                        ↓
                                                      InsufficientInstanceCapacity
                                                                        ↓
                                                    Gateway returns error to client
```

There is no retry, no fallback, and no capacity-aware routing. The gateway (`service_nats.go`) does a single `NATSRequest` and returns whatever the daemon responds with.

### Current code path

1. **Gateway** `ec2.go:54` → calls `RunInstances(input, gw.NATSConn)`
2. **Gateway** `RunInstances.go:66` → creates `NATSInstanceService`, calls `service.RunInstances(input)`
3. **Service** `service_nats.go:22` → `NATSRequest(conn, "ec2.RunInstances", input, 5*time.Minute)` — single NATS request-reply
4. **NATS** routes to one daemon in queue group `hive-workers`
5. **Daemon** `daemon_handlers.go:686` → `canAllocate(instanceType, maxCount)` → returns 0
6. **Daemon** `daemon_handlers.go:691` → responds with `InsufficientInstanceCapacity`
7. **Gateway** receives error, returns HTTP 503 to client

No retry logic exists anywhere in this chain.

## The per-instance-type unsubscribe idea (and why it doesn't work cleanly)

One approach: when a node is full for `t3.medium`, it unsubscribes from `ec2.RunInstances` so NATS won't route requests to it. But capacity is shared across instance types — a node with 2 free vCPUs and 2 GB free RAM can run a `t3.small` (2 vCPU, 2 GB) but not a `t3.medium` (2 vCPU, 4 GB). You can't unsubscribe from "RunInstances" globally without also blocking smaller instance types that would still fit.

Splitting into per-type topics (e.g., `ec2.RunInstances.t3.medium`) would fix this: unsubscribe from `t3.medium` when you can't fit one, while staying subscribed to `t3.small`. But this creates problems:

- With 14 instance types per node, each daemon manages 14 dynamic subscriptions
- Every `allocate` and `deallocate` must recalculate which types still fit and update subscriptions
- Race conditions: between the capacity check and NATS delivering the message, another request could consume the resources
- Gateway must know the instance type before publishing (it does — it's in the parsed request) but the topic routing becomes tightly coupled to the type catalog
- Adding new instance types requires matching subscription changes in daemon startup

This approach has merit for a future optimization (proactive capacity filtering) but doesn't solve the core problem: what happens when a node receives a request it can't serve?

## Proposed approach: daemon-side NATS retry

Instead of the daemon returning an error when it lacks capacity, have it **re-publish the request** so another node can try. This keeps the logic in the daemon layer (no gateway changes) and uses NATS naturally.

### How it works

1. Daemon receives `RunInstances` via queue group
2. `canAllocate()` returns 0 — not enough capacity
3. Instead of responding with error, daemon publishes a **`NAK`** (negative acknowledgment) back through NATS using a retry mechanism
4. NATS delivers to a different queue group member
5. If all nodes are full, the last node returns `InsufficientInstanceCapacity`

### Implementation: NATS request-retry pattern

The daemon doesn't respond to the original message. Instead, it re-publishes to the same topic with a retry header. NATS queue group semantics mean a different member will receive it (NATS avoids delivering to the same consumer consecutively when others are available).

```
Gateway → "ec2.RunInstances" [queue: hive-workers]
  → Node3 receives (full) → doesn't respond, publishes retry with node3 in "tried" list
  → Node1 receives (has capacity) → processes request, responds to gateway
```

The retry message includes:
- Original request payload
- Reply-to address from the original NATS message (so the response goes back to the gateway)
- List of nodes that already tried and failed (to detect exhaustion)

When all nodes are in the "tried" list, the last node responds with `InsufficientInstanceCapacity`.

### Key design details

**Retry metadata**: Attach a `Hive-Tried-Nodes` header to the NATS message. Each daemon that can't serve the request appends its node name before re-publishing. When `len(triedNodes) >= expectedNodes`, no capacity exists anywhere — return the error.

**Reply routing**: The re-published message must carry the original `msg.Reply` inbox address so the eventual response reaches the gateway. Use `natsConn.PublishMsg()` with the original reply subject.

**Timeout safety**: The gateway already sets a 5-minute timeout on the NATS request. If all nodes are slow or the retry loop takes too long, the gateway times out naturally. No additional timeout needed in the daemon.

**No infinite loops**: The tried-nodes list grows monotonically. Once it contains all nodes, the loop terminates. Maximum retries = number of nodes - 1.

### Pseudocode

```go
func (d *Daemon) handleEC2RunInstances(msg *nats.Msg) {
    // ... parse input, validate ...

    allocatableCount := d.resourceMgr.canAllocate(instanceType, maxCount)
    if allocatableCount < minCount {
        // Check if we can retry on another node
        triedNodes := getTriedNodes(msg) // from Hive-Tried-Nodes header
        triedNodes = append(triedNodes, d.node)

        if len(triedNodes) >= d.expectedNodes {
            // All nodes tried — no capacity anywhere
            msg.Respond(InsufficientInstanceCapacity)
            return
        }

        // Re-publish for another node to try
        retryMsg := &nats.Msg{
            Subject: "ec2.RunInstances",
            Data:    msg.Data,
            Reply:   msg.Reply, // preserve original reply-to
            Header:  nats.Header{},
        }
        retryMsg.Header.Set("Hive-Tried-Nodes", strings.Join(triedNodes, ","))
        d.natsConn.PublishMsg(retryMsg)
        return
    }

    // ... proceed with allocation and launch ...
}
```

### Why this approach

| Consideration | Daemon-side retry | Gateway retry | Per-type topics |
|---|---|---|---|
| Gateway changes | None | Significant | Moderate |
| Daemon changes | Small (retry logic) | None | Large (dynamic subs) |
| Latency (happy path) | Zero overhead | Zero overhead | Zero overhead |
| Latency (retry) | ~1ms per hop | ~1ms per retry + gateway logic | N/A (preemptive) |
| Race conditions | Same as current | Same as current | Subscription timing |
| Exhaustion detection | Tried-nodes header | Gateway tracks failures | Implicit (no subscribers) |
| NATS compatibility | Standard pub/sub | Requires node-specific topics | Standard but many topics |

Daemon-side retry is the simplest change: one function in the daemon, no gateway changes, no new NATS topic patterns. It also composes well — if per-type topic filtering is added later as an optimization, the retry logic still works as a safety net.

### Future optimization: proactive capacity filtering

Once the retry mechanism works, per-type subscriptions can be layered on top as a performance optimization to reduce unnecessary retries:

1. After each `allocate`/`deallocate`, recalculate which instance types can fit
2. Unsubscribe from types that no longer fit
3. Re-subscribe when capacity is freed

This is purely an optimization — it reduces the number of retry hops but isn't required for correctness. The retry mechanism handles the edge cases that subscription timing can't (e.g., two requests arriving simultaneously when only one fits).

## Files that would need changes

| File | Change |
|---|---|
| `hive/daemon/daemon_handlers.go` | Add retry logic to `handleEC2RunInstances` |
| `hive/daemon/daemon.go` | Add `expectedNodes` field to Daemon (from config) |
| `hive/daemon/daemon_test.go` | Test retry behavior: full node forwards, exhaustion returns error |

## Verification

1. 3-node cluster, fill one node to capacity
2. Run 10 `RunInstances` requests — all should succeed (routed to free nodes), none should return `InsufficientInstanceCapacity`
3. Fill all nodes — request should return `InsufficientInstanceCapacity` (not hang)
4. Stop an instance, retry — should succeed on the node that freed capacity
