package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

func errResult(msg string) Result {
	return Result{
		Output:  msg,
		IsError: true,
	}
}

// Parse-error keys preserved when tool arguments are not valid JSON.
// They use a __ prefix so they never collide with real tool params.
const (
	ParseErrorKey = "__parse_error"
	RawArgsKey    = "__raw_arguments"
)

// AttemptedArgsMaxRunes bounds how much of the marshalled params we echo
// back on error: enough to see what the model got wrong, never a 30KB dump.
const AttemptedArgsMaxRunes = 500

// RawArgsMaxRunes bounds the raw argument string kept when JSON parsing
// fails. The full 30KB still lives in the log; the model and the terminal
// only get the head.
const RawArgsMaxRunes = 2000

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(runes[:max-1]) + "…"
}

// AttemptedArgsSummary renders what the model actually sent, truncated.
// Empty when there is nothing to show so callers can skip enrichment.
func AttemptedArgsSummary(params map[string]any) string {
	if len(params) == 0 {
		return "attempted args: (none)"
	}
	raw, err := json.Marshal(params)
	if err != nil {
		raw = []byte(fmt.Sprint(params))
	}
	full := string(raw)
	if len(raw) == 0 {
		return "attempted args: (none)"
	}
	return fmt.Sprintf("attempted args (%d bytes): %s", len(raw), truncateRunes(full, AttemptedArgsMaxRunes))
}

// MissingParamError reports a required param plus what was actually received,
// so the failure tells the model (and the user) what went wrong.
func MissingParamError(param string, params map[string]any) Result {
	out := fmt.Sprintf("%s is required", param)
	if summary := AttemptedArgsSummary(params); summary != "" {
		out += "\n" + summary
	}
	return Result{
		Output:  out,
		IsError: true,
		HintsForModel: fmt.Sprintf("Retry the call with a valid %q. Do not retry the same arguments.", param),
	}
}

// InvalidParamError reports a wrongly-typed param plus what was received.
func InvalidParamError(msg string, params map[string]any) Result {
	out := msg
	if summary := AttemptedArgsSummary(params); summary != "" {
		out += "\n" + summary
	}
	return Result{
		Output:  out,
		IsError: true,
	}
}

// TruncateRawArgs keeps the head of a raw JSON argument string for error
// reports. The byte count always refers to the original.
func TruncateRawArgs(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return "(empty)"
	}
	if len([]rune(raw)) <= RawArgsMaxRunes {
		return raw
	}
	return fmt.Sprintf("%s…[truncated %d bytes total]", truncateRunes(raw, RawArgsMaxRunes), len(raw))
}

func isIgnoredSearchDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "dist", "build", "target", ".vibe-coder":
		return true
	default:
		return false
	}
}
