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
- [External Networking](#external-networking)
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

- Minimum 1 NIC per server (single-NIC uses macvlan for external bridge)
- 2 NICs recommended for production (management + WAN)
- UDP port 6081 open between hosts (Geneve tunnels)
- TCP ports 4222, 4248, 6641, 6642 open between hosts (NATS, OVN)

## Instructions

### Step 1. Install Spinifex on each server

Run the binary installer on **every server** in the cluster:

```bash
curl https://install.mulgadc.com | bash
```

### Step 2. Set node IP variables

On **each server**, export the IPs for all nodes. Use the management network IP (the interface that other nodes can reach):

```bash
export SPINIFEX_NODE1=192.168.1.10
export SPINIFEX_NODE2=192.168.1.11
export SPINIFEX_NODE3=192.168.1.12
```

To find your server's IP:

```bash
ip -4 route get 8.8.8.8 | awk '/src/{print $7}'
```

Define the region and AZ:

```bash
export AWS_REGION=us-east-1
export AWS_AZ=us-east-1a
```

### Step 3. Setup OVN networking

OVN must be configured on every server before forming the cluster. Server 1 runs OVN central (NB and SB databases) and must be set up first.

**Server 1 — OVN central + compute (run first):**

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh \
  --management \
  --external-bridge --external-iface=eth1 --dhcp \
  --encap-ip=$SPINIFEX_NODE1
```

Replace `eth1` with your WAN interface. The `--dhcp` flag obtains a gateway IP from your router automatically. For single-NIC servers, add `--single-nic`.

Verify OVN central is ready before proceeding:

```bash
sudo ovn-sbctl show
```

**Server 2 — Compute node (after server 1 is ready):**

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh \
  --external-bridge --external-iface=eth1 --dhcp \
  --ovn-remote=tcp:$SPINIFEX_NODE1:6642 \
  --encap-ip=$SPINIFEX_NODE2
```

**Server 3 — Compute node (after server 1 is ready):**

```bash
sudo /usr/local/share/spinifex/setup-ovn.sh \
  --external-bridge --external-iface=eth1 --dhcp \
  --ovn-remote=tcp:$SPINIFEX_NODE1:6642 \
  --encap-ip=$SPINIFEX_NODE3
```

Verify all chassis have registered (from server 1):

```bash
sudo ovn-sbctl show
```

You should see 3 chassis entries, one per server, each with a Geneve encap IP.

### Step 4. Form the cluster

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

> **Note:** Save the admin credentials printed during init. They will not be shown again. The CA certificate and cluster configuration are automatically distributed to joining nodes.

**Server 2 — Join the cluster (while init is running):**

```bash
sudo spx admin join \
  --node node2 \
  --bind $SPINIFEX_NODE2 \
  --cluster-bind $SPINIFEX_NODE2 \
  --host $SPINIFEX_NODE1:4432 \
  --region $AWS_REGION \
  --az $AWS_AZ
```

**Server 3 — Join the cluster (while init is running):**

```bash
sudo spx admin join \
  --node node3 \
  --bind $SPINIFEX_NODE3 \
  --cluster-bind $SPINIFEX_NODE3 \
  --host $SPINIFEX_NODE1:4432 \
  --region $AWS_REGION \
  --az $AWS_AZ
```

All three processes will exit with a cluster summary once formation is complete.

### Step 5. Trust the CA certificate

On **each server**, trust the CA generated during init. Joining nodes receive the CA automatically during formation:

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

This is required for the AWS CLI and inter-service HTTPS communication to work.

### Step 6. Start services

Start services on **all servers**:

```bash
sudo systemctl start spinifex.target
```

Set the AWS profile on each server:

```bash
export AWS_PROFILE=spinifex
```

Add this to `~/.bashrc` for persistence.

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

### Step 7. Import an AMI

From any node, list and import an image:

```bash
spx admin images list
spx admin images import --name debian-12-amd64
```

Find the imported AMI ID:

```bash
AMI_ID=$(aws ec2 describe-images --query 'Images[0].ImageId' --output text)
```

### Step 8. Create a VPC and launch instances

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

# Launch 3 instances — Spinifex distributes them across nodes
aws ec2 run-instances \
  --image-id $AMI_ID \
  --instance-type t3.small \
  --key-name spinifex-key \
  --subnet-id $SUBNET_ID \
  --count 3
```

Check which node each instance landed on:

```bash
spx get vms
```

### Step 9. Connect via SSH

Find an instance's private IP and connect:

```bash
INSTANCE_ID=$(aws ec2 describe-instances \
  --query 'Reservations[0].Instances[0].InstanceId' --output text)

PRIVATE_IP=$(aws ec2 describe-instances --instance-ids $INSTANCE_ID \
  --query 'Reservations[0].Instances[0].PrivateIpAddress' --output text)

ssh -i ~/.ssh/id_rsa ec2-user@$PRIVATE_IP
```

Instances are reachable via their private IP from any node in the cluster (OVN Geneve overlay handles cross-host routing).

## Shutdown

A graceful shutdown coordinates all nodes — drains VMs, flushes storage, and stops services:

```bash
spx admin cluster shutdown
```

---

## External Networking

By default, `setup-ovn.sh --dhcp` obtains a gateway IP from your router, giving VMs outbound internet via SNAT. To enable public IPs for VMs (inbound connectivity), pass `--external-pool` during `admin init`:

```bash
sudo spx admin init \
  --node node1 --nodes 3 \
  --bind $SPINIFEX_NODE1 --cluster-bind $SPINIFEX_NODE1 \
  --region $AWS_REGION --az $AWS_AZ \
  --external-pool=192.168.1.150-192.168.1.250
```

Then exclude that range from your router's DHCP scope. See the [VPC Public Subnets](/docs/vpc-public-subnets) guide for details on Elastic IPs, security groups, and public subnet configuration.

| Tier | Setup | What VMs get |
|---|---|---|
| **Auto** (default) | `--dhcp` on setup-ovn.sh | Outbound internet (SNAT) |
| **Pool** | `--external-pool=start-end` on admin init | Public IPs + inbound (1:1 NAT) |
| **Disabled** | `--no-external` on admin init | Overlay only, no internet |

---

## Troubleshooting

### Nodes not joining the cluster

The init command must still be running when you execute join on other servers. If init has already exited, re-run it with `--force`.

Check that all servers can reach the init node on the formation port:

```bash
curl -s http://$SPINIFEX_NODE1:4432/health
```

### OVN chassis not registering

Compute nodes need to reach OVN central on server 1. Verify from server 1:

```bash
sudo ovn-sbctl show
```

If chassis are missing, check firewall rules for port 6642:

```bash
sudo ss -tlnp | grep 6642
```

Re-run the OVN setup on the affected compute node.

### CA certificate not trusted

AWS CLI will reject HTTPS connections if the Spinifex CA is not trusted. Re-add the certificate on the affected node:

```bash
sudo cp ~/spinifex/config/ca.pem /usr/local/share/ca-certificates/spinifex-ca.crt
sudo update-ca-certificates
```

### Cross-host VMs cannot communicate

This indicates a Geneve tunnel issue. Verify tunnels are established:

```bash
sudo ovs-vsctl show | grep -i geneve
```

Ensure UDP port 6081 is open between all hosts:

```bash
sudo ss -ulnp | grep 6081
```

If tunnels are missing, verify `--encap-ip` was set correctly during OVN setup.

### NATS cluster routing issues

Check NATS logs for connection errors between nodes:

```bash
grep -i "route\|cluster" ~/spinifex/logs/nats.log
```

Ensure `--cluster-bind` IPs are reachable between all servers.

### DHCP failed to obtain IP

If `setup-ovn.sh --dhcp` reports a failure, verify a DHCP server is reachable on your WAN:

```bash
sudo dhcpcd --test enp0s3
```

As a fallback, set a static gateway IP in `~/spinifex/config/spinifex.toml` under `[[network.external_pools]]`:

```toml
gateway_ip = "192.168.1.100"
```

Then restart services.
