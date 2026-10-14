package tui

import (
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// RoleValue renders how a role is wired: its own "[provider] model" pair, the
// role it falls back to when it is unset, or a short note when the role has no
// model fallback. A fallback names the role it lands on instead of repeating
// that role's provider and model, because the same view already lists it. The
// same call renders plain text when styling is disabled, so the banner and
// command output share one wording.
func (s Style) RoleValue(status config.RoleStatus) string {
	switch {
	case status.Bound:
		return s.ProviderModel(status.Provider, status.Model)
	case status.Fallback:
		return s.Dim("Fallback to") + " " + s.fallbackTarget(status) +
			"  " + s.Green(config.RoleHint(status.Role))
	case status.Note != "":
		return s.Dim(status.Note) + "  " + s.Green(config.RoleHint(status.Role))
	default:
		return s.Green(config.RoleHint(status.Role))
	}
}

// fallbackTarget renders what an unbound role falls back to. A role that is
// always part of the role list is named, so the reader can look it up a line
// above; anything else keeps its own "[provider] model" pair.
func (s Style) fallbackTarget(status config.RoleStatus) string {
	if alwaysListed(status.Landed) {
		return s.BrightWhite(config.RoleTitle(status.Landed))
	}
	return s.ProviderModel(status.Provider, status.Model)
}

// alwaysListed reports whether a role is always rendered in the role list, which
// is what lets a fallback point at it by name. The fallback chain only lands on
// the main and sidecar roles, both of which the list always shows.
func alwaysListed(role config.Role) bool {
	switch role {
	case config.RoleMain, config.RoleSidecar:
		return true
	default:
		return false
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

// RoleRows renders one row per model role, in display order. A bound role
// shows "[provider] model" and, when its model is known to see images, the
// vision tag; an unbound role shows the fallback it uses or the behavior it
// degrades to, followed by the command that sets it. Roles that cannot add
// anything to what the other roles already do are left out, so the list stays
// about what actually runs.
func RoleRows(cfg *config.Config) []Row {
	rows := make([]Row, 0, len(config.RoleDisplayOrder))
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
func roleRow(cfg *config.Config, role config.Role) Row {
	status := cfg.RoleStatus(role)
	row := Row{
		Label:   config.RoleTitle(role),
		Plain:   Style{}.RoleValue(status),
		Section: RoleSection,
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
