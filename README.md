# ARC - Agentic Runtime Controller

[![GoDoc](https://pkg.go.dev/badge/github.com/rizome-dev/arc)](https://pkg.go.dev/github.com/rizome-dev/arc)
[![Go Report Card](https://goreportcard.com/badge/github.com/rizome-dev/arc)](https://goreportcard.com/report/github.com/rizome-dev/arc)
[![CI](https://github.com/rizome-dev/arc/actions/workflows/ci.yml/badge.svg)](https://github.com/rizome-dev/arc/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

ARC is a production-ready agent orchestrator for building and managing Agentic Development Swarms. It provides a simple yet powerful framework for orchestrating container-based agents that communicate via message queues to execute complex workflows.

built by [rizome labs](https://rizome.dev) | contact: [hi@rizome.dev](mailto:hi@rizome.dev)

```bash
go get github.com/rizome-dev/arc
```

## Quick Start

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

## Development

```bash
# Install dependencies
go mod download

# Run tests
go test ./...

# Build
go build ./...

# Run linter
golangci-lint run
```

## Deployment

### Docker

```bash
# Build Docker image
docker build -t arc:latest .

# Run with Docker Compose
docker-compose up
```

### Kubernetes

```bash
# Install with Helm
helm install arc ./helm/arc

# Configure values
helm install arc ./helm/arc -f values.yaml
```

## Contributing

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/amazing-feature`)
3. Commit your changes (`git commit -m 'Add amazing feature'`)
4. Push to the branch (`git push origin feature/amazing-feature`)
5. Open a Pull Request

## License

MIT License - see the [LICENSE](LICENSE) file for details.
