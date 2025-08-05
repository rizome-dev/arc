package main

import (
	"context"
	"fmt"
	"log"
	"time"
	
	"github.com/rizome-dev/arc/pkg/messagequeue"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"github.com/rizome-dev/arc/pkg/runtime"
	"github.com/rizome-dev/arc/pkg/state"
	"github.com/rizome-dev/arc/pkg/types"
)

func main() {
	ctx := context.Background()
	
	// Try to create gVisor runtime, fall back to Docker if not available
	var runtimeInstance runtime.Runtime
	var runtimeName string
	
	gvisorRuntime, err := runtime.NewGVisorRuntime(runtime.Config{
		Type: "gvisor",
		Labels: map[string]string{
			"managed-by": "arc",
			"runtime": "gvisor",
		},
	})
	if err != nil {
		fmt.Printf("gVisor runtime not available (%v), falling back to Docker...\n", err)
		
		// Fall back to Docker runtime
		dockerRuntime, dockerErr := runtime.NewDockerRuntime(runtime.Config{
			Type: "docker",
			Labels: map[string]string{
				"managed-by": "arc",
				"runtime": "docker",
			},
		})
		if dockerErr != nil {
			log.Fatalf("Failed to create Docker runtime: %v", dockerErr)
		}
		runtimeInstance = dockerRuntime
		runtimeName = "Docker"
	} else {
		runtimeInstance = gvisorRuntime
		runtimeName = "gVisor"
	}
	
	// Create message queue
	mq, err := messagequeue.NewAMQMessageQueue(messagequeue.Config{
		StorePath:      "./arc-amq-data",
		WorkerPoolSize: 10,
		MessageTimeout: 300, // 5 minutes
	})
	if err != nil {
		log.Fatalf("Failed to create message queue: %v", err)
	}
	defer mq.Close()
	
	// Create state manager
	stateManager := state.NewMemoryStore()
	if err := stateManager.Initialize(ctx); err != nil {
		log.Fatalf("Failed to initialize state manager: %v", err)
	}
	defer stateManager.Close(ctx)
	
	// Create orchestrator
	arc, err := orchestrator.New(orchestrator.Config{
		Runtime:      runtimeInstance,
		MessageQueue: mq,
		StateManager: stateManager,
	})
	if err != nil {
		log.Fatalf("Failed to create orchestrator: %v", err)
	}
	
	// Start orchestrator
	if err := arc.Start(); err != nil {
		log.Fatalf("Failed to start orchestrator: %v", err)
	}
	defer arc.Stop()
	
	// Get runtime info if gVisor
	if gvisorRT, ok := runtimeInstance.(*runtime.GVisorRuntime); ok && runtimeName == "gVisor" {
		info, err := gvisorRT.GetRuntimeInfo(ctx)
		if err == nil {
			fmt.Printf("%s Runtime Info:\n", runtimeName)
			for k, v := range info {
				fmt.Printf("  %s: %v\n", k, v)
			}
			fmt.Println()
		}
	}
	
	// Create a sample workflow with sandboxed containers
	workflow := &types.Workflow{
		Name:        "secure-data-processing",
		Description: "A secure data processing workflow using gVisor sandboxing",
		Tasks: []types.Task{
			{
				Name: "fetch-data",
				AgentConfig: types.AgentConfig{
					Command: []string{"alpine"},
					Args:    []string{"sh", "-c", "echo 'Fetching data in gVisor sandbox...'; sleep 5; echo 'Data fetched securely!'"},
					Environment: map[string]string{
						"TASK_TYPE": "fetch",
						"RUNTIME": "gvisor",
					},
					MessageQueue: types.MessageQueueConfig{
						Topics: []string{"secure-pipeline"},
					},
					Resources: types.ResourceRequirements{
						CPU:    "500m",
						Memory: "256Mi",
					},
				},
			},
			{
				Name:         "process-data",
				Dependencies: []string{}, // Will be set after task IDs are generated
				AgentConfig: types.AgentConfig{
					Command: []string{"alpine"},
					Args:    []string{"sh", "-c", "echo 'Processing data in isolated environment...'; sleep 10; echo 'Data processed with isolation!'"},
					Environment: map[string]string{
						"TASK_TYPE": "process",
						"RUNTIME": "gvisor",
					},
					MessageQueue: types.MessageQueueConfig{
						Topics: []string{"secure-pipeline"},
					},
					Resources: types.ResourceRequirements{
						CPU:    "1000m",
						Memory: "512Mi",
					},
				},
			},
			{
				Name:         "store-results",
				Dependencies: []string{}, // Will be set after task IDs are generated
				AgentConfig: types.AgentConfig{
					Command: []string{"alpine"},
					Args:    []string{"sh", "-c", "echo 'Storing results securely...'; sleep 3; echo 'Results stored in sandbox!'"},
					Environment: map[string]string{
						"TASK_TYPE": "store",
						"RUNTIME": "gvisor",
					},
					MessageQueue: types.MessageQueueConfig{
						Topics: []string{"secure-pipeline"},
					},
					Resources: types.ResourceRequirements{
						CPU:    "250m",
						Memory: "128Mi",
					},
				},
			},
		},
	}
	
	// Create workflow
	if err := arc.CreateWorkflow(ctx, workflow); err != nil {
		log.Fatalf("Failed to create workflow: %v", err)
	}
	
	// Set dependencies after workflow creation (tasks now have IDs)
	workflow.Tasks[1].Dependencies = []string{workflow.Tasks[0].ID}
	workflow.Tasks[2].Dependencies = []string{workflow.Tasks[1].ID}
	
	fmt.Printf("Created workflow: %s (ID: %s)\n", workflow.Name, workflow.ID)
	fmt.Printf("Using %s runtime for container execution\n", runtimeName)
	if runtimeName == "gVisor" {
		fmt.Println("Enhanced security and isolation enabled")
	}
	fmt.Println()
	
	// Start workflow execution
	fmt.Printf("Starting workflow execution with %s runtime...\n", runtimeName)
	if err := arc.StartWorkflow(ctx, workflow.ID); err != nil {
		log.Fatalf("Failed to start workflow: %v", err)
	}
	
	// Monitor workflow progress
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			wf, err := arc.GetWorkflow(ctx, workflow.ID)
			if err != nil {
				log.Printf("Failed to get workflow status: %v", err)
				continue
			}
			
			fmt.Printf("\nWorkflow Status: %s\n", wf.Status)
			for _, task := range wf.Tasks {
				fmt.Printf("  Task '%s' [%s]: %s\n", task.Name, runtimeName, task.Status)
			}
			
			// Check if workflow is complete
			if wf.Status == types.WorkflowStatusCompleted {
				fmt.Printf("\nWorkflow completed successfully with %s runtime!\n", runtimeName)
				if runtimeName == "gVisor" {
					fmt.Println("All tasks ran in secure sandboxed environments.")
				}
				return
			} else if wf.Status == types.WorkflowStatusFailed {
				fmt.Printf("\nWorkflow failed: %s\n", wf.Error)
				return
			}
		case <-time.After(2 * time.Minute):
			fmt.Println("\nTimeout waiting for workflow to complete")
			return
		}
	}
}