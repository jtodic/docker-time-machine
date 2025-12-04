package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	verbose bool
)

var rootCmd = &cobra.Command{
	Use:   "dtm",
	Short: "Docker Time Machine - Track Docker image evolution through git history",
	Long: `DTM (Docker Time Machine) helps you understand how your Docker images have
evolved over time by building them at different points in your git history.

Use DTM to:

  • Find the exact commit that introduced image bloat
  • Track build time trends across your project's history
  • Monitor layer count changes over time
  • Generate reports to share with your team
  • Compare image metrics between branches or tags

DTM walks through your git history, builds the Docker image at each commit,
and records key metrics. It then generates reports showing trends and
highlighting significant changes—both regressions and optimizations.

Getting started:
  Run 'dtm analyze' in a git repository containing a Dockerfile to generate
  your first report. Use 'dtm analyze --help' for all available options.`,
	Example: `  # Quick analysis of current repo
  dtm analyze

  # Generate HTML charts for visualization
  dtm analyze --format chart --output report.html

  # Analyze last 10 commits with verbose output
  dtm analyze -n 10 -v

  # Export to JSON for further processing
  dtm analyze --format json --output metrics.json`,
	Version: "0.2.0",
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "verbose output")
}
