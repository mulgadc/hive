#!/bin/bash
set -e

# ELBv2 (ALB) Internal Scheme Data Plane E2E Test
# Verifies that an internal ALB works correctly. The ALB agent reaches
# the gateway via the management NIC (br-mgmt), health checks reach targets
# over the VPC's OVN L2 network, and traffic from a VPC client is balanced
# across targets via the ALB's private IP.
#
# Key validations:
#   - ALB reaches active state via mgmt NIC heartbeat
#   - Scheme is "internal", DNS has "internal-" prefix
#   - ENI has NO public IP
#   - ALB health-checks targets within the VPC (OVN L2)
#   - VPC client can reach ALB via private IP and gets round-robin responses
#   - ENI cleanup on deletion
#
# Architecture:
#   - 2 "app" instances run a Python HTTP responder (returns instance ID)
#   - 1 internal ALB (HAProxy) balances traffic to the app instances
#   - 1 "client" instance curls the ALB's private IP from inside the VPC,
#     then serves the results via HTTP on its own public IP so the host can
#     fetch them. This avoids needing SSH/hostfwd/dev_networking.
#
# Requires: Pool mode with external IPAM (NOT dev_networking).
#
# Usage:
#   ./tests/e2e/run-elbv2-dataplane-e2e.sh
#   ENDPOINT=https://10.11.12.1:9999 ./tests/e2e/run-elbv2-dataplane-e2e.sh

cd "$(dirname "$0")/../.."

# Dev mode gate — skip when external IPAM is not available
SPINIFEX_CONFIG="${HOME}/spinifex/config/spinifex.toml"
if [ -f "$SPINIFEX_CONFIG" ]; then
    if grep -q 'dev_networking = true' "$SPINIFEX_CONFIG"; then
        echo "Skipping internal ALB E2E: dev_networking is enabled (no external IPAM)"
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
CLIENT_INSTANCE_ID=""
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

    # Terminate all instances (app + client)
    ALL_INSTANCES=("${APP_INSTANCE_IDS[@]}")
    if [ -n "$CLIENT_INSTANCE_ID" ]; then
        ALL_INSTANCES+=("$CLIENT_INSTANCE_ID")
    fi

    for inst_id in "${ALL_INSTANCES[@]}"; do
        if [ -n "$inst_id" ]; then
            echo "  Terminating instance $inst_id..."
            $AWS_EC2 terminate-instances --instance-ids "$inst_id" 2>/dev/null || true
        fi
    done

    if [ ${#ALL_INSTANCES[@]} -gt 0 ]; then
        echo "  Waiting for instances to terminate..."
        for attempt in $(seq 1 30); do
            ALL_TERMINATED=true
            for inst_id in "${ALL_INSTANCES[@]}"; do
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
    $AWS_EC2 delete-key-pair --key-name dp-test-key 2>/dev/null || true

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
    echo "Internal ALB Data Plane E2E Results: $PASSED passed, $FAILED failed"
    echo "========================================"

    if [ $FAILED -gt 0 ]; then
        exit 1
    fi
    exit $exit_code
}
trap cleanup EXIT

echo "========================================"
echo "ELBv2 (ALB) Internal Scheme Data Plane E2E"
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
# Use the Ubuntu AMI for app instances. The ALB system image (Alpine)
# contains "alb" in its name — skip it and find a general-purpose image.
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
$AWS_EC2 delete-key-pair --key-name dp-test-key 2>/dev/null || true
KEY_OUTPUT=$($AWS_EC2 create-key-pair --key-name dp-test-key --output json 2>&1) || {
    fail "create key pair"
    exit 1
}
pass "key pair: dp-test-key"

# ==========================================
# Phase 1: VPC + Subnet Setup
# ==========================================
echo ""
echo "Phase 1: VPC + Subnet Setup"
echo "========================================"

echo "Creating VPC..."
VPC_OUTPUT=$($AWS_EC2 create-vpc --cidr-block 10.201.0.0/16 --output json) || {
    fail "create-vpc"
    exit 1
}
VPC_ID=$(echo "$VPC_OUTPUT" | jq -r '.Vpc.VpcId')
pass "create-vpc: $VPC_ID"

# IGW provides external connectivity for the client VM to serve results to
# the host. The internal ALB itself does NOT use the IGW (no public IP).
echo "Creating internet gateway (for client VM access)..."
IGW_OUTPUT=$($AWS_EC2 create-internet-gateway --output json) || {
    fail "create-internet-gateway"
    exit 1
}
IGW_ID=$(echo "$IGW_OUTPUT" | jq -r '.InternetGateway.InternetGatewayId')
$AWS_EC2 attach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" || {
    fail "attach-internet-gateway"
    exit 1
}
pass "internet gateway: $IGW_ID (attached — for client VM only)"

echo "Creating subnet..."
SUBNET_OUTPUT=$($AWS_EC2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.201.1.0/24 --output json) || {
    fail "create-subnet"
    exit 1
}
SUBNET_ID=$(echo "$SUBNET_OUTPUT" | jq -r '.Subnet.SubnetId')
pass "create-subnet: $SUBNET_ID"

# Enable auto-assign public IP so app + client instances get public IPs.
# The IGW + MapPublicIpOnLaunch gives all instances external connectivity.
# The internal ALB itself does NOT get a public IP (scheme=internal overrides).
$AWS_EC2 modify-subnet-attribute --subnet-id "$SUBNET_ID" \
    --map-public-ip-on-launch 2>&1 || {
    fail "modify-subnet-attribute"
    exit 1
}
pass "MapPublicIpOnLaunch enabled"

# ==========================================
# Phase 2: Launch App Instances
# ==========================================
echo ""
echo "Phase 2: Launch App Instances"
echo "========================================"

# Cloud-init user data for app instances: start a minimal HTTP server that
# responds with the instance ID. The instance ID is discovered from the hostname
# (Spinifex sets hostname to instance ID).
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
        --key-name dp-test-key \
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

# Poll instances to running state
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

# Collect app instance private IPs
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

# Wait for cloud-init + user-data HTTP server to start.
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
    --name dp-test-tg \
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
# Phase 4: Create Internal ALB + Listener
# ==========================================
echo ""
echo "Phase 4: Internal ALB + Listener"
echo "========================================"

echo "Creating internal ALB..."
LB_OUTPUT=$($AWS_ELBV2 create-load-balancer \
    --name dp-test-alb \
    --scheme internal \
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

if [ "$LB_SCHEME" == "internal" ]; then
    pass "scheme confirmed: internal"
else
    fail "scheme mismatch: expected internal, got $LB_SCHEME"
fi

# ==========================================
# Phase 5: Verify ALB ENI — No Public IP
# ==========================================
echo ""
echo "Phase 5: Verify ALB ENI (No Public IP)"
echo "========================================"

LB_ID=$(echo "$LB_ARN" | sed 's|.*/||')
LB_NAME="dp-test-alb"

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
echo "  Public IP: ${ALB_PUBLIC_IP:-(none)}"

if [ -z "$ALB_PUBLIC_IP" ] || [ "$ALB_PUBLIC_IP" == "null" ]; then
    pass "internal ALB has no public IP (correct)"
else
    fail "internal ALB should NOT have a public IP, but got: $ALB_PUBLIC_IP"
fi

if [ -n "$ALB_PRIVATE_IP" ] && [ "$ALB_PRIVATE_IP" != "null" ]; then
    pass "ALB has private IP: $ALB_PRIVATE_IP"
else
    fail "ALB has no private IP"
    exit 1
fi

# Verify DNS name HAS internal- prefix
LB_DNS=$($AWS_ELBV2 describe-load-balancers --load-balancer-arns "$LB_ARN" \
    --query 'LoadBalancers[0].DNSName' --output text 2>/dev/null)
echo "  DNS name: $LB_DNS"
if echo "$LB_DNS" | grep -q "^internal-"; then
    pass "DNS name has internal- prefix"
else
    fail "DNS name missing internal- prefix for internal ALB: $LB_DNS"
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

# ==========================================
# Phase 6: ALB Reaches Active (mgmt NIC heartbeat)
# ==========================================
echo ""
echo "Phase 6: ALB Active (mgmt NIC heartbeat)"
echo "========================================"

echo "Waiting for ALB to become active (agent heartbeat via mgmt NIC)..."
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
    pass "ALB state: active (agent heartbeat via mgmt NIC)"
else
    fail "ALB did not reach active state (stuck in $LB_STATE)"
    echo ""
    echo "  The ALB agent must reach the gateway via the management NIC (br-mgmt),"
    echo "  independent of VPC routing/IGW."
    echo ""
    echo "  Debug: daemon logs:"
    grep -iE 'LaunchSystemInstance|ALB.VM|alb-agent|mgmt|MgmtTap|MgmtIP|heartbeat' ~/spinifex/logs/spinifex.log 2>/dev/null | tail -20 || echo "  (no matching log lines)"
    exit 1
fi

# ==========================================
# Phase 7: Wait for Targets to Become Healthy
# ==========================================
echo ""
echo "Phase 7: Wait for Target Health (ALB -> targets via VPC)"
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
    pass "both targets healthy (ALB health-checked targets via VPC network)"
else
    echo "  Current target health:"
    echo "$HEALTH_OUTPUT" | jq -r '.TargetHealthDescriptions[] | "    \(.Target.Id): \(.TargetHealth.State) (\(.TargetHealth.Reason // "n/a"))"'
    fail "targets did not become healthy within ${HEALTH_TIMEOUT}s"
fi

# ==========================================
# Phase 8: Launch VPC Client + Traffic Balancing
# ==========================================
echo ""
echo "Phase 8: Traffic Balancing (VPC client -> internal ALB)"
echo "========================================"

# Launch a client VM in the same subnet. Its cloud-init curls the ALB's
# private IP from inside the VPC and serves results via HTTP. The client gets
# a public IP (IGW is attached) so the host can fetch the results.
echo "Launching client VM to test ALB from inside the VPC..."

CLIENT_USER_DATA=$(cat <<USERDATA
#!/bin/bash
ALB_IP="${ALB_PRIVATE_IP}"
NUM_REQUESTS=20

mkdir -p /tmp/httpd
cd /tmp/httpd

# Wait for ALB to respond (up to 5 min)
echo "waiting" > status.txt
nohup python3 -m http.server 80 --bind 0.0.0.0 > /dev/null 2>&1 &

for i in \$(seq 1 60); do
    if curl -s --max-time 3 "http://\${ALB_IP}:80/" 2>/dev/null | grep -q instance_id; then
        break
    fi
    sleep 5
done

# Send test requests and collect responses (one JSON per line)
> results.txt
for i in \$(seq 1 \$NUM_REQUESTS); do
    curl -s --max-time 5 "http://\${ALB_IP}:80/" >> results.txt 2>/dev/null
    echo "" >> results.txt
done

echo "done" > status.txt
USERDATA
)

CLIENT_OUTPUT=$($AWS_EC2 run-instances \
    --image-id "$AMI_ID" \
    --instance-type "$INSTANCE_TYPE" \
    --key-name dp-test-key \
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
echo "Waiting for client cloud-init + ALB test (~120s)..."
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
RESULTS=$(curl -s --max-time 10 "http://${CLIENT_PUBLIC_IP}:80/results.txt" 2>/dev/null)

if [ -z "$RESULTS" ]; then
    fail "could not fetch results from client VM"
    exit 1
fi

# Parse results: count unique instance_id values
declare -A RESPONSE_COUNTS
TOTAL_SUCCESS=0
TOTAL_FAIL=0

while IFS= read -r line; do
    [ -z "$line" ] && continue
    RESP_INSTANCE=$(echo "$line" | jq -r '.instance_id // empty' 2>/dev/null)
    if [ -n "$RESP_INSTANCE" ]; then
        RESPONSE_COUNTS[$RESP_INSTANCE]=$(( ${RESPONSE_COUNTS[$RESP_INSTANCE]:-0} + 1 ))
        TOTAL_SUCCESS=$((TOTAL_SUCCESS + 1))
    else
        TOTAL_FAIL=$((TOTAL_FAIL + 1))
    fi
done <<< "$RESULTS"

echo "  Results: $TOTAL_SUCCESS successful, $TOTAL_FAIL failed"
echo "  Distribution:"
for inst_id in "${!RESPONSE_COUNTS[@]}"; do
    echo "    $inst_id: ${RESPONSE_COUNTS[$inst_id]} responses"
done

# Verify we got responses from BOTH instances
UNIQUE_RESPONDERS=${#RESPONSE_COUNTS[@]}
if [ "$UNIQUE_RESPONDERS" -ge 2 ]; then
    pass "round-robin via private IP: $UNIQUE_RESPONDERS unique instances responded"
else
    fail "round-robin: expected 2 unique responders, got $UNIQUE_RESPONDERS"
fi

if [ "$TOTAL_SUCCESS" -ge 10 ]; then
    pass "success rate: $TOTAL_SUCCESS/20 requests succeeded"
else
    fail "success rate: only $TOTAL_SUCCESS/20 requests succeeded"
fi

# ==========================================
# Phase 9: Cleanup Verification — ENI removed after deletion
# ==========================================
echo ""
echo "Phase 9: Cleanup Verification"
echo "========================================"

echo "Deleting internal ALB..."
$AWS_ELBV2 delete-load-balancer --load-balancer-arn "$LB_ARN" 2>&1 || {
    fail "delete-load-balancer"
}

echo "Verifying ENI cleanup..."
ENI_CLEANED=false
for attempt in $(seq 1 10); do
    ENI_CHECK=$($AWS_EC2 describe-network-interfaces \
        --filters "Name=description,Values=ELB app/${LB_NAME}/${LB_ID}" \
        --query 'NetworkInterfaces | length(@)' --output text 2>/dev/null)
    if [ "$ENI_CHECK" == "0" ] || [ -z "$ENI_CHECK" ]; then
        ENI_CLEANED=true
        break
    fi
    sleep 3
done

if [ "$ENI_CLEANED" = true ]; then
    pass "ALB ENI cleaned up after deletion"
else
    fail "ALB ENI still exists after deletion"
fi

# Clear so cleanup trap doesn't try again
LB_ARN=""
LISTENER_ARN=""

pass "internal ALB lifecycle complete"

echo ""
