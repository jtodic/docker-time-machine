package cmd

import (
	"context"
	"fmt"
	"log"

	"github.com/jtodic/dockerfile-time-machine/pkg/bisect"
	"github.com/spf13/cobra"
)

var (
	sizeThreshold float64
	timeThreshold float64
)

var bisectCmd = &cobra.Command{
	Use:   "bisect",
	Short: "Find the commit that introduced size/time regression",
	Long:  `Use binary search to efficiently find the commit where image size or build time crossed a threshold.`,
	Run: func(cmd *cobra.Command, args []string) {
		b := bisect.NewBisector(repoPath, dockerfile)

		ctx := context.Background()
		commit, err := b.FindRegression(ctx, sizeThreshold, timeThreshold)
		if err != nil {
			log.Fatal(err)
		}

		fmt.Printf("🎯 Found regression at commit: %s\n", commit)
	},
}

func init() {
	bisectCmd.Flags().StringVarP(&repoPath, "repo", "r", ".", "Path to git repository")
	bisectCmd.Flags().StringVarP(&dockerfile, "dockerfile", "d", "Dockerfile", "Path to Dockerfile")
	bisectCmd.Flags().Float64Var(&sizeThreshold, "size-threshold", 0, "Size threshold in MB")
	bisectCmd.Flags().Float64Var(&timeThreshold, "time-threshold", 0, "Build time threshold in seconds")
}
