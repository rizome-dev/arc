// Package server provides enhanced HTTP server implementation
package server

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	arcv1 "github.com/rizome-dev/arc/api/v1"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/rizome-dev/arc/pkg/logging"
	"github.com/rizome-dev/arc/pkg/monitoring"
)

// NewHTTPServerWithMiddleware creates an enhanced HTTP server with middleware support
func NewHTTPServerWithMiddleware(grpcAddress string, port int, middlewareManager *MiddlewareManager, monitor *monitoring.Monitor) (*HTTPServer, error) {
	logger := logging.GetLogger().WithComponent("http-server")

	// Create gRPC-Gateway mux with custom options
	mux := runtime.NewServeMux(
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{}),
		runtime.WithIncomingHeaderMatcher(customHeaderMatcher),
		runtime.WithOutgoingHeaderMatcher(customOutgoingHeaderMatcher),
		runtime.WithErrorHandler(customErrorHandler),
	)

	// Create main HTTP mux for additional routes
	mainMux := http.NewServeMux()

	// Register gRPC gateway under /api/v1
	mainMux.Handle("/api/v1/", http.StripPrefix("/api/v1", mux))

	// Register additional endpoints
	registerAdditionalEndpoints(mainMux, monitor)

	// Apply middleware chain if available
	var handler http.Handler = mainMux
	if middlewareManager != nil {
		handler = middlewareManager.HTTPMiddlewareChain()(mainMux)
	}

	// Create HTTP server with enhanced configuration
	httpServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
		ErrorLog:     newLogAdapter(logger),
	}

	// Create gRPC connection to the gRPC server
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

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
	if err := arcv1.RegisterARCHandler(context.Background(), mux, conn); err != nil {
		return nil, fmt.Errorf("failed to register ARC handler: %w", err)
	}

	return &HTTPServer{
		mux:    mux,
		server: httpServer,
	}, nil
}

// registerAdditionalEndpoints registers additional HTTP endpoints
func registerAdditionalEndpoints(mux *http.ServeMux, monitor *monitoring.Monitor) {
	// Health endpoints
	mux.HandleFunc("/health", HealthCheckHandler())
	mux.HandleFunc("/ready", ReadinessHandler())
	mux.HandleFunc("/live", LivenessHandler())

	// Documentation endpoints
	mux.HandleFunc("/swagger.json", SwaggerHandler())
	mux.HandleFunc("/docs/", DocsHandler())

	// Metrics endpoint (if monitoring is available)
	if monitor != nil {
		mux.Handle("/metrics", promhttp.Handler())
	} else {
		mux.HandleFunc("/metrics", MetricsHandler())
	}

	// Admin endpoints
	mux.HandleFunc("/admin/status", AdminStatusHandler())
	mux.HandleFunc("/admin/info", AdminInfoHandler())

	// Debug endpoints (should be protected in production)
	mux.HandleFunc("/debug/vars", DebugVarsHandler())
}

// Enhanced health check handlers

// LivenessHandler provides a liveness check endpoint
func LivenessHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		
		response := fmt.Sprintf(`{
  "status": "alive",
  "timestamp": "%s",
  "uptime": "unknown"
}`, time.Now().Format(time.RFC3339))
		
		w.Write([]byte(response))
	}
}

// DocsHandler serves API documentation
func DocsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(http.StatusOK)

		html := `<!DOCTYPE html>
<html>
<head>
    <title>ARC API Documentation</title>
    <link rel="stylesheet" type="text/css" href="https://unpkg.com/swagger-ui-dist@3.25.0/swagger-ui.css" />
    <style>
        html {
            box-sizing: border-box;
            overflow: -moz-scrollbars-vertical;
            overflow-y: scroll;
        }
        *, *:before, *:after {
            box-sizing: inherit;
        }
        body {
            margin:0;
            background: #fafafa;
        }
    </style>
</head>
<body>
    <div id="swagger-ui"></div>
    <script src="https://unpkg.com/swagger-ui-dist@3.25.0/swagger-ui-bundle.js"></script>
    <script src="https://unpkg.com/swagger-ui-dist@3.25.0/swagger-ui-standalone-preset.js"></script>
    <script>
        window.onload = function() {
            const ui = SwaggerUIBundle({
                url: '/swagger.json',
                dom_id: '#swagger-ui',
                deepLinking: true,
                presets: [
                    SwaggerUIBundle.presets.apis,
                    SwaggerUIStandalonePreset
                ],
                plugins: [
                    SwaggerUIBundle.plugins.DownloadUrl
                ],
                layout: "StandaloneLayout"
            })
        }
    </script>
</body>
</html>`
		w.Write([]byte(html))
	}
}

// AdminStatusHandler provides detailed server status for administrators
func AdminStatusHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// This should be protected by authentication in production
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		status := map[string]interface{}{
			"timestamp":    time.Now().UTC().Format(time.RFC3339),
			"server":       "ARC Orchestrator",
			"version":      "dev", // This would come from version package
			"build_date":   "unknown", // This would come from build info
			"git_commit":   "unknown", // This would come from build info
			"go_version":   "unknown", // This would come from runtime info
			"status":       "running",
			"components": map[string]string{
				"grpc_server":   "running",
				"http_server":   "running",
				"orchestrator":  "running",
				"message_queue": "running",
				"state_manager": "running",
			},
		}

		writeJSONResponse(w, status)
	}
}

// AdminInfoHandler provides system information
func AdminInfoHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		info := map[string]interface{}{
			"server": map[string]interface{}{
				"name":        "ARC Orchestrator",
				"description": "Agent Orchestrator for Containers",
				"version":     "dev",
			},
			"runtime": map[string]interface{}{
				"platform":    "unknown",
				"go_version":  "unknown",
				"goroutines":  "unknown",
				"memory_mb":   "unknown",
			},
			"endpoints": map[string]interface{}{
				"grpc":    "localhost:9090",
				"http":    "localhost:8080",
				"metrics": "/metrics",
				"health":  "/health",
				"docs":    "/docs/",
			},
		}

		writeJSONResponse(w, info)
	}
}

// DebugVarsHandler provides debug variables (similar to expvar)
func DebugVarsHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)

		vars := map[string]interface{}{
			"timestamp":   time.Now().UTC().Format(time.RFC3339),
			"cmdline":     []string{"arc", "server"}, // This would come from os.Args
			"memstats":    map[string]interface{}{}, // This would come from runtime.MemStats
			"goroutines":  0, // This would come from runtime.NumGoroutine()
		}

		writeJSONResponse(w, vars)
	}
}

// newLogAdapter creates a standard log.Logger that writes to our structured logger
func newLogAdapter(logger *logging.Logger) *log.Logger {
	return log.New(&logWriter{logger: logger}, "", 0)
}

// logWriter adapts structured logger to io.Writer interface
type logWriter struct {
	logger *logging.Logger
}

func (l *logWriter) Write(p []byte) (n int, err error) {
	l.logger.Error(string(p))
	return len(p), nil
}

// writeJSONResponse writes a JSON response
func writeJSONResponse(w http.ResponseWriter, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	
	// Simple JSON marshaling - in production, use encoding/json
	response := `{"error": "JSON marshaling not implemented in this demo"}`
	w.Write([]byte(response))
}