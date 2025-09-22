package analyzer

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/jtodic/dockerfile-time-machine/pkg/docker"
	"github.com/olekukonko/tablewriter"
	"github.com/schollz/progressbar/v3"
)

// Config holds configuration for the TimeMachine
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

// BuildResult represents the result of building a Docker image at a specific commit
type BuildResult struct {
	CommitHash    string    `json:"commit_hash"`
	CommitMessage string    `json:"commit_message"`
	Author        string    `json:"author"`
	Date          time.Time `json:"date"`
	ImageSize     int64     `json:"image_size"`
	BuildTime     float64   `json:"build_time_seconds"`
	LayerCount    int       `json:"layer_count"`
	Layers        []Layer   `json:"layers,omitempty"`
	Error         string    `json:"error,omitempty"`
	SizeDiff      int64     `json:"size_diff,omitempty"`
}

// Layer represents a Docker image layer
type Layer struct {
	ID      string `json:"id"`
	Size    int64  `json:"size"`
	Command string `json:"command"`
}

// TimeMachine is the main analyzer
type TimeMachine struct {
	config       Config
	repo         *git.Repository
	dockerClient *client.Client
	builder      *docker.Builder
	results      []BuildResult
}

// NewTimeMachine creates a new TimeMachine instance
func NewTimeMachine(config Config) (*TimeMachine, error) {
	repo, err := git.PlainOpen(config.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository: %w", err)
	}

	dockerClient, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker client: %w", err)
	}

	builder, err := docker.NewBuilder()
	if err != nil {
		return nil, fmt.Errorf("failed to create Docker builder: %w", err)
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
		config:       config,
		repo:         repo,
		dockerClient: dockerClient,
		builder:      builder,
		results:      []BuildResult{},
	}, nil
}

// Run executes the time machine analysis
func (tm *TimeMachine) Run(ctx context.Context) error {
	commits, err := tm.getCommits()
	if err != nil {
		return fmt.Errorf("failed to get commits: %w", err)
	}

	if len(commits) == 0 {
		return fmt.Errorf("no commits found that modified %s", tm.config.DockerfilePath)
	}

	fmt.Fprintf(os.Stderr, "🚀 Found %d commits to analyze\n", len(commits))

	bar := progressbar.NewOptions(len(commits),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWidth(40),
		progressbar.OptionSetDescription("[cyan]Analyzing commits...[reset]"),
	)

	for i, commit := range commits {
		bar.Add(1)

		if tm.config.Verbose {
			fmt.Fprintf(os.Stderr, "\n📦 Building at commit %s: %s\n",
				commit.Hash.String()[:8],
				strings.Split(commit.Message, "\n")[0])
		}

		result := tm.analyzeCommit(ctx, commit)

		// Calculate size difference
		if i > 0 && result.Error == "" && tm.results[i-1].Error == "" {
			result.SizeDiff = result.ImageSize - tm.results[i-1].ImageSize
		}

		tm.results = append(tm.results, result)

		if result.Error != "" && !tm.config.SkipFailed {
			if tm.config.Verbose {
				fmt.Fprintf(os.Stderr, "  ❌ Build failed: %s\n", result.Error)
			}
		}
	}

	fmt.Fprintf(os.Stderr, "\n")
	return nil
}

// getCommits retrieves commits that modified the Dockerfile
func (tm *TimeMachine) getCommits() ([]*object.Commit, error) {
	var commits []*object.Commit

	// Parse dates if provided
	var sinceTime, untilTime time.Time
	if tm.config.Since != "" {
		var err error
		sinceTime, err = time.Parse("2006-01-02", tm.config.Since)
		if err != nil {
			return nil, fmt.Errorf("invalid since date: %w", err)
		}
	}
	if tm.config.Until != "" {
		var err error
		untilTime, err = time.Parse("2006-01-02", tm.config.Until)
		if err != nil {
			return nil, fmt.Errorf("invalid until date: %w", err)
		}
	}

	// Get the branch reference
	ref, err := tm.repo.Reference(plumbing.ReferenceName("refs/heads/"+tm.config.Branch), true)
	if err != nil {
		// Try as tag
		ref, err = tm.repo.Reference(plumbing.ReferenceName("refs/tags/"+tm.config.Branch), true)
		if err != nil {
			return nil, fmt.Errorf("failed to get branch or tag %s: %w", tm.config.Branch, err)
		}
	}

	// Get commit iterator
	commitIter, err := tm.repo.Log(&git.LogOptions{
		From:     ref.Hash(),
		FileName: &tm.config.DockerfilePath,
	})
	if err != nil {
		return nil, err
	}

	count := 0
	err = commitIter.ForEach(func(c *object.Commit) error {
		// Check date filters
		if !sinceTime.IsZero() && c.Author.When.Before(sinceTime) {
			return nil
		}
		if !untilTime.IsZero() && c.Author.When.After(untilTime) {
			return nil
		}

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

// analyzeCommit checks out a commit and builds the Docker image
func (tm *TimeMachine) analyzeCommit(ctx context.Context, commit *object.Commit) BuildResult {
	result := BuildResult{
		CommitHash:    commit.Hash.String(),
		CommitMessage: strings.TrimSpace(strings.Split(commit.Message, "\n")[0]),
		Author:        commit.Author.Name,
		Date:          commit.Author.When,
	}

	// Checkout the commit
	worktree, err := tm.repo.Worktree()
	if err != nil {
		result.Error = fmt.Sprintf("failed to get worktree: %v", err)
		return result
	}

	err = worktree.Checkout(&git.CheckoutOptions{
		Hash:  commit.Hash,
		Force: true,
	})
	if err != nil {
		result.Error = fmt.Sprintf("failed to checkout commit: %v", err)
		return result
	}

	// Build the Docker image
	startTime := time.Now()
	imageName := fmt.Sprintf("dtm-%s", commit.Hash.String()[:12])

	contextPath := filepath.Dir(filepath.Join(tm.config.RepoPath, tm.config.DockerfilePath))
	dockerfileName := filepath.Base(tm.config.DockerfilePath)

	err = tm.builder.BuildImage(ctx, contextPath, dockerfileName, imageName)
	if err != nil {
		result.Error = fmt.Sprintf("build failed: %v", err)
		return result
	}

	result.BuildTime = time.Since(startTime).Seconds()

	// Inspect the image
	inspect, _, err := tm.dockerClient.ImageInspectWithRaw(ctx, imageName)
	if err != nil {
		result.Error = fmt.Sprintf("failed to inspect image: %v", err)
		// Clean up
		tm.dockerClient.ImageRemove(ctx, imageName, image.RemoveOptions{Force: true})
		return result
	}

	result.ImageSize = inspect.Size
	result.LayerCount = len(inspect.RootFS.Layers)

	// Get layer details using history
	history, err := tm.dockerClient.ImageHistory(ctx, imageName)
	if err == nil {
		for _, h := range history {
			if h.Size > 0 {
				result.Layers = append(result.Layers, Layer{
					ID:      h.ID,
					Size:    h.Size,
					Command: h.CreatedBy,
				})
			}
		}
	}

	// Clean up the image
	tm.dockerClient.ImageRemove(ctx, imageName, image.RemoveOptions{Force: true})

	return result
}

// GenerateReport generates output in the specified format
func (tm *TimeMachine) GenerateReport(format string, writer io.Writer) error {
	switch format {
	case "table":
		return tm.generateTableReport(writer)
	case "json":
		return tm.generateJSONReport(writer)
	case "csv":
		return tm.generateCSVReport(writer)
	case "chart":
		return tm.generateChartReport(writer)
	case "markdown":
		return tm.generateMarkdownReport(writer)
	default:
		return fmt.Errorf("unsupported format: %s", format)
	}
}

// generateTableReport creates a table output
func (tm *TimeMachine) generateTableReport(w io.Writer) error {
	table := tablewriter.NewWriter(w)
	table.SetHeader([]string{"Commit", "Date", "Author", "Size (MB)", "Diff", "Layers", "Time (s)", "Message"})
	table.SetBorder(false)
	table.SetAutoWrapText(false)
	table.SetColumnSeparator(" ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	var validResults []BuildResult
	for _, result := range tm.results {
		if result.Error == "" {
			validResults = append(validResults, result)
		}
	}

	for _, result := range validResults {
		diffStr := ""
		if result.SizeDiff != 0 {
			sign := "+"
			if result.SizeDiff < 0 {
				sign = ""
			}
			diffStr = fmt.Sprintf("%s%.1f", sign, float64(result.SizeDiff)/1024/1024)
		}

		row := []string{
			result.CommitHash[:8],
			result.Date.Format("2006-01-02"),
			truncate(result.Author, 12),
			fmt.Sprintf("%.2f", float64(result.ImageSize)/1024/1024),
			diffStr,
			fmt.Sprintf("%d", result.LayerCount),
			fmt.Sprintf("%.1f", result.BuildTime),
			truncate(result.CommitMessage, 40),
		}
		table.Append(row)
	}

	fmt.Fprintln(w, "\n📊 Docker Image Evolution Report")
	fmt.Fprintln(w, "=================================")
	table.Render()

	// Find insights
	if bloat := tm.findBloatCommit(); bloat != nil {
		fmt.Fprintf(w, "\n⚠️  Biggest size increase: %s\n", bloat.CommitHash[:8])
		fmt.Fprintf(w, "   Size increased by: %.2f MB\n", float64(bloat.SizeDiff)/1024/1024)
		fmt.Fprintf(w, "   Message: %s\n", bloat.CommitMessage)
	}

	if optimization := tm.findOptimizationCommit(); optimization != nil {
		fmt.Fprintf(w, "\n✅ Biggest size reduction: %s\n", optimization.CommitHash[:8])
		fmt.Fprintf(w, "   Size reduced by: %.2f MB\n", float64(-optimization.SizeDiff)/1024/1024)
		fmt.Fprintf(w, "   Message: %s\n", optimization.CommitMessage)
	}

	return nil
}

// generateJSONReport outputs results as JSON
func (tm *TimeMachine) generateJSONReport(w io.Writer) error {
	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(tm.results)
}

// generateCSVReport outputs results as CSV
func (tm *TimeMachine) generateCSVReport(w io.Writer) error {
	fmt.Fprintln(w, "commit_hash,date,author,size_bytes,size_diff,layer_count,build_time_seconds,message")
	for _, result := range tm.results {
		if result.Error != "" {
			continue
		}
		fmt.Fprintf(w, "%s,%s,%s,%d,%d,%d,%.2f,\"%s\"\n",
			result.CommitHash,
			result.Date.Format(time.RFC3339),
			result.Author,
			result.ImageSize,
			result.SizeDiff,
			result.LayerCount,
			result.BuildTime,
			strings.ReplaceAll(result.CommitMessage, "\"", "\"\""),
		)
	}
	return nil
}

// generateChartReport creates an HTML report with charts
func (tm *TimeMachine) generateChartReport(w io.Writer) error {
	visualizer := NewVisualizer(tm.results)
	return visualizer.GenerateHTML(w)
}

// generateMarkdownReport creates a markdown report
func (tm *TimeMachine) generateMarkdownReport(w io.Writer) error {
	fmt.Fprintln(w, "# Docker Image Evolution Report")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Summary")
	fmt.Fprintf(w, "- **Commits analyzed:** %d\n", len(tm.results))

	var validResults []BuildResult
	for _, r := range tm.results {
		if r.Error == "" {
			validResults = append(validResults, r)
		}
	}

	if len(validResults) > 0 {
		first := validResults[0]
		last := validResults[len(validResults)-1]

		fmt.Fprintf(w, "- **Initial size:** %.2f MB\n", float64(first.ImageSize)/1024/1024)
		fmt.Fprintf(w, "- **Final size:** %.2f MB\n", float64(last.ImageSize)/1024/1024)
		fmt.Fprintf(w, "- **Total change:** %+.2f MB\n", float64(last.ImageSize-first.ImageSize)/1024/1024)
		fmt.Fprintln(w)

		fmt.Fprintln(w, "## Details")
		fmt.Fprintln(w)
		fmt.Fprintln(w, "| Commit | Date | Size (MB) | Change | Layers | Build Time |")
		fmt.Fprintln(w, "|--------|------|-----------|--------|--------|------------|")

		for _, r := range validResults {
			change := ""
			if r.SizeDiff != 0 {
				change = fmt.Sprintf("%+.1f", float64(r.SizeDiff)/1024/1024)
			}
			fmt.Fprintf(w, "| %s | %s | %.2f | %s | %d | %.1fs |\n",
				r.CommitHash[:8],
				r.Date.Format("2006-01-02"),
				float64(r.ImageSize)/1024/1024,
				change,
				r.LayerCount,
				r.BuildTime,
			)
		}
	}

	return nil
}

// Helper methods
func (tm *TimeMachine) findBloatCommit() *BuildResult {
	var maxIncrease int64
	var bloatCommit *BuildResult

	for _, result := range tm.results {
		if result.Error == "" && result.SizeDiff > maxIncrease {
			maxIncrease = result.SizeDiff
			bloatCommit = &result
		}
	}

	return bloatCommit
}

func (tm *TimeMachine) findOptimizationCommit() *BuildResult {
	var maxDecrease int64
	var optimizationCommit *BuildResult

	for _, result := range tm.results {
		if result.Error == "" && result.SizeDiff < maxDecrease {
			maxDecrease = result.SizeDiff
			optimizationCommit = &result
		}
	}

	return optimizationCommit
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
