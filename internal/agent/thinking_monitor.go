package agent

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"
)

var (
	// Default intervals and counts for thinking supervision.
	defaultThinkingCheckInterval     = 45 * time.Second
	defaultThinkingNoDecisionTimeout = 5 * time.Minute
	defaultThinkingParagraphCount    = 5
	defaultMaxThinkingLoopRetries    = 2

	thinkingCheckInterval     = defaultThinkingCheckInterval
	thinkingNoDecisionTimeout = defaultThinkingNoDecisionTimeout
	thinkingParagraphCount    = defaultThinkingParagraphCount
	maxThinkingLoopRetries    = defaultMaxThinkingLoopRetries
)

func init() {
	if v := os.Getenv("VIBE_THINKING_CHECK_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			thinkingCheckInterval = d
		}
	}
	if v := os.Getenv("VIBE_THINKING_NO_DECISION_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			thinkingNoDecisionTimeout = d
		}
	}
}

var paraRegex = regexp.MustCompile(`\r?\n\s*\r?\n+`)

// extractLastParagraphs extracts the last n paragraphs from the given reasoning text.
// Paragraphs are separated by double newlines (\n\s*\n). If fewer than n
// double-newline paragraphs exist, non-empty lines are used as a fallback.
// If still fewer than n exist, all available paragraphs/lines are returned.
func extractLastParagraphs(text string, n int) string {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" || n <= 0 {
		return ""
	}

	rawParas := paraRegex.Split(trimmed, -1)
	var paras []string
	for _, p := range rawParas {
		p = strings.TrimSpace(p)
		if p != "" {
			paras = append(paras, p)
		}
	}

	// If double-newline split yielded fewer than n paragraphs, check if single lines
	// provide better granularity (e.g. if the model separated thoughts with single newlines).
	if len(paras) < n {
		lines := strings.Split(trimmed, "\n")
		var nonEmptyLines []string
		for _, l := range lines {
			l = strings.TrimSpace(l)
			if l != "" {
				nonEmptyLines = append(nonEmptyLines, l)
			}
		}
		if len(nonEmptyLines) > len(paras) {
			if len(nonEmptyLines) > n {
				nonEmptyLines = nonEmptyLines[len(nonEmptyLines)-n:]
			}
			return strings.Join(nonEmptyLines, "\n\n")
		}
	}

	if len(paras) > n {
		paras = paras[len(paras)-n:]
	}
	return strings.Join(paras, "\n\n")
}

var (
	// ErrThinkingLoopDetected is returned when the decision model detects that the model is in a cyclical loop.
	ErrThinkingLoopDetected = errors.New("thinking loop detected by the decision model")

	// ErrThinkingTimeoutNoDecision is returned when continuous thinking exceeds the safety limit without a decision model enabled.
	ErrThinkingTimeoutNoDecision = errors.New("thinking timeout: exceeded continuous thinking limit without a decision model enabled")

	// ErrThinkingLoopStop marks a turn that ended because the model's
	// reasoning looped repeatedly without producing an answer. Callers use
	// IsThinkingLoopStopErr to pause missions and offer a resumable stop.
	ErrThinkingLoopStop = errors.New("thinking loop stop")
)

// maxThinkingLoopStops caps consecutive ThinkingLoopLimitError stops within
// one Run. Each stop already bought a fresh set of chat attempts, so two
// loops in a row means the model is stuck for this turn.
const maxThinkingLoopStops = 2

// thinkingLoopStopErr builds the resumable stop error for a turn whose
// reasoning kept looping past maxThinkingLoopStops.
func thinkingLoopStopErr(stops int) error {
	return fmt.Errorf("%w: the model's reasoning looped %d times this turn without reaching an answer. Nothing was lost: press Enter to continue, type a correction instead, or lower the thinking level (--think low or --no-think)", ErrThinkingLoopStop, stops)
}

// ThinkingLoopLimitError is returned when a cyclical thinking loop has failed 3 consecutive times in the same step.
type ThinkingLoopLimitError struct {
	Retries int
}

func (e *ThinkingLoopLimitError) Error() string {
	return fmt.Sprintf("thinking loop limit exceeded: caught in a cyclical loop %d consecutive times", e.Retries)
}

// IsThinkingLoopLimitErr reports whether err is caused by exceeding the thinking loop retries limit.
func IsThinkingLoopLimitErr(err error) bool {
	var target *ThinkingLoopLimitError
	return errors.As(err, &target)
}

// IsThinkingTimeoutNoDecisionErr reports whether err is a 15-minute thinking timeout without a decision model enabled.
func IsThinkingTimeoutNoDecisionErr(err error) bool {
	return errors.Is(err, ErrThinkingTimeoutNoDecision)
}

// IsThinkingLoopStopErr reports whether err is the repeated thinking-loop stop returned by Run.
func IsThinkingLoopStopErr(err error) bool {
	return err != nil && errors.Is(err, ErrThinkingLoopStop)
}
