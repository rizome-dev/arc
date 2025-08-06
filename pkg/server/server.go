// Package server provides the main server implementation
package server

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	arcv1 "github.com/rizome-dev/arc/api/v1"
	"github.com/rizome-dev/arc/pkg/config"
	"github.com/rizome-dev/arc/pkg/logging"
	"github.com/rizome-dev/arc/pkg/monitoring"
	"github.com/rizome-dev/arc/pkg/orchestrator"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/reflection"
)

// Config holds server configuration
type Config struct {
	// gRPC server configuration
	GRPCPort int
	GRPCHost string
	
	// HTTP server configuration
	HTTPPort int
	HTTPHost string
	
	// TLS configuration
	TLSEnabled  bool
	TLSCertFile string
	TLSKeyFile  string
	
	// Server options
	MaxRecvMsgSize    int
	MaxSendMsgSize    int
	ConnectionTimeout time.Duration
	
	// Health check configuration
	HealthCheckInterval time.Duration
	
	// Graceful shutdown timeout
	ShutdownTimeout time.Duration

	// New production features
	MaxConnections    int
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ReflectionEnabled bool
}

// DefaultConfig returns a default server configuration
func DefaultConfig() *Config {
	return &Config{
		GRPCPort:            9090,
		GRPCHost:            "0.0.0.0",
		HTTPPort:            8080,
		HTTPHost:            "0.0.0.0",
		TLSEnabled:          false,
		MaxRecvMsgSize:      4 * 1024 * 1024, // 4MB
		MaxSendMsgSize:      4 * 1024 * 1024, // 4MB
		ConnectionTimeout:   30 * time.Second,
		HealthCheckInterval: 10 * time.Second,
		ShutdownTimeout:     30 * time.Second,
		MaxConnections:      1000,
		ReadTimeout:         30 * time.Second,
		WriteTimeout:        30 * time.Second,
		IdleTimeout:         120 * time.Second,
		ReflectionEnabled:   true,
	}
}

// Server manages both gRPC and HTTP servers
type Server struct {
	config            *Config
	orchestrator      *orchestrator.Orchestrator
	grpcServer        *grpc.Server
	httpServer        *HTTPServer
	healthServer      *health.Server
	monitor           *monitoring.Monitor
	logger            *logging.Logger
	middlewareManager *MiddlewareManager
	
	// Server state
	running      bool
	mu           sync.RWMutex
	shutdownChan chan struct{}
}

// NewServer creates a new server instance
func NewServer(config *Config, orch *orchestrator.Orchestrator) (*Server, error) {
	return NewServerWithMonitoring(config, orch, nil, nil)
}

// NewServerWithMonitoring creates a new server instance with monitoring and full config
func NewServerWithMonitoring(config *Config, orch *orchestrator.Orchestrator, monitor *monitoring.Monitor, appConfig *config.Config) (*Server, error) {
	if config == nil {
		config = DefaultConfig()
	}
	
	if orch == nil {
		return nil, fmt.Errorf("orchestrator is required")
	}
	
	server := &Server{
		config:       config,
		orchestrator: orch,
		monitor:      monitor,
		shutdownChan: make(chan struct{}),
		logger:       logging.GetLogger().WithComponent("server"),
	}
	
	// Initialize health server
	server.healthServer = health.NewServer()
	
	// Initialize middleware manager if we have app config
	if appConfig != nil {
		server.middlewareManager = NewMiddlewareManager(appConfig, monitor)
	}
	
	return server, nil
}

// Start starts both gRPC and HTTP servers
func (s *Server) Start() error {
	s.mu.Lock()
	if s.running {
		s.mu.Unlock()
		return fmt.Errorf("server is already running")
	}
	s.running = true
	s.mu.Unlock()
	
	s.logger.Info("Starting ARC server...")
	
	// Start gRPC server
	if err := s.startGRPCServer(); err != nil {
		return fmt.Errorf("failed to start gRPC server: %w", err)
	}
	
	// Start HTTP server
	if err := s.startHTTPServer(); err != nil {
		return fmt.Errorf("failed to start HTTP server: %w", err)
	}
	
	// Start health monitoring
	go s.startHealthMonitoring()
	
	s.logger.WithFields(map[string]interface{}{
		"grpc_address": fmt.Sprintf("%s:%d", s.config.GRPCHost, s.config.GRPCPort),
		"http_address": fmt.Sprintf("%s:%d", s.config.HTTPHost, s.config.HTTPPort),
	}).Info("ARC server started successfully")
	
	return nil
}

// Stop gracefully shuts down both servers
func (s *Server) Stop(ctx context.Context) error {
	s.mu.Lock()
	if !s.running {
		s.mu.Unlock()
		return nil
	}
	s.running = false
	s.mu.Unlock()
	
	s.logger.Info("Shutting down ARC server...")
	
	// Stop middleware manager
	if s.middlewareManager != nil {
		s.middlewareManager.Stop()
	}
	
	// Create shutdown context with timeout
	shutdownCtx, cancel := context.WithTimeout(ctx, s.config.ShutdownTimeout)
	defer cancel()
	
	// Signal shutdown
	close(s.shutdownChan)
	
	var wg sync.WaitGroup
	errChan := make(chan error, 2)
	
	// Stop gRPC server
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.stopGRPCServer()
	}()
	
	// Stop HTTP server
	wg.Add(1)
	go func() {
		defer wg.Done()
		if err := s.httpServer.Stop(shutdownCtx); err != nil {
			errChan <- fmt.Errorf("HTTP server shutdown error: %w", err)
		}
	}()
	
	// Wait for shutdown or timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()
	
	select {
	case <-done:
		s.logger.Info("ARC server shut down successfully")
	case <-shutdownCtx.Done():
		s.logger.Warn("Server shutdown timeout exceeded")
	}
	
	// Check for errors
	close(errChan)
	for err := range errChan {
		s.logger.WithError(err).Error("Shutdown error")
	}
	
	return nil
}

// WaitForShutdown blocks until shutdown signal is received
func (s *Server) WaitForShutdown() {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	
	select {
	case sig := <-sigChan:
		s.logger.WithField("signal", sig).Info("Received shutdown signal")
	case <-s.shutdownChan:
		s.logger.Info("Received internal shutdown signal")
	}
}

// IsRunning returns whether the server is currently running
func (s *Server) IsRunning() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.running
}

// GetGRPCAddress returns the gRPC server address
func (s *Server) GetGRPCAddress() string {
	return fmt.Sprintf("%s:%d", s.config.GRPCHost, s.config.GRPCPort)
}

// GetHTTPAddress returns the HTTP server address
func (s *Server) GetHTTPAddress() string {
	return fmt.Sprintf("%s:%d", s.config.HTTPHost, s.config.HTTPPort)
}

// Private methods

func (s *Server) startGRPCServer() error {
	// Create listener
	address := fmt.Sprintf("%s:%d", s.config.GRPCHost, s.config.GRPCPort)
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", address, err)
	}
	
	// Configure gRPC server options
	opts := []grpc.ServerOption{
		grpc.MaxRecvMsgSize(s.config.MaxRecvMsgSize),
		grpc.MaxSendMsgSize(s.config.MaxSendMsgSize),
		grpc.ConnectionTimeout(s.config.ConnectionTimeout),
	}

	// Add middleware interceptors
	if s.middlewareManager != nil {
		opts = append(opts, grpc.UnaryInterceptor(s.middlewareManager.GRPCUnaryInterceptorChain()))
	}
	
	// Add TLS if enabled
	if s.config.TLSEnabled {
		creds, err := s.loadTLSCredentials()
		if err != nil {
			return fmt.Errorf("failed to load TLS credentials: %w", err)
		}
		opts = append(opts, grpc.Creds(creds))
	}
	
	// Create gRPC server
	s.grpcServer = grpc.NewServer(opts...)
	
	// Register services
	arcGRPCServer := NewGRPCServer(s.orchestrator)
	arcv1.RegisterARCServer(s.grpcServer, arcGRPCServer)
	
	// Register health service
	grpc_health_v1.RegisterHealthServer(s.grpcServer, s.healthServer)
	
	// Enable reflection if configured
	if s.config.ReflectionEnabled {
		reflection.Register(s.grpcServer)
	}
	
	// Start server in goroutine
	go func() {
		s.logger.WithField("address", address).Info("gRPC server starting")
		if err := s.grpcServer.Serve(listener); err != nil {
			s.logger.WithError(err).Error("gRPC server error")
		}
	}()
	
	return nil
}

func (s *Server) startHTTPServer() error {
	grpcAddress := fmt.Sprintf("localhost:%d", s.config.GRPCPort)
	
	// Create HTTP server
	httpServer, err := NewHTTPServerWithMiddleware(grpcAddress, s.config.HTTPPort, s.middlewareManager, s.monitor)
	if err != nil {
		return err
	}
	
	s.httpServer = httpServer
	
	// Start HTTP server in goroutine
	go func() {
		s.logger.WithFields(map[string]interface{}{
			"host": s.config.HTTPHost,
			"port": s.config.HTTPPort,
		}).Info("HTTP server starting")
		if err := s.httpServer.Start(); err != nil && err != http.ErrServerClosed {
			s.logger.WithError(err).Error("HTTP server error")
		}
	}()
	
	return nil
}

func (s *Server) stopGRPCServer() {
	if s.grpcServer == nil {
		return
	}
	
	s.logger.Info("Stopping gRPC server...")
	
	// Graceful stop with timeout
	done := make(chan struct{})
	go func() {
		s.grpcServer.GracefulStop()
		close(done)
	}()
	
	// Force stop if timeout exceeded
	select {
	case <-done:
		s.logger.Info("gRPC server stopped gracefully")
	case <-time.After(15 * time.Second):
		s.logger.Warn("gRPC server graceful stop timeout, forcing stop")
		s.grpcServer.Stop()
	}
}

func (s *Server) startHealthMonitoring() {
	ticker := time.NewTicker(s.config.HealthCheckInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ticker.C:
			s.performHealthCheck()
		case <-s.shutdownChan:
			return
		}
	}
}

func (s *Server) performHealthCheck() {
	// Check orchestrator health
	// This is a basic implementation - could be extended
	s.healthServer.SetServingStatus("orchestrator", grpc_health_v1.HealthCheckResponse_SERVING)
	s.healthServer.SetServingStatus("grpc", grpc_health_v1.HealthCheckResponse_SERVING)
	s.healthServer.SetServingStatus("http", grpc_health_v1.HealthCheckResponse_SERVING)
}

func (s *Server) loadTLSCredentials() (credentials.TransportCredentials, error) {
	if s.config.TLSCertFile == "" || s.config.TLSKeyFile == "" {
		return nil, fmt.Errorf("TLS cert file and key file must be specified")
	}
	
	cert, err := tls.LoadX509KeyPair(s.config.TLSCertFile, s.config.TLSKeyFile)
	if err != nil {
		return nil, err
	}
	
	return credentials.NewTLS(&tls.Config{
		Certificates: []tls.Certificate{cert},
	}), nil
}


// RunServer is a convenience function to run the server with signal handling
func RunServer(config *Config, orch *orchestrator.Orchestrator) error {
	server, err := NewServer(config, orch)
	if err != nil {
		return fmt.Errorf("failed to create server: %w", err)
	}
	
	// Start server
	if err := server.Start(); err != nil {
		return fmt.Errorf("failed to start server: %w", err)
	}
	
	// Wait for shutdown signal
	server.WaitForShutdown()
	
	// Graceful shutdown
	ctx, cancel := context.WithTimeout(context.Background(), config.ShutdownTimeout)
	defer cancel()
	
	return server.Stop(ctx)
}