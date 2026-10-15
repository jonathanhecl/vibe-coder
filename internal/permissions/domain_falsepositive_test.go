package permissions

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
)

// URL routes quoted in a shell command (e.g. "/props", "/v1/models") are not
// filesystem paths and must not trigger an out-of-domain prompt.
func TestDomain_URLRoutesInBashAreNotOutOfDomain(t *testing.T) {
	ws := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws})
	cmd := `for ep in "/v3/list" "/v3/generate" "/v3/chat/format" "/props" "/slots" "/health" "/v1/messages"; do ` +
		`code=$(curl -s -m 6 -w "%{http_code}" "http://127.0.0.1:8080$ep" -H "Content-Type: application/json" -d '{}'); ` +
		`echo "$ep -> $code"; done; curl -s -m 6 http://127.0.0.1:8080/v1/models | head -c 300`
	if got := m.UnapprovedOutDomainPaths("Bash", map[string]any{"command": cmd}); len(got) != 0 {
		t.Fatalf("expected no out-of-domain paths for URL routes, got %v", got)
	}
	// Real system paths must still be detected.
	if got := m.UnapprovedOutDomainPaths("Bash", map[string]any{"command": "cat /etc/hostname"}); len(got) != 1 {
		t.Fatalf("expected /etc/hostname to be out of domain, got %v", got)
	}
}

// Relative subfolders without the workspace prefix (e.g. "function/") are
// inside the domain for every path-taking tool.
func TestDomain_RelativeSubfoldersAreInside(t *testing.T) {
	ws := t.TempDir()
	if err := os.MkdirAll(filepath.Join(ws, "function"), 0o755); err != nil {
		t.Fatal(err)
	}
	m := NewManager(&config.Config{Cwd: ws})
	cases := []struct {
		tool   string
		params map[string]any
	}{
		{"Bash", map[string]any{"command": "ls function/"}},
		{"Bash", map[string]any{"command": "cd function/ && ls"}},
		{"Bash", map[string]any{"command": "ls", "workdir": "function/"}},
		{"Read", map[string]any{"file_path": "function/a.go"}},
		{"Write", map[string]any{"file_path": "function/new/b.go"}},
		{"Glob", map[string]any{"pattern": "function/*.go"}},
		{"Glob", map[string]any{"path": "function/"}},
		{"Grep", map[string]any{"pattern": "x", "path": "function/"}},
		{"Grep", map[string]any{"pattern": "x", "glob": "function/**/*.go"}},
	}
	for _, c := range cases {
		if got := m.UnapprovedOutDomainPaths(c.tool, c.params); len(got) != 0 {
			t.Errorf("%s %v: expected inside domain, got %v", c.tool, c.params, got)
		}
	}
}

// When the workspace is reached through a symlink (or its physical path is
// used by the agent, e.g. from `pwd -P`), both spellings are inside; a
// symlink inside the workspace pointing outside is still rejected.
func TestDomain_SymlinkedWorkspaceAliases(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real")
	if err := os.MkdirAll(filepath.Join(real, "function"), 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	outside := filepath.Join(base, "outside")
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(real, "escape")); err != nil {
		t.Fatal(err)
	}

	for _, ws := range []string{link, real} {
		m := NewManager(&config.Config{Cwd: ws})
		for _, p := range []string{
			"function/x.go",
			filepath.Join(real, "function", "x.go"),
			filepath.Join(link, "function", "x.go"),
		} {
			if !m.IsInsideDomain(p) {
				t.Errorf("ws=%s: expected %s inside domain", ws, p)
			}
		}
		if m.IsInsideDomain("escape/secret.txt") {
			t.Errorf("ws=%s: symlink escaping the workspace must be outside domain", ws)
		}
		if m.IsInsideDomain(filepath.Join(outside, "secret.txt")) {
			t.Errorf("ws=%s: sibling directory must be outside domain", ws)
		}
	}
}

// The domain is the folder vibe runs in, for isolated sessions too.
func TestDomain_IsolatedSessionStillRequiresPermission(t *testing.T) {
	ws := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws, Isolated: true})
	if got := m.UnapprovedOutDomainPaths("Read", map[string]any{"file_path": "/etc/hostname"}); len(got) != 1 {
		t.Fatalf("expected out-of-domain read to need permission in isolated mode, got %v", got)
	}
	if got := m.UnapprovedOutDomainPaths("Read", map[string]any{"file_path": "main.go"}); len(got) != 0 {
		t.Fatalf("expected in-domain read to be allowed in isolated mode, got %v", got)
	}
}
