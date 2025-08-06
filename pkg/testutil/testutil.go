// Package testutil provides shared testing utilities and helpers for ARC
package testutil

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/rizome-dev/amq/pkg/client"
	amqtypes "github.com/rizome-dev/amq/pkg/types"
	"github.com/rizome-dev/arc/pkg/config"
	"github.com/rizome-dev/arc/pkg/messagequeue"
	"github.com/rizome-dev/arc/pkg/monitoring"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"github.com/rizome-dev/arc/pkg/runtime"
	"github.com/rizome-dev/arc/pkg/server"
	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
)

// TestEnvironment provides a complete test environment for ARC
type TestEnvironment struct {
	Server       *server.Server
	Orchestrator *orchestrator.Orchestrator
	Monitor      *monitoring.Monitor
	Runtime      runtime.Runtime
	StateManager state.StateManager
	MessageQueue messagequeue.MessageQueue
	Config       *config.Config
}

// MockRuntime provides a mock runtime implementation for testing
type MockRuntime struct {
	agents     map[string]*types.Agent
	containers map[string]string
	mu         sync.RWMutex
	
	// Configuration for test behavior
	CreateDelay time.Duration
	StartDelay  time.Duration
	StopDelay   time.Duration
	
	// Error simulation
	CreateError error
	StartError  error
	StopError   error
}

// NewMockRuntime creates a new mock runtime
func NewMockRuntime() *MockRuntime {
	return &MockRuntime{
		agents:     make(map[string]*types.Agent),
		containers: make(map[string]string),
		
		// Default delays
		CreateDelay: 10 * time.Millisecond,
		StartDelay:  10 * time.Millisecond,
		StopDelay:   10 * time.Millisecond,
	}
}

func (r *MockRuntime) CreateAgent(ctx context.Context, agent *types.Agent) error {
	if r.CreateError != nil {
		return r.CreateError
	}
	
	if r.CreateDelay > 0 {
		time.Sleep(r.CreateDelay)
	}
	
	r.mu.Lock()
	defer r.mu.Unlock()
	
	agent.Status = types.AgentStatusCreating
	containerID := fmt.Sprintf("container-%s", agent.ID)
	agent.ContainerID = containerID
	r.agents[agent.ID] = agent
	r.containers[agent.ID] = containerID
	
	return nil
}

func (r *MockRuntime) StartAgent(ctx context.Context, agentID string) error {
	if r.StartError != nil {
		return r.StartError
	}
	
	if r.StartDelay > 0 {
		time.Sleep(r.StartDelay)
	}
	
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if agent, ok := r.agents[agentID]; ok {
		agent.Status = types.AgentStatusRunning
		now := time.Now()
		agent.StartedAt = &now
	}
	
	return nil
}

func (r *MockRuntime) StopAgent(ctx context.Context, agentID string) error {
	if r.StopError != nil {
		return r.StopError
	}
	
	if r.StopDelay > 0 {
		time.Sleep(r.StopDelay)
	}
	
	r.mu.Lock()
	defer r.mu.Unlock()
	
	if agent, ok := r.agents[agentID]; ok {
		agent.Status = types.AgentStatusCompleted
		now := time.Now()
		agent.CompletedAt = &now
	}
	
	return nil
}

func (r *MockRuntime) DestroyAgent(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	delete(r.agents, agentID)
	delete(r.containers, agentID)
	return nil
}

func (r *MockRuntime) GetAgentStatus(ctx context.Context, agentID string) (*types.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
	if agent, ok := r.agents[agentID]; ok {
		return agent, nil
	}
	return nil, fmt.Errorf("agent not found: %s", agentID)
}

func (r *MockRuntime) ListAgents(ctx context.Context) ([]*types.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	
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

// SetCreateError sets an error to be returned by CreateAgent
func (r *MockRuntime) SetCreateError(err error) {
	r.CreateError = err
}

// SetStartError sets an error to be returned by StartAgent
func (r *MockRuntime) SetStartError(err error) {
	r.StartError = err
}

// SetStopError sets an error to be returned by StopAgent
func (r *MockRuntime) SetStopError(err error) {
	r.StopError = err
}

// GetAgentCount returns the number of agents in the runtime
func (r *MockRuntime) GetAgentCount() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.agents)
}

// MockMessageQueue provides a mock message queue implementation for testing
type MockMessageQueue struct {
	queues   map[string]bool
	messages map[string][]*messagequeue.Message
	mu       sync.RWMutex
}

// NewMockMessageQueue creates a new mock message queue
func NewMockMessageQueue() *MockMessageQueue {
	return &MockMessageQueue{
		queues:   make(map[string]bool),
		messages: make(map[string][]*messagequeue.Message),
	}
}

func (mq *MockMessageQueue) GetClient(agentID string, metadata map[string]string) (client.Client, error) {
	return nil, nil
}

func (mq *MockMessageQueue) GetAsyncConsumer(agentID string, metadata map[string]string) (client.AsyncConsumer, error) {
	return nil, nil
}

func (mq *MockMessageQueue) CreateQueue(ctx context.Context, name string) error {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	
	mq.queues[name] = true
	return nil
}

func (mq *MockMessageQueue) DeleteQueue(ctx context.Context, name string) error {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	
	delete(mq.queues, name)
	delete(mq.messages, name)
	return nil
}

func (mq *MockMessageQueue) SendMessage(ctx context.Context, from, to string, message *messagequeue.Message) error {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	
	if mq.messages[to] == nil {
		mq.messages[to] = make([]*messagequeue.Message, 0)
	}
	mq.messages[to] = append(mq.messages[to], message)
	return nil
}

func (mq *MockMessageQueue) PublishTask(ctx context.Context, from, topic string, message *messagequeue.Message) error {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	
	if mq.messages[topic] == nil {
		mq.messages[topic] = make([]*messagequeue.Message, 0)
	}
	mq.messages[topic] = append(mq.messages[topic], message)
	return nil
}

func (mq *MockMessageQueue) GetQueueStats(ctx context.Context, queueName string) (*amqtypes.QueueStats, error) {
	mq.mu.RLock()
	defer mq.mu.RUnlock()
	
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

func (mq *MockMessageQueue) ListQueues(ctx context.Context) ([]*amqtypes.Queue, error) {
	mq.mu.RLock()
	defer mq.mu.RUnlock()
	
	queues := make([]*amqtypes.Queue, 0, len(mq.queues))
	for name := range mq.queues {
		queues = append(queues, &amqtypes.Queue{
			Name: name,
		})
	}
	return queues, nil
}

func (mq *MockMessageQueue) Close() error {
	return nil
}

// GetMessages returns messages for a queue/topic
func (mq *MockMessageQueue) GetMessages(queueName string) []*messagequeue.Message {
	mq.mu.RLock()
	defer mq.mu.RUnlock()
	
	if messages, ok := mq.messages[queueName]; ok {
		// Return a copy
		result := make([]*messagequeue.Message, len(messages))
		copy(result, messages)
		return result
	}
	return nil
}

// GetQueueCount returns the number of queues
func (mq *MockMessageQueue) GetQueueCount() int {
	mq.mu.RLock()
	defer mq.mu.RUnlock()
	return len(mq.queues)
}

// ClearMessages clears all messages from all queues
func (mq *MockMessageQueue) ClearMessages() {
	mq.mu.Lock()
	defer mq.mu.Unlock()
	
	for name := range mq.messages {
		mq.messages[name] = nil
	}
}

// TestConfig creates a test configuration
func TestConfig() *config.Config {
	return &config.Config{
		Server: config.ServerConfig{
			GRPC: config.GRPCConfig{
				Host:              "127.0.0.1",
				Port:              0, // Dynamic port
				MaxRecvMsgSize:    4 * 1024 * 1024,
				MaxSendMsgSize:    4 * 1024 * 1024,
				ConnectionTimeout: 5 * time.Second,
				ReflectionEnabled: true,
			},
			HTTP: config.HTTPConfig{
				Host:         "127.0.0.1",
				Port:         0, // Dynamic port
				ReadTimeout:  5 * time.Second,
				WriteTimeout: 5 * time.Second,
				IdleTimeout:  10 * time.Second,
			},
			ShutdownTimeout: 5 * time.Second,
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
			MessageTimeout: 5 * time.Second,
		},
		Monitoring: config.MonitoringConfig{
			Metrics: config.MetricsConfig{
				Enabled: false, // Disabled for testing by default
			},
			HealthChecks: config.HealthChecksConfig{
				Enabled:  true,
				Interval: 100 * time.Millisecond,
			},
		},
		Logging: config.LoggingConfig{
			Level:  "info",
			Format: "json",
		},
	}
}

// CreateTestEnvironment creates a complete test environment
func CreateTestEnvironment() (*TestEnvironment, error) {
	cfg := TestConfig()
	
	// Create components
	runtime := NewMockRuntime()
	stateManager := state.NewMemoryStore()
	messageQueue := NewMockMessageQueue()
	
	// Create monitor if enabled
	var monitor *monitoring.Monitor
	if cfg.Monitoring.Metrics.Enabled || cfg.Monitoring.HealthChecks.Enabled {
		var err error
		monitor, err = monitoring.NewMonitor(&cfg.Monitoring)
		if err != nil {
			return nil, fmt.Errorf("failed to create monitor: %w", err)
		}
	}
	
	// Create orchestrator
	orch, err := orchestrator.New(orchestrator.Config{
		Runtime:      runtime,
		MessageQueue: messageQueue,
		StateManager: stateManager,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create orchestrator: %w", err)
	}
	
	env := &TestEnvironment{
		Orchestrator: orch,
		Monitor:      monitor,
		Runtime:      runtime,
		StateManager: stateManager,
		MessageQueue: messageQueue,
		Config:       cfg,
	}
	
	return env, nil
}

// Start starts the test environment
func (env *TestEnvironment) Start() error {
	// Start orchestrator
	if err := env.Orchestrator.Start(); err != nil {
		return fmt.Errorf("failed to start orchestrator: %w", err)
	}
	
	// Start monitor if available
	if env.Monitor != nil {
		ctx := context.Background()
		if err := env.Monitor.Start(ctx); err != nil {
			return fmt.Errorf("failed to start monitor: %w", err)
		}
	}
	
	return nil
}

// Stop stops the test environment
func (env *TestEnvironment) Stop() error {
	// Stop server if it exists
	if env.Server != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := env.Server.Stop(ctx); err != nil {
			return fmt.Errorf("failed to stop server: %w", err)
		}
	}
	
	// Stop orchestrator
	if err := env.Orchestrator.Stop(); err != nil {
		return fmt.Errorf("failed to stop orchestrator: %w", err)
	}
	
	// Stop monitor if available
	if env.Monitor != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := env.Monitor.Stop(ctx); err != nil {
			return fmt.Errorf("failed to stop monitor: %w", err)
		}
	}
	
	return nil
}

// CreateServer creates and starts a gRPC server for testing
func (env *TestEnvironment) CreateServer() error {
	serverConfig := &server.Config{
		GRPCPort:          env.Config.Server.GRPC.Port,
		GRPCHost:          env.Config.Server.GRPC.Host,
		HTTPPort:          env.Config.Server.HTTP.Port,
		HTTPHost:          env.Config.Server.HTTP.Host,
		MaxRecvMsgSize:    env.Config.Server.GRPC.MaxRecvMsgSize,
		MaxSendMsgSize:    env.Config.Server.GRPC.MaxSendMsgSize,
		ConnectionTimeout: env.Config.Server.GRPC.ConnectionTimeout,
		ShutdownTimeout:   env.Config.Server.ShutdownTimeout,
		ReflectionEnabled: env.Config.Server.GRPC.ReflectionEnabled,
	}
	
	srv, err := server.NewServerWithMonitoring(serverConfig, env.Orchestrator, env.Monitor, env.Config)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}
	
	if err := srv.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	
	env.Server = srv
	return nil
}

// GetFreePort returns a free port for testing
func GetFreePort() (int, error) {
	addr, err := net.ResolveTCPAddr("tcp", "localhost:0")
	if err != nil {
		return 0, err
	}
	
	listener, err := net.ListenTCP("tcp", addr)
	if err != nil {
		return 0, err
	}
	defer listener.Close()
	
	return listener.Addr().(*net.TCPAddr).Port, nil
}

// WaitForCondition waits for a condition to be true with timeout
func WaitForCondition(condition func() bool, timeout time.Duration, interval time.Duration) bool {
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	
	for {
		select {
		case <-timer.C:
			return false
		case <-ticker.C:
			if condition() {
				return true
			}
		}
	}
}

// CreateTestWorkflow creates a test workflow with given parameters
func CreateTestWorkflow(name string, numTasks int) *types.Workflow {
	tasks := make([]types.Task, numTasks)
	
	for i := 0; i < numTasks; i++ {
		tasks[i] = types.Task{
			Name: fmt.Sprintf("task-%d", i+1),
			AgentConfig: types.AgentConfig{
				Image:   "ubuntu:latest",
				Command: []string{"echo", fmt.Sprintf("Task %d", i+1)},
			},
		}
		
		// Add dependency to previous task (except for first task)
		if i > 0 {
			tasks[i].Dependencies = []string{tasks[i-1].Name}
		}
	}
	
	return &types.Workflow{
		Name:        name,
		Description: fmt.Sprintf("Test workflow with %d tasks", numTasks),
		Tasks:       tasks,
		Status:      types.WorkflowStatusPending,
		CreatedAt:   time.Now(),
	}
}

// CreateTestAgent creates a test agent with given parameters
func CreateTestAgent(name, image string) *types.Agent {
	return &types.Agent{
		Name:      name,
		Image:     image,
		Status:    types.AgentStatusPending,
		CreatedAt: time.Now(),
		Config: types.AgentConfig{
			Image:   image,
			Command: []string{"echo", "test"},
		},
	}
}

// CreateTestMessage creates a test message
func CreateTestMessage(id, from, to, msgType string, payload map[string]interface{}) *messagequeue.Message {
	return &messagequeue.Message{
		ID:        id,
		From:      from,
		To:        to,
		Type:      msgType,
		Payload:   payload,
		Timestamp: time.Now().Unix(),
	}
}

// AssertEventually asserts that a condition becomes true within a timeout
func AssertEventually(condition func() bool, timeout time.Duration, message string) error {
	if WaitForCondition(condition, timeout, 10*time.Millisecond) {
		return nil
	}
	return fmt.Errorf("condition not met within timeout: %s", message)
}

// GetMockRuntime returns the mock runtime from the environment (helper for type assertion)
func (env *TestEnvironment) GetMockRuntime() *MockRuntime {
	if mockRuntime, ok := env.Runtime.(*MockRuntime); ok {
		return mockRuntime
	}
	return nil
}

// GetMockMessageQueue returns the mock message queue from the environment (helper for type assertion)
func (env *TestEnvironment) GetMockMessageQueue() *MockMessageQueue {
	if mockMQ, ok := env.MessageQueue.(*MockMessageQueue); ok {
		return mockMQ
	}
	return nil
}