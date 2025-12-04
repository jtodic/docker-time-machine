# 🐳 Docker Time Machine (DTM)

Track your Docker image evolution through git history. Find bloat, optimize builds, and understand how your images changed over time.

## ⚡ Fast Analysis with Layer Caching

DTM leverages Docker's intelligent layer caching to analyze your image history **blazingly fast**. Unchanged layers are reused across builds, so you only pay the build cost for layers that actually changed. Analyzing 20+ commits typically takes just minutes, not hours.

## What It Does

DTM walks through your git history, builds the Docker image at each commit, and records key metrics:

- **Image size** — Total size in MB
- **Layer count** — Number of layers in the image  
- **Layer sizes** — Individual layer sizes for detailed analysis
- **Build time** — How long each build took (indicative only)

It then generates reports showing trends, comparing layers across commits, and highlighting significant changes.

## Features

- 📊 **Track image size changes** across commits
- 📦 **Layer-by-layer comparison** — see which layers changed between commits
- 📈 **Stacked layer visualization** — see how each layer contributes to total image size over time
- ⚡ **Fast builds** — leverages Docker layer caching for rapid analysis
- 🔍 **Find bloat** — automatically identifies the commit with the biggest size increase
- ✅ **Find optimizations** — identifies commits that reduced image size
- 📈 **Multiple output formats** — table, JSON, CSV, Markdown, interactive HTML charts

## Installation

### From Source

```bash
git clone https://github.com/jtodic/docker-time-machine.git
cd docker-time-machine
go mod download
make build
make install
```

> **Windows users:** The Makefile uses Unix commands. Use [WSL](https://docs.microsoft.com/en-us/windows/wsl/install) or [Git Bash](https://gitforwindows.org/) to build, or run Go commands directly:
> ```bash
> go build -o dtm.exe main.go
> ```

### Prerequisites

- Go 1.24+
- Docker (running daemon)
- Git repository with a Dockerfile

## Quick Start

```bash
# Navigate to your project with a Dockerfile
cd your-project

# Analyze last 20 commits (default)
dtm analyze

# Generate interactive HTML report
dtm analyze --format chart
```

## Usage Examples

### Basic Analysis

```bash
# Analyze current repository
dtm analyze

# Analyze specific number of commits
dtm analyze --max-commits 10

# Analyze all commits (no limit)
dtm analyze --max-commits 0
```

### Output Formats

```bash
# Table output (default) - prints to terminal
dtm analyze

# Interactive HTML charts
dtm analyze --format chart

# JSON for programmatic processing
dtm analyze --format json --output results.json

# CSV for spreadsheets
dtm analyze --format csv --output results.csv

# Markdown for documentation
dtm analyze --format markdown --output DOCKER_HISTORY.md
```

### Filtering Commits

```bash
# Analyze specific branch
dtm analyze --branch develop

# Analyze commits within date range
dtm analyze --since 2024-01-01 --until 2024-06-30

# Analyze a different repository
dtm analyze --repo /path/to/other/project

# Custom Dockerfile location
dtm analyze --dockerfile build/Dockerfile.prod
```

### Handling Build Failures

```bash
# Skip commits that fail to build and continue
dtm analyze --skip-failed

# Verbose output to see what's happening
dtm analyze --verbose
```

### Combined Examples

```bash
# Full analysis of develop branch with HTML report
dtm analyze --branch develop --format chart --output develop-report.html

# Last 50 commits of 2024, skip failures, JSON output
dtm analyze --max-commits 50 --since 2024-01-01 --skip-failed --format json -o analysis.json

# Quick check of last 5 commits with verbose output
dtm analyze -n 5 -v
```

## Output Examples

### HTML Chart Output

Generates an interactive report with:
- 📈 **Image size trend over time** — line chart showing total image size evolution
- 📊 **Image size by layer** — stacked bar chart showing how each layer contributes to total size across commits
- ⏱️ **Build time analysis** — bar chart with note that times are indicative only
- 📦 **Layer size comparison table** — detailed table comparing layer sizes across all commits

![Example Image1](images/example1.png)

![Example Image2](images/example2.png)

### Table Output

```
📊 Docker Image Evolution Report
=================================
  COMMIT     DATE         AUTHOR         SIZE (MB)   DIFF   LAYERS   TIME (S)   MESSAGE
-----------+------------+--------------+-----------+------+--------+----------+------------------------------------
  35287b4b   2025-02-14   Natanael ...   6.84               3        2.8        also remove github issue template
  66dcff23   2025-02-14   Natanael ...   6.84               3        2.7        Revert "add loongarch64"
  b62aa761   2024-05-10   fossdd         6.84        +0.0   3        2.6        github: add issue templates

⚠️  Biggest size increase: b62aa761
   Size increased by: 0.00 MB
   Message: github: add issue templates

📦 Layer Size Comparison Across Commits:
-----------------------------------------
  LAYER                                      35287B4B   66DCFF23   B62AA761
-------------------------------------------+----------+----------+-----------
  COPY file:e53e22235bc8d5dab61245a702f...   0.00       0.00       -
  apk add --no-cache lua5.3 lua-filesys...   1.55       1.55       1.55
  ADD file:b308dfeecaa300a430b4e65e312a...   5.29       5.29       5.29
  COPY file:67850e59ee891d256d4de67a5f0...   -          -          0.00

```

### JSON Output

```json
{
  "results": [
    {
      "commit_hash": "35287b4b8b1be91d683c2f9fa54cf509d9115483",
      "commit_message": "also remove github issue template",
      "author": "Natanael T04:16:21+01:00",
      "image_size": 7176915,
      "build_time_seconds": 3.2009856,
      "layer_count": 3,
      "layers": [
        {
          "id": "sha256:fe57c5ac8025eb268baa5dd02617027564bd9c33bc2c1e30331ba63f32026305",
          "created_by": "COPY file:e53e22235bc8d5dab61245a702fa2cdd6971233f1cc227a20bd074ab22e900ad in...",
          "size": 3896,
          "size_mb": 0.00371551513671875
        },
        {
          "id": "sha256:757ca765a193b01fdb5bb353b8dcf056dade6dfd2630502c9fd914b1623afe8b",
          "created_by": "apk add --no-cache lua5.3 lua-filesystem lua-lyaml lua-http",
          "size": 1628314,
          "size_mb": 1.5528812408447266
        },
        {
          "id": "sha256:86c72bcabfcfb4f4d4bcfea556cac1efde6249771ae9767df79da10accbdasdas",
          "created_by": "ADD file:b308dfeecaa300a430b4e65e312a48eb5f191df7754e93ff4e7b2d04016b3ca7 in /",
          "size": 5544705,
          "size_mb": 5.287842750549316
        }
      ]
    },

```

## Command Reference

```
dtm analyze [flags]

Flags:
  -r, --repo string        Path to git repository (default ".")
  -d, --dockerfile string  Path to Dockerfile relative to repo root (default "Dockerfile")
  -f, --format string      Output format: table, json, csv, chart, markdown (default "table")
  -n, --max-commits int    Maximum commits to analyze, 0 = all (default 20)
  -b, --branch string      Git branch to analyze (default: current branch)
  -o, --output string      Output file path (default: stdout, or timestamped file for chart)
      --since string       Analyze commits since date (YYYY-MM-DD)
      --until string       Analyze commits until date (YYYY-MM-DD)
      --skip-failed        Skip commits that fail to build
  -v, --verbose            Verbose output
  -h, --help               Help for analyze
```

## How It Works

1. **Reads git history** — Gets commits from the specified branch
2. **Checks out each commit** — Temporarily switches to each commit
3. **Builds the Docker image** — Using `docker build` (layer cache makes this fast!)
4. **Records metrics** — Image size, layer info, build time
5. **Cleans up** — Removes temporary images
6. **Restores branch** — Returns to original branch/commit
7. **Generates report** — In your chosen format

## Notes & Limitations

- **Build times are indicative only** — They depend on Docker's layer cache state and system load
- **Commits are analyzed by git parent chain** — Not chronological order by date
- **Temporary images are cleaned up** — Named `dtm-<commit-hash>`
- **Layer matching uses command string** — Layers are compared by their Dockerfile instruction

## Use Cases

### Finding Image Bloat

```bash
# Analyze history and look for the "Biggest size increase" in output
dtm analyze --max-commits 50

# Or export to JSON and process programmatically
dtm analyze --format json | jq '.results | max_by(.size_diff)'
```

### Monitoring Image Size Over Time

```bash
# Generate weekly reports
dtm analyze --since $(date -d '7 days ago' +%Y-%m-%d) --format chart -o weekly-report.html
```

### Comparing Branches

```bash
# Analyze main branch
dtm analyze --branch main --format json -o main.json

# Analyze feature branch  
dtm analyze --branch feature/new-build --format json -o feature.json
```

## License

MIT License - see [LICENSE](LICENSE) file.

## Contributing

Contributions welcome! Please open an issue or submit a pull request.
