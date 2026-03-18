---
title: "VPC Networking"
description: "Create and manage Virtual Private Clouds with subnets, DHCP, and Geneve overlay networking."
category: "Environments"
tags:
  - vpc
  - networking
  - ovn
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "OVN Architecture"
    url: "https://www.ovn.org/en/"
---

# VPC Networking

> Create and manage Virtual Private Clouds with subnets, DHCP, and Geneve overlay networking.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

Every EC2 instance runs inside a VPC with an isolated virtual network. Spinifex uses OVN (Open Virtual Network) to provide the networking layer.

**How it works:**
- VPC becomes an OVN logical router
- Subnet becomes an OVN logical switch with DHCP
- Each instance gets an OVN port with automatic IP assignment
- Cross-host traffic uses Geneve tunnels (UDP 6081)

## Instructions

## Create VPC

```bash
aws ec2 create-vpc --cidr-block 10.200.0.0/16
export SPINIFEX_VPC="vpc-XXX"
```

## Create Subnet

```bash
aws ec2 create-subnet --vpc-id $SPINIFEX_VPC --cidr-block 10.200.1.0/24
export SPINIFEX_SUBNET="subnet-XXX"
```

## Verify

```bash
aws ec2 describe-vpcs
aws ec2 describe-subnets
sudo ovn-nbctl lr-list
sudo ovn-nbctl ls-list
```

## Troubleshooting

## VPC creation fails

Ensure OVN services are running and the vpcd daemon is active:

```bash
sudo systemctl is-active ovn-controller
```

Check the vpcd logs for errors:

```bash
cat ~/spinifex/logs/vpcd.log
```

## Instances cannot reach each other

This typically means Geneve tunnels are not established between hosts. Verify tunnel configuration:

```bash
sudo ovs-vsctl show | grep -i geneve
```

Ensure UDP port 6081 is open between all hosts:

```bash
sudo ss -ulnp | grep 6081
```

From inside a VM, check that the private IP was assigned via DHCP:

```bash
ip addr show
```
