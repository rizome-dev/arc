# ARC - Agent oRchestrator for Containers

[![GoDoc](https://pkg.go.dev/badge/github.com/rizome-dev/arc)](https://pkg.go.dev/github.com/rizome-dev/arc)
[![Go Report Card](https://goreportcard.com/badge/github.com/rizome-dev/arc)](https://goreportcard.com/report/github.com/rizome-dev/arc)
[![CI](https://github.com/rizome-dev/arc/actions/workflows/ci.yml/badge.svg)](https://github.com/rizome-dev/arc/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

ARC is a production-ready orchestrator for managing container-based agents in Agentic Development Swarms. It provides a robust framework for deploying, managing, and coordinating autonomous agents at scale with support for both Docker and Kubernetes runtimes.

built by [rizome labs](https://rizome.dev) | contact: [hi@rizome.dev](mailto:hi@rizome.dev)

```bash
go get github.com/rizome-dev/arc
```

## Deployment

```bash
# Build and run the gRPC/HTTP server
make server

# Or separately:
make server-build
./build/arc-server
```

The server exposes:
- gRPC API on `:50051`
- REST API on `:8080`

### Using the Library

```go
package main

import (
    "context"
    "log"
    
    "github.com/rizome-dev/arc/pkg/messagequeue"
    "github.com/rizome-dev/arc/pkg/orchestrator"
    "github.com/rizome-dev/arc/pkg/runtime"
    "github.com/rizome-dev/arc/pkg/state"
    "github.com/rizome-dev/arc/pkg/types"
)

func main() {
    ctx := context.Background()
    
    // Create Docker runtime
    dockerRuntime, _ := runtime.NewDockerRuntime(runtime.Config{
        Type: "docker",
    })
    
    // Create message queue
    mq, _ := messagequeue.NewAMQMessageQueue(messagequeue.Config{
        StorePath:      "./arc-amq-data",
        WorkerPoolSize: 10,
        MessageTimeout: 300,
    })
    defer mq.Close()
    
    // Create state manager
    stateManager := state.NewMemoryStore()
    stateManager.Initialize(ctx)
    defer stateManager.Close(ctx)
    
    // Create orchestrator
    arc, _ := orchestrator.New(orchestrator.Config{
        Runtime:      dockerRuntime,
        MessageQueue: mq,
        StateManager: stateManager,
    })
    
    // Start orchestrator
    arc.Start()
    defer arc.Stop()
    
    // Create and execute workflow
    workflow := &types.Workflow{
        Name: "data-pipeline",
        Tasks: []types.Task{
            {
                Name: "fetch-data",
                AgentConfig: types.AgentConfig{
                    Command: []string{"python"},
                    Args:    []string{"fetch.py"},
                },
            },
            {
                Name:         "process-data",
                Dependencies: []string{"fetch-data"},
                AgentConfig: types.AgentConfig{
                    Command: []string{"python"},
                    Args:    []string{"process.py"},
                },
            },
        },
    }
    
    arc.CreateWorkflow(ctx, workflow)
    arc.StartWorkflow(ctx, workflow.ID)
}
```

### Using the gRPC API

```go
package main

import (
    "context"
    "log"
    
    "google.golang.org/grpc"
    "google.golang.org/grpc/credentials/insecure"
    "google.golang.org/protobuf/types/known/emptypb"
    
    arcv1 "github.com/rizome-dev/arc/api/v1"
)

func main() {
    // Connect to server
    conn, err := grpc.Dial("localhost:50051", 
        grpc.WithTransportCredentials(insecure.NewCredentials()))
    if err != nil {
        log.Fatal(err)
    }
    defer conn.Close()
    
    client := arcv1.NewARCClient(conn)
    
    // Check health
    health, _ := client.Health(context.Background(), &emptypb.Empty{})
    log.Printf("Server status: %s", health.Status)
    
    // Create workflow
    resp, _ := client.CreateWorkflow(context.Background(), 
        &arcv1.CreateWorkflowRequest{
            Workflow: &arcv1.Workflow{
                Name: "example",
                Tasks: []*arcv1.Task{
                    {
                        Name: "task-1",
                        AgentConfig: &arcv1.AgentConfig{
                            Image:   "ubuntu:latest",
                            Command: []string{"echo", "Hello"},
                        },
                    },
                },
            },
        })
    
    // Start workflow
    client.StartWorkflow(context.Background(), 
        &arcv1.StartWorkflowRequest{
            WorkflowId: resp.WorkflowId,
        })
}
```

### REST API Examples

```bash
# Health check
curl http://localhost:8080/health

# Create workflow
curl -X POST http://localhost:8080/api/v1/workflows \
  -H "Content-Type: application/json" \
  -d '{
    "workflow": {
      "name": "my-workflow",
      "tasks": [{
        "name": "task-1",
        "agent_config": {
          "image": "ubuntu:latest",
          "command": ["echo", "Hello World"]
        }
      }]
    }
  }'

# List agents
curl http://localhost:8080/api/v1/agents

# Stream events
curl http://localhost:8080/api/v1/events/stream
```

## Building and Testing

```bash
# Run all tests
make test

# Generate coverage report
make coverage

# Run linter
make lint

# Run security scan
make security

# Build for multiple platforms
make build-cross

# Build Docker image
make docker-build
```

## Proto File Management

### Files Included in Repository

The following generated files are committed for convenience:
- `api/v1/*.pb.go` - Go type definitions
- `api/v1/*_grpc.pb.go` - gRPC service implementations
- `api/v1/*.pb.gw.go` - HTTP gateway handlers

### Regenerating Proto Files

```bash
# Clean all generated files
make proto-clean

# Regenerate from proto definitions
make proto
```

### Required Proto Tools

Install with `make proto-install`:
- `protoc-gen-go` - Generate Go types
- `protoc-gen-go-grpc` - Generate gRPC code
- `protoc-gen-grpc-gateway` - Generate HTTP gateway
- `protoc-gen-openapiv2` - Generate OpenAPI specs

## Deployment

### Docker

```bash
# Build image
make docker-build

# Run container
docker run -d \
  -p 50051:50051 \
  -p 8080:8080 \
  -v /var/run/docker.sock:/var/run/docker.sock \
  arc:latest
```

### Kubernetes

```bash
# Install with Helm
helm install arc ./helm/arc

# With custom values
helm install arc ./helm/arc -f values.yaml

# Upgrade
helm upgrade arc ./helm/arc
```

### Configuration

Environment variables:
```bash
ARC_GRPC_PORT=50051           # gRPC server port
ARC_HTTP_PORT=8080            # HTTP server port
ARC_RUNTIME=docker            # Runtime: docker or kubernetes
ARC_STATE_BACKEND=memory      # State: memory, badger, postgres
ARC_AMQ_PATH=./data          # AMQ storage path
ARC_LOG_LEVEL=info           # Log level
```

## License

MIT License - see the [LICENSE](LICENSE) file for details.

---

Built with ❤️  by [Rizome Labs](https://rizome.dev)
