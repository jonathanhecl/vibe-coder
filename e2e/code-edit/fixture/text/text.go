package text

import "strings"

// Normalize trims surrounding whitespace and returns the text in UPPER case.
func Normalize(s string) string {
	return strings.TrimSpace(strings.ToLower(s))
}
