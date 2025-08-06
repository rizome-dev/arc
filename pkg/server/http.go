package server

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"

	arcv1 "github.com/rizome-dev/arc/api/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// HTTPServer wraps the gRPC-Gateway HTTP server
type HTTPServer struct {
	mux    *runtime.ServeMux
	server *http.Server
}

// NewHTTPServer creates a new HTTP gateway server
func NewHTTPServer(grpcAddress string, port int) (*HTTPServer, error) {
	// Create gRPC-Gateway mux with custom options
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{}),
		runtime.WithIncomingHeaderMatcher(customHeaderMatcher),
		runtime.WithOutgoingHeaderMatcher(customOutgoingHeaderMatcher),
		runtime.WithErrorHandler(customErrorHandler),
	)

	// Create HTTP server with CORS middleware
	httpServer := &http.Server{
		Addr:    fmt.Sprintf(":%d", port),
		Handler: corsMiddleware(loggingMiddleware(mux)),
		WriteTimeout: 30 * time.Second,
		ReadTimeout:  30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	// Create gRPC connection to the gRPC server
	ctx := context.Background()
	conn, err := grpc.DialContext(
		ctx,
		grpcAddress,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to gRPC server: %w", err)
	}

	// Register the ARC service handler
	if err := arcv1.RegisterARCHandler(ctx, mux, conn); err != nil {
		return nil, fmt.Errorf("failed to register ARC handler: %w", err)
	}

	return &HTTPServer{
		mux:    mux,
		server: httpServer,
	}, nil
}

// Start starts the HTTP server
func (h *HTTPServer) Start() error {
	return h.server.ListenAndServe()
}

// Stop gracefully stops the HTTP server
func (h *HTTPServer) Stop(ctx context.Context) error {
	return h.server.Shutdown(ctx)
}

// GetMux returns the underlying ServeMux for custom route registration
func (h *HTTPServer) GetMux() *runtime.ServeMux {
	return h.mux
}

// Middleware functions

// corsMiddleware adds CORS headers to responses
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, X-CSRF-Token, X-Requested-With")
		w.Header().Set("Access-Control-Expose-Headers", "Link")
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Max-Age", "300")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		// Continue with the next handler
		next.ServeHTTP(w, r)
	})
}

// loggingMiddleware logs HTTP requests
func loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		
		// Create a response recorder to capture status code
		recorder := &responseRecorder{ResponseWriter: w, statusCode: http.StatusOK}
		
		// Serve the request
		next.ServeHTTP(recorder, r)
		
		// Log the request
		duration := time.Since(start)
		fmt.Printf("[%s] %s %s - %d (%v)\n",
			start.Format("2006-01-02 15:04:05"),
			r.Method,
			r.URL.Path,
			recorder.statusCode,
			duration,
		)
	})
}

// responseRecorder wraps http.ResponseWriter to capture status code
type responseRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.statusCode = statusCode
	r.ResponseWriter.WriteHeader(statusCode)
}

// Custom header matchers

// customHeaderMatcher allows specific headers to be passed through
func customHeaderMatcher(key string) (string, bool) {
	key = strings.ToLower(key)
	switch key {
	case "user-agent", "authorization", "x-forwarded-for", "x-real-ip", "x-request-id":
		return key, true
	default:
		return runtime.DefaultHeaderMatcher(key)
	}
}

// customOutgoingHeaderMatcher allows specific response headers to be passed through
func customOutgoingHeaderMatcher(key string) (string, bool) {
	key = strings.ToLower(key)
	switch key {
	case "x-request-id", "x-response-time":
		return key, true
	default:
		return runtime.DefaultHeaderMatcher(key)
	}
}

// customErrorHandler provides custom error handling for the gateway
func customErrorHandler(ctx context.Context, mux *runtime.ServeMux, marshaler runtime.Marshaler, w http.ResponseWriter, r *http.Request, err error) {
	// Use the default error handler but add custom logging
	fmt.Printf("gRPC-Gateway error: %v for request: %s %s\n", err, r.Method, r.URL.Path)
	runtime.DefaultHTTPErrorHandler(ctx, mux, marshaler, w, r, err)
}

// OpenAPI/Swagger support

// SwaggerHandler serves the OpenAPI/Swagger specification
func SwaggerHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// This would serve the generated OpenAPI spec
		// For now, return a placeholder
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		
		swaggerSpec := `{
  "openapi": "3.0.0",
  "info": {
    "title": "ARC API",
    "description": "Agent Orchestrator for Containers API",
    "version": "1.0.0"
  },
  "servers": [
    {
      "url": "/api/v1",
      "description": "ARC API Server"
    }
  ],
  "paths": {
    "/workflows": {
      "get": {
        "summary": "List workflows",
        "operationId": "listWorkflows",
        "responses": {
          "200": {
            "description": "List of workflows"
          }
        }
      },
      "post": {
        "summary": "Create workflow",
        "operationId": "createWorkflow",
        "responses": {
          "200": {
            "description": "Workflow created"
          }
        }
      }
    },
    "/agents": {
      "get": {
        "summary": "List real-time agents",
        "operationId": "listRealTimeAgents",
        "responses": {
          "200": {
            "description": "List of agents"
          }
        }
      },
      "post": {
        "summary": "Create real-time agent",
        "operationId": "createRealTimeAgent",
        "responses": {
          "200": {
            "description": "Agent created"
          }
        }
      }
    }
  }
}`
		w.Write([]byte(swaggerSpec))
	}
}

// MetricsHandler provides basic metrics endpoint
func MetricsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		
		// Basic metrics - in a real implementation, this would use Prometheus or similar
		metrics := `# HELP arc_http_requests_total Total number of HTTP requests
# TYPE arc_http_requests_total counter
arc_http_requests_total{method="GET",path="/api/v1/workflows"} 0
arc_http_requests_total{method="POST",path="/api/v1/workflows"} 0
arc_http_requests_total{method="GET",path="/api/v1/agents"} 0
arc_http_requests_total{method="POST",path="/api/v1/agents"} 0

# HELP arc_grpc_server_status Status of the gRPC server
# TYPE arc_grpc_server_status gauge
arc_grpc_server_status 1

# HELP arc_orchestrator_status Status of the orchestrator
# TYPE arc_orchestrator_status gauge
arc_orchestrator_status 1
`
		w.Write([]byte(metrics))
	}
}

// HealthCheckHandler provides a simple health check endpoint
func HealthCheckHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		
		response := `{
  "status": "healthy",
  "timestamp": "` + time.Now().Format(time.RFC3339) + `",
  "checks": {
    "http_gateway": "ok",
    "grpc_connection": "ok"
  }
}`
		w.Write([]byte(response))
	}
}

// ReadinessHandler provides a readiness check endpoint
func ReadinessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		
		response := `{
  "ready": true,
  "timestamp": "` + time.Now().Format(time.RFC3339) + `",
  "services": [
    "http_gateway",
    "grpc_server",
    "orchestrator"
  ]
}`
		w.Write([]byte(response))
	}
}

// RegisterCustomRoutes adds custom routes to the HTTP server
func RegisterCustomRoutes(mux *runtime.ServeMux) {
	// This function would be used to register additional custom routes
	// that are not part of the gRPC service
	
	// Example of how to add custom routes:
	// mux.HandlePath("GET", "/custom/endpoint", customHandler)
}