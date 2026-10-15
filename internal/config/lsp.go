package config

import (
	"strings"
	"time"
)

// Language-server integration settings. The integration reports diagnostics for
// an edited file (or on demand through the Diagnostics tool) from the language
// server of the file's language, when one is installed.
const (
	// LSPAuto runs the integration where a server resolves for the workspace.
	LSPAuto = "auto"
	// LSPOn also reports a workspace where nothing resolves at startup, because
	// the user asked for it.
	LSPOn = "on"
	// LSPOff disables the integration, the tool and the automatic path alike.
	LSPOff = "off"
)

const (
	defaultLSPEnabled = LSPAuto
	defaultLSPTimeout = 5000
	minLSPTimeout     = 250
	maxLSPTimeout     = 60000
)

// LSPConfig centralizes the language-server settings so they can be set from
// the config file, the environment or the CLI instead of being hardcoded.
// Precedence: defaults < config file < env < CLI.
type LSPConfig struct {
	// Enabled is "auto" (default), "on" or "off"; anything else normalizes to
	// "auto". It gates the tool and the automatic post-edit path together, so
	// there is one switch to explain rather than two.
	Enabled string
	// Servers overrides the command for a language family, keyed by family
	// name ("go", "python", "rust", "typescript") with the command and its
	// arguments. A family listed here needs no workspace marker: asking for one
	// is reason enough.
	Servers map[string][]string
	// AfterEdit records diagnostics for a file that was just written (default
	// true).
	AfterEdit bool
	// TimeoutMS bounds the wait for the server's report, in milliseconds
	// (default 5000, clamped to 250..60000). A wait that times out is never
	// reported as a clean file.
	TimeoutMS int
}

// normalize folds the settings into their documented ranges. It runs after
// every source is applied, so a CLI value is clamped like any other.
func (l *LSPConfig) normalize() {
	l.Enabled = normalizeLSPEnabled(l.Enabled)
	if len(l.Servers) == 0 {
		l.Servers = nil
	}
	switch {
	case l.TimeoutMS <= 0:
		l.TimeoutMS = defaultLSPTimeout
	case l.TimeoutMS < minLSPTimeout:
		l.TimeoutMS = minLSPTimeout
	case l.TimeoutMS > maxLSPTimeout:
		l.TimeoutMS = maxLSPTimeout
	}
}

// normalizeLSPEnabled folds the accepted spellings of the switch, so a user
// typing "yes", "TRUE" or "disabled" is understood instead of silently falling
// back to the default.
func normalizeLSPEnabled(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case LSPAuto, "", "default":
		return LSPAuto
	case LSPOn, "true", "yes", "1", "enable", "enabled":
		return LSPOn
	case LSPOff, "false", "no", "0", "disable", "disabled", "none":
		return LSPOff
	default:
		return LSPAuto
	}
}

// Requested reports whether the settings allow the integration to run at all.
// "auto" and "on" both allow it: the servers that resolve decide the rest.
func (l LSPConfig) Requested() bool { return l.Enabled != LSPOff }

// Demanded distinguishes an explicit "on" from "auto": the user asked for the
// integration, so a workspace where nothing resolves is worth reporting at
// startup instead of passing in silence.
func (l LSPConfig) Demanded() bool { return l.Enabled == LSPOn }

// Timeout is the bounded wait for a publication, as a duration.
func (l LSPConfig) Timeout() time.Duration { return time.Duration(l.TimeoutMS) * time.Millisecond }

// parseLSPServers reads an LSP_SERVERS value such as "go:gopls, py:ruff server".
// Each entry is a family, a colon and the command with its arguments; an entry
// without a command is ignored rather than guessed at. What actually resolved
// is what /status reports, so a typo shows up there.
func parseLSPServers(value string) map[string][]string {
	var servers map[string][]string
	for _, entry := range strings.Split(value, ",") {
		family, command, ok := strings.Cut(entry, ":")
		if !ok {
			continue
		}
		family = strings.ToLower(strings.TrimSpace(family))
		argv := strings.Fields(command)
		if family == "" || len(argv) == 0 {
			continue
		}
		if servers == nil {
			servers = map[string][]string{}
		}
		servers[family] = argv
	}
	return servers
}
