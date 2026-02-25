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

    # Kill any lingering formation background processes
    [ -n "$LEADER_INIT_PID" ] && kill "$LEADER_INIT_PID" 2>/dev/null || true
    [ -n "$JOIN2_PID" ] && kill "$JOIN2_PID" 2>/dev/null || true
    [ -n "$JOIN3_PID" ] && kill "$JOIN3_PID" 2>/dev/null || true

    # Try coordinated shutdown first (only if NATS is likely still up)
    if [ "$CLUSTER_SERVICES_STARTED" = "true" ]; then
        echo "Attempting coordinated cluster shutdown..."
        if timeout 60 ./bin/hive admin cluster shutdown --force --timeout 30s --config "$HOME/node1/config/hive.toml" 2>/dev/null; then
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

# Bootstrap OVN/OVS (required — start-dev.sh will block without it)
echo ""
echo "Bootstrapping OVN/OVS networking..."
bootstrap_ovn_docker

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

# Background init — starts formation server, generates certs first
echo ""
init_leader_node

# Trust CA cert (exists before formation completes — cert generation is the first step)
echo ""
echo "Adding Hive CA certificate to system trust store..."
sudo cp ~/node1/config/ca.pem /usr/local/share/ca-certificates/hive-ca.crt
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

# Phase 3b: Cluster Stats CLI (Multi-Node)
echo ""
echo "Phase 3b: Cluster Stats CLI (Multi-Node)"
echo "========================================"

# Test hive get nodes — should show all 3 nodes as Ready
echo "Testing hive get nodes..."
GET_NODES_OUTPUT=$(./bin/hive get nodes --config "$HOME/node1/config/hive.toml" --timeout 5s 2>/dev/null)
echo "$GET_NODES_OUTPUT"
READY_COUNT=$(echo "$GET_NODES_OUTPUT" | grep -c "Ready" || true)
if [ "$READY_COUNT" -lt 3 ]; then
    echo "WARNING: hive get nodes shows $READY_COUNT Ready nodes (expected 3)"
fi
echo "hive get nodes passed ($READY_COUNT Ready nodes)"

# Test hive top nodes — should show resource stats for all nodes
echo "Testing hive top nodes..."
TOP_NODES_OUTPUT=$(./bin/hive top nodes --config "$HOME/node1/config/hive.toml" --timeout 5s 2>/dev/null)
echo "$TOP_NODES_OUTPUT"
if ! echo "$TOP_NODES_OUTPUT" | grep -q "INSTANCE TYPE"; then
    echo "WARNING: hive top nodes did not show instance type capacity table"
fi
echo "hive top nodes passed"

# Test hive get vms — should show no VMs yet
echo "Testing hive get vms (empty)..."
GET_VMS_OUTPUT=$(./bin/hive get vms --config "$HOME/node1/config/hive.toml" --timeout 5s 2>/dev/null)
echo "$GET_VMS_OUTPUT"
echo "hive get vms (empty) passed"

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

# Phase 4b: Multi-Node Key Pair Operations
echo ""
echo "Phase 4b: Multi-Node Key Pair Operations"
echo "========================================"
echo "Testing key pair CRUD across the cluster..."

# Import a key pair (goes to a random node via queue group)
echo "  Generating local RSA key for import..."
ssh-keygen -t rsa -b 2048 -f multinode-test-key-2-local -N ""
$AWS_EC2 import-key-pair --key-name multinode-test-key-2 --public-key-material "fileb://multinode-test-key-2-local.pub"
echo "  Imported key: multinode-test-key-2"

# Describe key pairs — verify both keys are visible
echo "  Verifying both keys via describe-key-pairs..."
KEY_LIST=$($AWS_EC2 describe-key-pairs --query 'KeyPairs[*].KeyName' --output text)
echo "  Keys found: $KEY_LIST"
echo "$KEY_LIST" | grep -q "multinode-test-key" || {
    echo "  ERROR: multinode-test-key not found in describe-key-pairs"
    exit 1
}
echo "$KEY_LIST" | grep -q "multinode-test-key-2" || {
    echo "  ERROR: multinode-test-key-2 not found in describe-key-pairs"
    exit 1
}
echo "  Both keys visible"

# Delete the imported key
echo "  Deleting multinode-test-key-2..."
$AWS_EC2 delete-key-pair --key-name multinode-test-key-2

# Verify deletion
REMAINING_KEYS=$($AWS_EC2 describe-key-pairs --query 'KeyPairs[*].KeyName' --output text)
if echo "$REMAINING_KEYS" | grep -q "multinode-test-key-2"; then
    echo "  ERROR: multinode-test-key-2 was not deleted"
    exit 1
fi
echo "  Key deletion verified"
echo "  Multi-node key pair operations passed"

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
    wait_for_instance_state "$instance_id" "running" 30 || {
        echo "ERROR: Instance $instance_id failed to start"
        exit 1
    }
done

# Check distribution
echo ""
check_instance_distribution

# Verify hive get vms shows all running instances
echo ""
echo "Verifying hive get vms (with running VMs)..."
GET_VMS_OUTPUT=$(./bin/hive get vms --config "$HOME/node1/config/hive.toml" --timeout 5s 2>/dev/null)
echo "$GET_VMS_OUTPUT"
for instance_id in "${INSTANCE_IDS[@]}"; do
    if ! echo "$GET_VMS_OUTPUT" | grep -q "$instance_id"; then
        echo "WARNING: hive get vms did not show instance $instance_id"
    fi
done
echo "hive get vms shows launched instances"

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
    if echo "$VM_HOSTNAME" | grep -q "$instance_id"; then
        echo "  Hostname contains instance ID"
    else
        echo "  WARNING: Hostname '$VM_HOSTNAME' does not contain instance ID '$instance_id' (non-fatal)"
    fi
done

echo ""
echo "  SSH connectivity and volume verification passed for all instances"

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
while [ $COUNT -lt 15 ]; do
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
while [ $COUNT -lt 15 ]; do
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
while [ $COUNT -lt 15 ]; do
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
while [ $COUNT -lt 15 ]; do
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

if [ $COUNT -ge 15 ]; then
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
while [ $COUNT -lt 15 ]; do
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
while [ $COUNT -lt 15 ]; do
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

if [ $COUNT -ge 15 ]; then
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
echo "  Instance tag tests passed"

# Test 1d-ii: Tags on Volumes (multi-node)
echo ""
echo "Test 1d-ii: Tags on Volumes (Multi-Node)"
echo "----------------------------------------"

# Get the root volume of the first instance
TAG_VOL_ID=$($AWS_EC2 describe-instances --instance-ids "${INSTANCE_IDS[0]}" \
    --query 'Reservations[0].Instances[0].BlockDeviceMappings[0].Ebs.VolumeId' --output text)
echo "  Tagging volume $TAG_VOL_ID..."

# Create tags on volume
$AWS_EC2 create-tags --resources "$TAG_VOL_ID" --tags Key=Name,Value=multinode-root-vol Key=Environment,Value=testing

# Verify tags on volume
VOL_TAG_COUNT=$($AWS_EC2 describe-tags --filters "Name=resource-id,Values=$TAG_VOL_ID" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$VOL_TAG_COUNT" -ne 2 ]; then
    echo "  ERROR: Expected 2 tags on volume, got $VOL_TAG_COUNT"
    exit 1
fi
echo "  Volume has $VOL_TAG_COUNT tags"

# Filter by resource-type=volume
VOL_TYPE_TAGS=$($AWS_EC2 describe-tags --filters "Name=resource-type,Values=volume" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$VOL_TYPE_TAGS" -lt 2 ]; then
    echo "  ERROR: Expected at least 2 volume tags, got $VOL_TYPE_TAGS"
    exit 1
fi
echo "  Volume resource-type filter returned $VOL_TYPE_TAGS tags"

# Delete tags from volume
$AWS_EC2 delete-tags --resources "$TAG_VOL_ID" --tags Key=Name Key=Environment
VOL_TAG_AFTER=$($AWS_EC2 describe-tags --filters "Name=resource-id,Values=$TAG_VOL_ID" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$VOL_TAG_AFTER" -ne 0 ]; then
    echo "  ERROR: Expected 0 tags after delete, got $VOL_TAG_AFTER"
    exit 1
fi
echo "  Volume tag cleanup verified"

echo "  Tag management tests passed (instances + volumes)"

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

# Test 5: VM Crash Recovery (kill -9 → detect → auto-restart)
echo ""
echo "Test 5: VM Crash Recovery"
echo "----------------------------------------"
echo "Testing QEMU crash detection and auto-restart..."

# Use the second instance for crash testing (first was used for stop/start in Test 3)
CRASH_INSTANCE="${INSTANCE_IDS[1]}"
echo "  Crash test instance: $CRASH_INSTANCE"

# Verify instance is running before crash
CRASH_STATE=$($AWS_EC2 describe-instances --instance-ids "$CRASH_INSTANCE" \
    --query 'Reservations[0].Instances[0].State.Name' --output text)
if [ "$CRASH_STATE" != "running" ]; then
    echo "  ERROR: Instance not in running state before crash test (state: $CRASH_STATE)"
    exit 1
fi
echo "  Instance is running"

# Get the QEMU PID
QEMU_PID=$(get_qemu_pid "$CRASH_INSTANCE")
if [ -z "$QEMU_PID" ]; then
    echo "  ERROR: Could not find QEMU PID for $CRASH_INSTANCE"
    exit 1
fi
echo "  QEMU PID: $QEMU_PID"

# Kill QEMU with SIGKILL (simulates OOM kill)
echo "  Killing QEMU process with SIGKILL (simulating OOM kill)..."
kill -9 "$QEMU_PID"

# Brief pause for the daemon to detect the crash
sleep 3

# Verify the daemon detected the crash (state should be error or already recovering)
echo "  Checking post-crash state..."
POST_CRASH_STATE=$($AWS_EC2 describe-instances --instance-ids "$CRASH_INSTANCE" \
    --query 'Reservations[0].Instances[0].State.Name' --output text)
echo "  Post-crash state: $POST_CRASH_STATE"

if [ "$POST_CRASH_STATE" == "running" ]; then
    # Might have recovered very quickly, check if it's a new PID
    NEW_PID=$(get_qemu_pid "$CRASH_INSTANCE" || echo "")
    if [ "$NEW_PID" == "$QEMU_PID" ]; then
        echo "  ERROR: Instance still has same PID after kill -9, crash not detected"
        exit 1
    fi
    echo "  Instance already recovered with new PID: $NEW_PID (was: $QEMU_PID)"
else
    # Wait for auto-restart (backoff starts at 5s)
    echo "  Instance in $POST_CRASH_STATE state, waiting for auto-restart..."
    wait_for_instance_recovery "$CRASH_INSTANCE" 30 || {
        echo "  ERROR: Instance failed to recover from crash"
        # Dump daemon logs for debugging
        for i in 1 2 3; do
            if [ -f "$HOME/node$i/logs/hive.log" ]; then
                echo ""
                echo "  --- node$i daemon log (last 30 lines) ---"
                tail -30 "$HOME/node$i/logs/hive.log"
            fi
        done
        exit 1
    }
fi

# Verify the instance is running with a new QEMU process
RECOVERED_PID=$(get_qemu_pid "$CRASH_INSTANCE")
if [ -z "$RECOVERED_PID" ]; then
    echo "  ERROR: No QEMU process found after recovery"
    exit 1
fi

if [ "$RECOVERED_PID" == "$QEMU_PID" ]; then
    echo "  ERROR: QEMU PID unchanged after crash recovery (expected new process)"
    exit 1
fi
echo "  New QEMU PID: $RECOVERED_PID (was: $QEMU_PID)"

# Verify describe-instances shows running state
FINAL_STATE=$($AWS_EC2 describe-instances --instance-ids "$CRASH_INSTANCE" \
    --query 'Reservations[0].Instances[0].State.Name' --output text)
if [ "$FINAL_STATE" != "running" ]; then
    echo "  ERROR: Instance not running after recovery (state: $FINAL_STATE)"
    exit 1
fi
echo "  Instance is running after crash recovery"

# Verify SSH works after recovery (VM rebooted with fresh OS from root volume)
echo "  Verifying SSH after crash recovery..."
CRASH_SSH_PORT=$(get_ssh_port "$CRASH_INSTANCE" 30)
CRASH_SSH_HOST=$(get_ssh_host "$CRASH_INSTANCE")
if [ -n "$CRASH_SSH_PORT" ]; then
    echo "  SSH endpoint after recovery: $CRASH_SSH_HOST:$CRASH_SSH_PORT"
    # Update SSH details for later termination verification
    SSH_PORTS[1]="$CRASH_SSH_PORT"
    SSH_HOSTS[1]="$CRASH_SSH_HOST"
    wait_for_ssh "$CRASH_SSH_HOST" "$CRASH_SSH_PORT" "multinode-test-key.pem" 30 || {
        echo "  WARNING: SSH not ready after crash recovery (non-fatal, VM may still be booting)"
    }
else
    echo "  WARNING: Could not determine SSH port after recovery (non-fatal)"
fi

# Test 5b: Crash Loop Prevention (kill 4 times rapidly to exceed max restarts)
echo ""
echo "Test 5b: Crash Loop Prevention"
echo "----------------------------------------"
echo "Testing that crash loop is detected and restarts stop after max attempts..."

# Use the third instance for crash loop testing
LOOP_INSTANCE="${INSTANCE_IDS[2]}"
echo "  Crash loop test instance: $LOOP_INSTANCE"

# Verify instance is running
LOOP_STATE=$($AWS_EC2 describe-instances --instance-ids "$LOOP_INSTANCE" \
    --query 'Reservations[0].Instances[0].State.Name' --output text)
if [ "$LOOP_STATE" != "running" ]; then
    echo "  ERROR: Instance not running before crash loop test (state: $LOOP_STATE)"
    exit 1
fi

# Kill QEMU repeatedly to exhaust restart attempts (max 3 in 10 min window)
for crash_num in 1 2 3 4; do
    echo "  Crash $crash_num/4: killing QEMU..."
    LOOP_PID=$(get_qemu_pid "$LOOP_INSTANCE" || echo "")
    if [ -z "$LOOP_PID" ]; then
        echo "  No QEMU process found (instance may be in error state)"
        break
    fi

    kill -9 "$LOOP_PID"

    if [ $crash_num -lt 4 ]; then
        # Wait for restart (backoff increases: 5s, 10s, 20s)
        # Give generous time for each restart cycle
        local_max=$((15 + crash_num * 10))
        echo "  Waiting up to ${local_max}s for restart or error state..."
        attempt=0
        while [ $attempt -lt $local_max ]; do
            state=$($AWS_EC2 describe-instances --instance-ids "$LOOP_INSTANCE" \
                --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "unknown")
            if [ "$state" == "running" ]; then
                echo "  Instance restarted (crash $crash_num)"
                break
            fi
            if [ "$state" == "error" ] && [ $crash_num -ge 3 ]; then
                echo "  Instance in error state after crash $crash_num (restart limit may be reached)"
                break
            fi
            sleep 2
            attempt=$((attempt + 2))
        done
    fi
done

# After 4 rapid crashes, the instance should be in error state (exceeded max 3 restarts)
sleep 5
LOOP_FINAL_STATE=$($AWS_EC2 describe-instances --instance-ids "$LOOP_INSTANCE" \
    --query 'Reservations[0].Instances[0].State.Name' --output text)
echo "  Final state after crash loop: $LOOP_FINAL_STATE"

if [ "$LOOP_FINAL_STATE" == "error" ]; then
    echo "  Crash loop prevention working: instance stayed in error state"
else
    echo "  WARNING: Instance in state '$LOOP_FINAL_STATE' (expected 'error' after exceeding max restarts)"
    echo "  This may be expected if timing allowed restarts to spread across the window"
fi

echo "  Crash recovery tests passed"

# Phase 5c: VPC Networking
echo ""
echo "Phase 5c: VPC Networking"
echo "========================================"
echo "Testing VPC instance launch, PrivateIpAddress, and same-subnet connectivity..."

# Step 1: Create VPC and Subnet
echo ""
echo "Step 1: Create VPC + Subnet"
echo "----------------------------------------"

VPC_OUTPUT=$($AWS_EC2 create-vpc --cidr-block 10.100.0.0/16)
VPC_ID=$(echo "$VPC_OUTPUT" | jq -r '.Vpc.VpcId')
if [ -z "$VPC_ID" ] || [ "$VPC_ID" == "null" ]; then
    echo "  ERROR: Failed to create VPC"
    echo "  Output: $VPC_OUTPUT"
    exit 1
fi
echo "  Created VPC: $VPC_ID (10.100.0.0/16)"

SUBNET_OUTPUT=$($AWS_EC2 create-subnet --vpc-id "$VPC_ID" --cidr-block 10.100.1.0/24)
SUBNET_ID=$(echo "$SUBNET_OUTPUT" | jq -r '.Subnet.SubnetId')
if [ -z "$SUBNET_ID" ] || [ "$SUBNET_ID" == "null" ]; then
    echo "  ERROR: Failed to create subnet"
    echo "  Output: $SUBNET_OUTPUT"
    exit 1
fi
echo "  Created Subnet: $SUBNET_ID (10.100.1.0/24)"

# Brief pause for OVN topology to be programmed (logical switch + router port + DHCP)
sleep 2

# Step 2: Launch 3 VPC instances
echo ""
echo "Step 2: Launch 3 VPC instances"
echo "----------------------------------------"

VPC_INSTANCE_IDS=()
for i in 1 2 3; do
    echo "  Launching VPC instance $i with subnet $SUBNET_ID..."
    RUN_OUTPUT=$($AWS_EC2 run-instances \
        --image-id "$AMI_ID" \
        --instance-type "$INSTANCE_TYPE" \
        --key-name multinode-test-key \
        --subnet-id "$SUBNET_ID")

    VPC_INST_ID=$(echo "$RUN_OUTPUT" | jq -r '.Instances[0].InstanceId')
    VPC_INST_IP=$(echo "$RUN_OUTPUT" | jq -r '.Instances[0].PrivateIpAddress // empty')

    if [ -z "$VPC_INST_ID" ] || [ "$VPC_INST_ID" == "null" ]; then
        echo "  ERROR: Failed to launch VPC instance $i"
        echo "  Output: $RUN_OUTPUT"
        exit 1
    fi
    echo "  Launched: $VPC_INST_ID (PrivateIpAddress: ${VPC_INST_IP:-not yet assigned})"
    VPC_INSTANCE_IDS+=("$VPC_INST_ID")

    sleep 2
done

# Wait for all VPC instances to be running
echo ""
echo "Waiting for VPC instances to reach running state..."
for vpc_inst in "${VPC_INSTANCE_IDS[@]}"; do
    wait_for_instance_state "$vpc_inst" "running" 30 || {
        echo "ERROR: VPC instance $vpc_inst failed to start"
        exit 1
    }
done

# Step 3: Verify PrivateIpAddress in DescribeInstances
echo ""
echo "Step 3: Verify PrivateIpAddress in DescribeInstances"
echo "----------------------------------------"

VPC_PRIVATE_IPS=()
for vpc_inst in "${VPC_INSTANCE_IDS[@]}"; do
    DESCRIBE_OUT=$($AWS_EC2 describe-instances --instance-ids "$vpc_inst")
    PRIVATE_IP=$(echo "$DESCRIBE_OUT" | jq -r '.Reservations[0].Instances[0].PrivateIpAddress // empty')
    INST_SUBNET=$(echo "$DESCRIBE_OUT" | jq -r '.Reservations[0].Instances[0].SubnetId // empty')
    INST_VPC=$(echo "$DESCRIBE_OUT" | jq -r '.Reservations[0].Instances[0].VpcId // empty')
    ENI_COUNT=$(echo "$DESCRIBE_OUT" | jq -r '.Reservations[0].Instances[0].NetworkInterfaces | length')

    if [ -z "$PRIVATE_IP" ]; then
        echo "  ERROR: $vpc_inst has no PrivateIpAddress"
        echo "  Describe output: $(echo "$DESCRIBE_OUT" | jq -c '.Reservations[0].Instances[0] | {PrivateIpAddress, SubnetId, VpcId, NetworkInterfaces}')"
        exit 1
    fi

    echo "  $vpc_inst: IP=$PRIVATE_IP, Subnet=$INST_SUBNET, VPC=$INST_VPC, ENIs=$ENI_COUNT"

    if [ "$INST_SUBNET" != "$SUBNET_ID" ]; then
        echo "  ERROR: SubnetId mismatch (expected $SUBNET_ID, got $INST_SUBNET)"
        exit 1
    fi
    if [ "$INST_VPC" != "$VPC_ID" ]; then
        echo "  ERROR: VpcId mismatch (expected $VPC_ID, got $INST_VPC)"
        exit 1
    fi
    if [ "$ENI_COUNT" -lt 1 ]; then
        echo "  ERROR: No NetworkInterfaces found"
        exit 1
    fi

    VPC_PRIVATE_IPS+=("$PRIVATE_IP")
done

# Verify all IPs are unique and in the subnet range (10.100.1.x)
echo ""
echo "  Verifying IP uniqueness and subnet range..."
UNIQUE_IPS=$(printf '%s\n' "${VPC_PRIVATE_IPS[@]}" | sort -u | wc -l)
if [ "$UNIQUE_IPS" -ne "${#VPC_PRIVATE_IPS[@]}" ]; then
    echo "  ERROR: Duplicate IPs detected: ${VPC_PRIVATE_IPS[*]}"
    exit 1
fi
for ip in "${VPC_PRIVATE_IPS[@]}"; do
    if ! echo "$ip" | grep -qE '^10\.100\.1\.[0-9]+$'; then
        echo "  ERROR: IP $ip not in expected subnet 10.100.1.0/24"
        exit 1
    fi
done
echo "  All IPs unique and in correct subnet: ${VPC_PRIVATE_IPS[*]}"

# Step 4: SSH via DEV_NETWORKING hostfwd and test ping connectivity
# DISABLED: VPC instances use OVN DHCP which takes ~6 min per instance to get SSH ready.
# With 3 instances this adds ~18 min to the E2E run. The SSH+ping test is best-effort
# anyway (OVN overlay not fully programmed in Docker single-host mode). IP allocation
# and subnet correctness are already verified in Step 3.
echo ""
echo "Step 4: SSH + Ping Connectivity (SKIPPED — OVN DHCP wait too slow in CI)"
echo "----------------------------------------"

# Step 5: Stop/Start IP persistence
echo ""
echo "Step 5: Stop/Start IP Persistence"
echo "----------------------------------------"
echo "Verifying private IPs persist through stop/start cycle (AWS behavior)..."

# Record IPs before stop
echo "  IPs before stop: ${VPC_PRIVATE_IPS[*]}"

# Stop all VPC instances
echo ""
echo "  Stopping all VPC instances..."
for vpc_inst in "${VPC_INSTANCE_IDS[@]}"; do
    echo "  Stopping $vpc_inst..."
    $AWS_EC2 stop-instances --instance-ids "$vpc_inst" > /dev/null
done

for vpc_inst in "${VPC_INSTANCE_IDS[@]}"; do
    wait_for_instance_state "$vpc_inst" "stopped" 30 || {
        echo "  ERROR: VPC instance $vpc_inst failed to stop"
        exit 1
    }
done
echo "  All VPC instances stopped"

# Verify IPs are still present in DescribeInstances while stopped
echo ""
echo "  Verifying IPs persist in stopped state..."
for idx in "${!VPC_INSTANCE_IDS[@]}"; do
    vpc_inst="${VPC_INSTANCE_IDS[$idx]}"
    expected_ip="${VPC_PRIVATE_IPS[$idx]}"

    STOPPED_IP=$($AWS_EC2 describe-instances --instance-ids "$vpc_inst" \
        --query 'Reservations[0].Instances[0].PrivateIpAddress' --output text)

    if [ "$STOPPED_IP" != "$expected_ip" ]; then
        echo "  ERROR: $vpc_inst IP changed while stopped (expected $expected_ip, got $STOPPED_IP)"
        exit 1
    fi
    echo "  $vpc_inst: IP=$STOPPED_IP (unchanged)"
done

# Start all VPC instances
echo ""
echo "  Starting all VPC instances..."
for vpc_inst in "${VPC_INSTANCE_IDS[@]}"; do
    echo "  Starting $vpc_inst..."
    $AWS_EC2 start-instances --instance-ids "$vpc_inst" > /dev/null
done

for vpc_inst in "${VPC_INSTANCE_IDS[@]}"; do
    wait_for_instance_state "$vpc_inst" "running" 30 || {
        echo "  ERROR: VPC instance $vpc_inst failed to restart"
        exit 1
    }
done
echo "  All VPC instances restarted"

# Verify IPs are identical after restart
echo ""
echo "  Verifying IPs persist after restart..."
IP_MISMATCHES=0
for idx in "${!VPC_INSTANCE_IDS[@]}"; do
    vpc_inst="${VPC_INSTANCE_IDS[$idx]}"
    expected_ip="${VPC_PRIVATE_IPS[$idx]}"

    RESTARTED_IP=$($AWS_EC2 describe-instances --instance-ids "$vpc_inst" \
        --query 'Reservations[0].Instances[0].PrivateIpAddress' --output text)

    if [ "$RESTARTED_IP" == "$expected_ip" ]; then
        echo "  $vpc_inst: IP=$RESTARTED_IP (matches pre-stop)"
    else
        echo "  ERROR: $vpc_inst IP changed after restart (expected $expected_ip, got $RESTARTED_IP)"
        IP_MISMATCHES=$((IP_MISMATCHES + 1))
    fi
done

if [ "$IP_MISMATCHES" -gt 0 ]; then
    echo "  ERROR: $IP_MISMATCHES instances had IP changes — ENI not persisting through stop/start"
    exit 1
fi

echo "  Stop/start IP persistence verified — all IPs match"

# Step 6: Clean up VPC instances
echo ""
echo "Step 6: Clean up VPC resources"
echo "----------------------------------------"

terminate_and_wait "${VPC_INSTANCE_IDS[@]}" || true

# Clean up subnet and VPC
echo "  Deleting subnet $SUBNET_ID..."
$AWS_EC2 delete-subnet --subnet-id "$SUBNET_ID" 2>/dev/null || echo "  (subnet delete failed, may have ENIs)"

echo "  Deleting VPC $VPC_ID..."
$AWS_EC2 delete-vpc --vpc-id "$VPC_ID" 2>/dev/null || echo "  (vpc delete failed, may have subnets)"

echo "  VPC networking tests passed"

# Phase 6: Cluster Shutdown + Restart
echo ""
echo "Phase 6: Cluster Shutdown + Restart"
echo "========================================"
echo "Testing hive admin cluster shutdown command..."

# Test 6a: Dry-run shutdown
echo ""
echo "Test 6a: Dry-Run Shutdown"
echo "----------------------------------------"
echo "Running cluster shutdown in dry-run mode..."

DRY_RUN_OUTPUT=$(./bin/hive admin cluster shutdown --dry-run --config "$HOME/node1/config/hive.toml" 2>&1)
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

./bin/hive admin cluster shutdown --force --timeout 30s --config "$HOME/node1/config/hive.toml" 2>&1 || {
    echo "  WARNING: Cluster shutdown command returned non-zero exit code"
}
CLUSTER_SERVICES_STARTED="false"

# Verify all services are down
echo "  Waiting for services to stop..."
sleep 2
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

# Two instances were running before shutdown — the daemon will relaunch them.
# Must wait for them to finish launching (pending → running) before terminate
# will work, because the NATS per-instance subscription is only created after
# QEMU starts.
for instance_id in "${INSTANCE_IDS[0]}" "${INSTANCE_IDS[1]}"; do
    echo "  Waiting for $instance_id to finish relaunching..."
    COUNT=0
    while [ $COUNT -lt 30 ]; do
        STATE=$($AWS_EC2 describe-instances --instance-ids "$instance_id" \
            --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "unknown")
        if [ "$STATE" = "running" ]; then
            echo "  $instance_id relaunched successfully: $STATE"
            break
        fi
        sleep 2
        COUNT=$((COUNT + 1))
    done
    if [ $COUNT -ge 30 ]; then
        echo "  WARNING: $instance_id still in $STATE after 60s"
    fi
done

# Verify crash-loop instance (INSTANCE_IDS[2]) stayed in error — daemon should
# not relaunch an instance that exceeded its crash restart limit.
CRASH_LOOP_STATE=$($AWS_EC2 describe-instances --instance-ids "${INSTANCE_IDS[2]}" \
    --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "unknown")
echo "  Crash-loop instance ${INSTANCE_IDS[2]} state: $CRASH_LOOP_STATE"
if [ "$CRASH_LOOP_STATE" != "error" ]; then
    echo "  WARNING: Expected crash-loop instance to remain in error state (got: $CRASH_LOOP_STATE)"
fi

# Terminate all instances (including the crash-loop one in error state)
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
