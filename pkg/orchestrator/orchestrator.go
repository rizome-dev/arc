// Package orchestrator provides the core orchestration engine
package orchestrator

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "fmt"
    "log"
    "sync"
    "time"

    "github.com/rizome-dev/amq/pkg/client"
    amqTypes "github.com/rizome-dev/amq/pkg/types"
    "github.com/rizome-dev/arc/pkg/messagequeue"
    "github.com/rizome-dev/arc/pkg/runtime"
    "github.com/rizome-dev/arc/pkg/state"
    "github.com/rizome-dev/arc/pkg/types"
)

// Orchestrator manages agent lifecycle and workflow execution
type Orchestrator struct {
    runtime      runtime.Runtime
    messageQueue messagequeue.MessageQueue
    stateManager state.StateManager
    workflows    map[string]*workflowExecution
    realTimeAgents map[string]*realTimeAgentExecution
    externalMessageQueues map[string]messagequeue.MessageQueue
    mu           sync.RWMutex
    rtaMu        sync.RWMutex // separate mutex for real-time agents
    ctx          context.Context
    cancel       context.CancelFunc
}

// workflowExecution tracks the execution state of a workflow
type workflowExecution struct {
    workflow *types.Workflow
    agents   map[string]*types.Agent // task ID -> agent
    cancel   context.CancelFunc
}

// realTimeAgentExecution tracks a standalone real-time agent
type realTimeAgentExecution struct {
    agent     *types.Agent
    consumer  client.AsyncConsumer
    messageHandler messagequeue.MessageHandler
    topics    []string
    status    types.AgentStatus
    ctx       context.Context
    cancel    context.CancelFunc
    paused    bool
    mu        sync.RWMutex
}

// RealTimeAgentConfig holds configuration for real-time agents
type RealTimeAgentConfig struct {
    Agent          *types.Agent
    Topics         []string
    MessageHandler messagequeue.MessageHandler
    ExternalAMQ    *ExternalAMQConfig // for external AMQ instances
}

// ExternalAMQConfig holds configuration for external AMQ instances
type ExternalAMQConfig struct {
    Name      string
    Endpoints []string
    Config    messagequeue.Config
}

// Config holds orchestrator configuration
type Config struct {
    Runtime      runtime.Runtime
    MessageQueue messagequeue.MessageQueue
    StateManager state.StateManager
}

// New creates a new orchestrator instance
func New(config Config) (*Orchestrator, error) {
    if config.Runtime == nil {
        return nil, fmt.Errorf("runtime is required")
    }
    if config.MessageQueue == nil {
        return nil, fmt.Errorf("message queue is required")
    }
    if config.StateManager == nil {
        return nil, fmt.Errorf("state manager is required")
    }

    ctx, cancel := context.WithCancel(context.Background())
    
    return &Orchestrator{
        runtime:      config.Runtime,
        messageQueue: config.MessageQueue,
        stateManager: config.StateManager,
        workflows:    make(map[string]*workflowExecution),
        realTimeAgents: make(map[string]*realTimeAgentExecution),
        externalMessageQueues: make(map[string]messagequeue.MessageQueue),
        ctx:          ctx,
        cancel:       cancel,
    }, nil
}

// Start starts the orchestrator
func (o *Orchestrator) Start() error {
    // Start monitoring goroutines
    go o.monitorAgents()
    go o.monitorRealTimeAgents()
    
    return nil
}

// Stop stops the orchestrator
func (o *Orchestrator) Stop() error {
    o.cancel()
    
    // Stop all running workflows
    o.mu.Lock()
    for _, exec := range o.workflows {
        exec.cancel()
    }
    o.mu.Unlock()
    
    // Stop all real-time agents
    o.rtaMu.Lock()
    for _, rtaExec := range o.realTimeAgents {
        rtaExec.cancel()
        if rtaExec.consumer != nil {
            _ = rtaExec.consumer.Stop()
        }
    }
    o.rtaMu.Unlock()
    
    // Close external message queues
    for _, mq := range o.externalMessageQueues {
        _ = mq.Close()
    }
    
    return o.messageQueue.Close()
}

// CreateWorkflow creates a new workflow
func (o *Orchestrator) CreateWorkflow(ctx context.Context, workflow *types.Workflow) error {
    // Validate workflow
    if err := o.validateWorkflow(workflow); err != nil {
        return fmt.Errorf("invalid workflow: %w", err)
    }
    
    // Set initial state
    workflow.Status = types.WorkflowStatusPending
    workflow.CreatedAt = time.Now()
    
    // Store workflow
    if err := o.stateManager.CreateWorkflow(ctx, workflow); err != nil {
        return fmt.Errorf("failed to create workflow: %w", err)
    }
    
    // Record event
    event := &types.Event{
        ID:        generateID(),
        Type:      types.EventTypeWorkflowStarted,
        Source:    "orchestrator",
        Timestamp: time.Now(),
        Data: map[string]interface{}{
            "workflow_id": workflow.ID,
            "name":        workflow.Name,
        },
    }
    
    return o.stateManager.RecordEvent(ctx, event)
}

// StartWorkflow starts executing a workflow
func (o *Orchestrator) StartWorkflow(ctx context.Context, workflowID string) error {
    // Get workflow from state
    workflow, err := o.stateManager.GetWorkflow(ctx, workflowID)
    if err != nil {
        return fmt.Errorf("failed to get workflow: %w", err)
    }
    
    if workflow.Status != types.WorkflowStatusPending {
        return fmt.Errorf("workflow is not in pending state")
    }
    
    // Create workflow execution
    execCtx, cancel := context.WithCancel(ctx)
    exec := &workflowExecution{
        workflow: workflow,
        agents:   make(map[string]*types.Agent),
        cancel:   cancel,
    }
    
    o.mu.Lock()
    o.workflows[workflowID] = exec
    o.mu.Unlock()
    
    // Update workflow status
    workflow.Status = types.WorkflowStatusRunning
    workflow.StartedAt = &[]time.Time{time.Now()}[0]
    
    if err := o.stateManager.UpdateWorkflow(ctx, workflow); err != nil {
        o.mu.Lock()
        delete(o.workflows, workflowID)
        o.mu.Unlock()
        cancel()
        return fmt.Errorf("failed to update workflow: %w", err)
    }
    
    // Start workflow execution
    go o.executeWorkflow(execCtx, exec)
    
    return nil
}

// StopWorkflow stops a running workflow
func (o *Orchestrator) StopWorkflow(ctx context.Context, workflowID string) error {
    o.mu.Lock()
    exec, exists := o.workflows[workflowID]
    if !exists {
        o.mu.Unlock()
        return fmt.Errorf("workflow not running")
    }
    o.mu.Unlock()
    
    // Cancel workflow execution
    exec.cancel()
    
    // Stop all agents
    for taskID, agent := range exec.agents {
        if err := o.runtime.StopAgent(ctx, agent.ID); err != nil {
            // Log error but continue
            fmt.Printf("failed to stop agent %s: %v\n", agent.ID, err)
        }
        
        // Update task status
        task := o.findTask(exec.workflow, taskID)
        if task != nil {
            task.Status = types.TaskStatusFailed
            task.Error = "workflow cancelled"
            _ = o.stateManager.UpdateTask(ctx, workflowID, task)
        }
    }
    
    // Update workflow status
    exec.workflow.Status = types.WorkflowStatusCancelled
    exec.workflow.CompletedAt = &[]time.Time{time.Now()}[0]
    
    return o.stateManager.UpdateWorkflow(ctx, exec.workflow)
}

// GetWorkflow returns workflow information
func (o *Orchestrator) GetWorkflow(ctx context.Context, workflowID string) (*types.Workflow, error) {
    return o.stateManager.GetWorkflow(ctx, workflowID)
}

// ListWorkflows returns all workflows
func (o *Orchestrator) ListWorkflows(ctx context.Context, filter map[string]string) ([]*types.Workflow, error) {
    return o.stateManager.ListWorkflows(ctx, filter)
}

// CreateRealTimeAgent creates a standalone real-time agent
func (o *Orchestrator) CreateRealTimeAgent(ctx context.Context, config RealTimeAgentConfig) error {
    if config.Agent == nil {
        return fmt.Errorf("agent configuration is required")
    }
    
    if config.Agent.ID == "" {
        config.Agent.ID = generateID()
    }
    
    if len(config.Topics) == 0 {
        return fmt.Errorf("at least one topic must be specified")
    }
    
    if config.MessageHandler == nil {
        return fmt.Errorf("message handler is required")
    }
    
    // Set agent status and timestamps
    config.Agent.Status = types.AgentStatusPending
    config.Agent.CreatedAt = time.Now()
    
    // Set default labels
    if config.Agent.Labels == nil {
        config.Agent.Labels = make(map[string]string)
    }
    config.Agent.Labels["type"] = "realtime"
    config.Agent.Labels["orchestrator"] = "arc"
    
    // Store agent
    if err := o.stateManager.CreateAgent(ctx, config.Agent); err != nil {
        return fmt.Errorf("failed to create agent: %w", err)
    }
    
    // Record agent creation event
    event := &types.Event{
        ID:        generateID(),
        Type:      types.EventTypeAgentCreated,
        Source:    "orchestrator",
        Timestamp: time.Now(),
        Data: map[string]interface{}{
            "agent_id": config.Agent.ID,
            "name":     config.Agent.Name,
            "topics":   config.Topics,
            "type":     "realtime",
        },
    }
    
    if err := o.stateManager.RecordEvent(ctx, event); err != nil {
        log.Printf("Failed to record agent creation event: %v", err)
    }
    
    // Register external AMQ if provided
    if config.ExternalAMQ != nil {
        if err := o.registerExternalAMQ(config.ExternalAMQ); err != nil {
            return fmt.Errorf("failed to register external AMQ: %w", err)
        }
    }
    
    execCtx, cancel := context.WithCancel(o.ctx)
    rtaExec := &realTimeAgentExecution{
        agent:          config.Agent,
        messageHandler: config.MessageHandler,
        topics:         config.Topics,
        status:         types.AgentStatusPending,
        ctx:            execCtx,
        cancel:         cancel,
        paused:         false,
    }
    
    o.rtaMu.Lock()
    o.realTimeAgents[config.Agent.ID] = rtaExec
    o.rtaMu.Unlock()
    
    return nil
}

// StartRealTimeAgent starts a real-time agent to listen for messages
func (o *Orchestrator) StartRealTimeAgent(ctx context.Context, agentID string) error {
    o.rtaMu.Lock()
    rtaExec, exists := o.realTimeAgents[agentID]
    if !exists {
        o.rtaMu.Unlock()
        return fmt.Errorf("real-time agent not found: %s", agentID)
    }
    o.rtaMu.Unlock()
    
    rtaExec.mu.Lock()
    defer rtaExec.mu.Unlock()
    
    if rtaExec.status == types.AgentStatusRunning {
        return fmt.Errorf("agent is already running")
    }
    
    // Create the agent container
    if err := o.runtime.CreateAgent(ctx, rtaExec.agent); err != nil {
        rtaExec.status = types.AgentStatusFailed
        rtaExec.agent.Status = types.AgentStatusFailed
        rtaExec.agent.Error = err.Error()
        _ = o.stateManager.UpdateAgent(ctx, rtaExec.agent)
        return fmt.Errorf("failed to create agent container: %w", err)
    }
    
    // Start the agent container
    if err := o.runtime.StartAgent(ctx, rtaExec.agent.ID); err != nil {
        rtaExec.status = types.AgentStatusFailed
        rtaExec.agent.Status = types.AgentStatusFailed
        rtaExec.agent.Error = err.Error()
        _ = o.stateManager.UpdateAgent(ctx, rtaExec.agent)
        return fmt.Errorf("failed to start agent container: %w", err)
    }
    
    // Determine which message queue to use
    var mq messagequeue.MessageQueue = o.messageQueue
    if rtaExec.agent.Config.MessageQueue.Brokers != nil && len(rtaExec.agent.Config.MessageQueue.Brokers) > 0 {
        // Use external AMQ if brokers are specified
        externalName := fmt.Sprintf("external-%s", rtaExec.agent.ID)
        if externalMQ, exists := o.externalMessageQueues[externalName]; exists {
            mq = externalMQ
        }
    }
    
    // Create async consumer for the agent
    metadata := map[string]string{
        "agent_id":   rtaExec.agent.ID,
        "agent_name": rtaExec.agent.Name,
        "type":       "realtime",
    }
    
    consumer, err := mq.GetAsyncConsumer(rtaExec.agent.ID, metadata)
    if err != nil {
        rtaExec.status = types.AgentStatusFailed
        rtaExec.agent.Status = types.AgentStatusFailed
        rtaExec.agent.Error = err.Error()
        _ = o.stateManager.UpdateAgent(ctx, rtaExec.agent)
        return fmt.Errorf("failed to create consumer: %w", err)
    }
    
    rtaExec.consumer = consumer
    
    // Subscribe to topics
    if err := consumer.Subscribe(ctx, rtaExec.topics...); err != nil {
        rtaExec.status = types.AgentStatusFailed
        rtaExec.agent.Status = types.AgentStatusFailed
        rtaExec.agent.Error = fmt.Sprintf("failed to subscribe to topics %v: %v", rtaExec.topics, err)
        _ = o.stateManager.UpdateAgent(ctx, rtaExec.agent)
        _ = consumer.Stop()
        return err
    }
    
    // Update agent status
    now := time.Now()
    rtaExec.status = types.AgentStatusRunning
    rtaExec.agent.Status = types.AgentStatusRunning
    rtaExec.agent.StartedAt = &now
    
    if err := o.stateManager.UpdateAgent(ctx, rtaExec.agent); err != nil {
        log.Printf("Failed to update agent status: %v", err)
    }
    
    // Start async message consumption
    consumerOpts := client.ConsumerOptions{
        MaxConcurrency: 5,
        BatchSize:      10,
        AutoAck:        true,
    }
    
    if err := consumer.Start(o.createMessageHandlerWrapper(rtaExec), consumerOpts); err != nil {
        rtaExec.status = types.AgentStatusFailed
        rtaExec.agent.Status = types.AgentStatusFailed
        rtaExec.agent.Error = fmt.Sprintf("failed to start consumer: %v", err)
        _ = o.stateManager.UpdateAgent(ctx, rtaExec.agent)
        _ = consumer.Stop()
        return err
    }
    
    // Record agent start event
    event := &types.Event{
        ID:        generateID(),
        Type:      types.EventTypeAgentStarted,
        Source:    "orchestrator",
        Timestamp: time.Now(),
        Data: map[string]interface{}{
            "agent_id": rtaExec.agent.ID,
            "topics":   rtaExec.topics,
        },
    }
    
    if err := o.stateManager.RecordEvent(ctx, event); err != nil {
        log.Printf("Failed to record agent start event: %v", err)
    }
    
    return nil
}

// StopRealTimeAgent stops a real-time agent
func (o *Orchestrator) StopRealTimeAgent(ctx context.Context, agentID string) error {
    o.rtaMu.Lock()
    rtaExec, exists := o.realTimeAgents[agentID]
    if !exists {
        o.rtaMu.Unlock()
        return fmt.Errorf("real-time agent not found: %s", agentID)
    }
    o.rtaMu.Unlock()
    
    rtaExec.mu.Lock()
    defer rtaExec.mu.Unlock()
    
    if rtaExec.status != types.AgentStatusRunning {
        return fmt.Errorf("agent is not running")
    }
    
    // Cancel the context to stop message processing
    rtaExec.cancel()
    
    // Stop consumer
    if rtaExec.consumer != nil {
        if err := rtaExec.consumer.Stop(); err != nil {
            log.Printf("Failed to stop consumer for agent %s: %v", agentID, err)
        }
    }
    
    // Stop the agent container
    if err := o.runtime.StopAgent(ctx, agentID); err != nil {
        log.Printf("Failed to stop agent container %s: %v", agentID, err)
    }
    
    // Update agent status
    now := time.Now()
    rtaExec.status = types.AgentStatusTerminated
    rtaExec.agent.Status = types.AgentStatusTerminated
    rtaExec.agent.CompletedAt = &now
    
    if err := o.stateManager.UpdateAgent(ctx, rtaExec.agent); err != nil {
        log.Printf("Failed to update agent status: %v", err)
    }
    
    // Record agent stop event
    event := &types.Event{
        ID:        generateID(),
        Type:      "agent.stopped",
        Source:    "orchestrator",
        Timestamp: time.Now(),
        Data: map[string]interface{}{
            "agent_id": agentID,
        },
    }
    
    if err := o.stateManager.RecordEvent(ctx, event); err != nil {
        log.Printf("Failed to record agent stop event: %v", err)
    }
    
    return nil
}

// PauseRealTimeAgent pauses message processing for a real-time agent
func (o *Orchestrator) PauseRealTimeAgent(ctx context.Context, agentID string) error {
    o.rtaMu.RLock()
    rtaExec, exists := o.realTimeAgents[agentID]
    if !exists {
        o.rtaMu.RUnlock()
        return fmt.Errorf("real-time agent not found: %s", agentID)
    }
    o.rtaMu.RUnlock()
    
    rtaExec.mu.Lock()
    defer rtaExec.mu.Unlock()
    
    if rtaExec.status != types.AgentStatusRunning {
        return fmt.Errorf("agent is not running")
    }
    
    rtaExec.paused = true
    
    // Record pause event
    event := &types.Event{
        ID:        generateID(),
        Type:      "agent.paused",
        Source:    "orchestrator",
        Timestamp: time.Now(),
        Data: map[string]interface{}{
            "agent_id": agentID,
        },
    }
    
    if err := o.stateManager.RecordEvent(ctx, event); err != nil {
        log.Printf("Failed to record agent pause event: %v", err)
    }
    
    return nil
}

// ResumeRealTimeAgent resumes message processing for a real-time agent
func (o *Orchestrator) ResumeRealTimeAgent(ctx context.Context, agentID string) error {
    o.rtaMu.RLock()
    rtaExec, exists := o.realTimeAgents[agentID]
    if !exists {
        o.rtaMu.RUnlock()
        return fmt.Errorf("real-time agent not found: %s", agentID)
    }
    o.rtaMu.RUnlock()
    
    rtaExec.mu.Lock()
    defer rtaExec.mu.Unlock()
    
    if rtaExec.status != types.AgentStatusRunning {
        return fmt.Errorf("agent is not running")
    }
    
    rtaExec.paused = false
    
    // Record resume event
    event := &types.Event{
        ID:        generateID(),
        Type:      "agent.resumed",
        Source:    "orchestrator",
        Timestamp: time.Now(),
        Data: map[string]interface{}{
            "agent_id": agentID,
        },
    }
    
    if err := o.stateManager.RecordEvent(ctx, event); err != nil {
        log.Printf("Failed to record agent resume event: %v", err)
    }
    
    return nil
}

// GetRealTimeAgent returns information about a real-time agent
func (o *Orchestrator) GetRealTimeAgent(ctx context.Context, agentID string) (*types.Agent, error) {
    o.rtaMu.RLock()
    rtaExec, exists := o.realTimeAgents[agentID]
    if !exists {
        o.rtaMu.RUnlock()
        return nil, fmt.Errorf("real-time agent not found: %s", agentID)
    }
    o.rtaMu.RUnlock()
    
    // Return a copy to prevent external modifications
    agent := *rtaExec.agent
    return &agent, nil
}

// ListRealTimeAgents returns all real-time agents
func (o *Orchestrator) ListRealTimeAgents(ctx context.Context) ([]*types.Agent, error) {
    o.rtaMu.RLock()
    defer o.rtaMu.RUnlock()
    
    agents := make([]*types.Agent, 0, len(o.realTimeAgents))
    for _, rtaExec := range o.realTimeAgents {
        // Return copies to prevent external modifications
        agent := *rtaExec.agent
        agents = append(agents, &agent)
    }
    
    return agents, nil
}

// SendMessageToRealTimeAgent sends a message directly to a real-time agent
func (o *Orchestrator) SendMessageToRealTimeAgent(ctx context.Context, agentID string, message *messagequeue.Message) error {
    o.rtaMu.RLock()
    rtaExec, exists := o.realTimeAgents[agentID]
    if !exists {
        o.rtaMu.RUnlock()
        return fmt.Errorf("real-time agent not found: %s", agentID)
    }
    o.rtaMu.RUnlock()
    
    if rtaExec.status != types.AgentStatusRunning {
        return fmt.Errorf("agent is not running")
    }
    
    // Determine which message queue to use
    var mq messagequeue.MessageQueue = o.messageQueue
    if rtaExec.agent.Config.MessageQueue.Brokers != nil && len(rtaExec.agent.Config.MessageQueue.Brokers) > 0 {
        externalName := fmt.Sprintf("external-%s", rtaExec.agent.ID)
        if externalMQ, exists := o.externalMessageQueues[externalName]; exists {
            mq = externalMQ
        }
    }
    
    return mq.SendMessage(ctx, "orchestrator", agentID, message)
}

// RegisterExternalAMQ registers an external AMQ instance
func (o *Orchestrator) RegisterExternalAMQ(config *ExternalAMQConfig) error {
    return o.registerExternalAMQ(config)
}

// executeWorkflow executes a workflow
func (o *Orchestrator) executeWorkflow(ctx context.Context, exec *workflowExecution) {
    defer func() {
        o.mu.Lock()
        delete(o.workflows, exec.workflow.ID)
        o.mu.Unlock()
    }()
    
    // Create queues for workflow communication
    if err := o.createWorkflowQueues(ctx, exec.workflow); err != nil {
        o.handleWorkflowError(ctx, exec, fmt.Errorf("failed to create queues: %w", err))
        return
    }
    
    // Execute tasks
    if err := o.executeTasks(ctx, exec); err != nil {
        o.handleWorkflowError(ctx, exec, err)
        return
    }
    
    // Mark workflow as completed
    exec.workflow.Status = types.WorkflowStatusCompleted
    exec.workflow.CompletedAt = &[]time.Time{time.Now()}[0]
    
    if err := o.stateManager.UpdateWorkflow(ctx, exec.workflow); err != nil {
        fmt.Printf("failed to update workflow status: %v\n", err)
    }
    
    // Record completion event
    event := &types.Event{
        ID:        generateID(),
        Type:      types.EventTypeWorkflowCompleted,
        Source:    "orchestrator",
        Timestamp: time.Now(),
        Data: map[string]interface{}{
            "workflow_id": exec.workflow.ID,
            "duration":    exec.workflow.CompletedAt.Sub(exec.workflow.CreatedAt).String(),
        },
    }
    
    _ = o.stateManager.RecordEvent(ctx, event)
}

// executeTasks executes workflow tasks respecting dependencies
func (o *Orchestrator) executeTasks(ctx context.Context, exec *workflowExecution) error {
    // Create task dependency graph
    graph := o.buildDependencyGraph(exec.workflow)
    
    // Execute tasks in topological order
    completed := make(map[string]bool)
    
    for len(completed) < len(exec.workflow.Tasks) {
        // Find ready tasks
        readyTasks := o.findReadyTasks(exec.workflow, graph, completed)
        
        if len(readyTasks) == 0 {
            // Check for circular dependencies
            if len(completed) < len(exec.workflow.Tasks) {
                return fmt.Errorf("circular dependency detected")
            }
            break
        }
        
        // Execute ready tasks in parallel
        var wg sync.WaitGroup
        errors := make(chan error, len(readyTasks))
        
        for _, task := range readyTasks {
            wg.Add(1)
            go func(t types.Task) {
                defer wg.Done()
                if err := o.executeTask(ctx, exec, &t); err != nil {
                    errors <- fmt.Errorf("task %s failed: %w", t.ID, err)
                } else {
                    completed[t.ID] = true
                }
            }(task)
        }
        
        wg.Wait()
        close(errors)
        
        // Check for errors
        for err := range errors {
            if err != nil {
                return err
            }
        }
    }
    
    return nil
}

// executeTask executes a single task
func (o *Orchestrator) executeTask(ctx context.Context, exec *workflowExecution, task *types.Task) error {
    // Update task status
    task.Status = types.TaskStatusRunning
    task.StartedAt = &[]time.Time{time.Now()}[0]
    
    if err := o.stateManager.UpdateTask(ctx, exec.workflow.ID, task); err != nil {
        return fmt.Errorf("failed to update task status: %w", err)
    }
    
    // Create agent for task
    agent := &types.Agent{
        ID:        generateID(),
        Name:      fmt.Sprintf("%s-%s", exec.workflow.Name, task.Name),
        Image:     task.AgentConfig.Image,
        Status:    types.AgentStatusPending,
        Config:    task.AgentConfig,
        CreatedAt: time.Now(),
        Labels: map[string]string{
            "workflow_id": exec.workflow.ID,
            "task_id":     task.ID,
        },
    }
    
    // Store agent
    if err := o.stateManager.CreateAgent(ctx, agent); err != nil {
        return fmt.Errorf("failed to create agent: %w", err)
    }
    
    task.AgentID = agent.ID
    exec.agents[task.ID] = agent
    
    // Create agent container
    if err := o.runtime.CreateAgent(ctx, agent); err != nil {
        task.Status = types.TaskStatusFailed
        task.Error = err.Error()
        _ = o.stateManager.UpdateTask(ctx, exec.workflow.ID, task)
        return err
    }
    
    // Start agent
    if err := o.runtime.StartAgent(ctx, agent.ID); err != nil {
        task.Status = types.TaskStatusFailed
        task.Error = err.Error()
        _ = o.stateManager.UpdateTask(ctx, exec.workflow.ID, task)
        return err
    }
    
    // Wait for task completion
    if err := o.waitForTaskCompletion(ctx, agent, task); err != nil {
        task.Status = types.TaskStatusFailed
        task.Error = err.Error()
        _ = o.stateManager.UpdateTask(ctx, exec.workflow.ID, task)
        return err
    }
    
    // Task completed successfully
    task.Status = types.TaskStatusCompleted
    task.CompletedAt = &[]time.Time{time.Now()}[0]
    
    return o.stateManager.UpdateTask(ctx, exec.workflow.ID, task)
}

// Helper functions

func (o *Orchestrator) validateWorkflow(workflow *types.Workflow) error {
    if workflow.ID == "" {
        workflow.ID = generateID()
    }
    
    if workflow.Name == "" {
        return fmt.Errorf("workflow name is required")
    }
    
    if len(workflow.Tasks) == 0 {
        return fmt.Errorf("workflow must have at least one task")
    }
    
    // Validate tasks
    taskIDs := make(map[string]bool)
    for i := range workflow.Tasks {
        task := &workflow.Tasks[i]
        
        if task.ID == "" {
            task.ID = generateID()
        }
        
        if task.Name == "" {
            return fmt.Errorf("task name is required")
        }
        
        if taskIDs[task.ID] {
            return fmt.Errorf("duplicate task ID: %s", task.ID)
        }
        taskIDs[task.ID] = true
        
        // Validate dependencies
        for _, dep := range task.Dependencies {
            if !taskIDs[dep] {
                return fmt.Errorf("task %s depends on unknown task %s", task.ID, dep)
            }
        }
        
        // Set defaults
        task.Status = types.TaskStatusPending
        if task.MaxRetries == 0 {
            task.MaxRetries = 3
        }
        if task.Timeout == 0 {
            task.Timeout = 5 * time.Minute
        }
    }
    
    return nil
}

func (o *Orchestrator) buildDependencyGraph(workflow *types.Workflow) map[string][]string {
    graph := make(map[string][]string)
    
    for _, task := range workflow.Tasks {
        graph[task.ID] = task.Dependencies
    }
    
    return graph
}

func (o *Orchestrator) findReadyTasks(workflow *types.Workflow, graph map[string][]string, completed map[string]bool) []types.Task {
    var ready []types.Task
    
    for _, task := range workflow.Tasks {
        if completed[task.ID] || task.Status == types.TaskStatusRunning {
            continue
        }
        
        // Check if all dependencies are completed
        allDepsCompleted := true
        for _, dep := range graph[task.ID] {
            if !completed[dep] {
                allDepsCompleted = false
                break
            }
        }
        
        if allDepsCompleted {
            ready = append(ready, task)
        }
    }
    
    return ready
}

func (o *Orchestrator) findTask(workflow *types.Workflow, taskID string) *types.Task {
    for i := range workflow.Tasks {
        if workflow.Tasks[i].ID == taskID {
            return &workflow.Tasks[i]
        }
    }
    return nil
}

func (o *Orchestrator) createWorkflowQueues(ctx context.Context, workflow *types.Workflow) error {
    // Create workflow-specific queues
    queueName := fmt.Sprintf("workflow-%s", workflow.ID)
    return o.messageQueue.CreateQueue(ctx, queueName)
}

func (o *Orchestrator) waitForTaskCompletion(ctx context.Context, agent *types.Agent, task *types.Task) error {
    ticker := time.NewTicker(5 * time.Second)
    defer ticker.Stop()
    
    timeout := time.After(task.Timeout)
    
    for {
        select {
        case <-ctx.Done():
            return ctx.Err()
        case <-timeout:
            return fmt.Errorf("task timeout")
        case <-ticker.C:
            // Check agent status
            status, err := o.runtime.GetAgentStatus(ctx, agent.ID)
            if err != nil {
                return fmt.Errorf("failed to get agent status: %w", err)
            }
            
            switch status.Status {
            case types.AgentStatusCompleted:
                return nil
            case types.AgentStatusFailed:
                return fmt.Errorf("agent failed: %s", status.Error)
            case types.AgentStatusTerminated:
                return fmt.Errorf("agent terminated unexpectedly")
            }
        }
    }
}

func (o *Orchestrator) handleWorkflowError(ctx context.Context, exec *workflowExecution, err error) {
    exec.workflow.Status = types.WorkflowStatusFailed
    exec.workflow.Error = err.Error()
    exec.workflow.CompletedAt = &[]time.Time{time.Now()}[0]
    
    _ = o.stateManager.UpdateWorkflow(ctx, exec.workflow)
    
    // Record failure event
    event := &types.Event{
        ID:        generateID(),
        Type:      types.EventTypeWorkflowFailed,
        Source:    "orchestrator",
        Timestamp: time.Now(),
        Data: map[string]interface{}{
            "workflow_id": exec.workflow.ID,
            "error":       err.Error(),
        },
    }
    
    _ = o.stateManager.RecordEvent(ctx, event)
}

func (o *Orchestrator) monitorAgents() {
    ticker := time.NewTicker(10 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-o.ctx.Done():
            return
        case <-ticker.C:
            o.checkAgentHealth()
        }
    }
}

func (o *Orchestrator) checkAgentHealth() {
    ctx := context.Background()
    
    // Get all running workflows
    o.mu.RLock()
    workflowsCopy := make(map[string]*workflowExecution)
    for id, exec := range o.workflows {
        workflowsCopy[id] = exec
    }
    o.mu.RUnlock()
    
    // Check health of all agents in running workflows
    for workflowID, exec := range workflowsCopy {
        for taskID, agent := range exec.agents {
            // Skip if agent is not running
            if agent.Status != types.AgentStatusRunning {
                continue
            }
            
            // Get current agent status from runtime
            status, err := o.runtime.GetAgentStatus(ctx, agent.ID)
            if err != nil {
                // Log error and mark agent as unhealthy
                event := &types.Event{
                    ID:        generateID(),
                    Type:      types.EventTypeAgentHealthCheckFailed,
                    Source:    "orchestrator",
                    Timestamp: time.Now(),
                    Data: map[string]interface{}{
                        "agent_id":    agent.ID,
                        "workflow_id": workflowID,
                        "task_id":     taskID,
                        "error":       err.Error(),
                    },
                }
                _ = o.stateManager.RecordEvent(ctx, event)
                
                // Mark agent as failed
                agent.Status = types.AgentStatusFailed
                agent.Error = fmt.Sprintf("health check failed: %v", err)
                _ = o.stateManager.UpdateAgent(ctx, agent)
                
                // Update task status
                task := o.findTask(exec.workflow, taskID)
                if task != nil {
                    task.Status = types.TaskStatusFailed
                    task.Error = fmt.Sprintf("agent health check failed: %v", err)
                    _ = o.stateManager.UpdateTask(ctx, workflowID, task)
                }
                continue
            }
            
            // Check if status has changed
            if status.Status != agent.Status {
                previousStatus := agent.Status
                agent.Status = status.Status
                agent.Error = status.Error
                
                // Update agent in state
                _ = o.stateManager.UpdateAgent(ctx, agent)
                
                // Record status change event
                event := &types.Event{
                    ID:        generateID(),
                    Type:      types.EventTypeAgentStatusChanged,
                    Source:    "orchestrator",
                    Timestamp: time.Now(),
                    Data: map[string]interface{}{
                        "agent_id":        agent.ID,
                        "workflow_id":     workflowID,
                        "task_id":         taskID,
                        "previous_status": previousStatus,
                        "new_status":      status.Status,
                    },
                }
                _ = o.stateManager.RecordEvent(ctx, event)
                
                // Handle status-specific actions
                switch status.Status {
                case types.AgentStatusFailed, types.AgentStatusTerminated:
                    // Update task status
                    task := o.findTask(exec.workflow, taskID)
                    if task != nil {
                        task.Status = types.TaskStatusFailed
                        task.Error = status.Error
                        task.CompletedAt = &[]time.Time{time.Now()}[0]
                        _ = o.stateManager.UpdateTask(ctx, workflowID, task)
                        
                        // Check if task has retries remaining
                        if task.RetryCount < task.MaxRetries {
                            task.RetryCount++
                            task.Status = types.TaskStatusPending
                            task.Error = ""
                            task.CompletedAt = nil
                            _ = o.stateManager.UpdateTask(ctx, workflowID, task)
                            
                            // Record retry event
                            retryEvent := &types.Event{
                                ID:        generateID(),
                                Type:      types.EventTypeTaskRetrying,
                                Source:    "orchestrator",
                                Timestamp: time.Now(),
                                Data: map[string]interface{}{
                                    "workflow_id":  workflowID,
                                    "task_id":      taskID,
                                    "retry_count":  task.RetryCount,
                                    "max_retries":  task.MaxRetries,
                                    "prev_error":   status.Error,
                                },
                            }
                            _ = o.stateManager.RecordEvent(ctx, retryEvent)
                            
                            // Re-execute the task
                            go func() {
                                time.Sleep(5 * time.Second) // Back-off before retry
                                _ = o.executeTask(ctx, exec, task)
                            }()
                        }
                    }
                    
                case types.AgentStatusCompleted:
                    // Update task status
                    task := o.findTask(exec.workflow, taskID)
                    if task != nil {
                        task.Status = types.TaskStatusCompleted
                        task.CompletedAt = status.CompletedAt
                        _ = o.stateManager.UpdateTask(ctx, workflowID, task)
                    }
                }
            }
            
            // Check for resource constraints violations
            if agent.Config.Resources.CPU != "" || agent.Config.Resources.Memory != "" {
                // This would require runtime-specific metrics collection
                // For now, just record that we checked
                event := &types.Event{
                    ID:        generateID(),
                    Type:      types.EventTypeAgentHealthChecked,
                    Source:    "orchestrator",
                    Timestamp: time.Now(),
                    Data: map[string]interface{}{
                        "agent_id":    agent.ID,
                        "workflow_id": workflowID,
                        "task_id":     taskID,
                        "status":      status.Status,
                    },
                }
                _ = o.stateManager.RecordEvent(ctx, event)
            }
        }
    }
}



// Real-time agent helper methods

// registerExternalAMQ registers an external AMQ instance
func (o *Orchestrator) registerExternalAMQ(config *ExternalAMQConfig) error {
    if config == nil {
        return fmt.Errorf("external AMQ config is required")
    }
    
    if config.Name == "" {
        return fmt.Errorf("external AMQ name is required")
    }
    
    if len(config.Endpoints) == 0 {
        return fmt.Errorf("at least one endpoint must be specified")
    }
    
    // Create external AMQ instance
    // For now, we use the same AMQ implementation but with different configuration
    externalAMQ, err := messagequeue.NewAMQMessageQueue(config.Config)
    if err != nil {
        return fmt.Errorf("failed to create external AMQ instance: %w", err)
    }
    
    o.externalMessageQueues[config.Name] = externalAMQ
    
    log.Printf("Registered external AMQ: %s with endpoints: %v", config.Name, config.Endpoints)
    return nil
}

// createMessageHandlerWrapper creates a wrapper for the AMQ AsyncConsumer that handles our message format
func (o *Orchestrator) createMessageHandlerWrapper(rtaExec *realTimeAgentExecution) client.MessageHandler {
    return func(ctx context.Context, amqMsg *amqTypes.Message) error {
        // Check if agent is paused
        rtaExec.mu.RLock()
        paused := rtaExec.paused
        rtaExec.mu.RUnlock()
        
        if paused {
            // Return error to not acknowledge message when paused
            return fmt.Errorf("agent is paused")
        }
        
        // Convert AMQ message to our message format
        arcMessage, err := messagequeue.PayloadToMessage(amqMsg.Payload)
        if err != nil {
            log.Printf("Failed to parse message for agent %s: %v", rtaExec.agent.ID, err)
            return err
        }
        
        // Record message received event
        event := &types.Event{
            ID:        generateID(),
            Type:      types.EventTypeMessageReceived,
            Source:    "orchestrator",
            Timestamp: time.Now(),
            Data: map[string]interface{}{
                "agent_id":   rtaExec.agent.ID,
                "message_id": arcMessage.ID,
                "topic":      arcMessage.Topic,
                "from":       arcMessage.From,
            },
        }
        _ = o.stateManager.RecordEvent(ctx, event)
        
        // Handle the message with timing
        startTime := time.Now()
        err = rtaExec.messageHandler(ctx, arcMessage)
        
        // Record processing result
        var eventType types.EventType
        eventData := map[string]interface{}{
            "agent_id":      rtaExec.agent.ID,
            "message_id":    arcMessage.ID,
            "topic":         arcMessage.Topic,
            "from":          arcMessage.From,
            "processing_ms": time.Since(startTime).Milliseconds(),
        }
        
        if err != nil {
            eventType = types.EventTypeMessageFailed
            eventData["error"] = err.Error()
            log.Printf("Message processing failed for agent %s: %v", rtaExec.agent.ID, err)
        } else {
            eventType = types.EventTypeMessageProcessed
        }
        
        recordEvent := &types.Event{
            ID:        generateID(),
            Type:      eventType,
            Source:    "orchestrator",
            Timestamp: time.Now(),
            Data:      eventData,
        }
        _ = o.stateManager.RecordEvent(ctx, recordEvent)
        
        return err
    }
}

// monitorRealTimeAgents monitors the health of real-time agents
func (o *Orchestrator) monitorRealTimeAgents() {
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()
    
    for {
        select {
        case <-o.ctx.Done():
            return
        case <-ticker.C:
            o.checkRealTimeAgentHealth()
        }
    }
}

// checkRealTimeAgentHealth checks the health of all real-time agents
func (o *Orchestrator) checkRealTimeAgentHealth() {
    ctx := context.Background()
    
    o.rtaMu.RLock()
    agentsCopy := make(map[string]*realTimeAgentExecution)
    for id, exec := range o.realTimeAgents {
        agentsCopy[id] = exec
    }
    o.rtaMu.RUnlock()
    
    for agentID, rtaExec := range agentsCopy {
        rtaExec.mu.RLock()
        status := rtaExec.status
        rtaExec.mu.RUnlock()
        
        if status != types.AgentStatusRunning {
            continue
        }
        
        // Check agent status from runtime
        runtimeStatus, err := o.runtime.GetAgentStatus(ctx, agentID)
        if err != nil {
            log.Printf("Health check failed for real-time agent %s: %v", agentID, err)
            
            rtaExec.mu.Lock()
            rtaExec.status = types.AgentStatusFailed
            rtaExec.agent.Status = types.AgentStatusFailed
            rtaExec.agent.Error = fmt.Sprintf("health check failed: %v", err)
            rtaExec.mu.Unlock()
            
            _ = o.stateManager.UpdateAgent(ctx, rtaExec.agent)
            
            // Record health check failure
            event := &types.Event{
                ID:        generateID(),
                Type:      types.EventTypeAgentHealthCheckFailed,
                Source:    "orchestrator",
                Timestamp: time.Now(),
                Data: map[string]interface{}{
                    "agent_id": agentID,
                    "error":    err.Error(),
                    "type":     "realtime",
                },
            }
            _ = o.stateManager.RecordEvent(ctx, event)
            continue
        }
        
        // Check if status has changed
        if runtimeStatus.Status != rtaExec.agent.Status {
            previousStatus := rtaExec.agent.Status
            
            rtaExec.mu.Lock()
            rtaExec.status = runtimeStatus.Status
            rtaExec.agent.Status = runtimeStatus.Status
            rtaExec.agent.Error = runtimeStatus.Error
            if runtimeStatus.CompletedAt != nil {
                rtaExec.agent.CompletedAt = runtimeStatus.CompletedAt
            }
            rtaExec.mu.Unlock()
            
            _ = o.stateManager.UpdateAgent(ctx, rtaExec.agent)
            
            // Record status change
            event := &types.Event{
                ID:        generateID(),
                Type:      types.EventTypeAgentStatusChanged,
                Source:    "orchestrator",
                Timestamp: time.Now(),
                Data: map[string]interface{}{
                    "agent_id":        agentID,
                    "previous_status": previousStatus,
                    "new_status":      runtimeStatus.Status,
                    "type":            "realtime",
                },
            }
            _ = o.stateManager.RecordEvent(ctx, event)
            
            // Handle specific status changes
            switch runtimeStatus.Status {
            case types.AgentStatusFailed, types.AgentStatusTerminated:
                log.Printf("Real-time agent %s has failed or terminated: %s", agentID, runtimeStatus.Error)
                // Stop consumer if it's still running
                if rtaExec.consumer != nil {
                    _ = rtaExec.consumer.Stop()
                }
                rtaExec.cancel()
            }
        }
        
        // Record successful health check
        event := &types.Event{
            ID:        generateID(),
            Type:      types.EventTypeAgentHealthChecked,
            Source:    "orchestrator",
            Timestamp: time.Now(),
            Data: map[string]interface{}{
                "agent_id": agentID,
                "status":   runtimeStatus.Status,
                "type":     "realtime",
            },
        }
        _ = o.stateManager.RecordEvent(ctx, event)
    }
}

// generateID generates a unique ID for events and other entities
func generateID() string {
    // Generate a random 4-byte suffix to ensure uniqueness
    b := make([]byte, 4)
    rand.Read(b)
    return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}
