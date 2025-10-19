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

# Final stage - use pinned alpine version for security
FROM alpine:3.22

WORKDIR /oracle-go

# Install runtime dependencies
RUN apk add --no-cache ca-certificates tzdata wget

# Create non-root user and group
RUN addgroup -g 1000 oracle && \
    adduser -D -u 1000 -G oracle oracle

# Create directories with proper ownership
RUN mkdir -p /oracle-go/config /var/log/oracle-go && \
    chown -R oracle:oracle /oracle-go /var/log/oracle-go

# Copy binary from builder with proper ownership
COPY --from=builder --chown=oracle:oracle /build/build/oracle-go .

# Switch to non-root user
USER oracle:oracle

# Expose ports
# 8080: HTTP API (price server)
# 9091: Prometheus metrics
EXPOSE 8080 9091

# Health check
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/health || exit 1

# Default command
ENTRYPOINT ["./oracle-go"]
CMD ["--config", "/oracle-go/config/config.yaml"]
