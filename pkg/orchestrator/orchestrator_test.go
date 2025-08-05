package orchestrator

import (
    "context"
    "fmt"
    "io"
    "testing"
    "time"

    "github.com/rizome-dev/amq/pkg/client"
    amqtypes "github.com/rizome-dev/amq/pkg/types"
    "github.com/rizome-dev/arc/pkg/messagequeue"
    "github.com/rizome-dev/arc/pkg/state"
    "github.com/rizome-dev/arc/pkg/types"
)

// MockRuntime implements runtime.Runtime for testing
type MockRuntime struct {
    agents map[string]*types.Agent
}

func NewMockRuntime() *MockRuntime {
    return &MockRuntime{
        agents: make(map[string]*types.Agent),
    }
}

func (r *MockRuntime) CreateAgent(ctx context.Context, agent *types.Agent) error {
    agent.Status = types.AgentStatusCreating
    agent.ContainerID = "mock-" + agent.ID
    r.agents[agent.ID] = agent
    return nil
}

func (r *MockRuntime) StartAgent(ctx context.Context, agentID string) error {
    if agent, ok := r.agents[agentID]; ok {
        agent.Status = types.AgentStatusRunning
        now := time.Now()
        agent.StartedAt = &now
    }
    return nil
}

func (r *MockRuntime) StopAgent(ctx context.Context, agentID string) error {
    if agent, ok := r.agents[agentID]; ok {
        agent.Status = types.AgentStatusCompleted
        now := time.Now()
        agent.CompletedAt = &now
    }
    return nil
}

func (r *MockRuntime) DestroyAgent(ctx context.Context, agentID string) error {
    delete(r.agents, agentID)
    return nil
}

func (r *MockRuntime) GetAgentStatus(ctx context.Context, agentID string) (*types.Agent, error) {
    if agent, ok := r.agents[agentID]; ok {
        return agent, nil
    }
    return nil, fmt.Errorf("agent not found")
}

func (r *MockRuntime) ListAgents(ctx context.Context) ([]*types.Agent, error) {
    agents := make([]*types.Agent, 0, len(r.agents))
    for _, agent := range r.agents {
        agents = append(agents, agent)
    }
    return agents, nil
}

func (r *MockRuntime) StreamLogs(ctx context.Context, agentID string) (io.ReadCloser, error) {
    return nil, fmt.Errorf("not implemented")
}

func (r *MockRuntime) ExecCommand(ctx context.Context, agentID string, cmd []string) (io.ReadCloser, error) {
    return nil, fmt.Errorf("not implemented")
}

// MockMessageQueue implements messagequeue.MessageQueue for testing
type MockMessageQueue struct {
    queues map[string]bool
}

func NewMockMessageQueue() *MockMessageQueue {
    return &MockMessageQueue{
        queues: make(map[string]bool),
    }
}

func (mq *MockMessageQueue) GetClient(agentID string, metadata map[string]string) (client.Client, error) {
    return nil, nil
}

func (mq *MockMessageQueue) GetAsyncConsumer(agentID string, metadata map[string]string) (client.AsyncConsumer, error) {
    return nil, nil
}

func (mq *MockMessageQueue) CreateQueue(ctx context.Context, name string) error {
    mq.queues[name] = true
    return nil
}

func (mq *MockMessageQueue) DeleteQueue(ctx context.Context, name string) error {
    delete(mq.queues, name)
    return nil
}

func (mq *MockMessageQueue) SendMessage(ctx context.Context, from, to string, message *messagequeue.Message) error {
    return nil
}

func (mq *MockMessageQueue) PublishTask(ctx context.Context, from, topic string, message *messagequeue.Message) error {
    return nil
}

func (mq *MockMessageQueue) GetQueueStats(ctx context.Context, queueName string) (*amqtypes.QueueStats, error) {
    return nil, nil
}

func (mq *MockMessageQueue) ListQueues(ctx context.Context) ([]*amqtypes.Queue, error) {
    return nil, nil
}

func (mq *MockMessageQueue) Close() error {
    return nil
}

func TestOrchestrator_CreateWorkflow(t *testing.T) {
    ctx := context.Background()
    
    // Create orchestrator with mocks
    arc, err := New(Config{
        Runtime:      NewMockRuntime(),
        MessageQueue: NewMockMessageQueue(),
        StateManager: state.NewMemoryStore(),
    })
    if err != nil {
        t.Fatalf("Failed to create orchestrator: %v", err)
    }
    
    // Start orchestrator
    if err := arc.Start(); err != nil {
        t.Fatalf("Failed to start orchestrator: %v", err)
    }
    defer arc.Stop()
    
    // Create a test workflow
    workflow := &types.Workflow{
        Name: "test-workflow",
        Tasks: []types.Task{
            {
                Name: "task1",
                AgentConfig: types.AgentConfig{
                    Command: []string{"echo"},
                    Args:    []string{"hello"},
                },
            },
            {
                Name:         "task2",
                Dependencies: []string{}, // Will be set after creation
                AgentConfig: types.AgentConfig{
                    Command: []string{"echo"},
                    Args:    []string{"world"},
                },
            },
        },
    }
    
    // Create workflow
    if err := arc.CreateWorkflow(ctx, workflow); err != nil {
        t.Fatalf("Failed to create workflow: %v", err)
    }
    
    // Verify workflow was created
    if workflow.ID == "" {
        t.Error("Workflow ID should be set")
    }
    if workflow.Status != types.WorkflowStatusPending {
        t.Errorf("Expected status %s, got %s", types.WorkflowStatusPending, workflow.Status)
    }
    
    // Verify tasks have IDs
    for i, task := range workflow.Tasks {
        if task.ID == "" {
            t.Errorf("Task %d should have ID", i)
        }
    }
}

func TestOrchestrator_ValidateWorkflow(t *testing.T) {
    ctx := context.Background()
    
    arc, err := New(Config{
        Runtime:      NewMockRuntime(),
        MessageQueue: NewMockMessageQueue(),
        StateManager: state.NewMemoryStore(),
    })
    if err != nil {
        t.Fatalf("Failed to create orchestrator: %v", err)
    }
    
    tests := []struct {
        name      string
        workflow  *types.Workflow
        wantError bool
    }{
        {
            name: "valid workflow",
            workflow: &types.Workflow{
                Name: "valid",
                Tasks: []types.Task{
                    {Name: "task1", AgentConfig: types.AgentConfig{}},
                },
            },
            wantError: false,
        },
        {
            name: "missing name",
            workflow: &types.Workflow{
                Tasks: []types.Task{
                    {Name: "task1", AgentConfig: types.AgentConfig{}},
                },
            },
            wantError: true,
        },
        {
            name: "no tasks",
            workflow: &types.Workflow{
                Name:  "empty",
                Tasks: []types.Task{},
            },
            wantError: true,
        },
        {
            name: "duplicate task IDs",
            workflow: &types.Workflow{
                Name: "duplicate",
                Tasks: []types.Task{
                    {ID: "task1", Name: "task1", AgentConfig: types.AgentConfig{}},
                    {ID: "task1", Name: "task2", AgentConfig: types.AgentConfig{}},
                },
            },
            wantError: true,
        },
    }
    
    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            err := arc.CreateWorkflow(ctx, tt.workflow)
            if (err != nil) != tt.wantError {
                t.Errorf("CreateWorkflow() error = %v, wantError %v", err, tt.wantError)
            }
        })
    }
}

func TestOrchestrator_DependencyGraph(t *testing.T) {
    arc := &Orchestrator{}
    
    workflow := &types.Workflow{
        Tasks: []types.Task{
            {ID: "a", Dependencies: []string{}},
            {ID: "b", Dependencies: []string{"a"}},
            {ID: "c", Dependencies: []string{"a"}},
            {ID: "d", Dependencies: []string{"b", "c"}},
        },
    }
    
    graph := arc.buildDependencyGraph(workflow)
    
    // Verify graph structure
    if len(graph["a"]) != 0 {
        t.Error("Task 'a' should have no dependencies")
    }
    if len(graph["b"]) != 1 || graph["b"][0] != "a" {
        t.Error("Task 'b' should depend on 'a'")
    }
    if len(graph["c"]) != 1 || graph["c"][0] != "a" {
        t.Error("Task 'c' should depend on 'a'")
    }
    if len(graph["d"]) != 2 {
        t.Error("Task 'd' should have 2 dependencies")
    }
}

func TestOrchestrator_FindReadyTasks(t *testing.T) {
    arc := &Orchestrator{}
    
    workflow := &types.Workflow{
        Tasks: []types.Task{
            {ID: "a", Name: "a", Status: types.TaskStatusPending, Dependencies: []string{}},
            {ID: "b", Name: "b", Status: types.TaskStatusPending, Dependencies: []string{"a"}},
            {ID: "c", Name: "c", Status: types.TaskStatusPending, Dependencies: []string{"a"}},
            {ID: "d", Name: "d", Status: types.TaskStatusPending, Dependencies: []string{"b", "c"}},
        },
    }
    
    graph := arc.buildDependencyGraph(workflow)
    completed := make(map[string]bool)
    
    // Initially, only 'a' should be ready
    ready := arc.findReadyTasks(workflow, graph, completed)
    if len(ready) != 1 || ready[0].ID != "a" {
        t.Error("Initially only task 'a' should be ready")
    }
    
    // After completing 'a', both 'b' and 'c' should be ready
    completed["a"] = true
    ready = arc.findReadyTasks(workflow, graph, completed)
    if len(ready) != 2 {
        t.Error("Tasks 'b' and 'c' should be ready after 'a' completes")
    }
    
    // After completing 'b' and 'c', 'd' should be ready
    completed["b"] = true
    completed["c"] = true
    ready = arc.findReadyTasks(workflow, graph, completed)
    if len(ready) != 1 || ready[0].ID != "d" {
        t.Error("Task 'd' should be ready after 'b' and 'c' complete")
    }
}