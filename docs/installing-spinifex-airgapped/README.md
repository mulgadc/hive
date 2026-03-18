---
title: "Air-Gapped Installation"
description: "Deploy Spinifex in environments without internet connectivity. Covers offline package caching, USB deployment, and package verification."
category: "Getting Started"
tags:
  - install
  - air-gapped
  - offline
badge: advanced
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "apt-cacher-ng"
    url: "https://wiki.debian.org/AptCacherNg"
---

# Air-Gapped Installation

> Deploy Spinifex in environments without internet connectivity.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

In air-gapped or disconnected environments, Spinifex can be deployed without internet access. This guide covers preparing offline packages on a connected machine, creating USB deployment media, and installing on the target server with package verification.

**Deployment methods:**
- **apt-cacher-ng** — Local APT cache for Debian/Ubuntu packages
- **Go module cache** — Pre-downloaded Go dependencies
- **USB deployment** — Portable installation media with all dependencies
- **GPG verification** — Cryptographic package integrity checks

## Instructions

## Step 1. Prepare packages (on a connected machine)

Set up a local APT cache to collect all required packages:

```bash
sudo apt install apt-cacher-ng
sudo systemctl enable --now apt-cacher-ng
```

Pre-download all Spinifex dependencies:

```bash
sudo apt install --download-only nbdkit nbdkit-plugin-dev pkg-config \
  qemu-system qemu-utils qemu-kvm libvirt-daemon-system \
  libvirt-clients libvirt-dev make gcc unzip xz-utils file \
  ovn-central ovn-host openvswitch-switch
```

## Step 2. Create USB deployment media

```bash
mkdir -p /media/hive-deploy/{apt-packages,go-cache,hive-source}
cp /var/cache/apt/archives/*.deb /media/hive-deploy/apt-packages/
cp -r ~/go/pkg/mod/cache/ /media/hive-deploy/go-cache/
cp -r ~/Development/mulga/spinifex /media/hive-deploy/hive-source/
```

## Step 3. Verify package integrity

Sign and verify packages before transferring to the target:

```bash
gpg --import /media/hive-deploy/mulga-signing-key.asc
gpg --verify hive-v1.0.tar.gz.sig hive-v1.0.tar.gz
sha256sum -c /media/hive-deploy/checksums.sha256
```

## Step 4. Install on the target server

Mount the USB and install:

```bash
sudo mount /dev/sdb1 /mnt/usb
sudo dpkg -i /mnt/usb/apt-packages/*.deb
cp -r /mnt/usb/hive-source ~/Development/mulga/spinifex
cd ~/Development/mulga/spinifex && make build
```

Then follow the [Source Install](/docs/source-install) guide from Step 3 (Setup OVN) onwards.

## Troubleshooting

## Missing dependencies after dpkg install

Some packages may have unresolved dependencies. Fix with:

```bash
sudo dpkg -i /mnt/usb/apt-packages/*.deb
sudo apt-get install -f
```

The `-f` flag tells apt to fix broken dependencies using what's available locally.

## GPG verification fails

The signing key may not match or the download was corrupted. Re-import the key and verify:

```bash
gpg --import /media/hive-deploy/mulga-signing-key.asc
gpg --verify hive-v1.0.tar.gz.sig hive-v1.0.tar.gz
```

If verification still fails, re-download the package on the connected machine and compare checksums:

```bash
sha256sum hive-v1.0.tar.gz
```

## Go module cache errors

The Go module cache may have been incompletely copied. On the connected machine, re-export the full cache:

```bash
cp -r ~/go/pkg/mod/cache/ /media/hive-deploy/go-cache/
```

On the target, restore it:

```bash
mkdir -p ~/go/pkg/mod/
cp -r /mnt/usb/go-cache ~/go/pkg/mod/cache
```
