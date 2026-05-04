#!/bin/sh

# Query images, get Ubuntu image
export AWS_PROFILE=spinifex

# Detect architecture
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    IMAGE_NAME="ami-ubuntu-26.04-x86_64"
elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    IMAGE_NAME="ami-ubuntu-26.04-arm64"
else
    echo "Warning: Unknown architecture $ARCH, defaulting to x86_64"
    IMAGE_NAME="ami-ubuntu-26.04-x86_64"
fi

# Query available instance types from Spinifex and pick the first micro type
INSTANCE_TYPE=$(aws ec2 describe-instance-types --query "InstanceTypes[?contains(InstanceType, '.micro')].InstanceType | [0]" --output text)
if [ -z "$INSTANCE_TYPE" ] || [ "$INSTANCE_TYPE" = "None" ]; then
    echo "Error: Could not find available micro instance type"
    echo "Available instance types:"
    aws ec2 describe-instance-types --query "InstanceTypes[*].InstanceType" --output table
    exit 1
fi
echo "Using instance type: $INSTANCE_TYPE"

echo "Detected architecture: $ARCH"
echo "Looking for image with Name: $IMAGE_NAME"

# Query images and extract ImageId matching the IMAGE_NAME
SPINIFEX_AMI=$(aws ec2 describe-images --query "Images[?Name=='$IMAGE_NAME'].ImageId" --output text)

if [ -z "$SPINIFEX_AMI" ]; then
    echo "Error: Could not find image with Name '$IMAGE_NAME'"
    echo "Available images:"
    aws ec2 describe-images --query "Images[*].[Name,ImageId]" --output table
    exit 1
fi

export SPINIFEX_AMI=$SPINIFEX_AMI

echo "Found ImageId: $SPINIFEX_AMI"
export SPINIFEX_AMI

echo "Launching instance"

# Launch instance
aws ec2 run-instances \
  --image-id "$SPINIFEX_AMI" \
  --instance-type "$INSTANCE_TYPE" \
  --key-name spinifex-key \
  --security-group-ids sg-0123456789abcdef0 \
  --subnet-id subnet-6e7f829e \
  --count 1

