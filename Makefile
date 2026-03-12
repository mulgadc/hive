GO_PROJECT_NAME := hive
SHELL := /bin/bash

# Detect architecture for cross-platform support
ARCH := $(shell uname -m)
ifeq ($(ARCH),x86_64)
  GO_ARCH := amd64
  AWS_ARCH := x86_64
  # On x86, we can run ARM VMs via emulation
  QEMU_PACKAGES := qemu-system-x86 qemu-system-arm
else ifeq ($(ARCH),aarch64)
  GO_ARCH := arm64
  AWS_ARCH := aarch64
  # On ARM, we can run x86 VMs via emulation
  QEMU_PACKAGES := qemu-system-aarch64 qemu-system-x86
else ifeq ($(ARCH),arm64)
  GO_ARCH := arm64
  AWS_ARCH := aarch64
  QEMU_PACKAGES := qemu-system-aarch64 qemu-system-x86
else
  $(error Unsupported architecture: $(ARCH). Only x86_64 and aarch64/arm64 are supported.)
endif

# Ask Go whether workspace mode is active
IN_WORKSPACE := $(shell go env GOWORK)

# Use -mod=mod unless Go reports an active workspace path
ifeq ($(IN_WORKSPACE),)
  GO_BUILD_MOD := -mod=mod
else ifeq ($(IN_WORKSPACE),off)
  GO_BUILD_MOD := -mod=mod
else
  GO_BUILD_MOD :=
endif

# Quiet-mode filters (active when QUIET=1, set by preflight via recursive make)
ifdef QUIET
  _Q     = @
  _COVQ  = 2>&1 | grep -Ev '^\s*(ok|PASS|\?|=== RUN|--- PASS:)\s' | grep -v 'coverage: 0\.0%' || true
  _RACEQ = 2>&1 | { grep -Ev '^\s*(ok|PASS|\?|=== RUN|--- PASS:)\s' || true; }; exit $${PIPESTATUS[0]}
  _SECQ  = >
else
  _Q     =
  _COVQ  = || true
  _RACEQ =
  _SECQ  = 2>&1 | tee
endif

build:
	$(MAKE) go_build

# Build hive-ui frontend (requires pnpm)
build-ui:
	@echo -e "\n....Building hive-ui frontend...."
	cd hive/services/hiveui/frontend && pnpm build

# GO commands
go_build:
	@echo -e "\n....Building $(GO_PROJECT_NAME)"
	go build $(GO_BUILD_MOD) -ldflags "-s -w" -o ./bin/$(GO_PROJECT_NAME) cmd/hive/main.go

go_run:
	@echo -e "\n....Running $(GO_PROJECT_NAME)...."
	$(GOPATH)/bin/$(GO_PROJECT_NAME)

# Preflight — runs the same checks as GitHub Actions (format + lint + security + tests).
# Use this before committing to catch CI failures locally.
preflight:
	@$(MAKE) --no-print-directory QUIET=1 check-format check-modernize vet security-check test-cover diff-coverage test-race
	@echo -e "\n ✅ Preflight passed — safe to commit."

# Run unit tests
test:
	@echo -e "\n....Running tests for $(GO_PROJECT_NAME)...."
	LOG_IGNORE=1 go test -timeout 120s ./hive/...

# Run unit tests with coverage profile
# Note: go test may exit non-zero due to Go version mismatch in coverage instrumentation
# for packages without test files. We check actual test results + coverage threshold instead.
COVERPROFILE ?= coverage.out
test-cover:
	@echo -e "\n....Running tests with coverage for $(GO_PROJECT_NAME)...."
	$(_Q)LOG_IGNORE=1 go test -timeout 120s -coverprofile=$(COVERPROFILE) -covermode=atomic ./hive/... $(_COVQ)
	@scripts/check-coverage.sh $(COVERPROFILE) $(QUIET)

# Run unit tests with race detector
test-race:
	@echo -e "\n....Running tests with race detector for $(GO_PROJECT_NAME)...."
	$(_Q)LOG_IGNORE=1 go test -race -timeout 300s ./hive/... $(_RACEQ)

# Check that new/changed code meets coverage threshold (runs tests first)
diff-coverage: test-cover
	@QUIET=$(QUIET) scripts/diff-coverage.sh $(COVERPROFILE)

bench:
	@echo -e "\n....Running benchmarks for $(GO_PROJECT_NAME)...."
	$(MAKE) easyjson
	LOG_IGNORE=1 go test -benchmem -run=. -bench=. ./...

run:
	$(MAKE) go_build
	$(MAKE) go_run

clean:
	rm -f ./bin/$(GO_PROJECT_NAME)
	rm -rf hive/services/hiveui/frontend/dist

install-system:
	@echo -e "\n....Installing system dependencies for $(ARCH)...."
	@echo "QEMU packages: $(QEMU_PACKAGES)"
	apt-get update && sudo apt-get install -y \
		nbdkit nbdkit-plugin-dev pkg-config $(QEMU_PACKAGES) qemu-utils qemu-kvm \
		libvirt-daemon-system libvirt-clients libvirt-dev make gcc jq curl \
		iproute2 netcat-openbsd openssh-client wget git unzip sudo xz-utils file \
		ovn-central ovn-host openvswitch-switch

install-go:
	@echo -e "\n....Installing Go 1.26.1 for $(ARCH) ($(GO_ARCH))...."
	@if [ ! -d "/usr/local/go" ]; then \
		curl -L https://go.dev/dl/go1.26.1.linux-$(GO_ARCH).tar.gz | tar -C /usr/local -xz; \
	else \
		echo "Go already installed in /usr/local/go"; \
	fi
	@echo "Go version: $$(go version)"

install-aws:
	@echo -e "\n....Installing AWS CLI v2 for $(ARCH) ($(AWS_ARCH))...."
	@if ! command -v aws >/dev/null 2>&1; then \
		curl "https://awscli.amazonaws.com/awscli-exe-linux-$(AWS_ARCH).zip" -o "awscliv2.zip"; \
		unzip -o awscliv2.zip; \
		./aws/install; \
		rm -rf awscliv2.zip aws/; \
	else \
		echo "AWS CLI already installed"; \
	fi

quickinstall: install-system install-go install-aws
	@echo -e "\n✅ Quickinstall complete for $(ARCH)."
	@echo "   Please ensure /usr/local/go/bin is in your PATH."
	@echo "   Installed: Go ($(GO_ARCH)), AWS CLI ($(AWS_ARCH)), QEMU ($(QEMU_PACKAGES))"

# Format all Go files in place
format:
	gofmt -w .

# Check that all Go files are formatted (CI-compatible, fails on diff)
check-format:
	@echo "Checking gofmt..."
	@UNFORMATTED=$$(gofmt -l .); \
	if [ -n "$$UNFORMATTED" ]; then \
		echo "Files not formatted:"; \
		echo "$$UNFORMATTED"; \
		echo "Run 'make format' to fix."; \
		exit 1; \
	fi
	@echo "  gofmt ok"

# Go vet (fails on issues, matches CI)
vet:
	@echo "Running go vet..."
	$(_Q)go vet ./...
	@echo "  go vet ok"

# Excluded: newexpr (replaces aws.String with new, not idiomatic for AWS SDK)
# Excluded: stringsbuilder (replaces string += in loops with strings.Builder, not worth the complexity for small loops)
GOFIX_EXCLUDE := -newexpr=false -stringsbuilder=false

# Apply go fix modernizations
modernize:
	@echo "Applying go fix modernizations..."
	go fix $(GOFIX_EXCLUDE) ./...
	@echo "  go fix applied"

# Check that code is modernized (CI-compatible, fails on diff)
check-modernize:
	@echo "Checking go fix modernizations..."
	@DIFF=$$(go fix $(GOFIX_EXCLUDE) -diff ./...); \
	if [ -n "$$DIFF" ]; then \
		echo "$$DIFF"; \
		echo "Run 'make modernize' to fix."; \
		exit 1; \
	fi
	@echo "  go fix ok"

# Security checks — each tool fails the build on findings (matches CI).
# Reports are also saved to tests/ for review.
security-check:
	@echo -e "\n....Running security checks for $(GO_PROJECT_NAME)...."
	$(_Q)set -o pipefail && go tool govulncheck ./... $(_SECQ) tests/govulncheck-report.txt $(if $(QUIET),|| { cat tests/govulncheck-report.txt; exit 1; })
	@echo "  govulncheck ok"
	$(_Q)set -o pipefail && go tool gosec -quiet -exclude=G204,G304,G402,G117,G703,G705,G706 -exclude-generated -exclude-dir=cmd ./... $(_SECQ) tests/gosec-report.txt $(if $(QUIET),|| { cat tests/gosec-report.txt; exit 1; })
	@echo "  gosec ok"
	$(_Q)set -o pipefail && go tool staticcheck -checks="all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-SA1019,-SA9005" ./... $(_SECQ) tests/staticcheck-report.txt $(if $(QUIET),|| { cat tests/staticcheck-report.txt; exit 1; })
	@echo "  staticcheck ok"

.PHONY: build build-ui go_build go_run preflight test test-cover test-race diff-coverage bench run clean \
	install-system install-go install-aws quickinstall \
	format check-format modernize check-modernize vet security-check
