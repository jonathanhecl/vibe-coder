package session

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

func TestMediaDir_Layout(t *testing.T) {
	sessionsDir := t.TempDir()

	dir, err := MediaDir(sessionsDir, "abc123")
	if err != nil {
		t.Fatalf("MediaDir: %v", err)
	}

	want := filepath.Join(sessionsDir, "media", "abc123")
	if dir != want {
		t.Errorf("MediaDir = %q, want %q", dir, want)
	}
	// MediaDir must not create anything: read-only lookups stay side-effect free.
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("MediaDir should not create the dir, stat err = %v", err)
	}
}

func TestMediaDir_RejectsEmptySessionsDir(t *testing.T) {
	// An unset sessions dir would otherwise resolve relative to the process
	// cwd and drop media into the user's project directory.
	for _, dir := range []string{"", "   "} {
		if got, err := MediaDir(dir, "abc123"); err == nil {
			t.Errorf("MediaDir(%q) = %q, want error", dir, got)
		}
	}
}

func TestMediaDir_RejectsInvalidID(t *testing.T) {
	if got, err := MediaDir(t.TempDir(), ""); err == nil {
		t.Errorf("MediaDir with empty id = %q, want error", got)
	}
}

func TestMediaDir_SanitizesID(t *testing.T) {
	sessionsDir := t.TempDir()

	dir, err := MediaDir(sessionsDir, "../escape")
	if err != nil {
		t.Fatalf("MediaDir: %v", err)
	}

	root := filepath.Join(sessionsDir, mediaRootName) + string(filepath.Separator)
	rel, relErr := filepath.Rel(sessionsDir, dir)
	if relErr != nil {
		t.Fatalf("filepath.Rel: %v", relErr)
	}
	if len(rel) == 0 || rel[0] == '.' || rel[0] == filepath.Separator {
		t.Errorf("MediaDir escaped the sessions dir: %q", rel)
	}
	if got := filepath.Dir(rel); got != mediaRootName {
		t.Errorf("media dir parent = %q, want %q (got %q)", got, mediaRootName, rel)
	}
	if !strings.HasPrefix(dir, root) {
		t.Errorf("MediaDir = %q, want it under %q", dir, root)
	}
}

func TestEnsureMediaDir_CreatesOnDemand(t *testing.T) {
	sessionsDir := t.TempDir()

	dir, err := EnsureMediaDir(sessionsDir, "abc123")
	if err != nil {
		t.Fatalf("EnsureMediaDir: %v", err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("EnsureMediaDir did not create the dir: %v", err)
	}
	if !info.IsDir() {
		t.Fatalf("EnsureMediaDir created a non-dir at %q", dir)
	}

	// Idempotent: a second call must succeed on the existing dir.
	if again, err := EnsureMediaDir(sessionsDir, "abc123"); err != nil || again != dir {
		t.Errorf("EnsureMediaDir second call = (%q, %v), want (%q, nil)", again, err, dir)
	}
}

func TestRemoveMediaDir_RemovesFiles(t *testing.T) {
	sessionsDir := t.TempDir()
	dir, err := EnsureMediaDir(sessionsDir, "abc123")
	if err != nil {
		t.Fatalf("EnsureMediaDir: %v", err)
	}
	img := filepath.Join(dir, "vibe_clipboard_1.png")
	if err := os.WriteFile(img, []byte("png"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	if err := RemoveMediaDir(sessionsDir, "abc123"); err != nil {
		t.Fatalf("RemoveMediaDir: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("media dir still present after removal, stat err = %v", err)
	}
	// A missing dir is not an error so repeated deletes stay safe.
	if err := RemoveMediaDir(sessionsDir, "abc123"); err != nil {
		t.Errorf("RemoveMediaDir on missing dir = %v, want nil", err)
	}
}

func TestDeleteSession_RemovesMediaDir(t *testing.T) {
	sessionsDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(sessionsDir, "abc123.jsonl"), []byte(`{"role":"user","content":"hi"}`+"\n"), 0o600); err != nil {
		t.Fatalf("write session: %v", err)
	}
	dir, err := EnsureMediaDir(sessionsDir, "abc123")
	if err != nil {
		t.Fatalf("EnsureMediaDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vibe_clipboard_1.png"), []byte("png"), 0o600); err != nil {
		t.Fatalf("write image: %v", err)
	}

	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: sessionsDir}
	if err := DeleteSession(cfg, "abc123"); err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("pasted image survived session deletion, stat err = %v", err)
	}
}

func TestDeleteAllSessions_RemovesMediaRoot(t *testing.T) {
	sessionsDir := t.TempDir()
	for _, id := range []string{"one", "two"} {
		if err := os.WriteFile(filepath.Join(sessionsDir, id+".jsonl"), []byte(`{"role":"user","content":"hi"}`+"\n"), 0o600); err != nil {
			t.Fatalf("write session %s: %v", id, err)
		}
		dir, err := EnsureMediaDir(sessionsDir, id)
		if err != nil {
			t.Fatalf("EnsureMediaDir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "vibe_clipboard_1.png"), []byte("png"), 0o600); err != nil {
			t.Fatalf("write image: %v", err)
		}
	}

	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: sessionsDir}
	removed, err := DeleteAllSessions(cfg)
	if err != nil {
		t.Fatalf("DeleteAllSessions: %v", err)
	}
	if removed != 2 {
		t.Errorf("DeleteAllSessions removed %d sessions, want 2", removed)
	}

	mediaRoot := filepath.Join(sessionsDir, mediaRootName)
	if _, err := os.Stat(mediaRoot); !os.IsNotExist(err) {
		t.Errorf("media root survived DeleteAllSessions, stat err = %v", err)
	}
}
