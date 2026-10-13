// Package metrics records per-request harness efficiency data: token usage
// reported by the model provider plus the estimated sizes of the prompt
// sections the harness assembled. Entries are appended as JSONL, best-effort:
// recording must never break a run.
package metrics

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Entry is one model request. Estimated sizes use the session's heuristic
// estimator; PromptTokens/CompletionTokens and the durations come from the
// provider's own usage report when it sends one (0 otherwise).
type Entry struct {
	Time                time.Time `json:"time"`
	Session             string    `json:"session"`
	Model               string    `json:"model"`
	Seq                 int       `json:"seq"`
	ToolMode            string    `json:"tool_mode"` // native|xml
	SystemTokensEst     int       `json:"system_tokens_est"`
	ToolSchemaTokensEst int       `json:"tool_schema_tokens_est"`
	TranscriptTokensEst int       `json:"transcript_tokens_est"`
	MessagesTotal       int       `json:"messages_total"`
	MessagesSent        int       `json:"messages_sent"`
	ToolResultsMasked   int       `json:"tool_results_masked"`
	PromptTokens        int       `json:"prompt_tokens"`
	CompletionTokens    int       `json:"completion_tokens"`
	PromptEvalMS        int64     `json:"prompt_eval_ms"`
	EvalMS              int64     `json:"eval_ms"`
	WallMS              int64     `json:"wall_ms"`
	ToolCalls           int       `json:"tool_calls"`
	Compacted           bool      `json:"compacted,omitempty"`
	Error               string    `json:"error,omitempty"`
}

// Summary aggregates a session's entries for /stats.
type Summary struct {
	Requests          int
	PromptTokens      int
	CompletionTokens  int
	ToolResultsMasked int
	Compactions       int
	Errors            int
	PromptEvalMS      int64
	EvalMS            int64
	WallMS            int64
}

// Add folds one entry into the summary.
func (s *Summary) Add(e Entry) {
	s.Requests++
	s.PromptTokens += e.PromptTokens
	s.CompletionTokens += e.CompletionTokens
	s.ToolResultsMasked += e.ToolResultsMasked
	s.PromptEvalMS += e.PromptEvalMS
	s.EvalMS += e.EvalMS
	s.WallMS += e.WallMS
	if e.Error != "" {
		s.Errors++
	}
}

// Format renders a human-readable, English summary for /stats.
func (s Summary) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Requests: %d", s.Requests)
	if s.Errors > 0 {
		fmt.Fprintf(&b, " (%d errors)", s.Errors)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Prompt tokens: %d", s.PromptTokens)
	if s.Requests > 0 {
		fmt.Fprintf(&b, " (avg %d/request)", s.PromptTokens/s.Requests)
	}
	b.WriteString("\n")
	fmt.Fprintf(&b, "Completion tokens: %d\n", s.CompletionTokens)
	if s.ToolResultsMasked > 0 {
		fmt.Fprintf(&b, "Tool results masked: %d\n", s.ToolResultsMasked)
	}
	if s.Compactions > 0 {
		fmt.Fprintf(&b, "Compactions: %d\n", s.Compactions)
	}
	fmt.Fprintf(&b, "Prompt eval time: %d ms\n", s.PromptEvalMS)
	fmt.Fprintf(&b, "Eval time: %d ms\n", s.EvalMS)
	fmt.Fprintf(&b, "Wall time: %d ms", s.WallMS)
	if s.Requests > 0 {
		fmt.Fprintf(&b, " (avg %d ms/request)", s.WallMS/int64(s.Requests))
	}
	b.WriteString("\n")
	return b.String()
}

// Summarize folds a slice of entries (e.g. read back from a JSONL file)
// into a Summary.
func Summarize(entries []Entry) Summary {
	var s Summary
	for _, e := range entries {
		s.Add(e)
	}
	return s
}

// Recorder appends entries to a JSONL file and keeps running totals. The
// path function is evaluated lazily on each record so a session id change
// moves the log without recreating the recorder; "" means memory only.
type Recorder struct {
	mu      sync.Mutex
	pathFn  func() string
	summary Summary
}

// NewRecorder creates a Recorder. pathFn may be nil (memory only).
func NewRecorder(pathFn func() string) *Recorder {
	return &Recorder{pathFn: pathFn}
}

// Record appends e as one JSONL line (best-effort) and updates the summary.
func (r *Recorder) Record(e Entry) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary.Add(e)
	path := ""
	if r.pathFn != nil {
		path = r.pathFn()
	}
	if path == "" {
		return
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(raw, '\n'))
}

// NoteCompaction counts one transcript compaction in the summary.
func (r *Recorder) NoteCompaction() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.summary.Compactions++
}

// Summary returns the running totals.
func (r *Recorder) Summary() Summary {
	if r == nil {
		return Summary{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.summary
}

// ReadFile loads every JSONL entry from path.
func ReadFile(path string) ([]Entry, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Entry
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 4096), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e Entry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}
		out = append(out, e)
	}
	if err := scanner.Err(); err != nil {
		return out, err
	}
	return out, nil
}
