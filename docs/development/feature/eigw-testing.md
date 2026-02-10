# Egress-Only Internet Gateway - Manual Testing Guide

## Prerequisites

- Hive gateway running on `https://localhost:9999`
- NATS server running with JetStream enabled
- At least one daemon instance connected
- AWS CLI configured with the Hive endpoint:

```bash
export HIVE="--endpoint-url https://localhost:9999 --no-verify-ssl"
```

All commands below use `$HIVE` as shorthand. Adjust if your endpoint differs.

## 1. Create an Egress-Only Internet Gateway

### 1a. Basic create

```bash
aws ec2 create-egress-only-internet-gateway \
  --vpc-id vpc-test001 \
  $HIVE
```

**Expected**: Returns JSON with `EgressOnlyInternetGateway` containing:
- `EgressOnlyInternetGatewayId` starting with `eigw-`
- `Attachments[0].VpcId` = `vpc-test001`
- `Attachments[0].State` = `attached`

Save the ID for subsequent tests:

```bash
EIGW_ID=$(aws ec2 create-egress-only-internet-gateway \
  --vpc-id vpc-test001 \
  --query 'EgressOnlyInternetGateway.EgressOnlyInternetGatewayId' \
  --output text \
  $HIVE)
echo $EIGW_ID
```

### 1b. Create with inline tags

```bash
aws ec2 create-egress-only-internet-gateway \
  --vpc-id vpc-test002 \
  --tag-specifications 'ResourceType=egress-only-internet-gateway,Tags=[{Key=Name,Value=my-eigw},{Key=Env,Value=staging}]' \
  $HIVE
```

**Expected**: Response includes `Tags` array with both Name and Env tags.

### 1c. Create with wrong resource type in tags (should be ignored)

```bash
aws ec2 create-egress-only-internet-gateway \
  --vpc-id vpc-test003 \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=should-be-ignored}]' \
  $HIVE
```

**Expected**: EIGW created successfully but `Tags` is empty (tags with resource type `instance` are silently skipped).

### 1d. Missing VpcId (error case)

```bash
aws ec2 create-egress-only-internet-gateway \
  $HIVE
```

**Expected**: Error response with `MissingParameter`.

## 2. Describe Egress-Only Internet Gateways

### 2a. Describe all

```bash
aws ec2 describe-egress-only-internet-gateways \
  $HIVE
```

**Expected**: Returns all EIGWs created in previous steps (at least 3 if all create tests ran).

### 2b. Describe by specific ID

```bash
aws ec2 describe-egress-only-internet-gateways \
  --egress-only-internet-gateway-ids $EIGW_ID \
  $HIVE
```

**Expected**: Returns exactly 1 EIGW matching the ID.

### 2c. Describe with multiple IDs

```bash
EIGW_ID2=$(aws ec2 create-egress-only-internet-gateway \
  --vpc-id vpc-multi \
  --query 'EgressOnlyInternetGateway.EgressOnlyInternetGatewayId' \
  --output text \
  $HIVE)

aws ec2 describe-egress-only-internet-gateways \
  --egress-only-internet-gateway-ids $EIGW_ID $EIGW_ID2 \
  $HIVE
```

**Expected**: Returns exactly 2 EIGWs.

### 2d. Describe nonexistent ID

```bash
aws ec2 describe-egress-only-internet-gateways \
  --egress-only-internet-gateway-ids eigw-doesnotexist \
  $HIVE
```

**Expected**: Returns empty `EgressOnlyInternetGateways` list (no error -- AWS behavior for describe with filter).

## 3. Delete an Egress-Only Internet Gateway

### 3a. Delete existing EIGW

```bash
aws ec2 delete-egress-only-internet-gateway \
  --egress-only-internet-gateway-id $EIGW_ID \
  $HIVE
```

**Expected**: `ReturnCode` = `true`.

### 3b. Verify deletion

```bash
aws ec2 describe-egress-only-internet-gateways \
  --egress-only-internet-gateway-ids $EIGW_ID \
  $HIVE
```

**Expected**: Empty `EgressOnlyInternetGateways` list.

### 3c. Delete nonexistent EIGW (error case)

```bash
aws ec2 delete-egress-only-internet-gateway \
  --egress-only-internet-gateway-id eigw-doesnotexist \
  $HIVE
```

**Expected**: Error response with `InvalidEgressOnlyInternetGatewayId.NotFound`.

### 3d. Delete with missing ID (error case)

```bash
aws ec2 delete-egress-only-internet-gateway \
  --egress-only-internet-gateway-id "" \
  $HIVE
```

**Expected**: Error response with `MissingParameter`.

## 4. Generic Tags Integration

The generic CreateTags/DeleteTags/DescribeTags APIs work with EIGW resource IDs (`eigw-` prefix is recognized as resource type `egress-only-internet-gateway`).

### 4a. Add tags to an existing EIGW

```bash
EIGW_TAG=$(aws ec2 create-egress-only-internet-gateway \
  --vpc-id vpc-tagtest \
  --query 'EgressOnlyInternetGateway.EgressOnlyInternetGatewayId' \
  --output text \
  $HIVE)

aws ec2 create-tags \
  --resources $EIGW_TAG \
  --tags Key=Team,Value=platform Key=Cost,Value=free \
  $HIVE
```

**Expected**: No error, empty response body (success).

### 4b. Describe tags for the EIGW

```bash
aws ec2 describe-tags \
  --filters "Name=resource-id,Values=$EIGW_TAG" \
  $HIVE
```

**Expected**: Returns 2 tag descriptions, each with:
- `ResourceId` = the EIGW ID
- `ResourceType` = `egress-only-internet-gateway`
- Correct Key/Value pairs

### 4c. Filter tags by resource type

```bash
aws ec2 describe-tags \
  --filters "Name=resource-type,Values=egress-only-internet-gateway" \
  $HIVE
```

**Expected**: Returns all tags on all EIGW resources.

### 4d. Delete a specific tag

```bash
aws ec2 delete-tags \
  --resources $EIGW_TAG \
  --tags Key=Cost \
  $HIVE
```

**Expected**: No error. Subsequent describe-tags should return only the Team tag.

### 4e. Verify tag deletion

```bash
aws ec2 describe-tags \
  --filters "Name=resource-id,Values=$EIGW_TAG" \
  $HIVE
```

**Expected**: Returns 1 tag (Team=platform only).

**Note**: Tags added via CreateTags (generic API, stored in S3/Predastore) and tags added via `--tag-specifications` at EIGW creation time (stored in NATS KV) are independent stores. The inline tags appear in `describe-egress-only-internet-gateways` output. The generic tags appear in `describe-tags` output. This is a known limitation -- unifying the two stores is future work.

## 5. Gateway-Level Checks

### 5a. Invalid action

```bash
curl -k -X POST https://localhost:9999/ \
  -d "Action=FakeEgressAction&Version=2016-11-15"
```

**Expected**: `InvalidAction` error.

### 5b. Nil NATS connection handling

If the daemon is stopped but the gateway is still running, all non-local EC2 actions (including EIGW operations) should return `InternalError` rather than panic. Local actions (`DescribeRegions`, `DescribeAvailabilityZones`, `DescribeAccountAttributes`) should still work:

```bash
# Should work without NATS
aws ec2 describe-regions $HIVE

# Should return InternalError without NATS
aws ec2 create-egress-only-internet-gateway --vpc-id vpc-test $HIVE
```

## 6. Persistence Across Restarts

### 6a. Create, restart daemon, verify data survives

```bash
# Create an EIGW
EIGW_PERSIST=$(aws ec2 create-egress-only-internet-gateway \
  --vpc-id vpc-persist \
  --query 'EgressOnlyInternetGateway.EgressOnlyInternetGatewayId' \
  --output text \
  $HIVE)

# Restart the daemon (not NATS)
# ... restart daemon process ...

# Verify EIGW still exists
aws ec2 describe-egress-only-internet-gateways \
  --egress-only-internet-gateway-ids $EIGW_PERSIST \
  $HIVE
```

**Expected**: EIGW is returned with the same ID, VPC, state, and tags. Data is persisted in the NATS JetStream KV bucket `hive-eigw`.

## Known Limitations

| Limitation | Detail |
|-----------|--------|
| No VPC validation | CreateEgressOnlyInternetGateway accepts any VpcId string without verifying the VPC exists. VPC support is not yet implemented in the main repo. |
| No Describe filters | DescribeEgressOnlyInternetGateways only supports filtering by ID. AWS filters (`attachment.state`, `attachment.vpc-id`, `tag:Key`) are not implemented. |
| Dual tag stores | Inline tags (via `--tag-specifications`) are stored in NATS KV as part of the EIGW record. Generic tags (via `create-tags`) are stored in S3/Predastore. The two are not synchronized. |
| No route table integration | Cannot create routes pointing to an EIGW as a target. Route table support is not yet in the main repo. |
| No pagination | DescribeEgressOnlyInternetGateways does not support `MaxResults`/`NextToken`. |
| In-memory fallback | If JetStream KV is unavailable at startup, the daemon falls back to an in-memory EIGW service that loses data on restart. |

## Required Fix Applied

**Tags service `eigw-` prefix**: The generic tags service (`hive/handlers/ec2/tags/service_impl.go`) did not recognize the `eigw-` prefix. Without this fix, `describe-tags` would report `ResourceType` as `"unknown"` for EIGW resources. The fix adds:

```go
if strings.HasPrefix(resourceID, "eigw-") {
    return "egress-only-internet-gateway"
}
```

This enables section 4 (Generic Tags Integration) to work correctly.
