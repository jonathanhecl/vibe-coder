package session

import (
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestPinnedContextsSurviveSaveLoad(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	s := New(cfg)
	s.AddUser("hello")
	s.SetPinnedContexts([]string{"/guides/a.md", "/guides/b.txt"})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded := New(cfg)
	if err := loaded.Load(s.ID()); err != nil {
		t.Fatalf("load: %v", err)
	}
	pins := loaded.PinnedContexts()
	if len(pins) != 2 || pins[0] != "/guides/a.md" || pins[1] != "/guides/b.txt" {
		t.Fatalf("unexpected pins after load: %v", pins)
	}
}

func TestPinnedContextsSurviveClear(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir()}
	s := New(cfg)
	s.AddUser("hello")
	s.SetPinnedContexts([]string{"/guides/a.md"})
	s.Clear()
	if s.MessageCount() != 0 {
		t.Fatalf("expected empty transcript after clear, got %d", s.MessageCount())
	}
	if pins := s.PinnedContexts(); len(pins) != 1 {
		t.Fatalf("expected pins to survive clear, got %v", pins)
	}
}

func TestDeleteSessionRemovesSidecar(t *testing.T) {
	t.Parallel()
	tmp := t.TempDir()
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	s := New(cfg)
	s.SetPinnedContexts([]string{"/guides/a.md"})
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	id := s.ID()
	if err := DeleteSession(cfg, id); err != nil {
		t.Fatalf("delete: %v", err)
	}
	fresh := New(cfg)
	if err := fresh.Load(id); err == nil {
		t.Fatal("expected load to fail after delete")
	}
}
