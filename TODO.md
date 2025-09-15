# Hive Development Roadmap

This roadmap has been integrated into the comprehensive [HIVE_DEVELOPMENT_PLAN.md](HIVE_DEVELOPMENT_PLAN.md).

## Original TODO Items → Development Plan Integration

### ✅ **Completed Integration**

All original TODO items have been incorporated into the structured development phases:

| Original TODO | Status | Integrated Into | Phase |
|---------------|--------|-----------------|-------|
| #1: Binary compile and install.sh | ✅ Planned | Phase 7: Task 7.1 (Binary Compilation) | Phase 7 |
| #2: Move daemon.go to services/hived/ | ✅ Planned | Phase 3: Task 3.2 (Specialized Services) | Phase 3 |
| #3: VPC with openvs-switch as `vpcd` | ✅ Planned | Phase 5: Task 5.1 (VPC with Open vSwitch) | Phase 5 |
| #4: AWS HTTP gateway (`awsd`) | ✅ Planned | Phase 3: Task 3.1 (AWS Gateway Service) | Phase 3 |
| #5: AWS SDK v2 input/output | ✅ Planned | Phase 2: Task 2.1 & Phase 3: Task 3.1 | Phase 2-3 |
| #6: `hive.service` for systemd | ✅ Planned | Phase 7: Task 7.2 (System Service Integration) | Phase 7 |
| #7: Gossip and RAFT protocols | ✅ Planned | Phase 0: Task 0.1 (Service Registry) | Phase 0 |
| #8: etcd/KV for configuration sync | ✅ Planned | Phase 0: Task 0.1 (Distributed Config) | Phase 0 |
| #9: Smithy model code generation | ✅ Planned | Phase 2: Task 2.1 (Smithy Code Generation) | Phase 2 |

## Development Phase Overview

**Phase 0**: Distributed Systems Foundation (1-2 weeks)
- Gossip, RAFT, and distributed configuration (#7, #8)

**Phase 1**: Development Environment Automation (2-3 weeks)
- Multi-service orchestration and hot reloading

**Phase 2**: AWS API Model Implementation (3-4 weeks)
- Smithy-based code generation (#9)
- AWS SDK v2 integration (#5)

**Phase 3**: Scalable Gateway and Daemon Architecture (2-3 weeks)
- AWS gateway service `awsd` (#4)
- Service refactoring to `hived` (#2)
- VPC daemon `vpcd` foundation (#3)

**Phase 4**: Service Integration and Orchestration (3-4 weeks)
- NATS clustering and cross-service coordination

**Phase 5**: Infrastructure Services (4-5 weeks)
- VPC networking with Open vSwitch (#3)
- Advanced VM features

**Phase 6**: Testing and Validation Framework (2-3 weeks)
- AWS CLI compatibility testing

**Phase 7**: Production Deployment and Packaging (2-3 weeks)
- Binary compilation and distribution (#1)
- Systemd service integration (#6)
- Production configuration management

## Service Architecture

The development plan implements these services:

- **`awsd`** - AWS API Gateway (TODO #4)
- **`hived`** - Main Hive compute daemon (TODO #2)
- **`vpcd`** - VPC networking daemon (TODO #3)
- **Predastore** - S3 service (existing)
- **Viperblock** - EBS service (existing)

## Getting Started

For development setup:
```bash
./scripts/dev-setup.sh     # Setup complete development environment
./scripts/start-dev.sh     # Start all services
```

For detailed implementation guidance, see [HIVE_DEVELOPMENT_PLAN.md](HIVE_DEVELOPMENT_PLAN.md).