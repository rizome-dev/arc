// Package runtime provides Docker runtime implementation
package runtime

import (
    "context"
    "fmt"
    "io"
    "strings"
    "time"

    "github.com/docker/docker/api/types"
    "github.com/docker/docker/api/types/container"
    "github.com/docker/docker/api/types/filters"
    "github.com/docker/docker/client"
    "github.com/docker/docker/pkg/stdcopy"
    
    arctypes "github.com/rizome-dev/arc/pkg/types"
    "github.com/rizome-dev/arc/pkg/validation"
)

// DockerRuntime implements Runtime interface for Docker
type DockerRuntime struct {
    client         *client.Client
    config         Config
    imageValidator validation.ImageValidator
}

// NewDockerRuntime creates a new Docker runtime
func NewDockerRuntime(config Config) (*DockerRuntime, error) {
    var clientOpts []client.Opt
    
    if config.Endpoint != "" {
        clientOpts = append(clientOpts, client.WithHost(config.Endpoint))
    }
    
    cli, err := client.NewClientWithOpts(clientOpts...)
    if err != nil {
        return nil, fmt.Errorf("failed to create Docker client: %w", err)
    }
    
    // Negotiate API version
    cli.NegotiateAPIVersion(context.Background())
    
    // Initialize image validator if security is enabled
    var imageValidator validation.ImageValidator
    if config.EnableImageValidation {
        validatorConfig := &validation.ValidationConfig{
            AllowedRegistries:   config.AllowedRegistries,
            BlockedRegistries:   config.BlockedRegistries,
            RequireDigest:       config.RequireImageDigest,
            EnableScanning:      config.EnableSecurityScan,
            MaxCriticalCVEs:     0,
            MaxHighCVEs:         config.MaxHighCVEs,
            MaxMediumCVEs:       config.MaxMediumCVEs,
            MaxLowCVEs:          -1, // No limit on low severity
        }
        imageValidator = validation.NewDefaultImageValidator(validatorConfig)
    }
    
    return &DockerRuntime{
        client:         cli,
        config:         config,
        imageValidator: imageValidator,
    }, nil
}

// CreateAgent creates a new agent container
func (r *DockerRuntime) CreateAgent(ctx context.Context, agent *arctypes.Agent) error {
    // Validate image if validator is configured
    if r.imageValidator != nil {
        if err := r.imageValidator.ValidateImage(ctx, agent.Image); err != nil {
            return fmt.Errorf("image validation failed: %w", err)
        }
        
        // Perform security scan if configured
        if r.config.EnableSecurityScan {
            scanResult, err := r.imageValidator.ScanImage(ctx, agent.Image)
            if err != nil {
                return fmt.Errorf("image security scan failed: %w", err)
            }
            
            // Check if vulnerabilities exceed thresholds
            if scanResult.CriticalCount > 0 && !r.config.AllowCriticalCVEs {
                return fmt.Errorf("image contains %d critical vulnerabilities", scanResult.CriticalCount)
            }
            if scanResult.HighCount > r.config.MaxHighCVEs {
                return fmt.Errorf("image contains %d high vulnerabilities (max allowed: %d)", 
                    scanResult.HighCount, r.config.MaxHighCVEs)
            }
        }
    }
    
    // Build container config
    containerConfig := &container.Config{
        Image:        agent.Image,
        Env:          r.buildEnvironment(agent),
        Cmd:          agent.Config.Args,
        WorkingDir:   agent.Config.WorkingDir,
        Labels:       r.mergeLabels(agent.Labels),
        AttachStdout: true,
        AttachStderr: true,
    }
    
    if len(agent.Config.Command) > 0 {
        containerConfig.Entrypoint = agent.Config.Command
    }
    
    // Build host config
    hostConfig := &container.HostConfig{
        RestartPolicy: container.RestartPolicy{
            Name: "no",
        },
        Resources: r.buildResourceConstraints(agent.Config.Resources),
    }
    
    // Add volume mounts
    for _, vol := range agent.Config.Volumes {
        hostConfig.Binds = append(hostConfig.Binds, fmt.Sprintf("%s:%s", vol.Name, vol.MountPath))
    }
    
    // Create container
    resp, err := r.client.ContainerCreate(
        ctx,
        containerConfig,
        hostConfig,
        nil, // NetworkingConfig
        nil, // Platform
        fmt.Sprintf("arc-agent-%s", agent.ID),
    )
    
    if err != nil {
        return fmt.Errorf("failed to create container: %w", err)
    }
    
    // Update agent with container ID
    agent.ContainerID = resp.ID
    agent.Status = arctypes.AgentStatusCreating
    
    return nil
}

// StartAgent starts an agent container
func (r *DockerRuntime) StartAgent(ctx context.Context, agentID string) error {
    containerID, err := r.findContainerByAgentID(ctx, agentID)
    if err != nil {
        return err
    }
    
    if err := r.client.ContainerStart(ctx, containerID, container.StartOptions{}); err != nil {
        return fmt.Errorf("failed to start container: %w", err)
    }
    
    return nil
}

// StopAgent stops an agent container
func (r *DockerRuntime) StopAgent(ctx context.Context, agentID string) error {
    containerID, err := r.findContainerByAgentID(ctx, agentID)
    if err != nil {
        return err
    }
    
    timeout := 30 // seconds
    if err := r.client.ContainerStop(ctx, containerID, container.StopOptions{
        Timeout: &timeout,
    }); err != nil {
        return fmt.Errorf("failed to stop container: %w", err)
    }
    
    return nil
}

// DestroyAgent removes an agent container
func (r *DockerRuntime) DestroyAgent(ctx context.Context, agentID string) error {
    containerID, err := r.findContainerByAgentID(ctx, agentID)
    if err != nil {
        return err
    }
    
    if err := r.client.ContainerRemove(ctx, containerID, container.RemoveOptions{
        Force: true,
    }); err != nil {
        return fmt.Errorf("failed to remove container: %w", err)
    }
    
    return nil
}

// GetAgentStatus returns the current status of an agent
func (r *DockerRuntime) GetAgentStatus(ctx context.Context, agentID string) (*arctypes.Agent, error) {
    containerID, err := r.findContainerByAgentID(ctx, agentID)
    if err != nil {
        return nil, err
    }
    
    inspect, err := r.client.ContainerInspect(ctx, containerID)
    if err != nil {
        return nil, fmt.Errorf("failed to inspect container: %w", err)
    }
    
    // Parse creation time from string to time.Time
    createdAt, err := time.Parse(time.RFC3339Nano, inspect.Created)
    if err != nil {
        return nil, fmt.Errorf("failed to parse container creation time: %w", err)
    }
    
    agent := &arctypes.Agent{
        ID:          agentID,
        Name:        strings.TrimPrefix(inspect.Name, "/"),
        Image:       inspect.Config.Image,
        ContainerID: containerID,
        Labels:      inspect.Config.Labels,
        CreatedAt:   createdAt,
    }
    
    // Map Docker state to agent status
    switch inspect.State.Status {
    case "created":
        agent.Status = arctypes.AgentStatusCreating
    case "running":
        agent.Status = arctypes.AgentStatusRunning
        if inspect.State.StartedAt != "" {
            if startedAt, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil {
                agent.StartedAt = &startedAt
            }
        }
    case "exited":
        if inspect.State.ExitCode == 0 {
            agent.Status = arctypes.AgentStatusCompleted
        } else {
            agent.Status = arctypes.AgentStatusFailed
            agent.Error = fmt.Sprintf("exit code: %d", inspect.State.ExitCode)
        }
        if inspect.State.StartedAt != "" {
            if startedAt, err := time.Parse(time.RFC3339Nano, inspect.State.StartedAt); err == nil {
                agent.StartedAt = &startedAt
            }
        }
        if inspect.State.FinishedAt != "" {
            if finishedAt, err := time.Parse(time.RFC3339Nano, inspect.State.FinishedAt); err == nil {
                agent.CompletedAt = &finishedAt
            }
        }
    case "dead", "removing":
        agent.Status = arctypes.AgentStatusTerminated
    default:
        agent.Status = arctypes.AgentStatusPending
    }
    
    return agent, nil
}

// ListAgents returns all agents managed by this runtime
func (r *DockerRuntime) ListAgents(ctx context.Context) ([]*arctypes.Agent, error) {
    // List containers with arc labels
    filterArgs := filters.NewArgs()
    filterArgs.Add("label", "arc.agent.id")
    
    containers, err := r.client.ContainerList(ctx, container.ListOptions{
        All:     true,
        Filters: filterArgs,
    })
    
    if err != nil {
        return nil, fmt.Errorf("failed to list containers: %w", err)
    }
    
    var agents []*arctypes.Agent
    for _, c := range containers {
        agentID, ok := c.Labels["arc.agent.id"]
        if !ok {
            continue
        }
        
        agent := &arctypes.Agent{
            ID:          agentID,
            Name:        strings.Join(c.Names, ","),
            Image:       c.Image,
            ContainerID: c.ID,
            Labels:      c.Labels,
            CreatedAt:   time.Unix(c.Created, 0),
        }
        
        // Map state
        switch c.State {
        case "created":
            agent.Status = arctypes.AgentStatusCreating
        case "running":
            agent.Status = arctypes.AgentStatusRunning
        case "exited":
            agent.Status = arctypes.AgentStatusCompleted
        case "dead":
            agent.Status = arctypes.AgentStatusFailed
        default:
            agent.Status = arctypes.AgentStatusPending
        }
        
        agents = append(agents, agent)
    }
    
    return agents, nil
}

// StreamLogs streams logs from an agent container
func (r *DockerRuntime) StreamLogs(ctx context.Context, agentID string) (io.ReadCloser, error) {
    containerID, err := r.findContainerByAgentID(ctx, agentID)
    if err != nil {
        return nil, err
    }
    
    options := container.LogsOptions{
        ShowStdout: true,
        ShowStderr: true,
        Follow:     true,
        Timestamps: true,
    }
    
    reader, err := r.client.ContainerLogs(ctx, containerID, options)
    if err != nil {
        return nil, fmt.Errorf("failed to get container logs: %w", err)
    }
    
    // Create a pipe to demultiplex stdout/stderr
    pr, pw := io.Pipe()
    
    go func() {
        defer pw.Close()
        _, err := stdcopy.StdCopy(pw, pw, reader)
        if err != nil {
            pw.CloseWithError(err)
        }
    }()
    
    return pr, nil
}

// ExecCommand executes a command in an agent container
func (r *DockerRuntime) ExecCommand(ctx context.Context, agentID string, cmd []string) (io.ReadCloser, error) {
    containerID, err := r.findContainerByAgentID(ctx, agentID)
    if err != nil {
        return nil, err
    }
    
    execConfig := types.ExecConfig{
        Cmd:          cmd,
        AttachStdout: true,
        AttachStderr: true,
    }
    
    execResp, err := r.client.ContainerExecCreate(ctx, containerID, execConfig)
    if err != nil {
        return nil, fmt.Errorf("failed to create exec: %w", err)
    }
    
    attachResp, err := r.client.ContainerExecAttach(ctx, execResp.ID, types.ExecStartCheck{})
    if err != nil {
        return nil, fmt.Errorf("failed to attach to exec: %w", err)
    }
    
    // Create a pipe to demultiplex output
    pr, pw := io.Pipe()
    
    go func() {
        defer pw.Close()
        defer attachResp.Close()
        
        _, err := stdcopy.StdCopy(pw, pw, attachResp.Reader)
        if err != nil {
            pw.CloseWithError(err)
        }
    }()
    
    return pr, nil
}

// Helper functions

func (r *DockerRuntime) findContainerByAgentID(ctx context.Context, agentID string) (string, error) {
    filterArgs := filters.NewArgs()
    filterArgs.Add("label", fmt.Sprintf("arc.agent.id=%s", agentID))
    
    containers, err := r.client.ContainerList(ctx, container.ListOptions{
        All:     true,
        Filters: filterArgs,
    })
    
    if err != nil {
        return "", fmt.Errorf("failed to list containers: %w", err)
    }
    
    if len(containers) == 0 {
        return "", fmt.Errorf("container not found for agent %s", agentID)
    }
    
    return containers[0].ID, nil
}

func (r *DockerRuntime) buildEnvironment(agent *arctypes.Agent) []string {
    env := make([]string, 0, len(agent.Config.Environment))
    
    for key, value := range agent.Config.Environment {
        env = append(env, fmt.Sprintf("%s=%s", key, value))
    }
    
    // Add AMQ configuration
    if agent.Config.MessageQueue.Brokers != nil {
        env = append(env, fmt.Sprintf("AMQ_BROKERS=%s", strings.Join(agent.Config.MessageQueue.Brokers, ",")))
    }
    if agent.Config.MessageQueue.Topics != nil {
        env = append(env, fmt.Sprintf("AMQ_TOPICS=%s", strings.Join(agent.Config.MessageQueue.Topics, ",")))
    }
    
    return env
}

func (r *DockerRuntime) mergeLabels(agentLabels map[string]string) map[string]string {
    labels := make(map[string]string)
    
    // Add default labels
    for k, v := range r.config.Labels {
        labels[k] = v
    }
    
    // Add agent labels
    for k, v := range agentLabels {
        labels[k] = v
    }
    
    // Add arc-specific labels
    labels["arc.managed"] = "true"
    
    // Extract agent ID from labels
    for k, v := range agentLabels {
        if k == "agent_id" || k == "arc.agent.id" {
            labels["arc.agent.id"] = v
            break
        }
    }
    
    return labels
}

func (r *DockerRuntime) buildResourceConstraints(req arctypes.ResourceRequirements) container.Resources {
    resources := container.Resources{}
    
    // Parse CPU limits
    if req.CPU != "" {
        // Convert CPU string to nanocpus
        // e.g., "1000m" = 1 CPU, "2" = 2 CPUs
        cpuValue := req.CPU
        if strings.HasSuffix(cpuValue, "m") {
            // Millicore notation
            cpuValue = strings.TrimSuffix(cpuValue, "m")
            if cpu, err := parseFloat(cpuValue); err == nil {
                resources.NanoCPUs = int64(cpu * 1e6) // Convert millicores to nanocpus
            }
        } else {
            // Full CPU notation
            if cpu, err := parseFloat(cpuValue); err == nil {
                resources.NanoCPUs = int64(cpu * 1e9) // Convert CPUs to nanocpus
            }
        }
    }
    
    // Parse memory limits
    if req.Memory != "" {
        // Convert memory string to bytes
        // Supports suffixes: Ki, Mi, Gi
        memValue := req.Memory
        multiplier := int64(1)
        
        if strings.HasSuffix(memValue, "Ki") {
            memValue = strings.TrimSuffix(memValue, "Ki")
            multiplier = 1024
        } else if strings.HasSuffix(memValue, "Mi") {
            memValue = strings.TrimSuffix(memValue, "Mi")
            multiplier = 1024 * 1024
        } else if strings.HasSuffix(memValue, "Gi") {
            memValue = strings.TrimSuffix(memValue, "Gi")
            multiplier = 1024 * 1024 * 1024
        }
        
        if mem, err := parseInt(memValue); err == nil {
            resources.Memory = mem * multiplier
        }
    }
    
    return resources
}

func parseFloat(s string) (float64, error) {
    var f float64
    _, err := fmt.Sscanf(s, "%f", &f)
    return f, err
}

func parseInt(s string) (int64, error) {
    var i int64
    _, err := fmt.Sscanf(s, "%d", &i)
    return i, err
}

// DockerFactory creates Docker runtime instances
type DockerFactory struct{}

// Create creates a new Docker runtime instance
func (f *DockerFactory) Create(config Config) (Runtime, error) {
    return NewDockerRuntime(config)
}