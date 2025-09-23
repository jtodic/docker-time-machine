package bisect

import (
	"context"
	"time"
)

type Config struct {
	RepoPath       string
	DockerfilePath string
	SizeThreshold  float64
	TimeThreshold  float64
	GoodCommit     string
	BadCommit      string
	Verbose        bool
}

type Result struct {
	CommitHash    string
	CommitMessage string
	Author        string
	Date          time.Time
	SizeMB        float64
	BuildTime     float64
}

type Bisector struct {
	config Config
}

func NewBisector(config Config) (*Bisector, error) {
	return &Bisector{config: config}, nil
}

func (b *Bisector) FindRegression(ctx context.Context) (*Result, error) {
	// Implementation here
	return &Result{}, nil
}
