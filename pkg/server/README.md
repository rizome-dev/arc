# ARC gRPC/HTTP Server

This package provides a comprehensive gRPC server with HTTP fallback for the ARC (Agent oRchestrator for Containers) orchestrator.

## Features

- **Dual Protocol Support**: Both gRPC and HTTP/REST APIs
- **Health & Readiness Probes**: Built-in health checking and readiness endpoints
- **Event Streaming**: Real-time event streaming via gRPC streams
- **Authentication Ready**: Hooks for authentication and authorization
- **CORS Support**: Full CORS support for web clients
- **Graceful Shutdown**: Clean shutdown with configurable timeouts
- **TLS Support**: Optional TLS encryption
- **Metrics**: Basic metrics endpoint

## API Endpoints

### Workflow Operations
- `POST /api/v1/workflows` - Create workflow
- `POST /api/v1/workflows/{id}/start` - Start workflow
- `POST /api/v1/workflows/{id}/stop` - Stop workflow
- `GET /api/v1/workflows/{id}` - Get workflow
- `GET /api/v1/workflows` - List workflows

### Real-time Agent Operations
- `POST /api/v1/agents` - Create real-time agent
- `POST /api/v1/agents/{id}/start` - Start agent
- `POST /api/v1/agents/{id}/stop` - Stop agent
- `POST /api/v1/agents/{id}/pause` - Pause agent
- `POST /api/v1/agents/{id}/resume` - Resume agent
- `GET /api/v1/agents/{id}` - Get agent
- `GET /api/v1/agents` - List agents

### Agent Management
- `GET /api/v1/agents/{id}/status` - Get agent status
- `GET /api/v1/agents/{id}/logs` - Get agent logs
- `GET /api/v1/agents/{id}/logs/stream` - Stream agent logs

### Message Queue Operations
- `POST /api/v1/messages/send` - Send message
- `POST /api/v1/tasks/publish` - Publish task
- `GET /api/v1/queues/{name}/stats` - Get queue stats
- `GET /api/v1/queues` - List queues

### Event Streaming
- `GET /api/v1/events/stream` - Stream events

### Health & Monitoring
- `GET /health` - Health check
- `GET /ready` - Readiness check
- `GET /metrics` - Basic metrics
- `GET /swagger.json` - OpenAPI specification

## Usage

### Basic Server

```go
package main

import (
    "log"
    
    "github.com/rizome-dev/arc/pkg/server"
    "github.com/rizome-dev/arc/pkg/orchestrator"
)

func main() {
    // Initialize orchestrator
    orch, err := orchestrator.New(orchestrator.Config{
        // ... configuration
    })
    if err != nil {
        log.Fatal(err)
    }
    
    // Create server config
    config := &server.Config{
        GRPCPort: 9090,
        HTTPPort: 8080,
    }
    
    // Run server with signal handling
    if err := server.RunServer(config, orch); err != nil {
        log.Fatal(err)
    }
}
```

### Custom Server

```go
// Create server
srv, err := server.NewServer(config, orchestrator)
if err != nil {
    log.Fatal(err)
}

// Start server
if err := srv.Start(); err != nil {
    log.Fatal(err)
}

// Wait for shutdown signal
srv.WaitForShutdown()

// Graceful shutdown
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()
srv.Stop(ctx)
```

## Configuration

```go
config := &server.Config{
    // Server addresses
    GRPCPort: 9090,
    GRPCHost: "0.0.0.0",
    HTTPPort: 8080,
    HTTPHost: "0.0.0.0",
    
    // TLS configuration
    TLSEnabled:  false,
    TLSCertFile: "server.crt",
    TLSKeyFile:  "server.key",
    
    // Server limits
    MaxRecvMsgSize:    4 * 1024 * 1024, // 4MB
    MaxSendMsgSize:    4 * 1024 * 1024, // 4MB
    ConnectionTimeout: 30 * time.Second,
    
    // Health monitoring
    HealthCheckInterval: 10 * time.Second,
    
    // Graceful shutdown
    ShutdownTimeout: 30 * time.Second,
}
```

## Building

```bash
# Generate protobuf code
make proto

# Build server
make server-build

# Run server
make server-run
```

## gRPC Client Example

```go
import (
    arcv1 "github.com/rizome-dev/arc/api/v1"
    "google.golang.org/grpc"
)

// Connect to server
conn, err := grpc.Dial("localhost:9090", grpc.WithInsecure())
if err != nil {
    log.Fatal(err)
}
defer conn.Close()

// Create client
client := arcv1.NewARCClient(conn)

// Create workflow
resp, err := client.CreateWorkflow(context.Background(), &arcv1.CreateWorkflowRequest{
    Workflow: &arcv1.Workflow{
        Name: "example-workflow",
        // ... workflow definition
    },
})
```

## HTTP Client Example

```bash
# Create workflow
curl -X POST http://localhost:8080/api/v1/workflows \
  -H "Content-Type: application/json" \
  -d '{
    "workflow": {
      "name": "example-workflow",
      "tasks": []
    }
  }'

# List workflows
curl http://localhost:8080/api/v1/workflows

# Health check
curl http://localhost:8080/health
```

## Architecture

The server implementation consists of:

- **grpc.go**: Core gRPC service implementation
- **http.go**: HTTP gateway with grpc-gateway  
- **server.go**: Main server orchestrating both protocols

The server integrates directly with the ARC orchestrator and provides a clean API for all orchestration operations.