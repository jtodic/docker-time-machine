package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/jtodic/docker-time-machine/pkg/docker"
	"github.com/olekukonko/tablewriter"
	"github.com/schollz/progressbar/v3"
	"github.com/spf13/cobra"
)

var registryFlags struct {
	tags     string
	last     int
	since    string
	until    string
	format   string
	output   string
	platform string
}

// RegistryResult holds analysis results for a registry image
type RegistryResult struct {
	Tag        string      `json:"tag"`
	Digest     string      `json:"digest"`
	Created    time.Time   `json:"created"`
	Size       int64       `json:"size"`
	SizeMB     float64     `json:"size_mb"`
	LayerCount int         `json:"layer_count"`
	Layers     []LayerInfo `json:"layers,omitempty"`
	SizeDiff   int64       `json:"size_diff,omitempty"`
	Error      string      `json:"error,omitempty"`
}

// LayerInfo represents a single layer
type LayerInfo struct {
	Digest    string  `json:"digest,omitempty"`
	CreatedBy string  `json:"created_by"`
	Size      int64   `json:"size"`
	SizeMB    float64 `json:"size_mb"`
}

// RegistryLayerComparison represents layer sizes across tags
type RegistryLayerComparison struct {
	LayerCommand string             `json:"layer_command"`
	SizeByTag    map[string]float64 `json:"size_by_tag"`
}

// RegistryInsights holds bloat/optimization findings
type RegistryInsights struct {
	BloatTag         string  `json:"bloat_tag,omitempty"`
	BloatSizeDiff    float64 `json:"bloat_size_diff_mb,omitempty"`
	OptimizationTag  string  `json:"optimization_tag,omitempty"`
	OptimizationDiff float64 `json:"optimization_size_diff_mb,omitempty"`
}

var registryCmd = &cobra.Command{
	Use:   "registry <image>",
	Short: "Analyze Docker image evolution from a container registry",
	Long: `Analyze Docker images directly from a container registry without rebuilding.

This command fetches image metadata from registries like Docker Hub, Amazon ECR,
Google GCR, GitHub GHCR, or any OCI-compliant registry.

It fetches only the manifest and config (~10KB per image) without downloading
the full image layers. This is fast and doesn't use disk space.

It tracks how image size and layers have changed across tags/versions, helping you
identify which release introduced bloat without access to the original source code.

Supported registries:
  - Docker Hub (docker.io)
  - Amazon ECR (*.amazonaws.com)
  - Google GCR (gcr.io)
  - GitHub GHCR (ghcr.io)
  - Azure ACR (*.azurecr.io)
  - Any OCI-compliant registry

Authentication uses your existing Docker credentials (~/.docker/config.json).
Run 'docker login <registry>' first if needed.`,
	Example: `  # Analyze last 10 tags
  dtm registry nginx --last 10

  # Analyze specific tags
  dtm registry mycompany/api --tags "v1.0.0,v1.1.0,v1.2.0,latest"

  # Generate HTML report
  dtm registry node --last 15 --format chart

  # Specify platform for multi-arch images
  dtm registry nginx --last 5 --platform linux/amd64`,
	Args: cobra.ExactArgs(1),
	RunE: runRegistry,
}

func init() {
	rootCmd.AddCommand(registryCmd)

	registryCmd.Flags().StringVar(&registryFlags.tags, "tags", "", "Comma-separated list of tags to analyze")
	registryCmd.Flags().IntVar(&registryFlags.last, "last", 10, "Analyze last N tags (by creation date)")
	registryCmd.Flags().StringVar(&registryFlags.since, "since", "", "Analyze tags created since date (YYYY-MM-DD)")
	registryCmd.Flags().StringVar(&registryFlags.until, "until", "", "Analyze tags created until date (YYYY-MM-DD)")
	registryCmd.Flags().StringVarP(&registryFlags.format, "format", "f", "table", "Output format: table, json, csv, chart, markdown")
	registryCmd.Flags().StringVarP(&registryFlags.output, "output", "o", "", "Output file path")
	registryCmd.Flags().StringVar(&registryFlags.platform, "platform", "", "Platform for multi-arch images (e.g., linux/amd64)")
}

func runRegistry(cmd *cobra.Command, args []string) error {
	imageName := args[0]

	ctx := context.Background()

	regClient := docker.NewRegistryClient()

	tags, err := getTagsToAnalyze(ctx, regClient, imageName)
	if err != nil {
		return fmt.Errorf("failed to get tags: %w", err)
	}

	if len(tags) == 0 {
		return fmt.Errorf("no tags found for image %s", imageName)
	}

	fmt.Fprintf(os.Stderr, "🐳 Analyzing %d tags for %s\n", len(tags), imageName)

	bar := progressbar.NewOptions(len(tags),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowCount(),
		progressbar.OptionSetWidth(40),
		progressbar.OptionSetDescription("[cyan]Fetching metadata...[reset]"),
	)

	var results []RegistryResult
	var errorCount int
	for _, tag := range tags {
		bar.Add(1)

		result := analyzeRegistryImageMetadata(ctx, regClient, imageName, tag)

		if result.Error != "" {
			errorCount++
			if verbose {
				fmt.Fprintf(os.Stderr, "\n  ⚠️ %s: %s\n", tag, result.Error)
			}
		}

		results = append(results, result)
	}

	fmt.Fprintf(os.Stderr, "\n")

	// Report error summary
	successCount := len(results) - errorCount
	if errorCount > 0 {
		fmt.Fprintf(os.Stderr, "⚠️  %d/%d tags failed to fetch metadata\n", errorCount, len(results))
		if !verbose {
			fmt.Fprintf(os.Stderr, "   Use -v for details\n")
		}
	}

	if successCount == 0 {
		return fmt.Errorf("all %d tags failed to fetch metadata - check network or authentication", len(results))
	}

	// Sort results by creation date (newest first)
	sort.Slice(results, func(i, j int) bool {
		// Put errors at the end
		if results[i].Error != "" && results[j].Error == "" {
			return false
		}
		if results[i].Error == "" && results[j].Error != "" {
			return true
		}
		if results[i].Error != "" && results[j].Error != "" {
			return results[i].Tag < results[j].Tag
		}
		return results[i].Created.After(results[j].Created)
	})

	// Recalculate size diffs after sorting (newest first)
	// Diff shows change FROM the previous (older) version TO this version
	// So we compare each item to the next item (which is older)
	for i := range results {
		if results[i].Error == "" && i < len(results)-1 {
			// Find next valid result (older version)
			for j := i + 1; j < len(results); j++ {
				if results[j].Error == "" {
					results[i].SizeDiff = results[i].Size - results[j].Size
					break
				}
			}
		}
	}

	return generateRegistryReport(results, imageName)
}

func getTagsToAnalyze(ctx context.Context, regClient *docker.RegistryClient, imageName string) ([]string, error) {
	if registryFlags.tags != "" {
		tags := strings.Split(registryFlags.tags, ",")
		for i := range tags {
			tags[i] = strings.TrimSpace(tags[i])
		}
		return tags, nil
	}

	remoteTags, err := regClient.ListTags(ctx, imageName, registryFlags.last)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags: %w", err)
	}

	if len(remoteTags) == 0 {
		return nil, fmt.Errorf("no tags found for %s", imageName)
	}

	var tags []string
	for _, t := range remoteTags {
		tags = append(tags, t.Name)
	}
	fmt.Fprintf(os.Stderr, "📋 Found %d tags from registry\n", len(tags))
	return tags, nil
}

// analyzeRegistryImageMetadata fetches image metadata from registry without pulling full image
func analyzeRegistryImageMetadata(ctx context.Context, regClient *docker.RegistryClient, imageName, tag string) RegistryResult {
	result := RegistryResult{
		Tag: tag,
	}

	metadata, err := regClient.GetImageMetadata(ctx, imageName, tag, registryFlags.platform)
	if err != nil {
		result.Error = fmt.Sprintf("metadata fetch failed: %v", err)
		return result
	}

	result.Digest = metadata.Digest
	result.Size = metadata.Size
	result.SizeMB = float64(metadata.Size) / 1024 / 1024
	result.Created = metadata.Created
	result.LayerCount = metadata.LayerCount

	// Convert layer metadata to LayerInfo
	for _, layer := range metadata.Layers {
		info := LayerInfo{
			Digest:    layer.Digest,
			CreatedBy: layer.CreatedBy,
			Size:      layer.Size,
			SizeMB:    layer.SizeMB,
		}
		result.Layers = append(result.Layers, info)
	}

	return result
}

func buildRegistryLayerComparison(validResults []RegistryResult) ([]string, []RegistryLayerComparison) {
	layerCommands := make([]string, 0)
	layerCommandSet := make(map[string]bool)

	// Handle empty input
	if len(validResults) == 0 {
		return layerCommands, []RegistryLayerComparison{}
	}

	// Collect unique layer commands from first result
	for _, layer := range validResults[0].Layers {
		if !layerCommandSet[layer.CreatedBy] {
			layerCommands = append(layerCommands, layer.CreatedBy)
			layerCommandSet[layer.CreatedBy] = true
		}
	}

	// Collect from remaining results
	if len(validResults) > 1 {
		for _, result := range validResults[1:] {
			for _, layer := range result.Layers {
				if !layerCommandSet[layer.CreatedBy] {
					layerCommands = append(layerCommands, layer.CreatedBy)
					layerCommandSet[layer.CreatedBy] = true
				}
			}
		}
	}

	comparisons := make([]RegistryLayerComparison, 0, len(layerCommands))
	for _, cmd := range layerCommands {
		comparison := RegistryLayerComparison{
			LayerCommand: cmd,
			SizeByTag:    make(map[string]float64),
		}

		for _, result := range validResults {
			// Sum all layers with this command (handles duplicate commands)
			var totalSize float64
			found := false
			for _, layer := range result.Layers {
				if layer.CreatedBy == cmd {
					totalSize += layer.SizeMB
					found = true
				}
			}
			if found {
				comparison.SizeByTag[result.Tag] = totalSize
			} else {
				comparison.SizeByTag[result.Tag] = -1
			}
		}

		comparisons = append(comparisons, comparison)
	}

	return layerCommands, comparisons
}

// findInsights finds the biggest size increase and decrease
func findInsights(validResults []RegistryResult) RegistryInsights {
	var insights RegistryInsights
	var maxIncrease, maxDecrease int64

	for _, r := range validResults {
		if r.Error == "" && r.SizeDiff > maxIncrease {
			maxIncrease = r.SizeDiff
			insights.BloatTag = r.Tag
			insights.BloatSizeDiff = float64(r.SizeDiff) / 1024 / 1024
		}
		if r.Error == "" && r.SizeDiff < maxDecrease {
			maxDecrease = r.SizeDiff
			insights.OptimizationTag = r.Tag
			insights.OptimizationDiff = float64(-r.SizeDiff) / 1024 / 1024
		}
	}

	return insights
}

func generateRegistryReport(results []RegistryResult, imageName string) error {
	var output *os.File
	var err error

	if registryFlags.output != "" {
		output, err = os.Create(registryFlags.output)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer output.Close()
	} else if registryFlags.format == "chart" {
		timestamp := time.Now().Format("2006-01-02-150405")
		filename := fmt.Sprintf("registry-report-%s.html", timestamp)
		output, err = os.Create(filename)
		if err != nil {
			return fmt.Errorf("failed to create output file: %w", err)
		}
		defer output.Close()
		fmt.Fprintf(os.Stderr, "✅ Report saved to: %s\n", filename)
	} else {
		output = os.Stdout
	}

	switch registryFlags.format {
	case "table":
		return generateRegistryTableReport(output, results, imageName)
	case "json":
		return generateRegistryJSONReport(output, results, imageName)
	case "csv":
		return generateRegistryCSVReport(output, results, imageName)
	case "chart":
		return generateRegistryChartReport(output, results, imageName)
	case "markdown":
		return generateRegistryMarkdownReport(output, results, imageName)
	default:
		return fmt.Errorf("unsupported format: %s", registryFlags.format)
	}
}

func generateRegistryTableReport(w io.Writer, results []RegistryResult, imageName string) error {
	fmt.Fprintf(w, "\n📊 Registry Image Analysis: %s\n", imageName)
	fmt.Fprintln(w, "==========================================")

	table := tablewriter.NewWriter(w)
	table.SetHeader([]string{"Tag", "Date", "Size (MB)", "Diff", "Layers"})
	table.SetBorder(false)
	table.SetAutoWrapText(false)
	table.SetColumnSeparator(" ")
	table.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
	table.SetAlignment(tablewriter.ALIGN_LEFT)

	var validResults []RegistryResult
	for _, r := range results {
		if r.Error == "" {
			validResults = append(validResults, r)
		}
	}

	for _, r := range validResults {
		diffStr := ""
		if r.SizeDiff != 0 {
			sign := "+"
			if r.SizeDiff < 0 {
				sign = ""
			}
			diffStr = fmt.Sprintf("%s%.1f", sign, float64(r.SizeDiff)/1024/1024)
		}

		created := "-"
		if !r.Created.IsZero() {
			created = r.Created.Format("2006-01-02")
		}

		row := []string{
			truncate(r.Tag, 20),
			created,
			fmt.Sprintf("%.2f", r.SizeMB),
			diffStr,
			fmt.Sprintf("%d", r.LayerCount),
		}
		table.Append(row)
	}

	table.Render()

	// Find and display insights
	insights := findInsights(validResults)

	if insights.BloatTag != "" && insights.BloatSizeDiff > 0 {
		fmt.Fprintf(w, "\n⚠️  Biggest size increase: %s\n", insights.BloatTag)
		fmt.Fprintf(w, "   Size increased by: %.2f MB\n", insights.BloatSizeDiff)
	}

	if insights.OptimizationTag != "" && insights.OptimizationDiff > 0 {
		fmt.Fprintf(w, "\n✅ Biggest size reduction: %s\n", insights.OptimizationTag)
		fmt.Fprintf(w, "   Size reduced by: %.2f MB\n", insights.OptimizationDiff)
	}

	// Print layer comparison across tags
	if len(validResults) > 0 {
		layerCommands, _ := buildRegistryLayerComparison(validResults)

		if len(layerCommands) > 0 {
			fmt.Fprintln(w, "\n📦 Layer Size Comparison Across Tags:")
			fmt.Fprintln(w, "--------------------------------------")

			header := []string{"Layer"}
			for _, result := range validResults {
				header = append(header, truncate(result.Tag, 12))
			}

			layerTable := tablewriter.NewWriter(w)
			layerTable.SetHeader(header)
			layerTable.SetBorder(false)
			layerTable.SetAutoWrapText(false)
			layerTable.SetColumnSeparator(" ")
			layerTable.SetHeaderAlignment(tablewriter.ALIGN_LEFT)
			layerTable.SetAlignment(tablewriter.ALIGN_LEFT)

			for _, cmd := range layerCommands {
				row := []string{truncate(cmd, 40)}

				for _, result := range validResults {
					found := false
					for _, layer := range result.Layers {
						if layer.CreatedBy == cmd {
							row = append(row, fmt.Sprintf("%.2f", layer.SizeMB))
							found = true
							break
						}
					}
					if !found {
						row = append(row, "-")
					}
				}

				layerTable.Append(row)
			}

			layerTable.Render()
		}
	}

	return nil
}

// RegistryJSONReport is the structure for JSON output
type RegistryJSONReport struct {
	Image           string                    `json:"image"`
	Summary         RegistrySummary           `json:"summary"`
	Insights        RegistryInsights          `json:"insights"`
	Results         []RegistryResult          `json:"results"`
	LayerComparison []RegistryLayerComparison `json:"layer_comparison"`
	TagOrder        []string                  `json:"tag_order"`
}

// RegistrySummary holds summary statistics
type RegistrySummary struct {
	TagsAnalyzed  int     `json:"tags_analyzed"`
	FirstTagSize  float64 `json:"first_tag_size_mb"`
	FirstTag      string  `json:"first_tag"`
	LastTagSize   float64 `json:"last_tag_size_mb"`
	LastTag       string  `json:"last_tag"`
	TotalChangeMB float64 `json:"total_change_mb"`
}

func generateRegistryJSONReport(w io.Writer, results []RegistryResult, imageName string) error {
	var validResults []RegistryResult
	for _, r := range results {
		if r.Error == "" {
			validResults = append(validResults, r)
		}
	}

	tagOrder := make([]string, 0, len(validResults))
	for _, result := range validResults {
		tagOrder = append(tagOrder, result.Tag)
	}

	_, comparisons := buildRegistryLayerComparison(validResults)
	insights := findInsights(validResults)

	var summary RegistrySummary
	summary.TagsAnalyzed = len(validResults)
	if len(validResults) > 0 {
		first := validResults[0]
		last := validResults[len(validResults)-1]
		summary.FirstTagSize = first.SizeMB
		summary.FirstTag = first.Tag
		summary.LastTagSize = last.SizeMB
		summary.LastTag = last.Tag
		summary.TotalChangeMB = last.SizeMB - first.SizeMB
	}

	report := RegistryJSONReport{
		Image:           imageName,
		Summary:         summary,
		Insights:        insights,
		Results:         results,
		LayerComparison: comparisons,
		TagOrder:        tagOrder,
	}

	encoder := json.NewEncoder(w)
	encoder.SetIndent("", "  ")
	return encoder.Encode(report)
}

func generateRegistryCSVReport(w io.Writer, results []RegistryResult, imageName string) error {
	var validResults []RegistryResult
	for _, r := range results {
		if r.Error == "" {
			validResults = append(validResults, r)
		}
	}

	// Part 1: Summary
	fmt.Fprintf(w, "# Registry Image Analysis: %s\n", imageName)
	fmt.Fprintln(w)

	// Part 2: Main results
	fmt.Fprintln(w, "# Tag Results")
	fmt.Fprintln(w, "tag,date,size_mb,diff_mb,layers,digest")

	for _, r := range results {
		if r.Error != "" {
			continue
		}

		diff := ""
		if r.SizeDiff != 0 {
			sign := "+"
			if r.SizeDiff < 0 {
				sign = ""
			}
			diff = fmt.Sprintf("%s%.2f", sign, float64(r.SizeDiff)/1024/1024)
		}

		created := ""
		if !r.Created.IsZero() {
			created = r.Created.Format("2006-01-02")
		}

		fmt.Fprintf(w, "%s,%s,%.2f,%s,%d,%s\n",
			r.Tag, created, r.SizeMB, diff, r.LayerCount, r.Digest)
	}

	// Part 3: Insights
	insights := findInsights(validResults)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# Insights")
	fmt.Fprintln(w, "metric,tag,size_diff_mb")
	if insights.BloatTag != "" && insights.BloatSizeDiff > 0 {
		fmt.Fprintf(w, "biggest_increase,%s,%.2f\n", insights.BloatTag, insights.BloatSizeDiff)
	}
	if insights.OptimizationTag != "" && insights.OptimizationDiff > 0 {
		fmt.Fprintf(w, "biggest_reduction,%s,%.2f\n", insights.OptimizationTag, insights.OptimizationDiff)
	}

	// Part 4: Layer comparison
	fmt.Fprintln(w)
	fmt.Fprintln(w, "# Layer Size Comparison (MB)")

	header := []string{"layer_command"}
	for _, result := range validResults {
		header = append(header, result.Tag)
	}
	fmt.Fprintln(w, strings.Join(header, ","))

	layerCommands, comparisons := buildRegistryLayerComparison(validResults)
	for i, cmd := range layerCommands {
		row := []string{fmt.Sprintf("\"%s\"", strings.ReplaceAll(cmd, "\"", "\"\""))}

		for _, result := range validResults {
			size := comparisons[i].SizeByTag[result.Tag]
			if size < 0 {
				row = append(row, "-")
			} else {
				row = append(row, fmt.Sprintf("%.2f", size))
			}
		}

		fmt.Fprintln(w, strings.Join(row, ","))
	}

	return nil
}

func generateRegistryMarkdownReport(w io.Writer, results []RegistryResult, imageName string) error {
	fmt.Fprintf(w, "# Registry Image Analysis: %s\n\n", imageName)

	var validResults []RegistryResult
	for _, r := range results {
		if r.Error == "" {
			validResults = append(validResults, r)
		}
	}

	// Summary section
	fmt.Fprintln(w, "## Summary")
	fmt.Fprintf(w, "- **Tags analyzed:** %d\n", len(validResults))

	if len(validResults) > 0 {
		first := validResults[0]
		last := validResults[len(validResults)-1]

		fmt.Fprintf(w, "- **First tag:** %s (%.2f MB)\n", first.Tag, first.SizeMB)
		fmt.Fprintf(w, "- **Last tag:** %s (%.2f MB)\n", last.Tag, last.SizeMB)
		fmt.Fprintf(w, "- **Total change:** %+.2f MB\n", last.SizeMB-first.SizeMB)
	}

	// Insights section
	insights := findInsights(validResults)
	if insights.BloatTag != "" || insights.OptimizationTag != "" {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Insights")
		if insights.BloatTag != "" && insights.BloatSizeDiff > 0 {
			fmt.Fprintf(w, "- ⚠️ **Biggest size increase:** %s (+%.2f MB)\n", insights.BloatTag, insights.BloatSizeDiff)
		}
		if insights.OptimizationTag != "" && insights.OptimizationDiff > 0 {
			fmt.Fprintf(w, "- ✅ **Biggest size reduction:** %s (-%.2f MB)\n", insights.OptimizationTag, insights.OptimizationDiff)
		}
	}

	// Details table
	fmt.Fprintln(w)
	fmt.Fprintln(w, "## Details")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "| Tag | Date | Size (MB) | Diff | Layers |")
	fmt.Fprintln(w, "|-----|------|-----------|------|--------|")

	for _, r := range validResults {
		diffStr := ""
		if r.SizeDiff != 0 {
			sign := "+"
			if r.SizeDiff < 0 {
				sign = ""
			}
			diffStr = fmt.Sprintf("%s%.1f", sign, float64(r.SizeDiff)/1024/1024)
		}

		created := "-"
		if !r.Created.IsZero() {
			created = r.Created.Format("2006-01-02")
		}

		fmt.Fprintf(w, "| %s | %s | %.2f | %s | %d |\n",
			r.Tag, created, r.SizeMB, diffStr, r.LayerCount)
	}

	// Layer comparison table
	layerCommands, comparisons := buildRegistryLayerComparison(validResults)
	if len(layerCommands) > 0 {
		fmt.Fprintln(w)
		fmt.Fprintln(w, "## Layer Size Comparison Across Tags")
		fmt.Fprintln(w)

		header := "| Layer |"
		separator := "|-------|"
		for _, result := range validResults {
			header += fmt.Sprintf(" %s |", truncate(result.Tag, 12))
			separator += "----------|"
		}
		fmt.Fprintln(w, header)
		fmt.Fprintln(w, separator)

		for i, cmd := range layerCommands {
			row := fmt.Sprintf("| `%s` |", truncate(cmd, 40))

			for _, result := range validResults {
				size := comparisons[i].SizeByTag[result.Tag]
				if size < 0 {
					row += " - |"
				} else {
					row += fmt.Sprintf(" %.2f |", size)
				}
			}

			fmt.Fprintln(w, row)
		}
	}

	return nil
}

func generateRegistryChartReport(w io.Writer, results []RegistryResult, imageName string) error {
	var validResults []RegistryResult
	for _, r := range results {
		if r.Error == "" {
			validResults = append(validResults, r)
		}
	}

	var labels []string
	var sizeData []float64

	for _, r := range validResults {
		labels = append(labels, r.Tag)
		sizeData = append(sizeData, r.SizeMB)
	}

	layerCommands, comparisons := buildRegistryLayerComparison(validResults)
	insights := findInsights(validResults)

	type LayerDataset struct {
		Label           string    `json:"label"`
		Data            []float64 `json:"data"`
		BackgroundColor string    `json:"backgroundColor"`
	}

	colors := []string{
		"rgba(75, 192, 192, 0.8)",
		"rgba(255, 99, 132, 0.8)",
		"rgba(255, 206, 86, 0.8)",
		"rgba(54, 162, 235, 0.8)",
		"rgba(153, 102, 255, 0.8)",
		"rgba(255, 159, 64, 0.8)",
		"rgba(199, 199, 199, 0.8)",
		"rgba(83, 102, 255, 0.8)",
		"rgba(255, 99, 255, 0.8)",
		"rgba(99, 255, 132, 0.8)",
	}

	var stackedDatasets []LayerDataset
	for i, cmd := range layerCommands {
		dataset := LayerDataset{
			Label:           truncate(cmd, 50),
			Data:            make([]float64, len(validResults)),
			BackgroundColor: colors[i%len(colors)],
		}

		for j, result := range validResults {
			size := comparisons[i].SizeByTag[result.Tag]
			if size < 0 {
				dataset.Data[j] = 0
			} else {
				dataset.Data[j] = size
			}
		}

		stackedDatasets = append(stackedDatasets, dataset)
	}

	stackedDatasetsJSON, _ := json.Marshal(stackedDatasets)

	type LayerTableRow struct {
		Command string             `json:"command"`
		Sizes   map[string]float64 `json:"sizes"`
	}

	var layerTableData []LayerTableRow
	for i, cmd := range layerCommands {
		row := LayerTableRow{
			Command: cmd,
			Sizes:   comparisons[i].SizeByTag,
		}
		layerTableData = append(layerTableData, row)
	}
	layerTableJSON, _ := json.Marshal(layerTableData)

	labelsJSON, _ := json.Marshal(labels)
	sizeJSON, _ := json.Marshal(sizeData)

	// Build insights HTML
	insightsHTML := ""
	if insights.BloatTag != "" && insights.BloatSizeDiff > 0 {
		insightsHTML += fmt.Sprintf(`<div class="insight warning">⚠️ <strong>Biggest size increase:</strong> %s (+%.2f MB)</div>`, insights.BloatTag, insights.BloatSizeDiff)
	}
	if insights.OptimizationTag != "" && insights.OptimizationDiff > 0 {
		insightsHTML += fmt.Sprintf(`<div class="insight success">✅ <strong>Biggest size reduction:</strong> %s (-%.2f MB)</div>`, insights.OptimizationTag, insights.OptimizationDiff)
	}

	// Summary stats
	summaryHTML := ""
	if len(validResults) > 0 {
		first := validResults[0]
		last := validResults[len(validResults)-1]
		change := last.SizeMB - first.SizeMB
		changeSign := "+"
		if change < 0 {
			changeSign = ""
		}
		summaryHTML = fmt.Sprintf(`
        <div class="summary">
            <div class="stat"><span class="label">Tags analyzed:</span> <span class="value">%d</span></div>
            <div class="stat"><span class="label">First tag:</span> <span class="value">%s (%.2f MB)</span></div>
            <div class="stat"><span class="label">Last tag:</span> <span class="value">%s (%.2f MB)</span></div>
            <div class="stat"><span class="label">Total change:</span> <span class="value">%s%.2f MB</span></div>
        </div>`, len(validResults), first.Tag, first.SizeMB, last.Tag, last.SizeMB, changeSign, change)
	}

	html := fmt.Sprintf(`<!DOCTYPE html>
<html>
<head>
    <title>Registry Analysis: %s</title>
    <script src="https://cdn.jsdelivr.net/npm/chart.js"></script>
    <style>
        body {
            font-family: -apple-system, BlinkMacSystemFont, 'Segoe UI', Roboto, Oxygen, Ubuntu, sans-serif;
            margin: 0;
            padding: 20px;
            background: #f5f5f5;
            color: #333;
        }
        h1 { color: #333; margin-bottom: 10px; }
        h2 { color: #555; margin-top: 0; font-size: 1.2em; }
        .summary {
            background: white;
            border-radius: 8px;
            padding: 15px 20px;
            margin: 10px 0 20px 0;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
            display: flex;
            flex-wrap: wrap;
            gap: 20px;
        }
        .stat { display: flex; gap: 8px; }
        .stat .label { color: #666; }
        .stat .value { font-weight: 600; }
        .insight {
            padding: 10px 15px;
            border-radius: 6px;
            margin: 5px 0;
        }
        .insight.warning { background: #fff3cd; border-left: 4px solid #ffc107; }
        .insight.success { background: #d4edda; border-left: 4px solid #28a745; }
        .chart-container {
            background: white;
            border-radius: 8px;
            padding: 20px;
            margin: 20px 0;
            box-shadow: 0 2px 4px rgba(0,0,0,0.1);
        }
        canvas { max-height: 400px; }
        .note {
            font-size: 0.85em;
            color: #666;
            font-style: italic;
            margin-top: 10px;
        }
        .layer-table-container { 
            overflow-x: auto;
            max-height: 600px;
            overflow-y: auto;
        }
        .layer-table {
            border-collapse: collapse;
            margin-top: 15px;
            font-size: 0.85em;
        }
        .layer-table th, .layer-table td {
            padding: 8px 10px;
            text-align: left;
            border-bottom: 1px solid #eee;
            white-space: nowrap;
        }
        .layer-table th {
            background: #f8f9fa;
            font-weight: 600;
            position: sticky;
            top: 0;
            z-index: 1;
        }
        .layer-table th:first-child {
            position: sticky;
            left: 0;
            z-index: 3;
            background: #f8f9fa;
            min-width: 300px;
        }
        .layer-table td:first-child {
            position: sticky;
            left: 0;
            z-index: 1;
            background: white;
            min-width: 300px;
            max-width: 400px;
            font-family: 'Monaco', 'Menlo', monospace;
            font-size: 0.8em;
            overflow: hidden;
            text-overflow: ellipsis;
            cursor: help;
        }
        .layer-table td:first-child:hover {
            white-space: normal;
            word-break: break-all;
            overflow: visible;
            z-index: 10;
            background: #ffffcc;
            box-shadow: 2px 2px 5px rgba(0,0,0,0.2);
        }
        .layer-table th:not(:first-child),
        .layer-table td:not(:first-child) {
            min-width: 70px;
            text-align: right;
        }
        .layer-table tr:hover td { background: #f8f9fa; }
        .layer-table tr:hover td:first-child { background: #f0f0f0; }
        .layer-table tr:hover td:first-child:hover { background: #ffffcc; }
        .size-cell { text-align: right; font-family: 'Monaco', 'Menlo', monospace; }
        .size-cell.missing { color: #999; }
    </style>
</head>
<body>
    <h1>🐳 Registry Image Analysis: %s</h1>
    %s
    %s
    
    <div class="chart-container">
        <h2>📈 Image Size Over Tags</h2>
        <canvas id="sizeChart"></canvas>
    </div>
    
    <div class="chart-container">
        <h2>📊 Image Size by Layer</h2>
        <canvas id="stackedLayerChart"></canvas>
        <p class="note">Each color represents a different layer. Hover over bars to see layer details.</p>
    </div>

    <div class="chart-container">
        <h2>📦 Layer Size Comparison Across Tags</h2>
        <p class="note">Scroll horizontally to see all tags. Hover over layer commands to see full text.</p>
        <div class="layer-table-container">
            <table class="layer-table" id="layerComparisonTable">
                <thead>
                    <tr id="layerTableHeader">
                        <th>Layer Command</th>
                    </tr>
                </thead>
                <tbody id="layerTableBody">
                </tbody>
            </table>
        </div>
    </div>

    <script>
        const labels = %s;
        const sizeData = %s;
        const stackedDatasets = %s;
        const layerTableData = %s;
        
        new Chart(document.getElementById('sizeChart'), {
            type: 'line',
            data: {
                labels: labels,
                datasets: [{
                    label: 'Image Size (MB)',
                    data: sizeData,
                    borderColor: 'rgb(75, 192, 192)',
                    backgroundColor: 'rgba(75, 192, 192, 0.2)',
                    tension: 0.1,
                    fill: true,
                    pointRadius: 4,
                    pointHoverRadius: 6
                }]
            },
            options: {
                responsive: true,
                plugins: { legend: { display: false } },
                scales: {
                    y: { beginAtZero: true, title: { display: true, text: 'Size (MB)' } },
                    x: { title: { display: true, text: 'Tag' } }
                }
            }
        });

        new Chart(document.getElementById('stackedLayerChart'), {
            type: 'bar',
            data: {
                labels: labels,
                datasets: stackedDatasets
            },
            options: {
                responsive: true,
                plugins: {
                    legend: {
                        display: true,
                        position: 'bottom',
                        labels: { boxWidth: 12, font: { size: 10 } }
                    },
                    tooltip: {
                        callbacks: {
                            label: function(context) {
                                return context.dataset.label + ': ' + context.raw.toFixed(2) + ' MB';
                            }
                        }
                    }
                },
                scales: {
                    x: { stacked: true, title: { display: true, text: 'Tag' } },
                    y: { stacked: true, beginAtZero: true, title: { display: true, text: 'Size (MB)' } }
                }
            }
        });

        const headerRow = document.getElementById('layerTableHeader');
        const tbody = document.getElementById('layerTableBody');

        labels.forEach(tag => {
            const th = document.createElement('th');
            th.textContent = tag;
            th.style.textAlign = 'right';
            headerRow.appendChild(th);
        });

        layerTableData.forEach(layer => {
            const row = document.createElement('tr');

            const cmdCell = document.createElement('td');
            cmdCell.textContent = layer.command;
            cmdCell.title = layer.command;
            row.appendChild(cmdCell);

            labels.forEach(tag => {
                const cell = document.createElement('td');
                cell.className = 'size-cell';
                const size = layer.sizes[tag];
                if (size === undefined || size < 0) {
                    cell.textContent = '-';
                    cell.classList.add('missing');
                } else {
                    cell.textContent = size.toFixed(2);
                }
                row.appendChild(cell);
            });

            tbody.appendChild(row);
        });
    </script>
</body>
</html>`,
		imageName, imageName, summaryHTML, insightsHTML,
		string(labelsJSON), string(sizeJSON), string(stackedDatasetsJSON), string(layerTableJSON))

	_, err := w.Write([]byte(html))
	return err
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-3] + "..."
}
