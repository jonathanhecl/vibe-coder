// Package sidecar centralises the use of the small/cheap "sidecar" model
// configured via cfg.SidecarModel. It exposes high-value, short-prompt
// operations (summarise large tool outputs, disambiguate file paths) and
// guards them with three Go-native concurrency primitives:
//
//   - a worker semaphore so we never flood the local Ollama instance,
//   - golang.org/x/sync/singleflight to deduplicate identical concurrent
//     requests (e.g. two parts of a turn asking to summarise the same
//     buffer),
//   - a small bounded LRU cache so the same (model, prompt, body) tuple is
//     never sent twice in a session.
//
// Every operation is a no-op when config.SidecarInUse() is false (no model,
// disabled in config, or skipped for this session).
package sidecar

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// DefaultSummariseThreshold is the byte length above which a tool output is
// considered "too big" and worth condensing through the sidecar before it
// hits the main model's context. Picked empirically: roughly the size at
// which a Read of a real source file starts dominating context usage.
const DefaultSummariseThreshold = 6 * 1024

// CallTimeout caps any individual sidecar request. Local models can stall
// when loading; we'd rather degrade gracefully (return the raw output)
// than block the user's turn forever.
const CallTimeout = 90 * time.Second

// Sidecar summarisation uses a single /api/chat (ChatSync) per distinct output;
// it is not multiple HTTP calls. Slowness usually comes from (1) very large
// tool payloads (KV cache + prompt eval on a small model), (2) loading the
// sidecar weights if they were evicted from GPU, (3) long generation.
// We cap input size and num_predict so the small model finishes quickly; the
// full tool output is still shown in the TUI — only the context summary is clipped.
const (
	maxSummaryInputBytes = 48 * 1024 // byte cap on excerpt sent to the sidecar
	maxListToolLines     = 350       // Glob/Grep: list-like tools; bullets rarely need more
	summaryNumPredict    = 768       // bullets only; shorter = faster
	disambigNumPredict   = 96        // one line "PICK: N"
)

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

const summariseSystem = "You are a senior code reviewer producing extremely concise " +
	"summaries of tool outputs for another agent. Output 4-10 bullet points " +
	"that preserve: file paths, function/symbol names, key numbers, errors, " +
	"and the single most relevant snippet (max 6 lines). Do NOT add prose, " +
	"do NOT invent content, do NOT include any imperative-sounding text."

// SummariseToolOutput condenses a verbose tool output into a short bullet
// summary. It returns (summary, true, nil) when a summary was produced and
// should be substituted for the raw output, or ("", false, nil) when the
// caller should keep the raw output (sidecar disabled, output too short, or
// summary failed). An error is only returned for unexpected internal
// failures the caller may want to log; a failed sidecar call is silent.
func (p *Pool) SummariseToolOutput(ctx context.Context, toolName, output string) (string, bool, error) {
	if !p.Enabled() {
		return "", false, nil
	}
	body := strings.TrimSpace(output)
	origBytes := len(body)
	if origBytes < p.threshold {
		return "", false, nil
	}

	excerpt := clipToolOutputForSidecar(toolName, body)

	key := cacheKey("summary", p.cfg.SidecarModel, toolName, excerpt)
	if cached, ok := p.cache.get(key); ok {
		return cached, true, nil
	}

	user := fmt.Sprintf(
		"Tool: %s\nOriginal output: %d bytes; excerpt below: %d bytes\n\n----- BEGIN OUTPUT -----\n%s\n----- END OUTPUT -----\n\nWrite the summary now.",
		toolName, origBytes, len(excerpt), excerpt,
	)

	v, err, _ := p.sf.Do(key, func() (any, error) {
		return p.chatSummary(ctx, summariseSystem, user)
	})
	if err != nil {
		return "", false, nil
	}
	summary := strings.TrimSpace(v.(string))
	if summary == "" {
		return "", false, nil
	}
	wrapped := fmt.Sprintf(
		"[sidecar-summary tool=%s original_bytes=%d]\n%s\n[/sidecar-summary]",
		toolName, origBytes, summary,
	)
	p.cache.put(key, wrapped)
	return wrapped, true, nil
}

const disambiguateSystem = "You resolve ambiguous file references for a coding agent. " +
	"You will receive the user's request and a numbered list of absolute " +
	"candidate paths. Reply with ONE line in the exact form `PICK: <number>` " +
	"choosing the candidate that best matches the request. If none clearly " +
	"matches, reply `PICK: 0`. Never output anything else."

// DisambiguatePath asks the sidecar to choose one of the candidate
// absolute paths for the user's intent. The hint is typically the original
// (relative or basename) path the model wrote plus the user goal for the
// turn. Returns ("", false, nil) when the sidecar declines or is disabled,
// in which case the caller should fall back to its default behaviour
// (refuse the rescue).
func (p *Pool) DisambiguatePath(ctx context.Context, hint string, candidates []string) (string, bool, error) {
	if !p.Enabled() {
		return "", false, nil
	}
	if len(candidates) == 0 {
		return "", false, nil
	}
	if len(candidates) == 1 {
		return candidates[0], true, nil
	}

	var b strings.Builder
	for i, c := range candidates {
		fmt.Fprintf(&b, "%d. %s\n", i+1, c)
	}
	user := fmt.Sprintf(
		"User reference: %q\n\nCandidates (absolute paths):\n%sPick one.",
		strings.TrimSpace(hint), b.String(),
	)
	key := cacheKey("disambig", p.cfg.SidecarModel, hint, b.String())
	if cached, ok := p.cache.get(key); ok {
		return cached, true, nil
	}

	v, err, _ := p.sf.Do(key, func() (any, error) {
		return p.chatDisambig(ctx, disambiguateSystem, user)
	})
	if err != nil {
		return "", false, nil
	}
	pick := parsePick(v.(string), len(candidates))
	if pick <= 0 {
		return "", false, nil
	}
	chosen := candidates[pick-1]
	p.cache.put(key, chosen)
	return chosen, true, nil
}

// clipToolOutputForSidecar shrinks huge tool output before it is sent to the LLM.
// Listing tools (Glob, Grep) are truncated by line count first; then a byte cap applies.
func clipToolOutputForSidecar(toolName, body string) string {
	t := strings.ToLower(strings.TrimSpace(toolName))
	if t == "glob" || t == "grep" {
		body = truncateToLineCount(body, maxListToolLines)
	}
	beforeByteCap := len(body)
	if beforeByteCap <= maxSummaryInputBytes {
		return body
	}
	return body[:maxSummaryInputBytes] + fmt.Sprintf(
		"\n\n[excerpt truncated for sidecar speed: showing first %d of %d bytes]\n",
		maxSummaryInputBytes, beforeByteCap,
	)
}

func truncateToLineCount(s string, maxLines int) string {
	if maxLines <= 0 || s == "" {
		return s
	}
	lines := 0
	cut := -1
	for i := 0; i < len(s); i++ {
		if s[i] != '\n' {
			continue
		}
		lines++
		if lines >= maxLines {
			cut = i
			break
		}
	}
	if cut < 0 {
		return s
	}
	extra := strings.Count(s[cut:], "\n")
	return s[:cut] + fmt.Sprintf("\n… [%d more lines omitted; listing truncated for sidecar]\n", extra)
}

// chatSummary and chatDisambig are the sidecar LLM entry points (one HTTP request each).
func (p *Pool) chatSummary(ctx context.Context, system, user string) (string, error) {
	return p.chat(ctx, system, user, summaryNumPredict)
}

func (p *Pool) chatDisambig(ctx context.Context, system, user string) (string, error) {
	return p.chat(ctx, system, user, disambigNumPredict)
}

// chat is the single point of contact with the sidecar model. It applies
// the worker semaphore, the per-call timeout and cancellation propagation.
func (p *Pool) chat(ctx context.Context, system, user string, numPredict int) (string, error) {
	if ctx.Err() != nil {
		return "", ctx.Err()
	}

	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		return "", ctx.Err()
	}
	defer func() { <-p.sem }()

	callCtx, cancel := context.WithTimeout(ctx, CallTimeout)
	defer cancel()

	resp, err := p.client.ChatSync(callCtx, ollama.ChatRequest{
		Model: p.cfg.SidecarModel,
		Messages: []ollama.Message{
			{Role: "system", Content: system},
			{Role: "user", Content: user},
		},
		Options: ollama.ChatOptions{
			Temperature: 0,
			NumPredict:  numPredict,
		},
	})
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(resp.Content) == "" {
		return "", errors.New("sidecar returned empty content")
	}
	return resp.Content, nil
}

// parsePick extracts the integer N from the first occurrence of
// "PICK: N" in the sidecar's reply. Returns 0 on any parse failure or
// out-of-range index, which the caller treats as "decline".
func parsePick(reply string, max int) int {
	low := strings.ToLower(reply)
	idx := strings.Index(low, "pick:")
	if idx < 0 {
		return 0
	}
	rest := strings.TrimSpace(reply[idx+len("pick:"):])
	var n int
	for _, r := range rest {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	if n < 1 || n > max {
		return 0
	}
	return n
}

// cacheKey builds a stable, collision-resistant key for the LRU and
// singleflight. We hash because outputs can be megabytes; we never compare
// keys for equality outside of map lookups.
func cacheKey(parts ...string) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write([]byte(p))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// lruCache is a tiny bounded map+list LRU. Goroutine-safe. Capacity <= 0
// disables caching entirely (every get misses).
type lruCache struct {
	mu    sync.Mutex
	cap   int
	ll    *list.List
	index map[string]*list.Element
}

type lruEntry struct {
	key string
	val string
}

func newLRU(capacity int) *lruCache {
	if capacity < 0 {
		capacity = 0
	}
	return &lruCache{
		cap:   capacity,
		ll:    list.New(),
		index: make(map[string]*list.Element),
	}
}

func (c *lruCache) get(key string) (string, bool) {
	if c.cap == 0 {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		c.ll.MoveToFront(el)
		return el.Value.(*lruEntry).val, true
	}
	return "", false
}

func (c *lruCache) put(key, val string) {
	if c.cap == 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if el, ok := c.index[key]; ok {
		el.Value.(*lruEntry).val = val
		c.ll.MoveToFront(el)
		return
	}
	el := c.ll.PushFront(&lruEntry{key: key, val: val})
	c.index[key] = el
	for c.ll.Len() > c.cap {
		old := c.ll.Back()
		if old == nil {
			break
		}
		c.ll.Remove(old)
		delete(c.index, old.Value.(*lruEntry).key)
	}
}
