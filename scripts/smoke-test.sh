#!/bin/bash
# smoke-test.sh — Bare-bones smoke test for a running Spinifex node.
#
# Imports an SSH keypair, imports the Ubuntu AMI, and launches a single
# instance to verify the platform is functional end-to-end.
#
# Assumes services are running and AWS_PROFILE=spinifex is configured
# by a prior 'spx admin init'.
set -euo pipefail

export AWS_PROFILE=spinifex

# --- Wait for EC2 daemon to subscribe to NATS ---
# Port 3000 (UI) becomes ready before the daemon finishes initialising.
# Poll describe-key-pairs until it doesn't return InternalError.
echo "==> Waiting for EC2 daemon to be ready"
DAEMON_TIMEOUT=60
DAEMON_ELAPSED=0
while [ $DAEMON_ELAPSED -lt $DAEMON_TIMEOUT ]; do
    if aws ec2 describe-key-pairs --output text >/dev/null 2>&1; then
        break
    fi
    sleep 2
    DAEMON_ELAPSED=$((DAEMON_ELAPSED + 2))
done
if [ $DAEMON_ELAPSED -ge $DAEMON_TIMEOUT ]; then
    echo "❌ EC2 daemon not ready after ${DAEMON_TIMEOUT}s"
    exit 1
fi
echo "   EC2 daemon ready after ${DAEMON_ELAPSED}s"

# --- SSH key ---
SSH_KEY="$HOME/.ssh/spinifex-key"
if [ ! -f "$SSH_KEY.pub" ]; then
    echo "==> Generating SSH key pair"
    mkdir -p "$HOME/.ssh"
    ssh-keygen -t ed25519 -f "$SSH_KEY" -N ""
fi

echo "==> Importing SSH key"
aws ec2 import-key-pair --key-name spinifex-key \
    --public-key-material "fileb://$SSH_KEY.pub"
aws ec2 describe-key-pairs

# --- Import AMI ---
echo "==> Importing AMI"
LOCAL_IMAGE="$HOME/images/ubuntu-24.04.img"
ARCH=$(uname -m)
case "$ARCH" in
    x86_64)        IMG_ARCH="x86_64"; IMAGE_NAME="ubuntu-24.04-x86_64" ;;
    aarch64|arm64) IMG_ARCH="arm64";  IMAGE_NAME="ubuntu-24.04-arm64"  ;;
    *)
        echo "  Warning: unknown arch $ARCH, defaulting to x86_64"
        IMG_ARCH="x86_64"; IMAGE_NAME="ubuntu-24.04-x86_64"
        ;;
esac

if [ -f "$LOCAL_IMAGE" ]; then
    echo "  Using local image: $LOCAL_IMAGE"
    sudo /usr/local/bin/spx admin images import \
        --file "$LOCAL_IMAGE" --distro ubuntu --version 24.04 --arch "$IMG_ARCH"
else
    echo "  Downloading image: $IMAGE_NAME"
    sudo /usr/local/bin/spx admin images import --name "$IMAGE_NAME"
fi

# --- Launch smoke-test instance ---
echo "==> Launching smoke-test instance"
if grep -q 'AuthenticAMD' /proc/cpuinfo; then
    INSTANCE_TYPE="t3a.small"
else
    INSTANCE_TYPE="t3.small"
fi

AMI_ID=$(aws ec2 describe-images --query "Images[0].ImageId" --output text)
if [ -z "$AMI_ID" ] || [ "$AMI_ID" = "None" ]; then
    echo "❌ No AMI found"
    exit 1
fi

SUBNET_ID=$(aws ec2 describe-subnets --query "Subnets[0].SubnetId" --output text)
if [ -z "$SUBNET_ID" ] || [ "$SUBNET_ID" = "None" ]; then
    echo "❌ No subnet found"
    exit 1
fi

echo "  AMI: $AMI_ID  type: $INSTANCE_TYPE  subnet: $SUBNET_ID"

INSTANCE_ID=$(aws ec2 run-instances \
    --image-id "$AMI_ID" \
    --instance-type "$INSTANCE_TYPE" \
    --key-name spinifex-key \
    --subnet-id "$SUBNET_ID" \
    --count 1 \
    --query 'Instances[0].InstanceId' --output text)

echo "✅ Smoke test passed — instance $INSTANCE_ID launched ($INSTANCE_TYPE)"
