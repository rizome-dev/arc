# ARC - Agentic Runtime Controller

[![GoDoc](https://pkg.go.dev/badge/github.com/rizome-dev/arc)](https://pkg.go.dev/github.com/rizome-dev/arc)
[![Go Report Card](https://goreportcard.com/badge/github.com/rizome-dev/arc)](https://goreportcard.com/report/github.com/rizome-dev/arc)
[![CI](https://github.com/rizome-dev/arc/actions/workflows/ci.yml/badge.svg)](https://github.com/rizome-dev/arc/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

ARC is a production-ready, unopinionated agent & workflow management system.

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

## Deployment

### Kubernetes

```bash
helm install arc ./helm/arc
# or
helm install arc ./helm/arc -f values.yaml
```

## License

MIT License - see the [LICENSE](LICENSE) file for details.

---

Built with ❤️  by [Rizome Labs](https://rizome.dev)
