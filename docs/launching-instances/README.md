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
- [Launch](#launch)
- [Manage](#manage)
- [Reboot](#reboot)
- [Modify Instance Attributes](#modify-instance-attributes)
- [Console Output](#console-output)
- [Instance Types](#instance-types)
- [SSH (Development)](#ssh-development)
- [Troubleshooting](#troubleshooting)
  - [Instance Fails to Boot](#instance-fails-to-boot)
  - [Cannot SSH Into Instance](#cannot-ssh-into-instance)

---

## Overview

Spinifex provides EC2-compatible VM management built on QEMU/KVM. Instances support cloud-init, SSH key injection, VPC networking, and standard AWS lifecycle operations.

**Supported operations:**

- `run-instances` — Launch new VMs
- `describe-instances` — Query state
- `stop-instances` / `start-instances` — Lifecycle
- `reboot-instances` — In-place restart (QMP reset)
- `terminate-instances` — Permanent removal
- `modify-instance-attribute` — Change type, user data, or source/dest check
- `get-console-output` — Retrieve serial console log
- `describe-instance-types` — List available instance types

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
aws ec2 reboot-instances --instance-ids $INSTANCE_ID
```

## Modify Instance Attributes

Change instance type, user data, or source/dest check. Instance type and user data require the instance to be **stopped** first.

### Change Instance Type

```bash
aws ec2 stop-instances --instance-ids $INSTANCE_ID

aws ec2 modify-instance-attribute \
  --instance-id $INSTANCE_ID \
  --instance-type t3.medium

aws ec2 start-instances --instance-ids $INSTANCE_ID
```

## Console Output

Retrieve the serial console log for a running instance. Output is base64-encoded.

```bash
aws ec2 get-console-output --instance-id $INSTANCE_ID
```

Decode the output:

```bash
aws ec2 get-console-output --instance-id $INSTANCE_ID \
  --query 'Output' --output text | base64 -d
```

## Instance Types

List instance types available on the current host. The catalog is generated from the host CPU (Intel, AMD, or ARM) and includes burstable (t-family), general purpose (m-family), compute optimised (c-family), and memory optimised (r-family) types.

```bash
aws ec2 describe-instance-types
```

Filter to a specific type:

```bash
aws ec2 describe-instance-types --instance-types t3.micro t3.small
```

Show capacity (how many of each type can still be launched):

```bash
aws ec2 describe-instance-types \
  --filters Name=capacity,Values=true
```

## SSH (Development)

```bash
ps auxw | grep $INSTANCE_ID
ssh -i ~/.ssh/spinifex-key ec2-user@127.0.0.1 -p <port>
```

## Troubleshooting

### Instance Fails to Boot

Check QEMU logs for the instance and verify the AMI architecture matches your host:

```bash
cat ~/spinifex/logs/spinifex.log
aws ec2 describe-images --image-ids $SPINIFEX_AMI
```

If the AMI is for a different architecture (e.g. arm64 on an x86_64 host), import the correct image:

```bash
./bin/spx admin images list
./bin/spx admin images import --name debian-12-x86_64
```

### Cannot SSH Into Instance

cloud-init needs time to configure the instance after boot. Wait 30-60 seconds and retry.

Verify the SSH key was specified correctly when launching:

```bash
aws ec2 describe-instances --instance-ids $INSTANCE_ID
```

Check the `KeyName` field matches the key you're using to connect.
