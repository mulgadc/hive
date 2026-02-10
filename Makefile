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
	GOEXPERIMENT=greenteagc go build $(GO_BUILD_MOD) -ldflags "-s -w" -o ./bin/$(GO_PROJECT_NAME) cmd/hive/main.go

go_run:
	@echo "\n....Running $(GO_PROJECT_NAME)...."
	$(GOPATH)/bin/$(GO_PROJECT_NAME)

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
	@echo "\n....Installing Go 1.25.7 for $(ARCH) ($(GO_ARCH))...."
	@if [ ! -d "/usr/local/go" ]; then \
		curl -L https://go.dev/dl/go1.25.7.linux-$(GO_ARCH).tar.gz | tar -C /usr/local -xz; \
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

security:
	@echo "\n....Running security checks for $(GO_PROJECT_NAME)...."

	go vet ./... 2>&1 | tee tests/govet-report.txt || true
	@echo "Go vet report saved to tests/govet-report.txt"

	go tool govulncheck ./... > tests/govulncheck-report.txt || true
	@echo "Govulncheck report saved to tests/govulncheck-report.txt"

	go tool gosec -exclude=G204,G304,G402 -exclude-generated -exclude-dir=cmd ./... > tests/gosec-report.txt || true
	@echo "Gosec report saved to tests/gosec-report.txt"

	go tool staticcheck -checks="all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-SA1019,-SA9005" ./...  > tests/staticcheck-report.txt || true
	@echo "Staticcheck report saved to tests/staticcheck-report.txt"

.PHONY: build build-ui go_build go_run test bench run clean install-system install-go install-aws quickinstall security
