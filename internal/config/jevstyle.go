package config

import "strings"

// JevstyleInUse reports whether a JEV Style decision model is configured.
func (c *Config) JevstyleInUse() bool {
	if c == nil {
		return false
	}
	return strings.TrimSpace(c.JevstyleModel) != ""
}
