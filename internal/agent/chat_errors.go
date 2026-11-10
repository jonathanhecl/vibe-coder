package agent

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// ErrContextOverflow marks a chat request that does not fit the model's
// context window even after the conversation was compacted. It is a terminal
// condition for the turn: retrying the same request can never succeed, so
// the run stops with advice instead of burning the retry budget.
var ErrContextOverflow = errors.New("context window exceeded")

// IsContextOverflowErr reports whether err is the terminal context-overflow
// stop.
func IsContextOverflowErr(err error) bool {
	return err != nil && errors.Is(err, ErrContextOverflow)
}

// contextOverflowNeedles are the provider error phrasings that mean the
// prompt did not fit the context window. Errors arrive as plain strings:
// Ollama's mapChatError returns "Context window exceeded" or "http %d: body",
// and the OpenAI-compatible client returns "openai chat failed (400 ...): body".
var contextOverflowNeedles = []string{
	"context window exceeded",
	"context length",
	"context_length_exceeded",
	"exceeds the available context size",
	"exceed_context_size",
	"maximum context length",
	"prompt is too long",
	"too many tokens",
}

func isContextOverflowError(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	for _, needle := range contextOverflowNeedles {
		if strings.Contains(msg, needle) {
			return true
		}
	}
	return false
}

// chatStatusRe extracts an HTTP status code from the error shapes the chat
// clients produce: "http 400: ..." (Ollama) and "failed (400 Bad Request): ..."
// (OpenAI-compatible).
var chatStatusRe = regexp.MustCompile(`(?i)(?:http |failed \()(4\d\d)\b`)

// isNonRetryableChatError reports whether err is a 4xx response that retrying
// cannot fix (bad request, auth, not found). 408 and 429 stay retryable:
// they are transient by definition. Context-overflow errors are handled
// separately and always return false here.
func isNonRetryableChatError(err error) bool {
	if err == nil || isContextOverflowError(err) {
		return false
	}
	m := chatStatusRe.FindStringSubmatch(err.Error())
	if m == nil {
		return false
	}
	switch m[1] {
	case "408", "429":
		return false
	}
	return true
}

// contextOverflowFatalErr builds the terminal error for an overflow that
// compaction could not fix.
func contextOverflowFatalErr(cfg *config.Config, err error) error {
	window := 0
	if cfg != nil {
		window = cfg.ContextWindow
	}
	return fmt.Errorf("%w: the request does not fit the model context window (%d tokens) even after compacting the conversation; lower --max-tokens, raise --context-window, or start a fresh session with /new (%v)",
		ErrContextOverflow, window, err)
}

// classifyChatError inspects a failed chat attempt. retry reports that the
// attempt should be re-run immediately after a successful mid-turn
// compaction; fatal is a non-nil terminal error. Anything else is an ordinary
// retryable failure the caller records as lastErr.
func (a *Agent) classifyChatError(rootCtx context.Context, overflowCompacted *bool, err error) (fatal error, retry bool) {
	if isContextOverflowError(err) {
		if *overflowCompacted {
			return contextOverflowFatalErr(a.cfg, err), false
		}
		*overflowCompacted = true
		a.ui.ShowToolResult("RECOVERY", "context window exceeded; compacting the conversation and retrying", false, nil)
		var gen uint64
		if a.sess != nil {
			gen = a.sess.Generation()
		}
		if cerr := a.CompactNow(rootCtx); cerr == nil && a.sess != nil && a.sess.Generation() != gen {
			return nil, true
		}
		return contextOverflowFatalErr(a.cfg, err), false
	}
	if isNonRetryableChatError(err) {
		return err, false
	}
	return nil, false
}
