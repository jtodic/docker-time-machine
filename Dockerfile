# Build stage
FROM golang:1.21-alpine AS builder

# Install git for go-git
RUN apk add --no-cache git

WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o dtm main.go

# Final stage
FROM docker:24-dind

# Install git
RUN apk add --no-cache git ca-certificates

# Copy the binary from builder
COPY --from=builder /app/dtm /usr/local/bin/dtm

# Make it executable
RUN chmod +x /usr/local/bin/dtm

ENTRYPOINT ["dtm"]
CMD ["--help"]
