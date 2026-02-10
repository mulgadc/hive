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
- `../viperblock/CLAUDE.md`
- `../predastore/CLAUDE.md`

## Commands

### Go (backend)

```bash
make build          # Build hive binary to ./bin/hive
make test           # Run all tests (sets LOG_IGNORE=1)
make bench          # Run benchmarks
make format         # Run gofmt
make security       # Run go vet, govulncheck, gosec, staticcheck
make clean          # Remove build artifacts
```

### Frontend (hive-ui)

```bash
make build-ui                                    # Production build
cd hive/services/hiveui/frontend && pnpm dev     # Dev server on port 3000
cd hive/services/hiveui/frontend && pnpm fix     # Biome lint/format fix
cd hive/services/hiveui/frontend && pnpm build   # Build + typecheck
```

Frontend stack: React 19, TanStack Router/Query, Tailwind CSS v4, Biome (lint/format), Vite, pnpm. See `.claude/rules/frontend-standards.md` for coding standards.

### Dev Environment

```bash
scripts/dev-setup.sh    # One-time setup: creates directories, generates config/certs
scripts/start-dev.sh    # Start all services (NATS → Predastore → Viperblock → NBDkit → Gateway → Daemon → UI)
scripts/stop-dev.sh     # Stop all services
```

Data directory: `~/hive/` (logs in `~/hive/logs/`, config in `~/hive/config/`)

## Project Standards

- Use `log/slog` instead of `log`. Use appropriate log level, eg `slog.Debug`
- All new features must have comprehensive unit tests
- For returning AWS errors, use `awserrors` package instead of manually typed strings

**Testing Policy:**
- **All unit tests MUST pass** before committing changes
- **No exceptions** - failing tests block commits
- Tests must complete without errors or panics

## Architecture

Read `DESIGN.md` to understand the full architecture.

### Message-Driven Architecture

```
AWS SDK (custom endpoint) → AWS Gateway (port 9999) → NATS → Specialized Daemons → Response
```

- **AWS Gateway** (`hive/services/awsgw/`): TLS endpoint, SigV4 auth, routes to service handlers via NATS
- **Daemons** (`hive/daemon/`): Subscribe to NATS topics, process requests, manage QEMU/KVM VMs
- **NATS Topics**: `ec2.*`, `ebs.*`, `s3.*`, `vpc.*` — queue groups (`hive-workers`) for load balancing, no queue group for fan-out (e.g., DescribeInstances)
- **Storage**: Viperblock for EBS volumes (`ebs.mount` topic), Predastore for S3/AMI/key storage

### Go Workspace

Uses `go.work` with local module replacements — all repos must be cloned to the same parent directory:
```
mulga/
├── hive/         # This repo
├── predastore/   # S3-compatible storage
└── viperblock/   # Block storage
```

Build flag: `GOEXPERIMENT=greenteagc` is used for the GC experiment.

### Testing with AWS CLI

```bash
AWS_PROFILE=hive aws ec2 describe-instances
```
