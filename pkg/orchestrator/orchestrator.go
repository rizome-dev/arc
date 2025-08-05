// Package orchestrator provides the core orchestration engine
package orchestrator

import (
    "context"
    "crypto/rand"
    "encoding/hex"
    "fmt"
    "sync"
    "time"

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
    mu           sync.RWMutex
    ctx          context.Context
    cancel       context.CancelFunc
}

// workflowExecution tracks the execution state of a workflow
type workflowExecution struct {
    workflow *types.Workflow
    agents   map[string]*types.Agent // task ID -> agent
    cancel   context.CancelFunc
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
        ctx:          ctx,
        cancel:       cancel,
    }, nil
}

// Start starts the orchestrator
func (o *Orchestrator) Start() error {
    // Start monitoring goroutines
    go o.monitorAgents()
    
    return nil
}

// Stop stops the orchestrator
func (o *Orchestrator) Stop() error {
    o.cancel()
    
    // Stop all running workflows
    o.mu.Lock()
    defer o.mu.Unlock()
    
    for _, exec := range o.workflows {
        exec.cancel()
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
        Image:     task.AgentConfig.Command[0], // Assuming image is in command[0]
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



// generateID generates a unique ID for events and other entities
func generateID() string {
    // Generate a random 4-byte suffix to ensure uniqueness
    b := make([]byte, 4)
    rand.Read(b)
    return fmt.Sprintf("%d-%s", time.Now().UnixNano(), hex.EncodeToString(b))
}
