package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestNormalSaveEmbedsWorkStateInSessionJSONL(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{Cwd: filepath.Join(tmp, "project"), SessionsDir: filepath.Join(tmp, "sessions")}
	s := New(cfg)
	s.AddUser("task")
	blob := []byte(`{"version":1,"notes":[{"id":"n1","kind":"fact","content":"c","evidence":"e","status":"active","updated_at":"2024-01-01T00:00:00Z"}]}`)
	s.SetWorkState(blob)

	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(cfg.SessionsDir, s.ID()+".jsonl"))
	if err != nil {
		t.Fatalf("read session file: %v", err)
	}
	content := string(raw)
	if !strings.Contains(content, `"_meta"`) || !strings.Contains(content, `"work_state"`) || !strings.Contains(content, "n1") {
		t.Fatalf("session file missing embedded work state:\n%s", content)
	}
	// The legacy sidecar is still written for backwards compatibility.
	if _, err := os.Stat(filepath.Join(cfg.SessionsDir, s.ID()+".work.json")); err != nil {
		t.Fatalf("legacy work state sidecar missing: %v", err)
	}
}

func TestLoadPrefersEmbeddedWorkStateOverSidecar(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{Cwd: filepath.Join(tmp, "project"), SessionsDir: filepath.Join(tmp, "sessions")}
	s := New(cfg)
	s.AddUser("task")
	embedded := []byte(`{"version":1,"notes":[{"id":"embedded","kind":"decision","content":"c","status":"active","updated_at":"2024-01-01T00:00:00Z"}]}`)
	s.SetWorkState(embedded)
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	// Corrupt the sidecar with a different blob: the embedded copy must win.
	sidecarPath, err := workStatePath(cfg.SessionsDir, s.ID())
	if err != nil {
		t.Fatalf("workStatePath: %v", err)
	}
	conflict := []byte(`{"work_state":{"version":1,"notes":[{"id":"sidecar","kind":"decision","content":"c","status":"active","updated_at":"2024-01-01T00:00:00Z"}]}}`)
	if err := os.WriteFile(sidecarPath, conflict, 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	loaded := New(cfg)
	if err := loaded.Load(s.ID()); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := string(loaded.WorkState())
	if !strings.Contains(got, "embedded") || strings.Contains(got, "sidecar") {
		t.Fatalf("embedded work state should win over sidecar, got %s", got)
	}
}

func TestLoadFallsBackToLegacySidecar(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{Cwd: filepath.Join(tmp, "project"), SessionsDir: filepath.Join(tmp, "sessions")}
	if err := os.MkdirAll(cfg.SessionsDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A pre-embedding session file: plain message lines, no _meta.
	id := "legacy-session-1"
	msg := `{"role":"user","content":"old task","timestamp":"2024-01-01T00:00:00Z"}` + "\n"
	if err := os.WriteFile(filepath.Join(cfg.SessionsDir, id+".jsonl"), []byte(msg), 0o600); err != nil {
		t.Fatalf("write legacy file: %v", err)
	}
	blob := []byte(`{"version":1,"notes":[{"id":"legacy","kind":"fact","content":"c","evidence":"e","status":"active","updated_at":"2024-01-01T00:00:00Z"}]}`)
	sidecarPath, err := workStatePath(cfg.SessionsDir, id)
	if err != nil {
		t.Fatalf("workStatePath: %v", err)
	}
	if err := os.WriteFile(sidecarPath, []byte(`{"work_state":`+string(blob)+`}`), 0o600); err != nil {
		t.Fatalf("write sidecar: %v", err)
	}

	loaded := New(cfg)
	if err := loaded.Load(id); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := string(loaded.WorkState())
	if !strings.Contains(got, "legacy") {
		t.Fatalf("legacy sidecar work state not loaded: %s", got)
	}
}

func TestScanSessionMetaSkipsMetadataLines(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{Cwd: filepath.Join(tmp, "project"), SessionsDir: filepath.Join(tmp, "sessions")}
	s := New(cfg)
	s.AddUser("one")
	s.AddUser("two")
	s.SetWorkState([]byte(`{"version":1,"notes":[{"id":"n","kind":"decision","content":"c","status":"active","updated_at":"2024-01-01T00:00:00Z"}]}`))
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	path := filepath.Join(cfg.SessionsDir, s.ID()+".jsonl")
	count, _ := scanSessionMeta(path)
	if count != 2 {
		t.Fatalf("metadata line inflated message count: got %d, want 2", count)
	}
}

func TestClearClearsWorkState(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir()}
	s := New(cfg)
	s.SetWorkState([]byte(`{"version":1,"notes":[{"id":"n","kind":"decision","content":"c","status":"active","updated_at":"2024-01-01T00:00:00Z"}]}`))
	s.Clear()
	if got := s.WorkState(); len(got) != 0 {
		t.Fatalf("Clear must drop work state, got %s", got)
	}
}

func TestIsolatedWorkStateWithNotesRoundTrips(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{Cwd: tmp, SessionsDir: filepath.Join(tmp, "_sessions"), Isolated: true}
	s := New(cfg)
	s.AddUser("task")
	blob := []byte(`{"version":1,"notes":[{"id":"iso","kind":"fact","content":"c","evidence":"e","status":"active","updated_at":"2024-01-01T00:00:00Z"}]}`)
	s.SetWorkState(blob)
	if err := s.Save(); err != nil {
		t.Fatalf("isolated save: %v", err)
	}
	loaded := New(cfg)
	ok, err := loaded.LoadIsolated()
	if err != nil || !ok {
		t.Fatalf("LoadIsolated: ok=%v err=%v", ok, err)
	}
	got := string(loaded.WorkState())
	if !strings.Contains(got, "iso") {
		t.Fatalf("isolated work state lost notes: %s", got)
	}
}
