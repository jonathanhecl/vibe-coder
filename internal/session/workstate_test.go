package session

import (
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestWorkStatePersistsAcrossSaveLoad(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{Cwd: filepath.Join(tmp, "project"), SessionsDir: filepath.Join(tmp, "sessions")}
	s := New(cfg)
	s.AddUser("original task")
	blob := []byte(`{"version":1,"todos":[{"id":"1","content":"step","status":"pending"}]}`)
	s.SetWorkState(blob)

	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}

	loaded := New(cfg)
	if err := loaded.Load(s.ID()); err != nil {
		t.Fatalf("load: %v", err)
	}
	got := loaded.WorkState()
	if string(got) != string(blob) {
		t.Fatalf("work state round trip failed: got %s", got)
	}
}

func TestWorkStateClearRemovesSidecar(t *testing.T) {
	t.Parallel()

	tmp := t.TempDir()
	cfg := &config.Config{Cwd: filepath.Join(tmp, "project"), SessionsDir: filepath.Join(tmp, "sessions")}
	s := New(cfg)
	s.AddUser("task")
	s.SetWorkState([]byte(`{"version":1}`))
	if err := s.Save(); err != nil {
		t.Fatalf("save: %v", err)
	}
	s.SetWorkState(nil)
	if err := s.Save(); err != nil {
		t.Fatalf("second save: %v", err)
	}

	loaded := New(cfg)
	if err := loaded.Load(s.ID()); err != nil {
		t.Fatalf("load: %v", err)
	}
	if got := loaded.WorkState(); len(got) != 0 {
		t.Fatalf("expected cleared work state, got %s", got)
	}
}

func TestSetWorkStateOnlyBumpsRevisionOnChange(t *testing.T) {
	t.Parallel()

	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir()}
	s := New(cfg)
	before := s.revision
	s.SetWorkState([]byte("a"))
	if s.revision != before+1 {
		t.Fatalf("expected revision bump on change, got %d -> %d", before, s.revision)
	}
	s.SetWorkState([]byte("a"))
	if s.revision != before+1 {
		t.Fatalf("expected no revision bump for identical state, got %d", s.revision)
	}
}
