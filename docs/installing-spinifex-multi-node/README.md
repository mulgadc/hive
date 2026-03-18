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

A Spinifex cluster operates as a fully distributed infrastructure region — similar to an AWS Region — where multiple nodes provide high availability, data durability, and fault tolerance.

**Services distributed across nodes:**

- **NATS** — Clustered message bus for request routing and JetStream replication
- **Predastore** — Raft consensus with Reed-Solomon erasure coding (RS 2+1)
- **Viperblock** — Block storage co-located with compute nodes
- **Spinifex Daemon** — Independent AWS API request serving per node

Cluster formation is automatic. When initializing with `--nodes 3`, the init node starts a formation server and waits for peers to join. Once all nodes register, each receives the full cluster topology — credentials, CA certificates, NATS routes, Predastore peer lists — no manual configuration synchronization required.

**Network requirements:**

- Minimum 1 NIC per server
- 2 NICs recommended for production (management + overlay)
- UDP port 6081 open between hosts (Geneve tunnels)

## Instructions

## Step 1. Install Spinifex on each server

Run the binary installer on **every server** in the cluster:

```bash
curl https://install.mulgadc.com | bash
```

## Step 2. Set node IP variables

On **each server**, export the IPs for all nodes (replace with your actual IPs):

```bash
export SPINIFEX_NODE1=192.168.1.10
export SPINIFEX_NODE2=192.168.1.11
export SPINIFEX_NODE3=192.168.1.12
```

To find your server's IP:

```bash
ip addr show | grep "inet "
```

Next, define the AWS region and AZ you are deploying locally.

```bash
export AWS_REGION=us-east-1
export AWS_AZ=us-east-1a
```

## Step 3. Setup OVN networking

OVN must be configured on every server before forming the cluster. Server 1 runs OVN central (NB and SB databases) and must be set up first.

**Server 1 — OVN central + compute (run first):**

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh --management --encap-ip=$SPINIFEX_NODE1
```

Verify OVN central is ready before proceeding:

```bash
sudo ovn-sbctl show
```

**Server 2 — Compute node (after server 1 is ready):**

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh --ovn-remote=tcp:$SPINIFEX_NODE1:6642 --encap-ip=$SPINIFEX_NODE2
```

**Server 3 — Compute node (after server 1 is ready):**

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh --ovn-remote=tcp:$SPINIFEX_NODE1:6642 --encap-ip=$SPINIFEX_NODE3
```

Verify all chassis have registered (from server 1):

```bash
sudo ovn-sbctl show
```

You should see 3 chassis entries, one per server, each with a Geneve encap IP.

## Step 4. Form the cluster

The formation process requires running init and join commands concurrently. The init node blocks until all expected nodes have joined.

**Server 1 — Initialize the cluster:**

```bash
sudo spx admin init \
  --node node1 \
  --nodes 3 \
  --bind $SPINIFEX_NODE1 \
  --cluster-bind $SPINIFEX_NODE1 \
  --port 4432 \
  --region $AWS_REGION \
  --az $AWS_AZ
```

**Server 2 — Join the cluster (while init is running):**

```bash
spx admin join \
  --node node2 \
  --bind $SPINIFEX_NODE2 \
  --cluster-bind $SPINIFEX_NODE2 \
  --host $SPINIFEX_NODE1:4432 \
  --region $AWS_REGION \
  --az $AWS_AZ
```

**Server 3 — Join the cluster (while init is running):**

```bash
spx admin join \
  --node node3 \
  --bind $SPINIFEX_NODE3 \
  --cluster-bind $SPINIFEX_NODE3 \
  --host $SPINIFEX_NODE1:4432 \
  --region $AWS_REGION \
  --az $AWS_AZ
```

All three processes will exit with a cluster summary once formation is complete.

## Step 5. Start services and verify

Start services on **all servers**:

```bash
sudo systemctl start spinifex.target
```

From any node, verify the cluster:

```bash
spx get nodes
```

```
NAME    STATUS    IP              REGION      AZ           UPTIME   VMs
node1   Ready     192.168.1.10    us-east-1   us-east-1a   2m       0
node2   Ready     192.168.1.11    us-east-1   us-east-1a   2m       0
node3   Ready     192.168.1.12    us-east-1   us-east-1a   2m       0
```

## Step 6. Trust the CA certificate

On **each server**, trust the CA generated during init:

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
export AWS_PROFILE=spinifex
```

## Step 7. Launch instances across the cluster

Connect to any node's AWS Gateway — all nodes serve the same cluster state:

```bash
aws ec2 run-instances \
  --image-id $SPINIFEX_AMI \
  --instance-type t3.small \
  --key-name spinifex-key \
  --subnet-id $SPINIFEX_SUBNET \
  --count 3
```

Spinifex distributes instances across nodes based on available capacity. Check which node each instance landed on:

```bash
spx get vms
```

## Shutdown

A graceful shutdown coordinates all nodes — drains VMs, flushes storage, and stops services:

```bash
spx admin cluster shutdown
```

## Troubleshooting

## Nodes not joining the cluster

The init command must still be running when you execute join on other servers. If init has already exited, re-run it.

Check that all servers can reach the init node on the formation port:

```bash
curl -s http://$SPINIFEX_NODE1:4432/health
```

## OVN chassis not registering

Compute nodes need to reach OVN central on server 1. Verify from server 1:

```bash
sudo ovn-sbctl show
```

If chassis are missing, check firewall rules for port 6642:

```bash
sudo ss -tlnp | grep 6642
```

Re-run the OVN setup on the affected compute node.

## Cross-host VMs cannot communicate

This indicates a Geneve tunnel issue. Verify tunnels are established:

```bash
sudo ovs-vsctl show | grep -i geneve
```

Ensure UDP port 6081 is open between all hosts:

```bash
sudo ss -ulnp | grep 6081
```

If tunnels are missing, verify `--encap-ip` was set correctly during OVN setup.

## Health check fails on a node

Check if the daemon is running and listening on the correct IP:

```bash
curl -s http://$SPINIFEX_NODE1:4432/health
```

Review the daemon logs for errors:

```bash
cat ~/spinifex/logs/spinifex.log
```

Verify `--bind` was set to the correct IP during cluster formation.

## NATS cluster routing issues

Check NATS logs for connection errors between nodes:

```bash
grep -i "route\|cluster" ~/spinifex/logs/nats.log
```

Ensure `--cluster-bind` IPs are reachable between all servers.
