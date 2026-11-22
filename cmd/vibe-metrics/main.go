// Command vibe-metrics summarizes harness metrics files produced by vibe.
// Each argument may be a *.jsonl metrics file or a directory (all *.jsonl
// inside are expanded). One row is printed per file plus a TOTAL row.
//
// With --diff, two inputs (each a file or a directory) are compared: the first
// is the baseline, the second the candidate, and every row prints both values
// with the delta and the percentage change between them. That is what turns a
// harness change into a measurement instead of an impression.
package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/jonathanhecl/vibe-coder/internal/metrics"
)

const usageText = `usage: vibe-metrics <metrics.jsonl|dir> [...]
       vibe-metrics --diff <baseline|dir> <candidate|dir>`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run executes one command mode and returns the process exit code, so tests
// can drive both modes without a subprocess.
func run(args []string, out, errOut io.Writer) int {
	if len(args) > 0 && args[0] == "--diff" {
		return runDiff(args[1:], out, errOut)
	}
	if len(args) == 0 {
		fmt.Fprintln(errOut, usageText)
		return 2
	}
	files, err := metrics.ExpandInputs(args)
	if err != nil {
		fmt.Fprintf(errOut, "vibe-metrics: %v\n", err)
		return 1
	}
	return summarize(files, out, errOut)
}

// summarize prints one row per metrics file plus the TOTAL row.
func summarize(files []string, out, errOut io.Writer) int {
	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "FILE\tREQ\tERR\tPROMPT\tPROMPT/REQ\tCOMPL\tCOMPL/REQ\tSYS_AVG\tTRANSCRIPT_AVG\tMASKED\tCOMPACT\tWALL_P50\tWALL_P95")
	all := make([]metrics.Entry, 0, 64)
	for _, path := range files {
		entries, err := metrics.ReadFile(path)
		if err != nil {
			// A single unreadable file never hides the rest of the report;
			// the diff mode is the one that refuses a partial group.
			fmt.Fprintf(errOut, "vibe-metrics: %s: %v\n", path, err)
			continue
		}
		printRow(w, filepath.Base(path), derive(metrics.Summarize(entries)))
		all = append(all, entries...)
	}
	printRow(w, "TOTAL", derive(metrics.Summarize(all)))
	_ = w.Flush()
	return 0
}

// runDiff compares two metric groups side by side.
func runDiff(args []string, out, errOut io.Writer) int {
	if len(args) != 2 {
		fmt.Fprintln(errOut, usageText)
		return 2
	}
	baseline, baselineFiles, err := loadGroup(args[0])
	if err != nil {
		fmt.Fprintf(errOut, "vibe-metrics: %v\n", err)
		return 1
	}
	candidate, candidateFiles, err := loadGroup(args[1])
	if err != nil {
		fmt.Fprintf(errOut, "vibe-metrics: %v\n", err)
		return 1
	}

	w := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(w, "METRIC\tBASELINE\tCANDIDATE\tDELTA\tCHANGE")
	for _, row := range diffRows {
		base, cand := row.get(baseline), row.get(candidate)
		fmt.Fprintf(w, "%s\t%d\t%d\t%s\t%s\n", row.name, base, cand, signed(base, cand), percentChange(base, cand))
	}
	_ = w.Flush()

	// The group composition matters as much as the numbers: two runs with
	// different request counts are compared per request, not per total.
	fmt.Fprintf(out, "\nbaseline:  %s (%d files, %d requests)\n", args[0], baselineFiles, baseline.requests)
	fmt.Fprintf(out, "candidate: %s (%d files, %d requests)\n", args[1], candidateFiles, candidate.requests)
	return 0
}

// loadGroup expands one diff side and folds every entry into a Summary.
func loadGroup(arg string) (derived, int, error) {
	files, err := metrics.ExpandInputs([]string{arg})
	if err != nil {
		return derived{}, 0, err
	}
	entries := make([]metrics.Entry, 0, 64)
	for _, path := range files {
		got, err := metrics.ReadFile(path)
		if err != nil {
			return derived{}, 0, fmt.Errorf("%s: %w", path, err)
		}
		entries = append(entries, got...)
	}
	return derive(metrics.Summarize(entries)), len(files), nil
}

// derived is a Summary reduced to the numbers the reports print: totals,
// per-request averages and the wall-time percentiles.
type derived struct {
	requests      int64
	errors        int64
	promptTotal   int64
	promptAvg     int64
	completionTot int64
	completionAvg int64
	systemAvg     int64
	transcriptAvg int64
	masked        int64
	compactions   int64
	wallP50       int64
	wallP95       int64
}

func derive(s metrics.Summary) derived {
	d := derived{
		requests:      int64(s.Requests),
		errors:        int64(s.Errors),
		promptTotal:   int64(s.PromptTokens),
		completionTot: int64(s.CompletionTokens),
		masked:        int64(s.ToolResultsMasked),
		compactions:   int64(s.Compactions),
		wallP50:       s.WallP50MS,
		wallP95:       s.WallP95MS,
	}
	if s.Requests > 0 {
		d.promptAvg = int64(s.PromptTokens) / int64(s.Requests)
		d.completionAvg = int64(s.CompletionTokens) / int64(s.Requests)
		d.systemAvg = int64(s.SystemTokensEst) / int64(s.Requests)
		d.transcriptAvg = int64(s.TranscriptTokensEst) / int64(s.Requests)
	}
	return d
}

// diffRows is the report order: volume first, then what the harness spends
// per request, then the events and the latencies.
var diffRows = []struct {
	name string
	get  func(derived) int64
}{
	{"requests", func(d derived) int64 { return d.requests }},
	{"errors", func(d derived) int64 { return d.errors }},
	{"prompt tokens", func(d derived) int64 { return d.promptTotal }},
	{"prompt/req", func(d derived) int64 { return d.promptAvg }},
	{"completion tokens", func(d derived) int64 { return d.completionTot }},
	{"completion/req", func(d derived) int64 { return d.completionAvg }},
	{"system avg", func(d derived) int64 { return d.systemAvg }},
	{"transcript avg", func(d derived) int64 { return d.transcriptAvg }},
	{"tool results masked", func(d derived) int64 { return d.masked }},
	{"compactions", func(d derived) int64 { return d.compactions }},
	{"wall p50 ms", func(d derived) int64 { return d.wallP50 }},
	{"wall p95 ms", func(d derived) int64 { return d.wallP95 }},
}

// signed renders candidate-baseline with an explicit sign, so a reader scans
// the column without doing arithmetic.
func signed(base, cand int64) string {
	delta := cand - base
	if delta == 0 {
		return "0"
	}
	return fmt.Sprintf("%+d", delta)
}

// percentChange is the delta relative to the baseline. A zero baseline has no
// meaningful percentage, and saying so beats dividing by zero or printing an
// invented 100%.
func percentChange(base, cand int64) string {
	if base == 0 {
		if cand == 0 {
			return "0.0%"
		}
		return "n/a"
	}
	return fmt.Sprintf("%+.1f%%", float64(cand-base)*100/float64(base))
}

func printRow(w *tabwriter.Writer, name string, d derived) {
	fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\t%d\n",
		name, d.requests, d.errors, d.promptTotal, d.promptAvg,
		d.completionTot, d.completionAvg, d.systemAvg, d.transcriptAvg,
		d.masked, d.compactions, d.wallP50, d.wallP95)
}
