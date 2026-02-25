#!/bin/bash
set -e

# Source helper functions for SSH testing
source ./tests/e2e/lib/multinode-helpers.sh

# Ensure services are stopped on exit and print logs on failure
cleanup() {
    EXIT_CODE=$?
    if [ $EXIT_CODE -ne 0 ]; then
        echo ""
        echo "=== NATS Service Log ==="
        if [ -f ~/hive/logs/nats.log ]; then
            tail -100 ~/hive/logs/nats.log 2>/dev/null
        fi
        echo ""
        echo "=== Predastore Service Log ==="
        if [ -f ~/hive/logs/predastore.log ]; then
            tail -100 ~/hive/logs/predastore.log 2>/dev/null
        fi
        echo ""
        echo "=== Viperblock Service Log ==="
        if [ -f ~/hive/logs/viperblock.log ]; then
            tail -100 ~/hive/logs/viperblock.log 2>/dev/null
        fi
        echo ""
        echo "=== Hive Daemon Log ==="
        if [ -f ~/hive/logs/hive.log ]; then
            tail -200 ~/hive/logs/hive.log 2>/dev/null
        fi
        echo ""
        echo "=== AWS Gateway Log ==="
        if [ -f ~/hive/logs/awsgw.log ]; then
            tail -200 ~/hive/logs/awsgw.log 2>/dev/null
        fi
    fi
    ./scripts/stop-dev.sh
    exit $EXIT_CODE
}
trap cleanup EXIT

# Use Hive profile
export AWS_PROFILE=hive

# Ensure we are in the project root
cd "$(dirname "$0")/../.."

# Phase 1: Environment Setup
echo "Phase 1: Environment Setup"

# Check for KVM support inside the container
echo "Checking for KVM support..."
if [ -e /dev/kvm ]; then
    echo "✅ /dev/kvm exists"
    if [ -w /dev/kvm ]; then
        echo "✅ /dev/kvm is writable"
    else
        echo "❌ /dev/kvm is NOT writable. QEMU will fail."
        exit 1
    fi
else
    echo "❌ /dev/kvm does NOT exist. Ensure --privileged and -v /dev/kvm:/dev/kvm are used."
    exit 1
fi

# Bootstrap OVN/OVS (required — start-dev.sh will block without it)
echo "Bootstrapping OVN/OVS networking..."
bootstrap_ovn_docker

./bin/hive admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1

# Trust the Hive CA certificate for AWS CLI SSL verification
echo "Adding Hive CA certificate to system trust store..."
sudo cp ~/hive/config/ca.pem /usr/local/share/ca-certificates/hive-ca.crt
sudo update-ca-certificates

# Start all services
# Ensure logs directory exists for start-dev.sh
mkdir -p ~/hive/logs
mkdir -p /mnt/ramdisk
./scripts/start-dev.sh

# Wait for health checks on https://localhost:9999 (AWS Gateway)
echo "Waiting for AWS Gateway..."
MAX_RETRIES=15
COUNT=0

until curl -s https://localhost:9999 > /dev/null || [ $COUNT -eq $MAX_RETRIES ]; do
    echo "Waiting for gateway... ($COUNT/$MAX_RETRIES)"
    sleep 2
    COUNT=$((COUNT + 1))
done

if [ $COUNT -eq $MAX_RETRIES ]; then
    echo "Gateway failed to start"
    exit 1
fi

# Wait for daemon NATS subscriptions to be active
wait_for_daemon_ready "https://localhost:9999"

# Define common AWS CLI args
AWS_EC2="aws --endpoint-url https://localhost:9999 ec2"

# Phase 1b: Cluster Stats CLI
echo "Phase 1b: Cluster Stats CLI"

# Test hive get nodes — should show node1 as Ready
echo "Testing hive get nodes..."
GET_NODES_OUTPUT=$(./bin/hive get nodes --config ~/hive/config/hive.toml --timeout 5s 2>/dev/null)
echo "$GET_NODES_OUTPUT"
if ! echo "$GET_NODES_OUTPUT" | grep -q "Ready"; then
    echo "hive get nodes did not show any Ready nodes"
    exit 1
fi
echo "hive get nodes passed"

# Test hive top nodes — should show CPU/MEM stats
echo "Testing hive top nodes..."
TOP_NODES_OUTPUT=$(./bin/hive top nodes --config ~/hive/config/hive.toml --timeout 5s 2>/dev/null)
echo "$TOP_NODES_OUTPUT"
if ! echo "$TOP_NODES_OUTPUT" | grep -q "0/"; then
    echo "hive top nodes did not show resource stats"
    exit 1
fi
echo "hive top nodes passed"

# Test hive get vms — should show no VMs yet
echo "Testing hive get vms (empty)..."
GET_VMS_OUTPUT=$(./bin/hive get vms --config ~/hive/config/hive.toml --timeout 5s 2>/dev/null)
echo "$GET_VMS_OUTPUT"
if ! echo "$GET_VMS_OUTPUT" | grep -q "No VMs found"; then
    echo "hive get vms should show 'No VMs found' before any launches"
    exit 1
fi
echo "hive get vms (empty) passed"

# Phase 2: Discovery & Metadata
echo "Phase 2: Discovery & Metadata"
# Verify describe-regions (just ensure it returns at least one region)
$AWS_EC2 describe-regions | jq -e '.Regions | length > 0'

# Verify describe-availability-zones
echo "Verifying describe-availability-zones..."
AZ_OUTPUT=$($AWS_EC2 describe-availability-zones)
echo "$AZ_OUTPUT" | jq -e '.AvailabilityZones | length > 0'
AZ_NAME=$(echo "$AZ_OUTPUT" | jq -r '.AvailabilityZones[0].ZoneName')
AZ_STATE=$(echo "$AZ_OUTPUT" | jq -r '.AvailabilityZones[0].State')
if [ "$AZ_STATE" != "available" ]; then
    echo "Expected AZ state 'available', got '$AZ_STATE'"
    exit 1
fi
echo "DescribeAvailabilityZones verified (Zone=$AZ_NAME, State=$AZ_STATE)"

# Discover available instance types from Hive
# Hive generates these based on the host CPU (e.g., m7i.micro, m8g.small, etc.)
echo "Discovering available instance types..."
AVAILABLE_TYPES=$($AWS_EC2 describe-instance-types --query 'InstanceTypes[*].InstanceType' --output text)
echo "Available instance types: $AVAILABLE_TYPES"

# Pick the nano instance type for minimal resource usage in tests
INSTANCE_TYPE=$(echo $AVAILABLE_TYPES | tr ' ' '\n' | grep -m1 'nano')
if [ -z "$INSTANCE_TYPE" ] || [ "$INSTANCE_TYPE" == "None" ]; then
    echo "No instance types found!"
    exit 1
fi
echo "Selected instance type for test: $INSTANCE_TYPE"

# Get architecture for the selected instance type
ARCH=$($AWS_EC2 describe-instance-types --instance-types "$INSTANCE_TYPE" --query 'InstanceTypes[0].ProcessorInfo.SupportedArchitectures[0]' --output text)
echo "Detected architecture for $INSTANCE_TYPE: $ARCH"

# Verify describe-instance-types (ensure the chosen type is available)
$AWS_EC2 describe-instance-types | jq -e ".InstanceTypes[] | select(.InstanceType==\"$INSTANCE_TYPE\")"

# Phase 2b: Serial Console Access Settings
echo "Phase 2b: Serial Console Access Settings"

# Default should be disabled
SERIAL_DEFAULT=$($AWS_EC2 get-serial-console-access-status --query 'SerialConsoleAccessEnabled' --output text)
if [ "$SERIAL_DEFAULT" != "False" ]; then
    echo "Expected serial console access default to be False, got $SERIAL_DEFAULT"
    exit 1
fi
echo "  Default state: disabled"

# Enable
ENABLE_RESULT=$($AWS_EC2 enable-serial-console-access --query 'SerialConsoleAccessEnabled' --output text)
if [ "$ENABLE_RESULT" != "True" ]; then
    echo "Expected enable to return True, got $ENABLE_RESULT"
    exit 1
fi
SERIAL_ENABLED=$($AWS_EC2 get-serial-console-access-status --query 'SerialConsoleAccessEnabled' --output text)
if [ "$SERIAL_ENABLED" != "True" ]; then
    echo "Expected serial console access to be True after enable, got $SERIAL_ENABLED"
    exit 1
fi
echo "  Enabled: $SERIAL_ENABLED"

# Disable
DISABLE_RESULT=$($AWS_EC2 disable-serial-console-access --query 'SerialConsoleAccessEnabled' --output text)
if [ "$DISABLE_RESULT" != "False" ]; then
    echo "Expected disable to return False, got $DISABLE_RESULT"
    exit 1
fi
SERIAL_DISABLED=$($AWS_EC2 get-serial-console-access-status --query 'SerialConsoleAccessEnabled' --output text)
if [ "$SERIAL_DISABLED" != "False" ]; then
    echo "Expected serial console access to be False after disable, got $SERIAL_DISABLED"
    exit 1
fi
echo "  Disabled: $SERIAL_DISABLED"
echo "Serial console access settings tests passed"

# Phase 3: SSH Key Management
echo "Phase 3: SSH Key Management"
# Create test-key-1 (create-key-pair) and verify private key material is returned
KEY_MATERIAL=$($AWS_EC2 create-key-pair --key-name test-key-1 --query 'KeyMaterial' --output text)
if [ -z "$KEY_MATERIAL" ] || [ "$KEY_MATERIAL" == "None" ]; then
    echo "Failed to create key pair test-key-1"
    exit 1
fi
echo "$KEY_MATERIAL" > test-key-1.pem
chmod 600 test-key-1.pem

# Generate a local RSA key and import it as test-key-2 (import-key-pair)
ssh-keygen -t rsa -b 2048 -f test-key-2-local -N ""
$AWS_EC2 import-key-pair --key-name test-key-2 --public-key-material "fileb://test-key-2-local.pub"

# Verify both keys are present (describe-key-pairs)
$AWS_EC2 describe-key-pairs --query 'KeyPairs[*].KeyName' --output text | grep test-key-1
$AWS_EC2 describe-key-pairs --query 'KeyPairs[*].KeyName' --output text | grep test-key-2

# Delete test-key-2 (delete-key-pair) and verify only one remains
$AWS_EC2 delete-key-pair --key-name test-key-2
REMAINING_KEYS=$($AWS_EC2 describe-key-pairs --query 'KeyPairs[*].KeyName' --output text)
echo "Remaining keys: $REMAINING_KEYS"
echo "$REMAINING_KEYS" | grep test-key-1
if echo "$REMAINING_KEYS" | grep -q test-key-2; then
    echo "test-key-2 was not deleted"
    exit 1
fi

# Phase 4: Image Management
echo "Phase 4: Image Management"
# Detect correct image name based on architecture
if [ "$ARCH" = "x86_64" ]; then
    IMAGE_NAME="ubuntu-24.04-x86_64"
else
    IMAGE_NAME="ubuntu-24.04-arm64"
fi
echo "Using image: $IMAGE_NAME"

# Import the pre-downloaded Ubuntu image using file-based import
echo "Importing pre-cached Ubuntu image..."
IMPORT_LOG=$(./bin/hive admin images import \
    --file /root/images/ubuntu-24.04.img \
    --arch "$ARCH" \
    --distro ubuntu \
    --version 24.04 \
    --force 2>/dev/null)
AMI_ID=$(echo "$IMPORT_LOG" | grep -o 'ami-[a-z0-9]\+')

if [ -z "$AMI_ID" ]; then
    echo "Failed to capture AMI ID from import command"
    exit 1
fi
echo "Captured AMI ID: $AMI_ID"

# Verify the AMI exists using its ID (describe-images)
echo "Verifying AMI availability..."
$AWS_EC2 describe-images --image-ids "$AMI_ID" | jq -e ".Images[0] | select(.ImageId==\"$AMI_ID\")"

# Phase 5: Instance Lifecycle
echo "Phase 5: Instance Lifecycle"
# Launch a VM (run-instances)
echo "Running: aws ec2 run-instances --image-id $AMI_ID --instance-type $INSTANCE_TYPE --key-name test-key-1"
# Capture full output for debugging
set +e  # Temporarily disable exit on error to capture output
RUN_OUTPUT=$($AWS_EC2 run-instances --image-id "$AMI_ID" --instance-type "$INSTANCE_TYPE" --key-name test-key-1 2>&1)
RUN_EXIT_CODE=$?
set -e  # Re-enable exit on error
echo "Run instances exit code: $RUN_EXIT_CODE"
echo "Run instances output: $RUN_OUTPUT"
if [ $RUN_EXIT_CODE -ne 0 ]; then
    echo "❌ Failed to launch instance - AWS CLI returned error"
    exit 1
fi
INSTANCE_ID=$(echo "$RUN_OUTPUT" | jq -r '.Instances[0].InstanceId')
if [ -z "$INSTANCE_ID" ] || [ "$INSTANCE_ID" == "None" ] || [ "$INSTANCE_ID" == "null" ]; then
    echo "Failed to launch instance - no InstanceId in response"
    exit 1
fi
echo "Launched Instance ID: $INSTANCE_ID"

# Poll until state is running (describe-instances)
echo "Polling for instance running state..."
COUNT=0
STATE="unknown"
while [ $COUNT -lt 30 ]; do
    # Capture full output to check if instance even exists in the response
    DESCRIBE_OUTPUT=$($AWS_EC2 describe-instances --instance-ids "$INSTANCE_ID") || {
        echo "⚠️  Gateway request failed, retrying... ($COUNT/30)"
        sleep 2
        COUNT=$((COUNT + 1))
        continue
    }

    if [ -z "$DESCRIBE_OUTPUT" ]; then
        echo "⚠️  Gateway returned empty response, retrying..."
        sleep 2
        COUNT=$((COUNT + 1))
        continue
    fi

    # Extract state using jq
    STATE=$(echo "$DESCRIBE_OUTPUT" | jq -r '.Reservations[0].Instances[0].State.Name // "not-found"')

    echo "Instance state: $STATE"
    if [ "$STATE" == "running" ]; then
        break
    fi

    if [ "$STATE" == "terminated" ]; then
        echo "❌ Instance terminated unexpectedly!"
        exit 1
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$STATE" != "running" ]; then
    echo "Instance failed to reach running state"
    exit 1
fi

# Phase 5a-pre: Verify hive get vms shows running instance
echo "Phase 5a-pre: Cluster Stats CLI (with running VM)"
GET_VMS_OUTPUT=$(./bin/hive get vms --config ~/hive/config/hive.toml --timeout 5s 2>/dev/null)
echo "$GET_VMS_OUTPUT"
if ! echo "$GET_VMS_OUTPUT" | grep -q "$INSTANCE_ID"; then
    echo "hive get vms did not show running instance $INSTANCE_ID"
    exit 1
fi
echo "hive get vms shows running instance"

# Phase 5a: Validate instance metadata fields
echo "Phase 5a: Instance Metadata Validation"
DESCRIBE_META=$($AWS_EC2 describe-instances --instance-ids "$INSTANCE_ID")
META_TYPE=$(echo "$DESCRIBE_META" | jq -r '.Reservations[0].Instances[0].InstanceType')
META_KEY=$(echo "$DESCRIBE_META" | jq -r '.Reservations[0].Instances[0].KeyName')
META_IMAGE=$(echo "$DESCRIBE_META" | jq -r '.Reservations[0].Instances[0].ImageId')
META_BDM=$(echo "$DESCRIBE_META" | jq -r '.Reservations[0].Instances[0].BlockDeviceMappings | length')

if [ "$META_TYPE" != "$INSTANCE_TYPE" ]; then
    echo "InstanceType mismatch: expected $INSTANCE_TYPE, got $META_TYPE"
    exit 1
fi
if [ "$META_KEY" != "test-key-1" ]; then
    echo "KeyName mismatch: expected test-key-1, got $META_KEY"
    exit 1
fi
if [ "$META_IMAGE" != "$AMI_ID" ]; then
    echo "ImageId mismatch: expected $AMI_ID, got $META_IMAGE"
    exit 1
fi
if [ "$META_BDM" -lt 1 ]; then
    echo "Expected at least 1 BlockDeviceMapping, got $META_BDM"
    exit 1
fi
echo "Instance metadata validated (Type=$META_TYPE, Key=$META_KEY, Image=$META_IMAGE, BDMs=$META_BDM)"

# Phase 5a-ii: SSH Connectivity & Volume Verification
echo "Phase 5a-ii: SSH Connectivity & Volume Verification"

# Get SSH connection details from QEMU process
echo "Getting SSH port for instance $INSTANCE_ID..."
SSH_PORT=$(get_ssh_port "$INSTANCE_ID")
if [ -z "$SSH_PORT" ]; then
    echo "Failed to get SSH port for instance $INSTANCE_ID"
    exit 1
fi
SSH_HOST=$(get_ssh_host "$INSTANCE_ID")
echo "SSH endpoint: $SSH_HOST:$SSH_PORT"

# Wait for SSH to become ready (VM boot + cloud-init)
wait_for_ssh "$SSH_HOST" "$SSH_PORT" "test-key-1.pem" 30

# Test basic SSH connectivity
test_ssh_connectivity "$SSH_HOST" "$SSH_PORT" "test-key-1.pem"

# Check root volume size via lsblk
echo "Verifying root volume size from inside the VM..."
ROOT_VOL_SIZE_API=$($AWS_EC2 describe-volumes --query 'Volumes[0].Size' --output text)
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
    -i "test-key-1.pem" \
    ec2-user@"$SSH_HOST" 'SRC=$(findmnt -n -o SOURCE /); PKN=$(lsblk -n -o PKNAME "$SRC" 2>/dev/null | head -1); DEV=${PKN:-$(basename "$SRC")}; lsblk -b -d -n -o SIZE "/dev/$DEV"' | tr -d '[:space:]')
if [ -z "$ROOT_DISK_BYTES" ] || [ "$ROOT_DISK_BYTES" = "0" ]; then
    echo "Failed to get root disk size from VM (got: '$ROOT_DISK_BYTES')"
    echo "lsblk debug output:"
    ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o LogLevel=ERROR \
        -o ConnectTimeout=5 -o BatchMode=yes -p "$SSH_PORT" -i "test-key-1.pem" \
        ec2-user@"$SSH_HOST" 'lsblk -b -d; echo "---"; findmnt -n -o SOURCE /; cat /proc/partitions' || true
    exit 1
fi
ROOT_DISK_GIB=$((ROOT_DISK_BYTES / 1073741824))
echo "Root disk size from VM: ${ROOT_DISK_GIB}GiB (API reports: ${ROOT_VOL_SIZE_API}GiB)"
if [ "$ROOT_DISK_GIB" -ne "$ROOT_VOL_SIZE_API" ]; then
    echo "Root volume size mismatch: VM reports ${ROOT_DISK_GIB}GiB, API reports ${ROOT_VOL_SIZE_API}GiB"
    exit 1
fi
echo "Root volume size verified"

# Verify hostname contains instance ID
echo "Verifying hostname inside the VM..."
VM_HOSTNAME=$(ssh -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    -o ConnectTimeout=5 \
    -o BatchMode=yes \
    -p "$SSH_PORT" \
    -i "test-key-1.pem" \
    ec2-user@"$SSH_HOST" 'hostname' 2>/dev/null)
echo "VM hostname: $VM_HOSTNAME"
if echo "$VM_HOSTNAME" | grep -q "$INSTANCE_ID"; then
    echo "Hostname contains instance ID"
else
    echo "WARNING: Hostname '$VM_HOSTNAME' does not contain instance ID '$INSTANCE_ID' (non-fatal)"
fi

echo "SSH connectivity and volume verification passed"

# Phase 5a-iii: Console Output
echo "Phase 5a-iii: Console Output"

CONSOLE_OUTPUT=$($AWS_EC2 get-console-output --instance-id "$INSTANCE_ID")
CONSOLE_INSTANCE=$(echo "$CONSOLE_OUTPUT" | jq -r '.InstanceId')
CONSOLE_DATA=$(echo "$CONSOLE_OUTPUT" | jq -r '.Output // empty')

if [ "$CONSOLE_INSTANCE" != "$INSTANCE_ID" ]; then
    echo "GetConsoleOutput InstanceId mismatch: expected $INSTANCE_ID, got $CONSOLE_INSTANCE"
    exit 1
fi
echo "  GetConsoleOutput succeeded (InstanceId=$CONSOLE_INSTANCE, has output=$([ -n "$CONSOLE_DATA" ] && echo yes || echo no))"

echo "Console output tests passed"

# Verify root volume attached to the instance (describe-volumes)
VOLUME_ID=$($AWS_EC2 describe-volumes --query 'Volumes[0].VolumeId' --output text)
if [ -z "$VOLUME_ID" ] || [ "$VOLUME_ID" == "None" ]; then
    echo "Failed to find volume for instance $INSTANCE_ID"
    exit 1
fi
echo "Volume ID: $VOLUME_ID"

# Phase 5b: Volume Lifecycle (Attach/Detach)
echo "Phase 5b: Volume Lifecycle (Attach/Detach)"
echo "Testing volume create -> resize -> attach -> detach -> delete..."

# Create a test volume
echo "Creating 10GB volume in ap-southeast-2a..."
CREATE_OUTPUT=$($AWS_EC2 create-volume --size 10 --availability-zone ap-southeast-2a)
TEST_VOLUME_ID=$(echo "$CREATE_OUTPUT" | jq -r '.VolumeId')

if [ -z "$TEST_VOLUME_ID" ] || [ "$TEST_VOLUME_ID" == "null" ]; then
    echo "Failed to create test volume"
    echo "Output: $CREATE_OUTPUT"
    exit 1
fi
echo "Created volume: $TEST_VOLUME_ID"

# Resize to 20GB
NEW_SIZE=20
echo "Modifying volume to ${NEW_SIZE}GB..."
$AWS_EC2 modify-volume --volume-id "$TEST_VOLUME_ID" --size "$NEW_SIZE"

# Verify resize
echo "Verifying resize..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    VOLUME_SIZE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].Size' --output text)

    if [ "$VOLUME_SIZE" -eq "$NEW_SIZE" ]; then
        echo "Volume resized successfully to ${NEW_SIZE}GB"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$VOLUME_SIZE" -ne "$NEW_SIZE" ]; then
    echo "Volume failed to resize to ${NEW_SIZE}GB (current: ${VOLUME_SIZE}GB)"
    exit 1
fi

# Attach volume to the running instance
echo "Attaching volume $TEST_VOLUME_ID to instance $INSTANCE_ID..."
$AWS_EC2 attach-volume --volume-id "$TEST_VOLUME_ID" --instance-id "$INSTANCE_ID" --device /dev/sdf

# Verify attachment
echo "Verifying volume attachment..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    ATTACH_STATE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].Attachments[0].State' --output text)
    ATTACH_INSTANCE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].Attachments[0].InstanceId' --output text)
    VOL_STATE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].State' --output text)

    if [ "$VOL_STATE" == "in-use" ] && [ "$ATTACH_STATE" == "attached" ] && [ "$ATTACH_INSTANCE" == "$INSTANCE_ID" ]; then
        echo "Volume attached successfully (State=$VOL_STATE, AttachState=$ATTACH_STATE, Instance=$ATTACH_INSTANCE)"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$ATTACH_STATE" != "attached" ] || [ "$ATTACH_INSTANCE" != "$INSTANCE_ID" ]; then
    echo "Volume attachment verification failed (AttachState=$ATTACH_STATE, Instance=$ATTACH_INSTANCE)"
    exit 1
fi

# Detach volume (without --instance-id to test gateway resolution path)
echo "Detaching volume $TEST_VOLUME_ID..."
$AWS_EC2 detach-volume --volume-id "$TEST_VOLUME_ID"

# Verify detachment
echo "Verifying volume detachment..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    VOL_STATE=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].State' --output text)

    if [ "$VOL_STATE" == "available" ]; then
        echo "Volume detached successfully (State=$VOL_STATE)"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$VOL_STATE" != "available" ]; then
    echo "Volume detachment verification failed (State=$VOL_STATE)"
    exit 1
fi

# Delete the test volume
echo "Deleting test volume $TEST_VOLUME_ID..."
$AWS_EC2 delete-volume --volume-id "$TEST_VOLUME_ID"

# Verify deletion
echo "Verifying volume deletion..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    set +e
    VOLUME_CHECK=$($AWS_EC2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].VolumeId' --output text 2>&1)
    DESCRIBE_EXIT=$?
    set -e

    if [ $DESCRIBE_EXIT -ne 0 ] || [ "$VOLUME_CHECK" == "None" ] || [ -z "$VOLUME_CHECK" ]; then
        echo "Volume deleted successfully"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ $COUNT -ge 30 ]; then
    echo "Volume deletion verification timed out"
    exit 1
fi

echo "Volume lifecycle test passed (create -> resize -> attach -> detach -> delete)"

# Phase 5b-ii: DescribeVolumeStatus
echo "Phase 5b-ii: DescribeVolumeStatus"
echo "Testing describe-volume-status on root volume..."
VOL_STATUS_OUTPUT=$($AWS_EC2 describe-volume-status --volume-ids "$VOLUME_ID")
VOL_STATUS_ID=$(echo "$VOL_STATUS_OUTPUT" | jq -r '.VolumeStatuses[0].VolumeId')
VOL_STATUS_STATE=$(echo "$VOL_STATUS_OUTPUT" | jq -r '.VolumeStatuses[0].VolumeStatus.Status')

if [ "$VOL_STATUS_ID" != "$VOLUME_ID" ]; then
    echo "DescribeVolumeStatus VolumeId mismatch: expected $VOLUME_ID, got $VOL_STATUS_ID"
    exit 1
fi
echo "DescribeVolumeStatus verified (VolumeId=$VOL_STATUS_ID, Status=$VOL_STATUS_STATE)"

# Phase 5c: Snapshot Lifecycle
echo "Phase 5c: Snapshot Lifecycle"
echo "Testing snapshot create -> describe -> copy -> delete..."

# Use the root volume from Phase 5 — it's already attached and mounted in
# viperblockd, which is required for create-snapshot (the ebs.snapshot handler
# needs a live VB instance to flush).
echo "Using root volume $VOLUME_ID (already attached to $INSTANCE_ID)"
ROOT_VOL_SIZE=$($AWS_EC2 describe-volumes --volume-ids "$VOLUME_ID" \
    --query 'Volumes[0].Size' --output text)

# Create a snapshot from the attached root volume
echo "Creating snapshot from volume $VOLUME_ID..."
SNAP_OUTPUT=$($AWS_EC2 create-snapshot --volume-id "$VOLUME_ID" --description "e2e-test-snapshot")
SNAPSHOT_ID=$(echo "$SNAP_OUTPUT" | jq -r '.SnapshotId')

if [ -z "$SNAPSHOT_ID" ] || [ "$SNAPSHOT_ID" == "null" ]; then
    echo "Failed to create snapshot"
    echo "Output: $SNAP_OUTPUT"
    exit 1
fi
echo "Created snapshot: $SNAPSHOT_ID"

# Verify snapshot fields from create response
SNAP_STATE=$(echo "$SNAP_OUTPUT" | jq -r '.State')
SNAP_VOL_REF=$(echo "$SNAP_OUTPUT" | jq -r '.VolumeId')
SNAP_SIZE=$(echo "$SNAP_OUTPUT" | jq -r '.VolumeSize')
SNAP_PROGRESS=$(echo "$SNAP_OUTPUT" | jq -r '.Progress')

if [ "$SNAP_VOL_REF" != "$VOLUME_ID" ]; then
    echo "Snapshot VolumeId mismatch: expected $VOLUME_ID, got $SNAP_VOL_REF"
    exit 1
fi
if [ "$SNAP_SIZE" -ne "$ROOT_VOL_SIZE" ]; then
    echo "Snapshot VolumeSize mismatch: expected $ROOT_VOL_SIZE, got $SNAP_SIZE"
    exit 1
fi
echo "Snapshot create response verified (State=$SNAP_STATE, VolumeId=$SNAP_VOL_REF, Size=$SNAP_SIZE, Progress=$SNAP_PROGRESS)"

# Poll until snapshot is completed (should be immediate in v1, but poll for forward-compat)
echo "Waiting for snapshot to complete..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    SNAP_STATE=$($AWS_EC2 describe-snapshots --snapshot-ids "$SNAPSHOT_ID" \
        --query 'Snapshots[0].State' --output text)

    if [ "$SNAP_STATE" == "completed" ]; then
        echo "Snapshot completed"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$SNAP_STATE" != "completed" ]; then
    echo "Snapshot failed to reach completed state (State=$SNAP_STATE)"
    exit 1
fi

# Describe snapshot by ID and verify fields
echo "Verifying snapshot via describe-snapshots..."
DESCRIBE_SNAP=$($AWS_EC2 describe-snapshots --snapshot-ids "$SNAPSHOT_ID")
DESC_VOL_ID=$(echo "$DESCRIBE_SNAP" | jq -r '.Snapshots[0].VolumeId')
DESC_SIZE=$(echo "$DESCRIBE_SNAP" | jq -r '.Snapshots[0].VolumeSize')
DESC_DESC=$(echo "$DESCRIBE_SNAP" | jq -r '.Snapshots[0].Description')

if [ "$DESC_VOL_ID" != "$VOLUME_ID" ]; then
    echo "Describe snapshot VolumeId mismatch: expected $VOLUME_ID, got $DESC_VOL_ID"
    exit 1
fi
if [ "$DESC_SIZE" -ne "$ROOT_VOL_SIZE" ]; then
    echo "Describe snapshot VolumeSize mismatch: expected $ROOT_VOL_SIZE, got $DESC_SIZE"
    exit 1
fi
if [ "$DESC_DESC" != "e2e-test-snapshot" ]; then
    echo "Describe snapshot Description mismatch: expected 'e2e-test-snapshot', got '$DESC_DESC'"
    exit 1
fi
echo "Describe snapshot verified (VolumeId=$DESC_VOL_ID, Size=$DESC_SIZE, Description=$DESC_DESC)"

# Copy the snapshot
echo "Copying snapshot $SNAPSHOT_ID..."
COPY_OUTPUT=$($AWS_EC2 copy-snapshot --source-snapshot-id "$SNAPSHOT_ID" --source-region ap-southeast-2 --description "e2e-copy")
COPY_SNAPSHOT_ID=$(echo "$COPY_OUTPUT" | jq -r '.SnapshotId')

if [ -z "$COPY_SNAPSHOT_ID" ] || [ "$COPY_SNAPSHOT_ID" == "null" ]; then
    echo "Failed to copy snapshot"
    echo "Output: $COPY_OUTPUT"
    exit 1
fi
echo "Copied snapshot: $COPY_SNAPSHOT_ID"

# Verify the copy is a distinct snapshot
if [ "$COPY_SNAPSHOT_ID" == "$SNAPSHOT_ID" ]; then
    echo "Copy snapshot ID should differ from original"
    exit 1
fi

# Describe all snapshots — should see both original and copy
TOTAL_SNAPS=$($AWS_EC2 describe-snapshots \
    --snapshot-ids "$SNAPSHOT_ID" "$COPY_SNAPSHOT_ID" \
    --query 'length(Snapshots)' --output text)

if [ "$TOTAL_SNAPS" -ne 2 ]; then
    echo "Expected 2 snapshots, got $TOTAL_SNAPS"
    exit 1
fi
echo "Both snapshots visible via describe-snapshots"

# Verify copy has correct description
COPY_DESC=$($AWS_EC2 describe-snapshots --snapshot-ids "$COPY_SNAPSHOT_ID" \
    --query 'Snapshots[0].Description' --output text)
if [ "$COPY_DESC" != "e2e-copy" ]; then
    echo "Copy description mismatch: expected 'e2e-copy', got '$COPY_DESC'"
    exit 1
fi

# Delete the original snapshot
echo "Deleting original snapshot $SNAPSHOT_ID..."
$AWS_EC2 delete-snapshot --snapshot-id "$SNAPSHOT_ID"

# Verify original is gone, copy remains
echo "Verifying snapshot deletion..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    set +e
    SNAP_CHECK=$($AWS_EC2 describe-snapshots --snapshot-ids "$SNAPSHOT_ID" \
        --query 'Snapshots[0].SnapshotId' --output text 2>&1)
    SNAP_EXIT=$?
    set -e

    if [ $SNAP_EXIT -ne 0 ] || [ "$SNAP_CHECK" == "None" ] || [ -z "$SNAP_CHECK" ]; then
        echo "Original snapshot deleted successfully"
        break
    fi

    sleep 2
    COUNT=$((COUNT + 1))
done

if [ $COUNT -ge 30 ]; then
    echo "Snapshot deletion verification timed out"
    exit 1
fi

# Verify copy still exists
COPY_STATE=$($AWS_EC2 describe-snapshots --snapshot-ids "$COPY_SNAPSHOT_ID" \
    --query 'Snapshots[0].State' --output text)
if [ "$COPY_STATE" != "completed" ]; then
    echo "Copy snapshot should still exist (State=$COPY_STATE)"
    exit 1
fi
echo "Copy snapshot still intact after original deletion"

# Delete the copy
echo "Deleting copy snapshot $COPY_SNAPSHOT_ID..."
$AWS_EC2 delete-snapshot --snapshot-id "$COPY_SNAPSHOT_ID"

echo "Snapshot lifecycle test passed (create -> describe -> copy -> delete)"

# Phase 5d: Verify Snapshot-Backed Instance Launch
echo "Phase 5d: Verify Snapshot-Backed Instance Launch"
echo "All run-instances calls go through cloneAMIToVolume() -> OpenFromSnapshot(),"
echo "so the Phase 5 instance is already snapshot-backed. Verify its volume config."

AWS_S3="aws --endpoint-url https://localhost:8443 s3"

# Verify the AMI snapshot exists in Predastore
echo "Checking AMI snapshot in Predastore..."
SNAP_PREFIX="snap-$AMI_ID"
SNAP_FILES=$($AWS_S3 ls "s3://predastore/$SNAP_PREFIX/" 2>&1 || echo "")
if echo "$SNAP_FILES" | grep -q "config.json"; then
    echo "AMI snapshot config found at $SNAP_PREFIX/"
else
    echo "AMI snapshot config not found at $SNAP_PREFIX/"
    exit 1
fi

# Verify the Phase 5 instance's root volume has SnapshotID and SourceVolumeName
echo "Verifying root volume $VOLUME_ID is snapshot-backed via Predastore config..."
VOL_CONFIG=$($AWS_S3 cp "s3://predastore/$VOLUME_ID/config.json" - 2>/dev/null || echo "{}")
VOL_SNAPSHOT_ID=$(echo "$VOL_CONFIG" | jq -r '.SnapshotID // empty')
VOL_SOURCE_NAME=$(echo "$VOL_CONFIG" | jq -r '.SourceVolumeName // empty')

if [ -z "$VOL_SNAPSHOT_ID" ]; then
    echo "Volume config missing SnapshotID — launch was NOT snapshot-backed"
    exit 1
fi
if [ -z "$VOL_SOURCE_NAME" ]; then
    echo "Volume config missing SourceVolumeName — launch was NOT snapshot-backed"
    exit 1
fi
echo "Volume is snapshot-backed (SnapshotID=$VOL_SNAPSHOT_ID, SourceVolumeName=$VOL_SOURCE_NAME)"

echo "Snapshot-backed instance launch verified"

# Phase 5e: CreateImage Lifecycle
echo "Phase 5e: CreateImage Lifecycle"
echo "Creating custom AMI from running instance $INSTANCE_ID..."

CREATE_IMAGE_OUTPUT=$($AWS_EC2 create-image --instance-id "$INSTANCE_ID" --name "e2e-custom-ami" --description "E2E test custom image")
CUSTOM_AMI_ID=$(echo "$CREATE_IMAGE_OUTPUT" | jq -r '.ImageId')

if [ -z "$CUSTOM_AMI_ID" ] || [ "$CUSTOM_AMI_ID" == "null" ]; then
    echo "Failed to create custom image"
    echo "Output: $CREATE_IMAGE_OUTPUT"
    exit 1
fi
echo "Created custom AMI: $CUSTOM_AMI_ID"

# Verify the custom AMI exists via describe-images
echo "Verifying custom AMI via describe-images..."
CUSTOM_IMAGE=$($AWS_EC2 describe-images --image-ids "$CUSTOM_AMI_ID")
CUSTOM_IMAGE_NAME=$(echo "$CUSTOM_IMAGE" | jq -r '.Images[0].Name')
CUSTOM_IMAGE_STATE=$(echo "$CUSTOM_IMAGE" | jq -r '.Images[0].State')

if [ "$CUSTOM_IMAGE_NAME" != "e2e-custom-ami" ]; then
    echo "Custom AMI name mismatch: expected 'e2e-custom-ami', got '$CUSTOM_IMAGE_NAME'"
    exit 1
fi
echo "Custom AMI verified (Name=$CUSTOM_IMAGE_NAME, State=$CUSTOM_IMAGE_STATE)"

# Extract the backing snapshot ID from the custom AMI config in Predastore
# (needed later to clean up before termination, so DeleteOnTermination can work)
CUSTOM_AMI_CONFIG=$($AWS_S3 cp "s3://predastore/$CUSTOM_AMI_ID/config.json" - 2>/dev/null || echo "{}")
CUSTOM_AMI_SNAP_ID=$(echo "$CUSTOM_AMI_CONFIG" | jq -r '.VolumeConfig.AMIMetadata.SnapshotID // empty')
if [ -n "$CUSTOM_AMI_SNAP_ID" ]; then
    echo "Custom AMI backing snapshot: $CUSTOM_AMI_SNAP_ID"
else
    echo "WARNING: Could not extract backing snapshot ID from custom AMI config"
fi

echo "CreateImage lifecycle test passed"

# Phase 6: Tag Management
echo "Phase 6: Tag Management"

# 6a: Create tags on the instance
echo "Creating tags on instance $INSTANCE_ID..."
$AWS_EC2 create-tags --resources "$INSTANCE_ID" --tags Key=Name,Value=e2e-test Key=Environment,Value=testing Key=DeleteMe,Value=please

# 6b: Verify tags with describe-tags (resource-id filter)
echo "Verifying tags on instance..."
TAG_COUNT=$($AWS_EC2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$TAG_COUNT" -ne 3 ]; then
    echo "Expected 3 tags on instance, got $TAG_COUNT"
    exit 1
fi
echo "Instance has $TAG_COUNT tags"

# 6c: Create tags on the root volume
echo "Creating tags on volume $VOLUME_ID..."
$AWS_EC2 create-tags --resources "$VOLUME_ID" --tags Key=Name,Value=e2e-root-vol Key=Environment,Value=testing

# 6d: Filter by key
echo "Testing key filter..."
ENV_TAGS=$($AWS_EC2 describe-tags --filters "Name=key,Values=Environment" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$ENV_TAGS" -ne 2 ]; then
    echo "Expected 2 'Environment' tags across resources, got $ENV_TAGS"
    exit 1
fi
echo "Key filter returned $ENV_TAGS tags"

# 6e: Filter by resource-type
echo "Testing resource-type filter..."
INSTANCE_TAGS=$($AWS_EC2 describe-tags --filters "Name=resource-type,Values=instance" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$INSTANCE_TAGS" -ne 3 ]; then
    echo "Expected 3 instance tags, got $INSTANCE_TAGS"
    exit 1
fi
echo "Resource-type filter returned $INSTANCE_TAGS instance tags"

# 6f: Overwrite a tag value
echo "Overwriting Name tag on instance..."
$AWS_EC2 create-tags --resources "$INSTANCE_ID" --tags Key=Name,Value=e2e-test-updated
UPDATED_NAME=$($AWS_EC2 describe-tags \
    --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Name" \
    --query 'Tags[0].Value' --output text)
if [ "$UPDATED_NAME" != "e2e-test-updated" ]; then
    echo "Tag overwrite failed: expected 'e2e-test-updated', got '$UPDATED_NAME'"
    exit 1
fi
echo "Tag overwrite verified"

# 6g: Delete tag by key (unconditional)
echo "Deleting DeleteMe tag unconditionally..."
$AWS_EC2 delete-tags --resources "$INSTANCE_ID" --tags Key=DeleteMe
REMAINING=$($AWS_EC2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$REMAINING" -ne 2 ]; then
    echo "Expected 2 tags after unconditional delete, got $REMAINING"
    exit 1
fi
echo "Unconditional delete verified ($REMAINING tags remaining)"

# 6h: Delete tag with wrong value (should NOT delete)
echo "Attempting delete with wrong value (should be no-op)..."
$AWS_EC2 delete-tags --resources "$INSTANCE_ID" --tags Key=Environment,Value=production
ENV_STILL=$($AWS_EC2 describe-tags \
    --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Environment" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$ENV_STILL" -ne 1 ]; then
    echo "Value-conditional delete incorrectly removed tag"
    exit 1
fi
echo "Value-conditional mismatch preserved tag"

# 6i: Delete tag with correct value
echo "Deleting Environment tag with correct value..."
$AWS_EC2 delete-tags --resources "$INSTANCE_ID" --tags Key=Environment,Value=testing
ENV_GONE=$($AWS_EC2 describe-tags \
    --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Environment" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$ENV_GONE" -ne 0 ]; then
    echo "Value-conditional delete failed to remove matching tag"
    exit 1
fi
echo "Value-conditional match deleted tag"

# 6j: Verify only Name tag remains on instance
FINAL_COUNT=$($AWS_EC2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$FINAL_COUNT" -ne 1 ]; then
    echo "Expected 1 tag remaining on instance, got $FINAL_COUNT"
    exit 1
fi
echo "Tag management tests passed"

# Phase 7: Instance State Transitions
echo "Phase 7: Instance State Transitions"

# Stop instance (stop-instances) and verify transition to stopped (describe-instances)
echo "Stopping instance..."
$AWS_EC2 stop-instances --instance-ids "$INSTANCE_ID"
COUNT=0
while [ $COUNT -lt 30 ]; do
    STATE=$($AWS_EC2 describe-instances --instance-ids "$INSTANCE_ID" --query 'Reservations[0].Instances[0].State.Name' --output text)
    echo "Instance state: $STATE"
    if [ "$STATE" == "stopped" ]; then
        break
    fi
    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$STATE" != "stopped" ]; then
    echo "Instance failed to reach stopped state"
    exit 1
fi

# Phase 7a: Attach volume to stopped instance (should fail)
echo "Phase 7a: Attach Volume to Stopped Instance (Error Path)"
echo "Creating a volume to test attach-to-stopped..."
STOPPED_VOL_OUTPUT=$($AWS_EC2 create-volume --size 10 --availability-zone ap-southeast-2a)
STOPPED_VOL_ID=$(echo "$STOPPED_VOL_OUTPUT" | jq -r '.VolumeId')
echo "Created volume: $STOPPED_VOL_ID"

echo "Attempting attach to stopped instance (should fail)..."
expect_error "IncorrectInstanceState" $AWS_EC2 attach-volume \
    --volume-id "$STOPPED_VOL_ID" --instance-id "$INSTANCE_ID" --device /dev/sdg
echo "Attach-to-stopped correctly rejected"

# Clean up the test volume
$AWS_EC2 delete-volume --volume-id "$STOPPED_VOL_ID"
echo "Cleaned up test volume $STOPPED_VOL_ID"

# Phase 7b: ModifyInstanceAttribute (change instance type while stopped, verify via SSH)
echo "Phase 7b: ModifyInstanceAttribute"
echo "Instance is stopped — modifying instance type to verify changes take effect on restart"

# Derive an upsized type in the same family: nano → xlarge (4 vCPUs instead of 2)
MODIFY_TYPE="${INSTANCE_TYPE%.nano}.xlarge"
echo "Changing instance type from $INSTANCE_TYPE to $MODIFY_TYPE..."

# Get expected vCPU and memory for the new type
# Note: --instance-types filter may not be supported; use jq to select the correct type
TYPES_JSON=$($AWS_EC2 describe-instance-types)
EXPECTED_VCPUS=$(echo "$TYPES_JSON" | jq -r ".InstanceTypes[] | select(.InstanceType==\"$MODIFY_TYPE\") | .VCpuInfo.DefaultVCpus")
EXPECTED_MEM_MIB=$(echo "$TYPES_JSON" | jq -r ".InstanceTypes[] | select(.InstanceType==\"$MODIFY_TYPE\") | .MemoryInfo.SizeInMiB")
echo "Expected resources after modify: ${EXPECTED_VCPUS} vCPUs, ${EXPECTED_MEM_MIB} MiB RAM"

# Modify the instance type
$AWS_EC2 modify-instance-attribute --instance-id "$INSTANCE_ID" \
    --instance-type "{\"Value\": \"$MODIFY_TYPE\"}"

# Verify describe-instances reflects the new type
MODIFIED_TYPE=$($AWS_EC2 describe-instances --instance-ids "$INSTANCE_ID" \
    --query 'Reservations[0].Instances[0].InstanceType' --output text)
if [ "$MODIFIED_TYPE" != "$MODIFY_TYPE" ]; then
    echo "ModifyInstanceAttribute failed: expected type $MODIFY_TYPE, got $MODIFIED_TYPE"
    exit 1
fi
echo "Instance type updated to $MODIFIED_TYPE"

# Start instance with the new type
echo "Starting instance with modified type..."
$AWS_EC2 start-instances --instance-ids "$INSTANCE_ID"
COUNT=0
while [ $COUNT -lt 30 ]; do
    STATE=$($AWS_EC2 describe-instances --instance-ids "$INSTANCE_ID" \
        --query 'Reservations[0].Instances[0].State.Name' --output text)
    echo "Instance state: $STATE"
    if [ "$STATE" == "running" ]; then
        break
    fi
    sleep 2
    COUNT=$((COUNT + 1))
done

if [ "$STATE" != "running" ]; then
    echo "Instance failed to reach running state after type change"
    exit 1
fi

# Get SSH port (may have changed after restart with new QEMU config)
echo "Getting SSH port for restarted instance..."
SSH_PORT=$(get_ssh_port "$INSTANCE_ID")
SSH_HOST=$(get_ssh_host "$INSTANCE_ID")
echo "SSH endpoint: $SSH_HOST:$SSH_PORT"

# Wait for SSH to become ready
wait_for_ssh "$SSH_HOST" "$SSH_PORT" "test-key-1.pem" 30

# Verify vCPU count matches the new instance type (nproc reports online CPUs)
echo "Verifying vCPU count inside the VM..."
VM_VCPUS=$(ssh -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    -o LogLevel=ERROR \
    -o ConnectTimeout=5 \
    -o BatchMode=yes \
    -p "$SSH_PORT" \
    -i "test-key-1.pem" \
    ec2-user@"$SSH_HOST" 'nproc' | tr -d '[:space:]')
echo "VM reports $VM_VCPUS vCPUs (expected $EXPECTED_VCPUS)"
if [ "$VM_VCPUS" != "$EXPECTED_VCPUS" ]; then
    echo "vCPU count mismatch after ModifyInstanceAttribute: VM reports $VM_VCPUS, expected $EXPECTED_VCPUS"
    exit 1
fi
echo "vCPU count verified"

# Verify memory matches the new instance type (MemTotal from /proc/meminfo)
echo "Verifying memory inside the VM..."
VM_MEM_KB=$(ssh -o StrictHostKeyChecking=no \
    -o UserKnownHostsFile=/dev/null \
    -o LogLevel=ERROR \
    -o ConnectTimeout=5 \
    -o BatchMode=yes \
    -p "$SSH_PORT" \
    -i "test-key-1.pem" \
    ec2-user@"$SSH_HOST" "awk '/MemTotal/ {print \$2}' /proc/meminfo" | tr -d '[:space:]')
VM_MEM_MIB=$((VM_MEM_KB / 1024))
# Allow 15% margin for kernel reserved memory
EXPECTED_MEM_LOW=$((EXPECTED_MEM_MIB * 85 / 100))
echo "VM reports ${VM_MEM_MIB} MiB total RAM (expected ~${EXPECTED_MEM_MIB} MiB, threshold ${EXPECTED_MEM_LOW} MiB)"
if [ "$VM_MEM_MIB" -lt "$EXPECTED_MEM_LOW" ]; then
    echo "Memory too low after ModifyInstanceAttribute: VM reports ${VM_MEM_MIB} MiB, expected at least ${EXPECTED_MEM_LOW} MiB"
    exit 1
fi
echo "Memory verified"

echo "ModifyInstanceAttribute test passed (type change + vCPU + memory verified via SSH)"

# Phase 7c: RunInstances with count > 1
echo "Phase 7c: RunInstances with MinCount/MaxCount > 1"
echo "Launching 2 instances in a single run-instances call..."
MULTI_RUN_OUTPUT=$($AWS_EC2 run-instances \
    --image-id "$AMI_ID" \
    --instance-type "$INSTANCE_TYPE" \
    --key-name test-key-1 \
    --count 2)
MULTI_COUNT=$(echo "$MULTI_RUN_OUTPUT" | jq '.Instances | length')

if [ "$MULTI_COUNT" -ne 2 ]; then
    echo "Expected 2 instances from run-instances --count 2, got $MULTI_COUNT"
    exit 1
fi

MULTI_ID_1=$(echo "$MULTI_RUN_OUTPUT" | jq -r '.Instances[0].InstanceId')
MULTI_ID_2=$(echo "$MULTI_RUN_OUTPUT" | jq -r '.Instances[1].InstanceId')
echo "Launched 2 instances: $MULTI_ID_1, $MULTI_ID_2"

# Wait for both to reach running state
for MID in "$MULTI_ID_1" "$MULTI_ID_2"; do
    echo "Waiting for $MID to reach running state..."
    COUNT=0
    while [ $COUNT -lt 30 ]; do
        MSTATE=$($AWS_EC2 describe-instances --instance-ids "$MID" \
            --query 'Reservations[0].Instances[0].State.Name' --output text) || {
            sleep 2
            COUNT=$((COUNT + 1))
            continue
        }
        if [ "$MSTATE" == "running" ]; then
            echo "Instance $MID is running"
            break
        fi
        sleep 2
        COUNT=$((COUNT + 1))
    done
    if [ "$MSTATE" != "running" ]; then
        echo "Instance $MID failed to reach running state"
        exit 1
    fi
done

# Terminate the multi-launch instances
echo "Terminating multi-launch instances..."
$AWS_EC2 terminate-instances --instance-ids "$MULTI_ID_1" "$MULTI_ID_2"
for MID in "$MULTI_ID_1" "$MULTI_ID_2"; do
    COUNT=0
    while [ $COUNT -lt 30 ]; do
        MSTATE=$($AWS_EC2 describe-instances --instance-ids "$MID" \
            --query 'Reservations[0].Instances[0].State.Name' --output text)
        if [ "$MSTATE" == "terminated" ] || [ "$MSTATE" == "None" ]; then
            break
        fi
        sleep 2
        COUNT=$((COUNT + 1))
    done
done
echo "RunInstances count>1 test passed"

# Phase 8: Negative / Error Path Tests
echo "Phase 8: Negative / Error Path Tests"

# 8a: RunInstances with malformed AMI ID (missing ami- prefix)
echo "8a: RunInstances with malformed AMI ID..."
expect_error "InvalidAMIID.Malformed" $AWS_EC2 run-instances \
    --image-id notanami --instance-type "$INSTANCE_TYPE" --key-name test-key-1

# 8b: RunInstances with invalid instance type
echo "8b: RunInstances with invalid instance type..."
expect_error "InvalidInstanceType" $AWS_EC2 run-instances \
    --image-id "$AMI_ID" --instance-type "x99.superlarge" --key-name test-key-1

# 8c: Attach an already in-use volume (root volume is attached to running instance)
echo "8c: Attach already in-use volume..."
expect_error "VolumeInUse" $AWS_EC2 attach-volume \
    --volume-id "$VOLUME_ID" --instance-id "$INSTANCE_ID" --device /dev/sdg

# 8d: Detach boot/root volume (should be rejected)
echo "8d: Detach boot volume..."
expect_error "OperationNotPermitted" $AWS_EC2 detach-volume \
    --volume-id "$VOLUME_ID" --instance-id "$INSTANCE_ID"

# 8e: Delete a non-existent snapshot
echo "8e: Delete non-existent snapshot..."
expect_error "InvalidSnapshot.NotFound" $AWS_EC2 delete-snapshot \
    --snapshot-id snap-nonexistent000000

# 8f: Call an unsupported Action (use raw curl to send an invalid Action)
echo "8f: Unsupported Action..."
set +e
UNSUPPORTED_OUTPUT=$(curl -s -k -X POST https://localhost:9999/ \
    -H "Content-Type: application/x-www-form-urlencoded" \
    -d "Action=DescribeFakeThings&Version=2016-11-15" 2>&1)
set -e
if echo "$UNSUPPORTED_OUTPUT" | grep -q "InvalidAction\|UnknownAction\|Error"; then
    echo "  Got expected error for unsupported action"
else
    echo "  WARNING: Unsupported action did not return expected error (may need auth)"
fi

# 8g: RunInstances with non-existent AMI (valid format but doesn't exist)
echo "8g: RunInstances with non-existent AMI..."
expect_error "InvalidAMIID.NotFound" $AWS_EC2 run-instances \
    --image-id ami-0000000000000dead --instance-type "$INSTANCE_TYPE" --key-name test-key-1

# 8h: RunInstances with non-existent key pair
echo "8h: RunInstances with non-existent key pair..."
expect_error "InvalidKeyPair.NotFound" $AWS_EC2 run-instances \
    --image-id "$AMI_ID" --instance-type "$INSTANCE_TYPE" --key-name nonexistent-key-xyz

# 8i: DeleteVolume on non-existent volume
echo "8i: DeleteVolume non-existent volume..."
expect_error "InvalidVolume.NotFound" $AWS_EC2 delete-volume \
    --volume-id vol-0000000000000dead

# 8j: CreateKeyPair with duplicate name (test-key-1 exists from Phase 3)
echo "8j: CreateKeyPair duplicate name..."
expect_error "InvalidKeyPair.Duplicate" $AWS_EC2 create-key-pair \
    --key-name test-key-1

# 8k: ImportKeyPair with duplicate name (test-key-1 exists from Phase 3)
echo "8k: ImportKeyPair duplicate name..."
expect_error "InvalidKeyPair.Duplicate" $AWS_EC2 import-key-pair \
    --key-name test-key-1 --public-key-material "fileb://test-key-2-local.pub"

# 8l: ImportKeyPair with invalid key format
echo "8l: ImportKeyPair invalid key format..."
echo "not-a-valid-public-key" > /tmp/bad-key.pub
expect_error "InvalidKey.Format" $AWS_EC2 import-key-pair \
    --key-name bad-format-key --public-key-material "fileb:///tmp/bad-key.pub"

# 8m: DescribeVolumes with non-existent volume ID
echo "8m: DescribeVolumes non-existent volume..."
expect_error "InvalidVolume.NotFound" $AWS_EC2 describe-volumes \
    --volume-ids vol-0000000000000dead

# 8n: DescribeImages with non-existent AMI ID
echo "8n: DescribeImages non-existent AMI..."
expect_error "InvalidAMIID.NotFound" $AWS_EC2 describe-images \
    --image-ids ami-0000000000000dead

# 8o: CreateImage with duplicate name (e2e-custom-ami exists from Phase 5e)
echo "8o: CreateImage duplicate name..."
expect_error "InvalidAMIName.Duplicate" $AWS_EC2 create-image \
    --instance-id "$INSTANCE_ID" --name "e2e-custom-ami"

# 8p: DeleteKeyPair for non-existent key — should succeed (idempotent, matches AWS)
echo "8p: DeleteKeyPair non-existent key (idempotent)..."
$AWS_EC2 delete-key-pair --key-name nonexistent-key-99999
echo "  DeleteKeyPair for non-existent key succeeded (idempotent)"

# 8q: ModifyInstanceAttribute on running instance (instance not in stopped KV → NotFound)
echo "8q: ModifyInstanceAttribute on running instance..."
expect_error "InvalidInstanceID.NotFound" $AWS_EC2 modify-instance-attribute \
    --instance-id "$INSTANCE_ID" --instance-type "{\"Value\": \"$INSTANCE_TYPE\"}"

echo "Negative test suite passed"

# Phase 9: Terminate and Verify Cleanup
echo "Phase 9: Terminate and Verify Cleanup"

# Save root volume ID before termination for cleanup verification
ROOT_VOLUME_ID="$VOLUME_ID"
echo "Root volume to verify cleanup: $ROOT_VOLUME_ID"

# Clean up the CreateImage backing snapshot so DeleteOnTermination can delete the root volume.
# checkVolumeHasNoSnapshots() correctly blocks volume deletion when snapshots reference it.
if [ -n "$CUSTOM_AMI_SNAP_ID" ]; then
    echo "Deleting CreateImage backing snapshot $CUSTOM_AMI_SNAP_ID before termination..."
    $AWS_EC2 delete-snapshot --snapshot-id "$CUSTOM_AMI_SNAP_ID"
    echo "CreateImage snapshot deleted"
fi

# Terminate instance (terminate-instances) and verify termination (describe-instances)
echo "Terminating instance..."
$AWS_EC2 terminate-instances --instance-ids "$INSTANCE_ID"
COUNT=0
while [ $COUNT -lt 30 ]; do
    STATE=$($AWS_EC2 describe-instances --instance-ids "$INSTANCE_ID" --query 'Reservations[0].Instances[0].State.Name' --output text)
    echo "Instance state: $STATE"
    if [ "$STATE" == "terminated" ] || [ "$STATE" == "None" ]; then
        break
    fi
    sleep 2
    COUNT=$((COUNT + 1))
done

# Phase 9a: Verify SSH unreachable after termination
echo "Phase 9a: SSH Unreachable Verification"
verify_ssh_unreachable "$SSH_HOST" "$SSH_PORT" "test-key-1.pem"
echo "SSH unreachable verification passed"

# Phase 9b: Verify root volume cleanup after termination
echo "Phase 9b: Volume Cleanup Verification"
echo "Verifying root volume $ROOT_VOLUME_ID is cleaned up after termination..."
sleep 5  # Allow time for async volume deletion

COUNT=0
VOLUME_CLEANED=false
while [ $COUNT -lt 20 ]; do
    set +e
    VOL_CHECK=$($AWS_EC2 describe-volumes --volume-ids "$ROOT_VOLUME_ID" \
        --query 'Volumes[0].State' --output text 2>&1)
    VOL_EXIT=$?
    set -e

    if [ $VOL_EXIT -ne 0 ] || [ "$VOL_CHECK" == "None" ] || [ -z "$VOL_CHECK" ]; then
        VOLUME_CLEANED=true
        echo "Root volume $ROOT_VOLUME_ID has been cleaned up (DeleteOnTermination)"
        break
    fi

    echo "Volume still exists (State=$VOL_CHECK), waiting... ($COUNT/20)"
    sleep 3
    COUNT=$((COUNT + 1))
done

if [ "$VOLUME_CLEANED" != "true" ]; then
    echo "WARNING: Root volume $ROOT_VOLUME_ID was not cleaned up after termination"
    echo "This may indicate a DeleteOnTermination regression"
    exit 1
fi

echo "Volume cleanup verification passed"

echo "E2E Test Completed Successfully"
exit 0
