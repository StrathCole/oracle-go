FROM golang:1.24-alpine AS builder

WORKDIR /build

# Install build dependencies
RUN apk add --no-cache git make

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY . .

# Build the binary
RUN make build

# Final stage
FROM alpine:latest

WORKDIR /oracle-go

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata

# Copy binary from builder
COPY --from=builder /build/build/oracle-go .

# Create directory for config and logs
RUN mkdir -p /oracle-go/config /var/log/oracle-go

# Expose ports
# 8080: HTTP API (price server)
# 8081: WebSocket API (price server)
# 9091: Prometheus metrics
EXPOSE 8080 8081 9091

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Default command
ENTRYPOINT ["./oracle-go"]
CMD ["--config", "/oracle-go/config/config.yaml"]
