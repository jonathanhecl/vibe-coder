// Command vibe-metrics summarizes harness metrics files produced by vibe.
// Each argument may be a *.jsonl metrics file or a directory (all *.jsonl
// inside are expanded). One row is printed per file plus a TOTAL row.
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/jonathanhecl/vibe-coder/internal/metrics"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: vibe-metrics <metrics.jsonl|dir> [...]")
		os.Exit(2)
	}
	files, err := metrics.ExpandInputs(os.Args[1:])
	if err != nil {
		fmt.Fprintln(os.Stderr, "vibe-metrics:", err)
		os.Exit(1)
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FILE\tREQ\tERR\tPROMPT\tPROMPT/REQ\tCOMPL\tCOMPL/REQ\tSYS_AVG\tTRANSCRIPT_AVG\tMASKED\tCOMPACT\tWALL_P50\tWALL_P95")
	total := metrics.Summary{}
	for _, path := range files {
		entries, err := metrics.ReadFile(path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "vibe-metrics: %s: %v\n", path, err)
			continue
		}
		s := metrics.Summarize(entries)
		printRow(w, filepath.Base(path), s)
		for _, e := range entries {
			total.Add(e)
		}
	}
	// Recompute percentiles for the total row from every file's entries.
	walls := collectWalls(files)
	if len(walls) > 0 {
		total.WallP50MS = metrics.WallPercentile(walls, 50)
		total.WallP95MS = metrics.WallPercentile(walls, 95)
	}
	printRow(w, "TOTAL", total)
	_ = w.Flush()
}

func collectWalls(files []string) []int64 {
	var out []int64
	for _, path := range files {
		entries, err := metrics.ReadFile(path)
		if err != nil {
			continue
		}
		for _, e := range entries {
			out = append(out, e.WallMS)
		}
	}
	return out
}

func printRow(w *tabwriter.Writer, name string, s metrics.Summary) {
	var promptAvg, complAvg, sysAvg, transcriptAvg int64
	if s.Requests > 0 {
		promptAvg = int64(s.PromptTokens) / int64(s.Requests)
		complAvg = int64(s.CompletionTokens) / int64(s.Requests)
		sysAvg = int64(s.SystemTokensEst) / int64(s.Requests)
		transcriptAvg = int64(s.TranscriptTokensEst) / int64(s.Requests)
	}
	fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n",
		name, s.Requests, s.Errors, s.PromptTokens, promptAvg,
		s.CompletionTokens, complAvg, sysAvg, transcriptAvg,
		s.ToolResultsMasked, s.Compactions, s.WallP50MS, s.WallP95MS)
}
