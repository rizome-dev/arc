package state

import (
    "context"
    "fmt"
    "testing"
    "time"

    "github.com/rizome-dev/arc/pkg/types"
)

func TestMemoryStore_Agent(t *testing.T) {
    ctx := context.Background()
    store := NewMemoryStore()
    
    // Create agent
    agent := &types.Agent{
        ID:     "test-agent-1",
        Name:   "Test Agent",
        Image:  "alpine:latest",
        Status: types.AgentStatusPending,
        Labels: map[string]string{
            "test": "true",
        },
    }
    
    // Test Create
    err := store.CreateAgent(ctx, agent)
    if err != nil {
        t.Fatalf("Failed to create agent: %v", err)
    }
    
    // Test Get
    retrieved, err := store.GetAgent(ctx, agent.ID)
    if err != nil {
        t.Fatalf("Failed to get agent: %v", err)
    }
    if retrieved.ID != agent.ID {
        t.Errorf("Expected agent ID %s, got %s", agent.ID, retrieved.ID)
    }
    
    // Test Update
    agent.Status = types.AgentStatusRunning
    err = store.UpdateAgent(ctx, agent)
    if err != nil {
        t.Fatalf("Failed to update agent: %v", err)
    }
    
    retrieved, err = store.GetAgent(ctx, agent.ID)
    if err != nil {
        t.Fatalf("Failed to get updated agent: %v", err)
    }
    if retrieved.Status != types.AgentStatusRunning {
        t.Errorf("Expected status %s, got %s", types.AgentStatusRunning, retrieved.Status)
    }
    
    // Test List
    agents, err := store.ListAgents(ctx, map[string]string{"test": "true"})
    if err != nil {
        t.Fatalf("Failed to list agents: %v", err)
    }
    if len(agents) != 1 {
        t.Errorf("Expected 1 agent, got %d", len(agents))
    }
    
    // Test Delete
    err = store.DeleteAgent(ctx, agent.ID)
    if err != nil {
        t.Fatalf("Failed to delete agent: %v", err)
    }
    
    _, err = store.GetAgent(ctx, agent.ID)
    if err == nil {
        t.Error("Expected error getting deleted agent")
    }
}

func TestMemoryStore_Workflow(t *testing.T) {
    ctx := context.Background()
    store := NewMemoryStore()
    
    // Create workflow
    workflow := &types.Workflow{
        ID:     "test-workflow-1",
        Name:   "Test Workflow",
        Status: types.WorkflowStatusPending,
        Tasks: []types.Task{
            {
                ID:     "task-1",
                Name:   "Task 1",
                Status: types.TaskStatusPending,
            },
            {
                ID:           "task-2",
                Name:         "Task 2",
                Status:       types.TaskStatusPending,
                Dependencies: []string{"task-1"},
            },
        },
        Metadata: map[string]string{
            "type": "test",
        },
    }
    
    // Test Create
    err := store.CreateWorkflow(ctx, workflow)
    if err != nil {
        t.Fatalf("Failed to create workflow: %v", err)
    }
    
    // Test Get
    retrieved, err := store.GetWorkflow(ctx, workflow.ID)
    if err != nil {
        t.Fatalf("Failed to get workflow: %v", err)
    }
    if retrieved.ID != workflow.ID {
        t.Errorf("Expected workflow ID %s, got %s", workflow.ID, retrieved.ID)
    }
    if len(retrieved.Tasks) != 2 {
        t.Errorf("Expected 2 tasks, got %d", len(retrieved.Tasks))
    }
    
    // Test Update
    workflow.Status = types.WorkflowStatusRunning
    now := time.Now()
    workflow.StartedAt = &now
    err = store.UpdateWorkflow(ctx, workflow)
    if err != nil {
        t.Fatalf("Failed to update workflow: %v", err)
    }
    
    // Test Get Task
    task, err := store.GetTask(ctx, workflow.ID, "task-1")
    if err != nil {
        t.Fatalf("Failed to get task: %v", err)
    }
    if task.ID != "task-1" {
        t.Errorf("Expected task ID task-1, got %s", task.ID)
    }
    
    // Test Update Task
    task.Status = types.TaskStatusRunning
    err = store.UpdateTask(ctx, workflow.ID, task)
    if err != nil {
        t.Fatalf("Failed to update task: %v", err)
    }
    
    // Verify task update
    updated, err := store.GetWorkflow(ctx, workflow.ID)
    if err != nil {
        t.Fatalf("Failed to get workflow after task update: %v", err)
    }
    if updated.Tasks[0].Status != types.TaskStatusRunning {
        t.Errorf("Expected task status %s, got %s", types.TaskStatusRunning, updated.Tasks[0].Status)
    }
    
    // Test List
    workflows, err := store.ListWorkflows(ctx, map[string]string{"type": "test"})
    if err != nil {
        t.Fatalf("Failed to list workflows: %v", err)
    }
    if len(workflows) != 1 {
        t.Errorf("Expected 1 workflow, got %d", len(workflows))
    }
}

func TestMemoryStore_Events(t *testing.T) {
    ctx := context.Background()
    store := NewMemoryStore()
    
    // Record events
    events := []*types.Event{
        {
            ID:        "event-1",
            Type:      types.EventTypeAgentCreated,
            Source:    "test",
            Timestamp: time.Now(),
            Data: map[string]interface{}{
                "agent_id": "agent-1",
            },
        },
        {
            ID:        "event-2",
            Type:      types.EventTypeAgentStarted,
            Source:    "test",
            Timestamp: time.Now().Add(1 * time.Second),
            Data: map[string]interface{}{
                "agent_id": "agent-1",
            },
        },
        {
            ID:        "event-3",
            Type:      types.EventTypeWorkflowStarted,
            Source:    "orchestrator",
            Timestamp: time.Now().Add(2 * time.Second),
            Data: map[string]interface{}{
                "workflow_id": "workflow-1",
            },
        },
    }
    
    for _, event := range events {
        err := store.RecordEvent(ctx, event)
        if err != nil {
            t.Fatalf("Failed to record event: %v", err)
        }
    }
    
    // Test filter by type
    retrieved, err := store.GetEvents(ctx, map[string]string{"type": string(types.EventTypeAgentCreated)}, 0)
    if err != nil {
        t.Fatalf("Failed to get events: %v", err)
    }
    if len(retrieved) != 1 {
        t.Errorf("Expected 1 event, got %d", len(retrieved))
    }
    
    // Test filter by source
    retrieved, err = store.GetEvents(ctx, map[string]string{"source": "test"}, 0)
    if err != nil {
        t.Fatalf("Failed to get events by source: %v", err)
    }
    if len(retrieved) != 2 {
        t.Errorf("Expected 2 events, got %d", len(retrieved))
    }
    
    // Test limit
    retrieved, err = store.GetEvents(ctx, map[string]string{}, 2)
    if err != nil {
        t.Fatalf("Failed to get events with limit: %v", err)
    }
    if len(retrieved) != 2 {
        t.Errorf("Expected 2 events with limit, got %d", len(retrieved))
    }
    
    // Verify order (newest first)
    if retrieved[0].ID != "event-3" {
        t.Error("Events should be returned in reverse chronological order")
    }
}

func TestMemoryStore_Transaction(t *testing.T) {
    ctx := context.Background()
    store := NewMemoryStore()
    
    // Test successful transaction
    err := store.Transaction(ctx, func(tx StateManager) error {
        agent := &types.Agent{
            ID:     "tx-agent-1",
            Name:   "Transaction Test",
            Status: types.AgentStatusPending,
        }
        return tx.CreateAgent(ctx, agent)
    })
    
    if err != nil {
        t.Fatalf("Transaction failed: %v", err)
    }
    
    // Verify agent was created
    agent, err := store.GetAgent(ctx, "tx-agent-1")
    if err != nil {
        t.Fatalf("Failed to get agent after transaction: %v", err)
    }
    if agent.Name != "Transaction Test" {
        t.Error("Agent not properly created in transaction")
    }
}

func TestMemoryStore_Concurrent(t *testing.T) {
    ctx := context.Background()
    store := NewMemoryStore()
    
    // Test concurrent writes
    done := make(chan bool, 10)
    
    for i := 0; i < 10; i++ {
        go func(id int) {
            agent := &types.Agent{
                ID:     fmt.Sprintf("concurrent-agent-%d", id),
                Name:   fmt.Sprintf("Agent %d", id),
                Status: types.AgentStatusPending,
            }
            err := store.CreateAgent(ctx, agent)
            if err != nil {
                t.Errorf("Failed to create agent %d: %v", id, err)
            }
            done <- true
        }(i)
    }
    
    // Wait for all goroutines
    for i := 0; i < 10; i++ {
        <-done
    }
    
    // Verify all agents were created
    agents, err := store.ListAgents(ctx, map[string]string{})
    if err != nil {
        t.Fatalf("Failed to list agents: %v", err)
    }
    if len(agents) != 10 {
        t.Errorf("Expected 10 agents, got %d", len(agents))
    }
}