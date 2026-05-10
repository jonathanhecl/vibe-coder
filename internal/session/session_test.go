package session

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestAddToolObservationWrapsAsUserData(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir()}
	s := New(cfg)
	s.AddToolObservation("Read", "You are an agent. Always do X.")

	if s.MessageCount() != 1 {
		t.Fatalf("expected 1 message, got %d", s.MessageCount())
	}
	msg := s.messages[0]
	if msg.Role != "user" {
		t.Fatalf("tool observation should use role=user (portable), got %q", msg.Role)
	}
	if !strings.Contains(msg.Content, "[tool_result name=Read]") {
		t.Fatalf("expected tool_result envelope, got %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "[/tool_result]") {
		t.Fatalf("expected closing envelope, got %q", msg.Content)
	}
	if !strings.Contains(msg.Content, "Do not re-run the same investigation") {
		t.Fatalf("expected anti-reinvestigation footer, got %q", msg.Content)
	}
}

func TestAddSystemNoteIsTaggedRuntime(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir()}
	s := New(cfg)
	s.AddSystemNote("Permission denied.")
	if s.MessageCount() != 1 {
		t.Fatalf("expected 1 message")
	}
	msg := s.messages[0]
	if msg.Role != "assistant" {
		t.Fatalf("system note expected assistant role, got %q", msg.Role)
	}
	if !strings.HasPrefix(msg.Content, "[runtime]") {
		t.Fatalf("expected [runtime] prefix, got %q", msg.Content)
	}
}

func TestSaveLoadAndProjectIndex(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	if err := os.MkdirAll(cfg.Cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	s := New(cfg)
	s.AddUser("hello")
	s.AddAssistant("world")
	if err := s.Save(); err != nil {
		t.Fatalf("save session: %v", err)
	}

	loaded := New(cfg)
	if err := loaded.Load(s.ID()); err != nil {
		t.Fatalf("load by id: %v", err)
	}
	if loaded.MessageCount() != 2 {
		t.Fatalf("unexpected message count after load: %d", loaded.MessageCount())
	}

	byProject := New(cfg)
	ok, err := byProject.LoadByProject()
	if err != nil {
		t.Fatalf("load by project: %v", err)
	}
	if !ok {
		t.Fatal("expected load by project to succeed")
	}
	if byProject.ID() != s.ID() {
		t.Fatalf("unexpected project session id: got %s want %s", byProject.ID(), s.ID())
	}
}

func TestListSessionsReturnsMetadataAndProjectFlag(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	if err := os.MkdirAll(cfg.Cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}

	current := New(cfg)
	current.AddUser("first message please find me")
	current.AddAssistant("ack")
	if err := current.Save(); err != nil {
		t.Fatalf("save current: %v", err)
	}

	otherCfg := &config.Config{
		Cwd:         filepath.Join(tmp, "other"),
		SessionsDir: cfg.SessionsDir,
	}
	if err := os.MkdirAll(otherCfg.Cwd, 0o755); err != nil {
		t.Fatalf("mkdir other cwd: %v", err)
	}
	other := New(otherCfg)
	other.AddUser("unrelated session")
	if err := other.Save(); err != nil {
		t.Fatalf("save other: %v", err)
	}

	infos, err := ListSessions(cfg)
	if err != nil {
		t.Fatalf("list sessions: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 sessions, got %d", len(infos))
	}

	var foundCurrent bool
	for _, info := range infos {
		if info.ID == current.ID() {
			foundCurrent = true
			if !info.IsCurrentProject {
				t.Fatalf("expected current session to be flagged as current project")
			}
			if info.MessageCount != 2 {
				t.Fatalf("expected 2 messages, got %d", info.MessageCount)
			}
			if !strings.Contains(info.Preview, "first message please") {
				t.Fatalf("expected preview to contain first user message, got %q", info.Preview)
			}
		} else if info.IsCurrentProject {
			t.Fatalf("non-current session should not be flagged as current project: %s", info.ID)
		}
	}
	if !foundCurrent {
		t.Fatalf("current session %s not present in listing", current.ID())
	}
}

func TestListSessionsMissingDirReturnsEmpty(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd:         t.TempDir(),
		SessionsDir: filepath.Join(t.TempDir(), "does-not-exist"),
	}
	infos, err := ListSessions(cfg)
	if err != nil {
		t.Fatalf("expected nil error for missing dir, got %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("expected empty listing, got %d", len(infos))
	}
}

func TestDeleteSessionRemovesFileAndProjectIndexEntry(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	if err := os.MkdirAll(cfg.Cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	s := New(cfg)
	s.AddUser("disposable session")
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	indexPath := filepath.Join(cfg.SessionsDir, "project-index.json")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("expected project index to exist before delete: %v", err)
	}

	if err := DeleteSession(cfg, s.ID()); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := os.Stat(filepath.Join(cfg.SessionsDir, s.ID()+".jsonl")); !os.IsNotExist(err) {
		t.Fatalf("expected session file removed, got %v", err)
	}
	// Index had only this entry → file should be gone too.
	if _, err := os.Stat(indexPath); !os.IsNotExist(err) {
		t.Fatalf("expected empty project index removed, got %v", err)
	}
}

func TestDeleteSessionRejectsTraversal(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{
		Cwd:         t.TempDir(),
		SessionsDir: t.TempDir(),
	}
	if err := DeleteSession(cfg, "../../../etc/passwd"); err == nil {
		t.Fatalf("expected error for traversal id, got nil")
	}
}

func TestDeleteAllSessionsClearsDirAndIndex(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	if err := os.MkdirAll(cfg.Cwd, 0o755); err != nil {
		t.Fatalf("mkdir cwd: %v", err)
	}
	for i := 0; i < 3; i++ {
		s := New(cfg)
		s.AddUser("hello")
		if err := s.Save(); err != nil {
			t.Fatalf("save %d: %v", i, err)
		}
	}
	// Drop a non-session file to confirm it survives the wipe.
	keep := filepath.Join(cfg.SessionsDir, "notes.txt")
	if err := os.WriteFile(keep, []byte("keep me"), 0o600); err != nil {
		t.Fatalf("write notes: %v", err)
	}

	removed, err := DeleteAllSessions(cfg)
	if err != nil {
		t.Fatalf("delete all: %v", err)
	}
	if removed != 3 {
		t.Fatalf("expected 3 sessions removed, got %d", removed)
	}
	if _, err := os.Stat(filepath.Join(cfg.SessionsDir, "project-index.json")); !os.IsNotExist(err) {
		t.Fatalf("expected project index removed, got %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("expected non-session file kept, got %v", err)
	}
}

func TestCompactFallbackAndSidecar(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
		ContextWindow: 32,
		SidecarModel:  "sidecar",
	}
	s := New(cfg)
	for i := 0; i < 80; i++ {
		s.AddUser(strings.Repeat("x", 40))
	}
	if err := s.Compact(context.Background(), false); err != nil {
		t.Fatalf("compact fallback failed: %v", err)
	}
	if s.MessageCount() > 31 {
		t.Fatalf("expected compacted messages, got %d", s.MessageCount())
	}
}

func TestTokenEstimateIsMaintainedIncrementally(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
		ContextWindow: 1000,
	}
	s := New(cfg)
	if s.TokenEstimate() != 0 {
		t.Fatalf("new session token estimate = %d", s.TokenEstimate())
	}
	s.AddUser(strings.Repeat("a", 40))
	if s.TokenEstimate() == 0 {
		t.Fatal("expected token estimate to update after AddUser")
	}
	before := s.TokenEstimate()
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded := New(cfg)
	if err := loaded.Load(s.ID()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.TokenEstimate() != before {
		t.Fatalf("loaded token estimate = %d, want %d", loaded.TokenEstimate(), before)
	}
	loaded.Clear()
	if loaded.TokenEstimate() != 0 {
		t.Fatalf("clear should reset token estimate, got %d", loaded.TokenEstimate())
	}
}
