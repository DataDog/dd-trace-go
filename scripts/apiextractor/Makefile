.PHONY: fmt
fmt:
	GOWORK=off golangci-lint fmt --no-config --enable gofumpt ./...

.PHONY: vet
vet:
	GOWORK=off go vet ./...

.PHONY: fix
fix: build
	GOWORK=off golangci-lint run --fix --default=standard --no-config ./...

.PHONY: lint
lint:
	GOWORK=off golangci-lint run --default=standard --no-config ./...

.PHONY: test
test: deps
	GOWORK=off go test -v ./...

.PHONY: test-update
test-update: deps
	GOWORK=off go test -v ./... -update

.PHONY: deps
deps:
	go mod tidy

.PHONY: build
build: deps
	go build -o api_extractor ./api_extractor.go

.PHONY: clean
clean:
	rm -f api_extractor

.PHONY: run
run: build
	./api_extractor ddtrace/tracer

.PHONY: run-testdata
run-testdata: build
	./api_extractor _testdata/dummy

.PHONY: help
help:
	@echo "Available targets:"
	@echo "  fmt          - Format code with gofumpt"
	@echo "  vet          - Run go vet"
	@echo "  fix          - Run golangci-lint with auto-fix"
	@echo "  lint         - Run golangci-lint"
	@echo "  test         - Run tests"
	@echo "  test-update  - Run tests and update golden files"
	@echo "  deps         - Tidy dependencies"
	@echo "  build        - Build the api_extractor binary"
	@echo "  clean        - Remove built binary"
	@echo "  run          - Run extractor on ddtrace/tracer package"
	@echo "  run-testdata - Run extractor on test data"
	@echo "  help         - Show this help message"

.DEFAULT_GOAL := help
