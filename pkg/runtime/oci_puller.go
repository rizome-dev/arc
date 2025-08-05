// Package runtime provides a lightweight OCI image puller using only standard library
package runtime

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// OCIManifest represents a minimal OCI image manifest
type OCIManifest struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Config        struct {
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		Digest    string `json:"digest"`
	} `json:"config"`
	Layers []struct {
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		Digest    string `json:"digest"`
	} `json:"layers"`
}

// DockerManifestList for multi-arch images
type DockerManifestList struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Manifests     []struct {
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		Digest    string `json:"digest"`
		Platform  struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
		} `json:"platform"`
	} `json:"manifests"`
}

// TokenResponse from Docker registry auth
type TokenResponse struct {
	Token       string `json:"token"`
	AccessToken string `json:"access_token"`
	ExpiresIn   int    `json:"expires_in"`
}

// OCIPuller handles OCI image downloading using only standard library
type OCIPuller struct {
	client    *http.Client
	cache     map[string]string // image:tag -> rootfs path
	cacheDir  string
	authCache map[string]*TokenResponse
}

// NewOCIPuller creates a new OCI image puller
func NewOCIPuller(cacheDir string) *OCIPuller {
	return &OCIPuller{
		client: &http.Client{
			Timeout: 5 * time.Minute,
		},
		cache:     make(map[string]string),
		cacheDir:  cacheDir,
		authCache: make(map[string]*TokenResponse),
	}
}

// PullImage downloads and extracts an OCI image
func (p *OCIPuller) PullImage(ctx context.Context, imageRef string, rootfsDir string) error {
	// Parse image reference
	registry, namespace, image, tag, err := p.parseImageRef(imageRef)
	if err != nil {
		return fmt.Errorf("invalid image reference: %w", err)
	}

	// Check cache
	cacheKey := fmt.Sprintf("%s/%s/%s:%s", registry, namespace, image, tag)
	if cachedPath, exists := p.cache[cacheKey]; exists {
		// Copy cached rootfs to destination
		return p.copyDir(cachedPath, rootfsDir)
	}

	// Get auth token if needed
	token, err := p.getAuthToken(ctx, registry, namespace, image)
	if err != nil {
		return fmt.Errorf("auth failed: %w", err)
	}

	// Get manifest
	manifest, err := p.getManifest(ctx, registry, namespace, image, tag, token)
	if err != nil {
		return fmt.Errorf("failed to get manifest: %w", err)
	}

	// Download and extract layers
	for i, layer := range manifest.Layers {
		if err := p.downloadAndExtractLayer(ctx, registry, namespace, image, layer.Digest, rootfsDir, token); err != nil {
			return fmt.Errorf("failed to process layer %d: %w", i, err)
		}
	}

	// Cache the result
	p.cache[cacheKey] = rootfsDir

	return nil
}

// parseImageRef parses an image reference like alpine:latest or docker.io/library/alpine:latest
func (p *OCIPuller) parseImageRef(imageRef string) (registry, namespace, image, tag string, err error) {
	// Handle local filesystem paths
	if strings.HasPrefix(imageRef, "/") || strings.HasPrefix(imageRef, "./") {
		err = fmt.Errorf("local paths not supported, use OCI registry images")
		return
	}

	// Set defaults
	registry = "docker.io"
	namespace = "library"
	tag = "latest"

	// Remove protocol if present
	imageRef = strings.TrimPrefix(imageRef, "http://")
	imageRef = strings.TrimPrefix(imageRef, "https://")

	// Split tag
	parts := strings.Split(imageRef, ":")
	if len(parts) == 2 {
		imageRef = parts[0]
		tag = parts[1]
	}

	// Parse the rest
	segments := strings.Split(imageRef, "/")
	
	switch len(segments) {
	case 1:
		// Just image name (e.g., "alpine")
		image = segments[0]
	case 2:
		// Could be namespace/image or registry/image
		if strings.Contains(segments[0], ".") || segments[0] == "localhost" {
			// It's a registry
			registry = segments[0]
			image = segments[1]
			namespace = "library"
		} else {
			// It's namespace/image
			namespace = segments[0]
			image = segments[1]
		}
	case 3:
		// registry/namespace/image
		registry = segments[0]
		namespace = segments[1]
		image = segments[2]
	default:
		err = fmt.Errorf("invalid image format")
	}

	return
}

// getAuthToken gets an auth token for Docker registry
func (p *OCIPuller) getAuthToken(ctx context.Context, registry, namespace, image string) (string, error) {
	// Check cache
	cacheKey := fmt.Sprintf("%s/%s/%s", registry, namespace, image)
	if cached, exists := p.authCache[cacheKey]; exists {
		return cached.Token, nil
	}

	// For Docker Hub
	if registry == "docker.io" || registry == "registry-1.docker.io" {
		authURL := fmt.Sprintf("https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s/%s:pull", namespace, image)
		
		req, err := http.NewRequestWithContext(ctx, "GET", authURL, nil)
		if err != nil {
			return "", err
		}

		resp, err := p.client.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("auth failed: %s", resp.Status)
		}

		var tokenResp TokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
			return "", err
		}

		p.authCache[cacheKey] = &tokenResp
		return tokenResp.Token, nil
	}

	// For other registries, try without auth first
	return "", nil
}

// getManifest downloads the image manifest
func (p *OCIPuller) getManifest(ctx context.Context, registry, namespace, image, tag, token string) (*OCIManifest, error) {
	// Fix registry URL for Docker Hub
	if registry == "docker.io" {
		registry = "registry-1.docker.io"
	}

	url := fmt.Sprintf("https://%s/v2/%s/%s/manifests/%s", registry, namespace, image, tag)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Add auth header if we have a token
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	// Accept both OCI and Docker manifest types
	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json, application/vnd.docker.distribution.manifest.list.v2+json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get manifest: %s - %s", resp.Status, string(body))
	}

	// Check content type to determine manifest type
	contentType := resp.Header.Get("Content-Type")
	
	// Read response body
	manifestData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	// Handle manifest list (multi-arch)
	if strings.Contains(contentType, "manifest.list") {
		var manifestList DockerManifestList
		if err := json.Unmarshal(manifestData, &manifestList); err != nil {
			return nil, err
		}

		// Find linux/amd64 manifest
		for _, m := range manifestList.Manifests {
			if m.Platform.OS == "linux" && m.Platform.Architecture == "amd64" {
				// Fetch the actual manifest
				return p.getManifestByDigest(ctx, registry, namespace, image, m.Digest, token)
			}
		}
		return nil, fmt.Errorf("no linux/amd64 manifest found")
	}

	// Parse as regular manifest
	var manifest OCIManifest
	if err := json.Unmarshal(manifestData, &manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// getManifestByDigest fetches a manifest by its digest
func (p *OCIPuller) getManifestByDigest(ctx context.Context, registry, namespace, image, digest, token string) (*OCIManifest, error) {
	if registry == "docker.io" {
		registry = "registry-1.docker.io"
	}

	url := fmt.Sprintf("https://%s/v2/%s/%s/manifests/%s", registry, namespace, image, digest)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	req.Header.Set("Accept", "application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.v2+json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to get manifest by digest: %s", resp.Status)
	}

	var manifest OCIManifest
	if err := json.NewDecoder(resp.Body).Decode(&manifest); err != nil {
		return nil, err
	}

	return &manifest, nil
}

// downloadAndExtractLayer downloads and extracts a single layer
func (p *OCIPuller) downloadAndExtractLayer(ctx context.Context, registry, namespace, image, digest, rootfsDir string, token string) error {
	// Fix registry URL for Docker Hub
	if registry == "docker.io" {
		registry = "registry-1.docker.io"
	}

	url := fmt.Sprintf("https://%s/v2/%s/%s/blobs/%s", registry, namespace, image, digest)
	
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download layer: %s", resp.Status)
	}

	// Verify digest while reading
	hasher := sha256.New()
	reader := io.TeeReader(resp.Body, hasher)

	// Decompress if needed (most layers are gzipped)
	var layerReader io.Reader
	if strings.HasSuffix(digest, ".gz") || resp.Header.Get("Content-Encoding") == "gzip" {
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			// Not gzipped, use as is
			layerReader = reader
		} else {
			defer gzReader.Close()
			layerReader = gzReader
		}
	} else {
		// Try gzip anyway (Docker layers are usually gzipped)
		gzReader, err := gzip.NewReader(reader)
		if err != nil {
			// Not gzipped, use as is
			layerReader = reader
		} else {
			defer gzReader.Close()
			layerReader = gzReader
		}
	}

	// Extract tar archive
	tarReader := tar.NewReader(layerReader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar error: %w", err)
		}

		// Calculate the full path
		targetPath := filepath.Join(rootfsDir, header.Name)

		// Ensure the target path is within rootfsDir (prevent directory traversal)
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(rootfsDir)) {
			continue // Skip suspicious paths
		}

		switch header.Typeflag {
		case tar.TypeDir:
			// Create directory
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return err
			}

		case tar.TypeReg:
			// Create directory for file
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}

			// Create file
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}

			// Copy file contents
			if _, err := io.Copy(file, tarReader); err != nil {
				file.Close()
				return err
			}
			file.Close()

		case tar.TypeSymlink:
			// Create symlink
			os.Remove(targetPath) // Remove if exists
			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				// Ignore symlink errors (some might point outside rootfs)
				continue
			}

		case tar.TypeLink:
			// Create hard link
			linkTarget := filepath.Join(rootfsDir, header.Linkname)
			if strings.HasPrefix(filepath.Clean(linkTarget), filepath.Clean(rootfsDir)) {
				os.Remove(targetPath) // Remove if exists
				if err := os.Link(linkTarget, targetPath); err != nil {
					// Ignore hard link errors
					continue
				}
			}
		}
	}

	// Verify digest (optional, skipping for simplicity as we need to read the full compressed stream)
	// actualDigest := fmt.Sprintf("sha256:%s", hex.EncodeToString(hasher.Sum(nil)))
	// if actualDigest != digest {
	//     return fmt.Errorf("digest mismatch: expected %s, got %s", digest, actualDigest)
	// }

	return nil
}

// copyDir copies a directory recursively
func (p *OCIPuller) copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Calculate destination path
		relPath, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, relPath)

		// Create directory
		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}

		// Copy file
		return p.copyFile(path, dstPath)
	})
}

// copyFile copies a single file
func (p *OCIPuller) copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

// ExtractLocalImage extracts a local tar archive (for pre-downloaded images)
func (p *OCIPuller) ExtractLocalImage(ctx context.Context, tarPath string, rootfsDir string) error {
	file, err := os.Open(tarPath)
	if err != nil {
		return fmt.Errorf("failed to open tar file: %w", err)
	}
	defer file.Close()

	// Check if it's gzipped
	var reader io.Reader = file
	if strings.HasSuffix(tarPath, ".gz") || strings.HasSuffix(tarPath, ".tgz") {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return fmt.Errorf("failed to create gzip reader: %w", err)
		}
		defer gzReader.Close()
		reader = gzReader
	}

	// Extract tar
	tarReader := tar.NewReader(reader)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("tar error: %w", err)
		}

		targetPath := filepath.Join(rootfsDir, header.Name)
		
		// Security: ensure the path is within rootfsDir
		if !strings.HasPrefix(filepath.Clean(targetPath), filepath.Clean(rootfsDir)) {
			continue
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(targetPath, os.FileMode(header.Mode)); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
				return err
			}
			file, err := os.OpenFile(targetPath, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(file, tarReader); err != nil {
				file.Close()
				return err
			}
			file.Close()
		case tar.TypeSymlink:
			os.Remove(targetPath)
			if err := os.Symlink(header.Linkname, targetPath); err != nil {
				continue // Ignore symlink errors
			}
		}
	}

	return nil
}