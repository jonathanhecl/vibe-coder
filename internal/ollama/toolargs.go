package ollama

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Parse-error keys preserved when tool arguments are not valid JSON.
// They use a __ prefix so they never collide with real tool params.
const (
	ParseErrorKey = "__parse_error"
	RawArgsKey    = "__raw_arguments"
)

// ParseToolArguments parses a JSON object string representing tool call
// arguments. If strict decoding fails, it performs safe deterministic repairs
// (stripping markdown code fences, escaping raw control characters and invalid
// backslash escapes inside string literals, and removing trailing commas before
// existing closing delimiters).
//
// Truncated JSON payloads (missing closing quotes or braces) are intentionally
// NOT auto-closed so upstream truncation can be detected and repaired via
// schema-constrained decoding or reported as a parse error.
func ParseToolArguments(raw string) (map[string]any, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, errors.New("empty tool call arguments")
	}

	if out, err := unmarshalObjectStrict(trimmed); err == nil {
		return out, nil
	} else {
		candidate := stripMarkdownCodeFence(trimmed)
		if candidate != trimmed {
			if out, cerr := unmarshalObjectStrict(candidate); cerr == nil {
				return out, nil
			}
		}

		repaired := stripTrailingCommas(repairJSONStringLiterals(candidate))
		if repaired != candidate {
			if out, rerr := unmarshalObjectStrict(repaired); rerr == nil {
				return out, nil
			}
		}
		return nil, err
	}
}

func unmarshalObjectStrict(s string) (map[string]any, error) {
	var out map[string]any
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return nil, err
	}
	if out == nil {
		return nil, errors.New("tool call arguments must be a JSON object")
	}
	return out, nil
}

func stripMarkdownCodeFence(s string) string {
	trimmed := strings.TrimSpace(s)
	if !strings.HasPrefix(trimmed, "```") {
		return trimmed
	}
	nl := strings.IndexByte(trimmed, '\n')
	if nl < 0 {
		return trimmed
	}
	body := trimmed[nl+1:]
	bodyTrimmed := strings.TrimSpace(body)
	if strings.HasSuffix(bodyTrimmed, "```") {
		bodyTrimmed = strings.TrimSpace(strings.TrimSuffix(bodyTrimmed, "```"))
		return bodyTrimmed
	}
	return trimmed
}

// repairJSONStringLiterals escapes raw control characters (\n, \r, \t, etc.) and
// invalid backslash escapes (such as Windows paths like "C:\Users\...") inside
// JSON string literals while leaving valid escapes and bytes outside strings
// untouched.
func repairJSONStringLiterals(raw string) string {
	var b strings.Builder
	b.Grow(len(raw) + 16)
	inString := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if !inString {
			if c == '"' {
				inString = true
			}
			b.WriteByte(c)
			continue
		}
		if c == '"' {
			inString = false
			b.WriteByte(c)
			continue
		}
		if c == '\\' {
			if i+1 >= len(raw) {
				b.WriteString(`\\`)
				continue
			}
			n := raw[i+1]
			if strings.IndexByte(`"\/bfnrt`, n) >= 0 {
				b.WriteByte(c)
				b.WriteByte(n)
				i++
				continue
			}
			if n == 'u' && i+5 < len(raw) && isHex4(raw[i+2:i+6]) {
				b.WriteString(raw[i : i+6])
				i += 5
				continue
			}
			b.WriteString(`\\`)
			continue
		}
		switch c {
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if c < 0x20 {
				fmt.Fprintf(&b, `\u%04x`, c)
			} else {
				b.WriteByte(c)
			}
		}
	}
	return b.String()
}

// stripTrailingCommas removes commas that are followed only by JSON whitespace
// and a closing '}' or ']' outside string literals.
func stripTrailingCommas(raw string) string {
	var b strings.Builder
	b.Grow(len(raw))
	inString := false
	escaped := false
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if inString {
			b.WriteByte(c)
			if escaped {
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			continue
		}
		if c == '"' {
			inString = true
			b.WriteByte(c)
			continue
		}
		if c == ',' {
			j := i + 1
			for j < len(raw) && (raw[j] == ' ' || raw[j] == '\t' || raw[j] == '\n' || raw[j] == '\r') {
				j++
			}
			if j < len(raw) && (raw[j] == '}' || raw[j] == ']') {
				continue
			}
		}
		b.WriteByte(c)
	}
	return b.String()
}

func isHex4(s string) bool {
	if len(s) != 4 {
		return false
	}
	for i := 0; i < 4; i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}
