package slash

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
)

type fakePlanAgent struct {
	plan   bool
	review bool
}

func (f *fakePlanAgent) EnterPlanMode() { f.plan = true }
func (f *fakePlanAgent) ExitPlanMode()  { f.plan = false }
func (f *fakePlanAgent) InPlanMode() bool {
	return f.plan
}
func (f *fakePlanAgent) EnterReviewMode() { f.review = true }
func (f *fakePlanAgent) ExitReviewMode()  { f.review = false }
func (f *fakePlanAgent) InReviewMode() bool {
	return f.review
}

func TestDispatchMinimumCommands(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   tmp,
	}
	s := session.New(cfg)

	var out bytes.Buffer
	planAgent := &fakePlanAgent{}
	ctx := &Ctx{
		Cfg:     cfg,
		Session: s,
		Agent:   planAgent,
		Out:     &out,
	}

	handled, shouldExit, err := Dispatch(ctx, "/yes")
	if err != nil || !handled || shouldExit || !cfg.YesMode {
		t.Fatalf("unexpected /yes result: handled=%t exit=%t err=%v yes=%t", handled, shouldExit, err, cfg.YesMode)
	}

	handled, shouldExit, err = Dispatch(ctx, "/no")
	if err != nil || !handled || shouldExit || cfg.YesMode {
		t.Fatalf("unexpected /no result: handled=%t exit=%t err=%v yes=%t", handled, shouldExit, err, cfg.YesMode)
	}

	s.AddUser("hello")
	handled, shouldExit, err = Dispatch(ctx, "/status")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /status result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Model:") {
		t.Fatalf("expected status output, got %q", out.String())
	}

	handled, shouldExit, err = Dispatch(ctx, "/plan")
	if err != nil || !handled || shouldExit || !planAgent.InPlanMode() {
		t.Fatalf("unexpected /plan result: handled=%t exit=%t err=%v plan=%t", handled, shouldExit, err, planAgent.InPlanMode())
	}

	handled, shouldExit, err = Dispatch(ctx, "/code")
	if err != nil || !handled || shouldExit || planAgent.InPlanMode() {
		t.Fatalf("unexpected /code result: handled=%t exit=%t err=%v plan=%t", handled, shouldExit, err, planAgent.InPlanMode())
	}

	handled, shouldExit, err = Dispatch(ctx, "/plan")
	if err != nil || !handled || shouldExit || !planAgent.InPlanMode() {
		t.Fatalf("unexpected second /plan result: handled=%t exit=%t err=%v plan=%t", handled, shouldExit, err, planAgent.InPlanMode())
	}

	handled, shouldExit, err = Dispatch(ctx, "/approve")
	if err != nil || !handled || shouldExit || planAgent.InPlanMode() {
		t.Fatalf("unexpected /approve result: handled=%t exit=%t err=%v plan=%t", handled, shouldExit, err, planAgent.InPlanMode())
	}

	handled, shouldExit, err = Dispatch(ctx, "/model")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /model inspect result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}

	handled, shouldExit, err = Dispatch(ctx, "/model qwen3.5:9b")
	if err != nil || !handled || shouldExit || cfg.Model != "qwen3.5:9b" {
		t.Fatalf("unexpected /model set result: handled=%t exit=%t err=%v model=%s", handled, shouldExit, err, cfg.Model)
	}

	handled, shouldExit, err = Dispatch(ctx, "/hide-think")
	if err != nil || !handled || shouldExit || !cfg.OllamaHideThink {
		t.Fatalf("unexpected /hide-think result: handled=%t exit=%t err=%v hide=%t", handled, shouldExit, err, cfg.OllamaHideThink)
	}

	handled, shouldExit, err = Dispatch(ctx, "/show-think")
	if err != nil || !handled || shouldExit || cfg.OllamaHideThink {
		t.Fatalf("unexpected /show-think result: handled=%t exit=%t err=%v hide=%t", handled, shouldExit, err, cfg.OllamaHideThink)
	}

	handled, shouldExit, err = Dispatch(ctx, "/tokens")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /tokens result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}

	for i := 0; i < 40; i++ {
		s.AddUser("x")
	}
	handled, shouldExit, err = Dispatch(ctx, "/compact")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /compact result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}

	handled, shouldExit, err = Dispatch(ctx, "/commit")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /commit result outside repo: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
}

func TestSessionsAndResumeCommands(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}

	// Pre-create a saved session for the same project so /resume (no args)
	// finds it via project-index.json.
	prev := session.New(cfg)
	prev.AddUser("look for me on resume")
	prev.AddAssistant("ok")
	if err := prev.Save(); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	prevID := prev.ID()

	// Live session starts empty; /resume should not save the empty
	// transcript and should swap to prevID.
	live := session.New(cfg)
	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:     cfg,
		Session: live,
		Agent:   &fakePlanAgent{},
		Out:     &out,
	}

	handled, shouldExit, err := Dispatch(ctx, "/sessions")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /sessions result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	idPrefix := prevID
	if len(idPrefix) > 16 {
		idPrefix = idPrefix[:16]
	}
	if !strings.Contains(out.String(), idPrefix) {
		t.Fatalf("expected /sessions output to mention seeded id prefix %s, got %q", idPrefix, out.String())
	}
	if !strings.Contains(out.String(), "*") {
		t.Fatalf("expected current-project marker (*) in /sessions output, got %q", out.String())
	}

	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/resume")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /resume result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != prevID {
		t.Fatalf("expected /resume to swap to %s, got %s", prevID, live.ID())
	}
	if live.MessageCount() != 2 {
		t.Fatalf("expected 2 loaded messages after /resume, got %d", live.MessageCount())
	}
	if !strings.Contains(out.String(), "Last assistant response") {
		t.Fatalf("expected /resume output to include last assistant response, got %q", out.String())
	}
	if !strings.Contains(out.String(), "ok") {
		t.Fatalf("expected /resume output to include the last assistant text, got %q", out.String())
	}
	if strings.Contains(out.String(), "Conversation summary") {
		t.Fatalf("did not expect /resume output to include conversation summary, got %q", out.String())
	}

	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/resume "+prevID)
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /resume <id> result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != prevID {
		t.Fatalf("/resume <id> should keep id %s, got %s", prevID, live.ID())
	}

	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/session "+prevID)
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /session <id> alias result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != prevID {
		t.Fatalf("/session <id> should resume id %s, got %s", prevID, live.ID())
	}

	out.Reset()
	shortID := prevID[:16]
	handled, shouldExit, err = Dispatch(ctx, "/resume "+shortID)
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /resume <prefix> result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != prevID {
		t.Fatalf("/resume <prefix> should resolve to id %s, got %s", prevID, live.ID())
	}
}

func TestSessionsDeleteSpecificAndAll(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}

	keep := session.New(cfg)
	keep.AddUser("keep me")
	if err := keep.Save(); err != nil {
		t.Fatalf("save keep: %v", err)
	}
	doomed := session.New(cfg)
	doomed.AddUser("delete me")
	if err := doomed.Save(); err != nil {
		t.Fatalf("save doomed: %v", err)
	}
	doomedID := doomed.ID()

	live := session.New(cfg)
	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:     cfg,
		Session: live,
		Agent:   &fakePlanAgent{},
		Out:     &out,
	}

	handled, shouldExit, err := Dispatch(ctx, "/sessions delete "+doomedID)
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /sessions delete result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Deleted") {
		t.Fatalf("expected 'Deleted' confirmation, got %q", out.String())
	}

	infos, err := session.ListSessions(cfg)
	if err != nil {
		t.Fatalf("list after delete: %v", err)
	}
	if len(infos) != 1 || infos[0].ID != keep.ID() {
		t.Fatalf("expected only %s left, got %#v", keep.ID(), infos)
	}

	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/sessions delete --all")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /sessions delete --all result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	infos, err = session.ListSessions(cfg)
	if err != nil {
		t.Fatalf("list after delete --all: %v", err)
	}
	if len(infos) != 0 {
		t.Fatalf("expected zero sessions after delete --all, got %d", len(infos))
	}
}

func TestHelpListsGroupedCommands(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:     cfg,
		Session: session.New(cfg),
		Agent:   &fakePlanAgent{},
		Out:     &out,
	}
	handled, shouldExit, err := Dispatch(ctx, "/help")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /help result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	got := out.String()
	for _, want := range []string{"Session", "Model", "Mode", "Git", "Misc", "/sessions", "/session <id>", "/resume", "/sessions delete --all", "/code", "/review"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected /help output to contain %q, got %q", want, got)
		}
	}
}

func TestTrimForDisplayPreservesFormatting(t *testing.T) {
	raw := "  **Titulo**\r\n\r\n1) linea uno\r\n2) linea dos  "
	got := trimForDisplay(raw, 500)
	if !strings.Contains(got, "**Titulo**\n\n1) linea uno\n2) linea dos") {
		t.Fatalf("expected formatting/newlines preserved, got %q", got)
	}
}

func TestReviewCommand(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	var out bytes.Buffer
	agent := &fakePlanAgent{}
	ctx := &Ctx{
		Cfg:     cfg,
		Session: session.New(cfg),
		Agent:   agent,
		Out:     &out,
	}

	// /review with a prompt should enter review mode
	handled, shouldExit, err := Dispatch(ctx, "/review explain this code")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /review result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !agent.InReviewMode() {
		t.Fatal("expected review mode to be enabled after /review <prompt>")
	}
	if !strings.Contains(out.String(), "Review mode enabled") {
		t.Fatalf("expected output to mention review mode, got %q", out.String())
	}

	// /review without a prompt should print usage and not enter review mode
	out.Reset()
	agent.review = false
	handled, shouldExit, err = Dispatch(ctx, "/review")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /review (no args) result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if agent.InReviewMode() {
		t.Fatal("expected review mode to stay off when no prompt is given")
	}
	if !strings.Contains(out.String(), "Usage:") {
		t.Fatalf("expected usage message, got %q", out.String())
	}
}
