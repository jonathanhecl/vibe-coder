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

// memoryEntry is one fragment of history that compaction removed from the live
// transcript, kept so it can be recalled by relevance instead of being lost.
type memoryEntry struct {
	text   string
	vector []float64
}

// sessionMemory is a session-scoped vector store of the history compaction
// dropped. It lives in memory on purpose: demoted history only has to survive
// the running session, and keeping it here means tiered memory works without
// the rag build tag. When no embedder or embedding model is available the
// store stays disabled and compaction simply drops the history as before.
type sessionMemory struct {
	mu       sync.Mutex
	entries  []memoryEntry
	embedder ollama.Embedder
	model    string
}

// newSessionMemory builds the store from the session's own chat client: a
// client that also implements ollama.Embedder turns the store on, and the
// model comes from the embedding role. Any missing piece leaves it disabled.
func newSessionMemory(cfg *config.Config, client ollama.Client) *sessionMemory {
	store := &sessionMemory{}
	embedder, ok := client.(ollama.Embedder)
	if !ok || cfg == nil {
		return store
	}
	model := cfg.RoleModelName(config.RoleEmbedding)
	if strings.TrimSpace(model) == "" {
		return store
	}
	store.embedder = embedder
	store.model = model
	return store
}

// enabled reports whether the store can embed.
func (m *sessionMemory) enabled() bool {
	return m != nil && m.embedder != nil && m.model != ""
}

// demote embeds the fragments of discarded history and appends them to the
// store. It never fails: an embedding error just means the fragments are not
// kept, exactly like the old behaviour where they were dropped.
func (m *sessionMemory) demote(ctx context.Context, texts []string) {
	if !m.enabled() || len(texts) == 0 {
		return
	}
	vectors, err := m.embedder.Embed(ctx, m.model, texts)
	if err != nil || len(vectors) != len(texts) {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, text := range texts {
		if len(vectors[i]) == 0 {
			continue
		}
		m.entries = append(m.entries, memoryEntry{text: text, vector: vectors[i]})
	}
	if len(m.entries) > maxMemoryEntries {
		m.entries = append([]memoryEntry(nil), m.entries[len(m.entries)-maxMemoryEntries:]...)
	}
}

// recall returns the fragments most similar to the query as a context block,
// or an empty string when the store is disabled or has nothing to offer.
func (m *sessionMemory) recall(ctx context.Context, query string, k int) string {
	if !m.enabled() || strings.TrimSpace(query) == "" {
		return ""
	}
	if k <= 0 {
		k = 3
	}
	vectors, err := m.embedder.Embed(ctx, m.model, []string{query})
	if err != nil || len(vectors) != 1 || len(vectors[0]) == 0 {
		return ""
	}
	queryVec := vectors[0]

	m.mu.Lock()
	candidates := append([]memoryEntry(nil), m.entries...)
	m.mu.Unlock()
	if len(candidates) == 0 {
		return ""
	}

	type scored struct {
		entry memoryEntry
		score float64
	}
	ranked := make([]scored, 0, len(candidates))
	for _, entry := range candidates {
		score := memoryCosine(queryVec, entry.vector)
		if score <= 0 {
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

// demoteTexts renders the discarded messages to bounded fragments ready to be
// embedded. Tool results are labeled so recall reads naturally.
func demoteTexts(messages []Message) []string {
	texts := make([]string, 0, len(messages))
	for _, msg := range messages {
		content := strings.TrimSpace(msg.Content)
		if content == "" {
			continue
		}
		label := msg.Role
		if msg.Role == "tool" && msg.ToolName != "" {
			label = fmt.Sprintf("tool:%s", msg.ToolName)
		}
		text := clipRunes(fmt.Sprintf("%s: %s", label, content), maxMemoryEntryRunes)
		texts = append(texts, text)
	}
	return texts
}
