package analyzer

import (
	"encoding/json"
	"fmt"
	"io"
	"text/template"
)

// Visualizer creates visual reports from build results
type Visualizer struct {
	results []BuildResult
}

// NewVisualizer creates a new Visualizer
func NewVisualizer(results []BuildResult) *Visualizer {
	return &Visualizer{results: results}
}

// GenerateHTML generates an HTML report with charts
func (v *Visualizer) GenerateHTML(w io.Writer) error {
	// Filter valid results
	var validResults []BuildResult
	for _, r := range v.results {
		if r.Error == "" {
			validResults = append(validResults, r)
		}
	}

	// Prepare data for charts
	chartData := struct {
		Results []BuildResult
		Labels  []string
		Sizes   []float64
		Times   []float64
		Layers  []int
	}{
		Results: validResults,
	}

	for _, r := range validResults {
		chartData.Labels = append(chartData.Labels, r.CommitHash[:8])
		chartData.Sizes = append(chartData.Sizes, float64(r.ImageSize)/1024/1024)
		chartData.Times = append(chartData.Times, r.BuildTime)
		chartData.Layers = append(chartData.Layers, r.LayerCount)
	}

	labelsJSON, _ := json.Marshal(chartData.Labels)
	sizesJSON, _ := json.Marshal(chartData.Sizes)
	timesJSON, _ := json.Marshal(chartData.Times)
	layersJSON, _ := json.Marshal(chartData.Layers)

	tmplStr := `<!DOCTYPE html>
<html>
<head>
    <title>Docker Image Evolution Report</title>
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
    <style>
        * { margin: 0; padding: 0; box-sizing: border-box; }
        body {
            font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, "Helvetica Neue", Arial, sans-serif;
            background: linear-gradient(135deg, #667eea 0%, #764ba2 100%);
            min-height: 100vh;
            padding: 20px;
        }
        .container {
            max-width: 1400px;
            margin: 0 auto;
        }
        h1 {
            color: white;
            text-align: center;
            margin-bottom: 30px;
            font-size: 2.5em;
            text-shadow: 2px 2px 4px rgba(0,0,0,0.2);
        }
        .stats-grid {
            display: grid;
            grid-template-columns: repeat(auto-fit, minmax(250px, 1fr));
            gap: 20px;
            margin-bottom: 30px;
        }
        .stat-card {
            background: white;
            padding: 20px;
            border-radius: 10px;
            box-shadow: 0 4px 6px rgba(0,0,0,0.1);
        }
        .stat-value {
            font-size: 2em;
            font-weight: bold;
            color: #667eea;
        }
        .stat-label {
            color: #666;
            margin-top: 5px;
        }
        .chart-container {
            background: white;
            padding: 20px;
            border-radius: 10px;
            box-shadow: 0 4px 6px rgba(0,0,0,0.1);
            margin-bottom: 20px;
        }
        .chart-title {
            font-size: 1.3em;
            margin-bottom: 15px;
            color: #333;
        }
        canvas {
            max-height: 400px;
        }
        .insights {
            background: white;
            padding: 20px;
            border-radius: 10px;
            box-shadow: 0 4px 6px rgba(0,0,0,0.1);
            margin-top: 30px;
        }
        .insight-item {
            padding: 10px;
            margin: 10px 0;
            border-left: 4px solid #667eea;
            background: #f8f9fa;
        }
        .warning { border-left-color: #f59e0b; }
        .success { border-left-color: #10b981; }
        @media (max-width: 768px) {
            h1 { font-size: 1.8em; }
            .stats-grid { grid-template-columns: 1fr; }
        }
    </style>
</head>
<body>
    <div class="container">
        <h1>🐳 Docker Image Evolution Report</h1>
        
        <div class="stats-grid">
            <div class="stat-card">
                <div class="stat-value">{{len .Results}}</div>
                <div class="stat-label">Commits Analyzed</div>
            </div>
            <div class="stat-card">
                <div class="stat-value">{{index .Sizes 0 | printf "%.1f"}} MB</div>
                <div class="stat-label">Initial Size</div>
            </div>
            <div class="stat-card">
                <div class="stat-value">{{index .Sizes (len .Sizes | add -1) | printf "%.1f"}} MB</div>
                <div class="stat-label">Final Size</div>
            </div>
            <div class="stat-card">
                <div class="stat-value">{{.Sizes | calcChange | printf "%+.1f"}} MB</div>
                <div class="stat-label">Total Change</div>
            </div>
        </div>

        <div class="chart-container">
            <div class="chart-title">📈 Image Size Evolution</div>
            <canvas id="sizeChart"></canvas>
        </div>

        <div class="chart-container">
            <div class="chart-title">⏱️ Build Time Trends</div>
            <canvas id="timeChart"></canvas>
        </div>

        <div class="chart-container">
            <div class="chart-title">📦 Layer Count Changes</div>
            <canvas id="layerChart"></canvas>
        </div>

        <div class="insights">
            <h2>💡 Insights</h2>
            <div id="insights-content"></div>
        </div>
    </div>

    <script>
        const labels = ` + string(labelsJSON) + `;
        const sizes = ` + string(sizesJSON) + `;
        const times = ` + string(timesJSON) + `;
        const layers = ` + string(layersJSON) + `;

        // Size Chart
        new Chart(document.getElementById('sizeChart'), {
            type: 'line',
            data: {
                labels: labels,
                datasets: [{
                    label: 'Image Size (MB)',
                    data: sizes,
                    borderColor: '#667eea',
                    backgroundColor: 'rgba(102, 126, 234, 0.1)',
                    tension: 0.1,
                    fill: true
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                plugins: {
                    legend: { display: false },
                    tooltip: {
                        callbacks: {
                            label: function(context) {
                                const value = context.parsed.y;
                                const prev = context.dataIndex > 0 ? sizes[context.dataIndex - 1] : value;
                                const diff = value - prev;
                                return value.toFixed(2) + ' MB' + (diff !== 0 ? ' (' + (diff > 0 ? '+' : '') + diff.toFixed(2) + ')' : '');
                            }
                        }
                    }
                }
            }
        });

        // Time Chart
        new Chart(document.getElementById('timeChart'), {
            type: 'bar',
            data: {
                labels: labels,
                datasets: [{
                    label: 'Build Time (seconds)',
                    data: times,
                    backgroundColor: '#764ba2',
                    borderColor: '#764ba2',
                    borderWidth: 1
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                plugins: {
                    legend: { display: false }
                }
            }
        });

        // Layer Chart
        new Chart(document.getElementById('layerChart'), {
            type: 'line',
            data: {
                labels: labels,
                datasets: [{
                    label: 'Layer Count',
                    data: layers,
                    borderColor: '#f59e0b',
                    backgroundColor: 'rgba(245, 158, 11, 0.1)',
                    stepped: true,
                    fill: true
                }]
            },
            options: {
                responsive: true,
                maintainAspectRatio: false,
                plugins: {
                    legend: { display: false }
                },
                scales: {
                    y: {
                        beginAtZero: true,
                        ticks: { stepSize: 1 }
                    }
                }
            }
        });

        // Generate insights
        function generateInsights() {
            const insights = [];
            
            // Find biggest increase
            let maxIncrease = 0, maxIncreaseIdx = 0;
            for (let i = 1; i < sizes.length; i++) {
                const diff = sizes[i] - sizes[i-1];
                if (diff > maxIncrease) {
                    maxIncrease = diff;
                    maxIncreaseIdx = i;
                }
            }
            if (maxIncrease > 0) {
                insights.push({
                    type: 'warning',
                    text: 'Biggest size increase: +' + maxIncrease.toFixed(2) + ' MB at commit ' + labels[maxIncreaseIdx]
                });
            }

            // Find biggest decrease
            let maxDecrease = 0, maxDecreaseIdx = 0;
            for (let i = 1; i < sizes.length; i++) {
                const diff = sizes[i] - sizes[i-1];
                if (diff < maxDecrease) {
                    maxDecrease = diff;
                    maxDecreaseIdx = i;
                }
            }
            if (maxDecrease < 0) {
                insights.push({
                    type: 'success',
                    text: 'Biggest optimization: ' + maxDecrease.toFixed(2) + ' MB at commit ' + labels[maxDecreaseIdx]
                });
            }

            // Average build time
            const avgTime = times.reduce((a, b) => a + b, 0) / times.length;
            insights.push({
                type: 'info',
                text: 'Average build time: ' + avgTime.toFixed(1) + ' seconds'
            });

            const container = document.getElementById('insights-content');
            insights.forEach(insight => {
                const div = document.createElement('div');
                div.className = 'insight-item ' + insight.type;
                div.textContent = insight.text;
                container.appendChild(div);
            });
        }

        generateInsights();
    </script>
</body>
</html>`

	// Create template functions
	funcMap := template.FuncMap{
		"add": func(a, b int) int { return a + b },
		"calcChange": func(sizes []float64) float64 {
			if len(sizes) < 2 {
				return 0
			}
			return sizes[len(sizes)-1] - sizes[0]
		},
	}

	tmpl, err := template.New("report").Funcs(funcMap).Parse(tmplStr)
	if err != nil {
		return err
	}

	return tmpl.Execute(w, chartData)
}

// ============ pkg/analyzer/comparer.go ============
package analyzer

import (
	"context"
	"fmt"
	"time"

	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/jtodic/dockerfile-time-machine/pkg/docker"
)

// ComparerConfig holds configuration for the Comparer
type ComparerConfig struct {
	RepoPath       string
	DockerfilePath string
	Verbose        bool
}

// ComparisonResult holds the results of comparing two branches
type ComparisonResult struct {
	BranchA              BranchInfo `json:"branch_a"`
	BranchB              BranchInfo `json:"branch_b"`
	SizeDiff             float64    `json:"size_diff_mb"`
	SizeDiffPercent      float64    `json:"size_diff_percent"`
	LayersDiff           int        `json:"layers_diff"`
	BuildTimeDiff        float64    `json:"build_time_diff"`
	BuildTimeDiffPercent float64    `json:"build_time_diff_percent"`
}

// BranchInfo holds information about a branch's Docker image
type BranchInfo struct {
	Name      string  `json:"name"`
	Commit    string  `json:"commit"`
	SizeMB    float64 `json:"size_mb"`
	Layers    int     `json:"layers"`
	BuildTime float64 `json:"build_time"`
}

// Comparer compares Docker images between branches
type Comparer struct {
	config  ComparerConfig
	repo    *git.Repository
	builder *docker.Builder
}

// NewComparer creates a new Comparer
func NewComparer(config ComparerConfig) (*Comparer, error) {
	repo, err := git.PlainOpen(config.RepoPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open repository: %w", err)
	}

	builder, err := docker.NewBuilder()
	if err != nil {
		return nil, fmt.Errorf("failed to create builder: %w", err)
	}

	return &Comparer{
		config:  config,
		repo:    repo,
		builder: builder,
	}, nil
}

// Compare compares Docker images between two branches
func (c *Comparer) Compare(ctx context.Context, branchA, branchB string) (*ComparisonResult, error) {
	// Analyze branch A
	infoA, err := c.analyzeBranch(ctx, branchA)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze branch %s: %w", branchA, err)
	}

	// Analyze branch B
	infoB, err := c.analyzeBranch(ctx, branchB)
	if err != nil {
		return nil, fmt.Errorf("failed to analyze branch %s: %w", branchB, err)
	}

	// Calculate differences
	result := &ComparisonResult{
		BranchA:         *infoA,
		BranchB:         *infoB,
		SizeDiff:        infoB.SizeMB - infoA.SizeMB,
		LayersDiff:      infoB.Layers - infoA.Layers,
		BuildTimeDiff:   infoB.BuildTime - infoA.BuildTime,
	}

	if infoA.SizeMB > 0 {
		result.SizeDiffPercent = (result.SizeDiff / infoA.SizeMB) * 100
	}
	if infoA.BuildTime > 0 {
		result.BuildTimeDiffPercent = (result.BuildTimeDiff / infoA.BuildTime) * 100
	}

	return result, nil
}

// analyzeBranch analyzes a single branch
func (c *Comparer) analyzeBranch(ctx context.Context, branchName string) (*BranchInfo, error) {
	// Get the branch reference
	ref, err := c.repo.Reference(plumbing.ReferenceName("refs/heads/"+branchName), true)
	if err != nil {
		// Try as tag
		ref, err = c.repo.Reference(plumbing.ReferenceName("refs/tags/"+branchName), true)
		if err != nil {
			return nil, fmt.Errorf("branch or tag not found: %s", branchName)
		}
	}

	// Get the commit
	commit, err := c.repo.CommitObject(ref.Hash())
	if err != nil {
		return nil, fmt.Errorf("failed to get commit: %w", err)
	}

	// Checkout the branch
	worktree, err := c.repo.Worktree()
	if err != nil {
		return nil, fmt.Errorf("failed to get worktree: %w", err)
	}

	err = worktree.Checkout(&git.CheckoutOptions{
		Hash:  commit.Hash,
		Force: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to checkout: %w", err)
	}

	// Build and analyze
	tm, err := NewTimeMachine(Config{
		RepoPath:       c.config.RepoPath,
		DockerfilePath: c.config.DockerfilePath,
		MaxCommits:     1,
		Branch:         branchName,
		Verbose:        c.config.Verbose,
	})
	if err != nil {
		return nil, err
	}

	// Analyze just the latest commit
	startTime := time.Now()
	result := tm.analyzeCommit(ctx, commit)
	
	if result.Error != "" {
		return nil, fmt.Errorf("build failed: %s", result.Error)
	}

	return &BranchInfo{
		Name:      branchName,
		Commit:    commit.Hash.String()[:8],
		SizeMB:    float64(result.ImageSize) / 1024 / 1024,
		Layers:    result.LayerCount,
		BuildTime: time.Since(startTime).Seconds(),
	}, nil
}
