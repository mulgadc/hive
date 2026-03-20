---
title: "Installing Spinifex"
description: "Install Spinifex on a single server using the binary installer. Get a working region with EC2, VPC, EBS, and S3 in minutes."
category: "Getting Started"
tags:
  - install
  - single node
  - quickstart
badge: quickstart
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "Predastore (S3)"
    url: "https://github.com/mulgadc/predastore"
  - title: "Viperblock (EBS)"
    url: "https://github.com/mulgadc/viperblock"
---

# Installing Spinifex

> Install Spinifex on a single server using the binary installer.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [External Networking](#external-networking)
- [Troubleshooting](#troubleshooting)

---

## Overview

Spinifex is an open-source infrastructure platform that brings core AWS services — EC2, VPC, EBS, and S3 — to bare-metal, edge, and on-prem environments.

This guide installs Spinifex on a single server using the binary installer. For multi-server clusters, see [Multi-Node Installation](/docs/installing-spinifex-multi-node). To build from source, see [Source Install](/docs/source-install).

**Supported Operating Systems:**

- Ubuntu 22.04 / 24.04 / 25.10
- Debian 12 / 13

**What gets installed:**

- Spinifex daemon and CLI
- QEMU/KVM for virtual machine management
- OVN/Open vSwitch for VPC networking
- Predastore (S3-compatible object storage)
- Viperblock (EBS-compatible block storage)
- AWS CLI v2

**Network requirements:**

- Minimum 1 NIC (single-NIC uses macvlan for external bridge, SSH-safe)
- 2 NICs recommended (management + WAN)
- Network auto-detection configures everything by default

## Instructions

### Step 1. Install Spinifex

```bash
curl https://install.mulgadc.com | bash
```

The installer downloads the Spinifex binary and bootstraps all dependencies (QEMU, OVN/OVS, AWS CLI).

### Step 2. Initialize a region

Create your first region and availability zone:

```bash
spx admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1
```

This auto-detects your network topology and configures external networking:

```
🔍 Detected network topology:

  Interface      IP                 Subnet               Gateway          Role
  enp0s3         192.168.1.31       192.168.0.0/23       192.168.1.1      WAN
  enp0s5         10.13.7.1          10.13.7.0/24         —                LAN

  Mode: multi-NIC (1 LAN + 1 WAN)

📡 External networking: nat
  WAN interface: enp0s3
  Gateway IP:    DHCP (obtained during OVN setup)
  VMs get:       outbound internet via SNAT
```

The init output shows the exact `setup-ovn.sh` command to run next — copy it from the output.

> **Note:** Save the admin credentials printed during init. They will not be shown again.

### Step 3. Trust the CA certificate

Spinifex generates a local CA during initialization. Add it to your system trust store so the AWS CLI trusts Spinifex's HTTPS endpoints:

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

### Step 4. Setup OVN networking

Run the `setup-ovn.sh` command printed by `admin init`. For a typical single-node setup:

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh --management --external-bridge --external-iface=enp0s3 --dhcp
```

Replace `enp0s3` with your WAN interface name (shown in the init output). The `--dhcp` flag automatically obtains an IP from your router for OVN's gateway — no manual IP reservation needed.

> **Single-NIC hosts:** If you only have one NIC, `admin init` adds `--single-nic` to the command. This creates a macvlan sub-interface so OVN gets its own IP without disrupting yours.

### Step 5. Start services

```bash
sudo systemctl start spinifex.target
```

Set the AWS profile:

```bash
export AWS_PROFILE=spinifex
```

Add this to your `~/.bashrc` for persistence.

### Step 6. Import an AMI

List available images and import one matching your architecture:

```bash
spx admin images list
spx admin images import --name debian-12-arm64
```

Find the imported AMI ID:

```bash
aws ec2 describe-images --query 'Images[0].ImageId' --output text
```

### Step 7. Create a VPC and launch an instance

```bash
# Create a VPC
VPC_ID=$(aws ec2 create-vpc --cidr-block 10.200.0.0/16 \
  --query 'Vpc.VpcId' --output text)

# Create a subnet
SUBNET_ID=$(aws ec2 create-subnet --vpc-id $VPC_ID \
  --cidr-block 10.200.1.0/24 \
  --query 'Subnet.SubnetId' --output text)

# Import your SSH key
aws ec2 import-key-pair --key-name "spinifex-key" \
  --public-key-material fileb://~/.ssh/id_rsa.pub

# Get the AMI ID
AMI_ID=$(aws ec2 describe-images --query 'Images[0].ImageId' --output text)

# Launch an instance
aws ec2 run-instances \
  --image-id $AMI_ID \
  --instance-type t3.small \
  --key-name spinifex-key \
  --subnet-id $SUBNET_ID \
  --count 1
```

### Step 8. Connect via SSH

Find the instance's private IP and connect through the VPC network:

```bash
INSTANCE_ID=$(aws ec2 describe-instances \
  --query 'Reservations[0].Instances[0].InstanceId' --output text)

PRIVATE_IP=$(aws ec2 describe-instances --instance-ids $INSTANCE_ID \
  --query 'Reservations[0].Instances[0].PrivateIpAddress' --output text)

ssh -i ~/.ssh/id_rsa ec2-user@$PRIVATE_IP
```

> **Note:** cloud-init takes 30-60 seconds to configure the instance after boot. If SSH is refused, wait and retry.

---

## External Networking

By default, `admin init` auto-detects your network and enables outbound internet for VMs via SNAT. Three tiers are available:

| Tier | Flags | What VMs get |
|---|---|---|
| **Auto** (default) | None | Outbound internet (SNAT via DHCP) |
| **Pool** | `--external-pool=start-end` | Public IPs + inbound (1:1 NAT) |
| **Disabled** | `--no-external` | Overlay only, no internet |

### Enabling public IPs for VMs

To give VMs routable public IPs, reserve an IP range from your network and pass it during init:

```bash
spx admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1 \
  --external-pool=192.168.1.150-192.168.1.250
```

Then exclude that range from your router's DHCP scope (e.g., set your router's DHCP to end at 192.168.1.149).

---

## Troubleshooting

### spx command not found

The binary is not in your PATH. Run the installer again or add the install directory:

```bash
export PATH=$PATH:/usr/local/bin
```

### OVN services not starting

Check if the OVN controller is active:

```bash
sudo systemctl is-active ovn-controller
sudo ovn-sbctl show
```

If inactive, re-run the OVN setup. Check system logs:

```bash
journalctl -u ovn-controller --no-pager -n 20
```

### CA certificate not trusted

AWS CLI will reject HTTPS connections if the Spinifex CA is not trusted:

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

### Instance stuck in pending

Check the Spinifex daemon logs:

```bash
cat ~/spinifex/logs/spinifex.log
```

Verify the AMI was imported:

```bash
aws ec2 describe-images
```

### SSH connection refused

cloud-init takes 30-60 seconds after boot. Wait and retry. Check instance state:

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID
```
