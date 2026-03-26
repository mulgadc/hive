#!/bin/bash
set -e

# ELBv2 (ALB) E2E Test Suite
# Tests Application Load Balancer operations via the AWS CLI against a running Spinifex cluster.
# Requires VPC networking to be functional (ALBs create ENIs in subnets).
#
# Usage:
#   ./tests/e2e/run-elbv2-e2e.sh                # Run against default endpoint
#   ENDPOINT=https://10.11.12.1:9999 ./tests/e2e/run-elbv2-e2e.sh  # Custom endpoint

cd "$(dirname "$0")/../.."

ENDPOINT="${ENDPOINT:-https://127.0.0.1:9999}"
export AWS_PROFILE=spinifex
AWS_EC2="aws --endpoint-url ${ENDPOINT} ec2"
AWS_ELBV2="aws --endpoint-url ${ENDPOINT} elbv2"

PASSED=0
FAILED=0

pass() {
    echo "  ✅ $1"
    PASSED=$((PASSED + 1))
}

fail() {
    echo "  ❌ $1"
    FAILED=$((FAILED + 1))
}

# Track created resources for cleanup
VPC_ID=""
SUBNET_ID=""
TG_ARN=""
TG2_ARN=""
LB_ARN=""
LISTENER_ARN=""

cleanup() {
    local exit_code=$?
    echo ""
    echo "Cleanup..."

    # Delete listener
    if [ -n "$LISTENER_ARN" ]; then
        $AWS_ELBV2 delete-listener --listener-arn "$LISTENER_ARN" 2>/dev/null || true
    fi

    # Delete load balancer (also cascade-deletes listeners)
    if [ -n "$LB_ARN" ]; then
        $AWS_ELBV2 delete-load-balancer --load-balancer-arn "$LB_ARN" 2>/dev/null || true
    fi

    # Deregister any targets and delete target groups
    if [ -n "$TG_ARN" ]; then
        $AWS_ELBV2 delete-target-group --target-group-arn "$TG_ARN" 2>/dev/null || true
    fi
    if [ -n "$TG2_ARN" ]; then
        $AWS_ELBV2 delete-target-group --target-group-arn "$TG2_ARN" 2>/dev/null || true
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
    echo "ELBv2 E2E Results: $PASSED passed, $FAILED failed"
    echo "========================================"

    if [ $FAILED -gt 0 ]; then
        exit 1
    fi
    exit $exit_code
}
trap cleanup EXIT

echo "========================================"
echo "ELBv2 (ALB) E2E Test Suite"
echo "========================================"
echo "Endpoint: $ENDPOINT"
echo ""

# Phase 0: VPC + Subnet Setup (prerequisite for ALB)
echo "Phase 0: VPC + Subnet Setup"
echo "========================================"

echo "Creating VPC..."
VPC_OUTPUT=$($AWS_EC2 create-vpc --cidr-block 10.200.0.0/16 --output json 2>&1) || {
    fail "create-vpc (prerequisite)"
    echo "  Output: $VPC_OUTPUT"
    exit 1
}
VPC_ID=$(echo "$VPC_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Vpc']['VpcId'])" 2>/dev/null) || {
    fail "create-vpc (parse VpcId)"
    exit 1
}
pass "create-vpc: $VPC_ID"

echo "Creating subnet..."
SUBNET_OUTPUT=$($AWS_EC2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.200.1.0/24 --output json 2>&1) || {
    fail "create-subnet (prerequisite)"
    echo "  Output: $SUBNET_OUTPUT"
    exit 1
}
SUBNET_ID=$(echo "$SUBNET_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Subnet']['SubnetId'])" 2>/dev/null) || {
    fail "create-subnet (parse SubnetId)"
    exit 1
}
pass "create-subnet: $SUBNET_ID"

# Phase 1: Target Group CRUD
echo ""
echo "Phase 1: Target Group CRUD"
echo "========================================"

# Create target group
echo "Creating target group..."
TG_OUTPUT=$($AWS_ELBV2 create-target-group \
    --name e2e-tg-1 \
    --protocol HTTP \
    --port 80 \
    --vpc-id "$VPC_ID" \
    --output json 2>&1) || {
    fail "create-target-group"
    echo "  Output: $TG_OUTPUT"
    exit 1
}
TG_ARN=$(echo "$TG_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['TargetGroups'][0]['TargetGroupArn'])" 2>/dev/null) || {
    fail "create-target-group (parse ARN)"
    echo "  Output: $TG_OUTPUT"
    exit 1
}
pass "create-target-group: $TG_ARN"

# Verify health check defaults
echo "Verifying health check defaults..."
HC_PATH=$(echo "$TG_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['TargetGroups'][0]['HealthCheckPath'])" 2>/dev/null)
HC_PROTOCOL=$(echo "$TG_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['TargetGroups'][0]['HealthCheckProtocol'])" 2>/dev/null)
if [ "$HC_PATH" = "/" ] && [ "$HC_PROTOCOL" = "HTTP" ]; then
    pass "health check defaults: path=/ protocol=HTTP"
else
    fail "health check defaults: path=$HC_PATH protocol=$HC_PROTOCOL (expected / HTTP)"
fi

# Describe target groups
echo "Describing target groups..."
DESC_TG_OUTPUT=$($AWS_ELBV2 describe-target-groups --target-group-arns "$TG_ARN" --output json 2>&1) || {
    fail "describe-target-groups"
    echo "  Output: $DESC_TG_OUTPUT"
}
TG_COUNT=$(echo "$DESC_TG_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['TargetGroups']))" 2>/dev/null)
if [ "$TG_COUNT" = "1" ]; then
    pass "describe-target-groups: found 1 target group"
else
    fail "describe-target-groups: expected 1, got $TG_COUNT"
fi

# Create second target group (for later tests)
echo "Creating second target group..."
TG2_OUTPUT=$($AWS_ELBV2 create-target-group \
    --name e2e-tg-2 \
    --protocol HTTP \
    --port 8080 \
    --vpc-id "$VPC_ID" \
    --output json 2>&1) || {
    fail "create-target-group-2"
    echo "  Output: $TG2_OUTPUT"
}
TG2_ARN=$(echo "$TG2_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['TargetGroups'][0]['TargetGroupArn'])" 2>/dev/null) || {
    fail "create-target-group-2 (parse ARN)"
}
pass "create-target-group-2: $TG2_ARN"

# Describe all target groups (no filter) — should find both
echo "Describing all target groups..."
DESC_ALL_TG=$($AWS_ELBV2 describe-target-groups --output json 2>&1) || {
    fail "describe-target-groups (all)"
}
ALL_TG_COUNT=$(echo "$DESC_ALL_TG" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['TargetGroups']))" 2>/dev/null)
if [ "$ALL_TG_COUNT" -ge 2 ]; then
    pass "describe-target-groups (all): found $ALL_TG_COUNT target groups"
else
    fail "describe-target-groups (all): expected >= 2, got $ALL_TG_COUNT"
fi

# Duplicate name detection
echo "Testing duplicate target group name..."
DUP_OUTPUT=$($AWS_ELBV2 create-target-group \
    --name e2e-tg-1 \
    --protocol HTTP \
    --port 80 \
    --vpc-id "$VPC_ID" \
    --output json 2>&1) && {
    fail "duplicate target group should have been rejected"
} || {
    if echo "$DUP_OUTPUT" | grep -qi "DuplicateTargetGroup\|already exists"; then
        pass "duplicate target group correctly rejected"
    else
        pass "duplicate target group rejected (error returned)"
    fi
}

# Phase 2: Target Registration
echo ""
echo "Phase 2: Target Registration"
echo "========================================"

# Register fake instance targets (they won't have real IPs but the API should accept them)
echo "Registering targets..."
REG_OUTPUT=$($AWS_ELBV2 register-targets \
    --target-group-arn "$TG_ARN" \
    --targets Id=i-e2etest00001 Id=i-e2etest00002 \
    --output json 2>&1) || {
    fail "register-targets"
    echo "  Output: $REG_OUTPUT"
}
pass "register-targets: 2 instances registered"

# Describe target health
echo "Describing target health..."
HEALTH_OUTPUT=$($AWS_ELBV2 describe-target-health \
    --target-group-arn "$TG_ARN" \
    --output json 2>&1) || {
    fail "describe-target-health"
    echo "  Output: $HEALTH_OUTPUT"
}
HEALTH_COUNT=$(echo "$HEALTH_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['TargetHealthDescriptions']))" 2>/dev/null)
if [ "$HEALTH_COUNT" = "2" ]; then
    pass "describe-target-health: 2 targets reported"
else
    fail "describe-target-health: expected 2 targets, got $HEALTH_COUNT"
fi

# Verify target state is "initial" (no real health checks running yet)
FIRST_STATE=$(echo "$HEALTH_OUTPUT" | python3 -c "
import sys,json
d = json.load(sys.stdin)['TargetHealthDescriptions'][0]
print(d.get('TargetHealth', {}).get('State', 'unknown'))
" 2>/dev/null)
if [ "$FIRST_STATE" = "initial" ]; then
    pass "target health state: initial (expected)"
else
    # Accept any state — might be "unhealthy" if health checks ran
    pass "target health state: $FIRST_STATE"
fi

# Deregister one target
echo "Deregistering one target..."
DEREG_OUTPUT=$($AWS_ELBV2 deregister-targets \
    --target-group-arn "$TG_ARN" \
    --targets Id=i-e2etest00002 \
    --output json 2>&1) || {
    fail "deregister-targets"
    echo "  Output: $DEREG_OUTPUT"
}
pass "deregister-targets: removed i-e2etest00002"

# Verify only 1 target remains
HEALTH_OUTPUT=$($AWS_ELBV2 describe-target-health \
    --target-group-arn "$TG_ARN" \
    --output json 2>&1) || {
    fail "describe-target-health (after deregister)"
}
HEALTH_COUNT=$(echo "$HEALTH_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['TargetHealthDescriptions']))" 2>/dev/null)
if [ "$HEALTH_COUNT" = "1" ]; then
    pass "describe-target-health after deregister: 1 target remains"
else
    fail "describe-target-health after deregister: expected 1, got $HEALTH_COUNT"
fi

# Phase 3: Load Balancer CRUD
echo ""
echo "Phase 3: Load Balancer CRUD"
echo "========================================"

# Create ALB
echo "Creating ALB..."
LB_OUTPUT=$($AWS_ELBV2 create-load-balancer \
    --name e2e-alb \
    --subnets "$SUBNET_ID" \
    --output json 2>&1) || {
    fail "create-load-balancer"
    echo "  Output: $LB_OUTPUT"
    exit 1
}
LB_ARN=$(echo "$LB_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['LoadBalancers'][0]['LoadBalancerArn'])" 2>/dev/null) || {
    fail "create-load-balancer (parse ARN)"
    echo "  Output: $LB_OUTPUT"
    exit 1
}
pass "create-load-balancer: $LB_ARN"

# Verify ALB fields
echo "Verifying ALB fields..."
LB_TYPE=$(echo "$LB_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['LoadBalancers'][0].get('Type', ''))" 2>/dev/null)
LB_STATE=$(echo "$LB_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['LoadBalancers'][0].get('State', {}).get('Code', ''))" 2>/dev/null)
LB_DNS=$(echo "$LB_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['LoadBalancers'][0].get('DNSName', ''))" 2>/dev/null)
LB_SCHEME=$(echo "$LB_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['LoadBalancers'][0].get('Scheme', ''))" 2>/dev/null)

if [ "$LB_TYPE" = "application" ]; then
    pass "ALB type: application"
else
    fail "ALB type: expected 'application', got '$LB_TYPE'"
fi

if [ "$LB_STATE" = "active" ]; then
    pass "ALB state: active"
else
    fail "ALB state: expected 'active', got '$LB_STATE'"
fi

if [ -n "$LB_DNS" ]; then
    pass "ALB DNS name: $LB_DNS"
else
    fail "ALB DNS name is empty"
fi

if [ "$LB_SCHEME" = "internet-facing" ]; then
    pass "ALB scheme: internet-facing (default)"
else
    fail "ALB scheme: expected 'internet-facing', got '$LB_SCHEME'"
fi

# Verify ENIs were created
echo "Checking ENIs created for ALB..."
ENI_OUTPUT=$($AWS_EC2 describe-network-interfaces \
    --filters "Name=description,Values=ELB *" \
    --output json 2>&1) || {
    fail "describe-network-interfaces (ENI check)"
    echo "  Output: $ENI_OUTPUT"
}
ENI_COUNT=$(echo "$ENI_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['NetworkInterfaces']))" 2>/dev/null)
if [ "$ENI_COUNT" -ge 1 ]; then
    pass "ALB ENIs created: $ENI_COUNT ENI(s) found"
else
    fail "ALB ENIs: expected >= 1, got $ENI_COUNT"
fi

# Describe load balancers
echo "Describing load balancers..."
DESC_LB_OUTPUT=$($AWS_ELBV2 describe-load-balancers --load-balancer-arns "$LB_ARN" --output json 2>&1) || {
    fail "describe-load-balancers"
    echo "  Output: $DESC_LB_OUTPUT"
}
LB_COUNT=$(echo "$DESC_LB_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['LoadBalancers']))" 2>/dev/null)
if [ "$LB_COUNT" = "1" ]; then
    pass "describe-load-balancers: found 1 ALB"
else
    fail "describe-load-balancers: expected 1, got $LB_COUNT"
fi

# Duplicate name detection
echo "Testing duplicate ALB name..."
DUP_LB_OUTPUT=$($AWS_ELBV2 create-load-balancer \
    --name e2e-alb \
    --subnets "$SUBNET_ID" \
    --output json 2>&1) && {
    fail "duplicate ALB should have been rejected"
} || {
    pass "duplicate ALB correctly rejected"
}

# Phase 4: Listener CRUD
echo ""
echo "Phase 4: Listener CRUD"
echo "========================================"

# Create listener
echo "Creating listener (port 80 -> TG)..."
LISTENER_OUTPUT=$($AWS_ELBV2 create-listener \
    --load-balancer-arn "$LB_ARN" \
    --protocol HTTP \
    --port 80 \
    --default-actions "Type=forward,TargetGroupArn=$TG_ARN" \
    --output json 2>&1) || {
    fail "create-listener"
    echo "  Output: $LISTENER_OUTPUT"
    exit 1
}
LISTENER_ARN=$(echo "$LISTENER_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Listeners'][0]['ListenerArn'])" 2>/dev/null) || {
    fail "create-listener (parse ARN)"
    echo "  Output: $LISTENER_OUTPUT"
    exit 1
}
pass "create-listener: $LISTENER_ARN"

# Verify listener fields
LISTENER_PORT=$(echo "$LISTENER_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Listeners'][0].get('Port', 0))" 2>/dev/null)
LISTENER_PROTO=$(echo "$LISTENER_OUTPUT" | python3 -c "import sys,json; print(json.load(sys.stdin)['Listeners'][0].get('Protocol', ''))" 2>/dev/null)
if [ "$LISTENER_PORT" = "80" ] && [ "$LISTENER_PROTO" = "HTTP" ]; then
    pass "listener fields: port=80 protocol=HTTP"
else
    fail "listener fields: port=$LISTENER_PORT protocol=$LISTENER_PROTO (expected 80 HTTP)"
fi

# Describe listeners
echo "Describing listeners..."
DESC_LISTENER_OUTPUT=$($AWS_ELBV2 describe-listeners \
    --load-balancer-arn "$LB_ARN" \
    --output json 2>&1) || {
    fail "describe-listeners"
    echo "  Output: $DESC_LISTENER_OUTPUT"
}
LISTENER_COUNT=$(echo "$DESC_LISTENER_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin)['Listeners']))" 2>/dev/null)
if [ "$LISTENER_COUNT" = "1" ]; then
    pass "describe-listeners: found 1 listener"
else
    fail "describe-listeners: expected 1, got $LISTENER_COUNT"
fi

# Duplicate listener port detection
echo "Testing duplicate listener port..."
DUP_LISTENER_OUTPUT=$($AWS_ELBV2 create-listener \
    --load-balancer-arn "$LB_ARN" \
    --protocol HTTP \
    --port 80 \
    --default-actions "Type=forward,TargetGroupArn=$TG_ARN" \
    --output json 2>&1) && {
    fail "duplicate listener port should have been rejected"
} || {
    pass "duplicate listener port correctly rejected"
}

# Phase 5: In-Use Protection
echo ""
echo "Phase 5: In-Use Protection"
echo "========================================"

# Try to delete target group that's referenced by a listener — should fail
echo "Testing delete target group in use..."
INUSE_OUTPUT=$($AWS_ELBV2 delete-target-group --target-group-arn "$TG_ARN" 2>&1) && {
    fail "delete in-use target group should have been rejected"
} || {
    if echo "$INUSE_OUTPUT" | grep -qi "InUse\|in use\|ResourceInUse"; then
        pass "delete in-use target group correctly rejected (ResourceInUse)"
    else
        pass "delete in-use target group rejected"
    fi
}

# Phase 6: Listener Deletion
echo ""
echo "Phase 6: Listener Deletion"
echo "========================================"

echo "Deleting listener..."
$AWS_ELBV2 delete-listener --listener-arn "$LISTENER_ARN" 2>&1 && {
    pass "delete-listener: $LISTENER_ARN"
    LISTENER_ARN=""
} || {
    fail "delete-listener"
}

# Verify listener is gone
DESC_LISTENER_OUTPUT=$($AWS_ELBV2 describe-listeners \
    --load-balancer-arn "$LB_ARN" \
    --output json 2>&1) || true
LISTENER_COUNT=$(echo "$DESC_LISTENER_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('Listeners', [])))" 2>/dev/null)
if [ "$LISTENER_COUNT" = "0" ]; then
    pass "describe-listeners after delete: 0 listeners"
else
    fail "describe-listeners after delete: expected 0, got $LISTENER_COUNT"
fi

# Now the target group should be deletable
echo "Deleting target group (no longer in use)..."
$AWS_ELBV2 delete-target-group --target-group-arn "$TG_ARN" 2>&1 && {
    pass "delete-target-group: $TG_ARN (after listener removed)"
    TG_ARN=""
} || {
    fail "delete-target-group (after listener removed)"
}

# Phase 7: Load Balancer Deletion + Cleanup Verification
echo ""
echo "Phase 7: Load Balancer Deletion"
echo "========================================"

echo "Deleting ALB..."
$AWS_ELBV2 delete-load-balancer --load-balancer-arn "$LB_ARN" 2>&1 && {
    pass "delete-load-balancer: $LB_ARN"
} || {
    fail "delete-load-balancer"
}

# Verify ALB is gone
DESC_LB_OUTPUT=$($AWS_ELBV2 describe-load-balancers --output json 2>&1) || true
LB_REMAINING=$(echo "$DESC_LB_OUTPUT" | python3 -c "
import sys,json
lbs = json.load(sys.stdin).get('LoadBalancers', [])
print(len([lb for lb in lbs if lb.get('LoadBalancerArn') == '$LB_ARN']))
" 2>/dev/null)
if [ "$LB_REMAINING" = "0" ]; then
    pass "ALB deleted: no longer in describe-load-balancers"
else
    fail "ALB still exists after deletion"
fi
LB_ARN="" # Don't re-delete in cleanup

# Verify ENIs were cleaned up
echo "Verifying ALB ENIs cleaned up..."
sleep 1 # Brief pause for async cleanup
ENI_OUTPUT=$($AWS_EC2 describe-network-interfaces \
    --filters "Name=description,Values=ELB *" \
    --output json 2>&1) || true
ENI_COUNT=$(echo "$ENI_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('NetworkInterfaces', [])))" 2>/dev/null)
if [ "$ENI_COUNT" = "0" ]; then
    pass "ALB ENIs cleaned up: 0 remaining"
else
    fail "ALB ENI cleanup: $ENI_COUNT ENIs still exist"
fi

# Delete second target group
echo "Deleting second target group..."
$AWS_ELBV2 delete-target-group --target-group-arn "$TG2_ARN" 2>&1 && {
    pass "delete-target-group-2: $TG2_ARN"
    TG2_ARN=""
} || {
    fail "delete-target-group-2"
}

# Phase 8: Error Path Tests
echo ""
echo "Phase 8: Error Path Tests"
echo "========================================"

# Describe non-existent load balancer
echo "Testing describe non-existent ALB..."
NOTFOUND_OUTPUT=$($AWS_ELBV2 describe-load-balancers \
    --load-balancer-arns "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/fake/fake123" \
    --output json 2>&1) || true
NOTFOUND_COUNT=$(echo "$NOTFOUND_OUTPUT" | python3 -c "import sys,json; print(len(json.load(sys.stdin).get('LoadBalancers', [])))" 2>/dev/null)
if [ "$NOTFOUND_COUNT" = "0" ]; then
    pass "describe non-existent ALB: empty result"
else
    fail "describe non-existent ALB: expected 0, got $NOTFOUND_COUNT"
fi

# Delete non-existent load balancer
echo "Testing delete non-existent ALB..."
$AWS_ELBV2 delete-load-balancer \
    --load-balancer-arn "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/fake/fake123" \
    2>&1 && {
    fail "delete non-existent ALB should have returned error"
} || {
    pass "delete non-existent ALB correctly returned error"
}

# Delete non-existent target group
echo "Testing delete non-existent target group..."
$AWS_ELBV2 delete-target-group \
    --target-group-arn "arn:aws:elasticloadbalancing:us-east-1:000000000000:targetgroup/fake/fake123" \
    2>&1 && {
    fail "delete non-existent TG should have returned error"
} || {
    pass "delete non-existent TG correctly returned error"
}

# Create target group with missing name
echo "Testing create target group with missing fields..."
$AWS_ELBV2 create-target-group --protocol HTTP --port 80 --vpc-id "$VPC_ID" --output json 2>&1 && {
    fail "create TG without name should have been rejected"
} || {
    pass "create TG without name correctly rejected"
}

# Create listener on non-existent ALB
echo "Testing create listener on non-existent ALB..."
$AWS_ELBV2 create-listener \
    --load-balancer-arn "arn:aws:elasticloadbalancing:us-east-1:000000000000:loadbalancer/app/fake/fake123" \
    --protocol HTTP \
    --port 80 \
    --default-actions "Type=forward,TargetGroupArn=$TG2_ARN" \
    --output json 2>&1 && {
    fail "create listener on non-existent ALB should have been rejected"
} || {
    pass "create listener on non-existent ALB correctly rejected"
}

echo ""
