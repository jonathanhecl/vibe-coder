package slash

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/session"
)

func seedSavedSession(t *testing.T, ctx *Ctx, text string) string {
	t.Helper()
	s := session.New(ctx.Cfg)
	s.AddUser(text)
	if err := s.Save(); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	return s.ID()
}

func sessionFileExists(cfgSessionsDir, id string) bool {
	_, err := os.Stat(filepath.Join(cfgSessionsDir, id+".jsonl"))
	return err == nil
}

func TestNewSavesAndStartsFresh(t *testing.T) {
	ctx, store, out := newContextTestCtx(t)
	guide := writeGuide(t, t.TempDir(), "guide.md", "Guide.")
	if _, _, err := Dispatch(ctx, "/context add "+guide); err != nil {
		t.Fatal(err)
	}
	ctx.Session.AddUser("hello")
	oldID := ctx.Session.ID()

	handled, shouldExit, err := Dispatch(ctx, "/new")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /new result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if ctx.Session.ID() == oldID {
		t.Fatal("expected a new session id after /new")
	}
	if ctx.Session.MessageCount() != 0 {
		t.Fatalf("expected empty transcript, got %d messages", ctx.Session.MessageCount())
	}
	if !sessionFileExists(ctx.Cfg.SessionsDir, oldID) {
		t.Fatal("expected the previous session to be archived on disk")
	}
	if store.Count() != 1 {
		t.Fatal("expected pinned contexts to survive /new")
	}
	if !strings.Contains(out.String(), "Session saved") {
		t.Fatalf("expected save confirmation, got %q", out.String())
	}
}

func TestClearBareShowsOptions(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	handled, shouldExit, err := Dispatch(ctx, "/clear")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /clear result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	for _, want := range []string{"/clear session", "/clear sessions", "/clear context", "/new"} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("expected options to contain %q, got %q", want, out.String())
		}
	}
}

func TestClearSessionDiscardsWithoutSaving(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	ctx.Session.AddUser("archived part")
	if err := ctx.Session.Save(); err != nil {
		t.Fatal(err)
	}
	oldID := ctx.Session.ID()
	ctx.Session.AddUser("unsaved part")

	handled, shouldExit, err := Dispatch(ctx, "/clear session")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /clear session result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if ctx.Session.ID() == oldID {
		t.Fatal("expected a new session id after /clear session")
	}
	if ctx.Session.MessageCount() != 0 {
		t.Fatalf("expected empty transcript, got %d", ctx.Session.MessageCount())
	}
	if sessionFileExists(ctx.Cfg.SessionsDir, oldID) {
		t.Fatal("expected the previous session file to be deleted, not archived")
	}
	if !strings.Contains(out.String(), "Discarded current session without saving") {
		t.Fatalf("expected discard confirmation, got %q", out.String())
	}
}

func TestClearSessionNeverSaved(t *testing.T) {
	ctx, _, _ := newContextTestCtx(t)
	oldID := ctx.Session.ID()
	if _, _, err := Dispatch(ctx, "/clear session"); err != nil {
		t.Fatalf("unsaved /clear session should not fail: %v", err)
	}
	if ctx.Session.ID() == oldID {
		t.Fatal("expected a new session id")
	}
}

func TestClearSessionsConfirmed(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	seedSavedSession(t, ctx, "keep me")
	seedSavedSession(t, ctx, "delete me")
	ctx.Prompter = &fakePrompter{answers: []string{"y"}}

	handled, shouldExit, err := Dispatch(ctx, "/clear sessions")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	infos, err := session.ListSessions(ctx.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 0 {
		t.Fatalf("expected zero sessions after confirmed clear, got %d", len(infos))
	}
	if !strings.Contains(out.String(), "Deleted") {
		t.Fatalf("expected deletion confirmation, got %q", out.String())
	}
}

func TestClearSessionsDenied(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	seedSavedSession(t, ctx, "keep me")
	ctx.Prompter = &fakePrompter{answers: []string{"n"}}

	if _, _, err := Dispatch(ctx, "/clear sessions"); err != nil {
		t.Fatal(err)
	}
	infos, err := session.ListSessions(ctx.Cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(infos) != 1 {
		t.Fatalf("expected session to survive denial, got %d", len(infos))
	}
	if !strings.Contains(out.String(), "Cancelled") {
		t.Fatalf("expected cancellation note, got %q", out.String())
	}
}

func TestClearSessionsNeedsFlagWithoutPrompter(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	seedSavedSession(t, ctx, "keep me")

	if _, _, err := Dispatch(ctx, "/clear sessions"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "--yes") {
		t.Fatalf("expected --yes guidance, got %q", out.String())
	}
	infos, _ := session.ListSessions(ctx.Cfg)
	if len(infos) != 1 {
		t.Fatalf("expected session to survive, got %d", len(infos))
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/clear sessions --yes"); err != nil {
		t.Fatal(err)
	}
	infos, _ = session.ListSessions(ctx.Cfg)
	if len(infos) != 0 {
		t.Fatalf("expected zero sessions after --yes, got %d", len(infos))
	}
}

func TestClearContextUnpins(t *testing.T) {
	ctx, store, out := newContextTestCtx(t)
	guide := writeGuide(t, t.TempDir(), "guide.md", "Guide.")
	if _, _, err := Dispatch(ctx, "/context add "+guide); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, _, err := Dispatch(ctx, "/clear context"); err != nil {
		t.Fatal(err)
	}
	if store.Has() {
		t.Fatal("expected pinned contexts to be dropped")
	}
	if !strings.Contains(out.String(), "Cleared all pinned context files") {
		t.Fatalf("expected clear confirmation, got %q", out.String())
	}
}

func TestClearUnknownTarget(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	if _, _, err := Dispatch(ctx, "/clear bogus"); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	if !strings.Contains(got, "Unknown clear target") || !strings.Contains(got, "/clear session") {
		t.Fatalf("expected unknown-target note plus options, got %q", got)
	}
}
