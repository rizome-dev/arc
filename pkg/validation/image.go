// Package validation provides image validation and security scanning for ARC
package validation

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"time"
)

// Severity levels for vulnerabilities
type Severity string

const (
	SeverityCritical Severity = "critical"
	SeverityHigh     Severity = "high"
	SeverityMedium   Severity = "medium"
	SeverityLow      Severity = "low"
	SeverityUnknown  Severity = "unknown"
)

// ImageValidator defines the interface for image validation
type ImageValidator interface {
	// ValidateImage performs comprehensive validation on an image reference
	ValidateImage(ctx context.Context, image string) error
	
	// ScanImage performs security scanning on an image
	ScanImage(ctx context.Context, image string) (*ScanResult, error)
	
	// CheckPolicy validates an image against a specific policy
	CheckPolicy(ctx context.Context, image string, policy Policy) error
}

// DefaultImageValidator provides production-ready image validation
type DefaultImageValidator struct {
	config     *ValidationConfig
	mu         sync.RWMutex
	scanCache  map[string]*cachedScan
	sigVerifier SignatureVerifier
	scanner    VulnerabilityScanner
}

// cachedScan holds cached scan results
type cachedScan struct {
	result    *ScanResult
	timestamp time.Time
}

// ValidationConfig configures the image validator
type ValidationConfig struct {
	// Registry validation
	AllowedRegistries   []string      // Allowed registries (empty = all allowed)
	BlockedRegistries   []string      // Blocked registries
	RequireHTTPS        bool          // Require HTTPS for registry connections
	
	// Tag validation
	AllowLatestTag      bool          // Allow "latest" tag
	AllowedTagPatterns  []string      // Regex patterns for allowed tags
	BlockedTagPatterns  []string      // Regex patterns for blocked tags
	RequireDigest       bool          // Require digest in image reference
	
	// Security scanning
	EnableScanning      bool          // Enable vulnerability scanning
	MaxCriticalCVEs     int           // Max critical vulnerabilities (-1 = unlimited, 0 = none allowed)
	MaxHighCVEs         int           // Max high vulnerabilities (-1 = unlimited, 0 = none allowed)
	MaxMediumCVEs       int           // Max medium vulnerabilities (-1 = unlimited, 0 = none allowed)
	MaxLowCVEs          int           // Max low vulnerabilities (-1 = unlimited, 0 = none allowed)
	ScanCacheDuration   time.Duration // How long to cache scan results
	
	// Image age validation
	MaxImageAge         time.Duration // Max age for images (0 = no limit)
	RequireRecentBuild  bool          // Require images built within MaxImageAge
	
	// Signature verification
	RequireSignature    bool          // Require signed images
	TrustAnchors        []string      // Public keys or certificates for verification
	SignatureType       string        // "cosign", "notary", etc.
	
	// Performance
	ConcurrentScans     int           // Max concurrent vulnerability scans
	ScanTimeout         time.Duration // Timeout for individual scans
	
	// Logging
	LogLevel            string        // "debug", "info", "warn", "error"
	AuditLog            bool          // Enable audit logging
}

// Policy defines validation rules for images
type Policy struct {
	Name                string        `json:"name"`
	Description         string        `json:"description"`
	Enabled             bool          `json:"enabled"`
	
	// Registry rules
	AllowedRegistries   []string      `json:"allowed_registries,omitempty"`
	BlockedRegistries   []string      `json:"blocked_registries,omitempty"`
	
	// Tag rules
	AllowLatestTag      bool          `json:"allow_latest_tag"`
	RequireDigest       bool          `json:"require_digest"`
	TagPatternWhitelist []string      `json:"tag_pattern_whitelist,omitempty"`
	TagPatternBlacklist []string      `json:"tag_pattern_blacklist,omitempty"`
	
	// Security thresholds
	MaxCriticalCVEs     int           `json:"max_critical_cves"`
	MaxHighCVEs         int           `json:"max_high_cves"`
	MaxMediumCVEs       int           `json:"max_medium_cves"`
	MaxLowCVEs          int           `json:"max_low_cves"`
	
	// Image requirements
	MaxImageAge         time.Duration `json:"max_image_age,omitempty"`
	RequireSignature    bool          `json:"require_signature"`
	RequiredLabels      map[string]string `json:"required_labels,omitempty"`
	
	// Enforcement
	EnforcementMode     string        `json:"enforcement_mode"` // "block", "warn", "audit"
	Exceptions          []string      `json:"exceptions,omitempty"` // Image patterns to exempt
}

// ScanResult contains vulnerability scan findings
type ScanResult struct {
	ImageReference      string                 `json:"image_reference"`
	ImageID             string                 `json:"image_id"`
	ImageDigest         string                 `json:"image_digest"`
	ScanTimestamp       time.Time              `json:"scan_timestamp"`
	Scanner             string                 `json:"scanner"`
	ScannerVersion      string                 `json:"scanner_version"`
	
	// Vulnerability summary
	CriticalCount       int                    `json:"critical_count"`
	HighCount           int                    `json:"high_count"`
	MediumCount         int                    `json:"medium_count"`
	LowCount            int                    `json:"low_count"`
	UnknownCount        int                    `json:"unknown_count"`
	TotalCount          int                    `json:"total_count"`
	
	// Detailed findings
	Vulnerabilities     []Vulnerability        `json:"vulnerabilities"`
	
	// Image metadata
	OS                  string                 `json:"os"`
	Architecture        string                 `json:"architecture"`
	CreatedAt           time.Time              `json:"created_at"`
	Size                int64                  `json:"size"`
	Layers              []Layer                `json:"layers"`
	
	// Compliance
	ComplianceStatus    string                 `json:"compliance_status"` // "pass", "fail", "warn"
	ComplianceChecks    []ComplianceCheck      `json:"compliance_checks"`
	
	// Additional metadata
	Metadata            map[string]interface{} `json:"metadata,omitempty"`
}

// Vulnerability represents a security vulnerability
type Vulnerability struct {
	ID                  string                 `json:"id"`
	Title               string                 `json:"title"`
	Description         string                 `json:"description"`
	Severity            Severity               `json:"severity"`
	CVSS                float64                `json:"cvss"`
	CVSSVector          string                 `json:"cvss_vector"`
	CWE                 []string               `json:"cwe,omitempty"`
	
	// Affected components
	Package             string                 `json:"package"`
	Version             string                 `json:"version"`
	FixedVersion        string                 `json:"fixed_version,omitempty"`
	
	// References
	References          []string               `json:"references"`
	PublishedDate       time.Time              `json:"published_date"`
	LastModified        time.Time              `json:"last_modified"`
	
	// Exploitation
	Exploitable         bool                   `json:"exploitable"`
	ExploitAvailable    bool                   `json:"exploit_available"`
	ExploitMaturity     string                 `json:"exploit_maturity,omitempty"`
}

// Layer represents a container image layer
type Layer struct {
	Digest              string                 `json:"digest"`
	Size                int64                  `json:"size"`
	MediaType           string                 `json:"media_type"`
	CreatedBy           string                 `json:"created_by,omitempty"`
}

// ComplianceCheck represents a compliance validation
type ComplianceCheck struct {
	ID                  string                 `json:"id"`
	Name                string                 `json:"name"`
	Description         string                 `json:"description"`
	Status              string                 `json:"status"` // "pass", "fail", "skip"
	Severity            Severity               `json:"severity"`
	Details             string                 `json:"details,omitempty"`
}

// ImageInfo contains parsed image reference details
type ImageInfo struct {
	Registry            string                 `json:"registry"`
	Namespace           string                 `json:"namespace"`
	Repository          string                 `json:"repository"`
	Tag                 string                 `json:"tag"`
	Digest              string                 `json:"digest"`
	FullReference       string                 `json:"full_reference"`
	IsOfficial          bool                   `json:"is_official"`
	HasDigest           bool                   `json:"has_digest"`
	HasTag              bool                   `json:"has_tag"`
}

// SignatureVerifier interface for image signature verification
type SignatureVerifier interface {
	VerifySignature(ctx context.Context, image string, trustAnchors []string) error
}

// VulnerabilityScanner interface for vulnerability scanning
type VulnerabilityScanner interface {
	Scan(ctx context.Context, image string) (*ScanResult, error)
}

// NewDefaultImageValidator creates a new image validator with default config
func NewDefaultImageValidator(config *ValidationConfig) *DefaultImageValidator {
	if config == nil {
		config = DefaultValidationConfig()
	}
	
	return &DefaultImageValidator{
		config:    config,
		scanCache: make(map[string]*cachedScan),
	}
}

// DefaultValidationConfig returns a production-ready default configuration
func DefaultValidationConfig() *ValidationConfig {
	return &ValidationConfig{
		RequireHTTPS:      true,
		AllowLatestTag:    false,
		EnableScanning:    true,
		MaxCriticalCVEs:   0,  // No critical CVEs allowed
		MaxHighCVEs:       5,
		MaxMediumCVEs:     20,
		MaxLowCVEs:        -1, // Unlimited
		ScanCacheDuration: 1 * time.Hour,
		MaxImageAge:       30 * 24 * time.Hour, // 30 days
		ConcurrentScans:   3,
		ScanTimeout:       5 * time.Minute,
		LogLevel:          "info",
		AuditLog:          true,
	}
}

// ValidateImage performs comprehensive validation on an image reference
func (v *DefaultImageValidator) ValidateImage(ctx context.Context, image string) error {
	// Parse image reference
	imageInfo, err := ParseImageReference(image)
	if err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}
	
	// Validate registry
	if err := v.validateRegistry(imageInfo); err != nil {
		return fmt.Errorf("registry validation failed: %w", err)
	}
	
	// Validate tag
	if err := v.validateTag(imageInfo); err != nil {
		return fmt.Errorf("tag validation failed: %w", err)
	}
	
	// Check digest requirement
	if v.config.RequireDigest && !imageInfo.HasDigest {
		return fmt.Errorf("image digest required but not provided")
	}
	
	// Verify signature if required
	if v.config.RequireSignature {
		if v.sigVerifier == nil {
			return fmt.Errorf("signature verification required but no verifier configured")
		}
		if err := v.sigVerifier.VerifySignature(ctx, image, v.config.TrustAnchors); err != nil {
			return fmt.Errorf("signature verification failed: %w", err)
		}
	}
	
	return nil
}

// ScanImage performs security scanning on an image
func (v *DefaultImageValidator) ScanImage(ctx context.Context, image string) (*ScanResult, error) {
	if !v.config.EnableScanning {
		return nil, fmt.Errorf("scanning is disabled")
	}
	
	// Check cache
	v.mu.RLock()
	if cached, ok := v.scanCache[image]; ok {
		if time.Since(cached.timestamp) < v.config.ScanCacheDuration {
			v.mu.RUnlock()
			return cached.result, nil
		}
	}
	v.mu.RUnlock()
	
	// Perform scan
	if v.scanner == nil {
		return nil, fmt.Errorf("no vulnerability scanner configured")
	}
	
	scanCtx, cancel := context.WithTimeout(ctx, v.config.ScanTimeout)
	defer cancel()
	
	result, err := v.scanner.Scan(scanCtx, image)
	if err != nil {
		return nil, fmt.Errorf("vulnerability scan failed: %w", err)
	}
	
	// Cache result
	v.mu.Lock()
	v.scanCache[image] = &cachedScan{
		result:    result,
		timestamp: time.Now(),
	}
	v.mu.Unlock()
	
	// Check CVE thresholds
	if err := v.checkCVEThresholds(result); err != nil {
		return result, err
	}
	
	return result, nil
}

// CheckPolicy validates an image against a specific policy
func (v *DefaultImageValidator) CheckPolicy(ctx context.Context, image string, policy Policy) error {
	if !policy.Enabled {
		return nil
	}
	
	// Check if image is in exceptions
	for _, exception := range policy.Exceptions {
		if matched, _ := regexp.MatchString(exception, image); matched {
			return nil // Image is exempted from policy
		}
	}
	
	// Parse image reference
	imageInfo, err := ParseImageReference(image)
	if err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}
	
	// Check registry policy
	if len(policy.AllowedRegistries) > 0 {
		if !IsAllowedRegistry(imageInfo.Registry, policy.AllowedRegistries) {
			return fmt.Errorf("registry %s is not in allowed list", imageInfo.Registry)
		}
	}
	
	if len(policy.BlockedRegistries) > 0 {
		for _, blocked := range policy.BlockedRegistries {
			if imageInfo.Registry == blocked {
				return fmt.Errorf("registry %s is blocked by policy", imageInfo.Registry)
			}
		}
	}
	
	// Check tag policy
	if !policy.AllowLatestTag && imageInfo.Tag == "latest" {
		return fmt.Errorf("'latest' tag is not allowed by policy")
	}
	
	if policy.RequireDigest && !imageInfo.HasDigest {
		return fmt.Errorf("digest required by policy but not provided")
	}
	
	// Check tag patterns
	if len(policy.TagPatternWhitelist) > 0 {
		allowed := false
		for _, pattern := range policy.TagPatternWhitelist {
			if matched, _ := regexp.MatchString(pattern, imageInfo.Tag); matched {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("tag %s does not match allowed patterns", imageInfo.Tag)
		}
	}
	
	if len(policy.TagPatternBlacklist) > 0 {
		for _, pattern := range policy.TagPatternBlacklist {
			if matched, _ := regexp.MatchString(pattern, imageInfo.Tag); matched {
				return fmt.Errorf("tag %s matches blocked pattern %s", imageInfo.Tag, pattern)
			}
		}
	}
	
	// Perform security scan if thresholds are set (>= 0 means threshold is set)
	if policy.MaxCriticalCVEs >= 0 || policy.MaxHighCVEs >= 0 || 
	   policy.MaxMediumCVEs >= 0 || policy.MaxLowCVEs >= 0 {
		scanResult, err := v.ScanImage(ctx, image)
		if err != nil {
			if policy.EnforcementMode == "block" {
				return fmt.Errorf("security scan required by policy: %w", err)
			}
			// In warn or audit mode, log but don't block
		} else {
			// Check CVE counts against policy
			if policy.MaxCriticalCVEs >= 0 && scanResult.CriticalCount > policy.MaxCriticalCVEs {
				return fmt.Errorf("image has %d critical CVEs, policy allows max %d", 
					scanResult.CriticalCount, policy.MaxCriticalCVEs)
			}
			if policy.MaxHighCVEs >= 0 && scanResult.HighCount > policy.MaxHighCVEs {
				return fmt.Errorf("image has %d high CVEs, policy allows max %d", 
					scanResult.HighCount, policy.MaxHighCVEs)
			}
			if policy.MaxMediumCVEs >= 0 && scanResult.MediumCount > policy.MaxMediumCVEs {
				return fmt.Errorf("image has %d medium CVEs, policy allows max %d", 
					scanResult.MediumCount, policy.MaxMediumCVEs)
			}
			if policy.MaxLowCVEs >= 0 && scanResult.LowCount > policy.MaxLowCVEs {
				return fmt.Errorf("image has %d low CVEs, policy allows max %d", 
					scanResult.LowCount, policy.MaxLowCVEs)
			}
		}
	}
	
	// Check signature requirement
	if policy.RequireSignature {
		if v.sigVerifier == nil {
			return fmt.Errorf("signature verification required by policy but no verifier configured")
		}
		if err := v.sigVerifier.VerifySignature(ctx, image, v.config.TrustAnchors); err != nil {
			return fmt.Errorf("signature verification required by policy: %w", err)
		}
	}
	
	return nil
}

// validateRegistry checks if the registry is allowed
func (v *DefaultImageValidator) validateRegistry(imageInfo *ImageInfo) error {
	// Check allowed registries
	if len(v.config.AllowedRegistries) > 0 {
		if !IsAllowedRegistry(imageInfo.Registry, v.config.AllowedRegistries) {
			return fmt.Errorf("registry %s is not in allowed list", imageInfo.Registry)
		}
	}
	
	// Check blocked registries
	for _, blocked := range v.config.BlockedRegistries {
		if imageInfo.Registry == blocked {
			return fmt.Errorf("registry %s is blocked", imageInfo.Registry)
		}
	}
	
	// Check HTTPS requirement
	if v.config.RequireHTTPS && !strings.HasPrefix(imageInfo.Registry, "localhost") {
		// In production, registries should use HTTPS
		// This is a placeholder check - actual implementation would verify protocol
		if strings.Contains(imageInfo.Registry, "http://") {
			return fmt.Errorf("HTTPS required for registry %s", imageInfo.Registry)
		}
	}
	
	return nil
}

// validateTag checks if the tag meets requirements
func (v *DefaultImageValidator) validateTag(imageInfo *ImageInfo) error {
	// Check latest tag
	if !v.config.AllowLatestTag && imageInfo.Tag == "latest" {
		return fmt.Errorf("'latest' tag is not allowed")
	}
	
	// Check allowed patterns
	if len(v.config.AllowedTagPatterns) > 0 {
		allowed := false
		for _, pattern := range v.config.AllowedTagPatterns {
			if matched, _ := regexp.MatchString(pattern, imageInfo.Tag); matched {
				allowed = true
				break
			}
		}
		if !allowed {
			return fmt.Errorf("tag %s does not match allowed patterns", imageInfo.Tag)
		}
	}
	
	// Check blocked patterns
	for _, pattern := range v.config.BlockedTagPatterns {
		if matched, _ := regexp.MatchString(pattern, imageInfo.Tag); matched {
			return fmt.Errorf("tag %s matches blocked pattern %s", imageInfo.Tag, pattern)
		}
	}
	
	return nil
}

// checkCVEThresholds validates scan results against configured thresholds
func (v *DefaultImageValidator) checkCVEThresholds(result *ScanResult) error {
	// MaxCriticalCVEs == 0 means no critical CVEs allowed, -1 means unlimited
	if v.config.MaxCriticalCVEs >= 0 && result.CriticalCount > v.config.MaxCriticalCVEs {
		return fmt.Errorf("image has %d critical vulnerabilities (max: %d)", 
			result.CriticalCount, v.config.MaxCriticalCVEs)
	}
	
	if v.config.MaxHighCVEs >= 0 && result.HighCount > v.config.MaxHighCVEs {
		return fmt.Errorf("image has %d high vulnerabilities (max: %d)", 
			result.HighCount, v.config.MaxHighCVEs)
	}
	
	if v.config.MaxMediumCVEs >= 0 && result.MediumCount > v.config.MaxMediumCVEs {
		return fmt.Errorf("image has %d medium vulnerabilities (max: %d)", 
			result.MediumCount, v.config.MaxMediumCVEs)
	}
	
	if v.config.MaxLowCVEs >= 0 && result.LowCount > v.config.MaxLowCVEs {
		return fmt.Errorf("image has %d low vulnerabilities (max: %d)", 
			result.LowCount, v.config.MaxLowCVEs)
	}
	
	return nil
}

// SetSignatureVerifier sets the signature verifier
func (v *DefaultImageValidator) SetSignatureVerifier(verifier SignatureVerifier) {
	v.sigVerifier = verifier
}

// SetVulnerabilityScanner sets the vulnerability scanner
func (v *DefaultImageValidator) SetVulnerabilityScanner(scanner VulnerabilityScanner) {
	v.scanner = scanner
}

// ClearCache clears the scan result cache
func (v *DefaultImageValidator) ClearCache() {
	v.mu.Lock()
	defer v.mu.Unlock()
	v.scanCache = make(map[string]*cachedScan)
}

// ParseImageReference parses a container image reference
func ParseImageReference(image string) (*ImageInfo, error) {
	if image == "" {
		return nil, fmt.Errorf("empty image reference")
	}
	
	info := &ImageInfo{
		FullReference: image,
	}
	
	// Handle digest
	parts := strings.Split(image, "@")
	if len(parts) == 2 {
		info.HasDigest = true
		info.Digest = parts[1]
		image = parts[0]
		
		// Validate digest format (SHA256)
		if !strings.HasPrefix(info.Digest, "sha256:") {
			return nil, fmt.Errorf("invalid digest format: %s", info.Digest)
		}
		digestHex := strings.TrimPrefix(info.Digest, "sha256:")
		if len(digestHex) != 64 {
			return nil, fmt.Errorf("invalid SHA256 digest length: %s", digestHex)
		}
		if _, err := hex.DecodeString(digestHex); err != nil {
			return nil, fmt.Errorf("invalid digest hex encoding: %w", err)
		}
	}
	
	// Handle tag
	parts = strings.Split(image, ":")
	if len(parts) == 2 {
		info.HasTag = true
		info.Tag = parts[1]
		image = parts[0]
		
		// Validate tag format
		if !isValidTag(info.Tag) {
			return nil, fmt.Errorf("invalid tag format: %s", info.Tag)
		}
	} else if len(parts) > 2 {
		// Could be a registry with port
		lastColon := strings.LastIndex(image, ":")
		possibleTag := image[lastColon+1:]
		if isValidTag(possibleTag) {
			info.HasTag = true
			info.Tag = possibleTag
			image = image[:lastColon]
		}
	}
	
	// If no tag and no digest, default to latest
	if !info.HasTag && !info.HasDigest {
		info.Tag = "latest"
		info.HasTag = true
	}
	
	// Parse registry and repository
	slashCount := strings.Count(image, "/")
	
	if slashCount == 0 {
		// Official image (e.g., "nginx")
		info.Registry = "docker.io"
		info.Namespace = "library"
		info.Repository = image
		info.IsOfficial = true
	} else if slashCount == 1 {
		// Could be docker.io/namespace/repo or registry/repo
		parts = strings.Split(image, "/")
		if isRegistry(parts[0]) {
			info.Registry = parts[0]
			info.Repository = parts[1]
		} else {
			info.Registry = "docker.io"
			info.Namespace = parts[0]
			info.Repository = parts[1]
		}
	} else {
		// registry/namespace/repo or deeper nesting
		parts = strings.SplitN(image, "/", 2)
		info.Registry = parts[0]
		
		// The rest is namespace/repository
		repoParts := strings.Split(parts[1], "/")
		if len(repoParts) == 2 {
			info.Namespace = repoParts[0]
			info.Repository = repoParts[1]
		} else {
			// Deep nesting - treat everything after registry as repository
			info.Repository = parts[1]
		}
	}
	
	// Validate components
	if !isValidRegistryName(info.Registry) {
		return nil, fmt.Errorf("invalid registry name: %s", info.Registry)
	}
	
	if info.Namespace != "" && !isValidNamespace(info.Namespace) {
		return nil, fmt.Errorf("invalid namespace: %s", info.Namespace)
	}
	
	if !isValidRepository(info.Repository) {
		return nil, fmt.Errorf("invalid repository name: %s", info.Repository)
	}
	
	return info, nil
}

// isRegistry checks if a string looks like a registry
func isRegistry(s string) bool {
	// Check for localhost
	if s == "localhost" || strings.HasPrefix(s, "localhost:") {
		return true
	}
	
	// Check for IP address
	if strings.Count(s, ".") == 3 {
		// Might be an IP
		return true
	}
	
	// Check for port
	if strings.Contains(s, ":") {
		return true
	}
	
	// Check for domain-like structure
	if strings.Contains(s, ".") {
		return true
	}
	
	return false
}

// isValidTag validates a tag format
func isValidTag(tag string) bool {
	if tag == "" {
		return false
	}
	
	// Tag must be ASCII alphanumerics, dots, dashes, and underscores
	// Max length 128 characters
	if len(tag) > 128 {
		return false
	}
	
	validTag := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	return validTag.MatchString(tag)
}

// isValidRegistryName validates a registry name
func isValidRegistryName(registry string) bool {
	if registry == "" {
		return false
	}
	
	// Allow localhost
	if registry == "localhost" || strings.HasPrefix(registry, "localhost:") {
		return true
	}
	
	// Basic validation for registry format
	validRegistry := regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9.-]*[a-zA-Z0-9](:[0-9]+)?$`)
	return validRegistry.MatchString(registry)
}

// isValidNamespace validates a namespace
func isValidNamespace(namespace string) bool {
	if namespace == "" || len(namespace) > 255 {
		return false
	}
	
	// Namespace must be lowercase alphanumerics, dots, dashes, and underscores
	validNamespace := regexp.MustCompile(`^[a-z0-9]+([._-][a-z0-9]+)*$`)
	return validNamespace.MatchString(namespace)
}

// isValidRepository validates a repository name
func isValidRepository(repo string) bool {
	if repo == "" || len(repo) > 255 {
		return false
	}
	
	// Repository name must be lowercase alphanumerics, dots, dashes, underscores, and slashes
	validRepo := regexp.MustCompile(`^[a-z0-9]+([._/-][a-z0-9]+)*$`)
	return validRepo.MatchString(repo)
}

// IsAllowedRegistry checks if a registry is in the allowed list
func IsAllowedRegistry(registry string, allowedList []string) bool {
	if len(allowedList) == 0 {
		return true // No restrictions
	}
	
	for _, allowed := range allowedList {
		// Support wildcards
		if allowed == "*" {
			return true
		}
		
		// Support prefix matching with wildcard
		if strings.HasSuffix(allowed, "*") {
			prefix := strings.TrimSuffix(allowed, "*")
			if strings.HasPrefix(registry, prefix) {
				return true
			}
		}
		
		// Exact match
		if registry == allowed {
			return true
		}
	}
	
	return false
}

// HasCriticalVulnerabilities checks if scan result has critical vulnerabilities
func HasCriticalVulnerabilities(scanResult *ScanResult) bool {
	if scanResult == nil {
		return false
	}
	return scanResult.CriticalCount > 0
}

// GetVulnerabilitiesBySeverity returns vulnerabilities filtered by severity
func GetVulnerabilitiesBySeverity(scanResult *ScanResult, severity Severity) []Vulnerability {
	if scanResult == nil {
		return nil
	}
	
	var filtered []Vulnerability
	for _, vuln := range scanResult.Vulnerabilities {
		if vuln.Severity == severity {
			filtered = append(filtered, vuln)
		}
	}
	
	return filtered
}

// CalculateRiskScore calculates a risk score for scan results
func CalculateRiskScore(scanResult *ScanResult) float64 {
	if scanResult == nil {
		return 0
	}
	
	// Weighted scoring: Critical=10, High=5, Medium=2, Low=1
	score := float64(scanResult.CriticalCount)*10 +
		float64(scanResult.HighCount)*5 +
		float64(scanResult.MediumCount)*2 +
		float64(scanResult.LowCount)*1
	
	// Normalize to 0-100 scale
	if score > 100 {
		score = 100
	}
	
	return score
}

// GenerateImageHash generates a hash for an image reference
func GenerateImageHash(image string) string {
	h := sha256.New()
	h.Write([]byte(image))
	return hex.EncodeToString(h.Sum(nil))
}

// ValidateImageAge checks if an image is within acceptable age limits
func ValidateImageAge(createdAt time.Time, maxAge time.Duration) error {
	if maxAge == 0 {
		return nil // No age limit
	}
	
	age := time.Since(createdAt)
	if age > maxAge {
		return fmt.Errorf("image is %v old, exceeds maximum age of %v", age, maxAge)
	}
	
	return nil
}

// SanitizeImageReference removes potentially dangerous characters from image reference
func SanitizeImageReference(image string) string {
	// Remove any shell metacharacters
	dangerous := []string{";", "&", "|", "$", "`", "(", ")", "{", "}", "[", "]", "<", ">", "\\", "\"", "'", "\n", "\r", "\t"}
	
	sanitized := image
	for _, char := range dangerous {
		sanitized = strings.ReplaceAll(sanitized, char, "")
	}
	
	return sanitized
}