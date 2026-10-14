package permissions

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/decision"
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

// safetyDecisionMaker is an optional interface for deciders that support
// multi-question safety evaluation with confidence scoring (the decision model).
// When implemented, the manager uses AskSafety for assisted mode decisions.
type safetyDecisionMaker interface {
	AskSafety(ctx context.Context, toolName, detail string) (*decision.SafetyDecision, error)
}

// safetyContextDecisionMaker is an optional extension for deciders that can
// take a runtime context note alongside the action. The note tells the model
// about facts it cannot read from the arguments, such as "this path was
// supplied by the user".
type safetyContextDecisionMaker interface {
	AskSafetyWithContext(ctx context.Context, toolName, detail, contextNote string) (*decision.SafetyDecision, error)
}

// networkSafetyDecider is an optional interface for safety deciders that can classify
// whether a shell command attempts external network or internet access.
type networkSafetyDecider interface {
	IsCommandNetworkAccess(ctx context.Context, command string) (bool, error)
}

// approvalNotifier is implemented by the UI to surface assisted-mode review
// outcomes, so the user can see why an action ran without a prompt, or why the
// normal permission prompt is being shown.
type approvalNotifier interface {
	NotifyAssisted(notice tui.AssistedNotice)
}

type Manager struct {
	mu sync.Mutex

	yesMode          bool
	assistedMode     bool
	offlineMode      bool
	lastDenialReason string
	safetyDecider    SafetyDecider
	// verdicts memoizes assisted-mode decision-model verdicts so an identical
	// tool action is judged once. It only ever stores successful verdicts.
	verdicts *verdictCache
	allow    map[string]struct{}
	deny     map[string]struct{}
	file     string

	workspaceDir     string
	allowedOutDomain map[string]struct{}
	deniedOutDomain  map[string]struct{}

	persistent map[string]string

	// unattended is set while an agent-managed mission is active. It auto-approves
	// Ask/Network tools so a long unattended run never blocks on a prompt, but it
	// never bypasses mandatory dangerous-command confirmations: those are denied
	// outright instead of prompted, so the run continues and the agent can adapt.
	// It does not change the user's own yes-mode setting.
	unattended bool

	// userProvided maps a lookup key (a user-supplied absolute path, and its
	// basename) to that absolute path. The decision model has no way to tell
	// such a file apart from an opaque name the agent invented, so assisted
	// mode is told about them explicitly instead of prompting for a file the
	// user just supplied.
	userProvided map[string]string

	// permissionCancelled is set when the user picks Cancel in the permission UI.
	permissionCancelled bool
}

func NewManager(cfg *config.Config) *Manager {
	var yesMode, assistedMode, offlineMode bool
	var permFile, workspaceDir string
	if cfg != nil {
		yesMode = cfg.YesMode
		offlineMode = cfg.Offline
		if cfg.DecisionInUse() {
			assistedMode = cfg.AssistedYes
		}
		permFile = cfg.PermFile
		workspaceDir = cfg.Cwd
	}
	if workspaceDir == "" {
		if cwd, err := os.Getwd(); err == nil {
			workspaceDir = cwd
		}
	}
	if abs, err := filepath.Abs(workspaceDir); err == nil {
		workspaceDir = filepath.Clean(abs)
	}
	m := &Manager{
		yesMode:          yesMode,
		assistedMode:     assistedMode,
		offlineMode:      offlineMode,
		workspaceDir:     workspaceDir,
		allow:            map[string]struct{}{},
		deny:             map[string]struct{}{},
		allowedOutDomain: map[string]struct{}{},
		deniedOutDomain:  map[string]struct{}{},
		file:             permFile,
		persistent:       map[string]string{},
		userProvided:     map[string]string{},
		verdicts:         newVerdictCache(verdictCacheSize),
	}
	m.loadPersistent()
	return m
}

// NoteUserProvidedPath records a path the user handed to the session directly,
// such as an image pasted from the clipboard. Assisted mode reports these to
// the decision model so a file the user just supplied is not mistaken for an
// unknown path chosen by the agent.
//
// Both the absolute path and its basename are matched, because the model may
// refer to the file either way. Callers should only register paths whose
// basename is not generic: clipboard temp names are unique per paste.
func (m *Manager) NoteUserProvidedPath(path string) {
	p := strings.TrimSpace(path)
	if p == "" {
		return
	}
	if abs, err := filepath.Abs(p); err == nil && abs != "" {
		p = abs
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.userProvided == nil {
		m.userProvided = map[string]string{}
	}
	m.userProvided[p] = p
	if base := filepath.Base(p); base != "" && base != "." && base != string(filepath.Separator) {
		m.userProvided[base] = p
	}
}

// userProvidedNote returns a decision-model context line naming any
// user-provided path mentioned in detail, or "" when none apply. Paths are
// always reported in full, even when the match came from a basename, so the
// model knows where the file actually lives.
func (m *Manager) userProvidedNote(detail string) string {
	if strings.TrimSpace(detail) == "" {
		return ""
	}
	m.mu.Lock()
	canonical := make(map[string]struct{})
	for key, full := range m.userProvided {
		if strings.Contains(detail, key) {
			canonical[full] = struct{}{}
		}
	}
	m.mu.Unlock()
	if len(canonical) == 0 {
		return ""
	}
	hits := make([]string, 0, len(canonical))
	for p := range canonical {
		hits = append(hits, p)
	}
	sort.Strings(hits)
	var b strings.Builder
	b.WriteString("The user supplied the following path(s) from their own clipboard via /paste ")
	b.WriteString("(not chosen by the agent), so inspecting or reading them is expected work:\n")
	for _, p := range hits {
		fmt.Fprintf(&b, "- %s\n", p)
	}
	return strings.TrimRight(b.String(), "\n")
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
	// A new decider invalidates every memoized verdict.
	m.verdicts.reset()
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

// SetOfflineMode toggles offline mode on or off.
func (m *Manager) SetOfflineMode(on bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.offlineMode = on
}

// OfflineMode reports whether offline mode is currently active.
func (m *Manager) OfflineMode() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.offlineMode
}

// LastDenialReason returns the specific reason the previous Check was denied, or "".
func (m *Manager) LastDenialReason() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastDenialReason
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
	m.lastDenialReason = ""
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
	offlineMode := m.offlineMode
	pRule := m.persistent[tool]
	m.mu.Unlock()

	if pRule == "deny" {
		return false
	}

	if offlineMode {
		if toolTier(tool) == TierNetwork {
			m.mu.Lock()
			m.lastDenialReason = "Blocked by offline mode: network tools are disabled."
			m.mu.Unlock()
			return false
		}
		if tool == "bash" || tool == "interactivebash" {
			command, _ := params["command"].(string)
			if m.isCommandOfflineBlocked(command, safetyDecider) {
				m.mu.Lock()
				m.lastDenialReason = "Blocked by offline mode: external network access is disabled."
				m.mu.Unlock()
				return false
			}
		}
	}

	unapprovedOutDomain := m.UnapprovedOutDomainPaths(toolName, params)
	isOutDomain := len(unapprovedOutDomain) > 0

	for _, p := range unapprovedOutDomain {
		if m.IsOutDomainBlocked(p) {
			m.mu.Lock()
			m.lastDenialReason = "Blocked: path is outside workspace domain and access was denied."
			m.mu.Unlock()
			return false
		}
	}

	command, _ := params["command"].(string)
	alwaysConfirm := (tool == "bash" || tool == "interactivebash") && needsAlwaysConfirmBash(command)

	if !isOutDomain {
		if toolTier(tool) == TierSafe {
			return true
		}
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

		// Assisted execution mode: a SafetyDecider (the decision model) reviews every
		// action that would otherwise prompt (shell commands, network tools, file
		// mutations, unknown/MCP tools) and auto-approves it when safe. Mandatory
		// confirmations (always-confirm shell patterns) and safe-tier tools keep
		// their existing paths.
		if assistedMode && !alwaysConfirm {
			if tool == "bash" || tool == "interactivebash" {
				if m.assistedCommandApproved(ui, safetyDecider, toolName, command) {
					return true
				}
			} else if toolTier(tool) != TierSafe {
				if m.assistedActionApproved(ui, safetyDecider, toolName, params) {
					return true
				}
			}
		}
	} else {
		// Out-of-domain target: requires explicit user confirmation.
		// Never treated as TierSafe or auto-approved by in-domain session tool allow.
		if unattended {
			if alwaysConfirm {
				return false
			}
			return true
		}
		if yesMode && !alwaysConfirm {
			return true
		}
	}

	// Keep prompting below.
	if ui == nil {
		m.mu.Lock()
		if isOutDomain {
			m.lastDenialReason = "Permission denied: cannot access path outside workspace domain without user confirmation."
		}
		m.mu.Unlock()
		return false
	}

	promptParams := params
	if isOutDomain {
		promptParams = make(map[string]any, len(params)+2)
		for k, v := range params {
			promptParams[k] = v
		}
		if len(unapprovedOutDomain) == 1 {
			promptParams["domain_warning"] = fmt.Sprintf("path is outside workspace domain (%s)", m.WorkspaceDir())
			promptParams["target_path"] = unapprovedOutDomain[0]
		} else {
			promptParams["domain_warning"] = fmt.Sprintf("paths are outside workspace domain (%s): %s", m.WorkspaceDir(), strings.Join(unapprovedOutDomain, ", "))
			promptParams["target_path"] = strings.Join(unapprovedOutDomain, ", ")
		}
	}

	decision := ui.AskPermission(toolName, promptParams)
	switch decision {
	case tui.DecisionAllowOnce:
		return true
	case tui.DecisionAllowSession:
		if isOutDomain {
			for _, p := range unapprovedOutDomain {
				m.AllowOutDomainSession(p)
			}
		} else {
			m.AllowSession(tool)
		}
		return true
	case tui.DecisionAllowPersistent:
		if isOutDomain {
			for _, p := range unapprovedOutDomain {
				m.AllowOutDomainPersistent(p)
			}
		} else {
			m.AllowAll(tool)
		}
		return true
	case tui.DecisionDenySession:
		if isOutDomain {
			for _, p := range unapprovedOutDomain {
				m.DenyOutDomainSession(p)
			}
		} else {
			m.DenySession(tool)
		}
		return false
	case tui.DecisionDenyPersistent:
		if isOutDomain {
			for _, p := range unapprovedOutDomain {
				m.DenyOutDomainPersistent(p)
			}
		} else {
			m.DenyAll(tool)
		}
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
		if isOutDomain {
			m.mu.Lock()
			m.lastDenialReason = "Permission denied: access to path outside workspace domain was rejected by user."
			m.mu.Unlock()
		}
		return false
	}
}

// assistedCommandApproved asks the safety decider whether a shell command is
// safe and auto-approves it when so. When the decider supports AskSafety
// (the decision model), it uses the multi-question decision with confidence scoring.
// It always reports the outcome to the UI so the user knows whether assisted
// mode approved, flagged, or could not review the command. A missing,
// disabled, or failing decider deactivates assisted mode and falls back to
// manual approval.
func (m *Manager) assistedCommandApproved(ui prompter, dec SafetyDecider, toolName, command string) bool {
	if dec == nil || !dec.Enabled() {
		m.SetAssistedMode(false)
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedUnavailable})
		return false
	}
	start := time.Now()

	note := m.userProvidedNote(command)
	key := verdictKey(toolName, command, note)
	if v, ok := m.verdicts.get(key); ok {
		return m.replayVerdict(ui, toolName, v, time.Since(start))
	}

	// Try AskSafety first (multi-question with confidence)
	if decision, handled, err := askSafety(context.Background(), dec, toolName, command, note); handled {
		elapsed := time.Since(start)
		if err != nil {
			m.SetAssistedMode(false)
			notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedUnavailable, Elapsed: elapsed})
			return false
		}
		if decision != nil {
			m.verdicts.put(key, cachedVerdict{decision: decision})
		}
		return m.handleSafetyDecision(ui, toolName, decision, elapsed)
	}

	// Fallback to binary IsCommandDangerous (binary decider)
	dangerous, err := dec.IsCommandDangerous(context.Background(), command)
	elapsed := time.Since(start)
	if err != nil {
		m.SetAssistedMode(false)
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedUnavailable, Elapsed: elapsed})
		return false
	}
	m.verdicts.put(key, cachedVerdict{binary: true, dangerous: dangerous})
	if dangerous {
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedDangerous, Elapsed: elapsed})
		return false
	}
	notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedApproved, Elapsed: elapsed})
	return true
}

// askSafety queries a decider that supports AskSafety, preferring the
// context-aware variant when available. handled is false when the decider
// supports neither, so callers fall through to the binary decider.
func askSafety(ctx context.Context, dec SafetyDecider, toolName, detail, note string) (decision *decision.SafetyDecision, handled bool, err error) {
	if scm, ok := dec.(safetyContextDecisionMaker); ok {
		d, err := scm.AskSafetyWithContext(ctx, toolName, detail, note)
		return d, true, err
	}
	if sdm, ok := dec.(safetyDecisionMaker); ok {
		d, err := sdm.AskSafety(ctx, toolName, detail)
		return d, true, err
	}
	return nil, false, nil
}

// handleSafetyDecision processes a SafetyDecision from AskSafety and notifies
// the UI of the outcome. Auto-approves when action is "allow" and confidence
// is high enough. When action is "block", prompts the user with a notice that
// the decision model is confident the action is dangerous (does not auto-reject).
func (m *Manager) handleSafetyDecision(ui prompter, toolName string, decision *decision.SafetyDecision, elapsed time.Duration) bool {
	if decision == nil {
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedUnavailable, Elapsed: elapsed})
		return false
	}

	// Auto-allow when action is "allow" and confidence is high enough
	if decision.Allowed() && decision.Confident() {
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedApproved, Elapsed: elapsed})
		return true
	}

	// "block" or "review" or low confidence: prompt the user
	if decision.Blocked() {
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedDangerous, Elapsed: elapsed})
	} else {
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedUncertain, Elapsed: elapsed})
	}
	return false
}

// assistedActionApproved asks the safety decider whether a non-shell action is
// safe and auto-approves it when so. When the decider supports AskSafety
// (the decision model), it uses the multi-question decision with confidence scoring.
// Deciders without action support keep the manual prompt for that action while
// assisted mode stays on for shell commands.
func (m *Manager) assistedActionApproved(ui prompter, dec SafetyDecider, toolName string, params map[string]any) bool {
	if dec == nil || !dec.Enabled() {
		m.SetAssistedMode(false)
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedUnavailable})
		return false
	}
	start := time.Now()

	summary := actionSummary(params)
	note := m.userProvidedNote(summary)
	key := verdictKey(toolName, summary, note)
	if v, ok := m.verdicts.get(key); ok {
		return m.replayVerdict(ui, toolName, v, time.Since(start))
	}

	// Try AskSafety first (multi-question with confidence)
	if decision, handled, err := askSafety(context.Background(), dec, toolName, summary, note); handled {
		elapsed := time.Since(start)
		if err != nil {
			m.SetAssistedMode(false)
			notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedUnavailable, Elapsed: elapsed})
			return false
		}
		if decision != nil {
			m.verdicts.put(key, cachedVerdict{decision: decision})
		}
		return m.handleSafetyDecision(ui, toolName, decision, elapsed)
	}

	// Fallback to binary IsActionDangerous (binary decider)
	actionDec, ok := dec.(actionSafetyDecider)
	if !ok {
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedUnsupported})
		return false
	}
	dangerous, err := actionDec.IsActionDangerous(context.Background(), toolName, summary)
	elapsed := time.Since(start)
	if err != nil {
		m.SetAssistedMode(false)
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedUnavailable, Elapsed: elapsed})
		return false
	}
	m.verdicts.put(key, cachedVerdict{binary: true, dangerous: dangerous})
	if dangerous {
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedDangerous, Elapsed: elapsed})
		return false
	}
	notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedApproved, Elapsed: elapsed})
	return true
}

func notifyAssisted(ui prompter, notice tui.AssistedNotice) {
	if n, ok := ui.(approvalNotifier); ok {
		n.NotifyAssisted(notice)
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

// isCommandOfflineBlocked evaluates whether a shell command is attempting to
// reach the external internet while offline mode is active.
func (m *Manager) isCommandOfflineBlocked(command string, safetyDecider SafetyDecider) bool {
	cmd := strings.TrimSpace(command)
	if cmd == "" {
		return false
	}

	if safetyDecider != nil && safetyDecider.Enabled() {
		if netDec, ok := safetyDecider.(networkSafetyDecider); ok {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			attemptsNet, err := netDec.IsCommandNetworkAccess(ctx, cmd)
			if err == nil {
				return attemptsNet
			}
		}
	}

	return heuristicIsNetworkCommand(cmd)
}

// verdictCacheSize bounds the assisted-mode verdict memo. The agent repeats the
// same handful of commands far more often than it explores new ones, so a small
// fixed table captures most of the reuse without unbounded growth.
const verdictCacheSize = 128

// cachedVerdict is the memoized outcome of one assisted-mode decision-model
// review. binary marks the fallback IsCommandDangerous/IsActionDangerous path;
// otherwise decision carries the full AskSafety verdict so a hit replays the
// exact allow/review/block outcome and risk score.
type cachedVerdict struct {
	decision  *decision.SafetyDecision
	binary    bool
	dangerous bool
}

// verdictCache is a bounded FIFO memo of successful decision-model verdicts,
// keyed by tool plus normalized arguments. Only successful verdicts are stored:
// a failure or an unavailable decider is never cached, so assisted mode keeps
// falling back to manual prompts.
type verdictCache struct {
	mu      sync.Mutex
	max     int
	order   []string
	entries map[string]cachedVerdict
}

func newVerdictCache(max int) *verdictCache {
	if max < 1 {
		max = 1
	}
	return &verdictCache{max: max, entries: map[string]cachedVerdict{}}
}

func (c *verdictCache) get(key string) (cachedVerdict, bool) {
	if c == nil {
		return cachedVerdict{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	v, ok := c.entries[key]
	return v, ok
}

func (c *verdictCache) put(key string, v cachedVerdict) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.entries[key]; !exists {
		c.order = append(c.order, key)
	}
	c.entries[key] = v
	for len(c.order) > c.max {
		oldest := c.order[0]
		c.order = c.order[1:]
		delete(c.entries, oldest)
	}
}

func (c *verdictCache) reset() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.order = nil
	c.entries = map[string]cachedVerdict{}
}

// verdictKey identifies a decision-model review by the tool and the exact text
// the model is shown, including the runtime context note (a user-provided path
// can change the verdict for the same arguments).
func verdictKey(toolName, detail, note string) string {
	return toolName + "\x00" + detail + "\x00" + note
}

// replayVerdict notifies the UI and returns the outcome for a memoized verdict,
// reproducing exactly what the original model review decided.
func (m *Manager) replayVerdict(ui prompter, toolName string, v cachedVerdict, elapsed time.Duration) bool {
	if v.binary {
		if v.dangerous {
			notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedDangerous, Elapsed: elapsed})
			return false
		}
		notifyAssisted(ui, tui.AssistedNotice{Tool: toolName, Outcome: tui.AssistedApproved, Elapsed: elapsed})
		return true
	}
	return m.handleSafetyDecision(ui, toolName, v.decision, elapsed)
}
