.PHONY: build run test clean install docker-build docker-run help

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -s -w"
BINARY := dtm

## help: Show this help message
help:
	@echo "Usage: make [target]"
	@echo ""
	@echo "Targets:"
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

## build: Build the binary
build:
	@echo "Building $(BINARY)..."
	@go build $(LDFLAGS) -o bin/$(BINARY) main.go
	@echo "Binary built at bin/$(BINARY)"

## run: Build and run
run: build
	./bin/$(BINARY) analyze

## test: Run tests
test:
	@echo "Running tests..."
	@go test -v ./...

## clean: Clean build artifacts
clean:
	@rm -rf bin/ *.html *.json *.csv coverage.* .dtm-cache/
	@echo "Cleaned build artifacts"

## install: Install binary to GOPATH
install:
	@go install $(LDFLAGS)
	@echo "Installed to $(GOPATH)/bin/$(BINARY)"

## docker-build: Build Docker image
docker-build:
	@docker build -t dtm:latest .

## docker-run: Run via Docker
docker-run: docker-build
	@docker run --rm \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(PWD):/workspace \
		-w /workspace \
		dtm:latest analyze
