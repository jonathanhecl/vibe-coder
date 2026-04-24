package permissions

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

const legacyPermissionsFile = "permissions.json"

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

	// permissionCancelled is set when the user picks Cancel in the permission UI.
	permissionCancelled bool
}

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

// AllowSession remembers allow for this process only (not written to disk).
func (m *Manager) AllowSession(tool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	norm := normalizeTool(tool)
	delete(m.deny, norm)
	m.allow[norm] = struct{}{}
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

// DenySession blocks the tool for this process only (not written to disk).
func (m *Manager) DenySession(tool string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	norm := normalizeTool(tool)
	delete(m.allow, norm)
	m.deny[norm] = struct{}{}
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

// WasCancelled reports whether the last Check ended with an explicit Cancel choice.
func (m *Manager) WasCancelled() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.permissionCancelled
}

func (m *Manager) Check(toolName string, params map[string]any, ui prompter) bool {
	tool := normalizeTool(toolName)

	m.mu.Lock()
	m.permissionCancelled = false
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
	case tui.DecisionAllowSession:
		m.AllowSession(tool)
		return true
	case tui.DecisionAllowPersistent:
		m.AllowAll(tool)
		return true
	case tui.DecisionDenySession:
		m.DenySession(tool)
		return false
	case tui.DecisionDenyPersistent:
		m.DenyAll(tool)
		return false
	case tui.DecisionYesMode:
		m.SetYesMode(true)
		return true
	case tui.DecisionCancel:
		m.mu.Lock()
		m.permissionCancelled = true
		m.mu.Unlock()
		return false
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
		m.tryLoadLegacyPermissionsFile()
		return
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return
	}
	data, err := os.ReadFile(m.file)
	if err != nil {
		m.tryLoadLegacyPermissionsFile()
		return
	}
	if p, ok := config.ParseToolPermissionsFromEnvContent(data); ok {
		m.mergePersistentMap(p)
	} else if raw := parseLegacyJSONPermissionsFile(data); len(raw) > 0 {
		// Standalone JSON file (old layout or misnamed config).
		m.mergePersistentMap(raw)
	}
	if !strings.Contains(string(data), config.ToolPermissionsKey+"=") {
		m.tryLoadLegacyPermissionsFile()
	}
}

func parseLegacyJSONPermissionsFile(data []byte) map[string]string {
	trim := strings.TrimSpace(string(data))
	if !strings.HasPrefix(trim, "{") {
		return nil
	}
	raw := map[string]string{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	return raw
}

func (m *Manager) mergePersistentMap(raw map[string]string) {
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

// tryLoadLegacyPermissionsFile loads permissions.json next to config.env once,
// so existing installs keep their rules until the next save (which writes TOOL_PERMISSIONS).
func (m *Manager) tryLoadLegacyPermissionsFile() {
	if strings.TrimSpace(m.file) == "" {
		return
	}
	legacy := filepath.Join(filepath.Dir(m.file), legacyPermissionsFile)
	info, err := os.Lstat(legacy)
	if err != nil || info.Mode()&os.ModeSymlink != 0 {
		return
	}
	data, err := os.ReadFile(legacy)
	if err != nil {
		return
	}
	raw := map[string]string{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return
	}
	if len(raw) == 0 {
		return
	}
	m.mergePersistentMap(raw)
}

func (m *Manager) savePersistentLocked() error {
	if strings.TrimSpace(m.file) == "" {
		return nil
	}
	out := map[string]string{}
	for k, v := range m.persistent {
		if k == "bash" && v == "allow" {
			continue
		}
		out[k] = v
	}
	if err := config.UpsertToolPermissions(m.file, out); err != nil {
		return err
	}
	// One-time migration: stop using a separate permissions.json next to config.
	legacy := filepath.Join(filepath.Dir(m.file), legacyPermissionsFile)
	if st, err := os.Lstat(legacy); err == nil && !st.IsDir() {
		_ = os.Remove(legacy)
	}
	return nil
}
