package permissions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// RuleState is the permission state a session can carry in its own file: the
// answers scoped to the session ("allow/block this session") and the saved ones
// ("always allow/deny"), plus the out-of-domain paths that were granted the same
// way. An isolated session keeps it in its metadata, so the file holds
// everything the run needs and nothing is written to the user's global store.
type RuleState struct {
	Allow    []string          `json:"allow,omitempty"`
	Deny     []string          `json:"deny,omitempty"`
	OutAllow []string          `json:"out_allow,omitempty"`
	OutDeny  []string          `json:"out_deny,omitempty"`
	Saved    map[string]string `json:"saved,omitempty"`
}

// SnapshotRules renders the current rules as JSON for a session file. It returns
// nil when there is nothing to carry, so an empty state never writes a key.
func (m *Manager) SnapshotRules() []byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.snapshotRulesLocked()
}

func (m *Manager) snapshotRulesLocked() []byte {
	state := RuleState{
		Allow:    sortedRuleKeys(m.allow),
		Deny:     sortedRuleKeys(m.deny),
		OutAllow: sortedRuleKeys(m.allowedOutDomain),
		OutDeny:  sortedRuleKeys(m.deniedOutDomain),
	}
	if len(m.persistent) > 0 {
		state.Saved = make(map[string]string, len(m.persistent))
		for k, v := range m.persistent {
			state.Saved[k] = v
		}
	}
	if len(state.Allow) == 0 && len(state.Deny) == 0 && len(state.OutAllow) == 0 &&
		len(state.OutDeny) == 0 && len(state.Saved) == 0 {
		return nil
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return nil
	}
	return raw
}

// RestoreRules merges a session's rules back into this manager. A corrupt blob is
// an error and changes nothing: losing the user's answers silently is worse than
// starting the resume without the session's extras.
func (m *Manager) RestoreRules(raw []byte) error {
	if len(raw) == 0 {
		return nil
	}
	var state RuleState
	if err := json.Unmarshal(raw, &state); err != nil {
		return fmt.Errorf("decode permission state: %w", err)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, tool := range state.Allow {
		if norm := normalizeTool(tool); norm != "" {
			delete(m.deny, norm)
			m.allow[norm] = struct{}{}
		}
	}
	for _, tool := range state.Deny {
		if norm := normalizeTool(tool); norm != "" {
			delete(m.allow, norm)
			m.deny[norm] = struct{}{}
		}
	}
	for _, path := range state.OutAllow {
		if path = trimRuleKey(path); path != "" {
			if m.allowedOutDomain == nil {
				m.allowedOutDomain = map[string]struct{}{}
			}
			delete(m.deniedOutDomain, path)
			m.allowedOutDomain[path] = struct{}{}
		}
	}
	for _, path := range state.OutDeny {
		if path = trimRuleKey(path); path != "" {
			if m.deniedOutDomain == nil {
				m.deniedOutDomain = map[string]struct{}{}
			}
			delete(m.allowedOutDomain, path)
			m.deniedOutDomain[path] = struct{}{}
		}
	}
	for tool, verdict := range state.Saved {
		norm := normalizeTool(tool)
		verdict = trimRuleKey(verdict)
		if norm == "" || (verdict != "allow" && verdict != "deny") {
			continue
		}
		// A saved allow for a tool that runs arbitrary commands is never
		// restored, exactly like the persistent loader refuses to load one.
		if verdict == "allow" && isArbitraryCommandTool(norm) {
			continue
		}
		if m.persistent == nil {
			m.persistent = map[string]string{}
		}
		m.persistent[norm] = verdict
	}
	return nil
}

// OnRulesChanged installs the sink an isolated session uses to keep its file in
// step with the answers the user gives. It receives the JSON snapshot after every
// change, never on a read. The callback runs while the manager holds its lock, so
// it must not call back into the manager.
func (m *Manager) OnRulesChanged(fn func([]byte)) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ruleSink = fn
}

// SetIsolated keeps the rules out of the user's global permission store: an
// isolated session carries them in its own file.
func (m *Manager) SetIsolated(isolated bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.isolated = isolated
}

// notifyRulesLocked hands the current snapshot to the session sink, if any.
func (m *Manager) notifyRulesLocked() {
	if m.ruleSink == nil {
		return
	}
	if raw := m.snapshotRulesLocked(); raw != nil {
		m.ruleSink(raw)
	}
}

func sortedRuleKeys(set map[string]struct{}) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for k := range set {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// trimRuleKey trims the edges of a stored key. Interior spaces stay: an
// out-of-domain path can contain them.
func trimRuleKey(s string) string {
	return strings.TrimSpace(s)
}
