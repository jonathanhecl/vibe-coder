package permissions

import (
	"regexp"
	"strings"
	"sync"
)

const legacyPermissionsFile = "permissions.json"

type Tier int

const (
	TierSafe Tier = iota
	TierAsk
	TierNetwork
)

var (
	safeTools = map[string]struct{}{
		"read": {}, "glob": {}, "grep": {}, "subagent": {}, "askuserquestion": {},
		"taskcreate": {}, "tasklist": {}, "taskget": {}, "taskupdate": {}, "todowrite": {},
	}
	askTools = map[string]struct{}{
		"bash": {}, "write": {}, "edit": {},
	}
	networkTools = map[string]struct{}{
		"webfetch": {}, "websearch": {},
	}
	alwaysConfirmBash = []*regexp.Regexp{
		regexp.MustCompile(`(?i)\brm\s+-rf\s+/`),
		regexp.MustCompile(`(?i)\bsudo\b`),
		regexp.MustCompile(`(?i)\bmkfs\b`),
		regexp.MustCompile(`(?i)\bdd\b.*\bof=/dev/`),
	}
)

var (
	askToolsMu sync.RWMutex
)

func toolTier(tool string) Tier {
	askToolsMu.RLock()
	_, ask := askTools[tool]
	askToolsMu.RUnlock()

	if _, ok := networkTools[tool]; ok {
		return TierNetwork
	}
	if ask {
		return TierAsk
	}
	if _, ok := safeTools[tool]; ok {
		return TierSafe
	}
	return TierAsk
}

func (m *Manager) AddAskTool(tool string) {
	askToolsMu.Lock()
	defer askToolsMu.Unlock()
	askTools[normalizeTool(tool)] = struct{}{}
}

func needsAlwaysConfirmBash(command string) bool {
	for _, pattern := range alwaysConfirmBash {
		if pattern.MatchString(command) {
			return true
		}
	}
	return false
}

func normalizeTool(tool string) string {
	return strings.ToLower(strings.TrimSpace(tool))
}
