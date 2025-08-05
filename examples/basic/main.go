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
    
    // Create runtime (Docker)
    dockerRuntime, err := runtime.NewDockerRuntime(runtime.Config{
        Type: "docker",
        Labels: map[string]string{
            "managed-by": "arc",
        },
    })
    if err != nil {
        log.Fatalf("Failed to create Docker runtime: %v", err)
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
        Runtime:      dockerRuntime,
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
    
    // Create a sample workflow
    workflow := &types.Workflow{
        Name:        "data-processing-pipeline",
        Description: "A sample data processing workflow",
        Tasks: []types.Task{
            {
                Name: "fetch-data",
                AgentConfig: types.AgentConfig{
                    Image:   "alpine:latest",
                    Command: []string{"sh"},
                    Args:    []string{"-c", "echo 'Fetching data...'; sleep 5; echo 'Data fetched!'"},
                    Environment: map[string]string{
                        "TASK_TYPE": "fetch",
                    },
                    MessageQueue: types.MessageQueueConfig{
                        Topics: []string{"data-pipeline"},
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
                    Image:   "alpine:latest",
                    Command: []string{"sh"},
                    Args:    []string{"-c", "echo 'Processing data...'; sleep 10; echo 'Data processed!'"},
                    Environment: map[string]string{
                        "TASK_TYPE": "process",
                    },
                    MessageQueue: types.MessageQueueConfig{
                        Topics: []string{"data-pipeline"},
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
                    Image:   "alpine:latest",
                    Command: []string{"sh"},
                    Args:    []string{"-c", "echo 'Storing results...'; sleep 3; echo 'Results stored!'"},
                    Environment: map[string]string{
                        "TASK_TYPE": "store",
                    },
                    MessageQueue: types.MessageQueueConfig{
                        Topics: []string{"data-pipeline"},
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
    
    // Start workflow execution
    fmt.Println("Starting workflow execution...")
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
                fmt.Printf("  Task '%s': %s\n", task.Name, task.Status)
            }
            
            // Check if workflow is complete
            if wf.Status == types.WorkflowStatusCompleted {
                fmt.Println("\nWorkflow completed successfully!")
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
