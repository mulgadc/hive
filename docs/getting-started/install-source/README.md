---
title: "Source Install"
description: "Build Spinifex from source for development, custom builds, or contributing."
category: "Getting Started"
tags:
  - install
  - source
  - development
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "Go Downloads"
    url: "https://go.dev/dl/"
---

# Source Installation

> Build Spinifex from source for development, custom builds, or contributing.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

This guide builds Spinifex from source. For production deployments, the [binary installer](/docs/install) is recommended.

**Requirements:**

- Ubuntu 22.04+ / Debian 12+
- Go 1.26.2+
- GCC, make, pkg-config
- QEMU/KVM, OVN/Open vSwitch, AWS CLI v2, dhcpcd-base

## Instructions

## Step 1. Install Dependencies

```bash
mkdir -p ~/Development/mulga && cd ~/Development/mulga
git clone https://github.com/mulgadc/spinifex.git
sudo make -C spinifex quickinstall
export PATH=$PATH:/usr/local/go/bin
```

## Step 2. Clone and Build

```bash
cd spinifex
./scripts/clone-deps.sh
./scripts/dev-setup.sh
```

Confirm `./bin/spx` exists.

## Step 3. Setup OVN

If your WAN interface is already a bridge (e.g. `br-wan`), setup-ovn.sh auto-detects it:

```bash
./scripts/setup-ovn.sh --management
```

If your WAN is a physical NIC, choose one:

```bash
# Dedicated WAN NIC (not your SSH connection):
./scripts/setup-ovn.sh --management --wan-bridge=br-wan --wan-iface=eth1

# Single-NIC host (SSH-safe macvlan):
./scripts/setup-ovn.sh --management --macvlan --wan-iface=enp0s3
```

## Step 4. Development initialisation

```bash
./scripts/dev-install.sh
```

Note, this will bootstrap a single node development environment

## Step 5. Start services

```bash
sudo systemctl status spinifex.target
```

Start all services

## Step 6. Verify Installation

```bash
export AWS_PROFILE=spinifex
aws ec2 describe-instance-types
```

If this returns a list of available instance types, your installation is working.

**Congratulations! Spinifex is installed from source.**

Continue to [Setting Up Your Cluster](/docs/setting-up-your-cluster) to import an AMI, create a VPC, and launch your first instance.

## Troubleshooting

### Go Not Found in PATH

```bash
export PATH=$PATH:/usr/local/go/bin
```

### ./bin/spx Missing After Build

```bash
./scripts/dev-setup.sh
```

### CA Certificate Not Trusted

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```
