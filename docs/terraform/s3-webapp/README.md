---
title: "Terraform: S3-Backed Web App"
description: "Deploy a Flask file-sharing web application backed by S3 (Predastore) using Terraform on Spinifex."
category: "Terraform Workbooks"
badge: "Workbook"
tags:
  - terraform
  - s3
  - predastore
  - flask
  - webapp
  - workbook
resources:
  - title: "Terraform AWS Provider"
    url: "https://registry.terraform.io/providers/hashicorp/aws/latest"
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "Predastore (S3)"
    url: "https://github.com/mulgadc/predastore"
  - title: "OpenTofu"
    url: "https://opentofu.org/"
---

# Terraform: S3-Backed Web App

> Deploy a Flask file-sharing web application backed by S3 (Predastore) using Terraform on Spinifex.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

Deploy an EC2 instance running a Flask file-sharing web application backed by S3 (Predastore). Users can upload files through a web form and browse uploaded content — demonstrating Terraform managing both compute and object storage together.

**Architecture:**

```
Browser ──HTTP──▶ EC2 Instance (Flask webapp, port 80)
                      │
                      ▼ S3 API (boto3)
                  Predastore (port 8443)
```

**What you'll learn:**

- Configuring the AWS provider with both Spinifex and Predastore endpoints
- Creating S3 buckets on Predastore via Terraform
- Deploying a Python webapp with cloud-init that talks to S3
- Passing credentials and configuration to instances via user-data

**Prerequisites:**

- Spinifex installed and running (see [Installing Spinifex](/docs/installing-spinifex))
- Predastore running (S3 API on port 8443)
- A Debian 12 AMI imported (see [Setting Up Your Cluster](/docs/setting-up-your-cluster))
- OpenTofu or Terraform installed
- `AWS_PROFILE=spinifex` configured
- The EC2 instance must be able to reach Predastore — use the host's br-wan IP, not localhost

## Instructions

### Step 1. Get the Template

Clone the Terraform examples from the Spinifex repository:

```bash
git clone --depth 1 --filter=blob:none --sparse https://github.com/mulgadc/spinifex.git spinifex-tf
cd spinifex-tf
git sparse-checkout set docs/terraform
cd docs/terraform/s3-webapp
```

Or create the files manually and paste the full configuration below.

### Step 2. Create terraform.tfvars

Before deploying, create a `terraform.tfvars` with your Predastore credentials. The `predastore_host` must be reachable from inside the VPC — use the host's br-wan or LAN IP, not localhost.

<!-- INCLUDE: terraform.tfvars.example lang:hcl -->

### Step 3. Create main.tf

<!-- INCLUDE: main.tf lang:hcl -->

### Step 4. Deploy

```bash
export AWS_PROFILE=spinifex
tofu init
tofu apply
```

### Step 5. Test the Application

> **Note:** EC2 instances can take 30+ seconds to boot after apply. If SSH or HTTP is unreachable, wait and retry.

Open the `web_url` output in your browser. You should see the file browser UI. Upload a file and verify it appears in the list.

```bash
# Verify via CLI
curl http://<public_ip>

# Check the S3 bucket directly
aws s3 ls s3://webapp-uploads/ --profile spinifex --endpoint-url https://localhost:8443
```

### Clean Up

```bash
tofu destroy
```

## Troubleshooting

### Predastore Connection Refused from Instance

The EC2 instance cannot reach `localhost` on the host. Set `predastore_host` to the host's br-wan or LAN IP address:

```hcl
predastore_host = "192.168.1.10:8443"
```

### S3 Bucket Creation Fails

Verify Predastore is running and accessible:

```bash
curl -k https://localhost:8443/
aws s3 ls --profile spinifex --endpoint-url https://localhost:8443
```

### Flask App Not Starting

SSH into the instance and check the service:

```bash
ssh -i s3-webapp-demo.pem ec2-user@<public_ip>
sudo systemctl status s3-webapp
sudo journalctl -u s3-webapp --no-pager -n 50
```

### Upload Fails or Files Not Appearing

Check that the S3 credentials in `/opt/webapp/.env` are correct and that the bucket exists:

```bash
ssh -i s3-webapp-demo.pem ec2-user@<public_ip>
cat /opt/webapp/.env
/opt/webapp/venv/bin/python -c "import boto3; print('boto3 OK')"
```

### AMI Not Found

Ensure you have imported a Debian 12 image:

```bash
aws ec2 describe-images --owners 000000000000 --profile spinifex
```
