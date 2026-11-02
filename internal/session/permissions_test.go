package session

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// The isolated session file is the whole state of that run: it already carries
// the work state and the memory, and the permission answers belong beside them
// so a resume keeps what the user allowed.
func TestIsolatedSessionPersistsPermissionState(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Cwd: dir, Isolated: true, SessionsDir: filepath.Join(dir, "sessions")}
	s := New(cfg)
	s.AddUser("hola")
	raw := []byte(`{"allow":["read"],"saved":{"write":"allow"}}`)
	s.SetPermissionState(raw)
	if err := s.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}

	path := filepath.Join(dir, config.IsolatedSessionFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read isolated session: %v", err)
	}
	var meta struct {
		Meta struct {
			Permissions json.RawMessage `json:"permissions"`
		} `json:"_meta"`
	}
	first := data
	if i := bytes.IndexByte(first, '\n'); i >= 0 {
		first = first[:i]
	}
	if err := json.Unmarshal(first, &meta); err != nil {
		t.Fatalf("decode meta line %q: %v", first, err)
	}
	if string(meta.Meta.Permissions) != string(raw) {
		t.Fatalf("permissions in the session file = %s, want %s", meta.Meta.Permissions, raw)
	}

	loaded := New(cfg)
	ok, err := loaded.LoadIsolated()
	if err != nil || !ok {
		t.Fatalf("LoadIsolated = (%v, %v), want (true, nil)", ok, err)
	}
	if got := string(loaded.PermissionState()); got != string(raw) {
		t.Fatalf("restored permissions = %s, want %s", got, raw)
	}
}

// A plain session keeps its rules in memory only: "until exit" still means
// until this process exits.
func TestPlainSessionDoesNotWritePermissionState(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{Cwd: dir, SessionsDir: filepath.Join(dir, "sessions")}
	s := New(cfg)
	s.AddUser("hola")
	s.SetPermissionState([]byte(`{"allow":["read"]}`))
	if err := s.Save(); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	entries, err := os.ReadDir(cfg.SessionsDir)
	if err != nil {
		t.Fatalf("read sessions dir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) != ".jsonl" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(cfg.SessionsDir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		if bytes.Contains(data, []byte("permissions")) {
			t.Fatalf("session %s carries permission state: %s", e.Name(), data)
		}
	}
}
