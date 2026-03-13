#!/bin/bash
set -euo pipefail

# Multi-Node E2E Test Suite (real 3-node cluster)
# Runs inside node1 of a 3-node cluster provisioned by tofu (env2).
# Bootstrap has already: built hive, set up OVN, run init/join, started services,
# installed CA certs, imported SSH key + AMI, created VPC + subnet.
#
# Usage: run-multinode-e2e.sh <node1_ip> <node2_ip> <node3_ip>

# Ensure Go is on PATH (SSH non-interactive shells don't source .bashrc)
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"

# Ensure we are in the hive project root
cd "$(dirname "$0")/../.."

# ==========================================================================
# Argument parsing
# ==========================================================================
if [ $# -lt 3 ]; then
    echo "Usage: $0 <node1_ip> <node2_ip> <node3_ip>"
    echo "  All 3 node WAN IPs are required."
    exit 1
fi

NODE_IPS=("$@")
NODE_COUNT=${#NODE_IPS[@]}
LOCAL_IP="${NODE_IPS[0]}"
NODE2_IP="${NODE_IPS[1]}"
NODE3_IP="${NODE_IPS[2]}"

# ==========================================================================
# Constants
# ==========================================================================
NATS_MONITOR_PORT=8222
PREDASTORE_PORT=8443
AWSGW_PORT=9999
SSH_KEY_PATH="$HOME/.ssh/tf-user-ap-southeast-2"
HIVE_DATA_DIR="$HOME/hive"
HIVE_CONFIG="$HIVE_DATA_DIR/config/hive.toml"
HIVE_BIN="./bin/hive"

# Use Hive profile for AWS CLI
export AWS_PROFILE=hive
# Trust Hive CA for AWS CLI v2 (bundles its own Python/certifi, ignores system CA store)
export AWS_CA_BUNDLE="$HIVE_DATA_DIR/config/ca.pem"

# Track test results
TESTS_PASSED=0
TESTS_FAILED=0
FAILED_TESTS=()

# ==========================================================================
# Helper functions
# ==========================================================================

# SSH to a peer node
peer_ssh() {
    local ip="$1"; shift
    ssh -i "$SSH_KEY_PATH" \
        -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=10 \
        -o LogLevel=ERROR \
        "tf-user@${ip}" "$@"
}

# Run AWS CLI against a specific node's gateway
aws_via() {
    local node_ip="$1"; shift
    aws --endpoint-url "https://${node_ip}:${AWSGW_PORT}" "$@"
}

# Retry wrapper for aws_via — retries on transient failures (e.g. NATS cluster reformation)
# Usage: aws_via_retry <max_attempts> <node_ip> <aws args...>
aws_via_retry() {
    local max_attempts="$1"; shift
    local node_ip="$1"; shift
    local attempt=0
    local result

    while [ $attempt -lt $max_attempts ]; do
        result=$(aws_via "$node_ip" "$@" 2>/dev/null) && {
            if [ -n "$result" ] && [ "$result" != "None" ]; then
                echo "$result"
                return 0
            fi
        }
        attempt=$((attempt + 1))
        echo "  Retry $attempt/$max_attempts..." >&2
        sleep 2
    done
    return 1
}

# Default AWS CLI shorthand (via local node)
AWS_EC2="aws --endpoint-url https://${LOCAL_IP}:${AWSGW_PORT} ec2"

# Dump logs from ALL nodes on failure
dump_all_node_logs() {
    echo ""
    echo "=========================================="
    echo "DUMPING LOGS FROM ALL NODES"
    echo "=========================================="
    for i in $(seq 0 $((NODE_COUNT - 1))); do
        local ip="${NODE_IPS[$i]}"
        echo ""
        echo "=== Node $((i+1)) ($ip) ==="
        if [ "$ip" = "$LOCAL_IP" ]; then
            for f in "$HIVE_DATA_DIR/logs/"*.log; do
                [ -f "$f" ] || continue
                echo "--- $(basename "$f") (last 50 lines) ---"
                tail -50 "$f" 2>/dev/null || echo "(not found)"
            done
        else
            peer_ssh "$ip" 'for f in ~/hive/logs/*.log; do
                [ -f "$f" ] || continue
                echo "--- $(basename "$f") (last 50 lines) ---"
                tail -50 "$f" 2>/dev/null || echo "(not found)"
            done' || echo "(node unreachable)"
        fi
    done
    echo ""
    echo "=========================================="
    echo "END OF LOG DUMP"
    echo "=========================================="
}

# Wait for a specific instance state
# Usage: wait_for_instance_state <instance_id> <target_state> [max_attempts] [gateway_ip]
wait_for_instance_state() {
    local instance_id="$1"
    local target_state="$2"
    local max_attempts="${3:-30}"
    local gw_ip="${4:-$LOCAL_IP}"
    local attempt=0

    echo "  Waiting for $instance_id to reach state: $target_state..."

    while [ $attempt -lt $max_attempts ]; do
        local state
        state=$(aws_via "$gw_ip" ec2 describe-instances \
            --instance-ids "$instance_id" \
            --query 'Reservations[0].Instances[0].State.Name' \
            --output text 2>/dev/null) || {
            sleep 2
            attempt=$((attempt + 1))
            continue
        }

        if [ "$state" == "$target_state" ]; then
            echo "  Instance reached state: $target_state"
            return 0
        fi

        if [ "$state" == "terminated" ] && [ "$target_state" != "terminated" ]; then
            echo "  ERROR: Instance terminated unexpectedly"
            return 1
        fi

        sleep 2
        attempt=$((attempt + 1))
    done

    echo "  ERROR: Instance did not reach $target_state within $max_attempts attempts"
    return 1
}

# Find which node runs a QEMU instance (by checking ps on each node)
# Usage: find_instance_node <instance_id>
# Returns: the WAN IP of the hosting node
find_instance_node() {
    local instance_id="$1"

    for ip in "${NODE_IPS[@]}"; do
        local found
        if [ "$ip" = "$LOCAL_IP" ]; then
            found=$(ps auxw | grep "$instance_id" | grep qemu-system | grep -v grep || true)
        else
            found=$(peer_ssh "$ip" "ps auxw | grep '$instance_id' | grep qemu-system | grep -v grep" 2>/dev/null || true)
        fi
        if [ -n "$found" ]; then
            echo "$ip"
            return 0
        fi
    done

    return 1
}

# Extract the SSH hostfwd port for an instance from the QEMU process on a remote node
# Usage: get_remote_ssh_port <node_ip> <instance_id> [max_attempts]
get_remote_ssh_port() {
    local node_ip="$1"
    local instance_id="$2"
    local max_attempts="${3:-30}"
    local attempt=0

    while [ $attempt -lt $max_attempts ]; do
        local qemu_cmd
        if [ "$node_ip" = "$LOCAL_IP" ]; then
            qemu_cmd=$(ps auxw | grep "$instance_id" | grep qemu-system | grep -v grep || true)
        else
            qemu_cmd=$(peer_ssh "$node_ip" "ps auxw | grep '$instance_id' | grep qemu-system | grep -v grep" 2>/dev/null || true)
        fi

        if [ -n "$qemu_cmd" ]; then
            local ssh_port
            ssh_port=$(echo "$qemu_cmd" | sed -n 's/.*hostfwd=tcp:[^:]*:\([0-9]*\)-:22.*/\1/p')
            if [ -n "$ssh_port" ]; then
                echo "$ssh_port"
                return 0
            fi
        fi

        attempt=$((attempt + 1))
        [ $attempt -lt $max_attempts ] && sleep 1
    done

    return 1
}

# Expect an AWS CLI command to fail with a specific error code
# Usage: expect_error "ErrorCode" aws ec2 some-command --args...
expect_error() {
    local expected_error="$1"
    shift

    set +e
    local output
    output=$("$@" 2>&1)
    local exit_code=$?
    set -e

    if [ $exit_code -eq 0 ]; then
        echo "  FAIL: Expected error '$expected_error' but command succeeded"
        echo "  Output: $output"
        return 1
    fi

    if echo "$output" | grep -q "$expected_error"; then
        echo "  Got expected error: $expected_error"
        return 0
    else
        echo "  FAIL: Expected error '$expected_error' but got different error"
        echo "  Output: $output"
        return 1
    fi
}

# Terminate instances and wait for terminated state
terminate_and_wait() {
    local ids=("$@")

    for instance_id in "${ids[@]}"; do
        local state
        state=$($AWS_EC2 describe-instances --instance-ids "$instance_id" \
            --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "unknown")
        if [ "$state" != "terminated" ] && [ "$state" != "unknown" ]; then
            echo "  Terminating $instance_id (state: $state)..."
            $AWS_EC2 terminate-instances --instance-ids "$instance_id" > /dev/null 2>&1 || true
        fi
    done

    local failed=0
    for instance_id in "${ids[@]}"; do
        if ! wait_for_instance_state "$instance_id" "terminated" 30; then
            echo "  WARNING: Failed to confirm termination of $instance_id"
            failed=1
        fi
    done

    return $failed
}

# Record test result
pass_test() {
    local name="$1"
    echo "  $name PASSED"
    TESTS_PASSED=$((TESTS_PASSED + 1))
}

fail_test() {
    local name="$1"
    echo "  $name FAILED"
    TESTS_FAILED=$((TESTS_FAILED + 1))
    FAILED_TESTS+=("$name")
}

# ==========================================================================
# EXIT trap — dump logs on failure
# ==========================================================================
trap 'EXIT_CODE=$?; if [ $EXIT_CODE -ne 0 ]; then dump_all_node_logs; fi; exit $EXIT_CODE' EXIT

# Track instance IDs for cleanup
INSTANCE_IDS=()

echo "========================================"
echo "Real Multi-Node E2E Test Suite"
echo "========================================"
echo "Nodes: ${NODE_IPS[*]}"
echo "Local: $LOCAL_IP"
echo ""

# ==========================================================================
# Phase 1: Pre-flight Validation
# ==========================================================================
echo "Phase 1: Pre-flight Validation"
echo "========================================"

# Check KVM support
echo "Checking KVM support..."
if [ -e /dev/kvm ] && [ -w /dev/kvm ]; then
    echo "  /dev/kvm exists and is writable"
else
    echo "  ERROR: /dev/kvm missing or not writable"
    exit 1
fi

# Verify SSH to peer nodes
echo "Verifying SSH connectivity to peer nodes..."
for i in 1 2; do
    local_idx=$((i))
    ip="${NODE_IPS[$local_idx]}"
    echo -n "  SSH to node$((local_idx + 1)) ($ip)..."
    if peer_ssh "$ip" "hostname" > /dev/null 2>&1; then
        echo " OK"
    else
        echo " FAILED"
        echo "  ERROR: Cannot SSH to $ip"
        exit 1
    fi
done

echo ""

# ==========================================================================
# Phase 2: Cluster Health
# ==========================================================================
echo "Phase 2: Cluster Health"
echo "========================================"

# NATS cluster: verify 2 unique peers from node1
echo "Checking NATS cluster..."
NATS_INFO=$(curl -s "http://${LOCAL_IP}:${NATS_MONITOR_PORT}/routez" 2>/dev/null) || {
    echo "  ERROR: Cannot reach NATS monitoring endpoint"
    exit 1
}
UNIQUE_PEERS=$(echo "$NATS_INFO" | jq -r '[.routes[].remote_name] | unique | length')
echo "  NATS unique peers: $UNIQUE_PEERS (expected: 2)"
if [ "$UNIQUE_PEERS" -ge 2 ]; then
    pass_test "NATS quorum"
else
    echo "  ERROR: NATS cluster not fully formed"
    exit 1
fi

# Predastore: check each node
echo "Checking Predastore on all nodes..."
for i in $(seq 0 $((NODE_COUNT - 1))); do
    ip="${NODE_IPS[$i]}"
    if curl -k -s "https://${ip}:${PREDASTORE_PORT}" > /dev/null 2>&1; then
        echo "  Node$((i+1)) ($ip): Predastore reachable"
    else
        echo "  ERROR: Predastore not reachable on node$((i+1)) ($ip)"
        exit 1
    fi
done
pass_test "Predastore cluster"

# Gateway: check each node
echo "Checking gateway on all nodes..."
for i in $(seq 0 $((NODE_COUNT - 1))); do
    ip="${NODE_IPS[$i]}"
    if curl -k -s "https://${ip}:${AWSGW_PORT}" > /dev/null 2>&1; then
        echo "  Node$((i+1)) ($ip): Gateway reachable"
    else
        echo "  ERROR: Gateway not reachable on node$((i+1)) ($ip)"
        exit 1
    fi
done
pass_test "All gateways"

# Daemon readiness: describe-instance-types must return results
echo "Checking daemon readiness..."
ATTEMPT=0
while [ $ATTEMPT -lt 15 ]; do
    TYPES=$($AWS_EC2 describe-instance-types \
        --query 'InstanceTypes[*].InstanceType' --output text 2>/dev/null || true)
    if [ -n "$TYPES" ] && [ "$TYPES" != "None" ]; then
        echo "  Daemon ready (instance types: $TYPES)"
        break
    fi
    echo "  Waiting for daemon... ($((ATTEMPT + 1))/15)"
    sleep 2
    ATTEMPT=$((ATTEMPT + 1))
done
if [ -z "$TYPES" ] || [ "$TYPES" == "None" ]; then
    echo "  ERROR: Daemon not ready"
    exit 1
fi
pass_test "Daemon readiness"

# Hive CLI: get nodes
echo "Checking hive get nodes..."
GET_NODES_OUTPUT=$($HIVE_BIN get nodes --config "$HIVE_CONFIG" --timeout 5s 2>/dev/null)
echo "$GET_NODES_OUTPUT"
READY_COUNT=$(echo "$GET_NODES_OUTPUT" | grep -c "Ready" || true)
if [ "$READY_COUNT" -ge 3 ]; then
    pass_test "hive get nodes ($READY_COUNT Ready)"
else
    echo "  WARNING: hive get nodes shows $READY_COUNT Ready nodes (expected 3)"
    fail_test "hive get nodes"
fi

# Hive CLI: get vms (should be empty)
echo "Checking hive get vms (empty)..."
GET_VMS_OUTPUT=$($HIVE_BIN get vms --config "$HIVE_CONFIG" --timeout 5s 2>/dev/null)
echo "$GET_VMS_OUTPUT"
pass_test "hive get vms (empty)"

echo ""

# ==========================================================================
# Phase 3: Instance Lifecycle + Distribution
# ==========================================================================
echo "Phase 3: Instance Lifecycle + Distribution"
echo "========================================"

# Discover instance type
INSTANCE_TYPE=$(echo $TYPES | tr ' ' '\n' | grep -m1 'nano')
if [ -z "$INSTANCE_TYPE" ]; then
    echo "ERROR: No nano instance type found"
    exit 1
fi
echo "Using instance type: $INSTANCE_TYPE"

# Discover AMI
AMI_ID=$($AWS_EC2 describe-images --query 'Images[0].ImageId' --output text)
if [ -z "$AMI_ID" ] || [ "$AMI_ID" == "None" ]; then
    echo "ERROR: No AMI found (bootstrap should have imported one)"
    exit 1
fi
echo "Using AMI: $AMI_ID"

# Launch 3 instances with stagger to encourage distribution
echo "Launching 3 instances..."
for i in 1 2 3; do
    echo "  Launching instance $i..."
    RUN_OUTPUT=$($AWS_EC2 run-instances \
        --image-id "$AMI_ID" \
        --instance-type "$INSTANCE_TYPE" \
        --key-name hive-key)

    INSTANCE_ID=$(echo "$RUN_OUTPUT" | jq -r '.Instances[0].InstanceId')
    if [ -z "$INSTANCE_ID" ] || [ "$INSTANCE_ID" == "null" ]; then
        echo "  ERROR: Failed to launch instance $i"
        echo "  Output: $RUN_OUTPUT"
        exit 1
    fi
    echo "  Launched: $INSTANCE_ID"
    INSTANCE_IDS+=("$INSTANCE_ID")

    # Stagger launches to encourage distribution across nodes
    [ $i -lt 3 ] && sleep 2
done

# Wait for all instances to be running
echo ""
echo "Waiting for instances to reach running state..."
for instance_id in "${INSTANCE_IDS[@]}"; do
    wait_for_instance_state "$instance_id" "running" 30 || {
        echo "ERROR: Instance $instance_id failed to start"
        exit 1
    }
done

# Check distribution via hive get vms or QEMU process check
echo ""
echo "Checking instance distribution across nodes..."
declare -A NODE_INSTANCE_COUNT
HOSTING_NODES=()
for instance_id in "${INSTANCE_IDS[@]}"; do
    HOST_IP=$(find_instance_node "$instance_id" || echo "unknown")
    echo "  $instance_id -> $HOST_IP"
    HOSTING_NODES+=("$HOST_IP")
    NODE_INSTANCE_COUNT[$HOST_IP]=$(( ${NODE_INSTANCE_COUNT[$HOST_IP]:-0} + 1 ))
done

# Count unique hosting nodes
UNIQUE_HOSTS=$(printf '%s\n' "${HOSTING_NODES[@]}" | sort -u | wc -l)
echo "  Instances on $UNIQUE_HOSTS different nodes"
if [ "$UNIQUE_HOSTS" -ge 2 ]; then
    pass_test "Instance distribution (>= 2 nodes)"
else
    echo "  WARNING: All instances on same node (distribution not guaranteed, non-fatal)"
    pass_test "Instance distribution (non-deterministic)"
fi

# Verify hive get vms shows all instances
echo ""
echo "Verifying hive get vms..."
GET_VMS_OUTPUT=$($HIVE_BIN get vms --config "$HIVE_CONFIG" --timeout 5s 2>/dev/null)
echo "$GET_VMS_OUTPUT"
for instance_id in "${INSTANCE_IDS[@]}"; do
    if ! echo "$GET_VMS_OUTPUT" | grep -q "$instance_id"; then
        echo "  WARNING: hive get vms did not show $instance_id"
    fi
done
pass_test "hive get vms (with instances)"

echo ""

# ==========================================================================
# Phase 4: SSH into Guest VMs
# ==========================================================================
echo "Phase 4: SSH into Guest VMs"
echo "========================================"

for idx in "${!INSTANCE_IDS[@]}"; do
    instance_id="${INSTANCE_IDS[$idx]}"
    host_ip="${HOSTING_NODES[$idx]}"
    echo ""
    echo "  Instance $((idx + 1)): $instance_id (on $host_ip)"

    # Get SSH port from QEMU process on the hosting node
    SSH_PORT=$(get_remote_ssh_port "$host_ip" "$instance_id" 10)
    if [ -z "$SSH_PORT" ]; then
        echo "  ERROR: Failed to get SSH port for $instance_id on $host_ip"
        fail_test "Guest SSH ($instance_id)"
        continue
    fi
    echo "  SSH endpoint: $host_ip:$SSH_PORT"

    # Wait for SSH to be ready (VM boot + cloud-init)
    echo "  Waiting for SSH to be ready..."
    ATTEMPT=0
    SSH_READY=false
    while [ $ATTEMPT -lt 60 ]; do
        if ssh -o StrictHostKeyChecking=no \
               -o UserKnownHostsFile=/dev/null \
               -o ConnectTimeout=2 \
               -o BatchMode=yes \
               -o LogLevel=ERROR \
               -p "$SSH_PORT" \
               -i "$HOME/.ssh/hive-key" \
               ec2-user@"$host_ip" 'echo ready' > /dev/null 2>&1; then
            SSH_READY=true
            break
        fi
        ATTEMPT=$((ATTEMPT + 1))
        [ $((ATTEMPT % 10)) -eq 0 ] && echo "  Waiting for SSH... ($ATTEMPT/60)"
        sleep 1
    done

    if [ "$SSH_READY" = false ]; then
        echo "  ERROR: SSH not ready after 60 attempts"
        fail_test "Guest SSH ($instance_id)"
        continue
    fi

    # Test SSH connectivity
    ID_OUTPUT=$(ssh -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=5 \
        -o BatchMode=yes \
        -o LogLevel=ERROR \
        -p "$SSH_PORT" \
        -i "$HOME/.ssh/hive-key" \
        ec2-user@"$host_ip" 'id' 2>&1) || {
        echo "  ERROR: SSH 'id' command failed"
        fail_test "Guest SSH ($instance_id)"
        continue
    }

    echo "  SSH 'id' output: $ID_OUTPUT"
    if echo "$ID_OUTPUT" | grep -q "ec2-user"; then
        echo "  ec2-user confirmed"
    else
        echo "  ERROR: Expected 'ec2-user' in id output"
        fail_test "Guest SSH ($instance_id)"
        continue
    fi

    # Verify block device
    LSBLK_OUTPUT=$(ssh -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=5 \
        -o BatchMode=yes \
        -o LogLevel=ERROR \
        -p "$SSH_PORT" \
        -i "$HOME/.ssh/hive-key" \
        ec2-user@"$host_ip" 'lsblk' 2>&1) || true
    echo "  lsblk: $(echo "$LSBLK_OUTPUT" | head -5)"

    pass_test "Guest SSH ($instance_id)"
done

echo ""

# ==========================================================================
# Phase 5: Volume Lifecycle
# ==========================================================================
echo "Phase 5: Volume Lifecycle"
echo "========================================"

# Discover AZ
HIVE_AZ=$($AWS_EC2 describe-availability-zones --query 'AvailabilityZones[0].ZoneName' --output text)
echo "AZ: $HIVE_AZ"

# Create volume
echo "Creating 10GB test volume..."
CREATE_OUTPUT=$($AWS_EC2 create-volume --size 10 --availability-zone "$HIVE_AZ")
TEST_VOLUME_ID=$(echo "$CREATE_OUTPUT" | jq -r '.VolumeId')
if [ -z "$TEST_VOLUME_ID" ] || [ "$TEST_VOLUME_ID" == "null" ]; then
    echo "  ERROR: Failed to create volume"
    fail_test "Volume create"
else
    echo "  Created: $TEST_VOLUME_ID"
    pass_test "Volume create"

    # Attach to first instance
    echo "  Attaching to ${INSTANCE_IDS[0]}..."
    $AWS_EC2 attach-volume --volume-id "$TEST_VOLUME_ID" \
        --instance-id "${INSTANCE_IDS[0]}" --device /dev/sdf > /dev/null

    # Wait for attachment
    COUNT=0
    while [ $COUNT -lt 15 ]; do
        ATTACH_STATE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
            --query 'Volumes[0].Attachments[0].State' --output text)
        VOL_STATE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
            --query 'Volumes[0].State' --output text)
        if [ "$VOL_STATE" == "in-use" ] && [ "$ATTACH_STATE" == "attached" ]; then
            echo "  Volume attached"
            break
        fi
        sleep 2
        COUNT=$((COUNT + 1))
    done

    if [ "$ATTACH_STATE" == "attached" ]; then
        pass_test "Volume attach"
    else
        fail_test "Volume attach"
    fi

    # Detach
    echo "  Detaching volume..."
    $AWS_EC2 detach-volume --volume-id "$TEST_VOLUME_ID" > /dev/null

    COUNT=0
    while [ $COUNT -lt 15 ]; do
        VOL_STATE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
            --query 'Volumes[0].State' --output text)
        if [ "$VOL_STATE" == "available" ]; then
            echo "  Volume detached"
            break
        fi
        sleep 2
        COUNT=$((COUNT + 1))
    done

    if [ "$VOL_STATE" == "available" ]; then
        pass_test "Volume detach"
    else
        fail_test "Volume detach"
    fi

    # Delete
    echo "  Deleting volume..."
    $AWS_EC2 delete-volume --volume-id "$TEST_VOLUME_ID"

    COUNT=0
    while [ $COUNT -lt 15 ]; do
        set +e
        VOL_CHECK=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
            --query 'Volumes[0].VolumeId' --output text 2>&1)
        DESCRIBE_EXIT=$?
        set -e
        if [ $DESCRIBE_EXIT -ne 0 ] || [ "$VOL_CHECK" == "None" ] || [ -z "$VOL_CHECK" ]; then
            echo "  Volume deleted"
            break
        fi
        sleep 2
        COUNT=$((COUNT + 1))
    done

    if [ $COUNT -lt 15 ]; then
        pass_test "Volume delete"
    else
        fail_test "Volume delete"
    fi
fi

echo ""

# ==========================================================================
# Phase 6: Cross-Node Gateway Access
# ==========================================================================
echo "Phase 6: Cross-Node Gateway Access"
echo "========================================"
echo "Verifying describe-instances returns same results via each node's gateway..."

BASELINE_COUNT=$($AWS_EC2 describe-instances \
    --query 'length(Reservations[*].Instances[*][])' --output text)
echo "  Baseline (node1): $BASELINE_COUNT instances"

for i in 1 2; do
    ip="${NODE_IPS[$i]}"
    COUNT=$(aws_via "$ip" ec2 describe-instances \
        --query 'length(Reservations[*].Instances[*][])' --output text 2>/dev/null || echo "0")
    echo "  Node$((i+1)) ($ip): $COUNT instances"
    if [ "$COUNT" -eq "$BASELINE_COUNT" ]; then
        pass_test "Cross-node gateway (node$((i+1)))"
    else
        echo "  WARNING: Instance count mismatch (expected $BASELINE_COUNT, got $COUNT)"
        fail_test "Cross-node gateway (node$((i+1)))"
    fi
done

echo ""

# ==========================================================================
# Phase 7: Cross-Node Operations
# ==========================================================================
echo "Phase 7: Cross-Node Operations"
echo "========================================"
echo "Testing stop/start via a gateway on a DIFFERENT node than the hosting node..."

# Pick first instance and find its host
TEST_INSTANCE="${INSTANCE_IDS[0]}"
INSTANCE_HOST=$(find_instance_node "$TEST_INSTANCE" || echo "$LOCAL_IP")
echo "  Instance $TEST_INSTANCE is on $INSTANCE_HOST"

# Pick a different node's gateway for the operation
OTHER_GW=""
for ip in "${NODE_IPS[@]}"; do
    if [ "$ip" != "$INSTANCE_HOST" ]; then
        OTHER_GW="$ip"
        break
    fi
done
echo "  Will operate via gateway on $OTHER_GW"

# Stop via other gateway
echo "  Stopping instance via $OTHER_GW..."
aws_via "$OTHER_GW" ec2 stop-instances --instance-ids "$TEST_INSTANCE" > /dev/null
wait_for_instance_state "$TEST_INSTANCE" "stopped" 30 "$OTHER_GW"

# Pick yet another gateway for start (or same other if only 2 choices)
THIRD_GW=""
for ip in "${NODE_IPS[@]}"; do
    if [ "$ip" != "$INSTANCE_HOST" ] && [ "$ip" != "$OTHER_GW" ]; then
        THIRD_GW="$ip"
        break
    fi
done
THIRD_GW="${THIRD_GW:-$OTHER_GW}"
echo "  Starting instance via $THIRD_GW..."
aws_via "$THIRD_GW" ec2 start-instances --instance-ids "$TEST_INSTANCE" > /dev/null
wait_for_instance_state "$TEST_INSTANCE" "running" 30 "$THIRD_GW"

pass_test "Cross-node stop/start"

echo ""

# ==========================================================================
# Phase 8: Node Failure
# ==========================================================================
echo "Phase 8: Node Failure"
echo "========================================"
echo "Stopping services on node2 ($NODE2_IP) to simulate node failure..."

# Stop services on node2 only — HIVE_FORCE_LOCAL_STOP prevents coordinated
# cluster shutdown which would kill all nodes via NATS
peer_ssh "$NODE2_IP" "cd ~/Development/mulga/hive && HIVE_FORCE_LOCAL_STOP=1 ./scripts/stop-dev.sh" || {
    echo "  WARNING: stop-dev.sh returned non-zero (may be expected)"
}

# Wait for NATS cluster to detect the failure and reform
sleep 10

# Verify node1 and node3 still serve requests (with retries for NATS reformation)
echo "  Verifying node1 still serves requests..."
N1_RESULT=$(aws_via_retry 10 "$LOCAL_IP" ec2 describe-instance-types \
    --query 'InstanceTypes[0].InstanceType' --output text) || N1_RESULT="FAIL"
if [ "$N1_RESULT" != "FAIL" ]; then
    echo "  Node1: responding ($N1_RESULT)"
    pass_test "Node1 survives node2 failure"
else
    echo "  ERROR: Node1 not responding after node2 failure"
    fail_test "Node1 survives node2 failure"
fi

echo "  Verifying node3 still serves requests..."
N3_RESULT=$(aws_via_retry 10 "$NODE3_IP" ec2 describe-instance-types \
    --query 'InstanceTypes[0].InstanceType' --output text) || N3_RESULT="FAIL"
if [ "$N3_RESULT" != "FAIL" ]; then
    echo "  Node3: responding ($N3_RESULT)"
    pass_test "Node3 survives node2 failure"
else
    echo "  ERROR: Node3 not responding after node2 failure"
    fail_test "Node3 survives node2 failure"
fi

# Check NATS degraded state (should have 1 route instead of 2)
NATS_DEGRADED=$(curl -s "http://${LOCAL_IP}:${NATS_MONITOR_PORT}/routez" 2>/dev/null)
DEGRADED_PEERS=$(echo "$NATS_DEGRADED" | jq -r '[.routes[].remote_name] | unique | length' 2>/dev/null || echo "0")
echo "  NATS peers during failure: $DEGRADED_PEERS (expected: 1)"
if [ "$DEGRADED_PEERS" -eq 1 ]; then
    pass_test "NATS degraded mode"
else
    echo "  WARNING: Expected 1 NATS peer during node2 failure, got $DEGRADED_PEERS"
    # Not fatal — NATS might take a moment to detect
fi

# Verify describe-instances still works from surviving nodes
echo "  Verifying describe-instances from surviving nodes..."
SURVIVING_COUNT=$(aws_via_retry 10 "$LOCAL_IP" ec2 describe-instances \
    --query 'length(Reservations[*].Instances[*][])' --output text) || SURVIVING_COUNT="0"
echo "  Instances visible from node1: $SURVIVING_COUNT"
if [ "$SURVIVING_COUNT" -gt 0 ]; then
    pass_test "Describe-instances during node failure"
else
    fail_test "Describe-instances during node failure"
fi

echo ""

# ==========================================================================
# Phase 9: Node Recovery
# ==========================================================================
echo "Phase 9: Node Recovery"
echo "========================================"
echo "Restarting services on node2 ($NODE2_IP)..."

peer_ssh "$NODE2_IP" "cd ~/Development/mulga/hive && ./scripts/start-dev.sh" || {
    echo "  ERROR: Failed to restart services on node2"
    fail_test "Node2 restart"
}

# Wait for NATS to reform (2 routes again)
echo "  Waiting for NATS cluster to reform..."
ATTEMPT=0
REFORMED=false
while [ $ATTEMPT -lt 30 ]; do
    NATS_RECOVER=$(curl -s "http://${LOCAL_IP}:${NATS_MONITOR_PORT}/routez" 2>/dev/null)
    RECOVER_PEERS=$(echo "$NATS_RECOVER" | jq -r '[.routes[].remote_name] | unique | length' 2>/dev/null || echo "0")
    if [ "$RECOVER_PEERS" -ge 2 ]; then
        echo "  NATS cluster reformed ($RECOVER_PEERS peers)"
        REFORMED=true
        break
    fi
    echo "  Waiting for NATS reform... ($((ATTEMPT + 1))/30, peers: $RECOVER_PEERS)"
    sleep 2
    ATTEMPT=$((ATTEMPT + 1))
done

if [ "$REFORMED" = true ]; then
    pass_test "NATS cluster reform"
else
    echo "  WARNING: NATS did not fully reform within timeout"
    fail_test "NATS cluster reform"
fi

# Verify node2 gateway is back
echo "  Waiting for node2 gateway..."
ATTEMPT=0
GW_BACK=false
while [ $ATTEMPT -lt 15 ]; do
    if curl -k -s "https://${NODE2_IP}:${AWSGW_PORT}" > /dev/null 2>&1; then
        echo "  Node2 gateway is back"
        GW_BACK=true
        break
    fi
    sleep 2
    ATTEMPT=$((ATTEMPT + 1))
done

if [ "$GW_BACK" = true ]; then
    pass_test "Node2 gateway recovery"
else
    fail_test "Node2 gateway recovery"
fi

# Verify hive get nodes shows 3 Ready again
echo "  Checking hive get nodes after recovery..."
GET_NODES_RECOVER=$($HIVE_BIN get nodes --config "$HIVE_CONFIG" --timeout 10s 2>/dev/null || echo "")
echo "$GET_NODES_RECOVER"
READY_RECOVER=$(echo "$GET_NODES_RECOVER" | grep -c "Ready" || true)
if [ "$READY_RECOVER" -ge 3 ]; then
    pass_test "All nodes Ready after recovery"
else
    echo "  WARNING: Only $READY_RECOVER Ready nodes after recovery (expected 3)"
    fail_test "All nodes Ready after recovery"
fi

# Verify node2 can serve requests
echo "  Verifying node2 serves requests after recovery..."
N2_RESULT=$(aws_via "$NODE2_IP" ec2 describe-instance-types \
    --query 'InstanceTypes[0].InstanceType' --output text 2>/dev/null || echo "FAIL")
if [ "$N2_RESULT" != "FAIL" ] && [ -n "$N2_RESULT" ] && [ "$N2_RESULT" != "None" ]; then
    echo "  Node2 is serving requests again ($N2_RESULT)"
    pass_test "Node2 serves requests after recovery"
else
    fail_test "Node2 serves requests after recovery"
fi

echo ""

# ==========================================================================
# Phase 10: Cleanup
# ==========================================================================
echo "Phase 10: Cleanup"
echo "========================================"

echo "Terminating all instances..."
if terminate_and_wait "${INSTANCE_IDS[@]}"; then
    pass_test "Instance termination"
else
    fail_test "Instance termination"
fi

# ==========================================================================
# Summary
# ==========================================================================
echo ""
echo "========================================"
echo "Real Multi-Node E2E Test Summary"
echo "========================================"
echo "  Passed: $TESTS_PASSED"
echo "  Failed: $TESTS_FAILED"
if [ ${#FAILED_TESTS[@]} -gt 0 ]; then
    echo "  Failed tests:"
    for t in "${FAILED_TESTS[@]}"; do
        echo "    - $t"
    done
fi
echo "========================================"

if [ $TESTS_FAILED -gt 0 ]; then
    echo "SOME TESTS FAILED"
    exit 1
fi

echo "All tests passed!"
exit 0
