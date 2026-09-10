package agent

import (
	"context"
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/contextfiles"
	gitx "github.com/jonathanhecl/vibe-coder/internal/git"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/sidecar"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
	"github.com/jonathanhecl/vibe-coder/internal/watcher"
)

const (
	MaxIterations = 50
	MaxRetries    = 2
)

type Agent struct {
	cfg    *config.Config
	client ollama.Client
	reg    *tools.Registry
	perm   *permissions.Manager
	sess   *session.Session
	ui     tui.UI

	mu          sync.RWMutex
	planMode    bool
	reviewMode  bool
	watcher     *watcher.Watcher
	cp          *gitx.Checkpoint
	autoTest    *gitx.AutoTest
	rag         ragProvider
	paths       *pathMemory
	side        *sidecar.Pool
	currentGoal string // verbatim text of the user's request for this Run()
	ctxStore    *contextfiles.Store
	imgCache    *vision.Cache
	// descCacheStore memoizes sidecar-generated image descriptions
	// ("borrowed vision") per file revision.
	descCacheStore *vision.Cache

	// sysPrompt caches the stable system prompt until disk/registry inputs change.
	sysPrompt promptCache
}

// promptCache holds memoized system prompt fragments between turns.
type promptCache struct {
	mu          sync.Mutex
	stableKey   string
	stableBody  string
	cacheGoal   string
	cachePlan   bool
	cacheReview bool
	full        string
}

func IsEmptyAssistantResponseErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "empty assistant response")
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
	return &Agent{
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
	}
}

// SetSidecar overrides the default sidecar pool. Tests use this to inject
// a fake; production code should leave the default in place.
func (a *Agent) SetSidecar(p *sidecar.Pool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.side = p
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
