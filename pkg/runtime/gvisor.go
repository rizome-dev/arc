// Package runtime provides gVisor runtime implementation
package runtime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	arctypes "github.com/rizome-dev/arc/pkg/types"
)

// GVisorRuntime implements Runtime interface for gVisor
type GVisorRuntime struct {
	config    Config
	mu        sync.RWMutex
	agents    map[string]*gVisorAgent
	rootDir   string
	logWriter io.Writer
}

// gVisorAgent represents a running gVisor container
type gVisorAgent struct {
	ID          string
	ContainerID string
	Config      *arctypes.AgentConfig
	Status      arctypes.AgentStatus
	StartedAt   *time.Time
	FinishedAt  *time.Time
	ExitCode    int
	Error       string
	LogFile     string
	BundleDir   string
}

// gVisorContainerState represents the state of a gVisor container
type gVisorContainerState struct {
	ID     string `json:"id"`
	Status string `json:"status"`
	PID    int    `json:"pid"`
	Bundle string `json:"bundle"`
}

// NewGVisorRuntime creates a new gVisor runtime
func NewGVisorRuntime(config Config) (*GVisorRuntime, error) {
	// Check if runsc is available
	if _, err := exec.LookPath("runsc"); err != nil {
		return nil, fmt.Errorf("runsc not found in PATH: %w", err)
	}

	// Create root directory for gVisor bundles
	rootDir := "/var/run/arc-gvisor"
	// Note: Config struct doesn't have RootDir field, using default

	if err := os.MkdirAll(rootDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create root directory: %w", err)
	}

	return &GVisorRuntime{
		config:    config,
		agents:    make(map[string]*gVisorAgent),
		rootDir:   rootDir,
		logWriter: os.Stdout,
	}, nil
}

// CreateAgent creates a new agent container using gVisor
func (r *GVisorRuntime) CreateAgent(ctx context.Context, agent *arctypes.Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Generate container ID
	containerID := fmt.Sprintf("arc-%s-%d", agent.ID, time.Now().Unix())
	
	// Create bundle directory
	bundleDir := filepath.Join(r.rootDir, containerID)
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return fmt.Errorf("failed to create bundle directory: %w", err)
	}

	// Create rootfs directory
	rootfsDir := filepath.Join(bundleDir, "rootfs")
	if err := os.MkdirAll(rootfsDir, 0755); err != nil {
		return fmt.Errorf("failed to create rootfs directory: %w", err)
	}

	// Create OCI config.json
	ociConfig := r.createOCIConfig(agent, rootfsDir)
	configPath := filepath.Join(bundleDir, "config.json")
	configData, err := json.MarshalIndent(ociConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal OCI config: %w", err)
	}
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return fmt.Errorf("failed to write OCI config: %w", err)
	}

	// Extract image if needed
	if err := r.extractImage(ctx, agent.Config.Command[0], rootfsDir); err != nil {
		return fmt.Errorf("failed to extract image: %w", err)
	}

	// Create log file
	logFile := filepath.Join(bundleDir, "container.log")

	// Store agent information
	r.agents[agent.ID] = &gVisorAgent{
		ID:          agent.ID,
		ContainerID: containerID,
		Config:      &agent.Config,
		Status:      arctypes.AgentStatusCreating,
		LogFile:     logFile,
		BundleDir:   bundleDir,
	}

	agent.ContainerID = containerID
	agent.Status = arctypes.AgentStatusCreating
	agent.CreatedAt = time.Now()

	return nil
}

// StartAgent starts the agent container
func (r *GVisorRuntime) StartAgent(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Create runsc command
	args := []string{
		"run",
		"--bundle", agent.BundleDir,
		"--network", "host", // Use host network for simplicity
		"--platform", "ptrace", // Use ptrace platform for compatibility
	}

	// Add debug logging if configured
	// Note: Config struct doesn't have Debug field, skipping debug logging

	// Add resource limits if specified
	if agent.Config.Resources.CPU != "" {
		// Convert CPU string to cores (e.g., "1000m" -> "1")
		cpuCores := r.parseCPULimit(agent.Config.Resources.CPU)
		args = append(args, "--cpu-num-from-quota", fmt.Sprintf("--total-cpu=%s", cpuCores))
	}

	if agent.Config.Resources.Memory != "" {
		// Convert memory string to bytes (e.g., "512Mi" -> "536870912")
		memBytes := r.parseMemoryLimit(agent.Config.Resources.Memory)
		args = append(args, "--total-memory", memBytes)
	}

	args = append(args, agent.ContainerID)

	// Start container in background
	cmd := exec.CommandContext(ctx, "runsc", args...)
	cmd.Dir = agent.BundleDir
	
	// Create log file for container output
	logFile, err := os.Create(agent.LogFile)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFile.Close()

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Start the container
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	// Update agent status
	now := time.Now()
	agent.Status = arctypes.AgentStatusRunning
	agent.StartedAt = &now

	// Start a goroutine to wait for container completion
	go func() {
		err := cmd.Wait()
		r.mu.Lock()
		defer r.mu.Unlock()
		
		finishedAt := time.Now()
		agent.FinishedAt = &finishedAt
		
		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				agent.ExitCode = exitErr.ExitCode()
				agent.Status = arctypes.AgentStatusFailed
				agent.Error = exitErr.Error()
			} else {
				agent.Status = arctypes.AgentStatusFailed
				agent.Error = err.Error()
			}
		} else {
			agent.ExitCode = 0
			agent.Status = arctypes.AgentStatusCompleted
		}
	}()

	return nil
}

// StopAgent stops the agent container
func (r *GVisorRuntime) StopAgent(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Kill the container
	cmd := exec.CommandContext(ctx, "runsc", "kill", agent.ContainerID, "TERM")
	if err := cmd.Run(); err != nil {
		// Try KILL signal if TERM fails
		cmd = exec.CommandContext(ctx, "runsc", "kill", agent.ContainerID, "KILL")
		if err := cmd.Run(); err != nil {
			return fmt.Errorf("failed to stop container: %w", err)
		}
	}

	// Wait for container to stop
	time.Sleep(2 * time.Second)

	// Delete the container
	cmd = exec.CommandContext(ctx, "runsc", "delete", agent.ContainerID)
	if err := cmd.Run(); err != nil {
		// Ignore error if container is already deleted
		if !strings.Contains(err.Error(), "not found") {
			return fmt.Errorf("failed to delete container: %w", err)
		}
	}

	agent.Status = arctypes.AgentStatusTerminated
	if agent.FinishedAt == nil {
		now := time.Now()
		agent.FinishedAt = &now
	}

	return nil
}

// DestroyAgent removes an agent container
func (r *GVisorRuntime) DestroyAgent(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Stop container if running
	if agent.Status == arctypes.AgentStatusRunning {
		cmd := exec.CommandContext(ctx, "runsc", "kill", agent.ContainerID, "KILL")
		_ = cmd.Run() // Ignore error
		
		time.Sleep(1 * time.Second)
	}
	
	// Delete the container
	cmd := exec.CommandContext(ctx, "runsc", "delete", agent.ContainerID)
	_ = cmd.Run() // Ignore error

	// Remove bundle directory
	if agent.BundleDir != "" {
		_ = os.RemoveAll(agent.BundleDir)
	}

	// Remove from agents map
	delete(r.agents, agentID)

	return nil
}

// GetAgentStatus returns the status of an agent
func (r *GVisorRuntime) GetAgentStatus(ctx context.Context, agentID string) (*arctypes.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	// Check container state using runsc
	cmd := exec.CommandContext(ctx, "runsc", "state", agent.ContainerID)
	output, err := cmd.Output()
	if err != nil {
		// Container might not exist anymore
		if strings.Contains(err.Error(), "not found") {
			if agent.Status == arctypes.AgentStatusRunning {
				agent.Status = arctypes.AgentStatusFailed
				agent.Error = "Container not found"
			}
		}
	} else {
		// Parse state output
		var state gVisorContainerState
		if err := json.Unmarshal(output, &state); err == nil {
			switch state.Status {
			case "running":
				agent.Status = arctypes.AgentStatusRunning
			case "stopped":
				if agent.ExitCode == 0 {
					agent.Status = arctypes.AgentStatusCompleted
				} else {
					agent.Status = arctypes.AgentStatusFailed
				}
			case "created":
				agent.Status = arctypes.AgentStatusCreating
			}
		}
	}

	// Create agent response
	return &arctypes.Agent{
		ID:          agent.ID,
		ContainerID: agent.ContainerID,
		Status:      agent.Status,
		Config:      *agent.Config,
		CreatedAt:   time.Now(), // This should be stored
		StartedAt:   agent.StartedAt,
		CompletedAt: agent.FinishedAt,
		Error:       agent.Error,
	}, nil
}

// GetAgentLogs returns the logs of an agent
func (r *GVisorRuntime) GetAgentLogs(ctx context.Context, agentID string, follow bool) (io.ReadCloser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	// Open log file
	file, err := os.Open(agent.LogFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	if !follow {
		return file, nil
	}

	// For follow mode, we need to tail the file
	reader, writer := io.Pipe()
	
	go func() {
		defer writer.Close()
		defer file.Close()
		
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			fmt.Fprintln(writer, scanner.Text())
		}
		
		// Continue following the file
		for agent.Status == arctypes.AgentStatusRunning {
			time.Sleep(100 * time.Millisecond)
			for scanner.Scan() {
				fmt.Fprintln(writer, scanner.Text())
			}
		}
	}()

	return reader, nil
}

// ListAgents lists all agents
func (r *GVisorRuntime) ListAgents(ctx context.Context) ([]*arctypes.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]*arctypes.Agent, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, &arctypes.Agent{
			ID:          agent.ID,
			ContainerID: agent.ContainerID,
			Status:      agent.Status,
			Config:      *agent.Config,
			CreatedAt:   time.Now(), // This should be stored
			StartedAt:   agent.StartedAt,
			CompletedAt: agent.FinishedAt,
			Error:       agent.Error,
		})
	}

	return agents, nil
}

// StreamLogs streams logs from an agent container (required by Runtime interface)
func (r *GVisorRuntime) StreamLogs(ctx context.Context, agentID string) (io.ReadCloser, error) {
	// Use the existing GetAgentLogs with follow=true
	return r.GetAgentLogs(ctx, agentID, true)
}

// ExecCommand executes a command in an agent container
func (r *GVisorRuntime) ExecCommand(ctx context.Context, agentID string, cmd []string) (io.ReadCloser, error) {
	r.mu.RLock()
	agent, exists := r.agents[agentID]
	r.mu.RUnlock()
	
	if !exists {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	// Execute command using runsc exec
	args := append([]string{"exec", agent.ContainerID}, cmd...)
	execCmd := exec.CommandContext(ctx, "runsc", args...)
	
	// Create pipe for output
	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}
	
	if err := execCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start exec command: %w", err)
	}
	
	// Return the stdout pipe as ReadCloser
	return stdout, nil
}

// CleanupAgent removes an agent and its resources
func (r *GVisorRuntime) CleanupAgent(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return nil // Already cleaned up
	}

	// Stop container if running
	if agent.Status == arctypes.AgentStatusRunning {
		cmd := exec.CommandContext(ctx, "runsc", "kill", agent.ContainerID, "KILL")
		_ = cmd.Run() // Ignore error
		
		time.Sleep(1 * time.Second)
		
		cmd = exec.CommandContext(ctx, "runsc", "delete", agent.ContainerID)
		_ = cmd.Run() // Ignore error
	}

	// Remove bundle directory
	if agent.BundleDir != "" {
		_ = os.RemoveAll(agent.BundleDir)
	}

	// Remove from agents map
	delete(r.agents, agentID)

	return nil
}

// GetRuntimeInfo returns information about the runtime
func (r *GVisorRuntime) GetRuntimeInfo(ctx context.Context) (map[string]interface{}, error) {
	// Get runsc version
	cmd := exec.CommandContext(ctx, "runsc", "--version")
	output, _ := cmd.Output()
	
	info := map[string]interface{}{
		"type":    "gvisor",
		"version": string(output),
		"rootDir": r.rootDir,
		"agents":  len(r.agents),
	}

	// Add platform info
	cmd = exec.CommandContext(ctx, "runsc", "debug", "--show-platform-info")
	if output, err := cmd.Output(); err == nil {
		info["platform"] = string(output)
	}

	return info, nil
}

// createOCIConfig creates an OCI runtime specification for the container
func (r *GVisorRuntime) createOCIConfig(agent *arctypes.Agent, rootfsPath string) map[string]interface{} {
	// Build command and args
	args := agent.Config.Command
	if len(agent.Config.Args) > 0 {
		args = append(args, agent.Config.Args...)
	}

	// Build environment variables
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOSTNAME=" + agent.ID,
	}
	for k, v := range agent.Config.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Build mounts
	mounts := []map[string]interface{}{
		{
			"destination": "/proc",
			"type":        "proc",
			"source":      "proc",
		},
		{
			"destination": "/dev",
			"type":        "tmpfs",
			"source":      "tmpfs",
			"options":     []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
		},
		{
			"destination": "/sys",
			"type":        "sysfs",
			"source":      "sysfs",
			"options":     []string{"nosuid", "noexec", "nodev", "ro"},
		},
	}

	// Add volume mounts
	for _, vol := range agent.Config.Volumes {
		mount := map[string]interface{}{
			"destination": vol.MountPath,
			"type":        "bind",
			"source":      vol.Name, // Assuming Name contains the source path
			"options":     []string{"rbind", "rprivate"},
		}
		if vol.ReadOnly {
			mount["options"] = append(mount["options"].([]string), "ro")
		}
		mounts = append(mounts, mount)
	}

	// Build resource limits
	resources := map[string]interface{}{
		"devices": []map[string]interface{}{
			{
				"allow": false,
				"access": "rwm",
			},
		},
	}

	if agent.Config.Resources.Memory != "" {
		memBytes := r.parseMemoryLimitToBytes(agent.Config.Resources.Memory)
		resources["memory"] = map[string]interface{}{
			"limit": memBytes,
			"reservation": memBytes / 2,
		}
	}

	if agent.Config.Resources.CPU != "" {
		cpuShares := r.parseCPUToShares(agent.Config.Resources.CPU)
		resources["cpu"] = map[string]interface{}{
			"shares": cpuShares,
		}
	}

	// Create OCI config
	return map[string]interface{}{
		"ociVersion": "1.0.2",
		"process": map[string]interface{}{
			"terminal": false,
			"user": map[string]interface{}{
				"uid": 0,
				"gid": 0,
			},
			"args": args,
			"env":  env,
			"cwd":  "/",
			"capabilities": map[string]interface{}{
				"bounding": []string{
					"CAP_AUDIT_WRITE",
					"CAP_KILL",
					"CAP_NET_BIND_SERVICE",
				},
				"effective": []string{
					"CAP_AUDIT_WRITE",
					"CAP_KILL",
					"CAP_NET_BIND_SERVICE",
				},
				"inheritable": []string{
					"CAP_AUDIT_WRITE",
					"CAP_KILL",
					"CAP_NET_BIND_SERVICE",
				},
				"permitted": []string{
					"CAP_AUDIT_WRITE",
					"CAP_KILL",
					"CAP_NET_BIND_SERVICE",
				},
			},
			"rlimits": []map[string]interface{}{
				{
					"type": "RLIMIT_NOFILE",
					"hard": 1024,
					"soft": 1024,
				},
			},
		},
		"root": map[string]interface{}{
			"path":     rootfsPath,
			"readonly": false,
		},
		"hostname": agent.ID,
		"mounts":   mounts,
		"linux": map[string]interface{}{
			"resources": resources,
			"namespaces": []map[string]interface{}{
				{"type": "pid"},
				{"type": "network"},
				{"type": "ipc"},
				{"type": "uts"},
				{"type": "mount"},
			},
			"maskedPaths": []string{
				"/proc/kcore",
				"/proc/latency_stats",
				"/proc/timer_list",
				"/proc/timer_stats",
				"/proc/sched_debug",
				"/sys/firmware",
			},
			"readonlyPaths": []string{
				"/proc/asound",
				"/proc/bus",
				"/proc/fs",
				"/proc/irq",
				"/proc/sys",
				"/proc/sysrq-trigger",
			},
		},
	}
}

// extractImage extracts a container image to the rootfs directory
func (r *GVisorRuntime) extractImage(ctx context.Context, image string, rootfsDir string) error {
	// For simplicity, we'll use Docker to export the image
	// In production, you might want to use a dedicated image extractor
	
	// Pull the image if needed
	pullCmd := exec.CommandContext(ctx, "docker", "pull", image)
	if err := pullCmd.Run(); err != nil {
		return fmt.Errorf("failed to pull image %s: %w", image, err)
	}

	// Create a temporary container
	createCmd := exec.CommandContext(ctx, "docker", "create", image)
	output, err := createCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to create container from image %s: %w", image, err)
	}
	containerID := strings.TrimSpace(string(output))

	// Export the container filesystem
	exportCmd := exec.CommandContext(ctx, "docker", "export", containerID)
	tarData, err := exportCmd.Output()
	if err != nil {
		return fmt.Errorf("failed to export container: %w", err)
	}

	// Extract tar to rootfs
	extractCmd := exec.CommandContext(ctx, "tar", "-xf", "-", "-C", rootfsDir)
	extractCmd.Stdin = bytes.NewReader(tarData)
	if err := extractCmd.Run(); err != nil {
		return fmt.Errorf("failed to extract rootfs: %w", err)
	}

	// Remove the temporary container
	rmCmd := exec.CommandContext(ctx, "docker", "rm", containerID)
	_ = rmCmd.Run() // Ignore error

	return nil
}

// parseCPULimit converts CPU string to cores (e.g., "1000m" -> "1")
func (r *GVisorRuntime) parseCPULimit(cpu string) string {
	if strings.HasSuffix(cpu, "m") {
		// Convert millicores to cores
		millis := strings.TrimSuffix(cpu, "m")
		var cores float64
		fmt.Sscanf(millis, "%f", &cores)
		return fmt.Sprintf("%.2f", cores/1000)
	}
	return cpu
}

// parseMemoryLimit converts memory string to bytes string
func (r *GVisorRuntime) parseMemoryLimit(memory string) string {
	return fmt.Sprintf("%d", r.parseMemoryLimitToBytes(memory))
}

// parseMemoryLimitToBytes converts memory string to bytes
func (r *GVisorRuntime) parseMemoryLimitToBytes(memory string) int64 {
	var bytes int64
	if strings.HasSuffix(memory, "Ki") {
		fmt.Sscanf(memory, "%dKi", &bytes)
		bytes *= 1024
	} else if strings.HasSuffix(memory, "Mi") {
		fmt.Sscanf(memory, "%dMi", &bytes)
		bytes *= 1024 * 1024
	} else if strings.HasSuffix(memory, "Gi") {
		fmt.Sscanf(memory, "%dGi", &bytes)
		bytes *= 1024 * 1024 * 1024
	} else {
		fmt.Sscanf(memory, "%d", &bytes)
	}
	return bytes
}

// parseCPUToShares converts CPU string to CPU shares
func (r *GVisorRuntime) parseCPUToShares(cpu string) int64 {
	if strings.HasSuffix(cpu, "m") {
		// Convert millicores to shares (1000m = 1024 shares)
		millis := strings.TrimSuffix(cpu, "m")
		var cores int64
		fmt.Sscanf(millis, "%d", &cores)
		return cores * 1024 / 1000
	}
	// Assume it's already in cores
	var cores int64
	fmt.Sscanf(cpu, "%d", &cores)
	return cores * 1024
}