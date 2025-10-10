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
	Short: "Dockerfile Time Machine - Track Docker image evolution through git history",
	Long: `DTM (Dockerfile Time Machine) analyzes how your Docker images have evolved 
over time by building images at different points in your git history.

It helps identify when and why your images became bloated, tracks build time trends,
and provides insights for optimization.

Examples:
  dtm analyze                     # Analyze current repo
  dtm analyze --format chart       # Generate HTML charts
  dtm bisect --size-threshold 500  # Find when image exceeded 500MB
  dtm compare -a main -b develop  # Compare branches`,
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
