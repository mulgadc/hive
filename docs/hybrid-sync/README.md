---
title: "Hybrid Sync"
description: "Synchronize data between Spinifex and AWS when network connectivity is available."
category: "Migration"
tags:
  - hybrid
  - sync
  - s3
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
---

# Hybrid Sync

> Synchronize data between Spinifex and AWS when network connectivity is available.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

Spinifex's hybrid mode enables bidirectional data synchronization between local infrastructure and AWS cloud services. Ideal for intermittent connectivity environments.

## Instructions

## Prerequisites

Ensure the AWS profile is set:

```bash
export AWS_PROFILE=spinifex
```

## Push Local to Cloud

```bash
aws s3 sync s3://local-bucket/ s3://cloud-bucket/ \
  --source-region spinifex --region us-east-1
```

## Pull Cloud to Local

```bash
aws s3 sync s3://cloud-bucket/ s3://local-bucket/ \
  --source-region us-east-1 --region spinifex
```

## EBS Volume Backup

```bash
rsync -avz /data/ user@cloud-server:/backup/spinifex-data/
```

## Troubleshooting

## Sync Fails Mid Transfer

S3 sync is idempotent — re-run the same command to resume where it left off:

```bash
aws s3 sync s3://local-bucket/ s3://cloud-bucket/ \
  --source-region spinifex --region us-east-1
```

Only changed or missing files will be transferred on subsequent runs.

## Permission Errors During Sync

Verify your AWS credentials are configured for both the source and destination:

```bash
aws sts get-caller-identity --profile spinifex
aws sts get-caller-identity --profile default
```

Ensure the IAM user has `s3:GetObject`, `s3:PutObject`, and `s3:ListBucket` permissions on both buckets.
