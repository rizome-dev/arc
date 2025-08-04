# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

This is the ARC (Agent oRchestrator for Containers) project (`github.com/rizome-dev/arc`) built by rizome labs. It provides a production-ready orchestrator for managing container-based agents in Agentic Development Swarms.

## Key Components

- **Orchestrator**: Core engine managing agent lifecycle and workflow execution
- **Runtime Interface**: Abstraction supporting Docker and Kubernetes
- **Message Queue**: Integration with AMQ (github.com/rizome-dev/amq) for agent communication
- **Workflow Engine**: DAG-based task execution with dependency management
- **State Management**: Pluggable storage backends (memory, BadgerDB, PostgreSQL)

## Build Commands

```bash
# Build the project
make build

# Run tests
make test

# Run linter
make lint

# Install development tools
make setup
```

## Development Setup

1. This is a Go 1.23.4 project
2. Run `make setup` to install development tools
3. Ensure Docker is running for Docker runtime support
4. For Kubernetes support, ensure kubectl is configured

## Project Structure

- `pkg/orchestrator/` - Main orchestration engine
- `pkg/runtime/` - Runtime implementations (Docker, Kubernetes)
- `pkg/messagequeue/` - Message queue interfaces and AMQ integration
- `pkg/state/` - State management implementations
- `pkg/types/` - Core types (Agent, Workflow, Task, etc.)
- `examples/` - Example usage
- `helm/arc/` - Kubernetes Helm charts

## Testing

```bash
# Run all tests
make test

# Run specific package tests
go test ./pkg/orchestrator/...

# Run with coverage
go test -cover ./...
```

## Important Notes

- All agents run as containers (Docker or Kubernetes pods)
- Agents communicate via the AMQ message queue
- Workflows are defined as DAGs with task dependencies
- The orchestrator supports both Docker and Kubernetes runtimes
- Resource constraints (CPU, memory, GPU) are enforced by the runtime
- Contact: hi@rizome.dev
- Documentation: https://pkg.go.dev/github.com/rizome-dev/arc