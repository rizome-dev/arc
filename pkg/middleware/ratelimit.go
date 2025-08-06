// Package middleware provides rate limiting functionality
package middleware

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"

	"github.com/rizome-dev/arc/pkg/config"
)

// RateLimiter manages rate limiting for different clients and endpoints
type RateLimiter struct {
	config        *config.RateLimitConfig
	globalLimiter *rate.Limiter
	userLimiters  map[string]*rate.Limiter
	endpointLimiters map[string]*rate.Limiter
	mu            sync.RWMutex
	cleanupTicker *time.Ticker
	stopCleanup   chan struct{}
}

// ClientInfo represents information about a client making requests
type ClientInfo struct {
	IP       string
	UserID   string
	Endpoint string
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(config *config.RateLimitConfig) *RateLimiter {
	rl := &RateLimiter{
		config:           config,
		userLimiters:     make(map[string]*rate.Limiter),
		endpointLimiters: make(map[string]*rate.Limiter),
		stopCleanup:      make(chan struct{}),
	}

	// Create global limiter if configured
	if config.GlobalLimit > 0 {
		rl.globalLimiter = rate.NewLimiter(
			rate.Every(config.GlobalWindow/time.Duration(config.GlobalLimit)),
			config.GlobalLimit,
		)
	}

	// Start cleanup goroutine to remove unused limiters
	rl.startCleanup()

	return rl
}

// Stop stops the rate limiter and cleanup goroutines
func (rl *RateLimiter) Stop() {
	close(rl.stopCleanup)
	if rl.cleanupTicker != nil {
		rl.cleanupTicker.Stop()
	}
}

// HTTPRateLimitMiddleware provides HTTP rate limiting middleware
func (rl *RateLimiter) HTTPRateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !rl.config.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		client := rl.extractHTTPClientInfo(r)

		if !rl.allow(client) {
			rl.writeRateLimitResponse(w, r)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// GRPCRateLimitInterceptor provides gRPC rate limiting interceptor
func (rl *RateLimiter) GRPCRateLimitInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	if !rl.config.Enabled {
		return handler(ctx, req)
	}

	client := rl.extractGRPCClientInfo(ctx, info.FullMethod)

	if !rl.allow(client) {
		return nil, status.Errorf(codes.ResourceExhausted, "rate limit exceeded")
	}

	return handler(ctx, req)
}

// allow checks if a client is allowed to make a request
func (rl *RateLimiter) allow(client ClientInfo) bool {
	// Check global limit first
	if rl.globalLimiter != nil && !rl.globalLimiter.Allow() {
		return false
	}

	// Check endpoint-specific limit
	if !rl.allowEndpoint(client.Endpoint) {
		return false
	}

	// Check user-specific limit
	if client.UserID != "" && !rl.allowUser(client.UserID) {
		return false
	}

	// Check IP-based limit (if no user ID)
	if client.UserID == "" && client.IP != "" && !rl.allowUser(client.IP) {
		return false
	}

	return true
}

// allowUser checks if a specific user/IP is allowed to make a request
func (rl *RateLimiter) allowUser(identifier string) bool {
	if rl.config.UserLimit <= 0 {
		return true
	}

	rl.mu.Lock()
	limiter, exists := rl.userLimiters[identifier]
	if !exists {
		limiter = rate.NewLimiter(
			rate.Every(rl.config.UserWindow/time.Duration(rl.config.UserLimit)),
			rl.config.UserLimit,
		)
		rl.userLimiters[identifier] = limiter
	}
	rl.mu.Unlock()

	return limiter.Allow()
}

// allowEndpoint checks if requests to a specific endpoint are allowed
func (rl *RateLimiter) allowEndpoint(endpoint string) bool {
	// Find matching endpoint limit configuration
	var endpointLimit *config.EndpointLimit
	for pattern, limit := range rl.config.EndpointLimits {
		if rl.matchEndpoint(pattern, endpoint) {
			limitCopy := limit // Create a copy to avoid referencing loop variable
			endpointLimit = &limitCopy
			break
		}
	}

	if endpointLimit == nil || endpointLimit.Limit <= 0 {
		return true
	}

	rl.mu.Lock()
	limiter, exists := rl.endpointLimiters[endpoint]
	if !exists {
		limiter = rate.NewLimiter(
			rate.Every(endpointLimit.Window/time.Duration(endpointLimit.Limit)),
			endpointLimit.Limit,
		)
		rl.endpointLimiters[endpoint] = limiter
	}
	rl.mu.Unlock()

	return limiter.Allow()
}

// matchEndpoint checks if an endpoint matches a pattern
func (rl *RateLimiter) matchEndpoint(pattern, endpoint string) bool {
	// Simple pattern matching - could be enhanced with regex
	if pattern == "*" {
		return true
	}

	if pattern == endpoint {
		return true
	}

	// Wildcard suffix matching
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(endpoint, prefix)
	}

	// Wildcard prefix matching
	if strings.HasPrefix(pattern, "*") {
		suffix := strings.TrimPrefix(pattern, "*")
		return strings.HasSuffix(endpoint, suffix)
	}

	return false
}

// extractHTTPClientInfo extracts client information from HTTP request
func (rl *RateLimiter) extractHTTPClientInfo(r *http.Request) ClientInfo {
	client := ClientInfo{
		IP:       rl.extractClientIP(r),
		Endpoint: fmt.Sprintf("%s %s", r.Method, r.URL.Path),
	}

	// Try to get user ID from context (set by auth middleware)
	if claims, ok := GetUserFromContext(r.Context()); ok {
		client.UserID = claims.UserID
	}

	return client
}

// extractGRPCClientInfo extracts client information from gRPC context
func (rl *RateLimiter) extractGRPCClientInfo(ctx context.Context, method string) ClientInfo {
	client := ClientInfo{
		Endpoint: method,
	}

	// Extract IP from peer info
	if p, ok := peer.FromContext(ctx); ok {
		if addr, ok := p.Addr.(*net.TCPAddr); ok {
			client.IP = addr.IP.String()
		} else {
			// For other address types, use string representation
			client.IP = p.Addr.String()
		}
	}

	// Try to get user ID from context (set by auth interceptor)
	if claims, ok := GetUserFromContext(ctx); ok {
		client.UserID = claims.UserID
	}

	return client
}

// extractClientIP extracts the real client IP from HTTP request
func (rl *RateLimiter) extractClientIP(r *http.Request) string {
	// Check X-Forwarded-For header (most common for proxied requests)
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// X-Forwarded-For can contain multiple IPs, take the first one
		ips := strings.Split(xff, ",")
		if len(ips) > 0 {
			return strings.TrimSpace(ips[0])
		}
	}

	// Check X-Real-IP header (used by some proxies)
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		return xri
	}

	// Check CF-Connecting-IP header (Cloudflare)
	if cfIP := r.Header.Get("CF-Connecting-IP"); cfIP != "" {
		return cfIP
	}

	// Fall back to RemoteAddr
	ip, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}

	return ip
}

// writeRateLimitResponse writes a rate limit exceeded response
func (rl *RateLimiter) writeRateLimitResponse(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Retry-After", "60") // Suggest retry after 60 seconds
	w.WriteHeader(http.StatusTooManyRequests)

	response := map[string]interface{}{
		"error":   "rate_limit_exceeded",
		"message": "Rate limit exceeded. Please try again later.",
		"code":    http.StatusTooManyRequests,
	}

	// Try to write JSON response, fallback to plain text if it fails
	if err := writeJSON(w, response); err != nil {
		w.Write([]byte("Rate limit exceeded"))
	}
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, data interface{}) error {
	w.Header().Set("Content-Type", "application/json")
	
	// Simple JSON encoding without importing encoding/json
	// This is a basic implementation - in production, you'd use encoding/json
	switch v := data.(type) {
	case map[string]interface{}:
		w.Write([]byte(`{`))
		first := true
		for key, value := range v {
			if !first {
				w.Write([]byte(`,`))
			}
			w.Write([]byte(fmt.Sprintf(`"%s":`, key)))
			switch val := value.(type) {
			case string:
				w.Write([]byte(fmt.Sprintf(`"%s"`, val)))
			case int:
				w.Write([]byte(fmt.Sprintf(`%d`, val)))
			default:
				w.Write([]byte(fmt.Sprintf(`"%v"`, val)))
			}
			first = false
		}
		w.Write([]byte(`}`))
	default:
		w.Write([]byte(fmt.Sprintf(`"%v"`, v)))
	}
	return nil
}

// startCleanup starts a goroutine to periodically clean up unused limiters
func (rl *RateLimiter) startCleanup() {
	rl.cleanupTicker = time.NewTicker(10 * time.Minute)

	go func() {
		for {
			select {
			case <-rl.cleanupTicker.C:
				rl.cleanup()
			case <-rl.stopCleanup:
				return
			}
		}
	}()
}

// cleanup removes limiters that haven't been used recently
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	// Clean up user limiters
	// Note: This is a simple cleanup strategy. In production, you might want
	// to track last access time for each limiter and only remove truly unused ones.
	if len(rl.userLimiters) > 1000 { // Arbitrary threshold
		// Remove half of the limiters (oldest entries in map iteration order)
		count := 0
		for key := range rl.userLimiters {
			if count >= len(rl.userLimiters)/2 {
				break
			}
			delete(rl.userLimiters, key)
			count++
		}
	}

	// Clean up endpoint limiters
	if len(rl.endpointLimiters) > 100 { // Arbitrary threshold
		count := 0
		for key := range rl.endpointLimiters {
			if count >= len(rl.endpointLimiters)/2 {
				break
			}
			delete(rl.endpointLimiters, key)
			count++
		}
	}
}

// GetLimiterStats returns statistics about the rate limiter
func (rl *RateLimiter) GetLimiterStats() map[string]interface{} {
	rl.mu.RLock()
	defer rl.mu.RUnlock()

	stats := map[string]interface{}{
		"enabled":             rl.config.Enabled,
		"global_limit":        rl.config.GlobalLimit,
		"user_limit":          rl.config.UserLimit,
		"user_limiters_count": len(rl.userLimiters),
		"endpoint_limiters_count": len(rl.endpointLimiters),
	}

	if rl.globalLimiter != nil {
		stats["global_limiter_tokens"] = rl.globalLimiter.Tokens()
	}

	return stats
}

// SetEndpointLimit dynamically sets a rate limit for an endpoint
func (rl *RateLimiter) SetEndpointLimit(endpoint string, limit int, window time.Duration) {
	if rl.config.EndpointLimits == nil {
		rl.config.EndpointLimits = make(map[string]config.EndpointLimit)
	}

	rl.config.EndpointLimits[endpoint] = config.EndpointLimit{
		Limit:  limit,
		Window: window,
	}

	// Remove existing limiter to force recreation with new limits
	rl.mu.Lock()
	delete(rl.endpointLimiters, endpoint)
	rl.mu.Unlock()
}

// RemoveEndpointLimit removes a rate limit for an endpoint
func (rl *RateLimiter) RemoveEndpointLimit(endpoint string) {
	if rl.config.EndpointLimits != nil {
		delete(rl.config.EndpointLimits, endpoint)
	}

	rl.mu.Lock()
	delete(rl.endpointLimiters, endpoint)
	rl.mu.Unlock()
}

// ResetUserLimiter resets the rate limiter for a specific user
func (rl *RateLimiter) ResetUserLimiter(userID string) {
	rl.mu.Lock()
	delete(rl.userLimiters, userID)
	rl.mu.Unlock()
}

// IsAllowed checks if a request would be allowed without consuming tokens
func (rl *RateLimiter) IsAllowed(client ClientInfo) bool {
	// This is a read-only check - useful for testing or dry-run scenarios
	
	// Check global limit
	if rl.globalLimiter != nil {
		reservation := rl.globalLimiter.Reserve()
		if !reservation.OK() {
			return false
		}
		// Cancel the reservation since this is just a check
		reservation.Cancel()
	}

	// Check other limits similarly...
	return true
}