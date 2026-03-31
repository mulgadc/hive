---
title: "Installing Spinifex"
description: "Install Spinifex on a single server using the binary installer."
category: "Getting Started"
tags:
  - install
  - single node
  - quickstart
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

**What Gets Installed:**

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

## Step 2. Setup OVN Networking

If your WAN interface is already a bridge (e.g. `br-wan`), setup-ovn.sh auto-detects it:

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh --management
```

If your WAN is a physical NIC, choose one:

```bash
# Dedicated WAN NIC (not your SSH connection):
sudo /usr/local/share/spinifex/setup-ovn.sh --management --wan-bridge=br-wan --wan-iface=eth1

# Single-NIC host (SSH-safe macvlan):
sudo /usr/local/share/spinifex/setup-ovn.sh --management --macvlan --wan-iface=enp0s3
```

**Separating VPC tunnel traffic from WAN:** If you want Geneve tunnel traffic (inter-VM east-west) to use a dedicated interface instead of the WAN IP, add `--encap-ip=<IP>` to specify the tunnel endpoint address:

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh --management --encap-ip=10.0.0.1
```

This is recommended for production and required in multi-node deployments. See [Multi-Node Installation](/docs/installing-spinifex-multi-node) for details.

## Step 3. Initialize

```bash
sudo spx admin init --node node1 --nodes 1
```

This auto-detects your network topology, generates configuration and TLS certificates, installs the CA into the system trust store, and configures AWS CLI credentials. Save the admin credentials printed during init — they will not be shown again.

## Step 4. Start Services

```bash
sudo systemctl start spinifex.target
```

## Step 5. Verify

```bash
export AWS_PROFILE=spinifex
aws ec2 describe-instance-types
```

If this returns a list of available instance types, your installation is working.

**Congratulations! Spinifex is installed.**

Continue to [Setting Up Your Cluster](/docs/setting-up-your-cluster) to import an AMI, create a VPC, and launch your first instance.

## Troubleshooting

### spx Command Not Found

```bash
export PATH=$PATH:/usr/local/bin
```

### CA Certificate Not Trusted

`sudo spx admin init` installs the CA automatically. If you need to re-install it manually:

```bash
sudo cp /etc/spinifex/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

### OVN Services Not Starting

```bash
sudo systemctl is-active ovn-controller
journalctl -u ovn-controller --no-pager -n 20
```
