.PHONY: build run test clean install docker-build docker-run lint fmt help

VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")
LDFLAGS := -ldflags "-X main.Version=$(VERSION) -s -w"
BINARY := dtm
DOCKER_IMAGE := dockerfile-time-machine

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
	@go test -v -race -coverprofile=coverage.out ./...
	@go tool cover -html=coverage.out -o coverage.html
	@echo "Coverage report generated at coverage.html"

## bench: Run benchmarks
bench:
	@go test -bench=. -benchmem ./...

## lint: Run linters
lint:
	@if ! which golangci-lint > /dev/null; then \
		echo "Installing golangci-lint..."; \
		go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest; \
	fi
	@golangci-lint run

## fmt: Format code
fmt:
	@go fmt ./...
	@go mod tidy

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
	@docker build -t $(DOCKER_IMAGE):latest -t $(DOCKER_IMAGE):$(VERSION) .
	@echo "Docker image built: $(DOCKER_IMAGE):$(VERSION)"

## docker-run: Run via Docker
docker-run: docker-build
	@docker run --rm \
		-v /var/run/docker.sock:/var/run/docker.sock \
		-v $(PWD):/workspace \
		-w /workspace \
		$(DOCKER_IMAGE):latest analyze

## release: Create a new release
release:
	@if [ -z "$(TAG)" ]; then \
		echo "Usage: make release TAG=v1.0.0"; \
		exit 1; \
	fi
	@git tag -a $(TAG) -m "Release $(TAG)"
	@git push origin $(TAG)
	@echo "Released $(TAG)"
