// Package main provides the ARC orchestrator command-line interface
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"flag"

	"github.com/rizome-dev/arc/internal/version"
	"github.com/rizome-dev/arc/pkg/config"
	"github.com/rizome-dev/arc/pkg/logging"
	"github.com/rizome-dev/arc/pkg/messagequeue"
	"github.com/rizome-dev/arc/pkg/monitoring"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"github.com/rizome-dev/arc/pkg/runtime"
	"github.com/rizome-dev/arc/pkg/server"
	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
)

const (
	appName        = "arc"
	appDescription = "ARC - Agent oRchestrator for Containers"
)

// Command represents a CLI command
type Command struct {
	Name        string
	Description string
	Usage       string
	Run         func(*config.Config, []string) error
}

// Global flags
var (
	configFile   = flag.String("config", "", "Configuration file path (YAML or JSON)")
	logLevel     = flag.String("log-level", "info", "Log level (debug, info, warn, error)")
	logFormat    = flag.String("log-format", "json", "Log format (json, text)")
	pidFile      = flag.String("pid-file", "/var/run/arc.pid", "PID file path")
	versionFlag  = flag.Bool("version", false, "Show version information")
	help         = flag.Bool("help", false, "Show help information")
)

// Main application
func main() {
	flag.Parse()

	if *versionFlag {
		fmt.Println(version.String())
		os.Exit(0)
	}

	if *help {
		showHelp()
		os.Exit(0)
	}

	// Get command from arguments
	args := flag.Args()
	if len(args) == 0 {
		// Default to server command
		args = []string{"server"}
	}

	command := args[0]
	commandArgs := args[1:]

	// Load configuration
	cfg, err := loadConfiguration()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Override log configuration from flags
	if *logLevel != "" {
		cfg.Logging.Level = *logLevel
	}
	if *logFormat != "" {
		cfg.Logging.Format = *logFormat
	}

	// Setup logging
	if err := setupLogging(cfg.Logging); err != nil {
		log.Fatalf("Failed to setup logging: %v", err)
	}

	// Get command handler
	cmd, exists := getCommand(command)
	if !exists {
		log.Fatalf("Unknown command: %s", command)
	}

	// Run command
	if err := cmd.Run(cfg, commandArgs); err != nil {
		log.Fatalf("Command failed: %v", err)
	}
}

// loadConfiguration loads the application configuration
func loadConfiguration() (*config.Config, error) {
	return config.LoadConfig(*configFile)
}

// setupLogging configures structured logging
func setupLogging(cfg config.LoggingConfig) error {
	return logging.InitializeGlobalLogger(&cfg)
}

// getCommand returns the command handler for the given command name
func getCommand(name string) (*Command, bool) {
	commands := map[string]*Command{
		"server":    serverCommand(),
		"agent":     agentCommand(),
		"workflow":  workflowCommand(),
		"version":   versionCommand(),
		"config":    configCommand(),
		"health":    healthCommand(),
	}

	cmd, exists := commands[name]
	return cmd, exists
}

// showHelp displays the main help message
func showHelp() {
	fmt.Printf("%s - %s\n\n", appName, appDescription)
	fmt.Printf("Usage:\n")
	fmt.Printf("  %s [global-flags] <command> [command-flags] [arguments]\n\n", appName)

	fmt.Printf("Global Flags:\n")
	flag.PrintDefaults()

	fmt.Printf("\nCommands:\n")
	fmt.Printf("  server     Start the ARC server\n")
	fmt.Printf("  agent      Manage agents\n")
	fmt.Printf("  workflow   Manage workflows\n")
	fmt.Printf("  version    Show version information\n")
	fmt.Printf("  config     Configuration utilities\n")
	fmt.Printf("  health     Check system health\n")

	fmt.Printf("\nUse '%s <command> --help' for command-specific help.\n", appName)
	fmt.Printf("\nExamples:\n")
	fmt.Printf("  %s server --config /etc/arc/config.yaml\n", appName)
	fmt.Printf("  %s agent create --image ubuntu:latest --name test-agent\n", appName)
	fmt.Printf("  %s workflow create --file workflow.yaml\n", appName)
	fmt.Printf("  %s health\n", appName)
}

// serverCommand returns the server command
func serverCommand() *Command {
	return &Command{
		Name:        "server",
		Description: "Start the ARC server",
		Usage:       "arc server [flags]",
		Run:         runServer,
	}
}

// agentCommand returns the agent management command
func agentCommand() *Command {
	return &Command{
		Name:        "agent",
		Description: "Manage agents",
		Usage:       "arc agent <subcommand> [flags]",
		Run:         runAgent,
	}
}

// workflowCommand returns the workflow management command
func workflowCommand() *Command {
	return &Command{
		Name:        "workflow",
		Description: "Manage workflows",
		Usage:       "arc workflow <subcommand> [flags]",
		Run:         runWorkflow,
	}
}

// versionCommand returns the version command
func versionCommand() *Command {
	return &Command{
		Name:        "version",
		Description: "Show version information",
		Usage:       "arc version",
		Run:         runVersion,
	}
}

// configCommand returns the configuration utilities command
func configCommand() *Command {
	return &Command{
		Name:        "config",
		Description: "Configuration utilities",
		Usage:       "arc config <subcommand> [flags]",
		Run:         runConfig,
	}
}

// healthCommand returns the health check command
func healthCommand() *Command {
	return &Command{
		Name:        "health",
		Description: "Check system health",
		Usage:       "arc health [flags]",
		Run:         runHealth,
	}
}

// runServer runs the ARC server
func runServer(cfg *config.Config, args []string) error {
	log.Printf("Starting ARC server...")

	// Create PID file
	if err := createPIDFile(*pidFile); err != nil {
		return fmt.Errorf("failed to create PID file: %w", err)
	}
	defer removePIDFile(*pidFile)

	// Initialize context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Setup signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

	// Initialize monitoring
	monitor, err := monitoring.NewMonitor(&cfg.Monitoring)
	if err != nil {
		return fmt.Errorf("failed to initialize monitoring: %w", err)
	}

	// Start monitoring services
	if err := monitor.Start(ctx); err != nil {
		return fmt.Errorf("failed to start monitoring: %w", err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := monitor.Stop(shutdownCtx); err != nil {
			log.Printf("Error stopping monitoring: %v", err)
		}
	}()

	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, monitor)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	// Start orchestrator
	if err := orch.Start(); err != nil {
		return fmt.Errorf("failed to start orchestrator: %w", err)
	}
	defer func() {
		if err := orch.Stop(); err != nil {
			log.Printf("Error stopping orchestrator: %v", err)
		}
	}()

	// Create server configuration
	serverConfig := &server.Config{
		GRPCPort:            cfg.Server.GRPC.Port,
		GRPCHost:            cfg.Server.GRPC.Host,
		HTTPPort:            cfg.Server.HTTP.Port,
		HTTPHost:            cfg.Server.HTTP.Host,
		TLSEnabled:          cfg.Server.TLS.Enabled,
		TLSCertFile:         cfg.Server.TLS.CertFile,
		TLSKeyFile:          cfg.Server.TLS.KeyFile,
		ShutdownTimeout:     cfg.Server.ShutdownTimeout,
		MaxRecvMsgSize:      cfg.Server.GRPC.MaxRecvMsgSize,
		MaxSendMsgSize:      cfg.Server.GRPC.MaxSendMsgSize,
		ConnectionTimeout:   cfg.Server.GRPC.ConnectionTimeout,
		HealthCheckInterval: 30 * time.Second,
		MaxConnections:      cfg.Server.MaxConnections,
		ReadTimeout:         cfg.Server.HTTP.ReadTimeout,
		WriteTimeout:        cfg.Server.HTTP.WriteTimeout,
		IdleTimeout:         cfg.Server.HTTP.IdleTimeout,
		ReflectionEnabled:   cfg.Server.GRPC.ReflectionEnabled,
	}

	// Create and start server with monitoring
	srv, err := server.NewServerWithMonitoring(serverConfig, orch, monitor, cfg)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}

	if err := srv.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}

	log.Printf("ARC server started successfully")
	log.Printf("gRPC server: %s", srv.GetGRPCAddress())
	log.Printf("HTTP server: %s", srv.GetHTTPAddress())

	// Handle signals
	go func() {
		for {
			select {
			case sig := <-sigChan:
				log.Printf("Received signal: %v", sig)
				switch sig {
				case syscall.SIGHUP:
					log.Printf("Reloading configuration...")
					// Implement configuration reload
				case syscall.SIGINT, syscall.SIGTERM:
					log.Printf("Shutting down...")
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	// Wait for shutdown
	<-ctx.Done()

	log.Printf("Shutting down ARC server...")

	// Graceful shutdown
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), cfg.Server.ShutdownTimeout)
	defer shutdownCancel()

	if err := srv.Stop(shutdownCtx); err != nil {
		log.Printf("Server shutdown error: %v", err)
	}

	log.Printf("ARC server shut down successfully")
	return nil
}

// runAgent handles agent management commands
func runAgent(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("agent subcommand required (create, list, get, delete)")
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "create":
		return createAgent(cfg, subArgs)
	case "list":
		return listAgents(cfg, subArgs)
	case "get":
		return getAgent(cfg, subArgs)
	case "delete":
		return deleteAgent(cfg, subArgs)
	case "logs":
		return getAgentLogs(cfg, subArgs)
	default:
		return fmt.Errorf("unknown agent subcommand: %s", subcommand)
	}
}

// runWorkflow handles workflow management commands
func runWorkflow(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("workflow subcommand required (create, list, get, delete)")
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "create":
		return createWorkflow(cfg, subArgs)
	case "list":
		return listWorkflows(cfg, subArgs)
	case "get":
		return getWorkflow(cfg, subArgs)
	case "delete":
		return deleteWorkflow(cfg, subArgs)
	case "start":
		return startWorkflow(cfg, subArgs)
	case "stop":
		return stopWorkflow(cfg, subArgs)
	default:
		return fmt.Errorf("unknown workflow subcommand: %s", subcommand)
	}
}

// runVersion displays version information
func runVersion(cfg *config.Config, args []string) error {
	fmt.Println(version.String())
	return nil
}

// runConfig handles configuration utilities
func runConfig(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("config subcommand required (generate, validate, show)")
	}

	subcommand := args[0]
	subArgs := args[1:]

	switch subcommand {
	case "generate":
		return generateConfig(cfg, subArgs)
	case "validate":
		return validateConfig(cfg, subArgs)
	case "show":
		return showConfig(cfg, subArgs)
	default:
		return fmt.Errorf("unknown config subcommand: %s", subcommand)
	}
}

// runHealth performs health checks
func runHealth(cfg *config.Config, args []string) error {
	log.Printf("Performing health checks...")

	// Initialize monitoring for health checks
	monitor, err := monitoring.NewMonitor(&cfg.Monitoring)
	if err != nil {
		return fmt.Errorf("failed to initialize monitoring: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	status := monitor.GetHealthStatus(ctx)

	// Print health status
	data, err := json.MarshalIndent(status, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal health status: %w", err)
	}

	fmt.Println(string(data))

	if status.Status != "healthy" {
		os.Exit(1)
	}

	return nil
}

// Agent management functions

func createAgent(cfg *config.Config, args []string) error {
	// Parse flags for agent creation
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	name := fs.String("name", "", "Agent name")
	image := fs.String("image", "", "Container image")
	envFlags := fs.String("env", "", "Environment variables (KEY=VALUE,KEY2=VALUE2)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *name == "" || *image == "" {
		return fmt.Errorf("name and image are required")
	}

	// Parse environment variables
	env := make(map[string]string)
	if *envFlags != "" {
		for _, pair := range strings.Split(*envFlags, ",") {
			parts := strings.SplitN(pair, "=", 2)
			if len(parts) == 2 {
				env[parts[0]] = parts[1]
			}
		}
	}

	// Create agent configuration
	agentConfig := types.AgentConfig{
		Image:       *image,
		Environment: env,
		MessageQueue: types.MessageQueueConfig{
			Topics: []string{"default"},
		},
	}

	// Initialize orchestrator to create real-time agent
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	// Create agent
	agent := &types.Agent{
		Name:   *name,
		Image:  *image,
		Config: agentConfig,
		Status: types.AgentStatusPending,
	}

	// Create real-time agent config
	rtaConfig := orchestrator.RealTimeAgentConfig{
		Agent:  agent,
		Topics: []string{"default"},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = orch.CreateRealTimeAgent(ctx, rtaConfig)
	if err != nil {
		return fmt.Errorf("failed to create agent: %w", err)
	}

	fmt.Printf("Real-time agent '%s' created successfully\n", *name)

	return nil
}

func listAgents(cfg *config.Config, args []string) error {
	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	agents, err := orch.ListRealTimeAgents(ctx)
	if err != nil {
		return fmt.Errorf("failed to list agents: %w", err)
	}

	fmt.Printf("Found %d real-time agents:\n", len(agents))
	for _, agent := range agents {
		fmt.Printf("- ID: %s, Name: %s, Status: %s, Image: %s\n",
			agent.ID, agent.Name, agent.Status, agent.Image)
	}

	return nil
}

func getAgent(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("agent ID required")
	}

	agentID := args[0]

	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	agent, err := orch.GetRealTimeAgent(ctx, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent: %w", err)
	}

	data, err := json.MarshalIndent(agent, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal agent: %w", err)
	}

	fmt.Println(string(data))
	return nil
}

func deleteAgent(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("agent ID required")
	}

	agentID := args[0]

	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := orch.StopRealTimeAgent(ctx, agentID); err != nil {
		return fmt.Errorf("failed to stop agent: %w", err)
	}

	fmt.Printf("Agent %s stopped successfully\n", agentID)
	return nil
}

func getAgentLogs(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("agent ID required")
	}

	agentID := args[0]

	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Get agent first to check if it exists
	agent, err := orch.GetRealTimeAgent(ctx, agentID)
	if err != nil {
		return fmt.Errorf("failed to get agent: %w", err)
	}

	fmt.Printf("Agent logs for %s (ContainerID: %s) - logs functionality would be implemented here\n", agent.Name, agent.ContainerID)
	return nil
}

// Workflow management functions

func createWorkflow(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("create", flag.ContinueOnError)
	file := fs.String("file", "", "Workflow definition file (YAML or JSON)")
	name := fs.String("name", "", "Workflow name")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *file == "" && *name == "" {
		return fmt.Errorf("either file or name is required")
	}

	var workflow *types.Workflow

	if *file != "" {
		// Load workflow from file
		data, err := os.ReadFile(*file)
		if err != nil {
			return fmt.Errorf("failed to read workflow file: %w", err)
		}

		workflow = &types.Workflow{}
		if err := json.Unmarshal(data, workflow); err != nil {
			return fmt.Errorf("failed to parse workflow: %w", err)
		}
	} else {
		// Create simple workflow
		workflow = &types.Workflow{
			Name:        *name,
			Description: "Workflow created via CLI",
			Status:      types.WorkflowStatusPending,
		}
	}

	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	err = orch.CreateWorkflow(ctx, workflow)
	if err != nil {
		return fmt.Errorf("failed to create workflow: %w", err)
	}

	fmt.Printf("Workflow '%s' created successfully\n", workflow.Name)

	return nil
}

func listWorkflows(cfg *config.Config, args []string) error {
	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	workflows, err := orch.ListWorkflows(ctx, map[string]string{})
	if err != nil {
		return fmt.Errorf("failed to list workflows: %w", err)
	}

	fmt.Printf("Found %d workflows:\n", len(workflows))
	for _, workflow := range workflows {
		fmt.Printf("- ID: %s, Name: %s, Status: %s, Tasks: %d\n",
			workflow.ID, workflow.Name, workflow.Status, len(workflow.Tasks))
	}

	return nil
}

func getWorkflow(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("workflow ID required")
	}

	workflowID := args[0]

	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	workflow, err := orch.GetWorkflow(ctx, workflowID)
	if err != nil {
		return fmt.Errorf("failed to get workflow: %w", err)
	}

	data, err := json.MarshalIndent(workflow, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal workflow: %w", err)
	}

	fmt.Println(string(data))
	return nil
}

func deleteWorkflow(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("workflow ID required")
	}

	workflowID := args[0]

	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := orch.StopWorkflow(ctx, workflowID); err != nil {
		return fmt.Errorf("failed to stop workflow: %w", err)
	}

	fmt.Printf("Workflow %s stopped successfully\n", workflowID)
	return nil
}

func startWorkflow(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("workflow ID required")
	}

	workflowID := args[0]

	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := orch.StartWorkflow(ctx, workflowID); err != nil {
		return fmt.Errorf("failed to start workflow: %w", err)
	}

	fmt.Printf("Workflow %s started successfully\n", workflowID)
	return nil
}

func stopWorkflow(cfg *config.Config, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("workflow ID required")
	}

	workflowID := args[0]

	// Initialize orchestrator
	orch, err := initializeOrchestrator(cfg, nil)
	if err != nil {
		return fmt.Errorf("failed to initialize orchestrator: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := orch.StopWorkflow(ctx, workflowID); err != nil {
		return fmt.Errorf("failed to stop workflow: %w", err)
	}

	fmt.Printf("Workflow %s stopped successfully\n", workflowID)
	return nil
}

// Configuration utility functions

func generateConfig(cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("generate", flag.ContinueOnError)
	output := fs.String("output", "arc-config.yaml", "Output file path")

	if err := fs.Parse(args); err != nil {
		return err
	}

	defaultConfig := config.DefaultConfig()

	if err := defaultConfig.SaveToFile(*output); err != nil {
		return fmt.Errorf("failed to save config: %w", err)
	}

	fmt.Printf("Configuration file generated: %s\n", *output)
	return nil
}

func validateConfig(cfg *config.Config, args []string) error {
	if err := cfg.Validate(); err != nil {
		fmt.Printf("Configuration validation failed: %v\n", err)
		os.Exit(1)
	}

	fmt.Println("Configuration is valid")
	return nil
}

func showConfig(cfg *config.Config, args []string) error {
	fmt.Println(cfg.String())
	return nil
}

// Helper functions

// initializeOrchestrator initializes the orchestrator with all dependencies
func initializeOrchestrator(cfg *config.Config, monitor *monitoring.Monitor) (*orchestrator.Orchestrator, error) {
	// Initialize runtime
	rt, err := initializeRuntime(cfg.Runtime)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize runtime: %w", err)
	}

	// Initialize state manager
	sm, err := initializeStateManager(cfg.State)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize state manager: %w", err)
	}

	// Initialize message queue
	mq, err := initializeMessageQueue(cfg.MessageQueue)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize message queue: %w", err)
	}

	// Create orchestrator configuration
	orchConfig := orchestrator.Config{
		Runtime:      rt,
		MessageQueue: mq,
		StateManager: sm,
	}

	return orchestrator.New(orchConfig)
}

// initializeRuntime initializes the container runtime
func initializeRuntime(cfg config.RuntimeConfig) (runtime.Runtime, error) {
	switch cfg.Type {
	case "docker":
		return runtime.NewDockerRuntime(runtime.Config{
			Type: "docker",
		})
	case "kubernetes":
		return runtime.NewKubernetesRuntime(runtime.Config{
			Type:      "kubernetes",
			Namespace: cfg.Namespace,
		})
	case "gvisor":
		return runtime.NewGVisorRuntime(runtime.Config{
			Type: "gvisor",
		})
	default:
		return nil, fmt.Errorf("unsupported runtime type: %s", cfg.Type)
	}
}

// initializeStateManager initializes the state management backend
func initializeStateManager(cfg config.StateConfig) (state.StateManager, error) {
	switch cfg.Type {
	case "memory":
		return state.NewMemoryStore(), nil
	case "badger":
		path := cfg.ConnectionURL
		if path == "" {
			path = "./arc-state"
		}
		return state.NewBadgerStore(state.BadgerStoreConfig{
			Path: path,
		})
	default:
		return nil, fmt.Errorf("unsupported state type: %s", cfg.Type)
	}
}

// initializeMessageQueue initializes the message queue
func initializeMessageQueue(cfg config.MessageQueueConfig) (messagequeue.MessageQueue, error) {
	mqConfig := messagequeue.Config{
		StorePath:      cfg.StorePath,
		WorkerPoolSize: cfg.WorkerPoolSize,
		MessageTimeout: int(cfg.MessageTimeout.Seconds()),
	}

	return messagequeue.NewAMQMessageQueue(mqConfig)
}

// createPIDFile creates a PID file
func createPIDFile(pidFile string) error {
	if pidFile == "" {
		return nil
	}

	// Create directory if it doesn't exist
	dir := filepath.Dir(pidFile)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Write PID to file
	pid := os.Getpid()
	return os.WriteFile(pidFile, []byte(strconv.Itoa(pid)), 0644)
}

// removePIDFile removes the PID file
func removePIDFile(pidFile string) {
	if pidFile != "" {
		os.Remove(pidFile)
	}
}