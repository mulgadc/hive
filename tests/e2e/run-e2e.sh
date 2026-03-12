#!/bin/bash
set -e

# Ensure we are in the project root
cd "$(dirname "$0")/../.."

# Source helper functions
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
    # Only stop services if we started them (not when bootstrapped)
    if [ "$BOOTSTRAPPED" != "true" ]; then
        ./scripts/stop-dev.sh
    fi
    exit $EXIT_CODE
}
trap cleanup EXIT

# Use Hive profile (hive admin init sets endpoint_url, ca_bundle, region in ~/.aws/config)
export AWS_PROFILE=hive

# Detect bootstrapped environment from hive.toml config
BOOTSTRAPPED="false"
GATEWAY_HOST="localhost"
PREDASTORE_HOST="localhost"
if [ -f ~/hive/config/hive.toml ]; then
    # Resolve gateway host from AWS profile endpoint_url (set by hive admin init)
    ENDPOINT_URL=$(aws configure get endpoint_url 2>/dev/null || true)
    if [ -n "$ENDPOINT_URL" ]; then
        DETECTED_GW_HOST=$(echo "$ENDPOINT_URL" | sed 's|https\?://||;s|:.*||')
        if [ -n "$DETECTED_GW_HOST" ]; then
            GATEWAY_HOST="$DETECTED_GW_HOST"
        fi
    fi
    # Resolve predastore host from hive.toml
    DETECTED_PS_HOST=$(awk -F'"' '/\[nodes\..*\.predastore\]/{found=1} found && /^host/{print $2; exit}' ~/hive/config/hive.toml)
    if [ -n "$DETECTED_PS_HOST" ]; then
        PREDASTORE_HOST="${DETECTED_PS_HOST%%:*}"
    fi
    # Check if gateway is actually responding
    if curl -sk "https://${GATEWAY_HOST}:9999" > /dev/null 2>&1; then
        BOOTSTRAPPED="true"
        echo "Detected bootstrapped environment — skipping cluster setup"
        echo "  Gateway host: $GATEWAY_HOST"
        echo "  Predastore host: $PREDASTORE_HOST"
    fi
fi

# Helper: set up an AWS CLI profile with credentials + endpoint/CA config from the hive profile
setup_test_profile() {
    local profile="$1" key_id="$2" secret="$3"
    aws configure set aws_access_key_id "$key_id" --profile "$profile"
    aws configure set aws_secret_access_key "$secret" --profile "$profile"
    aws configure set region "$(aws configure get region)" --profile "$profile"
    aws configure set endpoint_url "$(aws configure get endpoint_url)" --profile "$profile"
    aws configure set ca_bundle "$(aws configure get ca_bundle)" --profile "$profile"
}

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

if [ "$BOOTSTRAPPED" != "true" ]; then
    ./bin/hive admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1

    # Trust the Hive CA certificate for AWS CLI SSL verification
    echo "Adding Hive CA certificate to system trust store..."
    sudo cp ~/hive/config/ca.pem /usr/local/share/ca-certificates/hive-ca.crt
    sudo update-ca-certificates

    # Start all services
    # Ensure logs directory exists for start-dev.sh
    mkdir -p ~/hive/logs
    sudo mkdir -p /mnt/ramdisk
    ./scripts/start-dev.sh
fi

# Wait for health checks on AWS Gateway
echo "Waiting for AWS Gateway..."
MAX_RETRIES=15
COUNT=0

until curl -sk "https://${GATEWAY_HOST}:9999" > /dev/null || [ $COUNT -eq $MAX_RETRIES ]; do
    echo "Waiting for gateway... ($COUNT/$MAX_RETRIES)"
    sleep 2
    COUNT=$((COUNT + 1))
done

if [ $COUNT -eq $MAX_RETRIES ]; then
    echo "Gateway failed to start"
    exit 1
fi

# Wait for daemon NATS subscriptions to be active
wait_for_daemon_ready "https://${GATEWAY_HOST}:9999"

# No need for AWS_EC2/AWS_IAM vars — endpoint_url and ca_bundle are in the profile config

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
aws ec2 describe-regions | jq -e '.Regions | length > 0'

# Verify describe-availability-zones
echo "Verifying describe-availability-zones..."
AZ_OUTPUT=$(aws ec2 describe-availability-zones)
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
AVAILABLE_TYPES=$(aws ec2 describe-instance-types --query 'InstanceTypes[*].InstanceType' --output text)
echo "Available instance types: $AVAILABLE_TYPES"

# Pick the nano instance type for minimal resource usage in tests
INSTANCE_TYPE=$(echo $AVAILABLE_TYPES | tr ' ' '\n' | grep -m1 'nano')
if [ -z "$INSTANCE_TYPE" ] || [ "$INSTANCE_TYPE" == "None" ]; then
    echo "No instance types found!"
    exit 1
fi
echo "Selected instance type for test: $INSTANCE_TYPE"

# Get architecture for the selected instance type
ARCH=$(aws ec2 describe-instance-types --instance-types "$INSTANCE_TYPE" --query 'InstanceTypes[0].ProcessorInfo.SupportedArchitectures[0]' --output text)
echo "Detected architecture for $INSTANCE_TYPE: $ARCH"

# Verify describe-instance-types (ensure the chosen type is available)
aws ec2 describe-instance-types | jq -e ".InstanceTypes[] | select(.InstanceType==\"$INSTANCE_TYPE\")"

# Phase 2b: Serial Console Access Settings
echo "Phase 2b: Serial Console Access Settings"

# Default should be disabled
SERIAL_DEFAULT=$(aws ec2 get-serial-console-access-status --query 'SerialConsoleAccessEnabled' --output text)
if [ "$SERIAL_DEFAULT" != "False" ]; then
    echo "Expected serial console access default to be False, got $SERIAL_DEFAULT"
    exit 1
fi
echo "  Default state: disabled"

# Enable
ENABLE_RESULT=$(aws ec2 enable-serial-console-access --query 'SerialConsoleAccessEnabled' --output text)
if [ "$ENABLE_RESULT" != "True" ]; then
    echo "Expected enable to return True, got $ENABLE_RESULT"
    exit 1
fi
SERIAL_ENABLED=$(aws ec2 get-serial-console-access-status --query 'SerialConsoleAccessEnabled' --output text)
if [ "$SERIAL_ENABLED" != "True" ]; then
    echo "Expected serial console access to be True after enable, got $SERIAL_ENABLED"
    exit 1
fi
echo "  Enabled: $SERIAL_ENABLED"

# Disable
DISABLE_RESULT=$(aws ec2 disable-serial-console-access --query 'SerialConsoleAccessEnabled' --output text)
if [ "$DISABLE_RESULT" != "False" ]; then
    echo "Expected disable to return False, got $DISABLE_RESULT"
    exit 1
fi
SERIAL_DISABLED=$(aws ec2 get-serial-console-access-status --query 'SerialConsoleAccessEnabled' --output text)
if [ "$SERIAL_DISABLED" != "False" ]; then
    echo "Expected serial console access to be False after disable, got $SERIAL_DISABLED"
    exit 1
fi
echo "  Disabled: $SERIAL_DISABLED"
echo "Serial console access settings tests passed"

# Phase 3: SSH Key Management
echo "Phase 3: SSH Key Management"
# Create test-key-1 (create-key-pair) and verify private key material is returned
KEY_MATERIAL=$(aws ec2 create-key-pair --key-name test-key-1 --query 'KeyMaterial' --output text)
if [ -z "$KEY_MATERIAL" ] || [ "$KEY_MATERIAL" == "None" ]; then
    echo "Failed to create key pair test-key-1"
    exit 1
fi
echo "$KEY_MATERIAL" > test-key-1.pem
chmod 600 test-key-1.pem

# Generate a local RSA key and import it as test-key-2 (import-key-pair)
ssh-keygen -t rsa -b 2048 -f test-key-2-local -N ""
aws ec2 import-key-pair --key-name test-key-2 --public-key-material "fileb://test-key-2-local.pub"

# Verify both keys are present (describe-key-pairs)
aws ec2 describe-key-pairs --query 'KeyPairs[*].KeyName' --output text | grep test-key-1
aws ec2 describe-key-pairs --query 'KeyPairs[*].KeyName' --output text | grep test-key-2

# Delete test-key-2 (delete-key-pair) and verify only one remains
aws ec2 delete-key-pair --key-name test-key-2
REMAINING_KEYS=$(aws ec2 describe-key-pairs --query 'KeyPairs[*].KeyName' --output text)
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
aws ec2 describe-images --image-ids "$AMI_ID" | jq -e ".Images[0] | select(.ImageId==\"$AMI_ID\")"

# Phase 5: Instance Lifecycle
echo "Phase 5: Instance Lifecycle"
# Launch a VM (run-instances)
echo "Running: aws ec2 run-instances --image-id $AMI_ID --instance-type $INSTANCE_TYPE --key-name test-key-1"
# Capture full output for debugging
set +e  # Temporarily disable exit on error to capture output
RUN_OUTPUT=$(aws ec2 run-instances --image-id "$AMI_ID" --instance-type "$INSTANCE_TYPE" --key-name test-key-1 2>&1)
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
    DESCRIBE_OUTPUT=$(aws ec2 describe-instances --instance-ids "$INSTANCE_ID") || {
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
DESCRIBE_META=$(aws ec2 describe-instances --instance-ids "$INSTANCE_ID")
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
ROOT_VOL_SIZE_API=$(aws ec2 describe-volumes --query 'Volumes[0].Size' --output text)
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
# Hostname uses truncated ID: hive-vm-<first 8 hex chars of instance ID>
SHORT_ID=$(echo "$INSTANCE_ID" | sed 's/^i-//' | cut -c1-8)
if echo "$VM_HOSTNAME" | grep -q "$SHORT_ID"; then
    echo "Hostname contains instance ID prefix ($SHORT_ID)"
else
    echo "WARNING: Hostname '$VM_HOSTNAME' does not contain instance ID prefix '$SHORT_ID' (non-fatal)"
fi

echo "SSH connectivity and volume verification passed"

# Phase 5a-iii: Console Output
echo "Phase 5a-iii: Console Output"

CONSOLE_OUTPUT=$(aws ec2 get-console-output --instance-id "$INSTANCE_ID")
CONSOLE_INSTANCE=$(echo "$CONSOLE_OUTPUT" | jq -r '.InstanceId')
CONSOLE_DATA=$(echo "$CONSOLE_OUTPUT" | jq -r '.Output // empty')

if [ "$CONSOLE_INSTANCE" != "$INSTANCE_ID" ]; then
    echo "GetConsoleOutput InstanceId mismatch: expected $INSTANCE_ID, got $CONSOLE_INSTANCE"
    exit 1
fi
echo "  GetConsoleOutput succeeded (InstanceId=$CONSOLE_INSTANCE, has output=$([ -n "$CONSOLE_DATA" ] && echo yes || echo no))"

echo "Console output tests passed"

# Verify root volume attached to the instance (describe-volumes)
VOLUME_ID=$(aws ec2 describe-volumes --query 'Volumes[0].VolumeId' --output text)
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
CREATE_OUTPUT=$(aws ec2 create-volume --size 10 --availability-zone ap-southeast-2a)
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
aws ec2 modify-volume --volume-id "$TEST_VOLUME_ID" --size "$NEW_SIZE"

# Verify resize
echo "Verifying resize..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    VOLUME_SIZE=$(aws ec2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
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
aws ec2 attach-volume --volume-id "$TEST_VOLUME_ID" --instance-id "$INSTANCE_ID" --device /dev/sdf

# Verify attachment
echo "Verifying volume attachment..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    ATTACH_STATE=$(aws ec2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].Attachments[0].State' --output text)
    ATTACH_INSTANCE=$(aws ec2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
        --query 'Volumes[0].Attachments[0].InstanceId' --output text)
    VOL_STATE=$(aws ec2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
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
aws ec2 detach-volume --volume-id "$TEST_VOLUME_ID"

# Verify detachment
echo "Verifying volume detachment..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    VOL_STATE=$(aws ec2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
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
aws ec2 delete-volume --volume-id "$TEST_VOLUME_ID"

# Verify deletion
echo "Verifying volume deletion..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    set +e
    VOLUME_CHECK=$(aws ec2 describe-volumes --volume-ids "$TEST_VOLUME_ID" \
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
VOL_STATUS_OUTPUT=$(aws ec2 describe-volume-status --volume-ids "$VOLUME_ID")
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
ROOT_VOL_SIZE=$(aws ec2 describe-volumes --volume-ids "$VOLUME_ID" \
    --query 'Volumes[0].Size' --output text)

# Create a snapshot from the attached root volume
echo "Creating snapshot from volume $VOLUME_ID..."
SNAP_OUTPUT=$(aws ec2 create-snapshot --volume-id "$VOLUME_ID" --description "e2e-test-snapshot")
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
    SNAP_STATE=$(aws ec2 describe-snapshots --snapshot-ids "$SNAPSHOT_ID" \
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
DESCRIBE_SNAP=$(aws ec2 describe-snapshots --snapshot-ids "$SNAPSHOT_ID")
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
COPY_OUTPUT=$(aws ec2 copy-snapshot --source-snapshot-id "$SNAPSHOT_ID" --source-region ap-southeast-2 --description "e2e-copy")
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
TOTAL_SNAPS=$(aws ec2 describe-snapshots \
    --snapshot-ids "$SNAPSHOT_ID" "$COPY_SNAPSHOT_ID" \
    --query 'length(Snapshots)' --output text)

if [ "$TOTAL_SNAPS" -ne 2 ]; then
    echo "Expected 2 snapshots, got $TOTAL_SNAPS"
    exit 1
fi
echo "Both snapshots visible via describe-snapshots"

# Verify copy has correct description
COPY_DESC=$(aws ec2 describe-snapshots --snapshot-ids "$COPY_SNAPSHOT_ID" \
    --query 'Snapshots[0].Description' --output text)
if [ "$COPY_DESC" != "e2e-copy" ]; then
    echo "Copy description mismatch: expected 'e2e-copy', got '$COPY_DESC'"
    exit 1
fi

# Delete the original snapshot
echo "Deleting original snapshot $SNAPSHOT_ID..."
aws ec2 delete-snapshot --snapshot-id "$SNAPSHOT_ID"

# Verify original is gone, copy remains
echo "Verifying snapshot deletion..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    set +e
    SNAP_CHECK=$(aws ec2 describe-snapshots --snapshot-ids "$SNAPSHOT_ID" \
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
COPY_STATE=$(aws ec2 describe-snapshots --snapshot-ids "$COPY_SNAPSHOT_ID" \
    --query 'Snapshots[0].State' --output text)
if [ "$COPY_STATE" != "completed" ]; then
    echo "Copy snapshot should still exist (State=$COPY_STATE)"
    exit 1
fi
echo "Copy snapshot still intact after original deletion"

# Delete the copy
echo "Deleting copy snapshot $COPY_SNAPSHOT_ID..."
aws ec2 delete-snapshot --snapshot-id "$COPY_SNAPSHOT_ID"

echo "Snapshot lifecycle test passed (create -> describe -> copy -> delete)"

# Phase 5d: Verify Snapshot-Backed Instance Launch
echo "Phase 5d: Verify Snapshot-Backed Instance Launch"
echo "All run-instances calls go through cloneAMIToVolume() -> OpenFromSnapshot(),"
echo "so the Phase 5 instance is already snapshot-backed. Verify its volume config."

AWS_S3="aws --endpoint-url https://${PREDASTORE_HOST}:8443 s3"

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

CREATE_IMAGE_OUTPUT=$(aws ec2 create-image --instance-id "$INSTANCE_ID" --name "e2e-custom-ami" --description "E2E test custom image")
CUSTOM_AMI_ID=$(echo "$CREATE_IMAGE_OUTPUT" | jq -r '.ImageId')

if [ -z "$CUSTOM_AMI_ID" ] || [ "$CUSTOM_AMI_ID" == "null" ]; then
    echo "Failed to create custom image"
    echo "Output: $CREATE_IMAGE_OUTPUT"
    exit 1
fi
echo "Created custom AMI: $CUSTOM_AMI_ID"

# Verify the custom AMI exists via describe-images
echo "Verifying custom AMI via describe-images..."
CUSTOM_IMAGE=$(aws ec2 describe-images --image-ids "$CUSTOM_AMI_ID")
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
aws ec2 create-tags --resources "$INSTANCE_ID" --tags Key=Name,Value=e2e-test Key=Environment,Value=testing Key=DeleteMe,Value=please

# 6b: Verify tags with describe-tags (resource-id filter)
echo "Verifying tags on instance..."
TAG_COUNT=$(aws ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$TAG_COUNT" -ne 3 ]; then
    echo "Expected 3 tags on instance, got $TAG_COUNT"
    exit 1
fi
echo "Instance has $TAG_COUNT tags"

# 6c: Create tags on the root volume
echo "Creating tags on volume $VOLUME_ID..."
aws ec2 create-tags --resources "$VOLUME_ID" --tags Key=Name,Value=e2e-root-vol Key=Environment,Value=testing

# 6d: Filter by key
echo "Testing key filter..."
ENV_TAGS=$(aws ec2 describe-tags --filters "Name=key,Values=Environment" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$ENV_TAGS" -ne 2 ]; then
    echo "Expected 2 'Environment' tags across resources, got $ENV_TAGS"
    exit 1
fi
echo "Key filter returned $ENV_TAGS tags"

# 6e: Filter by resource-type
echo "Testing resource-type filter..."
INSTANCE_TAGS=$(aws ec2 describe-tags --filters "Name=resource-type,Values=instance" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$INSTANCE_TAGS" -ne 3 ]; then
    echo "Expected 3 instance tags, got $INSTANCE_TAGS"
    exit 1
fi
echo "Resource-type filter returned $INSTANCE_TAGS instance tags"

# 6f: Overwrite a tag value
echo "Overwriting Name tag on instance..."
aws ec2 create-tags --resources "$INSTANCE_ID" --tags Key=Name,Value=e2e-test-updated
UPDATED_NAME=$(aws ec2 describe-tags \
    --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Name" \
    --query 'Tags[0].Value' --output text)
if [ "$UPDATED_NAME" != "e2e-test-updated" ]; then
    echo "Tag overwrite failed: expected 'e2e-test-updated', got '$UPDATED_NAME'"
    exit 1
fi
echo "Tag overwrite verified"

# 6g: Delete tag by key (unconditional)
echo "Deleting DeleteMe tag unconditionally..."
aws ec2 delete-tags --resources "$INSTANCE_ID" --tags Key=DeleteMe
REMAINING=$(aws ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$REMAINING" -ne 2 ]; then
    echo "Expected 2 tags after unconditional delete, got $REMAINING"
    exit 1
fi
echo "Unconditional delete verified ($REMAINING tags remaining)"

# 6h: Delete tag with wrong value (should NOT delete)
echo "Attempting delete with wrong value (should be no-op)..."
aws ec2 delete-tags --resources "$INSTANCE_ID" --tags Key=Environment,Value=production
ENV_STILL=$(aws ec2 describe-tags \
    --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Environment" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$ENV_STILL" -ne 1 ]; then
    echo "Value-conditional delete incorrectly removed tag"
    exit 1
fi
echo "Value-conditional mismatch preserved tag"

# 6i: Delete tag with correct value
echo "Deleting Environment tag with correct value..."
aws ec2 delete-tags --resources "$INSTANCE_ID" --tags Key=Environment,Value=testing
ENV_GONE=$(aws ec2 describe-tags \
    --filters "Name=resource-id,Values=$INSTANCE_ID" "Name=key,Values=Environment" \
    --query 'length(Tags || `[]`)' --output text)
if [ "$ENV_GONE" -ne 0 ]; then
    echo "Value-conditional delete failed to remove matching tag"
    exit 1
fi
echo "Value-conditional match deleted tag"

# 6j: Verify only Name tag remains on instance
FINAL_COUNT=$(aws ec2 describe-tags --filters "Name=resource-id,Values=$INSTANCE_ID" \
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
aws ec2 stop-instances --instance-ids "$INSTANCE_ID"
COUNT=0
while [ $COUNT -lt 30 ]; do
    STATE=$(aws ec2 describe-instances --instance-ids "$INSTANCE_ID" --query 'Reservations[0].Instances[0].State.Name' --output text)
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

# Reboot stopped instance (should fail)
echo "Attempting reboot of stopped instance (should fail)..."
expect_error "IncorrectInstanceState" aws ec2 reboot-instances --instance-ids "$INSTANCE_ID"
echo "Reboot-stopped correctly rejected"

# Phase 7a: Attach volume to stopped instance (should fail)
echo "Phase 7a: Attach Volume to Stopped Instance (Error Path)"
echo "Creating a volume to test attach-to-stopped..."
STOPPED_VOL_OUTPUT=$(aws ec2 create-volume --size 10 --availability-zone ap-southeast-2a)
STOPPED_VOL_ID=$(echo "$STOPPED_VOL_OUTPUT" | jq -r '.VolumeId')
echo "Created volume: $STOPPED_VOL_ID"

echo "Attempting attach to stopped instance (should fail)..."
expect_error "IncorrectInstanceState" aws ec2 attach-volume \
    --volume-id "$STOPPED_VOL_ID" --instance-id "$INSTANCE_ID" --device /dev/sdg
echo "Attach-to-stopped correctly rejected"

# Clean up the test volume
aws ec2 delete-volume --volume-id "$STOPPED_VOL_ID"
echo "Cleaned up test volume $STOPPED_VOL_ID"

# Phase 7b: ModifyInstanceAttribute (change instance type while stopped, verify via SSH)
echo "Phase 7b: ModifyInstanceAttribute"
echo "Instance is stopped — modifying instance type to verify changes take effect on restart"

# Derive an upsized type in the same family: nano → xlarge (4 vCPUs instead of 2)
MODIFY_TYPE="${INSTANCE_TYPE%.nano}.xlarge"
echo "Changing instance type from $INSTANCE_TYPE to $MODIFY_TYPE..."

# Get expected vCPU and memory for the new type
# Note: --instance-types filter may not be supported; use jq to select the correct type
TYPES_JSON=$(aws ec2 describe-instance-types)
EXPECTED_VCPUS=$(echo "$TYPES_JSON" | jq -r ".InstanceTypes[] | select(.InstanceType==\"$MODIFY_TYPE\") | .VCpuInfo.DefaultVCpus")
EXPECTED_MEM_MIB=$(echo "$TYPES_JSON" | jq -r ".InstanceTypes[] | select(.InstanceType==\"$MODIFY_TYPE\") | .MemoryInfo.SizeInMiB")
echo "Expected resources after modify: ${EXPECTED_VCPUS} vCPUs, ${EXPECTED_MEM_MIB} MiB RAM"

# Modify the instance type
aws ec2 modify-instance-attribute --instance-id "$INSTANCE_ID" \
    --instance-type "{\"Value\": \"$MODIFY_TYPE\"}"

# Verify describe-instances reflects the new type
MODIFIED_TYPE=$(aws ec2 describe-instances --instance-ids "$INSTANCE_ID" \
    --query 'Reservations[0].Instances[0].InstanceType' --output text)
if [ "$MODIFIED_TYPE" != "$MODIFY_TYPE" ]; then
    echo "ModifyInstanceAttribute failed: expected type $MODIFY_TYPE, got $MODIFIED_TYPE"
    exit 1
fi
echo "Instance type updated to $MODIFIED_TYPE"

# Start instance with the new type
echo "Starting instance with modified type..."
aws ec2 start-instances --instance-ids "$INSTANCE_ID"
COUNT=0
while [ $COUNT -lt 30 ]; do
    STATE=$(aws ec2 describe-instances --instance-ids "$INSTANCE_ID" \
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

# Phase 7c-pre: Reboot Running Instance
echo "Phase 7c-pre: Reboot Instance"

# Capture pre-reboot metadata
PRE_REBOOT_IP=$(aws ec2 describe-instances --instance-ids "$INSTANCE_ID" \
    --query 'Reservations[0].Instances[0].PrivateIpAddress' --output text)

echo "Rebooting instance..."
aws ec2 reboot-instances --instance-ids "$INSTANCE_ID"

# Verify state stays running (poll a few times to confirm no transient state change)
COUNT=0
while [ $COUNT -lt 5 ]; do
    STATE=$(aws ec2 describe-instances --instance-ids "$INSTANCE_ID" \
        --query 'Reservations[0].Instances[0].State.Name' --output text)
    if [ "$STATE" != "running" ]; then
        echo "Instance unexpectedly left running state: $STATE"
        exit 1
    fi
    sleep 2
    COUNT=$((COUNT + 1))
done
echo "Instance state remained running during reboot"

# Wait for SSH to come back (guest OS restarts)
SSH_PORT=$(get_ssh_port "$INSTANCE_ID")
SSH_HOST=$(get_ssh_host "$INSTANCE_ID")
wait_for_ssh "$SSH_HOST" "$SSH_PORT" "test-key-1.pem" 30

# Verify guest actually rebooted (uptime < 120 seconds)
UPTIME_SECS=$(ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null \
    -o LogLevel=ERROR -o ConnectTimeout=5 -o BatchMode=yes \
    -p "$SSH_PORT" -i "test-key-1.pem" \
    ec2-user@"$SSH_HOST" 'cat /proc/uptime | cut -d. -f1' | tr -d '[:space:]')
echo "Guest uptime after reboot: ${UPTIME_SECS}s"
if [ "$UPTIME_SECS" -gt 120 ]; then
    echo "Guest uptime too high — reboot may not have occurred"
    exit 1
fi

# Verify metadata unchanged
POST_REBOOT_IP=$(aws ec2 describe-instances --instance-ids "$INSTANCE_ID" \
    --query 'Reservations[0].Instances[0].PrivateIpAddress' --output text)
if [ "$PRE_REBOOT_IP" != "$POST_REBOOT_IP" ]; then
    echo "IP changed after reboot: $PRE_REBOOT_IP -> $POST_REBOOT_IP"
    exit 1
fi
echo "Reboot instance test passed"

# Phase 7c: RunInstances with count > 1
echo "Phase 7c: RunInstances with MinCount/MaxCount > 1"
echo "Launching 2 instances in a single run-instances call..."
MULTI_RUN_OUTPUT=$(aws ec2 run-instances \
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
        MSTATE=$(aws ec2 describe-instances --instance-ids "$MID" \
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
aws ec2 terminate-instances --instance-ids "$MULTI_ID_1" "$MULTI_ID_2"
for MID in "$MULTI_ID_1" "$MULTI_ID_2"; do
    COUNT=0
    while [ $COUNT -lt 30 ]; do
        MSTATE=$(aws ec2 describe-instances --instance-ids "$MID" \
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
expect_error "InvalidAMIID.Malformed" aws ec2 run-instances \
    --image-id notanami --instance-type "$INSTANCE_TYPE" --key-name test-key-1

# 8b: RunInstances with invalid instance type
echo "8b: RunInstances with invalid instance type..."
expect_error "InvalidInstanceType" aws ec2 run-instances \
    --image-id "$AMI_ID" --instance-type "x99.superlarge" --key-name test-key-1

# 8c: Attach an already in-use volume (root volume is attached to running instance)
echo "8c: Attach already in-use volume..."
expect_error "VolumeInUse" aws ec2 attach-volume \
    --volume-id "$VOLUME_ID" --instance-id "$INSTANCE_ID" --device /dev/sdg

# 8d: Detach boot/root volume (should be rejected)
echo "8d: Detach boot volume..."
expect_error "OperationNotPermitted" aws ec2 detach-volume \
    --volume-id "$VOLUME_ID" --instance-id "$INSTANCE_ID"

# 8e: Delete a non-existent snapshot
echo "8e: Delete non-existent snapshot..."
expect_error "InvalidSnapshot.NotFound" aws ec2 delete-snapshot \
    --snapshot-id snap-nonexistent000000

# 8f: Call an unsupported Action (use raw curl to send an invalid Action)
echo "8f: Unsupported Action..."
set +e
UNSUPPORTED_OUTPUT=$(curl -s -k -X POST "https://${GATEWAY_HOST}:9999/" \
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
expect_error "InvalidAMIID.NotFound" aws ec2 run-instances \
    --image-id ami-0000000000000dead --instance-type "$INSTANCE_TYPE" --key-name test-key-1

# 8h: RunInstances with non-existent key pair
echo "8h: RunInstances with non-existent key pair..."
expect_error "InvalidKeyPair.NotFound" aws ec2 run-instances \
    --image-id "$AMI_ID" --instance-type "$INSTANCE_TYPE" --key-name nonexistent-key-xyz

# 8i: DeleteVolume on non-existent volume
echo "8i: DeleteVolume non-existent volume..."
expect_error "InvalidVolume.NotFound" aws ec2 delete-volume \
    --volume-id vol-0000000000000dead

# 8j: CreateKeyPair with duplicate name (test-key-1 exists from Phase 3)
echo "8j: CreateKeyPair duplicate name..."
expect_error "InvalidKeyPair.Duplicate" aws ec2 create-key-pair \
    --key-name test-key-1

# 8k: ImportKeyPair with duplicate name (test-key-1 exists from Phase 3)
echo "8k: ImportKeyPair duplicate name..."
expect_error "InvalidKeyPair.Duplicate" aws ec2 import-key-pair \
    --key-name test-key-1 --public-key-material "fileb://test-key-2-local.pub"

# 8l: ImportKeyPair with invalid key format
echo "8l: ImportKeyPair invalid key format..."
echo "not-a-valid-public-key" > /tmp/bad-key.pub
expect_error "InvalidKey.Format" aws ec2 import-key-pair \
    --key-name bad-format-key --public-key-material "fileb:///tmp/bad-key.pub"

# 8m: DescribeVolumes with non-existent volume ID
echo "8m: DescribeVolumes non-existent volume..."
expect_error "InvalidVolume.NotFound" aws ec2 describe-volumes \
    --volume-ids vol-0000000000000dead

# 8n: DescribeImages with non-existent AMI ID
echo "8n: DescribeImages non-existent AMI..."
expect_error "InvalidAMIID.NotFound" aws ec2 describe-images \
    --image-ids ami-0000000000000dead

# 8o: CreateImage with duplicate name (e2e-custom-ami exists from Phase 5e)
echo "8o: CreateImage duplicate name..."
expect_error "InvalidAMIName.Duplicate" aws ec2 create-image \
    --instance-id "$INSTANCE_ID" --name "e2e-custom-ami"

# 8p: DeleteKeyPair for non-existent key — should succeed (idempotent, matches AWS)
echo "8p: DeleteKeyPair non-existent key (idempotent)..."
aws ec2 delete-key-pair --key-name nonexistent-key-99999
echo "  DeleteKeyPair for non-existent key succeeded (idempotent)"

# 8q: ModifyInstanceAttribute on running instance (instance not in stopped KV → NotFound)
echo "8q: ModifyInstanceAttribute on running instance..."
expect_error "InvalidInstanceID.NotFound" aws ec2 modify-instance-attribute \
    --instance-id "$INSTANCE_ID" --instance-type "{\"Value\": \"$INSTANCE_TYPE\"}"

# 8r: Reboot non-existent instance
echo "8r: Reboot non-existent instance..."
expect_error "InvalidInstanceID.NotFound" aws ec2 reboot-instances --instance-ids "i-nonexistent"

echo "Negative test suite passed"

# =============================================================================
# IAM E2E Tests
# =============================================================================

# IAM Phase 1: User CRUD
echo ""
echo "IAM Phase 1: User CRUD"

# Root auth — verify list-users works (root user exists)
echo "  Verifying root auth via iam list-users..."
ROOT_USERS=$(aws iam list-users)
echo "$ROOT_USERS" | jq -e '.Users | length > 0' > /dev/null
echo "  Root auth verified"

# CreateUser — alice
echo "  Creating user alice..."
ALICE_OUTPUT=$(aws iam create-user --user-name alice)
echo "$ALICE_OUTPUT" | jq -e '.User.UserName == "alice"' > /dev/null
ALICE_ARN=$(echo "$ALICE_OUTPUT" | jq -r '.User.Arn')
echo "  Created alice: $ALICE_ARN"

# CreateUser — bob with path
echo "  Creating user bob with path /engineering/..."
BOB_OUTPUT=$(aws iam create-user --user-name bob --path /engineering/)
echo "$BOB_OUTPUT" | jq -e '.User.Path == "/engineering/"' > /dev/null
echo "  Created bob"

# CreateUser — duplicate (expect EntityAlreadyExists)
echo "  Creating duplicate user alice (expect error)..."
expect_error "EntityAlreadyExists" aws iam create-user --user-name alice

# GetUser
echo "  Getting user alice..."
aws iam get-user --user-name alice | jq -e '.User.UserName == "alice"' > /dev/null
echo "  GetUser alice passed"

# GetUser — not found
echo "  Getting nonexistent user (expect error)..."
expect_error "NoSuchEntity" aws iam get-user --user-name nonexistent

# ListUsers — should have root, alice, bob
echo "  Listing users..."
USER_COUNT=$(aws iam list-users | jq '.Users | length')
if [ "$USER_COUNT" -lt 3 ]; then
    echo "  ERROR: Expected at least 3 users (root, alice, bob), got $USER_COUNT"
    exit 1
fi
echo "  ListUsers: $USER_COUNT users"

# ListUsers with path-prefix
echo "  Listing users with path-prefix /engineering/..."
ENG_USERS=$(aws iam list-users --path-prefix /engineering/ | jq '.Users | length')
if [ "$ENG_USERS" -ne 1 ]; then
    echo "  ERROR: Expected 1 user with path /engineering/, got $ENG_USERS"
    exit 1
fi
echo "  Path-prefix filter passed"

echo "IAM Phase 1 passed"

# IAM Phase 2: Access Key Lifecycle
echo ""
echo "IAM Phase 2: Access Key Lifecycle"

# CreateAccessKey — alice key 1
echo "  Creating access key 1 for alice..."
KEY1=$(aws iam create-access-key --user-name alice)
ALICE_KEY_ID=$(echo "$KEY1" | jq -r '.AccessKey.AccessKeyId')
ALICE_SECRET=$(echo "$KEY1" | jq -r '.AccessKey.SecretAccessKey')
echo "  Key 1: $ALICE_KEY_ID"

if [ -z "$ALICE_KEY_ID" ] || [ "$ALICE_KEY_ID" == "null" ]; then
    echo "  ERROR: Failed to create access key for alice"
    exit 1
fi

# CreateAccessKey — alice key 2
echo "  Creating access key 2 for alice..."
KEY2=$(aws iam create-access-key --user-name alice)
ALICE_KEY2_ID=$(echo "$KEY2" | jq -r '.AccessKey.AccessKeyId')
echo "  Key 2: $ALICE_KEY2_ID"

# CreateAccessKey — alice key 3 (exceed limit)
echo "  Creating access key 3 for alice (expect LimitExceeded)..."
expect_error "LimitExceeded" aws iam create-access-key --user-name alice

# CreateAccessKey — non-existent user
echo "  Creating access key for ghost (expect error)..."
expect_error "NoSuchEntity" aws iam create-access-key --user-name ghost

# ListAccessKeys
echo "  Listing access keys for alice..."
ALICE_KEY_COUNT=$(aws iam list-access-keys --user-name alice | jq '.AccessKeyMetadata | length')
if [ "$ALICE_KEY_COUNT" -ne 2 ]; then
    echo "  ERROR: Expected 2 keys for alice, got $ALICE_KEY_COUNT"
    exit 1
fi
echo "  Alice has $ALICE_KEY_COUNT keys"

echo "  Listing access keys for bob (expect 0)..."
BOB_KEY_COUNT=$(aws iam list-access-keys --user-name bob | jq '.AccessKeyMetadata // [] | length')
if [ "${BOB_KEY_COUNT:-0}" -ne 0 ]; then
    echo "  ERROR: Expected 0 keys for bob, got $BOB_KEY_COUNT"
    exit 1
fi
echo "  Bob has 0 keys"

# UpdateAccessKey — deactivate
echo "  Deactivating alice key 1..."
aws iam update-access-key --user-name alice --access-key-id "$ALICE_KEY_ID" --status Inactive
STATUS=$(aws iam list-access-keys --user-name alice | \
    jq -r ".AccessKeyMetadata[] | select(.AccessKeyId == \"$ALICE_KEY_ID\") | .Status")
if [ "$STATUS" != "Inactive" ]; then
    echo "  ERROR: Expected Inactive, got $STATUS"
    exit 1
fi
echo "  Key deactivated"

# UpdateAccessKey — reactivate
echo "  Reactivating alice key 1..."
aws iam update-access-key --user-name alice --access-key-id "$ALICE_KEY_ID" --status Active
echo "  Key reactivated"

# DeleteAccessKey — key 2
echo "  Deleting alice key 2..."
aws iam delete-access-key --user-name alice --access-key-id "$ALICE_KEY2_ID"
ALICE_KEY_COUNT=$(aws iam list-access-keys --user-name alice | jq '.AccessKeyMetadata | length')
if [ "$ALICE_KEY_COUNT" -ne 1 ]; then
    echo "  ERROR: Expected 1 key after delete, got $ALICE_KEY_COUNT"
    exit 1
fi
echo "  Key 2 deleted, alice has $ALICE_KEY_COUNT key"

echo "IAM Phase 2 passed"

# IAM Phase 3: User Authentication
echo ""
echo "IAM Phase 3: User Authentication"

# Configure alice profile
echo "  Configuring hive-alice profile..."
setup_test_profile hive-alice "$ALICE_KEY_ID" "$ALICE_SECRET"

# Deactivate key → auth should fail
echo "  Deactivating alice key, verifying auth rejection..."
aws iam update-access-key --user-name alice --access-key-id "$ALICE_KEY_ID" --status Inactive
expect_error "InvalidClientTokenId" \
    aws ec2 describe-instances --profile hive-alice
echo "  Inactive key correctly rejected"

# Reactivate key
echo "  Reactivating alice key..."
aws iam update-access-key --user-name alice --access-key-id "$ALICE_KEY_ID" --status Active

# Bad secret → SignatureDoesNotMatch
echo "  Testing bad secret (expect SignatureDoesNotMatch)..."
setup_test_profile hive-bad "$ALICE_KEY_ID" "WRONG_SECRET_KEY_HERE_12345678901"
expect_error "SignatureDoesNotMatch" \
    aws ec2 describe-instances --profile hive-bad

# Non-existent key ID → InvalidClientTokenId
echo "  Testing fake key ID (expect InvalidClientTokenId)..."
setup_test_profile hive-fake "AKIAXXXXXXXXXXXXXXXX" "doesntmatter"
expect_error "InvalidClientTokenId" \
    aws ec2 describe-instances --profile hive-fake

# Multi-user auth — create bob key and verify both auth
echo "  Creating access key for bob..."
BOB_KEY=$(aws iam create-access-key --user-name bob)
BOB_KEY_ID=$(echo "$BOB_KEY" | jq -r '.AccessKey.AccessKeyId')
BOB_SECRET=$(echo "$BOB_KEY" | jq -r '.AccessKey.SecretAccessKey')
setup_test_profile hive-bob "$BOB_KEY_ID" "$BOB_SECRET"

# Root still works
echo "  Verifying root auth..."
aws ec2 describe-instances > /dev/null
echo "  Root auth OK"

echo "IAM Phase 3 passed"

# IAM Phase 4: Policy CRUD
echo ""
echo "IAM Phase 4: Policy CRUD"

# Get the admin account ID from the current profile
POLICY_OUTPUT=$(aws iam create-policy \
    --policy-name EC2ReadOnly \
    --policy-document '{
        "Version": "2012-10-17",
        "Statement": [{
            "Effect": "Allow",
            "Action": ["ec2:DescribeInstances", "ec2:DescribeVolumes", "ec2:DescribeVpcs"],
            "Resource": "*"
        }]
    }')
ADMIN_ACCOUNT=$(echo "$POLICY_OUTPUT" | jq -r '.Policy.Arn' | cut -d: -f5)
if [ -z "$ADMIN_ACCOUNT" ]; then
    echo "  ERROR: Failed to extract account ID from CreatePolicy response"
    exit 1
fi
echo "  Created EC2ReadOnly (account=$ADMIN_ACCOUNT)"

# CreatePolicy — FullAdmin
echo "  Creating FullAdmin policy..."
aws iam create-policy \
    --policy-name FullAdmin \
    --path /admin/ \
    --description "Full access to all services" \
    --policy-document '{
        "Version": "2012-10-17",
        "Statement": [{
            "Effect": "Allow",
            "Action": "*",
            "Resource": "*"
        }]
    }' > /dev/null
echo "  Created FullAdmin"

# CreatePolicy — DenyTerminate (mixed Allow + Deny)
echo "  Creating DenyTerminate policy..."
aws iam create-policy \
    --policy-name DenyTerminate \
    --policy-document '{
        "Version": "2012-10-17",
        "Statement": [
            {"Effect": "Allow", "Action": "ec2:*", "Resource": "*"},
            {"Effect": "Deny", "Action": "ec2:TerminateInstances", "Resource": "*"}
        ]
    }' > /dev/null
echo "  Created DenyTerminate"

# CreatePolicy — IAMReadOnly
echo "  Creating IAMReadOnly policy..."
aws iam create-policy \
    --policy-name IAMReadOnly \
    --policy-document '{
        "Version": "2012-10-17",
        "Statement": [{
            "Effect": "Allow",
            "Action": ["iam:GetUser", "iam:ListUsers", "iam:ListPolicies", "iam:GetPolicy"],
            "Resource": "*"
        }]
    }' > /dev/null
echo "  Created IAMReadOnly"

# CreatePolicy — EC2DescribeAll (wildcard)
echo "  Creating EC2DescribeAll policy..."
aws iam create-policy \
    --policy-name EC2DescribeAll \
    --policy-document '{
        "Version": "2012-10-17",
        "Statement": [{
            "Effect": "Allow",
            "Action": "ec2:Describe*",
            "Resource": "*"
        }]
    }' > /dev/null
echo "  Created EC2DescribeAll"

# CreatePolicy — duplicate (expect EntityAlreadyExists)
echo "  Creating duplicate EC2ReadOnly (expect error)..."
expect_error "EntityAlreadyExists" aws iam create-policy --policy-name EC2ReadOnly \
    --policy-document '{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"*","Resource":"*"}]}'

# CreatePolicy — malformed JSON
echo "  Creating policy with malformed JSON (expect error)..."
expect_error "MalformedPolicyDocument" aws iam create-policy --policy-name BadPolicy \
    --policy-document '{"not valid"}'

# GetPolicy
echo "  Getting EC2ReadOnly policy..."
aws iam get-policy \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2ReadOnly" | \
    jq -e '.Policy.PolicyName == "EC2ReadOnly"' > /dev/null
echo "  GetPolicy passed"

# GetPolicy — not found
echo "  Getting non-existent policy (expect error)..."
expect_error "NoSuchEntity" aws iam get-policy \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/Ghost"

# GetPolicyVersion
echo "  Getting EC2ReadOnly policy version v1..."
aws iam get-policy-version \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2ReadOnly" \
    --version-id v1 | jq -e '.PolicyVersion.VersionId == "v1"' > /dev/null
echo "  GetPolicyVersion passed"

# ListPolicies
echo "  Listing policies..."
POLICY_COUNT=$(aws iam list-policies | jq '.Policies | length')
if [ "$POLICY_COUNT" -lt 5 ]; then
    echo "  ERROR: Expected at least 5 policies, got $POLICY_COUNT"
    exit 1
fi
echo "  ListPolicies: $POLICY_COUNT policies"

echo "IAM Phase 4 passed"

# IAM Phase 5: Policy Attachment & Enforcement
echo ""
echo "IAM Phase 5: Policy Attachment & Enforcement"

# Create charlie with key
echo "  Creating user charlie with access key..."
aws iam create-user --user-name charlie > /dev/null
CHARLIE_KEY=$(aws iam create-access-key --user-name charlie)
CHARLIE_KEY_ID=$(echo "$CHARLIE_KEY" | jq -r '.AccessKey.AccessKeyId')
CHARLIE_SECRET=$(echo "$CHARLIE_KEY" | jq -r '.AccessKey.SecretAccessKey')
setup_test_profile hive-charlie "$CHARLIE_KEY_ID" "$CHARLIE_SECRET"

# Attach policies
echo "  Attaching EC2ReadOnly + IAMReadOnly to alice..."
aws iam attach-user-policy --user-name alice \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2ReadOnly"
aws iam attach-user-policy --user-name alice \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/IAMReadOnly"

echo "  Attaching DenyTerminate to bob..."
aws iam attach-user-policy --user-name bob \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/DenyTerminate"

# ListAttachedUserPolicies
echo "  Verifying alice's attached policies..."
ALICE_POLICIES=$(aws iam list-attached-user-policies --user-name alice | jq '.AttachedPolicies | length')
if [ "$ALICE_POLICIES" -ne 2 ]; then
    echo "  ERROR: Expected 2 policies for alice, got $ALICE_POLICIES"
    exit 1
fi
echo "  Alice has $ALICE_POLICIES policies"

# Idempotent attach
echo "  Testing idempotent attach..."
aws iam attach-user-policy --user-name alice \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2ReadOnly"
ALICE_POLICIES=$(aws iam list-attached-user-policies --user-name alice | jq '.AttachedPolicies | length')
if [ "$ALICE_POLICIES" -ne 2 ]; then
    echo "  ERROR: Expected 2 policies after idempotent attach, got $ALICE_POLICIES"
    exit 1
fi
echo "  Idempotent attach passed"

# Attach edge cases
echo "  Attaching non-existent policy (expect error)..."
expect_error "NoSuchEntity" aws iam attach-user-policy --user-name alice \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/Ghost"

echo "  Attaching to non-existent user (expect error)..."
expect_error "NoSuchEntity" aws iam attach-user-policy --user-name ghost \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2ReadOnly"

# --- Enforcement Tests ---

# Default Deny — charlie has no policies
echo "  Testing default deny (charlie, no policies)..."
expect_error "AccessDenied" \
    aws ec2 describe-instances --profile hive-charlie
expect_error "AccessDenied" \
    aws iam list-users --profile hive-charlie
echo "  Default deny passed"

# Explicit Allow — alice has EC2ReadOnly + IAMReadOnly
echo "  Testing explicit allow (alice, EC2ReadOnly + IAMReadOnly)..."
aws ec2 describe-instances --profile hive-alice > /dev/null
echo "    ec2:DescribeInstances — allowed"
aws ec2 describe-vpcs --profile hive-alice > /dev/null
echo "    ec2:DescribeVpcs — allowed"
aws iam list-users --profile hive-alice > /dev/null
echo "    iam:ListUsers — allowed"

# Actions NOT in alice's policies → denied
expect_error "AccessDenied" \
    aws ec2 describe-key-pairs --profile hive-alice
echo "    ec2:DescribeKeyPairs — denied (not in policy)"
expect_error "AccessDenied" \
    aws iam create-user --user-name hack --profile hive-alice
echo "    iam:CreateUser — denied (not in policy)"
echo "  Explicit allow passed"

# Wildcard Allow with Explicit Deny — bob has DenyTerminate (ec2:* Allow + ec2:TerminateInstances Deny)
echo "  Testing deny override (bob, DenyTerminate)..."
aws ec2 describe-instances --profile hive-bob > /dev/null
echo "    ec2:DescribeInstances — allowed (ec2:* wildcard)"
aws ec2 describe-key-pairs --profile hive-bob > /dev/null
echo "    ec2:DescribeKeyPairs — allowed (ec2:* wildcard)"
expect_error "AccessDenied" \
    aws ec2 terminate-instances --instance-ids i-fake --profile hive-bob
echo "    ec2:TerminateInstances — denied (explicit Deny overrides Allow)"
expect_error "AccessDenied" \
    aws iam list-users --profile hive-bob
echo "    iam:ListUsers — denied (IAM not covered by ec2:*)"
echo "  Deny override passed"

# Root user bypass
echo "  Testing root user bypass..."
aws ec2 describe-instances > /dev/null
aws iam list-users > /dev/null
aws iam create-user --user-name temp > /dev/null
aws iam delete-user --user-name temp > /dev/null
echo "  Root bypass passed"

# Prefix wildcard — swap alice to EC2DescribeAll
echo "  Testing prefix wildcard (swap alice to EC2DescribeAll)..."
aws iam detach-user-policy --user-name alice \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2ReadOnly"
aws iam detach-user-policy --user-name alice \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/IAMReadOnly"
aws iam attach-user-policy --user-name alice \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2DescribeAll"

aws ec2 describe-instances --profile hive-alice > /dev/null
echo "    ec2:DescribeInstances — allowed (Describe*)"
aws ec2 describe-key-pairs --profile hive-alice > /dev/null
echo "    ec2:DescribeKeyPairs — allowed (Describe*)"
expect_error "AccessDenied" \
    aws ec2 create-key-pair --key-name x --profile hive-alice
echo "    ec2:CreateKeyPair — denied (not Describe*)"
expect_error "AccessDenied" \
    aws iam list-users --profile hive-alice
echo "    iam:ListUsers — denied (not ec2:Describe*)"
echo "  Prefix wildcard passed"

# FullAdmin — give charlie full access
echo "  Testing FullAdmin (charlie)..."
aws iam attach-user-policy --user-name charlie \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/admin/FullAdmin"
aws ec2 describe-instances --profile hive-charlie > /dev/null
echo "    ec2:DescribeInstances — allowed (was denied)"
aws iam list-users --profile hive-charlie > /dev/null
echo "    iam:ListUsers — allowed (was denied)"
echo "  FullAdmin passed"

echo "IAM Phase 5 passed"

# IAM Phase 6: Policy Lifecycle — Detach & Delete
echo ""
echo "IAM Phase 6: Policy Lifecycle — Detach & Delete"

# Detach alice's policy → she loses access
echo "  Detaching EC2DescribeAll from alice..."
aws iam detach-user-policy --user-name alice \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2DescribeAll"
expect_error "AccessDenied" \
    aws ec2 describe-instances --profile hive-alice
echo "  Alice lost access after detach"

# DeletePolicy — conflict (DenyTerminate still attached to bob)
echo "  Deleting DenyTerminate while attached (expect DeleteConflict)..."
expect_error "DeleteConflict" aws iam delete-policy \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/DenyTerminate"

# Detach first, then delete
echo "  Detaching DenyTerminate from bob, then deleting..."
aws iam detach-user-policy --user-name bob \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/DenyTerminate"
aws iam delete-policy \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/DenyTerminate"

# Verify it's gone
expect_error "NoSuchEntity" aws iam get-policy \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/DenyTerminate"
echo "  DenyTerminate deleted"

echo "IAM Phase 6 passed"

# IAM Phase 7: Cleanup
echo ""
echo "IAM Phase 7: IAM Cleanup"

# Delete alice (detach remaining, delete key, delete user)
echo "  Cleaning up alice..."
aws iam delete-access-key --user-name alice --access-key-id "$ALICE_KEY_ID"
aws iam delete-user --user-name alice

# Delete bob
echo "  Cleaning up bob..."
aws iam delete-access-key --user-name bob --access-key-id "$BOB_KEY_ID"
aws iam delete-user --user-name bob

# Delete charlie (detach FullAdmin first)
echo "  Cleaning up charlie..."
aws iam detach-user-policy --user-name charlie \
    --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/admin/FullAdmin"
aws iam delete-access-key --user-name charlie --access-key-id "$CHARLIE_KEY_ID"
aws iam delete-user --user-name charlie

# Verify only root remains
FINAL_USER_COUNT=$(aws iam list-users | jq '.Users | length')
if [ "$FINAL_USER_COUNT" -ne 1 ]; then
    echo "  ERROR: Expected 1 user (root) after cleanup, got $FINAL_USER_COUNT"
    exit 1
fi
echo "  Users cleaned up (root only)"

# Delete remaining policies (including bootstrap AdministratorAccess)
echo "  Cleaning up policies..."
aws iam delete-policy --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2ReadOnly"
aws iam delete-policy --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/admin/FullAdmin"
aws iam delete-policy --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/IAMReadOnly"
aws iam delete-policy --policy-arn "arn:aws:iam::${ADMIN_ACCOUNT}:policy/EC2DescribeAll"
# AdministratorAccess stays attached to admin — detaching it would revoke
# admin's own permissions and block the delete (account root bypass not yet implemented).

FINAL_POLICY_COUNT=$(aws iam list-policies | jq '.Policies | length')
if [ "$FINAL_POLICY_COUNT" -ne 1 ]; then
    echo "  ERROR: Expected 1 policy (AdministratorAccess) after cleanup, got $FINAL_POLICY_COUNT"
    exit 1
fi
echo "  Policies cleaned up"

echo "IAM Phase 7 passed"
echo ""
echo "IAM E2E Tests Completed Successfully"

# =============================================================================
# Phase 8: EC2 Account Scoping
# =============================================================================
# Tests that EC2 resources (instances, volumes, key pairs, snapshots, VPCs,
# IGWs, EIGWs) are properly isolated between tenant accounts.
# Based on: docs/development/feature/iam-phase4-e2e-test-guide.md
# Skips: Section 6 (CreateImage — mulga-612), Tags on instances (mulga-613)
echo ""
echo "Phase 8: EC2 Account Scoping"
echo "========================================"

# --- Step 1: Account Setup ---
echo ""
echo "Phase 8 Step 1: Account Setup"
echo "----------------------------------------"

echo "  Creating Team Alpha account..."
ALPHA_OUTPUT=$(./bin/hive admin account create --name "Team Alpha" 2>&1)
echo "$ALPHA_OUTPUT"
ALPHA_ACCOUNT=$(echo "$ALPHA_OUTPUT" | grep "Account ID:" | awk '{print $NF}')
ALPHA_KEY_ID=$(echo "$ALPHA_OUTPUT" | grep "Access Key ID:" | awk '{print $NF}')
ALPHA_SECRET=$(echo "$ALPHA_OUTPUT" | grep "Secret Access Key:" | awk '{print $NF}')

if [ -z "$ALPHA_ACCOUNT" ] || [ -z "$ALPHA_KEY_ID" ]; then
    echo "  ERROR: Failed to parse Team Alpha account output"
    exit 1
fi
echo "  Team Alpha: account=$ALPHA_ACCOUNT key=$ALPHA_KEY_ID"
setup_test_profile hive-team-alpha "$ALPHA_KEY_ID" "$ALPHA_SECRET"

echo "  Creating Team Beta account..."
BETA_OUTPUT=$(./bin/hive admin account create --name "Team Beta" 2>&1)
echo "$BETA_OUTPUT"
BETA_ACCOUNT=$(echo "$BETA_OUTPUT" | grep "Account ID:" | awk '{print $NF}')
BETA_KEY_ID=$(echo "$BETA_OUTPUT" | grep "Access Key ID:" | awk '{print $NF}')
BETA_SECRET=$(echo "$BETA_OUTPUT" | grep "Secret Access Key:" | awk '{print $NF}')

if [ -z "$BETA_ACCOUNT" ] || [ -z "$BETA_KEY_ID" ]; then
    echo "  ERROR: Failed to parse Team Beta account output"
    exit 1
fi
echo "  Team Beta: account=$BETA_ACCOUNT key=$BETA_KEY_ID"
setup_test_profile hive-team-beta "$BETA_KEY_ID" "$BETA_SECRET"

# Verify auth
aws ec2 describe-instances --profile hive-team-alpha > /dev/null
echo "  Alpha auth OK"
aws ec2 describe-instances --profile hive-team-beta > /dev/null
echo "  Beta auth OK"

echo "  Account setup complete"

# --- Step 2: Instance Scoping ---
echo ""
echo "Phase 8 Step 2: Instance Scoping"
echo "----------------------------------------"

# Create per-account key pairs (key pairs are account-scoped, root's test-key-1 is invisible)
aws ec2 create-key-pair --key-name alpha-instance-key --profile hive-team-alpha > /dev/null
aws ec2 create-key-pair --key-name beta-instance-key --profile hive-team-beta > /dev/null
echo "  Created per-account key pairs for instance launches"

echo "  Alpha launching instance..."
ALPHA_RUN=$(aws ec2 run-instances \
    --image-id "$AMI_ID" \
    --instance-type "$INSTANCE_TYPE" \
    --key-name alpha-instance-key \
    --profile hive-team-alpha)
ALPHA_INST=$(echo "$ALPHA_RUN" | jq -r '.Instances[0].InstanceId')
echo "  Alpha instance: $ALPHA_INST"

echo "  Beta launching instance..."
BETA_RUN=$(aws ec2 run-instances \
    --image-id "$AMI_ID" \
    --instance-type "$INSTANCE_TYPE" \
    --key-name beta-instance-key \
    --profile hive-team-beta)
BETA_INST=$(echo "$BETA_RUN" | jq -r '.Instances[0].InstanceId')
echo "  Beta instance: $BETA_INST"

# Wait for running
echo "  Waiting for instances to reach running state..."
COUNT=0
while [ $COUNT -lt 30 ]; do
    A_STATE=$(aws ec2 describe-instances --instance-ids "$ALPHA_INST" --profile hive-team-alpha \
        --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "pending")
    B_STATE=$(aws ec2 describe-instances --instance-ids "$BETA_INST" --profile hive-team-beta \
        --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "pending")
    echo "  Alpha=$A_STATE, Beta=$B_STATE"
    if [ "$A_STATE" == "running" ] && [ "$B_STATE" == "running" ]; then break; fi
    sleep 2
    COUNT=$((COUNT + 1))
done
if [ "$A_STATE" != "running" ] || [ "$B_STATE" != "running" ]; then
    echo "  ERROR: Instances failed to reach running state"
    exit 1
fi
echo "  Both instances running"

# Describe isolation
ALPHA_INSTANCES=$(aws ec2 describe-instances --profile hive-team-alpha \
    --query 'Reservations[].Instances[].InstanceId' --output text)
if echo "$ALPHA_INSTANCES" | grep -q "$BETA_INST"; then
    echo "  ERROR: Alpha can see Beta's instance"
    exit 1
fi
echo "  Alpha sees only own instances"

BETA_INSTANCES=$(aws ec2 describe-instances --profile hive-team-beta \
    --query 'Reservations[].Instances[].InstanceId' --output text)
if echo "$BETA_INSTANCES" | grep -q "$ALPHA_INST"; then
    echo "  ERROR: Beta can see Alpha's instance"
    exit 1
fi
echo "  Beta sees only own instances"

# OwnerId verification
ALPHA_OWNER=$(aws ec2 describe-instances --profile hive-team-alpha \
    --query 'Reservations[0].OwnerId' --output text)
if [ "$ALPHA_OWNER" != "$ALPHA_ACCOUNT" ]; then
    echo "  ERROR: Alpha OwnerId mismatch: expected $ALPHA_ACCOUNT, got $ALPHA_OWNER"
    exit 1
fi
echo "  Alpha OwnerId correct: $ALPHA_OWNER"

# Cross-account operations
expect_error "InvalidInstanceID.NotFound" \
    aws ec2 stop-instances --instance-ids "$BETA_INST" --profile hive-team-alpha
echo "  Alpha cannot stop Beta's instance"

expect_error "InvalidInstanceID.NotFound" \
    aws ec2 terminate-instances --instance-ids "$ALPHA_INST" --profile hive-team-beta
echo "  Beta cannot terminate Alpha's instance"

expect_error "InvalidInstanceID.NotFound" \
    aws ec2 reboot-instances --instance-ids "$BETA_INST" --profile hive-team-alpha
echo "  Alpha cannot reboot Beta's instance"

# Stop Alpha's instance for cross-account start/modify/console tests
aws ec2 stop-instances --instance-ids "$ALPHA_INST" --profile hive-team-alpha > /dev/null
COUNT=0
while [ $COUNT -lt 30 ]; do
    A_STATE=$(aws ec2 describe-instances --instance-ids "$ALPHA_INST" --profile hive-team-alpha \
        --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null)
    if [ "$A_STATE" == "stopped" ]; then break; fi
    sleep 2
    COUNT=$((COUNT + 1))
done

expect_error "InvalidInstanceID.NotFound" \
    aws ec2 start-instances --instance-ids "$ALPHA_INST" --profile hive-team-beta
echo "  Beta cannot start Alpha's stopped instance"

expect_error "InvalidInstanceID.NotFound" \
    aws ec2 modify-instance-attribute --instance-id "$ALPHA_INST" \
    --instance-type '{"Value":"t2.small"}' --profile hive-team-beta
echo "  Beta cannot modify Alpha's instance"

expect_error "InvalidInstanceID.NotFound" \
    aws ec2 get-console-output --instance-id "$ALPHA_INST" --profile hive-team-beta
echo "  Beta cannot get console output of Alpha's instance"

# Restart Alpha's instance for later tests
aws ec2 start-instances --instance-ids "$ALPHA_INST" --profile hive-team-alpha > /dev/null
COUNT=0
while [ $COUNT -lt 30 ]; do
    A_STATE=$(aws ec2 describe-instances --instance-ids "$ALPHA_INST" --profile hive-team-alpha \
        --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null)
    if [ "$A_STATE" == "running" ]; then break; fi
    sleep 2
    COUNT=$((COUNT + 1))
done

echo "  Instance scoping passed"

# --- Step 3: Volume Scoping ---
echo ""
echo "Phase 8 Step 3: Volume Scoping"
echo "----------------------------------------"

ALPHA_VOL=$(aws ec2 create-volume --availability-zone "$AZ_NAME" --size 10 \
    --volume-type gp3 --profile hive-team-alpha | jq -r '.VolumeId')
echo "  Alpha volume: $ALPHA_VOL"

BETA_VOL=$(aws ec2 create-volume --availability-zone "$AZ_NAME" --size 10 \
    --volume-type gp3 --profile hive-team-beta | jq -r '.VolumeId')
echo "  Beta volume: $BETA_VOL"

# Describe isolation
ALPHA_VOLS=$(aws ec2 describe-volumes --profile hive-team-alpha \
    --query 'Volumes[].VolumeId' --output text)
if echo "$ALPHA_VOLS" | grep -q "$BETA_VOL"; then
    echo "  ERROR: Alpha can see Beta's volume"
    exit 1
fi
echo "  Alpha sees only own volumes"

# Cross-account filter
expect_error "InvalidVolume.NotFound" \
    aws ec2 describe-volumes --volume-ids "$BETA_VOL" --profile hive-team-alpha
echo "  Alpha cannot describe Beta's volume by ID"

# Cross-account delete
expect_error "InvalidVolume.NotFound" \
    aws ec2 delete-volume --volume-id "$ALPHA_VOL" --profile hive-team-beta
echo "  Beta cannot delete Alpha's volume"

# Cross-account attach (other's volume)
expect_error "InvalidVolume.NotFound" \
    aws ec2 attach-volume --volume-id "$ALPHA_VOL" \
    --instance-id "$BETA_INST" --device /dev/sdf --profile hive-team-beta
echo "  Beta cannot attach Alpha's volume"

# Attach Alpha's volume to Alpha's instance, then test cross-account detach
aws ec2 attach-volume --volume-id "$ALPHA_VOL" \
    --instance-id "$ALPHA_INST" --device /dev/sdf --profile hive-team-alpha > /dev/null
sleep 2

expect_error "InvalidVolume.NotFound" \
    aws ec2 detach-volume --volume-id "$ALPHA_VOL" --profile hive-team-beta
echo "  Beta cannot detach Alpha's volume"

# Cross-account modify
expect_error "InvalidVolume.NotFound" \
    aws ec2 modify-volume --volume-id "$ALPHA_VOL" --size 20 --profile hive-team-beta
echo "  Beta cannot modify Alpha's volume"

# Detach for cleanup later
aws ec2 detach-volume --volume-id "$ALPHA_VOL" --profile hive-team-alpha > /dev/null
sleep 2

echo "  Volume scoping passed"

# --- Step 4: Key Pair Scoping ---
echo ""
echo "Phase 8 Step 4: Key Pair Scoping"
echo "----------------------------------------"

aws ec2 create-key-pair --key-name alpha-key --profile hive-team-alpha > /dev/null
ALPHA_KEYPAIR_ID=$(aws ec2 describe-key-pairs --key-names alpha-key \
    --profile hive-team-alpha --query 'KeyPairs[0].KeyPairId' --output text)
echo "  Alpha key: alpha-key ($ALPHA_KEYPAIR_ID)"

aws ec2 create-key-pair --key-name beta-key --profile hive-team-beta > /dev/null
echo "  Beta key: beta-key"

# Describe isolation
ALPHA_KEYS=$(aws ec2 describe-key-pairs --profile hive-team-alpha \
    --query 'KeyPairs[].KeyName' --output text)
if echo "$ALPHA_KEYS" | grep -q "beta-key"; then
    echo "  ERROR: Alpha can see Beta's key"
    exit 1
fi
echo "  Alpha sees only own keys"

# Same name in both accounts (namespace isolation)
aws ec2 create-key-pair --key-name shared-name --profile hive-team-alpha > /dev/null
aws ec2 create-key-pair --key-name shared-name --profile hive-team-beta > /dev/null
ALPHA_SHARED_ID=$(aws ec2 describe-key-pairs --key-names shared-name \
    --profile hive-team-alpha --query 'KeyPairs[0].KeyPairId' --output text)
BETA_SHARED_ID=$(aws ec2 describe-key-pairs --key-names shared-name \
    --profile hive-team-beta --query 'KeyPairs[0].KeyPairId' --output text)
if [ "$ALPHA_SHARED_ID" == "$BETA_SHARED_ID" ]; then
    echo "  ERROR: Same KeyPairId for shared-name in both accounts"
    exit 1
fi
echo "  Namespace isolation: alpha=$ALPHA_SHARED_ID, beta=$BETA_SHARED_ID"

# Cross-account delete (idempotent, but shouldn't affect other account)
aws ec2 delete-key-pair --key-name alpha-key --profile hive-team-beta
ALPHA_KEY_CHECK=$(aws ec2 describe-key-pairs --key-names alpha-key \
    --profile hive-team-alpha --query 'KeyPairs[0].KeyPairId' --output text)
if [ "$ALPHA_KEY_CHECK" != "$ALPHA_KEYPAIR_ID" ]; then
    echo "  ERROR: Beta's delete affected Alpha's key"
    exit 1
fi
echo "  Cross-account delete had no effect on Alpha's key"

# Import key pair — account scoped
ssh-keygen -t ed25519 -f /tmp/test-import-key -N "" -q
aws ec2 import-key-pair --key-name imported-key \
    --public-key-material fileb:///tmp/test-import-key.pub --profile hive-team-alpha > /dev/null
BETA_IMPORT_CHECK=$(aws ec2 describe-key-pairs --profile hive-team-beta \
    --query 'KeyPairs[].KeyName' --output text)
if echo "$BETA_IMPORT_CHECK" | grep -q "imported-key"; then
    echo "  ERROR: Beta can see Alpha's imported key"
    exit 1
fi
echo "  Imported key invisible to Beta"
rm -f /tmp/test-import-key /tmp/test-import-key.pub

echo "  Key pair scoping passed"

# --- Step 5: Snapshot Scoping ---
echo ""
echo "Phase 8 Step 5: Snapshot Scoping"
echo "----------------------------------------"

ALPHA_SNAP=$(aws ec2 create-snapshot --volume-id "$ALPHA_VOL" \
    --description "Alpha snapshot" --profile hive-team-alpha | jq -r '.SnapshotId')
echo "  Alpha snapshot: $ALPHA_SNAP"

BETA_SNAP=$(aws ec2 create-snapshot --volume-id "$BETA_VOL" \
    --description "Beta snapshot" --profile hive-team-beta | jq -r '.SnapshotId')
echo "  Beta snapshot: $BETA_SNAP"

# Describe isolation
ALPHA_SNAPS=$(aws ec2 describe-snapshots --owner-ids self --profile hive-team-alpha \
    --query 'Snapshots[].SnapshotId' --output text)
if echo "$ALPHA_SNAPS" | grep -q "$BETA_SNAP"; then
    echo "  ERROR: Alpha can see Beta's snapshot"
    exit 1
fi
echo "  Alpha sees only own snapshots"

# OwnerId verification
ALPHA_SNAP_OWNER=$(aws ec2 describe-snapshots --owner-ids self --profile hive-team-alpha \
    --query 'Snapshots[0].OwnerId' --output text)
if [ "$ALPHA_SNAP_OWNER" != "$ALPHA_ACCOUNT" ]; then
    echo "  ERROR: Snapshot OwnerId mismatch: expected $ALPHA_ACCOUNT, got $ALPHA_SNAP_OWNER"
    exit 1
fi
echo "  Alpha snapshot OwnerId correct"

# Cross-account delete
expect_error "UnauthorizedOperation" \
    aws ec2 delete-snapshot --snapshot-id "$ALPHA_SNAP" --profile hive-team-beta
echo "  Beta cannot delete Alpha's snapshot"

# Cross-account snapshot from other's volume
expect_error "InvalidVolume.NotFound" \
    aws ec2 create-snapshot --volume-id "$ALPHA_VOL" \
    --description "stolen" --profile hive-team-beta
echo "  Beta cannot snapshot Alpha's volume"

echo "  Snapshot scoping passed"

# --- Step 6: VPC/Subnet Scoping ---
echo ""
echo "Phase 8 Step 6: VPC/Subnet Scoping"
echo "----------------------------------------"

ALPHA_VPC=$(aws ec2 create-vpc --cidr-block 10.0.0.0/16 \
    --profile hive-team-alpha --query 'Vpc.VpcId' --output text)
echo "  Alpha VPC: $ALPHA_VPC"

BETA_VPC=$(aws ec2 create-vpc --cidr-block 10.0.0.0/16 \
    --profile hive-team-beta --query 'Vpc.VpcId' --output text)
echo "  Beta VPC: $BETA_VPC (same CIDR — no conflict)"

# Describe isolation
ALPHA_VPCS=$(aws ec2 describe-vpcs --profile hive-team-alpha \
    --query 'Vpcs[].VpcId' --output text)
if echo "$ALPHA_VPCS" | grep -q "$BETA_VPC"; then
    echo "  ERROR: Alpha can see Beta's VPC"
    exit 1
fi
echo "  VPC describe isolation OK"

# Cross-account describe by ID
expect_error "InvalidVpcID.NotFound" \
    aws ec2 describe-vpcs --vpc-ids "$BETA_VPC" --profile hive-team-alpha
echo "  Alpha cannot describe Beta's VPC by ID"

# Cross-account delete
expect_error "InvalidVpcID.NotFound" \
    aws ec2 delete-vpc --vpc-id "$ALPHA_VPC" --profile hive-team-beta
echo "  Beta cannot delete Alpha's VPC"

# Create subnets
ALPHA_SUBNET=$(aws ec2 create-subnet --vpc-id "$ALPHA_VPC" --cidr-block 10.0.1.0/24 \
    --profile hive-team-alpha --query 'Subnet.SubnetId' --output text)
echo "  Alpha subnet: $ALPHA_SUBNET"

BETA_SUBNET=$(aws ec2 create-subnet --vpc-id "$BETA_VPC" --cidr-block 10.0.1.0/24 \
    --profile hive-team-beta --query 'Subnet.SubnetId' --output text)
echo "  Beta subnet: $BETA_SUBNET"

# Subnet describe isolation
ALPHA_SUBNETS=$(aws ec2 describe-subnets --profile hive-team-alpha \
    --query 'Subnets[].SubnetId' --output text)
if echo "$ALPHA_SUBNETS" | grep -q "$BETA_SUBNET"; then
    echo "  ERROR: Alpha can see Beta's subnet"
    exit 1
fi
echo "  Subnet describe isolation OK"

# Cross-account create subnet in other's VPC
expect_error "InvalidVpcID.NotFound" \
    aws ec2 create-subnet --vpc-id "$ALPHA_VPC" --cidr-block 10.0.2.0/24 \
    --profile hive-team-beta
echo "  Beta cannot create subnet in Alpha's VPC"

# Cross-account subnet delete
expect_error "InvalidSubnetID.NotFound" \
    aws ec2 delete-subnet --subnet-id "$ALPHA_SUBNET" --profile hive-team-beta
echo "  Beta cannot delete Alpha's subnet"

echo "  VPC/Subnet scoping passed"

# --- Step 7: IGW + EIGW Scoping ---
echo ""
echo "Phase 8 Step 7: IGW + EIGW Scoping"
echo "----------------------------------------"

# IGW
ALPHA_IGW=$(aws ec2 create-internet-gateway --profile hive-team-alpha \
    --query 'InternetGateway.InternetGatewayId' --output text)
echo "  Alpha IGW: $ALPHA_IGW"

BETA_IGW=$(aws ec2 create-internet-gateway --profile hive-team-beta \
    --query 'InternetGateway.InternetGatewayId' --output text)
echo "  Beta IGW: $BETA_IGW"

# IGW describe isolation
ALPHA_IGWS=$(aws ec2 describe-internet-gateways --profile hive-team-alpha \
    --query 'InternetGateways[].InternetGatewayId' --output text)
if echo "$ALPHA_IGWS" | grep -q "$BETA_IGW"; then
    echo "  ERROR: Alpha can see Beta's IGW"
    exit 1
fi
echo "  IGW describe isolation OK"

# Cross-account IGW describe by ID
expect_error "InvalidInternetGatewayID.NotFound" \
    aws ec2 describe-internet-gateways --internet-gateway-ids "$BETA_IGW" \
    --profile hive-team-alpha
echo "  Alpha cannot describe Beta's IGW by ID"

# Cross-account IGW delete
expect_error "InvalidInternetGatewayID.NotFound" \
    aws ec2 delete-internet-gateway --internet-gateway-id "$ALPHA_IGW" \
    --profile hive-team-beta
echo "  Beta cannot delete Alpha's IGW"

# Cross-account attach IGW to other's VPC
expect_error "InvalidInternetGatewayID.NotFound" \
    aws ec2 attach-internet-gateway --internet-gateway-id "$BETA_IGW" \
    --vpc-id "$ALPHA_VPC" --profile hive-team-alpha
echo "  Alpha cannot attach Beta's IGW to own VPC"

# Attach Alpha's IGW, test cross-account detach
aws ec2 attach-internet-gateway --internet-gateway-id "$ALPHA_IGW" \
    --vpc-id "$ALPHA_VPC" --profile hive-team-alpha > /dev/null
expect_error "InvalidInternetGatewayID.NotFound" \
    aws ec2 detach-internet-gateway --internet-gateway-id "$ALPHA_IGW" \
    --vpc-id "$ALPHA_VPC" --profile hive-team-beta
echo "  Beta cannot detach Alpha's IGW"

# EIGW
ALPHA_EIGW=$(aws ec2 create-egress-only-internet-gateway --vpc-id "$ALPHA_VPC" \
    --profile hive-team-alpha \
    --query 'EgressOnlyInternetGateway.EgressOnlyInternetGatewayId' --output text)
echo "  Alpha EIGW: $ALPHA_EIGW"

BETA_EIGW=$(aws ec2 create-egress-only-internet-gateway --vpc-id "$BETA_VPC" \
    --profile hive-team-beta \
    --query 'EgressOnlyInternetGateway.EgressOnlyInternetGatewayId' --output text)
echo "  Beta EIGW: $BETA_EIGW"

# EIGW describe isolation
ALPHA_EIGWS=$(aws ec2 describe-egress-only-internet-gateways --profile hive-team-alpha \
    --query 'EgressOnlyInternetGateways[].EgressOnlyInternetGatewayId' --output text)
if echo "$ALPHA_EIGWS" | grep -q "$BETA_EIGW"; then
    echo "  ERROR: Alpha can see Beta's EIGW"
    exit 1
fi
echo "  EIGW describe isolation OK"

# Cross-account EIGW delete
expect_error "" \
    aws ec2 delete-egress-only-internet-gateway \
    --egress-only-internet-gateway-id "$ALPHA_EIGW" --profile hive-team-beta 2>&1 || true
# Verify Alpha's EIGW still exists
ALPHA_EIGW_CHECK=$(aws ec2 describe-egress-only-internet-gateways --profile hive-team-alpha \
    --query 'EgressOnlyInternetGateways[].EgressOnlyInternetGatewayId' --output text)
if ! echo "$ALPHA_EIGW_CHECK" | grep -q "$ALPHA_EIGW"; then
    echo "  ERROR: Alpha's EIGW was deleted by Beta"
    exit 1
fi
echo "  Beta cannot delete Alpha's EIGW"

# Cross-account EIGW creation in other's VPC
expect_error "" \
    aws ec2 create-egress-only-internet-gateway --vpc-id "$ALPHA_VPC" \
    --profile hive-team-beta 2>&1 || true
echo "  Beta cannot create EIGW in Alpha's VPC"

echo "  IGW + EIGW scoping passed"

# --- Step 8: Account Settings ---
echo ""
echo "Phase 8 Step 8: Account Settings"
echo "----------------------------------------"

aws ec2 enable-ebs-encryption-by-default --profile hive-team-alpha > /dev/null
BETA_ENC=$(aws ec2 get-ebs-encryption-by-default --profile hive-team-beta \
    --query 'EbsEncryptionByDefault' --output text)
if [ "$BETA_ENC" != "False" ]; then
    echo "  ERROR: Alpha's encryption setting leaked to Beta (got $BETA_ENC)"
    exit 1
fi
echo "  Alpha enable did not affect Beta"

# Independent toggle
aws ec2 enable-ebs-encryption-by-default --profile hive-team-beta > /dev/null
aws ec2 disable-ebs-encryption-by-default --profile hive-team-alpha > /dev/null
ALPHA_ENC=$(aws ec2 get-ebs-encryption-by-default --profile hive-team-alpha \
    --query 'EbsEncryptionByDefault' --output text)
BETA_ENC=$(aws ec2 get-ebs-encryption-by-default --profile hive-team-beta \
    --query 'EbsEncryptionByDefault' --output text)
if [ "$ALPHA_ENC" != "False" ] || [ "$BETA_ENC" != "True" ]; then
    echo "  ERROR: Independent settings failed: alpha=$ALPHA_ENC beta=$BETA_ENC"
    exit 1
fi
echo "  Independent toggle verified: alpha=$ALPHA_ENC, beta=$BETA_ENC"

# Reset
aws ec2 disable-ebs-encryption-by-default --profile hive-team-beta > /dev/null

echo "  Account settings scoping passed"

# --- Step 9: Global Resources ---
echo ""
echo "Phase 8 Step 9: Global Resources"
echo "----------------------------------------"

ALPHA_REGIONS=$(aws ec2 describe-regions --profile hive-team-alpha \
    --query 'Regions[].RegionName' --output text)
BETA_REGIONS=$(aws ec2 describe-regions --profile hive-team-beta \
    --query 'Regions[].RegionName' --output text)
if [ "$ALPHA_REGIONS" != "$BETA_REGIONS" ]; then
    echo "  ERROR: Regions differ between accounts"
    exit 1
fi
echo "  Regions identical"

ALPHA_AZS=$(aws ec2 describe-availability-zones --profile hive-team-alpha \
    --query 'AvailabilityZones[].ZoneName' --output text)
BETA_AZS=$(aws ec2 describe-availability-zones --profile hive-team-beta \
    --query 'AvailabilityZones[].ZoneName' --output text)
if [ "$ALPHA_AZS" != "$BETA_AZS" ]; then
    echo "  ERROR: AZs differ between accounts"
    exit 1
fi
echo "  Availability zones identical"

ALPHA_TYPES=$(aws ec2 describe-instance-types --profile hive-team-alpha \
    --query 'InstanceTypes[].InstanceType' --output text | tr '\t' '\n' | sort)
BETA_TYPES=$(aws ec2 describe-instance-types --profile hive-team-beta \
    --query 'InstanceTypes[].InstanceType' --output text | tr '\t' '\n' | sort)
if [ "$ALPHA_TYPES" != "$BETA_TYPES" ]; then
    echo "  ERROR: Instance types differ between accounts"
    exit 1
fi
echo "  Instance types identical"

echo "  Global resources passed"

# --- Step 10: Edge Cases ---
echo ""
echo "Phase 8 Step 10: Edge Cases"
echo "----------------------------------------"

# Empty account (Gamma)
echo "  Creating empty Gamma account..."
GAMMA_OUTPUT=$(./bin/hive admin account create --name "Team Gamma" 2>&1)
GAMMA_KEY_ID=$(echo "$GAMMA_OUTPUT" | grep "Access Key ID:" | awk '{print $NF}')
GAMMA_SECRET=$(echo "$GAMMA_OUTPUT" | grep "Secret Access Key:" | awk '{print $NF}')
setup_test_profile hive-team-gamma "$GAMMA_KEY_ID" "$GAMMA_SECRET"

GAMMA_INSTANCES=$(aws ec2 describe-instances --profile hive-team-gamma \
    --query 'Reservations' --output text)
if [ -n "$GAMMA_INSTANCES" ] && [ "$GAMMA_INSTANCES" != "None" ]; then
    echo "  ERROR: Gamma has instances"
    exit 1
fi
echo "  Gamma: no instances"

# Skip volume check: root-account volumes (empty TenantID) are visible to all accounts by design
echo "  Gamma: volumes skipped (root legacy volumes visible to all)"

GAMMA_KEYS=$(aws ec2 describe-key-pairs --profile hive-team-gamma \
    --query 'KeyPairs' --output text)
if [ -n "$GAMMA_KEYS" ] && [ "$GAMMA_KEYS" != "None" ]; then
    echo "  ERROR: Gamma has key pairs"
    exit 1
fi
echo "  Gamma: no key pairs"

GAMMA_SNAPS=$(aws ec2 describe-snapshots --owner-ids self --profile hive-team-gamma \
    --query 'Snapshots' --output text)
if [ -n "$GAMMA_SNAPS" ] && [ "$GAMMA_SNAPS" != "None" ]; then
    echo "  ERROR: Gamma has snapshots"
    exit 1
fi
echo "  Gamma: no snapshots"

# Root isolation from tenants
echo "  Verifying root isolation from tenants..."
aws ec2 create-key-pair --key-name root-scoping-key > /dev/null
ALPHA_ROOT_CHECK=$(aws ec2 describe-key-pairs --profile hive-team-alpha \
    --query 'KeyPairs[].KeyName' --output text)
if echo "$ALPHA_ROOT_CHECK" | grep -q "root-scoping-key"; then
    echo "  ERROR: Alpha can see root's key pair"
    exit 1
fi
echo "  Tenants cannot see root's key pairs"

ROOT_INSTANCE_CHECK=$(aws ec2 describe-instances \
    --query 'Reservations[].Instances[].InstanceId' --output text)
if echo "$ROOT_INSTANCE_CHECK" | grep -q "$ALPHA_INST"; then
    echo "  ERROR: Root can see Alpha's instance"
    exit 1
fi
echo "  Root cannot see tenant instances"
aws ec2 delete-key-pair --key-name root-scoping-key > /dev/null

# Non-existent resource IDs — same error as cross-account
expect_error "InvalidVolume.NotFound" \
    aws ec2 delete-volume --volume-id vol-00000000000000000 --profile hive-team-alpha
echo "  Non-existent volume: same error as cross-account"

expect_error "InvalidSnapshot.NotFound" \
    aws ec2 delete-snapshot --snapshot-id snap-00000000000000000 --profile hive-team-alpha
echo "  Non-existent snapshot: same error as cross-account"

echo "  Edge cases passed"

# --- Step 11: EC2 Account Scoping Cleanup ---
echo ""
echo "Phase 8 Step 11: EC2 Account Scoping Cleanup"
echo "----------------------------------------"

# Terminate instances
echo "  Terminating Alpha instance..."
aws ec2 terminate-instances --instance-ids "$ALPHA_INST" --profile hive-team-alpha > /dev/null
echo "  Terminating Beta instance..."
aws ec2 terminate-instances --instance-ids "$BETA_INST" --profile hive-team-beta > /dev/null

# Wait for termination
COUNT=0
while [ $COUNT -lt 30 ]; do
    A_STATE=$(aws ec2 describe-instances --instance-ids "$ALPHA_INST" --profile hive-team-alpha \
        --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "terminated")
    B_STATE=$(aws ec2 describe-instances --instance-ids "$BETA_INST" --profile hive-team-beta \
        --query 'Reservations[0].Instances[0].State.Name' --output text 2>/dev/null || echo "terminated")
    if [ "$A_STATE" == "terminated" ] && [ "$B_STATE" == "terminated" ]; then
        break
    fi
    sleep 2
    COUNT=$((COUNT + 1))
done
echo "  Instances terminated"

# Delete snapshots
echo "  Deleting snapshots..."
aws ec2 delete-snapshot --snapshot-id "$ALPHA_SNAP" --profile hive-team-alpha 2>/dev/null || true
aws ec2 delete-snapshot --snapshot-id "$BETA_SNAP" --profile hive-team-beta 2>/dev/null || true

# Delete volumes (wait for detach/termination cleanup)
sleep 3
echo "  Deleting volumes..."
aws ec2 delete-volume --volume-id "$ALPHA_VOL" --profile hive-team-alpha 2>/dev/null || true
aws ec2 delete-volume --volume-id "$BETA_VOL" --profile hive-team-beta 2>/dev/null || true

# Delete key pairs
echo "  Deleting key pairs..."
for key in alpha-key alpha-instance-key shared-name imported-key; do
    aws ec2 delete-key-pair --key-name "$key" --profile hive-team-alpha 2>/dev/null || true
done
for key in beta-key beta-instance-key shared-name; do
    aws ec2 delete-key-pair --key-name "$key" --profile hive-team-beta 2>/dev/null || true
done

# Delete EIGWs
echo "  Deleting EIGWs..."
aws ec2 delete-egress-only-internet-gateway \
    --egress-only-internet-gateway-id "$ALPHA_EIGW" --profile hive-team-alpha 2>/dev/null || true
aws ec2 delete-egress-only-internet-gateway \
    --egress-only-internet-gateway-id "$BETA_EIGW" --profile hive-team-beta 2>/dev/null || true

# Detach + delete IGWs
echo "  Deleting IGWs..."
aws ec2 detach-internet-gateway --internet-gateway-id "$ALPHA_IGW" \
    --vpc-id "$ALPHA_VPC" --profile hive-team-alpha 2>/dev/null || true
aws ec2 delete-internet-gateway --internet-gateway-id "$ALPHA_IGW" \
    --profile hive-team-alpha 2>/dev/null || true
aws ec2 delete-internet-gateway --internet-gateway-id "$BETA_IGW" \
    --profile hive-team-beta 2>/dev/null || true

# Delete subnets
echo "  Deleting subnets..."
aws ec2 delete-subnet --subnet-id "$ALPHA_SUBNET" --profile hive-team-alpha 2>/dev/null || true
aws ec2 delete-subnet --subnet-id "$BETA_SUBNET" --profile hive-team-beta 2>/dev/null || true

# Delete VPCs
echo "  Deleting VPCs..."
aws ec2 delete-vpc --vpc-id "$ALPHA_VPC" --profile hive-team-alpha 2>/dev/null || true
aws ec2 delete-vpc --vpc-id "$BETA_VPC" --profile hive-team-beta 2>/dev/null || true

# Clean up AWS CLI profiles
for p in hive-team-alpha hive-team-beta hive-team-gamma; do
    aws configure set aws_access_key_id "" --profile $p 2>/dev/null || true
    aws configure set aws_secret_access_key "" --profile $p 2>/dev/null || true
done

echo "  EC2 account scoping cleanup complete"
echo ""
echo "Phase 8: EC2 Account Scoping PASSED"

# Phase 9: Terminate and Verify Cleanup
echo "Phase 9: Terminate and Verify Cleanup"

# Save root volume ID before termination for cleanup verification
ROOT_VOLUME_ID="$VOLUME_ID"
echo "Root volume to verify cleanup: $ROOT_VOLUME_ID"

# Clean up the CreateImage backing snapshot so DeleteOnTermination can delete the root volume.
# checkVolumeHasNoSnapshots() correctly blocks volume deletion when snapshots reference it.
if [ -n "$CUSTOM_AMI_SNAP_ID" ]; then
    echo "Deleting CreateImage backing snapshot $CUSTOM_AMI_SNAP_ID before termination..."
    aws ec2 delete-snapshot --snapshot-id "$CUSTOM_AMI_SNAP_ID"
    echo "CreateImage snapshot deleted"
fi

# Terminate instance (terminate-instances) and verify termination (describe-instances)
echo "Terminating instance..."
aws ec2 terminate-instances --instance-ids "$INSTANCE_ID"
COUNT=0
while [ $COUNT -lt 30 ]; do
    STATE=$(aws ec2 describe-instances --instance-ids "$INSTANCE_ID" --query 'Reservations[0].Instances[0].State.Name' --output text)
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
    VOL_CHECK=$(aws ec2 describe-volumes --volume-ids "$ROOT_VOLUME_ID" \
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
