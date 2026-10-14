package sidecar

import (
	"context"
	"strings"
)

const (
	// rewriteNumPredict keeps the answer short: a rewritten query is a single
	// line of keywords, not prose.
	rewriteNumPredict = 64
	// maxRewriteRunes bounds the rewritten query so a chatty model cannot turn
	// the retrieval query into a paragraph.
	maxRewriteRunes = 200
)

// rewriteSystem turns a request into a retrieval query. It must never answer
// the request, only name what to look up.
const rewriteSystem = "You turn a coding request into a short search query for a code index. " +
	"Reply with ONE line of keywords only, no quotes, no explanation, and never answer the request."

// RewriteQuery asks the sidecar to turn a user request into a concise search
// query for retrieval. The raw request is a poor query because it carries
// instructions and phrasing, while a keyword line matches the index better. It
// returns an empty string when the sidecar is disabled, the rewrite fails or the
// answer is unusable, so the caller keeps the original request and retrieval
// never depends on this call.
func (p *Pool) RewriteQuery(ctx context.Context, query string) string {
	query = strings.TrimSpace(query)
	if p == nil || !p.Enabled() || query == "" {
		return ""
	}
	key := cacheKey("rewrite", p.cfg.SidecarModel, query)
	if cached, ok := p.cache.get(key); ok {
		return cached
	}
	v, err, _ := p.sf.Do(key, func() (any, error) {
		return p.chat(ctx, rewriteSystem, query, rewriteNumPredict)
	})
	if err != nil {
		return ""
	}
	rewritten, _ := v.(string)
	rewritten = sanitizeQuery(rewritten)
	if rewritten == "" {
		return ""
	}
	p.cache.put(key, rewritten)
	return rewritten
}

// sanitizeQuery reduces a model answer to a single clean line: the first
// non-empty line, stripped of quotes, bullets and trailing punctuation, capped
// in length. Anything unusable becomes an empty string.
func sanitizeQuery(raw string) string {
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		line = strings.Trim(line, "\"'`*-#> ")
		if line == "" {
			continue
		}
		if runes := []rune(line); len(runes) > maxRewriteRunes {
			line = string(runes[:maxRewriteRunes])
		}
		return strings.TrimSpace(line)
	}
	return ""
}
