#!/bin/bash
set -e

# Ensure Go is on PATH (SSH non-interactive shells don't source .bashrc)
export PATH="/usr/local/go/bin:$HOME/go/bin:$PATH"

# Pseudo Multi-Node E2E test runner
# This script sets up a 3-node Spinifex cluster using simulated IPs on the loopback interface
# and runs distributed instance tests on a single VM.

# Ensure we are in the project root
cd "$(dirname "$0")/../.."

# Source helper functions
source ./tests/e2e/lib/multinode-helpers.sh

# Cleanup function - ensure resources are cleaned up on exit
cleanup() {
    local exit_code=$?

    echo ""
    echo "Cleanup triggered (exit code: $exit_code)..."

    if [ $exit_code -ne 0 ]; then
        dump_all_node_logs
    fi

    # Kill any lingering formation background processes
    [ -n "$LEADER_INIT_PID" ] && kill "$LEADER_INIT_PID" 2>/dev/null || true
    [ -n "$JOIN2_PID" ] && kill "$JOIN2_PID" 2>/dev/null || true
    [ -n "$JOIN3_PID" ] && kill "$JOIN3_PID" 2>/dev/null || true

    # Try coordinated shutdown first (only if NATS is likely still up)
    if [ "$CLUSTER_SERVICES_STARTED" = "true" ]; then
        echo "Attempting coordinated cluster shutdown..."
        if timeout 60 ./bin/spx admin cluster shutdown --force --timeout 30s --config "$HOME/node1/config/spinifex.toml" 2>/dev/null; then
            echo "Coordinated shutdown succeeded"
        else
            echo "Coordinated shutdown failed, falling back to per-node stop..."
            stop_all_nodes || true
        fi
    else
        stop_all_nodes || true
    fi

    # Force-kill anything that survived and clean up stale locks
    force_cleanup_all_nodes || true

    # Remove simulated IPs
    remove_simulated_ips || true

    echo "Cleanup complete"
}
trap cleanup EXIT

# PIDs for background formation processes (used in cleanup)
JOIN2_PID=""
JOIN3_PID=""

# Track whether cluster services have been started (for cleanup trap)
CLUSTER_SERVICES_STARTED="false"

# Use Spinifex profile for AWS CLI
export AWS_PROFILE=spinifex
# Trust Spinifex CA for all profiles (AWS CLI v2 bundles its own Python/certifi, ignores system CA store)
export AWS_CA_BUNDLE="$HOME/node1/config/ca.pem"


echo "========================================"
echo "Multi-Node E2E Test Suite"
echo "========================================"
echo ""

# Phase 1: Environment Setup
echo "Phase 1: Environment Setup"
echo "========================================"

# Check for KVM support
echo "Checking for KVM support..."
if [ -e /dev/kvm ]; then
    echo "  /dev/kvm exists"
    if [ -w /dev/kvm ]; then
        echo "  /dev/kvm is writable"
    else
        echo "  ERROR: /dev/kvm is NOT writable"
        exit 1
    fi
else
    echo "  ERROR: /dev/kvm does NOT exist"
    exit 1
fi

# Check for ip command (iproute2)
if ! command -v ip &> /dev/null; then
    echo "  ERROR: 'ip' command not found. Install iproute2."
    exit 1
fi


# Setup simulated network
echo ""
echo "Setting up simulated network..."
add_simulated_ips

echo ""

# Phase 2: Cluster Initialization
echo "Phase 2: Cluster Initialization"
echo "========================================"

# Background init — starts formation server, generates certs first
echo ""
init_leader_node

# Trust CA cert (exists before formation completes — cert generation is the first step)
echo ""
echo "Adding Spinifex CA certificate to system trust store..."
sudo cp ~/node1/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates

# Background BOTH joins — they poll /formation/status until all 3 nodes have joined.
# Must be concurrent: each join blocks until formation is complete.
echo ""
echo "Joining follower nodes concurrently..."
join_follower_node 2 &
JOIN2_PID=$!
join_follower_node 3 &
JOIN3_PID=$!

# Wait for formation to complete (all processes generate their configs)
echo "Waiting for cluster formation to complete..."
wait $JOIN2_PID || { echo "ERROR: Node 2 join failed"; exit 1; }
wait $JOIN3_PID || { echo "ERROR: Node 3 join failed"; exit 1; }
wait $LEADER_INIT_PID || { echo "ERROR: Leader init failed"; exit 1; }
echo "Cluster formation complete — all configs generated"

# Now start services (configs exist for all nodes)
echo ""
echo "Starting node services..."
start_node_services 1 "$HOME/node1"
start_node_services 2 "$HOME/node2"
start_node_services 3 "$HOME/node3"
CLUSTER_SERVICES_STARTED="true"

# Wait for all services to stabilize
echo ""
echo "Waiting for cluster to stabilize..."
sleep 5

# Phase 3: Cluster Health Verification
echo ""
echo "Phase 3: Cluster Health Verification"
echo "========================================"

# Verify NATS cluster
echo ""
verify_nats_cluster 3 || {
    echo "WARNING: NATS cluster verification failed, continuing anyway..."
}

# Verify Predastore cluster
echo ""
verify_predastore_cluster 3 || {
    echo "ERROR: Predastore cluster verification failed"
    dump_all_node_logs
    exit 1
}

# Wait for gateway on node1 (primary gateway)
echo ""
wait_for_gateway "${NODE1_IP}" 15

# Wait for daemon NATS subscriptions to be active
wait_for_daemon_ready "https://${NODE1_IP}:${AWSGW_PORT}"

# Define AWS CLI args pointing to node1's gateway
AWS_EC2="aws --endpoint-url https://${NODE1_IP}:${AWSGW_PORT} ec2"
AWS_IAM="aws --endpoint-url https://${NODE1_IP}:${AWSGW_PORT} iam"

# Discover the cluster's availability zone and region dynamically
SPINIFEX_AZ=$($AWS_EC2 describe-availability-zones --query 'AvailabilityZones[0].ZoneName' --output text)
SPINIFEX_REGION=$($AWS_EC2 describe-availability-zones --query 'AvailabilityZones[0].RegionName' --output text)
echo "Discovered AZ: $SPINIFEX_AZ, Region: $SPINIFEX_REGION"

# Phase 3b: Cluster Stats CLI (Multi-Node)
echo ""
echo "Phase 3b: Cluster Stats CLI (Multi-Node)"
echo "========================================"

# Test spx get nodes — should show all 3 nodes as Ready
echo "Testing spx get nodes..."
GET_NODES_OUTPUT=$(./bin/spx get nodes --config "$HOME/node1/config/spinifex.toml" --timeout 5s 2>/dev/null)
echo "$GET_NODES_OUTPUT"
READY_COUNT=$(echo "$GET_NODES_OUTPUT" | grep -c "Ready" || true)
if [ "$READY_COUNT" -lt 3 ]; then
    echo "WARNING: spx get nodes shows $READY_COUNT Ready nodes (expected 3)"
fi
echo "spx get nodes passed ($READY_COUNT Ready nodes)"

# Test spx top nodes — should show resource stats for all nodes
echo "Testing spx top nodes..."
TOP_NODES_OUTPUT=$(./bin/spx top nodes --config "$HOME/node1/config/spinifex.toml" --timeout 5s 2>/dev/null)
echo "$TOP_NODES_OUTPUT"
if ! echo "$TOP_NODES_OUTPUT" | grep -q "INSTANCE TYPE"; then
    echo "WARNING: spx top nodes did not show instance type capacity table"
fi
echo "spx top nodes passed"

# Test spx get vms — should show no VMs yet
echo "Testing spx get vms (empty)..."
GET_VMS_OUTPUT=$(./bin/spx get vms --config "$HOME/node1/config/spinifex.toml" --timeout 5s 2>/dev/null)
echo "$GET_VMS_OUTPUT"
echo "spx get vms (empty) passed"

# Verify gateway responds
echo ""
echo "Testing gateway connectivity..."
$AWS_EC2 describe-regions | jq -e '.Regions | length > 0' || {
    echo "ERROR: Gateway not responding correctly"
    exit 1
}
echo "  Gateway is responding"

# Phase 4: Image and Key Setup
echo ""
echo "Phase 4: Image and Key Setup"
echo "========================================"

# Discover instance types
echo "Discovering available instance types..."
AVAILABLE_TYPES=$($AWS_EC2 describe-instance-types --query 'InstanceTypes[*].InstanceType' --output text)
echo "  Available: $AVAILABLE_TYPES"

# Pick nano instance type
INSTANCE_TYPE=$(echo $AVAILABLE_TYPES | tr ' ' '\n' | grep -m1 'nano')
if [ -z "$INSTANCE_TYPE" ] || [ "$INSTANCE_TYPE" == "None" ]; then
    echo "ERROR: No instance types found"
    exit 1
fi
echo "  Selected: $INSTANCE_TYPE"

# Get architecture
ARCH=$($AWS_EC2 describe-instance-types --instance-types "$INSTANCE_TYPE" \
    --query 'InstanceTypes[0].ProcessorInfo.SupportedArchitectures[0]' --output text)
echo "  Architecture: $ARCH"

# Create test key
echo ""
echo "Creating test key pair..."
KEY_MATERIAL=$($AWS_EC2 create-key-pair --key-name multinode-test-key --query 'KeyMaterial' --output text)
echo "$KEY_MATERIAL" > multinode-test-key.pem
chmod 600 multinode-test-key.pem
echo "  Key created: multinode-test-key"

# Import Ubuntu image (use node1's config and spinifex-dir)
echo ""
echo "Importing Ubuntu image..."
IMPORT_LOG=$(./bin/spx admin images import \
    --file ~/images/ubuntu-24.04.img \
    --arch "$ARCH" \
    --distro ubuntu \
    --version 24.04 \
    --config "$HOME/node1/config/spinifex.toml" \
    --spinifex-dir "$HOME/node1/" \
    --force)
echo "Import output: $IMPORT_LOG"
AMI_ID=$(echo "$IMPORT_LOG" | grep -o 'ami-[a-z0-9]\+')

if [ -z "$AMI_ID" ]; then
    echo "ERROR: Failed to capture AMI ID"
    exit 1
fi
echo "  AMI ID: $AMI_ID"

# Verify AMI
$AWS_EC2 describe-images --image-ids "$AMI_ID" | jq -e ".Images[0] | select(.ImageId==\"$AMI_ID\")" > /dev/null
echo "  AMI verified"


# Phase 5: Multi-Node Instance Tests
echo ""
echo "Phase 5: Multi-Node Instance Tests"
echo "========================================"

# Test 1: Instance Distribution
echo ""
echo "Test 1: Instance Distribution"
echo "----------------------------------------"
echo "Launching 3 instances to test distribution across nodes..."

INSTANCE_IDS=()
for i in 1 2 3; do
    echo "  Launching instance $i..."
    RUN_OUTPUT=$($AWS_EC2 run-instances \
        --image-id "$AMI_ID" \
        --instance-type "$INSTANCE_TYPE" \
        --key-name multinode-test-key)

    INSTANCE_ID=$(echo "$RUN_OUTPUT" | jq -r '.Instances[0].InstanceId')
    if [ -z "$INSTANCE_ID" ] || [ "$INSTANCE_ID" == "null" ]; then
        echo "  ERROR: Failed to launch instance $i"
        echo "  Output: $RUN_OUTPUT"
        exit 1
    fi
    echo "  Launched: $INSTANCE_ID"
    INSTANCE_IDS+=("$INSTANCE_ID")

    # Small delay between launches to encourage distribution
    sleep 1
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

# Check distribution
echo ""
check_instance_distribution

# Verify spx get vms shows all running instances
echo ""
echo "Verifying spx get vms (with running VMs)..."
GET_VMS_OUTPUT=$(./bin/spx get vms --config "$HOME/node1/config/spinifex.toml" --timeout 5s 2>/dev/null)
echo "$GET_VMS_OUTPUT"
for instance_id in "${INSTANCE_IDS[@]}"; do
    if ! echo "$GET_VMS_OUTPUT" | grep -q "$instance_id"; then
        echo "WARNING: spx get vms did not show instance $instance_id"
    fi
done
echo "spx get vms shows launched instances"

# Test 1a-ii: SSH Connectivity & Volume Verification
echo ""
echo "Test 1a-ii: SSH Connectivity & Volume Verification"
echo "----------------------------------------"
echo "Testing SSH into all 3 instances..."

# Arrays to store SSH details for post-termination verification
SSH_PORTS=()
SSH_HOSTS=()

for idx in "${!INSTANCE_IDS[@]}"; do
    instance_id="${INSTANCE_IDS[$idx]}"
    echo ""
    echo "  Instance $((idx + 1)): $instance_id"

    # Get SSH connection details from QEMU process
    echo "  Getting SSH port..."
    SSH_PORT=$(get_ssh_port "$instance_id")
    if [ -z "$SSH_PORT" ]; then
        echo "  ERROR: Failed to get SSH port for instance $instance_id"
        exit 1
    fi
    SSH_HOST=$(get_ssh_host "$instance_id")
    echo "  SSH endpoint: $SSH_HOST:$SSH_PORT"

    SSH_PORTS+=("$SSH_PORT")
    SSH_HOSTS+=("$SSH_HOST")

    # Wait for SSH to become ready (VM boot + cloud-init)
    wait_for_ssh "$SSH_HOST" "$SSH_PORT" "multinode-test-key.pem" 30

    # Test basic SSH connectivity
    test_ssh_connectivity "$SSH_HOST" "$SSH_PORT" "multinode-test-key.pem"

    # Check root volume size via lsblk
    echo "  Verifying root volume size from inside the VM..."
    ROOT_VOL_ID_SSH=$($AWS_EC2 describe-instances --instance-ids "$instance_id" \
        --query 'Reservations[0].Instances[0].BlockDeviceMappings[0].Ebs.VolumeId' --output text)
    ROOT_VOL_SIZE_API=$($AWS_EC2 describe-volumes --volume-ids "$ROOT_VOL_ID_SSH" \
        --query 'Volumes[0].Size' --output text)
    # Find the disk backing the root filesystem (avoids picking up floppy/cdrom devices)
    # 1. findmnt gets the source device for / (e.g. /dev/vda1)
    # 2. lsblk PKNAME resolves to parent disk name (e.g. vda)
    # 3. lsblk -b -d gets that disk's byte size
    ROOT_DISK_BYTES=$(ssh -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o LogLevel=ERROR \
        -o ConnectTimeout=5 \
        -o BatchMode=yes \
        -p "$SSH_PORT" \
        -i "multinode-test-key.pem" \
        ec2-user@"$SSH_HOST" 'SRC=$(findmnt -n -o SOURCE /); PKN=$(lsblk -n -o PKNAME "$SRC" 2>/dev/null | head -1); DEV=${PKN:-$(basename "$SRC")}; lsblk -b -d -n -o SIZE "/dev/$DEV"' | tr -d '[:space:]')
    if [ -z "$ROOT_DISK_BYTES" ] || [ "$ROOT_DISK_BYTES" = "0" ]; then
        echo "  ERROR: Failed to get root disk size from VM (got: '$ROOT_DISK_BYTES')"
        echo "  lsblk debug output:"
        ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
            -o ConnectTimeout=5 -o BatchMode=yes -p "$SSH_PORT" -i "multinode-test-key.pem" \
            ec2-user@"$SSH_HOST" 'lsblk -b -d; echo "---"; findmnt -n -o SOURCE /; cat /proc/partitions' || true
        exit 1
    fi
    ROOT_DISK_GIB=$((ROOT_DISK_BYTES / 1073741824))
    echo "  Root disk size from VM: ${ROOT_DISK_GIB}GiB (API reports: ${ROOT_VOL_SIZE_API}GiB)"
    if [ "$ROOT_DISK_GIB" -ne "$ROOT_VOL_SIZE_API" ]; then
        echo "  ERROR: Root volume size mismatch: VM reports ${ROOT_DISK_GIB}GiB, API reports ${ROOT_VOL_SIZE_API}GiB"
        exit 1
    fi
    echo "  Root volume size verified"

    # Verify hostname contains instance ID
    echo "  Verifying hostname inside the VM..."
    VM_HOSTNAME=$(ssh -o StrictHostKeyChecking=no \
        -o UserKnownHostsFile=/dev/null \
        -o ConnectTimeout=5 \
        -o BatchMode=yes \
        -p "$SSH_PORT" \
        -i "multinode-test-key.pem" \
        ec2-user@"$SSH_HOST" 'hostname' 2>/dev/null)
    echo "  VM hostname: $VM_HOSTNAME"
    # Hostname uses truncated ID: spinifex-vm-<first 8 hex chars of instance ID>
    SHORT_ID=$(echo "$instance_id" | sed 's/^i-//' | cut -c1-8)
    if echo "$VM_HOSTNAME" | grep -q "$SHORT_ID"; then
        echo "  Hostname contains instance ID prefix ($SHORT_ID)"
    else
        echo "  WARNING: Hostname '$VM_HOSTNAME' does not contain instance ID prefix '$SHORT_ID' (non-fatal)"
    fi
done

echo ""
echo "  SSH connectivity and volume verification passed for all instances"

# Test 2: DescribeInstances Aggregation
echo ""
echo "Test 2: DescribeInstances Aggregation"
echo "----------------------------------------"
echo "Verifying all instances are returned via fan-out query..."

DESCRIBE_OUTPUT=$($AWS_EC2 describe-instances --query 'Reservations[*].Instances[*].InstanceId' --output text)
DESCRIBED_COUNT=$(echo "$DESCRIBE_OUTPUT" | wc -w)

echo "  Launched: ${#INSTANCE_IDS[@]} instances"
echo "  Described: $DESCRIBED_COUNT instances"

if [ "$DESCRIBED_COUNT" -lt "${#INSTANCE_IDS[@]}" ]; then
    echo "ERROR: DescribeInstances did not return all instances"
    echo "  Expected: ${#INSTANCE_IDS[@]}, Got: $DESCRIBED_COUNT"
    exit 1
fi
echo "  Aggregation test passed"

# Test 3: Cross-Node Operations
echo ""
echo "Test 3: Cross-Node Operations"
echo "----------------------------------------"
echo "Testing stop/start/terminate via gateway regardless of instance location..."

# Pick first instance for cross-node operations
TEST_INSTANCE="${INSTANCE_IDS[0]}"
echo "  Test instance: $TEST_INSTANCE"

# Stop instance
echo "  Stopping instance..."
$AWS_EC2 stop-instances --instance-ids "$TEST_INSTANCE" > /dev/null
wait_for_instance_state "$TEST_INSTANCE" "stopped" 30

# Start instance
echo "  Starting instance..."
$AWS_EC2 start-instances --instance-ids "$TEST_INSTANCE" > /dev/null
wait_for_instance_state "$TEST_INSTANCE" "running" 30

echo "  Cross-node operations test passed"

# Test 4: NATS Cluster Health (Post-Operations)
echo ""
echo "Test 4: NATS Cluster Health (Post-Operations)"
echo "----------------------------------------"
echo "Verifying NATS cluster is still healthy after operations..."

verify_nats_cluster 3 || {
    echo "WARNING: NATS cluster verification failed after operations"
}

# Phase 6: Cluster Shutdown + Restart
echo ""
echo "Phase 6: Cluster Shutdown + Restart"
echo "========================================"
echo "Testing spx admin cluster shutdown command..."

# Test 6a: Dry-run shutdown
echo ""
echo "Test 6a: Dry-Run Shutdown"
echo "----------------------------------------"
echo "Running cluster shutdown in dry-run mode..."

DRY_RUN_OUTPUT=$(./bin/spx admin cluster shutdown --dry-run --config "$HOME/node1/config/spinifex.toml" 2>&1)
echo "$DRY_RUN_OUTPUT"

# Validate dry-run output contains expected phases
for phase in GATE DRAIN STORAGE PERSIST INFRA; do
    if echo "$DRY_RUN_OUTPUT" | grep -qi "$phase"; then
        echo "  Phase $phase found in shutdown plan"
    else
        echo "  WARNING: Phase $phase not found in dry-run output"
    fi
done
echo "  Dry-run shutdown test passed"

# Test 6b: Real coordinated shutdown
echo ""
echo "Test 6b: Coordinated Cluster Shutdown"
echo "----------------------------------------"
echo "Running cluster shutdown..."

./bin/spx admin cluster shutdown --force --timeout 30s --config "$HOME/node1/config/spinifex.toml" 2>&1 || {
    echo "  WARNING: Cluster shutdown command returned non-zero exit code"
}
CLUSTER_SERVICES_STARTED="false"

# Verify all services are down
echo "  Waiting for services to stop..."
sleep 1
if ! verify_all_services_down; then
    echo "  Some services still running, force-cleaning..."
    force_cleanup_all_nodes
fi

# Test 6c: Restart and recovery
echo ""
echo "Test 6c: Cluster Restart + Recovery"
echo "----------------------------------------"
echo "Restarting all node services concurrently..."

# Cluster restart requires concurrent startup: NATS needs route peers to form,
# Predastore needs Raft quorum (2/3), and the daemon needs JetStream.
# Sequential start would leave node1 waiting for quorum that never arrives.
start_node_services 1 "$HOME/node1" &
start_node_services 2 "$HOME/node2" &
start_node_services 3 "$HOME/node3" &
wait
CLUSTER_SERVICES_STARTED="true"

echo ""
echo "Waiting for cluster to stabilize..."
sleep 5

# Verify NATS cluster reformed
echo ""
verify_nats_cluster 3 || {
    echo "WARNING: NATS cluster verification failed after restart"
}

# Wait for gateway
echo ""
wait_for_gateway "${NODE1_IP}" 15

# Wait for daemon readiness
wait_for_daemon_ready "https://${NODE1_IP}:${AWSGW_PORT}"

# Smoke test: describe-instance-types
echo ""
echo "Running post-restart smoke test..."
SMOKE_OUTPUT=$($AWS_EC2 describe-instance-types --query 'InstanceTypes[*].InstanceType' --output text 2>/dev/null)
if [ -n "$SMOKE_OUTPUT" ] && [ "$SMOKE_OUTPUT" != "None" ]; then
    echo "  Smoke test passed: describe-instance-types returned: $SMOKE_OUTPUT"
else
    echo "  ERROR: Smoke test failed: describe-instance-types returned empty/None"
    exit 1
fi

echo "  Cluster shutdown + restart test passed"

# Test 6d: Instance relaunch and terminate after restart
echo ""
echo "Test 6d: Instance Relaunch + Terminate"
echo "----------------------------------------"
echo "Waiting for instances to relaunch after cluster restart..."

# All 3 instances were running before shutdown — the daemon will relaunch them.
# Must wait for them to finish launching (pending → running) before terminate
# will work, because the NATS per-instance subscription is only created after
# QEMU starts.
for instance_id in "${INSTANCE_IDS[@]}"; do
    echo "  Waiting for $instance_id to finish relaunching..."
    COUNT=0
    while [ $COUNT -lt 60 ]; do
        STATE=$($AWS_EC2 describe-instances --instance-ids "$instance_id" \
            --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "unknown")
        if [ "$STATE" = "running" ]; then
            echo "  $instance_id relaunched successfully: $STATE"
            break
        fi
        sleep 1
        COUNT=$((COUNT + 1))
    done
    if [ $COUNT -ge 60 ]; then
        echo "  WARNING: $instance_id still in $STATE after 60s"
    fi
done

# Terminate all instances
echo ""
echo "Terminating all instances..."
if ! terminate_and_wait "${INSTANCE_IDS[@]}"; then
    echo ""
    echo "ERROR: Some instances failed to terminate properly after restart"
    dump_all_node_logs
    exit 1
fi

echo "  Instance relaunch + terminate after restart passed"

echo ""
echo "========================================"
echo "Multi-Node E2E Tests Completed Successfully"
echo "========================================"
exit 0
