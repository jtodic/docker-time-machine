package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"github.com/yourusername/dockerfile-time-machine/pkg/analyzer"
)

var compareFlags struct {
	repoPath       string
	dockerfilePath string
	branchA        string
	branchB        string
	format         string
}

var compareCmd = &cobra.Command{
	Use:   "compare",
	Short: "Compare Docker images between branches",
	Long:  `Build and compare Docker images from different branches to see size and layer differences.`,
	Example: `  dtm compare --branch-a main --branch-b feature/optimization
  dtm compare -a v1.0 -b v2.0 --format json`,
	RunE: runCompare,
}

func init() {
	rootCmd.AddCommand(compareCmd)

	compareCmd.Flags().StringVarP(&compareFlags.repoPath, "repo", "r", ".", "Path to git repository")
	compareCmd.Flags().StringVarP(&compareFlags.dockerfilePath, "dockerfile", "d", "Dockerfile", "Path to Dockerfile")
	compareCmd.Flags().StringVarP(&compareFlags.branchA, "branch-a", "a", "main", "First branch to compare")
	compareCmd.Flags().StringVarP(&compareFlags.branchB, "branch-b", "b", "", "Second branch to compare")
	compareCmd.Flags().StringVarP(&compareFlags.format, "format", "f", "table", "Output format: table, json")
	
	compareCmd.MarkFlagRequired("branch-b")
}

func runCompare(cmd *cobra.Command, args []string) error {
	comparer, err := analyzer.NewComparer(analyzer.ComparerConfig{
		RepoPath:       compareFlags.repoPath,
		DockerfilePath: compareFlags.dockerfilePath,
		Verbose:        verbose,
	})
	if err != nil {
		return fmt.Errorf("failed to create comparer: %w", err)
	}

	ctx := context.Background()
	
	fmt.Fprintf(os.Stderr, "📊 Comparing branches: %s vs %s\n", compareFlags.branchA, compareFlags.branchB)
	
	result, err := comparer.Compare(ctx, compareFlags.branchA, compareFlags.branchB)
	if err != nil {
		return fmt.Errorf("comparison failed: %w", err)
	}

	// Display results
	if compareFlags.format == "json" {
		// JSON output
		import "encoding/json"
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		return encoder.Encode(result)
	} else {
		// Table format (default)
		fmt.Printf("\n📈 Comparison Results\n")
		fmt.Printf("════════════════════\n\n")
		
		fmt.Printf("Branch A: %s (commit: %s)\n", result.BranchA.Name, result.BranchA.Commit)
		fmt.Printf("  Size: %.2f MB\n", result.BranchA.SizeMB)
		fmt.Printf("  Layers: %d\n", result.BranchA.Layers)
		fmt.Printf("  Build Time: %.2fs\n\n", result.BranchA.BuildTime)
		
		fmt.Printf("Branch B: %s (commit: %s)\n", result.BranchB.Name, result.BranchB.Commit)
		fmt.Printf("  Size: %.2f MB\n", result.BranchB.SizeMB)
		fmt.Printf("  Layers: %d\n", result.BranchB.Layers)
		fmt.Printf("  Build Time: %.2fs\n\n", result.BranchB.BuildTime)
		
		fmt.Printf("Differences:\n")
		fmt.Printf("  Size: %+.2f MB (%+.1f%%)\n", result.SizeDiff, result.SizeDiffPercent)
		fmt.Printf("  Layers: %+d\n", result.LayersDiff)
		fmt.Printf("  Build Time: %+.2fs (%+.1f%%)\n", result.BuildTimeDiff, result.BuildTimeDiffPercent)
	}

	return nil
}
