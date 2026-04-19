package permissions

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	file    string

	persistent map[string]string
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

var (
	askToolsMu sync.RWMutex
)

func NewManager(cfg *config.Config) *Manager {
	m := &Manager{
		yesMode:    cfg.YesMode,
		allow:      map[string]struct{}{},
		deny:       map[string]struct{}{},
		file:       cfg.PermFile,
		persistent: map[string]string{},
	}
	m.loadPersistent()
	return m
}

func (m *Manager) SetYesMode(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.yesMode = on
}

func (m *Manager) AllowAll(tool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	norm := normalizeTool(tool)
	delete(m.deny, norm)
	m.allow[norm] = struct{}{}
	if norm != "bash" {
		m.persistent[norm] = "allow"
		_ = m.savePersistentLocked()
	}
}

func (m *Manager) DenyAll(tool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	norm := normalizeTool(tool)
	delete(m.allow, norm)
	m.deny[norm] = struct{}{}
	m.persistent[norm] = "deny"
	_ = m.savePersistentLocked()
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
	pRule := m.persistent[tool]
	m.mu.Unlock()

	if toolTier(tool) == TierSafe {
		return true
	}
	if allowed {
		return true
	}
	if pRule == "allow" {
		return true
	}
	if pRule == "deny" {
		return false
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

func (m *Manager) loadPersistent() {
	if strings.TrimSpace(m.file) == "" {
		return
	}
	info, err := os.Lstat(m.file)
	if err != nil {
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return
	}
	data, err := os.ReadFile(m.file)
	if err != nil {
		return
	}
	raw := map[string]string{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	for k, v := range raw {
		k = normalizeTool(k)
		v = strings.ToLower(strings.TrimSpace(v))
		if k == "bash" && v == "allow" {
			continue
		}
		if v != "allow" && v != "deny" {
			continue
		}
		m.persistent[k] = v
	}
}

func (m *Manager) savePersistentLocked() error {
	if strings.TrimSpace(m.file) == "" {
		return nil
	}
	parent := filepath.Dir(m.file)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return err
	}
	out := map[string]string{}
	for k, v := range m.persistent {
		if k == "bash" && v == "allow" {
			continue
		}
		out[k] = v
	}
	raw, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(parent, "*.permissions.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpPath)
		return err
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Chmod(tmpPath, 0o600); err != nil {
		_ = os.Remove(tmpPath)
		return err
	}
	if err := os.Rename(tmpPath, m.file); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("save permissions: %w", err)
	}
	return nil
}
