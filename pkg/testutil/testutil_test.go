package testutil

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/rizome-dev/arc/pkg/types"
)

func TestMockRuntime(t *testing.T) {
	runtime := NewMockRuntime()
	
	if runtime == nil {
		t.Fatal("Mock runtime should not be nil")
	}
	
	if runtime.GetAgentCount() != 0 {
		t.Error("New mock runtime should have 0 agents")
	}
	
	// Test creating an agent
	agent := &types.Agent{
		ID:   "test-agent",
		Name: "Test Agent",
	}
	
	ctx := context.Background()
	if err := runtime.CreateAgent(ctx, agent); err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}
	
	if agent.Status != types.AgentStatusCreating {
		t.Errorf("Expected agent status %s, got %s", types.AgentStatusCreating, agent.Status)
	}
	
	if agent.ContainerID == "" {
		t.Error("Container ID should be set")
	}
	
	if runtime.GetAgentCount() != 1 {
		t.Error("Runtime should have 1 agent after creation")
	}
	
	// Test starting the agent
	if err := runtime.StartAgent(ctx, agent.ID); err != nil {
		t.Fatalf("Failed to start agent: %v", err)
	}
	
	if agent.Status != types.AgentStatusRunning {
		t.Errorf("Expected agent status %s, got %s", types.AgentStatusRunning, agent.Status)
	}
	
	if agent.StartedAt == nil {
		t.Error("Started time should be set")
	}
	
	// Test getting agent status
	retrievedAgent, err := runtime.GetAgentStatus(ctx, agent.ID)
	if err != nil {
		t.Fatalf("Failed to get agent status: %v", err)
	}
	
	if retrievedAgent.ID != agent.ID {
		t.Errorf("Expected agent ID %s, got %s", agent.ID, retrievedAgent.ID)
	}
	
	// Test listing agents
	agents, err := runtime.ListAgents(ctx)
	if err != nil {
		t.Fatalf("Failed to list agents: %v", err)
	}
	
	if len(agents) != 1 {
		t.Errorf("Expected 1 agent, got %d", len(agents))
	}
	
	// Test stopping the agent
	if err := runtime.StopAgent(ctx, agent.ID); err != nil {
		t.Fatalf("Failed to stop agent: %v", err)
	}
	
	if agent.Status != types.AgentStatusCompleted {
		t.Errorf("Expected agent status %s, got %s", types.AgentStatusCompleted, agent.Status)
	}
	
	if agent.CompletedAt == nil {
		t.Error("Completed time should be set")
	}
	
	// Test destroying the agent
	if err := runtime.DestroyAgent(ctx, agent.ID); err != nil {
		t.Fatalf("Failed to destroy agent: %v", err)
	}
	
	if runtime.GetAgentCount() != 0 {
		t.Error("Runtime should have 0 agents after destruction")
	}
}

func TestMockRuntime_ErrorSimulation(t *testing.T) {
	runtime := NewMockRuntime()
	
	// Test create error
	runtime.SetCreateError(fmt.Errorf("create error"))
	
	agent := &types.Agent{ID: "test", Name: "Test"}
	ctx := context.Background()
	
	if err := runtime.CreateAgent(ctx, agent); err == nil {
		t.Error("Expected create error")
	}
	
	// Reset error and create agent
	runtime.SetCreateError(nil)
	if err := runtime.CreateAgent(ctx, agent); err != nil {
		t.Fatalf("Failed to create agent: %v", err)
	}
	
	// Test start error
	runtime.SetStartError(fmt.Errorf("start error"))
	if err := runtime.StartAgent(ctx, agent.ID); err == nil {
		t.Error("Expected start error")
	}
	
	// Reset error
	runtime.SetStartError(nil)
	if err := runtime.StartAgent(ctx, agent.ID); err != nil {
		t.Fatalf("Failed to start agent: %v", err)
	}
	
	// Test stop error
	runtime.SetStopError(fmt.Errorf("stop error"))
	if err := runtime.StopAgent(ctx, agent.ID); err == nil {
		t.Error("Expected stop error")
	}
}

func TestMockMessageQueue(t *testing.T) {
	mq := NewMockMessageQueue()
	
	if mq == nil {
		t.Fatal("Mock message queue should not be nil")
	}
	
	if mq.GetQueueCount() != 0 {
		t.Error("New mock message queue should have 0 queues")
	}
	
	ctx := context.Background()
	
	// Test creating a queue
	queueName := "test-queue"
	if err := mq.CreateQueue(ctx, queueName); err != nil {
		t.Fatalf("Failed to create queue: %v", err)
	}
	
	if mq.GetQueueCount() != 1 {
		t.Error("Message queue should have 1 queue after creation")
	}
	
	// Test listing queues
	queues, err := mq.ListQueues(ctx)
	if err != nil {
		t.Fatalf("Failed to list queues: %v", err)
	}
	
	if len(queues) != 1 {
		t.Errorf("Expected 1 queue, got %d", len(queues))
	}
	
	if queues[0].Name != queueName {
		t.Errorf("Expected queue name %s, got %s", queueName, queues[0].Name)
	}
	
	// Test sending a message
	message := CreateTestMessage("msg-1", "sender", queueName, "test", map[string]interface{}{
		"data": "test data",
	})
	
	if err := mq.SendMessage(ctx, "sender", queueName, message); err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}
	
	// Test getting messages
	messages := mq.GetMessages(queueName)
	if len(messages) != 1 {
		t.Errorf("Expected 1 message, got %d", len(messages))
	}
	
	if messages[0].ID != "msg-1" {
		t.Errorf("Expected message ID 'msg-1', got %s", messages[0].ID)
	}
	
	// Test publishing a task
	taskMessage := CreateTestMessage("task-1", "publisher", "task-topic", "task", map[string]interface{}{
		"command": "execute",
	})
	
	if err := mq.PublishTask(ctx, "publisher", "task-topic", taskMessage); err != nil {
		t.Fatalf("Failed to publish task: %v", err)
	}
	
	taskMessages := mq.GetMessages("task-topic")
	if len(taskMessages) != 1 {
		t.Errorf("Expected 1 task message, got %d", len(taskMessages))
	}
	
	// Test queue stats
	stats, err := mq.GetQueueStats(ctx, queueName)
	if err != nil {
		t.Fatalf("Failed to get queue stats: %v", err)
	}
	
	if stats.MessageCount != 1 {
		t.Errorf("Expected 1 total message, got %d", stats.MessageCount)
	}
	
	// Test clearing messages
	mq.ClearMessages()
	clearedMessages := mq.GetMessages(queueName)
	if len(clearedMessages) != 0 {
		t.Errorf("Expected 0 messages after clear, got %d", len(clearedMessages))
	}
	
	// Test deleting queue
	if err := mq.DeleteQueue(ctx, queueName); err != nil {
		t.Fatalf("Failed to delete queue: %v", err)
	}
	
	if mq.GetQueueCount() != 0 {
		t.Error("Message queue should have 0 queues after deletion")
	}
}

func TestCreateTestEnvironment(t *testing.T) {
	env, err := CreateTestEnvironment()
	if err != nil {
		t.Fatalf("Failed to create test environment: %v", err)
	}
	
	if env.Orchestrator == nil {
		t.Error("Test environment should have orchestrator")
	}
	
	if env.Runtime == nil {
		t.Error("Test environment should have runtime")
	}
	
	if env.StateManager == nil {
		t.Error("Test environment should have state manager")
	}
	
	if env.MessageQueue == nil {
		t.Error("Test environment should have message queue")
	}
	
	if env.Config == nil {
		t.Error("Test environment should have config")
	}
	
	// Test starting and stopping
	if err := env.Start(); err != nil {
		t.Fatalf("Failed to start test environment: %v", err)
	}
	
	if err := env.Stop(); err != nil {
		t.Fatalf("Failed to stop test environment: %v", err)
	}
}

func TestWaitForCondition(t *testing.T) {
	// Test condition that becomes true
	counter := 0
	condition := func() bool {
		counter++
		return counter >= 3
	}
	
	if !WaitForCondition(condition, 100*time.Millisecond, 10*time.Millisecond) {
		t.Error("Condition should have become true")
	}
	
	// Test condition that never becomes true
	alwaysFalse := func() bool {
		return false
	}
	
	start := time.Now()
	if WaitForCondition(alwaysFalse, 50*time.Millisecond, 10*time.Millisecond) {
		t.Error("Condition should not have become true")
	}
	
	duration := time.Since(start)
	if duration < 45*time.Millisecond {
		t.Errorf("Should have waited at least 45ms, waited %v", duration)
	}
}

func TestCreateTestWorkflow(t *testing.T) {
	workflow := CreateTestWorkflow("test-workflow", 3)
	
	if workflow == nil {
		t.Fatal("Workflow should not be nil")
	}
	
	if workflow.Name != "test-workflow" {
		t.Errorf("Expected workflow name 'test-workflow', got %s", workflow.Name)
	}
	
	if len(workflow.Tasks) != 3 {
		t.Errorf("Expected 3 tasks, got %d", len(workflow.Tasks))
	}
	
	if workflow.Status != types.WorkflowStatusPending {
		t.Errorf("Expected status %s, got %s", types.WorkflowStatusPending, workflow.Status)
	}
	
	// Check task dependencies
	if len(workflow.Tasks[0].Dependencies) != 0 {
		t.Error("First task should have no dependencies")
	}
	
	if len(workflow.Tasks[1].Dependencies) != 1 {
		t.Error("Second task should have 1 dependency")
	}
	
	if len(workflow.Tasks[2].Dependencies) != 1 {
		t.Error("Third task should have 1 dependency")
	}
}

func TestCreateTestAgent(t *testing.T) {
	agent := CreateTestAgent("test-agent", "ubuntu:latest")
	
	if agent == nil {
		t.Fatal("Agent should not be nil")
	}
	
	if agent.Name != "test-agent" {
		t.Errorf("Expected agent name 'test-agent', got %s", agent.Name)
	}
	
	if agent.Image != "ubuntu:latest" {
		t.Errorf("Expected agent image 'ubuntu:latest', got %s", agent.Image)
	}
	
	if agent.Status != types.AgentStatusPending {
		t.Errorf("Expected status %s, got %s", types.AgentStatusPending, agent.Status)
	}
	
	if agent.Config.Image != "ubuntu:latest" {
		t.Errorf("Expected config image 'ubuntu:latest', got %s", agent.Config.Image)
	}
}

func TestCreateTestMessage(t *testing.T) {
	payload := map[string]interface{}{
		"command": "test",
		"data":    "test data",
	}
	
	message := CreateTestMessage("msg-1", "sender", "receiver", "task", payload)
	
	if message == nil {
		t.Fatal("Message should not be nil")
	}
	
	if message.ID != "msg-1" {
		t.Errorf("Expected message ID 'msg-1', got %s", message.ID)
	}
	
	if message.From != "sender" {
		t.Errorf("Expected from 'sender', got %s", message.From)
	}
	
	if message.To != "receiver" {
		t.Errorf("Expected to 'receiver', got %s", message.To)
	}
	
	if message.Type != "task" {
		t.Errorf("Expected type 'task', got %s", message.Type)
	}
	
	if message.Payload["command"] != "test" {
		t.Error("Payload should contain correct command")
	}
	
	if message.Timestamp == 0 {
		t.Error("Timestamp should not be zero")
	}
}

func TestAssertEventually(t *testing.T) {
	// Test successful condition
	counter := 0
	condition := func() bool {
		counter++
		return counter >= 2
	}
	
	if err := AssertEventually(condition, 100*time.Millisecond, "counter should reach 2"); err != nil {
		t.Errorf("AssertEventually should have succeeded: %v", err)
	}
	
	// Test failing condition
	alwaysFalse := func() bool {
		return false
	}
	
	if err := AssertEventually(alwaysFalse, 50*time.Millisecond, "should always fail"); err == nil {
		t.Error("AssertEventually should have failed")
	}
}

func TestGetFreePort(t *testing.T) {
	port, err := GetFreePort()
	if err != nil {
		t.Fatalf("Failed to get free port: %v", err)
	}
	
	if port <= 0 || port > 65535 {
		t.Errorf("Invalid port number: %d", port)
	}
	
	// Get another port, should be different
	port2, err := GetFreePort()
	if err != nil {
		t.Fatalf("Failed to get second free port: %v", err)
	}
	
	if port == port2 {
		t.Error("Two consecutive calls should return different ports")
	}
}

func TestTestConfig(t *testing.T) {
	config := TestConfig()
	
	if config == nil {
		t.Fatal("Test config should not be nil")
	}
	
	// Check some default values
	if config.Server.GRPC.Host != "127.0.0.1" {
		t.Errorf("Expected gRPC host '127.0.0.1', got %s", config.Server.GRPC.Host)
	}
	
	if config.Server.GRPC.Port != 0 {
		t.Error("Test config should use dynamic port (0)")
	}
	
	if config.Runtime.Type != "mock" {
		t.Errorf("Expected runtime type 'mock', got %s", config.Runtime.Type)
	}
	
	if config.State.Type != "memory" {
		t.Errorf("Expected state type 'memory', got %s", config.State.Type)
	}
	
	if config.Monitoring.Metrics.Enabled {
		t.Error("Metrics should be disabled in test config by default")
	}
	
	if !config.Monitoring.HealthChecks.Enabled {
		t.Error("Health checks should be enabled in test config")
	}
}

func TestMockHelpers(t *testing.T) {
	env, err := CreateTestEnvironment()
	if err != nil {
		t.Fatalf("Failed to create test environment: %v", err)
	}
	
	// Test getting mock runtime
	mockRuntime := env.GetMockRuntime()
	if mockRuntime == nil {
		t.Error("Should be able to get mock runtime from environment")
	}
	
	// Test getting mock message queue
	mockMQ := env.GetMockMessageQueue()
	if mockMQ == nil {
		t.Error("Should be able to get mock message queue from environment")
	}
	
	// Verify they're the same instances
	if mockRuntime != env.Runtime {
		t.Error("Mock runtime should be the same instance")
	}
	
	if mockMQ != env.MessageQueue {
		t.Error("Mock message queue should be the same instance")
	}
}