package session

import (
	"context"
	"fmt"
	"math"
	"sort"
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// maxMemoryEntries caps how many demoted fragments a session keeps so recall
// stays cheap and the store cannot grow without bound on a long run.
const maxMemoryEntries = 256

// maxMemoryEntryRunes bounds one demoted fragment before it is embedded.
const maxMemoryEntryRunes = 2000

// minMemoryScore is the minimum cosine similarity required for a demoted
// fragment to be recalled into the prompt. Fragments below this threshold are
// treated as unrelated noise and omitted.
const minMemoryScore = 0.25

// memoryEntry is one fragment of history that compaction removed from the live
// transcript, kept so it can be recalled by relevance instead of being lost.
type memoryEntry struct {
	text   string
	vector []float64
}

// sessionMemory is a session-scoped vector store of the history compaction
// dropped. Demoted fragment texts are also persisted in a lightweight session
// sidecar (.mem.json) so resumed sessions can re-embed and recall history that
// earlier compactions removed from the live transcript.
type sessionMemory struct {
	mu           sync.Mutex
	entries      []memoryEntry
	pendingTexts []string
	embedder     ollama.Embedder
	model        string
}

// newSessionMemory builds the store from the session's own chat client: a
// client that also implements ollama.Embedder turns the store on, and the
// model comes from the embedding role. Any missing piece leaves it disabled.
func newSessionMemory(cfg *config.Config, client ollama.Client) *sessionMemory {
	store := &sessionMemory{}
	store.bind(cfg, client)
	return store
}

// bind updates the embedder and embedding model on an existing store without
// discarding already-demoted entries or pending persisted texts.
func (m *sessionMemory) bind(cfg *config.Config, client ollama.Client) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.embedder = nil
	m.model = ""
	embedder, ok := client.(ollama.Embedder)
	if !ok || cfg == nil {
		return
	}
	model := strings.TrimSpace(cfg.RoleModelName(config.RoleEmbedding))
	if model == "" {
		return
	}
	m.embedder = embedder
	m.model = model
}

// enabled reports whether the store can embed.
func (m *sessionMemory) enabled() bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.embedder != nil && m.model != ""
}

// setPersistedTexts replaces the store's state with texts loaded from disk.
// They are embedded lazily on the next HydrateMemory, demote, or recall call.
func (m *sessionMemory) setPersistedTexts(texts []string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.entries = nil
	m.pendingTexts = deduplicateMemoryTexts(texts, nil)
	if len(m.pendingTexts) > maxMemoryEntries {
		m.pendingTexts = append([]string(nil), m.pendingTexts[len(m.pendingTexts)-maxMemoryEntries:]...)
	}
}

// texts returns all demoted fragment texts currently held (embedded or pending
// embedding) so Save can persist them in the session sidecar.
func (m *sessionMemory) texts() []string {
	if m == nil {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.entries)+len(m.pendingTexts))
	seen := make(map[string]struct{}, len(m.entries)+len(m.pendingTexts))
	for _, e := range m.entries {
		if _, ok := seen[e.text]; !ok && e.text != "" {
			seen[e.text] = struct{}{}
			out = append(out, e.text)
		}
	}
	for _, t := range m.pendingTexts {
		if _, ok := seen[t]; !ok && t != "" {
			seen[t] = struct{}{}
			out = append(out, t)
		}
	}
	if len(out) > maxMemoryEntries {
		out = out[len(out)-maxMemoryEntries:]
	}
	return out
}

// demote embeds the fragments of discarded history and appends them to the
// store. Duplicate fragments are skipped so repeated compactions or hydrations
// never store the same text twice.
func (m *sessionMemory) demote(ctx context.Context, texts []string) {
	if !m.enabled() {
		return
	}
	m.mu.Lock()
	embedder := m.embedder
	model := m.model
	combined := make([]string, 0, len(m.pendingTexts)+len(texts))
	combined = append(combined, m.pendingTexts...)
	combined = append(combined, texts...)
	existing := make(map[string]struct{}, len(m.entries))
	for _, e := range m.entries {
		existing[e.text] = struct{}{}
	}
	unique := deduplicateMemoryTexts(combined, existing)
	m.mu.Unlock()

	if len(unique) == 0 {
		return
	}
	vectors, err := embedder.Embed(ctx, model, unique)
	if err != nil || len(vectors) != len(unique) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.pendingTexts = nil
	seen := make(map[string]struct{}, len(m.entries))
	for _, e := range m.entries {
		seen[e.text] = struct{}{}
	}
	for i, text := range unique {
		if len(vectors[i]) == 0 {
			continue
		}
		if _, dup := seen[text]; dup {
			continue
		}
		seen[text] = struct{}{}
		m.entries = append(m.entries, memoryEntry{text: text, vector: vectors[i]})
	}
	if len(m.entries) > maxMemoryEntries {
		m.entries = append([]memoryEntry(nil), m.entries[len(m.entries)-maxMemoryEntries:]...)
	}
}

func deduplicateMemoryTexts(texts []string, existing map[string]struct{}) []string {
	if len(texts) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(texts))
	out := make([]string, 0, len(texts))
	for _, raw := range texts {
		t := strings.TrimSpace(raw)
		if t == "" {
			continue
		}
		if existing != nil {
			if _, ok := existing[t]; ok {
				continue
			}
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// flushPending embeds any persisted fragment texts that were loaded from disk
// before the first recall or hydration.
func (m *sessionMemory) flushPending(ctx context.Context) {
	if m == nil {
		return
	}
	m.mu.Lock()
	hasPending := len(m.pendingTexts) > 0
	m.mu.Unlock()
	if hasPending {
		m.demote(ctx, nil)
	}
}

// recall returns the fragments most similar to the query as a context block,
// or an empty string when the store is disabled or has nothing above the
// minimum similarity threshold.
func (m *sessionMemory) recall(ctx context.Context, query string, k int) string {
	if !m.enabled() || strings.TrimSpace(query) == "" {
		return ""
	}
	m.flushPending(ctx)
	if k <= 0 {
		k = 3
	}
	m.mu.Lock()
	embedder := m.embedder
	model := m.model
	candidates := append([]memoryEntry(nil), m.entries...)
	m.mu.Unlock()
	if len(candidates) == 0 {
		return ""
	}

	vectors, err := embedder.Embed(ctx, model, []string{query})
	if err != nil || len(vectors) != 1 || len(vectors[0]) == 0 {
		return ""
	}
	queryVec := vectors[0]

	type scored struct {
		entry memoryEntry
		score float64
	}
	ranked := make([]scored, 0, len(candidates))
	for _, entry := range candidates {
		score := memoryCosine(queryVec, entry.vector)
		if score < minMemoryScore {
			continue
		}
		ranked = append(ranked, scored{entry: entry, score: score})
	}
	if len(ranked) == 0 {
		return ""
	}
	sort.SliceStable(ranked, func(i, j int) bool { return ranked[i].score > ranked[j].score })
	if len(ranked) > k {
		ranked = ranked[:k]
	}

	var b strings.Builder
	b.WriteString("[Session memory]\n")
	for _, item := range ranked {
		b.WriteString("- ")
		b.WriteString(item.entry.text)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

// count reports how many fragments the store holds (used by tests and status).
func (m *sessionMemory) count() int {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.entries)
}

// memoryCosine is the cosine similarity of two vectors, 0 when either is empty,
// mismatched or has zero norm.
func memoryCosine(a, b []float64) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, normA, normB float64
	for i := range a {
		dot += a[i] * b[i]
		normA += a[i] * a[i]
		normB += b[i] * b[i]
	}
	if normA == 0 || normB == 0 {
		return 0
	}
	return dot / (math.Sqrt(normA) * math.Sqrt(normB))
}

// HydrateMemory demotes the transcript's older turns into the in-memory store
// so they can be recalled by relevance. If persisted memory texts were loaded
// from disk, it embeds those first. It is a no-op when the store already
// holds entries or when the transcript is short.
func (s *Session) HydrateMemory(ctx context.Context) {
	if s == nil {
		return
	}
	mem := s.ensureMemory()
	mem.flushPending(ctx)
	if mem.count() > 0 {
		return
	}
	s.mu.RLock()
	cfg := s.cfg
	if cfg == nil || len(s.messages) == 0 {
		s.mu.RUnlock()
		return
	}
	keepTailTokens := cfg.ContextWindow / 4
	if keepTailTokens <= 0 {
		keepTailTokens = 32768 / 4
	}
	cut := len(s.messages)
	sum := 0
	for i := len(s.messages) - 1; i >= 0; i-- {
		cost := s.calibrator.Adjust(estimateMessageTokens(s.messages[i].Content))
		if sum+cost > keepTailTokens && len(s.messages)-cut >= 4 {
			break
		}
		sum += cost
		cut = i
	}
	if cut <= 0 {
		s.mu.RUnlock()
		return
	}
	old := append([]Message(nil), s.messages[:cut]...)
	s.mu.RUnlock()

	mem.demote(ctx, demoteTexts(old))
}

// demoteTexts renders the discarded messages to bounded fragments ready to be
// embedded. Synthetic blocks (prior recalls, RAG dumps, compaction summaries,
// runtime reminders) are skipped so the store never embeds memories of
// memories. Assistant tool-call intents are paired with their tool results so
// each recalled tool fragment carries both the target argument and its output.
func demoteTexts(messages []Message) []string {
	texts := make([]string, 0, len(messages))
	pendingTargets := make(map[string][]string)

	popTarget := func(toolName string) string {
		q := pendingTargets[toolName]
		if len(q) == 0 {
			return ""
		}
		target := q[0]
		pendingTargets[toolName] = q[1:]
		return target
	}

	for _, msg := range messages {
		if msg.Role == "assistant" {
			for _, tc := range msg.ToolCalls {
				name := strings.TrimSpace(tc.Function.Name)
				if name != "" {
					pendingTargets[name] = append(pendingTargets[name], extractMemoryToolTarget(tc.Function.Arguments))
				}
			}
			for _, inv := range extractXMLInvokeTargets(msg.Content) {
				pendingTargets[inv.name] = append(pendingTargets[inv.name], inv.target)
			}
		}

		content := strings.TrimSpace(msg.Content)
		if content == "" || isSyntheticContextEnvelope(content) {
			continue
		}
		if msg.Role == "assistant" && strings.HasPrefix(content, "[runtime]") {
			continue
		}

		if msg.Role == "user" && strings.HasPrefix(content, "[tool_result") {
			toolName, body := parseToolObservationEnvelope(content)
			target := popTarget(toolName)
			label := "tool"
			if toolName != "" {
				if target != "" {
					label = fmt.Sprintf("tool:%s(%s)", toolName, target)
				} else {
					label = fmt.Sprintf("tool:%s", toolName)
				}
			}
			text := clipRunes(fmt.Sprintf("%s: %s", label, body), maxMemoryEntryRunes)
			texts = append(texts, text)
			continue
		}

		label := msg.Role
		if msg.Role == "tool" {
			target := popTarget(msg.ToolName)
			if msg.ToolName != "" {
				if target != "" {
					label = fmt.Sprintf("tool:%s(%s)", msg.ToolName, target)
				} else {
					label = fmt.Sprintf("tool:%s", msg.ToolName)
				}
			}
		}
		text := clipRunes(fmt.Sprintf("%s: %s", label, content), maxMemoryEntryRunes)
		texts = append(texts, text)
	}
	return deduplicateMemoryTexts(texts, nil)
}

var memoryTargetKeys = []string{
	"file_path",
	"path",
	"pattern",
	"command",
	"query",
	"url",
	"task_id",
}

func extractMemoryToolTarget(args map[string]any) string {
	if len(args) == 0 {
		return ""
	}
	for _, key := range memoryTargetKeys {
		raw, ok := args[key]
		if !ok || raw == nil {
			continue
		}
		s, ok := raw.(string)
		if !ok {
			continue
		}
		val := strings.TrimSpace(s)
		if val == "" {
			continue
		}
		val = strings.Join(strings.Fields(val), " ")
		if len(val) > 80 {
			val = val[:80] + "..."
		}
		return fmt.Sprintf("%s=%s", key, val)
	}
	return ""
}

type xmlInvokeTarget struct {
	name   string
	target string
}

func extractXMLInvokeTargets(content string) []xmlInvokeTarget {
	const prefix = `<invoke name="`
	const suffix = `</invoke>`
	var out []xmlInvokeTarget
	rest := content
	for {
		idx := strings.Index(rest, prefix)
		if idx < 0 {
			break
		}
		rest = rest[idx+len(prefix):]
		q := strings.IndexByte(rest, '"')
		if q < 0 {
			break
		}
		name := strings.TrimSpace(rest[:q])
		gt := strings.IndexByte(rest[q:], '>')
		if gt < 0 {
			break
		}
		bodyStart := q + gt + 1
		end := strings.Index(rest[bodyStart:], suffix)
		if end < 0 {
			break
		}
		body := strings.TrimSpace(rest[bodyStart : bodyStart+end])
		rest = rest[bodyStart+end+len(suffix):]
		if name == "" {
			continue
		}
		out = append(out, xmlInvokeTarget{
			name:   name,
			target: extractJSONBodyTarget(body),
		})
	}
	return out
}

func extractJSONBodyTarget(body string) string {
	for _, key := range memoryTargetKeys {
		needle := `"` + key + `"`
		idx := strings.Index(body, needle)
		if idx < 0 {
			continue
		}
		after := strings.TrimSpace(body[idx+len(needle):])
		if !strings.HasPrefix(after, ":") {
			continue
		}
		after = strings.TrimSpace(after[1:])
		if !strings.HasPrefix(after, `"`) {
			continue
		}
		after = after[1:]
		end := strings.IndexByte(after, '"')
		if end < 0 {
			continue
		}
		val := strings.TrimSpace(after[:end])
		if val == "" {
			continue
		}
		if len(val) > 80 {
			val = val[:80] + "..."
		}
		return fmt.Sprintf("%s=%s", key, val)
	}
	return ""
}

func parseToolObservationEnvelope(content string) (toolName, body string) {
	const prefix = "[tool_result name="
	const closeTag = "\n[/tool_result]"
	nl := strings.IndexByte(content, '\n')
	if nl < 0 {
		return "", content
	}
	header := strings.TrimSpace(content[:nl])
	if strings.HasPrefix(header, prefix) && strings.HasSuffix(header, "]") {
		toolName = strings.TrimSpace(header[len(prefix) : len(header)-1])
	}
	rest := content[nl+1:]
	if end := strings.Index(rest, closeTag); end >= 0 {
		body = strings.TrimSpace(rest[:end])
	} else {
		body = strings.TrimSpace(rest)
	}
	return toolName, body
}
