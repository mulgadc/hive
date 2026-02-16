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
make preflight            # Run ALL CI checks locally (format + vet + security + tests)
make build                # Build hive binary to ./bin/hive
make test                 # Run all unit tests (sets LOG_IGNORE=1)
make check-format         # Check gofmt (fails on diff, same as CI)
make format               # Fix gofmt in place
make vet                  # Run go vet (fails on issues)
make security-check       # Run govulncheck, gosec, staticcheck (fails on issues)
make bench                # Run benchmarks
make clean                # Remove build artifacts
make test-docker          # Run single + multi-node E2E in Docker (requires /dev/kvm)
make test-docker-single   # Single-node E2E only
make test-docker-multi    # Multi-node E2E only
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

## Development Process

All non-trivial work (features, bug fixes, improvements) follows a plan-first, document-as-you-go process. Plans are committed to git so they are visible to the whole team, reviewable in PRs, and serve as permanent documentation.

### When to write a plan

- **Required**: New features, multi-file changes, architectural changes, performance work, non-obvious bug fixes
- **Not required**: Typo fixes, single-line bug fixes, config tweaks, small refactors contained to one function

### Plan location

Plans live in `docs/development/` under the appropriate category:

```
docs/development/
├── bugs/              # Bug fixes
├── feature/           # New features
└── improvements/      # Performance, refactoring, tech debt
```

File naming: lowercase kebab-case describing the work, e.g. `nbd-performance.md`, `attach-volume.md`, `instance-state.md`.

### Plan lifecycle

#### 1. Planning phase

When entering plan mode for non-trivial work, **create the plan document first** and write it to the appropriate `docs/development/{category}/` path. The plan must include:

- **Summary** — What and why, in 1-3 sentences
- **Context / Problem Statement** — Current behavior, what's wrong or missing
- **Proposed Changes** — What will change, which files, key design decisions
- **Files to Modify** — List of files with brief description of changes
- **Testing** — How the changes will be verified

The plan document is committed to git before implementation begins. This makes the plan reviewable and visible to the team.

#### 2. Implementation phase

During implementation, **update the plan document as you go**:

- Mark completed steps (use `[x]` checkboxes or note "Done" inline)
- Record findings, corrections, or deviations from the original plan
- Add code references (file paths, line numbers, struct/function names) for what was actually implemented
- Note any follow-up work discovered during implementation

#### 3. Completion

When development is complete, update the document to serve as **permanent reference documentation**:

- Add a status line near the top: `**Status: Complete**` (or `**Status: In Progress**`, `**Status: Planned**`)
- Replace future-tense plan language with past-tense description of what was done
- Add a **"Files Modified"** summary table
- Add a **"Future Work"** section if there are known follow-ups
- Keep the original context (benchmarks, research, root cause analysis) — this is valuable reference material
- The document should read as a complete technical reference, not just a plan

#### Example status markers

```markdown
**Status: Planned** — Awaiting review/approval before implementation
**Status: In Progress** — Implementation underway
**Status: Complete** — All changes implemented and tests passing
```

### Git history as context

When starting work on a feature or investigating an issue, review the last 10-20 git commits (`git log --oneline -20`) to understand the current development context. Recent commits contain valuable context about in-progress features, architectural decisions, and related changes.

### Existing examples

- `docs/development/improvements/nbd-performance.md` — Complete improvement with benchmarks, root cause analysis, implementation details, and future work
- `docs/development/feature/attach-volume.md` — Complete feature with request flow, files changed, and design decisions
- `docs/development/bugs/instance-state.md` — Bug fix with problem statement, proposed architecture, and state transitions

## Project Standards

- Use `log/slog` instead of `log`. Use appropriate log level, eg `slog.Debug`
- All new features must have comprehensive unit tests
- For returning AWS errors, use `awserrors` package instead of manually typed strings

**Preflight Policy:**
- Run `make preflight` before pushing any major changes — it runs the same checks as GitHub Actions (gofmt, go vet, gosec, staticcheck, govulncheck, and all unit tests)
- All developers must install the pre-push hook to enforce this automatically:

```bash
cat > .git/hooks/pre-push << 'EOF'
#!/bin/sh
make preflight
make test
EOF
chmod +x .git/hooks/pre-push
```

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

### Testing with AWS CLI

```bash
AWS_PROFILE=hive aws ec2 describe-instances
```
