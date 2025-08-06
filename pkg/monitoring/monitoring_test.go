package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"github.com/rizome-dev/arc/pkg/config"
)

// MockHealthChecker implements HealthChecker for testing
type MockHealthChecker struct {
	name    string
	healthy bool
	err     error
}

func (m *MockHealthChecker) Name() string {
	return m.name
}

func (m *MockHealthChecker) Check(ctx context.Context) error {
	if !m.healthy {
		if m.err != nil {
			return m.err
		}
		return fmt.Errorf("mock health check failed")
	}
	return nil
}

func createTestMonitoringConfig() *config.MonitoringConfig {
	return &config.MonitoringConfig{
		Metrics: config.MetricsConfig{
			Enabled:   true,
			Host:      "127.0.0.1",
			Port:      0, // Use dynamic port for testing
			Path:      "/metrics",
			Namespace: "arc_test",
			Subsystem: "orchestrator",
		},
		Tracing: config.TracingConfig{
			Enabled:      false, // Disable for testing
			ServiceName:  "arc-test",
			SampleRate:   0.1,
			BatchTimeout: 5 * time.Second,
		},
		HealthChecks: config.HealthChecksConfig{
			Enabled:  true,
			Interval: 100 * time.Millisecond, // Fast interval for testing
			Timeout:  1 * time.Second,
		},
		Profiling: config.ProfilingConfig{
			Enabled:    true,
			Host:       "127.0.0.1",
			Port:       0, // Use dynamic port for testing
			CPUProfile: true,
			MemProfile: true,
		},
	}
}

func TestNewMonitor(t *testing.T) {
	config := createTestMonitoringConfig()
	
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	if monitor == nil {
		t.Fatal("Monitor should not be nil")
	}
	
	if monitor.config != config {
		t.Error("Monitor config should match provided config")
	}
	
	if monitor.registry == nil {
		t.Error("Prometheus registry should not be nil")
	}
	
	if monitor.metrics == nil {
		t.Error("Metrics should not be nil")
	}
	
	if monitor.healthChecks == nil {
		t.Error("Health checks map should not be nil")
	}
}

func TestNewMonitorWithNilConfig(t *testing.T) {
	_, err := NewMonitor(nil)
	if err == nil {
		t.Error("Expected error when creating monitor with nil config")
	}
}

func TestMonitorStartStop(t *testing.T) {
	config := createTestMonitoringConfig()
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	// Test initial state
	if monitor.IsRunning() {
		t.Error("Monitor should not be running initially")
	}
	
	// Start monitor
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	
	// Test running state
	if !monitor.IsRunning() {
		t.Error("Monitor should be running after start")
	}
	
	// Give monitor time to start servers
	time.Sleep(100 * time.Millisecond)
	
	// Stop monitor
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := monitor.Stop(stopCtx); err != nil {
		t.Errorf("Failed to stop monitor: %v", err)
	}
	
	// Test stopped state
	if monitor.IsRunning() {
		t.Error("Monitor should not be running after stop")
	}
}

func TestMonitorDoubleStart(t *testing.T) {
	config := createTestMonitoringConfig()
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	ctx := context.Background()
	
	// Start monitor first time
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop(context.Background())
	
	// Try to start again - should return error
	if err := monitor.Start(ctx); err == nil {
		t.Error("Expected error when starting monitor twice")
	}
}

func TestMonitorMetricsCollection(t *testing.T) {
	config := createTestMonitoringConfig()
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop(context.Background())
	
	// Test recording metrics
	monitor.RecordWorkflowCreated()
	monitor.RecordWorkflowCompleted(1500 * time.Millisecond)
	monitor.RecordAgentCreated()
	monitor.RecordAgentCompleted(2 * time.Second)
	monitor.RecordMessageProcessed(100 * time.Millisecond)
	
	// Verify metrics were recorded by gathering them
	metricFamilies, err := monitor.registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}
	
	// Check that we have some metrics
	if len(metricFamilies) == 0 {
		t.Error("Expected some metrics to be registered")
	}
	
	// Look for specific metrics
	metricNames := make(map[string]bool)
	for _, mf := range metricFamilies {
		metricNames[*mf.Name] = true
	}
	
	expectedMetrics := []string{
		"arc_test_orchestrator_workflows_total",
		"arc_test_orchestrator_workflow_duration_seconds",
		"arc_test_orchestrator_agents_total",
		"arc_test_orchestrator_agent_status_changes_total",
		"arc_test_orchestrator_messages_sent_total",
	}
	
	for _, expected := range expectedMetrics {
		if !metricNames[expected] {
			t.Errorf("Expected metric %s not found", expected)
		}
	}
}

func TestMonitorHealthChecks(t *testing.T) {
	config := createTestMonitoringConfig()
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	// Add test health checks
	healthyCheck := &MockHealthChecker{name: "healthy_service", healthy: true}
	unhealthyCheck := &MockHealthChecker{name: "unhealthy_service", healthy: false, err: fmt.Errorf("test error")}
	
	monitor.RegisterHealthCheck(healthyCheck)
	monitor.RegisterHealthCheck(unhealthyCheck)
	
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop(context.Background())
	
	// Wait for at least one health check cycle
	time.Sleep(200 * time.Millisecond)
	
	// Get health status
	status := monitor.GetHealthStatus(ctx)
	
	if status == nil {
		t.Fatal("Health status should not be nil")
	}
	
	if status.Status != "degraded" && status.Status != "unhealthy" {
		t.Errorf("Expected status 'degraded' or 'unhealthy' (due to unhealthy service), got %s", status.Status)
	}
	
	// Check individual check results
	if len(status.Checks) == 0 {
		t.Error("Health checks should not be empty")
	}
	
	healthyResult, exists := status.Checks["healthy_service"]
	if exists && healthyResult.Status != "healthy" {
		t.Errorf("Expected healthy_service status 'healthy', got %s", healthyResult.Status)
	}
	
	unhealthyResult, exists := status.Checks["unhealthy_service"]
	if exists && unhealthyResult.Status != "unhealthy" {
		t.Errorf("Expected unhealthy_service status 'unhealthy', got %s", unhealthyResult.Status)
	}
}

func TestMonitorHealthCheckRegistration(t *testing.T) {
	config := createTestMonitoringConfig()
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	// Test registering health checks
	checker := &MockHealthChecker{name: "test_service", healthy: true}
	
	monitor.RegisterHealthCheck(checker)
	
	// Try to register the same service again - should overwrite
	newChecker := &MockHealthChecker{name: "test_service", healthy: false}
	monitor.RegisterHealthCheck(newChecker)
	
	// Test unregistering
	monitor.UnregisterHealthCheck("test_service")
	
	// Verify it was removed
	ctx := context.Background()
	status := monitor.GetHealthStatus(ctx)
	
	if _, exists := status.Checks["test_service"]; exists {
		t.Error("test_service should have been unregistered")
	}
}

func TestMonitorStartWithDisabledFeatures(t *testing.T) {
	config := &config.MonitoringConfig{
		Metrics: config.MetricsConfig{
			Enabled: false,
		},
		Tracing: config.TracingConfig{
			Enabled: false,
		},
		HealthChecks: config.HealthChecksConfig{
			Enabled: false,
		},
		Profiling: config.ProfilingConfig{
			Enabled: false,
		},
	}
	
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor with disabled features: %v", err)
	}
	defer monitor.Stop(context.Background())
	
	// Monitor should start successfully even with all features disabled
	if !monitor.IsRunning() {
		t.Error("Monitor should be running even with disabled features")
	}
}

func TestMonitorTracingIntegration(t *testing.T) {
	config := createTestMonitoringConfig()
	// Enable tracing for this test (but don't set endpoint to avoid actual connections)
	config.Tracing.Enabled = true
	config.Tracing.ServiceName = "arc-test"
	
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	if monitor.tracer == nil {
		t.Error("Tracer should not be nil when tracing is enabled")
	}
	
	// Test creating a span
	ctx := context.Background()
	ctx, span := monitor.StartSpan(ctx, "test-operation")
	if span == nil {
		t.Error("Span should not be nil")
	}
	
	// Add attributes to span
	span.SetAttributes(
		attribute.String("test.key", "test.value"),
		attribute.Int("test.number", 42),
	)
	
	span.End()
}

func TestMonitorProfilingServer(t *testing.T) {
	config := createTestMonitoringConfig()
	config.Profiling.Enabled = true
	config.Profiling.Host = "127.0.0.1"
	config.Profiling.Port = 0 // Dynamic port
	
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop(context.Background())
	
	// Give server time to start
	time.Sleep(100 * time.Millisecond)
	
	// Profiling server should be running
	if monitor.profilingServer == nil {
		t.Error("Profiling server should not be nil when enabled")
	}
}

func TestMonitorMetricsServer(t *testing.T) {
	config := createTestMonitoringConfig()
	
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop(context.Background())
	
	// Give server time to start
	time.Sleep(100 * time.Millisecond)
	
	// Metrics server should be running
	if monitor.metricsServer == nil {
		t.Error("Metrics server should not be nil when metrics enabled")
	}
}

func TestMonitorConcurrentMetricRecording(t *testing.T) {
	config := createTestMonitoringConfig()
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	defer monitor.Stop(context.Background())
	
	// Record metrics concurrently
	var wg sync.WaitGroup
	numGoroutines := 10
	numOperations := 100
	
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()
			
			for j := 0; j < numOperations; j++ {
				monitor.RecordWorkflowCreated()
				monitor.RecordWorkflowCompleted(time.Duration(j) * time.Millisecond)
				monitor.RecordAgentCreated()
				monitor.RecordAgentCompleted(time.Duration(j) * time.Millisecond)
				monitor.RecordMessageProcessed(time.Duration(j) * time.Millisecond)
			}
		}(i)
	}
	
	wg.Wait()
	
	// Verify metrics were recorded without race conditions
	metricFamilies, err := monitor.registry.Gather()
	if err != nil {
		t.Fatalf("Failed to gather metrics: %v", err)
	}
	
	if len(metricFamilies) == 0 {
		t.Error("Expected some metrics to be registered after concurrent recording")
	}
}

func TestMonitorShutdownTimeout(t *testing.T) {
	config := createTestMonitoringConfig()
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	
	// Give monitor time to fully start
	time.Sleep(100 * time.Millisecond)
	
	// Stop with very short timeout
	stopCtx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	
	// Should still stop gracefully (or at least not hang)
	start := time.Now()
	err = monitor.Stop(stopCtx)
	duration := time.Since(start)
	
	// Should complete quickly even if timeout is hit
	if duration > 2*time.Second {
		t.Errorf("Stop took too long: %v", duration)
	}
	
	if monitor.IsRunning() {
		t.Error("Monitor should not be running after stop")
	}
}

func TestHealthStatus_JSON(t *testing.T) {
	status := &HealthStatus{
		Status: "healthy",
		Checks: map[string]CheckResult{
			"test_service": {
				Status:  "healthy",
				Message: "OK",
				Latency: 10 * time.Millisecond,
			},
		},
		Info: map[string]interface{}{
			"version": "1.0.0",
		},
	}
	
	// Test JSON marshaling
	data, err := json.Marshal(status)
	if err != nil {
		t.Errorf("Failed to marshal health status to JSON: %v", err)
	}
	
	if len(data) == 0 {
		t.Error("JSON data should not be empty")
	}
	
	// Test JSON unmarshaling
	var unmarshaled HealthStatus
	if err := json.Unmarshal(data, &unmarshaled); err != nil {
		t.Errorf("Failed to unmarshal health status from JSON: %v", err)
	}
	
	if unmarshaled.Status != status.Status {
		t.Errorf("Expected status %s, got %s", status.Status, unmarshaled.Status)
	}
}

func TestMonitorGracefulShutdown(t *testing.T) {
	config := createTestMonitoringConfig()
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	// Add health checks to test cleanup
	monitor.RegisterHealthCheck(&MockHealthChecker{name: "test_service", healthy: true})
	
	ctx := context.Background()
	if err := monitor.Start(ctx); err != nil {
		t.Fatalf("Failed to start monitor: %v", err)
	}
	
	// Record some metrics
	monitor.RecordWorkflowCreated()
	monitor.RecordAgentCreated()
	
	// Wait for health check cycle
	time.Sleep(150 * time.Millisecond)
	
	// Graceful shutdown
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	if err := monitor.Stop(stopCtx); err != nil {
		t.Errorf("Graceful shutdown failed: %v", err)
	}
	
	// Verify everything is cleaned up
	if monitor.IsRunning() {
		t.Error("Monitor should not be running after graceful shutdown")
	}
}

func TestMonitorHealthStatusWithInfo(t *testing.T) {
	config := createTestMonitoringConfig()
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	ctx := context.Background()
	status := monitor.GetHealthStatus(ctx)
	
	if status == nil {
		t.Fatal("Health status should not be nil")
	}
	
	// Info field may contain various information
	if status.Info != nil {
		// Just verify it's accessible, specific fields depend on implementation
		t.Logf("Health status info: %+v", status.Info)
	}
}

func TestMonitorStartSpanWithNoTracer(t *testing.T) {
	config := createTestMonitoringConfig()
	config.Tracing.Enabled = false // Disable tracing
	
	monitor, err := NewMonitor(config)
	if err != nil {
		t.Fatalf("Failed to create monitor: %v", err)
	}
	
	ctx := context.Background()
	
	// Should not panic even when tracer is not initialized
	ctx, span := monitor.StartSpan(ctx, "test-operation")
	
	// Span should still be valid (no-op span)
	if span == nil {
		t.Error("Span should not be nil even when tracing is disabled")
	}
	
	span.End() // Should not panic
}