package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/yourusername/dockerfile-time-machine/pkg/bisect"
)

var bisectFlags struct {
	repoPath       string
	dockerfilePath string
	sizeThreshold  float64
	timeThreshold  float64
	goodCommit     string
	badCommit      string
}

var bisectCmd = &cobra.Command{
	Use:   "bisect",
	Short: "Find the commit that introduced size/time regression",
	Long: `Use binary search to efficiently find the commit where image size or build time crossed a threshold.
	
Similar to git bisect, this command will:
1. Start with a known good commit and bad commit
2. Test the midpoint between them
3. Narrow down to the exact commit that introduced the regression`,
	Example: `  dtm bisect --size-threshold 500
  dtm bisect --time-threshold 120
  dtm bisect --good abc123 --bad def456 --size-threshold 1000`,
	RunE: runBisect,
}

func init() {
	rootCmd.AddCommand(bisectCmd)

	bisectCmd.Flags().StringVarP(&bisectFlags.repoPath, "repo", "r", ".", "Path to git repository")
	bisectCmd.Flags().StringVarP(&bisectFlags.dockerfilePath, "dockerfile", "d", "Dockerfile", "Path to Dockerfile")
	bisectCmd.Flags().Float64Var(&bisectFlags.sizeThreshold, "size-threshold", 0, "Size threshold in MB")
	bisectCmd.Flags().Float64Var(&bisectFlags.timeThreshold, "time-threshold", 0, "Build time threshold in seconds")
	bisectCmd.Flags().StringVar(&bisectFlags.goodCommit, "good", "", "Known good commit (optional)")
	bisectCmd.Flags().StringVar(&bisectFlags.badCommit, "bad", "", "Known bad commit (optional)")

	bisectCmd.MarkFlagsMutuallyExclusive("size-threshold", "time-threshold")
}

func runBisect(cmd *cobra.Command, args []string) error {
	if bisectFlags.sizeThreshold == 0 && bisectFlags.timeThreshold == 0 {
		return fmt.Errorf("either --size-threshold or --time-threshold must be specified")
	}

	config := bisect.Config{
		RepoPath:       bisectFlags.repoPath,
		DockerfilePath: bisectFlags.dockerfilePath,
		SizeThreshold:  bisectFlags.sizeThreshold,
		TimeThreshold:  bisectFlags.timeThreshold,
		GoodCommit:     bisectFlags.goodCommit,
		BadCommit:      bisectFlags.badCommit,
		Verbose:        verbose,
	}

	b, err := bisect.NewBisector(config)
	if err != nil {
		return fmt.Errorf("failed to create bisector: %w", err)
	}

	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "🔎 Starting bisect...\n")

	result, err := b.FindRegression(ctx)
	if err != nil {
		return fmt.Errorf("bisect failed: %w", err)
	}

	fmt.Printf("\n🎯 Found regression at commit: %s\n", result.CommitHash[:8])
	fmt.Printf("📝 Message: %s\n", result.CommitMessage)
	fmt.Printf("👤 Author: %s\n", result.Author)
	fmt.Printf("📅 Date: %s\n", result.Date)

	if bisectFlags.sizeThreshold > 0 {
		fmt.Printf("📦 Size: %.2f MB (threshold: %.2f MB)\n", result.SizeMB, bisectFlags.sizeThreshold)
	}
	if bisectFlags.timeThreshold > 0 {
		fmt.Printf("⏱️  Build time: %.2f seconds (threshold: %.2f seconds)\n", result.BuildTime, bisectFlags.timeThreshold)
	}

	return nil
}
