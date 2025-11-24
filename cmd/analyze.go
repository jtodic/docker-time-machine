package cmd

import (
	"context"
	"fmt"
	"os"

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
	Long: `Build and analyze Docker images at different commits to track size and build time evolution.
	
This command will:
1. Traverse git history for commits that modified the Dockerfile
2. Build the image at each commit point
3. Record size, build time, and layer information
4. Generate a report in the specified format`,
	Example: `  dtm analyze --repo /path/to/repo
  dtm analyze --format chart --output report.html
  dtm analyze --branch develop --max-commits 50
  dtm analyze --since "2024-01-01" --until "2024-12-31"`,
	RunE: runAnalyze,
}

func init() {
	rootCmd.AddCommand(analyzeCmd)

	analyzeCmd.Flags().StringVarP(&analyzeFlags.repoPath, "repo", "r", ".", "Path to git repository")
	analyzeCmd.Flags().StringVarP(&analyzeFlags.dockerfilePath, "dockerfile", "d", "Dockerfile", "Path to Dockerfile relative to repo root")
	analyzeCmd.Flags().StringVarP(&analyzeFlags.format, "format", "f", "table", "Output format: table, json, csv, chart, markdown")
	analyzeCmd.Flags().IntVarP(&analyzeFlags.maxCommits, "max-commits", "n", 20, "Maximum commits to analyze (0 = all)")
	analyzeCmd.Flags().StringVarP(&analyzeFlags.branch, "branch", "b", "", "Git branch to analyze (default: current branch)")
	analyzeCmd.Flags().StringVarP(&analyzeFlags.output, "output", "o", "", "Output file path (default: stdout)")
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

	fmt.Printf("\n%+v\n\n", config)

	// Create analyzer
	tm, err := analyzer.NewTimeMachine(config)
	if err != nil {
		return fmt.Errorf("failed to create analyzer: %w", err)
	}

	fmt.Printf("\n%+v\n\n", tm)

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
		output = os.Stdout
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
