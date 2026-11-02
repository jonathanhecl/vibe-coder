package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

// isolatedConfig builds the minimum config an isolated run works on: a folder,
// a session store outside it, and the global permission file a normal run would
// write to.
func isolatedConfig(dir string) *config.Config {
	return &config.Config{
		Cwd:         dir,
		Isolated:    true,
		SessionsDir: filepath.Join(dir, "sessions"),
		PermFile:    filepath.Join(dir, "vibe-coder.env"),
	}
}

// An isolated session must answer "always" and "this session" with its own file:
// the folder is the whole state of the run, and a throwaway project has no
// business rewriting the user's global rules.
func TestIsolatedPermissionsLiveInTheSessionFile(t *testing.T) {
	dir := t.TempDir()
	cfg := isolatedConfig(dir)
	sess := session.New(cfg)
	sess.AddUser("arranca el server")
	perm := permissions.NewManager(cfg)

	applyIsolatedPermissions(cfg, sess, perm)
	perm.AllowAll("Write")    // "Always allow"
	perm.AllowSession("Read") // "Allow this session"

	path := filepath.Join(dir, config.IsolatedSessionFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the isolated session file was not written: %v", err)
	}
	if !bytes.Contains(data, []byte(`"permissions"`)) {
		t.Fatalf("permission rules are missing from the session file:\n%s", data)
	}
	if _, err := os.Stat(cfg.PermFile); !os.IsNotExist(err) {
		t.Fatalf("isolated mode wrote the global permission store %s", cfg.PermFile)
	}

	// A resume starts from a fresh manager and gets the answers back.
	loaded := session.New(cfg)
	if ok, err := loaded.LoadIsolated(); err != nil || !ok {
		t.Fatalf("LoadIsolated = (%v, %v), want (true, nil)", ok, err)
	}
	resumedPerm := permissions.NewManager(cfg)
	applyIsolatedPermissions(cfg, loaded, resumedPerm)
	restored := string(resumedPerm.SnapshotRules())
	for _, want := range []string{"write", "read"} {
		if !strings.Contains(restored, want) {
			t.Fatalf("restored rules %s are missing %q", restored, want)
		}
	}
}

// A normal session keeps "this session" in memory and "always" in the global
// store, so the isolated behaviour must not change it.
func TestPlainSessionsKeepTheGlobalPermissionStore(t *testing.T) {
	dir := t.TempDir()
	cfg := isolatedConfig(dir)
	cfg.Isolated = false
	cfg.SessionsDir = filepath.Join(dir, "sessions")
	sess := session.New(cfg)
	sess.AddUser("arranca el server")
	perm := permissions.NewManager(cfg)

	applyIsolatedPermissions(cfg, sess, perm)
	perm.AllowAll("Write")

	if _, err := os.Stat(cfg.PermFile); err != nil {
		t.Fatalf("a plain session must still save the global permission store: %v", err)
	}
	if got := sess.PermissionState(); len(got) != 0 {
		t.Fatalf("a plain session must not carry permission state: %s", got)
	}
}
