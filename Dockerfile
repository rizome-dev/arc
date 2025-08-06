# Multi-stage build for ARC - Agent oRchestrator for Containers
FROM golang:1.23.4-alpine AS builder

# Install build dependencies
RUN apk add --no-cache git make gcc musl-dev ca-certificates tzdata

# Set working directory
WORKDIR /app

# Copy go mod files first for better caching
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download && go mod verify

# Copy source code
COPY cmd/ cmd/
COPY pkg/ pkg/
COPY internal/ internal/
COPY api/ api/
COPY vendor/ vendor/

# Set build-time variables
ARG VERSION=dev
ARG COMMIT=unknown
ARG BUILD_TIME
ENV CGO_ENABLED=0
ENV GOOS=linux
ENV GOARCH=amd64

# Build the ARC binary with optimizations
RUN go build \
    -a -installsuffix cgo \
    -ldflags="-w -s -extldflags '-static' \
    -X 'github.com/rizome-dev/arc/internal/version.Version=${VERSION}' \
    -X 'github.com/rizome-dev/arc/internal/version.Commit=${COMMIT}' \
    -X 'github.com/rizome-dev/arc/internal/version.BuildTime=${BUILD_TIME}'" \
    -o arc \
    ./cmd/arc/main.go

# Verify binary
RUN ./arc version

# Final production stage
FROM scratch

# Import from builder
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group

# Create non-root user (compatible with scratch)
COPY --from=builder /app/arc /usr/local/bin/arc

# Create necessary directories
# Note: We can't use RUN in scratch, so we'll handle data directories at runtime

# Set metadata
LABEL maintainer="rizome labs <hi@rizome.dev>"
LABEL description="ARC - Agent oRchestrator for Containers"
LABEL version="${VERSION}"

# Expose ports for services
# gRPC port
EXPOSE 9090
# HTTP port  
EXPOSE 8080
# Metrics port
EXPOSE 9091
# Profiling port (when enabled)
EXPOSE 6060

# Set environment variables
ENV ARC_GRPC_HOST=0.0.0.0
ENV ARC_GRPC_PORT=9090
ENV ARC_HTTP_HOST=0.0.0.0
ENV ARC_HTTP_PORT=8080
ENV ARC_METRICS_HOST=0.0.0.0
ENV ARC_METRICS_PORT=9091
ENV ARC_LOG_LEVEL=info
ENV ARC_LOG_FORMAT=json
ENV ARC_AMQ_STORE_PATH=/data/amq
ENV ARC_STATE_TYPE=badger
ENV ARC_STATE_CONNECTION_URL=/data/state
ENV ARC_RUNTIME_TYPE=docker
ENV ARC_RUNTIME_NAMESPACE=default

# Health check configuration
HEALTHCHECK --interval=30s --timeout=10s --start-period=5s --retries=3 \
    CMD ["/usr/local/bin/arc", "health"]

# Use non-root user (if we had adduser in scratch)
# USER arc

# Default command - run server
ENTRYPOINT ["/usr/local/bin/arc"]
CMD ["server"]