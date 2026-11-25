package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// fakeOptionAsker answers AskOptions with a canned selection and records the
// card it was shown.
type fakeOptionAsker struct {
	card   tui.AskCard
	answer string
}

func (f *fakeOptionAsker) AskOptions(card tui.AskCard) (string, error) {
	f.card = card
	return f.answer, nil
}

// newStaleReviewAgent builds an agent whose session carries a legacy
// (version 2, timestamp-less) pending checklist plus one user prompt, so the
// review prompt has real state to resolve and sess.Save has somewhere to go.
// The work state is saved to disk up front, including the legacy .work.json
// sidecar, so decisions can be verified after a real reload.
func newStaleReviewAgent(t *testing.T) (*agent.Agent, *session.Session, *config.Config) {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:       "fake:latest",
		Cwd:         tmp,
		SessionsDir: filepath.Join(tmp, "sessions"),
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true, Cwd: tmp})
	ag := agent.New(cfg, emptyRetryClient{}, reg, perm, sess, nil)
	// Mirror a resumed session: the file carries the work-state blob and the
	// agent restores its live stores from it.
	raw := []byte(`{"version":2,"todos":[{"id":"1","content":"finish the report","status":"pending"}],"resume":{"goal":"finish the report","next_action":"finish the report"}}`)
	sess.SetWorkState(raw)
	ag.RestoreWorkState(raw)
	sess.AddUser("keep transcript")
	if err := sess.Save(); err != nil {
		t.Fatalf("seed session save: %v", err)
	}
	return ag, sess, cfg
}

func TestReviewStaleChecklistCardOffersLaterContinueDiscard(t *testing.T) {
	ag, sess, _ := newStaleReviewAgent(t)
	now := time.Now()
	asker := &fakeOptionAsker{answer: "1"}

	approved, err := reviewStaleChecklist(ag, sess, asker, func() time.Time { return now })
	if err != nil || approved {
		t.Fatalf("Later review = (%v, %v), want (false, nil)", approved, err)
	}
	want := []string{"Later", "Continue", "Discard"}
	if len(asker.card.Options) != len(want) {
		t.Fatalf("options = %v, want %v", asker.card.Options, want)
	}
	for i, label := range want {
		if asker.card.Options[i].Label != label {
			t.Fatalf("option %d = %q, want %q", i, asker.card.Options[i].Label, label)
		}
	}
	if asker.card.Title != "Stale checklist" {
		t.Fatalf("card title = %q", asker.card.Title)
	}
	if strings.Contains(asker.card.Prompt, "Checklist needs review") {
		t.Fatalf("card prompt must not carry the Enter-to-continue footer: %q", asker.card.Prompt)
	}
	if !ag.StaleChecklist(now) {
		t.Fatal("Later must leave the checklist stale")
	}
	if len(sess.WorkState()) == 0 {
		t.Fatal("Later must keep the persisted checklist")
	}
}

func TestReviewStaleChecklistContinueApprovesAndPersists(t *testing.T) {
	ag, sess, _ := newStaleReviewAgent(t)
	now := time.Now()
	asker := &fakeOptionAsker{answer: "2"}

	approved, err := reviewStaleChecklist(ag, sess, asker, func() time.Time { return now })
	if err != nil || !approved {
		t.Fatalf("Continue review = (%v, %v), want (true, nil)", approved, err)
	}
	if ag.StaleChecklist(now) {
		t.Fatal("an approved checklist must not stay stale")
	}
	if !ag.CanResume() {
		t.Fatal("continue must keep the resume point")
	}
	raw := sess.WorkState()
	if len(raw) == 0 || !strings.Contains(string(raw), "todos_updated_at") {
		t.Fatalf("approved work state missing todos_updated_at: %s", raw)
	}
}

func TestReviewStaleChecklistDiscardClearsWorkState(t *testing.T) {
	ag, sess, cfg := newStaleReviewAgent(t)
	now := time.Now()
	asker := &fakeOptionAsker{answer: "discard"}

	approved, err := reviewStaleChecklist(ag, sess, asker, func() time.Time { return now })
	if err != nil || approved {
		t.Fatalf("Discard review = (%v, %v), want (false, nil)", approved, err)
	}
	if len(sess.WorkState()) != 0 {
		t.Fatalf("discard must empty the persisted work state, got %s", sess.WorkState())
	}
	if ag.StaleChecklist(now) || ag.CanResume() {
		t.Fatal("discard must clear the checklist and its resume point")
	}
	if got := ag.PendingTodoCount(); got != 0 {
		t.Fatalf("pending todos after discard = %d, want 0", got)
	}

	// Verify the decision survived on disk: a fresh load of the same session
	// file must show no work state (embedded meta and legacy sidecar alike)
	// while the transcript keeps the user's prompt.
	reloaded := session.New(cfg)
	if err := reloaded.Load(sess.ID()); err != nil {
		t.Fatalf("reload session: %v", err)
	}
	if len(reloaded.WorkState()) != 0 {
		t.Fatalf("reloaded work state = %s, want empty after discard", reloaded.WorkState())
	}
	if got := reloaded.MessageCount(); got != 1 {
		t.Fatalf("reloaded transcript messages = %d, want 1", got)
	}
	if msgs := reloaded.MessagesReadOnly(); len(msgs) != 1 || msgs[0].Content != "keep transcript" {
		t.Fatalf("reloaded transcript = %+v, want the user prompt kept", msgs)
	}
}
