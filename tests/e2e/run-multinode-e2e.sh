#!/bin/bash
set -e

# Multi-node E2E test runner
# This script sets up a 3-node Hive cluster using simulated IPs on the loopback interface
# and runs distributed instance tests.

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

    # Stop all node services
    stop_all_nodes || true

    # Remove simulated IPs
    remove_simulated_ips || true

    echo "Cleanup complete"
}
trap cleanup EXIT

# Use Hive profile for AWS CLI
export AWS_PROFILE=hive


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

# Create ramdisk mount point
mkdir -p /mnt/ramdisk

echo ""

# Phase 2: Cluster Initialization
echo "Phase 2: Cluster Initialization"
echo "========================================"

# Initialize leader node (node1)
echo ""
init_leader_node

# Trust the Hive CA certificate for AWS CLI SSL verification
echo ""
echo "Adding Hive CA certificate to system trust store..."
sudo cp ~/node1/config/ca.pem /usr/local/share/ca-certificates/hive-ca.crt
sudo update-ca-certificates

# Start node1 services first (leader must be running for join)
echo ""
echo "Starting node1 services..."
start_node_services 1 "$HOME/node1"

# Wait for node1's NATS and cluster service to be ready
echo "Waiting for node1 cluster service..."
sleep 5

# Join follower nodes
echo ""
join_follower_node 2
join_follower_node 3

# Start follower node services
echo ""
echo "Starting node2 services..."
start_node_services 2 "$HOME/node2"

echo ""
echo "Starting node3 services..."
start_node_services 3 "$HOME/node3"

# Wait for all services to stabilize
echo ""
echo "Waiting for cluster to stabilize..."
sleep 10

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
wait_for_gateway "${NODE1_IP}" 30

# Define AWS CLI args pointing to node1's gateway
AWS_EC2="aws --endpoint-url https://${NODE1_IP}:${AWSGW_PORT} ec2"

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

# Import Ubuntu image (use node1's config and hive-dir)
echo ""
echo "Importing Ubuntu image..."
IMPORT_LOG=$(./bin/hive admin images import \
    --file /root/images/ubuntu-24.04.img \
    --arch "$ARCH" \
    --distro ubuntu \
    --version 24.04 \
    --config "$HOME/node1/config/hive.toml" \
    --hive-dir "$HOME/node1/" \
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
    sleep 2
done

# Wait for all instances to be running
echo ""
echo "Waiting for instances to reach running state..."
for instance_id in "${INSTANCE_IDS[@]}"; do
    wait_for_instance_state "$instance_id" "running" 60 || {
        echo "ERROR: Instance $instance_id failed to start"
        exit 1
    }
done

# Check distribution
echo ""
check_instance_distribution

# Test 1b: Volume Lifecycle (Attach/Detach)
echo ""
echo "Test 1b: Volume Lifecycle (Attach/Detach)"
echo "----------------------------------------"
echo "Testing volume create -> resize -> attach -> detach -> delete..."

# Create a test volume
echo "  Creating 10GB volume in ap-southeast-2a..."
CREATE_OUTPUT=$($AWS_EC2 create-volume --size 10 --availability-zone ap-southeast-2a)
TEST_VOLUME_ID=$(echo "$CREATE_OUTPUT" | jq -r '.VolumeId')

if [ -z "$TEST_VOLUME_ID" ] || [ "$TEST_VOLUME_ID" == "null" ]; then
    echo "  ERROR: Failed to create test volume"
    echo "  Output: $CREATE_OUTPUT"
    exit 1
fi
echo "  Created volume: $TEST_VOLUME_ID"

# Resize to 20GB
NEW_SIZE=20
echo "  Modifying volume to ${NEW_SIZE}GB..."
$AWS_EC2 modify-volume --volume-id "$TEST_VOLUME_ID" --size "$NEW_SIZE"

# Verify resize
echo "  Verifying resize..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    VOLUME_SIZE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].Size' --output text)

    if [ "$VOLUME_SIZE" -eq "$NEW_SIZE" ]; then
        echo "  Volume resized successfully to ${NEW_SIZE}GB"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$VOLUME_SIZE" -ne "$NEW_SIZE" ]; then
    echo "  ERROR: Volume failed to resize to ${NEW_SIZE}GB (current: ${VOLUME_SIZE}GB)"
    exit 1
fi

# Attach volume to the first running instance
echo "  Attaching volume $TEST_VOLUME_ID to instance ${INSTANCE_IDS[0]}..."
$AWS_EC2 attach-volume --volume-id "$TEST_VOLUME_ID" --instance-id "${INSTANCE_IDS[0]}" --device /dev/sdf

# Verify attachment
echo "  Verifying volume attachment..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    ATTACH_STATE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].Attachments[0].State' --output text)
    ATTACH_INSTANCE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].Attachments[0].InstanceId' --output text)
    VOL_STATE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].State' --output text)

    if [ "$VOL_STATE" == "in-use" ] && [ "$ATTACH_STATE" == "attached" ] && [ "$ATTACH_INSTANCE" == "${INSTANCE_IDS[0]}" ]; then
        echo "  Volume attached successfully (State=$VOL_STATE, AttachState=$ATTACH_STATE, Instance=$ATTACH_INSTANCE)"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$ATTACH_STATE" != "attached" ] || [ "$ATTACH_INSTANCE" != "${INSTANCE_IDS[0]}" ]; then
    echo "  ERROR: Volume attachment verification failed (AttachState=$ATTACH_STATE, Instance=$ATTACH_INSTANCE)"
    exit 1
fi

# Detach volume (without --instance-id to test gateway resolution path)
echo "  Detaching volume $TEST_VOLUME_ID..."
$AWS_EC2 detach-volume --volume-id "$TEST_VOLUME_ID"

# Verify detachment
echo "  Verifying volume detachment..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    VOL_STATE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].State' --output text)

    if [ "$VOL_STATE" == "available" ]; then
        echo "  Volume detached successfully (State=$VOL_STATE)"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$VOL_STATE" != "available" ]; then
    echo "  ERROR: Volume detachment verification failed (State=$VOL_STATE)"
    exit 1
fi

# Delete the test volume
echo "  Deleting test volume $TEST_VOLUME_ID..."
$AWS_EC2 delete-volume --volume-id "$TEST_VOLUME_ID"

# Verify deletion
echo "  Verifying volume deletion..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    set +e
    VOLUME_CHECK=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].VolumeId' --output text 2>&1)
    DESCRIBE_EXIT=$?
    set -e

    if [ $DESCRIBE_EXIT -ne 0 ] || [ "$VOLUME_CHECK" == "None" ] || [ -z "$VOLUME_CHECK" ]; then
        echo "  Volume deleted successfully"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ $COUNT -ge 30 ]; then
    echo "  ERROR: Volume deletion verification timed out"
    exit 1
fi

echo "  Volume lifecycle test passed (create -> resize -> attach -> detach -> delete)"

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

# Cleanup: Terminate all test instances
echo ""
echo "Cleanup: Deleting test resources"
echo "----------------------------------------"

# Terminate all instances
for instance_id in "${INSTANCE_IDS[@]}"; do
    echo "  Terminating $instance_id..."
    $AWS_EC2 terminate-instances --instance-ids "$instance_id" > /dev/null
done

# Wait for termination - track failures
echo "  Waiting for termination..."
TERMINATION_FAILED=0
for instance_id in "${INSTANCE_IDS[@]}"; do
    if ! wait_for_instance_state "$instance_id" "terminated" 30; then
        echo "  WARNING: Failed to confirm termination of $instance_id"
        TERMINATION_FAILED=1
    fi
done

if [ $TERMINATION_FAILED -ne 0 ]; then
    echo ""
    echo "ERROR: Some instances failed to terminate properly"
    dump_all_node_logs
    exit 1
fi

echo ""
echo "========================================"
echo "Multi-Node E2E Tests Completed Successfully"
echo "========================================"
exit 0
