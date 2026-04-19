package permissions

import (
	"regexp"
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

type Tier int

const (
	TierSafe Tier = iota
	TierAsk
	TierNetwork
)

type prompter interface {
	AskPermission(tool string, params map[string]any) tui.Decision
}

type Manager struct {
	mu sync.Mutex

	yesMode bool
	allow   map[string]struct{}
	deny    map[string]struct{}
}

var (
	safeTools = map[string]struct{}{
		"read": {}, "glob": {}, "grep": {}, "subagent": {}, "askuserquestion": {},
		"taskcreate": {}, "tasklist": {}, "taskget": {}, "taskupdate": {},
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

func NewManager(cfg *config.Config) *Manager {
	return &Manager{
		yesMode: cfg.YesMode,
		allow:   map[string]struct{}{},
		deny:    map[string]struct{}{},
	}
}

func (m *Manager) SetYesMode(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.yesMode = on
}

func (m *Manager) AllowAll(tool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.deny, normalizeTool(tool))
	m.allow[normalizeTool(tool)] = struct{}{}
}

func (m *Manager) DenyAll(tool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.allow, normalizeTool(tool))
	m.deny[normalizeTool(tool)] = struct{}{}
}

func (m *Manager) Check(toolName string, params map[string]any, ui prompter) bool {
	tool := normalizeTool(toolName)

	m.mu.Lock()
	_, denied := m.deny[tool]
	if denied {
		m.mu.Unlock()
		return false
	}

	_, allowed := m.allow[tool]
	yesMode := m.yesMode
	m.mu.Unlock()

	if toolTier(tool) == TierSafe {
		return true
	}
	if allowed {
		return true
	}

	if yesMode {
		if tool == "bash" {
			cmd, _ := params["command"].(string)
			if needsAlwaysConfirmBash(cmd) {
				// Keep prompting below.
			} else {
				return true
			}
		} else {
			return true
		}
	}

	if ui == nil {
		return false
	}
	decision := ui.AskPermission(toolName, params)
	switch decision {
	case tui.DecisionAllowOnce:
		return true
	case tui.DecisionAllowAll:
		m.AllowAll(tool)
		return true
	case tui.DecisionDenyAll:
		m.DenyAll(tool)
		return false
	case tui.DecisionYesMode:
		m.SetYesMode(true)
		return true
	default:
		return false
	}
}

func toolTier(tool string) Tier {
	if _, ok := networkTools[tool]; ok {
		return TierNetwork
	}
	if _, ok := askTools[tool]; ok {
		return TierAsk
	}
	if _, ok := safeTools[tool]; ok {
		return TierSafe
	}
	return TierAsk
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
