package logger

import (
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
