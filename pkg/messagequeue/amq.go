// Package messagequeue provides AMQ implementation
package messagequeue

import (
    "context"
    "fmt"
    "time"

    "github.com/rizome-dev/amq"
    "github.com/rizome-dev/amq/pkg/client"
    "github.com/rizome-dev/amq/pkg/types"
)

// AMQMessageQueue implements MessageQueue using the AMQ package
type AMQMessageQueue struct {
    amq     *amq.AMQ
    clients map[string]client.Client
}

// NewAMQMessageQueue creates a new AMQ-based message queue
func NewAMQMessageQueue(config Config) (*AMQMessageQueue, error) {
    amqConfig := amq.Config{
        StorePath:         config.StorePath,
        WorkerPoolSize:    config.WorkerPoolSize,
        MessageTimeout:    time.Duration(config.MessageTimeout) * time.Second,
        HeartbeatInterval: 30 * time.Second,
    }
    
    amqInstance, err := amq.New(amqConfig)
    if err != nil {
        return nil, fmt.Errorf("failed to create AMQ instance: %w", err)
    }
    
    return &AMQMessageQueue{
        amq:     amqInstance,
        clients: make(map[string]client.Client),
    }, nil
}

// GetClient returns an AMQ client for an agent
func (mq *AMQMessageQueue) GetClient(agentID string, metadata map[string]string) (client.Client, error) {
    // Check if client already exists
    if c, exists := mq.clients[agentID]; exists {
        return c, nil
    }
    
    // Create client options
    var opts []client.ClientOption
    for k, v := range metadata {
        opts = append(opts, client.WithMetadata(k, v))
    }
    
    // Create new client
    c, err := mq.amq.NewClient(agentID, opts...)
    if err != nil {
        return nil, fmt.Errorf("failed to create client: %w", err)
    }
    
    mq.clients[agentID] = c
    return c, nil
}

// GetAsyncConsumer returns an async consumer for an agent
func (mq *AMQMessageQueue) GetAsyncConsumer(agentID string, metadata map[string]string) (client.AsyncConsumer, error) {
    // Create client options
    var opts []client.ClientOption
    for k, v := range metadata {
        opts = append(opts, client.WithMetadata(k, v))
    }
    
    // Create async consumer
    consumer, err := mq.amq.NewAsyncConsumer(agentID, opts...)
    if err != nil {
        return nil, fmt.Errorf("failed to create async consumer: %w", err)
    }
    
    return consumer, nil
}

// CreateQueue creates a new queue/topic
func (mq *AMQMessageQueue) CreateQueue(ctx context.Context, name string) error {
    // Default to topic queue type for pub/sub pattern
    return mq.amq.CreateQueue(ctx, name, types.QueueTypeTopic)
}

// DeleteQueue removes a queue/topic
func (mq *AMQMessageQueue) DeleteQueue(ctx context.Context, name string) error {
    return mq.amq.DeleteQueue(ctx, name)
}

// SendMessage sends a message directly between agents
func (mq *AMQMessageQueue) SendMessage(ctx context.Context, from, to string, message *Message) error {
    // Convert message to payload
    payload, err := MessageToPayload(message)
    if err != nil {
        return fmt.Errorf("failed to serialize message: %w", err)
    }
    
    // Send direct message
    _, err = mq.amq.AdminSendDirect(ctx, from, to, payload)
    return err
}

// PublishTask publishes a task to a topic
func (mq *AMQMessageQueue) PublishTask(ctx context.Context, from, topic string, message *Message) error {
    // Convert message to payload
    payload, err := MessageToPayload(message)
    if err != nil {
        return fmt.Errorf("failed to serialize message: %w", err)
    }
    
    // Publish to topic
    _, err = mq.amq.AdminSubmitTask(ctx, from, topic, payload)
    return err
}

// GetQueueStats returns queue statistics
func (mq *AMQMessageQueue) GetQueueStats(ctx context.Context, queueName string) (*types.QueueStats, error) {
    return mq.amq.GetQueueStats(ctx, queueName)
}

// ListQueues returns all queues
func (mq *AMQMessageQueue) ListQueues(ctx context.Context) ([]*types.Queue, error) {
    return mq.amq.ListQueues(ctx)
}

// Close closes the message queue connection
func (mq *AMQMessageQueue) Close() error {
    // Close all clients
    for _, c := range mq.clients {
        _ = c.Close()
    }
    
    // Close AMQ instance
    return mq.amq.Close()
}

// AMQFactory creates AMQ message queue instances
type AMQFactory struct{}

// Create creates a new AMQ message queue instance
func (f *AMQFactory) Create(config Config) (MessageQueue, error) {
    return NewAMQMessageQueue(config)
}