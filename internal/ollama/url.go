package ollama

import (
	"net"
	"net/url"
	"strings"
)

const DefaultPort = "11434"

// NormalizeURL trims a raw host/URL and ensures it has an HTTP scheme (defaulting to http://)
// and, for http:// URLs without an explicit port, defaults the port to Ollama's 11434.
// For https:// URLs without an explicit port, the port is left unspecified (standard 443).
func NormalizeURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// If no scheme is present, default to http://
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	// For http:// schemes, if no port is specified, default to Ollama's port 11434.
	if strings.EqualFold(u.Scheme, "http") && u.Port() == "" {
		host := u.Hostname()
		if host != "" {
			u.Host = net.JoinHostPort(host, DefaultPort)
		}
	}
	return strings.TrimRight(u.String(), "/")
}
