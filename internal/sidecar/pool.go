package sidecar

import (
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"golang.org/x/sync/singleflight"
)

// DefaultSummariseThreshold is the byte length above which a tool output is
// considered "too big" and worth condensing through the sidecar before it
// hits the main model's context. Picked empirically: roughly the size at
// which a Read of a real source file starts dominating context usage.
const DefaultSummariseThreshold = 6 * 1024

const (
	defaultMaxParallel = 2
	defaultCacheSize   = 64
)

// Pool is the agent-facing handle. Construct one per Agent via New.
//
// The zero value is **not** usable; always go through New so the cache and
// semaphore are initialised together.
type Pool struct {
	cfg    *config.Config
	client ollama.Client

	sem   chan struct{}
	sf    singleflight.Group
	cache *lruCache

	threshold int
}

// Option lets tests tune internals without exposing them on Config.
type Option func(*Pool)

// WithMaxParallel limits how many concurrent sidecar calls are in flight.
// Local Ollama is single-GPU and serialises by default; values >2 only help
// when the sidecar lives on a CPU-only or different host.
func WithMaxParallel(n int) Option {
	return func(p *Pool) {
		if n < 1 {
			n = 1
		}
		p.sem = make(chan struct{}, n)
	}
}

// WithSummariseThreshold overrides the byte threshold for
// SummariseToolOutput. Useful for tests that want to force the path.
func WithSummariseThreshold(n int) Option {
	return func(p *Pool) {
		if n < 0 {
			n = 0
		}
		p.threshold = n
	}
}

// WithCacheSize bounds the LRU cache. <=0 disables caching.
func WithCacheSize(n int) Option {
	return func(p *Pool) {
		p.cache = newLRU(n)
	}
}

// New builds a Pool. Safe to call with a nil ollama.Client or empty
// SidecarModel; in those cases Enabled() returns false and every method
// short-circuits.
func New(cfg *config.Config, client ollama.Client, opts ...Option) *Pool {
	p := &Pool{
		cfg:       cfg,
		client:    client,
		sem:       make(chan struct{}, defaultMaxParallel),
		cache:     newLRU(defaultCacheSize),
		threshold: DefaultSummariseThreshold,
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// Enabled reports whether the sidecar is wired and ready. Callers should
// check this first to avoid even computing the inputs when the user opted
// out of compaction/summarisation.
func (p *Pool) Enabled() bool {
	if p == nil || p.client == nil || p.cfg == nil {
		return false
	}
	return p.cfg.SidecarInUse()
}

// Threshold exposes the configured byte threshold so callers can decide
// whether to bother building inputs.
func (p *Pool) Threshold() int {
	if p == nil {
		return DefaultSummariseThreshold
	}
	return p.threshold
}
