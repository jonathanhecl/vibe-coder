package permissions

import (
	"context"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

type prompter interface {
	AskPermission(tool string, params map[string]any) tui.Decision
}

// SafetyDecider evaluates whether a shell command is dangerous or safe to run automatically.
type SafetyDecider interface {
	IsCommandDangerous(ctx context.Context, command string) (bool, error)
	Enabled() bool
}

type Manager struct {
	mu sync.Mutex

	yesMode       bool
	assistedMode  bool
	safetyDecider SafetyDecider
	allow         map[string]struct{}
	deny          map[string]struct{}
	file          string

	persistent map[string]string

	// unattended is set while an agent-managed mission is active. It auto-approves
	// Ask/Network tools so a long unattended run never blocks on a prompt, but it
	// never bypasses mandatory dangerous-command confirmations: those are denied
	// outright instead of prompted, so the run continues and the agent can adapt.
	// It does not change the user's own yes-mode setting.
	unattended bool

	// permissionCancelled is set when the user picks Cancel in the permission UI.
	permissionCancelled bool
}

func NewManager(cfg *config.Config) *Manager {
	var yesMode, assistedMode bool
	var permFile string
	if cfg != nil {
		yesMode = cfg.YesMode
		if cfg.JevstyleInUse() {
			assistedMode = cfg.AssistedYes
		}
		permFile = cfg.PermFile
	}
	m := &Manager{
		yesMode:      yesMode,
		assistedMode: assistedMode,
		allow:        map[string]struct{}{},
		deny:         map[string]struct{}{},
		file:         permFile,
		persistent:   map[string]string{},
	}
	m.loadPersistent()
	return m
}

func (m *Manager) SetYesMode(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.yesMode = on
}

// SetAssistedMode toggles assisted execution mode. When enabled, safe shell commands
// are auto-approved by a SafetyDecider, while dangerous ones prompt the user.
func (m *Manager) SetAssistedMode(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.assistedMode = on
}

// AssistedMode reports whether assisted execution mode is active.
func (m *Manager) AssistedMode() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.assistedMode
}

// SetSafetyDecider attaches a decision function model for command safety evaluation.
func (m *Manager) SetSafetyDecider(d SafetyDecider) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.safetyDecider = d
}

// SafetyDecider returns the currently attached safety decider.
func (m *Manager) SafetyDecider() SafetyDecider {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.safetyDecider
}

// SetUnattended toggles unattended auto-approval, used while a mission is active.
func (m *Manager) SetUnattended(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.unattended = on
}

// Unattended reports whether unattended auto-approval is currently on.
func (m *Manager) Unattended() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.unattended
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
	assistedMode := m.assistedMode
	safetyDecider := m.safetyDecider
	unattended := m.unattended
	pRule := m.persistent[tool]
	m.mu.Unlock()

	if pRule == "deny" {
		return false
	}
	if toolTier(tool) == TierSafe {
		return true
	}
	command, _ := params["command"].(string)
	alwaysConfirm := (tool == "bash" || tool == "interactivebash") && needsAlwaysConfirmBash(command)
	if unattended {
		// An unattended mission must never block on a prompt. Dangerous commands
		// are denied (not prompted) so the agent receives a denial and can adapt
		// or call MissionBlocked instead of hanging forever.
		if alwaysConfirm {
			return false
		}
		return true
	}
	if !alwaysConfirm && (allowed || pRule == "allow" || yesMode) {
		return true
	}

	// Assisted execution mode for shell commands using a SafetyDecider (e.g. JEV Style)
	if assistedMode && !alwaysConfirm && (tool == "bash" || tool == "interactivebash") {
		if safetyDecider == nil || !safetyDecider.Enabled() {
			// Decider is absent or disabled: deactivate assisted mode and fall back to manual approval
			m.SetAssistedMode(false)
		} else {
			dangerous, err := safetyDecider.IsCommandDangerous(context.Background(), command)
			if err == nil && !dangerous {
				// Classified safe by decision model: auto-approve
				return true
			}
			if err != nil {
				// Decider failed (e.g. connection error, timeout, offline).
				// Deactivate assisted mode so subsequent commands do not repeatedly fail/stall,
				// falling back cleanly to the prior manual confirmation mode.
				m.SetAssistedMode(false)
			}
		}
		// If dangerous == true or error occurred, prompt user below
	}

	// Keep prompting below.
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
