GO_PROJECT_NAME := webserver

build:
	@echo "\n....Building frontend...."
	cd frontend && pnpm build && cd ..

	@echo "\n....Building $(GO_PROJECT_NAME)...."
	GOWORK=off go build -ldflags "-s -w" -o ./bin/$(GO_PROJECT_NAME) .

run:
	$(MAKE) build
	@echo "\n....Running $(GO_PROJECT_NAME)...."
	./bin/$(GO_PROJECT_NAME)

clean:
	rm -f ./bin/$(GO_PROJECT_NAME)

test:
	@echo "\n....Running tests for $(GO_PROJECT_NAME)...."
	LOG_IGNORE=1 go test -v -timeout 120s ./...

security:
	@echo "\n....Running security checks for $(GO_PROJECT_NAME)...."

	go tool govulncheck ./... > ./tests/govulncheck-report.txt || true
	@echo "Govulncheck report saved to tests/govulncheck-report.txt"

	go tool gosec -exclude-generated ./... > ./tests/gosec-report.txt || true
	@echo "Gosec report saved to tests/gosec-report.txt"

	go tool staticcheck ./... > ./tests/staticcheck-report.txt || true
	@echo "Staticcheck report saved to tests/staticcheck-report.txt"

	go vet ./... 2>&1 | tee ./tests/govet-report.txt || true
	@echo "Go vet report saved to tests/govet-report.txt"

.PHONY: build run clean test security
