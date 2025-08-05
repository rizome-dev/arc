// Package types contains shared types for arc
package types

import (
    "time"
)

// Response represents a generic API response
type Response struct {
    Success bool        `json:"success"`
    Message string      `json:"message,omitempty"`
    Data    interface{} `json:"data,omitempty"`
}

// AgentStatus represents the current state of an agent
type AgentStatus string

const (
    AgentStatusPending    AgentStatus = "pending"
    AgentStatusCreating   AgentStatus = "creating"
    AgentStatusRunning    AgentStatus = "running"
    AgentStatusCompleted  AgentStatus = "completed"
    AgentStatusFailed     AgentStatus = "failed"
    AgentStatusTerminated AgentStatus = "terminated"
)

// Agent represents a container-based agent
type Agent struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Image       string            `json:"image"`
    Status      AgentStatus       `json:"status"`
    Config      AgentConfig       `json:"config"`
    CreatedAt   time.Time         `json:"created_at"`
    StartedAt   *time.Time        `json:"started_at,omitempty"`
    CompletedAt *time.Time        `json:"completed_at,omitempty"`
    Error       string            `json:"error,omitempty"`
    ContainerID string            `json:"container_id,omitempty"`
    Namespace   string            `json:"namespace,omitempty"` // For K8s
    Labels      map[string]string `json:"labels,omitempty"`
    Annotations map[string]string `json:"annotations,omitempty"`
}

// AgentConfig holds configuration for an agent
type AgentConfig struct {
    Image        string              `json:"image"`
    Environment  map[string]string   `json:"environment,omitempty"`
    Command      []string            `json:"command,omitempty"`
    Args         []string            `json:"args,omitempty"`
    Resources    ResourceRequirements `json:"resources,omitempty"`
    MessageQueue MessageQueueConfig   `json:"message_queue"`
    Volumes      []VolumeMount       `json:"volumes,omitempty"`
    WorkingDir   string              `json:"working_dir,omitempty"`
}

// ResourceRequirements specifies resource constraints
type ResourceRequirements struct {
    CPU    string `json:"cpu,omitempty"`    // e.g., "1000m" or "2"
    Memory string `json:"memory,omitempty"` // e.g., "512Mi" or "2Gi"
    GPU    string `json:"gpu,omitempty"`    // e.g., "1"
}

// VolumeMount represents a volume mount
type VolumeMount struct {
    Name      string `json:"name"`
    MountPath string `json:"mount_path"`
    ReadOnly  bool   `json:"read_only,omitempty"`
}

// MessageQueueConfig holds message queue configuration
type MessageQueueConfig struct {
    Topics      []string `json:"topics"`
    Brokers     []string `json:"brokers,omitempty"`
    Credentials string   `json:"credentials,omitempty"`
}

// WorkflowStatus represents the state of a workflow
type WorkflowStatus string

const (
    WorkflowStatusPending   WorkflowStatus = "pending"
    WorkflowStatusRunning   WorkflowStatus = "running"
    WorkflowStatusCompleted WorkflowStatus = "completed"
    WorkflowStatusFailed    WorkflowStatus = "failed"
    WorkflowStatusCancelled WorkflowStatus = "cancelled"
)

// Workflow represents a DAG of tasks
type Workflow struct {
    ID          string            `json:"id"`
    Name        string            `json:"name"`
    Description string            `json:"description,omitempty"`
    Status      WorkflowStatus    `json:"status"`
    Tasks       []Task            `json:"tasks"`
    CreatedAt   time.Time         `json:"created_at"`
    StartedAt   *time.Time        `json:"started_at,omitempty"`
    CompletedAt *time.Time        `json:"completed_at,omitempty"`
    Error       string            `json:"error,omitempty"`
    Metadata    map[string]string `json:"metadata,omitempty"`
}

// TaskStatus represents the state of a task
type TaskStatus string

const (
    TaskStatusPending   TaskStatus = "pending"
    TaskStatusRunning   TaskStatus = "running"
    TaskStatusCompleted TaskStatus = "completed"
    TaskStatusFailed    TaskStatus = "failed"
    TaskStatusSkipped   TaskStatus = "skipped"
)

// Task represents a unit of work in a workflow
type Task struct {
    ID           string            `json:"id"`
    Name         string            `json:"name"`
    AgentID      string            `json:"agent_id,omitempty"`
    Status       TaskStatus        `json:"status"`
    Dependencies []string          `json:"dependencies,omitempty"` // Task IDs this depends on
    AgentConfig  AgentConfig       `json:"agent_config"`
    RetryCount   int               `json:"retry_count,omitempty"`
    MaxRetries   int               `json:"max_retries,omitempty"`
    Timeout      time.Duration     `json:"timeout,omitempty"`
    StartedAt    *time.Time        `json:"started_at,omitempty"`
    CompletedAt  *time.Time        `json:"completed_at,omitempty"`
    Error        string            `json:"error,omitempty"`
    Context      map[string]string `json:"context,omitempty"` // Context to pass to agent
}

// Event represents a system event
type Event struct {
    ID        string    `json:"id"`
    Type      EventType `json:"type"`
    Source    string    `json:"source"`
    Timestamp time.Time `json:"timestamp"`
    Data      map[string]interface{} `json:"data"`
}

// EventType represents types of events
type EventType string

const (
    EventTypeAgentCreated            EventType = "agent.created"
    EventTypeAgentStarted            EventType = "agent.started"
    EventTypeAgentCompleted          EventType = "agent.completed"
    EventTypeAgentFailed             EventType = "agent.failed"
    EventTypeAgentHealthChecked      EventType = "agent.health_checked"
    EventTypeAgentHealthCheckFailed  EventType = "agent.health_check_failed"
    EventTypeAgentStatusChanged      EventType = "agent.status_changed"
    EventTypeWorkflowStarted         EventType = "workflow.started"
    EventTypeWorkflowCompleted       EventType = "workflow.completed"
    EventTypeWorkflowFailed          EventType = "workflow.failed"
    EventTypeTaskStarted             EventType = "task.started"
    EventTypeTaskCompleted           EventType = "task.completed"
    EventTypeTaskFailed              EventType = "task.failed"
    EventTypeTaskRetrying            EventType = "task.retrying"
    EventTypeMessageReceived         EventType = "message.received"
    EventTypeMessageProcessed        EventType = "message.processed"
    EventTypeMessageFailed           EventType = "message.failed"
)
