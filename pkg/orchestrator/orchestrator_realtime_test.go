package orchestrator

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/rizome-dev/arc/pkg/messagequeue"
	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
)

// MockRealTimeMessageQueue extends MockMessageQueue with real-time capabilities
type MockRealTimeMessageQueue struct {
	*MockMessageQueue
	subscribers map[string][]chan *messagequeue.Message
	mu          sync.RWMutex
}

func NewMockRealTimeMessageQueue() *MockRealTimeMessageQueue {
	return &MockRealTimeMessageQueue{
		MockMessageQueue: NewMockMessageQueue(),
		subscribers:      make(map[string][]chan *messagequeue.Message),
	}
}

func (mq *MockRealTimeMessageQueue) Subscribe(topic string) chan *messagequeue.Message {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	
	ch := make(chan *messagequeue.Message, 100)
	mq.subscribers[topic] = append(mq.subscribers[topic], ch)
	return ch
}

func (mq *MockRealTimeMessageQueue) PublishTask(ctx context.Context, from, topic string, message *messagequeue.Message) error {
	mq.mu.RLock()
	defer mq.mu.RUnlock()
	
	// Send to all subscribers of this topic
	if subscribers, exists := mq.subscribers[topic]; exists {
		for _, ch := range subscribers {
			select {
			case ch <- message:
			default:
				// Channel is full, skip
			}
		}
	}
	
	return mq.MockMessageQueue.PublishTask(ctx, from, topic, message)
}

func (mq *MockRealTimeMessageQueue) Close() error {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	
	// Close all subscriber channels
	for _, subscribers := range mq.subscribers {
		for _, ch := range subscribers {
			close(ch)
		}
	}
	mq.subscribers = make(map[string][]chan *messagequeue.Message)
	
	return mq.MockMessageQueue.Close()
}

func TestOrchestrator_CreateRealTimeAgent(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntime()
	stateManager := state.NewMemoryStore()
	messageQueue := NewMockRealTimeMessageQueue()
	
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: messageQueue,
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create real-time agent configuration
	agent := &types.Agent{
		Name:   "test-realtime-agent",
		Image:  "ubuntu:latest",
		Status: types.AgentStatusPending,
		Config: types.AgentConfig{
			Command:     []string{"bash", "-c", "while true; do sleep 1; done"},
			Environment: map[string]string{"TEST_VAR": "test_value"},
			MessageQueue: types.MessageQueueConfig{
				Topics: []string{"test-topic", "control-topic"},
			},
		},
	}
	
	config := RealTimeAgentConfig{
		Agent:  agent,
		Topics: []string{"test-topic", "control-topic"},
	}
	
	// Create real-time agent
	if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
		t.Fatalf("Failed to create real-time agent: %v", err)
	}
	
	// Verify agent was created
	if agent.ID == "" {
		t.Error("Agent ID should be set")
	}
	
	if agent.Status != types.AgentStatusCreating {
		t.Errorf("Expected status %s, got %s", types.AgentStatusCreating, agent.Status)
	}
	
	// Verify agent exists in state manager
	storedAgent, err := stateManager.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Errorf("Failed to get agent from state: %v", err)
	}
	if storedAgent == nil {
		t.Error("Agent should exist in state manager")
	}
	
	// Verify agent is tracked in orchestrator
	arc.mu.RLock()
	_, exists := arc.realTimeAgents[agent.ID]
	arc.mu.RUnlock()
	
	if !exists {
		t.Error("Agent should be tracked in real-time agents map")
	}
	
	// Verify queues were created for agent topics
	for _, topic := range config.Topics {
		if !messageQueue.queues[topic] {
			t.Errorf("Queue for topic %s should have been created", topic)
		}
	}
}

func TestOrchestrator_ListRealTimeAgents(t *testing.T) {
	ctx := context.Background()
	arc, err := New(Config{
		Runtime:      NewMockRuntime(),
		MessageQueue: NewMockRealTimeMessageQueue(),
		StateManager: state.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create multiple real-time agents
	numAgents := 3
	var createdAgents []*types.Agent
	
	for i := 0; i < numAgents; i++ {
		agent := &types.Agent{
			Name:   fmt.Sprintf("test-agent-%d", i),
			Image:  "ubuntu:latest",
			Status: types.AgentStatusPending,
			Config: types.AgentConfig{
				Command: []string{"echo", fmt.Sprintf("test-%d", i)},
				MessageQueue: types.MessageQueueConfig{
					Topics: []string{fmt.Sprintf("topic-%d", i)},
				},
			},
		}
		
		config := RealTimeAgentConfig{
			Agent:  agent,
			Topics: []string{fmt.Sprintf("topic-%d", i)},
		}
		
		if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
			t.Fatalf("Failed to create agent %d: %v", i, err)
		}
		
		createdAgents = append(createdAgents, agent)
	}
	
	// List real-time agents
	agents, err := arc.ListRealTimeAgents(ctx)
	if err != nil {
		t.Fatalf("Failed to list real-time agents: %v", err)
	}
	
	if len(agents) != numAgents {
		t.Errorf("Expected %d agents, got %d", numAgents, len(agents))
	}
	
	// Verify all created agents are in the list
	agentMap := make(map[string]*types.Agent)
	for _, agent := range agents {
		agentMap[agent.ID] = agent
	}
	
	for _, createdAgent := range createdAgents {
		if _, exists := agentMap[createdAgent.ID]; !exists {
			t.Errorf("Agent %s should be in the list", createdAgent.ID)
		}
	}
}

func TestOrchestrator_GetRealTimeAgent(t *testing.T) {
	ctx := context.Background()
	arc, err := New(Config{
		Runtime:      NewMockRuntime(),
		MessageQueue: NewMockRealTimeMessageQueue(),
		StateManager: state.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create a real-time agent
	agent := &types.Agent{
		Name:   "get-test-agent",
		Image:  "ubuntu:latest",
		Status: types.AgentStatusPending,
		Config: types.AgentConfig{
			Command: []string{"echo", "get test"},
			MessageQueue: types.MessageQueueConfig{
				Topics: []string{"get-test-topic"},
			},
		},
	}
	
	config := RealTimeAgentConfig{
		Agent:  agent,
		Topics: []string{"get-test-topic"},
	}
	
	if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}
	
	// Get the agent
	retrievedAgent, err := arc.GetRealTimeAgent(ctx, agent.ID)
	if err != nil {
		t.Fatalf("Failed to get agent: %v", err)
	}
	
	if retrievedAgent == nil {
		t.Fatal("Retrieved agent should not be nil")
	}
	
	if retrievedAgent.ID != agent.ID {
		t.Errorf("Expected agent ID %s, got %s", agent.ID, retrievedAgent.ID)
	}
	
	if retrievedAgent.Name != agent.Name {
		t.Errorf("Expected agent name %s, got %s", agent.Name, retrievedAgent.Name)
	}
	
	// Test getting non-existent agent
	_, err = arc.GetRealTimeAgent(ctx, "non-existent-id")
	if err == nil {
		t.Error("Expected error when getting non-existent agent")
	}
}

func TestOrchestrator_StopRealTimeAgent(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntime()
	messageQueue := NewMockRealTimeMessageQueue()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: messageQueue,
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create a real-time agent
	agent := &types.Agent{
		Name:   "stop-test-agent",
		Image:  "ubuntu:latest",
		Status: types.AgentStatusPending,
		Config: types.AgentConfig{
			Command: []string{"echo", "stop test"},
			MessageQueue: types.MessageQueueConfig{
				Topics: []string{"stop-test-topic"},
			},
		},
	}
	
	config := RealTimeAgentConfig{
		Agent:  agent,
		Topics: []string{"stop-test-topic"},
	}
	
	if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}
	
	// Start the agent (simulate runtime starting it)
	runtime.agents[agent.ID].Status = types.AgentStatusRunning
	agent.Status = types.AgentStatusRunning
	
	// Stop the agent
	if err := arc.StopRealTimeAgent(ctx, agent.ID); err != nil {
		t.Fatalf("Failed to stop agent: %v", err)
	}
	
	// Verify agent status was updated
	retrievedAgent, err := stateManager.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Errorf("Failed to get agent from state: %v", err)
	}
	if retrievedAgent.Status != types.AgentStatusCompleted {
		t.Errorf("Expected agent status %s, got %s", types.AgentStatusCompleted, retrievedAgent.Status)
	}
	
	// Verify agent was removed from real-time agents map
	arc.mu.RLock()
	_, exists := arc.realTimeAgents[agent.ID]
	arc.mu.RUnlock()
	
	if exists {
		t.Error("Agent should be removed from real-time agents map")
	}
	
	// Test stopping non-existent agent
	err = arc.StopRealTimeAgent(ctx, "non-existent-id")
	if err == nil {
		t.Error("Expected error when stopping non-existent agent")
	}
}

func TestOrchestrator_RealTimeAgentMessageHandling(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntime()
	messageQueue := NewMockRealTimeMessageQueue()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: messageQueue,
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create a real-time agent
	agent := &types.Agent{
		Name:   "message-test-agent",
		Image:  "ubuntu:latest",
		Status: types.AgentStatusPending,
		Config: types.AgentConfig{
			Command: []string{"echo", "message test"},
			MessageQueue: types.MessageQueueConfig{
				Topics: []string{"message-test-topic"},
			},
		},
	}
	
	config := RealTimeAgentConfig{
		Agent:  agent,
		Topics: []string{"message-test-topic"},
	}
	
	if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}
	
	// Subscribe to the topic to receive messages
	msgChan := messageQueue.Subscribe("message-test-topic")
	
	// Publish a test message to the topic
	testMessage := &messagequeue.Message{
		ID:      "test-msg-1",
		From:    "test-sender",
		To:      agent.ID,
		Topic:   "message-test-topic",
		Type:    "task",
		Payload: map[string]interface{}{"command": "hello", "data": "world"},
	}
	
	if err := messageQueue.PublishTask(ctx, "test-sender", "message-test-topic", testMessage); err != nil {
		t.Fatalf("Failed to publish test message: %v", err)
	}
	
	// Wait for message to be received
	select {
	case receivedMsg := <-msgChan:
		if receivedMsg.ID != testMessage.ID {
			t.Errorf("Expected message ID %s, got %s", testMessage.ID, receivedMsg.ID)
		}
		if receivedMsg.Topic != testMessage.Topic {
			t.Errorf("Expected topic %s, got %s", testMessage.Topic, receivedMsg.Topic)
		}
	case <-time.After(1 * time.Second):
		t.Error("Timeout waiting for message")
	}
}

func TestOrchestrator_RealTimeAgentHealthMonitoring(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntimeWithHealth()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: NewMockRealTimeMessageQueue(),
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create a real-time agent
	agent := &types.Agent{
		Name:   "health-test-agent",
		Image:  "ubuntu:latest",
		Status: types.AgentStatusPending,
		Config: types.AgentConfig{
			Command: []string{"echo", "health test"},
			MessageQueue: types.MessageQueueConfig{
				Topics: []string{"health-test-topic"},
			},
		},
	}
	
	config := RealTimeAgentConfig{
		Agent:  agent,
		Topics: []string{"health-test-topic"},
	}
	
	if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}
	
	// Set agent to running
	runtime.agents[agent.ID].Status = types.AgentStatusRunning
	agent.Status = types.AgentStatusRunning
	
	// Trigger health check
	arc.checkAgentHealth()
	
	// Wait for health check to complete
	time.Sleep(50 * time.Millisecond)
	
	// Verify health check was performed
	if runtime.GetHealthCheckCount() == 0 {
		t.Error("Health check should have been performed")
	}
	
	// Verify health check events were recorded
	events, err := stateManager.GetEvents(ctx, map[string]string{
		"type": string(types.EventTypeAgentHealthChecked),
	}, 10)
	if err != nil {
		t.Errorf("Failed to get health check events: %v", err)
	}
	
	if len(events) == 0 {
		t.Error("Health check events should have been recorded")
	}
}

func TestOrchestrator_ConcurrentRealTimeAgents(t *testing.T) {
	ctx := context.Background()
	arc, err := New(Config{
		Runtime:      NewMockRuntime(),
		MessageQueue: NewMockRealTimeMessageQueue(),
		StateManager: state.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create multiple agents concurrently
	numAgents := 10
	var wg sync.WaitGroup
	errors := make(chan error, numAgents)
	
	for i := 0; i < numAgents; i++ {
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			
			agent := &types.Agent{
				Name:   fmt.Sprintf("concurrent-agent-%d", index),
				Image:  "ubuntu:latest",
				Status: types.AgentStatusPending,
				Config: types.AgentConfig{
					Command: []string{"echo", fmt.Sprintf("concurrent-%d", index)},
					MessageQueue: types.MessageQueueConfig{
						Topics: []string{fmt.Sprintf("concurrent-topic-%d", index)},
					},
				},
			}
			
			config := RealTimeAgentConfig{
				Agent:  agent,
				Topics: []string{fmt.Sprintf("concurrent-topic-%d", index)},
			}
			
			if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
				errors <- fmt.Errorf("failed to create agent %d: %w", index, err)
				return
			}
		}(i)
	}
	
	wg.Wait()
	close(errors)
	
	// Check for errors
	for err := range errors {
		t.Error(err)
	}
	
	// Verify all agents were created
	agents, err := arc.ListRealTimeAgents(ctx)
	if err != nil {
		t.Fatalf("Failed to list agents: %v", err)
	}
	
	if len(agents) != numAgents {
		t.Errorf("Expected %d agents, got %d", numAgents, len(agents))
	}
	
	// Verify no data races in concurrent access
	arc.mu.RLock()
	realTimeAgentCount := len(arc.realTimeAgents)
	arc.mu.RUnlock()
	
	if realTimeAgentCount != numAgents {
		t.Errorf("Expected %d real-time agents in map, got %d", numAgents, realTimeAgentCount)
	}
}

func TestOrchestrator_RealTimeAgentResourceConstraints(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntime()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: NewMockRealTimeMessageQueue(),
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create agent with resource constraints
	agent := &types.Agent{
		Name:   "resource-constrained-agent",
		Image:  "ubuntu:latest",
		Status: types.AgentStatusPending,
		Config: types.AgentConfig{
			Command: []string{"echo", "resource test"},
			Resources: types.ResourceRequirements{
				CPU:    "500m",
				Memory: "256Mi",
				GPU:    "1",
			},
			MessageQueue: types.MessageQueueConfig{
				Topics: []string{"resource-topic"},
			},
		},
	}
	
	config := RealTimeAgentConfig{
		Agent:  agent,
		Topics: []string{"resource-topic"},
	}
	
	if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
		t.Fatalf("Failed to create agent with resources: %v", err)
	}
	
	// Verify agent was created with correct resource constraints
	createdAgent := runtime.agents[agent.ID]
	if createdAgent.Config.Resources.CPU != "500m" {
		t.Errorf("Expected CPU %s, got %s", "500m", createdAgent.Config.Resources.CPU)
	}
	if createdAgent.Config.Resources.Memory != "256Mi" {
		t.Errorf("Expected Memory %s, got %s", "256Mi", createdAgent.Config.Resources.Memory)
	}
	if createdAgent.Config.Resources.GPU != "1" {
		t.Errorf("Expected GPU %s, got %s", "1", createdAgent.Config.Resources.GPU)
	}
	
	// Verify resource constraints are persisted in state
	storedAgent, err := stateManager.GetAgent(ctx, agent.ID)
	if err != nil {
		t.Errorf("Failed to get agent: %v", err)
	}
	if storedAgent.Config.Resources.CPU != "500m" {
		t.Errorf("Resource constraints should be persisted in state")
	}
}

func TestOrchestrator_RealTimeAgentEnvironmentVariables(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntime()
	
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: NewMockRealTimeMessageQueue(),
		StateManager: state.NewMemoryStore(),
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Create agent with environment variables
	envVars := map[string]string{
		"API_KEY":     "test-api-key",
		"DEBUG_MODE":  "true",
		"SERVICE_URL": "https://example.com/api",
	}
	
	agent := &types.Agent{
		Name:   "env-test-agent",
		Image:  "ubuntu:latest",
		Status: types.AgentStatusPending,
		Config: types.AgentConfig{
			Command:     []string{"env"},
			Environment: envVars,
			MessageQueue: types.MessageQueueConfig{
				Topics: []string{"env-topic"},
			},
		},
	}
	
	config := RealTimeAgentConfig{
		Agent:  agent,
		Topics: []string{"env-topic"},
	}
	
	if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
		t.Fatalf("Failed to create agent with environment: %v", err)
	}
	
	// Verify environment variables were set
	createdAgent := runtime.agents[agent.ID]
	for key, expectedValue := range envVars {
		if actualValue, exists := createdAgent.Config.Environment[key]; !exists {
			t.Errorf("Environment variable %s should exist", key)
		} else if actualValue != expectedValue {
			t.Errorf("Expected env var %s=%s, got %s", key, expectedValue, actualValue)
		}
	}
}

func TestOrchestrator_RealTimeAgentGracefulShutdown(t *testing.T) {
	ctx := context.Background()
	runtime := NewMockRuntime()
	messageQueue := NewMockRealTimeMessageQueue()
	stateManager := state.NewMemoryStore()
	
	arc, err := New(Config{
		Runtime:      runtime,
		MessageQueue: messageQueue,
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	if err := arc.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	
	// Create multiple real-time agents
	numAgents := 5
	for i := 0; i < numAgents; i++ {
		agent := &types.Agent{
			Name:   fmt.Sprintf("shutdown-agent-%d", i),
			Image:  "ubuntu:latest",
			Status: types.AgentStatusPending,
			Config: types.AgentConfig{
				Command: []string{"sleep", "3600"}, // Long running
				MessageQueue: types.MessageQueueConfig{
					Topics: []string{fmt.Sprintf("shutdown-topic-%d", i)},
				},
			},
		}
		
		config := RealTimeAgentConfig{
			Agent:  agent,
			Topics: []string{fmt.Sprintf("shutdown-topic-%d", i)},
		}
		
		if err := arc.CreateRealTimeAgent(ctx, config); err != nil {
			t.Fatalf("Failed to create agent %d: %v", i, err)
		}
		
		// Set agents to running
		runtime.agents[agent.ID].Status = types.AgentStatusRunning
	}
	
	// Wait for agents to be set up
	time.Sleep(100 * time.Millisecond)
	
	// Verify all agents are running
	arc.mu.RLock()
	initialCount := len(arc.realTimeAgents)
	arc.mu.RUnlock()
	
	if initialCount != numAgents {
		t.Errorf("Expected %d running agents, got %d", numAgents, initialCount)
	}
	
	// Gracefully stop the orchestrator
	if err := arc.Stop(); err != nil {
		t.Fatalf("Failed to stop orchestrator: %v", err)
	}
	
	// Verify all agents were stopped
	arc.mu.RLock()
	finalCount := len(arc.realTimeAgents)
	arc.mu.RUnlock()
	
	if finalCount != 0 {
		t.Errorf("Expected 0 agents after shutdown, got %d", finalCount)
	}
	
	// Verify runtime cleanup was called for all agents
	for agentID := range runtime.agents {
		agent := runtime.agents[agentID]
		if agent.Status != types.AgentStatusCompleted && agent.Status != types.AgentStatusFailed {
			t.Errorf("Agent %s should have been stopped, status: %s", agentID, agent.Status)
		}
	}
}