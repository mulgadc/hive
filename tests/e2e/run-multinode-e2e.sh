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

# Test 1c: Snapshot Lifecycle
echo ""
echo "Test 1c: Snapshot Lifecycle"
echo "----------------------------------------"
echo "Testing snapshot create -> describe -> copy -> delete..."

# Use the root volume of the first instance — it's already attached and mounted
# in viperblockd, which is required for create-snapshot.
SNAP_VOL_ID=$($AWS_EC2 describe-instances --instance-ids "${INSTANCE_IDS[0]}" \
    --query 'Reservations[0].Instances[0].BlockDeviceMappings[0].Ebs.VolumeId' --output text)
echo "  Using root volume $SNAP_VOL_ID (attached to ${INSTANCE_IDS[0]})"
SNAP_VOL_SIZE=$($AWS_EC2 describe-volumes --volume-ids "$SNAP_VOL_ID" \
    --query 'Volumes[0].Size' --output text)

# Create a snapshot
echo "  Creating snapshot from volume $SNAP_VOL_ID..."
SNAP_OUTPUT=$($AWS_EC2 create-snapshot --volume-id "$SNAP_VOL_ID" --description "multinode-e2e-snapshot")
SNAPSHOT_ID=$(echo "$SNAP_OUTPUT" | jq -r '.SnapshotId')

if [ -z "$SNAPSHOT_ID" ] || [ "$SNAPSHOT_ID" == "null" ]; then
    echo "  ERROR: Failed to create snapshot"
    echo "  Output: $SNAP_OUTPUT"
    exit 1
fi
echo "  Created snapshot: $SNAPSHOT_ID"

# Verify create response fields
SNAP_STATE=$(echo "$SNAP_OUTPUT" | jq -r '.State')
SNAP_VOL_REF=$(echo "$SNAP_OUTPUT" | jq -r '.VolumeId')
SNAP_SIZE=$(echo "$SNAP_OUTPUT" | jq -r '.VolumeSize')

if [ "$SNAP_VOL_REF" != "$SNAP_VOL_ID" ]; then
    echo "  ERROR: Snapshot VolumeId mismatch: expected $SNAP_VOL_ID, got $SNAP_VOL_REF"
    exit 1
fi
if [ "$SNAP_SIZE" -ne "$SNAP_VOL_SIZE" ]; then
    echo "  ERROR: Snapshot VolumeSize mismatch: expected $SNAP_VOL_SIZE, got $SNAP_SIZE"
    exit 1
fi
echo "  Create response verified (State=$SNAP_STATE, VolumeId=$SNAP_VOL_REF, Size=$SNAP_SIZE)"

# Poll until completed
echo "  Waiting for snapshot to complete..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    SNAP_STATE=$($AWS_EC2 describe-snapshots --snapshot-ids "$SNAPSHOT_ID" \
        --query 'Snapshots[0].State' --output text)

    if [ "$SNAP_STATE" == "completed" ]; then
        echo "  Snapshot completed"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$SNAP_STATE" != "completed" ]; then
    echo "  ERROR: Snapshot failed to reach completed state (State=$SNAP_STATE)"
    exit 1
fi

# Describe by ID and verify
echo "  Verifying snapshot via describe-snapshots..."
DESCRIBE_SNAP=$($AWS_EC2 describe-snapshots --snapshot-ids "$SNAPSHOT_ID")
DESC_VOL_ID=$(echo "$DESCRIBE_SNAP" | jq -r '.Snapshots[0].VolumeId')
DESC_SIZE=$(echo "$DESCRIBE_SNAP" | jq -r '.Snapshots[0].VolumeSize')
DESC_DESC=$(echo "$DESCRIBE_SNAP" | jq -r '.Snapshots[0].Description')

if [ "$DESC_VOL_ID" != "$SNAP_VOL_ID" ]; then
    echo "  ERROR: Describe VolumeId mismatch: expected $SNAP_VOL_ID, got $DESC_VOL_ID"
    exit 1
fi
if [ "$DESC_SIZE" -ne "$SNAP_VOL_SIZE" ]; then
    echo "  ERROR: Describe VolumeSize mismatch: expected $SNAP_VOL_SIZE, got $DESC_SIZE"
    exit 1
fi
if [ "$DESC_DESC" != "multinode-e2e-snapshot" ]; then
    echo "  ERROR: Describe Description mismatch: expected 'multinode-e2e-snapshot', got '$DESC_DESC'"
    exit 1
fi
echo "  Describe verified (VolumeId=$DESC_VOL_ID, Size=$DESC_SIZE, Description=$DESC_DESC)"

# Copy the snapshot
echo "  Copying snapshot $SNAPSHOT_ID..."
COPY_OUTPUT=$($AWS_EC2 copy-snapshot --source-snapshot-id "$SNAPSHOT_ID" --source-region ap-southeast-2 --description "multinode-e2e-copy")
COPY_SNAPSHOT_ID=$(echo "$COPY_OUTPUT" | jq -r '.SnapshotId')

if [ -z "$COPY_SNAPSHOT_ID" ] || [ "$COPY_SNAPSHOT_ID" == "null" ]; then
    echo "  ERROR: Failed to copy snapshot"
    echo "  Output: $COPY_OUTPUT"
    exit 1
fi
echo "  Copied snapshot: $COPY_SNAPSHOT_ID"

if [ "$COPY_SNAPSHOT_ID" == "$SNAPSHOT_ID" ]; then
    echo "  ERROR: Copy snapshot ID should differ from original"
    exit 1
fi

# Verify both exist
TOTAL_SNAPS=$($AWS_EC2 describe-snapshots \
    --snapshot-ids "$SNAPSHOT_ID" "$COPY_SNAPSHOT_ID" \
    --query 'length(Snapshots)' --output text)

if [ "$TOTAL_SNAPS" -ne 2 ]; then
    echo "  ERROR: Expected 2 snapshots, got $TOTAL_SNAPS"
    exit 1
fi
echo "  Both snapshots visible via describe-snapshots"

# Verify copy description
COPY_DESC=$($AWS_EC2 describe-snapshots --snapshot-ids "$COPY_SNAPSHOT_ID" \
    --query 'Snapshots[0].Description' --output text)
if [ "$COPY_DESC" != "multinode-e2e-copy" ]; then
    echo "  ERROR: Copy description mismatch: expected 'multinode-e2e-copy', got '$COPY_DESC'"
    exit 1
fi

# Delete original
echo "  Deleting original snapshot $SNAPSHOT_ID..."
$AWS_EC2 delete-snapshot --snapshot-id "$SNAPSHOT_ID"

# Verify original gone, copy remains
echo "  Verifying snapshot deletion..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    set +e
    SNAP_CHECK=$($AWS_EC2 describe-snapshots --snapshot-ids "$SNAPSHOT_ID" \
        --query 'Snapshots[0].SnapshotId' --output text 2>&1)
    SNAP_EXIT=$?
    set -e

    if [ $SNAP_EXIT -ne 0 ] || [ "$SNAP_CHECK" == "None" ] || [ -z "$SNAP_CHECK" ]; then
        echo "  Original snapshot deleted successfully"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ $COUNT -ge 30 ]; then
    echo "  ERROR: Snapshot deletion verification timed out"
    exit 1
fi

# Verify copy still exists
COPY_STATE=$($AWS_EC2 describe-snapshots --snapshot-ids "$COPY_SNAPSHOT_ID" \
    --query 'Snapshots[0].State' --output text)
if [ "$COPY_STATE" != "completed" ]; then
    echo "  ERROR: Copy snapshot should still exist (State=$COPY_STATE)"
    exit 1
fi
echo "  Copy snapshot intact after original deletion"

# Delete copy
echo "  Deleting copy snapshot $COPY_SNAPSHOT_ID..."
$AWS_EC2 delete-snapshot --snapshot-id "$COPY_SNAPSHOT_ID"

echo "  Snapshot lifecycle test passed (create -> describe -> copy -> delete)"

# Test 1c-ii: Verify Snapshot-Backed Instance Launch
echo ""
echo "Test 1c-ii: Verify Snapshot-Backed Instance Launch"
echo "----------------------------------------"
echo "All run-instances calls go through cloneAMIToVolume() -> OpenFromSnapshot(),"
echo "so the Test 1 instances are already snapshot-backed. Verify their volume configs."

AWS_S3="aws --endpoint-url https://${NODE1_IP}:${PREDASTORE_PORT} s3"

# Verify the AMI snapshot exists in Predastore
echo "  Checking AMI snapshot in Predastore..."
SNAP_PREFIX="snap-$AMI_ID"
SNAP_FILES=$($AWS_S3 ls "s3://predastore/$SNAP_PREFIX/" 2>&1 || echo "")
if echo "$SNAP_FILES" | grep -q "config.json"; then
    echo "  AMI snapshot config found at $SNAP_PREFIX/"
else
    echo "  ERROR: AMI snapshot config not found at $SNAP_PREFIX/"
    exit 1
fi

# Verify the first instance's root volume has SnapshotID and SourceVolumeName
SNAP_ROOT_VOL=$($AWS_EC2 describe-instances --instance-ids "${INSTANCE_IDS[0]}" \
    --query 'Reservations[0].Instances[0].BlockDeviceMappings[0].Ebs.VolumeId' --output text)
echo "  Verifying root volume $SNAP_ROOT_VOL is snapshot-backed via Predastore config..."
VOL_CONFIG=$($AWS_S3 cp "s3://predastore/$SNAP_ROOT_VOL/config.json" - 2>/dev/null || echo "{}")
VOL_SNAPSHOT_ID=$(echo "$VOL_CONFIG" | jq -r '.SnapshotID // empty')
VOL_SOURCE_NAME=$(echo "$VOL_CONFIG" | jq -r '.SourceVolumeName // empty')

if [ -z "$VOL_SNAPSHOT_ID" ]; then
    echo "  ERROR: Volume config missing SnapshotID — launch was NOT snapshot-backed"
    exit 1
fi
if [ -z "$VOL_SOURCE_NAME" ]; then
    echo "  ERROR: Volume config missing SourceVolumeName — launch was NOT snapshot-backed"
    exit 1
fi
echo "  Volume is snapshot-backed (SnapshotID=$VOL_SNAPSHOT_ID, SourceVolumeName=$VOL_SOURCE_NAME)"

echo "  Snapshot-backed instance launch verified"

# Test 1d: Tag Management
echo ""
echo "Test 1d: Tag Management"
echo "----------------------------------------"
echo "Testing create-tags -> describe-tags -> delete-tags..."

# Use the first instance for tag tests
TAG_INSTANCE="${INSTANCE_IDS[0]}"

# Create tags on instance
echo "  Creating tags on instance $TAG_INSTANCE..."
$AWS_EC2 create-tags --resources "$TAG_INSTANCE" --tags Key=Name,Value=multinode-test Key=Environment,Value=testing Key=DeleteMe,Value=please

# Verify tags with describe-tags (resource-id filter)
echo "  Verifying tags on instance..."
TAG_COUNT=$($AWS_EC2 describe-tags --filters "Name=resource-id,Values=$TAG_INSTANCE" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$TAG_COUNT" -ne 3 ]; then
    echo "  ERROR: Expected 3 tags on instance, got $TAG_COUNT"
    exit 1
fi
echo "  Instance has $TAG_COUNT tags"

# Filter by key
echo "  Testing key filter..."
ENV_TAGS=$($AWS_EC2 describe-tags --filters "Name=key,Values=Environment" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$ENV_TAGS" -lt 1 ]; then
    echo "  ERROR: Expected at least 1 'Environment' tag, got $ENV_TAGS"
    exit 1
fi
echo "  Key filter returned $ENV_TAGS tags"

# Filter by resource-type
echo "  Testing resource-type filter..."
INSTANCE_TAGS=$($AWS_EC2 describe-tags --filters "Name=resource-type,Values=instance" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$INSTANCE_TAGS" -lt 3 ]; then
    echo "  ERROR: Expected at least 3 instance tags, got $INSTANCE_TAGS"
    exit 1
fi
echo "  Resource-type filter returned $INSTANCE_TAGS instance tags"

# Overwrite a tag value
echo "  Overwriting Name tag..."
$AWS_EC2 create-tags --resources "$TAG_INSTANCE" --tags Key=Name,Value=multinode-updated
UPDATED_NAME=$($AWS_EC2 describe-tags \
    --filters "Name=resource-id,Values=$TAG_INSTANCE" "Name=key,Values=Name" \
    --query 'Tags[0].Value' --output text)
if [ "$UPDATED_NAME" != "multinode-updated" ]; then
    echo "  ERROR: Tag overwrite failed: expected 'multinode-updated', got '$UPDATED_NAME'"
    exit 1
fi
echo "  Tag overwrite verified"

# Delete tag by key (unconditional)
echo "  Deleting DeleteMe tag unconditionally..."
$AWS_EC2 delete-tags --resources "$TAG_INSTANCE" --tags Key=DeleteMe
REMAINING=$($AWS_EC2 describe-tags --filters "Name=resource-id,Values=$TAG_INSTANCE" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$REMAINING" -ne 2 ]; then
    echo "  ERROR: Expected 2 tags after unconditional delete, got $REMAINING"
    exit 1
fi
echo "  Unconditional delete verified ($REMAINING tags remaining)"

# Delete tag with wrong value (should NOT delete)
echo "  Attempting delete with wrong value (should be no-op)..."
$AWS_EC2 delete-tags --resources "$TAG_INSTANCE" --tags Key=Environment,Value=production
ENV_STILL=$($AWS_EC2 describe-tags \
    --filters "Name=resource-id,Values=$TAG_INSTANCE" "Name=key,Values=Environment" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$ENV_STILL" -ne 1 ]; then
    echo "  ERROR: Value-conditional delete incorrectly removed tag"
    exit 1
fi
echo "  Value-conditional mismatch preserved tag"

# Delete tag with correct value
echo "  Deleting Environment tag with correct value..."
$AWS_EC2 delete-tags --resources "$TAG_INSTANCE" --tags Key=Environment,Value=testing
ENV_GONE=$($AWS_EC2 describe-tags \
    --filters "Name=resource-id,Values=$TAG_INSTANCE" "Name=key,Values=Environment" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$ENV_GONE" -ne 0 ]; then
    echo "  ERROR: Value-conditional delete failed to remove matching tag"
    exit 1
fi
echo "  Value-conditional match deleted tag"

# Verify only Name tag remains
FINAL_COUNT=$($AWS_EC2 describe-tags --filters "Name=resource-id,Values=$TAG_INSTANCE" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$FINAL_COUNT" -ne 1 ]; then
    echo "  ERROR: Expected 1 tag remaining, got $FINAL_COUNT"
    exit 1
fi
echo "  Tag management tests passed"

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
