// Package runtime provides interfaces and implementations for container runtimes
package runtime

import (
    "context"
    "io"

    "github.com/rizome-dev/arc/pkg/types"
)

// Runtime defines the interface for container runtimes
type Runtime interface {
    // CreateAgent creates a new agent container
    CreateAgent(ctx context.Context, agent *types.Agent) error
    
    // StartAgent starts an agent container
    StartAgent(ctx context.Context, agentID string) error
    
    // StopAgent stops an agent container
    StopAgent(ctx context.Context, agentID string) error
    
    // DestroyAgent removes an agent container
    DestroyAgent(ctx context.Context, agentID string) error
    
    // GetAgentStatus returns the current status of an agent
    GetAgentStatus(ctx context.Context, agentID string) (*types.Agent, error)
    
    // ListAgents returns all agents managed by this runtime
    ListAgents(ctx context.Context) ([]*types.Agent, error)
    
    // StreamLogs streams logs from an agent container
    StreamLogs(ctx context.Context, agentID string) (io.ReadCloser, error)
    
    // ExecCommand executes a command in an agent container
    ExecCommand(ctx context.Context, agentID string, cmd []string) (io.ReadCloser, error)
}

// Config holds runtime configuration
type Config struct {
    Type       string            // "docker" or "kubernetes"
    Endpoint   string            // Docker socket or K8s API endpoint
    Namespace  string            // K8s namespace
    KubeConfig string            // Path to kubeconfig file
    Labels     map[string]string // Default labels for agents
}

// Factory creates runtime instances
type Factory interface {
    Create(config Config) (Runtime, error)
}