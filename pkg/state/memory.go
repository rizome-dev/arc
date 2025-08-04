// Package state provides memory-based state management
package state

import (
    "context"
    "fmt"
    "sync"

    "github.com/rizome-dev/arc/pkg/types"
)

// MemoryStore implements Store interface using in-memory storage
type MemoryStore struct {
    agents    map[string]*types.Agent
    workflows map[string]*types.Workflow
    events    []*types.Event
    mu        sync.RWMutex
}

// NewMemoryStore creates a new memory-based store
func NewMemoryStore() *MemoryStore {
    return &MemoryStore{
        agents:    make(map[string]*types.Agent),
        workflows: make(map[string]*types.Workflow),
        events:    make([]*types.Event, 0),
    }
}

// Initialize initializes the store
func (s *MemoryStore) Initialize(ctx context.Context) error {
    return nil
}

// Close closes the store
func (s *MemoryStore) Close(ctx context.Context) error {
    return nil
}

// HealthCheck performs a health check
func (s *MemoryStore) HealthCheck(ctx context.Context) error {
    return nil
}

// CreateAgent creates a new agent
func (s *MemoryStore) CreateAgent(ctx context.Context, agent *types.Agent) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if _, exists := s.agents[agent.ID]; exists {
        return fmt.Errorf("agent %s already exists", agent.ID)
    }
    
    // Create a copy to avoid external modifications
    agentCopy := *agent
    s.agents[agent.ID] = &agentCopy
    
    return nil
}

// GetAgent retrieves an agent by ID
func (s *MemoryStore) GetAgent(ctx context.Context, agentID string) (*types.Agent, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    agent, exists := s.agents[agentID]
    if !exists {
        return nil, fmt.Errorf("agent %s not found", agentID)
    }
    
    // Return a copy
    agentCopy := *agent
    return &agentCopy, nil
}

// UpdateAgent updates an existing agent
func (s *MemoryStore) UpdateAgent(ctx context.Context, agent *types.Agent) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if _, exists := s.agents[agent.ID]; !exists {
        return fmt.Errorf("agent %s not found", agent.ID)
    }
    
    // Update with a copy
    agentCopy := *agent
    s.agents[agent.ID] = &agentCopy
    
    return nil
}

// DeleteAgent removes an agent
func (s *MemoryStore) DeleteAgent(ctx context.Context, agentID string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if _, exists := s.agents[agentID]; !exists {
        return fmt.Errorf("agent %s not found", agentID)
    }
    
    delete(s.agents, agentID)
    return nil
}

// ListAgents returns agents matching the filter
func (s *MemoryStore) ListAgents(ctx context.Context, filter map[string]string) ([]*types.Agent, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    var agents []*types.Agent
    
    for _, agent := range s.agents {
        if matchesFilter(agent.Labels, filter) {
            agentCopy := *agent
            agents = append(agents, &agentCopy)
        }
    }
    
    return agents, nil
}

// CreateWorkflow creates a new workflow
func (s *MemoryStore) CreateWorkflow(ctx context.Context, workflow *types.Workflow) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if _, exists := s.workflows[workflow.ID]; exists {
        return fmt.Errorf("workflow %s already exists", workflow.ID)
    }
    
    // Create a copy
    workflowCopy := *workflow
    s.workflows[workflow.ID] = &workflowCopy
    
    return nil
}

// GetWorkflow retrieves a workflow by ID
func (s *MemoryStore) GetWorkflow(ctx context.Context, workflowID string) (*types.Workflow, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    workflow, exists := s.workflows[workflowID]
    if !exists {
        return nil, fmt.Errorf("workflow %s not found", workflowID)
    }
    
    // Return a copy
    workflowCopy := *workflow
    return &workflowCopy, nil
}

// UpdateWorkflow updates an existing workflow
func (s *MemoryStore) UpdateWorkflow(ctx context.Context, workflow *types.Workflow) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if _, exists := s.workflows[workflow.ID]; !exists {
        return fmt.Errorf("workflow %s not found", workflow.ID)
    }
    
    // Update with a copy
    workflowCopy := *workflow
    s.workflows[workflow.ID] = &workflowCopy
    
    return nil
}

// DeleteWorkflow removes a workflow
func (s *MemoryStore) DeleteWorkflow(ctx context.Context, workflowID string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    if _, exists := s.workflows[workflowID]; !exists {
        return fmt.Errorf("workflow %s not found", workflowID)
    }
    
    delete(s.workflows, workflowID)
    return nil
}

// ListWorkflows returns workflows matching the filter
func (s *MemoryStore) ListWorkflows(ctx context.Context, filter map[string]string) ([]*types.Workflow, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    var workflows []*types.Workflow
    
    for _, workflow := range s.workflows {
        if matchesFilter(workflow.Metadata, filter) {
            workflowCopy := *workflow
            workflows = append(workflows, &workflowCopy)
        }
    }
    
    return workflows, nil
}

// GetTask retrieves a task from a workflow
func (s *MemoryStore) GetTask(ctx context.Context, workflowID, taskID string) (*types.Task, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    workflow, exists := s.workflows[workflowID]
    if !exists {
        return nil, fmt.Errorf("workflow %s not found", workflowID)
    }
    
    for i := range workflow.Tasks {
        if workflow.Tasks[i].ID == taskID {
            taskCopy := workflow.Tasks[i]
            return &taskCopy, nil
        }
    }
    
    return nil, fmt.Errorf("task %s not found in workflow %s", taskID, workflowID)
}

// UpdateTask updates a task within a workflow
func (s *MemoryStore) UpdateTask(ctx context.Context, workflowID string, task *types.Task) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    workflow, exists := s.workflows[workflowID]
    if !exists {
        return fmt.Errorf("workflow %s not found", workflowID)
    }
    
    for i := range workflow.Tasks {
        if workflow.Tasks[i].ID == task.ID {
            workflow.Tasks[i] = *task
            return nil
        }
    }
    
    return fmt.Errorf("task %s not found in workflow %s", task.ID, workflowID)
}

// RecordEvent records a system event
func (s *MemoryStore) RecordEvent(ctx context.Context, event *types.Event) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    eventCopy := *event
    s.events = append(s.events, &eventCopy)
    
    return nil
}

// GetEvents retrieves events matching the filter
func (s *MemoryStore) GetEvents(ctx context.Context, filter map[string]string, limit int) ([]*types.Event, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    var events []*types.Event
    count := 0
    
    // Iterate in reverse order (newest first)
    for i := len(s.events) - 1; i >= 0 && (limit <= 0 || count < limit); i-- {
        event := s.events[i]
        if matchesEventFilter(event, filter) {
            eventCopy := *event
            events = append(events, &eventCopy)
            count++
        }
    }
    
    return events, nil
}

// Transaction executes a function within a transaction
func (s *MemoryStore) Transaction(ctx context.Context, fn func(tx StateManager) error) error {
    // For memory store, we just execute the function with the store itself
    // In a real implementation, this would create a transaction context
    return fn(s)
}

// Helper functions

func matchesFilter(data map[string]string, filter map[string]string) bool {
    for key, value := range filter {
        if data[key] != value {
            return false
        }
    }
    return true
}

func matchesEventFilter(event *types.Event, filter map[string]string) bool {
    for key, value := range filter {
        switch key {
        case "type":
            if string(event.Type) != value {
                return false
            }
        case "source":
            if event.Source != value {
                return false
            }
        default:
            // Check in event data
            if dataValue, ok := event.Data[key]; ok {
                if fmt.Sprintf("%v", dataValue) != value {
                    return false
                }
            } else {
                return false
            }
        }
    }
    return true
}

// MemoryStoreFactory creates memory store instances
type MemoryStoreFactory struct{}

// Create creates a new memory store instance
func (f *MemoryStoreFactory) Create(config Config) (Store, error) {
    return NewMemoryStore(), nil
}