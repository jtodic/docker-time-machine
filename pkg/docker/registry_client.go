package docker

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
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
	Auths map[string]DockerAuthEntry `json:"auths"`
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

// getDockerCredentials reads credentials from ~/.docker/config.json
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

	// Try exact match first
	auth, ok := config.Auths[registry]
	if !ok {
		// Try with https://
		auth, ok = config.Auths["https://"+registry]
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
