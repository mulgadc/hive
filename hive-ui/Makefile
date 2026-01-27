GO_PROJECT_NAME := webserver

# Ask Go whether workspace mode is active
IN_WORKSPACE := $(shell go env GOWORK)

# Use -mod=mod unless Go reports an active workspace path
GO_BUILD_MOD := $(if $(filter-out off,$(IN_WORKSPACE)),,-mod=mod)

build:
	@echo "\n....Building frontend...."
	cd frontend && pnpm build && cd ..

	@echo "\n....Building $(GO_PROJECT_NAME)...."
	go build $(GO_BUILD_MOD) -ldflags "-s -w" -o ./bin/$(GO_PROJECT_NAME) webserver/main.go

run:
	$(MAKE) build
	@echo "\n....Running $(GO_PROJECT_NAME)...."
	./bin/$(GO_PROJECT_NAME)

clean:
	rm -f ./bin/$(GO_PROJECT_NAME)

test:
	@echo "\n....Running tests for $(GO_PROJECT_NAME)...."
	cd $(GO_PROJECT_NAME) && LOG_IGNORE=1 go test -v -timeout 120s ./...

security:
	@echo "\n....Running security checks for $(GO_PROJECT_NAME)...."

	cd $(GO_PROJECT_NAME) && go tool govulncheck ./... > ../tests/govulncheck-report.txt || true
	@echo "Govulncheck report saved to tests/govulncheck-report.txt"

	cd $(GO_PROJECT_NAME) && go tool gosec -exclude-generated ./... > ../tests/gosec-report.txt || true
	@echo "Gosec report saved to tests/gosec-report.txt"

	cd $(GO_PROJECT_NAME) && go tool staticcheck ./... > ../tests/staticcheck-report.txt || true
	@echo "Staticcheck report saved to tests/staticcheck-report.txt"

	cd $(GO_PROJECT_NAME) && go vet ./... 2>&1 | tee ../tests/govet-report.txt || true
	@echo "Go vet report saved to tests/govet-report.txt"

.PHONY: build run clean test security
