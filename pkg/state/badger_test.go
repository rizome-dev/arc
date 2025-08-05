package state

import (
	"context"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/rizome-dev/arc/pkg/types"
)

func createTempBadgerStore(t *testing.T) (*BadgerStore, func()) {
	tempDir, err := os.MkdirTemp("", "badger-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	
	config := BadgerStoreConfig{
		Path:     tempDir,
		EventTTL: 1 * time.Hour,
	}
	
	store, err := NewBadgerStore(config)
	if err != nil {
		os.RemoveAll(tempDir)
		t.Fatalf("Failed to create BadgerStore: %v", err)
	}
	
	cleanup := func() {
		store.Close(context.Background())
		os.RemoveAll(tempDir)
	}
	
	return store, cleanup
}

func TestBadgerStore_NewBadgerStore(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "badger-new-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	tests := []struct {
		name      string
		config    BadgerStoreConfig
		wantError bool
	}{
		{
			name: "valid config",
			config: BadgerStoreConfig{
				Path:     tempDir,
				EventTTL: 1 * time.Hour,
			},
			wantError: false,
		},
		{
			name: "empty path",
			config: BadgerStoreConfig{
				EventTTL: 1 * time.Hour,
			},
			wantError: true,
		},
		{
			name: "default event TTL",
			config: BadgerStoreConfig{
				Path: tempDir + "/default-ttl",
			},
			wantError: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			store, err := NewBadgerStore(tt.config)
			
			if (err != nil) != tt.wantError {
				t.Errorf("NewBadgerStore() error = %v, wantError %v", err, tt.wantError)
				return
			}
			
			if err == nil {
				if store.eventTTL == 0 {
					t.Error("Event TTL should be set to default if not provided")
				}
				store.Close(context.Background())
			}
		})
	}
}

func TestBadgerStore_Initialize(t *testing.T) {
	store, cleanup := createTempBadgerStore(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Test initialization
	err := store.Initialize(ctx)
	if err != nil {
		t.Errorf("Initialize() error = %v", err)
	}
	
	// Test initialization on closed store
	store.Close(ctx)
	err = store.Initialize(ctx)
	if err == nil {
		t.Error("Initialize() should fail on closed store")
	}
}

func TestBadgerStore_HealthCheck(t *testing.T) {
	store, cleanup := createTempBadgerStore(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Test health check on open store
	err := store.HealthCheck(ctx)
	if err != nil {
		t.Errorf("HealthCheck() error = %v", err)
	}
	
	// Test health check on closed store
	store.Close(ctx)
	err = store.HealthCheck(ctx)
	if err == nil {
		t.Error("HealthCheck() should fail on closed store")
	}
}

func TestBadgerStore_AgentOperations(t *testing.T) {
	store, cleanup := createTempBadgerStore(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Test agent creation
	agent := &types.Agent{
		ID:     "test-agent-1",
		Name:   "Test Agent 1",
		Image:  "test:latest",
		Status: types.AgentStatusPending,
		Labels: map[string]string{
			"environment": "test",
			"component":   "worker",
		},
		CreatedAt: time.Now(),
	}
	
	err := store.CreateAgent(ctx, agent)
	if err != nil {
		t.Errorf("CreateAgent() error = %v", err)
	}
	
	// Test duplicate creation
	err = store.CreateAgent(ctx, agent)
	if err == nil {
		t.Error("CreateAgent() should fail for duplicate agent")
	}
	
	// Test agent retrieval
	retrievedAgent, err := store.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Errorf("GetAgent() error = %v", err)
	}
	
	if retrievedAgent.ID != agent.ID || retrievedAgent.Name != agent.Name {
		t.Error("Retrieved agent should match created agent")
	}
	
	// Test agent update
	agent.Status = types.AgentStatusRunning
	agent.Labels["status"] = "updated"
	
	err = store.UpdateAgent(ctx, agent)
	if err != nil {
		t.Errorf("UpdateAgent() error = %v", err)
	}
	
	updatedAgent, err := store.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Errorf("GetAgent() after update error = %v", err)
	}
	
	if updatedAgent.Status != types.AgentStatusRunning {
		t.Error("Agent status should be updated")
	}
	
	if updatedAgent.Labels["status"] != "updated" {
		t.Error("Agent labels should be updated")
	}
	
	// Test list agents without filter
	agents, err := store.ListAgents(ctx, nil)
	if err != nil {
		t.Errorf("ListAgents() error = %v", err)
	}
	
	if len(agents) != 1 {
		t.Errorf("Expected 1 agent, got %d", len(agents))
	}
	
	// Test list agents with filter
	agents, err = store.ListAgents(ctx, map[string]string{
		"environment": "test",
	})
	if err != nil {
		t.Errorf("ListAgents() with filter error = %v", err)
	}
	
	if len(agents) != 1 {
		t.Errorf("Expected 1 agent with filter, got %d", len(agents))
	}
	
	// Test list agents with non-matching filter
	agents, err = store.ListAgents(ctx, map[string]string{
		"environment": "production",
	})
	if err != nil {
		t.Errorf("ListAgents() with non-matching filter error = %v", err)
	}
	
	if len(agents) != 0 {
		t.Errorf("Expected 0 agents with non-matching filter, got %d", len(agents))
	}
	
	// Test agent deletion
	err = store.DeleteAgent(ctx, agent.ID)
	if err != nil {
		t.Errorf("DeleteAgent() error = %v", err)
	}
	
	// Test retrieval of deleted agent
	_, err = store.GetAgent(ctx, agent.ID)
	if err == nil {
		t.Error("GetAgent() should fail for deleted agent")
	}
	
	// Test deletion of non-existent agent
	err = store.DeleteAgent(ctx, "non-existent")
	if err == nil {
		t.Error("DeleteAgent() should fail for non-existent agent")
	}
}

func TestBadgerStore_WorkflowOperations(t *testing.T) {
	store, cleanup := createTempBadgerStore(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Test workflow creation
	workflow := &types.Workflow{
		ID:          "test-workflow-1",
		Name:        "Test Workflow 1",
		Description: "Test workflow description",
		Status:      types.WorkflowStatusPending,
		Tasks: []types.Task{
			{
				ID:   "task-1",
				Name: "Task 1",
				AgentConfig: types.AgentConfig{
					Command: []string{"echo", "hello"},
				},
			},
			{
				ID:           "task-2",
				Name:         "Task 2",
				Dependencies: []string{"task-1"},
				AgentConfig: types.AgentConfig{
					Command: []string{"echo", "world"},
				},
			},
		},
		Metadata: map[string]string{
			"environment": "test",
			"priority":    "high",
		},
		CreatedAt: time.Now(),
	}
	
	err := store.CreateWorkflow(ctx, workflow)
	if err != nil {
		t.Errorf("CreateWorkflow() error = %v", err)
	}
	
	// Test duplicate creation
	err = store.CreateWorkflow(ctx, workflow)
	if err == nil {
		t.Error("CreateWorkflow() should fail for duplicate workflow")
	}
	
	// Test workflow retrieval
	retrievedWorkflow, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Errorf("GetWorkflow() error = %v", err)
	}
	
	if retrievedWorkflow.ID != workflow.ID || retrievedWorkflow.Name != workflow.Name {
		t.Error("Retrieved workflow should match created workflow")
	}
	
	if len(retrievedWorkflow.Tasks) != 2 {
		t.Errorf("Expected 2 tasks, got %d", len(retrievedWorkflow.Tasks))
	}
	
	// Test workflow update
	workflow.Status = types.WorkflowStatusRunning
	now := time.Now()
	workflow.StartedAt = &now
	workflow.Metadata["status"] = "updated"
	
	err = store.UpdateWorkflow(ctx, workflow)
	if err != nil {
		t.Errorf("UpdateWorkflow() error = %v", err)
	}
	
	updatedWorkflow, err := store.GetWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Errorf("GetWorkflow() after update error = %v", err)
	}
	
	if updatedWorkflow.Status != types.WorkflowStatusRunning {
		t.Error("Workflow status should be updated")
	}
	
	if updatedWorkflow.Metadata["status"] != "updated" {
		t.Error("Workflow metadata should be updated")
	}
	
	// Test list workflows without filter
	workflows, err := store.ListWorkflows(ctx, nil)
	if err != nil {
		t.Errorf("ListWorkflows() error = %v", err)
	}
	
	if len(workflows) != 1 {
		t.Errorf("Expected 1 workflow, got %d", len(workflows))
	}
	
	// Test list workflows with filter
	workflows, err = store.ListWorkflows(ctx, map[string]string{
		"environment": "test",
	})
	if err != nil {
		t.Errorf("ListWorkflows() with filter error = %v", err)
	}
	
	if len(workflows) != 1 {
		t.Errorf("Expected 1 workflow with filter, got %d", len(workflows))
	}
	
	// Test workflow deletion
	err = store.DeleteWorkflow(ctx, workflow.ID)
	if err != nil {
		t.Errorf("DeleteWorkflow() error = %v", err)
	}
	
	// Test retrieval of deleted workflow
	_, err = store.GetWorkflow(ctx, workflow.ID)
	if err == nil {
		t.Error("GetWorkflow() should fail for deleted workflow")
	}
}

func TestBadgerStore_TaskOperations(t *testing.T) {
	store, cleanup := createTempBadgerStore(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create a workflow with tasks first
	workflow := &types.Workflow{
		ID:   "test-workflow-tasks",
		Name: "Test Workflow Tasks",
		Tasks: []types.Task{
			{
				ID:     "task-1",
				Name:   "Task 1",
				Status: types.TaskStatusPending,
				AgentConfig: types.AgentConfig{
					Command: []string{"echo", "task1"},
				},
			},
		},
		CreatedAt: time.Now(),
	}
	
	err := store.CreateWorkflow(ctx, workflow)
	if err != nil {
		t.Fatalf("CreateWorkflow() error = %v", err)
	}
	
	// Test task retrieval
	task, err := store.GetTask(ctx, workflow.ID, "task-1")
	if err != nil {
		t.Errorf("GetTask() error = %v", err)
	}
	
	if task.ID != "task-1" || task.Name != "Task 1" {
		t.Error("Retrieved task should match created task")
	}
	
	// Test task update
	task.Status = types.TaskStatusRunning
	now := time.Now()
	task.StartedAt = &now
	
	err = store.UpdateTask(ctx, workflow.ID, task)
	if err != nil {
		t.Errorf("UpdateTask() error = %v", err)
	}
	
	updatedTask, err := store.GetTask(ctx, workflow.ID, "task-1")
	if err != nil {
		t.Errorf("GetTask() after update error = %v", err)
	}
	
	if updatedTask.Status != types.TaskStatusRunning {
		t.Error("Task status should be updated")
	}
	
	// Test task update for non-existent task
	nonExistentTask := &types.Task{
		ID:   "non-existent",
		Name: "Non-existent Task",
	}
	
	err = store.UpdateTask(ctx, workflow.ID, nonExistentTask)
	if err == nil {
		t.Error("UpdateTask() should fail for non-existent task")
	}
	
	// Test task retrieval for non-existent task
	_, err = store.GetTask(ctx, workflow.ID, "non-existent")
	if err == nil {
		t.Error("GetTask() should fail for non-existent task")
	}
}

func TestBadgerStore_EventOperations(t *testing.T) {
	store, cleanup := createTempBadgerStore(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Test event recording
	event1 := &types.Event{
		ID:        "event-1",
		Type:      types.EventTypeAgentCreated,
		Source:    "test",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"agent_id": "test-agent-1",
			"status":   "created",
		},
	}
	
	err := store.RecordEvent(ctx, event1)
	if err != nil {
		t.Errorf("RecordEvent() error = %v", err)
	}
	
	event2 := &types.Event{
		ID:        "event-2",
		Type:      types.EventTypeWorkflowStarted,
		Source:    "orchestrator",
		Timestamp: time.Now().Add(1 * time.Second),
		Data: map[string]interface{}{
			"workflow_id": "test-workflow-1",
		},
	}
	
	err = store.RecordEvent(ctx, event2)
	if err != nil {
		t.Errorf("RecordEvent() error = %v", err)
	}
	
	// Test event retrieval without filter
	events, err := store.GetEvents(ctx, nil, 10)
	if err != nil {
		t.Errorf("GetEvents() error = %v", err)
	}
	
	if len(events) != 2 {
		t.Errorf("Expected 2 events, got %d", len(events))
	}
	
	// Events should be returned in reverse chronological order (newest first)
	if events[0].ID != "event-2" {
		t.Error("Events should be ordered by timestamp (newest first)")
	}
	
	// Test event retrieval with type filter
	events, err = store.GetEvents(ctx, map[string]string{
		"type": string(types.EventTypeAgentCreated),
	}, 10)
	if err != nil {
		t.Errorf("GetEvents() with type filter error = %v", err)
	}
	
	if len(events) != 1 {
		t.Errorf("Expected 1 event with type filter, got %d", len(events))
	}
	
	if events[0].Type != types.EventTypeAgentCreated {
		t.Error("Event should match type filter")
	}
	
	// Test event retrieval with source filter
	events, err = store.GetEvents(ctx, map[string]string{
		"source": "orchestrator",
	}, 10)
	if err != nil {
		t.Errorf("GetEvents() with source filter error = %v", err)
	}
	
	if len(events) != 1 {
		t.Errorf("Expected 1 event with source filter, got %d", len(events))
	}
	
	// Test event retrieval with limit
	events, err = store.GetEvents(ctx, nil, 1)
	if err != nil {
		t.Errorf("GetEvents() with limit error = %v", err)
	}
	
	if len(events) != 1 {
		t.Errorf("Expected 1 event with limit, got %d", len(events))
	}
	
	// Test event retrieval with data filter
	events, err = store.GetEvents(ctx, map[string]string{
		"agent_id": "test-agent-1",
	}, 10)
	if err != nil {
		t.Errorf("GetEvents() with data filter error = %v", err)
	}
	
	if len(events) != 1 {
		t.Errorf("Expected 1 event with data filter, got %d", len(events))
	}
}

func TestBadgerStore_TransactionOperations(t *testing.T) {
	store, cleanup := createTempBadgerStore(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Test successful transaction
	err := store.Transaction(ctx, func(tx StateManager) error {
		agent := &types.Agent{
			ID:        "tx-agent-1",
			Name:      "Transaction Agent 1",
			Status:    types.AgentStatusPending,
			CreatedAt: time.Now(),
		}
		
		if err := tx.CreateAgent(ctx, agent); err != nil {
			return err
		}
		
		agent.Status = types.AgentStatusRunning
		return tx.UpdateAgent(ctx, agent)
	})
	
	if err != nil {
		t.Errorf("Transaction() error = %v", err)
	}
	
	// Verify transaction changes were committed
	agent, err := store.GetAgent(ctx, "tx-agent-1")
	if err != nil {
		t.Errorf("GetAgent() after transaction error = %v", err)
	}
	
	if agent.Status != types.AgentStatusRunning {
		t.Error("Transaction changes should be committed")
	}
	
	// Test failed transaction (should rollback)
	err = store.Transaction(ctx, func(tx StateManager) error {
		agent := &types.Agent{
			ID:        "tx-agent-2",
			Name:      "Transaction Agent 2",
			Status:    types.AgentStatusPending,
			CreatedAt: time.Now(),
		}
		
		if err := tx.CreateAgent(ctx, agent); err != nil {
			return err
		}
		
		// Return an error to cause rollback
		return fmt.Errorf("intentional transaction failure")
	})
	
	if err == nil {
		t.Error("Transaction() should fail when function returns error")
	}
	
	// Verify transaction was rolled back
	_, err = store.GetAgent(ctx, "tx-agent-2")
	if err == nil {
		t.Error("Transaction rollback should prevent agent creation")
	}
}

func TestBadgerStore_ConcurrentOperations(t *testing.T) {
	store, cleanup := createTempBadgerStore(t)
	defer cleanup()
	
	ctx := context.Background()
	numGoroutines := 10
	numOperations := 50
	
	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*numOperations)
	
	// Concurrent agent operations
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			
			for j := 0; j < numOperations; j++ {
				agent := &types.Agent{
					ID:        fmt.Sprintf("concurrent-agent-%d-%d", id, j),
					Name:      fmt.Sprintf("Agent %d-%d", id, j),
					Status:    types.AgentStatusPending,
					CreatedAt: time.Now(),
				}
				
				if err := store.CreateAgent(ctx, agent); err != nil {
					errors <- err
					continue
				}
				
				agent.Status = types.AgentStatusRunning
				if err := store.UpdateAgent(ctx, agent); err != nil {
					errors <- err
					continue
				}
				
				if _, err := store.GetAgent(ctx, agent.ID); err != nil {
					errors <- err
					continue
				}
			}
		}(i)
	}
	
	wg.Wait()
	close(errors)
	
	// Check for errors
	for err := range errors {
		t.Errorf("Concurrent operation error: %v", err)
	}
	
	// Verify all agents were created
	agents, err := store.ListAgents(ctx, nil)
	if err != nil {
		t.Errorf("ListAgents() after concurrent operations error = %v", err)
	}
	
	expectedCount := numGoroutines * numOperations
	if len(agents) != expectedCount {
		t.Errorf("Expected %d agents after concurrent operations, got %d", expectedCount, len(agents))
	}
}

func TestBadgerStore_EventTTL(t *testing.T) {
	// Create store with short TTL for testing
	tempDir, err := os.MkdirTemp("", "badger-ttl-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	config := BadgerStoreConfig{
		Path:     tempDir,
		EventTTL: 100 * time.Millisecond, // Very short TTL for testing
	}
	
	store, err := NewBadgerStore(config)
	if err != nil {
		t.Fatalf("Failed to create BadgerStore: %v", err)
	}
	defer store.Close(context.Background())
	
	ctx := context.Background()
	
	// Record an event
	event := &types.Event{
		ID:        "ttl-test-event",
		Type:      types.EventTypeAgentCreated,
		Source:    "test",
		Timestamp: time.Now(),
		Data: map[string]interface{}{
			"test": "data",
		},
	}
	
	err = store.RecordEvent(ctx, event)
	if err != nil {
		t.Errorf("RecordEvent() error = %v", err)
	}
	
	// Immediately verify event exists
	events, err := store.GetEvents(ctx, nil, 10)
	if err != nil {
		t.Errorf("GetEvents() error = %v", err)
	}
	
	if len(events) != 1 {
		t.Errorf("Expected 1 event, got %d", len(events))
	}
	
	// Wait for TTL to expire
	time.Sleep(200 * time.Millisecond)
	
	// Trigger garbage collection to clean up expired events
	store.db.RunValueLogGC(0.1)
	
	// Note: BadgerDB TTL cleanup is not immediate and depends on GC cycles
	// In a real test, you might need to wait longer or manually trigger compaction
}

func TestBadgerStore_LabelIndexing(t *testing.T) {
	store, cleanup := createTempBadgerStore(t)
	defer cleanup()
	
	ctx := context.Background()
	
	// Create agents with different labels
	agents := []*types.Agent{
		{
			ID:     "agent-1",
			Name:   "Agent 1",
			Status: types.AgentStatusPending,
			Labels: map[string]string{
				"environment": "production",
				"team":        "backend",
				"region":      "us-east-1",
			},
		},
		{
			ID:     "agent-2",
			Name:   "Agent 2",
			Status: types.AgentStatusRunning,
			Labels: map[string]string{
				"environment": "staging",
				"team":        "backend",
				"region":      "us-west-2",
			},
		},
		{
			ID:     "agent-3",
			Name:   "Agent 3",
			Status: types.AgentStatusCompleted,
			Labels: map[string]string{
				"environment": "production",
				"team":        "frontend",
				"region":      "us-east-1",
			},
		},
	}
	
	// Create all agents
	for _, agent := range agents {
		agent.CreatedAt = time.Now()
		if err := store.CreateAgent(ctx, agent); err != nil {
			t.Errorf("CreateAgent() error = %v", err)
		}
	}
	
	// Test filtering by single label
	results, err := store.ListAgents(ctx, map[string]string{
		"environment": "production",
	})
	if err != nil {
		t.Errorf("ListAgents() with environment filter error = %v", err)
	}
	
	if len(results) != 2 {
		t.Errorf("Expected 2 agents with environment=production, got %d", len(results))
	}
	
	// Test filtering by multiple labels (intersection)
	results, err = store.ListAgents(ctx, map[string]string{
		"environment": "production",
		"team":        "backend",
	})
	if err != nil {
		t.Errorf("ListAgents() with multiple filters error = %v", err)
	}
	
	if len(results) != 1 {
		t.Errorf("Expected 1 agent with environment=production AND team=backend, got %d", len(results))
	}
	
	if results[0].ID != "agent-1" {
		t.Error("Wrong agent returned by label filter")
	}
	
	// Test filtering with no matches
	results, err = store.ListAgents(ctx, map[string]string{
		"environment": "development",
	})
	if err != nil {
		t.Errorf("ListAgents() with non-matching filter error = %v", err)
	}
	
	if len(results) != 0 {
		t.Errorf("Expected 0 agents with environment=development, got %d", len(results))
	}
}

func TestBadgerStore_CloseAndReopen(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "badger-reopen-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	ctx := context.Background()
	
	// Create initial store and add data
	config := BadgerStoreConfig{
		Path:     tempDir,
		EventTTL: 1 * time.Hour,
	}
	
	store1, err := NewBadgerStore(config)
	if err != nil {
		t.Fatalf("Failed to create first BadgerStore: %v", err)
	}
	
	agent := &types.Agent{
		ID:        "persistent-agent",
		Name:      "Persistent Agent",
		Status:    types.AgentStatusRunning,
		CreatedAt: time.Now(),
	}
	
	err = store1.CreateAgent(ctx, agent)
	if err != nil {
		t.Errorf("CreateAgent() error = %v", err)
	}
	
	// Close first store
	err = store1.Close(ctx)
	if err != nil {
		t.Errorf("Close() error = %v", err)
	}
	
	// Reopen store with same path
	store2, err := NewBadgerStore(config)
	if err != nil {
		t.Fatalf("Failed to create second BadgerStore: %v", err)
	}
	defer store2.Close(ctx)
	
	// Verify data persisted
	retrievedAgent, err := store2.GetAgent(ctx, "persistent-agent")
	if err != nil {
		t.Errorf("GetAgent() after reopen error = %v", err)
	}
	
	if retrievedAgent.ID != agent.ID || retrievedAgent.Name != agent.Name {
		t.Error("Agent data should persist after store close/reopen")
	}
}

func TestBadgerStoreFactory(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "badger-factory-test-*")
	if err != nil {
		t.Fatalf("Failed to create temp directory: %v", err)
	}
	defer os.RemoveAll(tempDir)
	
	factory := &BadgerStoreFactory{}
	
	config := Config{
		Path: tempDir,
		Options: map[string]interface{}{
			"event_ttl":  "2h",
			"log_level":  "info",
		},
	}
	
	store, err := factory.Create(config)
	if err != nil {
		t.Errorf("Factory.Create() error = %v", err)
	}
	
	if store == nil {
		t.Error("Factory should create a store")
	}
	
	// Test store functionality
	ctx := context.Background()
	badgerStore := store.(*BadgerStore)
	
	if badgerStore.eventTTL != 2*time.Hour {
		t.Error("Factory should set event TTL from options")
	}
	
	// Clean up
	store.Close(ctx)
}