#!/bin/bash

# Reset dev environment (will purge all data)

set -euo pipefail

echo "Shutting down services"

if ! ./scripts/stop-dev.sh; then
    echo "❌ Failed to stop services. Aborting reset to prevent data loss."
    exit 1
fi

echo "Removing data"

rm -rf ~/spinifex/predastore/*

rm -rf ~/spinifex/spinifex/*

rm -rf ~/spinifex/viperblock/*

rm -rf ~/spinifex/nats/*

rm -rf ~/spinifex/images/*

# Re-initialize platform (generates fresh credentials, certs, config, and updates ~/.aws/credentials)
echo "Re-initializing platform..."
./bin/spx admin init --force

# Generate SSH key if it doesn't exist
if [ ! -f ~/.ssh/spinifex-key.pub ]; then
    echo "Generating SSH key pair..."
    ssh-keygen -t ed25519 -f ~/.ssh/spinifex-key -N ""
fi

# Enable pprof for development
PPROF_ENABLED=1 PPROF_OUTPUT=/tmp/spinifex-vm.prof ./scripts/start-dev.sh --build

export AWS_PROFILE=spinifex

# Import SSH key
echo "Importing SSH key pair..."
aws ec2 import-key-pair --key-name "spinifex-key" --public-key-material fileb://~/.ssh/spinifex-key.pub

echo "Validating key pair imported"

aws ec2 describe-key-pairs

# Detect architecture
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    IMAGE_NAME="ubuntu-24.04-x86_64"
elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    IMAGE_NAME="ubuntu-24.04-arm64"
else
    echo "Warning: Unknown architecture $ARCH, defaulting to x86_64"
    IMAGE_NAME="ubuntu-24.04-x86_64"
fi

echo "Detected architecture: $ARCH, using image: $IMAGE_NAME"
./bin/spx admin images import --name "$IMAGE_NAME"

aws ec2 describe-images

echo "Reset successful, fresh AMI imported, proceed to creating instances"
