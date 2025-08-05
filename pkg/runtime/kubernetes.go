// Package runtime provides Kubernetes runtime implementation
package runtime

import (
    "context"
    "fmt"
    "io"
    "path/filepath"
    "strings"

    corev1 "k8s.io/api/core/v1"
    "k8s.io/apimachinery/pkg/api/resource"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/apimachinery/pkg/labels"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/kubernetes/scheme"
    "k8s.io/client-go/rest"
    "k8s.io/client-go/tools/clientcmd"
    "k8s.io/client-go/tools/remotecommand"
    "k8s.io/client-go/util/homedir"
    
    arctypes "github.com/rizome-dev/arc/pkg/types"
    "github.com/rizome-dev/arc/pkg/validation"
)

// KubernetesRuntime implements Runtime interface for Kubernetes
type KubernetesRuntime struct {
    client         kubernetes.Interface
    config         Config
    namespace      string
    imageValidator validation.ImageValidator
}

// NewKubernetesRuntime creates a new Kubernetes runtime
func NewKubernetesRuntime(config Config) (*KubernetesRuntime, error) {
    var kubeConfig *rest.Config
    var err error
    
    if config.KubeConfig != "" {
        // Use provided kubeconfig
        kubeConfig, err = clientcmd.BuildConfigFromFlags("", config.KubeConfig)
    } else if config.Endpoint != "" {
        // Use in-cluster config
        kubeConfig, err = rest.InClusterConfig()
    } else {
        // Try default kubeconfig location
        if home := homedir.HomeDir(); home != "" {
            kubeconfigPath := filepath.Join(home, ".kube", "config")
            kubeConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
        } else {
            return nil, fmt.Errorf("no kubeconfig specified and unable to find default")
        }
    }
    
    if err != nil {
        return nil, fmt.Errorf("failed to create Kubernetes config: %w", err)
    }
    
    // Create the clientset
    clientset, err := kubernetes.NewForConfig(kubeConfig)
    if err != nil {
        return nil, fmt.Errorf("failed to create Kubernetes client: %w", err)
    }
    
    namespace := config.Namespace
    if namespace == "" {
        namespace = "default"
    }
    
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
    
    return &KubernetesRuntime{
        client:         clientset,
        config:         config,
        namespace:      namespace,
        imageValidator: imageValidator,
    }, nil
}

// CreateAgent creates a new agent pod
func (r *KubernetesRuntime) CreateAgent(ctx context.Context, agent *arctypes.Agent) error {
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
    
    pod := &corev1.Pod{
        ObjectMeta: metav1.ObjectMeta{
            Name:      fmt.Sprintf("arc-agent-%s", agent.ID),
            Namespace: r.namespace,
            Labels:    r.mergeLabels(agent.Labels),
        },
        Spec: r.buildPodSpec(agent),
    }
    
    createdPod, err := r.client.CoreV1().Pods(r.namespace).Create(ctx, pod, metav1.CreateOptions{})
    if err != nil {
        return fmt.Errorf("failed to create pod: %w", err)
    }
    
    agent.ContainerID = string(createdPod.UID)
    agent.Namespace = r.namespace
    agent.Status = arctypes.AgentStatusCreating
    
    return nil
}

// StartAgent starts an agent pod (pods start automatically in K8s)
func (r *KubernetesRuntime) StartAgent(ctx context.Context, agentID string) error {
    // In Kubernetes, pods start automatically after creation
    // We'll just verify the pod exists and is not in a terminal state
    podName := fmt.Sprintf("arc-agent-%s", agentID)
    
    pod, err := r.client.CoreV1().Pods(r.namespace).Get(ctx, podName, metav1.GetOptions{})
    if err != nil {
        return fmt.Errorf("failed to get pod: %w", err)
    }
    
    if pod.Status.Phase == corev1.PodFailed || pod.Status.Phase == corev1.PodSucceeded {
        return fmt.Errorf("pod is in terminal state: %s", pod.Status.Phase)
    }
    
    return nil
}

// StopAgent stops an agent pod
func (r *KubernetesRuntime) StopAgent(ctx context.Context, agentID string) error {
    podName := fmt.Sprintf("arc-agent-%s", agentID)
    
    // Delete the pod with grace period
    gracePeriod := int64(30)
    deleteOptions := metav1.DeleteOptions{
        GracePeriodSeconds: &gracePeriod,
    }
    
    err := r.client.CoreV1().Pods(r.namespace).Delete(ctx, podName, deleteOptions)
    if err != nil {
        return fmt.Errorf("failed to delete pod: %w", err)
    }
    
    return nil
}

// DestroyAgent removes an agent pod immediately
func (r *KubernetesRuntime) DestroyAgent(ctx context.Context, agentID string) error {
    podName := fmt.Sprintf("arc-agent-%s", agentID)
    
    // Delete immediately
    gracePeriod := int64(0)
    deleteOptions := metav1.DeleteOptions{
        GracePeriodSeconds: &gracePeriod,
    }
    
    err := r.client.CoreV1().Pods(r.namespace).Delete(ctx, podName, deleteOptions)
    if err != nil {
        return fmt.Errorf("failed to delete pod: %w", err)
    }
    
    return nil
}

// GetAgentStatus returns the current status of an agent
func (r *KubernetesRuntime) GetAgentStatus(ctx context.Context, agentID string) (*arctypes.Agent, error) {
    podName := fmt.Sprintf("arc-agent-%s", agentID)
    
    pod, err := r.client.CoreV1().Pods(r.namespace).Get(ctx, podName, metav1.GetOptions{})
    if err != nil {
        return nil, fmt.Errorf("failed to get pod: %w", err)
    }
    
    agent := &arctypes.Agent{
        ID:          agentID,
        Name:        pod.Name,
        ContainerID: string(pod.UID),
        Namespace:   pod.Namespace,
        Labels:      pod.Labels,
        CreatedAt:   pod.CreationTimestamp.Time,
    }
    
    // Extract image from first container
    if len(pod.Spec.Containers) > 0 {
        agent.Image = pod.Spec.Containers[0].Image
    }
    
    // Map pod phase to agent status
    switch pod.Status.Phase {
    case corev1.PodPending:
        agent.Status = arctypes.AgentStatusCreating
    case corev1.PodRunning:
        agent.Status = arctypes.AgentStatusRunning
        if pod.Status.StartTime != nil {
            agent.StartedAt = &pod.Status.StartTime.Time
        }
    case corev1.PodSucceeded:
        agent.Status = arctypes.AgentStatusCompleted
        if pod.Status.StartTime != nil {
            agent.StartedAt = &pod.Status.StartTime.Time
        }
        // Get completion time from container status
        if len(pod.Status.ContainerStatuses) > 0 && pod.Status.ContainerStatuses[0].State.Terminated != nil {
            finishedAt := pod.Status.ContainerStatuses[0].State.Terminated.FinishedAt.Time
            agent.CompletedAt = &finishedAt
        }
    case corev1.PodFailed:
        agent.Status = arctypes.AgentStatusFailed
        if pod.Status.StartTime != nil {
            agent.StartedAt = &pod.Status.StartTime.Time
        }
        // Get error from container status
        if len(pod.Status.ContainerStatuses) > 0 && pod.Status.ContainerStatuses[0].State.Terminated != nil {
            agent.Error = pod.Status.ContainerStatuses[0].State.Terminated.Reason
            if pod.Status.ContainerStatuses[0].State.Terminated.Message != "" {
                agent.Error += ": " + pod.Status.ContainerStatuses[0].State.Terminated.Message
            }
            finishedAt := pod.Status.ContainerStatuses[0].State.Terminated.FinishedAt.Time
            agent.CompletedAt = &finishedAt
        }
    default:
        agent.Status = arctypes.AgentStatusPending
    }
    
    return agent, nil
}

// ListAgents returns all agents managed by this runtime
func (r *KubernetesRuntime) ListAgents(ctx context.Context) ([]*arctypes.Agent, error) {
    // List pods with arc labels
    labelSelector := labels.Set{"arc.managed": "true"}.AsSelector()
    
    pods, err := r.client.CoreV1().Pods(r.namespace).List(ctx, metav1.ListOptions{
        LabelSelector: labelSelector.String(),
    })
    
    if err != nil {
        return nil, fmt.Errorf("failed to list pods: %w", err)
    }
    
    var agents []*arctypes.Agent
    for _, pod := range pods.Items {
        agentID, ok := pod.Labels["arc.agent.id"]
        if !ok {
            continue
        }
        
        agent := &arctypes.Agent{
            ID:          agentID,
            Name:        pod.Name,
            ContainerID: string(pod.UID),
            Namespace:   pod.Namespace,
            Labels:      pod.Labels,
            CreatedAt:   pod.CreationTimestamp.Time,
        }
        
        // Extract image
        if len(pod.Spec.Containers) > 0 {
            agent.Image = pod.Spec.Containers[0].Image
        }
        
        // Map status
        switch pod.Status.Phase {
        case corev1.PodPending:
            agent.Status = arctypes.AgentStatusCreating
        case corev1.PodRunning:
            agent.Status = arctypes.AgentStatusRunning
        case corev1.PodSucceeded:
            agent.Status = arctypes.AgentStatusCompleted
        case corev1.PodFailed:
            agent.Status = arctypes.AgentStatusFailed
        default:
            agent.Status = arctypes.AgentStatusPending
        }
        
        agents = append(agents, agent)
    }
    
    return agents, nil
}

// StreamLogs streams logs from an agent pod
func (r *KubernetesRuntime) StreamLogs(ctx context.Context, agentID string) (io.ReadCloser, error) {
    podName := fmt.Sprintf("arc-agent-%s", agentID)
    
    req := r.client.CoreV1().Pods(r.namespace).GetLogs(podName, &corev1.PodLogOptions{
        Follow:     true,
        Timestamps: true,
    })
    
    stream, err := req.Stream(ctx)
    if err != nil {
        return nil, fmt.Errorf("failed to stream logs: %w", err)
    }
    
    return stream, nil
}

// ExecCommand executes a command in an agent pod
func (r *KubernetesRuntime) ExecCommand(ctx context.Context, agentID string, cmd []string) (io.ReadCloser, error) {
    podName := fmt.Sprintf("arc-agent-%s", agentID)
    
    // Build the exec request
    req := r.client.CoreV1().RESTClient().Post().
        Resource("pods").
        Name(podName).
        Namespace(r.namespace).
        SubResource("exec").
        VersionedParams(&corev1.PodExecOptions{
            Container: "agent", // Default container name
            Command:   cmd,
            Stdin:     false,
            Stdout:    true,
            Stderr:    true,
            TTY:       false,
        }, scheme.ParameterCodec)
    
    // Get the config for the executor
    config, err := r.getRestConfig()
    if err != nil {
        return nil, fmt.Errorf("failed to get REST config: %w", err)
    }
    
    // Create the executor
    executor, err := remotecommand.NewSPDYExecutor(config, "POST", req.URL())
    if err != nil {
        return nil, fmt.Errorf("failed to create executor: %w", err)
    }
    
    // Create pipes for output
    reader, writer := io.Pipe()
    
    // Execute the command in a goroutine
    go func() {
        defer writer.Close()
        
        // Stream the output
        err := executor.StreamWithContext(ctx, remotecommand.StreamOptions{
            Stdout: writer,
            Stderr: writer,
            Tty:    false,
        })
        
        if err != nil {
            // Write error to the pipe
            fmt.Fprintf(writer, "Execution error: %v\n", err)
        }
    }()
    
    return reader, nil
}

// getRestConfig returns the REST config used to create the client
func (r *KubernetesRuntime) getRestConfig() (*rest.Config, error) {
    // Try to reconstruct the config based on how the client was created
    // In production, we should store this during initialization
    var kubeConfig *rest.Config
    var err error
    
    if r.config.KubeConfig != "" {
        kubeConfig, err = clientcmd.BuildConfigFromFlags("", r.config.KubeConfig)
    } else if r.config.Endpoint != "" {
        kubeConfig, err = rest.InClusterConfig()
    } else {
        if home := homedir.HomeDir(); home != "" {
            kubeconfigPath := filepath.Join(home, ".kube", "config")
            kubeConfig, err = clientcmd.BuildConfigFromFlags("", kubeconfigPath)
        } else {
            return nil, fmt.Errorf("no kubeconfig specified")
        }
    }
    
    if err != nil {
        return nil, fmt.Errorf("failed to create Kubernetes config: %w", err)
    }
    
    return kubeConfig, nil
}

// Helper functions

func (r *KubernetesRuntime) buildPodSpec(agent *arctypes.Agent) corev1.PodSpec {
    container := corev1.Container{
        Name:       "agent",
        Image:      agent.Image,
        Command:    agent.Config.Command,
        Args:       agent.Config.Args,
        WorkingDir: agent.Config.WorkingDir,
        Env:        r.buildEnvironment(agent),
        Resources:  r.buildResourceRequirements(agent.Config.Resources),
    }
    
    // Add volume mounts
    var volumes []corev1.Volume
    for i, vol := range agent.Config.Volumes {
        volumeName := fmt.Sprintf("vol-%d", i)
        
        container.VolumeMounts = append(container.VolumeMounts, corev1.VolumeMount{
            Name:      volumeName,
            MountPath: vol.MountPath,
            ReadOnly:  vol.ReadOnly,
        })
        
        // Create emptyDir volume for now
        // In production, this would map to PVCs or other volume types
        volumes = append(volumes, corev1.Volume{
            Name: volumeName,
            VolumeSource: corev1.VolumeSource{
                EmptyDir: &corev1.EmptyDirVolumeSource{},
            },
        })
    }
    
    return corev1.PodSpec{
        RestartPolicy: corev1.RestartPolicyNever,
        Containers:    []corev1.Container{container},
        Volumes:       volumes,
    }
}

func (r *KubernetesRuntime) buildEnvironment(agent *arctypes.Agent) []corev1.EnvVar {
    var env []corev1.EnvVar
    
    for key, value := range agent.Config.Environment {
        env = append(env, corev1.EnvVar{
            Name:  key,
            Value: value,
        })
    }
    
    // Add AMQ configuration
    if agent.Config.MessageQueue.Brokers != nil {
        env = append(env, corev1.EnvVar{
            Name:  "AMQ_BROKERS",
            Value: strings.Join(agent.Config.MessageQueue.Brokers, ","),
        })
    }
    if agent.Config.MessageQueue.Topics != nil {
        env = append(env, corev1.EnvVar{
            Name:  "AMQ_TOPICS",
            Value: strings.Join(agent.Config.MessageQueue.Topics, ","),
        })
    }
    
    return env
}

func (r *KubernetesRuntime) buildResourceRequirements(req arctypes.ResourceRequirements) corev1.ResourceRequirements {
    resources := corev1.ResourceRequirements{
        Requests: corev1.ResourceList{},
        Limits:   corev1.ResourceList{},
    }
    
    // Parse CPU
    if req.CPU != "" {
        if cpu, err := resource.ParseQuantity(req.CPU); err == nil {
            resources.Requests[corev1.ResourceCPU] = cpu
            resources.Limits[corev1.ResourceCPU] = cpu
        }
    }
    
    // Parse memory
    if req.Memory != "" {
        if mem, err := resource.ParseQuantity(req.Memory); err == nil {
            resources.Requests[corev1.ResourceMemory] = mem
            resources.Limits[corev1.ResourceMemory] = mem
        }
    }
    
    // Parse GPU (nvidia.com/gpu)
    if req.GPU != "" {
        if gpu, err := resource.ParseQuantity(req.GPU); err == nil {
            resources.Limits["nvidia.com/gpu"] = gpu
        }
    }
    
    return resources
}

func (r *KubernetesRuntime) mergeLabels(agentLabels map[string]string) map[string]string {
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

// KubernetesFactory creates Kubernetes runtime instances
type KubernetesFactory struct{}

// Create creates a new Kubernetes runtime instance
func (f *KubernetesFactory) Create(config Config) (Runtime, error) {
    return NewKubernetesRuntime(config)
}