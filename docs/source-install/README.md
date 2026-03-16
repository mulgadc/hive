---
title: "Source Install"
description: "Build Hive from source for development, custom builds, or contributing. Includes dependency setup, compilation, and OVN configuration."
category: "Getting Started"
tags:
  - install
  - source
  - development
badge: source
resources:
  - title: "Hive Repository"
    url: "https://github.com/mulgadc/hive"
  - title: "Predastore (S3)"
    url: "https://github.com/mulgadc/predastore"
  - title: "Viperblock (EBS)"
    url: "https://github.com/mulgadc/viperblock"
  - title: "Go Downloads"
    url: "https://go.dev/dl/"
  - title: "AWS CLI Install"
    url: "https://docs.aws.amazon.com/cli/latest/userguide/getting-started-install.html"
---

# Source Install

> Build Hive from source for development, custom builds, or contributing.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

This guide covers building Hive from source code. Use this if you want to contribute to Hive, build custom versions, or inspect the codebase. For production deployments, the [binary installer](/docs/installing-hive) is recommended.

**Supported Operating Systems:**
- Ubuntu 22.04 / 24.04 / 25.10
- Debian 12.13

**Requirements:**
- Go 1.26.1+
- GCC, make, pkg-config
- QEMU/KVM
- OVN/Open vSwitch
- AWS CLI v2

## Instructions

## Step 1. Install system dependencies

### Quick Install

Bootstrap all dependencies in one step:

```bash
sudo make -C hive quickinstall
```

Ensure Go is in your PATH:

```bash
export PATH=$PATH:/usr/local/go/bin/
```

### Manual Install

Alternatively, install packages individually:

```bash
sudo add-apt-repository universe
sudo apt install nbdkit nbdkit-plugin-dev pkg-config qemu-system qemu-utils qemu-kvm libvirt-daemon-system libvirt-clients libvirt-dev make gcc unzip xz-utils file ovn-central ovn-host openvswitch-switch
```

Install Go 1.26.1+ from [https://go.dev/dl/](https://go.dev/dl/) and verify:

```bash
go version
```

Install AWS CLI v2:

```bash
curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"
unzip awscliv2.zip
sudo ./aws/install
aws --version
```

## Step 2. Clone and build

```bash
mkdir -p ~/Development/mulga/
cd ~/Development/mulga/
git clone https://github.com/mulgadc/hive.git
cd hive
./scripts/clone-deps.sh    # Clone viperblock + predastore
./scripts/dev-setup.sh     # Setup complete dev environment
```

Confirm `./bin/hive` exists and is executable.

## Step 3. Setup OVN

For a single-node dev environment:

```bash
./scripts/setup-ovn.sh --management
```

This creates the `br-int` integration bridge, starts `ovn-controller`, configures Geneve tunnel endpoints, and enables IP forwarding.

## Step 4. Initialize

Create a region and availability zone:

```bash
./bin/hive admin init --region ap-southeast-2 --az ap-southeast-2a --node node1 --nodes 1
```

## Step 5. Trust the CA certificate

```bash
sudo cp ~/hive/config/ca.pem /usr/local/share/ca-certificates/hive-ca.crt
sudo update-ca-certificates
```

## Step 6. Start services

```bash
./scripts/start-dev.sh
export AWS_PROFILE=hive
```

## Troubleshooting

## Go not found in PATH

After installing Go, it may not be in your current shell session. Add it:

```bash
export PATH=$PATH:/usr/local/go/bin/
```

Add this to your `~/.bashrc` or `~/.zshrc` for persistence. Verify with:

```bash
go version
```

## ./bin/hive missing after build

The build did not complete successfully. Re-run the setup:

```bash
./scripts/dev-setup.sh
```

Check the output for compilation errors. Common causes are missing Go version or missing system packages.

## OVN services not starting

Verify the setup script was run with `--management`:

```bash
./scripts/setup-ovn.sh --management
```

Check OVN controller status:

```bash
sudo systemctl is-active ovn-controller
sudo ovn-sbctl show
```

## CA certificate not trusted

AWS CLI will reject HTTPS connections to Hive services. Re-add the certificate:

```bash
sudo cp ~/hive/config/ca.pem /usr/local/share/ca-certificates/hive-ca.crt
sudo update-ca-certificates
```

## clone-deps.sh fails

Ensure you have access to the GitHub repositories. If using SSH and it fails, try HTTPS:

```bash
git config --global url."https://github.com/".insteadOf "git@github.com:"
./scripts/clone-deps.sh
```
