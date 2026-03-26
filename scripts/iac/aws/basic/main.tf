# Spinifex IaC Compatibility Test — "basic" suite
#
# Exercises every AWS resource Spinifex currently supports via tofu apply,
# WITHOUT route tables, NACLs, or other unimplemented networking.
#
# Usage:
#   cd spinifex/scripts/iac/aws/basic
#   tofu init
#   tofu plan
#   tofu apply        # full round-trip against a running Spinifex cluster
#   tofu destroy      # clean teardown

terraform {
  required_version = ">= 1.6.0"

  required_providers {
    aws = {
      source  = "hashicorp/aws"
      version = ">= 5.0"
    }
    tls = {
      source  = "hashicorp/tls"
      version = ">= 4.0"
    }
    local = {
      source  = "hashicorp/local"
      version = ">= 2.0"
    }
  }
}

# ---------------------------------------------------------------------------
# Variables
# ---------------------------------------------------------------------------

variable "region" {
  type    = string
  default = "ap-southeast-2"
}

variable "mulgaos_endpoint" {
  type        = string
  default     = "https://localhost:9999"
  description = "Spinifex AWS gateway endpoint"
}

# ---------------------------------------------------------------------------
# Provider
# ---------------------------------------------------------------------------

provider "aws" {
  region = var.region

  endpoints {
    ec2 = var.mulgaos_endpoint
    s3  = var.mulgaos_endpoint
    iam = var.mulgaos_endpoint
    sts = var.mulgaos_endpoint
  }

  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true
  skip_region_validation      = true
}

# ---------------------------------------------------------------------------
# Data sources
# ---------------------------------------------------------------------------

data "aws_availability_zones" "available" {
  state = "available"
}

data "aws_ami" "debian12" {
  most_recent = true
  owners      = ["000000000000"]

  filter {
    name   = "name"
    values = ["*debian-12*"]
  }

  filter {
    name   = "virtualization-type"
    values = ["hvm"]
  }

  filter {
    name   = "root-device-type"
    values = ["ebs"]
  }
}

# ---------------------------------------------------------------------------
# 1. VPC + Subnet (no route table needed)
# ---------------------------------------------------------------------------

resource "aws_vpc" "test" {
  cidr_block           = "10.99.0.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "spx-basic-test-vpc"
  }
}

resource "aws_subnet" "test" {
  vpc_id                  = aws_vpc.test.id
  cidr_block              = "10.99.1.0/24"
  availability_zone       = data.aws_availability_zones.available.names[0]
  map_public_ip_on_launch = true

  tags = {
    Name = "spx-basic-test-subnet"
  }
}

# ---------------------------------------------------------------------------
# 2. Internet Gateway (attach to VPC, but no route table referencing it)
# ---------------------------------------------------------------------------

resource "aws_internet_gateway" "test" {
  vpc_id = aws_vpc.test.id

  tags = {
    Name = "spx-basic-test-igw"
  }
}

# ---------------------------------------------------------------------------
# 3. Security Group — inbound SSH + HTTP, all outbound
# ---------------------------------------------------------------------------

resource "aws_security_group" "test" {
  name        = "spx-basic-test-sg"
  description = "Basic test: SSH + HTTP inbound, all outbound"
  vpc_id      = aws_vpc.test.id

  ingress {
    description = "SSH"
    from_port   = 22
    to_port     = 22
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  ingress {
    description = "HTTP"
    from_port   = 80
    to_port     = 80
    protocol    = "tcp"
    cidr_blocks = ["0.0.0.0/0"]
  }

  egress {
    description = "All outbound"
    from_port   = 0
    to_port     = 0
    protocol    = "-1"
    cidr_blocks = ["0.0.0.0/0"]
  }

  tags = {
    Name = "spx-basic-test-sg"
  }
}

# ---------------------------------------------------------------------------
# 4. Key Pair
# ---------------------------------------------------------------------------

resource "tls_private_key" "test" {
  algorithm = "ED25519"
}

resource "aws_key_pair" "test" {
  key_name   = "spx-basic-test-key"
  public_key = tls_private_key.test.public_key_openssh
}

resource "local_file" "test_pem" {
  filename        = "${path.module}/spx-basic-test.pem"
  content         = tls_private_key.test.private_key_openssh
  file_permission = "0600"
}

# ---------------------------------------------------------------------------
# 5. Placement Group
# ---------------------------------------------------------------------------

resource "aws_placement_group" "test" {
  name     = "spx-basic-test-spread"
  strategy = "spread"
}

# ---------------------------------------------------------------------------
# 6. EC2 Instance
# ---------------------------------------------------------------------------

resource "aws_instance" "test" {
  ami           = data.aws_ami.debian12.id
  instance_type = "t3.small"

  subnet_id              = aws_subnet.test.id
  vpc_security_group_ids = [aws_security_group.test.id]
  key_name               = aws_key_pair.test.key_name
  placement_group        = aws_placement_group.test.name

  associate_public_ip_address = true

  tags = {
    Name        = "spx-basic-test-vm"
    Environment = "test"
  }
}

# ---------------------------------------------------------------------------
# 7. EBS Volume + Attachment
# ---------------------------------------------------------------------------

resource "aws_ebs_volume" "data" {
  availability_zone = data.aws_availability_zones.available.names[0]
  size              = 10
  type              = "gp3"

  tags = {
    Name = "spx-basic-test-data-vol"
  }
}


# ---------------------------------------------------------------------------
# 8. EBS Snapshot (of the standalone volume)
# ---------------------------------------------------------------------------

resource "aws_ebs_snapshot" "data" {
  volume_id = aws_ebs_volume.data.id

  tags = {
    Name = "spx-basic-test-snapshot"
  }
}

# ---------------------------------------------------------------------------
# 9. Elastic IP + Association
#    Requires external IPAM (pool mode) — uncomment when available.
# ---------------------------------------------------------------------------

# resource "aws_eip" "test" {
#   domain = "vpc"
#
#   tags = {
#     Name = "spx-basic-test-eip"
#   }
# }
#
# resource "aws_eip_association" "test" {
#   allocation_id = aws_eip.test.id
#   instance_id   = aws_instance.test.id
# }

# ---------------------------------------------------------------------------
# 10. Standalone Network Interface (not attached — just tests CRUD)
# ---------------------------------------------------------------------------

resource "aws_network_interface" "extra" {
  subnet_id       = aws_subnet.test.id
  security_groups = [aws_security_group.test.id]

  tags = {
    Name = "spx-basic-test-eni"
  }
}

# ---------------------------------------------------------------------------
# Outputs
# ---------------------------------------------------------------------------

output "vpc_id" {
  value = aws_vpc.test.id
}

output "instance_id" {
  value = aws_instance.test.id
}

output "instance_public_ip" {
  value = aws_instance.test.public_ip
}

# output "eip_public_ip" {
#   value = aws_eip.test.public_ip
# }

output "volume_id" {
  value = aws_ebs_volume.data.id
}

output "snapshot_id" {
  value = aws_ebs_snapshot.data.id
}

output "ssh_command" {
  value = "ssh -i spx-basic-test.pem admin@${aws_instance.test.public_ip}"
}
