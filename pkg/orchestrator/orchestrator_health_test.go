package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
)

// MockRuntimeWithHealth extends MockRuntime with health check capabilities
type MockRuntimeWithHealth struct {
	*MockRuntime
	healthCheckCount int
	healthCheckError error
	mu               sync.Mutex
}

func NewMockRuntimeWithHealth() *MockRuntimeWithHealth {
	return &MockRuntimeWithHealth{
		MockRuntime: NewMockRuntime(),
	}
}

func (r *MockRuntimeWithHealth) GetAgentStatus(ctx context.Context, agentID string) (*types.Agent, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	r.healthCheckCount++
	
	if r.healthCheckError != nil {
		return nil, r.healthCheckError
	}
	
	return r.MockRuntime.GetAgentStatus(ctx, agentID)
}

func (r *MockRuntimeWithHealth) SetHealthCheckError(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.healthCheckError = err
}

func (r *MockRuntimeWithHealth) GetHealthCheckCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.healthCheckCount
}

func (r *MockRuntimeWithHealth) ResetHealthCheckCount() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.healthCheckCount = 0
}

func TestOrchestrator_HealthMonitoring(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntimeWithHealth()
	stateManager := state.NewMemoryStore()
	
	// Create orchestrator
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: NewMockMessageQueue(),
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	// Start orchestrator with health monitoring
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create a test workflow with an agent
	workflow := &types.Workflow{
		Name: "health-test-workflow",
		Tasks: []types.Task{
			{
				Name: "health-test-task",
				AgentConfig: types.AgentConfig{
					Command: []string{"echo"},
					Args:    []string{"health test"},
				},
			},
		},
	}
	
	// Create and start workflow to create an agent
	if err := arc.CreateWorkflow(ctx, workflow); err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}
	
	if err := arc.StartWorkflow(ctx, workflow.ID); err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	
	// Wait for the workflow to be running and have agents
	time.Sleep(100 * time.Millisecond)
	
	// Verify that workflow is running and has agents
	arc.mu.RLock()
	exec, exists := arc.workflows[workflow.ID]
	arc.mu.RUnlock()
	
	if !exists {
		t.Fatal("Workflow execution should exist")
	}
	
	if len(exec.agents) == 0 {
		t.Fatal("Workflow should have at least one agent")
	}
	
	// Get the first agent
	var testAgent *types.Agent
	for _, agent := range exec.agents {
		testAgent = agent
		break
	}
	
	if testAgent == nil {
		t.Fatal("Test agent should exist")
	}
	
	// Set agent to running status
	testAgent.Status = types.AgentStatusRunning
	
	// Wait for workflow execution to complete or progress
	time.Sleep(200 * time.Millisecond)
	
	// Get agent status to verify monitoring works
	updatedAgent, err := stateManager.GetAgent(ctx, testAgent.ID)
	if err != nil {
		t.Errorf("Failed to get agent: %v", err)
	}
	
	if updatedAgent == nil {
		t.Error("Agent should exist in state manager")
	}
	
	// Verify agent creation events were recorded
	events, err := stateManager.GetEvents(ctx, map[string]string{
		"type": string(types.EventTypeAgentCreated),
	}, 10)
	if err != nil {
		t.Errorf("Failed to get agent creation events: %v", err)
	}
	
	if len(events) == 0 {
		t.Error("Agent creation events should have been recorded")
	}
}

func TestOrchestrator_AgentStatusTracking(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntimeWithHealth()
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
	
	// Create a test workflow
	workflow := &types.Workflow{
		Name: "status-tracking-test",
		Tasks: []types.Task{
			{
				Name: "tracking-task",
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
	
	// Wait for workflow execution to be set up
	time.Sleep(200 * time.Millisecond)
	
	// Get the workflow execution and agent
	arc.mu.RLock()
	exec, exists := arc.workflows[workflow.ID]
	arc.mu.RUnlock()
	
	if !exists || len(exec.agents) == 0 {
		t.Fatal("Workflow should exist and have agents")
	}
	
	var testAgent *types.Agent
	var taskID string
	for id, agent := range exec.agents {
		testAgent = agent
		taskID = id
		break
	}
	
	// Verify agent was created
	createdAgent, err := stateManager.GetAgent(ctx, testAgent.ID)
	if err != nil {
		t.Errorf("Failed to get created agent: %v", err)
	}
	
	if createdAgent == nil {
		t.Error("Agent should have been created in state manager")
	}
	
	// Verify task was created
	task, err := stateManager.GetTask(ctx, workflow.ID, taskID)
	if err != nil {
		t.Errorf("Failed to get task: %v", err)
	} else if task == nil {
		t.Error("Task should have been created in state manager")
	}
	
	// Verify workflow events were recorded
	events, err := stateManager.GetEvents(ctx, nil, 10)
	if err != nil {
		t.Errorf("Failed to get events: %v", err)
	}
	
	if len(events) == 0 {
		t.Error("Workflow events should have been recorded")
	}
}

func TestOrchestrator_AgentStatusChange(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntimeWithHealth()
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
	
	// Create workflow and start it
	workflow := &types.Workflow{
		Name: "status-change-test",
		Tasks: []types.Task{
			{
				Name: "status-change-task",
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
	
	time.Sleep(100 * time.Millisecond)
	
	// Get workflow execution and agent
	arc.mu.RLock()
	exec, exists := arc.workflows[workflow.ID]
	arc.mu.RUnlock()
	
	if !exists || len(exec.agents) == 0 {
		t.Fatal("Workflow should exist and have agents")
	}
	
	var testAgent *types.Agent
	var taskID string
	for id, agent := range exec.agents {
		testAgent = agent
		taskID = id
		break
	}
	
	// Set initial status
	testAgent.Status = types.AgentStatusRunning
	
	// Change agent status in the runtime mock
	runtime.agents[testAgent.ID].Status = types.AgentStatusCompleted
	runtime.agents[testAgent.ID].CompletedAt = &[]time.Time{time.Now()}[0]
	
	// Trigger health check to detect status change
	arc.checkAgentHealth()
	
	// Wait a bit for status update to propagate
	time.Sleep(50 * time.Millisecond)
	
	// Verify agent status was updated
	updatedAgent, err := stateManager.GetAgent(ctx, testAgent.ID)
	if err != nil {
		t.Errorf("Failed to get updated agent: %v", err)
	} else if updatedAgent.Status != types.AgentStatusCompleted {
		t.Errorf("Agent status should be completed, got: %s", updatedAgent.Status)
	}
	
	// Verify task status was updated
	task, err := stateManager.GetTask(ctx, workflow.ID, taskID)
	if err != nil {
		t.Errorf("Failed to get task: %v", err)
	} else if task.Status != types.TaskStatusCompleted {
		t.Errorf("Task status should be completed, got: %s", task.Status)
	}
	
	// Verify status change event was recorded
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

func TestOrchestrator_TaskRetryLogic(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntimeWithHealth()
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
	
	// Create workflow with retry configuration
	workflow := &types.Workflow{
		Name: "retry-test",
		Tasks: []types.Task{
			{
				Name: "retry-task",
				MaxRetries: 2,
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
	
	time.Sleep(100 * time.Millisecond)
	
	// Get workflow execution
	arc.mu.RLock()
	exec, exists := arc.workflows[workflow.ID]
	arc.mu.RUnlock()
	
	if !exists || len(exec.agents) == 0 {
		t.Fatal("Workflow should exist and have agents")
	}
	
	var testAgent *types.Agent
	var taskID string
	for id, agent := range exec.agents {
		testAgent = agent
		taskID = id
		break
	}
	
	// Set agent to running, then simulate failure
	testAgent.Status = types.AgentStatusRunning
	runtime.agents[testAgent.ID].Status = types.AgentStatusFailed
	runtime.agents[testAgent.ID].Error = "Simulated failure"
	
	// Trigger health check to detect failure
	arc.checkAgentHealth()
	
	// Wait for retry logic to process
	time.Sleep(200 * time.Millisecond)
	
	// Verify retry event was recorded
	events, err := stateManager.GetEvents(ctx, map[string]string{
		"type": string(types.EventTypeTaskRetrying),
	}, 10)
	if err != nil {
		t.Errorf("Failed to get retry events: %v", err)
	}
	
	if len(events) == 0 {
		t.Error("Task retry events should have been recorded")
	}
	
	// Verify task retry count was incremented
	task, err := stateManager.GetTask(ctx, workflow.ID, taskID)
	if err != nil {
		t.Errorf("Failed to get task: %v", err)
	} else if task.RetryCount == 0 {
		t.Error("Task retry count should have been incremented")
	}
}

func TestOrchestrator_ConcurrentHealthChecks(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntimeWithHealth()
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
	
	// Create multiple workflows with agents
	numWorkflows := 5
	var workflows []*types.Workflow
	
	for i := 0; i < numWorkflows; i++ {
		workflow := &types.Workflow{
			Name: fmt.Sprintf("concurrent-test-%d", i),
			Tasks: []types.Task{
				{
					Name: fmt.Sprintf("concurrent-task-%d", i),
					AgentConfig: types.AgentConfig{
						Command: []string{"echo"},
						Args:    []string{fmt.Sprintf("test-%d", i)},
					},
				},
			},
		}
		
		if err := arc.CreateWorkflow(ctx, workflow); err != nil {
			t.Fatalf("Failed to create workflow %d: %v", i, err)
		}
		
		if err := arc.StartWorkflow(ctx, workflow.ID); err != nil {
			t.Fatalf("Failed to start workflow %d: %v", i, err)
		}
		
		workflows = append(workflows, workflow)
	}
	
	// Wait for all workflows to be set up
	time.Sleep(200 * time.Millisecond)
	
	// Verify all workflows have agents
	arc.mu.RLock()
	totalAgents := 0
	for _, exec := range arc.workflows {
		totalAgents += len(exec.agents)
	}
	arc.mu.RUnlock()
	
	if totalAgents != numWorkflows {
		t.Errorf("Expected %d agents, got %d", numWorkflows, totalAgents)
	}
	
	// Reset health check count
	runtime.ResetHealthCheckCount()
	
	// Run concurrent health checks
	var wg sync.WaitGroup
	numConcurrentChecks := 10
	
	for i := 0; i < numConcurrentChecks; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			arc.checkAgentHealth()
		}()
	}
	
	wg.Wait()
	
	// Verify all health checks completed without race conditions
	if runtime.GetHealthCheckCount() == 0 {
		t.Error("Concurrent health checks should have been performed")
	}
	
	// Verify no data races occurred by checking that all agents are still accessible
	arc.mu.RLock()
	defer arc.mu.RUnlock()
	
	for workflowID, exec := range arc.workflows {
		if exec == nil {
			t.Errorf("Workflow execution %s should not be nil", workflowID)
		}
		if exec.agents == nil {
			t.Errorf("Agents map for workflow %s should not be nil", workflowID)
		}
	}
}

func TestOrchestrator_HealthCheckResourceMonitoring(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntimeWithHealth()
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
	
	// Create workflow with resource constraints
	workflow := &types.Workflow{
		Name: "resource-monitoring-test",
		Tasks: []types.Task{
			{
				Name: "resource-task",
				AgentConfig: types.AgentConfig{
					Command: []string{"echo"},
					Args:    []string{"test"},
					Resources: types.ResourceRequirements{
						CPU:    "1000m",
						Memory: "512Mi",
					},
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
	
	time.Sleep(100 * time.Millisecond)
	
	// Get the agent
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
	
	// Set agent to running with resource constraints
	testAgent.Status = types.AgentStatusRunning
	
	// Trigger health check
	arc.checkAgentHealth()
	
	// Verify resource monitoring events were recorded
	events, err := stateManager.GetEvents(ctx, map[string]string{
		"type": string(types.EventTypeAgentHealthChecked),
	}, 10)
	if err != nil {
		t.Errorf("Failed to get health check events: %v", err)
	}
	
	if len(events) == 0 {
		t.Error("Health check events should have been recorded for resource monitoring")
	}
	
	// Check that the event contains resource-related data
	found := false
	for _, event := range events {
		if agentID, ok := event.Data["agent_id"].(string); ok && agentID == testAgent.ID {
			found = true
			break
		}
	}
	
	if !found {
		t.Error("Health check event should contain agent resource information")
	}
}