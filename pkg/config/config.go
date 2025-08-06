// Package config provides comprehensive configuration management for ARC
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config holds the complete configuration for ARC
type Config struct {
	// Server configuration
	Server ServerConfig `yaml:"server" json:"server"`

	// Runtime configuration
	Runtime RuntimeConfig `yaml:"runtime" json:"runtime"`

	// State management configuration
	State StateConfig `yaml:"state" json:"state"`

	// Message queue configuration
	MessageQueue MessageQueueConfig `yaml:"message_queue" json:"message_queue"`

	// Monitoring configuration
	Monitoring MonitoringConfig `yaml:"monitoring" json:"monitoring"`

	// Logging configuration
	Logging LoggingConfig `yaml:"logging" json:"logging"`

	// Security configuration
	Security SecurityConfig `yaml:"security" json:"security"`

	// Feature flags
	Features FeatureConfig `yaml:"features" json:"features"`
}

// ServerConfig holds server-related configuration
type ServerConfig struct {
	// gRPC server configuration
	GRPC GRPCConfig `yaml:"grpc" json:"grpc"`

	// HTTP server configuration
	HTTP HTTPConfig `yaml:"http" json:"http"`

	// TLS configuration
	TLS TLSConfig `yaml:"tls" json:"tls"`

	// Graceful shutdown timeout
	ShutdownTimeout time.Duration `yaml:"shutdown_timeout" json:"shutdown_timeout"`

	// PID file location
	PIDFile string `yaml:"pid_file" json:"pid_file"`

	// Maximum number of concurrent connections
	MaxConnections int `yaml:"max_connections" json:"max_connections"`
}

// GRPCConfig holds gRPC server configuration
type GRPCConfig struct {
	Host               string        `yaml:"host" json:"host"`
	Port               int           `yaml:"port" json:"port"`
	MaxRecvMsgSize     int           `yaml:"max_recv_msg_size" json:"max_recv_msg_size"`
	MaxSendMsgSize     int           `yaml:"max_send_msg_size" json:"max_send_msg_size"`
	ConnectionTimeout  time.Duration `yaml:"connection_timeout" json:"connection_timeout"`
	ReflectionEnabled  bool          `yaml:"reflection_enabled" json:"reflection_enabled"`
	HealthCheckEnabled bool          `yaml:"health_check_enabled" json:"health_check_enabled"`
}

// HTTPConfig holds HTTP server configuration
type HTTPConfig struct {
	Host               string        `yaml:"host" json:"host"`
	Port               int           `yaml:"port" json:"port"`
	ReadTimeout        time.Duration `yaml:"read_timeout" json:"read_timeout"`
	WriteTimeout       time.Duration `yaml:"write_timeout" json:"write_timeout"`
	IdleTimeout        time.Duration `yaml:"idle_timeout" json:"idle_timeout"`
	MaxHeaderBytes     int           `yaml:"max_header_bytes" json:"max_header_bytes"`
	CORSEnabled        bool          `yaml:"cors_enabled" json:"cors_enabled"`
	CORSAllowedOrigins []string      `yaml:"cors_allowed_origins" json:"cors_allowed_origins"`
}

// TLSConfig holds TLS configuration
type TLSConfig struct {
	Enabled  bool   `yaml:"enabled" json:"enabled"`
	CertFile string `yaml:"cert_file" json:"cert_file"`
	KeyFile  string `yaml:"key_file" json:"key_file"`
	CAFile   string `yaml:"ca_file" json:"ca_file"`
}

// RuntimeConfig holds runtime configuration
type RuntimeConfig struct {
	Type       string            `yaml:"type" json:"type"`
	Namespace  string            `yaml:"namespace" json:"namespace"`
	Config     map[string]string `yaml:"config" json:"config"`
	Timeout    time.Duration     `yaml:"timeout" json:"timeout"`
	Registries []RegistryConfig  `yaml:"registries" json:"registries"`
}

// RegistryConfig holds container registry configuration
type RegistryConfig struct {
	Name     string `yaml:"name" json:"name"`
	URL      string `yaml:"url" json:"url"`
	Username string `yaml:"username" json:"username"`
	Password string `yaml:"password" json:"password"`
	Insecure bool   `yaml:"insecure" json:"insecure"`
}

// StateConfig holds state management configuration
type StateConfig struct {
	Type           string            `yaml:"type" json:"type"`
	ConnectionURL  string            `yaml:"connection_url" json:"connection_url"`
	DatabaseName   string            `yaml:"database_name" json:"database_name"`
	Config         map[string]string `yaml:"config" json:"config"`
	RetentionDays  int               `yaml:"retention_days" json:"retention_days"`
	BackupEnabled  bool              `yaml:"backup_enabled" json:"backup_enabled"`
	BackupInterval time.Duration     `yaml:"backup_interval" json:"backup_interval"`
}

// MessageQueueConfig holds message queue configuration
type MessageQueueConfig struct {
	StorePath         string        `yaml:"store_path" json:"store_path"`
	WorkerPoolSize    int           `yaml:"worker_pool_size" json:"worker_pool_size"`
	MessageTimeout    time.Duration `yaml:"message_timeout" json:"message_timeout"`
	RetryAttempts     int           `yaml:"retry_attempts" json:"retry_attempts"`
	RetryBackoff      time.Duration `yaml:"retry_backoff" json:"retry_backoff"`
	MaxMessageSize    int           `yaml:"max_message_size" json:"max_message_size"`
	CompressionEnabled bool         `yaml:"compression_enabled" json:"compression_enabled"`
}

// MonitoringConfig holds monitoring and observability configuration
type MonitoringConfig struct {
	Metrics       MetricsConfig       `yaml:"metrics" json:"metrics"`
	Tracing       TracingConfig       `yaml:"tracing" json:"tracing"`
	HealthChecks  HealthChecksConfig  `yaml:"health_checks" json:"health_checks"`
	Profiling     ProfilingConfig     `yaml:"profiling" json:"profiling"`
}

// MetricsConfig holds Prometheus metrics configuration
type MetricsConfig struct {
	Enabled       bool          `yaml:"enabled" json:"enabled"`
	Host          string        `yaml:"host" json:"host"`
	Port          int           `yaml:"port" json:"port"`
	Path          string        `yaml:"path" json:"path"`
	Namespace     string        `yaml:"namespace" json:"namespace"`
	Subsystem     string        `yaml:"subsystem" json:"subsystem"`
	PushGateway   string        `yaml:"push_gateway" json:"push_gateway"`
	PushInterval  time.Duration `yaml:"push_interval" json:"push_interval"`
}

// TracingConfig holds OpenTelemetry tracing configuration
type TracingConfig struct {
	Enabled    bool    `yaml:"enabled" json:"enabled"`
	Endpoint   string  `yaml:"endpoint" json:"endpoint"`
	ServiceName string `yaml:"service_name" json:"service_name"`
	SampleRate float64 `yaml:"sample_rate" json:"sample_rate"`
	BatchTimeout time.Duration `yaml:"batch_timeout" json:"batch_timeout"`
}

// HealthChecksConfig holds health check configuration
type HealthChecksConfig struct {
	Enabled  bool          `yaml:"enabled" json:"enabled"`
	Interval time.Duration `yaml:"interval" json:"interval"`
	Timeout  time.Duration `yaml:"timeout" json:"timeout"`
	Checks   []HealthCheck `yaml:"checks" json:"checks"`
}

// HealthCheck represents a single health check
type HealthCheck struct {
	Name     string            `yaml:"name" json:"name"`
	Type     string            `yaml:"type" json:"type"`
	Config   map[string]string `yaml:"config" json:"config"`
	Critical bool              `yaml:"critical" json:"critical"`
}

// ProfilingConfig holds profiling configuration
type ProfilingConfig struct {
	Enabled    bool   `yaml:"enabled" json:"enabled"`
	Host       string `yaml:"host" json:"host"`
	Port       int    `yaml:"port" json:"port"`
	CPUProfile bool   `yaml:"cpu_profile" json:"cpu_profile"`
	MemProfile bool   `yaml:"mem_profile" json:"mem_profile"`
}

// LoggingConfig holds logging configuration
type LoggingConfig struct {
	Level      string `yaml:"level" json:"level"`
	Format     string `yaml:"format" json:"format"`
	Output     string `yaml:"output" json:"output"`
	FilePath   string `yaml:"file_path" json:"file_path"`
	MaxSize    int    `yaml:"max_size" json:"max_size"`
	MaxBackups int    `yaml:"max_backups" json:"max_backups"`
	MaxAge     int    `yaml:"max_age" json:"max_age"`
	Compress   bool   `yaml:"compress" json:"compress"`
}

// SecurityConfig holds security-related configuration
type SecurityConfig struct {
	Authentication AuthenticationConfig `yaml:"authentication" json:"authentication"`
	Authorization  AuthorizationConfig  `yaml:"authorization" json:"authorization"`
	RateLimit      RateLimitConfig      `yaml:"rate_limit" json:"rate_limit"`
}

// AuthenticationConfig holds authentication configuration
type AuthenticationConfig struct {
	Enabled   bool              `yaml:"enabled" json:"enabled"`
	Type      string            `yaml:"type" json:"type"`
	Config    map[string]string `yaml:"config" json:"config"`
	JWTConfig JWTConfig         `yaml:"jwt" json:"jwt"`
}

// JWTConfig holds JWT configuration
type JWTConfig struct {
	SecretKey      string        `yaml:"secret_key" json:"secret_key"`
	Issuer         string        `yaml:"issuer" json:"issuer"`
	ExpiryDuration time.Duration `yaml:"expiry_duration" json:"expiry_duration"`
	Algorithm      string        `yaml:"algorithm" json:"algorithm"`
}

// AuthorizationConfig holds authorization configuration
type AuthorizationConfig struct {
	Enabled     bool              `yaml:"enabled" json:"enabled"`
	Type        string            `yaml:"type" json:"type"`
	Config      map[string]string `yaml:"config" json:"config"`
	AdminUsers  []string          `yaml:"admin_users" json:"admin_users"`
	Permissions map[string][]string `yaml:"permissions" json:"permissions"`
}

// RateLimitConfig holds rate limiting configuration
type RateLimitConfig struct {
	Enabled       bool                     `yaml:"enabled" json:"enabled"`
	GlobalLimit   int                      `yaml:"global_limit" json:"global_limit"`
	GlobalWindow  time.Duration            `yaml:"global_window" json:"global_window"`
	UserLimit     int                      `yaml:"user_limit" json:"user_limit"`
	UserWindow    time.Duration            `yaml:"user_window" json:"user_window"`
	EndpointLimits map[string]EndpointLimit `yaml:"endpoint_limits" json:"endpoint_limits"`
}

// EndpointLimit holds endpoint-specific rate limits
type EndpointLimit struct {
	Limit  int           `yaml:"limit" json:"limit"`
	Window time.Duration `yaml:"window" json:"window"`
}

// FeatureConfig holds feature flags
type FeatureConfig struct {
	EnableWorkflows   bool `yaml:"enable_workflows" json:"enable_workflows"`
	EnableMetrics     bool `yaml:"enable_metrics" json:"enable_metrics"`
	EnableTracing     bool `yaml:"enable_tracing" json:"enable_tracing"`
	EnableProfiling   bool `yaml:"enable_profiling" json:"enable_profiling"`
	EnableRateLimit   bool `yaml:"enable_rate_limit" json:"enable_rate_limit"`
	EnableAuth        bool `yaml:"enable_auth" json:"enable_auth"`
	EnableHealthCheck bool `yaml:"enable_health_check" json:"enable_health_check"`
}

// DefaultConfig returns a default configuration
func DefaultConfig() *Config {
	return &Config{
		Server: ServerConfig{
			GRPC: GRPCConfig{
				Host:               "0.0.0.0",
				Port:               9090,
				MaxRecvMsgSize:     4 * 1024 * 1024, // 4MB
				MaxSendMsgSize:     4 * 1024 * 1024, // 4MB
				ConnectionTimeout:  30 * time.Second,
				ReflectionEnabled:  true,
				HealthCheckEnabled: true,
			},
			HTTP: HTTPConfig{
				Host:               "0.0.0.0",
				Port:               8080,
				ReadTimeout:        10 * time.Second,
				WriteTimeout:       10 * time.Second,
				IdleTimeout:        60 * time.Second,
				MaxHeaderBytes:     1 << 20, // 1MB
				CORSEnabled:        true,
				CORSAllowedOrigins: []string{"*"},
			},
			TLS: TLSConfig{
				Enabled: false,
			},
			ShutdownTimeout: 30 * time.Second,
			PIDFile:         "/var/run/arc.pid",
			MaxConnections:  1000,
		},
		Runtime: RuntimeConfig{
			Type:      "docker",
			Namespace: "default",
			Timeout:   5 * time.Minute,
			Config:    make(map[string]string),
		},
		State: StateConfig{
			Type:          "memory",
			RetentionDays: 30,
			BackupEnabled: false,
			Config:        make(map[string]string),
		},
		MessageQueue: MessageQueueConfig{
			StorePath:          "./arc-amq-data",
			WorkerPoolSize:     10,
			MessageTimeout:     30 * time.Second,
			RetryAttempts:      3,
			RetryBackoff:       time.Second,
			MaxMessageSize:     10 * 1024 * 1024, // 10MB
			CompressionEnabled: true,
		},
		Monitoring: MonitoringConfig{
			Metrics: MetricsConfig{
				Enabled:   true,
				Host:      "0.0.0.0",
				Port:      9091,
				Path:      "/metrics",
				Namespace: "arc",
				Subsystem: "orchestrator",
			},
			Tracing: TracingConfig{
				Enabled:     false,
				ServiceName: "arc",
				SampleRate:  0.1,
				BatchTimeout: 5 * time.Second,
			},
			HealthChecks: HealthChecksConfig{
				Enabled:  true,
				Interval: 30 * time.Second,
				Timeout:  5 * time.Second,
			},
			Profiling: ProfilingConfig{
				Enabled:    false,
				Host:       "127.0.0.1",
				Port:       6060,
				CPUProfile: true,
				MemProfile: true,
			},
		},
		Logging: LoggingConfig{
			Level:      "info",
			Format:     "json",
			Output:     "stdout",
			MaxSize:    100,
			MaxBackups: 3,
			MaxAge:     7,
			Compress:   true,
		},
		Security: SecurityConfig{
			Authentication: AuthenticationConfig{
				Enabled: false,
				Type:    "jwt",
				JWTConfig: JWTConfig{
					ExpiryDuration: 24 * time.Hour,
					Algorithm:      "HS256",
				},
				Config: make(map[string]string),
			},
			Authorization: AuthorizationConfig{
				Enabled:     false,
				Type:        "rbac",
				Config:      make(map[string]string),
				Permissions: make(map[string][]string),
			},
			RateLimit: RateLimitConfig{
				Enabled:        false,
				GlobalLimit:    1000,
				GlobalWindow:   time.Minute,
				UserLimit:      100,
				UserWindow:     time.Minute,
				EndpointLimits: make(map[string]EndpointLimit),
			},
		},
		Features: FeatureConfig{
			EnableWorkflows:   true,
			EnableMetrics:     true,
			EnableTracing:     false,
			EnableProfiling:   false,
			EnableRateLimit:   false,
			EnableAuth:        false,
			EnableHealthCheck: true,
		},
	}
}

// LoadConfig loads configuration from file and environment variables
func LoadConfig(configPath string) (*Config, error) {
	config := DefaultConfig()

	// Load from file if provided
	if configPath != "" {
		if err := loadConfigFromFile(config, configPath); err != nil {
			return nil, fmt.Errorf("failed to load config from file: %w", err)
		}
	}

	// Override with environment variables
	loadConfigFromEnv(config)

	// Validate configuration
	if err := config.Validate(); err != nil {
		return nil, fmt.Errorf("config validation failed: %w", err)
	}

	return config, nil
}

// loadConfigFromFile loads configuration from YAML or JSON file
func loadConfigFromFile(config *Config, configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return err
	}

	ext := strings.ToLower(filepath.Ext(configPath))
	switch ext {
	case ".yaml", ".yml":
		return yaml.Unmarshal(data, config)
	case ".json":
		return json.Unmarshal(data, config)
	default:
		return fmt.Errorf("unsupported config file format: %s", ext)
	}
}

// loadConfigFromEnv loads configuration from environment variables
func loadConfigFromEnv(config *Config) {
	// Server configuration
	if val := os.Getenv("ARC_GRPC_HOST"); val != "" {
		config.Server.GRPC.Host = val
	}
	if val := os.Getenv("ARC_GRPC_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			config.Server.GRPC.Port = port
		}
	}
	if val := os.Getenv("ARC_HTTP_HOST"); val != "" {
		config.Server.HTTP.Host = val
	}
	if val := os.Getenv("ARC_HTTP_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			config.Server.HTTP.Port = port
		}
	}

	// TLS configuration
	if val := os.Getenv("ARC_TLS_ENABLED"); val != "" {
		config.Server.TLS.Enabled = strings.ToLower(val) == "true"
	}
	if val := os.Getenv("ARC_TLS_CERT_FILE"); val != "" {
		config.Server.TLS.CertFile = val
	}
	if val := os.Getenv("ARC_TLS_KEY_FILE"); val != "" {
		config.Server.TLS.KeyFile = val
	}

	// Runtime configuration
	if val := os.Getenv("ARC_RUNTIME_TYPE"); val != "" {
		config.Runtime.Type = val
	}
	if val := os.Getenv("ARC_RUNTIME_NAMESPACE"); val != "" {
		config.Runtime.Namespace = val
	}

	// State configuration
	if val := os.Getenv("ARC_STATE_TYPE"); val != "" {
		config.State.Type = val
	}
	if val := os.Getenv("ARC_STATE_CONNECTION_URL"); val != "" {
		config.State.ConnectionURL = val
	}

	// Message queue configuration
	if val := os.Getenv("ARC_AMQ_STORE_PATH"); val != "" {
		config.MessageQueue.StorePath = val
	}
	if val := os.Getenv("ARC_AMQ_WORKER_POOL_SIZE"); val != "" {
		if size, err := strconv.Atoi(val); err == nil {
			config.MessageQueue.WorkerPoolSize = size
		}
	}

	// Monitoring configuration
	if val := os.Getenv("ARC_METRICS_ENABLED"); val != "" {
		config.Monitoring.Metrics.Enabled = strings.ToLower(val) == "true"
	}
	if val := os.Getenv("ARC_METRICS_PORT"); val != "" {
		if port, err := strconv.Atoi(val); err == nil {
			config.Monitoring.Metrics.Port = port
		}
	}
	if val := os.Getenv("ARC_TRACING_ENABLED"); val != "" {
		config.Monitoring.Tracing.Enabled = strings.ToLower(val) == "true"
	}
	if val := os.Getenv("ARC_TRACING_ENDPOINT"); val != "" {
		config.Monitoring.Tracing.Endpoint = val
	}

	// Logging configuration
	if val := os.Getenv("ARC_LOG_LEVEL"); val != "" {
		config.Logging.Level = val
	}
	if val := os.Getenv("ARC_LOG_FORMAT"); val != "" {
		config.Logging.Format = val
	}

	// Security configuration
	if val := os.Getenv("ARC_AUTH_ENABLED"); val != "" {
		config.Security.Authentication.Enabled = strings.ToLower(val) == "true"
	}
	if val := os.Getenv("ARC_JWT_SECRET_KEY"); val != "" {
		config.Security.Authentication.JWTConfig.SecretKey = val
	}
	if val := os.Getenv("ARC_RATE_LIMIT_ENABLED"); val != "" {
		config.Security.RateLimit.Enabled = strings.ToLower(val) == "true"
	}
}

// Validate validates the configuration
func (c *Config) Validate() error {
	// Validate server configuration
	if c.Server.GRPC.Port <= 0 || c.Server.GRPC.Port > 65535 {
		return fmt.Errorf("invalid gRPC port: %d", c.Server.GRPC.Port)
	}
	if c.Server.HTTP.Port <= 0 || c.Server.HTTP.Port > 65535 {
		return fmt.Errorf("invalid HTTP port: %d", c.Server.HTTP.Port)
	}

	// Validate TLS configuration
	if c.Server.TLS.Enabled {
		if c.Server.TLS.CertFile == "" {
			return fmt.Errorf("TLS cert file must be specified when TLS is enabled")
		}
		if c.Server.TLS.KeyFile == "" {
			return fmt.Errorf("TLS key file must be specified when TLS is enabled")
		}
		if _, err := os.Stat(c.Server.TLS.CertFile); os.IsNotExist(err) {
			return fmt.Errorf("TLS cert file does not exist: %s", c.Server.TLS.CertFile)
		}
		if _, err := os.Stat(c.Server.TLS.KeyFile); os.IsNotExist(err) {
			return fmt.Errorf("TLS key file does not exist: %s", c.Server.TLS.KeyFile)
		}
	}

	// Validate runtime configuration
	validRuntimes := []string{"docker", "kubernetes", "gvisor"}
	if !contains(validRuntimes, c.Runtime.Type) {
		return fmt.Errorf("invalid runtime type: %s, must be one of %v", c.Runtime.Type, validRuntimes)
	}

	// Validate state configuration
	validStates := []string{"memory", "badger", "postgresql"}
	if !contains(validStates, c.State.Type) {
		return fmt.Errorf("invalid state type: %s, must be one of %v", c.State.Type, validStates)
	}

	// Validate logging configuration
	validLogLevels := []string{"debug", "info", "warn", "error"}
	if !contains(validLogLevels, strings.ToLower(c.Logging.Level)) {
		return fmt.Errorf("invalid log level: %s, must be one of %v", c.Logging.Level, validLogLevels)
	}

	validLogFormats := []string{"json", "text"}
	if !contains(validLogFormats, strings.ToLower(c.Logging.Format)) {
		return fmt.Errorf("invalid log format: %s, must be one of %v", c.Logging.Format, validLogFormats)
	}

	return nil
}

// String returns a string representation of the configuration
func (c *Config) String() string {
	data, _ := yaml.Marshal(c)
	return string(data)
}

// SaveToFile saves the configuration to a file
func (c *Config) SaveToFile(path string) error {
	ext := strings.ToLower(filepath.Ext(path))
	var data []byte
	var err error

	switch ext {
	case ".yaml", ".yml":
		data, err = yaml.Marshal(c)
	case ".json":
		data, err = json.MarshalIndent(c, "", "  ")
	default:
		return fmt.Errorf("unsupported config file format: %s", ext)
	}

	if err != nil {
		return err
	}

	return os.WriteFile(path, data, 0644)
}

// contains checks if a slice contains a string
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}