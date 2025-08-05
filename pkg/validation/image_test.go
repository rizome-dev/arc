package validation

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

// MockSignatureVerifier implements SignatureVerifier for testing
type MockSignatureVerifier struct {
	shouldFail bool
}

func (m *MockSignatureVerifier) VerifySignature(ctx context.Context, image string, trustAnchors []string) error {
	if m.shouldFail {
		return fmt.Errorf("signature verification failed")
	}
	return nil
}

// MockVulnerabilityScanner implements VulnerabilityScanner for testing
type MockVulnerabilityScanner struct {
	criticalCount int
	highCount     int
	mediumCount   int
	lowCount      int
	shouldFail    bool
}

func (m *MockVulnerabilityScanner) Scan(ctx context.Context, image string) (*ScanResult, error) {
	if m.shouldFail {
		return nil, fmt.Errorf("scan failed")
	}
	
	return &ScanResult{
		ImageReference: image,
		ImageID:        "test-image-id",
		ImageDigest:    "sha256:abcdef1234567890",
		ScanTimestamp:  time.Now(),
		Scanner:        "mock-scanner",
		ScannerVersion: "1.0.0",
		CriticalCount:  m.criticalCount,
		HighCount:      m.highCount,
		MediumCount:    m.mediumCount,
		LowCount:       m.lowCount,
		TotalCount:     m.criticalCount + m.highCount + m.mediumCount + m.lowCount,
		Vulnerabilities: []Vulnerability{},
		OS:             "linux",
		Architecture:   "amd64",
		CreatedAt:      time.Now().Add(-24 * time.Hour),
		Size:           1024 * 1024 * 50, // 50MB
		ComplianceStatus: "pass",
	}, nil
}

func TestParseImageReference(t *testing.T) {
	tests := []struct {
		name          string
		image         string
		wantErr       bool
		expectedInfo  *ImageInfo
	}{
		{
			name:  "official image with tag",
			image: "nginx:1.21",
			expectedInfo: &ImageInfo{
				Registry:      "docker.io",
				Namespace:     "library",
				Repository:    "nginx",
				Tag:           "1.21",
				FullReference: "nginx:1.21",
				IsOfficial:    true,
				HasTag:        true,
			},
		},
		{
			name:  "official image with latest",
			image: "nginx",
			expectedInfo: &ImageInfo{
				Registry:      "docker.io",
				Namespace:     "library",
				Repository:    "nginx",
				Tag:           "latest",
				FullReference: "nginx",
				IsOfficial:    true,
				HasTag:        true,
			},
		},
		{
			name:  "docker hub user image",
			image: "myuser/myapp:v1.0.0",
			expectedInfo: &ImageInfo{
				Registry:      "docker.io",
				Namespace:     "myuser",
				Repository:    "myapp",
				Tag:           "v1.0.0",
				FullReference: "myuser/myapp:v1.0.0",
				HasTag:        true,
			},
		},
		{
			name:  "private registry with port",
			image: "registry.example.com:5000/namespace/app:latest",
			expectedInfo: &ImageInfo{
				Registry:      "registry.example.com:5000",
				Namespace:     "namespace",
				Repository:    "app",
				Tag:           "latest",
				FullReference: "registry.example.com:5000/namespace/app:latest",
				HasTag:        true,
			},
		},
		{
			name:  "image with digest",
			image: "nginx@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			expectedInfo: &ImageInfo{
				Registry:      "docker.io",
				Namespace:     "library",
				Repository:    "nginx",
				Digest:        "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
				FullReference: "nginx@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
				IsOfficial:    true,
				HasDigest:     true,
				Tag:           "", // When digest is specified, tag is not needed
				HasTag:        false,
			},
		},
		{
			name:  "image with tag and digest",
			image: "nginx:1.21@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			expectedInfo: &ImageInfo{
				Registry:      "docker.io",
				Namespace:     "library",
				Repository:    "nginx",
				Tag:           "1.21",
				Digest:        "sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
				FullReference: "nginx:1.21@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
				IsOfficial:    true,
				HasTag:        true,
				HasDigest:     true,
			},
		},
		{
			name:  "localhost registry",
			image: "localhost:5000/myapp:dev",
			expectedInfo: &ImageInfo{
				Registry:      "localhost:5000",
				Repository:    "myapp",
				Tag:           "dev",
				FullReference: "localhost:5000/myapp:dev",
				HasTag:        true,
			},
		},
		{
			name:    "empty image reference",
			image:   "",
			wantErr: true,
		},
		{
			name:    "invalid tag characters",
			image:   "nginx:tag with spaces",
			wantErr: true,
		},
		{
			name:    "invalid digest format",
			image:   "nginx@md5:12345",
			wantErr: true,
		},
		{
			name:    "invalid digest length",
			image:   "nginx@sha256:tooshort",
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParseImageReference(tt.image)
			
			if (err != nil) != tt.wantErr {
				t.Errorf("ParseImageReference() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if !tt.wantErr && info != nil {
				if info.Registry != tt.expectedInfo.Registry {
					t.Errorf("Registry = %v, want %v", info.Registry, tt.expectedInfo.Registry)
				}
				if info.Namespace != tt.expectedInfo.Namespace {
					t.Errorf("Namespace = %v, want %v", info.Namespace, tt.expectedInfo.Namespace)
				}
				if info.Repository != tt.expectedInfo.Repository {
					t.Errorf("Repository = %v, want %v", info.Repository, tt.expectedInfo.Repository)
				}
				if info.Tag != tt.expectedInfo.Tag {
					t.Errorf("Tag = %v, want %v", info.Tag, tt.expectedInfo.Tag)
				}
				if info.Digest != tt.expectedInfo.Digest {
					t.Errorf("Digest = %v, want %v", info.Digest, tt.expectedInfo.Digest)
				}
				if info.IsOfficial != tt.expectedInfo.IsOfficial {
					t.Errorf("IsOfficial = %v, want %v", info.IsOfficial, tt.expectedInfo.IsOfficial)
				}
				if info.HasTag != tt.expectedInfo.HasTag {
					t.Errorf("HasTag = %v, want %v", info.HasTag, tt.expectedInfo.HasTag)
				}
				if info.HasDigest != tt.expectedInfo.HasDigest {
					t.Errorf("HasDigest = %v, want %v", info.HasDigest, tt.expectedInfo.HasDigest)
				}
			}
		})
	}
}

func TestIsAllowedRegistry(t *testing.T) {
	tests := []struct {
		name        string
		registry    string
		allowedList []string
		want        bool
	}{
		{
			name:        "empty allowed list allows all",
			registry:    "any.registry.com",
			allowedList: []string{},
			want:        true,
		},
		{
			name:        "exact match allowed",
			registry:    "docker.io",
			allowedList: []string{"docker.io", "quay.io"},
			want:        true,
		},
		{
			name:        "not in allowed list",
			registry:    "untrusted.com",
			allowedList: []string{"docker.io", "quay.io"},
			want:        false,
		},
		{
			name:        "wildcard allows all",
			registry:    "any.registry.com",
			allowedList: []string{"*"},
			want:        true,
		},
		{
			name:        "prefix wildcard match",
			registry:    "gcr.io",
			allowedList: []string{"gcr.*"},
			want:        true,
		},
		{
			name:        "prefix wildcard no match",
			registry:    "docker.io",
			allowedList: []string{"gcr.*"},
			want:        false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsAllowedRegistry(tt.registry, tt.allowedList); got != tt.want {
				t.Errorf("IsAllowedRegistry() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultImageValidator_ValidateImage(t *testing.T) {
	tests := []struct {
		name    string
		config  *ValidationConfig
		image   string
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid image passes validation",
			config: &ValidationConfig{
				AllowLatestTag: false,
				RequireHTTPS:   true,
			},
			image:   "docker.io/nginx:1.21",
			wantErr: false,
		},
		{
			name: "latest tag blocked",
			config: &ValidationConfig{
				AllowLatestTag: false,
			},
			image:   "nginx:latest",
			wantErr: true,
			errMsg:  "'latest' tag is not allowed",
		},
		{
			name: "registry not in allowed list",
			config: &ValidationConfig{
				AllowedRegistries: []string{"docker.io", "quay.io"},
			},
			image:   "untrusted.com/app:v1",
			wantErr: true,
			errMsg:  "not in allowed list",
		},
		{
			name: "blocked registry",
			config: &ValidationConfig{
				BlockedRegistries: []string{"untrusted.com"},
			},
			image:   "untrusted.com/app:v1",
			wantErr: true,
			errMsg:  "is blocked",
		},
		{
			name: "digest required but missing",
			config: &ValidationConfig{
				RequireDigest: true,
			},
			image:   "nginx:1.21",
			wantErr: true,
			errMsg:  "digest required",
		},
		{
			name: "digest provided when required",
			config: &ValidationConfig{
				RequireDigest: true,
			},
			image:   "nginx@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
			wantErr: false,
		},
		{
			name: "tag pattern validation success",
			config: &ValidationConfig{
				AllowedTagPatterns: []string{`^v\d+\.\d+\.\d+$`}, // Semantic versioning
			},
			image:   "myapp:v1.2.3",
			wantErr: false,
		},
		{
			name: "tag pattern validation failure",
			config: &ValidationConfig{
				AllowedTagPatterns: []string{`^v\d+\.\d+\.\d+$`}, // Semantic versioning
			},
			image:   "myapp:dev",
			wantErr: true,
			errMsg:  "does not match allowed patterns",
		},
		{
			name: "blocked tag pattern",
			config: &ValidationConfig{
				BlockedTagPatterns: []string{`^dev.*`},
			},
			image:   "myapp:dev-branch",
			wantErr: true,
			errMsg:  "matches blocked pattern",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewDefaultImageValidator(tt.config)
			err := validator.ValidateImage(context.Background(), tt.image)
			
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateImage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if err != nil && tt.errMsg != "" {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("ValidateImage() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestDefaultImageValidator_ScanImage(t *testing.T) {
	tests := []struct {
		name          string
		config        *ValidationConfig
		scanner       *MockVulnerabilityScanner
		image         string
		wantErr       bool
		expectCached  bool
	}{
		{
			name: "successful scan with no vulnerabilities",
			config: &ValidationConfig{
				EnableScanning:    true,
				MaxCriticalCVEs:   0,
				MaxHighCVEs:       5,
				ScanCacheDuration: 1 * time.Hour,
			},
			scanner: &MockVulnerabilityScanner{
				criticalCount: 0,
				highCount:     0,
				mediumCount:   0,
				lowCount:      0,
			},
			image:   "nginx:1.21",
			wantErr: false,
		},
		{
			name: "scan exceeds critical threshold",
			config: &ValidationConfig{
				EnableScanning:  true,
				MaxCriticalCVEs: 0,
			},
			scanner: &MockVulnerabilityScanner{
				criticalCount: 1,
			},
			image:   "vulnerable:latest",
			wantErr: true, // Returns both result and error
		},
		{
			name: "scan exceeds high threshold",
			config: &ValidationConfig{
				EnableScanning: true,
				MaxHighCVEs:    5,
			},
			scanner: &MockVulnerabilityScanner{
				highCount: 10,
			},
			image:   "vulnerable:latest",
			wantErr: true,
		},
		{
			name: "scanning disabled",
			config: &ValidationConfig{
				EnableScanning: false,
			},
			image:   "nginx:1.21",
			wantErr: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewDefaultImageValidator(tt.config)
			if tt.scanner != nil {
				validator.SetVulnerabilityScanner(tt.scanner)
			}
			
			result, err := validator.ScanImage(context.Background(), tt.image)
			
			// Special handling for threshold exceeded cases - we get both result and error
			if tt.wantErr && tt.config.EnableScanning && tt.scanner != nil {
				// When thresholds are exceeded, we should get both result and error
				if err == nil {
					t.Errorf("ScanImage() expected error for threshold violation")
					return
				}
				if result == nil {
					t.Errorf("ScanImage() should return result even with threshold error")
					return
				}
			} else {
				// Normal error checking for other cases
				if (err != nil) != tt.wantErr {
					t.Errorf("ScanImage() error = %v, wantErr %v", err, tt.wantErr)
					return
				}
				
				if err == nil && !tt.wantErr && result == nil {
					t.Error("ScanImage() returned nil result without error")
				}
			}
			
			// Test caching if applicable
			if !tt.wantErr && tt.config.EnableScanning {
				// Second call should use cache
				result2, err2 := validator.ScanImage(context.Background(), tt.image)
				if err2 != nil {
					t.Errorf("ScanImage() cached call error = %v", err2)
				}
				if result2 != result {
					// Should be the same cached object
					t.Error("ScanImage() did not return cached result")
				}
			}
		})
	}
}

func TestDefaultImageValidator_CheckPolicy(t *testing.T) {
	tests := []struct {
		name    string
		config  *ValidationConfig
		scanner *MockVulnerabilityScanner
		image   string
		policy  Policy
		wantErr bool
		errMsg  string
	}{
		{
			name: "disabled policy always passes",
			image: "any:image",
			policy: Policy{
				Name:    "test",
				Enabled: false,
			},
			wantErr: false,
		},
		{
			name: "exception allows blocked image",
			image: "nginx:latest",
			policy: Policy{
				Name:           "no-latest",
				Enabled:        true,
				AllowLatestTag: false,
				Exceptions:     []string{"nginx:.*"},
			},
			wantErr: false,
		},
		{
			name: "registry not in allowed list",
			image: "untrusted.com/app:v1",
			policy: Policy{
				Name:              "trusted-registries",
				Enabled:           true,
				AllowedRegistries: []string{"docker.io", "quay.io"},
			},
			wantErr: true,
			errMsg:  "not in allowed list",
		},
		{
			name: "tag pattern whitelist",
			image: "app:v1.2.3",
			policy: Policy{
				Name:                "semantic-versioning",
				Enabled:             true,
				TagPatternWhitelist: []string{`^v\d+\.\d+\.\d+$`},
			},
			wantErr: false,
		},
		{
			name: "CVE threshold enforcement",
			config: &ValidationConfig{
				EnableScanning: true,
			},
			scanner: &MockVulnerabilityScanner{
				criticalCount: 2,
			},
			image: "vulnerable:v1",
			policy: Policy{
				Name:            "no-critical-cves",
				Enabled:         true,
				MaxCriticalCVEs: 1,
				EnforcementMode: "block",
			},
			wantErr: true,
			errMsg:  "critical",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			config := tt.config
			if config == nil {
				config = DefaultValidationConfig()
			}
			
			validator := NewDefaultImageValidator(config)
			if tt.scanner != nil {
				validator.SetVulnerabilityScanner(tt.scanner)
			}
			
			err := validator.CheckPolicy(context.Background(), tt.image, tt.policy)
			
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckPolicy() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if err != nil && tt.errMsg != "" {
				if !contains(err.Error(), tt.errMsg) {
					t.Errorf("CheckPolicy() error = %v, want error containing %v", err, tt.errMsg)
				}
			}
		})
	}
}

func TestHasCriticalVulnerabilities(t *testing.T) {
	tests := []struct {
		name   string
		result *ScanResult
		want   bool
	}{
		{
			name:   "nil result",
			result: nil,
			want:   false,
		},
		{
			name: "no critical vulnerabilities",
			result: &ScanResult{
				CriticalCount: 0,
				HighCount:     5,
			},
			want: false,
		},
		{
			name: "has critical vulnerabilities",
			result: &ScanResult{
				CriticalCount: 2,
			},
			want: true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := HasCriticalVulnerabilities(tt.result); got != tt.want {
				t.Errorf("HasCriticalVulnerabilities() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCalculateRiskScore(t *testing.T) {
	tests := []struct {
		name   string
		result *ScanResult
		want   float64
	}{
		{
			name:   "nil result",
			result: nil,
			want:   0,
		},
		{
			name: "no vulnerabilities",
			result: &ScanResult{
				CriticalCount: 0,
				HighCount:     0,
				MediumCount:   0,
				LowCount:      0,
			},
			want: 0,
		},
		{
			name: "mixed vulnerabilities",
			result: &ScanResult{
				CriticalCount: 1,  // 10 points
				HighCount:     2,  // 10 points
				MediumCount:   5,  // 10 points
				LowCount:      10, // 10 points
			},
			want: 40,
		},
		{
			name: "score caps at 100",
			result: &ScanResult{
				CriticalCount: 20, // Would be 200 points
			},
			want: 100,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CalculateRiskScore(tt.result); got != tt.want {
				t.Errorf("CalculateRiskScore() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestValidateImageAge(t *testing.T) {
	tests := []struct {
		name      string
		createdAt time.Time
		maxAge    time.Duration
		wantErr   bool
	}{
		{
			name:      "no age limit",
			createdAt: time.Now().Add(-365 * 24 * time.Hour),
			maxAge:    0,
			wantErr:   false,
		},
		{
			name:      "within age limit",
			createdAt: time.Now().Add(-7 * 24 * time.Hour),
			maxAge:    30 * 24 * time.Hour,
			wantErr:   false,
		},
		{
			name:      "exceeds age limit",
			createdAt: time.Now().Add(-60 * 24 * time.Hour),
			maxAge:    30 * 24 * time.Hour,
			wantErr:   true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateImageAge(tt.createdAt, tt.maxAge)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateImageAge() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestSanitizeImageReference(t *testing.T) {
	tests := []struct {
		name  string
		image string
		want  string
	}{
		{
			name:  "clean image reference",
			image: "docker.io/nginx:1.21",
			want:  "docker.io/nginx:1.21",
		},
		{
			name:  "removes shell metacharacters",
			image: "nginx:1.21; rm -rf /",
			want:  "nginx:1.21 rm -rf /",
		},
		{
			name:  "removes command substitution",
			image: "nginx:$(whoami)",
			want:  "nginx:whoami",
		},
		{
			name:  "removes pipes and redirects",
			image: "nginx:1.21 | cat > /etc/passwd",
			want:  "nginx:1.21  cat  /etc/passwd",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := SanitizeImageReference(tt.image); got != tt.want {
				t.Errorf("SanitizeImageReference() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestDefaultImageValidator_ConcurrentScanning(t *testing.T) {
	config := &ValidationConfig{
		EnableScanning:    true,
		MaxCriticalCVEs:   0,
		MaxHighCVEs:       5,
		ScanCacheDuration: 1 * time.Hour,
		ConcurrentScans:   3,
		ScanTimeout:       5 * time.Second,
	}
	
	validator := NewDefaultImageValidator(config)
	scanner := &MockVulnerabilityScanner{
		criticalCount: 0,
		highCount:     2,
		mediumCount:   5,
		lowCount:      10,
	}
	validator.SetVulnerabilityScanner(scanner)
	
	numGoroutines := 10
	numImagesPerGoroutine := 5
	
	errChan := make(chan error, numGoroutines*numImagesPerGoroutine)
	
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			for j := 0; j < numImagesPerGoroutine; j++ {
				image := fmt.Sprintf("nginx:test-%d-%d", id, j)
				_, err := validator.ScanImage(context.Background(), image)
				errChan <- err
			}
		}(i)
	}
	
	// Collect all errors
	for i := 0; i < numGoroutines*numImagesPerGoroutine; i++ {
		if err := <-errChan; err != nil {
			t.Errorf("Concurrent ScanImage() error: %v", err)
		}
	}
}

func TestDefaultImageValidator_CacheExpiration(t *testing.T) {
	config := &ValidationConfig{
		EnableScanning:    true,
		ScanCacheDuration: 100 * time.Millisecond, // Very short for testing
	}
	
	validator := NewDefaultImageValidator(config)
	scanner := &MockVulnerabilityScanner{
		criticalCount: 0,
		highCount:     1,
	}
	validator.SetVulnerabilityScanner(scanner)
	
	image := "nginx:cache-test"
	
	// First scan
	result1, err := validator.ScanImage(context.Background(), image)
	if err != nil {
		t.Errorf("First ScanImage() error: %v", err)
	}
	
	// Wait for cache to expire
	time.Sleep(200 * time.Millisecond)
	
	// Change scanner result
	scanner.highCount = 2
	
	// Second scan should not use cache and return updated result
	result2, err := validator.ScanImage(context.Background(), image)
	if err != nil {
		t.Errorf("Second ScanImage() error: %v", err)
	}
	
	if result1.HighCount == result2.HighCount {
		t.Error("Cache should have expired and returned updated scan result")
	}
}

func TestDefaultImageValidator_ScanTimeout(t *testing.T) {
	config := &ValidationConfig{
		EnableScanning: true,
		ScanTimeout:    50 * time.Millisecond, // Very short timeout
	}
	
	validator := NewDefaultImageValidator(config)
	
	// Create a scanner that will timeout
	slowScanner := &MockVulnerabilityScanner{}
	// We'll just rely on the context timeout to trigger the error
	
	validator.SetVulnerabilityScanner(slowScanner)
	
	_, err := validator.ScanImage(context.Background(), "nginx:timeout-test")
	if err == nil {
		t.Error("ScanImage() should timeout and return error")
	}
	
	if !strings.Contains(err.Error(), "context deadline exceeded") {
		t.Errorf("Expected timeout error, got: %v", err)
	}
}

func TestDefaultImageValidator_SignatureVerification(t *testing.T) {
	tests := []struct {
		name        string
		config      *ValidationConfig
		verifier    *MockSignatureVerifier
		image       string
		wantErr     bool
		errContains string
	}{
		{
			name: "signature verification disabled",
			config: &ValidationConfig{
				RequireSignature: false,
			},
			image:   "nginx:unsigned",
			wantErr: false,
		},
		{
			name: "signature verification enabled but no verifier",
			config: &ValidationConfig{
				RequireSignature: true,
			},
			image:       "nginx:signed",
			wantErr:     true,
			errContains: "no verifier configured",
		},
		{
			name: "signature verification success",
			config: &ValidationConfig{
				RequireSignature: true,
				TrustAnchors:     []string{"public-key-1"},
			},
			verifier: &MockSignatureVerifier{shouldFail: false},
			image:    "nginx:signed",
			wantErr:  false,
		},
		{
			name: "signature verification failure",
			config: &ValidationConfig{
				RequireSignature: true,
				TrustAnchors:     []string{"public-key-1"},
			},
			verifier:    &MockSignatureVerifier{shouldFail: true},
			image:       "nginx:unsigned",
			wantErr:     true,
			errContains: "signature verification failed",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewDefaultImageValidator(tt.config)
			if tt.verifier != nil {
				validator.SetSignatureVerifier(tt.verifier)
			}
			
			err := validator.ValidateImage(context.Background(), tt.image)
			
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateImage() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if err != nil && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("ValidateImage() error = %v, want error containing %v", err, tt.errContains)
				}
			}
		})
	}
}

func TestGetVulnerabilitiesBySeverity(t *testing.T) {
	vulnerabilities := []Vulnerability{
		{ID: "CVE-2021-1", Severity: SeverityCritical},
		{ID: "CVE-2021-2", Severity: SeverityHigh},
		{ID: "CVE-2021-3", Severity: SeverityHigh},
		{ID: "CVE-2021-4", Severity: SeverityMedium},
		{ID: "CVE-2021-5", Severity: SeverityLow},
	}
	
	result := &ScanResult{
		Vulnerabilities: vulnerabilities,
	}
	
	tests := []struct {
		name     string
		result   *ScanResult
		severity Severity
		want     int
	}{
		{
			name:     "nil result",
			result:   nil,
			severity: SeverityCritical,
			want:     0,
		},
		{
			name:     "critical vulnerabilities",
			result:   result,
			severity: SeverityCritical,
			want:     1,
		},
		{
			name:     "high vulnerabilities",
			result:   result,
			severity: SeverityHigh,
			want:     2,
		},
		{
			name:     "medium vulnerabilities",
			result:   result,
			severity: SeverityMedium,
			want:     1,
		},
		{
			name:     "low vulnerabilities",
			result:   result,
			severity: SeverityLow,
			want:     1,
		},
		{
			name:     "unknown severity",
			result:   result,
			severity: SeverityUnknown,
			want:     0,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GetVulnerabilitiesBySeverity(tt.result, tt.severity)
			if len(got) != tt.want {
				t.Errorf("GetVulnerabilitiesBySeverity() returned %d vulnerabilities, want %d", len(got), tt.want)
			}
		})
	}
}

func TestGenerateImageHash(t *testing.T) {
	tests := []struct {
		name  string
		image string
	}{
		{
			name:  "simple image",
			image: "nginx:latest",
		},
		{
			name:  "image with digest",
			image: "nginx@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
		},
		{
			name:  "empty image",
			image: "",
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			hash := GenerateImageHash(tt.image)
			
			if hash == "" {
				t.Error("GenerateImageHash() should not return empty string")
			}
			
			if len(hash) != 64 { // SHA256 hex length
				t.Errorf("GenerateImageHash() returned hash of length %d, want 64", len(hash))
			}
			
			// Hash should be deterministic
			hash2 := GenerateImageHash(tt.image)
			if hash != hash2 {
				t.Error("GenerateImageHash() should be deterministic")
			}
		})
	}
}

func TestDefaultImageValidator_ClearCache(t *testing.T) {
	config := &ValidationConfig{
		EnableScanning:    true,
		ScanCacheDuration: 1 * time.Hour,
	}
	
	validator := NewDefaultImageValidator(config)
	scanner := &MockVulnerabilityScanner{
		highCount: 1,
	}
	validator.SetVulnerabilityScanner(scanner)
	
	image := "nginx:clear-cache-test"
	
	// Perform initial scan to populate cache
	_, err := validator.ScanImage(context.Background(), image)
	if err != nil {
		t.Errorf("Initial ScanImage() error: %v", err)
	}
	
	// Verify cache is used (scanner count doesn't change)
	scanner.highCount = 2
	result1, err := validator.ScanImage(context.Background(), image)
	if err != nil {
		t.Errorf("Cached ScanImage() error: %v", err)
	}
	
	if result1.HighCount != 1 { // Should still be 1 from cache
		t.Error("Cache should be used for second scan")
	}
	
	// Clear cache
	validator.ClearCache()
	
	// Next scan should not use cache
	result2, err := validator.ScanImage(context.Background(), image)
	if err != nil {
		t.Errorf("Post-clear ScanImage() error: %v", err)
	}
	
	if result2.HighCount != 2 { // Should now be 2 after cache clear
		t.Error("Cache should be cleared and new scan performed")
	}
}

func TestDefaultImageValidator_PolicyEnforcementModes(t *testing.T) {
	tests := []struct {
		name            string
		config          *ValidationConfig
		scanner         *MockVulnerabilityScanner
		image           string
		policy          Policy
		wantErr         bool
		expectScanError bool
	}{
		{
			name: "block mode fails on scan error",
			config: &ValidationConfig{
				EnableScanning: true,
			},
			scanner: &MockVulnerabilityScanner{shouldFail: true},
			image:   "nginx:scan-fail",
			policy: Policy{
				Name:            "strict-policy",
				Enabled:         true,
				MaxCriticalCVEs: 0,
				EnforcementMode: "block",
			},
			wantErr:         true,
			expectScanError: true,
		},
		{
			name: "warn mode allows scan errors",
			config: &ValidationConfig{
				EnableScanning: true,
			},
			scanner: &MockVulnerabilityScanner{shouldFail: true},
			image:   "nginx:scan-fail",
			policy: Policy{
				Name:            "warn-policy",
				Enabled:         true,
				MaxCriticalCVEs: 0,
				EnforcementMode: "warn",
			},
			wantErr:         false,
			expectScanError: false,
		},
		{
			name: "audit mode allows scan errors",
			config: &ValidationConfig{
				EnableScanning: true,
			},
			scanner: &MockVulnerabilityScanner{shouldFail: true},
			image:   "nginx:scan-fail",
			policy: Policy{
				Name:            "audit-policy",
				Enabled:         true,
				MaxCriticalCVEs: 0,
				EnforcementMode: "audit",
			},
			wantErr:         false,
			expectScanError: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewDefaultImageValidator(tt.config)
			if tt.scanner != nil {
				validator.SetVulnerabilityScanner(tt.scanner)
			}
			
			err := validator.CheckPolicy(context.Background(), tt.image, tt.policy)
			
			if (err != nil) != tt.wantErr {
				t.Errorf("CheckPolicy() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			
			if tt.expectScanError && err != nil {
				if !strings.Contains(err.Error(), "security scan required") {
					t.Errorf("Expected scan error message, got: %v", err)
				}
			}
		})
	}
}

func TestImageValidation_ComplexScenarios(t *testing.T) {
	tests := []struct {
		name            string
		image           string
		config          *ValidationConfig
		scanner         *MockVulnerabilityScanner
		verifier        *MockSignatureVerifier
		wantValidateErr bool
		wantScanErr     bool
	}{
		{
			name:  "enterprise security policy",
			image: "registry.company.com/apps/web-service:v2.1.0",
			config: &ValidationConfig{
				AllowedRegistries:   []string{"registry.company.com", "docker.io"},
				AllowLatestTag:      false,
				AllowedTagPatterns:  []string{`^v\d+\.\d+\.\d+$`},
				EnableScanning:      true,
				MaxCriticalCVEs:     0,
				MaxHighCVEs:         3,
				MaxMediumCVEs:       10,
				RequireSignature:    true,
				RequireDigest:       false,
			},
			scanner: &MockVulnerabilityScanner{
				criticalCount: 0,
				highCount:     2,
				mediumCount:   5,
				lowCount:      20,
			},
			verifier:        &MockSignatureVerifier{shouldFail: false},
			wantValidateErr: false,
			wantScanErr:     false,
		},
		{
			name:  "development environment policy",
			image: "nginx:dev-latest",
			config: &ValidationConfig{
				AllowedRegistries:  []string{"docker.io", "localhost:5000"},
				AllowLatestTag:     true,
				BlockedRegistries:  []string{"malicious.com"},
				EnableScanning:     true,
				MaxCriticalCVEs:    2,
				MaxHighCVEs:        10,
				RequireSignature:   false,
			},
			scanner: &MockVulnerabilityScanner{
				criticalCount: 1,
				highCount:     5,
				mediumCount:   15,
				lowCount:      50,
			},
			wantValidateErr: false,
			wantScanErr:     false,
		},
		{
			name:  "strict production policy violation",
			image: "docker.io/nginx:latest",
			config: &ValidationConfig{
				AllowLatestTag:      false,
				EnableScanning:      true,
				MaxCriticalCVEs:     0,
				RequireSignature:    true,
			},
			scanner: &MockVulnerabilityScanner{
				criticalCount: 1,
			},
			verifier:        &MockSignatureVerifier{shouldFail: true},
			wantValidateErr: true,
			wantScanErr:     true,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			validator := NewDefaultImageValidator(tt.config)
			if tt.scanner != nil {
				validator.SetVulnerabilityScanner(tt.scanner)
			}
			if tt.verifier != nil {
				validator.SetSignatureVerifier(tt.verifier)
			}
			
			// Test validation
			err := validator.ValidateImage(context.Background(), tt.image)
			if (err != nil) != tt.wantValidateErr {
				t.Errorf("ValidateImage() error = %v, wantErr %v", err, tt.wantValidateErr)
			}
			
			// Test scanning if enabled
			if tt.config.EnableScanning {
				_, err = validator.ScanImage(context.Background(), tt.image)
				if (err != nil) != tt.wantScanErr {
					t.Errorf("ScanImage() error = %v, wantErr %v", err, tt.wantScanErr)
				}
			}
		})
	}
}

// Benchmark tests
func BenchmarkParseImageReference(b *testing.B) {
	images := []string{
		"nginx:latest",
		"docker.io/library/nginx:1.21",
		"registry.example.com:5000/namespace/app:v1.2.3",
		"nginx@sha256:1234567890abcdef1234567890abcdef1234567890abcdef1234567890abcdef",
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		image := images[i%len(images)]
		_, _ = ParseImageReference(image)
	}
}

func BenchmarkImageValidation(b *testing.B) {
	config := DefaultValidationConfig()
	validator := NewDefaultImageValidator(config)
	
	images := []string{
		"nginx:1.21",
		"docker.io/nginx:stable",
		"registry.example.com/app:v1.0.0",
	}
	
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		image := images[i%len(images)]
		_ = validator.ValidateImage(context.Background(), image)
	}
}

// Helper function
func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}