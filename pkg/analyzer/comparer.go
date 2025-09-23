package analyzer

import "context"

type ComparerConfig struct {
	RepoPath       string
	DockerfilePath string
	Verbose        bool
}

type ComparisonResult struct {
	BranchA              BranchInfo `json:"branch_a"`
	BranchB              BranchInfo `json:"branch_b"`
	SizeDiff             float64    `json:"size_diff_mb"`
	SizeDiffPercent      float64    `json:"size_diff_percent"`
	LayersDiff           int        `json:"layers_diff"`
	BuildTimeDiff        float64    `json:"build_time_diff"`
	BuildTimeDiffPercent float64    `json:"build_time_diff_percent"`
}

type BranchInfo struct {
	Name      string  `json:"name"`
	Commit    string  `json:"commit"`
	SizeMB    float64 `json:"size_mb"`
	Layers    int     `json:"layers"`
	BuildTime float64 `json:"build_time"`
}

type Comparer struct {
	config ComparerConfig
}

func NewComparer(config ComparerConfig) (*Comparer, error) {
	return &Comparer{config: config}, nil
}

func (c *Comparer) Compare(ctx context.Context, branchA, branchB string) (*ComparisonResult, error) {
	// Implementation here
	return &ComparisonResult{}, nil
}
