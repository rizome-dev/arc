package integration

import (
	"context"
	"fmt"
	"io"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rizome-dev/amq/pkg/client"
	amqtypes "github.com/rizome-dev/amq/pkg/types"
	arcv1 "github.com/rizome-dev/arc/api/v1"
	"github.com/rizome-dev/arc/pkg/config"
	"github.com/rizome-dev/arc/pkg/messagequeue"
	"github.com/rizome-dev/arc/pkg/monitoring"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"github.com/rizome-dev/arc/pkg/server"
	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
)

// IntegrationTestSuite holds the test environment
type IntegrationTestSuite struct {
	server       *server.Server
	orchestrator *orchestrator.Orchestrator
	grpcClient   arcv1.ARCClient
	grpcConn     *grpc.ClientConn
	monitor      *monitoring.Monitor
	config       *config.Config
}

// MockRuntime implements runtime.Runtime for integration testing
type MockIntegrationRuntime struct {
	agents     map[string]*types.Agent
	containers map[string]string // agent ID -> container ID
}

func NewMockIntegrationRuntime() *MockIntegrationRuntime {
	return &MockIntegrationRuntime{
		agents:     make(map[string]*types.Agent),
		containers: make(map[string]string),
	}
}

func (r *MockIntegrationRuntime) CreateAgent(ctx context.Context, agent *types.Agent) error {
	agent.Status = types.AgentStatusCreating
	containerID := fmt.Sprintf("container-%s", agent.ID)
	agent.ContainerID = containerID
	r.agents[agent.ID] = agent
	r.containers[agent.ID] = containerID
	
	// Simulate container creation time
	time.Sleep(10 * time.Millisecond)
	return nil
}

func (r *MockIntegrationRuntime) StartAgent(ctx context.Context, agentID string) error {
	if agent, ok := r.agents[agentID]; ok {
		agent.Status = types.AgentStatusRunning
		now := time.Now()
		agent.StartedAt = &now
		
		// Simulate startup time
		time.Sleep(10 * time.Millisecond)
	}
	return nil
}

func (r *MockIntegrationRuntime) StopAgent(ctx context.Context, agentID string) error {
	if agent, ok := r.agents[agentID]; ok {
		agent.Status = types.AgentStatusCompleted
		now := time.Now()
		agent.CompletedAt = &now
	}
	return nil
}

func (r *MockIntegrationRuntime) DestroyAgent(ctx context.Context, agentID string) error {
	delete(r.agents, agentID)
	delete(r.containers, agentID)
	return nil
}

func (r *MockIntegrationRuntime) GetAgentStatus(ctx context.Context, agentID string) (*types.Agent, error) {
	if agent, ok := r.agents[agentID]; ok {
		return agent, nil
	}
	return nil, fmt.Errorf("agent not found")
}

func (r *MockIntegrationRuntime) ListAgents(ctx context.Context) ([]*types.Agent, error) {
	agents := make([]*types.Agent, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, agent)
	}
	return agents, nil
}

func (r *MockIntegrationRuntime) StreamLogs(ctx context.Context, agentID string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

func (r *MockIntegrationRuntime) ExecCommand(ctx context.Context, agentID string, cmd []string) (io.ReadCloser, error) {
	return nil, fmt.Errorf("not implemented")
}

// MockMessageQueue implements messagequeue.MessageQueue for integration testing
type MockIntegrationMessageQueue struct {
	queues   map[string]bool
	messages map[string][]*messagequeue.Message
}

func NewMockIntegrationMessageQueue() *MockIntegrationMessageQueue {
	return &MockIntegrationMessageQueue{
		queues:   make(map[string]bool),
		messages: make(map[string][]*messagequeue.Message),
	}
}

func (mq *MockIntegrationMessageQueue) GetClient(agentID string, metadata map[string]string) (client.Client, error) {
	return nil, nil
}

func (mq *MockIntegrationMessageQueue) GetAsyncConsumer(agentID string, metadata map[string]string) (client.AsyncConsumer, error) {
	return nil, nil
}

func (mq *MockIntegrationMessageQueue) CreateQueue(ctx context.Context, name string) error {
	mq.queues[name] = true
	return nil
}

func (mq *MockIntegrationMessageQueue) DeleteQueue(ctx context.Context, name string) error {
	delete(mq.queues, name)
	delete(mq.messages, name)
	return nil
}

func (mq *MockIntegrationMessageQueue) SendMessage(ctx context.Context, from, to string, message *messagequeue.Message) error {
	if mq.messages[to] == nil {
		mq.messages[to] = make([]*messagequeue.Message, 0)
	}
	mq.messages[to] = append(mq.messages[to], message)
	return nil
}

func (mq *MockIntegrationMessageQueue) PublishTask(ctx context.Context, from, topic string, message *messagequeue.Message) error {
	if mq.messages[topic] == nil {
		mq.messages[topic] = make([]*messagequeue.Message, 0)
	}
	mq.messages[topic] = append(mq.messages[topic], message)
	return nil
}

func (mq *MockIntegrationMessageQueue) GetQueueStats(ctx context.Context, queueName string) (*amqtypes.QueueStats, error) {
	messageCount := int64(0)
	if messages, ok := mq.messages[queueName]; ok {
		messageCount = int64(len(messages))
	}
	
	return &amqtypes.QueueStats{
		Name:         queueName,
		Type:         amqtypes.QueueTypeTask,
		MessageCount: messageCount,
		Subscribers:  1,
		EnqueueRate:  0.0,
		DequeueRate:  0.0,
		ErrorRate:    0.0,
		AverageWait:  0,
	}, nil
}

func (mq *MockIntegrationMessageQueue) ListQueues(ctx context.Context) ([]*amqtypes.Queue, error) {
	queues := make([]*amqtypes.Queue, 0, len(mq.queues))
	for name := range mq.queues {
		queues = append(queues, &amqtypes.Queue{
			Name: name,
		})
	}
	return queues, nil
}

func (mq *MockIntegrationMessageQueue) Close() error {
	return nil
}

func setupIntegrationTest(t *testing.T) *IntegrationTestSuite {
	// Create test configuration
	cfg := &config.Config{
		Server: config.ServerConfig{
			GRPC: config.GRPCConfig{
				Host:              "127.0.0.1",
				Port:              0, // Dynamic port
				MaxRecvMsgSize:    4 * 1024 * 1024,
				MaxSendMsgSize:    4 * 1024 * 1024,
				ConnectionTimeout: 30 * time.Second,
				ReflectionEnabled: true,
			},
			HTTP: config.HTTPConfig{
				Host:         "127.0.0.1",
				Port:         0, // Dynamic port
				ReadTimeout:  10 * time.Second,
				WriteTimeout: 10 * time.Second,
				IdleTimeout:  60 * time.Second,
			},
			ShutdownTimeout: 10 * time.Second,
		},
		Runtime: config.RuntimeConfig{
			Type:      "mock",
			Namespace: "test",
		},
		State: config.StateConfig{
			Type: "memory",
		},
		MessageQueue: config.MessageQueueConfig{
			StorePath:      "/tmp/test-amq",
			WorkerPoolSize: 5,
			MessageTimeout: 30 * time.Second,
		},
		Monitoring: config.MonitoringConfig{
			Metrics: config.MetricsConfig{
				Enabled: true,
				Port:    0, // Dynamic port
			},
			HealthChecks: config.HealthChecksConfig{
				Enabled:  true,
				Interval: 1 * time.Second,
			},
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
	
	// Create components
	runtime := NewMockIntegrationRuntime()
	stateManager := state.NewMemoryStore()
	messageQueue := NewMockIntegrationMessageQueue()
	
	// Create monitor
	monitor, err := monitoring.NewMonitor(&cfg.Monitoring)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	// Create orchestrator
	orch, err := orchestrator.New(orchestrator.Config{
		Runtime:      runtime,
		MessageQueue: messageQueue,
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	// Start orchestrator
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	
	// Start monitor
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	
	// Create server config with dynamic ports
	serverConfig := &server.Config{
		GRPCPort:          cfg.Server.GRPC.Port,
		GRPCHost:          cfg.Server.GRPC.Host,
		HTTPPort:          cfg.Server.HTTP.Port,
		HTTPHost:          cfg.Server.HTTP.Host,
		MaxRecvMsgSize:    cfg.Server.GRPC.MaxRecvMsgSize,
		MaxSendMsgSize:    cfg.Server.GRPC.MaxSendMsgSize,
		ConnectionTimeout: cfg.Server.GRPC.ConnectionTimeout,
		ShutdownTimeout:   cfg.Server.ShutdownTimeout,
		ReflectionEnabled: cfg.Server.GRPC.ReflectionEnabled,
	}
	
	// Create and start server
	srv, err := server.NewServerWithMonitoring(serverConfig, orch, monitor, cfg)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	if err := srv.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	
	// Give server time to start
	time.Sleep(100 * time.Millisecond)
	
	// Create gRPC client
	grpcAddr := srv.GetGRPCAddress()
	conn, err := grpc.Dial(grpcAddr, 
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.WithTimeout(5*time.Second))
	if err != nil {
		t.Fatalf("Failed to connect to gRPC server: %v", err)
	}
	
	client := arcv1.NewARCClient(conn)
	
	suite := &IntegrationTestSuite{
		server:       srv,
		orchestrator: orch,
		grpcClient:   client,
		grpcConn:     conn,
		monitor:      monitor,
		config:       cfg,
	}
	
	// Setup cleanup
	t.Cleanup(func() {
		suite.Cleanup()
	})
	
	return suite
}

func (suite *IntegrationTestSuite) Cleanup() {
	if suite.grpcConn != nil {
		suite.grpcConn.Close()
	}
	
	if suite.server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		suite.server.Stop(ctx)
	}
	
	if suite.orchestrator != nil {
		suite.orchestrator.Stop()
	}
	
	if suite.monitor != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		suite.monitor.Stop(ctx)
	}
}

func TestIntegration_WorkflowLifecycle(t *testing.T) {
	suite := setupIntegrationTest(t)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	// Create a workflow
	createReq := &arcv1.CreateWorkflowRequest{
		Workflow: &arcv1.Workflow{
			Name:        "integration-test-workflow",
			Description: "Integration test workflow",
			Tasks: []*arcv1.Task{
				{
					Name: "task-1",
					AgentConfig: &arcv1.AgentConfig{
						Image:   "ubuntu:latest",
						Command: []string{"echo", "hello from task 1"},
					},
				},
				{
					Name:         "task-2",
					Dependencies: []string{"task-1"},
					AgentConfig: &arcv1.AgentConfig{
						Image:   "ubuntu:latest",
						Command: []string{"echo", "hello from task 2"},
					},
				},
			},
		},
	}
	
	createResp, err := suite.grpcClient.CreateWorkflow(ctx, createReq)
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}
	
	if createResp.WorkflowId == "" {
		t.Fatal("Workflow ID should not be empty")
	}
	
	workflowID := createResp.WorkflowId
	
	// Get the workflow
	getReq := &arcv1.GetWorkflowRequest{
		WorkflowId: workflowID,
	}
	
	getResp, err := suite.grpcClient.GetWorkflow(ctx, getReq)
	if err != nil {
		t.Fatalf("Failed to get workflow: %v", err)
	}
	
	if getResp.Workflow.Name != "integration-test-workflow" {
		t.Errorf("Expected workflow name 'integration-test-workflow', got %s", getResp.Workflow.Name)
	}
	
	if len(getResp.Workflow.Tasks) != 2 {
		t.Errorf("Expected 2 tasks, got %d", len(getResp.Workflow.Tasks))
	}
	
	// Start the workflow
	startReq := &arcv1.StartWorkflowRequest{
		WorkflowId: workflowID,
	}
	
	_, err = suite.grpcClient.StartWorkflow(ctx, startReq)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	
	// Wait for workflow execution
	time.Sleep(200 * time.Millisecond)
	
	// Check workflow status
	getResp, err = suite.grpcClient.GetWorkflow(ctx, getReq)
	if err != nil {
		t.Fatalf("Failed to get workflow after start: %v", err)
	}
	
	if getResp.Workflow.Status == arcv1.WorkflowStatus_WORKFLOW_STATUS_PENDING {
		t.Error("Workflow should have progressed from pending status")
	}
	
	// Stop the workflow
	stopReq := &arcv1.StopWorkflowRequest{
		WorkflowId: workflowID,
	}
	
	_, err = suite.grpcClient.StopWorkflow(ctx, stopReq)
	if err != nil {
		t.Fatalf("Failed to stop workflow: %v", err)
	}
	
	// List workflows
	listReq := &arcv1.ListWorkflowsRequest{}
	listResp, err := suite.grpcClient.ListWorkflows(ctx, listReq)
	if err != nil {
		t.Fatalf("Failed to list workflows: %v", err)
	}
	
	found := false
	for _, workflow := range listResp.Workflows {
		if workflow.Id == workflowID {
			found = true
			break
		}
	}
	
	if !found {
		t.Error("Created workflow should be in the list")
	}
}

func TestIntegration_RealTimeAgentLifecycle(t *testing.T) {
	suite := setupIntegrationTest(t)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	// Create a real-time agent
	createReq := &arcv1.CreateRealTimeAgentRequest{
		Config: &arcv1.RealTimeAgentConfig{
			Agent: &arcv1.Agent{
				Name:  "integration-test-agent",
				Image: "ubuntu:latest",
				Config: &arcv1.AgentConfig{
					Command: []string{"bash", "-c", "while true; do sleep 1; done"},
					MessageQueue: &arcv1.MessageQueueConfig{
						Topics: []string{"test-topic"},
					},
				},
			},
			Topics: []string{"test-topic"},
		},
	}
	
	createResp, err := suite.grpcClient.CreateRealTimeAgent(ctx, createReq)
	if err != nil {
		t.Fatalf("Failed to create real-time agent: %v", err)
	}
	
	if createResp.AgentId == "" {
		t.Fatal("Agent ID should not be empty")
	}
	
	agentID := createResp.AgentId
	
	// Get the agent
	getReq := &arcv1.GetRealTimeAgentRequest{
		AgentId: agentID,
	}
	
	getResp, err := suite.grpcClient.GetRealTimeAgent(ctx, getReq)
	if err != nil {
		t.Fatalf("Failed to get real-time agent: %v", err)
	}
	
	if getResp.Agent.Name != "integration-test-agent" {
		t.Errorf("Expected agent name 'integration-test-agent', got %s", getResp.Agent.Name)
	}
	
	// Start the agent
	startReq := &arcv1.StartRealTimeAgentRequest{
		AgentId: agentID,
	}
	
	_, err = suite.grpcClient.StartRealTimeAgent(ctx, startReq)
	if err != nil {
		t.Fatalf("Failed to start real-time agent: %v", err)
	}
	
	// Wait for agent to start
	time.Sleep(100 * time.Millisecond)
	
	// Check agent status
	statusReq := &arcv1.GetAgentStatusRequest{
		AgentId: agentID,
	}
	
	statusResp, err := suite.grpcClient.GetAgentStatus(ctx, statusReq)
	if err != nil {
		t.Fatalf("Failed to get agent status: %v", err)
	}
	
	if statusResp.Status == arcv1.AgentStatus_AGENT_STATUS_PENDING {
		t.Error("Agent should have progressed from pending status")
	}
	
	// Send a message to the agent
	msgReq := &arcv1.SendMessageRequest{
		From: "test-sender",
		To:   agentID,
		Message: &arcv1.Message{
			Id:   "test-message-1",
			Type: "task",
			Payload: map[string]string{
				"command": "hello",
				"data":    "world",
			},
		},
	}
	
	msgResp, err := suite.grpcClient.SendMessage(ctx, msgReq)
	if err != nil {
		t.Fatalf("Failed to send message: %v", err)
	}
	
	if msgResp.Status != "sent" {
		t.Errorf("Expected message status 'sent', got %s", msgResp.Status)
	}
	
	// List real-time agents
	listReq := &arcv1.ListRealTimeAgentsRequest{}
	listResp, err := suite.grpcClient.ListRealTimeAgents(ctx, listReq)
	if err != nil {
		t.Fatalf("Failed to list real-time agents: %v", err)
	}
	
	found := false
	for _, agent := range listResp.Agents {
		if agent.Id == agentID {
			found = true
			break
		}
	}
	
	if !found {
		t.Error("Created agent should be in the list")
	}
	
	// Stop the agent
	stopReq := &arcv1.StopRealTimeAgentRequest{
		AgentId: agentID,
	}
	
	_, err = suite.grpcClient.StopRealTimeAgent(ctx, stopReq)
	if err != nil {
		t.Fatalf("Failed to stop real-time agent: %v", err)
	}
}

func TestIntegration_HealthAndReadiness(t *testing.T) {
	suite := setupIntegrationTest(t)
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	// Check health
	healthResp, err := suite.grpcClient.Health(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to get health status: %v", err)
	}
	
	if healthResp.Status != "healthy" {
		t.Errorf("Expected health status 'healthy', got %s", healthResp.Status)
	}
	
	if healthResp.Checks == nil || len(healthResp.Checks) == 0 {
		t.Error("Health checks should not be empty")
	}
	
	// Check readiness
	readyResp, err := suite.grpcClient.Ready(ctx, nil)
	if err != nil {
		t.Fatalf("Failed to get readiness status: %v", err)
	}
	
	if !readyResp.Ready {
		t.Error("Service should be ready")
	}
	
	if len(readyResp.Services) == 0 {
		t.Error("Ready services list should not be empty")
	}
}

func TestIntegration_ConcurrentWorkflows(t *testing.T) {
	suite := setupIntegrationTest(t)
	
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	
	numWorkflows := 5
	workflowIDs := make([]string, numWorkflows)
	
	// Create multiple workflows concurrently
	for i := 0; i < numWorkflows; i++ {
		workflowName := fmt.Sprintf("concurrent-workflow-%d", i)
		
		createReq := &arcv1.CreateWorkflowRequest{
			Workflow: &arcv1.Workflow{
				Name:        workflowName,
				Description: fmt.Sprintf("Concurrent test workflow %d", i),
				Tasks: []*arcv1.Task{
					{
						Name: fmt.Sprintf("task-%d", i),
						AgentConfig: &arcv1.AgentConfig{
							Image:   "ubuntu:latest",
							Command: []string{"echo", fmt.Sprintf("hello from workflow %d", i)},
						},
					},
				},
			},
		}
		
		createResp, err := suite.grpcClient.CreateWorkflow(ctx, createReq)
		if err != nil {
			t.Fatalf("Failed to create workflow %d: %v", i, err)
		}
		
		workflowIDs[i] = createResp.WorkflowId
		
		// Start the workflow
		startReq := &arcv1.StartWorkflowRequest{
			WorkflowId: workflowIDs[i],
		}
		
		_, err = suite.grpcClient.StartWorkflow(ctx, startReq)
		if err != nil {
			t.Fatalf("Failed to start workflow %d: %v", i, err)
		}
	}
	
	// Wait for workflows to process
	time.Sleep(500 * time.Millisecond)
	
	// List all workflows and verify they all exist
	listReq := &arcv1.ListWorkflowsRequest{}
	listResp, err := suite.grpcClient.ListWorkflows(ctx, listReq)
	if err != nil {
		t.Fatalf("Failed to list workflows: %v", err)
	}
	
	if len(listResp.Workflows) != numWorkflows {
		t.Errorf("Expected %d workflows, got %d", numWorkflows, len(listResp.Workflows))
	}
	
	// Stop all workflows
	for i, workflowID := range workflowIDs {
		stopReq := &arcv1.StopWorkflowRequest{
			WorkflowId: workflowID,
		}
		
		_, err := suite.grpcClient.StopWorkflow(ctx, stopReq)
		if err != nil {
			t.Errorf("Failed to stop workflow %d: %v", i, err)
		}
	}
}

func TestIntegration_WorkflowWithDependencies(t *testing.T) {
	suite := setupIntegrationTest(t)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	// Create a workflow with task dependencies
	createReq := &arcv1.CreateWorkflowRequest{
		Workflow: &arcv1.Workflow{
			Name:        "dependency-test-workflow",
			Description: "Test workflow with task dependencies",
			Tasks: []*arcv1.Task{
				{
					Name: "setup-task",
					AgentConfig: &arcv1.AgentConfig{
						Image:   "ubuntu:latest",
						Command: []string{"echo", "setup complete"},
					},
				},
				{
					Name:         "process-task-1",
					Dependencies: []string{"setup-task"},
					AgentConfig: &arcv1.AgentConfig{
						Image:   "ubuntu:latest",
						Command: []string{"echo", "processing data 1"},
					},
				},
				{
					Name:         "process-task-2",
					Dependencies: []string{"setup-task"},
					AgentConfig: &arcv1.AgentConfig{
						Image:   "ubuntu:latest",
						Command: []string{"echo", "processing data 2"},
					},
				},
				{
					Name:         "cleanup-task",
					Dependencies: []string{"process-task-1", "process-task-2"},
					AgentConfig: &arcv1.AgentConfig{
						Image:   "ubuntu:latest",
						Command: []string{"echo", "cleanup complete"},
					},
				},
			},
		},
	}
	
	createResp, err := suite.grpcClient.CreateWorkflow(ctx, createReq)
	if err != nil {
		t.Fatalf("Failed to create workflow with dependencies: %v", err)
	}
	
	workflowID := createResp.WorkflowId
	
	// Start the workflow
	startReq := &arcv1.StartWorkflowRequest{
		WorkflowId: workflowID,
	}
	
	_, err = suite.grpcClient.StartWorkflow(ctx, startReq)
	if err != nil {
		t.Fatalf("Failed to start workflow: %v", err)
	}
	
	// Wait for workflow to process
	time.Sleep(300 * time.Millisecond)
	
	// Get workflow and check task execution
	getReq := &arcv1.GetWorkflowRequest{
		WorkflowId: workflowID,
	}
	
	getResp, err := suite.grpcClient.GetWorkflow(ctx, getReq)
	if err != nil {
		t.Fatalf("Failed to get workflow: %v", err)
	}
	
	// Verify all tasks exist and have proper dependencies
	taskMap := make(map[string]*arcv1.Task)
	for _, task := range getResp.Workflow.Tasks {
		taskMap[task.Name] = task
	}
	
	// Check dependencies are correctly set
	if setupTask, ok := taskMap["setup-task"]; ok {
		if len(setupTask.Dependencies) != 0 {
			t.Error("Setup task should have no dependencies")
		}
	} else {
		t.Error("Setup task not found")
	}
	
	if cleanupTask, ok := taskMap["cleanup-task"]; ok {
		if len(cleanupTask.Dependencies) != 2 {
			t.Errorf("Cleanup task should have 2 dependencies, got %d", len(cleanupTask.Dependencies))
		}
	} else {
		t.Error("Cleanup task not found")
	}
}

func TestIntegration_ErrorHandling(t *testing.T) {
	suite := setupIntegrationTest(t)
	
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	// Test creating workflow with invalid data
	createReq := &arcv1.CreateWorkflowRequest{
		Workflow: nil, // Invalid - nil workflow
	}
	
	_, err := suite.grpcClient.CreateWorkflow(ctx, createReq)
	if err == nil {
		t.Error("Expected error when creating workflow with nil data")
	}
	
	// Test getting non-existent workflow
	getReq := &arcv1.GetWorkflowRequest{
		WorkflowId: "non-existent-workflow-id",
	}
	
	_, err = suite.grpcClient.GetWorkflow(ctx, getReq)
	if err == nil {
		t.Error("Expected error when getting non-existent workflow")
	}
	
	// Test starting non-existent workflow
	startReq := &arcv1.StartWorkflowRequest{
		WorkflowId: "non-existent-workflow-id",
	}
	
	_, err = suite.grpcClient.StartWorkflow(ctx, startReq)
	if err == nil {
		t.Error("Expected error when starting non-existent workflow")
	}
	
	// Test creating real-time agent with invalid config
	agentReq := &arcv1.CreateRealTimeAgentRequest{
		Config: nil, // Invalid - nil config
	}
	
	_, err = suite.grpcClient.CreateRealTimeAgent(ctx, agentReq)
	if err == nil {
		t.Error("Expected error when creating agent with nil config")
	}
}

func TestIntegration_MessagePassing(t *testing.T) {
	suite := setupIntegrationTest(t)
	
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	
	// Create a real-time agent for message testing
	createReq := &arcv1.CreateRealTimeAgentRequest{
		Config: &arcv1.RealTimeAgentConfig{
			Agent: &arcv1.Agent{
				Name:  "message-test-agent",
				Image: "ubuntu:latest",
				Config: &arcv1.AgentConfig{
					Command: []string{"bash", "-c", "while true; do sleep 0.1; done"},
					MessageQueue: &arcv1.MessageQueueConfig{
						Topics: []string{"message-test-topic"},
					},
				},
			},
			Topics: []string{"message-test-topic"},
		},
	}
	
	createResp, err := suite.grpcClient.CreateRealTimeAgent(ctx, createReq)
	if err != nil {
		t.Fatalf("Failed to create agent for message test: %v", err)
	}
	
	agentID := createResp.AgentId
	
	// Start the agent
	startReq := &arcv1.StartRealTimeAgentRequest{
		AgentId: agentID,
	}
	
	_, err = suite.grpcClient.StartRealTimeAgent(ctx, startReq)
	if err != nil {
		t.Fatalf("Failed to start agent: %v", err)
	}
	
	// Send multiple messages
	messages := []struct {
		id      string
		payload map[string]string
	}{
		{
			id: "msg-1",
			payload: map[string]string{
				"type": "command",
				"data": "execute task A",
			},
		},
		{
			id: "msg-2",
			payload: map[string]string{
				"type": "query",
				"data": "get status",
			},
		},
		{
			id: "msg-3",
			payload: map[string]string{
				"type": "shutdown",
				"data": "graceful shutdown",
			},
		},
	}
	
	for _, msg := range messages {
		msgReq := &arcv1.SendMessageRequest{
			From: "integration-test",
			To:   agentID,
			Message: &arcv1.Message{
				Id:      msg.id,
				Type:    "task",
				Payload: msg.payload,
			},
		}
		
		msgResp, err := suite.grpcClient.SendMessage(ctx, msgReq)
		if err != nil {
			t.Errorf("Failed to send message %s: %v", msg.id, err)
			continue
		}
		
		if msgResp.Status != "sent" {
			t.Errorf("Expected message status 'sent' for %s, got %s", msg.id, msgResp.Status)
		}
	}
	
	// Stop the agent
	stopReq := &arcv1.StopRealTimeAgentRequest{
		AgentId: agentID,
	}
	
	_, err = suite.grpcClient.StopRealTimeAgent(ctx, stopReq)
	if err != nil {
		t.Errorf("Failed to stop agent: %v", err)
	}
}