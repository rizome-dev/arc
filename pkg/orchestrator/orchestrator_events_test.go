package orchestrator

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
)

func TestOrchestrator_EventRecording(t *testing.T) {
	ctx := context.Background()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      NewMockRuntime(),
		MessageQueue: NewMockMessageQueue(),
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create and start workflow to generate events
	workflow := &types.Workflow{
		Name: "event-test-workflow",
		Tasks: []types.Task{
			{
				Name: "event-test-task",
				AgentConfig: types.AgentConfig{
					Command: []string{"echo"},
					Args:    []string{"event test"},
				},
			},
		},
	}
	
	// Create workflow - should generate WorkflowCreated event
	if err := arc.CreateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}
	
	// Start workflow - should generate WorkflowStarted event
	if err := arc.StartWorkflow(ctx, workflow.ID); err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	
	// Wait a bit for events to be recorded
	time.Sleep(100 * time.Millisecond)
	
	// Verify events were recorded
	events, err := stateManager.GetEvents(ctx, nil, 10)
	if err != nil {
		t.Errorf("Failed to get events: %v", err)
	}
	
	if len(events) == 0 {
		t.Error("Expected events to be recorded")
	}
	
	// Look for workflow started event
	foundWorkflowStarted := false
	for _, event := range events {
		if event.Type == types.EventTypeWorkflowStarted {
			foundWorkflowStarted = true
			break
		}
	}
	
	if !foundWorkflowStarted {
		t.Error("Expected WorkflowStarted event to be recorded")
	}
}

func TestOrchestrator_AgentStatusChangeEvents(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntime()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: NewMockMessageQueue(),
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create workflow with agent
	workflow := &types.Workflow{
		Name: "status-change-test",
		Tasks: []types.Task{
			{
				Name: "status-test-task",
				AgentConfig: types.AgentConfig{
					Command: []string{"echo"},
					Args:    []string{"test"},
				},
			},
		},
	}
	
	if err := arc.CreateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}
	
	if err := arc.StartWorkflow(ctx, workflow.ID); err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	
	// Wait for workflow to be set up
	time.Sleep(100 * time.Millisecond)
	
	// Get the workflow execution and agent
	arc.mu.RLock()
	exec, exists := arc.workflows[workflow.ID]
	arc.mu.RUnlock()
	
	if !exists || len(exec.agents) == 0 {
		t.Fatal("Workflow should exist and have agents")
	}
	
	var testAgent *types.Agent
	for _, agent := range exec.agents {
		testAgent = agent
		break
	}
	
	// Simulate agent status change by updating in runtime
	runtime.agents[testAgent.ID].Status = types.AgentStatusCompleted
	runtime.agents[testAgent.ID].CompletedAt = &[]time.Time{time.Now()}[0]
	
	// Trigger health check to detect status change
	arc.checkAgentHealth()
	
	// Wait for status update to propagate
	time.Sleep(100 * time.Millisecond)
	
	// Verify status change events were recorded
	events, err := stateManager.GetEvents(ctx, map[string]string{
		"type": string(types.EventTypeAgentStatusChanged),
	}, 10)
	if err != nil {
		t.Errorf("Failed to get status change events: %v", err)
	}
	
	if len(events) == 0 {
		t.Error("Status change events should have been recorded")
	}
}

func TestOrchestrator_WorkflowLifecycleEvents(t *testing.T) {
	ctx := context.Background()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      NewMockRuntime(),
		MessageQueue: NewMockMessageQueue(),
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create workflow
	workflow := &types.Workflow{
		Name: "lifecycle-test",
		Tasks: []types.Task{
			{
				Name: "lifecycle-task",
				AgentConfig: types.AgentConfig{
					Command: []string{"echo"},
					Args:    []string{"lifecycle test"},
				},
			},
		},
	}
	
	// Create workflow
	if err := arc.CreateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}
	
	// Start workflow
	if err := arc.StartWorkflow(ctx, workflow.ID); err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	
	// Stop the orchestrator to trigger cleanup events
	arc.Stop()
	
	// Wait for events to be recorded
	time.Sleep(100 * time.Millisecond)
	
	// Verify lifecycle events
	events, err := stateManager.GetEvents(ctx, nil, 20)
	if err != nil {
		t.Errorf("Failed to get events: %v", err)
	}
	
	eventTypes := make(map[types.EventType]bool)
	for _, event := range events {
		eventTypes[event.Type] = true
	}
	
	expectedEvents := []types.EventType{
		types.EventTypeWorkflowStarted,
	}
	
	for _, expectedType := range expectedEvents {
		if !eventTypes[expectedType] {
			t.Errorf("Expected event type %s not found", expectedType)
		}
	}
}

func TestOrchestrator_TaskExecutionEvents(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntime()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: NewMockMessageQueue(),
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create workflow with task
	workflow := &types.Workflow{
		Name: "task-execution-test",
		Tasks: []types.Task{
			{
				Name: "execution-task",
				AgentConfig: types.AgentConfig{
					Command: []string{"echo"},
					Args:    []string{"task execution test"},
				},
			},
		},
	}
	
	if err := arc.CreateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}
	
	if err := arc.StartWorkflow(ctx, workflow.ID); err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	
	// Wait for workflow execution to be set up
	time.Sleep(100 * time.Millisecond)
	
	// Get the workflow execution and simulate task completion
	arc.mu.RLock()
	exec, exists := arc.workflows[workflow.ID]
	arc.mu.RUnlock()
	
	if !exists || len(exec.agents) == 0 {
		t.Fatal("Workflow should exist and have agents")
	}
	
	var testAgent *types.Agent
	for _, agent := range exec.agents {
		testAgent = agent
		break
	}
	
	// Simulate task completion
	runtime.agents[testAgent.ID].Status = types.AgentStatusCompleted
	runtime.agents[testAgent.ID].CompletedAt = &[]time.Time{time.Now()}[0]
	
	// Trigger health check to detect completion
	arc.checkAgentHealth()
	
	// Wait for events to be recorded
	time.Sleep(100 * time.Millisecond)
	
	// Verify task execution events
	events, err := stateManager.GetEvents(ctx, nil, 20)
	if err != nil {
		t.Errorf("Failed to get events: %v", err)
	}
	
	// Look for agent-related events
	foundAgentEvents := false
	for _, event := range events {
		if event.Type == types.EventTypeAgentCompleted || 
		   event.Type == types.EventTypeAgentStatusChanged {
			foundAgentEvents = true
			break
		}
	}
	
	if !foundAgentEvents {
		t.Error("Expected agent execution events to be recorded")
	}
}

func TestOrchestrator_ConcurrentEventRecording(t *testing.T) {
	ctx := context.Background()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      NewMockRuntime(),
		MessageQueue: NewMockMessageQueue(),
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create multiple workflows concurrently
	numWorkflows := 5
	workflows := make([]*types.Workflow, numWorkflows)
	
	for i := 0; i < numWorkflows; i++ {
		workflows[i] = &types.Workflow{
			Name: fmt.Sprintf("concurrent-test-%d", i),
			Tasks: []types.Task{
				{
					Name: fmt.Sprintf("concurrent-task-%d", i),
					AgentConfig: types.AgentConfig{
						Command: []string{"echo"},
						Args:    []string{fmt.Sprintf("concurrent test %d", i)},
					},
				},
			},
		}
	}
	
	// Create and start workflows concurrently
	for i := 0; i < numWorkflows; i++ {
		go func(workflow *types.Workflow) {
			arc.CreateWorkflow(ctx, workflow)
			arc.StartWorkflow(ctx, workflow.ID)
		}(workflows[i])
	}
	
	// Wait for all workflows to be processed
	time.Sleep(300 * time.Millisecond)
	
	// Verify events were recorded for all workflows
	events, err := stateManager.GetEvents(ctx, nil, numWorkflows*10)
	if err != nil {
		t.Errorf("Failed to get events: %v", err)
	}
	
	if len(events) < numWorkflows {
		t.Errorf("Expected at least %d events, got %d", numWorkflows, len(events))
	}
	
	// Count workflow started events
	workflowStartedCount := 0
	for _, event := range events {
		if event.Type == types.EventTypeWorkflowStarted {
			workflowStartedCount++
		}
	}
	
	if workflowStartedCount != numWorkflows {
		t.Errorf("Expected %d workflow started events, got %d", numWorkflows, workflowStartedCount)
	}
}