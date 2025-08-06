// Package server provides middleware integration for the ARC server
package server

import (
	"context"
	"net/http"
	"time"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"

	"github.com/rizome-dev/arc/pkg/config"
	"github.com/rizome-dev/arc/pkg/logging"
	"github.com/rizome-dev/arc/pkg/middleware"
	"github.com/rizome-dev/arc/pkg/monitoring"
)

// MiddlewareManager manages all middleware for the server
type MiddlewareManager struct {
	config      *config.Config
	monitor     *monitoring.Monitor
	logger      *logging.Logger
	authService *middleware.AuthService
	rateLimiter *middleware.RateLimiter
}

// NewMiddlewareManager creates a new middleware manager
func NewMiddlewareManager(config *config.Config, monitor *monitoring.Monitor) *MiddlewareManager {
	logger := logging.GetLogger().WithComponent("middleware")

	mm := &MiddlewareManager{
		config:  config,
		monitor: monitor,
		logger:  logger,
	}

	// Initialize authentication service
	if config.Security.Authentication.Enabled {
		mm.authService = middleware.NewAuthService(&config.Security)
	}

	// Initialize rate limiter
	if config.Security.RateLimit.Enabled {
		mm.rateLimiter = middleware.NewRateLimiter(&config.Security.RateLimit)
	}

	return mm
}

// Stop stops all middleware services
func (mm *MiddlewareManager) Stop() {
	if mm.rateLimiter != nil {
		mm.rateLimiter.Stop()
	}
}

// HTTPMiddlewareChain returns the complete HTTP middleware chain
func (mm *MiddlewareManager) HTTPMiddlewareChain() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		// Apply middleware in order (last applied runs first)
		handler := next

		// Metrics middleware (innermost)
		if mm.monitor != nil && mm.config.Features.EnableMetrics {
			handler = mm.HTTPMetricsMiddleware(handler)
		}

		// Authentication middleware
		if mm.authService != nil && mm.config.Security.Authentication.Enabled {
			handler = mm.authService.HTTPAuthMiddleware(handler)
		}

		// Rate limiting middleware
		if mm.rateLimiter != nil && mm.config.Security.RateLimit.Enabled {
			handler = mm.rateLimiter.HTTPRateLimitMiddleware(handler)
		}

		// CORS middleware
		if mm.config.Server.HTTP.CORSEnabled {
			handler = mm.CORSMiddleware(handler)
		}

		// Request ID middleware
		handler = mm.RequestIDMiddleware(handler)

		// Logging middleware (outermost)
		handler = mm.HTTPLoggingMiddleware(handler)

		return handler
	}
}

// GRPCUnaryInterceptorChain returns the complete gRPC unary interceptor chain
func (mm *MiddlewareManager) GRPCUnaryInterceptorChain() grpc.UnaryServerInterceptor {
	interceptors := []grpc.UnaryServerInterceptor{}

	// Logging interceptor (first)
	interceptors = append(interceptors, mm.GRPCLoggingInterceptor)

	// Request ID interceptor
	interceptors = append(interceptors, mm.GRPCRequestIDInterceptor)

	// Rate limiting interceptor
	if mm.rateLimiter != nil && mm.config.Security.RateLimit.Enabled {
		interceptors = append(interceptors, mm.rateLimiter.GRPCRateLimitInterceptor)
	}

	// Authentication interceptor
	if mm.authService != nil && mm.config.Security.Authentication.Enabled {
		interceptors = append(interceptors, mm.authService.GRPCAuthInterceptor)
	}

	// Metrics interceptor (last)
	if mm.monitor != nil && mm.config.Features.EnableMetrics {
		interceptors = append(interceptors, mm.GRPCMetricsInterceptor)
	}

	// Chain all interceptors
	return chainUnaryInterceptors(interceptors...)
}

// HTTP Middleware implementations

// RequestIDMiddleware adds a unique request ID to each request
func (mm *MiddlewareManager) RequestIDMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requestID := r.Header.Get("X-Request-ID")
		if requestID == "" {
			requestID = uuid.New().String()
		}

		// Add to response headers
		w.Header().Set("X-Request-ID", requestID)

		// Add to context
		ctx := context.WithValue(r.Context(), "request_id", requestID)
		r = r.WithContext(ctx)

		next.ServeHTTP(w, r)
	})
}

// HTTPLoggingMiddleware logs HTTP requests
func (mm *MiddlewareManager) HTTPLoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Create a response writer that captures status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Log request
		logger := mm.logger.WithContext(r.Context()).WithFields(map[string]interface{}{
			"method":     r.Method,
			"path":       r.URL.Path,
			"query":      r.URL.RawQuery,
			"remote_ip":  getClientIP(r),
			"user_agent": r.UserAgent(),
		})

		logger.Info("HTTP request started")

		// Process request
		next.ServeHTTP(rw, r)

		// Log response
		duration := time.Since(start)
		logger.WithFields(map[string]interface{}{
			"status_code":     rw.statusCode,
			"response_time_ms": duration.Milliseconds(),
			"response_size":   rw.size,
		}).Info("HTTP request completed")
	})
}

// HTTPMetricsMiddleware records HTTP metrics
func (mm *MiddlewareManager) HTTPMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		// Increment in-flight requests
		mm.monitor.GetMetrics().HTTPRequestsInFlight.Inc()
		defer mm.monitor.GetMetrics().HTTPRequestsInFlight.Dec()

		// Create a response writer that captures status code
		rw := &responseWriter{ResponseWriter: w, statusCode: http.StatusOK}

		// Process request
		next.ServeHTTP(rw, r)

		// Record metrics
		duration := time.Since(start)
		mm.monitor.GetMetrics().HTTPRequestsTotal.Inc()
		mm.monitor.GetMetrics().HTTPRequestDuration.Observe(duration.Seconds())
	})
}

// CORSMiddleware handles CORS headers
func (mm *MiddlewareManager) CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		
		// Check if origin is allowed
		if mm.isOriginAllowed(origin) {
			w.Header().Set("Access-Control-Allow-Origin", origin)
		} else if len(mm.config.Server.HTTP.CORSAllowedOrigins) > 0 && 
			mm.config.Server.HTTP.CORSAllowedOrigins[0] == "*" {
			w.Header().Set("Access-Control-Allow-Origin", "*")
		}

		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Request-ID")
		w.Header().Set("Access-Control-Expose-Headers", "X-Request-ID")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Handle preflight requests
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// gRPC Interceptor implementations

// GRPCRequestIDInterceptor adds a request ID to gRPC requests
func (mm *MiddlewareManager) GRPCRequestIDInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	requestID := uuid.New().String()
	ctx = context.WithValue(ctx, "request_id", requestID)
	return handler(ctx, req)
}

// GRPCLoggingInterceptor logs gRPC requests
func (mm *MiddlewareManager) GRPCLoggingInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()

	logger := mm.logger.WithContext(ctx).WithFields(map[string]interface{}{
		"method": info.FullMethod,
	})

	logger.Info("gRPC request started")

	// Process request
	resp, err := handler(ctx, req)

	// Log response
	duration := time.Since(start)
	logFields := map[string]interface{}{
		"response_time_ms": duration.Milliseconds(),
	}

	if err != nil {
		if s, ok := status.FromError(err); ok {
			logFields["grpc_code"] = s.Code().String()
			logFields["error"] = s.Message()
		}
		logger.WithFields(logFields).Error("gRPC request failed")
	} else {
		logger.WithFields(logFields).Info("gRPC request completed")
	}

	return resp, err
}

// GRPCMetricsInterceptor records gRPC metrics
func (mm *MiddlewareManager) GRPCMetricsInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	start := time.Now()

	// Increment in-flight requests
	mm.monitor.GetMetrics().GRPCRequestsInFlight.Inc()
	defer mm.monitor.GetMetrics().GRPCRequestsInFlight.Dec()

	// Process request
	resp, err := handler(ctx, req)

	// Record metrics
	duration := time.Since(start)
	mm.monitor.GetMetrics().GRPCRequestsTotal.Inc()
	mm.monitor.GetMetrics().GRPCRequestDuration.Observe(duration.Seconds())

	return resp, err
}

// Helper types and functions

// responseWriter wraps http.ResponseWriter to capture response details
type responseWriter struct {
	http.ResponseWriter
	statusCode int
	size       int
}

func (rw *responseWriter) WriteHeader(statusCode int) {
	rw.statusCode = statusCode
	rw.ResponseWriter.WriteHeader(statusCode)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	size, err := rw.ResponseWriter.Write(b)
	rw.size += size
	return size, err
}

// getClientIP extracts the client IP from the request
func getClientIP(r *http.Request) string {
	// Check X-Forwarded-For header
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		return xff
	}
	// Check X-Real-IP header
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}
	// Fall back to RemoteAddr
	return r.RemoteAddr
}

// isOriginAllowed checks if an origin is allowed for CORS
func (mm *MiddlewareManager) isOriginAllowed(origin string) bool {
	for _, allowedOrigin := range mm.config.Server.HTTP.CORSAllowedOrigins {
		if allowedOrigin == origin {
			return true
		}
	}
	return false
}

// chainUnaryInterceptors chains multiple unary interceptors into one
func chainUnaryInterceptors(interceptors ...grpc.UnaryServerInterceptor) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		// Build chain from last to first
		for i := len(interceptors) - 1; i >= 0; i-- {
			interceptor := interceptors[i]
			currentHandler := handler
			handler = func(currentCtx context.Context, currentReq interface{}) (interface{}, error) {
				return interceptor(currentCtx, currentReq, info, currentHandler)
			}
		}
		return handler(ctx, req)
	}
}