---
title: "Creating an Environment"
description: "End-to-end guide: user accounts, SSH keys, VPCs, AMIs, and launching instances."
category: "Environments"
tags:
  - environment
  - vpc
  - ec2
  - ssh
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "Debian Cloud Images"
    url: "https://cloud.debian.org/images/cloud/bookworm/latest/"
---

# Creating an Environment

> End-to-end guide: user accounts, SSH keys, VPCs, AMIs, and launching instances.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

This guide walks through the complete workflow for creating a production-ready Spinifex environment.

**Steps:**

1. Create a user account
2. Import or create SSH keys
3. Create a VPC and subnet
4. Select and import an AMI
5. Launch EC2 instances
6. Connect via SSH
7. Monitor instance health

## Instructions

## 1. Import SSH Key

```bash
aws ec2 import-key-pair --key-name "spinifex-key" --public-key-material fileb://~/.ssh/id_rsa.pub
```

## 2. Create VPC

```bash
aws ec2 create-vpc --cidr-block 10.200.0.0/16
export SPINIFEX_VPC="vpc-XXX"

aws ec2 create-subnet --vpc-id $SPINIFEX_VPC --cidr-block 10.200.1.0/24
export SPINIFEX_SUBNET="subnet-XXX"
```

## 3. Import AMI

```bash
./bin/spx admin images list
./bin/spx admin images import --name debian-12-x86_64
export SPINIFEX_AMI="ami-XXX"
```

## 4. Launch Instance

```bash
aws ec2 run-instances \
  --image-id $SPINIFEX_AMI \
  --instance-type t3.small \
  --key-name spinifex-key \
  --subnet-id $SPINIFEX_SUBNET \
  --count 1
```

## 5. Connect via SSH

```bash
ps auxw | grep $INSTANCE_ID
# Look for: hostfwd=tcp:127.0.0.1:<port>-:22

ssh -i ~/.ssh/spinifex-key ec2-user@127.0.0.1 -p <port>
```

## Troubleshooting

## Instance stuck in pending

Check the Spinifex daemon and QEMU logs for boot errors:

```bash
ls ~/spinifex/logs/
cat ~/spinifex/logs/spinifex.log
```

Verify the AMI exists and architecture matches your host:

```bash
aws ec2 describe-images --image-ids $SPINIFEX_AMI
```

## SSH connection refused

cloud-init takes 30-60 seconds to configure networking and SSH after boot. Wait and retry.

Verify the SSH port forwarding is active:

```bash
ps auxw | grep hostfwd
```

Ensure you're using the correct key and port:

```bash
ssh -i ~/.ssh/spinifex-key ec2-user@127.0.0.1 -p <port>
```
