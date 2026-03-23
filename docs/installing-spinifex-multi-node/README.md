---
title: "Multi-Node Installation"
description: "Deploy Spinifex across multiple servers to create an availability zone with high availability, data durability, and fault tolerance."
category: "Getting Started"
tags:
  - install
  - multi node
  - cluster
badge: availability-zone
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "Predastore (S3)"
    url: "https://github.com/mulgadc/predastore"
  - title: "Viperblock (EBS)"
    url: "https://github.com/mulgadc/viperblock"
---

# Multi-Node Installation

> Deploy Spinifex across multiple servers to create an availability zone.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

A Spinifex cluster distributes services across multiple servers for high availability, data durability, and fault tolerance. Cluster formation is automatic — the init node waits for peers to join, then distributes credentials, CA certificates, and configuration.

**Network requirements:**

- Minimum 1 NIC per server (2 recommended for production)
- UDP port 6081 open between hosts (Geneve tunnels)
- TCP ports 4222, 4248, 6641, 6642 open between hosts (NATS, OVN)

## Instructions

### Step 1. Install Spinifex on each server

```bash
curl https://install.mulgadc.com | bash
```

### Step 2. Set node IP variables

On **each server**, export the management IPs for all nodes:

```bash
export SPINIFEX_NODE1=192.168.1.10
export SPINIFEX_NODE2=192.168.1.11
export SPINIFEX_NODE3=192.168.1.12
export AWS_REGION=us-east-1
export AWS_AZ=us-east-1a
```

### Step 3. Setup OVN networking

Server 1 runs OVN central and must be set up first.

**Server 1:**

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh \
  --management \
  --external-bridge --external-iface=eth1 --dhcp \
  --encap-ip=$SPINIFEX_NODE1
```

**Server 2** (after server 1 is ready):

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh \
  --external-bridge --external-iface=eth1 --dhcp \
  --ovn-remote=tcp:$SPINIFEX_NODE1:6642 \
  --encap-ip=$SPINIFEX_NODE2
```

**Server 3** (after server 1 is ready):

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh \
  --external-bridge --external-iface=eth1 --dhcp \
  --ovn-remote=tcp:$SPINIFEX_NODE1:6642 \
  --encap-ip=$SPINIFEX_NODE3
```

Replace `eth1` with your WAN interface.

Verify all 3 chassis registered:

```bash
sudo ovn-sbctl show
```

### Step 4. Form the cluster

Run init and join concurrently — init blocks until all nodes join.

**Server 1 — Initialize:**

```bash
sudo spx admin init \
  --node node1 --nodes 3 \
  --bind $SPINIFEX_NODE1 --cluster-bind $SPINIFEX_NODE1 \
  --port 4432 --region $AWS_REGION --az $AWS_AZ
```

Save the admin credentials — they will not be shown again.

**Server 2 — Join** (while init is running):

```bash
sudo spx admin join \
  --node node2 --bind $SPINIFEX_NODE2 --cluster-bind $SPINIFEX_NODE2 \
  --host $SPINIFEX_NODE1:4432 --region $AWS_REGION --az $AWS_AZ
```

**Server 3 — Join** (while init is running):

```bash
sudo spx admin join \
  --node node3 --bind $SPINIFEX_NODE3 --cluster-bind $SPINIFEX_NODE3 \
  --host $SPINIFEX_NODE1:4432 --region $AWS_REGION --az $AWS_AZ
```

### Step 5. Trust the CA certificate

On **each server**:

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

### Step 6. Start services

On **all servers**:

```bash
sudo systemctl start spinifex.target
export AWS_PROFILE=spinifex
```

### Step 7. Verify

From any node:

```bash
aws ec2 describe-instance-types
```

If this returns a list of available instance types, your cluster is working.

**Congratulations! Your Spinifex cluster is installed.**

Continue to [Setting Up Your Cluster](/docs/setting-up-your-cluster) to import an AMI, create a VPC, and launch your first instance.

---

## Troubleshooting

### Nodes not joining

The init command must still be running when join executes. If init exited, re-run with `--force`.

```bash
curl -s http://$SPINIFEX_NODE1:4432/health
```

### OVN chassis not registering

```bash
sudo ovn-sbctl show
sudo ss -tlnp | grep 6642
```

### CA certificate not trusted

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

### Cross-host VMs cannot communicate

```bash
sudo ovs-vsctl show | grep -i geneve
sudo ss -ulnp | grep 6081
```
