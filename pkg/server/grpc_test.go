package server

import (
	"context"
	"testing"
	"time"

	arcv1 "github.com/rizome-dev/arc/api/v1"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func createTestGRPCServer(t *testing.T) *GRPCServer {
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
	
	if err := orch.Start(); err != nil {
		t.Fatalf("Failed to start orchestrator: %v", err)
	}
	t.Cleanup(func() { orch.Stop() })
	
	return NewGRPCServer(orch)
}

func TestGRPCServer_CreateWorkflow(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	tests := []struct {
		name    string
		request *arcv1.CreateWorkflowRequest
		wantErr bool
		errCode codes.Code
	}{
		{
			name: "valid workflow",
			request: &arcv1.CreateWorkflowRequest{
				Workflow: &arcv1.Workflow{
					Name:        "test-workflow",
					Description: "Test workflow description",
					Tasks: []*arcv1.Task{
						{
							Name: "test-task",
							AgentConfig: &arcv1.AgentConfig{
								Image:   "ubuntu:latest",
								Command: []string{"echo", "hello"},
							},
						},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "nil workflow",
			request: &arcv1.CreateWorkflowRequest{
				Workflow: nil,
			},
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.CreateWorkflow(ctx, tt.request)
			
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if st, ok := status.FromError(err); ok {
					if st.Code() != tt.errCode {
						t.Errorf("Expected error code %v, got %v", tt.errCode, st.Code())
					}
				}
				return
			}
			
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			if resp == nil {
				t.Error("Response should not be nil")
				return
			}
			
			if resp.WorkflowId == "" {
				t.Error("Workflow ID should not be empty")
			}
			
			if resp.Message == "" {
				t.Error("Message should not be empty")
			}
		})
	}
}

func TestGRPCServer_StartWorkflow(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	// First create a workflow
	createResp, err := server.CreateWorkflow(ctx, &arcv1.CreateWorkflowRequest{
		Workflow: &arcv1.Workflow{
			Name: "test-workflow",
			Tasks: []*arcv1.Task{
				{
					Name: "test-task",
					AgentConfig: &arcv1.AgentConfig{
						Image:   "ubuntu:latest",
						Command: []string{"echo", "hello"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}
	
	tests := []struct {
		name       string
		workflowID string
		wantErr    bool
		errCode    codes.Code
	}{
		{
			name:       "valid workflow ID",
			workflowID: createResp.WorkflowId,
			wantErr:    false,
		},
		{
			name:       "empty workflow ID",
			workflowID: "",
			wantErr:    true,
			errCode:    codes.InvalidArgument,
		},
		{
			name:       "nonexistent workflow ID",
			workflowID: "nonexistent-id",
			wantErr:    true,
			errCode:    codes.Internal, // Orchestrator will return error
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &arcv1.StartWorkflowRequest{
				WorkflowId: tt.workflowID,
			}
			
			resp, err := server.StartWorkflow(ctx, req)
			
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if st, ok := status.FromError(err); ok {
					if st.Code() != tt.errCode {
						t.Errorf("Expected error code %v, got %v", tt.errCode, st.Code())
					}
				}
				return
			}
			
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			if resp == nil {
				t.Error("Response should not be nil")
			}
		})
	}
}

func TestGRPCServer_GetWorkflow(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	// Create a workflow first
	createResp, err := server.CreateWorkflow(ctx, &arcv1.CreateWorkflowRequest{
		Workflow: &arcv1.Workflow{
			Name:        "test-workflow",
			Description: "Test description",
			Tasks: []*arcv1.Task{
				{
					Name: "test-task",
					AgentConfig: &arcv1.AgentConfig{
						Image:   "ubuntu:latest",
						Command: []string{"echo", "hello"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create workflow: %v", err)
	}
	
	tests := []struct {
		name       string
		workflowID string
		wantErr    bool
		errCode    codes.Code
	}{
		{
			name:       "existing workflow",
			workflowID: createResp.WorkflowId,
			wantErr:    false,
		},
		{
			name:       "empty workflow ID",
			workflowID: "",
			wantErr:    true,
			errCode:    codes.InvalidArgument,
		},
		{
			name:       "nonexistent workflow",
			workflowID: "nonexistent-id",
			wantErr:    true,
			errCode:    codes.NotFound,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &arcv1.GetWorkflowRequest{
				WorkflowId: tt.workflowID,
			}
			
			resp, err := server.GetWorkflow(ctx, req)
			
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if st, ok := status.FromError(err); ok {
					if st.Code() != tt.errCode {
						t.Errorf("Expected error code %v, got %v", tt.errCode, st.Code())
					}
				}
				return
			}
			
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			if resp == nil || resp.Workflow == nil {
				t.Error("Response and workflow should not be nil")
				return
			}
			
			if resp.Workflow.Name != "test-workflow" {
				t.Errorf("Expected workflow name 'test-workflow', got %s", resp.Workflow.Name)
			}
			
			if len(resp.Workflow.Tasks) != 1 {
				t.Errorf("Expected 1 task, got %d", len(resp.Workflow.Tasks))
			}
		})
	}
}

func TestGRPCServer_ListWorkflows(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	// Create multiple workflows
	for i := 0; i < 3; i++ {
		_, err := server.CreateWorkflow(ctx, &arcv1.CreateWorkflowRequest{
			Workflow: &arcv1.Workflow{
				Name: "test-workflow-" + string(rune(i+'1')),
				Tasks: []*arcv1.Task{
					{
						Name: "test-task",
						AgentConfig: &arcv1.AgentConfig{
							Image:   "ubuntu:latest",
							Command: []string{"echo", "hello"},
						},
					},
				},
			},
		})
		if err != nil {
			t.Fatalf("Failed to create workflow %d: %v", i, err)
		}
	}
	
	req := &arcv1.ListWorkflowsRequest{}
	resp, err := server.ListWorkflows(ctx, req)
	
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}
	
	if resp == nil {
		t.Error("Response should not be nil")
		return
	}
	
	if len(resp.Workflows) != 3 {
		t.Errorf("Expected 3 workflows, got %d", len(resp.Workflows))
	}
	
	// Test with filter
	reqWithFilter := &arcv1.ListWorkflowsRequest{
		Filter: map[string]string{
			"name": "test-workflow-1",
		},
	}
	
	respWithFilter, err := server.ListWorkflows(ctx, reqWithFilter)
	if err != nil {
		t.Errorf("Unexpected error with filter: %v", err)
		return
	}
	
	if respWithFilter == nil {
		t.Error("Response with filter should not be nil")
	}
}

func TestGRPCServer_CreateRealTimeAgent(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	tests := []struct {
		name    string
		request *arcv1.CreateRealTimeAgentRequest
		wantErr bool
		errCode codes.Code
	}{
		{
			name: "valid real-time agent",
			request: &arcv1.CreateRealTimeAgentRequest{
				Config: &arcv1.RealTimeAgentConfig{
					Agent: &arcv1.Agent{
						Name:  "test-realtime-agent",
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
			},
			wantErr: false,
		},
		{
			name: "nil config",
			request: &arcv1.CreateRealTimeAgentRequest{
				Config: nil,
			},
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.CreateRealTimeAgent(ctx, tt.request)
			
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if st, ok := status.FromError(err); ok {
					if st.Code() != tt.errCode {
						t.Errorf("Expected error code %v, got %v", tt.errCode, st.Code())
					}
				}
				return
			}
			
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			if resp == nil {
				t.Error("Response should not be nil")
				return
			}
			
			if resp.AgentId == "" {
				t.Error("Agent ID should not be empty")
			}
		})
	}
}

func TestGRPCServer_SendMessage(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	// Create a real-time agent first
	agentResp, err := server.CreateRealTimeAgent(ctx, &arcv1.CreateRealTimeAgentRequest{
		Config: &arcv1.RealTimeAgentConfig{
			Agent: &arcv1.Agent{
				Name:  "test-agent",
				Image: "ubuntu:latest",
				Config: &arcv1.AgentConfig{
					MessageQueue: &arcv1.MessageQueueConfig{
						Topics: []string{"test-topic"},
					},
				},
			},
			Topics: []string{"test-topic"},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create real-time agent: %v", err)
	}
	
	tests := []struct {
		name    string
		request *arcv1.SendMessageRequest
		wantErr bool
		errCode codes.Code
	}{
		{
			name: "valid message",
			request: &arcv1.SendMessageRequest{
				From: "test-sender",
				To:   agentResp.AgentId,
				Message: &arcv1.Message{
					Id:      "test-msg-1",
					Type:    "task",
					Payload: map[string]string{"command": "hello"},
				},
			},
			wantErr: false,
		},
		{
			name: "empty from",
			request: &arcv1.SendMessageRequest{
				From: "",
				To:   agentResp.AgentId,
				Message: &arcv1.Message{
					Id: "test-msg-1",
				},
			},
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
		{
			name: "empty to",
			request: &arcv1.SendMessageRequest{
				From: "test-sender",
				To:   "",
				Message: &arcv1.Message{
					Id: "test-msg-1",
				},
			},
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
		{
			name: "nil message",
			request: &arcv1.SendMessageRequest{
				From:    "test-sender",
				To:      agentResp.AgentId,
				Message: nil,
			},
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := server.SendMessage(ctx, tt.request)
			
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if st, ok := status.FromError(err); ok {
					if st.Code() != tt.errCode {
						t.Errorf("Expected error code %v, got %v", tt.errCode, st.Code())
					}
				}
				return
			}
			
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			if resp == nil {
				t.Error("Response should not be nil")
				return
			}
			
			if resp.MessageId == "" {
				t.Error("Message ID should not be empty")
			}
			
			if resp.Status != "sent" {
				t.Errorf("Expected status 'sent', got %s", resp.Status)
			}
		})
	}
}

func TestGRPCServer_Health(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	resp, err := server.Health(ctx, nil)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}
	
	if resp == nil {
		t.Error("Response should not be nil")
		return
	}
	
	if resp.Status != "healthy" {
		t.Errorf("Expected status 'healthy', got %s", resp.Status)
	}
	
	if resp.Checks == nil {
		t.Error("Checks should not be nil")
		return
	}
	
	expectedChecks := []string{"orchestrator", "grpc_server"}
	for _, check := range expectedChecks {
		if status, ok := resp.Checks[check]; !ok || status != "ok" {
			t.Errorf("Expected check %s to be 'ok', got %s", check, status)
		}
	}
	
	if resp.Timestamp == nil {
		t.Error("Timestamp should not be nil")
	}
}

func TestGRPCServer_Ready(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	resp, err := server.Ready(ctx, nil)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}
	
	if resp == nil {
		t.Error("Response should not be nil")
		return
	}
	
	if !resp.Ready {
		t.Error("Service should be ready")
	}
	
	expectedServices := []string{"orchestrator", "message_queue", "state_manager"}
	if len(resp.Services) != len(expectedServices) {
		t.Errorf("Expected %d services, got %d", len(expectedServices), len(resp.Services))
	}
	
	for _, expectedService := range expectedServices {
		found := false
		for _, service := range resp.Services {
			if service == expectedService {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Expected service %s not found", expectedService)
		}
	}
	
	if resp.Timestamp == nil {
		t.Error("Timestamp should not be nil")
	}
}

func TestGRPCServer_ConversionHelpers(t *testing.T) {
	// Test workflow conversion
	t.Run("workflow conversion", func(t *testing.T) {
		now := time.Now()
		internal := &types.Workflow{
			ID:          "test-id",
			Name:        "test-workflow",
			Description: "test description",
			Status:      types.WorkflowStatusRunning,
			CreatedAt:   now,
			StartedAt:   &now,
			Tasks: []types.Task{
				{
					ID:   "task-1",
					Name: "test-task",
					AgentConfig: types.AgentConfig{
						Image:   "ubuntu:latest",
						Command: []string{"echo", "hello"},
					},
				},
			},
			Metadata: map[string]string{"key": "value"},
		}
		
		// Convert to proto
		proto := workflowToProto(internal)
		if proto.Id != internal.ID {
			t.Errorf("Expected ID %s, got %s", internal.ID, proto.Id)
		}
		if proto.Name != internal.Name {
			t.Errorf("Expected name %s, got %s", internal.Name, proto.Name)
		}
		if proto.Status != arcv1.WorkflowStatus_WORKFLOW_STATUS_RUNNING {
			t.Errorf("Expected status RUNNING, got %v", proto.Status)
		}
		if len(proto.Tasks) != 1 {
			t.Errorf("Expected 1 task, got %d", len(proto.Tasks))
		}
		
		// Convert back to internal
		converted := protoToWorkflow(proto)
		if converted.ID != internal.ID {
			t.Errorf("Expected ID %s, got %s", internal.ID, converted.ID)
		}
		if converted.Status != internal.Status {
			t.Errorf("Expected status %s, got %s", internal.Status, converted.Status)
		}
	})
	
	// Test agent status conversion
	t.Run("agent status conversion", func(t *testing.T) {
		tests := []struct {
			internal types.AgentStatus
			proto    arcv1.AgentStatus
		}{
			{types.AgentStatusPending, arcv1.AgentStatus_AGENT_STATUS_PENDING},
			{types.AgentStatusRunning, arcv1.AgentStatus_AGENT_STATUS_RUNNING},
			{types.AgentStatusCompleted, arcv1.AgentStatus_AGENT_STATUS_COMPLETED},
			{types.AgentStatusFailed, arcv1.AgentStatus_AGENT_STATUS_FAILED},
		}
		
		for _, test := range tests {
			// Internal to proto
			protoStatus := agentStatusToProto(test.internal)
			if protoStatus != test.proto {
				t.Errorf("Expected proto status %v, got %v", test.proto, protoStatus)
			}
			
			// Proto to internal
			internalStatus := protoToAgentStatus(test.proto)
			if internalStatus != test.internal {
				t.Errorf("Expected internal status %v, got %v", test.internal, internalStatus)
			}
		}
	})
	
	// Test message conversion
	t.Run("message conversion", func(t *testing.T) {
		proto := &arcv1.Message{
			Id:        "test-id",
			Topic:     "test-topic",
			From:      "sender",
			To:        "receiver",
			Type:      "task",
			Payload:   map[string]string{"key": "value"},
			Timestamp: 1672531200, // 2023-01-01T00:00:00Z as Unix timestamp
		}
		
		internal := protoToMessage(proto)
		if internal.ID != proto.Id {
			t.Errorf("Expected ID %s, got %s", proto.Id, internal.ID)
		}
		if internal.Topic != proto.Topic {
			t.Errorf("Expected topic %s, got %s", proto.Topic, internal.Topic)
		}
		if internal.Type != proto.Type {
			t.Errorf("Expected type %s, got %s", proto.Type, internal.Type)
		}
		
		// Check payload conversion
		if value, ok := internal.Payload["key"]; !ok || value.(string) != "value" {
			t.Error("Payload conversion failed")
		}
	})
}

func TestGRPCServer_GetAgentStatus(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	// Create a real-time agent first
	agentResp, err := server.CreateRealTimeAgent(ctx, &arcv1.CreateRealTimeAgentRequest{
		Config: &arcv1.RealTimeAgentConfig{
			Agent: &arcv1.Agent{
				Name:  "status-test-agent",
				Image: "ubuntu:latest",
			},
			Topics: []string{"test-topic"},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create real-time agent: %v", err)
	}
	
	tests := []struct {
		name    string
		agentID string
		wantErr bool
		errCode codes.Code
	}{
		{
			name:    "existing agent",
			agentID: agentResp.AgentId,
			wantErr: false,
		},
		{
			name:    "empty agent ID",
			agentID: "",
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
		{
			name:    "nonexistent agent",
			agentID: "nonexistent-id",
			wantErr: true,
			errCode: codes.NotFound,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &arcv1.GetAgentStatusRequest{
				AgentId: tt.agentID,
			}
			
			resp, err := server.GetAgentStatus(ctx, req)
			
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if st, ok := status.FromError(err); ok {
					if st.Code() != tt.errCode {
						t.Errorf("Expected error code %v, got %v", tt.errCode, st.Code())
					}
				}
				return
			}
			
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			if resp == nil {
				t.Error("Response should not be nil")
			}
		})
	}
}

func TestGRPCServer_ListRealTimeAgents(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	// Create multiple real-time agents
	numAgents := 3
	for i := 0; i < numAgents; i++ {
		_, err := server.CreateRealTimeAgent(ctx, &arcv1.CreateRealTimeAgentRequest{
			Config: &arcv1.RealTimeAgentConfig{
				Agent: &arcv1.Agent{
					Name:  "test-agent-" + string(rune(i+'1')),
					Image: "ubuntu:latest",
				},
				Topics: []string{"test-topic-" + string(rune(i+'1'))},
			},
		})
		if err != nil {
			t.Fatalf("Failed to create real-time agent %d: %v", i, err)
		}
	}
	
	req := &arcv1.ListRealTimeAgentsRequest{}
	resp, err := server.ListRealTimeAgents(ctx, req)
	
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
		return
	}
	
	if resp == nil {
		t.Error("Response should not be nil")
		return
	}
	
	if len(resp.Agents) != numAgents {
		t.Errorf("Expected %d agents, got %d", numAgents, len(resp.Agents))
	}
	
	// Check that all agents have required fields
	for _, agent := range resp.Agents {
		if agent.Id == "" {
			t.Error("Agent ID should not be empty")
		}
		if agent.Name == "" {
			t.Error("Agent name should not be empty")
		}
	}
}

func TestGRPCServer_StopRealTimeAgent(t *testing.T) {
	server := createTestGRPCServer(t)
	ctx := context.Background()
	
	// Create a real-time agent first
	agentResp, err := server.CreateRealTimeAgent(ctx, &arcv1.CreateRealTimeAgentRequest{
		Config: &arcv1.RealTimeAgentConfig{
			Agent: &arcv1.Agent{
				Name:  "stop-test-agent",
				Image: "ubuntu:latest",
			},
			Topics: []string{"test-topic"},
		},
	})
	if err != nil {
		t.Fatalf("Failed to create real-time agent: %v", err)
	}
	
	tests := []struct {
		name    string
		agentID string
		wantErr bool
		errCode codes.Code
	}{
		{
			name:    "existing agent",
			agentID: agentResp.AgentId,
			wantErr: false,
		},
		{
			name:    "empty agent ID",
			agentID: "",
			wantErr: true,
			errCode: codes.InvalidArgument,
		},
		{
			name:    "nonexistent agent",
			agentID: "nonexistent-id",
			wantErr: true,
			errCode: codes.Internal, // Orchestrator will return error
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := &arcv1.StopRealTimeAgentRequest{
				AgentId: tt.agentID,
			}
			
			resp, err := server.StopRealTimeAgent(ctx, req)
			
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				if st, ok := status.FromError(err); ok {
					if st.Code() != tt.errCode {
						t.Errorf("Expected error code %v, got %v", tt.errCode, st.Code())
					}
				}
				return
			}
			
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			if resp == nil {
				t.Error("Response should not be nil")
			}
		})
	}
}