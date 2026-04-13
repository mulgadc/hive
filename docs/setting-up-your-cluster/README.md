---
title: "Setting Up Your Cluster"
description: "Import an AMI, create a VPC with a public subnet, and launch your first EC2 instance on Spinifex."
category: "Getting Started"
tags:
  - setup
  - vpc
  - ec2
  - quickstart
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "AWS EC2 CLI Reference"
    url: "https://docs.aws.amazon.com/cli/latest/reference/ec2/"
---

# Setting Up Your Cluster

> Import an AMI, create a VPC with a public subnet, and launch your first instance.

## Table of Contents

- [1. Import an AMI](#1-import-an-ami)
- [2. Create an SSH Key](#2-create-an-ssh-key)
- [3. Create a VPC and Public Subnet](#3-create-a-vpc-and-public-subnet)
- [4. Launch an Instance](#4-launch-an-instance)
- [5. Connect via SSH](#5-connect-via-ssh)
- [6. Managing Instances](#6-managing-instances)
- [Troubleshooting](#troubleshooting)

---

## Overview

This guide assumes Spinifex is already installed and running. If not, follow one of the installation guides first:
- [Binary Install (Single Node)](/docs/install)
- [Multi-Node Install](/docs/install-multi-node)
- [Source Install](/docs/install-source)

## Instructions

## Prerequisites

Ensure the AWS profile is set:

```bash
export AWS_PROFILE=spinifex
```

## 1. Import an AMI

List available images and import one matching your architecture:

```bash
spx admin images list
```

```
NAME                 | DISTRO | VERSION | ARCH   | BOOT
debian-12-arm64      | debian | 12      | arm64  | bios
debian-12-x86_64     | debian | 12      | x86_64 | bios
ubuntu-24.04-arm64   | ubuntu | 24.04   | arm64  | bios
ubuntu-24.04-x86_64  | ubuntu | 24.04   | x86_64 | bios
```

Import an image:

```bash
spx admin images import --name ubuntu-24.04-x86_64
```

Or import a local image file:

```bash
spx admin images import --file ~/images/ubuntu-24.04.img --distro ubuntu --version 24.04 --arch x86_64
```

Verify the import and note the AMI ID:

```bash
aws ec2 describe-images
```

## 2. Create an SSH Key

### Option A: Import an existing key

```bash
aws ec2 import-key-pair \
  --key-name "spinifex-key" \
  --public-key-material fileb://~/.ssh/id_rsa.pub
```

### Option B: Create a new key pair

```bash
aws ec2 create-key-pair --key-name spinifex-key \
  | jq -r '.KeyMaterial | rtrimstr("\n")' > ~/.ssh/spinifex-key

chmod 600 ~/.ssh/spinifex-key
ssh-keygen -y -f ~/.ssh/spinifex-key > ~/.ssh/spinifex-key.pub
```

Verify:

```bash
aws ec2 describe-key-pairs
```

## 3. Create a VPC and Public Subnet

### Create a VPC

```bash
VPC_ID=$(aws ec2 create-vpc --cidr-block 10.200.0.0/16 \
  --query 'Vpc.VpcId' --output text)

echo "VPC: $VPC_ID"
```

### Create an Internet Gateway

An Internet Gateway enables instances in public subnets to reach the internet and be reachable from the LAN/WAN.

```bash
IGW_ID=$(aws ec2 create-internet-gateway \
  --query 'InternetGateway.InternetGatewayId' --output text)

aws ec2 attach-internet-gateway \
  --internet-gateway-id $IGW_ID \
  --vpc-id $VPC_ID

echo "IGW: $IGW_ID (attached to $VPC_ID)"
```

### Create a Public Subnet

A public subnet auto-assigns a routable IP to each instance, making it directly reachable from your network.

```bash
SUBNET_ID=$(aws ec2 create-subnet \
  --vpc-id $VPC_ID \
  --cidr-block 10.200.1.0/24 \
  --query 'Subnet.SubnetId' --output text)

# Enable auto-assign public IP
aws ec2 modify-subnet-attribute \
  --subnet-id $SUBNET_ID \
  --map-public-ip-on-launch

echo "Subnet: $SUBNET_ID (public)"
```

### Verify

```bash
aws ec2 describe-vpcs --vpc-ids $VPC_ID
aws ec2 describe-subnets --subnet-ids $SUBNET_ID
```

## 4. Launch an Instance

Query available instance types for your hardware:

```bash
aws ec2 describe-instance-types
```

Launch an instance in the public subnet, note this will select an instance type with 2 VCPU and 1024MB of memory.

> **Note:** Replace `AMI_NAME` with the previously imported image above with the `ami-` prefix.

```bash
AMI_NAME="ami-ubuntu-24.04-x86_64"

AMI_ID=$(aws ec2 describe-images \
  --filters "Name=name,Values=$AMI_NAME" \
  --query 'Images | sort_by(@, &CreationDate) | [-1].ImageId' \
  --output text)

INSTANCE_TYPE=$(aws ec2 describe-instance-types \
  --query "sort_by(InstanceTypes[?VCpuInfo.DefaultVCpus==\`2\` && MemoryInfo.SizeInMiB>=\`1024\`], &MemoryInfo.SizeInMiB)[0].InstanceType" \
  --output text)

INSTANCE_ID=$(aws ec2 run-instances \
  --image-id $AMI_ID \
  --instance-type $INSTANCE_TYPE \
  --key-name spinifex-key \
  --subnet-id $SUBNET_ID \
  --count 1 \
  --query 'Instances[0].InstanceId' --output text)

echo "Instance: $INSTANCE_ID"
```

Wait for the instance to reach `running` state:

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID \
  --query 'Reservations[0].Instances[0].[State.Name, PrivateIpAddress, PublicIpAddress]' \
  --output text
```

Expected output:

```
running    10.200.1.4    192.168.1.155
```

The instance has both a private IP (VPC overlay) and a public IP (from your external pool, routable on your network).

## 5. Connect via SSH

SSH directly to the instance's public IP:

```bash
PUBLIC_IP=$(aws ec2 describe-instances --instance-ids $INSTANCE_ID \
  --query 'Reservations[0].Instances[0].PublicIpAddress' --output text)

ssh -i ~/.ssh/spinifex-key ec2-user@$PUBLIC_IP
```

> **Note:** cloud-init takes 30-60 seconds to configure the instance. If SSH is refused, wait and retry.

Once connected, verify the instance has internet access:

```bash
curl -s http://ifconfig.me
```

This should return the instance's public IP or the gateway's SNAT address.

## 6. Managing Instances

### Stop

```bash
aws ec2 stop-instances --instance-ids $INSTANCE_ID
```

### Start

```bash
aws ec2 start-instances --instance-ids $INSTANCE_ID
```

### Terminate

```bash
aws ec2 terminate-instances --instance-ids $INSTANCE_ID
```

### Console Output

View the instance's serial console log (useful for debugging boot issues):

```bash
aws ec2 get-console-output --instance-id $INSTANCE_ID \
  --query 'Output' --output text
```

### Multi-Node: Check Instance Placement

On a multi-node cluster, instances are distributed across nodes:

```bash
spx get vms
```

## Additional Options

### Private Subnets (No Public IP)

Create a subnet without `--map-public-ip-on-launch`:

```bash
PRIVATE_SUBNET=$(aws ec2 create-subnet \
  --vpc-id $VPC_ID \
  --cidr-block 10.200.2.0/24 \
  --query 'Subnet.SubnetId' --output text)
```

Instances in private subnets get a private IP only. They can still reach the internet via SNAT through the VPC router, but are not directly reachable from your network.

### Multiple Accounts

Create isolated accounts with their own resources:

```bash
spx admin account create --name myteam
export AWS_PROFILE=spinifex-myteam
```

### Web UI

The Spinifex web interface is available at `https://localhost:3000` when services are running.

## Troubleshooting

### Instance Stuck in Pending

```bash
cat ~/spinifex/logs/spinifex.log
aws ec2 describe-images
```

### SSH Connection Refused

cloud-init needs 30-60 seconds after boot. Check instance state:

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID
```

### No Public IP Assigned

Verify the subnet has `MapPublicIpOnLaunch` enabled:

```bash
aws ec2 describe-subnets --subnet-ids $SUBNET_ID
```

If `MapPublicIpOnLaunch` is false:

```bash
aws ec2 modify-subnet-attribute --subnet-id $SUBNET_ID --map-public-ip-on-launch
```

Also verify an Internet Gateway is attached to the VPC:

```bash
aws ec2 describe-internet-gateways
```

### Instance Has No Internet Access

Check the VPC router's NAT rules (from the host):

```bash
sudo ovn-nbctl lr-nat-list $(sudo ovn-nbctl lr-list | awk '{print $2}' | head -1)
```

Verify the default route exists:

```bash
sudo ovn-nbctl lr-route-list $(sudo ovn-nbctl lr-list | awk '{print $2}' | head -1)
```
