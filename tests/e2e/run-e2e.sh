#!/bin/bash
set -e

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

./bin/hive admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1 --force

# Start all services
# Ensure logs directory exists for start-dev.sh
mkdir -p ~/hive/logs
HIVE_SKIP_BUILD=true ./scripts/start-dev.sh

# Wait for health checks on https://localhost:9999 (AWS Gateway)
echo "Waiting for AWS Gateway..."
MAX_RETRIES=30
COUNT=0
# Using -k because of self-signed certs
until curl -k -s https://localhost:9999 > /dev/null || [ $COUNT -eq $MAX_RETRIES ]; do
    echo "Waiting for gateway... ($COUNT/$MAX_RETRIES)"
    sleep 2
    COUNT=$((COUNT + 1))
done

if [ $COUNT -eq $MAX_RETRIES ]; then
    echo "Gateway failed to start"
    exit 1
fi

# Define common AWS CLI args
# Use --endpoint-url and --no-verify-ssl to be safe in the container environment
# Suppress InsecureRequestWarning from urllib3
export PYTHONWARNINGS="ignore:Unverified HTTPS request"
AWS_EC2="aws --endpoint-url https://localhost:9999 --no-verify-ssl ec2"

# Phase 2: Discovery & Metadata
echo "Phase 2: Discovery & Metadata"
# Verify describe-regions (just ensure it returns at least one region)
$AWS_EC2 describe-regions | jq -e '.Regions | length > 0'

# Discover available instance types from Hive
# Hive generates these based on the host CPU (e.g., m7i.micro, m8g.small, etc.)
echo "Discovering available instance types..."
AVAILABLE_TYPES=$($AWS_EC2 describe-instance-types --query 'InstanceTypes[*].InstanceType' --output text)
echo "Available instance types: $AVAILABLE_TYPES"

# Pick the first one as our primary test type
INSTANCE_TYPE=$(echo $AVAILABLE_TYPES | awk '{print $1}')
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

# Import a reliable Ubuntu image and capture the AMI ID from the output
echo "Importing image $IMAGE_NAME..."
echo "May appear stalled here, just takes a while to import..."
IMPORT_LOG=$(./bin/hive admin images import --name "$IMAGE_NAME" --force)
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
INSTANCE_ID=$($AWS_EC2 run-instances --image-id "$AMI_ID" --instance-type "$INSTANCE_TYPE" --key-name test-key-1 --query 'Instances[0].InstanceId' --output text)
if [ -z "$INSTANCE_ID" ] || [ "$INSTANCE_ID" == "None" ]; then
    echo "Failed to launch instance"
    exit 1
fi
echo "Launched Instance ID: $INSTANCE_ID"

# Poll until state is running (describe-instances)
echo "Polling for instance running state..."
COUNT=0
STATE="unknown"
while [ $COUNT -lt 60 ]; do
    # Capture full output to check if instance even exists in the response
    DESCRIBE_OUTPUT=$($AWS_EC2 describe-instances --instance-ids "$INSTANCE_ID") || {
        echo "⚠️  Gateway request failed, retrying... ($COUNT/60)"
        sleep 5
        COUNT=$((COUNT + 1))
        continue
    }

    if [ -z "$DESCRIBE_OUTPUT" ]; then
        echo "⚠️  Gateway returned empty response, retrying..."
        sleep 5
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

    sleep 5
    COUNT=$((COUNT + 1))
done

if [ "$STATE" != "running" ]; then
    echo "Instance failed to reach running state"
    exit 1
fi

# Verify root volume attached to the instance (describe-volumes)
VOLUME_ID=$($AWS_EC2 describe-volumes --query 'Volumes[0].VolumeId' --output text)
if [ -z "$VOLUME_ID" ] || [ "$VOLUME_ID" == "None" ]; then
    echo "Failed to find volume for instance $INSTANCE_ID"
    exit 1
fi
echo "Volume ID: $VOLUME_ID"

# Stop instance (stop-instances) and verify transition to stopped (describe-instances)
echo "Stopping instance..."
$AWS_EC2 stop-instances --instance-ids "$INSTANCE_ID"
COUNT=0
while [ $COUNT -lt 60 ]; do
    STATE=$($AWS_EC2 describe-instances --instance-ids "$INSTANCE_ID" --query 'Reservations[0].Instances[0].State.Name' --output text)
    echo "Instance state: $STATE"
    if [ "$STATE" == "stopped" ]; then
        break
    fi
    sleep 5
    COUNT=$((COUNT + 1))
done

if [ "$STATE" != "stopped" ]; then
    echo "Instance failed to reach stopped state"
    exit 1
fi

# Start instance (start-instances) and verify transition back to running (describe-instances)
echo "Starting instance..."
$AWS_EC2 start-instances --instance-ids "$INSTANCE_ID"
COUNT=0
while [ $COUNT -lt 30 ]; do
    STATE=$($AWS_EC2 describe-instances --instance-ids "$INSTANCE_ID" --query 'Reservations[0].Instances[0].State.Name' --output text)
    echo "Instance state: $STATE"
    if [ "$STATE" == "running" ]; then
        break
    fi
    sleep 5
    COUNT=$((COUNT + 1))
done

if [ "$STATE" != "running" ]; then
    echo "Instance failed to reach running state after restart"
    exit 1
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
    sleep 5
    COUNT=$((COUNT + 1))
done

echo "E2E Test Completed Successfully"
exit 0
