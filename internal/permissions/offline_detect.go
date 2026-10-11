package permissions

import (
	"regexp"
	"strings"
)

var (
	// networkCmdRegex matches common network CLI tools at word boundaries.
	networkCmdRegex = regexp.MustCompile(`(?i)\b(curl|wget|http|httpie|fetch|ssh|scp|sftp|ftp|telnet|nc|netcat|ncat|socat|ping|traceroute|dig|nslookup|host)\b`)
	// packageNetRegex matches package and tool managers performing network operations.
	packageNetRegex = regexp.MustCompile(`(?i)\b(git\s+(clone|fetch|pull|push|remote)|npm\s+(i|install)|yarn\s+add|pnpm\s+add|pip\s+install|cargo\s+install|gem\s+install|go\s+get|docker\s+(pull|push))\b`)
	// localhostRegex matches local loopback addresses.
	localhostRegex = regexp.MustCompile(`(?i)(localhost|127\.0\.0\.1|0\.0\.0\.0|::1)`)
	// urlRegex matches HTTP/HTTPS URLs.
	urlRegex = regexp.MustCompile(`(?i)https?://[^\s'"]+`)
)

// heuristicIsNetworkCommand analyzes a command string to determine whether it
// is attempting to contact external network or internet resources.
func heuristicIsNetworkCommand(cmd string) bool {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return false
	}

	if packageNetRegex.MatchString(trimmed) {
		return true
	}

	if networkCmdRegex.MatchString(trimmed) {
		// If command references localhost, verify that all external-looking URLs are strictly local.
		if localhostRegex.MatchString(trimmed) {
			urls := urlRegex.FindAllString(trimmed, -1)
			if len(urls) > 0 {
				allLocal := true
				for _, u := range urls {
					lower := strings.ToLower(u)
					if !strings.Contains(lower, "localhost") &&
						!strings.Contains(lower, "127.0.0.1") &&
						!strings.Contains(lower, "0.0.0.0") &&
						!strings.Contains(lower, "::1") {
						allLocal = false
						break
					}
				}
				if allLocal {
					return false
				}
			} else {
				// Non-URL local commands, e.g. "nc -zv localhost 8080" or "ping 127.0.0.1"
				return false
			}
		}
		return true
	}

	return false
}
