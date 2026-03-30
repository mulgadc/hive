#!/bin/bash
set -e

# ELBv2 (ALB) Internet-Facing Data Plane E2E Test
# Verifies that an internet-facing ALB gets a public IP on its ENI and is
# reachable from both the local host AND a peer node (external validation).
#
# Requires:
#   - Pool mode with external IPAM (NOT dev_networking)
#   - At least 2 cluster nodes (peer node used for external validation)
#
# Usage:
#   ./tests/e2e/run-elbv2-dataplane-internet-facing-e2e.sh <peer_node_ip>
#   ENDPOINT=https://10.11.12.1:9999 ./tests/e2e/run-elbv2-dataplane-internet-facing-e2e.sh <peer_node_ip>

cd "$(dirname "$0")/../.."

# ==========================================================================
# Dev mode gate — skip when external IPAM is not available
# ==========================================================================
SPINIFEX_CONFIG="${HOME}/spinifex/config/spinifex.toml"
if [ -f "$SPINIFEX_CONFIG" ]; then
    if grep -q 'dev_networking = true' "$SPINIFEX_CONFIG"; then
        echo "⚠ Skipping internet-facing E2E: dev_networking is enabled (no external IPAM)"
        echo "  This test requires pool mode with external networking."
        exit 0
    fi
fi

# ==========================================================================
# Arguments
# ==========================================================================
if [ $# -lt 1 ]; then
    echo "Usage: $0 <peer_node_ip>"
    echo "  peer_node_ip: IP of another cluster node for external validation"
    exit 1
fi

PEER_NODE_IP="$1"

ENDPOINT="${ENDPOINT:-https://127.0.0.1:9999}"
export AWS_PROFILE=spinifex
AWS_EC2="aws --endpoint-url ${ENDPOINT} ec2"
AWS_ELBV2="aws --endpoint-url ${ENDPOINT} elbv2"

SSH_KEY_PATH="$HOME/.ssh/tf-user-ap-southeast-2"

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

# SSH to peer node (same pattern as run-multinode-e2e.sh)
peer_ssh() {
    local ip="$1"; shift
    ssh -i "$SSH_KEY_PATH" \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=10 \
        -o LogLevel=ERROR \
        "tf-user@${ip}" "$@"
}

# Track created resources for cleanup
VPC_ID=""
SUBNET_IDS=()
IGW_ID=""
APP_INSTANCE_IDS=()
TG_ARN=""
LB_ARN=""
LISTENER_ARN=""

cleanup() {
    local exit_code=$?
    echo ""
    echo "Cleanup..."

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
    $AWS_EC2 delete-key-pair --key-name dp-inet-test-key 2>/dev/null || true

    if [ -n "$IGW_ID" ] && [ -n "$VPC_ID" ]; then
        echo "  Detaching IGW..."
        $AWS_EC2 detach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" 2>/dev/null || true
        $AWS_EC2 delete-internet-gateway --internet-gateway-id "$IGW_ID" 2>/dev/null || true
    fi

    for sid in "${SUBNET_IDS[@]}"; do
        if [ -n "$sid" ]; then
            echo "  Deleting subnet $sid..."
            $AWS_EC2 delete-subnet --subnet-id "$sid" 2>/dev/null || true
        fi
    done

    if [ -n "$VPC_ID" ]; then
        echo "  Deleting VPC..."
        $AWS_EC2 delete-vpc --vpc-id "$VPC_ID" 2>/dev/null || true
    fi

    echo "Cleanup complete"
    echo ""
    echo "========================================"
    echo "Internet-Facing ALB E2E Results: $PASSED passed, $FAILED failed"
    echo "========================================"

    if [ $FAILED -gt 0 ]; then
        exit 1
    fi
    exit $exit_code
}
trap cleanup EXIT

echo "========================================"
echo "ELBv2 (ALB) Internet-Facing Data Plane E2E"
echo "========================================"
echo "Endpoint:  $ENDPOINT"
echo "Peer node: $PEER_NODE_IP"
echo ""

# ==========================================
# Phase 0: Prerequisites
# ==========================================
echo "Phase 0: Prerequisites"
echo "========================================"

echo "Verifying SSH to peer node..."
if peer_ssh "$PEER_NODE_IP" "hostname" > /dev/null 2>&1; then
    pass "SSH to peer node $PEER_NODE_IP"
else
    fail "cannot SSH to peer node $PEER_NODE_IP"
    echo "  External validation requires SSH access to a peer cluster node."
    exit 1
fi

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
AMI_ID=$(echo "$ALL_IMAGES" | jq -r '[.Images[] | select(.Name | test("alb") | not)][0].ImageId // empty')
if [ -z "$AMI_ID" ]; then
    AMI_ID=$(echo "$ALL_IMAGES" | jq -r '.Images[0].ImageId // empty')
fi
if [ -z "$AMI_ID" ] || [ "$AMI_ID" == "None" ]; then
    echo "ERROR: No AMI found — import an image first"
    exit 1
fi
pass "AMI: $AMI_ID"

echo "Creating key pair..."
$AWS_EC2 delete-key-pair --key-name dp-inet-test-key 2>/dev/null || true
KEY_OUTPUT=$($AWS_EC2 create-key-pair --key-name dp-inet-test-key --output json 2>&1) || {
    fail "create key pair"
    exit 1
}
pass "key pair: dp-inet-test-key"

# ==========================================
# Phase 1: VPC + Public Subnet Setup
# ==========================================
echo ""
echo "Phase 1: VPC + Public Subnet Setup"
echo "========================================"

echo "Creating VPC..."
VPC_OUTPUT=$($AWS_EC2 create-vpc --cidr-block 10.202.0.0/16 --output json) || {
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

echo "Creating public subnet..."
SUBNET_OUTPUT=$($AWS_EC2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.202.1.0/24 --output json) || {
    fail "create-subnet"
    exit 1
}
SUBNET_ID=$(echo "$SUBNET_OUTPUT" | jq -r '.Subnet.SubnetId')
SUBNET_IDS+=("$SUBNET_ID")
pass "create-subnet: $SUBNET_ID"

pass "public subnet configured (IGW attached)"

# ==========================================
# Phase 2: Launch App Instances
# ==========================================
echo ""
echo "Phase 2: Launch App Instances"
echo "========================================"

APP_USER_DATA=$(cat <<'USERDATA'
#!/bin/bash
INSTANCE_ID=$(hostname)
mkdir -p /tmp/httpd
echo "{\"instance_id\": \"${INSTANCE_ID}\"}" > /tmp/httpd/index.html
cd /tmp/httpd
nohup python3 -m http.server 80 --bind 0.0.0.0 > /dev/null 2>&1 &
USERDATA
)
echo "Launching 2 app instances with HTTP responder..."
for i in 1 2; do
    echo "  Launching app instance $i..."
    RUN_OUTPUT=$($AWS_EC2 run-instances \
        --image-id "$AMI_ID" \
        --instance-type "$INSTANCE_TYPE" \
        --key-name dp-inet-test-key \
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

echo "Waiting for cloud-init to complete (~100s on t3.nano with Ubuntu)..."
sleep 100

# ==========================================
# Phase 3: Create Target Group + Register Targets
# ==========================================
echo ""
echo "Phase 3: Target Group + Registration"
echo "========================================"

echo "Creating target group..."
TG_OUTPUT=$($AWS_ELBV2 create-target-group \
    --name dp-inet-tg \
    --protocol HTTP \
    --port 80 \
    --vpc-id "$VPC_ID" \
    --health-check-path "/index.html" \
    --health-check-interval-seconds 5 \
    --healthy-threshold-count 2 \
    --unhealthy-threshold-count 2 \
    --output json 2>&1) || {
    fail "create-target-group"
    echo "  Output: $TG_OUTPUT"
    exit 1
}
TG_ARN=$(echo "$TG_OUTPUT" | jq -r '.TargetGroups[0].TargetGroupArn')
pass "create-target-group: $TG_ARN"

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
# Phase 4: Create Internet-Facing ALB + Listener
# ==========================================
echo ""
echo "Phase 4: Internet-Facing ALB + Listener"
echo "========================================"

echo "Creating internet-facing ALB..."
LB_OUTPUT=$($AWS_ELBV2 create-load-balancer \
    --name dp-inet-alb \
    --scheme internet-facing \
    --subnets "$SUBNET_ID" \
    --output json 2>&1) || {
    fail "create-load-balancer"
    echo "  Output: $LB_OUTPUT"
    exit 1
}
LB_ARN=$(echo "$LB_OUTPUT" | jq -r '.LoadBalancers[0].LoadBalancerArn')
LB_SCHEME=$(echo "$LB_OUTPUT" | jq -r '.LoadBalancers[0].Scheme')
LB_STATE=$(echo "$LB_OUTPUT" | jq -r '.LoadBalancers[0].State.Code')
pass "create-load-balancer: $LB_ARN (scheme: $LB_SCHEME, state: $LB_STATE)"

# Verify scheme is internet-facing
if [ "$LB_SCHEME" == "internet-facing" ]; then
    pass "scheme confirmed: internet-facing"
else
    fail "scheme mismatch: expected internet-facing, got $LB_SCHEME"
fi

# ==========================================
# Phase 5: Verify ALB ENI has Public IP
# ==========================================
echo ""
echo "Phase 5: Verify ALB ENI Public IP"
echo "========================================"

LB_ID=$(echo "$LB_ARN" | sed 's|.*/||')
LB_NAME="dp-inet-alb"

echo "Looking up ALB ENI..."
sleep 3

ENI_OUTPUT=$($AWS_EC2 describe-network-interfaces \
    --filters "Name=description,Values=ELB app/${LB_NAME}/${LB_ID}" \
    --output json 2>/dev/null)

ALB_PUBLIC_IP=$(echo "$ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].Association.PublicIp // empty' 2>/dev/null)
ALB_PRIVATE_IP=$(echo "$ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].PrivateIpAddress // empty' 2>/dev/null)
ALB_ENI_ID=$(echo "$ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].NetworkInterfaceId // empty' 2>/dev/null)

echo "  ENI: $ALB_ENI_ID"
echo "  Private IP: $ALB_PRIVATE_IP"
echo "  Public IP: $ALB_PUBLIC_IP"

if [ -n "$ALB_PUBLIC_IP" ] && [ "$ALB_PUBLIC_IP" != "null" ]; then
    pass "ALB ENI has public IP: $ALB_PUBLIC_IP"
else
    fail "ALB ENI does NOT have a public IP (internet-facing scheme should assign one)"
    echo "  ENI output:"
    echo "$ENI_OUTPUT" | jq .
    echo ""
    echo "  Debug: LOCAL daemon logs (LaunchSystemInstance + IPAM + ALB):"
    grep -E 'LaunchSystemInstance|IPAM|UpdateENI|public.?[Ii][Pp]|AllocateIP|System AMI|systemAMI|instanceLauncher|CreateLoadBalancer' ~/spinifex/logs/*.log 2>/dev/null | tail -30 || echo "  (no matching log lines on local node)"
    echo ""
    echo "  Debug: PEER daemon logs ($PEER_NODE_IP):"
    peer_ssh "$PEER_NODE_IP" "grep -E 'LaunchSystemInstance|IPAM|UpdateENI|public.?[Ii][Pp]|AllocateIP|System AMI|CreateLoadBalancer' ~/spinifex/logs/*.log 2>/dev/null | tail -30" 2>/dev/null || echo "  (no matching log lines on peer)"
    echo ""
    echo "  Debug: external_mode from config:"
    grep -E 'external_mode|external_pool' ~/spinifex/config/spinifex.toml 2>/dev/null || echo "  (not found)"
    exit 1
fi

ALB_URL="http://${ALB_PUBLIC_IP}:80"
echo "  ALB URL: $ALB_URL"

# Verify DNS name does NOT have internal- prefix
LB_DNS=$($AWS_ELBV2 describe-load-balancers --load-balancer-arns "$LB_ARN" \
    --query 'LoadBalancers[0].DNSName' --output text 2>/dev/null)
echo "  DNS name: $LB_DNS"
if echo "$LB_DNS" | grep -q "^internal-"; then
    fail "DNS name has internal- prefix for internet-facing ALB"
else
    pass "DNS name does not have internal- prefix"
fi

echo "Creating listener (port 80 -> target group)..."
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
LISTENER_ARN=$(echo "$LISTENER_OUTPUT" | jq -r '.Listeners[0].ListenerArn')
pass "create-listener: $LISTENER_ARN"

# Wait for ALB to become active
echo "Waiting for ALB to become active (agent ping)..."
ALB_ACTIVE=false
for attempt in $(seq 1 90); do
    LB_STATE=$($AWS_ELBV2 describe-load-balancers --load-balancer-arns "$LB_ARN" \
        --query 'LoadBalancers[0].State.Code' --output text 2>/dev/null)
    if [ "$LB_STATE" == "active" ]; then
        ALB_ACTIVE=true
        break
    fi
    if [ $((attempt % 10)) -eq 0 ]; then
        echo "  ALB state: $LB_STATE (attempt $attempt/90)"
    fi
    sleep 3
done

if [ "$ALB_ACTIVE" = true ]; then
    pass "ALB state: active (agent connected)"
else
    fail "ALB did not reach active state (stuck in $LB_STATE)"
    exit 1
fi

# ==========================================
# Phase 6: Host Connectivity Test
# ==========================================
echo ""
echo "Phase 6: Host Connectivity (via Public IP)"
echo "========================================"

echo "Testing connectivity from host to ALB at $ALB_URL..."
CONNECTIVITY_OK=false
for attempt in $(seq 1 20); do
    if curl -s --max-time 3 "$ALB_URL/" 2>/dev/null | grep -q "instance_id"; then
        CONNECTIVITY_OK=true
        break
    fi
    echo "  Attempt $attempt/20: ALB not yet responding..."
    sleep 5
done

if [ "$CONNECTIVITY_OK" = true ]; then
    pass "host can reach ALB via public IP: $ALB_URL"
else
    fail "host cannot reach ALB at $ALB_URL"
    echo "  Debug: trying to curl ALB directly..."
    curl -v --max-time 5 "$ALB_URL/" 2>&1 | tail -10
    exit 1
fi

# ==========================================
# Phase 7: Wait for Targets to Become Healthy
# ==========================================
echo ""
echo "Phase 7: Wait for Target Health"
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
    pass "both targets healthy"
else
    echo "  Current target health:"
    echo "$HEALTH_OUTPUT" | jq -r '.TargetHealthDescriptions[] | "    \(.Target.Id): \(.TargetHealth.State) (\(.TargetHealth.Reason // "n/a"))"'
    fail "targets did not become healthy within ${HEALTH_TIMEOUT}s"
fi

# ==========================================
# Phase 8: Traffic Balancing from Host
# ==========================================
echo ""
echo "Phase 8: Traffic Balancing (from host)"
echo "========================================"

NUM_REQUESTS=20
echo "Sending $NUM_REQUESTS requests to ALB at $ALB_URL ..."

declare -A RESPONSE_COUNTS
TOTAL_SUCCESS=0
TOTAL_FAIL=0

for i in $(seq 1 $NUM_REQUESTS); do
    RESPONSE=$(curl -s --max-time 5 "$ALB_URL/" 2>/dev/null) || {
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
        continue
    }

    RESP_INSTANCE=$(echo "$RESPONSE" | jq -r '.instance_id // empty' 2>/dev/null)
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
    pass "round-robin from host: $UNIQUE_RESPONDERS unique instances responded"
else
    fail "round-robin from host: expected 2 unique responders, got $UNIQUE_RESPONDERS"
fi

if [ "$TOTAL_SUCCESS" -ge $((NUM_REQUESTS / 2)) ]; then
    pass "host success rate: $TOTAL_SUCCESS/$NUM_REQUESTS requests succeeded"
else
    fail "host success rate: only $TOTAL_SUCCESS/$NUM_REQUESTS requests succeeded"
fi

# ==========================================
# Phase 9: External Validation (from Peer Node)
# ==========================================
echo ""
echo "Phase 9: External Validation (from Peer Node)"
echo "========================================"

echo "Testing ALB reachability from peer node $PEER_NODE_IP..."
echo "  Peer will curl: http://${ALB_PUBLIC_IP}:80/"

PEER_RESULT=$(peer_ssh "$PEER_NODE_IP" "curl -s --max-time 10 http://${ALB_PUBLIC_IP}:80/" 2>/dev/null) || true

if echo "$PEER_RESULT" | jq -r '.instance_id' 2>/dev/null | grep -q .; then
    PEER_INSTANCE=$(echo "$PEER_RESULT" | jq -r '.instance_id')
    pass "peer node reached ALB via public IP (responded: $PEER_INSTANCE)"
else
    fail "peer node could NOT reach ALB at http://${ALB_PUBLIC_IP}:80/"
    echo "  Peer response: $PEER_RESULT"
    echo "  This means the ALB is NOT externally accessible."
fi

# Send multiple requests from peer to verify balancing works externally too
echo "Sending $NUM_REQUESTS requests from peer node..."
declare -A PEER_COUNTS
PEER_SUCCESS=0
PEER_FAIL=0

for i in $(seq 1 $NUM_REQUESTS); do
    RESPONSE=$(peer_ssh "$PEER_NODE_IP" "curl -s --max-time 5 http://${ALB_PUBLIC_IP}:80/" 2>/dev/null) || {
        PEER_FAIL=$((PEER_FAIL + 1))
        continue
    }

    RESP_INSTANCE=$(echo "$RESPONSE" | jq -r '.instance_id // empty' 2>/dev/null)
    if [ -n "$RESP_INSTANCE" ]; then
        PEER_COUNTS[$RESP_INSTANCE]=$(( ${PEER_COUNTS[$RESP_INSTANCE]:-0} + 1 ))
        PEER_SUCCESS=$((PEER_SUCCESS + 1))
    else
        PEER_FAIL=$((PEER_FAIL + 1))
    fi
done

echo "  Results: $PEER_SUCCESS successful, $PEER_FAIL failed"
echo "  Distribution:"
for inst_id in "${!PEER_COUNTS[@]}"; do
    echo "    $inst_id: ${PEER_COUNTS[$inst_id]} responses"
done

PEER_RESPONDERS=${#PEER_COUNTS[@]}
if [ "$PEER_RESPONDERS" -ge 2 ]; then
    pass "round-robin from peer: $PEER_RESPONDERS unique instances responded"
else
    fail "round-robin from peer: expected 2 unique responders, got $PEER_RESPONDERS"
fi

if [ "$PEER_SUCCESS" -ge $((NUM_REQUESTS / 2)) ]; then
    pass "peer success rate: $PEER_SUCCESS/$NUM_REQUESTS requests succeeded"
else
    fail "peer success rate: only $PEER_SUCCESS/$NUM_REQUESTS requests succeeded"
fi

echo ""
