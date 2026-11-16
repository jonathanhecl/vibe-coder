package permissions

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// A sandboxed run (isolated or temporal) must never create temporary files in a
// global temp root: those paths are refused outright, not offered to the user as
// an out-of-domain prompt that yes mode or a saved rule could approve.
func TestSandboxBlocksGlobalTempPaths(t *testing.T) {
	for _, mode := range []struct {
		name string
		cfg  func(cwd string) *config.Config
	}{
		{"isolated", func(cwd string) *config.Config { return &config.Config{Cwd: cwd, Isolated: true, YesMode: true} }},
		{"temporal", func(cwd string) *config.Config { return &config.Config{Cwd: cwd, Temporal: true, YesMode: true} }},
	} {
		t.Run(mode.name, func(t *testing.T) {
			ws := t.TempDir()
			m := NewManager(mode.cfg(ws))
			// Approving everything for the session must not help: the block is
			// not a prompt.
			m.AllowSession("Bash")
			m.AllowSession("Write")

			cases := []struct {
				tool   string
				params map[string]any
			}{
				{"Bash", map[string]any{"command": "mktemp -d /tmp/vibe-work"}},
				{"Bash", map[string]any{"command": "echo hi > /tmp/vibe-out.txt"}},
				{"Bash", map[string]any{"command": "go build -o /var/tmp/app ."}},
				{"Bash", map[string]any{"command": "sh -c 'mktemp -d /tmp/vibe-hidden'"}},
				{"Bash", map[string]any{"command": "TMPDIR=/tmp/vibe-tmp make"}},
				{"Bash", map[string]any{"command": "ls", "workdir": "/tmp"}},
				{"Bash", map[string]any{"command": "cat file.txt > /tmp/vibe-copy"}},
				{"Write", map[string]any{"file_path": "/tmp/vibe-note.md", "content": "x"}},
				{"Edit", map[string]any{"file_path": "/var/tmp/vibe-note.md"}},
			}
			for _, c := range cases {
				if m.Check(c.tool, c.params, nil) {
					t.Errorf("%s %v: expected the global temp path to be blocked", c.tool, c.params)
					continue
				}
				reason := m.LastDenialReason()
				if !strings.Contains(reason, config.LocalTempDirName) {
					t.Errorf("%s %v: denial reason should name %s, got %q", c.tool, c.params, config.LocalTempDirName, reason)
				}
				if !strings.Contains(reason, "/tmp") && !strings.Contains(reason, "/var/tmp") {
					t.Errorf("%s %v: denial reason should name the rejected path, got %q", c.tool, c.params, reason)
				}
			}

			// The block survives every approval path, including an explicit
			// out-of-domain grant for /tmp.
			m.AllowOutDomainPersistent("/tmp")
			ui := fakeUI{decision: tui.DecisionAllowOnce}
			if m.Check("Bash", map[string]any{"command": "mktemp -d /tmp/vibe-still-blocked"}, ui) {
				t.Error("an approved out-of-domain /tmp grant must not unlock a sandboxed run")
			}
		})
	}
}

// Scratch files inside the working directory stay allowed: the sandbox moves the
// temp location, it does not forbid temporary files.
func TestSandboxAllowsFolderLocalScratch(t *testing.T) {
	ws := t.TempDir()
	for _, cfg := range []*config.Config{
		{Cwd: ws, Isolated: true, YesMode: true},
		{Cwd: ws, Temporal: true, YesMode: true},
	} {
		m := NewManager(cfg)
		local := filepath.Join(ws, config.LocalTempDirName)
		cases := []struct {
			tool   string
			params map[string]any
		}{
			{"Bash", map[string]any{"command": "mktemp -d " + filepath.Join(local, "work.XXXX")}},
			{"Bash", map[string]any{"command": "echo hi > " + filepath.Join(local, "out.txt")}},
			{"Bash", map[string]any{"command": "TMPDIR=" + local + " go build ./..."}},
			{"Bash", map[string]any{"command": "ls", "workdir": local}},
			{"Write", map[string]any{"file_path": filepath.Join(local, "note.md"), "content": "x"}},
			{"Read", map[string]any{"file_path": filepath.Join(local, "note.md")}},
		}
		for _, c := range cases {
			if !m.Check(c.tool, c.params, nil) {
				t.Errorf("%s %v: expected folder-local scratch to be allowed (%s), got %q",
					c.tool, c.params, cfg.Mode(), m.LastDenialReason())
			}
		}
		// Only temp roots are in scope: a build that points a cache elsewhere
		// still runs, because the rule is about temporary files, not about
		// leaving the folder entirely (that stays an ordinary prompt).
		if got := m.TempEscapePaths("Bash", map[string]any{"command": "PKG_CONFIG_PATH=/usr/lib/pkgconfig make"}); len(got) != 0 {
			t.Errorf("a non-temp env assignment must not be blocked, got %v", got)
		}
	}
}

// A normal run is unchanged: the OS temp dir stays a prompt-free system temp
// location as before.
func TestSandboxLeavesSharedRunsAlone(t *testing.T) {
	ws := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws, YesMode: true})
	if got := m.TempEscapePaths("Write", map[string]any{"file_path": "/tmp/note.md"}); len(got) != 0 {
		t.Errorf("a shared run must not flag /tmp, got %v", got)
	}
	if !m.Check("Write", map[string]any{"file_path": "/tmp/note.md", "content": "x"}, nil) {
		t.Errorf("a shared run must keep the old /tmp behavior, got %q", m.LastDenialReason())
	}
	// `VAR=/path` assignments are read only by the sandbox check, so an ordinary
	// build command keeps prompting for nothing.
	if got := m.UnapprovedOutDomainPaths("Bash", map[string]any{"command": "GOCACHE=/home/user/.cache/go-build go build ./..."}); len(got) != 0 {
		t.Errorf("an env assignment must not become an out-of-domain prompt, got %v", got)
	}
}

// TempEscapePaths only reports paths that are both inside a global temp root and
// outside the workspace, so a project that lives in a temp directory is not
// starved of its own scratch space.
func TestTempEscapePathsScopes(t *testing.T) {
	ws := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws, Isolated: true})

	if got := m.TempEscapePaths("Read", map[string]any{"file_path": filepath.Join(ws, config.LocalTempDirName, "x")}); len(got) != 0 {
		t.Errorf("folder-local path must not escape, got %v", got)
	}
	if got := m.TempEscapePaths("Write", map[string]any{"file_path": "/etc/hostname"}); len(got) != 0 {
		t.Errorf("only temp roots are in scope for this check, got %v", got)
	}
	got := m.TempEscapePaths("Bash", map[string]any{"command": "cp a.txt /tmp/b.txt"})
	if len(got) != 1 || got[0] != "/tmp/b.txt" {
		t.Errorf("expected [/tmp/b.txt], got %v", got)
	}
	if got := m.TempEscapePaths("Bash", map[string]any{"command": "git commit -m 'never write to /tmp'"}); len(got) != 0 {
		t.Errorf("prose mentioning /tmp must not be read as a path, got %v", got)
	}
}

// A pasted clipboard image the user supplied lives in the OS temp dir in some
// modes; the user handed it over, so reading it is not the agent's escape.
func TestSandboxKeepsUserProvidedTempPaths(t *testing.T) {
	ws := t.TempDir()
	m := NewManager(&config.Config{Cwd: ws, Isolated: true})
	img := filepath.Join(os.TempDir(), "vibe_clipboard_4242.png")
	m.NoteUserProvidedPath(img)
	if got := m.TempEscapePaths("Read", map[string]any{"file_path": img}); len(got) != 0 {
		t.Errorf("a user-provided clipboard path must not be blocked, got %v", got)
	}
}
