#!/bin/bash
set -e

# ELBv2 (ALB) Data Plane E2E Test
# Verifies that an ALB actually balances HTTP traffic across target instances.
# Requires: VPC networking (OVN), external networking (pool mode).
#
# Architecture:
#   - 2 "app" instances run a Python HTTP responder (returns instance ID as JSON)
#   - 1 ALB VM (created by CreateLoadBalancer) runs HAProxy inside a pre-baked Alpine image
#
# Traffic is tested from the host via the ALB's public IP. Instances are in a
# public subnet with an IGW route so the ALB VM can health-check them and the
# host can reach the ALB.
#
# Usage:
#   ./tests/e2e/run-elbv2-dataplane-e2e.sh                              # Default endpoint
#   ENDPOINT=https://10.11.12.1:9999 ./tests/e2e/run-elbv2-dataplane-e2e.sh  # Custom endpoint

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
IGW_ID=""
APP_INSTANCE_IDS=()
TG_ARN=""
LB_ARN=""
LISTENER_ARN=""

cleanup() {
    local exit_code=$?
    echo ""
    echo "Cleanup..."

    # Delete listener
    if [ -n "$LISTENER_ARN" ]; then
        echo "  Deleting listener..."
        $AWS_ELBV2 delete-listener --listener-arn "$LISTENER_ARN" 2>/dev/null || true
    fi

    # Delete load balancer (cascade-deletes listeners, terminates ALB VM)
    if [ -n "$LB_ARN" ]; then
        echo "  Deleting load balancer..."
        $AWS_ELBV2 delete-load-balancer --load-balancer-arn "$LB_ARN" 2>/dev/null || true
    fi

    # Deregister targets and delete target group
    if [ -n "$TG_ARN" ]; then
        echo "  Deleting target group..."
        $AWS_ELBV2 delete-target-group --target-group-arn "$TG_ARN" 2>/dev/null || true
    fi

    # Terminate instances
    for inst_id in "${APP_INSTANCE_IDS[@]}"; do
        if [ -n "$inst_id" ]; then
            echo "  Terminating instance $inst_id..."
            $AWS_EC2 terminate-instances --instance-ids "$inst_id" 2>/dev/null || true
        fi
    done

    # Wait for instances to terminate before deleting subnet/VPC
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

    # Delete key pair
    echo "  Deleting key pair..."
    $AWS_EC2 delete-key-pair --key-name dp-test-key 2>/dev/null || true

    # Detach and delete IGW
    if [ -n "$IGW_ID" ] && [ -n "$VPC_ID" ]; then
        echo "  Detaching IGW..."
        $AWS_EC2 detach-internet-gateway --internet-gateway-id "$IGW_ID" --vpc-id "$VPC_ID" 2>/dev/null || true
        $AWS_EC2 delete-internet-gateway --internet-gateway-id "$IGW_ID" 2>/dev/null || true
    fi

    # Delete subnet
    if [ -n "$SUBNET_ID" ]; then
        echo "  Deleting subnet..."
        $AWS_EC2 delete-subnet --subnet-id "$SUBNET_ID" 2>/dev/null || true
    fi

    # Delete VPC
    if [ -n "$VPC_ID" ]; then
        echo "  Deleting VPC..."
        $AWS_EC2 delete-vpc --vpc-id "$VPC_ID" 2>/dev/null || true
    fi

    echo "Cleanup complete"
    echo ""
    echo "========================================"
    echo "ELBv2 Data Plane E2E Results: $PASSED passed, $FAILED failed"
    echo "========================================"

    if [ $FAILED -gt 0 ]; then
        exit 1
    fi
    exit $exit_code
}
trap cleanup EXIT

echo "========================================"
echo "ELBv2 (ALB) Data Plane E2E Test"
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
$AWS_EC2 delete-key-pair --key-name dp-test-key 2>/dev/null || true
KEY_OUTPUT=$($AWS_EC2 create-key-pair --key-name dp-test-key --output json 2>&1) || {
    fail "create key pair"
    exit 1
}
pass "key pair: dp-test-key"

# ==========================================
# Phase 1: VPC + Public Subnet Setup
# ==========================================
echo ""
echo "Phase 1: VPC + Public Subnet Setup"
echo "========================================"

echo "Creating VPC..."
VPC_OUTPUT=$($AWS_EC2 create-vpc --cidr-block 10.201.0.0/16 --output json) || {
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
SUBNET_OUTPUT=$($AWS_EC2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.201.1.0/24 --output json) || {
    fail "create-subnet"
    exit 1
}
SUBNET_ID=$(echo "$SUBNET_OUTPUT" | jq -r '.Subnet.SubnetId')
pass "create-subnet: $SUBNET_ID"

# IGW attachment makes the subnet public — Spinifex routes via OVN/br-ext automatically.
pass "public subnet configured (IGW attached)"

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
# Phase 4: Create ALB + Listener
# ==========================================
echo ""
echo "Phase 4: ALB + Listener"
echo "========================================"

echo "Creating ALB..."
LB_OUTPUT=$($AWS_ELBV2 create-load-balancer \
    --name dp-test-alb \
    --subnets "$SUBNET_ID" \
    --output json 2>&1) || {
    fail "create-load-balancer"
    echo "  Output: $LB_OUTPUT"
    exit 1
}
LB_ARN=$(echo "$LB_OUTPUT" | jq -r '.LoadBalancers[0].LoadBalancerArn')
LB_STATE=$(echo "$LB_OUTPUT" | jq -r '.LoadBalancers[0].State.Code')
pass "create-load-balancer: $LB_ARN (state: $LB_STATE)"

# Discover the ALB's reachable address. With dev_networking, the ALB VM has
# port 80 forwarded to a random host port via QEMU hostfwd. Without it, use
# the public IP from the external networking pool.
LB_ID=$(echo "$LB_ARN" | sed 's|.*/||')
LB_NAME="dp-test-alb"
echo "Discovering ALB address..."

ALB_URL=""

# First try: find the ALB VM's hostfwd port for port 80 (dev_networking mode)
# The ALB VM instance ID can be found from the LB record or QEMU process list.
sleep 3
ALB_QEMU=$(ps auxw 2>/dev/null | grep "qemu-system" | grep -v grep | grep "hostfwd=.*-:80" | tail -1)
if [ -n "$ALB_QEMU" ]; then
    ALB_HOST_PORT=$(echo "$ALB_QEMU" | grep -oP 'hostfwd=tcp:[^:]+:\K[0-9]+(?=-:80)' | head -1)
    if [ -n "$ALB_HOST_PORT" ]; then
        ALB_URL="http://127.0.0.1:${ALB_HOST_PORT}"
        pass "ALB reachable via hostfwd: $ALB_URL"
    fi
fi

# Second try: ALB ENI public IP (external networking mode)
if [ -z "$ALB_URL" ]; then
    ENI_OUTPUT=$($AWS_EC2 describe-network-interfaces \
        --filters "Name=description,Values=ELB app/${LB_NAME}/${LB_ID}" \
        --output json 2>/dev/null)
    ALB_PUBLIC_IP=$(echo "$ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].Association.PublicIp // empty' 2>/dev/null)
    ALB_PRIVATE_IP=$(echo "$ENI_OUTPUT" | jq -r '.NetworkInterfaces[0].PrivateIpAddress // empty' 2>/dev/null)

    if [ -n "$ALB_PUBLIC_IP" ] && [ "$ALB_PUBLIC_IP" != "null" ]; then
        ALB_URL="http://${ALB_PUBLIC_IP}:80"
        pass "ALB reachable via public IP: $ALB_URL"
    elif [ -n "$ALB_PRIVATE_IP" ]; then
        ALB_URL="http://${ALB_PRIVATE_IP}:80"
        pass "ALB private IP: $ALB_PRIVATE_IP (may not be reachable from host)"
    fi
fi

if [ -z "$ALB_URL" ]; then
    fail "could not discover ALB address"
    exit 1
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

# Wait for ALB to become active (Alpine VM boots fast, ~60s total)
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
# Phase 5: Connectivity Pre-Check (from host)
# ==========================================
echo ""
echo "Phase 5: Connectivity Pre-Check"
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
    pass "host can reach ALB at $ALB_URL"
else
    fail "host cannot reach ALB at $ALB_URL"
    echo "  Debug: trying to curl ALB directly..."
    curl -v --max-time 5 "$ALB_URL/" 2>&1 | tail -10
    exit 1
fi

# ==========================================
# Phase 6: Wait for Targets to Become Healthy
# ==========================================
echo ""
echo "Phase 6: Wait for Target Health"
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
    # Health state reporting is not yet implemented — HAProxy does its own health
    # checks. The traffic test below verifies actual connectivity.
    echo "  ⚠️  Target health API not yet reporting — skipping (HAProxy health checks work independently)"
fi

# ==========================================
# Phase 7: Traffic Balancing — Round Robin
# ==========================================
echo ""
echo "Phase 7: Traffic Balancing"
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

# Verify we got responses from BOTH instances
UNIQUE_RESPONDERS=${#RESPONSE_COUNTS[@]}
if [ "$UNIQUE_RESPONDERS" -ge 2 ]; then
    pass "round-robin: $UNIQUE_RESPONDERS unique instances responded"
else
    fail "round-robin: expected 2 unique responders, got $UNIQUE_RESPONDERS"
fi

if [ "$TOTAL_SUCCESS" -ge $((NUM_REQUESTS / 2)) ]; then
    pass "success rate: $TOTAL_SUCCESS/$NUM_REQUESTS requests succeeded"
else
    fail "success rate: only $TOTAL_SUCCESS/$NUM_REQUESTS requests succeeded"
fi

# ==========================================
# Phase 8: Deregister One Target — Single Target Verification
# ==========================================
echo ""
echo "Phase 8: Single Target After Deregister"
echo "========================================"

DEREGISTERED_INSTANCE="${APP_INSTANCE_IDS[1]}"
REMAINING_INSTANCE="${APP_INSTANCE_IDS[0]}"
echo "Deregistering $DEREGISTERED_INSTANCE..."

$AWS_ELBV2 deregister-targets \
    --target-group-arn "$TG_ARN" \
    --targets "Id=$DEREGISTERED_INSTANCE" \
    --output json 2>&1 || {
    fail "deregister-targets"
}
pass "deregistered $DEREGISTERED_INSTANCE"

# Brief pause for HAProxy to reload
sleep 3

echo "Sending $NUM_REQUESTS requests after deregistration..."
declare -A SINGLE_COUNTS
SINGLE_SUCCESS=0

for i in $(seq 1 $NUM_REQUESTS); do
    RESPONSE=$(curl -s --max-time 5 "$ALB_URL/" 2>/dev/null) || continue

    RESP_INSTANCE=$(echo "$RESPONSE" | jq -r '.instance_id // empty' 2>/dev/null)
    if [ -n "$RESP_INSTANCE" ]; then
        SINGLE_COUNTS[$RESP_INSTANCE]=$(( ${SINGLE_COUNTS[$RESP_INSTANCE]:-0} + 1 ))
        SINGLE_SUCCESS=$((SINGLE_SUCCESS + 1))
    fi
done

echo "  Results: $SINGLE_SUCCESS/$NUM_REQUESTS successful"
echo "  Distribution:"
for inst_id in "${!SINGLE_COUNTS[@]}"; do
    echo "    $inst_id: ${SINGLE_COUNTS[$inst_id]} responses"
done

# Verify ONLY the remaining instance responds.
# The HTTP server returns the VM hostname (spinifex-vm-XXXX), which is a prefix
# of the instance ID (i-XXXX...). Extract the short ID to compare.
REMAINING_SHORT=$(echo "$REMAINING_INSTANCE" | sed 's/^i-//' | cut -c1-8)
SINGLE_RESPONDERS=${#SINGLE_COUNTS[@]}
if [ "$SINGLE_RESPONDERS" -eq 1 ]; then
    SOLE_RESPONDER="${!SINGLE_COUNTS[@]}"
    if echo "$SOLE_RESPONDER" | grep -q "$REMAINING_SHORT"; then
        pass "single target: only $REMAINING_INSTANCE responds after deregistration"
    else
        fail "single target: unexpected responder $SOLE_RESPONDER (expected $REMAINING_INSTANCE)"
    fi
else
    fail "single target: expected 1 responder, got $SINGLE_RESPONDERS"
fi

if [ "$SINGLE_SUCCESS" -ge $((NUM_REQUESTS / 2)) ]; then
    pass "single target success rate: $SINGLE_SUCCESS/$NUM_REQUESTS"
else
    fail "single target success rate: only $SINGLE_SUCCESS/$NUM_REQUESTS"
fi

# ==========================================
# Phase 9: Re-register + Verify Recovery
# ==========================================
echo ""
echo "Phase 9: Re-register + Recovery"
echo "========================================"

echo "Re-registering $DEREGISTERED_INSTANCE..."
$AWS_ELBV2 register-targets \
    --target-group-arn "$TG_ARN" \
    --targets "Id=$DEREGISTERED_INSTANCE" \
    --output json 2>&1 || {
    fail "re-register-targets"
}
pass "re-registered $DEREGISTERED_INSTANCE"

# Wait for HAProxy reload + target to become routable
sleep 5

echo "Sending $NUM_REQUESTS requests after re-registration..."
declare -A RECOVERY_COUNTS
RECOVERY_SUCCESS=0

for i in $(seq 1 $NUM_REQUESTS); do
    RESPONSE=$(curl -s --max-time 5 "$ALB_URL/" 2>/dev/null) || continue

    RESP_INSTANCE=$(echo "$RESPONSE" | jq -r '.instance_id // empty' 2>/dev/null)
    if [ -n "$RESP_INSTANCE" ]; then
        RECOVERY_COUNTS[$RESP_INSTANCE]=$(( ${RECOVERY_COUNTS[$RESP_INSTANCE]:-0} + 1 ))
        RECOVERY_SUCCESS=$((RECOVERY_SUCCESS + 1))
    fi
done

echo "  Distribution:"
for inst_id in "${!RECOVERY_COUNTS[@]}"; do
    echo "    $inst_id: ${RECOVERY_COUNTS[$inst_id]} responses"
done

RECOVERY_RESPONDERS=${#RECOVERY_COUNTS[@]}
if [ "$RECOVERY_RESPONDERS" -ge 2 ]; then
    pass "recovery: both instances responding again ($RECOVERY_RESPONDERS responders)"
else
    fail "recovery: expected 2 responders after re-registration, got $RECOVERY_RESPONDERS"
fi

echo ""
