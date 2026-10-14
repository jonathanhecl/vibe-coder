package slash

import (
	"fmt"

	"github.com/jonathanhecl/vibe-coder/internal/metrics"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
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
	writeStats(c, tui.NewStyle(c.Out))
}

// writeStats renders the summary in the shared block layout, matching /status.
func writeStats(c *Ctx, st tui.Style) {
	reporter, ok := c.Agent.(statsReporter)
	if !ok || reporter == nil {
		fmt.Fprintln(c.Out, "No metrics recorded this session.")
		return
	}
	tui.WriteBlock(c.Out, st, "stats", statsRows(reporter))
}

// statsRows renders the metrics summary through the shared row layout, so
// /stats matches the rest of the command output. Counts drop the unit while
// timings keep their "ms" suffix, and the optional notes (errors, averages)
// are the only parts that change color.
func statsRows(reporter statsReporter) []tui.Row {
	summary := reporter.MetricsSummary()
	rows := make([]tui.Row, 0, 9)

	requests := fmt.Sprintf("%d", summary.Requests)
	rows = append(rows, tui.Row{
		Label: "Requests",
		Plain: requests + errorNote(summary, " "),
		Render: func(st tui.Style) string {
			value := st.BrightWhite(requests)
			if summary.Errors > 0 {
				value += " " + st.Red(fmt.Sprintf("(%d errors)", summary.Errors))
			}
			return value
		},
	})

	promptTokens := fmt.Sprintf("%d", summary.PromptTokens)
	rows = append(rows, tui.Row{
		Label: "Prompt tokens",
		Plain: promptTokens + perRequestNote(int64(summary.PromptTokens), summary.Requests, "", " "),
		Render: func(st tui.Style) string {
			return st.BrightWhite(promptTokens) + st.Dim(perRequestNote(int64(summary.PromptTokens), summary.Requests, "", " "))
		},
	})

	rows = append(rows, tui.Row{
		Label: "Completion tokens",
		Plain: fmt.Sprintf("%d", summary.CompletionTokens),
	})

	if summary.ToolResultsMasked > 0 {
		rows = append(rows, tui.Row{
			Label: "Tool results masked",
			Plain: fmt.Sprintf("%d", summary.ToolResultsMasked),
		})
	}
	if summary.Compactions > 0 {
		rows = append(rows, tui.Row{
			Label: "Compactions",
			Plain: fmt.Sprintf("%d", summary.Compactions),
		})
	}

	rows = append(rows, durationRow("Prompt eval time", summary.PromptEvalMS))
	rows = append(rows, durationRow("Eval time", summary.EvalMS))

	wall := fmt.Sprintf("%d", summary.WallMS)
	rows = append(rows, tui.Row{
		Label: "Wall time",
		Plain: wall + " ms" + perRequestNote(summary.WallMS, summary.Requests, " ms", " "),
		Render: func(st tui.Style) string {
			return st.BrightWhite(wall) + " " + st.Gray("ms") +
				st.Dim(perRequestNote(summary.WallMS, summary.Requests, " ms", " "))
		},
	})

	if path := reporter.MetricsPath(); path != "" {
		rows = append(rows, tui.Row{
			Label: "Metrics file",
			Plain: path,
			Render: func(st tui.Style) string {
				return st.Gray(path)
			},
		})
	} else {
		rows = append(rows, tui.Row{Label: "Metrics file", Plain: "disabled"})
	}
	return rows
}

// durationRow renders one millisecond timing, keeping the unit in both layouts.
func durationRow(label string, ms int64) tui.Row {
	value := fmt.Sprintf("%d", ms)
	return tui.Row{
		Label: label,
		Plain: value + " ms",
		Render: func(st tui.Style) string {
			return st.BrightWhite(value) + " " + st.Gray("ms")
		},
	}
}

// perRequestNote returns the per-request average of total, or an empty string
// when there is nothing to average. The suffix carries any unit the total needs.
func perRequestNote(total int64, requests int, suffix, prefix string) string {
	if requests <= 0 {
		return ""
	}
	return fmt.Sprintf("%s(avg %d%s/request)", prefix, total/int64(requests), suffix)
}

// errorNote returns the error count note, or an empty string when there were no
// errors.
func errorNote(summary metrics.Summary, prefix string) string {
	if summary.Errors <= 0 {
		return ""
	}
	return fmt.Sprintf("%s(%d errors)", prefix, summary.Errors)
}
