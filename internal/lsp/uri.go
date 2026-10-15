package lsp

import (
	"net/url"
	"path/filepath"
	"strings"
)

// fileURI converts a filesystem path into the file:// URI the protocol expects.
//
// Spaces, quotes and non-ASCII characters are percent-encoded rather than
// passed through: a server that parses the URI sees a truncated path otherwise,
// and on macOS a project directory with a space in its name is common enough to
// matter. A Windows drive path (C:/x) is normalized into the file:///C:/x form
// the specification asks for.
func fileURI(path string) string {
	slash := filepath.ToSlash(path)
	if !strings.HasPrefix(slash, "/") {
		slash = "/" + slash
	}
	return (&url.URL{Scheme: "file", Path: slash}).String()
}
