---
title: "Launching Instances"
description: "Launch, manage, and connect to EC2-compatible virtual machines on Spinifex."
category: "Environments"
tags:
  - ec2
  - instances
  - vm
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "AWS EC2 CLI"
    url: "https://docs.aws.amazon.com/cli/latest/reference/ec2/"
---

# Launching Instances

> Launch, manage, and connect to EC2-compatible virtual machines on Spinifex.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

Spinifex provides EC2-compatible VM management built on QEMU/KVM. Instances support cloud-init, SSH key injection, VPC networking, and standard AWS lifecycle operations.

**Supported operations:**
- `run-instances` — Launch new VMs
- `describe-instances` — Query state
- `stop-instances` / `start-instances` — Lifecycle
- `terminate-instances` — Permanent removal

## Instructions

## Launch

```bash
aws ec2 run-instances \
  --image-id $SPINIFEX_AMI \
  --instance-type t3.small \
  --key-name spinifex-key \
  --subnet-id $SPINIFEX_SUBNET \
  --count 1

export INSTANCE_ID="i-XXX"
```

## Manage

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID
aws ec2 stop-instances --instance-ids $INSTANCE_ID
aws ec2 start-instances --instance-ids $INSTANCE_ID
aws ec2 terminate-instances --instance-ids $INSTANCE_ID
```

## SSH (Development)

```bash
ps auxw | grep $INSTANCE_ID
ssh -i ~/.ssh/spinifex-key ec2-user@127.0.0.1 -p <port>
```

## Troubleshooting

## Instance fails to boot

Check QEMU logs for the instance and verify the AMI architecture matches your host:

```bash
cat ~/spinifex/logs/daemon.log
aws ec2 describe-images --image-ids $SPINIFEX_AMI
```

If the AMI is for a different architecture (e.g. arm64 on an x86_64 host), import the correct image:

```bash
./bin/spx admin images list
./bin/spx admin images import --name debian-12-x86_64
```

## Cannot SSH into instance

cloud-init needs time to configure the instance after boot. Wait 30-60 seconds and retry.

Verify the SSH key was specified correctly when launching:

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID
```

Check the `KeyName` field matches the key you're using to connect.
