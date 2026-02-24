#!/bin/bash
set -e

# VPC E2E Test Suite
# Tests VPC networking operations via the AWS CLI against a running Hive cluster.
# Can run with or without OVN — skips network-level tests when OVN is unavailable.
#
# Usage:
#   ./tests/e2e/run-vpc-e2e.sh                # Run against default endpoint
#   ENDPOINT=https://10.11.12.1:9999 ./tests/e2e/run-vpc-e2e.sh  # Custom endpoint

cd "$(dirname "$0")/../.."

ENDPOINT="${ENDPOINT:-https://127.0.0.1:9999}"
export AWS_PROFILE=hive
AWS_EC2="aws --endpoint-url ${ENDPOINT} ec2"

PASSED=0
FAILED=0
SKIPPED=0

pass() {
    echo "  ✅ $1"
    PASSED=$((PASSED + 1))
}

fail() {
    echo "  ❌ $1"
    FAILED=$((FAILED + 1))
}

skip() {
    echo "  ⏭️  $1 (skipped)"
    SKIPPED=$((SKIPPED + 1))
}

# Check if OVN is available
HAS_OVN=false
if command -v ovn-nbctl &>/dev/null; then
    HAS_OVN=true
fi

# Track created resources for cleanup
IGW_ID=""
VPC_ID=""
SUBNET_ID=""

cleanup() {
    local exit_code=$?
    echo ""
    echo "Cleanup..."

    # Detach and delete IGW
    if [ -n "$IGW_ID" ] && [ -n "$VPC_ID" ]; then
        $AWS_EC2 detach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" 2>/dev/null || true
    fi
    if [ -n "$IGW_ID" ]; then
        $AWS_EC2 delete-internet-gateway --internet-gateway-id "$IGW_ID" 2>/dev/null || true
    fi

    # Delete subnet
    if [ -n "$SUBNET_ID" ]; then
        $AWS_EC2 delete-subnet --subnet-id "$SUBNET_ID" 2>/dev/null || true
    fi

    # Delete VPC
    if [ -n "$VPC_ID" ]; then
        $AWS_EC2 delete-vpc --vpc-id "$VPC_ID" 2>/dev/null || true
    fi

    echo "Cleanup complete"
    echo ""
    echo "========================================"
    echo "VPC E2E Results: $PASSED passed, $FAILED failed, $SKIPPED skipped"
    echo "========================================"

    if [ $FAILED -gt 0 ]; then
        exit 1
    fi
    exit $exit_code
}
trap cleanup EXIT

echo "========================================"
echo "VPC E2E Test Suite"
echo "========================================"
echo "Endpoint: $ENDPOINT"
echo "OVN available: $HAS_OVN"
echo ""

# Phase 1: VPC CRUD
echo "Phase 1: VPC CRUD"
echo "========================================"

# Create VPC
echo "Creating VPC..."
VPC_OUTPUT=$($AWS_EC2 create-vpc --cidr-block 10.99.0.0/16 --output json 2>&1) || {
    fail "create-vpc"
    echo "  Output: $VPC_OUTPUT"
    exit 1
}
VPC_ID=$(echo "$VPC_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Vpc']['VpcId'])" 2>/dev/null) || {
    fail "create-vpc (parse VpcId)"
    echo "  Output: $VPC_OUTPUT"
    exit 1
}
pass "create-vpc: $VPC_ID"

# Describe VPCs — should include our VPC
echo "Describing VPCs..."
DESC_OUTPUT=$($AWS_EC2 describe-vpcs --vpc-ids "$VPC_ID" --output json 2>&1) || {
    fail "describe-vpcs"
    echo "  Output: $DESC_OUTPUT"
}
VPC_COUNT=$(echo "$DESC_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['Vpcs']))" 2>/dev/null)
if [ "$VPC_COUNT" = "1" ]; then
    pass "describe-vpcs: found $VPC_ID"
else
    fail "describe-vpcs: expected 1 VPC, got $VPC_COUNT"
fi

# Phase 2: Subnet CRUD
echo ""
echo "Phase 2: Subnet CRUD"
echo "========================================"

# Create subnet
echo "Creating subnet..."
SUBNET_OUTPUT=$($AWS_EC2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.99.1.0/24 --output json 2>&1) || {
    fail "create-subnet"
    echo "  Output: $SUBNET_OUTPUT"
    exit 1
}
SUBNET_ID=$(echo "$SUBNET_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Subnet']['SubnetId'])" 2>/dev/null) || {
    fail "create-subnet (parse SubnetId)"
    exit 1
}
pass "create-subnet: $SUBNET_ID"

# Describe subnets
echo "Describing subnets..."
DESC_OUTPUT=$($AWS_EC2 describe-subnets --subnet-ids "$SUBNET_ID" --output json 2>&1) || {
    fail "describe-subnets"
    echo "  Output: $DESC_OUTPUT"
}
SUBNET_COUNT=$(echo "$DESC_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['Subnets']))" 2>/dev/null)
if [ "$SUBNET_COUNT" = "1" ]; then
    pass "describe-subnets: found $SUBNET_ID"
else
    fail "describe-subnets: expected 1 subnet, got $SUBNET_COUNT"
fi

# Phase 3: Internet Gateway CRUD
echo ""
echo "Phase 3: Internet Gateway CRUD"
echo "========================================"

# Create IGW
echo "Creating Internet Gateway..."
IGW_OUTPUT=$($AWS_EC2 create-internet-gateway --output json 2>&1) || {
    fail "create-internet-gateway"
    echo "  Output: $IGW_OUTPUT"
    exit 1
}
IGW_ID=$(echo "$IGW_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['InternetGateway']['InternetGatewayId'])" 2>/dev/null) || {
    fail "create-internet-gateway (parse IGW ID)"
    exit 1
}
pass "create-internet-gateway: $IGW_ID"

# Describe IGW
echo "Describing Internet Gateways..."
DESC_OUTPUT=$($AWS_EC2 describe-internet-gateways --internet-gateway-ids "$IGW_ID" --output json 2>&1) || {
    fail "describe-internet-gateways"
}
IGW_COUNT=$(echo "$DESC_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['InternetGateways']))" 2>/dev/null)
if [ "$IGW_COUNT" = "1" ]; then
    pass "describe-internet-gateways: found $IGW_ID"
else
    fail "describe-internet-gateways: expected 1, got $IGW_COUNT"
fi

# Phase 4: IGW Attach / Detach
echo ""
echo "Phase 4: Internet Gateway Attach / Detach"
echo "========================================"

# Attach IGW
echo "Attaching IGW to VPC..."
$AWS_EC2 attach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" 2>&1 && {
    pass "attach-internet-gateway: $IGW_ID -> $VPC_ID"
} || {
    fail "attach-internet-gateway"
}

# Verify attachment via describe
DESC_OUTPUT=$($AWS_EC2 describe-internet-gateways --internet-gateway-ids "$IGW_ID" --output json 2>&1)
ATTACHED_VPC=$(echo "$DESC_OUTPUT" | python3 -c "
import sys,json
igw = json.load(sys.stdin)['InternetGateways'][0]
if igw.get('Attachments'):
    print(igw['Attachments'][0].get('VpcId', ''))
else:
    print('')
" 2>/dev/null)
if [ "$ATTACHED_VPC" = "$VPC_ID" ]; then
    pass "verify attachment: IGW attached to $VPC_ID"
else
    fail "verify attachment: expected $VPC_ID, got '$ATTACHED_VPC'"
fi

# Try to delete attached IGW — should fail
echo "Verifying delete-while-attached fails..."
if $AWS_EC2 delete-internet-gateway --internet-gateway-id "$IGW_ID" 2>&1 | grep -qi "error\|DependencyViolation"; then
    pass "delete-while-attached correctly rejected"
else
    # Check if it actually failed
    if $AWS_EC2 describe-internet-gateways --internet-gateway-ids "$IGW_ID" --output json 2>/dev/null | python3 -c "import sys,json; assert len(json.load(sys.stdin)['InternetGateways']) == 1" 2>/dev/null; then
        pass "delete-while-attached: IGW still exists (correctly rejected)"
    else
        fail "delete-while-attached: IGW was deleted (should have been rejected)"
    fi
fi

# Detach IGW
echo "Detaching IGW from VPC..."
$AWS_EC2 detach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" 2>&1 && {
    pass "detach-internet-gateway: $IGW_ID from $VPC_ID"
} || {
    fail "detach-internet-gateway"
}

# Verify detachment
DESC_OUTPUT=$($AWS_EC2 describe-internet-gateways --internet-gateway-ids "$IGW_ID" --output json 2>&1)
ATTACHED_VPC=$(echo "$DESC_OUTPUT" | python3 -c "
import sys,json
igw = json.load(sys.stdin)['InternetGateways'][0]
if igw.get('Attachments'):
    print(igw['Attachments'][0].get('VpcId', ''))
else:
    print('')
" 2>/dev/null)
if [ -z "$ATTACHED_VPC" ]; then
    pass "verify detachment: IGW is detached"
else
    fail "verify detachment: IGW still attached to '$ATTACHED_VPC'"
fi

# Phase 5: OVN Topology Verification (requires OVN)
echo ""
echo "Phase 5: OVN Topology Verification"
echo "========================================"

if [ "$HAS_OVN" = true ]; then
    # Re-attach IGW for OVN topology verification
    $AWS_EC2 attach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" 2>/dev/null || true
    sleep 1

    # Verify logical router exists for VPC
    if ovn-nbctl --no-leader-only ls-list 2>/dev/null | grep -q "subnet-"; then
        pass "OVN logical switch exists for subnet"
    else
        fail "OVN logical switch not found"
    fi

    if ovn-nbctl --no-leader-only lr-list 2>/dev/null | grep -q "vpc-"; then
        pass "OVN logical router exists for VPC"
    else
        fail "OVN logical router not found"
    fi

    # Check for external switch (IGW topology)
    if ovn-nbctl --no-leader-only ls-list 2>/dev/null | grep -q "ext-"; then
        pass "OVN external switch exists (IGW attached)"
    else
        fail "OVN external switch not found"
    fi

    # Check NAT rules
    NAT_COUNT=$(ovn-nbctl --no-leader-only lr-nat-list "vpc-${VPC_ID}" 2>/dev/null | grep -c "snat" || echo "0")
    if [ "$NAT_COUNT" -ge 1 ]; then
        pass "OVN SNAT rule exists on VPC router"
    else
        fail "OVN SNAT rule not found"
    fi

    # Check default route
    ROUTE_COUNT=$(ovn-nbctl --no-leader-only lr-route-list "vpc-${VPC_ID}" 2>/dev/null | grep -c "0.0.0.0/0" || echo "0")
    if [ "$ROUTE_COUNT" -ge 1 ]; then
        pass "OVN default route exists on VPC router"
    else
        fail "OVN default route not found"
    fi

    # Show full topology for debugging
    echo ""
    echo "OVN NB DB topology:"
    ovn-nbctl --no-leader-only show 2>/dev/null || echo "  (unable to query OVN NB DB)"

    # Detach IGW again for cleanup
    $AWS_EC2 detach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" 2>/dev/null || true
    sleep 1
else
    skip "OVN topology verification (ovn-nbctl not available)"
fi

# Phase 6: Cleanup and Verify
echo ""
echo "Phase 6: Cleanup"
echo "========================================"

# Delete IGW
echo "Deleting Internet Gateway..."
$AWS_EC2 delete-internet-gateway --internet-gateway-id "$IGW_ID" 2>&1 && {
    pass "delete-internet-gateway: $IGW_ID"
    IGW_ID="" # Don't re-delete in cleanup trap
} || {
    fail "delete-internet-gateway"
}

# Delete subnet
echo "Deleting subnet..."
$AWS_EC2 delete-subnet --subnet-id "$SUBNET_ID" 2>&1 && {
    pass "delete-subnet: $SUBNET_ID"
    SUBNET_ID="" # Don't re-delete in cleanup trap
} || {
    fail "delete-subnet"
}

# Delete VPC
echo "Deleting VPC..."
$AWS_EC2 delete-vpc --vpc-id "$VPC_ID" 2>&1 && {
    pass "delete-vpc: $VPC_ID"
    VPC_ID="" # Don't re-delete in cleanup trap
} || {
    fail "delete-vpc"
}

# Verify OVN cleanup
if [ "$HAS_OVN" = true ]; then
    sleep 1
    REMAINING_ROUTERS=$(ovn-nbctl --no-leader-only lr-list 2>/dev/null | grep -c "vpc-" || echo "0")
    if [ "$REMAINING_ROUTERS" = "0" ]; then
        pass "OVN cleanup: no VPC routers remaining"
    else
        fail "OVN cleanup: $REMAINING_ROUTERS VPC routers still exist"
    fi
fi

echo ""
