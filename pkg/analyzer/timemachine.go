package analyzer

import (
	"context"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
)

type Config struct {
	RepoPath       string
	DockerfilePath string
	MaxCommits     int
	Branch         string
	Since          string
	Until          string
	SkipFailed     bool
	Verbose        bool
}

type BuildResult struct {
	CommitHash    string    `json:"commit_hash"`
	CommitMessage string    `json:"commit_message"`
	Author        string    `json:"author"`
	Date          time.Time `json:"date"`
	ImageSize     int64     `json:"image_size"`
	BuildTime     float64   `json:"build_time_seconds"`
	LayerCount    int       `json:"layer_count"`
	Error         string    `json:"error,omitempty"`
}

type TimeMachine struct {
	config  Config
	repo    *git.Repository
	results []BuildResult
}

func NewTimeMachine(config Config) (*TimeMachine, error) {
	repo, err := git.PlainOpen(config.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}

	// Set default branch if not specified
	if config.Branch == "" {
		head, err := repo.Head()
		if err != nil {
			return nil, fmt.Errorf("failed to get HEAD: %w", err)
		}
		config.Branch = head.Name().Short()
	}

	return &TimeMachine{
		config:  config,
		repo:    repo,
		results: []BuildResult{},
	}, nil
}

func (tm *TimeMachine) Run(ctx context.Context) error {
	commits, err := tm.getCommits()
	if err != nil {
		return fmt.Errorf("failed to get commits: %w", err)
	}

	if len(commits) == 0 {
		return fmt.Errorf("no commits found that modified %s", tm.config.DockerfilePath)
	}

	fmt.Fprintf(os.Stderr, "🚀 Found %d commits to analyze\n", len(commits))

	// For now, just analyze without actually building
	// (to test the commit finding logic)
	for _, commit := range commits {
		fmt.Fprintf(os.Stderr, "📦 Analyzing commit %s: %s\n",
			commit.Hash.String()[:7],
			strings.Split(commit.Message, "\n")[0])

		result := BuildResult{
			CommitHash:    commit.Hash.String(),
			CommitMessage: strings.TrimSpace(strings.Split(commit.Message, "\n")[0]),
			Author:        commit.Author.Name,
			Date:          commit.Author.When,
			// Mock data for testing
			ImageSize:  1024 * 1024 * 100, // 100MB
			BuildTime:  10.5,
			LayerCount: 5,
		}

		tm.results = append(tm.results, result)
	}

	return nil
}

func (tm *TimeMachine) getCommits() ([]*object.Commit, error) {
	var commits []*object.Commit

	// Get the branch reference
	branchRef := plumbing.ReferenceName("refs/heads/" + tm.config.Branch)
	ref, err := tm.repo.Reference(branchRef, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get branch %s: %w", tm.config.Branch, err)
	}

	// Get commit iterator
	commitIter, err := tm.repo.Log(&git.LogOptions{
		From:     ref.Hash(),
		FileName: &tm.config.DockerfilePath,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get log: %w", err)
	}

	count := 0
	err = commitIter.ForEach(func(c *object.Commit) error {
		if tm.config.MaxCommits > 0 && count >= tm.config.MaxCommits {
			return nil
		}
		commits = append(commits, c)
		count++
		return nil
	})

	if err != nil {
		return nil, err
	}

	// Reverse to get chronological order
	for i, j := 0, len(commits)-1; i < j; i, j = i+1, j-1 {
		commits[i], commits[j] = commits[j], commits[i]
	}

	return commits, nil
}

func (tm *TimeMachine) GenerateReport(format string, writer io.Writer) error {
	if len(tm.results) == 0 {
		return fmt.Errorf("no results to report")
	}

	// Simple table output for testing
	fmt.Fprintln(writer, "\n📊 Docker Image Evolution Report")
	fmt.Fprintln(writer, "=================================")
	fmt.Fprintln(writer, "Commit   Date        Message")
	fmt.Fprintln(writer, "--------------------------------")

	for _, result := range tm.results {
		fmt.Fprintf(writer, "%s  %s  %s\n",
			result.CommitHash[:7],
			result.Date.Format("2006-01-02"),
			result.CommitMessage,
		)
	}

	return nil
}

func (tm *TimeMachine) AnalyzeCommit(ctx context.Context, commit interface{}) BuildResult {
	return BuildResult{}
}
