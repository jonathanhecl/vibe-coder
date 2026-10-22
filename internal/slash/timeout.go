package slash

import (
	"fmt"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// runSetTimeoutCommand implements /settimeout: show or set how long a
// permission prompt waits for an answer before the action is denied. The value
// applies to the next prompt and persists via /save (PERMISSION_TIMEOUT in
// vibe-coder.env).
//
//	/settimeout          Show the effective bound and the usage.
//	/settimeout 3m       Wait three minutes (also 5m, 10m, 90s, 45 = 45 seconds).
//	/settimeout off      Wait until the user answers.
func runSetTimeoutCommand(c *Ctx, args []string) error {
	if c.Cfg == nil {
		fmt.Fprintln(c.Out, "No configuration to change.")
		return nil
	}
	if len(args) == 0 || strings.EqualFold(strings.TrimSpace(args[0]), "status") {
		printTimeoutStatus(c)
		return nil
	}
	raw := strings.TrimSpace(args[0])
	parsed, ok := config.ParsePermissionTimeout(raw)
	if !ok {
		fmt.Fprintf(c.Out, "Invalid permission timeout %q: expected 3m, 90s, seconds as a bare number (45), or off\n", raw)
		return nil
	}
	c.Cfg.PermissionTimeout = parsed
	if parsed < 0 {
		fmt.Fprintln(c.Out, "Permission prompt timeout disabled: prompts wait until answered. Run /save to persist.")
		return nil
	}
	fmt.Fprintf(c.Out, "Permission prompt timeout set to: %s. Run /save to persist.\n", config.FormatPermissionTimeout(parsed))
	return nil
}

func printTimeoutStatus(c *Ctx) {
	fmt.Fprintf(c.Out, "Permission prompt timeout: %s\n", permissionTimeoutStatus(c))
	fmt.Fprintln(c.Out, "Usage: /settimeout 3m|5m|10m|90s|45 (seconds)|off")
}

// permissionTimeoutStatus renders the bound in force, marking the built-in
// default so a bare number in /status reads as the product default rather than
// a setting the user picked.
func permissionTimeoutStatus(c *Ctx) string {
	if c.Cfg == nil {
		return "3m (default)"
	}
	if c.Cfg.PermissionTimeout < 0 {
		return "off (waits until answered)"
	}
	effective := c.Cfg.EffectivePermissionTimeout()
	if c.Cfg.PermissionTimeout == 0 {
		return config.FormatPermissionTimeout(effective) + " (default)"
	}
	return config.FormatPermissionTimeout(effective)
}
