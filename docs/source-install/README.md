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

# Source Install

> Build Spinifex from source for development, custom builds, or contributing.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

This guide builds Spinifex from source. For production deployments, the [binary installer](/docs/installing-spinifex) is recommended.

**Requirements:**

- Ubuntu 22.04+ / Debian 12+
- Go 1.26.1+
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

## Step 3. Initialize

```bash
sudo ./bin/spx admin init --node node1 --nodes 1
```

Save the admin credentials printed during init.

## Step 4. Setup OVN

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

## Step 5. Start Services

```bash
./scripts/start-dev.sh
```

## Step 6. Verify

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
