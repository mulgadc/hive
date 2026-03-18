---
title: "Spinifex Admin CLI"
description: "Complete reference for the Spinifex administration CLI. Manage accounts, nodes, VMs, and services."
category: "Administration"
tags:
  - cli
  - admin
  - reference
badge: cli
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
---

# Spinifex Admin CLI

> Complete reference for the Spinifex administration CLI.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

The `spx` binary is the central administration tool for managing your Spinifex infrastructure. It provides commands for cluster initialization, account management, node operations, VM lifecycle, and service control.

**Binary location:** `~/Development/mulga/spinifex/bin/spx`

All services in the Spinifex platform are managed through this single binary.

## Instructions

## Account Management

```bash
./bin/spx admin account create --name myteam
export AWS_PROFILE=spinifex-myteam
```

## Node Management

```bash
./bin/spx get nodes
```

```
NAME    STATUS    IP              REGION           AZ               UPTIME   VMs
node1   Ready     127.0.0.1       ap-southeast-2   ap-southeast-2a  2m       0
node2   Ready     127.0.0.2       ap-southeast-2   ap-southeast-2a  2m       0
node3   Ready     127.0.0.3       ap-southeast-2   ap-southeast-2a  2m       0
```

## Monitor Resources

```bash
./bin/spx top nodes
```

## Image Management

```bash
./bin/spx admin images list
./bin/spx admin images import --name debian-12-arm64
```

## Cluster Shutdown

```bash
./bin/spx admin cluster shutdown
```

## Troubleshooting

## Permission denied running spinifex

The binary may not be executable. Fix permissions:

```bash
chmod +x ./bin/spx
```

If you get permission errors during operations, ensure you're running with appropriate privileges. Some OVN and networking commands require `sudo`.

## Services fail to start

Check the daemon logs for specific errors:

```bash
ls ~/spinifex/logs/
cat ~/spinifex/logs/daemon.log
```

Common causes include port conflicts, missing OVN configuration, or untrusted CA certificates.
