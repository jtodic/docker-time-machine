# 🐳 Docker Time Machine (DTM)

Track your Docker image evolution through git history locally!
Commits are analyzed following git's parent chain (depth-first), not in chronological order by date.

## Features
- 📊 Track image size changes across commits
- ⚡ Monitor build performance trends
- 🔍 Find exactly which commit introduced bloat
- 📈 Generate interactive HTML reports

⚠️ **Note on Build Times:** Build times are indicative only and depend on Docker's layer cache state.

## Quick Start
```bash
# Install
go build -o dtm main.go

# Analyze current repo
./dtm analyze

# Generate HTML report
./dtm analyze --format chart --output report.html

# Top 5 commits by size increase
./dtm analyze --sort diff --max-commits 5
```

## Installation

### From Source
```bash
go mod download
make build
make install
```

## Commands

- `analyze` - Analyze image evolution

See full documentation with: `dtm --help`
