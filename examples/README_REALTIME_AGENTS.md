# Real-Time Agents in ARC Orchestrator

This document explains how to use the enhanced orchestrator to create and manage real-time agents that can process messages independently from workflows.

## Overview

Real-time agents are standalone container-based agents that:
- Run independently from workflows
- Listen to message queues for incoming messages
- Process messages asynchronously using custom handlers
- Can connect to external LLM services
- Support both internal and external AMQ instances
- Provide lifecycle management (create, start, stop, pause, resume)

## Key Features

### 1. Standalone Operation
- Agents run independently without being part of a workflow
- Long-running processes that continuously listen for messages
- Independent lifecycle management

### 2. Message Processing
- Subscribe to one or more topics/queues
- Process messages asynchronously with custom handlers
- Send responses back through the queue system
- Support for message acknowledgment and error handling

### 3. LLM Integration
- Configure LLM endpoints through environment variables
- Process natural language messages with AI models
- Support for multiple LLM providers (OpenAI, Anthropic, etc.)

### 4. External AMQ Support
- Connect to external AMQ instances
- Support multiple message queue configurations
- Seamless switching between internal and external queues

## API Reference

### Creating a Real-Time Agent

```go
// Define your message handler
func MyMessageHandler(ctx context.Context, msg *messagequeue.Message) error {
    // Process the message
    content := msg.Payload["content"].(string)
    
    // Your processing logic here
    response := processMessage(content)
    
    // Optionally send a response
    return nil
}

// Create agent configuration
agent := &types.Agent{
    Name:  "my-realtime-agent",
    Image: "my-agent:latest",
    Config: types.AgentConfig{
        Image: "my-agent:latest",
        Environment: map[string]string{
            "LLM_ENDPOINT": "https://api.openai.com/v1/chat/completions",
            "LLM_API_KEY":  os.Getenv("OPENAI_API_KEY"),
        },
        MessageQueue: types.MessageQueueConfig{
            Topics: []string{"chat", "support"},
        },
        Resources: types.ResourceRequirements{
            CPU:    "500m",
            Memory: "1Gi",
        },
    },
}

// Create real-time agent configuration
rtaConfig := orchestrator.RealTimeAgentConfig{
    Agent:          agent,
    Topics:         []string{"chat", "support"},
    MessageHandler: MyMessageHandler,
}

// Create the agent
err := orch.CreateRealTimeAgent(ctx, rtaConfig)
```

### Starting and Managing Agents

```go
// Start the agent
err := orch.StartRealTimeAgent(ctx, agent.ID)

// Pause message processing
err := orch.PauseRealTimeAgent(ctx, agent.ID)

// Resume message processing
err := orch.ResumeRealTimeAgent(ctx, agent.ID)

// Stop the agent
err := orch.StopRealTimeAgent(ctx, agent.ID)

// Get agent information
agentInfo, err := orch.GetRealTimeAgent(ctx, agent.ID)

// List all real-time agents
agents, err := orch.ListRealTimeAgents(ctx)
```

### Sending Messages to Agents

```go
message := &messagequeue.Message{
    ID:    "msg-123",
    Topic: "chat",
    From:  "user-client",
    Type:  "question",
    Payload: map[string]interface{}{
        "content": "What's the weather like?",
        "user_id": "user-123",
    },
    Timestamp: time.Now().Unix(),
}

err := orch.SendMessageToRealTimeAgent(ctx, agent.ID, message)
```

### External AMQ Configuration

```go
// Register external AMQ instance
externalConfig := &orchestrator.ExternalAMQConfig{
    Name:      "production-amq",
    Endpoints: []string{"amq://prod-server:5672"},
    Config: messagequeue.Config{
        StorePath:      "/data/external-amq",
        WorkerPoolSize: 10,
        MessageTimeout: 60,
    },
}

err := orch.RegisterExternalAMQ(externalConfig)

// Configure agent to use external AMQ
agent.Config.MessageQueue.Brokers = []string{"amq://prod-server:5672"}
```

## Environment Variables for LLM Integration

The orchestrator supports configuring LLM connections through environment variables:

- `LLM_ENDPOINT`: The LLM API endpoint URL
- `LLM_API_KEY`: API key for authentication
- `LLM_MODEL`: Model name to use (e.g., "gpt-3.5-turbo", "claude-3-sonnet")
- `LLM_TIMEOUT`: Request timeout in seconds
- `LLM_MAX_TOKENS`: Maximum tokens for responses

Example:
```bash
export LLM_ENDPOINT="https://api.openai.com/v1/chat/completions"
export LLM_API_KEY="your-openai-api-key"
export LLM_MODEL="gpt-3.5-turbo"
export LLM_TIMEOUT="30"
export LLM_MAX_TOKENS="1000"
```

## Message Handler Examples

### Basic Chat Handler

```go
func ChatHandler(ctx context.Context, msg *messagequeue.Message) error {
    content, ok := msg.Payload["content"].(string)
    if !ok {
        return fmt.Errorf("invalid message content")
    }
    
    // Process with LLM
    response, err := callLLM(ctx, content)
    if err != nil {
        return err
    }
    
    // Send response back (implementation depends on your message queue setup)
    return sendResponse(ctx, msg, response)
}
```

### Support Ticket Handler

```go
func SupportHandler(ctx context.Context, msg *messagequeue.Message) error {
    ticket := msg.Payload
    
    // Extract ticket information
    subject := ticket["subject"].(string)
    description := ticket["description"].(string)
    priority := ticket["priority"].(string)
    
    // Process based on priority
    switch priority {
    case "high":
        return handleHighPriorityTicket(ctx, subject, description)
    case "medium":
        return handleMediumPriorityTicket(ctx, subject, description)
    default:
        return handleLowPriorityTicket(ctx, subject, description)
    }
}
```

### Document Processing Handler

```go
func DocumentHandler(ctx context.Context, msg *messagequeue.Message) error {
    docURL, ok := msg.Payload["document_url"].(string)
    if !ok {
        return fmt.Errorf("document URL required")
    }
    
    // Download and process document
    content, err := downloadDocument(ctx, docURL)
    if err != nil {
        return err
    }
    
    // Extract text and analyze
    text := extractText(content)
    analysis, err := analyzeWithLLM(ctx, text)
    if err != nil {
        return err
    }
    
    // Store results
    return storeAnalysis(ctx, docURL, analysis)
}
```

## Agent Container Requirements

Your agent containers should:

1. **Handle Graceful Shutdown**: Respond to SIGTERM signals
2. **Health Checks**: Provide health check endpoints
3. **Environment Configuration**: Read configuration from environment variables
4. **Message Queue Integration**: Connect to AMQ for message processing
5. **Logging**: Provide structured logging for monitoring

Example Dockerfile:
```dockerfile
FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY . .
RUN go build -o agent ./cmd/agent

FROM alpine:latest
RUN apk --no-cache add ca-certificates
WORKDIR /root/
COPY --from=builder /app/agent .
CMD ["./agent"]
```

## Monitoring and Events

The orchestrator provides comprehensive event tracking:

- `agent.created`: Agent created
- `agent.started`: Agent started successfully  
- `agent.stopped`: Agent stopped
- `agent.paused`: Agent paused
- `agent.resumed`: Agent resumed
- `agent.health_checked`: Regular health check
- `agent.health_check_failed`: Health check failure
- `agent.status_changed`: Status change detected
- `message.received`: Message received by agent
- `message.processed`: Message processed successfully
- `message.failed`: Message processing failed

## Best Practices

1. **Error Handling**: Always handle errors gracefully in message handlers
2. **Timeouts**: Set appropriate timeouts for LLM calls and external services
3. **Resource Limits**: Configure CPU and memory limits for agents
4. **Health Monitoring**: Implement health check endpoints in your agents
5. **Message Acknowledgment**: Properly acknowledge processed messages
6. **Graceful Shutdown**: Handle shutdown signals to clean up resources
7. **Logging**: Use structured logging for better observability
8. **Security**: Secure API keys and sensitive configuration

## Troubleshooting

### Agent Won't Start
- Check container image exists and is accessible
- Verify resource requirements are available
- Check runtime (Docker/Kubernetes) connectivity
- Review environment variable configuration

### Messages Not Processing
- Verify agent is subscribed to correct topics
- Check message format and payload structure
- Review message handler error logs
- Confirm message queue connectivity

### Performance Issues
- Monitor resource usage (CPU, memory)
- Check message processing time
- Review concurrent message handling
- Consider scaling agent instances

For more examples and advanced usage, see the `examples/` directory in the repository.