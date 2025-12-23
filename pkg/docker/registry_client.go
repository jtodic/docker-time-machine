package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// RegistryClient handles communication with container registries
type RegistryClient struct {
	httpClient *http.Client
}

// TagInfo contains information about a tag
type TagInfo struct {
	Name      string    `json:"name"`
	Digest    string    `json:"digest,omitempty"`
	CreatedAt time.Time `json:"created_at,omitempty"`
}

// DockerConfig represents ~/.docker/config.json structure
type DockerConfig struct {
	Auths      map[string]DockerAuthEntry `json:"auths"`
	CredsStore string                     `json:"credsStore,omitempty"`
}

// DockerAuthEntry represents a single auth entry
type DockerAuthEntry struct {
	Auth string `json:"auth"` // base64(username:password)
}

// NewRegistryClient creates a new registry client
func NewRegistryClient() *RegistryClient {
	return &RegistryClient{
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// getDockerCredentials reads credentials from ~/.docker/config.json or credential helper
func (rc *RegistryClient) getDockerCredentials(registry string) (username, password string, err error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", "", err
	}

	configPath := filepath.Join(homeDir, ".docker", "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", "", fmt.Errorf("cannot read docker config: %w", err)
	}

	var config DockerConfig
	if err := json.Unmarshal(data, &config); err != nil {
		return "", "", fmt.Errorf("cannot parse docker config: %w", err)
	}

	// Determine which registry key to look for
	registryKey := registry

	// For Docker Hub, try common variations
	dockerHubKeys := []string{}
	if registry == "docker.io" || registry == "" {
		dockerHubKeys = []string{
			"https://index.docker.io/v1/",
			"index.docker.io",
			"https://index.docker.io/v2/",
			"registry-1.docker.io",
			"https://registry-1.docker.io",
		}
	}

	// Check if credential helper is configured
	if config.CredsStore != "" {
		// Try to get credentials from credential helper
		keysToTry := []string{registryKey, "https://" + registryKey}
		keysToTry = append(keysToTry, dockerHubKeys...)

		for _, key := range keysToTry {
			u, p, err := rc.getCredentialsFromHelper(config.CredsStore, key)
			if err == nil && u != "" {
				return u, p, nil
			}
		}

		return "", "", fmt.Errorf("no credentials found in %s credential helper for %s - run 'docker login %s' first", config.CredsStore, registry, registry)
	}

	// Try to find credentials in auths
	auth, ok := config.Auths[registryKey]
	if !ok {
		auth, ok = config.Auths["https://"+registryKey]
	}

	// For Docker Hub, try common variations
	if !ok && len(dockerHubKeys) > 0 {
		for _, key := range dockerHubKeys {
			if a, found := config.Auths[key]; found && a.Auth != "" {
				auth = a
				ok = true
				break
			}
		}
	}

	if !ok {
		// Try without port for jfrog
		if strings.Contains(registry, ".jfrog.io") {
			for key, val := range config.Auths {
				if strings.Contains(key, ".jfrog.io") {
					auth = val
					ok = true
					break
				}
			}
		}
	}

	if !ok || auth.Auth == "" {
		return "", "", fmt.Errorf("no credentials found for %s - run 'docker login %s' first", registry, registry)
	}

	// Decode base64 auth (username:password)
	decoded, err := base64.StdEncoding.DecodeString(auth.Auth)
	if err != nil {
		return "", "", fmt.Errorf("cannot decode auth: %w", err)
	}

	parts := strings.SplitN(string(decoded), ":", 2)
	if len(parts) != 2 {
		return "", "", fmt.Errorf("invalid auth format")
	}

	return parts[0], parts[1], nil
}

// getCredentialsFromHelper retrieves credentials from a Docker credential helper
func (rc *RegistryClient) getCredentialsFromHelper(helper, registry string) (username, password string, err error) {
	// Docker credential helpers are named docker-credential-<helper>
	helperCmd := "docker-credential-" + helper

	cmd := exec.Command(helperCmd, "get")
	cmd.Stdin = strings.NewReader(registry)

	output, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("credential helper failed: %w", err)
	}

	var creds struct {
		Username string `json:"Username"`
		Secret   string `json:"Secret"`
	}

	if err := json.Unmarshal(output, &creds); err != nil {
		return "", "", fmt.Errorf("cannot parse credential helper output: %w", err)
	}

	return creds.Username, creds.Secret, nil
}

// ListTags lists tags for an image from the registry
func (rc *RegistryClient) ListTags(ctx context.Context, imageName string, limit int) ([]TagInfo, error) {
	// Parse image name to determine registry
	registry, repo := parseImageName(imageName)

	switch {
	case registry == "docker.io" || registry == "":
		return rc.listDockerHubTags(ctx, repo, limit)
	case strings.Contains(registry, "gcr.io"):
		return rc.listGCRTags(ctx, registry, repo, limit)
	case strings.Contains(registry, "ghcr.io"):
		return rc.listGHCRTags(ctx, repo, limit)
	case strings.Contains(registry, ".jfrog.io"):
		return rc.listJFrogTags(ctx, registry, repo, limit)
	case strings.Contains(registry, "azurecr.io"):
		return rc.listACRTags(ctx, registry, repo, limit)
	case strings.Contains(registry, ".ecr.") && strings.Contains(registry, ".amazonaws.com"):
		return rc.listECRTags(ctx, registry, repo, limit)
	default:
		// Generic OCI registry with auth
		return rc.listOCITagsWithAuth(ctx, registry, repo, limit)
	}
}

func parseImageName(imageName string) (registry, repo string) {
	// Strip protocol prefix if present
	imageName = strings.TrimPrefix(imageName, "https://")
	imageName = strings.TrimPrefix(imageName, "http://")

	parts := strings.SplitN(imageName, "/", 2)

	// Check if first part looks like a registry
	if len(parts) == 2 && (strings.Contains(parts[0], ".") || strings.Contains(parts[0], ":")) {
		return parts[0], parts[1]
	}

	// Docker Hub
	if len(parts) == 1 {
		// Official image like "nginx"
		return "docker.io", "library/" + imageName
	}

	// User image like "user/repo"
	return "docker.io", imageName
}

// Docker Hub API response structures
type dockerHubTagsResponse struct {
	Results []struct {
		Name        string    `json:"name"`
		LastUpdated time.Time `json:"last_updated"`
		Digest      string    `json:"digest"`
	} `json:"results"`
	Next string `json:"next"`
}

func (rc *RegistryClient) listDockerHubTags(ctx context.Context, repo string, limit int) ([]TagInfo, error) {
	url := fmt.Sprintf("https://hub.docker.com/v2/repositories/%s/tags?page_size=%d&ordering=last_updated", repo, limit)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query Docker Hub: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Docker Hub API error (%d): %s", resp.StatusCode, string(body))
	}

	var result dockerHubTagsResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var tags []TagInfo
	for _, t := range result.Results {
		tags = append(tags, TagInfo{
			Name:      t.Name,
			Digest:    t.Digest,
			CreatedAt: t.LastUpdated,
		})
	}

	return tags, nil
}

// GCR/Artifact Registry token response
type gcrTokenResponse struct {
	Token string `json:"token"`
}

func (rc *RegistryClient) listGCRTags(ctx context.Context, registry, repo string, limit int) ([]TagInfo, error) {
	// GCR uses token-based auth
	tokenURL := fmt.Sprintf("https://%s/v2/token?service=%s&scope=repository:%s:pull", registry, registry, repo)

	tokenReq, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
	if err != nil {
		return nil, err
	}

	tokenResp, err := rc.httpClient.Do(tokenReq)
	if err != nil {
		return nil, fmt.Errorf("failed to get GCR token: %w", err)
	}
	defer tokenResp.Body.Close()

	var tokenResult gcrTokenResponse
	if err := json.NewDecoder(tokenResp.Body).Decode(&tokenResult); err != nil {
		return nil, err
	}

	// List tags
	tagsURL := fmt.Sprintf("https://%s/v2/%s/tags/list", registry, repo)
	tagsReq, err := http.NewRequestWithContext(ctx, "GET", tagsURL, nil)
	if err != nil {
		return nil, err
	}
	tagsReq.Header.Set("Authorization", "Bearer "+tokenResult.Token)

	tagsResp, err := rc.httpClient.Do(tagsReq)
	if err != nil {
		return nil, err
	}
	defer tagsResp.Body.Close()

	var tagsResult struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(tagsResp.Body).Decode(&tagsResult); err != nil {
		return nil, err
	}

	var tags []TagInfo
	for _, t := range tagsResult.Tags {
		tags = append(tags, TagInfo{Name: t})
	}

	// Sort and limit
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Name > tags[j].Name
	})

	if limit > 0 && len(tags) > limit {
		tags = tags[:limit]
	}

	return tags, nil
}

func (rc *RegistryClient) listGHCRTags(ctx context.Context, repo string, limit int) ([]TagInfo, error) {
	// GHCR uses OCI distribution spec
	return rc.listOCITagsWithAuth(ctx, "ghcr.io", repo, limit)
}

// listJFrogTags lists tags from JFrog Artifactory
func (rc *RegistryClient) listJFrogTags(ctx context.Context, registry, repo string, limit int) ([]TagInfo, error) {
	username, password, err := rc.getDockerCredentials(registry)
	if err != nil {
		return nil, err
	}

	// JFrog uses standard OCI/Docker Registry API v2
	url := fmt.Sprintf("https://%s/v2/%s/tags/list", registry, repo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	// Add basic auth
	req.SetBasicAuth(username, password)

	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query JFrog: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("authentication failed - check your credentials with 'docker login %s'", registry)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("JFrog API error (%d): %s", resp.StatusCode, string(body))
	}

	var result struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}

	var tags []TagInfo
	for _, t := range result.Tags {
		tags = append(tags, TagInfo{Name: t})
	}

	// Sort by name (reverse to get newest first assuming semver)
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Name > tags[j].Name
	})

	if limit > 0 && len(tags) > limit {
		tags = tags[:limit]
	}

	return tags, nil
}

// listACRTags lists tags from Azure Container Registry
func (rc *RegistryClient) listACRTags(ctx context.Context, registry, repo string, limit int) ([]TagInfo, error) {
	username, password, err := rc.getDockerCredentials(registry)
	if err != nil {
		return nil, err
	}

	// ACR uses standard OCI/Docker Registry API v2
	url := fmt.Sprintf("https://%s/v2/%s/tags/list", registry, repo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(username, password)

	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query ACR: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("authentication failed - run 'az acr login --name %s'", strings.Split(registry, ".")[0])
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ACR API error (%d)", resp.StatusCode)
	}

	var result struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var tags []TagInfo
	for _, t := range result.Tags {
		tags = append(tags, TagInfo{Name: t})
	}

	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Name > tags[j].Name
	})

	if limit > 0 && len(tags) > limit {
		tags = tags[:limit]
	}

	return tags, nil
}

// listECRTags lists tags from AWS ECR
func (rc *RegistryClient) listECRTags(ctx context.Context, registry, repo string, limit int) ([]TagInfo, error) {
	// ECR requires AWS credentials via docker login
	// The token is obtained via: aws ecr get-login-password | docker login --username AWS --password-stdin <registry>
	username, password, err := rc.getDockerCredentials(registry)
	if err != nil {
		return nil, fmt.Errorf("ECR credentials not found - run 'aws ecr get-login-password | docker login --username AWS --password-stdin %s'", registry)
	}

	url := fmt.Sprintf("https://%s/v2/%s/tags/list", registry, repo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	req.SetBasicAuth(username, password)

	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query ECR: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("ECR authentication failed/expired - run 'aws ecr get-login-password | docker login --username AWS --password-stdin %s'", registry)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("ECR API error (%d)", resp.StatusCode)
	}

	var result struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var tags []TagInfo
	for _, t := range result.Tags {
		tags = append(tags, TagInfo{Name: t})
	}

	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Name > tags[j].Name
	})

	if limit > 0 && len(tags) > limit {
		tags = tags[:limit]
	}

	return tags, nil
}

// listOCITagsWithAuth lists tags using OCI Distribution API with authentication
func (rc *RegistryClient) listOCITagsWithAuth(ctx context.Context, registry, repo string, limit int) ([]TagInfo, error) {
	// Try to get credentials
	username, password, err := rc.getDockerCredentials(registry)

	url := fmt.Sprintf("https://%s/v2/%s/tags/list", registry, repo)

	req, err2 := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err2 != nil {
		return nil, err2
	}

	// Add auth if we have credentials
	if err == nil && username != "" {
		req.SetBasicAuth(username, password)
	}

	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("authentication required - run 'docker login %s' first", registry)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry error (%d)", resp.StatusCode)
	}

	var result struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var tags []TagInfo
	for _, t := range result.Tags {
		tags = append(tags, TagInfo{Name: t})
	}

	if limit > 0 && len(tags) > limit {
		tags = tags[:limit]
	}

	return tags, nil
}

func (rc *RegistryClient) listOCITags(ctx context.Context, registry, repo string, limit int) ([]TagInfo, error) {
	// Standard OCI distribution API
	url := fmt.Sprintf("https://%s/v2/%s/tags/list", registry, repo)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to query registry: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, fmt.Errorf("authentication required - run 'docker login %s' first", registry)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("registry error (%d)", resp.StatusCode)
	}

	var result struct {
		Tags []string `json:"tags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	var tags []TagInfo
	for _, t := range result.Tags {
		tags = append(tags, TagInfo{Name: t})
	}

	if limit > 0 && len(tags) > limit {
		tags = tags[:limit]
	}

	return tags, nil
}

// ImageMetadata holds metadata fetched from registry without pulling full image
type ImageMetadata struct {
	Digest     string          `json:"digest"`
	Size       int64           `json:"size"`
	Created    time.Time       `json:"created"`
	LayerCount int             `json:"layer_count"`
	Layers     []LayerMetadata `json:"layers"`
}

// LayerMetadata holds layer information from registry
type LayerMetadata struct {
	Digest    string  `json:"digest"`
	Size      int64   `json:"size"`
	SizeMB    float64 `json:"size_mb"`
	CreatedBy string  `json:"created_by"`
	Empty     bool    `json:"empty"`
}

// ManifestResponse represents the OCI/Docker manifest
type ManifestResponse struct {
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

// ManifestListResponse represents a multi-arch manifest list
type ManifestListResponse struct {
	SchemaVersion int    `json:"schemaVersion"`
	MediaType     string `json:"mediaType"`
	Manifests     []struct {
		MediaType string `json:"mediaType"`
		Size      int64  `json:"size"`
		Digest    string `json:"digest"`
		Platform  struct {
			Architecture string `json:"architecture"`
			OS           string `json:"os"`
			Variant      string `json:"variant,omitempty"`
		} `json:"platform"`
	} `json:"manifests"`
}

// ConfigResponse represents the image config blob
type ConfigResponse struct {
	Created      time.Time `json:"created"`
	Architecture string    `json:"architecture"`
	OS           string    `json:"os"`
	History      []struct {
		Created    time.Time `json:"created"`
		CreatedBy  string    `json:"created_by"`
		EmptyLayer bool      `json:"empty_layer,omitempty"`
	} `json:"history"`
	RootFS struct {
		Type    string   `json:"type"`
		DiffIDs []string `json:"diff_ids"`
	} `json:"rootfs"`
}

// GetImageMetadata fetches image metadata from registry without pulling the full image
func (rc *RegistryClient) GetImageMetadata(ctx context.Context, imageName, tag, platform string) (*ImageMetadata, error) {
	registry, repo := parseImageName(imageName)

	// Get auth token
	token, err := rc.getAuthToken(ctx, registry, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to get auth token: %w", err)
	}

	// Fetch manifest
	manifest, manifestDigest, err := rc.fetchManifest(ctx, registry, repo, tag, token, platform)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch manifest: %w", err)
	}

	// Fetch config blob to get history (CreatedBy commands)
	config, err := rc.fetchConfigBlob(ctx, registry, repo, manifest.Config.Digest, token)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch config: %w", err)
	}

	// Build layer metadata by matching layers with history
	layers := rc.buildLayerMetadata(manifest, config)

	// Calculate total size
	var totalSize int64
	for _, layer := range manifest.Layers {
		totalSize += layer.Size
	}

	return &ImageMetadata{
		Digest:     manifestDigest,
		Size:       totalSize,
		Created:    config.Created,
		LayerCount: len(manifest.Layers),
		Layers:     layers,
	}, nil
}

// getAuthToken gets an authentication token for the registry
func (rc *RegistryClient) getAuthToken(ctx context.Context, registry, repo string) (string, error) {
	// Try to get credentials from docker config
	username, password, credErr := rc.getDockerCredentials(registry)

	// For Docker Hub, use auth.docker.io
	if registry == "docker.io" {
		tokenURL := fmt.Sprintf("https://auth.docker.io/token?service=registry.docker.io&scope=repository:%s:pull", repo)
		req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
		if err != nil {
			return "", err
		}

		// Add basic auth if we have credentials
		if credErr == nil && username != "" {
			req.SetBasicAuth(username, password)
		}

		resp, err := rc.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var tokenResp struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
			return "", err
		}
		return tokenResp.Token, nil
	}

	// For GCR
	if strings.Contains(registry, "gcr.io") {
		tokenURL := fmt.Sprintf("https://%s/v2/token?service=%s&scope=repository:%s:pull", registry, registry, repo)
		req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
		if err != nil {
			return "", err
		}

		resp, err := rc.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var tokenResp struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
			return "", err
		}
		return tokenResp.Token, nil
	}

	// For GHCR
	if strings.Contains(registry, "ghcr.io") {
		tokenURL := fmt.Sprintf("https://%s/token?service=%s&scope=repository:%s:pull", registry, registry, repo)
		req, err := http.NewRequestWithContext(ctx, "GET", tokenURL, nil)
		if err != nil {
			return "", err
		}

		// Add basic auth if we have credentials (for private repos)
		if credErr == nil && username != "" {
			req.SetBasicAuth(username, password)
		}

		resp, err := rc.httpClient.Do(req)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()

		var tokenResp struct {
			Token string `json:"token"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&tokenResp); err != nil {
			return "", err
		}
		return tokenResp.Token, nil
	}

	// For other registries, return basic auth credentials encoded
	if credErr == nil && username != "" {
		return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+password)), nil
	}

	return "", nil
}

// fetchManifest fetches the image manifest from the registry
func (rc *RegistryClient) fetchManifest(ctx context.Context, registry, repo, tag, token, platform string) (*ManifestResponse, string, error) {
	var baseURL string
	if registry == "docker.io" {
		baseURL = "https://registry-1.docker.io"
	} else {
		baseURL = fmt.Sprintf("https://%s", registry)
	}

	url := fmt.Sprintf("%s/v2/%s/manifests/%s", baseURL, repo, tag)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, "", err
	}

	// Accept both manifest types
	req.Header.Set("Accept", "application/vnd.docker.distribution.manifest.v2+json, application/vnd.oci.image.manifest.v1+json, application/vnd.docker.distribution.manifest.list.v2+json, application/vnd.oci.image.index.v1+json")

	if token != "" {
		if strings.HasPrefix(token, "Basic ") {
			req.Header.Set("Authorization", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("manifest fetch failed (%d): %s", resp.StatusCode, string(body))
	}

	manifestDigest := resp.Header.Get("Docker-Content-Digest")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}

	// Check if it's a manifest list (multi-arch)
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(contentType, "manifest.list") || strings.Contains(contentType, "image.index") {
		var manifestList ManifestListResponse
		if err := json.Unmarshal(body, &manifestList); err != nil {
			return nil, "", err
		}

		// Find the right platform
		targetArch := "amd64"
		targetOS := "linux"
		targetVariant := ""

		if platform != "" {
			parts := strings.Split(platform, "/")
			if len(parts) >= 2 {
				targetOS = parts[0]
				targetArch = parts[1]
			}
			if len(parts) >= 3 {
				targetVariant = parts[2]
			}
		}

		var selectedDigest string
		for _, m := range manifestList.Manifests {
			if m.Platform.OS == targetOS && m.Platform.Architecture == targetArch {
				if targetVariant == "" || m.Platform.Variant == targetVariant {
					selectedDigest = m.Digest
					break
				}
			}
		}

		if selectedDigest == "" {
			// Fallback to first manifest
			if len(manifestList.Manifests) > 0 {
				selectedDigest = manifestList.Manifests[0].Digest
			} else {
				return nil, "", fmt.Errorf("no suitable manifest found for platform %s", platform)
			}
		}

		// Fetch the actual manifest by digest
		return rc.fetchManifest(ctx, registry, repo, selectedDigest, token, "")
	}

	var manifest ManifestResponse
	if err := json.Unmarshal(body, &manifest); err != nil {
		return nil, "", err
	}

	return &manifest, manifestDigest, nil
}

// fetchConfigBlob fetches the config blob containing history
func (rc *RegistryClient) fetchConfigBlob(ctx context.Context, registry, repo, digest, token string) (*ConfigResponse, error) {
	var baseURL string
	if registry == "docker.io" {
		baseURL = "https://registry-1.docker.io"
	} else {
		baseURL = fmt.Sprintf("https://%s", registry)
	}

	url := fmt.Sprintf("%s/v2/%s/blobs/%s", baseURL, repo, digest)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	if token != "" {
		if strings.HasPrefix(token, "Basic ") {
			req.Header.Set("Authorization", token)
		} else {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	resp, err := rc.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("config fetch failed (%d): %s", resp.StatusCode, string(body))
	}

	var config ConfigResponse
	if err := json.NewDecoder(resp.Body).Decode(&config); err != nil {
		return nil, err
	}

	return &config, nil
}

// buildLayerMetadata matches manifest layers with config history
func (rc *RegistryClient) buildLayerMetadata(manifest *ManifestResponse, config *ConfigResponse) []LayerMetadata {
	var layers []LayerMetadata

	// History includes empty layers, manifest.Layers does not
	// We need to match them up correctly
	layerIdx := 0
	for _, h := range config.History {
		layer := LayerMetadata{
			CreatedBy: cleanCreatedBy(h.CreatedBy),
			Empty:     h.EmptyLayer,
		}

		if !h.EmptyLayer && layerIdx < len(manifest.Layers) {
			layer.Digest = manifest.Layers[layerIdx].Digest
			layer.Size = manifest.Layers[layerIdx].Size
			layer.SizeMB = float64(manifest.Layers[layerIdx].Size) / 1024 / 1024
			layerIdx++
		}

		layers = append(layers, layer)
	}

	return layers
}

// cleanCreatedBy cleans up the CreatedBy string
func cleanCreatedBy(cmd string) string {
	cmd = strings.TrimPrefix(cmd, "/bin/sh -c ")
	cmd = strings.TrimPrefix(cmd, "#(nop) ")
	cmd = strings.TrimSpace(cmd)
	return cmd
}
