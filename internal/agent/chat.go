package agent

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"
	"syscall"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/prompt"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/skills"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// maxUserAnchorChars bounds the re-anchored user request a trimmed window may
// prepend, so a pasted document cannot defeat the transcript character budget.
const maxUserAnchorChars = 4000

// windowStats describes the transcript window a request carried so metrics
// can record what was actually sent.
type windowStats struct {
	MessagesTotal       int
	MessagesSent        int
	TranscriptTokensEst int
	ToolResultsMasked   int
	WindowStart         int
	MessagesDropped     int
}

func (a *Agent) buildOllamaMessages(ctx context.Context, systemPrompt string) ([]ollama.Message, windowStats) {
	hist := a.sess.MessagesReadOnly()
	stats := windowStats{MessagesTotal: len(hist)}
	out := []ollama.Message{{Role: "system", Content: systemPrompt}}
	if len(hist) == 0 {
		stats.MessagesSent = len(out) - 1
		return out, stats
	}
	// Budget the transcript share in tokens (masking + windowing both price
	// messages with the session estimator), then mask old tool outputs and
	// pick the window start. The start is sticky: within one session
	// generation it only moves when the suffix outgrows the transcript
	// budget, so earlier requests don't reshuffle the visible history.
	budget := a.computeBudget(systemPrompt)
	gen := a.sess.Generation()
	a.wireMu.Lock()
	sameGen := a.wire.set && a.wire.gen == gen
	prevStart := a.wire.start
	maskBefore := a.maskBeforeLocked(hist, gen)
	masked, _ := maskToolResults(hist, maskBefore)
	start := -1
	if sameGen && prevStart >= 0 && prevStart < len(masked) &&
		transcriptCost(masked[prevStart:]) <= budget.Transcript {
		start = prevStart
	} else {
		start = windowStartForBudget(masked, budget.Transcript)
	}
	a.wire.start = start
	a.wireMu.Unlock()
	stats.WindowStart = start
	stats.MessagesDropped = start
	for i := start; i < maskBefore && i < len(masked); i++ {
		if masked[i].Content != hist[i].Content {
			stats.ToolResultsMasked++
		}
	}
	// A budget-trimmed window can begin with a role="tool" result whose
	// assistant tool_calls fell outside the cut, and can lose the user turn
	// entirely. Strict OpenAI-compatible servers reject both shapes: a tool
	// result must answer a preceding assistant tool_calls, and every request
	// must carry a user query ("No user query found in messages."). Repair the
	// window before it reaches the wire.
	out = append(out, windowForWire(masked, start)...)
	// Markers become image bytes (or sidecar descriptions, or honest notes)
	// only here, at send time. The transcript keeps the cheap text form.
	a.setLastImage(out)
	out = scrubPastedImagePaths(a.resolveOutgoingImages(ctx, out))
	// The ephemeral runtime-state note rides at the tail of the window so it
	// is always fresh: durable progress and mission turn counts change every
	// iteration, and pinning them inside the system prompt would bust the
	// KV cache. It is never stored in the session transcript.
	if note := a.runtimeStateNote(); note != "" {
		out = append(out, ollama.Message{
			Role:    "user",
			Content: session.RuntimeReminderPrefix + "\n" + note + "\n[/runtime_reminder]",
		})
	}
	stats.MessagesSent = len(out) - 1
	for _, m := range out[1:] {
		stats.TranscriptTokensEst += session.MessageTokenCost(m.Content)
	}
	return out, stats
}

// transcriptCost prices a slice of transcript messages in estimated tokens
// (text + vision marker cost).
func transcriptCost(msgs []session.Message) int {
	total := 0
	for _, m := range msgs {
		total += session.MessageTokenCost(m.Content)
	}
	return total
}

// windowStartForBudget walks backward from the newest message accumulating
// token cost while the total stays within 60% of budget, always including at
// least the last message. It returns the first included index.
func windowStartForBudget(hist []session.Message, budget int) int {
	limit := budget * 60 / 100
	start := len(hist) - 1
	sum := 0
	for i := len(hist) - 1; i >= 0; i-- {
		cost := session.MessageTokenCost(hist[i].Content)
		if i < len(hist)-1 && sum+cost > limit {
			break
		}
		sum += cost
		start = i
	}
	if start < 0 {
		start = 0
	}
	return start
}

// windowForWire converts the transcript suffix [start:] into valid wire
// history. Orphaned tool results (their assistant tool_calls fell outside the
// cut) become user observation envelopes, and when the trimmed window carries
// no user-role message at all it is re-anchored on the original request.
func windowForWire(hist []session.Message, start int) []ollama.Message {
	window := make([]ollama.Message, 0, len(hist)-start)
	pendingToolResults := 0
	for i := start; i < len(hist); i++ {
		m := hist[i]
		switch m.Role {
		case "tool":
			if pendingToolResults <= 0 {
				window = append(window, ollama.Message{
					Role:    "user",
					Content: session.ToolObservationUserContent(m.ToolName, m.Content),
				})
				continue
			}
			pendingToolResults--
		case "assistant":
			pendingToolResults = len(m.ToolCalls)
		case "user":
			pendingToolResults = 0
		default:
			continue
		}
		window = append(window, ollama.Message{
			Role:      m.Role,
			Content:   m.Content,
			ToolCalls: m.ToolCalls,
			ToolName:  m.ToolName,
		})
	}
	if !hasUserRole(window) {
		if anchor := transcriptUserAnchor(hist); anchor != "" {
			window = append([]ollama.Message{{Role: "user", Content: anchor}}, window...)
		}
	}
	return window
}

func hasUserRole(msgs []ollama.Message) bool {
	for _, m := range msgs {
		if m.Role == "user" {
			return true
		}
	}
	return false
}

// transcriptUserAnchor returns a user-role anchor for a trimmed window that
// lost its user turn: the most recent real user request, or, failing that, any
// user-role message (a runtime reminder or an earlier summary). It returns ""
// only when the transcript has no user-role message at all.
func transcriptUserAnchor(hist []session.Message) string {
	fallback := ""
	for i := len(hist) - 1; i >= 0; i-- {
		m := hist[i]
		if m.Role != "user" {
			continue
		}
		c := strings.TrimSpace(m.Content)
		if c == "" {
			continue
		}
		if fallback == "" {
			fallback = c
		}
		if strings.HasPrefix(c, "[tool_result") ||
			strings.HasPrefix(c, session.RuntimeReminderPrefix) ||
			strings.HasPrefix(c, "[Earlier conversation summary]") {
			continue
		}
		return clipAnchor(c)
	}
	return clipAnchor(fallback)
}

func clipAnchor(s string) string {
	if len(s) <= maxUserAnchorChars {
		return s
	}
	runes := []rune(s)
	if len(runes) <= maxUserAnchorChars {
		return s
	}
	return string(runes[:maxUserAnchorChars]) + "…"
}

// isConnectionError reports whether err means the remote model endpoint is
// unreachable or refused the connection at the transport level. It excludes
// user cancellations and our own request timeouts so those stay fatal.
func isConnectionError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		switch {
		case errors.Is(e, syscall.ECONNREFUSED),
			errors.Is(e, syscall.ETIMEDOUT),
			errors.Is(e, syscall.EHOSTUNREACH),
			errors.Is(e, syscall.ENETUNREACH),
			errors.Is(e, syscall.ECONNRESET):
			return true
		}
		msg := strings.ToLower(e.Error())
		if strings.Contains(msg, "connection refused") ||
			strings.Contains(msg, "no such host") ||
			strings.Contains(msg, "i/o timeout") ||
			strings.Contains(msg, "broken pipe") ||
			strings.Contains(msg, "network is unreachable") ||
			(strings.Contains(msg, "dial") && strings.Contains(msg, "connect:")) {
			return true
		}
	}
	return false
}

func (a *Agent) chatOnce(rootCtx context.Context) (string, []ToolCall, error) {
	var lastErr error
	thinkingLoopRetries := 0

	for {
		for attempt := 0; attempt <= MaxRetries; attempt++ {
			ctx, cancel := context.WithTimeout(rootCtx, a.cfg.EffectiveChatTimeout())
			systemPrompt := a.buildSystemPrompt()
			messages, wstats := a.buildOllamaMessages(ctx, systemPrompt)
			// Usage belongs to the request that produced it; clear it so a
			// failed attempt never records the previous turn's counters.
			a.lastUsage = nil
			if thinkingLoopRetries > 0 {
				a.ui.StartWaiting(fmt.Sprintf("restarting thinking after loop (attempt %d/%d)…", thinkingLoopRetries+1, maxThinkingLoopRetries))
			} else {
				a.ui.StartWaiting(fmt.Sprintf("waiting for %s…", shortModelName(a.cfg.Model)))
			}
			chatStart := time.Now()
			stream, err := a.client.Chat(ctx, ollama.ChatRequest{
				Model:     a.cfg.Model,
				Messages:  messages,
				Stream:    true,
				Think:     ollama.ResolveThinkSetting(a.cfg.OllamaThinkLevel, a.cfg.OllamaNoThink, a.cfg.ThinkingKnown, a.cfg.ThinkingSupported),
				Tools:     a.nativeChatTools(),
				KeepAlive: a.cfg.KeepAliveFor(config.RoleMain),
				Options: ollama.ChatOptions{
					NumCtx:      a.cfg.ContextWindow,
					NumPredict:  a.cfg.MaxTokens,
					Temperature: a.cfg.Temperature,
				},
			})
			if err != nil {
				a.ui.StopWaiting()
				cancel()
				a.recordChatAttempt(systemPrompt, wstats, chatStart, 0, err)
				if isCancelledByUser(rootCtx, err) {
					return "[Cancelled by user]", nil, nil
				}
				if strings.Contains(strings.ToLower(err.Error()), "model not found") {
					if pulled := a.tryAutoPullModel(rootCtx); pulled {
						attempt--
						continue
					}
				}
				lastErr = err
			} else if reply, toolCalls, err := a.streamAssistantResponse(rootCtx, cancel, stream); err != nil {
				a.recordChatAttempt(systemPrompt, wstats, chatStart, len(toolCalls), err)
				if errors.Is(err, ErrThinkingLoopDetected) {
					cancel()
					thinkingLoopRetries++
					if thinkingLoopRetries < maxThinkingLoopRetries {
						lastErr = err
						// Re-send the operation so thinking restarts from scratch
						break
					}
					return "", nil, &ThinkingLoopLimitError{Retries: thinkingLoopRetries}
				}
				if errors.Is(err, ErrThinkingTimeoutNoDecision) {
					cancel()
					return "", nil, err
				}
				lastErr = err
			} else {
				a.recordChatAttempt(systemPrompt, wstats, chatStart, len(toolCalls), nil)
				if reply != "" || len(toolCalls) > 0 {
					// A native-only turn (tool calls with blank visible text)
					// is a valid tool turn, not an empty response.
					cancel()
					return reply, toolCalls, nil
				}
			}
			cancel()
			if attempt < MaxRetries {
				select {
				case <-rootCtx.Done():
					return "", nil, rootCtx.Err()
				case <-time.After(time.Duration(1+attempt) * time.Second):
				}
			}
		}

		if isConnectionError(lastErr) && a.side != nil && a.side.Enabled() {
			if reply, toolCalls, err := a.trySidecarAsMain(rootCtx); err == nil {
				if reply != "" || len(toolCalls) > 0 {
					return reply, toolCalls, nil
				}
				lastErr = fmt.Errorf("main model %s unreachable; sidecar returned empty response", shortModelName(a.cfg.Model))
			} else {
				lastErr = fmt.Errorf("main model %s unreachable (%v); sidecar fallback failed: %w", shortModelName(a.cfg.Model), lastErr, err)
			}
		}

		if thinkingLoopRetries > 0 && thinkingLoopRetries < maxThinkingLoopRetries && errors.Is(lastErr, ErrThinkingLoopDetected) {
			select {
			case <-rootCtx.Done():
				return "", nil, rootCtx.Err()
			case <-time.After(500 * time.Millisecond):
			}
			continue
		}
		break
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("empty assistant response")
	}
	return "", nil, lastErr
}

// trySidecarAsMain sends the current turn to the sidecar model as a temporary
// substitute when the main model endpoint is unreachable. It reuses the same
// system prompt and transcript, but disables tools and explicit thinking so a
// small sidecar is more likely to answer. On success the reply is returned as
// if it came from the main model; the next turn will try the main model again.
func (a *Agent) trySidecarAsMain(rootCtx context.Context) (string, []ToolCall, error) {
	sideClient := a.side.ChatClient()
	if sideClient == nil {
		return "", nil, errors.New("sidecar client not available")
	}

	ctx, cancel := context.WithTimeout(rootCtx, a.cfg.EffectiveChatTimeout())
	systemPrompt := a.buildSystemPrompt()
	messages, wstats := a.buildOllamaMessages(ctx, systemPrompt)
	// Usage belongs to the sidecar request; clear it so a failed sidecar
	// attempt never records the previous turn's counters.
	a.lastUsage = nil
	a.ui.StartWaiting(fmt.Sprintf("%s unreachable; using sidecar %s…", shortModelName(a.cfg.Model), shortModelName(a.cfg.SidecarModel)))
	chatStart := time.Now()
	stream, err := sideClient.Chat(ctx, ollama.ChatRequest{
		Model:     a.cfg.SidecarModel,
		Messages:  messages,
		Stream:    true,
		Think:     ollama.ResolveThinkSetting(a.cfg.OllamaThinkLevel, a.cfg.OllamaNoThink, false, false),
		Tools:     nil,
		KeepAlive: a.cfg.KeepAliveFor(config.RoleSidecar),
		Options: ollama.ChatOptions{
			NumCtx:      a.cfg.ContextWindow,
			NumPredict:  a.cfg.MaxTokens,
			Temperature: a.cfg.Temperature,
		},
	})
	if err != nil {
		a.ui.StopWaiting()
		cancel()
		a.recordChatAttempt(systemPrompt, wstats, chatStart, 0, err)
		return "", nil, err
	}
	reply, toolCalls, err := a.streamAssistantResponse(rootCtx, cancel, stream)
	a.recordChatAttempt(systemPrompt, wstats, chatStart, len(toolCalls), err)
	return reply, toolCalls, err
}

// nativeChatTools converts the registry schemas to Ollama function
// declarations. It returns nil when the model is known-unsupported so no
// doomed 400 round trip is attempted; unknown models get tools
// optimistically (the client falls back per session on a 400 tool error).
// Native turns persist structured history (assistant tool_calls + role
// "tool" results); the XML fallback keeps envelope-based text so models
// without native function calling keep working.
func (a *Agent) nativeChatTools() []ollama.ChatTool {
	if a.cfg != nil && a.cfg.ToolsKnown && !a.cfg.ToolsSupported {
		return nil
	}
	if a.reg == nil {
		return nil
	}
	schemas := a.reg.Schemas()
	if len(schemas) == 0 {
		return nil
	}
	out := make([]ollama.ChatTool, 0, len(schemas))
	for _, s := range schemas {
		name := strings.TrimSpace(s.Function.Name)
		if name == "" {
			continue
		}
		out = append(out, ollama.ChatTool{
			Type: "function",
			Function: ollama.ChatToolFunction{
				Name:        name,
				Description: s.Function.Description,
				Parameters:  s.Function.Parameters,
			},
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// nativeCallsToToolCalls maps Ollama function invocations onto the agent's
// internal ToolCall shape (nil arguments become an empty params map).
func nativeCallsToToolCalls(calls []ollama.MessageToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, 0, len(calls))
	for _, c := range calls {
		name := strings.TrimSpace(c.Function.Name)
		if name == "" {
			continue
		}
		params := map[string]any{}
		for k, v := range c.Function.Arguments {
			params[k] = v
		}
		out = append(out, ToolCall{Name: name, Params: params})
	}
	return out
}

// nativeCallNames renders tool names for transcript notes and logs.
func nativeCallNames(calls []ToolCall) string {
	names := make([]string, 0, len(calls))
	for _, c := range calls {
		names = append(names, c.Name)
	}
	return strings.Join(names, ", ")
}

// streamIdleProgressInterval controls how often a silent stream re-shows the
// waiting spinner. The spinner stops once the first tokens arrive, so a model
// that goes quiet mid-turn would otherwise look frozen with no elapsed time
// advancing. Re-showing it proves the agent is still waiting.
var streamIdleProgressInterval = 20 * time.Second

func (a *Agent) streamAssistantResponse(rootCtx context.Context, cancel context.CancelFunc, stream <-chan ollama.Chunk) (string, []ToolCall, error) {
	var buf []byte
	var toolCalls []ollama.MessageToolCall
	var thinkingBuf strings.Builder
	thinkingSeen := false
	thinkingActive := false
	inBandThinking := false
	var thinkingTimer *time.Timer
	lastShown := 0 // bytes of assistant text already streamed to the terminal (hides tool XML)

	stopThinkingTimer := func() {
		if thinkingTimer != nil {
			if !thinkingTimer.Stop() {
				select {
				case <-thinkingTimer.C:
				default:
				}
			}
			thinkingTimer = nil
		}
	}
	defer stopThinkingTimer()

	startThinkingTimer := func() {
		if thinkingTimer != nil {
			return
		}
		if a.decider != nil && a.decider.Enabled() {
			thinkingTimer = time.NewTimer(thinkingCheckInterval)
		} else {
			thinkingTimer = time.NewTimer(thinkingNoDecisionTimeout)
		}
	}

	endThinking := func() {
		if thinkingSeen {
			a.ui.EndThinking()
			thinkingSeen = false
		}
		if thinkingActive {
			thinkingActive = false
			stopThinkingTimer()
		}
	}
	finishAssistant := func() {
		a.ui.StopWaiting()
		endThinking()
		cancel()
		a.ui.EndAssistant(a.turnUsage())
	}
	flushTailAfterTool := func(full string) {
		if tail := assistantTextAfterFirstClosedTool(full); strings.TrimSpace(tail) != "" {
			a.ui.StreamAssistant(tail)
		}
	}
	flushUnprinted := func(full string) {
		rel := toolEnvelopeByteIndex(full)
		var textToPrint string
		if rel >= 0 {
			if rel > lastShown {
				textToPrint = full[lastShown:rel]
			}
		} else {
			if idx, ok := HasPotentialToolStart(full); ok {
				if idx > lastShown {
					textToPrint = full[lastShown:idx]
				}
			} else if len(full) > lastShown {
				textToPrint = full[lastShown:]
			}
		}
		if strings.TrimSpace(textToPrint) != "" {
			a.ui.StreamAssistant(textToPrint)
		}
	}

	waitingLabel := fmt.Sprintf("waiting for %s.", shortModelName(a.cfg.Model))
	idle := time.NewTimer(streamIdleProgressInterval)
	defer idle.Stop()
	resetIdle := func() {
		if !idle.Stop() {
			select {
			case <-idle.C:
			default:
			}
		}
		idle.Reset(streamIdleProgressInterval)
	}
	for {
		var timerChan <-chan time.Time
		if thinkingTimer != nil {
			timerChan = thinkingTimer.C
		}

		select {
		case <-timerChan:
			if thinkingActive {
				if a.decider != nil && a.decider.Enabled() {
					lastParas := extractLastParagraphs(thinkingBuf.String(), thinkingParagraphCount)
					checkCtx, checkCancel := context.WithTimeout(rootCtx, 30*time.Second)
					isLoop, err := a.decider.CheckThinkingLoop(checkCtx, lastParas)
					checkCancel()
					if err == nil && isLoop {
						finishAssistant()
						return "", nil, ErrThinkingLoopDetected
					}
					// Not a loop or decision check failure: continue thinking and check again after interval
					thinkingTimer.Reset(thinkingCheckInterval)
				} else {
					// 15 minutes of continuous thinking exceeded without the decision model: cancel operation without retries
					finishAssistant()
					return "", nil, ErrThinkingTimeoutNoDecision
				}
			}
		case <-idle.C:
			// Silence mid-turn: re-show the spinner with its elapsed
			// counter so the wait is visibly alive. Skipped while a
			// thinking panel is open to avoid garbling its line.
			if !thinkingSeen {
				a.ui.StartWaiting(waitingLabel)
			}
			idle.Reset(streamIdleProgressInterval)
		case chunk, ok := <-stream:
			if !ok {
				a.ui.StopWaiting()
				endThinking()
				if len(buf) == 0 && len(toolCalls) == 0 {
					cancel()
					a.ui.EndAssistant(a.turnUsage())
					return "", nil, nil
				}
				full := string(buf)
				flushUnprinted(full)
				flushTailAfterTool(full)
				cancel()
				a.ui.EndAssistant(a.turnUsage())
				return full, nativeCallsToToolCalls(toolCalls), nil
			}
			resetIdle()
			if chunk.Err != nil {
				if isCancelledByUser(rootCtx, chunk.Err) {
					finishAssistant()
					return "[Cancelled by user]", nil, nil
				}
				a.ui.StopWaiting()
				endThinking()
				a.ui.EndAssistant(a.turnUsage())
				return "", nil, chunk.Err
			}
			// Tool calls may arrive split across chunks (or repeated in the final
			// one): accumulate them instead of keeping only the last chunk.
			toolCalls = ollama.MergeToolCalls(toolCalls, chunk.ToolCalls)
			if chunk.Usage != nil {
				a.lastUsage = chunk.Usage
			}
			if chunk.Thinking != "" {
				thinkingSeen = true
				thinkingBuf.WriteString(chunk.Thinking)
				if !thinkingActive {
					thinkingActive = true
					startThinkingTimer()
				}
				a.ui.StreamThinking(chunk.Thinking)
			}
			if chunk.Delta != "" {
				if inBandThinking {
					closeIdx := -1
					for _, tag := range []string{"</think>", "</thinking>"} {
						if idx := strings.Index(strings.ToLower(chunk.Delta), tag); idx >= 0 {
							if closeIdx < 0 || idx < closeIdx {
								closeIdx = idx + len(tag)
							}
						}
					}
					if closeIdx >= 0 {
						thinkingBuf.WriteString(chunk.Delta[:closeIdx])
						inBandThinking = false
						endThinking()
						a.ui.StopWaiting()
						buf = append(buf, chunk.Delta[closeIdx:]...)
					} else {
						thinkingBuf.WriteString(chunk.Delta)
					}
				} else {
					openIdx := -1
					for _, tag := range []string{"<think>", "<thinking>"} {
						if idx := strings.Index(strings.ToLower(chunk.Delta), tag); idx >= 0 {
							if openIdx < 0 || idx < openIdx {
								openIdx = idx
							}
						}
					}
					if openIdx >= 0 && !strings.Contains(strings.ToLower(chunk.Delta[openIdx:]), "</think>") && !strings.Contains(strings.ToLower(chunk.Delta[openIdx:]), "</thinking>") {
						inBandThinking = true
						if !thinkingActive {
							thinkingActive = true
							startThinkingTimer()
						}
						thinkingBuf.WriteString(chunk.Delta[openIdx:])
						buf = append(buf, chunk.Delta[:openIdx]...)
					} else {
						endThinking()
						a.ui.StopWaiting()
						buf = append(buf, chunk.Delta...)
					}
				}

				// Tool XML is parsed after the stream completes. Until then, show
				// only the natural-language prefix so users do not see raw envelopes.
				rel := toolEnvelopeByteIndex(string(buf))
				end := len(buf)
				if rel >= 0 {
					end = rel
				} else {
					if idx, ok := HasPotentialToolStart(string(buf)); ok {
						end = idx
					}
				}
				if end > lastShown {
					segment := string(buf[lastShown:end])
					if strings.TrimSpace(segment) != "" {
						a.ui.StreamAssistant(segment)
						lastShown = end
					}
					// Whitespace-only deltas (common when a model streams one
					// character at a time) must not advance lastShown: dropping
					// them here would permanently delete the spaces and newlines
					// that separate words and lines, collapsing the answer. They
					// ride along with the next non-whitespace segment instead.
				}
			}
			if chunk.Done {
				full := string(buf)
				flushUnprinted(full)
				flushTailAfterTool(full)
				finishAssistant()
				native := nativeCallsToToolCalls(toolCalls)
				// Native thinking often arrives only in chunk.Thinking; delta can be empty.
				// A native-only tool turn is valid work, not an empty response.
				// Treat that as retryable instead of ending the run with no visible work.
				if strings.TrimSpace(full) == "" && len(native) == 0 {
					return "", nil, fmt.Errorf("empty assistant response (no assistant text or tool call; model may have only emitted thinking)")
				}
				return full, native, nil
			}
		}
	}
}

// xmlToolMode reports whether the model must use the XML <invoke> envelope
// instead of native function calling: when tool support is unknown, when it
// is known-unsupported, or when the client already saw the model reject
// tools at the wire level this session.
func (a *Agent) xmlToolMode() bool {
	if a == nil || a.cfg == nil {
		return false
	}
	cfg := a.cfg
	if !cfg.ToolsKnown || !cfg.ToolsSupported {
		return true
	}
	if u, ok := a.client.(interface{ ToolsUnsupported(model string) bool }); ok && u.ToolsUnsupported(cfg.Model) {
		return true
	}
	return false
}

func (a *Agent) rebuildStableSystemPromptBody() string {
	systemPrompt := prompt.Build(a.cfg)
	// Pinned session context sits right after the base + project
	// instructions: it is explicit user intent, so it outranks auto-loaded
	// guides but never replaces the base prompt. Because it lives in the
	// system message (rebuilt every turn), compaction and transcript
	// truncation can never drop it.
	if ctxBlock := a.getContextBlock(); ctxBlock != "" {
		systemPrompt = systemPrompt + "\n\n" + ctxBlock
	}
	if guidelines := tools.RenderGuidelinesBlock(a.reg, a.isOffline()); guidelines != "" {
		systemPrompt = systemPrompt + "\n\n" + guidelines
	}
	// Native-mode models get their tool schemas in the request's tools field;
	// only XML-mode models need the invocation protocol and text catalogue.
	if a.xmlToolMode() {
		if protocol := tools.RenderXMLProtocolBlock(a.reg); protocol != "" {
			systemPrompt = systemPrompt + "\n\n" + protocol
		}
	}
	if catalog := skills.RenderCatalog(skills.Load(a.cfg)); catalog != "" {
		systemPrompt = systemPrompt + "\n\n" + catalog
	}
	return systemPrompt
}
func (a *Agent) buildSystemPrompt() string {
	a.mu.RLock()
	goalRaw := a.currentGoal
	inPlanMode := a.planMode
	inReviewMode := a.reviewMode
	a.mu.RUnlock()
	goal := strings.TrimSpace(goalRaw)
	offline := a.isOffline()

	key := stableSystemCacheKey(a.cfg, a.reg, a.contextFingerprint(), a.xmlToolMode(), offline)
	missionKey := a.missionCacheKey()

	a.sysPrompt.mu.Lock()
	defer a.sysPrompt.mu.Unlock()

	stableChanged := a.sysPrompt.stableKey != key
	if stableChanged {
		a.sysPrompt.stableKey = key
		a.sysPrompt.stableBody = a.rebuildStableSystemPromptBody()
	}

	if !stableChanged && a.sysPrompt.cacheGoal == goal && a.sysPrompt.cachePlan == inPlanMode && a.sysPrompt.cacheReview == inReviewMode && a.sysPrompt.cacheMission == missionKey && a.sysPrompt.cacheOffline == offline && a.sysPrompt.full != "" {
		return a.sysPrompt.full
	}

	// Order is KV-cache friendly: the stable body first (byte-identical
	// across turns), then the mode toggles, then the mission, and the goal
	// last so the model reads its current instruction closest to the
	// conversation.
	systemPrompt := a.sysPrompt.stableBody
	if inPlanMode {
		systemPrompt = systemPrompt + "\n\n" + prompt.Render("plan_mode", nil)
	}
	if inReviewMode {
		systemPrompt = systemPrompt + "\n\n" + prompt.Render("review_mode", nil)
	}
	if offline {
		systemPrompt = systemPrompt + "\n\n" + prompt.Render("offline_mode", nil)
	}
	if missionBlock := a.missionPromptBlock(); missionBlock != "" {
		systemPrompt = systemPrompt + "\n\n" + missionBlock
	}
	if goal != "" {
		systemPrompt = systemPrompt + "\n\n" + prompt.Render("goal", struct{ Goal string }{Goal: goal})
	}
	a.sysPrompt.cacheGoal = goal
	a.sysPrompt.cachePlan = inPlanMode
	a.sysPrompt.cacheReview = inReviewMode
	a.sysPrompt.cacheMission = missionKey
	a.sysPrompt.cacheOffline = offline
	a.sysPrompt.full = systemPrompt
	return systemPrompt
}
func (a *Agent) tryAutoPullModel(ctx context.Context) bool {
	allow := a.perm.Check("Bash", map[string]any{
		"command": "ollama pull " + a.cfg.Model,
	}, a.ui)
	if !allow {
		return false
	}
	short := shortModelName(a.cfg.Model)
	a.ui.StartWaiting("pulling " + short + "…")
	defer a.ui.StopWaiting()
	pullErr := a.client.Pull(ctx, a.cfg.Model, func(ev ollama.PullEvent) {
		progress := ev.Status
		if ev.Total > 0 {
			progress = fmt.Sprintf("%s (%d/%d)", ev.Status, ev.Completed, ev.Total)
		}
		a.ui.StartWaiting("pulling " + short + " — " + progress)
	})
	return pullErr == nil
}
