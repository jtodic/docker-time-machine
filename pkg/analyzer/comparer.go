package analyzer

import (
	"context"
	"fmt"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/jtodic/docker-time-machine/pkg/docker"
)

// ComparerConfig holds configuration for the Comparer
type ComparerConfig struct {
	RepoPath       string
	DockerfilePath string
	Verbose        bool
}

// CompareResult holds the comparison results
type CompareResult struct {
	BranchA              BranchInfo `json:"branch_a"`
	BranchB              BranchInfo `json:"branch_b"`
	SizeDiff             float64    `json:"size_diff_mb"`
	SizeDiffPercent      float64    `json:"size_diff_percent"`
	LayersDiff           int        `json:"layers_diff"`
	BuildTimeDiff        float64    `json:"build_time_diff"`
	BuildTimeDiffPercent float64    `json:"build_time_diff_percent"`
}

// BranchInfo holds information about a branch's Docker image
type BranchInfo struct {
	Name      string  `json:"name"`
	Commit    string  `json:"commit"`
	SizeMB    float64 `json:"size_mb"`
	Layers    int     `json:"layers"`
	BuildTime float64 `json:"build_time"`
}

// Comparer compares Docker images between branches
type Comparer struct {
	config  ComparerConfig
	repo    *git.Repository
	builder *docker.Builder
}

// NewComparer creates a new Comparer
func NewComparer(config ComparerConfig) (*Comparer, error) {
	repo, err := git.PlainOpen(config.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	builder, err := docker.NewBuilder()
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker builder: %w", err)
	}

	return &Comparer{
		config:  config,
		repo:    repo,
		builder: builder,
	}, nil
}

// Compare compares Docker images between two branches
func (c *Comparer) Compare(ctx context.Context, branchA, branchB string) (*CompareResult, error) {
	// Get worktree to switch branches
	worktree, err := c.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	// Store current branch to restore later
	originalRef, err := c.repo.Head()
	if err != nil {
		return nil, fmt.Errorf("failed to get HEAD: %w", err)
	}

	// Build branch A
	if c.config.Verbose {
		fmt.Printf("Building branch %s...\n", branchA)
	}
	infoA, err := c.buildBranch(ctx, worktree, branchA)
	if err != nil {
		return nil, fmt.Errorf("failed to build branch %s: %w", branchA, err)
	}

	// Build branch B
	if c.config.Verbose {
		fmt.Printf("Building branch %s...\n", branchB)
	}
	infoB, err := c.buildBranch(ctx, worktree, branchB)
	if err != nil {
		return nil, fmt.Errorf("failed to build branch %s: %w", branchB, err)
	}

	// Restore original branch
	err = worktree.Checkout(&git.CheckoutOptions{
		Hash:  originalRef.Hash(),
		Force: true,
	})
	if err != nil {
		fmt.Printf("Warning: failed to restore original branch: %v\n", err)
	}

	// Calculate differences
	result := &CompareResult{
		BranchA:       *infoA,
		BranchB:       *infoB,
		SizeDiff:      infoB.SizeMB - infoA.SizeMB,
		LayersDiff:    infoB.Layers - infoA.Layers,
		BuildTimeDiff: infoB.BuildTime - infoA.BuildTime,
	}

	// Calculate percentages
	if infoA.SizeMB > 0 {
		result.SizeDiffPercent = (result.SizeDiff / infoA.SizeMB) * 100
	}
	if infoA.BuildTime > 0 {
		result.BuildTimeDiffPercent = (result.BuildTimeDiff / infoA.BuildTime) * 100
	}

	return result, nil
}

// buildBranch builds a Docker image for a specific branch
func (c *Comparer) buildBranch(ctx context.Context, worktree *git.Worktree, branchName string) (*BranchInfo, error) {
	// Get branch reference
	ref, err := c.repo.Reference(plumbing.ReferenceName("refs/heads/"+branchName), true)
	if err != nil {
		// Try as tag
		ref, err = c.repo.Reference(plumbing.ReferenceName("refs/tags/"+branchName), true)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve reference %s: %w", branchName, err)
		}
	}

	// Checkout branch
	err = worktree.Checkout(&git.CheckoutOptions{
		Hash:  ref.Hash(),
		Force: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to checkout: %w", err)
	}

	// Get commit info
	commit, err := c.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	// Build the Docker image
	imageName := fmt.Sprintf("dtm-compare-%s", ref.Hash().String()[:12])
	startTime := time.Now()

	err = c.builder.BuildImage(ctx, c.config.RepoPath, c.config.DockerfilePath, imageName)
	if err != nil {
		return nil, fmt.Errorf("build failed: %w", err)
	}

	buildTime := time.Since(startTime).Seconds()

	// Get image info
	imageInfo, err := c.builder.GetImageInfo(ctx, imageName)
	if err != nil {
		c.builder.RemoveImage(ctx, imageName)
		return nil, fmt.Errorf("failed to inspect image: %w", err)
	}

	// Clean up
	c.builder.RemoveImage(ctx, imageName)

	return &BranchInfo{
		Name:      branchName,
		Commit:    commit.Hash.String()[:8],
		SizeMB:    float64(imageInfo.Size) / 1024 / 1024,
		Layers:    len(imageInfo.RootFS.Layers),
		BuildTime: buildTime,
	}, nil
}
