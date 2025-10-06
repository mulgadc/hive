GO_PROJECT_NAME := hive

build:
	$(MAKE) go_build

# GO commands
go_build:
	@echo "\n....Building $(GO_PROJECT_NAME)"
	go build -ldflags "-s -w" -o ./bin/$(GO_PROJECT_NAME) cmd/hive/main.go

go_run:
	@echo "\n....Running $(GO_PROJECT_NAME)...."
	$(GOPATH)/bin/$(GO_PROJECT_NAME)

test:
	@echo "\n....Running tests for $(GO_PROJECT_NAME)...."
	LOG_IGNORE=1 go test -v -timeout 300s ./...

bench:
	@echo "\n....Running benchmarks for $(GO_PROJECT_NAME)...."
	$(MAKE) easyjson
	LOG_IGNORE=1 go test -benchmem -run=. -bench=. ./...

run:
	$(MAKE) go_build
	$(MAKE) go_run

clean:
	rm ./bin/$(GO_PROJECT_NAME)

.PHONY: go_build go_run build run test clean