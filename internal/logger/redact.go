package logger

import (
	"regexp"
	"strings"
)

// redactedValue replaces a secret value in log output.
const redactedValue = "[REDACTED]"

// sensitiveKeyMarkers flags an assignment key as secret-bearing. It mirrors
// the sensitive substrings used for environment scrubbing.
var sensitiveKeyMarkers = []string{
	"SECRET", "TOKEN", "KEY", "PASSWORD", "PASSWD", "CREDENTIAL",
	"API_KEY", "AUTH", "PRIVATE",
}

// RedactArgs returns a copy of args safe for log output. Values passed via
// --env (both "--env KEY=VALUE" and "--env=KEY=VALUE" forms), values of
// KEY=VALUE assignments with secret-looking keys, and credentials embedded
// in URLs (scheme://user:pass@host) are replaced with [REDACTED].
func RedactArgs(args []string) []string {
	out := make([]string, len(args))
	prevBareEnv := false
	for i, arg := range args {
		if prevBareEnv {
			out[i] = redactedValue
			prevBareEnv = false
			continue
		}
		if arg == "--env" {
			out[i] = arg
			prevBareEnv = true
			continue
		}
		out[i] = redactSingleArg(arg)
	}
	return out
}

func redactSingleArg(arg string) string {
	// --env=KEY=VALUE form: redact everything after the flag prefix.
	if strings.HasPrefix(arg, "--env=") {
		return "--env=" + redactedValue
	}
	// Credentials embedded in URLs: scheme://user:pass@host.
	if redacted, ok := redactURLCredentials(arg); ok {
		return redacted
	}
	// KEY=VALUE assignments with secret-looking keys. Strip leading dashes
	// so --token=abc is caught as well.
	key, _, ok := strings.Cut(arg, "=")
	if !ok {
		return arg
	}
	upper := strings.ToUpper(strings.TrimLeft(key, "-"))
	for _, marker := range sensitiveKeyMarkers {
		if strings.Contains(upper, marker) {
			return key + "=" + redactedValue
		}
	}
	return arg
}

var (
	// sensitiveAssignment finds KEY=VALUE pairs whose key looks secret-bearing,
	// anywhere in a free-form string (e.g. a mission goal or log message).
	sensitiveAssignment = regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:SECRET|TOKEN|KEY|PASSWORD|PASSWD|CREDENTIAL|AUTH|PRIVATE)[A-Z0-9_]*)\s*=\s*\S+`)
	// urlCredentials finds userinfo in scheme://user:pass@host inside text.
	urlCredentials = regexp.MustCompile(`([A-Za-z][A-Za-z0-9+.-]*://)[^/@\s]+@`)
)

// RedactText masks secret-looking KEY=VALUE assignments and URL credentials
// inside a free-form string. Used for persisted run logs, which must never
// store secrets.
func RedactText(s string) string {
	if s == "" {
		return s
	}
	s = sensitiveAssignment.ReplaceAllString(s, "$1="+redactedValue)
	s = urlCredentials.ReplaceAllString(s, "$1"+redactedValue+"@")
	return s
}

// redactURLCredentials masks userinfo in scheme://user:pass@host arguments.
func redactURLCredentials(arg string) (string, bool) {
	schemeIdx := strings.Index(arg, "://")
	if schemeIdx < 0 {
		return arg, false
	}
	rest := arg[schemeIdx+3:]
	atIdx := strings.LastIndex(rest, "@")
	if atIdx < 0 {
		return arg, false
	}
	if !strings.Contains(rest[:atIdx], ":") {
		return arg, false
	}
	return arg[:schemeIdx+3] + redactedValue + rest[atIdx:], true
}
