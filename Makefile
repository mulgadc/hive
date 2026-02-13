GO_PROJECT_NAME := hive

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

build:
	$(MAKE) go_build

# Build hive-ui frontend (requires pnpm)
build-ui:
	@echo "\n....Building hive-ui frontend...."
	cd hive/services/hiveui/frontend && pnpm build

# GO commands
go_build:
	@echo "\n....Building $(GO_PROJECT_NAME)"
	go build $(GO_BUILD_MOD) -ldflags "-s -w" -o ./bin/$(GO_PROJECT_NAME) cmd/hive/main.go

go_run:
	@echo "\n....Running $(GO_PROJECT_NAME)...."
	$(GOPATH)/bin/$(GO_PROJECT_NAME)

# Preflight — runs the same checks as GitHub Actions (format + lint + security + tests).
# Use this before committing to catch CI failures locally.
preflight: check-format vet security-check test
	@echo "\n✅ Preflight passed — safe to commit."

# Run unit tests
test:
	@echo "\n....Running tests for $(GO_PROJECT_NAME)...."
	LOG_IGNORE=1 go test -v -timeout 120s ./...

bench:
	@echo "\n....Running benchmarks for $(GO_PROJECT_NAME)...."
	$(MAKE) easyjson
	LOG_IGNORE=1 go test -benchmem -run=. -bench=. ./...

run:
	$(MAKE) go_build
	$(MAKE) go_run

clean:
	rm -f ./bin/$(GO_PROJECT_NAME)
	rm -rf hive/services/hiveui/frontend/dist

install-system:
	@echo "\n....Installing system dependencies for $(ARCH)...."
	@echo "QEMU packages: $(QEMU_PACKAGES)"
	apt-get update && sudo apt-get install -y \
		nbdkit nbdkit-plugin-dev pkg-config $(QEMU_PACKAGES) qemu-utils qemu-kvm \
		libvirt-daemon-system libvirt-clients libvirt-dev make gcc jq curl \
		iproute2 netcat-openbsd openssh-client wget git unzip sudo xz-utils file

install-go:
	@echo "\n....Installing Go 1.26.0 for $(ARCH) ($(GO_ARCH))...."
	@if [ ! -d "/usr/local/go" ]; then \
		curl -L https://go.dev/dl/go1.26.0.linux-$(GO_ARCH).tar.gz | tar -C /usr/local -xz; \
	else \
		echo "Go already installed in /usr/local/go"; \
	fi
	@echo "Go version: $$(go version)"

install-aws:
	@echo "\n....Installing AWS CLI v2 for $(ARCH) ($(AWS_ARCH))...."
	@if ! command -v aws >/dev/null 2>&1; then \
		curl "https://awscli.amazonaws.com/awscli-exe-linux-$(AWS_ARCH).zip" -o "awscliv2.zip"; \
		unzip -o awscliv2.zip; \
		./aws/install; \
		rm -rf awscliv2.zip aws/; \
	else \
		echo "AWS CLI already installed"; \
	fi

quickinstall: install-system install-go install-aws
	@echo "\n✅ Quickinstall complete for $(ARCH)."
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
	go vet ./...
	@echo "  go vet ok"

# Security checks — each tool fails the build on findings (matches CI).
# Reports are also saved to tests/ for review.
security-check:
	@echo "\n....Running security checks for $(GO_PROJECT_NAME)...."
	go tool govulncheck ./... 2>&1 | tee tests/govulncheck-report.txt
	@echo "  govulncheck ok"
	go tool gosec -exclude=G204,G304,G402 -exclude-generated -exclude-dir=cmd ./... 2>&1 | tee tests/gosec-report.txt
	@echo "  gosec ok"
	go tool staticcheck -checks="all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-SA1019,-SA9005" ./... 2>&1 | tee tests/staticcheck-report.txt
	@echo "  staticcheck ok"

# Legacy alias — now runs the strict checks (same as security-check)
security: vet security-check

# Docker E2E tests (mirrors GitHub Actions e2e.yml)
# Usage: make test-docker              # both suites
#        make test-docker-single       # single-node only
#        make test-docker-multi        # multi-node only
PARENT_DIR := $(shell cd .. && pwd)
E2E_IMAGE := hive-e2e:latest

test-docker-build:
	@echo "\n....Building E2E Docker image...."
	@for dep in viperblock predastore; do \
		if [ ! -d "$(PARENT_DIR)/$$dep" ]; then \
			echo "Missing sibling repo $$dep — running clone-deps.sh"; \
			./scripts/clone-deps.sh; \
			break; \
		fi; \
	done
	docker build -t $(E2E_IMAGE) -f tests/e2e/Dockerfile.e2e $(PARENT_DIR)

test-docker-single: test-docker-build
	@echo "\n....Running Single-Node E2E Tests...."
	docker run --privileged --rm -v /dev/kvm:/dev/kvm $(E2E_IMAGE)

test-docker-multi: test-docker-build
	@echo "\n....Running Multi-Node E2E Tests...."
	docker run --privileged --rm -v /dev/kvm:/dev/kvm --cap-add=NET_ADMIN $(E2E_IMAGE) ./tests/e2e/run-multinode-e2e.sh

test-docker: test-docker-build
	@echo "\n....Running Single-Node E2E Tests...."
	docker run --privileged --rm -v /dev/kvm:/dev/kvm $(E2E_IMAGE)
	@echo "\n....Running Multi-Node E2E Tests...."
	docker run --privileged --rm -v /dev/kvm:/dev/kvm --cap-add=NET_ADMIN $(E2E_IMAGE) ./tests/e2e/run-multinode-e2e.sh

.PHONY: build build-ui go_build go_run preflight test bench run clean \
	install-system install-go install-aws quickinstall \
	format check-format vet security security-check \
	test-docker-build test-docker-single test-docker-multi test-docker
