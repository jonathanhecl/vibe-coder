package session

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

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
