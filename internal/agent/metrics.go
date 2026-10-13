package agent

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/metrics"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

// MetricsPath returns the per-session JSONL metrics path, or "" when
// recording is suppressed: temporal sessions keep nothing on disk, isolated
// sessions write next to their workspace file, and MetricsDisabled turns the
// file off while the in-memory summary keeps running.
func (a *Agent) MetricsPath() string {
	if a == nil || a.cfg == nil || a.sess == nil {
		return ""
	}
	if a.cfg.Harness.MetricsDisabled || a.cfg.Temporal {
		return ""
	}
	if a.cfg.Isolated {
		cwd := strings.TrimSpace(a.cfg.Cwd)
		if cwd == "" {
			return ""
		}
		return filepath.Join(cwd, ".vibe-isolated-metrics.jsonl")
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

// recordChatAttempt stores one metrics entry for a completed model request.
// start is the moment the request was sent; toolCalls counts the parsed
// tool invocations (0 on error); callErr is nil on success.
func (a *Agent) recordChatAttempt(systemPrompt string, wstats windowStats, start time.Time, toolCalls int, callErr error) {
	if a == nil || a.metrics == nil {
		return
	}
	a.metricsSeq++
	toolMode := "native"
	if a.xmlToolMode() {
		toolMode = "xml"
	}
	schemaTokens := 0
	if raw, err := json.Marshal(a.nativeChatTools()); err == nil {
		schemaTokens = session.EstimateTokens(string(raw))
	}
	e := metrics.Entry{
		Time:                time.Now().UTC(),
		Session:             a.sess.ID(),
		Model:               a.cfg.Model,
		Seq:                 a.metricsSeq,
		ToolMode:            toolMode,
		SystemTokensEst:     session.EstimateTokens(systemPrompt),
		ToolSchemaTokensEst: schemaTokens,
		TranscriptTokensEst: wstats.TranscriptTokensEst,
		MessagesTotal:       wstats.MessagesTotal,
		MessagesSent:        wstats.MessagesSent,
		ToolResultsMasked:   wstats.ToolResultsMasked,
		WallMS:              time.Since(start).Milliseconds(),
		ToolCalls:           toolCalls,
	}
	if u := a.lastUsage; u != nil {
		e.PromptTokens = u.PromptTokens
		e.CompletionTokens = u.CompletionTokens
		e.PromptEvalMS = u.PromptEvalDuration.Milliseconds()
		e.EvalMS = u.EvalDuration.Milliseconds()
	}
	if callErr != nil {
		e.Error = callErr.Error()
	}
	a.metrics.Record(e)
}
