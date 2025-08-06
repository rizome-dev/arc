// Package server provides gRPC and HTTP servers for the ARC orchestrator
package server

import (
	"context"
	"log"
	"strconv"

	arcv1 "github.com/rizome-dev/arc/api/v1"
	"github.com/rizome-dev/arc/pkg/messagequeue"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"github.com/rizome-dev/arc/pkg/types"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"
	"google.golang.org/protobuf/types/known/emptypb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// GRPCServer implements the ARC gRPC service
type GRPCServer struct {
	arcv1.UnimplementedARCServer
	orchestrator *orchestrator.Orchestrator
}

// NewGRPCServer creates a new gRPC server instance
func NewGRPCServer(orch *orchestrator.Orchestrator) *GRPCServer {
	return &GRPCServer{
		orchestrator: orch,
	}
}

// Workflow operations

func (s *GRPCServer) CreateWorkflow(ctx context.Context, req *arcv1.CreateWorkflowRequest) (*arcv1.CreateWorkflowResponse, error) {
	if req.Workflow == nil {
		return nil, status.Error(codes.InvalidArgument, "workflow is required")
	}

	// Convert protobuf workflow to internal type
	workflow := protoToWorkflow(req.Workflow)
	
	if err := s.orchestrator.CreateWorkflow(ctx, workflow); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create workflow: %v", err)
	}

	return &arcv1.CreateWorkflowResponse{
		WorkflowId: workflow.ID,
		Message:    "Workflow created successfully",
	}, nil
}

func (s *GRPCServer) StartWorkflow(ctx context.Context, req *arcv1.StartWorkflowRequest) (*arcv1.StartWorkflowResponse, error) {
	if req.WorkflowId == "" {
		return nil, status.Error(codes.InvalidArgument, "workflow_id is required")
	}

	if err := s.orchestrator.StartWorkflow(ctx, req.WorkflowId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to start workflow: %v", err)
	}

	return &arcv1.StartWorkflowResponse{
		Message: "Workflow started successfully",
	}, nil
}

func (s *GRPCServer) StopWorkflow(ctx context.Context, req *arcv1.StopWorkflowRequest) (*arcv1.StopWorkflowResponse, error) {
	if req.WorkflowId == "" {
		return nil, status.Error(codes.InvalidArgument, "workflow_id is required")
	}

	if err := s.orchestrator.StopWorkflow(ctx, req.WorkflowId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to stop workflow: %v", err)
	}

	return &arcv1.StopWorkflowResponse{
		Message: "Workflow stopped successfully",
	}, nil
}

func (s *GRPCServer) GetWorkflow(ctx context.Context, req *arcv1.GetWorkflowRequest) (*arcv1.GetWorkflowResponse, error) {
	if req.WorkflowId == "" {
		return nil, status.Error(codes.InvalidArgument, "workflow_id is required")
	}

	workflow, err := s.orchestrator.GetWorkflow(ctx, req.WorkflowId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "workflow not found: %v", err)
	}

	return &arcv1.GetWorkflowResponse{
		Workflow: workflowToProto(workflow),
	}, nil
}

func (s *GRPCServer) ListWorkflows(ctx context.Context, req *arcv1.ListWorkflowsRequest) (*arcv1.ListWorkflowsResponse, error) {
	workflows, err := s.orchestrator.ListWorkflows(ctx, req.Filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list workflows: %v", err)
	}

	protoWorkflows := make([]*arcv1.Workflow, len(workflows))
	for i, workflow := range workflows {
		protoWorkflows[i] = workflowToProto(workflow)
	}

	return &arcv1.ListWorkflowsResponse{
		Workflows: protoWorkflows,
	}, nil
}

// Real-time agent operations

func (s *GRPCServer) CreateRealTimeAgent(ctx context.Context, req *arcv1.CreateRealTimeAgentRequest) (*arcv1.CreateRealTimeAgentResponse, error) {
	if req.Config == nil {
		return nil, status.Error(codes.InvalidArgument, "config is required")
	}

	config := protoToRealTimeAgentConfig(req.Config)
	
	if err := s.orchestrator.CreateRealTimeAgent(ctx, *config); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create real-time agent: %v", err)
	}

	return &arcv1.CreateRealTimeAgentResponse{
		AgentId: config.Agent.ID,
		Message: "Real-time agent created successfully",
	}, nil
}

func (s *GRPCServer) StartRealTimeAgent(ctx context.Context, req *arcv1.StartRealTimeAgentRequest) (*arcv1.StartRealTimeAgentResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	if err := s.orchestrator.StartRealTimeAgent(ctx, req.AgentId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to start real-time agent: %v", err)
	}

	return &arcv1.StartRealTimeAgentResponse{
		Message: "Real-time agent started successfully",
	}, nil
}

func (s *GRPCServer) StopRealTimeAgent(ctx context.Context, req *arcv1.StopRealTimeAgentRequest) (*arcv1.StopRealTimeAgentResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	if err := s.orchestrator.StopRealTimeAgent(ctx, req.AgentId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to stop real-time agent: %v", err)
	}

	return &arcv1.StopRealTimeAgentResponse{
		Message: "Real-time agent stopped successfully",
	}, nil
}

func (s *GRPCServer) PauseRealTimeAgent(ctx context.Context, req *arcv1.PauseRealTimeAgentRequest) (*arcv1.PauseRealTimeAgentResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	if err := s.orchestrator.PauseRealTimeAgent(ctx, req.AgentId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to pause real-time agent: %v", err)
	}

	return &arcv1.PauseRealTimeAgentResponse{
		Message: "Real-time agent paused successfully",
	}, nil
}

func (s *GRPCServer) ResumeRealTimeAgent(ctx context.Context, req *arcv1.ResumeRealTimeAgentRequest) (*arcv1.ResumeRealTimeAgentResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	if err := s.orchestrator.ResumeRealTimeAgent(ctx, req.AgentId); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to resume real-time agent: %v", err)
	}

	return &arcv1.ResumeRealTimeAgentResponse{
		Message: "Real-time agent resumed successfully",
	}, nil
}

func (s *GRPCServer) GetRealTimeAgent(ctx context.Context, req *arcv1.GetRealTimeAgentRequest) (*arcv1.GetRealTimeAgentResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	agent, err := s.orchestrator.GetRealTimeAgent(ctx, req.AgentId)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "real-time agent not found: %v", err)
	}

	return &arcv1.GetRealTimeAgentResponse{
		Agent: agentToProto(agent),
	}, nil
}

func (s *GRPCServer) ListRealTimeAgents(ctx context.Context, req *arcv1.ListRealTimeAgentsRequest) (*arcv1.ListRealTimeAgentsResponse, error) {
	agents, err := s.orchestrator.ListRealTimeAgents(ctx)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list real-time agents: %v", err)
	}

	protoAgents := make([]*arcv1.Agent, len(agents))
	for i, agent := range agents {
		protoAgents[i] = agentToProto(agent)
	}

	return &arcv1.ListRealTimeAgentsResponse{
		Agents: protoAgents,
	}, nil
}

// Agent management

func (s *GRPCServer) GetAgentStatus(ctx context.Context, req *arcv1.GetAgentStatusRequest) (*arcv1.GetAgentStatusResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	// Try to get real-time agent first
	agent, err := s.orchestrator.GetRealTimeAgent(ctx, req.AgentId)
	if err == nil {
		resp := &arcv1.GetAgentStatusResponse{
			Status: agentStatusToProto(agent.Status),
			Error:  agent.Error,
		}
		if agent.StartedAt != nil {
			resp.StartedAt = timestamppb.New(*agent.StartedAt)
		}
		if agent.CompletedAt != nil {
			resp.CompletedAt = timestamppb.New(*agent.CompletedAt)
		}
		return resp, nil
	}

	// If not found, this implementation would need to check workflow agents
	// This would require extending the orchestrator interface
	return nil, status.Errorf(codes.NotFound, "agent not found: %s", req.AgentId)
}

func (s *GRPCServer) GetAgentLogs(ctx context.Context, req *arcv1.GetAgentLogsRequest) (*arcv1.GetAgentLogsResponse, error) {
	if req.AgentId == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	// This would require implementing log retrieval in the runtime interface
	// For now, return a placeholder response
	return &arcv1.GetAgentLogsResponse{
		Logs: []*arcv1.LogEntry{
			{
				Timestamp: timestamppb.Now(),
				Level:     "INFO",
				Message:   "Log retrieval not yet implemented",
			},
		},
	}, nil
}

func (s *GRPCServer) StreamAgentLogs(req *arcv1.StreamAgentLogsRequest, stream grpc.ServerStreamingServer[arcv1.LogEntry]) error {
	if req.AgentId == "" {
		return status.Error(codes.InvalidArgument, "agent_id is required")
	}

	// This would require implementing log streaming in the runtime interface
	// For now, send a placeholder message and close
	entry := &arcv1.LogEntry{
		Timestamp: timestamppb.Now(),
		Level:     "INFO",
		Message:   "Log streaming not yet implemented",
	}
	
	if err := stream.Send(entry); err != nil {
		return err
	}
	
	return nil
}

// Message queue operations

func (s *GRPCServer) SendMessage(ctx context.Context, req *arcv1.SendMessageRequest) (*arcv1.SendMessageResponse, error) {
	if req.From == "" {
		return nil, status.Error(codes.InvalidArgument, "from is required")
	}
	if req.To == "" {
		return nil, status.Error(codes.InvalidArgument, "to is required")
	}
	if req.Message == nil {
		return nil, status.Error(codes.InvalidArgument, "message is required")
	}

	message := protoToMessage(req.Message)
	
	if err := s.orchestrator.SendMessageToRealTimeAgent(ctx, req.To, message); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to send message: %v", err)
	}

	return &arcv1.SendMessageResponse{
		MessageId: message.ID,
		Status:    "sent",
	}, nil
}

func (s *GRPCServer) PublishTask(ctx context.Context, req *arcv1.PublishTaskRequest) (*arcv1.PublishTaskResponse, error) {
	if req.From == "" {
		return nil, status.Error(codes.InvalidArgument, "from is required")
	}
	if req.Topic == "" {
		return nil, status.Error(codes.InvalidArgument, "topic is required")
	}
	if req.Message == nil {
		return nil, status.Error(codes.InvalidArgument, "message is required")
	}

	// This would require extending the orchestrator interface to publish to topics
	// For now, return a placeholder response
	return &arcv1.PublishTaskResponse{
		MessageId: req.Message.Id,
		Status:    "published",
	}, nil
}

func (s *GRPCServer) GetQueueStats(ctx context.Context, req *arcv1.GetQueueStatsRequest) (*arcv1.GetQueueStatsResponse, error) {
	if req.QueueName == "" {
		return nil, status.Error(codes.InvalidArgument, "queue_name is required")
	}

	// This would require extending the orchestrator interface to access message queue stats
	// For now, return placeholder stats
	return &arcv1.GetQueueStatsResponse{
		Stats: &arcv1.QueueStats{
			Name:              req.QueueName,
			TotalMessages:     0,
			PendingMessages:   0,
			ProcessingMessages: 0,
			Consumers:         0,
			LastActivity:      timestamppb.Now(),
		},
	}, nil
}

func (s *GRPCServer) ListQueues(ctx context.Context, req *arcv1.ListQueuesRequest) (*arcv1.ListQueuesResponse, error) {
	// This would require extending the orchestrator interface to list queues
	// For now, return empty list
	return &arcv1.ListQueuesResponse{
		Queues: []*arcv1.Queue{},
	}, nil
}

// Event streaming

func (s *GRPCServer) StreamEvents(req *arcv1.StreamEventsRequest, stream grpc.ServerStreamingServer[arcv1.Event]) error {
	// This would require implementing event streaming from the state manager
	// For now, send a placeholder event and close
	event := &arcv1.Event{
		Id:        "placeholder",
		Type:      arcv1.EventType_EVENT_TYPE_AGENT_CREATED,
		Source:    "grpc-server",
		Timestamp: timestamppb.Now(),
		Data:      map[string]string{"message": "Event streaming not yet implemented"},
	}
	
	if err := stream.Send(event); err != nil {
		return err
	}
	
	return nil
}

// External AMQ management

func (s *GRPCServer) RegisterExternalAMQ(ctx context.Context, req *arcv1.RegisterExternalAMQRequest) (*arcv1.RegisterExternalAMQResponse, error) {
	if req.Config == nil {
		return nil, status.Error(codes.InvalidArgument, "config is required")
	}

	config := protoToExternalAMQConfig(req.Config)
	
	if err := s.orchestrator.RegisterExternalAMQ(config); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to register external AMQ: %v", err)
	}

	return &arcv1.RegisterExternalAMQResponse{
		Message: "External AMQ registered successfully",
	}, nil
}

// Health and readiness

func (s *GRPCServer) Health(ctx context.Context, req *emptypb.Empty) (*arcv1.HealthResponse, error) {
	// Basic health check - could be extended to check orchestrator components
	checks := map[string]string{
		"orchestrator": "ok",
		"grpc_server":  "ok",
	}

	return &arcv1.HealthResponse{
		Status:    "healthy",
		Checks:    checks,
		Timestamp: timestamppb.Now(),
	}, nil
}

func (s *GRPCServer) Ready(ctx context.Context, req *emptypb.Empty) (*arcv1.ReadyResponse, error) {
	// Check if services are ready
	services := []string{
		"orchestrator",
		"message_queue",
		"state_manager",
	}

	return &arcv1.ReadyResponse{
		Ready:     true,
		Services:  services,
		Timestamp: timestamppb.Now(),
	}, nil
}

// Conversion helpers

func protoToWorkflow(proto *arcv1.Workflow) *types.Workflow {
	workflow := &types.Workflow{
		ID:          proto.Id,
		Name:        proto.Name,
		Description: proto.Description,
		Status:      protoToWorkflowStatus(proto.Status),
		CreatedAt:   proto.CreatedAt.AsTime(),
		Metadata:    proto.Metadata,
	}

	if proto.StartedAt != nil {
		startedAt := proto.StartedAt.AsTime()
		workflow.StartedAt = &startedAt
	}
	if proto.CompletedAt != nil {
		completedAt := proto.CompletedAt.AsTime()
		workflow.CompletedAt = &completedAt
	}
	if proto.Error != "" {
		workflow.Error = proto.Error
	}

	// Convert tasks
	workflow.Tasks = make([]types.Task, len(proto.Tasks))
	for i, protoTask := range proto.Tasks {
		workflow.Tasks[i] = protoToTask(protoTask)
	}

	return workflow
}

func workflowToProto(workflow *types.Workflow) *arcv1.Workflow {
	proto := &arcv1.Workflow{
		Id:          workflow.ID,
		Name:        workflow.Name,
		Description: workflow.Description,
		Status:      workflowStatusToProto(workflow.Status),
		CreatedAt:   timestamppb.New(workflow.CreatedAt),
		Error:       workflow.Error,
		Metadata:    workflow.Metadata,
	}

	if workflow.StartedAt != nil {
		proto.StartedAt = timestamppb.New(*workflow.StartedAt)
	}
	if workflow.CompletedAt != nil {
		proto.CompletedAt = timestamppb.New(*workflow.CompletedAt)
	}

	// Convert tasks
	proto.Tasks = make([]*arcv1.Task, len(workflow.Tasks))
	for i, task := range workflow.Tasks {
		proto.Tasks[i] = taskToProto(&task)
	}

	return proto
}

func protoToTask(proto *arcv1.Task) types.Task {
	task := types.Task{
		ID:           proto.Id,
		Name:         proto.Name,
		AgentID:      proto.AgentId,
		Status:       protoToTaskStatus(proto.Status),
		Dependencies: proto.Dependencies,
		AgentConfig:  protoToAgentConfig(proto.AgentConfig),
		RetryCount:   int(proto.RetryCount),
		MaxRetries:   int(proto.MaxRetries),
		Error:        proto.Error,
		Context:      proto.Context,
	}

	if proto.Timeout != nil {
		task.Timeout = proto.Timeout.AsDuration()
	}
	if proto.StartedAt != nil {
		startedAt := proto.StartedAt.AsTime()
		task.StartedAt = &startedAt
	}
	if proto.CompletedAt != nil {
		completedAt := proto.CompletedAt.AsTime()
		task.CompletedAt = &completedAt
	}

	return task
}

func taskToProto(task *types.Task) *arcv1.Task {
	proto := &arcv1.Task{
		Id:           task.ID,
		Name:         task.Name,
		AgentId:      task.AgentID,
		Status:       taskStatusToProto(task.Status),
		Dependencies: task.Dependencies,
		AgentConfig:  agentConfigToProto(&task.AgentConfig),
		RetryCount:   int32(task.RetryCount),
		MaxRetries:   int32(task.MaxRetries),
		Error:        task.Error,
		Context:      task.Context,
	}

	if task.Timeout > 0 {
		proto.Timeout = durationpb.New(task.Timeout)
	}
	if task.StartedAt != nil {
		proto.StartedAt = timestamppb.New(*task.StartedAt)
	}
	if task.CompletedAt != nil {
		proto.CompletedAt = timestamppb.New(*task.CompletedAt)
	}

	return proto
}

func protoToAgent(proto *arcv1.Agent) *types.Agent {
	agent := &types.Agent{
		ID:          proto.Id,
		Name:        proto.Name,
		Image:       proto.Image,
		Status:      protoToAgentStatus(proto.Status),
		Config:      protoToAgentConfig(proto.Config),
		CreatedAt:   proto.CreatedAt.AsTime(),
		Error:       proto.Error,
		ContainerID: proto.ContainerId,
		Namespace:   proto.Namespace,
		Labels:      proto.Labels,
		Annotations: proto.Annotations,
	}

	if proto.StartedAt != nil {
		startedAt := proto.StartedAt.AsTime()
		agent.StartedAt = &startedAt
	}
	if proto.CompletedAt != nil {
		completedAt := proto.CompletedAt.AsTime()
		agent.CompletedAt = &completedAt
	}

	return agent
}

func agentToProto(agent *types.Agent) *arcv1.Agent {
	proto := &arcv1.Agent{
		Id:          agent.ID,
		Name:        agent.Name,
		Image:       agent.Image,
		Status:      agentStatusToProto(agent.Status),
		Config:      agentConfigToProto(&agent.Config),
		CreatedAt:   timestamppb.New(agent.CreatedAt),
		Error:       agent.Error,
		ContainerId: agent.ContainerID,
		Namespace:   agent.Namespace,
		Labels:      agent.Labels,
		Annotations: agent.Annotations,
	}

	if agent.StartedAt != nil {
		proto.StartedAt = timestamppb.New(*agent.StartedAt)
	}
	if agent.CompletedAt != nil {
		proto.CompletedAt = timestamppb.New(*agent.CompletedAt)
	}

	return proto
}

func protoToAgentConfig(proto *arcv1.AgentConfig) types.AgentConfig {
	config := types.AgentConfig{
		Image:       proto.Image,
		Environment: proto.Environment,
		Command:     proto.Command,
		Args:        proto.Args,
		WorkingDir:  proto.WorkingDir,
	}

	if proto.Resources != nil {
		config.Resources = types.ResourceRequirements{
			CPU:    proto.Resources.Cpu,
			Memory: proto.Resources.Memory,
			GPU:    proto.Resources.Gpu,
		}
	}

	if proto.MessageQueue != nil {
		config.MessageQueue = types.MessageQueueConfig{
			Topics:      proto.MessageQueue.Topics,
			Brokers:     proto.MessageQueue.Brokers,
			Credentials: proto.MessageQueue.Credentials,
		}
	}

	if len(proto.Volumes) > 0 {
		config.Volumes = make([]types.VolumeMount, len(proto.Volumes))
		for i, vol := range proto.Volumes {
			config.Volumes[i] = types.VolumeMount{
				Name:      vol.Name,
				MountPath: vol.MountPath,
				ReadOnly:  vol.ReadOnly,
			}
		}
	}

	return config
}

func agentConfigToProto(config *types.AgentConfig) *arcv1.AgentConfig {
	proto := &arcv1.AgentConfig{
		Image:       config.Image,
		Environment: config.Environment,
		Command:     config.Command,
		Args:        config.Args,
		WorkingDir:  config.WorkingDir,
		Resources: &arcv1.ResourceRequirements{
			Cpu:    config.Resources.CPU,
			Memory: config.Resources.Memory,
			Gpu:    config.Resources.GPU,
		},
		MessageQueue: &arcv1.MessageQueueConfig{
			Topics:      config.MessageQueue.Topics,
			Brokers:     config.MessageQueue.Brokers,
			Credentials: config.MessageQueue.Credentials,
		},
	}

	if len(config.Volumes) > 0 {
		proto.Volumes = make([]*arcv1.VolumeMount, len(config.Volumes))
		for i, vol := range config.Volumes {
			proto.Volumes[i] = &arcv1.VolumeMount{
				Name:      vol.Name,
				MountPath: vol.MountPath,
				ReadOnly:  vol.ReadOnly,
			}
		}
	}

	return proto
}

func protoToRealTimeAgentConfig(proto *arcv1.RealTimeAgentConfig) *orchestrator.RealTimeAgentConfig {
	config := &orchestrator.RealTimeAgentConfig{
		Agent:  protoToAgent(proto.Agent),
		Topics: proto.Topics,
		MessageHandler: func(ctx context.Context, msg *messagequeue.Message) error {
			// Default message handler - this would need to be customized
			log.Printf("Received message: %+v", msg)
			return nil
		},
	}

	if proto.ExternalAmq != nil {
		config.ExternalAMQ = protoToExternalAMQConfig(proto.ExternalAmq)
	}

	return config
}

func protoToExternalAMQConfig(proto *arcv1.ExternalAMQConfig) *orchestrator.ExternalAMQConfig {
	return &orchestrator.ExternalAMQConfig{
		Name:      proto.Name,
		Endpoints: proto.Endpoints,
		Config: messagequeue.Config{
			StorePath:      proto.Config["store_path"],
			WorkerPoolSize: parseIntFromConfig(proto.Config["worker_pool_size"], 10),
			MessageTimeout: parseIntFromConfig(proto.Config["message_timeout"], 30),
		},
	}
}

func protoToMessage(proto *arcv1.Message) *messagequeue.Message {
	return &messagequeue.Message{
		ID:        proto.Id,
		Topic:     proto.Topic,
		From:      proto.From,
		To:        proto.To,
		Type:      proto.Type,
		Payload:   convertStringMapToInterface(proto.Payload),
		Timestamp: proto.Timestamp,
	}
}

// Status conversion helpers

func protoToAgentStatus(status arcv1.AgentStatus) types.AgentStatus {
	switch status {
	case arcv1.AgentStatus_AGENT_STATUS_PENDING:
		return types.AgentStatusPending
	case arcv1.AgentStatus_AGENT_STATUS_CREATING:
		return types.AgentStatusCreating
	case arcv1.AgentStatus_AGENT_STATUS_RUNNING:
		return types.AgentStatusRunning
	case arcv1.AgentStatus_AGENT_STATUS_COMPLETED:
		return types.AgentStatusCompleted
	case arcv1.AgentStatus_AGENT_STATUS_FAILED:
		return types.AgentStatusFailed
	case arcv1.AgentStatus_AGENT_STATUS_TERMINATED:
		return types.AgentStatusTerminated
	default:
		return types.AgentStatusPending
	}
}

func agentStatusToProto(status types.AgentStatus) arcv1.AgentStatus {
	switch status {
	case types.AgentStatusPending:
		return arcv1.AgentStatus_AGENT_STATUS_PENDING
	case types.AgentStatusCreating:
		return arcv1.AgentStatus_AGENT_STATUS_CREATING
	case types.AgentStatusRunning:
		return arcv1.AgentStatus_AGENT_STATUS_RUNNING
	case types.AgentStatusCompleted:
		return arcv1.AgentStatus_AGENT_STATUS_COMPLETED
	case types.AgentStatusFailed:
		return arcv1.AgentStatus_AGENT_STATUS_FAILED
	case types.AgentStatusTerminated:
		return arcv1.AgentStatus_AGENT_STATUS_TERMINATED
	default:
		return arcv1.AgentStatus_AGENT_STATUS_UNSPECIFIED
	}
}

func protoToWorkflowStatus(status arcv1.WorkflowStatus) types.WorkflowStatus {
	switch status {
	case arcv1.WorkflowStatus_WORKFLOW_STATUS_PENDING:
		return types.WorkflowStatusPending
	case arcv1.WorkflowStatus_WORKFLOW_STATUS_RUNNING:
		return types.WorkflowStatusRunning
	case arcv1.WorkflowStatus_WORKFLOW_STATUS_COMPLETED:
		return types.WorkflowStatusCompleted
	case arcv1.WorkflowStatus_WORKFLOW_STATUS_FAILED:
		return types.WorkflowStatusFailed
	case arcv1.WorkflowStatus_WORKFLOW_STATUS_CANCELLED:
		return types.WorkflowStatusCancelled
	default:
		return types.WorkflowStatusPending
	}
}

func workflowStatusToProto(status types.WorkflowStatus) arcv1.WorkflowStatus {
	switch status {
	case types.WorkflowStatusPending:
		return arcv1.WorkflowStatus_WORKFLOW_STATUS_PENDING
	case types.WorkflowStatusRunning:
		return arcv1.WorkflowStatus_WORKFLOW_STATUS_RUNNING
	case types.WorkflowStatusCompleted:
		return arcv1.WorkflowStatus_WORKFLOW_STATUS_COMPLETED
	case types.WorkflowStatusFailed:
		return arcv1.WorkflowStatus_WORKFLOW_STATUS_FAILED
	case types.WorkflowStatusCancelled:
		return arcv1.WorkflowStatus_WORKFLOW_STATUS_CANCELLED
	default:
		return arcv1.WorkflowStatus_WORKFLOW_STATUS_UNSPECIFIED
	}
}

func protoToTaskStatus(status arcv1.TaskStatus) types.TaskStatus {
	switch status {
	case arcv1.TaskStatus_TASK_STATUS_PENDING:
		return types.TaskStatusPending
	case arcv1.TaskStatus_TASK_STATUS_RUNNING:
		return types.TaskStatusRunning
	case arcv1.TaskStatus_TASK_STATUS_COMPLETED:
		return types.TaskStatusCompleted
	case arcv1.TaskStatus_TASK_STATUS_FAILED:
		return types.TaskStatusFailed
	case arcv1.TaskStatus_TASK_STATUS_SKIPPED:
		return types.TaskStatusSkipped
	default:
		return types.TaskStatusPending
	}
}

func taskStatusToProto(status types.TaskStatus) arcv1.TaskStatus {
	switch status {
	case types.TaskStatusPending:
		return arcv1.TaskStatus_TASK_STATUS_PENDING
	case types.TaskStatusRunning:
		return arcv1.TaskStatus_TASK_STATUS_RUNNING
	case types.TaskStatusCompleted:
		return arcv1.TaskStatus_TASK_STATUS_COMPLETED
	case types.TaskStatusFailed:
		return arcv1.TaskStatus_TASK_STATUS_FAILED
	case types.TaskStatusSkipped:
		return arcv1.TaskStatus_TASK_STATUS_SKIPPED
	default:
		return arcv1.TaskStatus_TASK_STATUS_UNSPECIFIED
	}
}

// Utility helpers

func parseIntFromConfig(value string, defaultValue int) int {
	if value == "" {
		return defaultValue
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	return defaultValue
}

func convertStringMapToInterface(stringMap map[string]string) map[string]interface{} {
	result := make(map[string]interface{})
	for k, v := range stringMap {
		result[k] = v
	}
	return result
}