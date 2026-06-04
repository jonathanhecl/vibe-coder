package config

import "strings"

// SidecarInUse reports whether the sidecar model should run (tool summaries,
// path disambiguation, session compaction). It is false when disabled in
// config, skipped for this session, or no model name is set.
func (c *Config) SidecarInUse() bool {
	if c == nil {
		return false
	}
	if c.SidecarDisabled || c.SidecarSkipSession {
		return false
	}
	return strings.TrimSpace(c.SidecarModel) != ""
}

// PersistSidecarOffFromSave sets SidecarDisabled when the user runs the -save
// directive together with --no-sidecar (CLI: SidecarSkipSession).
func (c *Config) PersistSidecarOffFromSave(persist bool) {
	if c == nil || !persist {
		return
	}
	if c.SidecarSkipSession {
		c.SidecarDisabled = true
	}
}
