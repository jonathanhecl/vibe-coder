package tui

import (
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// RoleValue renders how a role is wired: its own "[provider] model" pair, the
// fallback target when the role is unset, or a short note when the role has no
// model fallback. The same call renders plain text when styling is disabled, so
// the banner and command output share one wording.
func (s Style) RoleValue(status config.RoleStatus) string {
	switch {
	case status.Bound:
		return s.ProviderModel(status.Provider, status.Model)
	case status.Fallback:
		return s.Dim("Fallback to") + " " + s.ProviderModel(status.Provider, status.Model) +
			"  " + s.Green(config.RoleHint(status.Role))
	case status.Note != "":
		return s.Dim(status.Note) + "  " + s.Green(config.RoleHint(status.Role))
	default:
		return s.Green(config.RoleHint(status.Role))
	}
}

// ProviderModel renders the "[provider] model" pair shared by the banner, the
// provider list, and the model picker.
func (s Style) ProviderModel(provider, model string) string {
	provider = strings.TrimSpace(provider)
	model = strings.TrimSpace(model)
	if provider == "" {
		return s.BrightWhite(model)
	}
	return s.Gray("["+provider+"]") + " " + s.BrightWhite(model)
}

// RoleRow is one configuration line: a label with its value in both the plain
// and the styled variant. It is the shared shape of the startup banner and the
// /status table, so both surfaces stay word-for-word identical.
type RoleRow struct {
	Label  string
	Plain  string
	Render func(Style) string
}

// RoleRows renders one row per model role, in display order. A bound role
// shows "[provider] model" and, when its model is known to see images, the
// vision tag; an unbound role shows the fallback it uses or the behavior it
// degrades to, followed by the command that sets it. Roles that cannot add
// anything to what the other roles already do are left out, so the list stays
// about what actually runs.
func RoleRows(cfg *config.Config) []RoleRow {
	rows := make([]RoleRow, 0, len(config.RoleDisplayOrder))
	for _, role := range config.RoleDisplayOrder {
		if !roleWorthListing(cfg, role) {
			continue
		}
		rows = append(rows, roleRow(cfg, role))
	}
	return rows
}

// roleWorthListing reports whether a role has something to say. The vision role
// is redundant while an unset one would only restate the routing that the main
// or sidecar model already covers by seeing images; the decision role only
// matters once a model or endpoint is configured for it.
func roleWorthListing(cfg *config.Config, role config.Role) bool {
	switch role {
	case config.RoleVision:
		return !visionCovered(cfg)
	case config.RoleDecision:
		return cfg != nil && cfg.DecisionInUse()
	default:
		return true
	}
}

// visionCovered reports whether a bound main or sidecar model already takes
// images, in which case an unset vision role changes nothing. A bound vision
// role is an explicit choice and is always listed.
func visionCovered(cfg *config.Config) bool {
	if cfg == nil || cfg.RoleStatus(config.RoleVision).Bound {
		return false
	}
	for _, role := range []config.Role{config.RoleMain, config.RoleSidecar} {
		if status := cfg.RoleStatus(role); status.Bound && cfg.ModelHasVision(status.Model) {
			return true
		}
	}
	return false
}

// roleRow renders one role, letting the role's own binding win and falling back
// to the behavior it degrades to. The decision endpoint and the per-session
// sidecar state refine the row when they apply.
func roleRow(cfg *config.Config, role config.Role) RoleRow {
	status := cfg.RoleStatus(role)
	row := RoleRow{
		Label: config.RoleTitle(role),
		Plain: Style{}.RoleValue(status),
	}
	row.Render = func(st Style) string { return st.RoleValue(status) }

	// A bound model that can see images carries the same tag as /providers.
	if status.Bound && cfg.ModelHasVision(status.Model) {
		base := row.Render
		row.Plain += "  " + config.CapabilityVision
		row.Render = func(st Style) string {
			return base(st) + "  " + st.CapabilityTag(config.CapabilityVision)
		}
	}
	if role == config.RoleDecision && cfg != nil {
		if endpoint := strings.TrimSpace(cfg.DecisionEndpoint); endpoint != "" {
			row.Plain = endpoint
			row.Render = func(st Style) string { return st.BrightWhite(endpoint) }
		}
	}
	if role == config.RoleSidecar && cfg != nil {
		switch {
		case cfg.SidecarDisabled:
			row.Plain = "(disabled)"
			row.Render = func(st Style) string { return st.Dim("(disabled)") }
		case cfg.SidecarSkipSession && status.Bound:
			// Only a bound sidecar can be turned off for the session; an unset
			// one already explains its fallback instead.
			base := row.Render
			row.Plain += " (off this session)"
			row.Render = func(st Style) string { return base(st) + " " + st.Dim("(off this session)") }
		}
	}
	return row
}
