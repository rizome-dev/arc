// Package runtime provides gVisor runtime implementation
package runtime

import (
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	arctypes "github.com/rizome-dev/arc/pkg/types"
)

const (
	// Default configuration values
	defaultRootDir           = "/var/run/arc-gvisor"
	defaultImageCacheDir     = "/var/cache/arc-gvisor/images"
	defaultNetworkNamespace  = "arc-gvisor"
	defaultPlatform          = "systrap" // More performant than ptrace
	defaultLogLevel          = "warning"
	defaultContainerTimeout  = 30 * time.Second
	defaultHealthCheckInterval = 10 * time.Second
	
	// gVisor specific constants
	runscBinary = "runsc"
	configJSON  = "config.json"
	rootfsDir   = "rootfs"
)

// GVisorPlatform represents supported gVisor platforms
type GVisorPlatform string

const (
	PlatformPtrace  GVisorPlatform = "ptrace"  // Most compatible
	PlatformKVM     GVisorPlatform = "kvm"     // Hardware virtualization
	PlatformSystrap GVisorPlatform = "systrap" // Fastest, requires kernel 4.12+
)

// GVisorRuntime implements Runtime interface for gVisor with production features
type GVisorRuntime struct {
	config      GVisorConfig
	mu          sync.RWMutex
	agents      map[string]*gVisorAgent
	rootDir     string
	cacheDir    string
	logWriter   io.Writer
	healthCheck *time.Ticker
	ctx         context.Context
	cancel      context.CancelFunc
	
	// Platform detection
	platform       GVisorPlatform
	runscVersion   string
	capabilities   map[string]bool
	
	// Image management
	imageCache     map[string]*ImageCacheEntry
	imageCacheMu   sync.RWMutex
}

// GVisorConfig extends the base Config with gVisor-specific options
type GVisorConfig struct {
	Config // Embed base config
	
	// gVisor specific settings
	Platform         GVisorPlatform `json:"platform,omitempty"`
	Debug            bool           `json:"debug,omitempty"`
	LogLevel         string         `json:"log_level,omitempty"`
	EnableProfiling  bool           `json:"enable_profiling,omitempty"`
	
	// Security settings
	EnableSandbox         bool     `json:"enable_sandbox"`
	AllowedSyscalls      []string `json:"allowed_syscalls,omitempty"`
	BlockedSyscalls      []string `json:"blocked_syscalls,omitempty"`
	EnableSeccomp        bool     `json:"enable_seccomp"`
	EnableAppArmor       bool     `json:"enable_apparmor"`
	
	// Network settings
	NetworkMode      string   `json:"network_mode,omitempty"` // "none", "host", "bridge"
	EnableIPv6       bool     `json:"enable_ipv6,omitempty"`
	DNSServers       []string `json:"dns_servers,omitempty"`
	
	// Resource settings
	EnableCgroups    bool `json:"enable_cgroups"`
	CgroupsPath      string `json:"cgroups_path,omitempty"`
	
	// Image settings
	ImageCacheSize   int64  `json:"image_cache_size,omitempty"` // In bytes
	ImageCacheTTL    time.Duration `json:"image_cache_ttl,omitempty"`
	
	// Monitoring
	EnableHealthChecks bool          `json:"enable_health_checks"`
	HealthCheckInterval time.Duration `json:"health_check_interval,omitempty"`
}

// gVisorAgent represents a running gVisor container with enhanced metadata
type gVisorAgent struct {
	ID           string
	ContainerID  string
	Config       *arctypes.AgentConfig
	Status       arctypes.AgentStatus
	StartedAt    *time.Time
	FinishedAt   *time.Time
	ExitCode     int
	Error        string
	LogFile      string
	BundleDir    string
	PID          int
	
	// Enhanced metadata
	ImageDigest  string
	SecurityProfile string
	NetworkConfig   *NetworkConfig
	ResourceUsage   *ResourceUsage
	HealthStatus    *HealthStatus
	
	// Lifecycle management
	cmd          *exec.Cmd
	stopChan     chan struct{}
	healthTicker *time.Ticker
}

// ImageCacheEntry represents a cached container image
type ImageCacheEntry struct {
	Digest      string
	Size        int64
	LastUsed    time.Time
	Path        string
	Layers      []string
	Config      map[string]interface{} // Generic config, was *v1.Image
}

// NetworkConfig holds network configuration for a container
type NetworkConfig struct {
	Mode      string            `json:"mode"`
	IPAddress string            `json:"ip_address,omitempty"`
	Gateway   string            `json:"gateway,omitempty"`
	DNS       []string          `json:"dns,omitempty"`
	Ports     map[string]string `json:"ports,omitempty"`
}

// ResourceUsage tracks resource consumption
type ResourceUsage struct {
	CPUUsage    float64   `json:"cpu_usage"`
	MemoryUsage int64     `json:"memory_usage"`
	DiskUsage   int64     `json:"disk_usage"`
	NetworkRx   int64     `json:"network_rx"`
	NetworkTx   int64     `json:"network_tx"`
	UpdatedAt   time.Time `json:"updated_at"`
}

// HealthStatus represents container health information  
type HealthStatus struct {
	Status      string    `json:"status"` // healthy, unhealthy, starting
	LastCheck   time.Time `json:"last_check"`
	FailureCount int      `json:"failure_count"`
	Output      string    `json:"output,omitempty"`
}

// gVisorContainerState represents the state returned by runsc
type gVisorContainerState struct {
	ID       string `json:"id"`
	Status   string `json:"status"`
	PID      int    `json:"pid"`
	Bundle   string `json:"bundle"`
	Created  string `json:"created"`
	Owner    string `json:"owner"`
}

// NewGVisorRuntime creates a new production-ready gVisor runtime
func NewGVisorRuntime(config Config) (*GVisorRuntime, error) {
	// Convert base config to GVisorConfig
	gvisorConfig := GVisorConfig{
		Config:              config,
		Platform:            PlatformSystrap,
		Debug:               false,
		LogLevel:            defaultLogLevel,
		EnableSandbox:       true,
		NetworkMode:         "bridge",
		EnableCgroups:       true,
		ImageCacheSize:      1024 * 1024 * 1024, // 1GB
		ImageCacheTTL:       24 * time.Hour,
		EnableHealthChecks:  true,
		HealthCheckInterval: defaultHealthCheckInterval,
		EnableSeccomp:       true,
	}

	return NewGVisorRuntimeWithConfig(gvisorConfig)
}

// NewGVisorRuntimeWithConfig creates a new gVisor runtime with detailed configuration
func NewGVisorRuntimeWithConfig(config GVisorConfig) (*GVisorRuntime, error) {
	// Validate and setup runsc
	if err := validateRunscInstallation(); err != nil {
		return nil, fmt.Errorf("runsc validation failed: %w", err)
	}

	// Detect platform capabilities
	platform, capabilities, err := detectPlatformCapabilities(config.Platform)
	if err != nil {
		return nil, fmt.Errorf("platform detection failed: %w", err)
	}

	// Get runsc version
	version, err := getRunscVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get runsc version: %w", err)
	}

	// Setup directories
	rootDir := defaultRootDir
	cacheDir := defaultImageCacheDir
	
	if err := setupDirectories(rootDir, cacheDir); err != nil {
		return nil, fmt.Errorf("failed to setup directories: %w", err)
	}

	// Create context for runtime lifecycle
	ctx, cancel := context.WithCancel(context.Background())

	runtime := &GVisorRuntime{
		config:         config,
		agents:         make(map[string]*gVisorAgent),
		rootDir:        rootDir,
		cacheDir:       cacheDir,
		logWriter:      os.Stdout,
		ctx:            ctx,
		cancel:         cancel,
		platform:       platform,
		runscVersion:   version,
		capabilities:   capabilities,
		imageCache:     make(map[string]*ImageCacheEntry),
	}

	// Start health check routine if enabled
	if config.EnableHealthChecks {
		runtime.startHealthCheckRoutine()
	}

	// Start cache cleanup routine
	runtime.startCacheCleanupRoutine()

	return runtime, nil
}

// validateRunscInstallation checks if runsc is properly installed
func validateRunscInstallation() error {
	// Check if runsc is in PATH
	runscPath, err := exec.LookPath(runscBinary)
	if err != nil {
		return fmt.Errorf(`runsc not found in PATH. Please install gVisor:
		
Ubuntu/Debian:
  curl -fsSL https://gvisor.dev/archive.key | sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
  echo "deb [arch=$(dpkg --print-architecture) signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" | sudo tee /etc/apt/sources.list.d/gvisor.list > /dev/null
  sudo apt-get update && sudo apt-get install -y runsc

CentOS/RHEL/Fedora:
  sudo yum install -y dnf-plugins-core
  sudo dnf config-manager --add-repo https://storage.googleapis.com/gvisor/releases/rpm/gvisor.repo
  sudo dnf install -y runsc

Manual installation:
  wget https://storage.googleapis.com/gvisor/releases/release/latest/x86_64/runsc
  chmod +x runsc && sudo mv runsc /usr/local/bin/

Error: %v`, err)
	}

	// Verify runsc can execute
	cmd := exec.Command(runscPath, "--version")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("runsc executable verification failed: %w", err)
	}

	return nil
}

// detectPlatformCapabilities detects the best platform and available capabilities
func detectPlatformCapabilities(preferredPlatform GVisorPlatform) (GVisorPlatform, map[string]bool, error) {
	capabilities := make(map[string]bool)
	
	// Test platforms in order of preference (if not specified)
	platforms := []GVisorPlatform{preferredPlatform}
	if preferredPlatform == "" {
		platforms = []GVisorPlatform{PlatformSystrap, PlatformKVM, PlatformPtrace}
	}

	for _, platform := range platforms {
		if testPlatform(platform) {
			capabilities["platform_"+string(platform)] = true
			
			// Test additional capabilities
			capabilities["networking"] = testNetworking()
			capabilities["cgroups"] = testCgroups()
			capabilities["seccomp"] = testSeccomp()
			
			return platform, capabilities, nil
		}
	}

	return PlatformPtrace, capabilities, fmt.Errorf("no suitable platform found")
}

// testPlatform checks if a specific platform works
func testPlatform(platform GVisorPlatform) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	
	cmd := exec.CommandContext(ctx, runscBinary, "--platform", string(platform), "version")
	return cmd.Run() == nil
}

// testNetworking checks if network namespaces are supported
func testNetworking() bool {
	// Check if we can create network namespaces
	_, err := os.Stat("/proc/sys/net")
	return err == nil
}

// testCgroups checks if cgroups are available
func testCgroups() bool {
	// Check for cgroups v1 or v2
	paths := []string{"/sys/fs/cgroup/memory", "/sys/fs/cgroup/unified"}
	for _, path := range paths {
		if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	return false
}

// testSeccomp checks if seccomp is available
func testSeccomp() bool {
	_, err := os.Stat("/proc/sys/kernel/seccomp")
	return err == nil
}

// getRunscVersion gets the runsc version string
func getRunscVersion() (string, error) {
	cmd := exec.Command(runscBinary, "version")
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(output)), nil
}

// setupDirectories creates necessary directories with proper permissions
func setupDirectories(rootDir, cacheDir string) error {
	dirs := []string{rootDir, cacheDir}
	
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0755); err != nil {
			return fmt.Errorf("failed to create directory %s: %w", dir, err)
		}
		
		// Ensure proper ownership (root:root for security)
		if os.Getuid() == 0 {
			if err := os.Chown(dir, 0, 0); err != nil {
				return fmt.Errorf("failed to set ownership for %s: %w", dir, err)
			}
		}
	}
	
	return nil
}

// CreateAgent creates a new agent container using gVisor
func (r *GVisorRuntime) CreateAgent(ctx context.Context, agent *arctypes.Agent) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Generate unique container ID
	containerID := fmt.Sprintf("arc-%s-%d", agent.ID, time.Now().Unix())
	
	// Create bundle directory
	bundleDir := filepath.Join(r.rootDir, containerID)
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		return fmt.Errorf("failed to create bundle directory: %w", err)
	}

	// Create rootfs directory
	rootfsPath := filepath.Join(bundleDir, rootfsDir)
	if err := os.MkdirAll(rootfsPath, 0755); err != nil {
		return fmt.Errorf("failed to create rootfs directory: %w", err)
	}

	// Extract and cache image
	imageDigest, err := r.extractImageAdvanced(ctx, agent.Image, rootfsPath)
	if err != nil {
		return fmt.Errorf("failed to extract image %s: %w", agent.Image, err)
	}

	// Create enhanced OCI config
	ociConfig, err := r.createAdvancedOCIConfig(agent, rootfsPath)
	if err != nil {
		return fmt.Errorf("failed to create OCI config: %w", err)
	}

	// Write OCI config
	configPath := filepath.Join(bundleDir, configJSON)
	configData, err := json.MarshalIndent(ociConfig, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal OCI config: %w", err)
	}
	if err := os.WriteFile(configPath, configData, 0644); err != nil {
		return fmt.Errorf("failed to write OCI config: %w", err)
	}

	// Create log file
	logFile := filepath.Join(bundleDir, "container.log")

	// Setup network configuration
	networkConfig := &NetworkConfig{
		Mode: r.config.NetworkMode,
		DNS:  r.config.DNSServers,
	}

	// Store agent information with enhanced metadata
	r.agents[agent.ID] = &gVisorAgent{
		ID:              agent.ID,
		ContainerID:     containerID,
		Config:          &agent.Config,
		Status:          arctypes.AgentStatusCreating,
		LogFile:         logFile,
		BundleDir:       bundleDir,
		ImageDigest:     imageDigest,
		SecurityProfile: "gvisor-default",
		NetworkConfig:   networkConfig,
		ResourceUsage:   &ResourceUsage{},
		HealthStatus:    &HealthStatus{Status: "starting"},
		stopChan:        make(chan struct{}),
	}

	// Update agent metadata
	agent.ContainerID = containerID
	agent.Status = arctypes.AgentStatusCreating
	agent.CreatedAt = time.Now()

	return nil
}

// extractImageAdvanced extracts container images using our lightweight OCI puller
func (r *GVisorRuntime) extractImageAdvanced(ctx context.Context, imageSpec string, rootfsDir string) (string, error) {
	// Check cache first
	digest := r.calculateImageDigest(imageSpec)
	if cached, exists := r.getCachedImage(digest); exists {
		if err := r.extractCachedImage(cached, rootfsDir); err == nil {
			return digest, nil
		}
		// Cache invalid, remove it
		r.removeCachedImage(digest)
	}

	// Create OCI puller
	puller := NewOCIPuller(r.cacheDir)
	
	// Pull and extract the image
	if err := puller.PullImage(ctx, imageSpec, rootfsDir); err != nil {
		// If OCI pull fails, try local tar extraction as fallback
		if strings.HasSuffix(imageSpec, ".tar") || strings.HasSuffix(imageSpec, ".tar.gz") {
			if err := puller.ExtractLocalImage(ctx, imageSpec, rootfsDir); err != nil {
				return "", fmt.Errorf("failed to extract local image: %w", err)
			}
		} else {
			return "", fmt.Errorf("failed to pull image: %w", err)
		}
	}

	// Cache the extracted image
	cacheEntry := &ImageCacheEntry{
		Digest:   digest,
		Size:     r.calculateDirectorySize(rootfsDir),
		LastUsed: time.Now(),
		Path:     rootfsDir,
		Layers:   []string{}, // Layers tracked by OCI puller internally
	}
	r.cacheImage(digest, cacheEntry)

	return digest, nil
}

// extractOCILayers extracts OCI image layers to rootfs
func (r *GVisorRuntime) extractOCILayers(ociDir, rootfsDir string) error {
	// Read OCI index
	indexPath := filepath.Join(ociDir, "index.json")
	indexData, err := os.ReadFile(indexPath)
	if err != nil {
		return fmt.Errorf("failed to read OCI index: %w", err)
	}

	// Parse OCI index as generic JSON
	var index map[string]interface{}
	if err := json.Unmarshal(indexData, &index); err != nil {
		return fmt.Errorf("failed to parse OCI index: %w", err)
	}

	// Get manifests array
	manifests, ok := index["manifests"].([]interface{})
	if !ok || len(manifests) == 0 {
		return fmt.Errorf("no manifests found in OCI index")
	}

	// Get first manifest
	firstManifest, ok := manifests[0].(map[string]interface{})
	if !ok {
		return fmt.Errorf("invalid manifest format")
	}

	// Read manifest
	manifestDigest, ok := firstManifest["digest"].(string)
	if !ok {
		return fmt.Errorf("manifest digest not found")
	}
	manifestPath := filepath.Join(ociDir, "blobs", strings.Replace(manifestDigest, ":", "/", 1))
	manifestData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	// Parse manifest as generic JSON
	var manifest map[string]interface{}
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Extract each layer
	layers, ok := manifest["layers"].([]interface{})
	if !ok {
		return fmt.Errorf("no layers found in manifest")
	}

	for i, layerIface := range layers {
		layer, ok := layerIface.(map[string]interface{})
		if !ok {
			return fmt.Errorf("invalid layer format at index %d", i)
		}
		
		digest, ok := layer["digest"].(string)
		if !ok {
			return fmt.Errorf("layer digest not found at index %d", i)
		}
		
		layerPath := filepath.Join(ociDir, "blobs", strings.Replace(digest, ":", "/", 1))
		if err := r.extractLayer(layerPath, rootfsDir); err != nil {
			return fmt.Errorf("failed to extract layer %s: %w", digest, err)
		}
	}

	return nil
}

// extractLayer extracts a single OCI layer to the rootfs
func (r *GVisorRuntime) extractLayer(layerPath, rootfsDir string) error {
	layerFile, err := os.Open(layerPath)
	if err != nil {
		return fmt.Errorf("failed to open layer file: %w", err)
	}
	defer layerFile.Close()

	// Check if layer is gzipped
	var reader io.Reader = layerFile
	if filepath.Ext(layerPath) == ".gz" || r.isGzipped(layerFile) {
		layerFile.Seek(0, 0) // Reset file position
		gzReader, err := gzip.NewReader(layerFile)
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	// Extract tar archive
	cmd := exec.Command("tar", "-xf", "-", "-C", rootfsDir)
	cmd.Stdin = reader
	cmd.Stderr = os.Stderr
	
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to extract tar: %w", err)
	}

	return nil
}

// isGzipped checks if a file is gzipped by reading magic bytes
func (r *GVisorRuntime) isGzipped(file *os.File) bool {
	buf := make([]byte, 2)
	n, err := file.Read(buf)
	if err != nil || n < 2 {
		return false
	}
	return buf[0] == 0x1f && buf[1] == 0x8b
}

// StartAgent starts the agent container with enhanced security and monitoring
func (r *GVisorRuntime) StartAgent(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Build comprehensive runsc command
	args := r.buildRunscArgs(agent)

	// Create runsc command with proper context
	cmd := exec.CommandContext(ctx, runscBinary, args...)
	cmd.Dir = agent.BundleDir

	// Setup logging
	logFile, err := os.Create(agent.LogFile)
	if err != nil {
		return fmt.Errorf("failed to create log file: %w", err)
	}
	defer logFile.Close()

	cmd.Stdout = logFile
	cmd.Stderr = logFile

	// Start the container
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start container: %w", err)
	}

	// Store command reference for lifecycle management
	agent.cmd = cmd
	agent.PID = cmd.Process.Pid

	// Update agent status
	now := time.Now()
	agent.Status = arctypes.AgentStatusRunning
	agent.StartedAt = &now
	agent.HealthStatus.Status = "starting"
	agent.HealthStatus.LastCheck = now

	// Start monitoring goroutines
	go r.monitorAgent(agent)
	if r.config.EnableHealthChecks {
		go r.healthCheckAgent(agent)
	}

	return nil
}

// buildRunscArgs builds comprehensive runsc command arguments
func (r *GVisorRuntime) buildRunscArgs(agent *gVisorAgent) []string {
	args := []string{"run"}

	// Basic configuration
	args = append(args, "--bundle", agent.BundleDir)
	args = append(args, "--platform", string(r.platform))

	// Debug and logging
	if r.config.Debug {
		args = append(args, "--debug")
		args = append(args, "--debug-log", filepath.Join(agent.BundleDir, "debug.log"))
	}
	args = append(args, "--log-level", r.config.LogLevel)

	// Network configuration
	switch r.config.NetworkMode {
	case "host":
		args = append(args, "--network", "host")
	case "none":
		args = append(args, "--network", "none")
	default: // bridge
		args = append(args, "--network", "sandbox")
	}

	// Resource limits
	if agent.Config.Resources.CPU != "" {
		cpuShares := r.parseCPUToShares(agent.Config.Resources.CPU)
		args = append(args, "--cpu-shares", fmt.Sprintf("%d", cpuShares))
	}
	if agent.Config.Resources.Memory != "" {
		memBytes := r.parseMemoryLimitToBytes(agent.Config.Resources.Memory)
		args = append(args, "--total-memory", fmt.Sprintf("%d", memBytes))
	}

	// Security features
	if r.config.EnableSeccomp {
		args = append(args, "--seccomp-filter", "default")
	}

	// Cgroups
	if r.config.EnableCgroups {
		cgroupPath := r.config.CgroupsPath
		if cgroupPath == "" {
			cgroupPath = fmt.Sprintf("/arc/%s", agent.ContainerID)
		}
		args = append(args, "--systemd-cgroup", "--cgroup-parent", cgroupPath)
	}

	// Profiling
	if r.config.EnableProfiling {
		args = append(args, "--profile-cpu", filepath.Join(agent.BundleDir, "cpu.prof"))
		args = append(args, "--profile-heap", filepath.Join(agent.BundleDir, "heap.prof"))
	}

	// Container ID (must be last)
	args = append(args, agent.ContainerID)

	return args
}

// createAdvancedOCIConfig creates a comprehensive OCI runtime specification
func (r *GVisorRuntime) createAdvancedOCIConfig(agent *arctypes.Agent, rootfsPath string) (map[string]interface{}, error) {
	// Build command and args
	args := agent.Config.Command
	if len(agent.Config.Args) > 0 {
		args = append(args, agent.Config.Args...)
	}

	// Build environment variables
	env := []string{
		"PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOSTNAME=" + agent.ID,
	}
	for k, v := range agent.Config.Environment {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}

	// Build mounts with enhanced security
	mounts := []map[string]interface{}{
		{
			"destination": "/proc",
			"type":        "proc",
			"source":      "proc",
			"options":     []string{"nosuid", "noexec", "nodev"},
		},
		{
			"destination": "/dev",
			"type":        "tmpfs",
			"source":      "tmpfs",
			"options":     []string{"nosuid", "strictatime", "mode=755", "size=65536k"},
		},
		{
			"destination": "/dev/pts",
			"type":        "devpts",
			"source":      "devpts",
			"options":     []string{"nosuid", "noexec", "newinstance", "ptmxmode=0666", "mode=0620"},
		},
		{
			"destination": "/dev/shm",
			"type":        "tmpfs",
			"source":      "shm",
			"options":     []string{"nosuid", "noexec", "nodev", "mode=1777", "size=65536k"},
		},
		{
			"destination": "/sys",
			"type":        "sysfs",
			"source":      "sysfs",
			"options":     []string{"nosuid", "noexec", "nodev", "ro"},
		},
		{
			"destination": "/tmp",
			"type":        "tmpfs",
			"source":      "tmpfs",
			"options":     []string{"nosuid", "nodev", "mode=1777"},
		},
	}

	// Add custom volume mounts
	for _, vol := range agent.Config.Volumes {
		options := []string{"rbind", "rprivate"}
		if vol.ReadOnly {
			options = append(options, "ro")
		}
		mount := map[string]interface{}{
			"destination": vol.MountPath,
			"type":        "bind",
			"source":      vol.Name,
			"options":     options,
		}
		mounts = append(mounts, mount)
	}

	// Build resource limits
	resources := map[string]interface{}{
		"devices": []map[string]interface{}{
			{
				"allow":  false,
				"access": "rwm",
			},
		},
	}

	// Memory limits
	if agent.Config.Resources.Memory != "" {
		memBytes := r.parseMemoryLimitToBytes(agent.Config.Resources.Memory)
		resources["memory"] = map[string]interface{}{
			"limit": memBytes,
		}
	}

	// CPU limits
	if agent.Config.Resources.CPU != "" {
		cpuShares := r.parseCPUToShares(agent.Config.Resources.CPU)
		resources["cpu"] = map[string]interface{}{
			"shares": cpuShares,
		}
	}

	// Enhanced security settings
	capabilities := []string{
		"CAP_AUDIT_WRITE",
		"CAP_KILL",
		"CAP_NET_BIND_SERVICE",
	}

	// Seccomp profile
	var seccomp map[string]interface{}
	if r.config.EnableSeccomp {
		seccomp = r.createSeccompProfile()
	}

	// AppArmor profile
	var apparmorProfile string
	if r.config.EnableAppArmor {
		apparmorProfile = "arc-gvisor-default"
	}

	// Create the full OCI spec
	spec := map[string]interface{}{
		"ociVersion": "1.0.2",
		"process": map[string]interface{}{
			"terminal": false,
			"user": map[string]interface{}{
				"uid": 0,
				"gid": 0,
			},
			"args": args,
			"env":  env,
			"cwd":  "/",
			"capabilities": map[string]interface{}{
				"bounding":    capabilities,
				"effective":   capabilities,
				"inheritable": capabilities,
				"permitted":   capabilities,
			},
			"rlimits": []map[string]interface{}{
				{
					"type": "RLIMIT_NOFILE",
					"hard": 1024,
					"soft": 1024,
				},
				{
					"type": "RLIMIT_NPROC",
					"hard": 1024,
					"soft": 1024,
				},
			},
			"noNewPrivileges": true,
			"apparmorProfile": apparmorProfile,
		},
		"root": map[string]interface{}{
			"path":     rootfsPath,
			"readonly": false,
		},
		"hostname": agent.ID,
		"mounts":   mounts,
		"linux": map[string]interface{}{
			"resources": resources,
			"namespaces": []map[string]interface{}{
				{"type": "pid"},
				{"type": "network"},
				{"type": "ipc"},
				{"type": "uts"},
				{"type": "mount"},
				{"type": "user"},
			},
			"maskedPaths": []string{
				"/proc/kcore",
				"/proc/latency_stats",
				"/proc/timer_list",
				"/proc/timer_stats",
				"/proc/sched_debug",
				"/proc/scsi",
				"/sys/firmware",
				"/sys/fs/selinux",
			},
			"readonlyPaths": []string{
				"/proc/asound",
				"/proc/bus",
				"/proc/fs",
				"/proc/irq",
				"/proc/sys",
				"/proc/sysrq-trigger",
			},
			"seccomp": seccomp,
		},
	}

	return spec, nil
}

// createSeccompProfile creates a default seccomp security profile
func (r *GVisorRuntime) createSeccompProfile() map[string]interface{} {
	return map[string]interface{}{
		"defaultAction": "SCMP_ACT_ERRNO",
		"architectures": []string{"SCMP_ARCH_X86_64", "SCMP_ARCH_X86", "SCMP_ARCH_X32"},
		"syscalls": []map[string]interface{}{
			// Allow basic syscalls
			{"names": []string{"read", "write", "open", "close"}, "action": "SCMP_ACT_ALLOW"},
			{"names": []string{"stat", "fstat", "lstat"}, "action": "SCMP_ACT_ALLOW"},
			{"names": []string{"poll", "select", "epoll_wait"}, "action": "SCMP_ACT_ALLOW"},
			{"names": []string{"mmap", "munmap", "mprotect"}, "action": "SCMP_ACT_ALLOW"},
			{"names": []string{"brk", "rt_sigaction", "rt_sigprocmask"}, "action": "SCMP_ACT_ALLOW"},
			{"names": []string{"ioctl"}, "action": "SCMP_ACT_ALLOW"},
			{"names": []string{"exit", "exit_group"}, "action": "SCMP_ACT_ALLOW"},
		},
	}
}

// StopAgent stops the agent container gracefully
func (r *GVisorRuntime) StopAgent(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Signal stop to monitoring goroutines
	close(agent.stopChan)

	// Stop health checks
	if agent.healthTicker != nil {
		agent.healthTicker.Stop()
	}

	// Try graceful shutdown first
	if err := r.signalContainer(ctx, agent.ContainerID, syscall.SIGTERM); err != nil {
		// If graceful shutdown fails, force kill
		if err := r.signalContainer(ctx, agent.ContainerID, syscall.SIGKILL); err != nil {
			return fmt.Errorf("failed to stop container: %w", err)
		}
	}

	// Wait for container to stop with timeout
	stopCtx, cancel := context.WithTimeout(ctx, defaultContainerTimeout)
	defer cancel()

	if err := r.waitForContainerStop(stopCtx, agent.ContainerID); err != nil {
		return fmt.Errorf("container did not stop within timeout: %w", err)
	}

	// Clean up container
	if err := r.deleteContainer(ctx, agent.ContainerID); err != nil {
		// Log error but don't fail the operation
		fmt.Fprintf(r.logWriter, "Warning: failed to delete container %s: %v\n", agent.ContainerID, err)
	}

	// Update agent status
	agent.Status = arctypes.AgentStatusTerminated
	if agent.FinishedAt == nil {
		now := time.Now()
		agent.FinishedAt = &now
	}

	return nil
}

// signalContainer sends a signal to the container
func (r *GVisorRuntime) signalContainer(ctx context.Context, containerID string, signal syscall.Signal) error {
	cmd := exec.CommandContext(ctx, runscBinary, "kill", containerID, signal.String())
	return cmd.Run()
}

// waitForContainerStop waits for a container to stop
func (r *GVisorRuntime) waitForContainerStop(ctx context.Context, containerID string) error {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			state, err := r.getContainerState(ctx, containerID)
			if err != nil || state.Status == "stopped" {
				return nil
			}
		}
	}
}

// deleteContainer removes a container
func (r *GVisorRuntime) deleteContainer(ctx context.Context, containerID string) error {
	cmd := exec.CommandContext(ctx, runscBinary, "delete", containerID)
	return cmd.Run()
}

// getContainerState gets the current state of a container
func (r *GVisorRuntime) getContainerState(ctx context.Context, containerID string) (*gVisorContainerState, error) {
	cmd := exec.CommandContext(ctx, runscBinary, "state", containerID)
	output, err := cmd.Output()
	if err != nil {
		return nil, err
	}

	var state gVisorContainerState
	if err := json.Unmarshal(output, &state); err != nil {
		return nil, err
	}

	return &state, nil
}

// DestroyAgent removes an agent container and cleans up resources
func (r *GVisorRuntime) DestroyAgent(ctx context.Context, agentID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return fmt.Errorf("agent %s not found", agentID)
	}

	// Stop container if running
	if agent.Status == arctypes.AgentStatusRunning {
		if err := r.signalContainer(ctx, agent.ContainerID, syscall.SIGKILL); err != nil {
			// Log but continue with cleanup
			fmt.Fprintf(r.logWriter, "Warning: failed to kill container %s: %v\n", agent.ContainerID, err)
		}
		time.Sleep(1 * time.Second)
	}

	// Clean up container
	_ = r.deleteContainer(ctx, agent.ContainerID)

	// Remove bundle directory
	if agent.BundleDir != "" {
		if err := os.RemoveAll(agent.BundleDir); err != nil {
			fmt.Fprintf(r.logWriter, "Warning: failed to remove bundle directory %s: %v\n", agent.BundleDir, err)
		}
	}

	// Clean up monitoring resources
	if agent.stopChan != nil {
		close(agent.stopChan)
	}
	if agent.healthTicker != nil {
		agent.healthTicker.Stop()
	}

	// Remove from agents map
	delete(r.agents, agentID)

	return nil
}

// GetAgentStatus returns comprehensive status of an agent
func (r *GVisorRuntime) GetAgentStatus(ctx context.Context, agentID string) (*arctypes.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	// Update status from container state
	if state, err := r.getContainerState(ctx, agent.ContainerID); err == nil {
		switch state.Status {
		case "running":
			agent.Status = arctypes.AgentStatusRunning
		case "stopped":
			if agent.ExitCode == 0 {
				agent.Status = arctypes.AgentStatusCompleted
			} else {
				agent.Status = arctypes.AgentStatusFailed
			}
		case "created":
			agent.Status = arctypes.AgentStatusCreating
		}
	}

	// Create comprehensive agent response
	return &arctypes.Agent{
		ID:          agent.ID,
		ContainerID: agent.ContainerID,
		Status:      agent.Status,
		Config:      *agent.Config,
		CreatedAt:   time.Now(), // TODO: Store actual creation time
		StartedAt:   agent.StartedAt,
		CompletedAt: agent.FinishedAt,
		Error:       agent.Error,
	}, nil
}

// GetAgentLogs returns logs from an agent container
func (r *GVisorRuntime) GetAgentLogs(ctx context.Context, agentID string, follow bool) (io.ReadCloser, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agent, exists := r.agents[agentID]
	if !exists {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	file, err := os.Open(agent.LogFile)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	if !follow {
		return file, nil
	}

	// Implement log following
	reader, writer := io.Pipe()
	go r.followLogs(file, writer, agent.stopChan)
	
	return reader, nil
}

// followLogs implements log following functionality
func (r *GVisorRuntime) followLogs(file *os.File, writer *io.PipeWriter, stopChan chan struct{}) {
	defer writer.Close()
	defer file.Close()

	scanner := bufio.NewScanner(file)
	
	// Read existing content
	for scanner.Scan() {
		fmt.Fprintln(writer, scanner.Text())
	}

	// Follow new content
	for {
		select {
		case <-stopChan:
			return
		case <-time.After(100 * time.Millisecond):
			for scanner.Scan() {
				fmt.Fprintln(writer, scanner.Text())
			}
		}
	}
}

// ListAgents returns all agents with enhanced metadata
func (r *GVisorRuntime) ListAgents(ctx context.Context) ([]*arctypes.Agent, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	agents := make([]*arctypes.Agent, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, &arctypes.Agent{
			ID:          agent.ID,
			ContainerID: agent.ContainerID,
			Status:      agent.Status,
			Config:      *agent.Config,
			CreatedAt:   time.Now(), // TODO: Store actual creation time
			StartedAt:   agent.StartedAt,
			CompletedAt: agent.FinishedAt,
			Error:       agent.Error,
		})
	}

	return agents, nil
}

// StreamLogs streams logs from an agent container
func (r *GVisorRuntime) StreamLogs(ctx context.Context, agentID string) (io.ReadCloser, error) {
	return r.GetAgentLogs(ctx, agentID, true)
}

// ExecCommand executes a command in an agent container
func (r *GVisorRuntime) ExecCommand(ctx context.Context, agentID string, cmd []string) (io.ReadCloser, error) {
	r.mu.RLock()
	agent, exists := r.agents[agentID]
	r.mu.RUnlock()

	if !exists {
		return nil, fmt.Errorf("agent %s not found", agentID)
	}

	// Execute command using runsc exec
	args := append([]string{"exec", agent.ContainerID}, cmd...)
	execCmd := exec.CommandContext(ctx, runscBinary, args...)

	stdout, err := execCmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	if err := execCmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start exec command: %w", err)
	}

	return stdout, nil
}

// GetRuntimeInfo returns comprehensive runtime information
func (r *GVisorRuntime) GetRuntimeInfo(ctx context.Context) (map[string]interface{}, error) {
	info := map[string]interface{}{
		"type":         "gvisor",
		"version":      r.runscVersion,
		"platform":     string(r.platform),
		"rootDir":      r.rootDir,
		"cacheDir":     r.cacheDir,
		"agents":       len(r.agents),
		"capabilities": r.capabilities,
		"config": map[string]interface{}{
			"debug":             r.config.Debug,
			"log_level":         r.config.LogLevel,
			"network_mode":      r.config.NetworkMode,
			"enable_cgroups":    r.config.EnableCgroups,
			"enable_seccomp":    r.config.EnableSeccomp,
			"enable_sandbox":    r.config.EnableSandbox,
			"health_checks":     r.config.EnableHealthChecks,
		},
	}

	// Add cache statistics
	r.imageCacheMu.RLock()
	cacheStats := map[string]interface{}{
		"entries": len(r.imageCache),
		"size":    r.calculateCacheSize(),
	}
	r.imageCacheMu.RUnlock()
	info["image_cache"] = cacheStats

	return info, nil
}

// Monitoring and health check functions

// monitorAgent monitors an agent's lifecycle
func (r *GVisorRuntime) monitorAgent(agent *gVisorAgent) {
	if agent.cmd == nil {
		return
	}

	// Wait for process to complete
	err := agent.cmd.Wait()
	
	r.mu.Lock()
	defer r.mu.Unlock()

	finishedAt := time.Now()
	agent.FinishedAt = &finishedAt

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			agent.ExitCode = exitErr.ExitCode()
			agent.Status = arctypes.AgentStatusFailed
			agent.Error = exitErr.Error()
		} else {
			agent.Status = arctypes.AgentStatusFailed
			agent.Error = err.Error()
		}
	} else {
		agent.ExitCode = 0
		agent.Status = arctypes.AgentStatusCompleted
	}

	agent.HealthStatus.Status = "stopped"
}

// healthCheckAgent performs periodic health checks
func (r *GVisorRuntime) healthCheckAgent(agent *gVisorAgent) {
	agent.healthTicker = time.NewTicker(r.config.HealthCheckInterval)
	defer agent.healthTicker.Stop()

	for {
		select {
		case <-agent.stopChan:
			return
		case <-agent.healthTicker.C:
			r.performHealthCheck(agent)
		}
	}
}

// performHealthCheck checks container health
func (r *GVisorRuntime) performHealthCheck(agent *gVisorAgent) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Simple health check: verify container is still running
	state, err := r.getContainerState(ctx, agent.ContainerID)
	
	agent.HealthStatus.LastCheck = time.Now()
	
	if err != nil || state.Status != "running" {
		agent.HealthStatus.FailureCount++
		agent.HealthStatus.Status = "unhealthy"
		agent.HealthStatus.Output = fmt.Sprintf("Container check failed: %v", err)
	} else {
		agent.HealthStatus.FailureCount = 0
		agent.HealthStatus.Status = "healthy"
		agent.HealthStatus.Output = "Container is running"
	}
}

// startHealthCheckRoutine starts the global health check routine
func (r *GVisorRuntime) startHealthCheckRoutine() {
	r.healthCheck = time.NewTicker(r.config.HealthCheckInterval)
	
	go func() {
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-r.healthCheck.C:
				r.performGlobalHealthCheck()
			}
		}
	}()
}

// performGlobalHealthCheck performs health checks on all agents
func (r *GVisorRuntime) performGlobalHealthCheck() {
	r.mu.RLock()
	agents := make([]*gVisorAgent, 0, len(r.agents))
	for _, agent := range r.agents {
		if agent.Status == arctypes.AgentStatusRunning {
			agents = append(agents, agent)
		}
	}
	r.mu.RUnlock()

	for _, agent := range agents {
		r.performHealthCheck(agent)
	}
}

// Image caching functions

// calculateImageDigest calculates a digest for image caching
func (r *GVisorRuntime) calculateImageDigest(imageSpec string) string {
	hash := sha256.Sum256([]byte(imageSpec))
	return fmt.Sprintf("sha256:%x", hash)
}

// getCachedImage retrieves an image from cache
func (r *GVisorRuntime) getCachedImage(digest string) (*ImageCacheEntry, bool) {
	r.imageCacheMu.RLock()
	defer r.imageCacheMu.RUnlock()
	
	entry, exists := r.imageCache[digest]
	if !exists {
		return nil, false
	}
	
	// Check if cache entry is still valid
	if time.Since(entry.LastUsed) > r.config.ImageCacheTTL {
		return nil, false
	}
	
	entry.LastUsed = time.Now()
	return entry, true
}

// cacheImage stores an image in cache
func (r *GVisorRuntime) cacheImage(digest string, entry *ImageCacheEntry) {
	r.imageCacheMu.Lock()
	defer r.imageCacheMu.Unlock()
	
	r.imageCache[digest] = entry
}

// removeCachedImage removes an image from cache
func (r *GVisorRuntime) removeCachedImage(digest string) {
	r.imageCacheMu.Lock()
	defer r.imageCacheMu.Unlock()
	
	if entry, exists := r.imageCache[digest]; exists {
		os.RemoveAll(entry.Path)
		delete(r.imageCache, digest)
	}
}

// extractCachedImage extracts a cached image to rootfs
func (r *GVisorRuntime) extractCachedImage(cached *ImageCacheEntry, rootfsDir string) error {
	// Simple copy from cache to rootfs
	return r.copyDirectory(cached.Path, rootfsDir)
}

// copyDirectory recursively copies a directory
func (r *GVisorRuntime) copyDirectory(src, dst string) error {
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		
		dstPath := filepath.Join(dst, relPath)
		
		if d.IsDir() {
			return os.MkdirAll(dstPath, d.Type().Perm())
		}
		
		return r.copyFile(path, dstPath)
	})
}

// copyFile copies a single file
func (r *GVisorRuntime) copyFile(src, dst string) error {
	srcFile, err := os.Open(src)
	if err != nil {
		return err
	}
	defer srcFile.Close()
	
	dstFile, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer dstFile.Close()
	
	_, err = io.Copy(dstFile, srcFile)
	return err
}

// calculateDirectorySize calculates the size of a directory
func (r *GVisorRuntime) calculateDirectorySize(dir string) int64 {
	var size int64
	
	filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // Skip errors
		}
		
		if !d.IsDir() {
			info, err := d.Info()
			if err == nil {
				size += info.Size()
			}
		}
		return nil
	})
	
	return size
}

// calculateCacheSize calculates total cache size
func (r *GVisorRuntime) calculateCacheSize() int64 {
	var total int64
	for _, entry := range r.imageCache {
		total += entry.Size
	}
	return total
}

// startCacheCleanupRoutine starts cache cleanup routine
func (r *GVisorRuntime) startCacheCleanupRoutine() {
	go func() {
		ticker := time.NewTicker(1 * time.Hour)
		defer ticker.Stop()
		
		for {
			select {
			case <-r.ctx.Done():
				return
			case <-ticker.C:
				r.cleanupCache()
			}
		}
	}()
}

// cleanupCache removes expired cache entries
func (r *GVisorRuntime) cleanupCache() {
	r.imageCacheMu.Lock()
	defer r.imageCacheMu.Unlock()
	
	now := time.Now()
	for digest, entry := range r.imageCache {
		if now.Sub(entry.LastUsed) > r.config.ImageCacheTTL {
			os.RemoveAll(entry.Path)
			delete(r.imageCache, digest)
		}
	}
}

// Utility functions for resource parsing

// parseMemoryLimitToBytes converts memory string to bytes
func (r *GVisorRuntime) parseMemoryLimitToBytes(memory string) int64 {
	var bytes int64
	memory = strings.TrimSpace(memory)
	
	switch {
	case strings.HasSuffix(memory, "Ki"):
		fmt.Sscanf(memory, "%dKi", &bytes)
		bytes *= 1024
	case strings.HasSuffix(memory, "Mi"):
		fmt.Sscanf(memory, "%dMi", &bytes)
		bytes *= 1024 * 1024
	case strings.HasSuffix(memory, "Gi"):
		fmt.Sscanf(memory, "%dGi", &bytes)
		bytes *= 1024 * 1024 * 1024
	case strings.HasSuffix(memory, "K"):
		fmt.Sscanf(memory, "%dK", &bytes)
		bytes *= 1000
	case strings.HasSuffix(memory, "M"):
		fmt.Sscanf(memory, "%dM", &bytes)
		bytes *= 1000 * 1000
	case strings.HasSuffix(memory, "G"):
		fmt.Sscanf(memory, "%dG", &bytes)
		bytes *= 1000 * 1000 * 1000
	default:
		fmt.Sscanf(memory, "%d", &bytes)
	}
	
	return bytes
}

// parseCPUToShares converts CPU string to CPU shares
func (r *GVisorRuntime) parseCPUToShares(cpu string) int64 {
	cpu = strings.TrimSpace(cpu)
	
	if strings.HasSuffix(cpu, "m") {
		// Convert millicores to shares (1000m = 1024 shares)
		millis := strings.TrimSuffix(cpu, "m")
		var cores int64
		fmt.Sscanf(millis, "%d", &cores)
		return cores * 1024 / 1000
	}
	
	// Assume it's already in cores
	var cores int64
	fmt.Sscanf(cpu, "%d", &cores)
	return cores * 1024
}

// parseCPULimit converts CPU string to cores (e.g., "1000m" -> "1")
func (r *GVisorRuntime) parseCPULimit(cpu string) string {
	if strings.HasSuffix(cpu, "m") {
		// Convert millicores to cores
		millis := strings.TrimSuffix(cpu, "m")
		var cores float64
		fmt.Sscanf(millis, "%f", &cores)
		return fmt.Sprintf("%.2f", cores/1000)
	}
	return cpu
}

// parseMemoryLimit converts memory string to bytes string
func (r *GVisorRuntime) parseMemoryLimit(memory string) string {
	return strconv.FormatInt(r.parseMemoryLimitToBytes(memory), 10)
}

// Cleanup function
func (r *GVisorRuntime) Shutdown(ctx context.Context) error {
	// Cancel context to stop all goroutines
	r.cancel()
	
	// Stop health check routine
	if r.healthCheck != nil {
		r.healthCheck.Stop()
	}
	
	// Clean up all agents
	r.mu.Lock()
	agents := make([]*gVisorAgent, 0, len(r.agents))
	for _, agent := range r.agents {
		agents = append(agents, agent)
	}
	r.mu.Unlock()
	
	for _, agent := range agents {
		r.DestroyAgent(ctx, agent.ID)
	}
	
	return nil
}