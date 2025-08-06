package middleware

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/rizome-dev/arc/pkg/config"
)

// User represents a user in the context
type User struct {
	ID       string
	Username string
	Roles    []string
}

// userContextKey is a key for storing user in context
type contextKey string

const userContextKey contextKey = "user"

// UserFromContext extracts user from context for testing
func UserFromContext(ctx context.Context) *User {
	if claims, ok := GetUserFromContext(ctx); ok {
		return &User{
			ID:       claims.UserID,
			Username: claims.Username,
			Roles:    claims.Roles,
		}
	}
	return nil
}

// CheckPermission checks if a user has permission for a resource/action
func (a *AuthService) CheckPermission(user *User, resource, action string) bool {
	endpoint := fmt.Sprintf("%s:%s", resource, action)
	return a.checkPermission(endpoint, user.Roles)
}

// IsAdminUser checks if a username is in the admin users list
func (a *AuthService) IsAdminUser(username string) bool {
	return contains(a.config.Authorization.AdminUsers, username)
}

func createTestAuthConfig() *config.SecurityConfig {
	return &config.SecurityConfig{
		Authentication: config.AuthenticationConfig{
			Enabled: true,
			Type:    "jwt",
			JWTConfig: config.JWTConfig{
				SecretKey:      "test-secret-key-for-testing",
				Issuer:         "arc-test",
				ExpiryDuration: 24 * time.Hour,
				Algorithm:      "HS256",
			},
		},
		Authorization: config.AuthorizationConfig{
			Enabled: true,
			Type:    "rbac",
			AdminUsers: []string{
				"admin@example.com",
				"root@example.com",
			},
			Permissions: map[string][]string{
				"admin": {
					"workflow:*",
					"agent:*",
					"system:*",
				},
				"user": {
					"workflow:read",
					"agent:read",
				},
				"operator": {
					"workflow:create",
					"workflow:read",
					"agent:*",
				},
			},
		},
	}
}

func createTestToken(authService *AuthService, claims *Claims) (string, error) {
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return token.SignedString(authService.jwtKey)
}

func TestNewAuthService(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	if authService == nil {
		t.Fatal("Auth service should not be nil")
	}
	
	if authService.config != config {
		t.Error("Auth service config should match provided config")
	}
	
	expectedKey := []byte(config.Authentication.JWTConfig.SecretKey)
	if string(authService.jwtKey) != string(expectedKey) {
		t.Error("JWT key should match config secret key")
	}
}

func TestHTTPAuthMiddleware_Disabled(t *testing.T) {
	config := createTestAuthConfig()
	config.Authentication.Enabled = false
	
	authService := NewAuthService(config)
	
	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})
	
	// Wrap with auth middleware
	middleware := authService.HTTPAuthMiddleware(testHandler)
	
	// Test request without token (should pass because auth is disabled)
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	
	middleware.ServeHTTP(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
	
	if w.Body.String() != "success" {
		t.Errorf("Expected body 'success', got %s", w.Body.String())
	}
}

func TestHTTPAuthMiddleware_ValidToken(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	// Create valid token
	claims := &Claims{
		UserID:   "test-user",
		Username: "testuser",
		Roles:    []string{"user"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    config.Authentication.JWTConfig.Issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	
	token, err := createTestToken(authService, claims)
	if err != nil {
		t.Fatalf("Failed to create test token: %v", err)
	}
	
	// Create test handler
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Verify user context was set
		user := UserFromContext(r.Context())
		if user == nil {
			t.Error("User should be set in context")
		} else {
			if user.ID != "test-user" {
				t.Errorf("Expected user ID 'test-user', got %s", user.ID)
			}
			if user.Username != "testuser" {
				t.Errorf("Expected username 'testuser', got %s", user.Username)
			}
		}
		
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("success"))
	})
	
	// Wrap with auth middleware
	middleware := authService.HTTPAuthMiddleware(testHandler)
	
	// Test request with valid token
	req := httptest.NewRequest("GET", "/test", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	w := httptest.NewRecorder()
	
	middleware.ServeHTTP(w, req)
	
	if w.Code != http.StatusOK {
		t.Errorf("Expected status 200, got %d", w.Code)
	}
}

func TestHTTPAuthMiddleware_NoToken(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	
	middleware := authService.HTTPAuthMiddleware(testHandler)
	
	// Test request without token
	req := httptest.NewRequest("GET", "/test", nil)
	w := httptest.NewRecorder()
	
	middleware.ServeHTTP(w, req)
	
	if w.Code != http.StatusUnauthorized {
		t.Errorf("Expected status 401, got %d", w.Code)
	}
}

func TestHTTPAuthMiddleware_InvalidToken(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	
	middleware := authService.HTTPAuthMiddleware(testHandler)
	
	tests := []struct {
		name  string
		token string
	}{
		{
			name:  "malformed token",
			token: "invalid-token",
		},
		{
			name:  "expired token",
			token: createExpiredToken(authService),
		},
		{
			name:  "wrong signing key",
			token: createTokenWithWrongKey(),
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("Authorization", "Bearer "+tt.token)
			w := httptest.NewRecorder()
			
			middleware.ServeHTTP(w, req)
			
			if w.Code != http.StatusUnauthorized {
				t.Errorf("Expected status 401 for %s, got %d", tt.name, w.Code)
			}
		})
	}
}

func TestGRPCAuthInterceptor_ValidToken(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	// Create valid token
	claims := &Claims{
		UserID:   "test-user",
		Username: "testuser",
		Roles:    []string{"admin"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    config.Authentication.JWTConfig.Issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	
	token, err := createTestToken(authService, claims)
	if err != nil {
		t.Fatalf("Failed to create test token: %v", err)
	}
	
	// Create test handler
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		// Check if claims are in context
		if claims, ok := GetUserFromContext(ctx); ok {
			if claims.UserID != "test-user" {
				return nil, fmt.Errorf("expected user ID 'test-user', got %s", claims.UserID)
			}
		} else {
			return nil, fmt.Errorf("user claims not found in context")
		}
		return "success", nil
	}
	
	// Create context with metadata
	md := metadata.Pairs("authorization", "Bearer "+token)
	ctx := metadata.NewIncomingContext(context.Background(), md)
	
	// Test interceptor
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}
	
	resp, err := authService.GRPCAuthInterceptor(ctx, nil, info, handler)
	if err != nil {
		t.Errorf("Unexpected error: %v", err)
	}
	
	if resp != "success" {
		t.Errorf("Expected response 'success', got %v", resp)
	}
}

func TestGRPCAuthInterceptor_NoToken(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "success", nil
	}
	
	// Create context without metadata
	ctx := context.Background()
	
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}
	
	_, err := authService.GRPCAuthInterceptor(ctx, nil, info, handler)
	if err == nil {
		t.Error("Expected error for missing token")
	}
	
	st, ok := status.FromError(err)
	if !ok {
		t.Error("Error should be a gRPC status")
	} else if st.Code() != codes.Unauthenticated {
		t.Errorf("Expected code Unauthenticated, got %v", st.Code())
	}
}

func TestGRPCAuthInterceptor_Disabled(t *testing.T) {
	config := createTestAuthConfig()
	config.Authentication.Enabled = false
	
	authService := NewAuthService(config)
	
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return "success", nil
	}
	
	ctx := context.Background()
	
	info := &grpc.UnaryServerInfo{
		FullMethod: "/test.Service/Method",
	}
	
	resp, err := authService.GRPCAuthInterceptor(ctx, nil, info, handler)
	if err != nil {
		t.Errorf("Unexpected error when auth disabled: %v", err)
	}
	
	if resp != "success" {
		t.Errorf("Expected response 'success', got %v", resp)
	}
}

func TestValidateToken(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	tests := []struct {
		name        string
		tokenFunc   func() string
		expectError bool
		errorMsg    string
	}{
		{
			name: "valid token",
			tokenFunc: func() string {
				claims := &Claims{
					UserID:   "test-user",
					Username: "testuser",
					Roles:    []string{"user"},
					RegisteredClaims: jwt.RegisteredClaims{
						Issuer:    config.Authentication.JWTConfig.Issuer,
						ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
						IssuedAt:  jwt.NewNumericDate(time.Now()),
					},
				}
				token, _ := createTestToken(authService, claims)
				return token
			},
			expectError: false,
		},
		{
			name: "expired token",
			tokenFunc: func() string {
				return createExpiredToken(authService)
			},
			expectError: true,
			errorMsg:    "token is expired",
		},
		{
			name: "invalid signature",
			tokenFunc: func() string {
				return createTokenWithWrongKey()
			},
			expectError: true,
			errorMsg:    "signature is invalid",
		},
		{
			name: "malformed token",
			tokenFunc: func() string {
				return "not-a-jwt-token"
			},
			expectError: true,
			errorMsg:    "invalid token format",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := tt.tokenFunc()
			claims, err := authService.ValidateToken(token)
			
			if tt.expectError {
				if err == nil {
					t.Error("Expected error but got none")
				} else if tt.errorMsg != "" && !strings.Contains(err.Error(), tt.errorMsg) {
					t.Errorf("Expected error containing %q, got: %v", tt.errorMsg, err)
				}
				if claims != nil {
					t.Error("Claims should be nil when validation fails")
				}
			} else {
				if err != nil {
					t.Errorf("Unexpected error: %v", err)
				}
				if claims == nil {
					t.Error("Claims should not be nil for valid token")
				} else {
					if claims.UserID != "test-user" {
						t.Errorf("Expected UserID 'test-user', got %s", claims.UserID)
					}
				}
			}
		})
	}
}

func TestCheckPermission(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	tests := []struct {
		name       string
		userRoles  []string
		resource   string
		action     string
		expectAuth bool
	}{
		{
			name:       "admin can do anything",
			userRoles:  []string{"admin"},
			resource:   "workflow",
			action:     "create",
			expectAuth: true,
		},
		{
			name:       "admin wildcard permission",
			userRoles:  []string{"admin"},
			resource:   "system",
			action:     "delete",
			expectAuth: true,
		},
		{
			name:       "user can read workflows",
			userRoles:  []string{"user"},
			resource:   "workflow",
			action:     "read",
			expectAuth: true,
		},
		{
			name:       "user cannot create workflows",
			userRoles:  []string{"user"},
			resource:   "workflow",
			action:     "create",
			expectAuth: false,
		},
		{
			name:       "operator can create workflows",
			userRoles:  []string{"operator"},
			resource:   "workflow",
			action:     "create",
			expectAuth: true,
		},
		{
			name:       "operator has agent wildcard",
			userRoles:  []string{"operator"},
			resource:   "agent",
			action:     "delete",
			expectAuth: true,
		},
		{
			name:       "multiple roles - any match grants access",
			userRoles:  []string{"user", "operator"},
			resource:   "workflow",
			action:     "create",
			expectAuth: true,
		},
		{
			name:       "no matching role",
			userRoles:  []string{"guest"},
			resource:   "workflow",
			action:     "read",
			expectAuth: false,
		},
		{
			name:       "empty roles",
			userRoles:  []string{},
			resource:   "workflow",
			action:     "read",
			expectAuth: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			user := &User{
				ID:       "test-user",
				Username: "testuser",
				Roles:    tt.userRoles,
			}
			
			authorized := authService.CheckPermission(user, tt.resource, tt.action)
			if authorized != tt.expectAuth {
				t.Errorf("Expected authorization %v, got %v", tt.expectAuth, authorized)
			}
		})
	}
}

func TestUserFromContext(t *testing.T) {
	// Test with user in context
	user := &User{
		ID:       "test-user",
		Username: "testuser",
		Roles:    []string{"admin"},
	}
	
	ctx := context.WithValue(context.Background(), userContextKey, user)
	
	retrievedUser := UserFromContext(ctx)
	if retrievedUser == nil {
		t.Error("User should not be nil")
	} else {
		if retrievedUser.ID != user.ID {
			t.Errorf("Expected user ID %s, got %s", user.ID, retrievedUser.ID)
		}
	}
	
	// Test with no user in context
	emptyCtx := context.Background()
	noUser := UserFromContext(emptyCtx)
	if noUser != nil {
		t.Error("User should be nil for empty context")
	}
}

func TestHTTPAuthMiddleware_AuthorizationHeader_Formats(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	// Create valid token
	claims := &Claims{
		UserID:   "test-user",
		Username: "testuser",
		Roles:    []string{"user"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    config.Authentication.JWTConfig.Issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	
	validToken, err := createTestToken(authService, claims)
	if err != nil {
		t.Fatalf("Failed to create test token: %v", err)
	}
	
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	
	middleware := authService.HTTPAuthMiddleware(testHandler)
	
	tests := []struct {
		name           string
		authHeader     string
		expectedStatus int
	}{
		{
			name:           "valid bearer token",
			authHeader:     "Bearer " + validToken,
			expectedStatus: http.StatusOK,
		},
		{
			name:           "bearer token lowercase",
			authHeader:     "bearer " + validToken,
			expectedStatus: http.StatusUnauthorized, // Should be case sensitive
		},
		{
			name:           "no bearer prefix",
			authHeader:     validToken,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "wrong prefix",
			authHeader:     "Token " + validToken,
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "empty header",
			authHeader:     "",
			expectedStatus: http.StatusUnauthorized,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest("GET", "/test", nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			w := httptest.NewRecorder()
			
			middleware.ServeHTTP(w, req)
			
			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}
		})
	}
}

func TestIsAdminUser(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	tests := []struct {
		name     string
		username string
		expected bool
	}{
		{
			name:     "admin user",
			username: "admin@example.com",
			expected: true,
		},
		{
			name:     "root user",
			username: "root@example.com",
			expected: true,
		},
		{
			name:     "regular user",
			username: "user@example.com",
			expected: false,
		},
		{
			name:     "empty username",
			username: "",
			expected: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := authService.IsAdminUser(tt.username)
			if result != tt.expected {
				t.Errorf("Expected %v for username %s, got %v", tt.expected, tt.username, result)
			}
		})
	}
}

// Helper functions for testing

func createExpiredToken(authService *AuthService) string {
	claims := &Claims{
		UserID:   "test-user",
		Username: "testuser",
		Roles:    []string{"user"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "arc-test",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(-time.Hour)), // Expired
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-2 * time.Hour)),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	tokenString, _ := token.SignedString(authService.jwtKey)
	return tokenString
}

func createTokenWithWrongKey() string {
	claims := &Claims{
		UserID:   "test-user",
		Username: "testuser",
		Roles:    []string{"user"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    "arc-test",
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	wrongKey := []byte("wrong-secret-key")
	tokenString, _ := token.SignedString(wrongKey)
	return tokenString
}

func TestHTTPAuthMiddleware_ConcurrentRequests(t *testing.T) {
	config := createTestAuthConfig()
	authService := NewAuthService(config)
	
	// Create valid token
	claims := &Claims{
		UserID:   "test-user",
		Username: "testuser",
		Roles:    []string{"user"},
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    config.Authentication.JWTConfig.Issuer,
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
			IssuedAt:  jwt.NewNumericDate(time.Now()),
		},
	}
	
	token, err := createTestToken(authService, claims)
	if err != nil {
		t.Fatalf("Failed to create test token: %v", err)
	}
	
	testHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := UserFromContext(r.Context())
		if user == nil {
			t.Error("User should be in context")
		}
		w.WriteHeader(http.StatusOK)
	})
	
	middleware := authService.HTTPAuthMiddleware(testHandler)
	
	// Run concurrent requests
	const numRequests = 100
	results := make(chan int, numRequests)
	
	for i := 0; i < numRequests; i++ {
		go func() {
			req := httptest.NewRequest("GET", "/test", nil)
			req.Header.Set("Authorization", "Bearer "+token)
			w := httptest.NewRecorder()
			
			middleware.ServeHTTP(w, req)
			results <- w.Code
		}()
	}
	
	// Collect results
	for i := 0; i < numRequests; i++ {
		code := <-results
		if code != http.StatusOK {
			t.Errorf("Expected status 200, got %d for concurrent request", code)
		}
	}
}