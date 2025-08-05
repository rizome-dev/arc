// Package runtime provides Docker runtime implementation
package runtime

import (
    "context"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
    "time"

    "github.com/docker/docker/api/types/container"
    "github.com/docker/docker/api/types/filters"
    "github.com/docker/docker/api/types/image"
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

// detectDockerSocket finds an available Docker socket from common locations
func detectDockerSocket() (string, []string) {
    var checkedPaths []string
    
    // Check DOCKER_HOST environment variable first
    if dockerHost := os.Getenv("DOCKER_HOST"); dockerHost != "" {
        checkedPaths = append(checkedPaths, fmt.Sprintf("DOCKER_HOST=%s", dockerHost))
        return dockerHost, checkedPaths
    }
    
    // Common Docker socket locations to check
    socketPaths := []string{
        "/var/run/docker.sock",                              // Linux default
        filepath.Join(os.Getenv("HOME"), ".docker/run/docker.sock"), // Docker Desktop on macOS
        "/run/user/" + fmt.Sprint(os.Getuid()) + "/docker.sock",     // Rootless Docker on Linux
        "/var/run/docker-desktop/docker.sock",               // Docker Desktop alternative
        filepath.Join(os.Getenv("HOME"), ".colima/docker.sock"),     // Colima
        filepath.Join(os.Getenv("HOME"), ".rd/docker.sock"),         // Rancher Desktop
        "/var/run/podman/podman.sock",                       // Podman compatibility
    }
    
    // Check each socket path
    for _, socketPath := range socketPaths {
        if socketPath == "" {
            continue
        }
        
        checkedPaths = append(checkedPaths, socketPath)
        
        // Check if the socket exists and is a socket file
        if info, err := os.Stat(socketPath); err == nil {
            if info.Mode()&os.ModeSocket != 0 {
                return "unix://" + socketPath, checkedPaths
            }
        }
    }
    
    // Return empty string if no socket found (will use Docker client defaults)
    return "", checkedPaths
}

// NewDockerRuntime creates a new Docker runtime
func NewDockerRuntime(config Config) (*DockerRuntime, error) {
    var clientOpts []client.Opt
    var checkedPaths []string
    
    // Use provided endpoint if available
    if config.Endpoint != "" {
        clientOpts = append(clientOpts, client.WithHost(config.Endpoint))
        fmt.Fprintf(os.Stderr, "[ARC] Using configured Docker endpoint: %s\n", config.Endpoint)
    } else {
        // Auto-detect Docker socket location
        detectedHost, paths := detectDockerSocket()
        checkedPaths = paths
        
        if detectedHost != "" {
            clientOpts = append(clientOpts, client.WithHost(detectedHost))
            fmt.Fprintf(os.Stderr, "[ARC] Detected Docker socket: %s\n", detectedHost)
        } else {
            // Try Docker client defaults as fallback
            fmt.Fprintf(os.Stderr, "[ARC] No Docker socket found, using Docker client defaults\n")
            fmt.Fprintf(os.Stderr, "[ARC] Checked paths: %v\n", checkedPaths)
        }
    }
    
    // Allow override via ARC_DOCKER_HOST environment variable
    if arcDockerHost := os.Getenv("ARC_DOCKER_HOST"); arcDockerHost != "" {
        clientOpts = []client.Opt{client.WithHost(arcDockerHost)}
        fmt.Fprintf(os.Stderr, "[ARC] Using ARC_DOCKER_HOST override: %s\n", arcDockerHost)
    }
    
    cli, err := client.NewClientWithOpts(clientOpts...)
    if err != nil {
        return nil, fmt.Errorf("failed to create Docker client: %w", err)
    }
    
    // Test connection and negotiate API version
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()
    
    fmt.Fprintf(os.Stderr, "[ARC] Testing Docker connection...\n")
    if _, err := cli.Ping(ctx); err != nil {
        socketHint := ""
        if config.Endpoint == "" {
            socketHint = fmt.Sprintf("\n[ARC] Checked locations:\n")
            for _, path := range checkedPaths {
                socketHint += fmt.Sprintf("  - %s\n", path)
            }
            socketHint += "\n[ARC] To specify a custom Docker socket, set ARC_DOCKER_HOST or DOCKER_HOST environment variable"
        }
        return nil, fmt.Errorf("cannot connect to Docker daemon: %w%s\n\n[ARC] Troubleshooting steps:\n1. Ensure Docker Desktop is running\n2. Try restarting Docker Desktop\n3. Check Docker Desktop settings -> Advanced -> Allow the default Docker socket to be used\n4. Set ARC_DOCKER_HOST=unix:///path/to/docker.sock if using a custom location", err, socketHint)
    }
    
    fmt.Fprintf(os.Stderr, "[ARC] Successfully connected to Docker daemon\n")
    
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
    // Ensure image has a tag
    imageName := agent.Image
    if !strings.Contains(imageName, ":") {
        imageName = imageName + ":latest"
    }
    agent.Image = imageName
    
    // Pull image if it doesn't exist locally
    fmt.Fprintf(os.Stderr, "[ARC] Checking for image: %s\n", imageName)
    
    // Check if image exists locally
    images, err := r.client.ImageList(ctx, image.ListOptions{})
    if err != nil {
        return fmt.Errorf("failed to list images: %w", err)
    }
    
    imageExists := false
    for _, img := range images {
        for _, tag := range img.RepoTags {
            if tag == imageName {
                imageExists = true
                break
            }
        }
        if imageExists {
            break
        }
    }
    
    // Pull image if it doesn't exist
    if !imageExists {
        fmt.Fprintf(os.Stderr, "[ARC] Pulling image: %s\n", imageName)
        reader, err := r.client.ImagePull(ctx, imageName, image.PullOptions{})
        if err != nil {
            return fmt.Errorf("failed to pull image %s: %w", imageName, err)
        }
        defer reader.Close()
        
        // Read the output to ensure the pull completes
        _, err = io.Copy(io.Discard, reader)
        if err != nil {
            return fmt.Errorf("failed to complete image pull for %s: %w", imageName, err)
        }
        fmt.Fprintf(os.Stderr, "[ARC] Successfully pulled image: %s\n", imageName)
    } else {
        fmt.Fprintf(os.Stderr, "[ARC] Image already exists locally: %s\n", imageName)
    }
    
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
        Labels:       r.mergeLabels(agent),
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
    
    execConfig := container.ExecOptions{
        Cmd:          cmd,
        AttachStdout: true,
        AttachStderr: true,
    }
    
    execResp, err := r.client.ContainerExecCreate(ctx, containerID, execConfig)
    if err != nil {
        return nil, fmt.Errorf("failed to create exec: %w", err)
    }
    
    attachResp, err := r.client.ContainerExecAttach(ctx, execResp.ID, container.ExecAttachOptions{})
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

func (r *DockerRuntime) mergeLabels(agent *arctypes.Agent) map[string]string {
    labels := make(map[string]string)
    
    // Add default labels
    for k, v := range r.config.Labels {
        labels[k] = v
    }
    
    // Add agent labels
    for k, v := range agent.Labels {
        labels[k] = v
    }
    
    // Add arc-specific labels
    labels["arc.managed"] = "true"
    labels["arc.agent.id"] = agent.ID
    
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