// Package main provides the ARC server command
package main

import (
	"flag"
	"log"
	"os"

	"github.com/rizome-dev/arc/pkg/messagequeue"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"github.com/rizome-dev/arc/pkg/runtime"
	"github.com/rizome-dev/arc/pkg/server"
	"github.com/rizome-dev/arc/pkg/state"
)

var (
	// Server configuration flags
	grpcPort = flag.Int("grpc-port", 9090, "gRPC server port")
	httpPort = flag.Int("http-port", 8080, "HTTP server port")
	host     = flag.String("host", "0.0.0.0", "Server host")
	
	// TLS configuration flags
	tlsEnabled = flag.Bool("tls", false, "Enable TLS")
	tlsCert    = flag.String("tls-cert", "", "TLS certificate file")
	tlsKey     = flag.String("tls-key", "", "TLS key file")
	
	// Backend configuration flags
	runtimeType = flag.String("runtime", "docker", "Runtime type (docker, kubernetes, gvisor)")
	stateType   = flag.String("state", "memory", "State backend type (memory, badger)")
	amqPath     = flag.String("amq-path", "./arc-amq-data", "AMQ storage path")
	
	// Help flag
	helpFlag = flag.Bool("help", false, "Show help message")
)

func main() {
	flag.Parse()

	if *helpFlag {
		showHelp()
		os.Exit(0)
	}

	log.Printf("Starting ARC server...")
	log.Printf("gRPC port: %d, HTTP port: %d, Host: %s", *grpcPort, *httpPort, *host)

	// Create server configuration
	config := &server.Config{
		GRPCPort:    *grpcPort,
		GRPCHost:    *host,
		HTTPPort:    *httpPort,
		HTTPHost:    *host,
		TLSEnabled:  *tlsEnabled,
		TLSCertFile: *tlsCert,
		TLSKeyFile:  *tlsKey,
	}

	// Initialize orchestrator
	orch, err := initializeOrchestrator()
	if err != nil {
		log.Fatalf("Failed to initialize orchestrator: %v", err)
	}

	// Start orchestrator
	if err := orch.Start(); err != nil {
		log.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer func() {
		if err := orch.Stop(); err != nil {
			log.Printf("Error stopping orchestrator: %v", err)
		}
	}()

	// Run server
	if err := server.RunServer(config, orch); err != nil {
		log.Fatalf("Server error: %v", err)
	}
}

func initializeOrchestrator() (*orchestrator.Orchestrator, error) {
	// Initialize runtime
	var rt runtime.Runtime
	var err error

	switch *runtimeType {
	case "docker":
		rt, err = runtime.NewDockerRuntime(runtime.Config{
			Type: "docker",
		})
	case "kubernetes":
		rt, err = runtime.NewKubernetesRuntime(runtime.Config{
			Type:      "kubernetes",
			Namespace: "default",
		})
	case "gvisor":
		rt, err = runtime.NewGVisorRuntime(runtime.Config{
			Type: "gvisor",
		})
	default:
		log.Fatalf("Unknown runtime type: %s", *runtimeType)
	}

	if err != nil {
		return nil, err
	}

	// Initialize state manager
	var sm state.StateManager

	switch *stateType {
	case "memory":
		sm = state.NewMemoryStore()
	case "badger":
		store, err := state.NewBadgerStore(state.BadgerStoreConfig{
			Path: *amqPath + "-state",
		})
		if err != nil {
			return nil, err
		}
		sm = store
	default:
		log.Fatalf("Unknown state type: %s", *stateType)
	}

	// Initialize message queue
	mqConfig := messagequeue.Config{
		StorePath:      *amqPath,
		WorkerPoolSize: 10,
		MessageTimeout: 30,
	}

	mq, err := messagequeue.NewAMQMessageQueue(mqConfig)
	if err != nil {
		return nil, err
	}

	// Create orchestrator
	orchConfig := orchestrator.Config{
		Runtime:      rt,
		MessageQueue: mq,
		StateManager: sm,
	}

	return orchestrator.New(orchConfig)
}

func showHelp() {
	log.Printf("ARC Server - Agent Orchestrator for Containers")
	log.Printf("")
	log.Printf("Usage:")
	log.Printf("  %s [flags]", os.Args[0])
	log.Printf("")
	log.Printf("Flags:")
	flag.PrintDefaults()
	log.Printf("")
	log.Printf("Examples:")
	log.Printf("  # Start server with default settings")
	log.Printf("  %s", os.Args[0])
	log.Printf("")
	log.Printf("  # Start server with custom ports")
	log.Printf("  %s -grpc-port 9091 -http-port 8081", os.Args[0])
	log.Printf("")
	log.Printf("  # Start server with TLS")
	log.Printf("  %s -tls -tls-cert server.crt -tls-key server.key", os.Args[0])
	log.Printf("")
	log.Printf("  # Start server with Kubernetes runtime")
	log.Printf("  %s -runtime kubernetes", os.Args[0])
	log.Printf("")
	log.Printf("  # Start server with BadgerDB state backend")
	log.Printf("  %s -state badger -amq-path /path/to/storage", os.Args[0])
}