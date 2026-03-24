---
title: "Installing Spinifex"
description: "Install Spinifex on a single server using the binary installer."
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
- Debian 12 / 13

**What gets installed:**

- Spinifex daemon and CLI
- QEMU/KVM for virtual machine management
- OVN/Open vSwitch for VPC networking
- Predastore (S3-compatible object storage)
- Viperblock (EBS-compatible block storage)
- AWS CLI v2

## Instructions

### Step 1. Install Spinifex

```bash
curl https://install.mulgadc.com | bash
```

The installer downloads the Spinifex binary and bootstraps all dependencies (QEMU, OVN/OVS, AWS CLI).

### Step 2. Initialize

```bash
sudo spx admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1
```

This auto-detects your network topology, generates configuration and TLS certificates, installs the CA into the system trust store, and configures AWS CLI credentials. Save the admin credentials printed during init — they will not be shown again.

### Step 3. Setup OVN networking

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh --management --external-bridge --external-iface=enp0s3 --dhcp
```

Replace `enp0s3` with your WAN interface.

### Step 4. Start services

```bash
sudo systemctl start spinifex.target
export AWS_PROFILE=spinifex
```

### Step 5. Verify

```bash
aws ec2 describe-instance-types
```

If this returns a list of available instance types, your installation is working.

**Congratulations! Spinifex is installed.**

Continue to [Setting Up Your Cluster](/docs/setting-up-your-cluster) to import an AMI, create a VPC, and launch your first instance.

---

## Troubleshooting

### spx command not found

```bash
export PATH=$PATH:/usr/local/bin
```

### CA certificate not trusted

`sudo spx admin init` installs the CA automatically. If you need to re-install it manually:

```bash
sudo cp /etc/spinifex/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

### OVN services not starting

```bash
sudo systemctl is-active ovn-controller
journalctl -u ovn-controller --no-pager -n 20
```
