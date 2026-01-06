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

# Where to install Go tools
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
  GOBIN := $(shell go env GOPATH)/bin
endif

# Where to install Go tools
GOVULNCHECK := $(GOBIN)/govulncheck

# Install govulncheck only if the binary is missing / out of date
$(GOVULNCHECK):
	go install golang.org/x/vuln/cmd/govulncheck@latest

GOSECCHECK := $(GOBIN)/gosec

# Install gosec only if the binary is missing / out of date
$(GOSECCHECK):
	go install github.com/securego/gosec/v2/cmd/gosec@latest

GOSTATICCHECK := $(GOBIN)/staticcheck

# Install govulncheck only if the binary is missing / out of date
$(GOSTATICCHECK):
	go install honnef.co/go/tools/cmd/staticcheck@latest

# Install govulncheck only if the binary is missing / out of date
$(GOVULNCHECK):
	go install golang.org/x/vuln/cmd/govulncheck@latest

build:
	$(MAKE) go_build

# GO commands
go_build:
	@echo "\n....Building $(GO_PROJECT_NAME)"
	go build $(GO_BUILD_MOD) -ldflags "-s -w" -o ./bin/$(GO_PROJECT_NAME) cmd/hive/main.go

go_run:
	@echo "\n....Running $(GO_PROJECT_NAME)...."
	$(GOPATH)/bin/$(GO_PROJECT_NAME)

test: $(GOVULNCHECK)
	@echo "\n....Running tests for $(GO_PROJECT_NAME)...."
	LOG_IGNORE=1 go test -v -timeout 120s ./...
	$(GOVULNCHECK) ./...

bench:
	@echo "\n....Running benchmarks for $(GO_PROJECT_NAME)...."
	$(MAKE) easyjson
	LOG_IGNORE=1 go test -benchmem -run=. -bench=. ./...

run:
	$(MAKE) go_build
	$(MAKE) go_run

clean:
	rm ./bin/$(GO_PROJECT_NAME)

security: $(GOVULNCHECK) $(GOSECCHECK) $(GOSTATICCHECK)
	@echo "\n....Running security checks for $(GO_PROJECT_NAME)...."

	$(GOVULNCHECK) ./... > tests/govulncheck-report.txt || true
	@echo "Govulncheck report saved to tests/govulncheck-report.txt"

	$(GOSECCHECK) ./... > tests/gosec-report.txt || true
	@echo "Gosec report saved to tests/gosec-report.txt"

	$(GOSTATICCHECK) -checks="all,-ST1000,-ST1003,-ST1016,-ST1020,-ST1021,-ST1022,-SA1019" ./...  > tests/staticcheck-report.txt || true
	@echo "Staticcheck report saved to tests/staticcheck-report.txt"

	go vet ./... 2>&1 | tee tests/govet-report.txt || true
	@echo "Go vet report saved to tests/govet-report.txt"

.PHONY: go_build go_run build run test clean
