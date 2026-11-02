package permissions

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// An isolated session carries its own permission state: the answers scoped to
// the session and the saved ones must survive a resume through the session file,
// not through the user's global store.
func TestSnapshotRestoreRoundTrip(t *testing.T) {
	m := NewManager(&config.Config{})
	m.AllowSession("Read")
	m.DenySession("WebFetch")
	m.AllowAll("Write")
	m.DenyAll("Bash")
	m.AllowOutDomainPersistent("/tmp/outside")

	raw := m.SnapshotRules()
	if len(raw) == 0 {
		t.Fatal("snapshot is empty with rules set")
	}
	for _, want := range []string{"read", "webfetch", "write", "bash", "/tmp/outside"} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("snapshot %s is missing %q", raw, want)
		}
	}

	restored := NewManager(&config.Config{})
	if err := restored.RestoreRules(raw); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	if got, want := string(restored.SnapshotRules()), string(raw); got != want {
		t.Fatalf("round trip changed the rules:\n got: %s\nwant: %s", got, want)
	}
}

// Restoring a corrupt blob must leave the manager untouched instead of wiping
// rules the user already has.
func TestRestoreRulesRejectsCorruptState(t *testing.T) {
	m := NewManager(&config.Config{})
	m.AllowSession("Read")
	before := string(m.SnapshotRules())

	if err := m.RestoreRules([]byte("not json")); err == nil {
		t.Fatal("expected an error for a corrupt blob")
	}
	if got := string(m.SnapshotRules()); got != before {
		t.Fatalf("corrupt restore changed the rules:\n got: %s\nwant: %s", got, before)
	}
}

// A saved "allow" for a tool that runs arbitrary commands is never restored: the
// same rule the persistent loader enforces.
func TestRestoreRulesDropsArbitraryCommandAllow(t *testing.T) {
	restored := NewManager(&config.Config{})
	if err := restored.RestoreRules([]byte(`{"saved":{"bash":"allow","read":"allow"}}`)); err != nil {
		t.Fatalf("restore failed: %v", err)
	}
	raw := string(restored.SnapshotRules())
	if strings.Contains(raw, `"bash"`) {
		t.Fatalf("an allow for a shell tool must not be restored: %s", raw)
	}
	if !strings.Contains(raw, `"read"`) {
		t.Fatalf("the safe allow was dropped: %s", raw)
	}
}
