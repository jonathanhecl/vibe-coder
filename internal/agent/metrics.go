package agent

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
	"github.com/jonathanhecl/vibe-coder/internal/metrics"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// MetricsPath returns the per-session JSONL metrics path, or "" when
// recording is suppressed: temporal and isolated sessions record nothing on
// disk (isolated keeps only its folder session, temporal keeps no trace), and
// MetricsDisabled turns the file off while the in-memory summary keeps
// running so /stats still reports the current run.
func (a *Agent) MetricsPath() string {
	if a == nil || a.cfg == nil || a.sess == nil {
		return ""
	}
	if a.cfg.Harness.MetricsDisabled || a.cfg.Temporal || a.cfg.Isolated {
		return ""
	}
	dir := strings.TrimSpace(a.cfg.StateDir)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "metrics", safeRunLogName(a.sess.ID())+".jsonl")
}

// MetricsSummary returns the running totals for /stats.
func (a *Agent) MetricsSummary() metrics.Summary {
	if a == nil || a.metrics == nil {
		return metrics.Summary{}
	}
	return a.metrics.Summary()
}

// turnUsage converts the provider-reported usage of the last chat stream into
// the metrics the UI footer renders. It returns the zero value when no usage
// arrived, which makes the UI omit the token-speed segment.
func (a *Agent) turnUsage() tui.TurnUsage {
	if a == nil || a.lastUsage == nil {
		return tui.TurnUsage{}
	}
	return tui.TurnUsage{
		CompletionTokens: a.lastUsage.CompletionTokens,
		EvalDuration:     a.lastUsage.EvalDuration,
	}
}

// recordChatAttempt stores one metrics entry for a completed model request.
// start is the moment the request was sent; toolCalls counts the parsed
// tool invocations (0 on error); callErr is nil on success.
func (a *Agent) recordChatAttempt(systemPrompt string, wstats windowStats, start time.Time, toolCalls int, callErr error) {
	if a == nil || a.metrics == nil || a.sess == nil || a.cfg == nil {
		return
	}
	a.metricsSeq++
	toolMode := "native"
	if a.xmlToolMode() {
		toolMode = "xml"
	}
	e := metrics.Entry{
		Time:                time.Now().UTC(),
		Session:             a.sess.ID(),
		Model:               a.cfg.Model,
		Seq:                 a.metricsSeq,
		ToolMode:            toolMode,
		SystemTokensEst:     session.EstimateTokens(systemPrompt),
		ToolSchemaTokensEst: a.toolSchemaTokens(),
		TranscriptTokensEst: wstats.TranscriptTokensEst,
		MessagesTotal:       wstats.MessagesTotal,
		MessagesSent:        wstats.MessagesSent,
		ToolResultsMasked:   wstats.ToolResultsMasked,
		WindowStart:         wstats.WindowStart,
		MessagesDropped:     wstats.MessagesDropped,
		WallMS:              time.Since(start).Milliseconds(),
		ToolCalls:           toolCalls,
	}
	if u := a.lastUsage; u != nil {
		e.PromptTokens = u.PromptTokens
		e.CompletionTokens = u.CompletionTokens
		e.PromptEvalMS = u.PromptEvalDuration.Milliseconds()
		e.EvalMS = u.EvalDuration.Milliseconds()
		// Feed the real prompt count back so the session's coarse estimate is
		// corrected by what the provider actually counted. ToolSchemaTokensEst
		// is included because providers bill native tool definitions in
		// PromptTokens.
		a.sess.ObservePromptUsage(e.SystemTokensEst+e.ToolSchemaTokensEst+e.TranscriptTokensEst, u.PromptTokens)
	}
	if callErr != nil {
		// Error text can carry secrets embedded in provider or tool error
		// output; run it through the same redactor the run log uses.
		e.Error = logger.RedactText(callErr.Error())
	}
	if a.compactedPending.Swap(false) {
		e.Compacted = true
	}
	a.metrics.Record(e)
}
