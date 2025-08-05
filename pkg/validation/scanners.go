package validation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// TrivyScanner implements VulnerabilityScanner using Trivy
type TrivyScanner struct {
	binaryPath string
	timeout    time.Duration
}

// NewTrivyScanner creates a new Trivy-based vulnerability scanner
func NewTrivyScanner(binaryPath string) *TrivyScanner {
	if binaryPath == "" {
		binaryPath = "trivy"
	}
	return &TrivyScanner{
		binaryPath: binaryPath,
		timeout:    5 * time.Minute,
	}
}

// Scan performs a vulnerability scan using Trivy
func (t *TrivyScanner) Scan(ctx context.Context, image string) (*ScanResult, error) {
	// Sanitize image reference to prevent command injection
	safeImage := SanitizeImageReference(image)
	
	// Build Trivy command
	args := []string{
		"image",
		"--format", "json",
		"--quiet",
		"--no-progress",
		"--security-checks", "vuln",
		safeImage,
	}
	
	cmd := exec.CommandContext(ctx, t.binaryPath, args...)
	
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	
	// Run Trivy
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("trivy scan failed: %w, stderr: %s", err, stderr.String())
	}
	
	// Parse Trivy output
	var trivyResult TrivyOutput
	if err := json.Unmarshal(stdout.Bytes(), &trivyResult); err != nil {
		return nil, fmt.Errorf("failed to parse trivy output: %w", err)
	}
	
	// Convert to our ScanResult format
	return t.convertTrivyResult(&trivyResult, image), nil
}

// TrivyOutput represents Trivy JSON output structure
type TrivyOutput struct {
	SchemaVersion int                 `json:"SchemaVersion"`
	ArtifactName  string              `json:"ArtifactName"`
	ArtifactType  string              `json:"ArtifactType"`
	Metadata      TrivyMetadata       `json:"Metadata"`
	Results       []TrivyResult       `json:"Results"`
}

// TrivyMetadata contains image metadata from Trivy
type TrivyMetadata struct {
	OS           TrivyOS      `json:"OS"`
	ImageID      string       `json:"ImageID"`
	DiffIDs      []string     `json:"DiffIDs"`
	RepoTags     []string     `json:"RepoTags"`
	RepoDigests  []string     `json:"RepoDigests"`
	ImageConfig  interface{}  `json:"ImageConfig"`
}

// TrivyOS contains OS information
type TrivyOS struct {
	Family  string `json:"Family"`
	Name    string `json:"Name"`
	Version string `json:"Version"`
}

// TrivyResult contains scan results for a target
type TrivyResult struct {
	Target          string               `json:"Target"`
	Class           string               `json:"Class"`
	Type            string               `json:"Type"`
	Vulnerabilities []TrivyVulnerability `json:"Vulnerabilities"`
}

// TrivyVulnerability represents a vulnerability found by Trivy
type TrivyVulnerability struct {
	VulnerabilityID  string                 `json:"VulnerabilityID"`
	PkgName          string                 `json:"PkgName"`
	InstalledVersion string                 `json:"InstalledVersion"`
	FixedVersion     string                 `json:"FixedVersion"`
	Title            string                 `json:"Title"`
	Description      string                 `json:"Description"`
	Severity         string                 `json:"Severity"`
	CVSS             map[string]interface{} `json:"CVSS"`
	References       []string               `json:"References"`
	PublishedDate    *time.Time             `json:"PublishedDate"`
	LastModifiedDate *time.Time             `json:"LastModifiedDate"`
}

// convertTrivyResult converts Trivy output to our ScanResult format
func (t *TrivyScanner) convertTrivyResult(trivy *TrivyOutput, image string) *ScanResult {
	result := &ScanResult{
		ImageReference: image,
		ImageID:        trivy.Metadata.ImageID,
		ScanTimestamp:  time.Now(),
		Scanner:        "trivy",
		ScannerVersion: fmt.Sprintf("%d", trivy.SchemaVersion),
		OS:             trivy.Metadata.OS.Family,
		Vulnerabilities: []Vulnerability{},
	}
	
	// Extract digest if available
	if len(trivy.Metadata.RepoDigests) > 0 {
		result.ImageDigest = trivy.Metadata.RepoDigests[0]
	}
	
	// Process vulnerabilities
	for _, target := range trivy.Results {
		for _, vuln := range target.Vulnerabilities {
			v := Vulnerability{
				ID:           vuln.VulnerabilityID,
				Title:        vuln.Title,
				Description:  vuln.Description,
				Severity:     t.mapSeverity(vuln.Severity),
				Package:      vuln.PkgName,
				Version:      vuln.InstalledVersion,
				FixedVersion: vuln.FixedVersion,
				References:   vuln.References,
			}
			
			// Extract CVSS score if available
			if cvss, ok := vuln.CVSS["nvd"]; ok {
				if cvssMap, ok := cvss.(map[string]interface{}); ok {
					if score, ok := cvssMap["V3Score"].(float64); ok {
						v.CVSS = score
					}
					if vector, ok := cvssMap["V3Vector"].(string); ok {
						v.CVSSVector = vector
					}
				}
			}
			
			if vuln.PublishedDate != nil {
				v.PublishedDate = *vuln.PublishedDate
			}
			if vuln.LastModifiedDate != nil {
				v.LastModified = *vuln.LastModifiedDate
			}
			
			result.Vulnerabilities = append(result.Vulnerabilities, v)
			
			// Update counts
			switch v.Severity {
			case SeverityCritical:
				result.CriticalCount++
			case SeverityHigh:
				result.HighCount++
			case SeverityMedium:
				result.MediumCount++
			case SeverityLow:
				result.LowCount++
			default:
				result.UnknownCount++
			}
		}
	}
	
	result.TotalCount = len(result.Vulnerabilities)
	
	// Determine compliance status
	if result.CriticalCount > 0 {
		result.ComplianceStatus = "fail"
	} else if result.HighCount > 5 {
		result.ComplianceStatus = "warn"
	} else {
		result.ComplianceStatus = "pass"
	}
	
	return result
}

// mapSeverity maps Trivy severity to our Severity type
func (t *TrivyScanner) mapSeverity(trivySeverity string) Severity {
	switch strings.ToUpper(trivySeverity) {
	case "CRITICAL":
		return SeverityCritical
	case "HIGH":
		return SeverityHigh
	case "MEDIUM":
		return SeverityMedium
	case "LOW":
		return SeverityLow
	default:
		return SeverityUnknown
	}
}

// CosignVerifier implements SignatureVerifier using Cosign
type CosignVerifier struct {
	binaryPath string
	timeout    time.Duration
}

// NewCosignVerifier creates a new Cosign-based signature verifier
func NewCosignVerifier(binaryPath string) *CosignVerifier {
	if binaryPath == "" {
		binaryPath = "cosign"
	}
	return &CosignVerifier{
		binaryPath: binaryPath,
		timeout:    2 * time.Minute,
	}
}

// VerifySignature verifies an image signature using Cosign
func (c *CosignVerifier) VerifySignature(ctx context.Context, image string, trustAnchors []string) error {
	// Sanitize image reference
	safeImage := SanitizeImageReference(image)
	
	// Build cosign command
	args := []string{"verify"}
	
	// Add trust anchors (public keys or certificates)
	for _, anchor := range trustAnchors {
		args = append(args, "--key", anchor)
	}
	
	// Add image reference
	args = append(args, safeImage)
	
	cmd := exec.CommandContext(ctx, c.binaryPath, args...)
	
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	
	// Run cosign
	if err := cmd.Run(); err != nil {
		// Check if it's a verification failure
		if strings.Contains(stderr.String(), "no matching signatures") {
			return fmt.Errorf("no valid signature found for image %s", image)
		}
		if strings.Contains(stderr.String(), "signature verification failed") {
			return fmt.Errorf("signature verification failed for image %s", image)
		}
		return fmt.Errorf("cosign verify failed: %w, stderr: %s", err, stderr.String())
	}
	
	// Signature verified successfully
	return nil
}

// ClairScanner implements VulnerabilityScanner using Clair API
type ClairScanner struct {
	apiEndpoint string
	apiKey      string
	timeout     time.Duration
}

// NewClairScanner creates a new Clair-based vulnerability scanner
func NewClairScanner(endpoint, apiKey string) *ClairScanner {
	return &ClairScanner{
		apiEndpoint: endpoint,
		apiKey:      apiKey,
		timeout:     5 * time.Minute,
	}
}

// Scan performs a vulnerability scan using Clair API
func (c *ClairScanner) Scan(ctx context.Context, image string) (*ScanResult, error) {
	// This is a placeholder implementation
	// Actual implementation would:
	// 1. Submit image manifest to Clair
	// 2. Wait for analysis to complete
	// 3. Retrieve vulnerability report
	// 4. Convert to our ScanResult format
	
	return nil, fmt.Errorf("Clair scanner not implemented")
}

// NotaryVerifier implements SignatureVerifier using Notary
type NotaryVerifier struct {
	serverURL string
	timeout   time.Duration
}

// NewNotaryVerifier creates a new Notary-based signature verifier
func NewNotaryVerifier(serverURL string) *NotaryVerifier {
	return &NotaryVerifier{
		serverURL: serverURL,
		timeout:   2 * time.Minute,
	}
}

// VerifySignature verifies an image signature using Notary
func (n *NotaryVerifier) VerifySignature(ctx context.Context, image string, trustAnchors []string) error {
	// This is a placeholder implementation
	// Actual implementation would:
	// 1. Parse image reference
	// 2. Connect to Notary server
	// 3. Retrieve trust data for the image
	// 4. Verify signatures against trust anchors
	
	return fmt.Errorf("Notary verifier not implemented")
}

// GrypeScanner implements VulnerabilityScanner using Grype
type GrypeScanner struct {
	binaryPath string
	timeout    time.Duration
}

// NewGrypeScanner creates a new Grype-based vulnerability scanner
func NewGrypeScanner(binaryPath string) *GrypeScanner {
	if binaryPath == "" {
		binaryPath = "grype"
	}
	return &GrypeScanner{
		binaryPath: binaryPath,
		timeout:    5 * time.Minute,
	}
}

// Scan performs a vulnerability scan using Grype
func (g *GrypeScanner) Scan(ctx context.Context, image string) (*ScanResult, error) {
	// Sanitize image reference
	safeImage := SanitizeImageReference(image)
	
	// Build grype command
	args := []string{
		safeImage,
		"--output", "json",
		"--quiet",
	}
	
	cmd := exec.CommandContext(ctx, g.binaryPath, args...)
	
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	
	// Run grype
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("grype scan failed: %w, stderr: %s", err, stderr.String())
	}
	
	// Parse and convert grype output
	// This would be similar to the Trivy implementation
	// but adapted for Grype's JSON format
	
	return nil, fmt.Errorf("Grype scanner conversion not implemented")
}

// CompositeSigner implements SignatureVerifier with multiple verifiers
type CompositeVerifier struct {
	verifiers []SignatureVerifier
}

// NewCompositeVerifier creates a verifier that tries multiple verification methods
func NewCompositeVerifier(verifiers ...SignatureVerifier) *CompositeVerifier {
	return &CompositeVerifier{
		verifiers: verifiers,
	}
}

// VerifySignature tries to verify signature with any of the configured verifiers
func (c *CompositeVerifier) VerifySignature(ctx context.Context, image string, trustAnchors []string) error {
	if len(c.verifiers) == 0 {
		return fmt.Errorf("no signature verifiers configured")
	}
	
	var lastErr error
	for _, verifier := range c.verifiers {
		if err := verifier.VerifySignature(ctx, image, trustAnchors); err == nil {
			return nil // Signature verified successfully
		} else {
			lastErr = err
		}
	}
	
	return fmt.Errorf("signature verification failed with all verifiers: %w", lastErr)
}