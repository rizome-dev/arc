// Package state provides interfaces for managing orchestrator state
package state

import (
    "context"

    "github.com/rizome-dev/arc/pkg/types"
)

// StateManager defines the interface for state management
type StateManager interface {
    // Agent state management
    CreateAgent(ctx context.Context, agent *types.Agent) error
    GetAgent(ctx context.Context, agentID string) (*types.Agent, error)
    UpdateAgent(ctx context.Context, agent *types.Agent) error
    DeleteAgent(ctx context.Context, agentID string) error
    ListAgents(ctx context.Context, filter map[string]string) ([]*types.Agent, error)
    
    // Workflow state management
    CreateWorkflow(ctx context.Context, workflow *types.Workflow) error
    GetWorkflow(ctx context.Context, workflowID string) (*types.Workflow, error)
    UpdateWorkflow(ctx context.Context, workflow *types.Workflow) error
    DeleteWorkflow(ctx context.Context, workflowID string) error
    ListWorkflows(ctx context.Context, filter map[string]string) ([]*types.Workflow, error)
    
    // Task state management
    GetTask(ctx context.Context, workflowID, taskID string) (*types.Task, error)
    UpdateTask(ctx context.Context, workflowID string, task *types.Task) error
    
    // Event management
    RecordEvent(ctx context.Context, event *types.Event) error
    GetEvents(ctx context.Context, filter map[string]string, limit int) ([]*types.Event, error)
    
    // Transactional operations
    Transaction(ctx context.Context, fn func(tx StateManager) error) error
}

// Store defines the backend storage interface
type Store interface {
    StateManager
    
    // Initialize the store
    Initialize(ctx context.Context) error
    
    // Close the store
    Close(ctx context.Context) error
    
    // Health check
    HealthCheck(ctx context.Context) error
}

// Config holds state store configuration
type Config struct {
    Type     string // "memory", "badger", "postgres", etc.
    URL      string // Connection URL for external stores
    Path     string // File path for embedded stores
    Options  map[string]interface{}
}

// Factory creates state store instances
type Factory interface {
    Create(config Config) (Store, error)
}