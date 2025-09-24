package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/docker/docker/api/types"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/jsonmessage"
)

// Builder handles Docker image building operations
type Builder struct {
	client *client.Client
}

// NewBuilder creates a new Docker builder
func NewBuilder() (*Builder, error) {
	cli, err := client.NewClientWithOpts(
		client.FromEnv,
		client.WithAPIVersionNegotiation(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	// Ping to verify connection
	ctx := context.Background()
	_, err = cli.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("Docker daemon not responding: %w", err)
	}

	return &Builder{client: cli}, nil
}

// BuildImage builds a Docker image from the specified context
func (b *Builder) BuildImage(ctx context.Context, contextPath, dockerfilePath, tag string) error {
	// Create build context tar
	buildContext, err := b.createBuildContext(contextPath, dockerfilePath)
	if err != nil {
		return fmt.Errorf("failed to create build context: %w", err)
	}

	// Build options
	opts := types.ImageBuildOptions{
		Tags:           []string{tag},
		Dockerfile:     dockerfilePath,
		Remove:         true,
		ForceRemove:    true,
		PullParent:     false,
		NoCache:        true,
		SuppressOutput: false,
	}

	// Build the image
	resp, err := b.client.ImageBuild(ctx, buildContext, opts)
	if err != nil {
		return fmt.Errorf("image build failed: %w", err)
	}
	defer resp.Body.Close()

	// Process build output
	return b.processBuildOutput(resp.Body)
}

// createBuildContext creates a tar archive of the build context
func (b *Builder) createBuildContext(contextPath, dockerfilePath string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer tw.Close()

	// Check if Dockerfile exists
	dockerfileFull := filepath.Join(contextPath, dockerfilePath)
	if _, err := os.Stat(dockerfileFull); os.IsNotExist(err) {
		return nil, fmt.Errorf("Dockerfile not found: %s", dockerfileFull)
	}

	// Walk the context directory
	err := filepath.Walk(contextPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip .git and .dockerignore paths
		relPath, err := filepath.Rel(contextPath, path)
		if err != nil {
			return err
		}

		if strings.HasPrefix(relPath, ".git") ||
			strings.HasPrefix(relPath, ".dtm-cache") ||
			info.Name() == ".dockerignore" {
			if info.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		// Skip directories (they're created implicitly)
		if info.IsDir() {
			return nil
		}

		// Create tar header
		header, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}

		// Use forward slashes for tar
		header.Name = filepath.ToSlash(relPath)

		// Write header
		if err := tw.WriteHeader(header); err != nil {
			return err
		}

		// Write file content
		file, err := os.Open(path)
		if err != nil {
			return err
		}
		defer file.Close()

		_, err = io.Copy(tw, file)
		return err
	})

	if err != nil {
		return nil, fmt.Errorf("failed to create tar: %w", err)
	}

	return bytes.NewReader(buf.Bytes()), nil
}

// processBuildOutput processes the build output and checks for errors
func (b *Builder) processBuildOutput(reader io.Reader) error {
	decoder := json.NewDecoder(reader)

	for {
		var msg jsonmessage.JSONMessage
		if err := decoder.Decode(&msg); err != nil {
			if err == io.EOF {
				break
			}
			return err
		}

		// Check for errors
		if msg.Error != nil {
			return fmt.Errorf("build error: %s", msg.Error.Message)
		}

		// Optionally print progress
		if msg.Stream != "" {
			// Could print to stderr if verbose mode
			// fmt.Fprint(os.Stderr, msg.Stream)
		}
	}

	return nil
}

// RemoveImage removes a Docker image
func (b *Builder) RemoveImage(ctx context.Context, imageID string) error {
	_, err := b.client.ImageRemove(ctx, imageID, image.RemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	return err
}

// ImageExists checks if an image exists
func (b *Builder) ImageExists(ctx context.Context, imageID string) bool {
	_, _, err := b.client.ImageInspectWithRaw(ctx, imageID)
	return err == nil
}

// GetImageSize returns the size of an image in bytes
func (b *Builder) GetImageSize(ctx context.Context, imageID string) (int64, error) {
	inspect, _, err := b.client.ImageInspectWithRaw(ctx, imageID)
	if err != nil {
		return 0, err
	}
	return inspect.Size, nil
}

// Close closes the Docker client connection
func (b *Builder) Close() error {
	return b.client.Close()
}
