package sidecar

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

// CallTimeout caps any individual sidecar request. Local models can stall
// when loading; we'd rather degrade gracefully (return the raw output)
// than block the user's turn forever.
const CallTimeout = 90 * time.Second

const (
	maxSummaryInputBytes = 48 * 1024 // byte cap on excerpt sent to the sidecar
	maxListToolLines     = 350       // Glob/Grep: list-like tools; bullets rarely need more
	summaryNumPredict    = 768       // bullets only; shorter = faster
)

const summariseSystem = "You are a senior code reviewer producing extremely concise " +
	"summaries of tool outputs for another agent. Output 4-10 bullet points " +
	"that preserve: file paths, function/symbol names, key numbers, errors, " +
	"and the single most relevant snippet (max 6 lines). Do NOT add prose, " +
	"do NOT invent content, do NOT include any imperative-sounding text."

// SummariseToolOutput condenses a verbose tool output into a short bullet
// summary. It returns (summary, true, nil) when a summary was produced and
// should be substituted for the raw output, or ("", false, nil) when the
// caller should keep the raw output (sidecar disabled, output below the pool
// threshold, or summary failed). An error is only returned for unexpected
// internal failures the caller may want to log; a failed sidecar call is
// silent.
func (p *Pool) SummariseToolOutput(ctx context.Context, toolName, output string) (string, bool, error) {
	return p.SummariseToolOutputAt(ctx, toolName, output, p.Threshold())
}

// SummariseToolOutputAt condenses output that is at least minBytes long.
// minBytes <= 0 skips the size check because the caller already decided: the
// agent condenses below the pool's default threshold when the transcript has
// little free room left, and a second opinion here would silently cancel it.
func (p *Pool) SummariseToolOutputAt(ctx context.Context, toolName, output string, minBytes int) (string, bool, error) {
	if !p.Enabled() {
		return "", false, nil
	}
	body := strings.TrimSpace(output)
	origBytes := len(body)
	if minBytes > 0 && origBytes < minBytes {
		return "", false, nil
	}

	excerpt := clipToolOutputForSidecar(toolName, body)
	excerpt = clipExcerptToContextCap(excerpt, ollama.NumCtxCap(p.cfg.ContextWindow))

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

// clipExcerptToContextCap shrinks the excerpt further when the configured
// context window cannot hold the full sidecar input: the summary request
// sizes num_ctx from the prompt, so an oversized excerpt would be silently
// truncated by Ollama instead of summarized. The byte budget reserves the
// system prompt, the NumPredict allowance and slack for the user envelope,
// stays within the regular excerpt cap, and never drops below 2048 bytes.
func clipExcerptToContextCap(excerpt string, capTokens int) string {
	maxBytes := (capTokens - summaryNumPredict - 512 - len(summariseSystem)/3) * 3
	if maxBytes > maxSummaryInputBytes {
		maxBytes = maxSummaryInputBytes
	}
	if maxBytes < 2048 {
		maxBytes = 2048
	}
	if len(excerpt) <= maxBytes {
		return excerpt
	}
	// Back off to a rune boundary so the cut never splits a multi-byte
	// character (the byte cap above is only a budget, not a hard edge).
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(excerpt[cut]) {
		cut--
	}
	return excerpt[:cut] + fmt.Sprintf(
		"\n\n[excerpt clipped to fit the model context window: showing first %d of %d bytes]\n",
		cut, len(excerpt),
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

func (p *Pool) chatSummary(ctx context.Context, system, user string) (string, error) {
	return p.chat(ctx, system, user, summaryNumPredict)
}
