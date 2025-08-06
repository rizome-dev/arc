package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()
	
	if config == nil {
		t.Fatal("Default config should not be nil")
	}
	
	// Test server configuration
	if config.Server.GRPC.Port != 9090 {
		t.Errorf("Expected default gRPC port 9090, got %d", config.Server.GRPC.Port)
	}
	
	if config.Server.HTTP.Port != 8080 {
		t.Errorf("Expected default HTTP port 8080, got %d", config.Server.HTTP.Port)
	}
	
	if config.Server.GRPC.Host != "0.0.0.0" {
		t.Errorf("Expected default gRPC host '0.0.0.0', got %s", config.Server.GRPC.Host)
	}
	
	if config.Server.TLS.Enabled {
		t.Error("TLS should be disabled by default")
	}
	
	// Test runtime configuration
	if config.Runtime.Type != "docker" {
		t.Errorf("Expected default runtime type 'docker', got %s", config.Runtime.Type)
	}
	
	if config.Runtime.Namespace != "default" {
		t.Errorf("Expected default namespace 'default', got %s", config.Runtime.Namespace)
	}
	
	// Test state configuration
	if config.State.Type != "memory" {
		t.Errorf("Expected default state type 'memory', got %s", config.State.Type)
	}
	
	// Test message queue configuration
	if config.MessageQueue.StorePath != "./arc-amq-data" {
		t.Errorf("Expected default AMQ store path './arc-amq-data', got %s", config.MessageQueue.StorePath)
	}
	
	if config.MessageQueue.WorkerPoolSize != 10 {
		t.Errorf("Expected default worker pool size 10, got %d", config.MessageQueue.WorkerPoolSize)
	}
	
	// Test monitoring configuration
	if !config.Monitoring.Metrics.Enabled {
		t.Error("Metrics should be enabled by default")
	}
	
	if config.Monitoring.Metrics.Port != 9091 {
		t.Errorf("Expected default metrics port 9091, got %d", config.Monitoring.Metrics.Port)
	}
	
	// Test logging configuration
	if config.Logging.Level != "info" {
		t.Errorf("Expected default log level 'info', got %s", config.Logging.Level)
	}
	
	if config.Logging.Format != "json" {
		t.Errorf("Expected default log format 'json', got %s", config.Logging.Format)
	}
	
	// Test feature flags
	if !config.Features.EnableWorkflows {
		t.Error("Workflows should be enabled by default")
	}
	
	if !config.Features.EnableMetrics {
		t.Error("Metrics should be enabled by default")
	}
	
	if config.Features.EnableAuth {
		t.Error("Auth should be disabled by default")
	}
}

func TestLoadConfigFromYAMLFile(t *testing.T) {
	// Create a temporary YAML config file
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "test-config.yaml")
	
	yamlContent := `
server:
  grpc:
    host: "127.0.0.1"
    port: 9091
  http:
    host: "127.0.0.1"
    port: 8081
  tls:
    enabled: true
    cert_file: "/path/to/cert.pem"
    key_file: "/path/to/key.pem"

runtime:
  type: "kubernetes"
  namespace: "test-namespace"

state:
  type: "badger"
  connection_url: "/tmp/test-state"

message_queue:
  store_path: "/tmp/test-amq"
  worker_pool_size: 20

logging:
  level: "debug"
  format: "text"

features:
  enable_auth: true
  enable_rate_limit: true
`
	
	if err := os.WriteFile(configFile, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to write test config file: %v", err)
	}
	
	config, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	
	// Verify loaded values
	if config.Server.GRPC.Host != "127.0.0.1" {
		t.Errorf("Expected gRPC host '127.0.0.1', got %s", config.Server.GRPC.Host)
	}
	
	if config.Server.GRPC.Port != 9091 {
		t.Errorf("Expected gRPC port 9091, got %d", config.Server.GRPC.Port)
	}
	
	if config.Server.HTTP.Port != 8081 {
		t.Errorf("Expected HTTP port 8081, got %d", config.Server.HTTP.Port)
	}
	
	if !config.Server.TLS.Enabled {
		t.Error("TLS should be enabled")
	}
	
	if config.Server.TLS.CertFile != "/path/to/cert.pem" {
		t.Errorf("Expected cert file '/path/to/cert.pem', got %s", config.Server.TLS.CertFile)
	}
	
	if config.Runtime.Type != "kubernetes" {
		t.Errorf("Expected runtime type 'kubernetes', got %s", config.Runtime.Type)
	}
	
	if config.Runtime.Namespace != "test-namespace" {
		t.Errorf("Expected namespace 'test-namespace', got %s", config.Runtime.Namespace)
	}
	
	if config.State.Type != "badger" {
		t.Errorf("Expected state type 'badger', got %s", config.State.Type)
	}
	
	if config.MessageQueue.WorkerPoolSize != 20 {
		t.Errorf("Expected worker pool size 20, got %d", config.MessageQueue.WorkerPoolSize)
	}
	
	if config.Logging.Level != "debug" {
		t.Errorf("Expected log level 'debug', got %s", config.Logging.Level)
	}
	
	if config.Logging.Format != "text" {
		t.Errorf("Expected log format 'text', got %s", config.Logging.Format)
	}
	
	if !config.Features.EnableAuth {
		t.Error("Auth should be enabled")
	}
}

func TestLoadConfigFromJSONFile(t *testing.T) {
	// Create a temporary JSON config file
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "test-config.json")
	
	jsonContent := `{
  "server": {
    "grpc": {
      "host": "0.0.0.0",
      "port": 9092
    },
    "http": {
      "port": 8082
    }
  },
  "runtime": {
    "type": "gvisor"
  },
  "logging": {
    "level": "warn"
  }
}`
	
	if err := os.WriteFile(configFile, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("Failed to write test config file: %v", err)
	}
	
	config, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	
	// Verify loaded values
	if config.Server.GRPC.Port != 9092 {
		t.Errorf("Expected gRPC port 9092, got %d", config.Server.GRPC.Port)
	}
	
	if config.Server.HTTP.Port != 8082 {
		t.Errorf("Expected HTTP port 8082, got %d", config.Server.HTTP.Port)
	}
	
	if config.Runtime.Type != "gvisor" {
		t.Errorf("Expected runtime type 'gvisor', got %s", config.Runtime.Type)
	}
	
	if config.Logging.Level != "warn" {
		t.Errorf("Expected log level 'warn', got %s", config.Logging.Level)
	}
}

func TestLoadConfigWithEnvironmentVariables(t *testing.T) {
	// Set environment variables
	testEnvVars := map[string]string{
		"ARC_GRPC_HOST":              "test-host",
		"ARC_GRPC_PORT":              "9999",
		"ARC_HTTP_PORT":              "8888",
		"ARC_TLS_ENABLED":            "true",
		"ARC_TLS_CERT_FILE":          "/env/cert.pem",
		"ARC_TLS_KEY_FILE":           "/env/key.pem",
		"ARC_RUNTIME_TYPE":           "kubernetes",
		"ARC_RUNTIME_NAMESPACE":      "env-namespace",
		"ARC_STATE_TYPE":             "badger",
		"ARC_STATE_CONNECTION_URL":   "/env/state",
		"ARC_AMQ_STORE_PATH":         "/env/amq",
		"ARC_AMQ_WORKER_POOL_SIZE":   "15",
		"ARC_METRICS_ENABLED":        "false",
		"ARC_METRICS_PORT":           "9999",
		"ARC_TRACING_ENABLED":        "true",
		"ARC_TRACING_ENDPOINT":       "http://jaeger:14268/api/traces",
		"ARC_LOG_LEVEL":              "error",
		"ARC_LOG_FORMAT":             "text",
		"ARC_AUTH_ENABLED":           "true",
		"ARC_JWT_SECRET_KEY":         "test-secret-key",
		"ARC_RATE_LIMIT_ENABLED":     "true",
	}
	
	// Set environment variables
	for key, value := range testEnvVars {
		os.Setenv(key, value)
		defer os.Unsetenv(key)
	}
	
	// Load config without file (env vars only)
	config, err := LoadConfig("")
	if err != nil {
		t.Fatalf("Failed to load config from env vars: %v", err)
	}
	
	// Verify environment variable values
	if config.Server.GRPC.Host != "test-host" {
		t.Errorf("Expected gRPC host 'test-host', got %s", config.Server.GRPC.Host)
	}
	
	if config.Server.GRPC.Port != 9999 {
		t.Errorf("Expected gRPC port 9999, got %d", config.Server.GRPC.Port)
	}
	
	if config.Server.HTTP.Port != 8888 {
		t.Errorf("Expected HTTP port 8888, got %d", config.Server.HTTP.Port)
	}
	
	if !config.Server.TLS.Enabled {
		t.Error("TLS should be enabled from env var")
	}
	
	if config.Server.TLS.CertFile != "/env/cert.pem" {
		t.Errorf("Expected cert file '/env/cert.pem', got %s", config.Server.TLS.CertFile)
	}
	
	if config.Runtime.Type != "kubernetes" {
		t.Errorf("Expected runtime type 'kubernetes', got %s", config.Runtime.Type)
	}
	
	if config.Runtime.Namespace != "env-namespace" {
		t.Errorf("Expected namespace 'env-namespace', got %s", config.Runtime.Namespace)
	}
	
	if config.State.Type != "badger" {
		t.Errorf("Expected state type 'badger', got %s", config.State.Type)
	}
	
	if config.State.ConnectionURL != "/env/state" {
		t.Errorf("Expected state connection URL '/env/state', got %s", config.State.ConnectionURL)
	}
	
	if config.MessageQueue.StorePath != "/env/amq" {
		t.Errorf("Expected AMQ store path '/env/amq', got %s", config.MessageQueue.StorePath)
	}
	
	if config.MessageQueue.WorkerPoolSize != 15 {
		t.Errorf("Expected worker pool size 15, got %d", config.MessageQueue.WorkerPoolSize)
	}
	
	if config.Monitoring.Metrics.Enabled {
		t.Error("Metrics should be disabled from env var")
	}
	
	if config.Monitoring.Metrics.Port != 9999 {
		t.Errorf("Expected metrics port 9999, got %d", config.Monitoring.Metrics.Port)
	}
	
	if !config.Monitoring.Tracing.Enabled {
		t.Error("Tracing should be enabled from env var")
	}
	
	if config.Monitoring.Tracing.Endpoint != "http://jaeger:14268/api/traces" {
		t.Errorf("Expected tracing endpoint, got %s", config.Monitoring.Tracing.Endpoint)
	}
	
	if config.Logging.Level != "error" {
		t.Errorf("Expected log level 'error', got %s", config.Logging.Level)
	}
	
	if config.Logging.Format != "text" {
		t.Errorf("Expected log format 'text', got %s", config.Logging.Format)
	}
	
	if !config.Security.Authentication.Enabled {
		t.Error("Auth should be enabled from env var")
	}
	
	if config.Security.Authentication.JWTConfig.SecretKey != "test-secret-key" {
		t.Errorf("Expected JWT secret key 'test-secret-key', got %s", config.Security.Authentication.JWTConfig.SecretKey)
	}
	
	if !config.Security.RateLimit.Enabled {
		t.Error("Rate limiting should be enabled from env var")
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name        string
		config      func() *Config
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid config",
			config: func() *Config {
				return DefaultConfig()
			},
			expectError: false,
		},
		{
			name: "invalid gRPC port - too low",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Server.GRPC.Port = 0
				return cfg
			},
			expectError: true,
			errorMsg:    "invalid gRPC port",
		},
		{
			name: "invalid gRPC port - too high",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Server.GRPC.Port = 70000
				return cfg
			},
			expectError: true,
			errorMsg:    "invalid gRPC port",
		},
		{
			name: "invalid HTTP port - too low",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Server.HTTP.Port = -1
				return cfg
			},
			expectError: true,
			errorMsg:    "invalid HTTP port",
		},
		{
			name: "invalid HTTP port - too high",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Server.HTTP.Port = 65536
				return cfg
			},
			expectError: true,
			errorMsg:    "invalid HTTP port",
		},
		{
			name: "TLS enabled without cert file",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Server.TLS.Enabled = true
				cfg.Server.TLS.CertFile = ""
				return cfg
			},
			expectError: true,
			errorMsg:    "TLS cert file must be specified",
		},
		{
			name: "TLS enabled without key file",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Server.TLS.Enabled = true
				cfg.Server.TLS.CertFile = "/path/to/cert.pem"
				cfg.Server.TLS.KeyFile = ""
				return cfg
			},
			expectError: true,
			errorMsg:    "TLS key file must be specified",
		},
		{
			name: "invalid runtime type",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Runtime.Type = "invalid"
				return cfg
			},
			expectError: true,
			errorMsg:    "invalid runtime type",
		},
		{
			name: "invalid state type",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.State.Type = "invalid"
				return cfg
			},
			expectError: true,
			errorMsg:    "invalid state type",
		},
		{
			name: "invalid log level",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Logging.Level = "invalid"
				return cfg
			},
			expectError: true,
			errorMsg:    "invalid log level",
		},
		{
			name: "invalid log format",
			config: func() *Config {
				cfg := DefaultConfig()
				cfg.Logging.Format = "invalid"
				return cfg
			},
			expectError: true,
			errorMsg:    "invalid log format",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config()
			err := config.Validate()
			
			if tt.expectError {
				if err == nil {
					t.Error("Expected validation error but got none")
				} else if tt.errorMsg != "" && !containsString(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing %q, got: %v", tt.errorMsg, err)
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected validation error: %v", err)
				}
			}
		})
	}
}

func TestConfigString(t *testing.T) {
	config := DefaultConfig()
	configStr := config.String()
	
	if configStr == "" {
		t.Error("Config string should not be empty")
	}
	
	// Should contain YAML format
	if !containsString(configStr, "server:") {
		t.Error("Config string should contain 'server:' key")
	}
	
	if !containsString(configStr, "runtime:") {
		t.Error("Config string should contain 'runtime:' key")
	}
}

func TestConfigSaveToFile(t *testing.T) {
	tempDir := t.TempDir()
	
	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		{
			name:     "save as YAML",
			filename: "config.yaml",
			wantErr:  false,
		},
		{
			name:     "save as YML",
			filename: "config.yml",
			wantErr:  false,
		},
		{
			name:     "save as JSON",
			filename: "config.json",
			wantErr:  false,
		},
		{
			name:     "unsupported format",
			filename: "config.txt",
			wantErr:  true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := DefaultConfig()
			filePath := filepath.Join(tempDir, tt.filename)
			
			err := config.SaveToFile(filePath)
			
			if tt.wantErr {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}
			
			if err != nil {
				t.Errorf("Unexpected error: %v", err)
				return
			}
			
			// Verify file exists
			if _, err := os.Stat(filePath); os.IsNotExist(err) {
				t.Error("Config file should have been created")
				return
			}
			
			// Try to load the saved config to verify it's valid
			loadedConfig, err := LoadConfig(filePath)
			if err != nil {
				t.Errorf("Failed to load saved config: %v", err)
				return
			}
			
			// Basic verification that key values match
			if loadedConfig.Server.GRPC.Port != config.Server.GRPC.Port {
				t.Error("Saved config should match original config")
			}
		})
	}
}

func TestLoadConfigNonexistentFile(t *testing.T) {
	_, err := LoadConfig("/nonexistent/path/config.yaml")
	if err == nil {
		t.Error("Expected error when loading nonexistent file")
	}
}

func TestLoadConfigInvalidFormat(t *testing.T) {
	tempDir := t.TempDir()
	invalidFile := filepath.Join(tempDir, "invalid.yaml")
	
	// Write invalid YAML
	invalidContent := `
server:
  grpc:
    port: not-a-number
    host: [invalid yaml structure
`
	
	if err := os.WriteFile(invalidFile, []byte(invalidContent), 0644); err != nil {
		t.Fatalf("Failed to write invalid config file: %v", err)
	}
	
	_, err := LoadConfig(invalidFile)
	if err == nil {
		t.Error("Expected error when loading invalid config file")
	}
}

func TestLoadConfigUnsupportedExtension(t *testing.T) {
	tempDir := t.TempDir()
	unsupportedFile := filepath.Join(tempDir, "config.txt")
	
	if err := os.WriteFile(unsupportedFile, []byte("some content"), 0644); err != nil {
		t.Fatalf("Failed to write unsupported config file: %v", err)
	}
	
	_, err := LoadConfig(unsupportedFile)
	if err == nil {
		t.Error("Expected error when loading unsupported config file format")
	}
}

func TestEnvironmentVariableOverridesFile(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "config.yaml")
	
	// Create config file with specific values
	yamlContent := `
server:
  grpc:
    port: 9090
  http:
    port: 8080
runtime:
  type: "docker"
logging:
  level: "info"
`
	
	if err := os.WriteFile(configFile, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}
	
	// Set environment variables to override file values
	os.Setenv("ARC_GRPC_PORT", "9999")
	os.Setenv("ARC_HTTP_PORT", "8888")
	os.Setenv("ARC_RUNTIME_TYPE", "kubernetes")
	os.Setenv("ARC_LOG_LEVEL", "debug")
	defer func() {
		os.Unsetenv("ARC_GRPC_PORT")
		os.Unsetenv("ARC_HTTP_PORT")
		os.Unsetenv("ARC_RUNTIME_TYPE")
		os.Unsetenv("ARC_LOG_LEVEL")
	}()
	
	config, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	
	// Verify environment variables override file values
	if config.Server.GRPC.Port != 9999 {
		t.Errorf("Expected gRPC port 9999 (from env), got %d", config.Server.GRPC.Port)
	}
	
	if config.Server.HTTP.Port != 8888 {
		t.Errorf("Expected HTTP port 8888 (from env), got %d", config.Server.HTTP.Port)
	}
	
	if config.Runtime.Type != "kubernetes" {
		t.Errorf("Expected runtime type 'kubernetes' (from env), got %s", config.Runtime.Type)
	}
	
	if config.Logging.Level != "debug" {
		t.Errorf("Expected log level 'debug' (from env), got %s", config.Logging.Level)
	}
}

func TestComplexConfigWithAllFeatures(t *testing.T) {
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "complex-config.yaml")
	
	complexConfig := `
server:
  grpc:
    host: "0.0.0.0"
    port: 9090
    max_recv_msg_size: 8388608
    max_send_msg_size: 8388608
    connection_timeout: "60s"
    reflection_enabled: true
    health_check_enabled: true
  http:
    host: "0.0.0.0"
    port: 8080
    read_timeout: "30s"
    write_timeout: "30s"
    idle_timeout: "120s"
    max_header_bytes: 1048576
    cors_enabled: true
    cors_allowed_origins:
      - "https://example.com"
      - "https://app.example.com"
  tls:
    enabled: false
  shutdown_timeout: "30s"
  max_connections: 1000

runtime:
  type: "kubernetes"
  namespace: "arc-production"
  timeout: "10m"
  config:
    node_selector: "app=arc"
  registries:
    - name: "docker-hub"
      url: "https://registry-1.docker.io"
      username: "user"
      password: "pass"
    - name: "internal"
      url: "https://registry.internal.com"
      insecure: true

state:
  type: "badger"
  connection_url: "/data/arc-state"
  retention_days: 90
  backup_enabled: true
  backup_interval: "24h"
  config:
    sync_writes: "true"

message_queue:
  store_path: "/data/arc-amq"
  worker_pool_size: 20
  message_timeout: "60s"
  retry_attempts: 5
  retry_backoff: "2s"
  max_message_size: 16777216
  compression_enabled: true

monitoring:
  metrics:
    enabled: true
    host: "0.0.0.0"
    port: 9091
    path: "/metrics"
    namespace: "arc"
    subsystem: "orchestrator"
    push_gateway: "http://prometheus-pushgateway:9091"
    push_interval: "30s"
  tracing:
    enabled: true
    endpoint: "http://jaeger:14268/api/traces"
    service_name: "arc-orchestrator"
    sample_rate: 0.1
    batch_timeout: "5s"
  health_checks:
    enabled: true
    interval: "30s"
    timeout: "10s"
    checks:
      - name: "database"
        type: "tcp"
        config:
          address: "postgres:5432"
        critical: true
      - name: "redis"
        type: "tcp"
        config:
          address: "redis:6379"
        critical: false
  profiling:
    enabled: true
    host: "127.0.0.1"
    port: 6060
    cpu_profile: true
    mem_profile: true

logging:
  level: "info"
  format: "json"
  output: "stdout"
  file_path: "/var/log/arc/arc.log"
  max_size: 100
  max_backups: 5
  max_age: 14
  compress: true

security:
  authentication:
    enabled: true
    type: "jwt"
    config:
      issuer: "arc-orchestrator"
    jwt:
      secret_key: "super-secret-key-change-me"
      issuer: "arc-orchestrator"
      expiry_duration: "24h"
      algorithm: "HS256"
  authorization:
    enabled: true
    type: "rbac"
    admin_users:
      - "admin@example.com"
      - "devops@example.com"
    permissions:
      admin:
        - "workflow:*"
        - "agent:*"
        - "system:*"
      user:
        - "workflow:read"
        - "agent:read"
  rate_limit:
    enabled: true
    global_limit: 1000
    global_window: "1m"
    user_limit: 100
    user_window: "1m"
    endpoint_limits:
      "/api/v1/workflows":
        limit: 50
        window: "1m"

features:
  enable_workflows: true
  enable_metrics: true
  enable_tracing: true
  enable_profiling: true
  enable_rate_limit: true
  enable_auth: true
  enable_health_check: true
`
	
	if err := os.WriteFile(configFile, []byte(complexConfig), 0644); err != nil {
		t.Fatalf("Failed to write complex config file: %v", err)
	}
	
	config, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load complex config: %v", err)
	}
	
	// Verify complex configuration values
	if config.Server.GRPC.MaxRecvMsgSize != 8388608 {
		t.Errorf("Expected max recv msg size 8388608, got %d", config.Server.GRPC.MaxRecvMsgSize)
	}
	
	if len(config.Server.HTTP.CORSAllowedOrigins) != 2 {
		t.Errorf("Expected 2 CORS allowed origins, got %d", len(config.Server.HTTP.CORSAllowedOrigins))
	}
	
	if len(config.Runtime.Registries) != 2 {
		t.Errorf("Expected 2 registries, got %d", len(config.Runtime.Registries))
	}
	
	if config.Runtime.Registries[0].Name != "docker-hub" {
		t.Errorf("Expected first registry name 'docker-hub', got %s", config.Runtime.Registries[0].Name)
	}
	
	if config.State.RetentionDays != 90 {
		t.Errorf("Expected retention days 90, got %d", config.State.RetentionDays)
	}
	
	if !config.State.BackupEnabled {
		t.Error("Backup should be enabled")
	}
	
	if config.MessageQueue.RetryAttempts != 5 {
		t.Errorf("Expected retry attempts 5, got %d", config.MessageQueue.RetryAttempts)
	}
	
	if config.Monitoring.Tracing.SampleRate != 0.1 {
		t.Errorf("Expected tracing sample rate 0.1, got %f", config.Monitoring.Tracing.SampleRate)
	}
	
	if len(config.Monitoring.HealthChecks.Checks) != 2 {
		t.Errorf("Expected 2 health checks, got %d", len(config.Monitoring.HealthChecks.Checks))
	}
	
	if config.Security.Authentication.JWTConfig.ExpiryDuration != 24*time.Hour {
		t.Errorf("Expected JWT expiry 24h, got %v", config.Security.Authentication.JWTConfig.ExpiryDuration)
	}
	
	if len(config.Security.Authorization.AdminUsers) != 2 {
		t.Errorf("Expected 2 admin users, got %d", len(config.Security.Authorization.AdminUsers))
	}
	
	if config.Security.RateLimit.GlobalLimit != 1000 {
		t.Errorf("Expected global rate limit 1000, got %d", config.Security.RateLimit.GlobalLimit)
	}
	
	if len(config.Security.RateLimit.EndpointLimits) != 1 {
		t.Errorf("Expected 1 endpoint limit, got %d", len(config.Security.RateLimit.EndpointLimits))
	}
}

// Helper function to check if a string contains a substring
func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(substr) == 0 || s[0:len(substr)] == substr || 
		s[len(s)-len(substr):] == substr || stringContains(s, substr))
}

func stringContains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestConfigMerging(t *testing.T) {
	// Test that file config and env vars merge properly
	tempDir := t.TempDir()
	configFile := filepath.Join(tempDir, "merge-test.yaml")
	
	// File has some values
	yamlContent := `
server:
  grpc:
    port: 9090
    host: "0.0.0.0"
  http:
    port: 8080
runtime:
  type: "docker"
logging:
  level: "info"
  format: "json"
`
	
	if err := os.WriteFile(configFile, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("Failed to write config file: %v", err)
	}
	
	// Env vars override some values and add others
	os.Setenv("ARC_GRPC_PORT", "9999")           // Override file value
	os.Setenv("ARC_RUNTIME_NAMESPACE", "test")   // Add new value not in file
	os.Setenv("ARC_LOG_FORMAT", "text")          // Override file value
	defer func() {
		os.Unsetenv("ARC_GRPC_PORT")
		os.Unsetenv("ARC_RUNTIME_NAMESPACE")
		os.Unsetenv("ARC_LOG_FORMAT")
	}()
	
	config, err := LoadConfig(configFile)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}
	
	// File values that weren't overridden should remain
	if config.Server.GRPC.Host != "0.0.0.0" {
		t.Errorf("Expected gRPC host '0.0.0.0' (from file), got %s", config.Server.GRPC.Host)
	}
	
	if config.Server.HTTP.Port != 8080 {
		t.Errorf("Expected HTTP port 8080 (from file), got %d", config.Server.HTTP.Port)
	}
	
	if config.Runtime.Type != "docker" {
		t.Errorf("Expected runtime type 'docker' (from file), got %s", config.Runtime.Type)
	}
	
	if config.Logging.Level != "info" {
		t.Errorf("Expected log level 'info' (from file), got %s", config.Logging.Level)
	}
	
	// Env var overrides
	if config.Server.GRPC.Port != 9999 {
		t.Errorf("Expected gRPC port 9999 (from env override), got %d", config.Server.GRPC.Port)
	}
	
	if config.Logging.Format != "text" {
		t.Errorf("Expected log format 'text' (from env override), got %s", config.Logging.Format)
	}
	
	// Env var additions
	if config.Runtime.Namespace != "test" {
		t.Errorf("Expected namespace 'test' (from env), got %s", config.Runtime.Namespace)
	}
}