package slash

import (
	"fmt"

	"github.com/jonathanhecl/vibe-coder/internal/metrics"
)

// statsReporter is the optional slice of the agent runtime that exposes the
// session's metrics recorder. Kept local so older fakes keep compiling.
type statsReporter interface {
	MetricsSummary() metrics.Summary
	MetricsPath() string
}

// printStats shows the session's harness metrics summary plus the JSONL file
// the entries are appended to (or that recording is disabled).
func printStats(c *Ctx) {
	reporter, ok := c.Agent.(statsReporter)
	if !ok || reporter == nil {
		fmt.Fprintln(c.Out, "No metrics recorded this session.")
		return
	}
	fmt.Fprint(c.Out, reporter.MetricsSummary().Format())
	if path := reporter.MetricsPath(); path != "" {
		fmt.Fprintf(c.Out, "Metrics file: %s\n", path)
	} else {
		fmt.Fprintln(c.Out, "Metrics file disabled.")
	}
}
