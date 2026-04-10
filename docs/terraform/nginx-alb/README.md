---
title: "Nginx Web Server (Load Balanced)"
description: "Deploy a VPC with a public subnet and two EC2 instances running Nginx, served by an ALB using Terraform on Spinifex."
category: "Terraform Workbooks"
tags:
  - terraform
  - nginx
  - ec2
  - elbv2
  - alb
  - vpc
  - workbook
resources:
  - title: "Terraform AWS Provider"
    url: "https://registry.terraform.io/providers/hashicorp/aws/latest"
  - title: "Spinifex Repository"
    url: "https://github.com/mulgadc/spinifex"
  - title: "OpenTofu"
    url: "https://opentofu.org/"
---

# Terraform: Nginx Web Servers with ALB

> Deploys two Nginx web server instances behind an Application Load Balancer on Spinifex.

## Table of Contents

- [Overview](#overview)
- [Instructions](#instructions)
- [Troubleshooting](#troubleshooting)

---

## Overview

Deploy two complete Nginx web servers behind an Application Load Balancer on Spinifex using Terraform/OpenTofu. This workbook provisions a VPC, public subnet, internet gateway, route table, security group, SSH key pair, an application load balancer (ALB) and two EC2 instances with cloud-init user-data that installs and starts Nginx.

**What you'll learn:**

- Configuring the AWS Terraform provider to target Spinifex
- Creating a VPC with public internet access
- Provisioning an EC2 instance with cloud-init user-data
- Provisioning an internet-facing application load balancer
- Generating SSH key pairs with the TLS provider

**What gets created**

| Resource | Name | Purpose |
|---|---|---|
| VPC | `nginx-alb-vpc` | Isolated network (10.20.0.0/16) |
| Subnets | `nginx-alb-public-a`, `nginx-alb-public-b` | Two public subnets for ALB and instances |
| Internet Gateway | `nginx-alb-igw` | Routes internet traffic |
| Security Group | `nginx-alb-sg` | Allows SSH (22) and HTTP (80) inbound |
| EC2 Instances | `nginx-alb-1`, `nginx-alb-2` | Debian 12 with Nginx via cloud-init |
| ALB | `nginx-alb` | Application Load Balancer on port 80 |
| Target Group | `nginx-alb-tg` | HTTP health-checked group for both instances |
| Listener | HTTP :80 | Forwards traffic to the target group |

**Prerequisites:**

- Spinifex installed and running (see [Installing Spinifex](/docs/install))
- A Debian 12 AMI imported (see [Setting Up Your Cluster](/docs/setting-up-your-cluster))
- OpenTofu or Terraform installed
- `AWS_PROFILE=spinifex` configured

## Instructions

### Step 1. Get the Template

Clone the Terraform examples from the Spinifex repository:

```bash
git clone --depth 1 --filter=blob:none --sparse https://github.com/mulgadc/spinifex.git spinifex-tf
cd spinifex-tf
git sparse-checkout set docs/terraform
cd docs/terraform/nginx-alb
```

Or create a `main.tf` file and paste the full configuration below.

<!-- INCLUDE: main.tf lang:hcl -->

### Step 2. Deploy

```bash
export AWS_PROFILE=spinifex
tofu init
tofu apply
```

### Step 3. Verify

> **Note:** EC2 instances can take 30+ seconds to boot after apply. If SSH or HTTP is unreachable, wait and retry.

After apply completes, use the outputs to test:

```bash
# Hit the ALB — successive requests should alternate between Server 1 and Server 2
curl http://<alb_public_ip>

# Direct instance access
curl http://<instance_1_public_ip>
curl http://<instance_2_public_ip>

# SSH into either instance
ssh -i nginx-alb-demo.pem ec2-user@<instance_public_ip>

# Check target health via AWS CLI
aws elbv2 describe-target-health --target-group-arn <tg_arn>
```

Open and refresh the `http://<alb_public_ip>` output in your browser to see the page alternate content served from each instance.

### Cleanup

```bash
tofu destroy
```

## Troubleshooting

### AMI Not Found

Ensure you have imported a Debian 12 image. Check available AMIs:

```bash
aws ec2 describe-images --owners 000000000000 --profile spinifex
```

### Provider Connection Refused

Verify Spinifex services are running:

```bash
sudo systemctl status spinifex.target
curl -k https://localhost:9999/
```

### SSH Connection Timeout

Check that the security group allows inbound SSH (port 22) and that the instance has a public IP assigned. Verify the instance is running:

```bash
aws ec2 describe-instances --profile spinifex
```

### Nginx Not Responding

SSH into the instance and check cloud-init logs:

```bash
ssh -i nginx-alb-demo.pem ec2-user@<instance_public_ip>
sudo journalctl -u cloud-init --no-pager
sudo systemctl status nginx
```
