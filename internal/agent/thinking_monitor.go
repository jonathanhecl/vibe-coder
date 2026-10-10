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
	defaultThinkingCheckInterval  = 2 * time.Minute
	defaultThinkingNoJevTimeout   = 15 * time.Minute
	defaultThinkingParagraphCount = 5
	defaultMaxThinkingLoopRetries = 3

	thinkingCheckInterval  = defaultThinkingCheckInterval
	thinkingNoJevTimeout   = defaultThinkingNoJevTimeout
	thinkingParagraphCount = defaultThinkingParagraphCount
	maxThinkingLoopRetries = defaultMaxThinkingLoopRetries
)

func init() {
	if v := os.Getenv("VIBE_THINKING_CHECK_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			thinkingCheckInterval = d
		}
	}
	if v := os.Getenv("VIBE_THINKING_NO_JEV_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			thinkingNoJevTimeout = d
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
	// ErrThinkingLoopDetected is returned when JEV detects that the model is in a cyclical loop.
	ErrThinkingLoopDetected = errors.New("thinking loop detected by JEV")

	// ErrThinkingTimeoutNoJev is returned when continuous thinking exceeds 15 minutes without JEV enabled.
	ErrThinkingTimeoutNoJev = errors.New("thinking timeout: exceeded 15 minutes of thinking without JEV enabled")
)

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

// IsThinkingTimeoutNoJevErr reports whether err is a 15-minute thinking timeout without JEV enabled.
func IsThinkingTimeoutNoJevErr(err error) bool {
	return errors.Is(err, ErrThinkingTimeoutNoJev)
}
