package runtime

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"

	arctypes "github.com/rizome-dev/arc/pkg/types"
	"github.com/rizome-dev/arc/pkg/validation"
)

// MockImageValidator for testing
type MockImageValidator struct {
	validateErr error
	scanResult  *validation.ScanResult
	scanErr     error
}

func (m *MockImageValidator) ValidateImage(ctx context.Context, image string) error {
	return m.validateErr
}

func (m *MockImageValidator) ScanImage(ctx context.Context, image string) (*validation.ScanResult, error) {
	if m.scanErr != nil {
		return nil, m.scanErr
	}
	return m.scanResult, nil
}

func (m *MockImageValidator) CheckPolicy(ctx context.Context, image string, policy validation.Policy) error {
	return nil
}

func createTestKubernetesRuntime(t *testing.T) *KubernetesRuntime {
	fakeClient := fake.NewSimpleClientset()
	
	config := Config{
		Namespace: "test-namespace",
		Labels: map[string]string{
			"app": "arc-test",
		},
	}
	
	return &KubernetesRuntime{
		client:    fakeClient,
		config:    config,
		namespace: "test-namespace",
	}
}

func createTestKubernetesRuntimeWithValidation(t *testing.T) *KubernetesRuntime {
	runtime := createTestKubernetesRuntime(t)
	runtime.config.EnableImageValidation = true
	runtime.imageValidator = &MockImageValidator{}
	return runtime
}

func TestKubernetesRuntime_NewKubernetesRuntime(t *testing.T) {
	tests := []struct {
		name      string
		config    Config
		wantError bool
	}{
		{
			name: "valid config with kubeconfig",
			config: Config{
				KubeConfig: "/tmp/fake-kubeconfig",
				Namespace:  "test",
			},
			wantError: true, // Will fail because kubeconfig doesn't exist
		},
		{
			name:      "empty config",
			config:    Config{},
			wantError: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewKubernetesRuntime(tt.config)
			
			if (err != nil) != tt.wantError {
				t.Errorf("NewKubernetesRuntime() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestKubernetesRuntime_CreateAgent(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	ctx := context.Background()
	
	agent := &arctypes.Agent{
		ID:    "test-agent-1",
		Name:  "Test Agent 1",
		Image: "nginx:latest",
		Labels: map[string]string{
			"component": "test",
		},
		Config: arctypes.AgentConfig{
			Command: []string{"nginx"},
			Args:    []string{"-g", "daemon off;"},
			Environment: map[string]string{
				"ENV": "test",
			},
			Resources: arctypes.ResourceRequirements{
				CPU:    "500m",
				Memory: "256Mi",
			},
		},
	}
	
	err := runtime.CreateAgent(ctx, agent)
	if err != nil {
		t.Errorf("CreateAgent() error = %v", err)
	}
	
	// Verify pod was created
	podName := fmt.Sprintf("arc-agent-%s", agent.ID)
	pod, err := runtime.client.CoreV1().Pods(runtime.namespace).Get(ctx, podName, metav1.GetOptions{})
	if err != nil {
		t.Errorf("Pod should be created: %v", err)
	}
	
	if pod.Name != podName {
		t.Errorf("Pod name should be %s, got %s", podName, pod.Name)
	}
	
	// Verify pod spec
	if len(pod.Spec.Containers) != 1 {
		t.Errorf("Expected 1 container, got %d", len(pod.Spec.Containers))
	}
	
	container := pod.Spec.Containers[0]
	if container.Image != agent.Image {
		t.Errorf("Container image should be %s, got %s", agent.Image, container.Image)
	}
	
	if len(container.Command) != 1 || container.Command[0] != "nginx" {
		t.Errorf("Container command not set correctly")
	}
	
	// Verify labels
	if pod.Labels["arc.managed"] != "true" {
		t.Error("Pod should have arc.managed=true label")
	}
	
	// Verify agent was updated (ContainerID is set to pod UID in real implementation)
	if agent.Status != arctypes.AgentStatusCreating {
		t.Errorf("Agent status should be creating, got %s", agent.Status)
	}
	
	if agent.Namespace != runtime.namespace {
		t.Errorf("Agent namespace should be %s, got %s", runtime.namespace, agent.Namespace)
	}
}

func TestKubernetesRuntime_CreateAgentWithImageValidation(t *testing.T) {
	runtime := createTestKubernetesRuntimeWithValidation(t)
	ctx := context.Background()
	
	agent := &arctypes.Agent{
		ID:    "test-agent-validation",
		Name:  "Test Agent Validation",
		Image: "nginx:latest",
	}
	
	// Test successful validation
	err := runtime.CreateAgent(ctx, agent)
	if err != nil {
		t.Errorf("CreateAgent() with validation should succeed: %v", err)
	}
	
	// Test validation failure
	validator := runtime.imageValidator.(*MockImageValidator)
	validator.validateErr = fmt.Errorf("image validation failed")
	
	agent2 := &arctypes.Agent{
		ID:    "test-agent-validation-fail",
		Name:  "Test Agent Validation Fail",
		Image: "malicious:latest",
	}
	
	err = runtime.CreateAgent(ctx, agent2)
	if err == nil {
		t.Error("CreateAgent() should fail when image validation fails")
	}
	
	if !strings.Contains(err.Error(), "image validation failed") {
		t.Errorf("Error should mention image validation failure: %v", err)
	}
}

func TestKubernetesRuntime_CreateAgentWithSecurityScan(t *testing.T) {
	runtime := createTestKubernetesRuntimeWithValidation(t)
	runtime.config.EnableSecurityScan = true
	runtime.config.MaxHighCVEs = 5
	ctx := context.Background()
	
	agent := &arctypes.Agent{
		ID:    "test-agent-scan",
		Name:  "Test Agent Scan",
		Image: "nginx:latest",
	}
	
	validator := runtime.imageValidator.(*MockImageValidator)
	
	// Test with acceptable vulnerabilities
	validator.scanResult = &validation.ScanResult{
		CriticalCount: 0,
		HighCount:     3,
		MediumCount:   10,
		LowCount:      50,
	}
	
	err := runtime.CreateAgent(ctx, agent)
	if err != nil {
		t.Errorf("CreateAgent() with acceptable vulnerabilities should succeed: %v", err)
	}
	
	// Test with too many high vulnerabilities
	validator.scanResult = &validation.ScanResult{
		CriticalCount: 0,
		HighCount:     10, // Exceeds MaxHighCVEs (5)
		MediumCount:   5,
		LowCount:      20,
	}
	
	agent2 := &arctypes.Agent{
		ID:    "test-agent-scan-fail",
		Name:  "Test Agent Scan Fail",
		Image: "vulnerable:latest",
	}
	
	err = runtime.CreateAgent(ctx, agent2)
	if err == nil {
		t.Error("CreateAgent() should fail when image has too many high CVEs")
	}
	
	// Test with critical vulnerabilities
	validator.scanResult = &validation.ScanResult{
		CriticalCount: 1,
		HighCount:     0,
		MediumCount:   0,
		LowCount:      0,
	}
	
	agent3 := &arctypes.Agent{
		ID:    "test-agent-critical",
		Name:  "Test Agent Critical",
		Image: "critical:latest",
	}
	
	err = runtime.CreateAgent(ctx, agent3)
	if err == nil {
		t.Error("CreateAgent() should fail when image has critical vulnerabilities")
	}
}

func TestKubernetesRuntime_StartAgent(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	ctx := context.Background()
	
	// Create a pod first
	agentID := "test-agent-start"
	podName := fmt.Sprintf("arc-agent-%s", agentID)
	
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: runtime.namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "agent",
					Image: "nginx:latest",
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
		},
	}
	
	_, err := runtime.client.CoreV1().Pods(runtime.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test pod: %v", err)
	}
	
	// Test starting agent
	err = runtime.StartAgent(ctx, agentID)
	if err != nil {
		t.Errorf("StartAgent() error = %v", err)
	}
	
	// Test starting non-existent agent
	err = runtime.StartAgent(ctx, "non-existent")
	if err == nil {
		t.Error("StartAgent() should fail for non-existent agent")
	}
}

func TestKubernetesRuntime_StopAgent(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	ctx := context.Background()
	
	// Create a pod first
	agentID := "test-agent-stop"
	podName := fmt.Sprintf("arc-agent-%s", agentID)
	
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: runtime.namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "agent",
					Image: "nginx:latest",
				},
			},
		},
	}
	
	_, err := runtime.client.CoreV1().Pods(runtime.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test pod: %v", err)
	}
	
	// Test stopping agent
	err = runtime.StopAgent(ctx, agentID)
	if err != nil {
		t.Errorf("StopAgent() error = %v", err)
	}
	
	// Test stopping non-existent agent
	err = runtime.StopAgent(ctx, "non-existent")
	if err == nil {
		t.Error("StopAgent() should fail for non-existent agent")
	}
}

func TestKubernetesRuntime_DestroyAgent(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	ctx := context.Background()
	
	// Create a pod first
	agentID := "test-agent-destroy"
	podName := fmt.Sprintf("arc-agent-%s", agentID)
	
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: runtime.namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "agent",
					Image: "nginx:latest",
				},
			},
		},
	}
	
	_, err := runtime.client.CoreV1().Pods(runtime.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test pod: %v", err)
	}
	
	// Test destroying agent
	err = runtime.DestroyAgent(ctx, agentID)
	if err != nil {
		t.Errorf("DestroyAgent() error = %v", err)
	}
	
	// Test destroying non-existent agent
	err = runtime.DestroyAgent(ctx, "non-existent")
	if err == nil {
		t.Error("DestroyAgent() should fail for non-existent agent")
	}
}

func TestKubernetesRuntime_GetAgentStatus(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	ctx := context.Background()
	
	agentID := "test-agent-status"
	podName := fmt.Sprintf("arc-agent-%s", agentID)
	
	// Test different pod phases
	testCases := []struct {
		name          string
		podPhase      corev1.PodPhase
		expectedStatus arctypes.AgentStatus
	}{
		{
			name:          "pending",
			podPhase:      corev1.PodPending,
			expectedStatus: arctypes.AgentStatusCreating,
		},
		{
			name:          "running",
			podPhase:      corev1.PodRunning,
			expectedStatus: arctypes.AgentStatusRunning,
		},
		{
			name:          "succeeded",
			podPhase:      corev1.PodSucceeded,
			expectedStatus: arctypes.AgentStatusCompleted,
		},
		{
			name:          "failed",
			podPhase:      corev1.PodFailed,
			expectedStatus: arctypes.AgentStatusFailed,
		},
	}
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create pod with specific phase
			pod := &corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{
					Name:      podName,
					Namespace: runtime.namespace,
					Labels: map[string]string{
						"arc.agent.id": agentID,
					},
				},
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{
						{
							Name:  "agent",
							Image: "nginx:latest",
						},
					},
				},
				Status: corev1.PodStatus{
					Phase: tc.podPhase,
				},
			}
			
			// Add start time for running/completed/failed states
			if tc.podPhase != corev1.PodPending {
				now := metav1.Now()
				pod.Status.StartTime = &now
			}
			
			// Add container status for completed/failed states
			if tc.podPhase == corev1.PodSucceeded || tc.podPhase == corev1.PodFailed {
				pod.Status.ContainerStatuses = []corev1.ContainerStatus{
					{
						Name: "agent",
						State: corev1.ContainerState{
							Terminated: &corev1.ContainerStateTerminated{
								FinishedAt: metav1.Now(),
								Reason:     "Completed",
								Message:    "Task completed",
							},
						},
					},
				}
			}
			
			// Delete existing pod if it exists
			runtime.client.CoreV1().Pods(runtime.namespace).Delete(ctx, podName, metav1.DeleteOptions{})
			
			// Create new pod
			_, err := runtime.client.CoreV1().Pods(runtime.namespace).Create(ctx, pod, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("Failed to create test pod: %v", err)
			}
			
			// Get agent status
			agent, err := runtime.GetAgentStatus(ctx, agentID)
			if err != nil {
				t.Errorf("GetAgentStatus() error = %v", err)
				return
			}
			
			if agent.Status != tc.expectedStatus {
				t.Errorf("Expected status %s, got %s", tc.expectedStatus, agent.Status)
			}
			
			if agent.ID != agentID {
				t.Errorf("Expected agent ID %s, got %s", agentID, agent.ID)
			}
		})
	}
	
	// Test non-existent agent
	_, err := runtime.GetAgentStatus(ctx, "non-existent")
	if err == nil {
		t.Error("GetAgentStatus() should fail for non-existent agent")
	}
}

func TestKubernetesRuntime_ListAgents(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	ctx := context.Background()
	
	// Create multiple pods
	agents := []string{"agent-1", "agent-2", "agent-3"}
	
	for _, agentID := range agents {
		podName := fmt.Sprintf("arc-agent-%s", agentID)
		pod := &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      podName,
				Namespace: runtime.namespace,
				Labels: map[string]string{
					"arc.managed":  "true",
					"arc.agent.id": agentID,
				},
			},
			Spec: corev1.PodSpec{
				Containers: []corev1.Container{
					{
						Name:  "agent",
						Image: "nginx:latest",
					},
				},
			},
			Status: corev1.PodStatus{
				Phase: corev1.PodRunning,
			},
		}
		
		_, err := runtime.client.CoreV1().Pods(runtime.namespace).Create(ctx, pod, metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("Failed to create test pod %s: %v", podName, err)
		}
	}
	
	// List agents
	retrievedAgents, err := runtime.ListAgents(ctx)
	if err != nil {
		t.Errorf("ListAgents() error = %v", err)
	}
	
	if len(retrievedAgents) != len(agents) {
		t.Errorf("Expected %d agents, got %d", len(agents), len(retrievedAgents))
	}
	
	// Verify agent IDs
	retrievedIDs := make(map[string]bool)
	for _, agent := range retrievedAgents {
		retrievedIDs[agent.ID] = true
	}
	
	for _, expectedID := range agents {
		if !retrievedIDs[expectedID] {
			t.Errorf("Agent %s not found in list", expectedID)
		}
	}
}

func TestKubernetesRuntime_StreamLogs(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	ctx := context.Background()
	
	agentID := "test-agent-logs"
	podName := fmt.Sprintf("arc-agent-%s", agentID)
	
	// Create a pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: runtime.namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "agent",
					Image: "nginx:latest",
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	
	_, err := runtime.client.CoreV1().Pods(runtime.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test pod: %v", err)
	}
	
	// Test streaming logs (will fail in fake client, but we test the interface)
	_, err = runtime.StreamLogs(ctx, agentID)
	if err == nil {
		t.Error("StreamLogs() should fail with fake client (expected for testing)")
	}
	
	// Test streaming logs for non-existent agent
	_, err = runtime.StreamLogs(ctx, "non-existent")
	if err == nil {
		t.Error("StreamLogs() should fail for non-existent agent")
	}
}

func TestKubernetesRuntime_ExecCommand(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	ctx := context.Background()
	
	agentID := "test-agent-exec"
	podName := fmt.Sprintf("arc-agent-%s", agentID)
	
	// Create a pod
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      podName,
			Namespace: runtime.namespace,
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name:  "agent",
					Image: "nginx:latest",
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
		},
	}
	
	_, err := runtime.client.CoreV1().Pods(runtime.namespace).Create(ctx, pod, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test pod: %v", err)
	}
	
	// Test exec command
	cmd := []string{"echo", "hello world"}
	
	// This will fail with the fake client, but we test the interface
	_, err = runtime.ExecCommand(ctx, agentID, cmd)
	if err == nil {
		t.Error("ExecCommand() should fail with fake client (expected for testing)")
	}
	
	// The error should be related to REST config or SPDY executor creation
	if !strings.Contains(err.Error(), "REST config") && !strings.Contains(err.Error(), "executor") {
		t.Errorf("ExecCommand() error should be related to REST config or executor: %v", err)
	}
}

func TestKubernetesRuntime_BuildPodSpec(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	
	agent := &arctypes.Agent{
		ID:    "test-agent-spec",
		Name:  "Test Agent Spec",
		Image: "nginx:1.21",
		Config: arctypes.AgentConfig{
			Command:    []string{"/bin/sh"},
			Args:       []string{"-c", "echo hello"},
			WorkingDir: "/app",
			Environment: map[string]string{
				"ENV1": "value1",
				"ENV2": "value2",
			},
			Resources: arctypes.ResourceRequirements{
				CPU:    "500m",
				Memory: "512Mi",
				GPU:    "1",
			},
			MessageQueue: arctypes.MessageQueueConfig{
				Brokers: []string{"broker1", "broker2"},
				Topics:  []string{"topic1", "topic2"},
			},
			Volumes: []arctypes.VolumeMount{
				{
					Name:      "data",
					MountPath: "/data",
					ReadOnly:  false,
				},
				{
					Name:      "config",
					MountPath: "/config",
					ReadOnly:  true,
				},
			},
		},
	}
	
	podSpec := runtime.buildPodSpec(agent)
	
	// Verify basic pod spec
	if podSpec.RestartPolicy != corev1.RestartPolicyNever {
		t.Error("Pod should have RestartPolicyNever")
	}
	
	if len(podSpec.Containers) != 1 {
		t.Errorf("Expected 1 container, got %d", len(podSpec.Containers))
	}
	
	container := podSpec.Containers[0]
	
	// Verify container properties
	if container.Name != "agent" {
		t.Errorf("Container name should be 'agent', got %s", container.Name)
	}
	
	if container.Image != agent.Image {
		t.Errorf("Container image should be %s, got %s", agent.Image, container.Image)
	}
	
	if len(container.Command) != 1 || container.Command[0] != "/bin/sh" {
		t.Error("Container command not set correctly")
	}
	
	if len(container.Args) != 2 || container.Args[0] != "-c" || container.Args[1] != "echo hello" {
		t.Error("Container args not set correctly")
	}
	
	if container.WorkingDir != "/app" {
		t.Errorf("Container working dir should be /app, got %s", container.WorkingDir)
	}
	
	// Verify environment variables
	envMap := make(map[string]string)
	for _, env := range container.Env {
		envMap[env.Name] = env.Value
	}
	
	if envMap["ENV1"] != "value1" || envMap["ENV2"] != "value2" {
		t.Error("Environment variables not set correctly")
	}
	
	if envMap["AMQ_BROKERS"] != "broker1,broker2" {
		t.Error("AMQ_BROKERS environment variable not set correctly")
	}
	
	if envMap["AMQ_TOPICS"] != "topic1,topic2" {
		t.Error("AMQ_TOPICS environment variable not set correctly")
	}
	
	// Verify resources
	cpuQuantity := container.Resources.Requests.Cpu()
	if cpuQuantity.String() != "500m" {
		t.Error("CPU request not set correctly")
	}
	
	memQuantity := container.Resources.Limits.Memory()
	if memQuantity.String() != "512Mi" {
		t.Error("Memory limit not set correctly")
	}
	
	gpuQuantity := container.Resources.Limits["nvidia.com/gpu"]
	if gpuQuantity.String() != "1" {
		t.Error("GPU limit not set correctly")
	}
	
	// Verify volume mounts
	if len(container.VolumeMounts) != 2 {
		t.Errorf("Expected 2 volume mounts, got %d", len(container.VolumeMounts))
	}
	
	// Verify volumes
	if len(podSpec.Volumes) != 2 {
		t.Errorf("Expected 2 volumes, got %d", len(podSpec.Volumes))
	}
}

func TestKubernetesRuntime_MergeLabels(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	
	agentLabels := map[string]string{
		"component":    "worker",
		"environment":  "test",
		"arc.agent.id": "test-agent",
	}
	
	mergedLabels := runtime.mergeLabels(agentLabels)
	
	// Verify default labels
	if mergedLabels["app"] != "arc-test" {
		t.Error("Default app label should be preserved")
	}
	
	if mergedLabels["arc.managed"] != "true" {
		t.Error("arc.managed label should be set to true")
	}
	
	// Verify agent labels
	if mergedLabels["component"] != "worker" {
		t.Error("Agent component label should be preserved")
	}
	
	if mergedLabels["arc.agent.id"] != "test-agent" {
		t.Error("arc.agent.id label should be preserved")
	}
}

func TestKubernetesRuntime_ResourceRequirements(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	
	tests := []struct {
		name         string
		requirements arctypes.ResourceRequirements
		expectCPU    bool
		expectMemory bool
		expectGPU    bool
	}{
		{
			name: "all resources",
			requirements: arctypes.ResourceRequirements{
				CPU:    "1000m",
				Memory: "1Gi",
				GPU:    "2",
			},
			expectCPU:    true,
			expectMemory: true,
			expectGPU:    true,
		},
		{
			name: "only CPU",
			requirements: arctypes.ResourceRequirements{
				CPU: "500m",
			},
			expectCPU:    true,
			expectMemory: false,
			expectGPU:    false,
		},
		{
			name:         "no resources",
			requirements: arctypes.ResourceRequirements{},
			expectCPU:    false,
			expectMemory: false,
			expectGPU:    false,
		},
		{
			name: "invalid CPU format",
			requirements: arctypes.ResourceRequirements{
				CPU: "invalid",
			},
			expectCPU:    false,
			expectMemory: false,
			expectGPU:    false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resources := runtime.buildResourceRequirements(tt.requirements)
			
			cpuQuantity := resources.Requests.Cpu()
			hasCPU := !cpuQuantity.IsZero()
			if hasCPU != tt.expectCPU {
				t.Errorf("Expected CPU resource: %v, got: %v", tt.expectCPU, hasCPU)
			}
			
			memQuantity := resources.Requests.Memory()
			hasMemory := !memQuantity.IsZero()
			if hasMemory != tt.expectMemory {
				t.Errorf("Expected Memory resource: %v, got: %v", tt.expectMemory, hasMemory)
			}
			
			gpuQuantity := resources.Limits["nvidia.com/gpu"]
			hasGPU := !gpuQuantity.IsZero()
			if hasGPU != tt.expectGPU {
				t.Errorf("Expected GPU resource: %v, got: %v", tt.expectGPU, hasGPU)
			}
		})
	}
}

func TestKubernetesRuntime_GetRestConfig(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	
	// Test getting REST config (will fail because no real config is set up)
	_, err := runtime.getRestConfig()
	if err == nil {
		t.Error("getRestConfig() should fail in test environment")
	}
	
	// Test with kubeconfig path set
	runtime.config.KubeConfig = "/tmp/fake-kubeconfig"
	_, err = runtime.getRestConfig()
	if err == nil {
		t.Error("getRestConfig() should fail with non-existent kubeconfig")
	}
}

func TestKubernetesFactory(t *testing.T) {
	factory := &KubernetesFactory{}
	
	config := Config{
		Namespace:  "test-factory",
		KubeConfig: "/tmp/fake-kubeconfig",
	}
	
	// This will fail because the kubeconfig doesn't exist, but we test the interface
	_, err := factory.Create(config)
	if err == nil {
		t.Error("Factory.Create() should fail with non-existent kubeconfig")
	}
}

func TestKubernetesRuntime_ConcurrentOperations(t *testing.T) {
	runtime := createTestKubernetesRuntime(t)
	ctx := context.Background()
	
	// Test concurrent agent creation
	numAgents := 10
	agents := make([]*arctypes.Agent, numAgents)
	
	for i := 0; i < numAgents; i++ {
		agents[i] = &arctypes.Agent{
			ID:    fmt.Sprintf("concurrent-agent-%d", i),
			Name:  fmt.Sprintf("Concurrent Agent %d", i),
			Image: "nginx:latest",
		}
	}
	
	// Create agents concurrently
	errChan := make(chan error, numAgents)
	for i := 0; i < numAgents; i++ {
		go func(agent *arctypes.Agent) {
			err := runtime.CreateAgent(ctx, agent)
			errChan <- err
		}(agents[i])
	}
	
	// Check for errors
	for i := 0; i < numAgents; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent CreateAgent() error: %v", err)
		}
	}
	
	// Verify all agents were created
	retrievedAgents, err := runtime.ListAgents(ctx)
	if err != nil {
		t.Errorf("ListAgents() error: %v", err)
	}
	
	if len(retrievedAgents) != numAgents {
		t.Errorf("Expected %d agents, got %d", numAgents, len(retrievedAgents))
	}
}