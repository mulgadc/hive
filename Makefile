GO_PROJECT_NAME := hive

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
	rm ./bin/$(GO_PROJECT_NAME)

quickinstall:
	@echo "\n....Installing system dependencies...."
	sudo apt-get update && sudo apt-get install -y \
		nbdkit nbdkit-plugin-dev pkg-config qemu-system-x86 qemu-utils qemu-kvm \
		libvirt-daemon-system libvirt-clients libvirt-dev make gcc jq curl \
		iproute2 netcat-openbsd openssh-client wget git unzip sudo xz-utils file

	@echo "\n....Installing AWS CLI v2...."
	@if ! command -v aws >/dev/null 2>&1; then \
		curl "https://awscli.amazonaws.com/awscli-exe-linux-x86_64.zip" -o "awscliv2.zip"; \
		unzip -o awscliv2.zip; \
		sudo ./aws/install; \
		rm -rf awscliv2.zip aws/; \
	else \
		echo "AWS CLI already installed"; \
	fi

	@echo "\n....Installing Go 1.25.5...."
	@if [ ! -d "/usr/local/go" ]; then \
		curl -L https://go.dev/dl/go1.25.5.linux-amd64.tar.gz | sudo tar -C /usr/local -xz; \
	else \
		echo "Go already installed in /usr/local/go"; \
	fi

	@echo "\n✅ Quickinstall complete. Please ensure /usr/local/go/bin is in your PATH."

security:
	@echo "\n....Running security checks for $(GO_PROJECT_NAME)...."

	go tool govulncheck ./... > tests/govulncheck-report.txt || true
	@echo "Govulncheck report saved to tests/govulncheck-report.txt"

	go tool gosec ./... > tests/gosec-report.txt || true
	@echo "Gosec report saved to tests/gosec-report.txt"

	go tool staticcheck -checks="all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-SA1019,-SA9005" ./...  > tests/staticcheck-report.txt || true
	@echo "Staticcheck report saved to tests/staticcheck-report.txt"

	go vet ./... 2>&1 | tee tests/govet-report.txt || true
	@echo "Go vet report saved to tests/govet-report.txt"

e2e-test:
	@echo "\n....Ensuring E2E Base Image exists...."
	@if ! sudo docker image inspect hive-base:latest >/dev/null 2>&1; then \
		echo "Building hive-base:latest..."; \
		sudo docker build -t hive-base -f tests/e2e/Dockerfile.base .; \
	fi

	@echo "\n....Removing old E2E image if it exists...."
	-sudo docker rmi hive-e2e:latest 2>/dev/null || true

	@echo "\n....Building E2E Docker image (building everything inside)...."
	cd .. && sudo docker build -t hive-e2e -f hive/tests/e2e/Dockerfile.e2e .

	@echo "\n....Running E2E Docker container...."
	sudo docker run --privileged --rm -v /dev/kvm:/dev/kvm --name hive-e2e-test hive-e2e

.PHONY: build go_build go_run test bench run clean quickinstall security e2e-test
