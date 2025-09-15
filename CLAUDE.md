# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is the Hive platform - an AWS-compatible infrastructure stack for bare-metal, edge, and on-premise deployments. Hive orchestrates multiple independent components that work together to provide AWS-compatible services.

## Component Repositories

The Hive platform consists of these independent repositories:

- **[viperblock](../viperblock/)** - High-performance block storage service (EBS alternative) with WAL-backed storage
- **[predastore](../predastore/)** - S3-compatible object storage service with Reed-Solomon erasure coding
- **nbdkit/** - Network Block Device (NBD) server for exporting block devices (upstream project)
- **hive/** (this repo) - VM orchestration service and platform coordinator

Each component can be used independently or as part of the integrated Hive stack. See component-specific `CLAUDE.md` files for detailed development guidance:
- `../viperblock/CLAUDE.md` - Viperblock development guide
- `../predastore/CLAUDE.md` - Predastore development guide

## Cross-Repository Development Setup

For platform development, clone all repositories to the same parent directory:
```bash
mkdir mulga
cd mulga
git clone <hive-repo-url> hive
git clone <viperblock-repo-url> viperblock
git clone <predastore-repo-url> predastore
```

The `hive/go.mod` uses local replace directives for cross-component development:
```go
replace github.com/mulgadc/viperblock => ../viperblock
replace github.com/mulgadc/predastore => ../predastore
```

## Build Commands

### Hive (Platform Orchestrator)
```bash
make build          # Build the hive binary
make test           # Run Go tests
make bench          # Run benchmarks
make clean          # Clean build artifacts
```

### Component Build Commands
For detailed build instructions, see component-specific CLAUDE.md files:

**Viperblock** (`../viperblock/CLAUDE.md`):
```bash
cd ../viperblock && make build    # Build sfs, vblock binaries + NBD plugin
```

**Predastore** (`../predastore/CLAUDE.md`):
```bash
cd ../predastore && make build    # Build s3d binary
```

**NBDkit** (system dependency):
```bash
cd nbdkit && autoreconf -i && ./configure && make && make install
```

## Architecture Notes

### Message-Driven Microservices Architecture

Hive uses a **scalable message-driven architecture** that separates API handling from request processing:

```
AWS SDK (custom endpoint) → Hive API Gateway → NATS Topics → Specialized Daemons
                                    ↑                              ↓
                         AWS-compatible response ←  NATS Topics  ←  Processing Results
```

#### Core Components:

1. **Hive API Gateway Service** - TLS endpoint that handles AWS SDK requests
2. **Specialized Daemon Services** - Horizontally scalable workers for each AWS service type
3. **NATS Message Broker** - Topic-based routing with queue groups for load balancing

#### Current Implementation (daemon.go):
- **Gateway Logic**: `StartAWSGateway()` handles AWS API calls on port 9999
- **NATS Integration**: Uses request-response pattern: `d.natsConn.Request("ec2.runinstances", data, 30s)`
- **Queue Groups**: Load balancing via `"hive-workers"` queue group
- **Service Coordination**: All inter-service communication via NATS topics

### Inter-component Dependencies
- **Service Startup Order**: NATS → Predastore → Viperblock → NBDkit → Gateway → Daemons
- **Message Flow**: Gateway translates AWS calls to NATS messages, daemons process and respond
- **Scaling**: Multiple daemon instances can subscribe to same topics for horizontal scaling
- All Go projects use local module replacements for development

### Storage Backend Integration
- **Viperblock Integration**: EBS volumes mounted via NATS messaging (`ebs.mount` topic)
- **Predastore Integration**: S3 operations proxied through dedicated S3 daemons
- **NBDkit Integration**: Block device access for VM storage
- **Volume Lifecycle**: Coordinated through NATS between EC2 and EBS daemons

### Key Design Patterns
- **Message-Driven**: All service communication via NATS topics (ec2.*, ebs.*, s3.*, vpc.*)
- **Queue Groups**: Load balancing and fault tolerance via NATS queue groups
- **Request-Response**: 30-second timeout pattern for AWS API compatibility
- **Service Specialization**: Dedicated daemon types for each AWS service
- **Horizontal Scaling**: Multiple daemon instances handle same topic types

### NATS Topic Structure

The system uses structured NATS topics for service communication:

```bash
# EC2 Service Topics:
ec2.runinstances              # Launch new instances
ec2.describeinstances         # Query instance status
ec2.startinstances           # Start stopped instances
ec2.stopinstances            # Stop running instances
ec2.terminateinstances       # Terminate instances

# EBS Service Topics:
ebs.createvolume             # Create new EBS volume
ebs.attachvolume             # Attach volume to instance
ebs.detachvolume             # Detach volume
ebs.describevolumes          # List volumes
ebs.mount                    # Mount volume (internal)
ebs.unmount                  # Unmount volume (internal)

# S3 Service Topics:
s3.createbucket              # Create S3 bucket
s3.putobject                 # Store object
s3.getobject                 # Retrieve object
s3.listbuckets               # List buckets

# VPC Service Topics:
vpc.createvpc                # Create VPC
vpc.createsubnet             # Create subnet
vpc.describevpcs             # List VPCs

# Health and Discovery:
health.ec2.daemon.{id}       # Daemon health heartbeats
discovery.services           # Service registration
```

### Go Module Structure
All Go projects use:
- Go 1.23+ with module mode
- Local replace directives for cross-component development
- Standard build flags: `-ldflags "-s -w"` for optimized binaries
- Test environment variable: `LOG_IGNORE=1` to suppress logs during testing

### Development Workflow

#### Quick Setup (Recommended)
```bash
# 1. Clone dependencies (if not already done)
./scripts/clone-deps.sh

# 2. Setup development environment
./scripts/dev-setup.sh

# 3. Start all services
./scripts/start-dev.sh

# 4. Stop all services when done
./scripts/stop-dev.sh
```

#### Manual Development Process
1. **Cross-repo Setup**: Clone all repositories to same parent directory
2. **Service Dependencies**: Start services in order: NATS → Predastore → Viperblock → NBDkit → Gateway → Daemons
3. **Build Order**: Build components: predastore → viperblock → hive
4. **Testing**: Use `make test` in each component to verify changes
5. **Message-Driven Development**: Test NATS message flows between gateway and daemons
6. **Scaling**: Test with multiple daemon instances subscribing to same topics

#### Development Tools
- **Hot Reloading**: Use `air` for automatic restarts during development
- **TLS Certificates**: Auto-generated self-signed certificates for HTTPS endpoints
- **Log Monitoring**: Centralized logs in `data/logs/` directory
- **AWS CLI Testing**: Test endpoints with `aws --endpoint-url https://localhost:9999`

### Development Roadmap
For major feature development and architectural changes, refer to `HIVE_DEVELOPMENT_PLAN.md` which contains:
- Comprehensive roadmap for transforming Hive from proof-of-concept to beta release
- Detailed implementation phases with specific tasks and file structures
- AWS API compatibility implementation guidelines
- Development environment automation plans
- Testing and validation frameworks

When implementing new AWS services or enhancing existing functionality, follow the structured approach outlined in the development plan.