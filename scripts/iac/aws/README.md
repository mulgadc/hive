# MulgaOS AWS Infrastructure as Code

This directory contains OpenTofu/Terraform configuration files for deploying infrastructure on MulgaOS using AWS-compatible APIs.

## Prerequisites

1. **OpenTofu or Terraform installed**
   ```bash
   # Install OpenTofu (recommended)
   # See: https://opentofu.org/docs/intro/install/

   # Or use Terraform
   # See: https://developer.hashicorp.com/terraform/install
   ```

2. **MulgaOS services running**
   - `hive daemon` - Core daemon with NATS subscriptions
   - `awsd` - AWS API gateway (default: http://localhost:8080)
   - `predastore` - S3-compatible storage
   - `viperblock` - EBS-compatible block storage

3. **AWS credentials configured**
   ```bash
   # Option 1: Environment variables
   export AWS_ACCESS_KEY_ID=your_access_key
   export AWS_SECRET_ACCESS_KEY=your_secret_key

   # Option 2: AWS CLI profile
   aws configure --profile hive
   export AWS_PROFILE=hive
   ```

4. **AMI available**
   Before running, ensure you have a Debian 12 AMI imported:
   ```bash
   # Import an AMI using the hive CLI or aws CLI
   aws --endpoint-url http://localhost:8080 ec2 describe-images
   ```

## Configuration

### Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `TF_VAR_mulgaos_endpoint` | `http://localhost:8080` | MulgaOS API endpoint |
| `TF_VAR_region` | `ap-southeast-2` | AWS region (for compatibility) |
| `AWS_ACCESS_KEY_ID` | - | MulgaOS access key |
| `AWS_SECRET_ACCESS_KEY` | - | MulgaOS secret key |

### terraform.tfvars (optional)

Create a `terraform.tfvars` file for custom settings:

```hcl
mulgaos_endpoint = "http://192.168.1.100:8080"
region           = "us-west-2"
```

## Usage

### Initialize

```bash
cd iac/aws
tofu init
# or: terraform init
```

### Validate

```bash
tofu validate
# or: terraform validate
```

### Plan

```bash
tofu plan
# or: terraform plan
```

### Apply

```bash
tofu apply
# or: terraform apply
```

### Destroy

```bash
tofu destroy
# or: terraform destroy
```

## What Gets Created

The `main.tf` creates:

1. **VPC** (`10.1.1.0/16`) - Virtual Private Cloud
2. **Internet Gateway** - For public internet access
3. **Public Subnet** (`10.1.2.0/24`) - With auto-assign public IP
4. **Route Table** - Routes `0.0.0.0/0` through IGW
5. **Security Group** - Allows SSH (22), HTTP (80), HTTPS (443) inbound
6. **Placement Group** - Spread strategy for HA
7. **3 EC2 Instances** - t3.small Debian 12 VMs
8. **SSH Key Pair** - ED25519 key saved to `hive-test.pem`

## SSH Access

After `tofu apply`, SSH into instances:

```bash
# Using the generated key
ssh -i hive-test.pem admin@<public_ip>

# For local development with QEMU port forwarding
# Find the forwarded port from QEMU args (-netdev user,id=net0,hostfwd=tcp:127.0.0.1:PORT-:22)
ssh -p PORT -i hive-test.pem admin@127.0.0.1
```

## Troubleshooting

### "No valid credential sources found"

Ensure AWS credentials are set:
```bash
export AWS_ACCESS_KEY_ID=your_key
export AWS_SECRET_ACCESS_KEY=your_secret
```

### "Error: No AMI found"

Import a Debian 12 AMI first, or modify `main.tf` to use a different AMI filter.

### Connection refused

Ensure MulgaOS services are running:
```bash
# Check if awsd gateway is running
curl http://localhost:8080/health

# Check daemon status
hive daemon status
```

### "InvalidAction" errors

Some AWS operations may not be implemented yet. Check `TODO.md` for the current implementation status.

## Customization

### Using a different AMI

Modify the `data "aws_ami"` block in `main.tf`:

```hcl
data "aws_ami" "custom" {
  most_recent = true
  owners      = ["self"]  # Your imported AMIs

  filter {
    name   = "name"
    values = ["my-custom-ami-*"]
  }
}
```

### Changing instance count

Modify the `count` parameter:

```hcl
resource "aws_instance" "vm" {
  count = 5  # Change from 3 to 5
  # ...
}
```

### Different instance type

```hcl
resource "aws_instance" "vm" {
  instance_type = "t3.medium"  # Change from t3.small
  # ...
}
```

## Files

| File | Description |
|------|-------------|
| `main.tf` | Main Terraform/OpenTofu configuration |
| `hive-test.pem` | Generated SSH private key (after apply) |
| `.terraform/` | Provider plugins (after init) |
| `terraform.tfstate` | State file (after apply) |
| `terraform.tfvars` | Custom variables (optional) |

## Related Documentation

- [MulgaOS DEV.md](../../DEV.md) - Development guide
- [MulgaOS TODO.md](../../TODO.md) - Implementation status
- [OpenTofu Documentation](https://opentofu.org/docs/)
- [AWS Provider Documentation](https://registry.terraform.io/providers/hashicorp/aws/latest/docs)
