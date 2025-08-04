// Package messagequeue provides interfaces for agent communication
package messagequeue

import (
    "context"
    "encoding/json"
    
    "github.com/rizome-dev/amq/pkg/client"
    "github.com/rizome-dev/amq/pkg/types"
)

// Message represents a message in the queue
type Message struct {
    ID        string                 `json:"id"`
    Topic     string                 `json:"topic"`
    From      string                 `json:"from"`       // Agent ID
    To        string                 `json:"to,omitempty"` // Agent ID or empty for broadcast
    Type      string                 `json:"type"`
    Payload   map[string]interface{} `json:"payload"`
    Timestamp int64                  `json:"timestamp"`
}

// MessageHandler is a callback for handling messages
type MessageHandler func(ctx context.Context, msg *Message) error

// MessageQueue defines the interface for agent message queue operations
type MessageQueue interface {
    // GetClient returns an AMQ client for an agent
    GetClient(agentID string, metadata map[string]string) (client.Client, error)
    
    // GetAsyncConsumer returns an async consumer for an agent
    GetAsyncConsumer(agentID string, metadata map[string]string) (client.AsyncConsumer, error)
    
    // CreateQueue creates a new queue/topic
    CreateQueue(ctx context.Context, name string) error
    
    // DeleteQueue removes a queue/topic
    DeleteQueue(ctx context.Context, name string) error
    
    // SendMessage sends a message directly between agents
    SendMessage(ctx context.Context, from, to string, message *Message) error
    
    // PublishTask publishes a task to a topic
    PublishTask(ctx context.Context, from, topic string, message *Message) error
    
    // GetQueueStats returns queue statistics
    GetQueueStats(ctx context.Context, queueName string) (*types.QueueStats, error)
    
    // ListQueues returns all queues
    ListQueues(ctx context.Context) ([]*types.Queue, error)
    
    // Close closes the message queue connection
    Close() error
}

// Config holds message queue configuration
type Config struct {
    StorePath         string // Path for BadgerDB storage
    WorkerPoolSize    int    // Number of workers per queue
    MessageTimeout    int    // Message timeout in seconds
}

// Factory creates message queue instances
type Factory interface {
    Create(config Config) (MessageQueue, error)
}

// MessageToPayload converts a Message to JSON bytes
func MessageToPayload(msg *Message) ([]byte, error) {
    return json.Marshal(msg)
}

// PayloadToMessage converts JSON bytes to a Message
func PayloadToMessage(payload []byte) (*Message, error) {
    var msg Message
    if err := json.Unmarshal(payload, &msg); err != nil {
        return nil, err
    }
    return &msg, nil
}