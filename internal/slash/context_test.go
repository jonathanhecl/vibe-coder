package slash

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

var errFakeInterrupt = errors.New("interrupted")

func newContextTestCtx(t *testing.T) (*Ctx, *contextfiles.Store, *bytes.Buffer) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "test-model",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	store := contextfiles.NewStore()
	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:      cfg,
		Session:  session.New(cfg),
		Agent:    &fakePlanAgent{},
		Out:      &out,
		Contexts: store,
	}
	return ctx, store, &out
}

func writeGuide(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// fakePrompter replays queued answers for GetInput.
type fakePrompter struct {
	answers []string
	err     error
	asked   []string
}

func (f *fakePrompter) GetInput(prompt string) (string, error) {
	f.asked = append(f.asked, prompt)
	if f.err != nil {
		return "", f.err
	}
	if len(f.answers) == 0 {
		return "", nil
	}
	answer := f.answers[0]
	f.answers = f.answers[1:]
	return answer, nil
}

func TestContextAddListDropClear(t *testing.T) {
	ctx, store, out := newContextTestCtx(t)
	guide := writeGuide(t, t.TempDir(), "guide.md", "Follow the guide always.")

	handled, _, err := Dispatch(ctx, "/context "+guide)
	if err != nil || !handled {
		t.Fatalf("add: handled=%t err=%v", handled, err)
	}
	if store.Count() != 1 {
		t.Fatalf("expected 1 pinned, got %d", store.Count())
	}

	out.Reset()
	if _, _, err := Dispatch(ctx, "/context list"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "guide.md") {
		t.Fatalf("expected list to mention guide.md, got %q", out.String())
	}

	// Bare form with an already-pinned file must ask instead of guessing.
	out.Reset()
	if _, _, err := Dispatch(ctx, "/context "+guide); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "add") || !strings.Contains(out.String(), "replace") {
		t.Fatalf("expected add/replace hint, got %q", out.String())
	}
	if store.Count() != 1 {
		t.Fatalf("bare re-pin must not duplicate, count=%d", store.Count())
	}

	second := writeGuide(t, t.TempDir(), "extra.txt", "Extra rules.")
	if _, _, err := Dispatch(ctx, "/context add "+second); err != nil {
		t.Fatal(err)
	}
	if store.Count() != 2 {
		t.Fatalf("expected 2 pinned after add, got %d", store.Count())
	}

	if _, _, err := Dispatch(ctx, "/context drop guide.md"); err != nil {
		t.Fatal(err)
	}
	if store.Count() != 1 {
		t.Fatalf("expected 1 pinned after drop, got %d", store.Count())
	}

	if _, _, err := Dispatch(ctx, "/context clear"); err != nil {
		t.Fatal(err)
	}
	if store.Has() {
		t.Fatal("expected empty store after clear")
	}
}

func TestContextReplaceResets(t *testing.T) {
	ctx, store, _ := newContextTestCtx(t)
	first := writeGuide(t, t.TempDir(), "first.md", "first")
	second := writeGuide(t, t.TempDir(), "second.md", "second")

	if _, _, err := Dispatch(ctx, "/context add "+first); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Dispatch(ctx, "/context replace "+second); err != nil {
		t.Fatal(err)
	}
	entries := store.List()
	if len(entries) != 1 || entries[0].Name != "second.md" {
		t.Fatalf("unexpected entries after replace: %+v", entries)
	}
}

func TestContextRejectsBadFiles(t *testing.T) {
	ctx, _, _ := newContextTestCtx(t)
	bad := writeGuide(t, t.TempDir(), "code.go", "package main")
	if _, _, err := Dispatch(ctx, "/context add "+bad); err == nil {
		t.Fatal("expected error for unsupported extension")
	}
	if _, _, err := Dispatch(ctx, "/context add /nonexistent/missing.md"); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestContextPinsPersistAcrossSaveResume(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	ctx.Session.AddUser("initial conversation message")
	guide := writeGuide(t, t.TempDir(), "guide.md", "Persistent guide.")
	if _, _, err := Dispatch(ctx, "/context add "+guide); err != nil {
		t.Fatal(err)
	}
	savedID := ctx.Session.ID()

	// A fresh session object loading the same id must recover the pins.
	fresh := session.New(ctx.Cfg)
	if err := fresh.Load(savedID); err != nil {
		t.Fatalf("load: %v", err)
	}
	if len(fresh.PinnedContexts()) != 1 {
		t.Fatalf("expected 1 persisted pin, got %v", fresh.PinnedContexts())
	}

	ctx.Session = fresh
	out.Reset()
	restorePinnedContexts(ctx)
	if ctx.Contexts.Count() == 0 {
		t.Fatal("expected restored store to be non-empty")
	}
	if got := out.String(); !strings.Contains(got, "Restored 1") {
		t.Fatalf("expected restore confirmation, got %q", got)
	}
}

func TestStatusShowsPinnedContexts(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	ctx.Cfg.VisionKnown = true
	ctx.Cfg.VisionAvailable = true
	guide := writeGuide(t, t.TempDir(), "guide.md", "Guide.")
	if _, _, err := Dispatch(ctx, "/context add "+guide); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, _, err := Dispatch(ctx, "/status"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "Pinned contexts") || !strings.Contains(out.String(), "guide.md") {
		t.Fatalf("expected status to list pinned file, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Vision: yes") {
		t.Fatalf("expected status to report vision, got %q", out.String())
	}
	if !strings.Contains(out.String(), "Tools: ") {
		t.Fatalf("expected status to report tools, got %q", out.String())
	}
}

func TestHelpMentionsContext(t *testing.T) {
	ctx, _, out := newContextTestCtx(t)
	if _, _, err := Dispatch(ctx, "/help"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "/context") {
		t.Fatalf("expected /help to mention /context, got %q", out.String())
	}
}

func TestBareContextAsksAppendOrReplace(t *testing.T) {
	cases := []struct {
		name      string
		answer    string
		wantCount int
		wantNames []string
		wantOut   string
	}{
		{"append word", "append", 2, []string{"first.md", "second.md"}, "Appended context: second.md"},
		{"append letter", "a", 2, []string{"first.md", "second.md"}, "Appended context: second.md"},
		{"append default on empty", "", 2, []string{"first.md", "second.md"}, "Appended context: second.md"},
		{"replace word", "replace", 1, []string{"second.md"}, "Replaced pinned context with 1 file(s)."},
		{"replace letter", "R", 1, []string{"second.md"}, "Replaced pinned context with 1 file(s)."},
		{"cancel", "cancel", 1, []string{"first.md"}, "Cancelled. Kept existing pinned contexts."},
		{"cancel letter", "c", 1, []string{"first.md"}, "Cancelled. Kept existing pinned contexts."},
		{"unknown keeps", "maybe", 1, []string{"first.md"}, "Unknown choice"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, store, out := newContextTestCtx(t)
			ctx.Prompter = &fakePrompter{answers: []string{tc.answer}}
			first := writeGuide(t, t.TempDir(), "first.md", "first")
			second := writeGuide(t, t.TempDir(), "second.md", "second")
			if _, _, err := Dispatch(ctx, "/context add "+first); err != nil {
				t.Fatal(err)
			}
			out.Reset()
			if _, _, err := Dispatch(ctx, "/context "+second); err != nil {
				t.Fatal(err)
			}
			if store.Count() != tc.wantCount {
				t.Fatalf("expected %d pinned, got %d", tc.wantCount, store.Count())
			}
			for _, want := range tc.wantNames {
				found := false
				for _, e := range store.List() {
					if e.Name == want {
						found = true
					}
				}
				if !found {
					t.Fatalf("expected %q pinned, got %+v", want, store.List())
				}
			}
			if !strings.Contains(out.String(), tc.wantOut) {
				t.Fatalf("expected output %q, got %q", tc.wantOut, out.String())
			}
			p, ok := ctx.Prompter.(*fakePrompter)
			if !ok || len(p.asked) != 1 {
				t.Fatalf("expected exactly one prompt, got %+v", ctx.Prompter)
			}
			if !strings.Contains(p.asked[0], "[A]ppend") || !strings.Contains(p.asked[0], "[R]eplace") {
				t.Fatalf("expected English Append/Replace prompt, got %q", p.asked[0])
			}
		})
	}
}

func TestBareContextWithoutPrompterPrintsGuidance(t *testing.T) {
	ctx, store, out := newContextTestCtx(t)
	first := writeGuide(t, t.TempDir(), "first.md", "first")
	second := writeGuide(t, t.TempDir(), "second.md", "second")
	if _, _, err := Dispatch(ctx, "/context add "+first); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, _, err := Dispatch(ctx, "/context "+second); err != nil {
		t.Fatal(err)
	}
	if store.Count() != 1 {
		t.Fatalf("guidance mode must not change pins, count=%d", store.Count())
	}
	got := out.String()
	if !strings.Contains(got, "/context add") || !strings.Contains(got, "/context replace") {
		t.Fatalf("expected add/replace guidance, got %q", got)
	}
}

func TestBareContextPrompterErrorCancels(t *testing.T) {
	ctx, store, out := newContextTestCtx(t)
	ctx.Prompter = &fakePrompter{err: errFakeInterrupt}
	first := writeGuide(t, t.TempDir(), "first.md", "first")
	second := writeGuide(t, t.TempDir(), "second.md", "second")
	if _, _, err := Dispatch(ctx, "/context add "+first); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	if _, _, err := Dispatch(ctx, "/context "+second); err != nil {
		t.Fatal(err)
	}
	if store.Count() != 1 {
		t.Fatalf("expected pins unchanged after prompt error, count=%d", store.Count())
	}
	if !strings.Contains(out.String(), "Cancelled") {
		t.Fatalf("expected cancellation note, got %q", out.String())
	}
}
