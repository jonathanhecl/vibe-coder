package permissions

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

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

// actionSafetyDecider is an optional extension for deciders that can also
// classify non-shell tool actions (for example network requests). Deciders that
// only understand shell commands keep prompting for those actions.
type actionSafetyDecider interface {
	IsActionDangerous(ctx context.Context, toolName, summary string) (bool, error)
}

// approvalNotifier is implemented by the UI to surface assisted-mode
// auto-approvals, so the user can see why an action ran without a prompt.
type approvalNotifier interface {
	NotifyAutoApproval(tool string, elapsed time.Duration)
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

	// Assisted execution mode: a SafetyDecider (e.g. JEV Style) classifies the
	// proposed action and auto-approves it when safe. Shell commands use the
	// command text; network tools use their arguments. File mutations stay on
	// the manual path for safety.
	if assistedMode {
		switch {
		case (tool == "bash" || tool == "interactivebash") && !alwaysConfirm:
			if m.assistedCommandApproved(ui, safetyDecider, toolName, command) {
				return true
			}
		case toolTier(tool) == TierNetwork:
			if m.assistedActionApproved(ui, safetyDecider, toolName, params) {
				return true
			}
		}
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

// assistedCommandApproved asks the safety decider whether a shell command is
// safe and auto-approves it when so. A missing, disabled, or failing decider
// deactivates assisted mode and falls back to manual approval.
func (m *Manager) assistedCommandApproved(ui prompter, dec SafetyDecider, toolName, command string) bool {
	if dec == nil || !dec.Enabled() {
		m.SetAssistedMode(false)
		return false
	}
	start := time.Now()
	dangerous, err := dec.IsCommandDangerous(context.Background(), command)
	elapsed := time.Since(start)
	if err != nil {
		m.SetAssistedMode(false)
		return false
	}
	if !dangerous {
		notifyAutoApproval(ui, toolName, elapsed)
		return true
	}
	return false
}

// assistedActionApproved asks the safety decider whether a non-shell action is
// safe and auto-approves it when so. Deciders without action support keep the
// manual prompt for this action while assisted mode stays on for shell commands.
func (m *Manager) assistedActionApproved(ui prompter, dec SafetyDecider, toolName string, params map[string]any) bool {
	if dec == nil || !dec.Enabled() {
		m.SetAssistedMode(false)
		return false
	}
	actionDec, ok := dec.(actionSafetyDecider)
	if !ok {
		return false
	}
	start := time.Now()
	dangerous, err := actionDec.IsActionDangerous(context.Background(), toolName, actionSummary(params))
	elapsed := time.Since(start)
	if err != nil {
		m.SetAssistedMode(false)
		return false
	}
	if !dangerous {
		notifyAutoApproval(ui, toolName, elapsed)
		return true
	}
	return false
}

func notifyAutoApproval(ui prompter, toolName string, elapsed time.Duration) {
	if n, ok := ui.(approvalNotifier); ok {
		n.NotifyAutoApproval(toolName, elapsed)
	}
}

// actionSummary renders a tool's parameters as a short, stable text block for
// the decision model.
func actionSummary(params map[string]any) string {
	if len(params) == 0 {
		return "(no arguments)"
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		text := strings.TrimSpace(fmt.Sprintf("%v", params[k]))
		text = strings.ReplaceAll(text, "\n", " ")
		if len(text) > 400 {
			text = text[:400] + "…"
		}
		fmt.Fprintf(&b, "- %s: %s\n", k, text)
	}
	return strings.TrimRight(b.String(), "\n")
}
