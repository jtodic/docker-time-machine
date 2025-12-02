package cmd

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/jtodic/docker-time-machine/pkg/analyzer"
	"github.com/spf13/cobra"
)

var analyzeFlags struct {
	repoPath       string
	dockerfilePath string
	format         string
	maxCommits     int
	branch         string
	output         string
	since          string
	until          string
	skipFailed     bool
}

var analyzeCmd = &cobra.Command{
	Use:   "analyze",
	Short: "Analyze Docker image evolution across git history",
	Long: `Build Docker images at each commit in your repository's history to track
how image size, build time, and layer count have changed over time.

The analyzer walks through commits following git's parent chain (depth-first
traversal), not chronological order. At each commit, it:

  • Checks out the repository state
  • Builds the Docker image using the specified Dockerfile
  • Records metrics: image size, layer count, build time
  • Calculates size deltas between consecutive successful builds
  • Cleans up temporary images

Output formats include interactive HTML charts, tables, JSON, CSV, and Markdown.
The tool automatically identifies commits that caused the largest size increases
(bloat) or decreases (optimizations).

Note: Build times are indicative only—they depend on Docker's layer cache state
and system load at the time of analysis.`,
	Example: `  # Analyze current repo with default settings (last 20 commits, table output)
  dtm analyze

  # Generate an interactive HTML report with charts
  dtm analyze --format chart --output report.html

  # Analyze a specific branch, limiting to 50 commits
  dtm analyze --branch develop --max-commits 50

  # Analyze commits within a date range
  dtm analyze --since "2024-01-01" --until "2024-12-31"

  # Analyze a different repo with a custom Dockerfile path
  dtm analyze --repo /path/to/project --dockerfile build/Dockerfile

  # Skip commits that fail to build and continue analysis
  dtm analyze --skip-failed`,
	RunE: runAnalyze,
}

func init() {
	rootCmd.AddCommand(analyzeCmd)

	analyzeCmd.Flags().StringVarP(&analyzeFlags.repoPath, "repo", "r", ".", "Path to git repository")
	analyzeCmd.Flags().StringVarP(&analyzeFlags.dockerfilePath, "dockerfile", "d", "Dockerfile", "Path to Dockerfile relative to repo root")
	analyzeCmd.Flags().StringVarP(&analyzeFlags.format, "format", "f", "table", "Output format: table, json, csv, chart, markdown")
	analyzeCmd.Flags().IntVarP(&analyzeFlags.maxCommits, "max-commits", "n", 20, "Maximum commits to analyze (0 = all)")
	analyzeCmd.Flags().StringVarP(&analyzeFlags.branch, "branch", "b", "", "Git branch to analyze (default: current branch)")
	analyzeCmd.Flags().StringVarP(&analyzeFlags.output, "output", "o", "", "Output file path (default: stdout for table/json/csv/markdown, auto-generated timestamped file for chart)")
	analyzeCmd.Flags().StringVar(&analyzeFlags.since, "since", "", "Analyze commits since date (YYYY-MM-DD)")
	analyzeCmd.Flags().StringVar(&analyzeFlags.until, "until", "", "Analyze commits until date (YYYY-MM-DD)")
	analyzeCmd.Flags().BoolVar(&analyzeFlags.skipFailed, "skip-failed", false, "Skip commits that fail to build")
}

func runAnalyze(cmd *cobra.Command, args []string) error {
	// Create analyzer config
	config := analyzer.Config{
		RepoPath:       analyzeFlags.repoPath,
		DockerfilePath: analyzeFlags.dockerfilePath,
		MaxCommits:     analyzeFlags.maxCommits,
		Branch:         analyzeFlags.branch,
		Since:          analyzeFlags.since,
		Until:          analyzeFlags.until,
		SkipFailed:     analyzeFlags.skipFailed,
		Verbose:        verbose,
	}

	//fmt.Printf("\n%+v\n\n", config)

	// Create analyzer
	tm, err := analyzer.NewTimeMachine(config)
	if err != nil {
		return fmt.Errorf("failed to create analyzer: %w", err)
	}

	//fmt.Printf("\n%+v\n\n", tm)

	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "🔍 Analyzing repository: %s\n", analyzeFlags.repoPath)
	fmt.Fprintf(os.Stderr, "📄 Dockerfile: %s\n", analyzeFlags.dockerfilePath)

	// Run analysis
	if err := tm.Run(ctx); err != nil {
		return fmt.Errorf("analysis failed: %w", err)
	}

	// Setup output writer
	var output *os.File
	if analyzeFlags.output != "" {
		var err error
		output, err = os.Create(analyzeFlags.output)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer output.Close()
	} else {
		// If format is chart and no output specified, create timestamped filename
		if analyzeFlags.format == "chart" {
			timestamp := time.Now().Format("2006-01-02-150405")
			filename := fmt.Sprintf("report-%s.html", timestamp)
			var err error
			output, err = os.Create(filename)
			if err != nil {
				return fmt.Errorf("failed to create output file: %w", err)
			}
			defer output.Close()
			analyzeFlags.output = filename // Store for the success message
		} else {
			output = os.Stdout
		}
	}

	// Generate report
	if err := tm.GenerateReport(analyzeFlags.format, output); err != nil {
		return fmt.Errorf("failed to generate report: %w", err)
	}

	if analyzeFlags.output != "" {
		fmt.Fprintf(os.Stderr, "✅ Report saved to: %s\n", analyzeFlags.output)
	}

	return nil
}
