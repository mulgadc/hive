#!/bin/sh

# Query images, get Ubuntu image
export AWS_PROFILE=hive

# Detect architecture
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ]; then
    IMAGE_NAME="ami-ubuntu-24.04-x86_64"
    INSTANCE_TYPE="t3.micro"
elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
    IMAGE_NAME="ami-ubuntu-24.04-arm64"
    INSTANCE_TYPE="t4g.micro"
else
    echo "Warning: Unknown architecture $ARCH, defaulting to x86_64"
    IMAGE_NAME="ami-ubuntu-24.04-x86_64"
    INSTANCE_TYPE="t3.micro"
fi

echo "Detected architecture: $ARCH"
echo "Looking for image with Name: $IMAGE_NAME"

# Query images and extract ImageId matching the IMAGE_NAME
HIVE_AMI=$(aws ec2 describe-images --query "Images[?Name=='$IMAGE_NAME'].ImageId" --output text)

if [ -z "$HIVE_AMI" ]; then
    echo "Error: Could not find image with Name '$IMAGE_NAME'"
    echo "Available images:"
    aws ec2 describe-images --query "Images[*].[Name,ImageId]" --output table
    exit 1
fi

export HIVE_AMI=$HIVE_AMI

echo "Found ImageId: $HIVE_AMI"
export HIVE_AMI

echo "Launching instance"

# Launch instance
aws ec2 run-instances \
  --image-id "$HIVE_AMI" \
  --instance-type "$INSTANCE_TYPE" \
  --key-name hive-key \
  --security-group-ids sg-0123456789abcdef0 \
  --subnet-id subnet-6e7f829e \
  --count 1

