package slash

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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
	if !strings.Contains(out.String(), "vision: unknown") {
		t.Fatalf("expected /model output to report unknown vision without a Tags cache, got %q", out.String())
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
	if !strings.Contains(out.String(), "PATH") {
		t.Fatalf("expected PATH column header in /sessions output, got %q", out.String())
	}
	shortProj := shortenProjectPath(cfg.Cwd, 28)
	if !strings.Contains(out.String(), shortProj) {
		t.Fatalf("expected /sessions output to mention project path %s, got %q", shortProj, out.String())
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
		t.Fatalf("unexpected /resume with args result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Usage: /resume") {
		t.Fatalf("expected /resume with args to print usage, got %q", out.String())
	}
	if live.ID() != prevID {
		t.Fatalf("/resume with args must not swap sessions, got %s", live.ID())
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
	handled, shouldExit, err = Dispatch(ctx, "/session "+shortID)
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /session <prefix> result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != prevID {
		t.Fatalf("/session <prefix> should resolve to id %s, got %s", prevID, live.ID())
	}
}

func TestResumeLastLoadsMostRecent(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}

	oldSess := session.New(cfg)
	oldSess.AddUser("old session marker")
	if err := oldSess.Save(); err != nil {
		t.Fatalf("save old: %v", err)
	}
	newerSess := session.New(cfg)
	newerSess.AddUser("newer session marker")
	if err := newerSess.Save(); err != nil {
		t.Fatalf("save newer: %v", err)
	}
	// Force distinct modtimes regardless of filesystem granularity.
	now := time.Now()
	_ = os.Chtimes(filepath.Join(cfg.SessionsDir, oldSess.ID()+".jsonl"), now.Add(-2*time.Hour), now.Add(-2*time.Hour))
	_ = os.Chtimes(filepath.Join(cfg.SessionsDir, newerSess.ID()+".jsonl"), now.Add(-time.Hour), now.Add(-time.Hour))

	live := session.New(cfg)
	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:     cfg,
		Session: live,
		Agent:   &fakePlanAgent{},
		Out:     &out,
	}

	handled, shouldExit, err := Dispatch(ctx, "/session last")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /session last result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != newerSess.ID() {
		t.Fatalf("/session last should load most recent %s, got %s", newerSess.ID(), live.ID())
	}
	if !strings.Contains(out.String(), "Resumed session") {
		t.Fatalf("expected resume confirmation, got %q", out.String())
	}
	// Now current == newer (just re-saved, so newest on disk); "last" must
	// skip it and load the older one instead of reloading itself.
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/session last")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /session last result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != oldSess.ID() {
		t.Fatalf("/session last should skip current and load %s, got %s", oldSess.ID(),
			live.ID())
	}

	// Empty store reports no other sessions instead of erroring.
	emptyCfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "no-sessions-here"),
	}
	emptyLive := session.New(emptyCfg)
	var emptyOut bytes.Buffer
	emptyCtx := &Ctx{
		Cfg:     emptyCfg,
		Session: emptyLive,
		Agent:   &fakePlanAgent{},
		Out:     &emptyOut,
	}
	handled, shouldExit, err = Dispatch(emptyCtx, "/session last")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected empty /session last result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(emptyOut.String(), "No other saved sessions") {
		t.Fatalf("expected no-sessions message, got %q", emptyOut.String())
	}
}

func TestSessionIDAcceptsPasteBlockMarkers(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}

	seed := session.New(cfg)
	seed.AddUser("find me via pasted block id")
	if err := seed.Save(); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	seedID := seed.ID()

	live := session.New(cfg)
	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:     cfg,
		Session: live,
		Agent:   &fakePlanAgent{},
		Out:     &out,
	}

	// Pasting the id can arrive wrapped in the line editor's paste preview
	// markers; the lookup must unwrap them instead of failing.
	handled, shouldExit, err := Dispatch(ctx, "/session [block]"+seedID+"[/block]")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected pasted /session result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if live.ID() != seedID {
		t.Fatalf("pasted /session should load %s, got %s", seedID, live.ID())
	}

	if got := unwrapPasteBlock("plain-id"); got != "plain-id" {
		t.Fatalf("plain ids must pass through untouched, got %q", got)
	}
	if got := unwrapPasteBlock("[block][/block]"); got != "[block][/block]" {
		t.Fatalf("empty blocks must pass through untouched, got %q", got)
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
	for _, want := range []string{"Session", "Model", "Mode", "Git", "Misc", "/sessions", "/session <id>", "/resume", "/sessions delete --all", "/code", "/review", "ESC ESC"} {
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

func TestByeExitsAndIgnoresCase(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "llama3.2:3b",
		ContextWindow: 32768,
		Cwd:           tmp,
		SessionsDir:   tmp,
	}
	ctx := &Ctx{
		Cfg:     cfg,
		Session: session.New(cfg),
		Agent:   &fakePlanAgent{},
		Out:     &bytes.Buffer{},
	}

	for _, input := range []string{"bye", "Bye", "BYE", "  bye  ", "/bye"} {
		var out bytes.Buffer
		ctx.Out = &out
		ctx.Session.AddUser("hello")
		handled, shouldExit, err := Dispatch(ctx, input)
		if err != nil || !handled || !shouldExit {
			t.Fatalf("unexpected %q result: handled=%t exit=%t err=%v", input, handled, shouldExit, err)
		}
		if !strings.Contains(out.String(), "Session saved") {
			t.Fatalf("expected %q to save session, got %q", input, out.String())
		}
	}

	// An empty session without messages must NOT be saved on exit.
	emptyCtx := &Ctx{
		Cfg:     cfg,
		Session: session.New(cfg),
		Agent:   &fakePlanAgent{},
		Out:     &bytes.Buffer{},
	}
	handled, shouldExit, err := Dispatch(emptyCtx, "/exit")
	if err != nil || !handled || !shouldExit {
		t.Fatalf("unexpected empty exit result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if strings.Contains(emptyCtx.Out.(*bytes.Buffer).String(), "Session saved") {
		t.Fatalf("empty session should not be saved on exit, got %q", emptyCtx.Out.(*bytes.Buffer).String())
	}

	// "bye" embedded in a longer message must NOT be treated as exit.
	var out bytes.Buffer
	ctx.Out = &out
	handled, shouldExit, err = Dispatch(ctx, "bye please help me")
	if err != nil || handled || shouldExit {
		t.Fatalf("unexpected embedded bye result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
}

func TestShortenProjectPath(t *testing.T) {
	// Empty path returns "-"
	if got := shortenProjectPath("", 28); got != "-" {
		t.Fatalf("expected \"-\" for empty path, got %q", got)
	}
	if got := shortenProjectPath("   ", 28); got != "-" {
		t.Fatalf("expected \"-\" for whitespace path, got %q", got)
	}

	// Short path returns unchanged
	short := "/tmp/proj"
	if got := shortenProjectPath(short, 28); got != short {
		t.Fatalf("expected %q, got %q", short, got)
	}

	// Long path gets shortened and stays within maxLen
	long := "/mnt/cloud/Clouds/Github/vibe-coder"
	got := shortenProjectPath(long, 28)
	if len(got) > 28 {
		t.Fatalf("expected len <= 28, got %d (%q)", len(got), got)
	}
	if !strings.HasPrefix(got, "...") {
		t.Fatalf("expected prefix \"...\", got %q", got)
	}
	if !strings.HasSuffix(got, "vibe-coder") {
		t.Fatalf("expected suffix \"vibe-coder\", got %q", got)
	}

	// Single very long segment
	huge := "/verylongdirectorynamethatexceedstwentycightcharacters"
	gotHuge := shortenProjectPath(huge, 20)
	if len(gotHuge) > 20 {
		t.Fatalf("expected len <= 20, got %d (%q)", len(gotHuge), gotHuge)
	}
}

func TestDispatchTemporalSession(t *testing.T) {
	// 1. /exit in temporal mode discards session without saving to disk.
	tmp := t.TempDir()
	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{
		Cwd:         filepath.Join(tmp, "project"),
		SessionsDir: sessionsDir,
		Temporal:    true,
		ConfigDir:   tmp,
		ConfigFile:  filepath.Join(tmp, "vibe-coder.env"),
	}
	s := session.New(cfg)
	s.AddUser("hello world")
	s.AddAssistant("response")

	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:     cfg,
		Session: s,
		Agent:   &fakePlanAgent{},
		Out:     &out,
	}

	handled, shouldExit, err := Dispatch(ctx, "/exit")
	if err != nil || !handled || !shouldExit {
		t.Fatalf("unexpected exit result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Temporal session discarded.") {
		t.Fatalf("expected 'Temporal session discarded.', got %q", out.String())
	}
	files, err := os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no files written in temporal mode, found %d", len(files))
	}

	// 2. "bye" in temporal mode also discards without saving.
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "bye")
	if err != nil || !handled || !shouldExit {
		t.Fatalf("unexpected bye result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Temporal session discarded.") {
		t.Fatalf("expected 'Temporal session discarded.' for bye, got %q", out.String())
	}

	// 3. /new in temporal mode clears session and stays temporal.
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/new")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /new result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Started a new temporal session") {
		t.Fatalf("expected 'Started a new temporal session', got %q", out.String())
	}
	if s.MessageCount() != 0 {
		t.Fatalf("expected 0 messages after /new, got %d", s.MessageCount())
	}
	if !cfg.Temporal {
		t.Fatal("expected cfg.Temporal to remain true after /new")
	}

	// 4. /save persists settings but does NOT promote the temporal session.
	s.AddUser("durable message")
	s.AddAssistant("durable reply")
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/save")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /save result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Temporal session not saved") {
		t.Fatalf("expected /save to warn about the temporal session, got %q", out.String())
	}
	if !cfg.Temporal {
		t.Fatal("expected cfg.Temporal to stay true after /save")
	}
	files, err = os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) != 0 {
		t.Fatalf("expected no session files after /save in temporal mode, found %d", len(files))
	}

	// 5. /promote keeps the temporal session: promotes it and saves to disk.
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/promote")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /promote result: handled=%t exit=%t err=%v", handled, shouldExit, err)
	}
	if !strings.Contains(out.String(), "Temporal session promoted to permanent and saved") {
		t.Fatalf("expected 'Temporal session promoted to permanent and saved', got %q", out.String())
	}
	if cfg.Temporal {
		t.Fatal("expected cfg.Temporal to be false after promotion")
	}
	files, err = os.ReadDir(sessionsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("expected session files to be written after /promote")
	}
}

func TestDispatchIsolatedSession(t *testing.T) {
	tmp := t.TempDir()
	projectDir := filepath.Join(tmp, "project")
	sessionsDir := filepath.Join(tmp, "sessions")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(sessionsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	cfg := &config.Config{
		Cwd:         projectDir,
		SessionsDir: sessionsDir,
		Isolated:    true,
		ConfigDir:   tmp,
		ConfigFile:  filepath.Join(tmp, "vibe-coder.env"),
	}
	s := session.New(cfg)
	s.AddUser("isolated prompt")
	s.AddAssistant("isolated answer")

	var out bytes.Buffer
	ctx := &Ctx{
		Cfg:     cfg,
		Session: s,
		Agent:   &fakePlanAgent{},
		Out:     &out,
	}

	// 1. /save saves to .vibe-isolated.jsonl and announces saved isolated session
	handled, shouldExit, err := Dispatch(ctx, "/save")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /save result: %v", err)
	}
	if !strings.Contains(out.String(), "Saved isolated session") {
		t.Fatalf("expected 'Saved isolated session', got %q", out.String())
	}
	if !session.HasIsolatedSession(projectDir) {
		t.Fatal("expected .vibe-isolated.jsonl to exist after /save")
	}

	// 2. /new clears the in-memory session and truncates .vibe-isolated.jsonl
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/new")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /new result: %v", err)
	}
	if !strings.Contains(out.String(), "Started a new isolated session") {
		t.Fatalf("expected 'Started a new isolated session', got %q", out.String())
	}
	if s.MessageCount() != 0 {
		t.Fatalf("expected 0 messages after /new, got %d", s.MessageCount())
	}
	info, err := os.Stat(filepath.Join(projectDir, session.IsolatedSessionFileName))
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != 0 {
		t.Fatalf("expected file to be truncated to 0 bytes, got %d", info.Size())
	}

	// 3. /sessions lists notice that isolated session is active in current directory
	s.AddUser("post-new message")
	s.AddAssistant("post-new reply")
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	out.Reset()
	handled, shouldExit, err = Dispatch(ctx, "/sessions")
	if err != nil || !handled || shouldExit {
		t.Fatalf("unexpected /sessions result: %v", err)
	}
	if !strings.Contains(out.String(), "Isolated session active in") {
		t.Fatalf("expected 'Isolated session active in', got %q", out.String())
	}
}
