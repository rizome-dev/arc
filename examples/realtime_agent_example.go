// Package main demonstrates real-time agent usage with ARC orchestrator
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/rizome-dev/arc/pkg/messagequeue"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"github.com/rizome-dev/arc/pkg/runtime"
	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
)

// LLMMessageHandler handles incoming messages by processing them with an LLM
func LLMMessageHandler(ctx context.Context, msg *messagequeue.Message) error {
	log.Printf("Processing message from %s on topic %s", msg.From, msg.Topic)

	// Extract message content
	content, ok := msg.Payload["content"].(string)
	if !ok {
		return fmt.Errorf("message content not found or not string")
	}

	// Get LLM configuration from environment
	llmEndpoint := os.Getenv("LLM_ENDPOINT")
	llmAPIKey := os.Getenv("LLM_API_KEY")
	llmModel := os.Getenv("LLM_MODEL")

	if llmEndpoint == "" {
		llmEndpoint = "https://api.openai.com/v1/chat/completions" // Default to OpenAI
	}
	if llmModel == "" {
		llmModel = "gpt-3.5-turbo"
	}

	log.Printf("Processing message with LLM: %s", content)

	// Simulate LLM processing (replace with actual LLM client)
	response, err := processWithLLM(ctx, llmEndpoint, llmAPIKey, llmModel, content)
	if err != nil {
		return fmt.Errorf("LLM processing failed: %w", err)
	}

	// Create response message
	responseMsg := &messagequeue.Message{
		ID:        fmt.Sprintf("resp-%d", time.Now().UnixNano()),
		Topic:     msg.Topic + "-response",
		From:      "llm-agent",
		To:        msg.From,
		Type:      "response",
		Payload: map[string]interface{}{
			"original_message": content,
			"response":         response,
			"model":           llmModel,
			"processing_time": time.Now().Unix(),
		},
		Timestamp: time.Now().Unix(),
	}

	// In a real implementation, you would send this back through the message queue
	log.Printf("Generated response: %s", response)

	// Log the response message (in practice, send it back via message queue)
	responseJSON, _ := json.MarshalIndent(responseMsg, "", "  ")
	log.Printf("Response message: %s", responseJSON)

	return nil
}

// processWithLLM simulates processing content with an LLM service
func processWithLLM(ctx context.Context, endpoint, apiKey, model, content string) (string, error) {
	// Simulate processing time
	time.Sleep(500 * time.Millisecond)

	// In a real implementation, this would make HTTP requests to the LLM service
	// For demo purposes, we'll just return a mock response
	return fmt.Sprintf("LLM Response (model: %s): Processed your message '%s'", model, content), nil
}

func main() {
	ctx := context.Background()

	// Initialize components
	log.Println("Initializing ARC orchestrator...")

	// Create runtime (using Docker runtime in this example)
	runtimeInstance, err := createRuntime()
	if err != nil {
		log.Fatalf("Failed to create runtime: %v", err)
	}

	// Create message queue
	mqConfig := messagequeue.Config{
		StorePath:      "./amq-data",
		WorkerPoolSize: 10,
		MessageTimeout: 30,
	}
	mq, err := messagequeue.NewAMQMessageQueue(mqConfig)
	if err != nil {
		log.Fatalf("Failed to create message queue: %v", err)
	}

	// Create state manager
	stateManager, err := createStateManager()
	if err != nil {
		log.Fatalf("Failed to create state manager: %v", err)
	}

	// Create orchestrator
	orch, err := orchestrator.New(orchestrator.Config{
		Runtime:      runtimeInstance,
		MessageQueue: mq,
		StateManager: stateManager,
	})
	if err != nil {
		log.Fatalf("Failed to create orchestrator: %v", err)
	}

	// Start orchestrator
	if err := orch.Start(); err != nil {
		log.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer orch.Stop()

	// Create real-time agent configuration
	agent := &types.Agent{
		Name:  "llm-chat-agent",
		Image: "your-llm-agent:latest", // Replace with your LLM agent image
		Config: types.AgentConfig{
			Image: "your-llm-agent:latest",
			Environment: map[string]string{
				"LLM_ENDPOINT": os.Getenv("LLM_ENDPOINT"),
				"LLM_API_KEY":  os.Getenv("LLM_API_KEY"),
				"LLM_MODEL":    os.Getenv("LLM_MODEL"),
			},
			MessageQueue: types.MessageQueueConfig{
				Topics: []string{"chat-requests", "support-requests"},
			},
			Resources: types.ResourceRequirements{
				CPU:    "500m",
				Memory: "1Gi",
			},
		},
	}

	// Create real-time agent
	rtaConfig := orchestrator.RealTimeAgentConfig{
		Agent:          agent,
		Topics:         []string{"chat-requests", "support-requests"},
		MessageHandler: LLMMessageHandler,
	}

	log.Println("Creating real-time agent...")
	if err := orch.CreateRealTimeAgent(ctx, rtaConfig); err != nil {
		log.Fatalf("Failed to create real-time agent: %v", err)
	}

	// Start the real-time agent
	log.Println("Starting real-time agent...")
	if err := orch.StartRealTimeAgent(ctx, agent.ID); err != nil {
		log.Fatalf("Failed to start real-time agent: %v", err)
	}

	log.Printf("Real-time agent %s is now listening for messages on topics: %v", agent.ID, rtaConfig.Topics)

	// Example: Create external AMQ configuration
	if externalAMQEndpoint := os.Getenv("EXTERNAL_AMQ_ENDPOINT"); externalAMQEndpoint != "" {
		log.Println("Registering external AMQ...")
		externalConfig := &orchestrator.ExternalAMQConfig{
			Name:      "external-production-amq",
			Endpoints: []string{externalAMQEndpoint},
			Config: messagequeue.Config{
				StorePath:      "./external-amq-data",
				WorkerPoolSize: 5,
				MessageTimeout: 60,
			},
		}

		if err := orch.RegisterExternalAMQ(externalConfig); err != nil {
			log.Printf("Failed to register external AMQ: %v", err)
		} else {
			log.Println("External AMQ registered successfully")
		}
	}

	// Simulate sending messages to the agent for testing
	go func() {
		time.Sleep(5 * time.Second) // Wait for agent to be ready

		// Send test messages
		testMessages := []string{
			"Hello, can you help me with my question?",
			"What's the weather like today?",
			"Please explain how AI works.",
		}

		for i, content := range testMessages {
			msg := &messagequeue.Message{
				ID:    fmt.Sprintf("test-msg-%d", i+1),
				Topic: "chat-requests",
				From:  "test-client",
				Type:  "chat",
				Payload: map[string]interface{}{
					"content": content,
					"user_id": "test-user-123",
				},
				Timestamp: time.Now().Unix(),
			}

			log.Printf("Sending test message: %s", content)
			if err := orch.SendMessageToRealTimeAgent(ctx, agent.ID, msg); err != nil {
				log.Printf("Failed to send message: %v", err)
			}

			time.Sleep(2 * time.Second)
		}
	}()

	// Set up graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	log.Println("Real-time agent is running. Press Ctrl+C to stop...")

	// Wait for shutdown signal
	<-sigChan
	log.Println("Shutting down...")

	// Stop the real-time agent
	if err := orch.StopRealTimeAgent(ctx, agent.ID); err != nil {
		log.Printf("Failed to stop real-time agent: %v", err)
	}

	log.Println("Real-time agent example completed")
}

// createRuntime creates a runtime instance (Docker or Kubernetes)
func createRuntime() (runtime.Runtime, error) {
	// This is a placeholder - you would implement the actual runtime creation
	// based on your runtime package implementation
	return nil, fmt.Errorf("runtime creation not implemented in example")
}

// createStateManager creates a state manager instance
func createStateManager() (state.StateManager, error) {
	// This is a placeholder - you would implement the actual state manager creation
	// based on your state package implementation
	return nil, fmt.Errorf("state manager creation not implemented in example")
}