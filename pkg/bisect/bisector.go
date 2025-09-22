package bisect

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/jtodic/dockerfile-time-machine/pkg/analyzer"
)

// Config holds configuration for bisecting
type Config struct {
	RepoPath       string
	DockerfilePath string
	SizeThreshold  float64 // in MB
	TimeThreshold  float64 // in seconds
	GoodCommit     string
	BadCommit      string
	Verbose        bool
}

// Result represents a bisect result
type Result struct {
	CommitHash    string
	CommitMessage string
	Author        string
	Date          time.Time
	SizeMB        float64
	BuildTime     float64
}

// Bisector performs binary search for regressions
type Bisector struct {
	config Config
	repo   *git.Repository
	tm     *analyzer.TimeMachine
}

// NewBisector creates a new Bisector
func NewBisector(config Config) (*Bisector, error) {
	repo, err := git.PlainOpen(config.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	tm, err := analyzer.NewTimeMachine(analyzer.Config{
		RepoPath:       config.RepoPath,
		DockerfilePath: config.DockerfilePath,
		MaxCommits:     1,
		Verbose:        config.Verbose,
	})
	if err != nil {
		return nil, err
	}

	return &Bisector{
		config: config,
		repo:   repo,
		tm:     tm,
	}, nil
}

// FindRegression finds the commit that introduced a regression
func (b *Bisector) FindRegression(ctx context.Context) (*Result, error) {
	// Get commit range
	commits, err := b.getCommitRange()
	if err != nil {
		return nil, err
	}

	if len(commits) < 2 {
		return nil, fmt.Errorf("need at least 2 commits to bisect")
	}

	fmt.Printf("Bisecting %d commits...\n", len(commits))

	// Binary search
	left, right := 0, len(commits)-1
	var badCommit *object.Commit
	var badResult analyzer.BuildResult

	for left <= right {
		mid := (left + right) / 2
		commit := commits[mid]

		fmt.Printf("Testing commit %s: %s\n",
			commit.Hash.String()[:8],
			strings.Split(commit.Message, "\n")[0])

		// Test this commit
		result := b.tm.AnalyzeCommit(ctx, commit)
		if result.Error != "" {
			fmt.Printf("  Build failed, skipping\n")
			// Skip failed builds
			left = mid + 1
			continue
		}

		sizeMB := float64(result.ImageSize) / 1024 / 1024
		exceedsThreshold := false

		if b.config.SizeThreshold > 0 && sizeMB > b.config.SizeThreshold {
			exceedsThreshold = true
			fmt.Printf("  Size: %.2f MB (exceeds threshold)\n", sizeMB)
		}
		if b.config.TimeThreshold > 0 && result.BuildTime > b.config.TimeThreshold {
			exceedsThreshold = true
			fmt.Printf("  Build time: %.2fs (exceeds threshold)\n", result.BuildTime)
		}

		if exceedsThreshold {
			// This is bad, regression is here or earlier
			badCommit = commit
			badResult = result
			right = mid - 1
		} else {
			// This is good, regression is later
			fmt.Printf("  Within threshold\n")
			left = mid + 1
		}
	}

	if badCommit == nil {
		return nil, fmt.Errorf("no regression found in the commit range")
	}

	return &Result{
		CommitHash:    badCommit.Hash.String(),
		CommitMessage: strings.TrimSpace(badCommit.Message),
		Author:        badCommit.Author.Name,
		Date:          badCommit.Author.When,
		SizeMB:        float64(badResult.ImageSize) / 1024 / 1024,
		BuildTime:     badResult.BuildTime,
	}, nil
}

// getCommitRange gets the range of commits to bisect
func (b *Bisector) getCommitRange() ([]*object.Commit, error) {
	var startHash, endHash plumbing.Hash

	// Determine start commit
	if b.config.GoodCommit != "" {
		hash, err := b.resolveRevision(b.config.GoodCommit)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve good commit: %w", err)
		}
		startHash = hash
	} else {
		// Use the oldest commit that modified the Dockerfile
		commits, err := b.getAllCommits()
		if err != nil {
			return nil, err
		}
		if len(commits) > 0 {
			startHash = commits[0].Hash
		}
	}

	// Determine end commit
	if b.config.BadCommit != "" {
		hash, err := b.resolveRevision(b.config.BadCommit)
		if err != nil {
			return nil, fmt.Errorf("failed to resolve bad commit: %w", err)
		}
		endHash = hash
	} else {
		// Use HEAD
		head, err := b.repo.Head()
		if err != nil {
			return nil, err
		}
		endHash = head.Hash()
	}

	// Get commits between start and end
	return b.getCommitsBetween(startHash, endHash)
}

// resolveRevision resolves a revision string to a hash
func (b *Bisector) resolveRevision(rev string) (plumbing.Hash, error) {
	// Try as hash first
	hash := plumbing.NewHash(rev)
	if _, err := b.repo.CommitObject(hash); err == nil {
		return hash, nil
	}

	// Try as reference
	ref, err := b.repo.Reference(plumbing.ReferenceName("refs/heads/"+rev), true)
	if err == nil {
		return ref.Hash(), nil
	}

	// Try as tag
	ref, err = b.repo.Reference(plumbing.ReferenceName("refs/tags/"+rev), true)
	if err == nil {
		return ref.Hash(), nil
	}

	return plumbing.ZeroHash, fmt.Errorf("could not resolve revision: %s", rev)
}

// getAllCommits gets all commits that modified the Dockerfile
func (b *Bisector) getAllCommits() ([]*object.Commit, error) {
	head, err := b.repo.Head()
	if err != nil {
		return nil, err
	}

	var commits []*object.Commit
	iter, err := b.repo.Log(&git.LogOptions{
		From:     head.Hash(),
		FileName: &b.config.DockerfilePath,
	})
	if err != nil {
		return nil, err
	}

	err = iter.ForEach(func(c *object.Commit) error {
		commits = append(commits, c)
		return nil
	})

	// Reverse to chronological order
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}

	return commits, err
}

// getCommitsBetween gets commits between two hashes
func (b *Bisector) getCommitsBetween(startHash, endHash plumbing.Hash) ([]*object.Commit, error) {
	var commits []*object.Commit

	// Get all commits from end going back
	iter, err := b.repo.Log(&git.LogOptions{
		From:     endHash,
		FileName: &b.config.DockerfilePath,
	})
	if err != nil {
		return nil, err
	}

	foundStart := false
	err = iter.ForEach(func(c *object.Commit) error {
		commits = append(commits, c)
		if c.Hash == startHash {
			foundStart = true
			return object.ErrCanceled
		}
		return nil
	})

	if !foundStart && err != object.ErrCanceled {
		return nil, fmt.Errorf("start commit not found in history")
	}

	// Reverse to chronological order
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}

	return commits, nil
}
