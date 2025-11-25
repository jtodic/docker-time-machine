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

	"github.com/docker/docker/api/types/build"
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
		return nil, fmt.Errorf("docker daemon not responding: %w", err)
	}

	return &Builder{client: cli}, nil
}

// BuildImage builds a Docker image from the specified context
func (b *Builder) BuildImage(ctx context.Context, contextPath, dockerfileName, tag string) error {
	// Create build context tar
	buildContext, err := b.createBuildContext(contextPath)
	if err != nil {
		return fmt.Errorf("failed to create build context: %w", err)
	}

	// Build options
	opts := build.ImageBuildOptions{
		Tags:           []string{tag},
		Dockerfile:     dockerfileName,
		Remove:         true,
		ForceRemove:    true,
		PullParent:     false,
		NoCache:        true,
		SuppressOutput: false,
		Context:        buildContext,
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
func (b *Builder) createBuildContext(contextPath string) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	defer tw.Close()

	// Walk the context directory
	err := filepath.Walk(contextPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		// Skip .git and other unnecessary directories
		relPath, err := filepath.Rel(contextPath, path)
		if err != nil {
			return err
		}

		// Skip unwanted paths
		if relPath == "." {
			return nil
		}

		// Skip .git and .dtm-cache directories entirely
		if strings.HasPrefix(relPath, ".git") || strings.HasPrefix(relPath, ".dtm-cache") {
			if info.IsDir() {
				return filepath.SkipDir
			}
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

		// Write file content for regular files
		if !info.IsDir() {
			file, err := os.Open(path)
			if err != nil {
				return err
			}
			defer file.Close()

			_, err = io.Copy(tw, file)
			if err != nil {
				return err
			}
		}

		return nil
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

		// Optionally print progress (you can make this conditional on verbose flag)
		if msg.Stream != "" && strings.Contains(msg.Stream, "Step") {
			fmt.Fprint(os.Stderr, msg.Stream)
		}
	}

	return nil
}

// GetImageInfo retrieves information about a Docker image
func (b *Builder) GetImageInfo(ctx context.Context, imageID string) (*image.InspectResponse, error) {
	inspect, err := b.client.ImageInspect(ctx, imageID)
	if err != nil {
		return nil, err
	}
	return &inspect, nil
}

// GetImageHistory retrieves the history of a Docker image
func (b *Builder) GetImageHistory(ctx context.Context, imageID string) ([]image.HistoryResponseItem, error) {
	history, err := b.client.ImageHistory(ctx, imageID)
	if err != nil {
		return nil, err
	}
	return history, nil
}

// RemoveImage removes a Docker image by ID or name
func (b *Builder) RemoveImage(ctx context.Context, imageID string) error {
	_, err := b.client.ImageRemove(ctx, imageID, image.RemoveOptions{
		Force:         true,
		PruneChildren: true,
	})
	return err
}

// Close closes the Docker client connection
func (b *Builder) Close() error {
	return b.client.Close()
}
