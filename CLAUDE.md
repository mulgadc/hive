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

## Build Commands

### Hive (Platform Orchestrator)
```bash
make build          # Build the hive binary
make test           # Run Go tests
make bench          # Run benchmarks
make clean          # Clean build artifacts
```

### **MANDATORY: Unit Testing Requirements**

**⚠️ CRITICAL: Always run unit tests before any commit or push operation.**

```bash
# REQUIRED before any git commit or push
make test           # Must pass with zero failures

# Example workflow:
make test                    # Verify all tests pass
git add .                    # Stage changes
git commit -m "..."          # Only after tests pass
git push origin main         # Only after tests pass
```

**Testing Policy:**
- **All unit tests MUST pass** before committing changes
- **No exceptions** - failing tests block commits
- Tests must complete without errors or panics
- Use `make test` command to run the full test suite
- If tests fail, fix issues before proceeding with git operations

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
- **Message-Driven**: All service communication via NATS topics
- **Queue Groups**: Load balancing and fault tolerance via NATS queue groups
- **Request-Response**: 30-second timeout pattern for AWS API compatibility
- **Service Specialization**: Dedicated daemon types for each AWS service
- **Horizontal Scaling**: Multiple daemon instances handle same topic types

### NATS Topic Structure

- **EC2 Daemons**: Listen on `ec2.*` topics, manage QEMU/KVM instances
- **EBS Daemons**: Listen on `ebs.*` topics, coordinate with Viperblock
- **S3 Daemons**: Listen on `s3.*` topics, proxy to Predastore
- **VPC Daemons**: Listen on `vpc.*` topics, manage networking

```bash
# Health and Discovery:
health.ec2.daemon.{id}       # Daemon health heartbeats
discovery.services           # Service registration
```

### Go Module Structure
All Go projects use:
- Go 1.25+ with module mode
- Local replace directives for cross-component development
- Standard build flags: `-ldflags "-s -w"` for optimized binaries
- Test environment variable: `LOG_IGNORE=1` to suppress logs during testing

### Development Workflow

#### Development Process
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
