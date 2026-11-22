package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/metrics"
)

// writeMetrics writes one metrics JSONL file and returns its path.
func writeMetrics(t *testing.T, dir, name string, entries ...metrics.Entry) string {
	t.Helper()
	path := filepath.Join(dir, name)
	var b strings.Builder
	for _, e := range entries {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatalf("marshal entry: %v", err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestDiffReportsWindowDrops pins the row that answers "did the window cut
// history before compaction could summarize it" in an A/B run.
func TestDiffReportsWindowDrops(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	cand := t.TempDir()
	writeMetrics(t, base, "a.jsonl", metrics.Entry{PromptTokens: 100, MessagesDropped: 14})
	writeMetrics(t, cand, "a.jsonl", metrics.Entry{PromptTokens: 100, MessagesDropped: 0})

	var out, errOut bytes.Buffer
	if code := run([]string{"--diff", base, cand}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "max messages dropped") {
		t.Fatalf("diff missing the drop row:\n%s", got)
	}
	if !strings.Contains(got, "-100.0%") {
		t.Fatalf("diff should show the drop row going to zero:\n%s", got)
	}
}

func TestDiffComparesPerRequestAverages(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	cand := t.TempDir()
	// Baseline: two requests, 1000 prompt tokens → 500 per request.
	writeMetrics(t, base, "a.jsonl", metrics.Entry{PromptTokens: 400, CompletionTokens: 40, WallMS: 100, ToolResultsMasked: 2})
	writeMetrics(t, base, "b.jsonl", metrics.Entry{PromptTokens: 600, CompletionTokens: 60, WallMS: 300})
	// Candidate: one request, 200 prompt tokens, one masked result.
	writeMetrics(t, cand, "a.jsonl", metrics.Entry{PromptTokens: 200, CompletionTokens: 20, WallMS: 150, ToolResultsMasked: 1, Compacted: true})

	var out, errOut bytes.Buffer
	if code := run([]string{"--diff", base, cand}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut.String())
	}
	got := out.String()
	// prompt/req 500 → 200 is -300, -60.0%.
	if !strings.Contains(got, "-300") || !strings.Contains(got, "-60.0%") {
		t.Fatalf("missing prompt/req delta in:\n%s", got)
	}
	if !strings.Contains(got, "baseline:  "+base+" (2 files, 2 requests)") {
		t.Fatalf("missing baseline composition in:\n%s", got)
	}
	if !strings.Contains(got, "candidate: "+cand+" (1 files, 1 requests)") {
		t.Fatalf("missing candidate composition in:\n%s", got)
	}
}

func TestDiffNeedsTwoInputs(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := run([]string{"--diff", "only-one"}, &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "--diff") {
		t.Fatalf("expected usage on stderr, got %q", errOut.String())
	}
}

// A diff over a partially readable group would compare fewer files than the
// user named, so the mode refuses instead of reporting a wrong delta.
func TestDiffFailsOnMissingGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMetrics(t, dir, "a.jsonl", metrics.Entry{PromptTokens: 10})

	var out, errOut bytes.Buffer
	if code := run([]string{"--diff", filepath.Join(dir, "nope"), dir}, &out, &errOut); code != 1 {
		t.Fatalf("exit = %d, want 1", code)
	}
	if !strings.Contains(errOut.String(), "nope") {
		t.Fatalf("expected the failing path on stderr, got %q", errOut.String())
	}
}

func TestSummaryStillPrintsTotal(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	writeMetrics(t, dir, "a.jsonl", metrics.Entry{PromptTokens: 100, WallMS: 20})
	writeMetrics(t, dir, "b.jsonl", metrics.Entry{PromptTokens: 300, WallMS: 40})

	var out, errOut bytes.Buffer
	if code := run([]string{dir}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d, stderr:\n%s", code, errOut.String())
	}
	got := out.String()
	if !strings.Contains(got, "TOTAL") || !strings.Contains(got, "PROMPT/REQ") {
		t.Fatalf("summary header or total row missing:\n%s", got)
	}
}

func TestSummaryNeedsInput(t *testing.T) {
	t.Parallel()
	var out, errOut bytes.Buffer
	if code := run(nil, &out, &errOut); code != 2 {
		t.Fatalf("exit = %d, want 2", code)
	}
	if !strings.Contains(errOut.String(), "usage:") {
		t.Fatalf("expected usage on stderr, got %q", errOut.String())
	}
}

func TestPercentChangeHandlesZeroBaseline(t *testing.T) {
	t.Parallel()
	if got := percentChange(0, 0); got != "0.0%" {
		t.Fatalf("percentChange(0,0) = %q", got)
	}
	if got := percentChange(0, 50); got != "n/a" {
		t.Fatalf("percentChange(0,50) = %q, want n/a", got)
	}
	if got := percentChange(200, 150); got != "-25.0%" {
		t.Fatalf("percentChange(200,150) = %q", got)
	}
	if got := signed(7, 7); got != "0" {
		t.Fatalf("signed(7,7) = %q", got)
	}
	if got := signed(7, 9); got != "+2" {
		t.Fatalf("signed(7,9) = %q", got)
	}
}
