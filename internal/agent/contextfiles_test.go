package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func newPinnedTestAgent(t *testing.T) (*Agent, *contextfiles.Store) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "m",
		ContextWindow: 8000,
		MaxTokens:     32,
		Temperature:   0.1,
		Cwd:           tmp,
		SessionsDir:   tmp,
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	ag := New(cfg, fakeClient{}, reg, perm, sess, &fakeUI{})
	store := contextfiles.NewStore()
	ag.SetContextStore(store)
	return ag, store
}

func TestPinnedContextAppearsInSystemPrompt(t *testing.T) {
	t.Parallel()
	ag, store := newPinnedTestAgent(t)
	guide := filepath.Join(t.TempDir(), "guide.md")
	if err := os.WriteFile(guide, []byte("Always follow the guide."), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Add(guide); err != nil {
		t.Fatal(err)
	}
	got := ag.buildSystemPrompt()
	if !strings.Contains(got, "Session Context") || !strings.Contains(got, "Always follow the guide.") {
		t.Fatalf("expected pinned block in system prompt, got:\n%s", got)
	}
	// Base prompt must still be present: pinning never replaces it.
	if !strings.Contains(got, "vibe-coder") {
		t.Fatalf("expected base prompt to survive pinning, got:\n%s", got)
	}
}

func TestPinnedContextSurvivesCompaction(t *testing.T) {
	t.Parallel()
	ag, store := newPinnedTestAgent(t)
	guide := filepath.Join(t.TempDir(), "guide.md")
	if err := os.WriteFile(guide, []byte("Never drop the guide."), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Add(guide); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 40; i++ {
		ag.sess.AddUser("filler message for compaction")
	}
	if err := ag.sess.Compact(context.Background(), true); err != nil {
		t.Fatalf("compact: %v", err)
	}
	got := ag.buildSystemPrompt()
	if !strings.Contains(got, "Never drop the guide.") {
		t.Fatalf("pinned context lost after compaction, got:\n%s", got)
	}
}

func TestPinnedContextInvalidatesCacheKey(t *testing.T) {
	t.Parallel()
	ag, store := newPinnedTestAgent(t)
	before := ag.buildSystemPrompt()
	guide := filepath.Join(t.TempDir(), "guide.md")
	if err := os.WriteFile(guide, []byte("Fresh instructions."), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Add(guide); err != nil {
		t.Fatal(err)
	}
	after := ag.buildSystemPrompt()
	if before == after || !strings.Contains(after, "Fresh instructions.") {
		t.Fatal("expected system prompt to refresh after pinning a file")
	}
}

func TestNoPinnedContextMeansNoBlock(t *testing.T) {
	t.Parallel()
	ag, _ := newPinnedTestAgent(t)
	if got := ag.buildSystemPrompt(); strings.Contains(got, "Session Context") {
		t.Fatalf("did not expect a session context block, got:\n%s", got)
	}
}
