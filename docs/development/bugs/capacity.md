# RunInstances capacity routing via per-instance-type NATS subscriptions

**Status:** Implemented (local tests pass, pending remote verification)

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

```
AWS SDK → Gateway → NATS "ec2.RunInstances" [queue: hive-workers] → ONE daemon (random)
                                                                        ↓
                                                          canAllocate() fails
                                                                        ↓
                                                      InsufficientInstanceCapacity
                                                                        ↓
                                                    Gateway returns error to client
```

There is no capacity-aware routing. NATS blindly round-robins to any daemon, regardless of whether it can serve the requested instance type.

## Solution: per-instance-type NATS topics with dynamic subscription

Leverage NATS subscription semantics directly: each daemon subscribes to `ec2.RunInstances.{instanceType}` only for types it has capacity to serve. When a node fills up for a given type, it unsubscribes. When capacity is freed, it re-subscribes.

This means NATS itself becomes the capacity-aware router — requests are only delivered to nodes that can serve them. No retry logic, no discovery, no hacks.

### Fixed NATS topic flow

```
Gateway → "ec2.RunInstances.t3.medium" [queue: hive-workers] → only daemons with capacity
  → Daemon receives (guaranteed to have capacity) → allocates → launches VM
  → Daemon recalculates: t3.medium no longer fits → unsubscribes from ec2.RunInstances.t3.medium
  → Other daemons still subscribed → next request routed to them

All nodes full for t3.medium:
Gateway → "ec2.RunInstances.t3.medium" → no subscribers → NATS returns ErrNoResponders
  → Gateway maps ErrNoResponders to InsufficientInstanceCapacity → returns to client
```

### How dynamic subscription works

Each daemon maintains a set of per-instance-type NATS subscriptions. After every resource change (allocate or deallocate), it recalculates which types can fit at least 1 instance:

```
Node starts (8 vCPUs, 32 GB free):
  → subscribes to: ec2.RunInstances.t3.nano, .micro, .small, .medium, .large, .xlarge, .2xlarge
                    ec2.RunInstances.m8a.nano, .micro, .small, .medium, .large, .xlarge, .2xlarge

After launching t3.2xlarge (8 vCPU, 32 GB → 0 free):
  → unsubscribes from ALL ec2.RunInstances.* types (nothing fits)

After terminating t3.2xlarge (resources freed → 8 vCPU, 32 GB free again):
  → re-subscribes to all 14 instance types
```

### Race condition handling

There's a small window between a subscription existing and resources being allocated where two requests for the last available slot could arrive at the same node. This is handled naturally:

1. Request A arrives → daemon allocates → success → recalculates subscriptions
2. Request B arrives before unsubscribe completes → daemon tries to allocate → `canAllocate()` returns 0
3. Daemon responds with `InsufficientInstanceCapacity` (same as today)

This is an edge case that only occurs when a node is at exact capacity boundary with concurrent requests. The existing `canAllocate()` check remains as a safety net. This is acceptable — the alternative (distributed locking) adds complexity far exceeding the rare failure.

## Implementation

### Step 1: Gateway publishes to instance-type-specific topic

**File:** `hive/handlers/ec2/instance/service_nats.go`

Change:
```go
func (s *NATSInstanceService) RunInstances(input *ec2.RunInstancesInput) (*ec2.Reservation, error) {
    topic := fmt.Sprintf("ec2.RunInstances.%s", aws.StringValue(input.InstanceType))
    return utils.NATSRequest[ec2.Reservation](s.natsConn, topic, input, 5*time.Minute)
}
```

### Step 2: Gateway maps ErrNoResponders to InsufficientInstanceCapacity

**File:** `hive/utils/nats.go`

Update `NATSRequest` to detect `nats.ErrNoResponders` and return a specific error the gateway can map to `InsufficientInstanceCapacity`:

```go
msg, err := conn.Request(subject, jsonData, timeout)
if err != nil {
    if errors.Is(err, nats.ErrNoResponders) {
        return nil, fmt.Errorf("no responders for subject %s: %w", subject, nats.ErrNoResponders)
    }
    return nil, fmt.Errorf("NATS request failed: %w", err)
}
```

**File:** `hive/gateway/ec2/instance/RunInstances.go`

Map the no-responders error to the AWS-compatible error:
```go
reservationPtr, err := service.RunInstances(input)
if err != nil {
    if errors.Is(err, nats.ErrNoResponders) {
        return reservation, errors.New(awserrors.ErrorInsufficientInstanceCapacity)
    }
    return reservation, err
}
```

### Step 3: Add subscription management to ResourceManager

**File:** `hive/daemon/daemon.go`

Add to `ResourceManager`:
```go
type ResourceManager struct {
    // ... existing fields ...
    natsConn    *nats.Conn
    node        string
    instanceSubs map[string]*nats.Subscription  // topic → subscription
    handler      nats.MsgHandler                // RunInstances handler
}
```

Add methods:
```go
// updateInstanceSubscriptions recalculates which instance types can fit
// and subscribes/unsubscribes accordingly. Called after every allocate/deallocate.
func (rm *ResourceManager) updateInstanceSubscriptions() {
    rm.mu.RLock()
    defer rm.mu.RUnlock()

    for typeName, typeInfo := range rm.instanceTypes {
        topic := fmt.Sprintf("ec2.RunInstances.%s", typeName)
        canFit := rm.canAllocateUnlocked(typeInfo, 1) >= 1

        _, subscribed := rm.instanceSubs[topic]
        if canFit && !subscribed {
            sub, err := rm.natsConn.QueueSubscribe(topic, "hive-workers", rm.handler)
            if err != nil {
                slog.Error("Failed to subscribe to instance type topic", "topic", topic, "err", err)
                continue
            }
            rm.instanceSubs[topic] = sub
            slog.Info("Subscribed to instance type", "topic", topic)
        } else if !canFit && subscribed {
            rm.instanceSubs[topic].Unsubscribe()
            delete(rm.instanceSubs, topic)
            slog.Info("Unsubscribed from instance type (capacity full)", "topic", topic)
        }
    }
}
```

### Step 4: Call updateInstanceSubscriptions after allocate/deallocate

**File:** `hive/daemon/daemon.go`

Update `allocate()` to recalculate subscriptions after reserving resources:
```go
func (rm *ResourceManager) allocate(instanceType *ec2.InstanceTypeInfo) error {
    // ... existing allocation logic ...
    rm.updateInstanceSubscriptions()
    return nil
}
```

Update `deallocate()` to recalculate subscriptions after freeing resources:
```go
func (rm *ResourceManager) deallocate(instanceType *ec2.InstanceTypeInfo) {
    // ... existing deallocation logic ...
    rm.updateInstanceSubscriptions()
}
```

### Step 5: Remove static ec2.RunInstances subscription from subscribeAll()

**File:** `hive/daemon/daemon.go`

Remove `{"ec2.RunInstances", d.handleEC2RunInstances, "hive-workers"}` from the `subscribeAll()` table. The initial subscriptions are created by `updateInstanceSubscriptions()` called during daemon startup (after `NewResourceManager()` sets up the handler).

### Step 6: Initialize subscription management on daemon start

**File:** `hive/daemon/daemon.go`

After `subscribeAll()` in `Start()`, initialize the instance type subscriptions:
```go
d.resourceMgr.handler = d.handleEC2RunInstances
d.resourceMgr.natsConn = d.natsConn
d.resourceMgr.node = d.node
d.resourceMgr.instanceSubs = make(map[string]*nats.Subscription)
d.resourceMgr.updateInstanceSubscriptions()
```

### Step 7: Update tests

**File:** `hive/daemon/daemon_test.go`

- Update tests that publish to `ec2.RunInstances` to publish to `ec2.RunInstances.{instanceType}` instead
- Add test: verify that after filling a node, it unsubscribes from the filled type
- Add test: verify that after deallocating, it re-subscribes
- Add test: verify `ErrNoResponders` when no node has capacity

**File:** `hive/handlers/ec2/instance/service_nats_test.go` (if exists)

- Update topic assertions

## Files summary

| File | Change |
|------|--------|
| `hive/handlers/ec2/instance/service_nats.go` | Publish to `ec2.RunInstances.{instanceType}` |
| `hive/utils/nats.go` | Preserve `ErrNoResponders` in error chain |
| `hive/gateway/ec2/instance/RunInstances.go` | Map `ErrNoResponders` → `InsufficientInstanceCapacity` |
| `hive/daemon/daemon.go` | Add subscription management to ResourceManager, remove static subscription |
| `hive/daemon/daemon_test.go` | Update topics, add subscribe/unsubscribe tests |

## Existing behavior preserved

- `canAllocate()` still runs as a safety net (handles race conditions)
- Instance type validation (`instanceType, exists := d.resourceMgr.instanceTypes[...]`) still runs
- All other NATS subscriptions unchanged
- DescribeInstanceTypes still uses fan-out (no queue group) — unchanged

## Verification

1. `make test` — all tests pass
2. `make build` — compiles
3. 3-node cluster test:
   - Start cluster, all nodes subscribe to all 14 instance types
   - Fill node1 with t3.2xlarge — node1 unsubscribes from all types
   - `RunInstances t3.medium` → NATS routes to node2 or node3 (never node1)
   - Fill all nodes → `RunInstances` returns `InsufficientInstanceCapacity` (NATS ErrNoResponders)
   - Terminate instance on node1 → node1 re-subscribes → next RunInstances succeeds
