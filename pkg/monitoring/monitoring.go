// Package monitoring provides comprehensive monitoring and observability for ARC
package monitoring

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/http/pprof"
	"runtime"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.4.0"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/rizome-dev/arc/pkg/config"
)

// Monitor manages all monitoring and observability features
type Monitor struct {
	config   *config.MonitoringConfig
	registry *prometheus.Registry
	tracer   oteltrace.Tracer

	// Prometheus metrics
	metrics        *Metrics
	metricsServer  *http.Server
	healthServer   *http.Server
	profilingServer *http.Server

	// Health checks
	healthChecks map[string]HealthChecker
	healthMu     sync.RWMutex

	// Server state
	running bool
	mu      sync.RWMutex
}

// Metrics holds all Prometheus metrics
type Metrics struct {
	// Agent metrics
	AgentsTotal       prometheus.Gauge
	AgentsActive      prometheus.Gauge
	AgentCreations    prometheus.Counter
	AgentFailures     prometheus.Counter
	AgentDuration     prometheus.Histogram

	// Workflow metrics
	WorkflowsTotal     prometheus.Gauge
	WorkflowsActive    prometheus.Gauge
	WorkflowCreations  prometheus.Counter
	WorkflowFailures   prometheus.Counter
	WorkflowDuration   prometheus.Histogram

	// Task metrics
	TasksTotal      prometheus.Gauge
	TasksActive     prometheus.Gauge
	TaskCreations   prometheus.Counter
	TaskFailures    prometheus.Counter
	TaskDuration    prometheus.Histogram

	// Message queue metrics
	MessagesTotal     prometheus.Counter
	MessagesProcessed prometheus.Counter
	MessagesFailed    prometheus.Counter
	MessageQueueDepth prometheus.Gauge
	MessageLatency    prometheus.Histogram

	// Runtime metrics
	RuntimeOperations prometheus.Counter
	RuntimeErrors     prometheus.Counter
	RuntimeLatency    prometheus.Histogram

	// System metrics
	GoInfo          prometheus.Gauge
	ProcessMemory   prometheus.Gauge
	ProcessCPU      prometheus.Gauge
	GoroutineCount  prometheus.Gauge
	GCDuration      prometheus.Histogram

	// HTTP metrics
	HTTPRequestsTotal    prometheus.Counter
	HTTPRequestDuration  prometheus.Histogram
	HTTPRequestsInFlight prometheus.Gauge

	// gRPC metrics
	GRPCRequestsTotal    prometheus.Counter
	GRPCRequestDuration  prometheus.Histogram
	GRPCRequestsInFlight prometheus.Gauge
}

// HealthChecker interface for health checks
type HealthChecker interface {
	Name() string
	Check(ctx context.Context) error
}

// HealthStatus represents the health status
type HealthStatus struct {
	Status string                    `json:"status"`
	Checks map[string]CheckResult    `json:"checks"`
	Info   map[string]interface{}    `json:"info"`
}

// CheckResult represents a single health check result
type CheckResult struct {
	Status  string        `json:"status"`
	Message string        `json:"message,omitempty"`
	Error   string        `json:"error,omitempty"`
	Latency time.Duration `json:"latency"`
}

// NewMonitor creates a new monitoring instance
func NewMonitor(config *config.MonitoringConfig) (*Monitor, error) {
	if config == nil {
		return nil, fmt.Errorf("monitoring config is required")
	}

	monitor := &Monitor{
		config:       config,
		registry:     prometheus.NewRegistry(),
		healthChecks: make(map[string]HealthChecker),
	}

	// Initialize metrics
	if err := monitor.initMetrics(); err != nil {
		return nil, fmt.Errorf("failed to initialize metrics: %w", err)
	}

	// Initialize tracing if enabled
	if config.Tracing.Enabled {
		if err := monitor.initTracing(); err != nil {
			return nil, fmt.Errorf("failed to initialize tracing: %w", err)
		}
	}

	return monitor, nil
}

// Start starts all monitoring services
func (m *Monitor) Start(ctx context.Context) error {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return fmt.Errorf("monitor is already running")
	}
	m.running = true
	m.mu.Unlock()

	log.Printf("Starting monitoring services...")

	// Start metrics server
	if m.config.Metrics.Enabled {
		if err := m.startMetricsServer(); err != nil {
			return fmt.Errorf("failed to start metrics server: %w", err)
		}
	}

	// Start health check server
	if m.config.HealthChecks.Enabled {
		if err := m.startHealthServer(); err != nil {
			return fmt.Errorf("failed to start health server: %w", err)
		}
	}

	// Start profiling server
	if m.config.Profiling.Enabled {
		if err := m.startProfilingServer(); err != nil {
			return fmt.Errorf("failed to start profiling server: %w", err)
		}
	}

	// Start system metrics collection
	go m.collectSystemMetrics(ctx)

	// Start health checks
	if m.config.HealthChecks.Enabled {
		go m.runHealthChecks(ctx)
	}

	log.Printf("Monitoring services started successfully")
	return nil
}

// Stop stops all monitoring services
// IsRunning returns whether the monitor is currently running
func (m *Monitor) IsRunning() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.running
}

func (m *Monitor) Stop(ctx context.Context) error {
	m.mu.Lock()
	if !m.running {
		m.mu.Unlock()
		return nil
	}
	m.running = false
	m.mu.Unlock()

	log.Printf("Stopping monitoring services...")

	var wg sync.WaitGroup
	errChan := make(chan error, 3)

	// Stop metrics server
	if m.metricsServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.metricsServer.Shutdown(ctx); err != nil {
				errChan <- fmt.Errorf("metrics server shutdown error: %w", err)
			}
		}()
	}

	// Stop health server
	if m.healthServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.healthServer.Shutdown(ctx); err != nil {
				errChan <- fmt.Errorf("health server shutdown error: %w", err)
			}
		}()
	}

	// Stop profiling server
	if m.profilingServer != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := m.profilingServer.Shutdown(ctx); err != nil {
				errChan <- fmt.Errorf("profiling server shutdown error: %w", err)
			}
		}()
	}

	wg.Wait()
	close(errChan)

	for err := range errChan {
		log.Printf("Monitor shutdown error: %v", err)
	}

	log.Printf("Monitoring services stopped")
	return nil
}

// GetMetrics returns the metrics instance
func (m *Monitor) GetMetrics() *Metrics {
	return m.metrics
}

// GetTracer returns the OpenTelemetry tracer
func (m *Monitor) GetTracer() oteltrace.Tracer {
	return m.tracer
}

// RegisterHealthCheck registers a new health checker
func (m *Monitor) RegisterHealthCheck(checker HealthChecker) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	m.healthChecks[checker.Name()] = checker
}

// UnregisterHealthCheck removes a health checker
func (m *Monitor) UnregisterHealthCheck(name string) {
	m.healthMu.Lock()
	defer m.healthMu.Unlock()
	delete(m.healthChecks, name)
}

// GetHealthStatus returns the current health status
func (m *Monitor) GetHealthStatus(ctx context.Context) *HealthStatus {
	m.healthMu.RLock()
	defer m.healthMu.RUnlock()

	status := &HealthStatus{
		Status: "healthy",
		Checks: make(map[string]CheckResult),
		Info: map[string]interface{}{
			"timestamp": time.Now().UTC(),
			"uptime":    time.Since(time.Now()), // This would be calculated from start time
			"version":   "dev", // This would come from version package
		},
	}

	for name, checker := range m.healthChecks {
		start := time.Now()
		err := checker.Check(ctx)
		latency := time.Since(start)

		if err != nil {
			status.Status = "unhealthy"
			status.Checks[name] = CheckResult{
				Status:  "unhealthy",
				Error:   err.Error(),
				Latency: latency,
			}
		} else {
			status.Checks[name] = CheckResult{
				Status:  "healthy",
				Latency: latency,
			}
		}
	}

	return status
}

// initMetrics initializes Prometheus metrics
func (m *Monitor) initMetrics() error {
	m.metrics = &Metrics{
		// Agent metrics
		AgentsTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "arc_agents_total",
			Help: "Total number of agents",
		}),
		AgentsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "arc_agents_active",
			Help: "Number of active agents",
		}),
		AgentCreations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_agent_creations_total",
			Help: "Total number of agent creations",
		}),
		AgentFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_agent_failures_total",
			Help: "Total number of agent failures",
		}),
		AgentDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "arc_agent_duration_seconds",
			Help:    "Agent execution duration",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
		}),

		// Workflow metrics
		WorkflowsTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "arc_workflows_total",
			Help: "Total number of workflows",
		}),
		WorkflowsActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "arc_workflows_active",
			Help: "Number of active workflows",
		}),
		WorkflowCreations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_workflow_creations_total",
			Help: "Total number of workflow creations",
		}),
		WorkflowFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_workflow_failures_total",
			Help: "Total number of workflow failures",
		}),
		WorkflowDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "arc_workflow_duration_seconds",
			Help:    "Workflow execution duration",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10),
		}),

		// Task metrics
		TasksTotal: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "arc_tasks_total",
			Help: "Total number of tasks",
		}),
		TasksActive: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "arc_tasks_active",
			Help: "Number of active tasks",
		}),
		TaskCreations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_task_creations_total",
			Help: "Total number of task creations",
		}),
		TaskFailures: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_task_failures_total",
			Help: "Total number of task failures",
		}),
		TaskDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "arc_task_duration_seconds",
			Help:    "Task execution duration",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
		}),

		// Message queue metrics
		MessagesTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_messages_total",
			Help: "Total number of messages",
		}),
		MessagesProcessed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_messages_processed_total",
			Help: "Total number of processed messages",
		}),
		MessagesFailed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_messages_failed_total",
			Help: "Total number of failed messages",
		}),
		MessageQueueDepth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "arc_message_queue_depth",
			Help: "Current message queue depth",
		}),
		MessageLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "arc_message_latency_seconds",
			Help:    "Message processing latency",
			Buckets: prometheus.ExponentialBuckets(0.001, 2, 10),
		}),

		// Runtime metrics
		RuntimeOperations: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_runtime_operations_total",
			Help: "Total number of runtime operations",
		}),
		RuntimeErrors: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "arc_runtime_errors_total",
			Help: "Total number of runtime errors",
		}),
		RuntimeLatency: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "arc_runtime_latency_seconds",
			Help:    "Runtime operation latency",
			Buckets: prometheus.ExponentialBuckets(0.1, 2, 10),
		}),

		// System metrics
		GoInfo: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "go_info",
			Help: "Information about the Go environment",
		}),
		ProcessMemory: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "process_memory_bytes",
			Help: "Process memory usage in bytes",
		}),
		ProcessCPU: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "process_cpu_seconds_total",
			Help: "Process CPU usage in seconds",
		}),
		GoroutineCount: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "go_goroutines",
			Help: "Number of goroutines",
		}),
		GCDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "go_gc_duration_seconds",
			Help:    "Garbage collection duration",
			Buckets: prometheus.ExponentialBuckets(0.0001, 2, 10),
		}),

		// HTTP metrics
		HTTPRequestsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total number of HTTP requests",
		}),
		HTTPRequestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request duration",
			Buckets: prometheus.DefBuckets,
		}),
		HTTPRequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "http_requests_in_flight",
			Help: "Number of HTTP requests currently in flight",
		}),

		// gRPC metrics
		GRPCRequestsTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "grpc_requests_total",
			Help: "Total number of gRPC requests",
		}),
		GRPCRequestDuration: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "grpc_request_duration_seconds",
			Help:    "gRPC request duration",
			Buckets: prometheus.DefBuckets,
		}),
		GRPCRequestsInFlight: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "grpc_requests_in_flight",
			Help: "Number of gRPC requests currently in flight",
		}),
	}

	// Register all metrics
	collectors := []prometheus.Collector{
		m.metrics.AgentsTotal,
		m.metrics.AgentsActive,
		m.metrics.AgentCreations,
		m.metrics.AgentFailures,
		m.metrics.AgentDuration,
		m.metrics.WorkflowsTotal,
		m.metrics.WorkflowsActive,
		m.metrics.WorkflowCreations,
		m.metrics.WorkflowFailures,
		m.metrics.WorkflowDuration,
		m.metrics.TasksTotal,
		m.metrics.TasksActive,
		m.metrics.TaskCreations,
		m.metrics.TaskFailures,
		m.metrics.TaskDuration,
		m.metrics.MessagesTotal,
		m.metrics.MessagesProcessed,
		m.metrics.MessagesFailed,
		m.metrics.MessageQueueDepth,
		m.metrics.MessageLatency,
		m.metrics.RuntimeOperations,
		m.metrics.RuntimeErrors,
		m.metrics.RuntimeLatency,
		m.metrics.GoInfo,
		m.metrics.ProcessMemory,
		m.metrics.ProcessCPU,
		m.metrics.GoroutineCount,
		m.metrics.GCDuration,
		m.metrics.HTTPRequestsTotal,
		m.metrics.HTTPRequestDuration,
		m.metrics.HTTPRequestsInFlight,
		m.metrics.GRPCRequestsTotal,
		m.metrics.GRPCRequestDuration,
		m.metrics.GRPCRequestsInFlight,
	}

	for _, collector := range collectors {
		if err := m.registry.Register(collector); err != nil {
			return fmt.Errorf("failed to register metric: %w", err)
		}
	}

	return nil
}

// initTracing initializes OpenTelemetry tracing
func (m *Monitor) initTracing() error {
	ctx := context.Background()

	// Create OTLP HTTP exporter
	exporter, err := otlptracehttp.New(ctx,
		otlptracehttp.WithEndpoint(m.config.Tracing.Endpoint),
		otlptracehttp.WithInsecure(), // Configure based on your setup
	)
	if err != nil {
		return fmt.Errorf("failed to create OTLP exporter: %w", err)
	}

	// Create resource
	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(m.config.Tracing.ServiceName),
			semconv.ServiceVersionKey.String("dev"), // This would come from version package
		),
	)
	if err != nil {
		return fmt.Errorf("failed to create resource: %w", err)
	}

	// Create trace provider
	tp := trace.NewTracerProvider(
		trace.WithBatcher(exporter, trace.WithBatchTimeout(m.config.Tracing.BatchTimeout)),
		trace.WithResource(res),
		trace.WithSampler(trace.TraceIDRatioBased(m.config.Tracing.SampleRate)),
	)

	// Register as global tracer provider
	otel.SetTracerProvider(tp)

	// Set global propagator
	otel.SetTextMapPropagator(propagation.TraceContext{})

	// Get tracer
	m.tracer = otel.Tracer("arc")

	return nil
}

// startMetricsServer starts the Prometheus metrics server
func (m *Monitor) startMetricsServer() error {
	addr := fmt.Sprintf("%s:%d", m.config.Metrics.Host, m.config.Metrics.Port)

	mux := http.NewServeMux()
	mux.Handle(m.config.Metrics.Path, promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{}))

	m.metricsServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("Metrics server listening on %s", addr)
		if err := m.metricsServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Metrics server error: %v", err)
		}
	}()

	return nil
}

// startHealthServer starts the health check server
func (m *Monitor) startHealthServer() error {
	addr := fmt.Sprintf("%s:%d", m.config.Metrics.Host, m.config.Metrics.Port+1) // Use metrics port + 1

	mux := http.NewServeMux()
	mux.HandleFunc("/health", m.healthHandler)
	mux.HandleFunc("/ready", m.readinessHandler)

	m.healthServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("Health server listening on %s", addr)
		if err := m.healthServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Health server error: %v", err)
		}
	}()

	return nil
}

// startProfilingServer starts the pprof profiling server
func (m *Monitor) startProfilingServer() error {
	addr := fmt.Sprintf("%s:%d", m.config.Profiling.Host, m.config.Profiling.Port)

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)

	m.profilingServer = &http.Server{
		Addr:    addr,
		Handler: mux,
	}

	go func() {
		log.Printf("Profiling server listening on %s", addr)
		if err := m.profilingServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Profiling server error: %v", err)
		}
	}()

	return nil
}

// collectSystemMetrics collects system metrics periodically
func (m *Monitor) collectSystemMetrics(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			var memStats runtime.MemStats
			runtime.ReadMemStats(&memStats)

			m.metrics.ProcessMemory.Set(float64(memStats.Alloc))
			m.metrics.GoroutineCount.Set(float64(runtime.NumGoroutine()))
			m.metrics.GoInfo.Set(1)
		}
	}
}

// runHealthChecks runs health checks periodically
func (m *Monitor) runHealthChecks(ctx context.Context) {
	ticker := time.NewTicker(m.config.HealthChecks.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			m.performHealthChecks(ctx)
		}
	}
}

// performHealthChecks performs all registered health checks
func (m *Monitor) performHealthChecks(ctx context.Context) {
	checkCtx, cancel := context.WithTimeout(ctx, m.config.HealthChecks.Timeout)
	defer cancel()

	status := m.GetHealthStatus(checkCtx)
	if status.Status == "unhealthy" {
		log.Printf("Health check failed: %+v", status)
	}
}

// healthHandler handles health check HTTP requests
func (m *Monitor) healthHandler(w http.ResponseWriter, r *http.Request) {
	status := m.GetHealthStatus(r.Context())

	w.Header().Set("Content-Type", "application/json")
	if status.Status == "healthy" {
		w.WriteHeader(http.StatusOK)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
	}

	if err := json.NewEncoder(w).Encode(status); err != nil {
		http.Error(w, "Failed to encode health status", http.StatusInternalServerError)
	}
}

// readinessHandler handles readiness check HTTP requests
func (m *Monitor) readinessHandler(w http.ResponseWriter, r *http.Request) {
	// Simple readiness check - just check if the monitor is running
	m.mu.RLock()
	running := m.running
	m.mu.RUnlock()

	w.Header().Set("Content-Type", "application/json")
	if running {
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"status":"ready"}`)
	} else {
		w.WriteHeader(http.StatusServiceUnavailable)
		fmt.Fprintf(w, `{"status":"not ready"}`)
	}
}

// StartSpan starts a new trace span
func (m *Monitor) StartSpan(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	if m.tracer == nil {
		return ctx, oteltrace.SpanFromContext(ctx)
	}
	return m.tracer.Start(ctx, name, oteltrace.WithAttributes(attrs...))
}

// RecordAgentCreated records an agent creation event
func (m *Monitor) RecordAgentCreated() {
	if m.metrics != nil {
		m.metrics.AgentCreations.Inc()
		m.metrics.AgentsActive.Inc()
	}
}

// RecordAgentCompleted records an agent completion event
func (m *Monitor) RecordAgentCompleted(duration time.Duration) {
	if m.metrics != nil {
		m.metrics.AgentsActive.Dec()
		m.metrics.AgentDuration.Observe(duration.Seconds())
	}
}

// RecordAgentFailed records an agent failure event
func (m *Monitor) RecordAgentFailed() {
	if m.metrics != nil {
		m.metrics.AgentFailures.Inc()
		m.metrics.AgentsActive.Dec()
	}
}

// RecordWorkflowCreated records a workflow creation event
func (m *Monitor) RecordWorkflowCreated() {
	if m.metrics != nil {
		m.metrics.WorkflowCreations.Inc()
		m.metrics.WorkflowsActive.Inc()
	}
}

// RecordWorkflowCompleted records a workflow completion event
func (m *Monitor) RecordWorkflowCompleted(duration time.Duration) {
	if m.metrics != nil {
		m.metrics.WorkflowsActive.Dec()
		m.metrics.WorkflowDuration.Observe(duration.Seconds())
	}
}

// RecordWorkflowFailed records a workflow failure event
func (m *Monitor) RecordWorkflowFailed() {
	if m.metrics != nil {
		m.metrics.WorkflowFailures.Inc()
		m.metrics.WorkflowsActive.Dec()
	}
}

// RecordMessageProcessed records a message processing event
func (m *Monitor) RecordMessageProcessed(latency time.Duration) {
	if m.metrics != nil {
		m.metrics.MessagesProcessed.Inc()
		m.metrics.MessageLatency.Observe(latency.Seconds())
	}
}

// RecordMessageFailed records a message failure event
func (m *Monitor) RecordMessageFailed() {
	if m.metrics != nil {
		m.metrics.MessagesFailed.Inc()
	}
}

// RecordRuntimeOperation records a runtime operation
func (m *Monitor) RecordRuntimeOperation(operation string, latency time.Duration, success bool) {
	if m.metrics != nil {
		m.metrics.RuntimeOperations.Inc()
		m.metrics.RuntimeLatency.Observe(latency.Seconds())
		if !success {
			m.metrics.RuntimeErrors.Inc()
		}
	}
}