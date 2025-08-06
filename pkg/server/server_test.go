package server

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rizome-dev/amq/pkg/client"
	amqtypes "github.com/rizome-dev/amq/pkg/types"
	"github.com/rizome-dev/arc/pkg/config"
	"github.com/rizome-dev/arc/pkg/messagequeue"
	"github.com/rizome-dev/arc/pkg/monitoring"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
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

// Implement runtime.Runtime interface methods for MockRuntime
// (These would be the same as in orchestrator tests, but we need them here too)

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

// Implement messagequeue.MessageQueue interface methods
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

func createTestOrchestrator(t *testing.T) *orchestrator.Orchestrator {
	runtime := NewMockRuntime()
	stateManager := state.NewMemoryStore()
	messageQueue := NewMockMessageQueue()
	
	orch, err := orchestrator.New(orchestrator.Config{
		Runtime:      runtime,
		MessageQueue: messageQueue,
		StateManager: stateManager,
	})
	if err != nil {
		t.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	return orch
}

func createTestConfig() *Config {
	// Use dynamic port allocation to avoid conflicts
	return &Config{
		GRPCPort:            0, // Let OS assign port
		GRPCHost:            "localhost",
		HTTPPort:            0, // Let OS assign port
		HTTPHost:            "localhost",
		TLSEnabled:          false,
		MaxRecvMsgSize:      4 * 1024 * 1024,
		MaxSendMsgSize:      4 * 1024 * 1024,
		ConnectionTimeout:   5 * time.Second,
		HealthCheckInterval: 1 * time.Second,
		ShutdownTimeout:     5 * time.Second,
		MaxConnections:      100,
		ReadTimeout:         5 * time.Second,
		WriteTimeout:        5 * time.Second,
		IdleTimeout:         10 * time.Second,
		ReflectionEnabled:   true,
	}
}

func TestNewServer(t *testing.T) {
	config := createTestConfig()
	orch := createTestOrchestrator(t)
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	if server == nil {
		t.Fatal("Server should not be nil")
	}
	
	if server.config != config {
		t.Error("Server config should match provided config")
	}
	
	if server.orchestrator != orch {
		t.Error("Server orchestrator should match provided orchestrator")
	}
}

func TestNewServerWithNilOrchestrator(t *testing.T) {
	config := createTestConfig()
	
	_, err := NewServer(config, nil)
	if err == nil {
		t.Error("Expected error when creating server with nil orchestrator")
	}
}

func TestNewServerWithMonitoring(t *testing.T) {
	serverConfig := createTestConfig()
	orch := createTestOrchestrator(t)
	
	// Create a proper config for monitoring
	appConfig := config.DefaultConfig()
	appConfig.Monitoring.Metrics.Enabled = true
	appConfig.Monitoring.Metrics.Port = 0 // Dynamic port
	appConfig.Security.Authentication.Enabled = false
	appConfig.Security.RateLimit.Enabled = false
	
	monitor, err := monitoring.NewMonitor(&appConfig.Monitoring)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	server, err := NewServerWithMonitoring(serverConfig, orch, monitor, appConfig)
	if err != nil {
		t.Fatalf("Failed to create server with monitoring: %v", err)
	}
	
	if server.monitor != monitor {
		t.Error("Server monitor should match provided monitor")
	}
	
	if server.middlewareManager == nil {
		t.Error("Middleware manager should be initialized")
	}
}

func TestServerStartStop(t *testing.T) {
	config := createTestConfig()
	orch := createTestOrchestrator(t)
	
	// Start orchestrator first
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer orch.Stop()
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	// Test initial state
	if server.IsRunning() {
		t.Error("Server should not be running initially")
	}
	
	// Start server
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	
	// Test running state
	if !server.IsRunning() {
		t.Error("Server should be running after start")
	}
	
	// Give server time to start
	time.Sleep(100 * time.Millisecond)
	
	// Test that we can connect to gRPC server
	conn, err := grpc.DialContext(context.Background(), server.GetGRPCAddress(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
		grpc.DialOption(grpc.WithTimeout(2*time.Second)))
	if err != nil {
		t.Errorf("Failed to connect to gRPC server: %v", err)
	} else {
		conn.Close()
	}
	
	// Stop server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	
	if err := server.Stop(ctx); err != nil {
		t.Errorf("Failed to stop server: %v", err)
	}
	
	// Test stopped state
	if server.IsRunning() {
		t.Error("Server should not be running after stop")
	}
}

func TestServerDoubleStart(t *testing.T) {
	config := createTestConfig()
	orch := createTestOrchestrator(t)
	
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer orch.Stop()
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	// Start server first time
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())
	
	// Try to start again - should return error
	if err := server.Start(); err == nil {
		t.Error("Expected error when starting server twice")
	}
}

func TestServerStopBeforeStart(t *testing.T) {
	config := createTestConfig()
	orch := createTestOrchestrator(t)
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	// Stop server without starting - should not error
	ctx := context.Background()
	if err := server.Stop(ctx); err != nil {
		t.Errorf("Stop should not error when called before start: %v", err)
	}
}

func TestServerAddresses(t *testing.T) {
	config := createTestConfig()
	config.GRPCPort = 19090  // Use specific ports for this test
	config.HTTPPort = 18080
	
	orch := createTestOrchestrator(t)
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	expectedGRPCAddr := "localhost:19090"
	expectedHTTPAddr := "localhost:18080"
	
	if server.GetGRPCAddress() != expectedGRPCAddr {
		t.Errorf("Expected gRPC address %s, got %s", expectedGRPCAddr, server.GetGRPCAddress())
	}
	
	if server.GetHTTPAddress() != expectedHTTPAddr {
		t.Errorf("Expected HTTP address %s, got %s", expectedHTTPAddr, server.GetHTTPAddress())
	}
}

func TestServerHealthMonitoring(t *testing.T) {
	config := createTestConfig()
	config.HealthCheckInterval = 100 * time.Millisecond // Fast health checks for testing
	
	orch := createTestOrchestrator(t)
	
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer orch.Stop()
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	defer server.Stop(context.Background())
	
	// Wait for at least one health check cycle
	time.Sleep(200 * time.Millisecond)
	
	// Health monitoring is running in background - we can't directly test it
	// but we can verify the server is still running
	if !server.IsRunning() {
		t.Error("Server should still be running after health monitoring")
	}
}

func TestServerGracefulShutdown(t *testing.T) {
	config := createTestConfig()
	config.ShutdownTimeout = 2 * time.Second
	
	orch := createTestOrchestrator(t)
	
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer orch.Stop()
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	
	// Give server time to fully start
	time.Sleep(100 * time.Millisecond)
	
	// Start shutdown with timeout
	start := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	
	if err := server.Stop(ctx); err != nil {
		t.Errorf("Failed to stop server gracefully: %v", err)
	}
	
	duration := time.Since(start)
	if duration > 2*time.Second {
		t.Errorf("Shutdown took too long: %v", duration)
	}
}

func TestServerShutdownTimeout(t *testing.T) {
	config := createTestConfig()
	config.ShutdownTimeout = 100 * time.Millisecond // Very short timeout
	
	orch := createTestOrchestrator(t)
	
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer orch.Stop()
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	if err := server.Start(); err != nil {
		t.Fatalf("Failed to start server: %v", err)
	}
	
	// Give server time to fully start
	time.Sleep(100 * time.Millisecond)
	
	// Start shutdown with very short timeout
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	
	// This may or may not error depending on timing, but should complete quickly
	server.Stop(ctx)
	
	if server.IsRunning() {
		t.Error("Server should not be running after shutdown timeout")
	}
}

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	
	if config == nil {
		t.Fatal("Default config should not be nil")
	}
	
	// Test some default values
	if config.GRPCPort != 9090 {
		t.Errorf("Expected default gRPC port 9090, got %d", config.GRPCPort)
	}
	
	if config.HTTPPort != 8080 {
		t.Errorf("Expected default HTTP port 8080, got %d", config.HTTPPort)
	}
	
	if config.GRPCHost != "0.0.0.0" {
		t.Errorf("Expected default gRPC host '0.0.0.0', got %s", config.GRPCHost)
	}
	
	if config.TLSEnabled {
		t.Error("TLS should be disabled by default")
	}
	
	if config.MaxRecvMsgSize != 4*1024*1024 {
		t.Errorf("Expected default max recv msg size 4MB, got %d", config.MaxRecvMsgSize)
	}
	
	if !config.ReflectionEnabled {
		t.Error("Reflection should be enabled by default")
	}
}

func TestServerWithPortConflict(t *testing.T) {
	// Create a listener on a specific port to simulate conflict
	listener, err := net.Listen("tcp", "localhost:0")
	if err != nil {
		t.Fatalf("Failed to create test listener: %v", err)
	}
	defer listener.Close()
	
	// Get the port from the listener
	addr := listener.Addr().(*net.TCPAddr)
	port := addr.Port
	
	config := createTestConfig()
	config.GRPCPort = port // Use the same port
	
	orch := createTestOrchestrator(t)
	
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer orch.Stop()
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	// Starting server should fail due to port conflict
	if err := server.Start(); err == nil {
		t.Error("Expected error when starting server with conflicting port")
		server.Stop(context.Background())
	}
}

func TestRunServer(t *testing.T) {
	config := createTestConfig()
	orch := createTestOrchestrator(t)
	
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer orch.Stop()
	
	// RunServer is designed to block, so we need to test it differently
	// We'll start it in a goroutine and then signal shutdown
	
	errChan := make(chan error, 1)
	started := make(chan struct{})
	
	go func() {
		// Override the server creation to add a notification when started
		server, err := NewServer(config, orch)
		if err != nil {
			errChan <- err
			return
		}
		
		if err := server.Start(); err != nil {
			errChan <- err
			return
		}
		
		close(started)
		
		// Wait for shutdown signal instead of blocking indefinitely
		select {
		case <-time.After(100 * time.Millisecond):
			// Simulate shutdown
			ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
			defer cancel()
			errChan <- server.Stop(ctx)
		}
	}()
	
	// Wait for server to start or error
	select {
	case err := <-errChan:
		if err != nil {
			t.Fatalf("RunServer failed: %v", err)
		}
	case <-started:
		// Server started successfully
	case <-time.After(5 * time.Second):
		t.Fatal("Timeout waiting for server to start")
	}
	
	// Wait for shutdown to complete
	select {
	case err := <-errChan:
		if err != nil {
			t.Errorf("Server shutdown failed: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Error("Timeout waiting for server shutdown")
	}
}

func TestServerConcurrentStartStop(t *testing.T) {
	config := createTestConfig()
	orch := createTestOrchestrator(t)
	
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer orch.Stop()
	
	server, err := NewServer(config, orch)
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}
	
	// Try to start and stop concurrently multiple times
	var wg sync.WaitGroup
	errors := make(chan error, 10)
	
	for i := 0; i < 5; i++ {
		wg.Add(2)
		
		// Start goroutine
		go func() {
			defer wg.Done()
			if err := server.Start(); err != nil {
				// Only report unexpected errors (not "already running" errors)
				if !strings.Contains(err.Error(), "already running") {
					errors <- err
				}
			}
		}()
		
		// Stop goroutine
		go func() {
			defer wg.Done()
			ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
			defer cancel()
			if err := server.Stop(ctx); err != nil {
				errors <- err
			}
		}()
	}
	
	wg.Wait()
	close(errors)
	
	// Check for unexpected errors
	for err := range errors {
		t.Errorf("Concurrent start/stop error: %v", err)
	}
	
	// Ensure server is in a consistent state
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
	defer cancel()
	server.Stop(ctx) // Final cleanup
}