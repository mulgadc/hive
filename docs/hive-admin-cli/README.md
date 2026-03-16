---
title: "Hive Admin CLI"
description: "Complete reference for the Hive administration CLI. Manage accounts, nodes, VMs, and services."
category: "Administration"
tags:
  - cli
  - admin
  - reference
badge: cli
resources:
  - title: "Hive Repository"
    url: "https://github.com/mulgadc/hive"
---

# Hive Admin CLI

> Complete reference for the Hive administration CLI.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

The `hive` binary is the central administration tool for managing your Hive infrastructure. It provides commands for cluster initialization, account management, node operations, VM lifecycle, and service control.

**Binary location:** `~/Development/mulga/hive/bin/hive`

All services in the Hive platform are managed through this single binary.

## Instructions

## Account Management

```bash
./bin/hive admin account create --name myteam
export AWS_PROFILE=hive-myteam
```

## Node Management

```bash
./bin/hive get nodes
```

```
NAME    STATUS    IP              REGION           AZ               UPTIME   VMs
node1   Ready     127.0.0.1       ap-southeast-2   ap-southeast-2a  2m       0
node2   Ready     127.0.0.2       ap-southeast-2   ap-southeast-2a  2m       0
node3   Ready     127.0.0.3       ap-southeast-2   ap-southeast-2a  2m       0
```

## Monitor Resources

```bash
./bin/hive top nodes
```

## Image Management

```bash
./bin/hive admin images list
./bin/hive admin images import --name debian-12-arm64
```

## Cluster Shutdown

```bash
./bin/hive admin cluster shutdown
```

## Troubleshooting

## Permission denied running hive

The binary may not be executable. Fix permissions:

```bash
chmod +x ./bin/hive
```

If you get permission errors during operations, ensure you're running with appropriate privileges. Some OVN and networking commands require `sudo`.

## Services fail to start

Check the daemon logs for specific errors:

```bash
ls ~/hive/logs/
cat ~/hive/logs/daemon.log
```

Common causes include port conflicts, missing OVN configuration, or untrusted CA certificates.
