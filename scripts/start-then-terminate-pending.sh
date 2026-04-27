#!/bin/sh
# Launch instance then terminate it while still in `pending` state.

set -e

export AWS_PROFILE=spinifex

ARCH=$(uname -m)
case "$ARCH" in
    x86_64)            IMAGE_NAME="ami-ubuntu-24.04-x86_64" ;;
    aarch64|arm64)     IMAGE_NAME="ami-ubuntu-24.04-arm64" ;;
    *)                 echo "Unknown arch $ARCH, default x86_64"; IMAGE_NAME="ami-ubuntu-24.04-x86_64" ;;
esac

INSTANCE_TYPE=$(aws ec2 describe-instance-types \
    --query "InstanceTypes[?contains(InstanceType, '.micro')].InstanceType | [0]" \
    --output text)
[ -z "$INSTANCE_TYPE" ] || [ "$INSTANCE_TYPE" = "None" ] && { echo "no micro type"; exit 1; }

AMI=$(aws ec2 describe-images --query "Images[?Name=='$IMAGE_NAME'].ImageId" --output text)
[ -z "$AMI" ] && { echo "no AMI $IMAGE_NAME"; exit 1; }

SUBNET=$(aws ec2 describe-subnets --query 'Subnets[0].SubnetId' --output text)
SG=$(aws ec2 describe-security-groups --query 'SecurityGroups[0].GroupId' --output text)
[ -z "$SUBNET" ] || [ "$SUBNET" = "None" ] && { echo "no subnet"; exit 1; }
[ -z "$SG" ]     || [ "$SG" = "None" ]     && { echo "no SG"; exit 1; }

echo "Launch: ami=$AMI type=$INSTANCE_TYPE subnet=$SUBNET sg=$SG"

INSTANCE_ID=$(aws ec2 run-instances \
    --image-id "$AMI" \
    --instance-type "$INSTANCE_TYPE" \
    --key-name spinifex-key \
    --security-group-ids "$SG" \
    --subnet-id "$SUBNET" \
    --count 1 \
    --query 'Instances[0].InstanceId' \
    --output text)

echo "Launched: $INSTANCE_ID — waiting for QEMU"

# Wait until QEMU process exists (LaunchInstance.StartInstance fired) but
# BEFORE TransitionState moves to running. Poll tight; terminate on first hit.
TIMEOUT=60
DEADLINE=$(( $(date +%s) + TIMEOUT ))
QEMU_PID=""
while [ "$(date +%s)" -lt "$DEADLINE" ]; do
    QEMU_PID=$(pgrep -f "qemu.*$INSTANCE_ID" | head -1 || true)
    [ -n "$QEMU_PID" ] && break
done

[ -z "$QEMU_PID" ] && { echo "ERR: QEMU never appeared"; exit 1; }

echo "QEMU pid=$QEMU_PID — terminate now"
aws ec2 terminate-instances --instance-ids "$INSTANCE_ID" \
    --query 'TerminatingInstances[0].[InstanceId,PreviousState.Name,CurrentState.Name]' \
    --output text
