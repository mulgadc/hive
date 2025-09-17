# Hive: An Open Source AWS-Compatible Stack for Bare-Metal, Edge, and On-Prem Deployments

(Developer Preview) - Hive developed by [Mulga Defense Corporation](https://mulgadc.com/) is an open source infrastructure platform that brings the core services of AWS—like EC2, VPC, EBS, and S3—to environments where running in the cloud isn't an option. Whether you're deploying to edge sites, private data-centers, or need to operate in low-connectivity or highly contested environments, Hive gives you AWS-style workflows on your own hardware.

## What is Hive?

Hive replicates essential AWS primitives—virtual machines, block volumes, and object storage—using lightweight, self-contained components that are easy to deploy and integrate.

It’s designed for developers and operators who need:

- Cloud-like infrastructure without the cloud
- Minimal dependencies, full control
- Drop-in compatibility with tools like the AWS CLI, SDKs, and Terraform
- A secure cloud environment you control and own the entire hardware, network and software stack

You can run Hive on a few servers in a rack, a field site, or anywhere centralized cloud services aren’t feasible.

## Core Components

### Hive (Compute Service – EC2 Alternative)

Hive is a minimal VM orchestration layer built on top of QEMU, exposing APIs similar to EC2. It manages lifecycle operations like start, stop, and terminate, using QEMU’s QMP interface. Designed to be straightforward and scriptable, Hive lets you launch VMs using the AWS CLI, SDKs, or Terraform—without needing Kubernetes or heavyweight orchestrators. Keep in mind, you can also setup a Kubernetes environment using Hive with underlying instances.

- EC2-like VM management on bare metal
- Launches with cloud-init metadata support
- Works with standard AWS tooling

### Viperblock (Block Storage – EBS Alternative)

[Viperblock](https://github.com/mulgadc/viperblock) is a high-performance, WAL-backed block storage service that replicates volumes across multiple nodes. It’s built for reliability and speed, with support for snapshots, recovery, and direct connection to QEMU instances using NBD or virtio-blk.

- Fast, durable virtual disks
- Replication for resilience
- Exposed over NBD or embedded in VMs
- Supports high performance WAL logs using local NVMe drives to reduce IO traffic to S3.
- In memory read/write block cache for blazing performance.

### Predastore (Object Storage – S3-Compatible)

[Predastore](https://github.com/mulgadc/predastore) is a fully S3-compatible object storage system. It supports the AWS S3 API, including Signature V4 authentication, multipart uploads, and Terraform provisioning. Data is chunked and distributed across nodes using Reed-Solomon erasure coding, making it fault-tolerant and ideal for large-scale or low-bandwidth scenarios.

- S3-compatible API and auth
- Multipart uploads, streaming reads/writes
- Data redundancy with Reed-Solomon encoding

## Key Features

AWS-Compatible Interfaces – Provision infrastructure with awscli, Terraform, or SDKs you already use.

- Designed for Control – Run on your own terms, whether that’s in a datacenter, remote site, or sensitive environment.
- Minimal Dependencies – Each service is standalone and avoids complex orchestration layers.
- Works Offline – No reliance on centralized cloud services or external networks.
- Open Source – Licensed under Apache 2.0. Fork it, extend it, or deploy it as-is.

## Development Setup

### For Developers

Get started with Hive development in minutes:

```bash
# Clone the repository
git clone https://github.com/mulgadc/hive.git hive
cd hive

# Setup dependencies and development environment
./scripts/clone-deps.sh    # Clone viperblock + predastore repositories
./scripts/dev-setup.sh     # Setup complete development environment

# Start all services (NATS, Predastore, Viperblock, Hive Gateway)
./scripts/start-dev.sh

# Provision a local EC2 instance running on Hive
aws --endpoint-url https://localhost:9999 --no-verify-ssl ec2 run-instances \
  --image-id ami-185c47c7b6d31bba9 \
  --instance-type t3.micro \
  --key-name test-keypair \
  --security-group-ids sg-0123456789abcdef0 \
  --subnet-id subnet-6e7f829e \
  --count 1

# Validate instance
aws --endpoint-url https://localhost:9999 --no-verify-ssl ec2 describe-instances

# Stop all services when done
./scripts/stop-dev.sh
```

**Development Features:**

- **Hot Reloading**: Automatic service restarts during development
- **Multi-Repository**: Seamless cross-component development workflow
- **TLS Ready**: Auto-generated certificates for HTTPS endpoints
- **AWS Compatible**: Test with real AWS CLI and SDKs

### Component Repositories

Hive coordinates these independent components:

- **[Predastore](https://github.com/mulgadc/predastore)** - S3-compatible object storage
- **[Viperblock](https://github.com/mulgadc/viperblock)** - EBS-compatible block storage

Each component can be developed independently. See component-specific documentation for focused development guides.

## Quick Start

See [Installation Guide](INSTALL.md) on how to setup a Hive node to get started.

Then configure your AWS CLI to point to Hive's endpoints and start launching VMs, buckets, and volumes using familiar commands and Terraform scripts.

## Development Philosophy

### Built by Engineers, For Engineers

Hive is developed by experienced infrastructure engineers with deep AWS expertise, including former AWS team members who understand the intricacies of building production-grade cloud services. Our team brings decades of combined experience from AWS, enterprise infrastructure, and edge computing environments.

**Real-World Experience:**

- Production AWS service development and operations
- Large-scale infrastructure deployment and management
- Edge computing and resource-constrained environments
- Enterprise security and compliance requirements

### AI-Assisted Development

While Hive is architected and implemented by experienced engineers, we leverage **Claude Code** (Anthropic's AI coding assistant) to accelerate certain development tasks. This approach combines human expertise with AI efficiency:

**How We Use Claude Code:**

- **Code Generation**: Boilerplate AWS API structures and handlers
- **Documentation**: Comprehensive development guides and API documentation
- **Testing**: Test case generation and validation scenarios
- **Refactoring**: Large-scale code restructuring and optimization

**What Remains Human-Driven:**

- **Architecture Decisions**: Core system design and scalability choices
- **Security Implementation**: Authentication, encryption, and threat modeling
- **Performance Optimization**: Real-world performance tuning and benchmarking
- **Production Operations**: Deployment strategies and operational procedures

**Development Artifacts:**

- `CLAUDE.md` - Instructions for Claude Code when working on this codebase
- `HIVE_DEVELOPMENT_PLAN.md` - Comprehensive roadmap and implementation strategy
- Component-specific guidance in `viperblock/CLAUDE.md` and `predastore/CLAUDE.md`

This hybrid approach ensures Hive benefits from both proven engineering expertise and modern development acceleration, while maintaining the quality and reliability standards required for production infrastructure.

## License

Hive is open source under the Apache 2.0 License. You're free to use, modify, and deploy it—anywhere you need reliable infrastructure without depending on centralized cloud platforms
