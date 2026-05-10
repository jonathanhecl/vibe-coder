package tools

import "strings"

func errResult(msg string) Result {
	return Result{
		Output:  msg,
		IsError: true,
	}
}

func isIgnoredSearchDir(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case ".git", ".hg", ".svn", "node_modules", "vendor", "dist", "build", "target", ".vibe-coder":
		return true
	default:
		return false
	}
}
