package config

import "strings"

// JevstyleInUse reports whether a JEV Style decision model is configured.
func (c *Config) JevstyleInUse() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.JevstyleModel) != ""
}

// JevstyleV3InUse reports whether a JEV Style v3 endpoint is configured.
func (c *Config) JevstyleV3InUse() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.JevstyleV3Endpoint) != ""
}
