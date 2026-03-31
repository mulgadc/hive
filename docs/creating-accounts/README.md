---
title: "Creating Accounts"
description: "Create and manage isolated user accounts with their own resources."
category: "Administration"
tags:
  - accounts
  - iam
  - admin
resources:
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
---

# Creating Accounts

> Create and manage isolated user accounts with their own resources.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

Spinifex supports multi-tenant account isolation. Each account gets its own IAM credentials, AWS CLI profile, and isolated resource namespace.

## Instructions

## Create Account

```bash
spx admin account create --name myteam
export AWS_PROFILE=spinifex-myteam
```

## Verify

```bash
aws sts get-caller-identity
```

## Troubleshooting

## Credentials Not Working

Verify the AWS CLI configuration files exist and contain the correct profile:

```bash
cat ~/.aws/config
cat ~/.aws/credentials
```

Ensure the `AWS_PROFILE` environment variable matches the profile name:

```bash
echo $AWS_PROFILE
export AWS_PROFILE=spinifex-myteam
```
