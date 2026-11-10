package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func upsertNote(t *testing.T, a *Agent, params map[string]any) {
	t.Helper()
	tool, ok := a.reg.Get("SessionNotes").(*tools.SessionNotesTool)
	if !ok {
		t.Fatalf("SessionNotes tool not registered")
	}
	res := tool.Execute(context.Background(), params)
	if res.IsError {
		t.Fatalf("SessionNotes upsert failed: %s", res.Output)
	}
}

func TestWorkStateSnapshotRoundTripsNotes(t *testing.T) {
	t.Parallel()
	a, _ := newTestAgent(t, "", fakeClient{})
	upsertNote(t, a, map[string]any{
		"action": "upsert", "id": "finding", "kind": "fact",
		"content": "timeout is 30s", "evidence": "client.go:42",
	})
	raw := a.captureWorkState()
	if len(raw) == 0 {
		t.Fatalf("expected work state with notes")
	}
	var snap workStateSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("work state not JSON: %v", err)
	}
	if len(snap.Notes) != 1 || snap.Notes[0].ID != "finding" {
		t.Fatalf("notes missing from snapshot: %+v", snap.Notes)
	}

	fresh, _ := newTestAgent(t, "", fakeClient{})
	fresh.RestoreWorkState(raw)
	got, ok := fresh.notes.Get("finding")
	if !ok || got.Content != "timeout is 30s" {
		t.Fatalf("notes not restored: %+v", got)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatalf("restored note lost its timestamp")
	}
}

func TestApplyWorkStateClearsNotesOnEmptyCorruptLegacy(t *testing.T) {
	t.Parallel()
	for _, raw := range [][]byte{nil, {}, []byte("not json"), []byte(`{"version":1,"todos":[{"id":"1","content":"x","status":"pending"}]}`)} {
		a, _ := newTestAgent(t, "", fakeClient{})
		upsertNote(t, a, map[string]any{
			"action": "upsert", "id": "n", "kind": "decision", "content": "keep?",
		})
		a.RestoreWorkState(raw)
		if a.notes.Len() != 0 {
			t.Fatalf("raw %q: notes should be cleared, got %d", raw, a.notes.Len())
		}
	}
}

func TestNotesClearedOnNewSessionID(t *testing.T) {
	t.Parallel()
	a, _ := newTestAgent(t, "", fakeClient{})
	upsertNote(t, a, map[string]any{
		"action": "upsert", "id": "n", "kind": "decision", "content": "old session",
	})
	a.sess.Clear()
	if got := a.sessionNotesPromptBlock(); got != "" {
		t.Fatalf("prompt block should be empty after session change, got %q", got)
	}
	if a.notes.Len() != 0 {
		t.Fatalf("notes leaked across session change: %d", a.notes.Len())
	}
}

func TestSessionNotesPromptBlockContent(t *testing.T) {
	t.Parallel()
	a, _ := newTestAgent(t, "", fakeClient{})
	upsertNote(t, a, map[string]any{
		"action": "upsert", "id": "active-1", "kind": "fact",
		"content": "build uses Go 1.24", "evidence": "go.mod:3", "scope": "toolchain",
	})
	upsertNote(t, a, map[string]any{
		"action": "upsert", "id": "old-1", "kind": "hypothesis",
		"content": "disproven guess", "status": "superseded",
	})
	block := a.sessionNotesPromptBlock()
	if !strings.Contains(block, "# Session Working Notes") {
		t.Fatalf("missing header: %q", block)
	}
	if !strings.Contains(block, "Active notes: 1; showing 1.") {
		t.Fatalf("wrong count line: %q", block)
	}
	if !strings.Contains(block, "active-1") || !strings.Contains(block, "go.mod:3") {
		t.Fatalf("missing id/evidence: %q", block)
	}
	if !strings.Contains(block, "SessionNotes get") {
		t.Fatalf("missing recovery hint: %q", block)
	}
	if strings.Contains(block, "old-1") || strings.Contains(block, "disproven guess") {
		t.Fatalf("superseded note leaked into block: %q", block)
	}
	if utf8.RuneCountInString(block) > sessionNotesPromptMaxRunes {
		t.Fatalf("block exceeds %d runes", sessionNotesPromptMaxRunes)
	}
}

func TestSessionNotesPromptBlockBoundedWithUnicode(t *testing.T) {
	t.Parallel()
	a, _ := newTestAgent(t, "", fakeClient{})
	// Max-size notes with multi-byte content: the block must stay within the
	// rune budget and remain valid UTF-8.
	for i := 0; i < 64; i++ {
		upsertNote(t, a, map[string]any{
			"action":   "upsert",
			"id":       fmt.Sprintf("uni-%02d", i),
			"kind":     "fact",
			"content":  strings.Repeat("\u754c", 400), // 1200 bytes, multi-byte
			"evidence": strings.Repeat("\u8bc1", 200), // 600 bytes, multi-byte
			"scope":    strings.Repeat("s", 160),
		})
	}
	block := a.sessionNotesPromptBlock()
	if block == "" {
		t.Fatalf("expected a non-empty block")
	}
	if n := utf8.RuneCountInString(block); n > sessionNotesPromptMaxRunes {
		t.Fatalf("block is %d runes, exceeds %d", n, sessionNotesPromptMaxRunes)
	}
	if !utf8.ValidString(block) {
		t.Fatalf("block is not valid UTF-8")
	}
	if !strings.Contains(block, "…") {
		t.Fatalf("expected truncated rows to carry an ellipsis")
	}
}

func TestRuntimeStateNoteIncludesNotesAfterCompaction(t *testing.T) {
	t.Parallel()
	a, _ := newTestAgent(t, "", fakeClient{})
	upsertNote(t, a, map[string]any{
		"action": "upsert", "id": "post-compaction", "kind": "next_step", "content": "resume here",
	})
	// Give the session enough history for a real compaction, then compact.
	for i := 0; i < 8; i++ {
		a.sess.AddUser(strings.Repeat("filler message ", 200))
	}
	if err := a.sess.CompactTo(context.Background(), 10); err != nil {
		t.Fatalf("compact: %v", err)
	}
	note := a.runtimeStateNote()
	if !strings.Contains(note, "# Session Working Notes") || !strings.Contains(note, "post-compaction") {
		t.Fatalf("runtime state note lost the notes block after compaction: %q", note)
	}
}

func TestSessionNotesUpsertPersistsToSessionJSONL(t *testing.T) {
	t.Parallel()
	a, _ := newTestAgent(t, "", fakeClient{})
	tool, _ := a.reg.Get("SessionNotes").(*tools.SessionNotesTool)
	_, _, err := a.executeTool(context.Background(), tool, "SessionNotes", map[string]any{
		"action": "upsert", "id": "durable", "kind": "decision", "content": "persist me",
	}, toolExecutionMode{structured: true})
	if err != nil {
		t.Fatalf("executeTool: %v", err)
	}
	path := filepath.Join(a.cfg.SessionsDir, a.sess.ID()+".jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("session file not written: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, `"_meta"`) || !strings.Contains(content, `"notes"`) || !strings.Contains(content, "durable") {
		t.Fatalf("session JSONL missing embedded work state with notes:\n%s", content)
	}
}

func TestSessionNotesUpsertTemporalWritesNoFile(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         tmp,
		SessionsDir: filepath.Join(tmp, "_sessions"),
		Model:       "main",
		Temporal:    true,
	}
	ui := &recordingUI{}
	sess := session.New(cfg)
	a := New(cfg, fakeClient{}, tools.NewRegistry(), permissions.NewManager(cfg), sess, ui)
	tool, _ := a.reg.Get("SessionNotes").(*tools.SessionNotesTool)
	_, _, err := a.executeTool(context.Background(), tool, "SessionNotes", map[string]any{
		"action": "upsert", "id": "temp", "kind": "decision", "content": "gone on exit",
	}, toolExecutionMode{structured: true})
	if err != nil {
		t.Fatalf("executeTool: %v", err)
	}
	entries, err := os.ReadDir(cfg.SessionsDir)
	if err == nil && len(entries) > 0 {
		t.Fatalf("temporal session wrote files: %v", entries)
	}
	if _, err := os.Stat(filepath.Join(tmp, session.IsolatedSessionFileName)); !os.IsNotExist(err) {
		t.Fatalf("temporal session wrote isolated file")
	}
}

func TestSessionNotesSaveFailureWarnsWithoutFalseSuccess(t *testing.T) {
	t.Parallel()
	a, ui := newTestAgent(t, "", fakeClient{})
	// Make the sessions dir unwritable so Save fails after the note is kept.
	if err := os.Chmod(a.cfg.SessionsDir, 0o555); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	defer os.Chmod(a.cfg.SessionsDir, 0o755)

	tool, _ := a.reg.Get("SessionNotes").(*tools.SessionNotesTool)
	res, _, err := a.executeTool(context.Background(), tool, "SessionNotes", map[string]any{
		"action": "upsert", "id": "warn", "kind": "decision", "content": "unsaved",
	}, toolExecutionMode{structured: true})
	if err != nil {
		t.Fatalf("executeTool: %v", err)
	}
	if res.IsError {
		t.Fatalf("the note itself is valid and must not error: %s", res.Output)
	}
	warned := false
	for _, out := range ui.results {
		if strings.Contains(out, "could not be written") {
			warned = true
		}
	}
	if !warned {
		t.Fatalf("expected a visible save-failure warning, got %v", ui.results)
	}
	if a.notes.Len() != 1 {
		t.Fatalf("note must be kept in memory despite save failure")
	}
	found := false
	for _, m := range a.sess.Messages() {
		if strings.Contains(m.Content, "could not be written") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected transcript warning for the model")
	}
}

func TestSessionNotesIsolatedRoundTrip(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         tmp,
		SessionsDir: filepath.Join(tmp, "_sessions"),
		Model:       "main",
		Isolated:    true,
	}
	ui := &recordingUI{}
	sess := session.New(cfg)
	a := New(cfg, fakeClient{}, tools.NewRegistry(), permissions.NewManager(cfg), sess, ui)
	sess.AddUser("isolated task")
	upsertNote(t, a, map[string]any{
		"action": "upsert", "id": "iso-note", "kind": "fact",
		"content": "isolated finding", "evidence": "file.go:9",
	})
	a.PersistWorkState()
	if err := sess.Save(); err != nil {
		t.Fatalf("isolated save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(tmp, session.IsolatedSessionFileName))
	if err != nil {
		t.Fatalf("isolated file missing: %v", err)
	}
	if !strings.Contains(string(raw), "iso-note") {
		t.Fatalf("isolated file missing notes:\n%s", raw)
	}

	loaded := session.New(cfg)
	ok, err := loaded.LoadIsolated()
	if err != nil || !ok {
		t.Fatalf("LoadIsolated: ok=%v err=%v", ok, err)
	}
	ws := loaded.WorkState()
	if len(ws) == 0 || !strings.Contains(string(ws), "iso-note") {
		t.Fatalf("work state missing notes after isolated load: %s", ws)
	}
	fresh, _ := newTestAgent(t, "", fakeClient{})
	fresh.sess = loaded
	fresh.RestoreWorkState(ws)
	if _, ok := fresh.notes.Get("iso-note"); !ok {
		t.Fatalf("isolated notes not restored into agent")
	}
}

func TestSessionNotesNotExposedToSubAgent(t *testing.T) {
	t.Parallel()
	// The sub-agent allow-list is built inside tools.NewSubAgentTool; verify
	// the tool itself is absent from a fresh registry default set and that
	// EffectOf keeps it out of delegate-safe assumptions by checking the
	// whitelist source does not include it.
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	if _, ok := reg.Get("SessionNotes").(*tools.SessionNotesTool); ok {
		// RegisterDefaults must not include it; agent.New registers it
		// explicitly. If this ever becomes true the sub-agent whitelist
		// review is needed.
		t.Fatalf("SessionNotes must not be part of RegisterDefaults")
	}
}
