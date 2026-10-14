package metrics

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
)

func TestSummaryAdd(t *testing.T) {
	t.Parallel()

	var s Summary
	s.Add(Entry{PromptTokens: 100, CompletionTokens: 10, WallMS: 200})
	s.Add(Entry{PromptTokens: 300, CompletionTokens: 30, WallMS: 400, Error: "boom", ToolResultsMasked: 2})

	if s.Requests != 2 || s.Errors != 1 {
		t.Fatalf("requests=%d errors=%d, want 2 and 1", s.Requests, s.Errors)
	}
	if s.PromptTokens != 400 || s.CompletionTokens != 40 {
		t.Fatalf("prompt=%d completion=%d, want 400 and 40", s.PromptTokens, s.CompletionTokens)
	}
	if s.WallMS != 600 || s.ToolResultsMasked != 2 {
		t.Fatalf("wall=%d masked=%d, want 600 and 2", s.WallMS, s.ToolResultsMasked)
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

func TestSummarizePercentilesAndCompactions(t *testing.T) {
	t.Parallel()

	var entries []Entry
	for i := 1; i <= 20; i++ {
		entries = append(entries, Entry{WallMS: int64(i * 100)})
	}
	entries[3].Compacted = true
	entries[19].Compacted = true
	s := Summarize(entries)
	if s.WallP50MS != 1000 || s.WallP95MS != 1900 {
		t.Fatalf("p50=%d p95=%d, want 1000/1900", s.WallP50MS, s.WallP95MS)
	}
	if s.Compactions != 2 {
		t.Fatalf("Compactions=%d, want 2 (entries with Compacted)", s.Compactions)
	}
}

func TestExpandInputsDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.jsonl", "a.jsonl"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	// Non-JSONL files and subdirectories are ignored.
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	files, err := ExpandInputs([]string{dir})
	if err != nil {
		t.Fatalf("ExpandInputs: %v", err)
	}
	if len(files) != 2 || !strings.HasSuffix(files[0], "a.jsonl") || !strings.HasSuffix(files[1], "b.jsonl") {
		t.Fatalf("unexpected expansion: %v", files)
	}

	single := filepath.Join(dir, "a.jsonl")
	files, err = ExpandInputs([]string{single})
	if err != nil || len(files) != 1 || files[0] != single {
		t.Fatalf("single file expansion: %v %v", files, err)
	}
	if _, err := ExpandInputs([]string{filepath.Join(dir, "missing")}); err == nil {
		t.Fatal("missing path should error")
	}
	if _, err := ExpandInputs([]string{sub}); err == nil {
		t.Fatal("directory without jsonl files should error")
	}
}

func TestMetricsEntryErrorRedacted(t *testing.T) {
	t.Parallel()
	// The agent applies logger.RedactText to Entry.Error before recording;
	// a secret-bearing assignment must never land in the metrics file.
	raw := "request failed: API_KEY=sk-live-secret-123 for https://user:hunter2@example.com/x"
	redacted := logger.RedactText(raw)
	if strings.Contains(redacted, "sk-live-secret-123") || strings.Contains(redacted, "hunter2") {
		t.Fatalf("secret survived redaction: %q", redacted)
	}
}
