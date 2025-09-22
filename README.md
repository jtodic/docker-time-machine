# 🐳 Dockerfile Time Machine (DTM)

[![Go Version](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)](https://go.dev)
[![License](https://img.shields.io/badge/license-MIT-blue)](LICENSE)
[![Docker](https://img.shields.io/badge/Docker-required-2496ED?logo=docker)](https://docker.com)

Track your Docker image evolution through git history! DTM analyzes how your Docker images have changed over time, helping you identify when and why they became bloated.

## ✨ Features

- 📊 **Size Evolution Tracking** - Monitor how image size changes across commits
- ⚡ **Build Time Analysis** - Track build performance trends over time
- 🔍 **Bloat Detection** - Pinpoint exactly which commit introduced size increases
- 📈 **Interactive Charts** - Generate beautiful HTML reports with visual trends
- 🎯 **Binary Search** - Use bisect to find when metrics exceeded thresholds
- 🔄 **Branch Comparison** - Compare image sizes between different branches
- 🚀 **CI/CD Ready** - Integrate into pipelines to prevent regressions

## 🚀 Quick Start

### Installation

#### From Source
```bash
git clone https://github.com/jtodic/dockerfile-time-machine
cd dockerfile-time-machine
make install
```

#### Using Go
```bash
go install github.com/jtodic/dockerfile-time-machine@latest
```

#### Using Docker
```bash
docker pull ghcr.io/jtodic/dtm:latest
```

### Basic Usage

```bash
# Analyze current repository
dtm analyze

# Generate interactive HTML report
dtm analyze --format chart --output report.html

# Find when image exceeded 500MB
dtm bisect --size-threshold 500

# Compare branches
dtm compare --branch-a main --branch-b feature/optimization
```

## 📖 Documentation

### Commands

#### `analyze` - Analyze image evolution

```bash
dtm analyze [flags]

Flags:
  -r, --repo string         Path to git repository (default ".")
  -d, --dockerfile string   Path to Dockerfile (default "Dockerfile")
  -f, --format string       Output format: table, json, csv, chart, markdown (default "table")
  -n, --max-commits int     Maximum commits to analyze, 0 for all (default 20)
  -b, --branch string       Git branch to analyze (default: current branch)
  -o, --output string       Output file path (default: stdout)
      --since string        Analyze commits since date (YYYY-MM-DD)
      --until string        Analyze commits until date (YYYY-MM-DD)
      --skip-failed         Skip commits that fail to build
  -v, --verbose            Verbose output
```

#### `bisect` - Find regression commits

```bash
dtm bisect [flags]

Flags:
  -r, --repo string            Path to git repository (default ".")
  -d, --dockerfile string      Path to Dockerfile (default "Dockerfile")
      --size-threshold float   Size threshold in MB
      --time-threshold float   Build time threshold in seconds
      --good string           Known good commit (optional)
      --bad string            Known bad commit (optional)
```

#### `compare` - Compare branches

```bash
dtm compare [flags]

Flags:
  -r, --repo string         Path to git repository (default ".")
  -d, --dockerfile string   Path to Dockerfile (default "Dockerfile")
  -a, --branch-a string    First branch to compare (default "main")
  -b, --branch-b string    Second branch to compare (required)
  -f, --format string      Output format: table, json (default "table")
```

## 📊 Example Output

### Table Format
```
📊 Docker Image Evolution Report
=================================
Commit   Date        Author      Size (MB)  Diff   Layers  Time (s)  Message
────────────────────────────────────────────────────────────────────────────
a3b4c5d  2024-01-01  John Doe    245.32            12      45.2      Initial Dockerfile
b4c5d6e  2024-01-05  Jane Smith  247.89     +2.6   13      46.1      Add dev dependencies
d6e7f8g  2024-01-15  Bob Wilson  389.45     +141.6 18      72.3      Add ML libraries
i1j2k3l  2024-02-10  Jane Smith  312.33     -77.1  15      52.3      Optimize layers

⚠️  Biggest size increase: d6e7f8g
   Size increased by: 141.56 MB
   Message: Add ML libraries
```

### Interactive HTML Chart
The HTML report includes interactive charts showing:
- Size evolution over time
- Build time trends
- Layer count changes
- Automated insights and recommendations

## 🔧 Configuration

Create `.dtm.yml` in your repository:

```yaml
# .dtm.yml
dockerfile: Dockerfile
max_commits: 50
cache: true
thresholds:
  max_size: 1000  # MB
  max_time: 300   # seconds
ignore:
  - "*.tmp"
  - "test/*"
```

## 🤝 CI/CD Integration

### GitHub Actions

```yaml
name: Docker Image Analysis

on: [push, pull_request]

jobs:
  analyze:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
        with:
          fetch-depth: 0  # Get full history
      
      - name: Install DTM
        run: |
          wget https://github.com/jtodic/dtm/releases/latest/download/dtm-linux-amd64
          chmod +x dtm-linux-amd64
          sudo mv dtm-linux-amd64 /usr/local/bin/dtm
      
      - name: Analyze Docker image
        run: |
          dtm analyze --format json > analysis.json
          size=$(jq -r '.[-1].image_size' analysis.json)
          echo "Final image size: $((size / 1048576)) MB"
      
      - name: Check for regression
        run: |
          dtm bisect --size-threshold 500 || echo "No regression found"
      
      - name: Upload report
        uses: actions/upload-artifact@v3
        with:
          name: dtm-report
          path: analysis.json
```

### GitLab CI

```yaml
docker-analysis:
  stage: test
  image: docker:latest
  services:
    - docker:dind
  before_script:
    - apk add --no-cache git
    - wget -O /usr/local/bin/dtm https://github.com/jtodic/dtm/releases/latest/download/dtm-linux-amd64
    - chmod +x /usr/local/bin/dtm
  script:
    - dtm analyze --format chart --output report.html
  artifacts:
    paths:
      - report.html
    expire_in: 1 week
```

## 🐳 Running with Docker

```bash
# Using Docker directly
docker run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v $(pwd):/workspace \
  ghcr.io/jtodic/dtm:latest \
  analyze --format chart --output /workspace/report.html

# Using docker-compose
docker-compose run --rm dtm analyze
```

## 🧪 Development

### Prerequisites
- Go 1.21+
- Docker
- Git

### Setup
```bash
# Clone the repository
git clone https://github.com/jtodic/dockerfile-time-machine
cd dockerfile-time-machine

# Install dependencies
go mod download

# Run tests
make test

# Build binary
make build

# Run locally
./bin/dtm analyze --repo /path/to/your/project
```

## 🤔 How It Works

DTM works by:
1. **Traversing git history** - Finding commits that modified your Dockerfile
2. **Building images** - Creating Docker images at each commit point
3. **Collecting metrics** - Recording size, build time, and layer information
4. **Analyzing trends** - Identifying patterns and regressions
5. **Generating reports** - Creating actionable insights and visualizations

## 📝 License

MIT License - see [LICENSE](LICENSE) file for details

## 🙏 Acknowledgments

- Inspired by the need to understand Docker image bloat
- Built with [Docker SDK for Go](https://github.com/docker/docker)
- Git operations via [go-git](https://github.com/go-git/go-git)
- Charts powered by [Chart.js](https://chartjs.org)

## 🤝 Contributing

Contributions are welcome! Please feel free to submit a Pull Request.

1. Fork the repository
2. Create your feature branch (`git checkout -b feature/AmazingFeature`)
3. Commit your changes (`git commit -m 'Add some AmazingFeature'`)
4. Push to the branch (`git push origin feature/AmazingFeature`)
5. Open a Pull Request

## 📞 Support

- 📧 Email: your.email@example.com
- 💬 Slack: [Join our channel](https://slack.example.com)
- 🐛 Issues: [GitHub Issues](https://github.com/jtodic/dockerfile-time-machine/issues)

---

Made with ❤️ for the DevOps community
