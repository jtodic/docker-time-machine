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
	Short: "Docker Time Machine - Track Docker image evolution through git history and registries",
	Long: `DTM (Docker Time Machine) helps you understand how your Docker images have
evolved over time—either by analyzing tags from a container registry or by
building images at different points in your git history.

Use DTM to:

  • Analyze image size trends directly from registries (Docker Hub, ECR, GCR, GHCR, ACR, JFrog)
  • Find the exact commit that introduced image bloat
  • Track build time trends across your project's history
  • Monitor layer count changes over time
  • Generate reports to share with your team
  • Compare image metrics between branches or tags

Commands:
  registry  - Analyze images directly from a container registry (fast, no rebuilds)
  analyze   - Build and analyze images across git history (requires source code)

Getting started:
  Run 'dtm registry nginx --last 10' to analyze the last 10 tags of nginx.
  Run 'dtm analyze' in a git repository containing a Dockerfile.`,
	Example: `  # Analyze image tags from a registry (fast - no rebuilds needed)
  dtm registry nginx --last 10
  dtm registry mycompany/api --tags "v1.0.0,v1.1.0,v1.2.0"
  dtm registry ghcr.io/owner/repo --format chart

  # Analyze git history by rebuilding at each commit
  dtm analyze
  dtm analyze --format chart --output report.html
  dtm analyze -n 10 -v

  # Export to JSON for further processing
  dtm analyze --format json --output metrics.json`,
	Version: "1.0.0",
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
