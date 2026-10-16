package permissions

import (
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
		"read": {}, "readmany": {}, "glob": {}, "grep": {}, "subagent": {}, "askuserquestion": {},
		"taskcreate": {}, "tasklist": {}, "taskget": {}, "taskupdate": {}, "todowrite": {},
		"missionstart": {}, "missioncomplete": {}, "missionblocked": {},
		"describeimage": {}, "screenshot": {}, "skill": {}, "gitstatus": {}, "gitdiff": {}, "decide": {},
		"backgroundstatus": {}, "backgroundlist": {}, "backgroundkill": {},
		"terminatesession": {},
	}
	askTools = map[string]struct{}{
		"bash": {}, "interactivebash": {}, "sendinput": {}, "write": {}, "edit": {}, "gitundo": {},
		"backgroundrun": {},
	}
	networkTools = map[string]struct{}{
		"webfetch": {}, "websearch": {}, "httprequest": {},
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

func normalizeTool(tool string) string {
	trimmed := strings.TrimSpace(tool)
	if strings.HasPrefix(trimmed, "out_domain:") {
		return "out_domain:" + strings.TrimPrefix(trimmed, "out_domain:")
	}
	return strings.ToLower(trimmed)
}

// isArbitraryCommandTool reports whether a tool can execute arbitrary shell commands
// and therefore must never be granted persistent "allow" permissions.
func isArbitraryCommandTool(tool string) bool {
	switch normalizeTool(tool) {
	case "bash", "interactivebash", "sendinput", "backgroundrun":
		return true
	}
	return false
}
