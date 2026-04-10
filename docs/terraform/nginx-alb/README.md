# Nginx Web Servers with ALB

Deploys two Nginx web server instances behind an Application Load Balancer on Spinifex.

## What gets created

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

## Prerequisites

- Spinifex running locally (default: `https://localhost:9999`)
- OpenTofu >= 1.6.0
- AWS CLI profile `spinifex` configured

## Deploy

```bash
cd spinifex/docs/terraform/nginx-alb
export AWS_PROFILE=spinifex
tofu init
tofu apply
```

## Verify

```bash
# Hit the ALB — successive requests should alternate between Server 1 and Server 2
curl http://<alb_dns_name>

# Direct instance access
curl http://<instance_1_public_ip>
curl http://<instance_2_public_ip>

# SSH into either instance
ssh -i nginx-alb-demo.pem ec2-user@<instance_public_ip>

# Check target health via AWS CLI
aws elbv2 describe-target-health --target-group-arn <tg_arn>
```

## Cleanup

```bash
tofu destroy
```
