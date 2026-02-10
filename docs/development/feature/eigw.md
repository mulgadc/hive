# Egress-Only Internet Gateway (EIGW)

## Overview

Egress-Only Internet Gateways provide outbound-only IPv6 connectivity for instances in a VPC. In Hive, EIGWs are lightweight metadata objects persisted in NATS JetStream KV. The implementation covers the three standard EC2 API operations: Create, Delete, and Describe.

## Key Design Decisions

### 1. NATS JetStream KV for persistence

EIGW records are stored in a dedicated KV bucket (`hive-eigw`) with 10-revision history. This avoids introducing a database dependency -- the same NATS infrastructure used for messaging also handles persistence.

The `getOrCreateKVBucket` helper tries `CreateKeyValue` first, then falls back to `KeyValue` (open existing). This makes the service idempotent across restarts.

**Trade-off**: KV is not suited for complex queries (e.g. filtering by tag values). Describe currently iterates all keys and filters in-memory, which is fine for typical EIGW counts but wouldn't scale to thousands of records.

### 2. Constructor fails loudly, daemon falls back gracefully

`NewEgressOnlyIGWServiceImplWithNATS` returns an error if the KV bucket can't be created or opened. This ensures the service never operates with a nil KV handle, eliminating an entire class of nil-pointer bugs.

The daemon handles this with a graceful fallback:

```go
if eigwSvc, err := handlers_ec2_eigw.NewEgressOnlyIGWServiceImplWithNATS(d.config, d.natsConn); err != nil {
    slog.Warn("Failed to initialize EIGW service, falling back to in-memory", "error", err)
    d.eigwService = handlers_ec2_eigw.NewEgressOnlyIGWServiceImpl(d.config)
} else {
    d.eigwService = eigwSvc
}
```

**Trade-off**: The in-memory fallback (`NewEgressOnlyIGWServiceImpl`) has no KV backing, so EIGW state won't survive daemon restarts. This is acceptable for degraded mode -- the alternative is refusing to start the daemon entirely.

### 3. crypto/rand for ID generation

EIGW IDs (`eigw-` + 16 hex chars) are generated using `crypto/rand` + `encoding/hex`. This matches the pattern used by instance and volume ID generation elsewhere in the codebase.

```go
func generateEgressOnlyIGWID() string {
    b := make([]byte, 8)
    if _, err := rand.Read(b); err != nil {
        panic("crypto/rand failed: " + err.Error())
    }
    return "eigw-" + hex.EncodeToString(b)
}
```

The panic on `crypto/rand` failure is intentional -- if the OS entropy source is broken, the system has larger problems.

### 4. Error strings are awserrors constants

All errors returned from `service_impl.go` use `errors.New(awserrors.ErrorXxx)` with exact constant values. This is critical because the error string travels through a round-trip:

```
service_impl: errors.New(awserrors.ErrorMissingParameter)
  -> daemon handleNATSRequest: utils.GenerateErrorPayload(err.Error())
    -> JSON over NATS: {"Code": "MissingParameter"}
      -> gateway NATSRequest[Out]: ValidateErrorPayload extracts Code
        -> gateway ErrorHandler: awserrors.ErrorLookup[err.Error()] -> HTTP status + XML
```

If the error string doesn't match a key in `ErrorLookup`, the gateway returns a generic 500. Using `fmt.Errorf` with extra context would break this chain.

### 5. Centralized nil NATS check in the gateway

Rather than checking `natsConn == nil` in each gateway function, a single check in `EC2_Request` covers all NATS-dependent actions. An allowlist (`ec2LocalActions`) exempts actions that work without NATS:

```go
var ec2LocalActions = map[string]bool{
    "DescribeRegions":           true,
    "DescribeAvailabilityZones": true,
    "DescribeAccountAttributes": true,
}
```

If `gw.NATSConn` is nil and the action isn't in the allowlist, the gateway returns `ErrorServerInternal` immediately.

### 6. Tag filtering by resource type

Create accepts `TagSpecifications` but only applies tags where `ResourceType` equals `"egress-only-internet-gateway"`. Tags with other resource types (e.g. `"instance"`) are silently ignored, matching AWS behavior.

## Request Flow

```
AWS SDK (create-egress-only-internet-gateway --vpc-id vpc-xxx)
  -> Gateway (port 9999): ec2Handler parses AWS query params into SDK struct
    -> Validate input (nil check, required fields)
    -> NATS ec2.CreateEgressOnlyInternetGateway: routed to daemon via "hive-workers" queue group
      -> Daemon handleEC2CreateEgressOnlyInternetGateway
        -> handleNATSRequest: unmarshal -> EgressOnlyIGWServiceImpl.Create -> marshal -> respond
          1. Validate VpcId is present
          2. Generate eigw-{16hex} ID via crypto/rand
          3. Build EgressOnlyIGWRecord with state "attached"
          4. Process matching tag specifications
          5. JSON marshal and store in NATS KV (hive-eigw bucket)
        <- NATS response: ec2.CreateEgressOnlyInternetGatewayOutput JSON
    <- Gateway: wrap in XML (CreateEgressOnlyInternetGatewayResponse)
  <- AWS SDK: response
```

## NATS Topics

| Topic | Direction | Purpose |
|-------|-----------|---------|
| `ec2.CreateEgressOnlyInternetGateway` | Gateway -> Daemon | Create new EIGW attached to a VPC |
| `ec2.DeleteEgressOnlyInternetGateway` | Gateway -> Daemon | Delete EIGW by ID |
| `ec2.DescribeEgressOnlyInternetGateways` | Gateway -> Daemon | List/filter EIGWs |

All topics use the `hive-workers` queue group for load balancing across daemon instances.

## Storage Layout

```
NATS JetStream KV bucket: hive-eigw (history: 10)

Key: eigw-a1b2c3d4e5f6g7h8
Value: {
  "egress_only_internet_gateway_id": "eigw-a1b2c3d4e5f6g7h8",
  "vpc_id": "vpc-abc123",
  "state": "attached",
  "tags": {"Name": "my-eigw", "Env": "prod"},
  "created_at": "2026-02-10T12:00:00Z"
}
```

## Files Changed

| File | Change |
|------|--------|
| `hive/handlers/ec2/eigw/service.go` | `EgressOnlyIGWService` interface (3 methods) |
| `hive/handlers/ec2/eigw/service_impl.go` | Direct implementation with NATS JetStream KV persistence |
| `hive/handlers/ec2/eigw/service_impl_test.go` | 12 test cases against embedded NATS server |
| `hive/handlers/ec2/eigw/service_nats.go` | NATS proxy (gateway -> daemon via `utils.NATSRequest`) |
| `hive/gateway/ec2/eigw/CreateEgressOnlyInternetGateway.go` | Gateway validation + handler for Create |
| `hive/gateway/ec2/eigw/DeleteEgressOnlyInternetGateway.go` | Gateway validation + handler for Delete |
| `hive/gateway/ec2/eigw/DescribeEgressOnlyInternetGateways.go` | Gateway validation + handler for Describe |
| `hive/gateway/ec2/eigw/eigw_test.go` | Gateway validation tests (9 cases) |
| `hive/gateway/ec2.go` | 3 entries in `ec2Actions` map; centralized nil NATS check |
| `hive/daemon/daemon.go` | `eigwService` field, 3 NATS subscriptions, service initialization with fallback |
| `hive/daemon/daemon_handlers.go` | 3 handler functions delegating to `handleNATSRequest` |
| `hive/awserrors/awserrors.go` | `ErrorInvalidEgressOnlyInternetGatewayId.Malformed` and `.NotFound` constants + lookup entries |

## Test Coverage

### Service Implementation Tests (`service_impl_test.go`)

| Test | What it validates |
|------|-------------------|
| `TestCreateEgressOnlyInternetGateway` | Basic create: ID format, VPC attachment, state |
| `TestCreateEgressOnlyInternetGateway_MissingVpcId` | Rejects nil VpcId with MissingParameter |
| `TestCreateEgressOnlyInternetGateway_EmptyVpcId` | Rejects empty string VpcId |
| `TestCreateEgressOnlyInternetGateway_WithTags` | Tags applied and persisted through describe round-trip |
| `TestCreateEgressOnlyInternetGateway_TagsWrongResourceType` | Tags with non-matching resource type are ignored |
| `TestDeleteEgressOnlyInternetGateway` | Delete succeeds, describe confirms removal |
| `TestDeleteEgressOnlyInternetGateway_NotFound` | Returns NotFound for nonexistent ID |
| `TestDeleteEgressOnlyInternetGateway_MissingID` | Rejects nil ID with MissingParameter |
| `TestDeleteEgressOnlyInternetGateway_EmptyID` | Rejects empty string ID |
| `TestDescribeEgressOnlyInternetGateways_All` | Lists all EIGWs when no filter specified |
| `TestDescribeEgressOnlyInternetGateways_ByID` | Filters by specific EIGW ID |
| `TestDescribeEgressOnlyInternetGateways_Empty` | Returns empty list for empty bucket |

All tests run against an embedded NATS server with JetStream enabled.

### Gateway Validation Tests (`eigw_test.go`)

| Test | What it validates |
|------|-------------------|
| `ValidateCreateEgressOnlyInternetGatewayInput` | nil input, missing VpcId, empty VpcId, valid input |
| `ValidateDeleteEgressOnlyInternetGatewayInput` | nil input, missing ID, empty ID, valid input |
| `ValidateDescribeEgressOnlyInternetGatewaysInput` | nil input, empty input, with filter IDs |

## AWS CLI Usage

```bash
# Create
aws ec2 create-egress-only-internet-gateway \
  --vpc-id vpc-abc123 \
  --tag-specifications 'ResourceType=egress-only-internet-gateway,Tags=[{Key=Name,Value=my-eigw}]' \
  --endpoint-url https://localhost:9999

# Describe all
aws ec2 describe-egress-only-internet-gateways \
  --endpoint-url https://localhost:9999

# Describe by ID
aws ec2 describe-egress-only-internet-gateways \
  --egress-only-internet-gateway-ids eigw-a1b2c3d4e5f6g7h8 \
  --endpoint-url https://localhost:9999

# Delete
aws ec2 delete-egress-only-internet-gateway \
  --egress-only-internet-gateway-id eigw-a1b2c3d4e5f6g7h8 \
  --endpoint-url https://localhost:9999
```
