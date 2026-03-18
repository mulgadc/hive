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
- [Troubleshooting](#troubleshooting)

---

## Overview

Spinifex is an open-source infrastructure platform that brings core AWS services — EC2, VPC, EBS, and S3 — to bare-metal, edge, and on-prem environments.

This guide installs Spinifex on a single server using the binary installer. For multi-server clusters, see [Multi-Node Installation](/docs/installing-spinifex-multi-node). To build from source, see [Source Install](/docs/source-install).

**Supported Operating Systems:**
- Ubuntu 22.04 / 24.04 / 25.10
- Debian 12.13

**What gets installed:**
- Spinifex daemon and CLI
- QEMU/KVM for virtual machine management
- OVN/Open vSwitch for VPC networking
- Predastore (S3-compatible object storage)
- Viperblock (EBS-compatible block storage)
- AWS CLI v2

## Instructions

## Step 1. Install Spinifex

```bash
curl https://install.mulgadc.com | bash
```

The installer downloads the Spinifex binary and bootstraps all dependencies (QEMU, OVN/OVS, AWS CLI).

## Step 2. Initialize a region

Create your first region and availability zone, replace `ap-southeast-2` with your desired region:

```bash
spx admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1
```

## Step 3. Trust the CA certificate

Spinifex generates a local CA during initialization. Add it to your system trust store:

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

## Step 4. Start services

```bash
sudo systemctl start spinifex.target
```

Set the AWS profile to use the default admin account:

```bash
export AWS_PROFILE=spinifex
```

## Step 5. Import an AMI

List available images and import one matching your architecture:

```bash
spx admin images list
spx admin images import --name debian-12-arm64
```

## Step 6. Create a VPC and launch an instance

```bash
aws ec2 create-vpc --cidr-block 10.200.0.0/16
export SPINIFEX_VPC="vpc-XXX"

aws ec2 create-subnet --vpc-id $SPINIFEX_VPC --cidr-block 10.200.1.0/24
export SPINIFEX_SUBNET="subnet-XXX"

aws ec2 import-key-pair --key-name "spinifex-key" --public-key-material fileb://~/.ssh/id_rsa.pub

aws ec2 run-instances \
  --image-id $SPINIFEX_AMI \
  --instance-type t3.small \
  --key-name spinifex-key \
  --subnet-id $SPINIFEX_SUBNET \
  --count 1
```

## Step 7. Connect via SSH

Find the forwarded SSH port and connect:

```bash
ps auxw | grep hostfwd
# Look for: hostfwd=tcp:127.0.0.1:<port>-:22

ssh -i ~/.ssh/spinifex-key ec2-user@127.0.0.1 -p <port>
```

## Troubleshooting

## spx command not found

The binary is not in your PATH. Run the installer again or add the install directory to your PATH:

```bash
export PATH=$PATH:~/spinifex/bin/
```

Add this to your `~/.bashrc` or `~/.zshrc` for persistence.

## OVN services not starting

Check if the OVN controller is active:

```bash
sudo systemctl is-active ovn-controller
sudo ovn-sbctl show
```

If inactive, re-run the OVN setup that the installer performs. Check system logs for details:

```bash
journalctl -u ovn-controller --no-pager -n 20
```

## CA certificate not trusted

AWS CLI will reject HTTPS connections if the Spinifex CA is not trusted. Re-add the certificate:

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

Verify it was added:

```bash
ls -la /usr/local/share/ca-certificates/spinifex-ca.crt
```

## Instance stuck in pending

Check the Spinifex daemon logs for errors:

```bash
ls ~/spinifex/logs/
cat ~/spinifex/logs/daemon.log
```

Verify the AMI was imported successfully:

```bash
aws ec2 describe-images
```

## SSH connection refused

cloud-init takes 30-60 seconds to configure the instance after boot. Wait and retry.

Verify the SSH port forwarding is active:

```bash
ps auxw | grep hostfwd
```

If no `hostfwd` entry appears, the instance may not have started correctly. Check instance state:

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID
```
