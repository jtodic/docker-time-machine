.PHONY: build run test clean install help

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
	@go build -o bin/$(BINARY) main.go
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
	@echo "Building $(BINARY)..."
	@go build -o $(BINARY) main.go
	@if [ -z "$(GOPATH)" ]; then \
		echo "Error: GOPATH is not set"; \
		exit 1; \
	fi
	@mkdir -p $(GOPATH)/bin
	@mv $(BINARY) $(GOPATH)/bin/$(BINARY)
	@echo "Installed to $(GOPATH)/bin/$(BINARY)"
	@which $(BINARY) || echo "Note: Make sure $(GOPATH)/bin is in your PATH"
