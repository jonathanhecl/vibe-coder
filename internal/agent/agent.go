package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
	"github.com/jonathanhecl/vibe-coder/internal/decision"
	gitx "github.com/jonathanhecl/vibe-coder/internal/git"
	"github.com/jonathanhecl/vibe-coder/internal/metrics"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
	"github.com/jonathanhecl/vibe-coder/internal/visionrole"
	"github.com/jonathanhecl/vibe-coder/internal/watcher"
)

const (
	MaxIterations    = 50
	MaxRetries       = 2
	MaxToolsPerReply = 5
)

// turnStop is how the provider ended one streamed reply: its stop reason and
// whether the stream was cut before the provider finished. It distinguishes a
// tool call truncated mid-JSON from one the model genuinely malformed.
type turnStop struct {
	Reason      string
	Interrupted bool
}

// truncated reports a reply the provider cut at its output limit.
func (t turnStop) truncated() bool {
	return strings.EqualFold(strings.TrimSpace(t.Reason), ollama.StopReasonLength)
}

type Agent struct {
	cfg    *config.Config
	client ollama.Client
	reg    *tools.Registry
	perm   *permissions.Manager
	sess   *session.Session
	ui     tui.UI

	mu          sync.RWMutex
	delegatedMu sync.Mutex
	planMode    bool
	reviewMode  bool
	watcher     *watcher.Watcher
	cp          *gitx.Checkpoint
	autoTest    *gitx.AutoTest
	// diagnostics is the language server consulted after every successful edit.
	// Nil (the default) disables the automatic diagnostics path.
	diagnostics diagnosticsSource
	rag         ragProvider
	paths       *pathMemory
	side        *sidecar.Pool
	// visionRole is the model explicitly bound to the vision role
	// (/setmodel vision). When set it describes images before the main
	// model and the sidecar are consulted.
	visionRole       *visionrole.Describer
	decider          decision.Decider
	currentGoal      string            // verbatim text of the user's request for this Run()
	resume           *resumeCheckpoint // durable continuation cursor; guarded by mu
	checklistPaused  bool              // pauses leftover pending TODOs from restored sessions or earlier turns so a new unrelated user message is never hijacked; guarded by mu
	runContextBlocks []string          // ephemeral RAG/memory blocks for the active Run(); guarded by mu
	ctxStore         *contextfiles.Store
	imgCache         *vision.Cache
	lastImage        string // newest user-attached image path; guarded by mu
	// mission is the agent-declared long-running goal. While active, the
	// runtime keeps starting turns until the agent completes or blocks it.
	mission *tools.MissionStore
	// notes is the per-session working-notes store behind the SessionNotes
	// tool. notesSessionID records which session owns the current notes so a
	// session switch (/new, resume, Clear) cannot leak them into the new one.
	notes          *tools.NoteStore
	notesSessionID string
	// turnToolCalls counts tool executions in the current Run so the mission
	// loop can detect a turn that did no work at all (a stall) without imposing
	// an arbitrary turn limit.
	turnToolCalls atomic.Int32
	// noProgress stops a run that keeps repeating the same tool call with
	// identical output, so an active mission cannot spin forever.
	noProgress noProgressGuard
	// inspection stops the broader stall where different read-only commands
	// keep inspecting state without taking any concrete action.
	inspection inspectionGuard
	// replyRepeat stops a turn that keeps restating the same visible answer
	// while the checklist does not move (the text-only version of the same
	// stall: no tool call to compare, so noProgressGuard never sees it).
	replyRepeat replyRepeatGuard
	// descCacheStore memoizes sidecar-generated image descriptions
	// ("borrowed vision") per file revision.
	descCacheStore *vision.Cache
	// metrics records one entry per model request (token usage, prompt
	// section sizes). It writes a JSONL file unless the session is
	// temporal or metrics are disabled; the in-memory summary always runs.
	metrics *metrics.Recorder
	// metricsSeq numbers the recorded entries within this agent.
	metricsSeq int
	// lastUsage holds the provider-reported token usage of the most recent
	// chat stream (nil until the first done chunk with usage arrives).
	lastUsage *ollama.Usage
	// lastStop records how the provider ended the reply that just streamed,
	// so a tool call whose arguments end mid-JSON is reported as truncation
	// instead of a malformed call.
	lastStop turnStop
	// wire tracks the send-time transcript window: the session generation it
	// was computed for, the masking frontier, and the sticky window start.
	wireMu sync.Mutex
	wire   wireState
	// reads tracks unchanged files whose Read results are still visible in the
	// current wire window so redundant re-reads short-circuit.
	reads readCache
	// compactedPending marks that a compaction rewrote the transcript since
	// the last recorded metrics entry, so the next entry carries
	// Entry.Compacted.
	compactedPending atomic.Bool

	// sysPrompt caches the stable system prompt until disk/registry inputs change.
	sysPrompt promptCache
}

// promptCache holds memoized system prompt fragments between turns.
type promptCache struct {
	mu           sync.Mutex
	stableKey    string
	stableBody   string
	cachePlan    bool
	cacheReview  bool
	cacheMission string
	cacheOffline bool
	full         string
	// schemaKey/schemaTokens memoize the native tool schema token estimate
	// per registered tool-name list (schema rarely changes between turns).
	schemaKey    string
	schemaTokens int
}

func IsEmptyAssistantResponseErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "empty assistant response")
}

// emptyAssistantResponseErr builds the give-up error for a model that answered
// with nothing. Every path that ends a run for this reason must use this
// wording: the caller detects it with IsEmptyAssistantResponseErr to offer a
// retry, and ending with a nil error instead is what made a dead turn look
// like a crash with no message.
func emptyAssistantResponseErr() error {
	return errors.New("empty assistant response: the model returned no visible text and no tool call (it may have spent its output budget on reasoning)")
}

type ragProvider interface {
	QueryText(ctx context.Context, query string, k int) (string, error)
}

func New(
	cfg *config.Config,
	client ollama.Client,
	reg *tools.Registry,
	perm *permissions.Manager,
	sess *session.Session,
	ui tui.UI,
) *Agent {
	a := &Agent{
		cfg:      cfg,
		client:   client,
		reg:      reg,
		perm:     perm,
		sess:     sess,
		ui:       ui,
		cp:       gitx.NewCheckpoint(cfg.Cwd),
		autoTest: gitx.NewAutoTest(cfg.Cwd),
		paths:    newPathMemory(cfg.Cwd),
		side:     sidecar.New(cfg, client),
		mission:  tools.NewMissionStore(),
		notes:    tools.NewNoteStore(),
	}
	if sess != nil {
		a.notesSessionID = sess.ID()
	}
	// Lazily resolved on each record: the session id can change (resume,
	// /new) and temporal/isolated state lives on cfg.
	a.metrics = metrics.NewRecorder(func() string { return a.MetricsPath() })
	a.decider = decision.FromConfig(cfg)
	// Mission tools were retired in favor of TodoWrite + /goal autonomous mode.
	if reg != nil {
		reg.Register(tools.NewSessionNotesTool(a.notes))
		// One shared output cap for every tool that honors it, derived from
		// the harness tuning knob (or 15% of the context window on auto).
		reg.SetOutputLimit(tools.OutputLimitBytes(cfg))
	}
	return a
}

// SetDecider overrides the decision model decider.
func (a *Agent) SetDecider(d decision.Decider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.decider = d
}

// Decider returns the current decision model decider.
func (a *Agent) Decider() decision.Decider {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.decider
}

// SetSidecar overrides the default sidecar pool. Tests use this to inject
// a fake; production code should leave the default in place.
func (a *Agent) SetSidecar(p *sidecar.Pool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.side = p
	if a.sess != nil && p != nil {
		a.sess.SetSidecarClient(p.ChatClient())
	}
}

// SetVisionRole installs the optional vision-role describer. A nil describer
// (the default) means no vision role is bound and image routing falls back to
// the main model and then the sidecar.
func (a *Agent) SetVisionRole(d *visionrole.Describer) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.visionRole = d
}

func (a *Agent) SetRAG(r ragProvider) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rag = r
}

func (a *Agent) SetWatcher(w *watcher.Watcher) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.watcher = w
}

// SetContextStore pins a context-file store whose contents are injected
// into the system prompt on every turn. The same store instance should be
// shared with the slash dispatcher so /context updates take effect
// immediately. A nil store disables pinned context.
func (a *Agent) SetContextStore(s *contextfiles.Store) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.ctxStore = s
}

func (a *Agent) getContextStore() *contextfiles.Store {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.ctxStore
}

func (a *Agent) getContextBlock() string {
	store := a.getContextStore()
	if store == nil {
		return ""
	}
	return store.RenderBlock()
}

func (a *Agent) contextFingerprint() string {
	store := a.getContextStore()
	if store == nil {
		return ""
	}
	return store.Fingerprint()
}

func (a *Agent) EnterPlanMode() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.planMode = true
}

func (a *Agent) ExitPlanMode() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.planMode = false
}

func (a *Agent) InPlanMode() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.planMode
}

// SetUI replaces the active terminal UI in-place (used by /ui hot-swap).
func (a *Agent) SetUI(ui tui.UI) {
	if a == nil || ui == nil {
		return
	}
	a.mu.Lock()
	a.ui = ui
	a.mu.Unlock()
}

// TodoCounts returns the number of completed and total meaningful TODO items.
func (a *Agent) TodoCounts() (done, total int) {
	if a == nil {
		return 0, 0
	}
	tw := a.todoWriteTool()
	if tw == nil {
		return 0, 0
	}
	for _, it := range tw.Store().Snapshot() {
		if !isMeaningfulTodoContent(it.Content) {
			continue
		}
		total++
		if it.Status == tools.TodoStatusCompleted {
			done++
		}
	}
	return done, total
}

func (a *Agent) EnterReviewMode() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reviewMode = true
}

func (a *Agent) ExitReviewMode() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.reviewMode = false
}

func (a *Agent) InReviewMode() bool {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.reviewMode
}

func (a *Agent) getWatcher() *watcher.Watcher {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.watcher
}

func (a *Agent) getRAG() ragProvider {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.rag
}

func (a *Agent) isOffline() bool {
	if a.perm != nil {
		return a.perm.OfflineMode()
	}
	return a.cfg != nil && a.cfg.Offline
}
