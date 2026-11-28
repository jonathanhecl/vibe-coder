package session

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/vision"
)

const (
	compactionTimeout  = 90 * time.Second
	compactionKeepTail = 30
	// compactionSummaryMaxChars bounds the rendered transcript sent to the
	// summarizer; compactionSummaryMinChars is the floor so a tiny context
	// window still leaves a usable summary input.
	compactionSummaryMaxChars = 48 * 1024
	compactionSummaryMinChars = 4096
	// defaultMaxSummaryChars is the budget renderMessagesForSummary uses when
	// the caller does not size it to a context window.
	defaultMaxSummaryChars = 48 * 1024
)

func (s *Session) Compact(ctx context.Context, force bool) error {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	cfg := s.cfg
	if cfg == nil || len(s.messages) <= compactionKeepTail {
		s.mu.RUnlock()
		return nil
	}
	if !force && len(s.messages) <= 300 && s.calibrator.Adjust(s.tokenEstimate) <= int(0.7*float64(cfg.ContextWindow)) {
		s.mu.RUnlock()
		return nil
	}
	keepTail := cfg.ContextWindow / 4
	s.mu.RUnlock()
	return s.CompactTo(ctx, keepTail)
}

// CompactTo summarizes all but the newest messages whose cumulative token
// cost fits inside keepTailTokens (at least four messages are always kept).
// When the cut would start on an orphaned tool result it walks backward (up
// to ten steps) to include the assistant tool_calls that produced it. If the
// tail covers the whole transcript, nothing is older than the cut and the
// call is a no-op.
func (s *Session) CompactTo(ctx context.Context, keepTailTokens int) error {
	if s == nil {
		return nil
	}

	s.mu.RLock()
	cfg := s.cfg
	client := s.client
	sideClient := s.sideClient
	if cfg == nil || len(s.messages) == 0 {
		s.mu.RUnlock()
		return nil
	}
	if keepTailTokens <= 0 {
		keepTailTokens = cfg.ContextWindow / 4
	}
	cut := len(s.messages)
	sum := 0
	for i := len(s.messages) - 1; i >= 0; i-- {
		cost := s.calibrator.Adjust(MessageCost(s.messages[i]))
		if sum+cost > keepTailTokens && len(s.messages)-cut >= 4 {
			break
		}
		sum += cost
		cut = i
	}
	// A kept tail that begins on a role:"tool" result lost its assistant
	// tool_calls on the wire; slide the cut back to include them when they
	// are close enough to reach.
	for steps := 0; steps < 10 && cut > 0 && s.messages[cut].Role == "tool"; steps++ {
		cut--
	}
	if cut <= 0 {
		s.mu.RUnlock()
		return nil
	}
	old := append([]Message(nil), s.messages[:cut]...)
	recent := append([]Message(nil), s.messages[cut:]...)
	firstUserIdx := s.firstUserMessageIndexLocked()
	lastUserIdx := -1
	if !hasSubstantiveUserMessage(recent) {
		lastUserIdx = s.lastSubstantiveUserMessageIndexBeforeLocked(cut)
	}
	confirmedChoices := filterChoicesNotInRecent(extractConfirmedUserChoices(old), recent)
	revision := s.revision
	s.mu.RUnlock()

	// Demote the discarded history into the session memory so it can be
	// recalled by goal later instead of being lost. This runs outside the lock
	// (it embeds over the network) and never fails compaction: an unavailable
	// or failing embedder simply keeps nothing.
	s.ensureMemory().demote(ctx, demoteTexts(old))

	var summary string
	for _, target := range compactionTargets(cfg, client, sideClient) {
		summary = requestCompactionSummary(ctx, old, cfg, target)
		if strings.TrimSpace(summary) != "" {
			break
		}
		// A cancelled or expired parent context means the run is winding down:
		// the next model would only spend the same budget again.
		if ctx.Err() != nil {
			break
		}
	}
	if summary == "" {
		summary = "Earlier conversation truncated to stay within context limits."
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.revision != revision {
		return fmt.Errorf("session changed during compaction")
	}
	// Move the compacted region into the archive so the session file keeps
	// the full transcript in chronological order. The anchor copy is skipped:
	// it duplicates a message already archived. Archived copies stay marked
	// so a reload routes them back to the archive, never to the wire view.
	for _, m := range old {
		if m.Anchor {
			continue
		}
		m.Archived = true
		s.archive = append(s.archive, m)
	}
	// Build the compacted transcript: summary, then the original first user
	// message preserved verbatim (so the agent never loses the task), plus
	// the latest substantive user message/preference when none remains in the
	// kept tail and any confirmed AskUserQuestion answers from old turns, then
	// the kept tail with orphaned native tool results converted to user-role
	// envelopes so the wire history stays valid. Anchors stay flagged Anchor so
	// later compactions do not archive them a second time.
	now := time.Now().UTC()
	newMsgs := make([]Message, 0, len(recent)+5)
	newMsgs = append(newMsgs, Message{
		Role:      "user",
		Content:   "[Earlier conversation summary]\n" + summary,
		Timestamp: now,
	})
	var firstUserAnchor *Message
	if firstUserIdx >= 0 && firstUserIdx < cut {
		anchor := s.messages[firstUserIdx]
		anchor.Anchor = true
		newMsgs = append(newMsgs, anchor)
		firstUserAnchor = &anchor
	}
	if lastUserIdx >= 0 && lastUserIdx < cut && lastUserIdx != firstUserIdx {
		cand := s.messages[lastUserIdx]
		if firstUserAnchor == nil || strings.TrimSpace(cand.Content) != strings.TrimSpace(firstUserAnchor.Content) {
			cand.Anchor = true
			newMsgs = append(newMsgs, cand)
		}
	}
	if len(confirmedChoices) > 0 {
		newMsgs = append(newMsgs, Message{
			Role:      "user",
			Content:   ConfirmedChoicesAnchorPrefix + "\n" + strings.Join(confirmedChoices, "\n"),
			Timestamp: now,
			Anchor:    true,
		})
	}
	newMsgs = append(newMsgs, normalizeKeptMessages(recent)...)
	s.messages = newMsgs
	s.recomputeTokenEstimate()
	s.revision++
	s.generation++
	return nil
}

const maxConfirmedChoiceAnchors = 12

// extractConfirmedUserChoices collects user answers from prior AskUserQuestion
// observations and previous choice anchors in chronological order.
func extractConfirmedUserChoices(msgs []Message) []string {
	var collected []string
	seen := make(map[string]struct{})
	addLine := func(line string) {
		t := strings.TrimSpace(line)
		if t == "" {
			return
		}
		if _, dup := seen[t]; dup {
			return
		}
		seen[t] = struct{}{}
		collected = append(collected, t)
	}
	for _, m := range msgs {
		if m.Role == "user" && strings.HasPrefix(strings.TrimSpace(m.Content), ConfirmedChoicesAnchorPrefix) {
			for _, line := range strings.Split(m.Content, "\n") {
				t := strings.TrimSpace(line)
				if strings.HasPrefix(t, "- ") {
					addLine(t)
				}
			}
			continue
		}
		var body string
		switch {
		case m.Role == "tool" && m.ToolName == "AskUserQuestion":
			body = strings.TrimSpace(m.Content)
		case m.Role == "user" && strings.HasPrefix(m.Content, "[tool_result name=AskUserQuestion]"):
			_, parsed := parseToolObservationEnvelope(m.Content)
			body = strings.TrimSpace(parsed)
		default:
			continue
		}
		if body == "" || strings.HasPrefix(body, "[elided]") || strings.HasPrefix(body, "Error:") {
			continue
		}
		foundHint := false
		if idx := strings.Index(body, "User confirmed choice (do not re-ask):"); idx >= 0 {
			section := body[idx:]
			if end := strings.Index(section, "[/assistant-hints]"); end >= 0 {
				section = section[:end]
			}
			for _, line := range strings.Split(section, "\n") {
				t := strings.TrimSpace(line)
				if strings.HasPrefix(t, "- ") {
					addLine(t)
					foundHint = true
				}
			}
		}
		if !foundHint {
			firstLine := body
			if nl := strings.IndexByte(firstLine, '\n'); nl >= 0 {
				firstLine = firstLine[:nl]
			}
			firstLine = strings.TrimSpace(firstLine)
			if firstLine != "" && strings.HasPrefix(firstLine, "{") {
				addLine("- Answer recorded: " + clipBytes(firstLine, 200))
			}
		}
	}
	if len(collected) > maxConfirmedChoiceAnchors {
		collected = collected[len(collected)-maxConfirmedChoiceAnchors:]
	}
	return collected
}

func filterChoicesNotInRecent(choices []string, recent []Message) []string {
	if len(choices) == 0 {
		return nil
	}
	var out []string
	for _, ch := range choices {
		present := false
		for _, m := range recent {
			if strings.Contains(m.Content, ch) {
				present = true
				break
			}
		}
		if !present {
			out = append(out, ch)
		}
	}
	return out
}

// compactionTarget is one model that may summarize the region being cut.
type compactionTarget struct {
	client    ollama.Client
	model     string
	keepAlive int
}

// compactionTargets lists the models that may summarize, in the order they are
// tried: the sidecar when it is in use, then the main model. The main model is
// in the list so a sidecar that is unloaded, unreachable or misconfigured costs
// one failed request instead of the summary itself. It is skipped when its
// model name repeats the sidecar's (retrying the same model would ask the same
// question twice) or when its client or model name is missing, so a setup
// without a sidecar still makes exactly one attempt.
func compactionTargets(cfg *config.Config, main, side ollama.Client) []compactionTarget {
	if cfg == nil {
		return nil
	}
	targets := make([]compactionTarget, 0, 2)
	if cfg.SidecarInUse() && strings.TrimSpace(cfg.SidecarModel) != "" {
		// Without a dedicated sidecar client the main one serves the sidecar
		// model: the model name alone decides which endpoint answers.
		sidecarClient := side
		if sidecarClient == nil {
			sidecarClient = main
		}
		if sidecarClient != nil {
			targets = append(targets, compactionTarget{
				client:    sidecarClient,
				model:     cfg.SidecarModel,
				keepAlive: cfg.KeepAliveFor(config.RoleSidecar),
			})
		}
	}
	if main != nil && strings.TrimSpace(cfg.Model) != "" {
		if len(targets) == 0 || targets[0].model != cfg.Model {
			targets = append(targets, compactionTarget{
				client:    main,
				model:     cfg.Model,
				keepAlive: cfg.KeepAliveFor(config.RoleMain),
			})
		}
	}
	return targets
}

// requestCompactionSummary asks one target to summarize the compacted region.
// It returns an empty string when the request fails or the reply is blank,
// which is how the caller learns to try the next target. Keep_alive follows
// whichever model actually runs the compaction.
func requestCompactionSummary(ctx context.Context, old []Message, cfg *config.Config, target compactionTarget) string {
	compactCtx, cancel := context.WithTimeout(ctx, compactionTimeout)
	defer cancel()
	systemPrompt := prompt.Render("compaction_summary", nil)
	// Size num_ctx and the rendered input to the context window: without
	// num_ctx Ollama applies its own default (often 4096) and silently
	// truncates the transcript being summarized.
	capTokens := ollama.NumCtxCap(cfg.ContextWindow)
	maxChars := (capTokens - 1024 - 512 - len(systemPrompt)/3) * 3
	if maxChars > compactionSummaryMaxChars {
		maxChars = compactionSummaryMaxChars
	}
	if maxChars < compactionSummaryMinChars {
		maxChars = compactionSummaryMinChars
	}
	userPrompt := renderMessagesForSummary(old, maxChars)
	resp, err := target.client.ChatSync(compactCtx, ollama.ChatRequest{
		Model: target.model,
		Messages: []ollama.Message{
			// The compaction prompt is a compile-time template: it tells the
			// summarizer to preserve the task, decisions, file paths, TODO state,
			// and what remains to do.
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: userPrompt},
		},
		Stream:    false,
		Think:     ollama.ResolveThinkSetting("", true, false, false),
		KeepAlive: target.keepAlive,
		Options: ollama.ChatOptions{
			NumPredict: 1024,
			NumCtx:     ollama.NumCtxFor(len(systemPrompt)+len(userPrompt), 1024, capTokens),
		},
	})
	if err != nil {
		return ""
	}
	return resp.Content
}

// normalizeKeptMessages converts any role:"tool" message that lost its
// preceding assistant tool_calls (because that assistant turn was compacted)
// into a user-role observation envelope. This keeps the wire history valid
// for Ollama /api/chat and preserves the tool output for the model.
func normalizeKeptMessages(msgs []Message) []Message {
	out := make([]Message, 0, len(msgs))
	pendingToolResults := 0
	for _, m := range msgs {
		switch m.Role {
		case "tool":
			if pendingToolResults <= 0 {
				// Orphaned tool result: convert to user observation envelope.
				out = append(out, Message{
					Role:      "user",
					Content:   ToolObservationUserContent(m.ToolName, m.Content),
					Timestamp: m.Timestamp,
				})
				continue
			}
			pendingToolResults--
		case "assistant":
			pendingToolResults = len(m.ToolCalls)
		case "user":
			pendingToolResults = 0
		}
		out = append(out, m)
	}
	return out
}

func hasToolCalls(m Message) bool {
	return m.Role == "assistant" && len(m.ToolCalls) > 0
}

func (s *Session) recomputeTokenEstimate() {
	total := 0
	for _, msg := range s.messages {
		total += MessageCost(msg)
	}
	s.tokenEstimate = total
}

// clipRunes truncates s to at most n runes, marking the cut with a clipped
// suffix so the reader knows content was removed. Rune-safe for CJK text.
func clipRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…[clipped]"
}

// clipBytes truncates s to at most maxBytes bytes on a rune boundary,
// marking the cut with a clipped suffix. Unlike clipRunes the budget is in
// bytes, so multi-byte text cannot overflow it.
func clipBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	end := 0
	for i := range s {
		if i > maxBytes {
			break
		}
		end = i
	}
	return s[:end] + "…[clipped]"
}

// renderMessagesForSummary renders the discarded transcript for the summary
// call. maxChars bounds the ENTIRE returned text — prior-summary prefix,
// omission marker and body together — so the caller's num_ctx sizing stays
// honest; callers size it to the context window and maxChars <= 0 falls back
// to the default 48 KB budget.
func renderMessagesForSummary(messages []Message, maxChars int) string {
	// Bound the sidecar input: at the compaction threshold the old
	// transcript can hold megabytes (verbatim tool outputs), which would
	// stall the small sidecar model past its timeout and degrade to the
	// static truncation note. Keep the most recent content so the summary
	// stays continuous with the messages kept verbatim. Each message is
	// clipped to a fixed byte budget so one giant tool output cannot crowd
	// out everything else.
	if maxChars <= 0 {
		maxChars = defaultMaxSummaryChars
	}
	const (
		maxPerMessageBytes   = 2000
		maxPriorSummaryBytes = 8000
		priorSummaryPrefix   = "[Earlier conversation summary]"
		omittedMarker        = "[earliest history omitted for size; summarizing the most recent part]\n"
	)
	prefix := ""
	start := 0
	// A prior compaction summary is the only memory of everything already
	// summarized; always carry it into the next summary (clipped), outside
	// the newest-first budget but inside maxChars. It gets at most half the
	// budget so the recent transcript always keeps room.
	if len(messages) > 0 && strings.HasPrefix(messages[0].Content, priorSummaryPrefix) {
		priorBudget := maxChars / 2
		if priorBudget > maxPriorSummaryBytes {
			priorBudget = maxPriorSummaryBytes
		}
		prefix = "user: " + clipBytes(messages[0].Content, priorBudget) + "\n"
		start = 1
	}
	// Reserve the marker up front so prefix + marker + body can never exceed
	// maxChars, whether or not the body ends up clipped.
	bodyBudget := maxChars - len(prefix) - len(omittedMarker)
	if bodyBudget < 0 {
		bodyBudget = 0
	}
	// Collect each rendered entry (role header plus its multi-line content)
	// as one element, newest-first under the size budget, then reverse the
	// slice to restore chronological order. Reversing whole entries — not
	// raw lines — keeps a message's internal line order intact.
	var entries []string
	clipped := false
	size := 0
	for i := len(messages) - 1; i >= start; i-- {
		m := messages[i]
		if isSyntheticContextEnvelope(m.Content) {
			continue
		}
		entry := m.Role
		if m.ToolName != "" {
			entry += "(" + m.ToolName + ")"
		}
		entry += ": " + clipBytes(m.Content, maxPerMessageBytes)
		if len(m.ToolCalls) > 0 {
			if raw, err := json.Marshal(m.ToolCalls); err == nil {
				entry += " [tool_calls: " + string(raw) + "]"
			}
		}
		entry += "\n"
		if size+len(entry) > bodyBudget {
			clipped = true
			break
		}
		size += len(entry)
		entries = append(entries, entry)
	}
	for i, j := 0, len(entries)-1; i < j; i, j = i+1, j-1 {
		entries[i], entries[j] = entries[j], entries[i]
	}
	body := strings.Join(entries, "")
	if !clipped {
		return prefix + body
	}
	return prefix + omittedMarker + body
}

// EstimateTokens exposes the session's heuristic token estimator so metrics
// and budgeting code can price prompts with the same ruler compaction uses.
func EstimateTokens(text string) int {
	return estimateTextTokens(text)
}

func estimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk := 0
	for _, r := range text {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3040 && r <= 0x30FF) || (r >= 0xAC00 && r <= 0xD7AF) {
			cjk++
		}
	}
	asciiApprox := len(text) / 4
	return cjk + asciiApprox
}

// estimateMessageTokens adds the vision proxy cost for attached images so
// compaction triggers and /tokens account for pictures, not just text.
func estimateMessageTokens(content string) int {
	return estimateTextTokens(content) + vision.CountMarkers(content)*vision.EstimatedTokensPerImage
}
