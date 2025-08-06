// Package middleware provides HTTP and gRPC middleware for ARC
package middleware

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/rizome-dev/arc/pkg/config"
)

// Claims represents JWT claims
type Claims struct {
	UserID   string   `json:"user_id"`
	Username string   `json:"username"`
	Roles    []string `json:"roles"`
	jwt.RegisteredClaims
}

// AuthService handles authentication and authorization
type AuthService struct {
	config *config.SecurityConfig
	jwtKey []byte
}

// NewAuthService creates a new authentication service
func NewAuthService(config *config.SecurityConfig) *AuthService {
	return &AuthService{
		config: config,
		jwtKey: []byte(config.Authentication.JWTConfig.SecretKey),
	}
}

// HTTPAuthMiddleware provides HTTP authentication middleware
func (a *AuthService) HTTPAuthMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !a.config.Authentication.Enabled {
			next.ServeHTTP(w, r)
			return
		}

		// Extract token from Authorization header
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, "Authorization header required", http.StatusUnauthorized)
			return
		}

		// Check if it's a Bearer token
		if !strings.HasPrefix(authHeader, "Bearer ") {
			http.Error(w, "Bearer token required", http.StatusUnauthorized)
			return
		}

		tokenString := strings.TrimPrefix(authHeader, "Bearer ")

		// Validate token
		claims, err := a.ValidateToken(tokenString)
		if err != nil {
			http.Error(w, "Invalid token", http.StatusUnauthorized)
			return
		}

		// Add claims to context
		ctx := context.WithValue(r.Context(), "user_claims", claims)
		r = r.WithContext(ctx)

		// Check authorization if enabled
		if a.config.Authorization.Enabled {
			if !a.authorizeHTTPRequest(r, claims) {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}
		}

		next.ServeHTTP(w, r)
	})
}

// GRPCAuthInterceptor provides gRPC authentication interceptor
func (a *AuthService) GRPCAuthInterceptor(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
	if !a.config.Authentication.Enabled {
		return handler(ctx, req)
	}

	// Extract metadata
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return nil, status.Errorf(codes.Unauthenticated, "metadata not found")
	}

	// Get authorization header
	authHeaders := md.Get("authorization")
	if len(authHeaders) == 0 {
		return nil, status.Errorf(codes.Unauthenticated, "authorization header not found")
	}

	authHeader := authHeaders[0]
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return nil, status.Errorf(codes.Unauthenticated, "bearer token required")
	}

	tokenString := strings.TrimPrefix(authHeader, "Bearer ")

	// Validate token
	claims, err := a.ValidateToken(tokenString)
	if err != nil {
		return nil, status.Errorf(codes.Unauthenticated, "invalid token: %v", err)
	}

	// Add claims to context
	ctx = context.WithValue(ctx, "user_claims", claims)

	// Check authorization if enabled
	if a.config.Authorization.Enabled {
		if !a.authorizeGRPCRequest(info.FullMethod, claims) {
			return nil, status.Errorf(codes.PermissionDenied, "access denied")
		}
	}

	return handler(ctx, req)
}

// BasicAuthMiddleware provides HTTP basic authentication
func (a *AuthService) BasicAuthMiddleware(username, password string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			// Use constant time comparison to prevent timing attacks
			userValid := subtle.ConstantTimeCompare([]byte(user), []byte(username)) == 1
			passValid := subtle.ConstantTimeCompare([]byte(pass), []byte(password)) == 1

			if !userValid || !passValid {
				w.Header().Set("WWW-Authenticate", `Basic realm="Restricted"`)
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// ValidateToken validates a JWT token and returns the claims
func (a *AuthService) ValidateToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		// Validate signing method
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return a.jwtKey, nil
	})

	if err != nil {
		return nil, err
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	// Check expiration
	if claims.ExpiresAt != nil && claims.ExpiresAt.Time.Before(time.Now()) {
		return nil, fmt.Errorf("token expired")
	}

	return claims, nil
}

// GenerateToken generates a JWT token for a user
func (a *AuthService) GenerateToken(userID, username string, roles []string) (string, error) {
	expirationTime := time.Now().Add(a.config.Authentication.JWTConfig.ExpiryDuration)

	claims := &Claims{
		UserID:   userID,
		Username: username,
		Roles:    roles,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    a.config.Authentication.JWTConfig.Issuer,
			Subject:   userID,
			ExpiresAt: jwt.NewNumericDate(expirationTime),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(a.jwtKey)
}

// authorizeHTTPRequest checks if the user is authorized for an HTTP request
func (a *AuthService) authorizeHTTPRequest(r *http.Request, claims *Claims) bool {
	// Get the endpoint from the request
	endpoint := fmt.Sprintf("%s %s", r.Method, r.URL.Path)

	return a.checkPermission(endpoint, claims.Roles)
}

// authorizeGRPCRequest checks if the user is authorized for a gRPC request
func (a *AuthService) authorizeGRPCRequest(method string, claims *Claims) bool {
	return a.checkPermission(method, claims.Roles)
}

// checkPermission checks if any of the user's roles have permission for the endpoint
func (a *AuthService) checkPermission(endpoint string, userRoles []string) bool {
	// Check if user is admin (admins have access to everything)
	for _, role := range userRoles {
		if contains(a.config.Authorization.AdminUsers, role) {
			return true
		}
	}

	// Check specific permissions
	for _, role := range userRoles {
		if permissions, exists := a.config.Authorization.Permissions[role]; exists {
			for _, permission := range permissions {
				if matchesEndpoint(permission, endpoint) {
					return true
				}
			}
		}
	}

	return false
}

// matchesEndpoint checks if a permission pattern matches an endpoint
func matchesEndpoint(pattern, endpoint string) bool {
	// Simple pattern matching - could be enhanced with regex or glob patterns
	if pattern == "*" {
		return true
	}

	// Exact match
	if pattern == endpoint {
		return true
	}

	// Wildcard prefix match
	if strings.HasSuffix(pattern, "*") {
		prefix := strings.TrimSuffix(pattern, "*")
		return strings.HasPrefix(endpoint, prefix)
	}

	return false
}

// GetUserFromContext extracts user claims from context
func GetUserFromContext(ctx context.Context) (*Claims, bool) {
	claims, ok := ctx.Value("user_claims").(*Claims)
	return claims, ok
}

// RequireRole creates a middleware that requires specific roles
func RequireRole(requiredRoles ...string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := GetUserFromContext(r.Context())
			if !ok {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			hasRole := false
			for _, userRole := range claims.Roles {
				for _, requiredRole := range requiredRoles {
					if userRole == requiredRole {
						hasRole = true
						break
					}
				}
				if hasRole {
					break
				}
			}

			if !hasRole {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RequireAdmin creates a middleware that requires admin role
func RequireAdmin(adminUsers []string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			claims, ok := GetUserFromContext(r.Context())
			if !ok {
				http.Error(w, "Unauthorized", http.StatusUnauthorized)
				return
			}

			isAdmin := false
			for _, userRole := range claims.Roles {
				if contains(adminUsers, userRole) {
					isAdmin = true
					break
				}
			}

			if !isAdmin {
				http.Error(w, "Forbidden", http.StatusForbidden)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
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