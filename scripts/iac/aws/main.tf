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

variable "region" {
  type    = string
  default = "ap-southeast-2" # Sydney, change if you want
}

# MulgaOS AWS-compatible endpoints
# Set these environment variables or use terraform.tfvars:
#   export AWS_ACCESS_KEY_ID=your_access_key
#   export AWS_SECRET_ACCESS_KEY=your_secret_key
#   export TF_VAR_mulgaos_endpoint=http://localhost:8080
variable "mulgaos_endpoint" {
  type        = string
  default     = "http://localhost:8080"
  description = "MulgaOS API endpoint (awsd gateway)"
}

provider "aws" {
  region = var.region

  # MulgaOS custom endpoints
  endpoints {
    ec2 = var.mulgaos_endpoint
    s3  = var.mulgaos_endpoint
    iam = var.mulgaos_endpoint
    sts = var.mulgaos_endpoint
  }

  # Skip AWS credential validation (MulgaOS handles auth)
  skip_credentials_validation = true
  skip_metadata_api_check     = true
  skip_requesting_account_id  = true

  # For local development, skip region validation
  skip_region_validation = true
}

data "aws_availability_zones" "available" {
  state = "available"
}

# Debian 12 AMI (official Debian account)
# For MulgaOS, you may need to import a local AMI first
data "aws_ami" "debian12" {
  most_recent = true
  owners      = ["136693071363"] # Debian official

  filter {
    name   = "name"
    values = ["debian-12-amd64-*"]
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

# Keypair: hive-test
resource "tls_private_key" "hive_test" {
  algorithm = "ED25519"
}

resource "aws_key_pair" "hive_test" {
  key_name   = "hive-test"
  public_key = tls_private_key.hive_test.public_key_openssh
}

# Save private key locally for SSH
resource "local_file" "hive_test_pem" {
  filename        = "${path.module}/hive-test.pem"
  content         = tls_private_key.hive_test.private_key_openssh
  file_permission = "0600"
}

# 1: VPC 10.1.1.0/16
resource "aws_vpc" "main" {
  cidr_block           = "10.1.1.0/16"
  enable_dns_hostnames = true
  enable_dns_support   = true

  tags = {
    Name = "hive-test-vpc"
  }
}

# Internet access for a public subnet
resource "aws_internet_gateway" "igw" {
  vpc_id = aws_vpc.main.id

  tags = {
    Name = "hive-test-igw"
  }
}

# 2: Subnet 10.1.2.0/24 (public)
resource "aws_subnet" "public" {
  vpc_id                  = aws_vpc.main.id
  cidr_block              = "10.1.2.0/24"
  availability_zone       = data.aws_availability_zones.available.names[0]
  map_public_ip_on_launch = true

  tags = {
    Name = "hive-test-public-subnet"
  }
}

resource "aws_route_table" "public" {
  vpc_id = aws_vpc.main.id

  route {
    cidr_block = "0.0.0.0/0"
    gateway_id = aws_internet_gateway.igw.id
  }

  tags = {
    Name = "hive-test-public-rt"
  }
}

resource "aws_route_table_association" "public" {
  subnet_id      = aws_subnet.public.id
  route_table_id = aws_route_table.public.id
}

# 3: Security group
resource "aws_security_group" "web_ssh" {
  name        = "hive-test-sg"
  description = "Allow inbound 22,80,443; allow all outbound"
  vpc_id      = aws_vpc.main.id

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

  ingress {
    description = "HTTPS"
    from_port   = 443
    to_port     = 443
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
    Name = "hive-test-sg"
  }
}

# 4: Ensure 3 servers on different underlying hardware (spread placement group)
resource "aws_placement_group" "spread" {
  name     = "hive-test-spread"
  strategy = "spread"
}

resource "aws_instance" "vm" {
  count         = 3
  ami           = data.aws_ami.debian12.id
  instance_type = "t3.small"

  subnet_id              = aws_subnet.public.id
  vpc_security_group_ids = [aws_security_group.web_ssh.id]
  key_name               = aws_key_pair.hive_test.key_name

  associate_public_ip_address = true
  placement_group             = aws_placement_group.spread.name

  tags = {
    Name = "hive-test-vm-${count.index + 1}"
  }
}

output "instance_public_ips" {
  value = aws_instance.vm[*].public_ip
}

output "ssh_commands" {
  value = [
    for ip in aws_instance.vm[*].public_ip :
    "ssh -i hive-test.pem admin@${ip}"
  ]
}
