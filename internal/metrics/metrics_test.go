package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSummaryAddAndFormat(t *testing.T) {
	t.Parallel()

	var s Summary
	s.Add(Entry{PromptTokens: 100, CompletionTokens: 10, WallMS: 200})
	s.Add(Entry{PromptTokens: 300, CompletionTokens: 30, WallMS: 400, Error: "boom", ToolResultsMasked: 2})

	out := s.Format()
	for _, want := range []string{
		"Requests: 2 (1 errors)",
		"Prompt tokens: 400 (avg 200/request)",
		"Completion tokens: 40",
		"Tool results masked: 2",
		"Wall time: 600 ms (avg 300 ms/request)",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("Format() missing %q:\n%s", want, out)
		}
	}
}

func TestRecorderWritesJSONLAndSummarizes(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "m.jsonl")
	r := NewRecorder(func() string { return path })

	r.Record(Entry{Time: time.Now().UTC(), Session: "s1", Model: "m", Seq: 1, ToolMode: "native", PromptTokens: 5})
	r.Record(Entry{Time: time.Now().UTC(), Session: "s1", Model: "m", Seq: 2, ToolMode: "xml", Error: "x"})
	r.NoteCompaction()

	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("expected metrics file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 file mode, got %v", info.Mode().Perm())
	}

	entries, err := ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].ToolMode != "native" || entries[1].ToolMode != "xml" {
		t.Fatalf("unexpected tool modes: %+v", entries)
	}

	s := r.Summary()
	if s.Requests != 2 || s.Errors != 1 || s.Compactions != 1 || s.PromptTokens != 5 {
		t.Fatalf("unexpected summary: %+v", s)
	}
	if got := Summarize(entries); got.Requests != 2 || got.Errors != 1 {
		t.Fatalf("unexpected Summarize result: %+v", got)
	}
}

func TestRecorderMemoryOnly(t *testing.T) {
	t.Parallel()

	r := NewRecorder(func() string { return "" })
	r.Record(Entry{PromptTokens: 3})
	if s := r.Summary(); s.Requests != 1 || s.PromptTokens != 3 {
		t.Fatalf("unexpected summary: %+v", s)
	}

	var nilRecorder *Recorder
	nilRecorder.Record(Entry{})
	nilRecorder.NoteCompaction()
	if s := nilRecorder.Summary(); s.Requests != 0 {
		t.Fatal("nil recorder must be a no-op")
	}
}
