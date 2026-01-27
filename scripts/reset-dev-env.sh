#!/bin/sh

# Reset dev environment (will purge all data)

echo "Shutting down services"

./scripts/stop-dev.sh

echo "Removing data"

rm -rf ~/hive/predastore/

rm -rf ~/hive/hive/*

rm -rf ~/hive/viperblock/*

rm -rf ~/hive/nats/*

# Enable pprof for development
PPROF_ENABLED=1 PPROF_OUTPUT=/tmp/hive-vm.prof ./scripts/start-dev.sh

# Follow INSTALL.md steps
echo "Importing AMI"

export AWS_PROFILE=hive

# Step 1 SSH key
aws ec2 import-key-pair --key-name "hive-key" --public-key-material fileb://~/.ssh/hive-key.pub

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
./bin/hive admin images import --name "$IMAGE_NAME"

aws ec2 describe-images

echo "Reset successful, fresh AMI imported, proceed to creating instances"
