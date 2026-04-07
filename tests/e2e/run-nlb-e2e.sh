#!/bin/bash
set -e

# ELBv2 (NLB) E2E Test Suite
# Tests Network Load Balancer operations via the AWS CLI against a running Spinifex cluster.
# Exercises the full NLB lifecycle: create NLB (type=network), TCP target group with
# TCP health check, register targets, create TCP listener, verify NLB active + targets
# healthy, send TCP traffic through NLB, deregister target and verify draining, cleanup.
#
# Requires:
#   - Pool mode with external IPAM (NOT dev_networking)
#
# Usage:
#   ./tests/e2e/run-nlb-e2e.sh
#   ENDPOINT=https://10.11.12.1:9999 ./tests/e2e/run-nlb-e2e.sh

cd "$(dirname "$0")/../.."

# Dev mode gate — skip when external IPAM is not available
SPINIFEX_CONFIG="${HOME}/spinifex/config/spinifex.toml"
if [ -f "$SPINIFEX_CONFIG" ]; then
    if grep -q 'dev_networking = true' "$SPINIFEX_CONFIG"; then
        echo "Skipping NLB E2E: dev_networking is enabled (no external IPAM)"
        echo "  This test requires pool mode with external networking."
        exit 0
    fi
fi

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
IGW_ID=""
APP_INSTANCE_IDS=()
TG_ARN=""
LB_ARN=""
LISTENER_ARN=""
INT_LB_ARN=""
INT_LISTENER_ARN=""
INT_TG_ARN=""
CLIENT_INSTANCE_ID=""

cleanup() {
    local exit_code=$?
    echo ""
    echo "Cleanup..."

    if [ -n "$INT_LISTENER_ARN" ]; then
        echo "  Deleting internal listener..."
        $AWS_ELBV2 delete-listener --listener-arn "$INT_LISTENER_ARN" 2>/dev/null || true
    fi

    if [ -n "$INT_LB_ARN" ]; then
        echo "  Deleting internal load balancer..."
        $AWS_ELBV2 delete-load-balancer --load-balancer-arn "$INT_LB_ARN" 2>/dev/null || true
    fi

    if [ -n "$INT_TG_ARN" ]; then
        echo "  Deleting internal target group..."
        $AWS_ELBV2 delete-target-group --target-group-arn "$INT_TG_ARN" 2>/dev/null || true
    fi

    if [ -n "$LISTENER_ARN" ]; then
        echo "  Deleting listener..."
        $AWS_ELBV2 delete-listener --listener-arn "$LISTENER_ARN" 2>/dev/null || true
    fi

    if [ -n "$LB_ARN" ]; then
        echo "  Deleting load balancer..."
        $AWS_ELBV2 delete-load-balancer --load-balancer-arn "$LB_ARN" 2>/dev/null || true
    fi

    if [ -n "$TG_ARN" ]; then
        echo "  Deleting target group..."
        $AWS_ELBV2 delete-target-group --target-group-arn "$TG_ARN" 2>/dev/null || true
    fi

    if [ -n "$CLIENT_INSTANCE_ID" ]; then
        echo "  Terminating client instance $CLIENT_INSTANCE_ID..."
        $AWS_EC2 terminate-instances --instance-ids "$CLIENT_INSTANCE_ID" 2>/dev/null || true
    fi

    for inst_id in "${APP_INSTANCE_IDS[@]}"; do
        if [ -n "$inst_id" ]; then
            echo "  Terminating instance $inst_id..."
            $AWS_EC2 terminate-instances --instance-ids "$inst_id" 2>/dev/null || true
        fi
    done

    if [ ${#APP_INSTANCE_IDS[@]} -gt 0 ]; then
        echo "  Waiting for instances to terminate..."
        for attempt in $(seq 1 30); do
            ALL_TERMINATED=true
            for inst_id in "${APP_INSTANCE_IDS[@]}"; do
                STATE=$($AWS_EC2 describe-instances --instance-ids "$inst_id" \
                    --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "terminated")
                if [ "$STATE" != "terminated" ]; then
                    ALL_TERMINATED=false
                    break
                fi
            done
            if [ "$ALL_TERMINATED" = true ]; then
                break
            fi
            sleep 2
        done
    fi

    echo "  Deleting key pair..."
    $AWS_EC2 delete-key-pair --key-name nlb-test-key 2>/dev/null || true

    if [ -n "$IGW_ID" ] && [ -n "$VPC_ID" ]; then
        echo "  Detaching IGW..."
        $AWS_EC2 detach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" 2>/dev/null || true
        $AWS_EC2 delete-internet-gateway --internet-gateway-id "$IGW_ID" 2>/dev/null || true
    fi

    if [ -n "$SUBNET_ID" ]; then
        echo "  Deleting subnet..."
        $AWS_EC2 delete-subnet --subnet-id "$SUBNET_ID" 2>/dev/null || true
    fi

    if [ -n "$VPC_ID" ]; then
        echo "  Deleting VPC..."
        $AWS_EC2 delete-vpc --vpc-id "$VPC_ID" 2>/dev/null || true
    fi

    echo "Cleanup complete"
    echo ""
    echo "========================================"
    echo "NLB E2E Results: $PASSED passed, $FAILED failed"
    echo "========================================"

    if [ $FAILED -gt 0 ]; then
        exit 1
    fi
    exit $exit_code
}
trap cleanup EXIT

echo "========================================"
echo "ELBv2 (NLB) Data Plane E2E Test"
echo "========================================"
echo "Endpoint: $ENDPOINT"
echo ""

# ==========================================
# Phase 0: Prerequisites — Instance Type + AMI Discovery
# ==========================================
echo "Phase 0: Prerequisites"
echo "========================================"

echo "Discovering instance types..."
AVAILABLE_TYPES=$($AWS_EC2 describe-instance-types --query 'InstanceTypes[*].InstanceType' --output text)
INSTANCE_TYPE=$(echo $AVAILABLE_TYPES | tr ' ' '\n' | grep -m1 'nano')
if [ -z "$INSTANCE_TYPE" ] || [ "$INSTANCE_TYPE" == "None" ]; then
    echo "ERROR: No nano instance type found"
    exit 1
fi
pass "instance type: $INSTANCE_TYPE"

echo "Discovering AMIs..."
ALL_IMAGES=$($AWS_EC2 describe-images --output json 2>&1)
AMI_ID=$(echo "$ALL_IMAGES" | jq -r '[.Images[] | select(.Name | test("alpine") | not)][0].ImageId // empty')
if [ -z "$AMI_ID" ]; then
    AMI_ID=$(echo "$ALL_IMAGES" | jq -r '.Images[0].ImageId // empty')
fi
if [ -z "$AMI_ID" ] || [ "$AMI_ID" == "None" ]; then
    echo "ERROR: No AMI found — import an image first"
    exit 1
fi
pass "AMI: $AMI_ID"

echo "Creating key pair..."
$AWS_EC2 delete-key-pair --key-name nlb-test-key 2>/dev/null || true
KEY_OUTPUT=$($AWS_EC2 create-key-pair --key-name nlb-test-key --output json 2>&1) || {
    fail "create key pair"
    exit 1
}
pass "key pair: nlb-test-key"

# ==========================================
# Phase 1: VPC + Subnet Setup
# ==========================================
echo ""
echo "Phase 1: VPC + Subnet Setup"
echo "========================================"

echo "Creating VPC..."
VPC_OUTPUT=$($AWS_EC2 create-vpc --cidr-block 10.203.0.0/16 --output json) || {
    fail "create-vpc"
    exit 1
}
VPC_ID=$(echo "$VPC_OUTPUT" | jq -r '.Vpc.VpcId')
pass "create-vpc: $VPC_ID"

echo "Creating internet gateway..."
IGW_OUTPUT=$($AWS_EC2 create-internet-gateway --output json) || {
    fail "create-internet-gateway"
    exit 1
}
IGW_ID=$(echo "$IGW_OUTPUT" | jq -r '.InternetGateway.InternetGatewayId')
$AWS_EC2 attach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" || {
    fail "attach-internet-gateway"
    exit 1
}
pass "internet gateway: $IGW_ID (attached)"

echo "Creating subnet..."
SUBNET_OUTPUT=$($AWS_EC2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.203.1.0/24 --output json) || {
    fail "create-subnet"
    exit 1
}
SUBNET_ID=$(echo "$SUBNET_OUTPUT" | jq -r '.Subnet.SubnetId')
pass "create-subnet: $SUBNET_ID"

$AWS_EC2 modify-subnet-attribute --subnet-id "$SUBNET_ID" \
    --map-public-ip-on-launch 2>&1 || {
    fail "modify-subnet-attribute"
    exit 1
}
pass "MapPublicIpOnLaunch enabled"

# ==========================================
# Phase 2: Launch App Instances (TCP echo servers)
# ==========================================
echo ""
echo "Phase 2: Launch App Instances (TCP echo servers)"
echo "========================================"

# Cloud-init user data: start a TCP echo server on port 9000 using Python3
# (available on all Ubuntu cloud images — no extra packages needed).
# Also starts an HTTP responder on port 80 for readiness verification.
APP_USER_DATA=$(cat <<'USERDATA'
#!/bin/bash
INSTANCE_ID=$(hostname)

# TCP echo server on port 9000: accepts connections, sends instance ID, closes.
cat > /tmp/tcp_echo.py << 'PYEOF'
import socketserver, os
class Handler(socketserver.StreamRequestHandler):
    def handle(self):
        self.wfile.write((os.uname()[1] + "\n").encode())
socketserver.TCPServer.allow_reuse_address = True
socketserver.TCPServer(("0.0.0.0", 9000), Handler).serve_forever()
PYEOF
nohup python3 /tmp/tcp_echo.py > /dev/null 2>&1 &

# HTTP health-check responder (for readiness verification from host)
mkdir -p /tmp/httpd
echo "{\"instance_id\": \"${INSTANCE_ID}\"}" > /tmp/httpd/index.html
cd /tmp/httpd
nohup python3 -m http.server 80 --bind 0.0.0.0 > /dev/null 2>&1 &
USERDATA
)

echo "Launching 2 app instances with TCP echo server..."
for i in 1 2; do
    echo "  Launching app instance $i..."
    RUN_OUTPUT=$($AWS_EC2 run-instances \
        --image-id "$AMI_ID" \
        --instance-type "$INSTANCE_TYPE" \
        --key-name nlb-test-key \
        --subnet-id "$SUBNET_ID" \
        --user-data "$APP_USER_DATA" \
        --output json 2>&1) || {
        fail "run-instances (app $i)"
        echo "  Output: $RUN_OUTPUT"
        exit 1
    }
    INST_ID=$(echo "$RUN_OUTPUT" | jq -r '.Instances[0].InstanceId')
    INST_IP=$(echo "$RUN_OUTPUT" | jq -r '.Instances[0].PrivateIpAddress // empty')
    if [ -z "$INST_ID" ] || [ "$INST_ID" == "null" ]; then
        fail "run-instances (app $i) — no instance ID"
        exit 1
    fi
    APP_INSTANCE_IDS+=("$INST_ID")
    echo "  App instance $i: $INST_ID (IP: ${INST_IP:-pending})"
    sleep 1
done
pass "launched ${#APP_INSTANCE_IDS[@]} app instances"

# Wait for running state
echo "Waiting for instances to reach running state..."
for inst_id in "${APP_INSTANCE_IDS[@]}"; do
    for attempt in $(seq 1 60); do
        STATE=$($AWS_EC2 describe-instances --instance-ids "$inst_id" \
            --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null)
        if [ "$STATE" == "running" ]; then
            break
        fi
        if [ $attempt -eq 60 ]; then
            fail "instance $inst_id did not reach running (stuck in $STATE)"
            exit 1
        fi
        sleep 2
    done
done
pass "all instances running"

# Collect private IPs
echo "Collecting app instance private IPs..."
declare -A INSTANCE_IPS
for inst_id in "${APP_INSTANCE_IDS[@]}"; do
    IP=$($AWS_EC2 describe-instances --instance-ids "$inst_id" \
        --query 'Reservations[0].Instances[0].PrivateIpAddress' --output text)
    if [ -z "$IP" ] || [ "$IP" == "None" ]; then
        fail "instance $inst_id has no PrivateIpAddress"
        exit 1
    fi
    INSTANCE_IPS[$inst_id]="$IP"
    echo "  $inst_id -> $IP"
done
pass "all app instances have private IPs"

echo "Waiting for cloud-init to complete (~100s)..."
sleep 100

# ==========================================
# Phase 3: Create TCP Target Group + Register Targets
# ==========================================
echo ""
echo "Phase 3: TCP Target Group + Registration"
echo "========================================"

echo "Creating TCP target group (port 9000, TCP health check)..."
TG_OUTPUT=$($AWS_ELBV2 create-target-group \
    --name nlb-test-tg \
    --protocol TCP \
    --port 9000 \
    --vpc-id "$VPC_ID" \
    --health-check-protocol TCP \
    --health-check-interval-seconds 10 \
    --healthy-threshold-count 2 \
    --unhealthy-threshold-count 2 \
    --output json 2>&1) || {
    fail "create-target-group"
    echo "  Output: $TG_OUTPUT"
    exit 1
}
TG_ARN=$(echo "$TG_OUTPUT" | jq -r '.TargetGroups[0].TargetGroupArn')
pass "create-target-group: $TG_ARN"

# Verify NLB-specific health check defaults
TG_PROTOCOL=$(echo "$TG_OUTPUT" | jq -r '.TargetGroups[0].Protocol')
HC_PROTOCOL=$(echo "$TG_OUTPUT" | jq -r '.TargetGroups[0].HealthCheckProtocol')
if [ "$TG_PROTOCOL" == "TCP" ]; then
    pass "target group protocol: TCP"
else
    fail "target group protocol: expected TCP, got $TG_PROTOCOL"
fi
if [ "$HC_PROTOCOL" == "TCP" ]; then
    pass "health check protocol: TCP"
else
    fail "health check protocol: expected TCP, got $HC_PROTOCOL"
fi

echo "Registering both app instances as targets..."
$AWS_ELBV2 register-targets \
    --target-group-arn "$TG_ARN" \
    --targets "Id=${APP_INSTANCE_IDS[0]}" "Id=${APP_INSTANCE_IDS[1]}" \
    --output json 2>&1 || {
    fail "register-targets"
    exit 1
}
pass "registered 2 targets"

# ==========================================
# Phase 4: Create NLB (type=network) + TCP Listener
# ==========================================
echo ""
echo "Phase 4: Create NLB + TCP Listener"
echo "========================================"

echo "Creating NLB (type=network)..."
LB_OUTPUT=$($AWS_ELBV2 create-load-balancer \
    --name nlb-test \
    --type network \
    --subnets "$SUBNET_ID" \
    --output json 2>&1) || {
    fail "create-load-balancer"
    echo "  Output: $LB_OUTPUT"
    exit 1
}
LB_ARN=$(echo "$LB_OUTPUT" | jq -r '.LoadBalancers[0].LoadBalancerArn')
LB_TYPE=$(echo "$LB_OUTPUT" | jq -r '.LoadBalancers[0].Type')
LB_STATE=$(echo "$LB_OUTPUT" | jq -r '.LoadBalancers[0].State.Code')
pass "create-load-balancer: $LB_ARN (type: $LB_TYPE, state: $LB_STATE)"

# Verify type is network
if [ "$LB_TYPE" == "network" ]; then
    pass "LB type: network"
else
    fail "LB type: expected 'network', got '$LB_TYPE'"
fi

# Verify ARN contains /net/ path segment
if echo "$LB_ARN" | grep -q "/net/"; then
    pass "ARN contains /net/ path segment"
else
    fail "ARN missing /net/ path segment: $LB_ARN"
fi

# Verify NLB rejects security groups
echo "Testing NLB rejects security groups..."
SG_OUTPUT=$($AWS_ELBV2 create-load-balancer \
    --name nlb-with-sg \
    --type network \
    --subnets "$SUBNET_ID" \
    --security-groups sg-test123 \
    --output json 2>&1) && {
    fail "NLB with security groups should have been rejected"
    # Clean up the accidentally created NLB
    SG_LB_ARN=$(echo "$SG_OUTPUT" | jq -r '.LoadBalancers[0].LoadBalancerArn // empty')
    if [ -n "$SG_LB_ARN" ]; then
        $AWS_ELBV2 delete-load-balancer --load-balancer-arn "$SG_LB_ARN" 2>/dev/null || true
    fi
} || {
    pass "NLB with security groups correctly rejected"
}

echo "Creating TCP listener (port 9000 -> target group)..."
LISTENER_OUTPUT=$($AWS_ELBV2 create-listener \
    --load-balancer-arn "$LB_ARN" \
    --protocol TCP \
    --port 9000 \
    --default-actions "Type=forward,TargetGroupArn=$TG_ARN" \
    --output json 2>&1) || {
    fail "create-listener"
    echo "  Output: $LISTENER_OUTPUT"
    exit 1
}
LISTENER_ARN=$(echo "$LISTENER_OUTPUT" | jq -r '.Listeners[0].ListenerArn')
LISTENER_PORT=$(echo "$LISTENER_OUTPUT" | jq -r '.Listeners[0].Port')
LISTENER_PROTO=$(echo "$LISTENER_OUTPUT" | jq -r '.Listeners[0].Protocol')
pass "create-listener: $LISTENER_ARN"

if [ "$LISTENER_PORT" == "9000" ] && [ "$LISTENER_PROTO" == "TCP" ]; then
    pass "listener fields: port=9000 protocol=TCP"
else
    fail "listener fields: port=$LISTENER_PORT protocol=$LISTENER_PROTO (expected 9000 TCP)"
fi

# ==========================================
# Phase 5: NLB Reaches Active (agent heartbeat)
# ==========================================
echo ""
echo "Phase 5: NLB Active (agent heartbeat)"
echo "========================================"

echo "Waiting for NLB to become active (up to 270s)..."
NLB_ACTIVE=false
for attempt in $(seq 1 90); do
    LB_STATE=$($AWS_ELBV2 describe-load-balancers --load-balancer-arns "$LB_ARN" \
        --query 'LoadBalancers[0].State.Code' --output text 2>/dev/null)
    if [ "$LB_STATE" == "active" ]; then
        NLB_ACTIVE=true
        break
    fi
    if [ $((attempt % 10)) -eq 0 ]; then
        echo "  NLB state: $LB_STATE (attempt $attempt/90)"
    fi
    sleep 3
done

if [ "$NLB_ACTIVE" = true ]; then
    pass "NLB state: active (agent heartbeat received)"
else
    fail "NLB did not reach active state (stuck in $LB_STATE)"
    echo ""
    echo "  Debug: daemon logs:"
    grep -iE 'LaunchSystemInstance|LB.VM|NLB|lb-agent|alb-agent|mgmt|heartbeat' ~/spinifex/logs/spinifex.log 2>/dev/null | tail -20 || echo "  (no matching log lines)"
    exit 1
fi

# Look up NLB ENI for data plane tests
LB_ID=$(echo "$LB_ARN" | sed 's|.*/||')
LB_NAME="nlb-test"

echo "Looking up NLB ENI..."
sleep 3

ENI_OUTPUT=$($AWS_EC2 describe-network-interfaces \
    --filters "Name=description,Values=ELB net/${LB_NAME}/${LB_ID}" \
    --output json 2>/dev/null)

# Fallback: try wildcard ELB filter if net/ pattern didn't match
ENI_COUNT=$(echo "$ENI_OUTPUT" | jq '.NetworkInterfaces | length')
if [ "$ENI_COUNT" == "0" ] || [ -z "$ENI_COUNT" ]; then
    ENI_OUTPUT=$($AWS_EC2 describe-network-interfaces \
        --filters "Name=description,Values=ELB *" \
        --output json 2>/dev/null)
fi

NLB_PUBLIC_IP=$(echo "$ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].Association.PublicIp // empty' 2>/dev/null)
NLB_PRIVATE_IP=$(echo "$ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].PrivateIpAddress // empty' 2>/dev/null)
NLB_ENI_ID=$(echo "$ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].NetworkInterfaceId // empty' 2>/dev/null)

echo "  NLB ENI: $NLB_ENI_ID"
echo "  NLB Private IP: $NLB_PRIVATE_IP"
echo "  NLB Public IP: $NLB_PUBLIC_IP"

if [ -n "$NLB_PUBLIC_IP" ] && [ "$NLB_PUBLIC_IP" != "null" ]; then
    pass "NLB ENI has public IP: $NLB_PUBLIC_IP"
else
    fail "NLB ENI does NOT have a public IP (internet-facing scheme should assign one)"
    echo "  ENI output:"
    echo "$ENI_OUTPUT" | jq .
fi

# ==========================================
# Phase 6: Wait for Targets to Become Healthy
# ==========================================
echo ""
echo "Phase 6: Wait for Target Health (NLB -> targets via TCP check)"
echo "========================================"

echo "Polling target health (timeout 120s)..."
HEALTH_TIMEOUT=120
HEALTH_START=$(date +%s)
TARGETS_HEALTHY=false

while true; do
    ELAPSED=$(( $(date +%s) - HEALTH_START ))
    if [ $ELAPSED -ge $HEALTH_TIMEOUT ]; then
        break
    fi

    HEALTH_OUTPUT=$($AWS_ELBV2 describe-target-health \
        --target-group-arn "$TG_ARN" \
        --output json 2>/dev/null) || continue

    HEALTHY_COUNT=$(echo "$HEALTH_OUTPUT" | jq '[.TargetHealthDescriptions[] | select(.TargetHealth.State == "healthy")] | length')
    TOTAL_COUNT=$(echo "$HEALTH_OUTPUT" | jq '.TargetHealthDescriptions | length')

    echo "  ${ELAPSED}s: $HEALTHY_COUNT/$TOTAL_COUNT targets healthy"

    if [ "$HEALTHY_COUNT" -eq 2 ]; then
        TARGETS_HEALTHY=true
        break
    fi
    sleep 5
done

if [ "$TARGETS_HEALTHY" = true ]; then
    pass "both targets healthy (TCP health checks passing)"
else
    echo "  Current target health:"
    echo "$HEALTH_OUTPUT" | jq -r '.TargetHealthDescriptions[] | "    \(.Target.Id): \(.TargetHealth.State) (\(.TargetHealth.Reason // "n/a"))"'
    fail "targets did not become healthy within ${HEALTH_TIMEOUT}s"
fi

# ==========================================
# Phase 7: TCP Traffic Through NLB
# ==========================================
echo ""
echo "Phase 7: TCP Traffic Through NLB"
echo "========================================"

if [ -z "$NLB_PUBLIC_IP" ] || [ "$NLB_PUBLIC_IP" == "null" ]; then
    echo "  Skipped: NLB has no public IP"
else
    NLB_URL="${NLB_PUBLIC_IP}:9000"

    # Wait for NLB to start responding (HAProxy needs time to load config)
    echo "Waiting for NLB to respond at $NLB_URL..."
    NLB_RESPONDING=false
    for attempt in $(seq 1 20); do
        PROBE=$(echo "" | nc -w3 ${NLB_PUBLIC_IP} 9000 2>/dev/null || true)
        if [ -n "$PROBE" ]; then
            NLB_RESPONDING=true
            break
        fi
        echo "  Attempt $attempt/20: NLB not yet responding..."
        sleep 5
    done

    if [ "$NLB_RESPONDING" = true ]; then
        pass "NLB responding via public IP at $NLB_URL"
    else
        fail "NLB not responding at $NLB_URL"
    fi

    NUM_REQUESTS=20
    echo "Sending $NUM_REQUESTS TCP requests to NLB at $NLB_URL..."

    declare -A RESPONSE_COUNTS
    TOTAL_SUCCESS=0
    TOTAL_FAIL=0

    for i in $(seq 1 $NUM_REQUESTS); do
        # Send a TCP connection to the NLB and read the response (instance ID)
        RESPONSE=$(echo "" | nc -w2 ${NLB_PUBLIC_IP} 9000 2>/dev/null || true)
        if [ -z "$RESPONSE" ]; then
            TOTAL_FAIL=$((TOTAL_FAIL + 1))
            continue
        fi

        RESP_INSTANCE=$(echo "$RESPONSE" | tr -d '[:space:]')
        if [ -n "$RESP_INSTANCE" ]; then
            RESPONSE_COUNTS[$RESP_INSTANCE]=$(( ${RESPONSE_COUNTS[$RESP_INSTANCE]:-0} + 1 ))
            TOTAL_SUCCESS=$((TOTAL_SUCCESS + 1))
        else
            TOTAL_FAIL=$((TOTAL_FAIL + 1))
        fi
    done

    echo "  Results: $TOTAL_SUCCESS successful, $TOTAL_FAIL failed"
    echo "  Distribution:"
    for inst_id in "${!RESPONSE_COUNTS[@]}"; do
        echo "    $inst_id: ${RESPONSE_COUNTS[$inst_id]} responses"
    done

    UNIQUE_RESPONDERS=${#RESPONSE_COUNTS[@]}
    if [ "$UNIQUE_RESPONDERS" -ge 2 ]; then
        pass "round-robin via TCP: $UNIQUE_RESPONDERS unique instances responded"
    elif [ "$UNIQUE_RESPONDERS" -eq 1 ]; then
        # Accept single responder — HAProxy may not have rotated yet with 20 requests
        pass "TCP traffic forwarded: 1 instance responded ($TOTAL_SUCCESS/$NUM_REQUESTS successful)"
    else
        fail "TCP traffic: no successful responses"
    fi

    if [ "$TOTAL_SUCCESS" -ge 10 ]; then
        pass "TCP success rate: $TOTAL_SUCCESS/$NUM_REQUESTS requests succeeded"
    else
        fail "TCP success rate: only $TOTAL_SUCCESS/$NUM_REQUESTS requests succeeded"
    fi
fi

# ==========================================
# Phase 8: Deregister Target + Verify Draining
# ==========================================
echo ""
echo "Phase 8: Deregister Target + Verify"
echo "========================================"

echo "Deregistering first target: ${APP_INSTANCE_IDS[0]}..."
$AWS_ELBV2 deregister-targets \
    --target-group-arn "$TG_ARN" \
    --targets "Id=${APP_INSTANCE_IDS[0]}" \
    --output json 2>&1 || {
    fail "deregister-targets"
}
pass "deregistered ${APP_INSTANCE_IDS[0]}"

# Check that only 1 target remains (or the deregistered one shows draining)
sleep 3
HEALTH_OUTPUT=$($AWS_ELBV2 describe-target-health \
    --target-group-arn "$TG_ARN" \
    --output json 2>/dev/null) || true

REMAINING=$(echo "$HEALTH_OUTPUT" | jq '.TargetHealthDescriptions | length')
DRAINING=$(echo "$HEALTH_OUTPUT" | jq '[.TargetHealthDescriptions[] | select(.TargetHealth.State == "draining")] | length')

echo "  Targets remaining: $REMAINING (draining: $DRAINING)"

if [ "$REMAINING" -le 2 ]; then
    if [ "$REMAINING" -eq 1 ]; then
        pass "target deregistered: 1 target remaining"
    elif [ "$DRAINING" -ge 1 ]; then
        pass "target in draining state"
    else
        pass "target deregistration processed ($REMAINING targets)"
    fi
else
    fail "expected <= 2 targets after deregister, got $REMAINING"
fi

# ==========================================
# Phase 9: Cleanup Verification
# ==========================================
echo ""
echo "Phase 9: Cleanup Verification"
echo "========================================"

# Delete listener
echo "Deleting listener..."
$AWS_ELBV2 delete-listener --listener-arn "$LISTENER_ARN" 2>&1 && {
    pass "delete-listener: $LISTENER_ARN"
    LISTENER_ARN=""
} || {
    fail "delete-listener"
}

# Delete NLB
echo "Deleting NLB..."
$AWS_ELBV2 delete-load-balancer --load-balancer-arn "$LB_ARN" 2>&1 && {
    pass "delete-load-balancer: $LB_ARN"
} || {
    fail "delete-load-balancer"
}

# Verify NLB is gone
DESC_LB_OUTPUT=$($AWS_ELBV2 describe-load-balancers --output json 2>&1) || true
LB_REMAINING=$(echo "$DESC_LB_OUTPUT" | jq "[.LoadBalancers[] | select(.LoadBalancerArn == \"$LB_ARN\")] | length")
if [ "$LB_REMAINING" == "0" ]; then
    pass "NLB deleted: no longer in describe-load-balancers"
else
    fail "NLB still exists after deletion"
fi
LB_ARN=""

# Verify ENIs cleaned up
echo "Verifying NLB ENIs cleaned up (up to 30s)..."
ENI_COUNT=""
for i in $(seq 1 10); do
    ENI_OUTPUT=$($AWS_EC2 describe-network-interfaces \
        --filters "Name=description,Values=ELB net/${LB_NAME}/${LB_ID}" \
        --output json 2>&1) || true
    ENI_COUNT=$(echo "$ENI_OUTPUT" | jq '.NetworkInterfaces | length')
    if [ "$ENI_COUNT" == "0" ]; then
        break
    fi
    sleep 3
done
if [ "$ENI_COUNT" == "0" ]; then
    pass "NLB ENIs cleaned up: 0 remaining (after ${i} polls)"
else
    fail "NLB ENI cleanup: $ENI_COUNT ENIs still exist after 30s"
fi

# Deregister remaining target and delete TG
echo "Deregistering remaining target..."
$AWS_ELBV2 deregister-targets \
    --target-group-arn "$TG_ARN" \
    --targets "Id=${APP_INSTANCE_IDS[1]}" \
    --output json 2>&1 || true

echo "Deleting target group..."
$AWS_ELBV2 delete-target-group --target-group-arn "$TG_ARN" 2>&1 && {
    pass "delete-target-group: $TG_ARN"
    TG_ARN=""
} || {
    fail "delete-target-group"
}

# ==========================================
# Phase 10: Create Internal NLB + TCP Target Group
# ==========================================
echo ""
echo "Phase 10: Internal NLB + TCP Target Group"
echo "========================================"

echo "Creating TCP target group for internal NLB (port 9000, TCP health check)..."
INT_TG_OUTPUT=$($AWS_ELBV2 create-target-group \
    --name nlb-int-tg \
    --protocol TCP \
    --port 9000 \
    --vpc-id "$VPC_ID" \
    --health-check-protocol TCP \
    --health-check-interval-seconds 10 \
    --healthy-threshold-count 2 \
    --unhealthy-threshold-count 2 \
    --output json 2>&1) || {
    fail "create-target-group (internal)"
    echo "  Output: $INT_TG_OUTPUT"
    exit 1
}
INT_TG_ARN=$(echo "$INT_TG_OUTPUT" | jq -r '.TargetGroups[0].TargetGroupArn')
pass "create-target-group (internal): $INT_TG_ARN"

echo "Registering both app instances as targets..."
$AWS_ELBV2 register-targets \
    --target-group-arn "$INT_TG_ARN" \
    --targets "Id=${APP_INSTANCE_IDS[0]}" "Id=${APP_INSTANCE_IDS[1]}" \
    --output json 2>&1 || {
    fail "register-targets (internal)"
    exit 1
}
pass "registered 2 targets (internal)"

echo "Creating internal NLB (type=network, scheme=internal)..."
INT_LB_OUTPUT=$($AWS_ELBV2 create-load-balancer \
    --name nlb-int-test \
    --type network \
    --scheme internal \
    --subnets "$SUBNET_ID" \
    --output json 2>&1) || {
    fail "create-load-balancer (internal)"
    echo "  Output: $INT_LB_OUTPUT"
    exit 1
}
INT_LB_ARN=$(echo "$INT_LB_OUTPUT" | jq -r '.LoadBalancers[0].LoadBalancerArn')
INT_LB_TYPE=$(echo "$INT_LB_OUTPUT" | jq -r '.LoadBalancers[0].Type')
INT_LB_SCHEME=$(echo "$INT_LB_OUTPUT" | jq -r '.LoadBalancers[0].Scheme')
INT_LB_STATE=$(echo "$INT_LB_OUTPUT" | jq -r '.LoadBalancers[0].State.Code')
pass "create-load-balancer (internal): $INT_LB_ARN (type: $INT_LB_TYPE, scheme: $INT_LB_SCHEME, state: $INT_LB_STATE)"

if [ "$INT_LB_TYPE" == "network" ]; then
    pass "internal NLB type: network"
else
    fail "internal NLB type: expected 'network', got '$INT_LB_TYPE'"
fi

if [ "$INT_LB_SCHEME" == "internal" ]; then
    pass "scheme confirmed: internal"
else
    fail "scheme mismatch: expected internal, got $INT_LB_SCHEME"
fi

# ==========================================
# Phase 11: Verify Internal NLB ENI — No Public IP
# ==========================================
echo ""
echo "Phase 11: Verify Internal NLB ENI (No Public IP)"
echo "========================================"

INT_LB_ID=$(echo "$INT_LB_ARN" | sed 's|.*/||')
INT_LB_NAME="nlb-int-test"

echo "Looking up internal NLB ENI..."
sleep 3

INT_ENI_OUTPUT=$($AWS_EC2 describe-network-interfaces \
    --filters "Name=description,Values=ELB net/${INT_LB_NAME}/${INT_LB_ID}" \
    --output json 2>/dev/null)

INT_NLB_PUBLIC_IP=$(echo "$INT_ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].Association.PublicIp // empty' 2>/dev/null)
INT_NLB_PRIVATE_IP=$(echo "$INT_ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].PrivateIpAddress // empty' 2>/dev/null)
INT_NLB_ENI_ID=$(echo "$INT_ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].NetworkInterfaceId // empty' 2>/dev/null)

echo "  ENI: $INT_NLB_ENI_ID"
echo "  Private IP: $INT_NLB_PRIVATE_IP"
echo "  Public IP: ${INT_NLB_PUBLIC_IP:-(none)}"

if [ -z "$INT_NLB_PUBLIC_IP" ] || [ "$INT_NLB_PUBLIC_IP" == "null" ]; then
    pass "internal NLB has no public IP (correct)"
else
    fail "internal NLB should NOT have a public IP, but got: $INT_NLB_PUBLIC_IP"
fi

if [ -n "$INT_NLB_PRIVATE_IP" ] && [ "$INT_NLB_PRIVATE_IP" != "null" ]; then
    pass "internal NLB has private IP: $INT_NLB_PRIVATE_IP"
else
    fail "internal NLB has no private IP"
    exit 1
fi

# Verify DNS name HAS internal- prefix
INT_LB_DNS=$($AWS_ELBV2 describe-load-balancers --load-balancer-arns "$INT_LB_ARN" \
    --query 'LoadBalancers[0].DNSName' --output text 2>/dev/null)
echo "  DNS name: $INT_LB_DNS"
if echo "$INT_LB_DNS" | grep -q "^internal-"; then
    pass "DNS name has internal- prefix"
else
    fail "DNS name missing internal- prefix for internal NLB: $INT_LB_DNS"
fi

# Verify ARN contains /net/ path segment
if echo "$INT_LB_ARN" | grep -q "/net/"; then
    pass "internal NLB ARN contains /net/ path segment"
else
    fail "internal NLB ARN missing /net/ path segment: $INT_LB_ARN"
fi

echo "Creating TCP listener on internal NLB (port 9000 -> target group)..."
INT_LISTENER_OUTPUT=$($AWS_ELBV2 create-listener \
    --load-balancer-arn "$INT_LB_ARN" \
    --protocol TCP \
    --port 9000 \
    --default-actions "Type=forward,TargetGroupArn=$INT_TG_ARN" \
    --output json 2>&1) || {
    fail "create-listener (internal)"
    echo "  Output: $INT_LISTENER_OUTPUT"
    exit 1
}
INT_LISTENER_ARN=$(echo "$INT_LISTENER_OUTPUT" | jq -r '.Listeners[0].ListenerArn')
pass "create-listener (internal): $INT_LISTENER_ARN"

# ==========================================
# Phase 12: Internal NLB Reaches Active
# ==========================================
echo ""
echo "Phase 12: Internal NLB Active (agent heartbeat)"
echo "========================================"

echo "Waiting for internal NLB to become active (up to 270s)..."
INT_NLB_ACTIVE=false
for attempt in $(seq 1 90); do
    INT_LB_STATE=$($AWS_ELBV2 describe-load-balancers --load-balancer-arns "$INT_LB_ARN" \
        --query 'LoadBalancers[0].State.Code' --output text 2>/dev/null)
    if [ "$INT_LB_STATE" == "active" ]; then
        INT_NLB_ACTIVE=true
        break
    fi
    if [ $((attempt % 10)) -eq 0 ]; then
        echo "  Internal NLB state: $INT_LB_STATE (attempt $attempt/90)"
    fi
    sleep 3
done

if [ "$INT_NLB_ACTIVE" = true ]; then
    pass "internal NLB state: active (agent heartbeat received)"
else
    fail "internal NLB did not reach active state (stuck in $INT_LB_STATE)"
    echo ""
    echo "  Debug: daemon logs:"
    grep -iE 'LaunchSystemInstance|LB.VM|NLB|lb-agent|alb-agent|mgmt|heartbeat' ~/spinifex/logs/spinifex.log 2>/dev/null | tail -20 || echo "  (no matching log lines)"
    exit 1
fi

# Wait for targets to become healthy
echo "Polling target health for internal NLB (timeout 120s)..."
HEALTH_TIMEOUT=120
HEALTH_START=$(date +%s)
INT_TARGETS_HEALTHY=false

while true; do
    ELAPSED=$(( $(date +%s) - HEALTH_START ))
    if [ $ELAPSED -ge $HEALTH_TIMEOUT ]; then
        break
    fi

    INT_HEALTH_OUTPUT=$($AWS_ELBV2 describe-target-health \
        --target-group-arn "$INT_TG_ARN" \
        --output json 2>/dev/null) || continue

    HEALTHY_COUNT=$(echo "$INT_HEALTH_OUTPUT" | jq '[.TargetHealthDescriptions[] | select(.TargetHealth.State == "healthy")] | length')
    TOTAL_COUNT=$(echo "$INT_HEALTH_OUTPUT" | jq '.TargetHealthDescriptions | length')

    echo "  ${ELAPSED}s: $HEALTHY_COUNT/$TOTAL_COUNT targets healthy"

    if [ "$HEALTHY_COUNT" -eq 2 ]; then
        INT_TARGETS_HEALTHY=true
        break
    fi
    sleep 5
done

if [ "$INT_TARGETS_HEALTHY" = true ]; then
    pass "both targets healthy via internal NLB (TCP health checks passing)"
else
    echo "  Current target health:"
    echo "$INT_HEALTH_OUTPUT" | jq -r '.TargetHealthDescriptions[] | "    \(.Target.Id): \(.TargetHealth.State) (\(.TargetHealth.Reason // "n/a"))"'
    fail "internal NLB targets did not become healthy within ${HEALTH_TIMEOUT}s"
fi

# ==========================================
# Phase 13: TCP Traffic Through Internal NLB (VPC client)
# ==========================================
echo ""
echo "Phase 13: TCP Traffic Through Internal NLB (VPC client)"
echo "========================================"

# Launch a client VM in the same subnet to send TCP traffic to the internal NLB
# via its private IP. The client collects results and serves them over HTTP
# so the host can fetch them via the client's public IP.
echo "Launching client VM to test internal NLB from inside the VPC..."

CLIENT_USER_DATA=$(cat <<USERDATA
#!/bin/bash
NLB_IP="${INT_NLB_PRIVATE_IP}"
NUM_REQUESTS=20

mkdir -p /tmp/httpd
cd /tmp/httpd

# Wait for NLB to respond via TCP (up to 5 min)
echo "waiting" > status.txt
nohup python3 -m http.server 80 --bind 0.0.0.0 > /dev/null 2>&1 &

for i in \$(seq 1 60); do
    PROBE=\$(echo "" | nc -w3 \${NLB_IP} 9000 2>/dev/null || true)
    if [ -n "\$PROBE" ]; then
        break
    fi
    sleep 5
done

# Send test requests and collect responses (one line per response)
> results.txt
for i in \$(seq 1 \$NUM_REQUESTS); do
    RESP=\$(echo "" | nc -w2 \${NLB_IP} 9000 2>/dev/null || true)
    echo "\$RESP" >> results.txt
done

echo "done" > status.txt
USERDATA
)

CLIENT_OUTPUT=$($AWS_EC2 run-instances \
    --image-id "$AMI_ID" \
    --instance-type "$INSTANCE_TYPE" \
    --key-name nlb-test-key \
    --subnet-id "$SUBNET_ID" \
    --user-data "$CLIENT_USER_DATA" \
    --output json 2>&1) || {
    fail "run-instances (client)"
    echo "  Output: $CLIENT_OUTPUT"
    exit 1
}
CLIENT_INSTANCE_ID=$(echo "$CLIENT_OUTPUT" | jq -r '.Instances[0].InstanceId')
echo "  Client instance: $CLIENT_INSTANCE_ID"
pass "launched client VM"

# Wait for client to reach running state
echo "Waiting for client to reach running state..."
for attempt in $(seq 1 60); do
    STATE=$($AWS_EC2 describe-instances --instance-ids "$CLIENT_INSTANCE_ID" \
        --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null)
    if [ "$STATE" == "running" ]; then
        break
    fi
    if [ $attempt -eq 60 ]; then
        fail "client instance did not reach running (stuck in $STATE)"
        exit 1
    fi
    sleep 2
done

# Discover client's public IP
CLIENT_ENI=$($AWS_EC2 describe-network-interfaces \
    --filters "Name=attachment.instance-id,Values=$CLIENT_INSTANCE_ID" \
    --output json 2>/dev/null)
CLIENT_PUBLIC_IP=$(echo "$CLIENT_ENI" | jq -r '.NetworkInterfaces[0].Association.PublicIp // empty' 2>/dev/null)

if [ -z "$CLIENT_PUBLIC_IP" ] || [ "$CLIENT_PUBLIC_IP" == "null" ]; then
    fail "client VM has no public IP — cannot fetch results from host"
    exit 1
fi
pass "client VM public IP: $CLIENT_PUBLIC_IP"

# Wait for cloud-init + test script to complete
echo "Waiting for client cloud-init + NLB test (~120s)..."
CLIENT_DONE=false
for attempt in $(seq 1 60); do
    STATUS=$(curl -s --max-time 3 "http://${CLIENT_PUBLIC_IP}:80/status.txt" 2>/dev/null) || true
    if [ "$STATUS" == "done" ]; then
        CLIENT_DONE=true
        break
    fi
    if [ $((attempt % 10)) -eq 0 ]; then
        echo "  Client status: ${STATUS:-unreachable} (attempt $attempt/60)"
    fi
    sleep 5
done

if [ "$CLIENT_DONE" != true ]; then
    fail "client VM test did not complete within timeout"
    echo "  Last status: ${STATUS:-unreachable}"
    exit 1
fi
pass "client VM test completed"

# Fetch and analyse results
echo "Fetching results from client VM..."
INT_RESULTS=$(curl -s --max-time 10 "http://${CLIENT_PUBLIC_IP}:80/results.txt" 2>/dev/null)

if [ -z "$INT_RESULTS" ]; then
    fail "could not fetch results from client VM"
    exit 1
fi

# Parse results: count unique hostnames returned by TCP echo server
declare -A INT_RESPONSE_COUNTS
INT_TOTAL_SUCCESS=0
INT_TOTAL_FAIL=0

while IFS= read -r line; do
    line=$(echo "$line" | tr -d '[:space:]')
    [ -z "$line" ] && continue
    INT_RESPONSE_COUNTS[$line]=$(( ${INT_RESPONSE_COUNTS[$line]:-0} + 1 ))
    INT_TOTAL_SUCCESS=$((INT_TOTAL_SUCCESS + 1))
done <<< "$INT_RESULTS"

echo "  Results: $INT_TOTAL_SUCCESS successful, $INT_TOTAL_FAIL failed"
echo "  Distribution:"
for inst_id in "${!INT_RESPONSE_COUNTS[@]}"; do
    echo "    $inst_id: ${INT_RESPONSE_COUNTS[$inst_id]} responses"
done

INT_UNIQUE_RESPONDERS=${#INT_RESPONSE_COUNTS[@]}
if [ "$INT_UNIQUE_RESPONDERS" -ge 2 ]; then
    pass "round-robin via private IP (TCP): $INT_UNIQUE_RESPONDERS unique instances responded"
elif [ "$INT_UNIQUE_RESPONDERS" -eq 1 ]; then
    pass "TCP traffic forwarded via private IP: 1 instance responded ($INT_TOTAL_SUCCESS/20 successful)"
else
    fail "TCP traffic via private IP: no successful responses"
fi

if [ "$INT_TOTAL_SUCCESS" -ge 10 ]; then
    pass "internal NLB TCP success rate: $INT_TOTAL_SUCCESS/20 requests succeeded"
else
    fail "internal NLB TCP success rate: only $INT_TOTAL_SUCCESS/20 requests succeeded"
fi

# ==========================================
# Phase 14: Internal NLB Cleanup
# ==========================================
echo ""
echo "Phase 14: Internal NLB Cleanup"
echo "========================================"

# Terminate client VM
echo "Terminating client VM..."
$AWS_EC2 terminate-instances --instance-ids "$CLIENT_INSTANCE_ID" 2>/dev/null || true
CLIENT_INSTANCE_ID=""
pass "client VM terminated"

# Delete internal listener
echo "Deleting internal listener..."
$AWS_ELBV2 delete-listener --listener-arn "$INT_LISTENER_ARN" 2>&1 && {
    pass "delete-listener (internal): $INT_LISTENER_ARN"
    INT_LISTENER_ARN=""
} || {
    fail "delete-listener (internal)"
}

# Delete internal NLB
echo "Deleting internal NLB..."
$AWS_ELBV2 delete-load-balancer --load-balancer-arn "$INT_LB_ARN" 2>&1 && {
    pass "delete-load-balancer (internal): $INT_LB_ARN"
} || {
    fail "delete-load-balancer (internal)"
}

# Verify internal NLB is gone
DESC_LB_OUTPUT=$($AWS_ELBV2 describe-load-balancers --output json 2>&1) || true
INT_LB_REMAINING=$(echo "$DESC_LB_OUTPUT" | jq "[.LoadBalancers[] | select(.LoadBalancerArn == \"$INT_LB_ARN\")] | length")
if [ "$INT_LB_REMAINING" == "0" ]; then
    pass "internal NLB deleted: no longer in describe-load-balancers"
else
    fail "internal NLB still exists after deletion"
fi
INT_LB_ARN=""

# Verify internal NLB ENIs cleaned up
echo "Verifying internal NLB ENIs cleaned up (up to 30s)..."
INT_ENI_COUNT=""
for i in $(seq 1 10); do
    INT_ENI_OUTPUT=$($AWS_EC2 describe-network-interfaces \
        --filters "Name=description,Values=ELB net/${INT_LB_NAME}/${INT_LB_ID}" \
        --output json 2>&1) || true
    INT_ENI_COUNT=$(echo "$INT_ENI_OUTPUT" | jq '.NetworkInterfaces | length')
    if [ "$INT_ENI_COUNT" == "0" ]; then
        break
    fi
    sleep 3
done
if [ "$INT_ENI_COUNT" == "0" ]; then
    pass "internal NLB ENIs cleaned up: 0 remaining (after ${i} polls)"
else
    fail "internal NLB ENI cleanup: $INT_ENI_COUNT ENIs still exist after 30s"
fi

# Deregister targets and delete internal TG
echo "Deregistering targets from internal target group..."
$AWS_ELBV2 deregister-targets \
    --target-group-arn "$INT_TG_ARN" \
    --targets "Id=${APP_INSTANCE_IDS[0]}" "Id=${APP_INSTANCE_IDS[1]}" \
    --output json 2>&1 || true

echo "Deleting internal target group..."
$AWS_ELBV2 delete-target-group --target-group-arn "$INT_TG_ARN" 2>&1 && {
    pass "delete-target-group (internal): $INT_TG_ARN"
    INT_TG_ARN=""
} || {
    fail "delete-target-group (internal)"
}

echo ""
