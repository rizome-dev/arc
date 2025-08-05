package validation

import (
	"context"
	"fmt"
	"log"
	"time"

	"github.com/rizome-dev/arc/pkg/types"
)

// OrchestratorIntegration provides integration between the validator and orchestrator
type OrchestratorIntegration struct {
	validator ImageValidator
	policies  map[string]Policy
	logger    *log.Logger
}

// NewOrchestratorIntegration creates a new orchestrator integration
func NewOrchestratorIntegration(validator ImageValidator, logger *log.Logger) *OrchestratorIntegration {
	if logger == nil {
		logger = log.Default()
	}
	
	return &OrchestratorIntegration{
		validator: validator,
		policies:  make(map[string]Policy),
		logger:    logger,
	}
}

// ValidateAgentImage validates an agent's container image before creation
func (o *OrchestratorIntegration) ValidateAgentImage(ctx context.Context, agent *types.Agent) error {
	o.logger.Printf("Validating image for agent %s: %s", agent.ID, agent.Image)
	
	// Basic validation
	if err := o.validator.ValidateImage(ctx, agent.Image); err != nil {
		o.auditLog("image_validation_failed", agent.ID, agent.Image, err.Error())
		return fmt.Errorf("image validation failed for agent %s: %w", agent.ID, err)
	}
	
	// Apply namespace-specific policy if available
	if policy, ok := o.policies[agent.Namespace]; ok {
		if err := o.validator.CheckPolicy(ctx, agent.Image, policy); err != nil {
			o.auditLog("policy_check_failed", agent.ID, agent.Image, err.Error())
			return fmt.Errorf("policy check failed for agent %s: %w", agent.ID, err)
		}
	}
	
	// Apply default policy
	if defaultPolicy, ok := o.policies["default"]; ok {
		if err := o.validator.CheckPolicy(ctx, agent.Image, defaultPolicy); err != nil {
			o.auditLog("default_policy_check_failed", agent.ID, agent.Image, err.Error())
			return fmt.Errorf("default policy check failed for agent %s: %w", agent.ID, err)
		}
	}
	
	o.auditLog("image_validated", agent.ID, agent.Image, "success")
	return nil
}

// ScanAndValidateImage performs security scanning and validation
func (o *OrchestratorIntegration) ScanAndValidateImage(ctx context.Context, image string) (*SecurityReport, error) {
	report := &SecurityReport{
		Image:     image,
		Timestamp: time.Now(),
		Status:    "pending",
	}
	
	// Perform validation
	if err := o.validator.ValidateImage(ctx, image); err != nil {
		report.Status = "failed"
		report.ValidationErrors = append(report.ValidationErrors, err.Error())
		return report, err
	}
	
	// Perform security scan
	scanResult, err := o.validator.ScanImage(ctx, image)
	if err != nil {
		report.Status = "failed"
		report.ScanErrors = append(report.ScanErrors, err.Error())
		return report, err
	}
	
	report.ScanResult = scanResult
	report.RiskScore = CalculateRiskScore(scanResult)
	
	// Determine overall status
	if scanResult.CriticalCount > 0 {
		report.Status = "rejected"
		report.Recommendation = "Image contains critical vulnerabilities and should not be used"
	} else if scanResult.HighCount > 5 {
		report.Status = "warning"
		report.Recommendation = "Image contains multiple high-severity vulnerabilities. Consider updating."
	} else {
		report.Status = "approved"
		report.Recommendation = "Image meets security requirements"
	}
	
	return report, nil
}

// ValidateWorkflowImages validates all images in a workflow before execution
func (o *OrchestratorIntegration) ValidateWorkflowImages(ctx context.Context, workflow *types.Workflow) error {
	images := make(map[string]bool)
	
	// Collect unique images from all tasks
	for _, task := range workflow.Tasks {
		// Assuming task has an Image field or it's in AgentConfig
		// This would need to be adapted based on actual task structure
		image := extractImageFromTask(task)
		if image != "" {
			images[image] = true
		}
	}
	
	// Validate each unique image
	for image := range images {
		if err := o.validator.ValidateImage(ctx, image); err != nil {
			return fmt.Errorf("workflow %s validation failed for image %s: %w", 
				workflow.ID, image, err)
		}
	}
	
	o.logger.Printf("Validated %d unique images for workflow %s", len(images), workflow.ID)
	return nil
}

// AddPolicy adds a policy for validation
func (o *OrchestratorIntegration) AddPolicy(name string, policy Policy) {
	o.policies[name] = policy
	o.logger.Printf("Added policy: %s", name)
}

// RemovePolicy removes a policy
func (o *OrchestratorIntegration) RemovePolicy(name string) {
	delete(o.policies, name)
	o.logger.Printf("Removed policy: %s", name)
}

// GetPolicy retrieves a policy by name
func (o *OrchestratorIntegration) GetPolicy(name string) (Policy, bool) {
	policy, ok := o.policies[name]
	return policy, ok
}

// auditLog logs security-related events for audit trail
func (o *OrchestratorIntegration) auditLog(event, agentID, image, details string) {
	o.logger.Printf("[AUDIT] event=%s agent=%s image=%s details=%s timestamp=%s",
		event, agentID, image, details, time.Now().Format(time.RFC3339))
}

// SecurityReport contains the complete security assessment of an image
type SecurityReport struct {
	Image             string       `json:"image"`
	Timestamp         time.Time    `json:"timestamp"`
	Status            string       `json:"status"` // "approved", "warning", "rejected", "failed"
	RiskScore         float64      `json:"risk_score"`
	ScanResult        *ScanResult  `json:"scan_result,omitempty"`
	ValidationErrors  []string     `json:"validation_errors,omitempty"`
	ScanErrors        []string     `json:"scan_errors,omitempty"`
	Recommendation    string       `json:"recommendation"`
	PolicyViolations  []string     `json:"policy_violations,omitempty"`
}

// extractImageFromTask extracts the image reference from a task
func extractImageFromTask(task types.Task) string {
	// This is a placeholder - actual implementation would depend on task structure
	// For now, we'll assume there's an Image field in AgentConfig
	// In reality, this might need to inspect the task's agent configuration
	return "" // Placeholder
}

// DefaultSecurityPolicies returns a set of default security policies
func DefaultSecurityPolicies() map[string]Policy {
	return map[string]Policy{
		"production": {
			Name:             "production",
			Description:      "Strict security policy for production environments",
			Enabled:          true,
			AllowLatestTag:   false,
			RequireDigest:    true,
			RequireSignature: true,
			MaxCriticalCVEs:  0,
			MaxHighCVEs:      0,
			MaxMediumCVEs:    10,
			MaxImageAge:      7 * 24 * time.Hour, // 7 days
			EnforcementMode:  "block",
			AllowedRegistries: []string{
				"docker.io",
				"gcr.io",
				"quay.io",
				"*.amazonaws.com", // ECR
			},
			TagPatternWhitelist: []string{
				`^v\d+\.\d+\.\d+$`,           // Semantic versioning
				`^release-\d{8}$`,            // Release dates
				`^[a-f0-9]{7,40}$`,           // Git commit SHA
			},
		},
		"development": {
			Name:            "development",
			Description:     "Relaxed policy for development environments",
			Enabled:         true,
			AllowLatestTag:  true,
			RequireDigest:   false,
			MaxCriticalCVEs: 5,
			MaxHighCVEs:     20,
			MaxImageAge:     30 * 24 * time.Hour, // 30 days
			EnforcementMode: "warn",
		},
		"testing": {
			Name:            "testing",
			Description:     "Moderate policy for testing environments",
			Enabled:         true,
			AllowLatestTag:  false,
			RequireDigest:   false,
			MaxCriticalCVEs: 0,
			MaxHighCVEs:     10,
			MaxMediumCVEs:   50,
			MaxImageAge:     14 * 24 * time.Hour, // 14 days
			EnforcementMode: "block",
			AllowedRegistries: []string{
				"docker.io",
				"gcr.io",
				"quay.io",
				"localhost:5000", // Local registry for testing
			},
		},
		"ci-cd": {
			Name:               "ci-cd",
			Description:        "Policy for CI/CD pipeline images",
			Enabled:            true,
			AllowLatestTag:     false,
			RequireDigest:      true,
			RequireSignature:   false,
			MaxCriticalCVEs:    0,
			MaxHighCVEs:        5,
			EnforcementMode:    "block",
			TagPatternWhitelist: []string{
				`^build-\d+$`,                // Build numbers
				`^pr-\d+$`,                   // Pull request numbers
				`^[a-f0-9]{7,40}$`,           // Git commit SHA
			},
		},
	}
}

// CreateValidatorWithDefaults creates a validator with sensible defaults for production
func CreateValidatorWithDefaults() (*DefaultImageValidator, error) {
	config := &ValidationConfig{
		// Registry settings
		RequireHTTPS: true,
		AllowedRegistries: []string{
			"docker.io",
			"gcr.io",
			"ghcr.io",
			"quay.io",
			"*.amazonaws.com",
		},
		BlockedRegistries: []string{
			"untrusted.example.com",
		},
		
		// Tag settings
		AllowLatestTag: false,
		RequireDigest:  false, // Can be enabled for extra security
		AllowedTagPatterns: []string{
			`^v\d+\.\d+\.\d+$`,   // Semantic versioning
			`^[a-f0-9]{7,40}$`,   // Git SHA
			`^\d{8}$`,            // Date format YYYYMMDD
		},
		BlockedTagPatterns: []string{
			`^dev.*`,
			`^test.*`,
			`^temp.*`,
		},
		
		// Security scanning
		EnableScanning:    true,
		MaxCriticalCVEs:   0,  // No critical CVEs allowed
		MaxHighCVEs:       5,
		MaxMediumCVEs:     20,
		MaxLowCVEs:        -1, // Unlimited
		ScanCacheDuration: 1 * time.Hour,
		
		// Image age
		MaxImageAge:        30 * 24 * time.Hour, // 30 days
		RequireRecentBuild: true,
		
		// Signature verification
		RequireSignature: false, // Enable when Cosign/Notary is configured
		SignatureType:    "cosign",
		
		// Performance
		ConcurrentScans: 3,
		ScanTimeout:     5 * time.Minute,
		
		// Logging
		LogLevel: "info",
		AuditLog: true,
	}
	
	validator := NewDefaultImageValidator(config)
	
	// Set up Trivy scanner if available
	trivyScanner := NewTrivyScanner("")
	validator.SetVulnerabilityScanner(trivyScanner)
	
	// Set up Cosign verifier if required
	if config.RequireSignature {
		cosignVerifier := NewCosignVerifier("")
		validator.SetSignatureVerifier(cosignVerifier)
	}
	
	return validator, nil
}

// ValidateImageForEnvironment validates an image based on the target environment
func ValidateImageForEnvironment(ctx context.Context, image, environment string) error {
	validator, err := CreateValidatorWithDefaults()
	if err != nil {
		return fmt.Errorf("failed to create validator: %w", err)
	}
	
	policies := DefaultSecurityPolicies()
	policy, ok := policies[environment]
	if !ok {
		// Fall back to development policy if environment not found
		policy = policies["development"]
	}
	
	// Validate against the environment-specific policy
	if err := validator.CheckPolicy(ctx, image, policy); err != nil {
		return fmt.Errorf("image %s failed %s environment policy: %w", image, environment, err)
	}
	
	return nil
}