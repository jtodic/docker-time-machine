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

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
	"github.com/jtodic/docker-time-machine/pkg/docker"
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

// LayerInfo represents information about a single Docker image layer
type LayerInfo struct {
	ID        string  `json:"id,omitempty"`
	CreatedBy string  `json:"created_by"`
	Size      int64   `json:"size"`
	SizeMB    float64 `json:"size_mb"`
}

// BuildResult represents the result of building a Docker image at a specific commit
type BuildResult struct {
	CommitHash    string      `json:"commit_hash"`
	CommitMessage string      `json:"commit_message"`
	Author        string      `json:"author"`
	Date          time.Time   `json:"date"`
	ImageSize     int64       `json:"image_size"`
	BuildTime     float64     `json:"build_time_seconds"`
	LayerCount    int         `json:"layer_count"`
	Layers        []LayerInfo `json:"layers,omitempty"`
	Error         string      `json:"error,omitempty"`
	SizeDiff      int64       `json:"size_diff,omitempty"`
}

// LayerComparison represents layer sizes across commits
type LayerComparison struct {
	LayerCommand string             `json:"layer_command"`
	SizeByCommit map[string]float64 `json:"size_by_commit"` // commit hash -> size in MB
}

// TimeMachine is the main analyzer
type TimeMachine struct {
	config  Config
	repo    *git.Repository
	builder *docker.Builder
	results []BuildResult
}

// NewTimeMachine creates a new TimeMachine instance
func NewTimeMachine(config Config) (*TimeMachine, error) {
	// Open git repository
	repo, err := git.PlainOpen(config.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open git repository at %s: %w", config.RepoPath, err)
	}

	// Create Docker builder
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
		config:  config,
		repo:    repo,
		builder: builder,
		results: []BuildResult{},
	}, nil
}

// Run executes the time machine analysis
func (tm *TimeMachine) Run(ctx context.Context) error {
	// Get commits that modified the Dockerfile
	commits, err := tm.getCommits()
	if err != nil {
		return fmt.Errorf("failed to get commits: %w", err)
	}

	if len(commits) == 0 {
		return fmt.Errorf("no commits found that modified %s", tm.config.DockerfilePath)
	}

	fmt.Fprintf(os.Stderr, "🚀 Found %d commits to analyze\n", len(commits))

	// Create progress bar
	bar := progressbar.NewOptions(len(commits),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWidth(40),
		progressbar.OptionSetDescription("[cyan]Analyzing commits...[reset]"),
	)

	// Store current branch to restore later
	worktree, err := tm.repo.Worktree()
	if err != nil {
		return fmt.Errorf("failed to get worktree: %w", err)
	}

	originalRef, err := tm.repo.Head()
	if err != nil {
		return fmt.Errorf("failed to get HEAD: %w", err)
	}

	// Analyze each commit
	for _, commit := range commits {
		bar.Add(1)

		if tm.config.Verbose {
			fmt.Fprintf(os.Stderr, "\n📦 Building at commit %s: %s\n",
				commit.Hash.String()[:8],
				strings.Split(commit.Message, "\n")[0])
		}

		result := tm.analyzeCommit(ctx, commit)
		tm.results = append(tm.results, result)

		if result.Error != "" && !tm.config.SkipFailed {
			if tm.config.Verbose {
				fmt.Fprintf(os.Stderr, "  ❌ Build failed: %s\n", result.Error)
			}
		}
	}

	// Calculate size differences after all commits are analyzed
	// Commits are ordered newest-first, so we compare each commit to the next one (older)
	// The diff shows how size changed FROM the older commit TO the current (newer) one
	for i := 0; i < len(tm.results); i++ {
		if tm.results[i].Error != "" {
			continue
		}
		// Find the next (older) successful build to compare against
		for j := i + 1; j < len(tm.results); j++ {
			if tm.results[j].Error == "" {
				tm.results[i].SizeDiff = tm.results[i].ImageSize - tm.results[j].ImageSize
				break
			}
		}
		// If no older commit found, SizeDiff remains 0 (the oldest commit has no diff)
	}

	// Restore original branch
	var checkoutErr error
	if originalRef.Name().IsBranch() {
		// Was on a branch - restore the branch (not detached)
		checkoutErr = worktree.Checkout(&git.CheckoutOptions{
			Branch: originalRef.Name(),
			Force:  true,
		})
	} else {
		// Was detached HEAD - restore the commit
		checkoutErr = worktree.Checkout(&git.CheckoutOptions{
			Hash:  originalRef.Hash(),
			Force: true,
		})
	}

	if checkoutErr != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to restore original branch: %v\n", checkoutErr)
	}

	fmt.Fprintf(os.Stderr, "\n")
	return nil
}

// getCommits retrieves all commits
func (tm *TimeMachine) getCommits() ([]*object.Commit, error) {
	var commits []*object.Commit
	seen := make(map[string]bool) // Track seen commit hashes to avoid duplicates

	// Parse date filters if provided
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
			// Try to resolve as a commit hash
			hash := plumbing.NewHash(tm.config.Branch)
			if _, err := tm.repo.CommitObject(hash); err == nil {
				ref = plumbing.NewHashReference(plumbing.HEAD, hash)
			} else {
				return nil, fmt.Errorf("failed to resolve reference %s: %w", tm.config.Branch, err)
			}
		}
	}

	// Get commit iterator - all commits, not filtered by file
	commitIter, err := tm.repo.Log(&git.LogOptions{
		From: ref.Hash(),
		All:  false,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get log: %w", err)
	}

	count := 0
	err = commitIter.ForEach(func(c *object.Commit) error {
		// Skip duplicate commits (can happen with merge commits in git history)
		commitHash := c.Hash.String()
		if seen[commitHash] {
			return nil
		}
		seen[commitHash] = true

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

	// Determine context path and Dockerfile name
	contextPath := tm.config.RepoPath
	dockerfileName := tm.config.DockerfilePath

	// If Dockerfile path contains directory, adjust context and dockerfile name
	if strings.Contains(tm.config.DockerfilePath, "/") {
		dir := filepath.Dir(tm.config.DockerfilePath)
		contextPath = filepath.Join(tm.config.RepoPath, dir)
		dockerfileName = filepath.Base(tm.config.DockerfilePath)
	}

	// Build the image
	err = tm.builder.BuildImage(ctx, contextPath, dockerfileName, imageName)
	if err != nil {
		result.Error = fmt.Sprintf("build failed: %v", err)
		return result
	}

	result.BuildTime = time.Since(startTime).Seconds()

	// Get image information
	imageInfo, err := tm.builder.GetImageInfo(ctx, imageName)
	if err != nil {
		result.Error = fmt.Sprintf("failed to inspect image: %v", err)
		tm.builder.RemoveImage(ctx, imageName)
		return result
	}

	result.ImageSize = imageInfo.Size
	result.LayerCount = len(imageInfo.RootFS.Layers)

	// Get layer history for detailed layer information
	history, err := tm.builder.GetImageHistory(ctx, imageName)
	if err == nil {
		for _, layer := range history {
			// Skip empty layers (metadata-only)
			if layer.Size == 0 {
				continue
			}
			layerInfo := LayerInfo{
				ID:        layer.ID,
				CreatedBy: truncateLayerCommand(layer.CreatedBy),
				Size:      layer.Size,
				SizeMB:    float64(layer.Size) / 1024 / 1024,
			}
			result.Layers = append(result.Layers, layerInfo)
		}
	}

	// Clean up the image
	tm.builder.RemoveImage(ctx, imageName)

	return result
}

// buildLayerComparison builds layer comparison data across commits
func (tm *TimeMachine) buildLayerComparison(validResults []BuildResult) ([]string, []LayerComparison) {
	// Collect all unique layer commands across all commits
	layerCommands := make([]string, 0)
	layerCommandSet := make(map[string]bool)

	// Use the latest commit's layers as the base order
	if len(validResults) > 0 {
		for _, layer := range validResults[0].Layers {
			if !layerCommandSet[layer.CreatedBy] {
				layerCommands = append(layerCommands, layer.CreatedBy)
				layerCommandSet[layer.CreatedBy] = true
			}
		}
	}

	// Add any layers from other commits that aren't in the latest
	for _, result := range validResults[1:] {
		for _, layer := range result.Layers {
			if !layerCommandSet[layer.CreatedBy] {
				layerCommands = append(layerCommands, layer.CreatedBy)
				layerCommandSet[layer.CreatedBy] = true
			}
		}
	}

	// Build comparison data
	comparisons := make([]LayerComparison, 0, len(layerCommands))
	for _, cmd := range layerCommands {
		comparison := LayerComparison{
			LayerCommand: cmd,
			SizeByCommit: make(map[string]float64),
		}

		for _, result := range validResults {
			// Find this layer in the commit
			found := false
			for _, layer := range result.Layers {
				if layer.CreatedBy == cmd {
					comparison.SizeByCommit[result.CommitHash[:8]] = layer.SizeMB
					found = true
					break
				}
			}
			if !found {
				comparison.SizeByCommit[result.CommitHash[:8]] = -1 // -1 indicates not present
			}
		}

		comparisons = append(comparisons, comparison)
	}

	return layerCommands, comparisons
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
	case "markdown":
		return tm.generateMarkdownReport(writer)
	case "chart":
		return tm.generateHTMLChart(writer)
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

	// Print layer comparison across commits
	if len(validResults) > 0 {
		layerCommands, _ := tm.buildLayerComparison(validResults)

		if len(layerCommands) > 0 {
			fmt.Fprintln(w, "\n📦 Layer Size Comparison Across Commits:")
			fmt.Fprintln(w, "-----------------------------------------")

			// Build header: Layer | commit1 | commit2 | ...
			header := []string{"Layer"}
			for _, result := range validResults {
				header = append(header, result.CommitHash[:8])
			}

			layerTable := tablewriter.NewWriter(w)
			layerTable.SetHeader(header)
			layerTable.SetBorder(false)
			layerTable.SetAutoWrapText(false)
			layerTable.SetColumnSeparator(" ")
			layerTable.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
			layerTable.SetAlignment(tablewriter.ALIGN_LEFT)

			// For each layer command, show its size in each commit
			for _, cmd := range layerCommands {
				row := []string{truncate(cmd, 40)}

				for _, result := range validResults {
					// Find this layer in the commit
					found := false
					for _, layer := range result.Layers {
						if layer.CreatedBy == cmd {
							row = append(row, fmt.Sprintf("%.2f", layer.SizeMB))
							found = true
							break
						}
					}
					if !found {
						row = append(row, "-")
					}
				}

				layerTable.Append(row)
			}

			layerTable.Render()
		}
	}

	return nil
}

// JSONReport is the structure for JSON output
type JSONReport struct {
	Results         []BuildResult     `json:"results"`
	LayerComparison []LayerComparison `json:"layer_comparison"`
	CommitOrder     []string          `json:"commit_order"`
}

// generateJSONReport outputs results as JSON
func (tm *TimeMachine) generateJSONReport(w io.Writer) error {
	var validResults []BuildResult
	for _, result := range tm.results {
		if result.Error == "" {
			validResults = append(validResults, result)
		}
	}

	// Build commit order
	commitOrder := make([]string, 0, len(validResults))
	for _, result := range validResults {
		commitOrder = append(commitOrder, result.CommitHash[:8])
	}

	// Build layer comparison
	_, comparisons := tm.buildLayerComparison(validResults)

	report := JSONReport{
		Results:         tm.results,
		LayerComparison: comparisons,
		CommitOrder:     commitOrder,
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

// generateCSVReport outputs results as CSV
func (tm *TimeMachine) generateCSVReport(w io.Writer) error {
	var validResults []BuildResult
	for _, result := range tm.results {
		if result.Error == "" {
			validResults = append(validResults, result)
		}
	}

	// Part 1: Main results
	fmt.Fprintln(w, "# Commit Results")
	fmt.Fprintln(w, "commit,date,author,size_mb,diff_mb,layers,time_s,message")
	for _, result := range tm.results {
		if result.Error != "" {
			continue
		}

		diff := ""
		if result.SizeDiff != 0 {
			sign := "+"
			if result.SizeDiff < 0 {
				sign = ""
			}
			diff = fmt.Sprintf("%s%.1f", sign, float64(result.SizeDiff)/1024/1024)
		}

		fmt.Fprintf(w, "%s,%s,%s,%.2f,%s,%d,%.1f,\"%s\"\n",
			result.CommitHash[:8],
			result.Date.Format("2006-01-02"),
			result.Author,
			float64(result.ImageSize)/1024/1024,
			diff,
			result.LayerCount,
			result.BuildTime,
			strings.ReplaceAll(result.CommitMessage, "\"", "\"\""),
		)
	}

	// Part 2: Layer comparison
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# Layer Size Comparison (MB)")

	// Build header
	header := []string{"layer_command"}
	for _, result := range validResults {
		header = append(header, result.CommitHash[:8])
	}
	fmt.Fprintln(w, strings.Join(header, ","))

	// Build rows
	layerCommands, comparisons := tm.buildLayerComparison(validResults)
	for i, cmd := range layerCommands {
		row := []string{fmt.Sprintf("\"%s\"", strings.ReplaceAll(cmd, "\"", "\"\""))}

		for _, result := range validResults {
			size := comparisons[i].SizeByCommit[result.CommitHash[:8]]
			if size < 0 {
				row = append(row, "-")
			} else {
				row = append(row, fmt.Sprintf("%.2f", size))
			}
		}

		fmt.Fprintln(w, strings.Join(row, ","))
	}

	return nil
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
		fmt.Fprintln(w, "| Commit | Date | Author | Size (MB) | Diff | Layers | Time (s) | Message |")
		fmt.Fprintln(w, "|--------|------|--------|-----------|------|--------|----------|---------|")

		for _, r := range validResults {
			diff := ""
			if r.SizeDiff != 0 {
				sign := "+"
				if r.SizeDiff < 0 {
					sign = ""
				}
				diff = fmt.Sprintf("%s%.1f", sign, float64(r.SizeDiff)/1024/1024)
			}
			fmt.Fprintf(w, "| %s | %s | %s | %.2f | %s | %d | %.1f | %s |\n",
				r.CommitHash[:8],
				r.Date.Format("2006-01-02"),
				truncate(r.Author, 12),
				float64(r.ImageSize)/1024/1024,
				diff,
				r.LayerCount,
				r.BuildTime,
				truncate(r.CommitMessage, 40),
			)
		}

		// Layer comparison table
		layerCommands, comparisons := tm.buildLayerComparison(validResults)
		if len(layerCommands) > 0 {
			fmt.Fprintln(w)
			fmt.Fprintln(w, "## Layer Size Comparison Across Commits")
			fmt.Fprintln(w)

			// Build header
			header := "| Layer |"
			separator := "|-------|"
			for _, result := range validResults {
				header += fmt.Sprintf(" %s |", result.CommitHash[:8])
				separator += "----------|"
			}
			fmt.Fprintln(w, header)
			fmt.Fprintln(w, separator)

			// Build rows
			for i, cmd := range layerCommands {
				row := fmt.Sprintf("| `%s` |", truncate(cmd, 40))

				for _, result := range validResults {
					size := comparisons[i].SizeByCommit[result.CommitHash[:8]]
					if size < 0 {
						row += " - |"
					} else {
						row += fmt.Sprintf(" %.2f |", size)
					}
				}

				fmt.Fprintln(w, row)
			}
		}
	}

	return nil
}

// generateHTMLChart creates an interactive HTML chart
func (tm *TimeMachine) generateHTMLChart(w io.Writer) error {
	// Filter out failed builds
	var validResults []BuildResult
	for _, r := range tm.results {
		if r.Error == "" {
			validResults = append(validResults, r)
		}
	}

	// Prepare data for charts
	var labels []string
	var sizeData []float64
	var timeData []float64

	for _, r := range validResults {
		labels = append(labels, r.CommitHash[:8])
		sizeData = append(sizeData, float64(r.ImageSize)/1024/1024)
		timeData = append(timeData, r.BuildTime)
	}

	// Build layer comparison data for stacked chart
	layerCommands, comparisons := tm.buildLayerComparison(validResults)

	// Prepare stacked layer data - each layer becomes a dataset
	type LayerDataset struct {
		Label           string    `json:"label"`
		Data            []float64 `json:"data"`
		BackgroundColor string    `json:"backgroundColor"`
	}

	colors := []string{
		"rgba(75, 192, 192, 0.8)",
		"rgba(255, 99, 132, 0.8)",
		"rgba(255, 206, 86, 0.8)",
		"rgba(54, 162, 235, 0.8)",
		"rgba(153, 102, 255, 0.8)",
		"rgba(255, 159, 64, 0.8)",
		"rgba(199, 199, 199, 0.8)",
		"rgba(83, 102, 255, 0.8)",
		"rgba(255, 99, 255, 0.8)",
		"rgba(99, 255, 132, 0.8)",
	}

	var stackedDatasets []LayerDataset
	for i, cmd := range layerCommands {
		dataset := LayerDataset{
			Label:           truncate(cmd, 50),
			Data:            make([]float64, len(validResults)),
			BackgroundColor: colors[i%len(colors)],
		}

		for j, result := range validResults {
			size := comparisons[i].SizeByCommit[result.CommitHash[:8]]
			if size < 0 {
				dataset.Data[j] = 0
			} else {
				dataset.Data[j] = size
			}
		}

		stackedDatasets = append(stackedDatasets, dataset)
	}

	// Convert stacked datasets to JSON
	stackedDatasetsJSON, _ := json.Marshal(stackedDatasets)

	// Build layer comparison table data for HTML
	type LayerTableRow struct {
		Command string             `json:"command"`
		Sizes   map[string]float64 `json:"sizes"`
	}

	var layerTableData []LayerTableRow
	for i, cmd := range layerCommands {
		row := LayerTableRow{
			Command: cmd,
			Sizes:   comparisons[i].SizeByCommit,
		}
		layerTableData = append(layerTableData, row)
	}
	layerTableJSON, _ := json.Marshal(layerTableData)

	// Generate HTML with Chart.js
	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <title>Docker Image Evolution Report</title>
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            margin: 0;
            padding: 20px;
            background: #f5f5f5;
            color: #333;
        }
        h1 {
            color: #333;
            margin-bottom: 30px;
        }
        h2 {
            color: #555;
            margin-top: 0;
            font-size: 1.2em;
        }
        .chart-container {
            background: white;
            border-radius: 8px;
            padding: 20px;
            margin: 20px 0;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        canvas {
            max-height: 400px;
        }
        .note {
            font-size: 0.85em;
            color: #666;
            font-style: italic;
            margin-top: 10px;
        }
        .layer-table-container {
            overflow-x: auto;
        }
        .layer-table {
            width: 100%%;
            border-collapse: collapse;
            margin-top: 15px;
            font-size: 0.9em;
        }
        .layer-table th, .layer-table td {
            padding: 10px 12px;
            text-align: left;
            border-bottom: 1px solid #eee;
            white-space: nowrap;
        }
        .layer-table th {
            background: #f8f9fa;
            font-weight: 600;
            position: sticky;
            top: 0;
        }
        .layer-table th:first-child {
            position: sticky;
            left: 0;
            z-index: 2;
            background: #f8f9fa;
        }
        .layer-table td:first-child {
            position: sticky;
            left: 0;
            background: white;
            max-width: 300px;
            overflow: hidden;
            text-overflow: ellipsis;
            font-family: 'Monaco', 'Menlo', monospace;
            font-size: 0.85em;
        }
        .layer-table tr:hover td {
            background: #f8f9fa;
        }
        .layer-table tr:hover td:first-child {
            background: #f0f0f0;
        }
        .size-cell {
            text-align: right;
            font-family: 'Monaco', 'Menlo', monospace;
        }
        .size-cell.missing {
            color: #999;
        }
    </style>
</head>
<body>
    <h1>🐳 Docker Image Evolution Report</h1>
    
    <div class="chart-container">
        <h2>📈 Image Size Over Time</h2>
        <canvas id="sizeChart"></canvas>
    </div>
    
    <div class="chart-container">
        <h2>📊 Image Size by Layer</h2>
        <canvas id="stackedLayerChart"></canvas>
        <p class="note">Each color represents a different layer. Hover over bars to see layer details.</p>
    </div>
    
    <div class="chart-container">
        <h2>⏱️ Build Time Analysis</h2>
        <canvas id="timeChart"></canvas>
        <p class="note">Build times are indicative only - they depend on Docker's layer cache state and system load at the time of analysis.</p>
    </div>

    <div class="chart-container">
        <h2>📦 Layer Size Comparison Across Commits</h2>
        <div class="layer-table-container">
            <table class="layer-table" id="layerComparisonTable">
                <thead>
                    <tr id="layerTableHeader">
                        <th>Layer Command</th>
                    </tr>
                </thead>
                <tbody id="layerTableBody">
                </tbody>
            </table>
        </div>
    </div>

    <script>
        const labels = %s;
        const sizeData = %s;
        const timeData = %s;
        const stackedDatasets = %s;
        const layerTableData = %s;
        
        // Image Size Over Time Chart
        new Chart(document.getElementById('sizeChart'), {
            type: 'line',
            data: {
                labels: labels,
                datasets: [{
                    label: 'Image Size (MB)',
                    data: sizeData,
                    borderColor: 'rgb(75, 192, 192)',
                    backgroundColor: 'rgba(75, 192, 192, 0.2)',
                    tension: 0.1,
                    fill: true,
                    pointRadius: 4,
                    pointHoverRadius: 6
                }]
            },
            options: {
                responsive: true,
                plugins: {
                    legend: {
                        display: false
                    }
                },
                scales: {
                    y: {
                        beginAtZero: true,
                        title: {
                            display: true,
                            text: 'Size (MB)'
                        }
                    },
                    x: {
                        title: {
                            display: true,
                            text: 'Commit'
                        }
                    }
                }
            }
        });

        // Stacked Layer Chart
        new Chart(document.getElementById('stackedLayerChart'), {
            type: 'bar',
            data: {
                labels: labels,
                datasets: stackedDatasets
            },
            options: {
                responsive: true,
                plugins: {
                    legend: {
                        display: true,
                        position: 'bottom',
                        labels: {
                            boxWidth: 12,
                            font: {
                                size: 10
                            }
                        }
                    },
                    tooltip: {
                        callbacks: {
                            label: function(context) {
                                return context.dataset.label + ': ' + context.raw.toFixed(2) + ' MB';
                            }
                        }
                    }
                },
                scales: {
                    x: {
                        stacked: true,
                        title: {
                            display: true,
                            text: 'Commit'
                        }
                    },
                    y: {
                        stacked: true,
                        beginAtZero: true,
                        title: {
                            display: true,
                            text: 'Size (MB)'
                        }
                    }
                }
            }
        });
        
        // Build Time Chart
        new Chart(document.getElementById('timeChart'), {
            type: 'bar',
            data: {
                labels: labels,
                datasets: [{
                    label: 'Build Time (seconds)',
                    data: timeData,
                    backgroundColor: 'rgba(255, 159, 64, 0.6)',
                    borderColor: 'rgb(255, 159, 64)',
                    borderWidth: 1
                }]
            },
            options: {
                responsive: true,
                plugins: {
                    legend: {
                        display: false
                    }
                },
                scales: {
                    y: {
                        beginAtZero: true,
                        title: {
                            display: true,
                            text: 'Time (seconds)'
                        }
                    },
                    x: {
                        title: {
                            display: true,
                            text: 'Commit'
                        }
                    }
                }
            }
        });

        // Populate layer comparison table
        const headerRow = document.getElementById('layerTableHeader');
        const tbody = document.getElementById('layerTableBody');

        // Add commit columns to header
        labels.forEach(commit => {
            const th = document.createElement('th');
            th.textContent = commit;
            th.style.textAlign = 'right';
            headerRow.appendChild(th);
        });

        // Add rows for each layer
        layerTableData.forEach(layer => {
            const row = document.createElement('tr');
            
            // Layer command cell
            const cmdCell = document.createElement('td');
            cmdCell.textContent = layer.command;
            cmdCell.title = layer.command;
            row.appendChild(cmdCell);

            // Size cells for each commit
            labels.forEach(commit => {
                const cell = document.createElement('td');
                cell.className = 'size-cell';
                const size = layer.sizes[commit];
                if (size === undefined || size < 0) {
                    cell.textContent = '-';
                    cell.classList.add('missing');
                } else {
                    cell.textContent = size.toFixed(2);
                }
                row.appendChild(cell);
            });

            tbody.appendChild(row);
        });
    </script>
</body>
</html>`,
		toJSONArray(labels),
		toJSONFloatArray(sizeData),
		toJSONFloatArray(timeData),
		string(stackedDatasetsJSON),
		string(layerTableJSON),
	)

	_, err := w.Write([]byte(html))
	return err
}

// Helper functions
func (tm *TimeMachine) findBloatCommit() *BuildResult {
	var maxIncrease int64
	var bloatCommit *BuildResult

	for i := range tm.results {
		if tm.results[i].Error == "" && tm.results[i].SizeDiff > maxIncrease {
			maxIncrease = tm.results[i].SizeDiff
			bloatCommit = &tm.results[i]
		}
	}

	return bloatCommit
}

func (tm *TimeMachine) findOptimizationCommit() *BuildResult {
	var maxDecrease int64
	var optimizationCommit *BuildResult

	for i := range tm.results {
		if tm.results[i].Error == "" && tm.results[i].SizeDiff < maxDecrease {
			maxDecrease = tm.results[i].SizeDiff
			optimizationCommit = &tm.results[i]
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

func truncateLayerCommand(cmd string) string {
	// Clean up the command string - remove /bin/sh -c prefix
	// NOTE: We do NOT truncate here to preserve uniqueness for layer matching
	// Truncation happens only at display time via truncate() function
	cmd = strings.TrimPrefix(cmd, "/bin/sh -c ")
	cmd = strings.TrimPrefix(cmd, "#(nop) ")
	cmd = strings.TrimSpace(cmd)
	return cmd
}

func toJSONArray(arr []string) string {
	bytes, _ := json.Marshal(arr)
	return string(bytes)
}

func toJSONFloatArray(arr []float64) string {
	bytes, _ := json.Marshal(arr)
	return string(bytes)
}

func toJSONIntArray(arr []int) string {
	bytes, _ := json.Marshal(arr)
	return string(bytes)
}

// AnalyzeCommit is exported for use by bisect package
func (tm *TimeMachine) AnalyzeCommit(ctx context.Context, commit *object.Commit) BuildResult {
	return tm.analyzeCommit(ctx, commit)
}
