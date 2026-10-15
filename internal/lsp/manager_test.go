package lsp

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create directory for %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func newTestManager(t *testing.T, root string) *Manager {
	t.Helper()
	manager := NewManager(Config{Root: root})
	t.Cleanup(manager.Close)
	return manager
}

// noServers clears PATH, so nothing in the machine's real installs can make a
// detection test pass by accident.
func noServers(t *testing.T) {
	t.Helper()
	t.Setenv("PATH", t.TempDir())
}

// goRoot is the smallest thing that looks like a Go workspace.
func goRoot(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/stub\n")
	return root
}

func TestResolveRequiresMarkerAndBinary(t *testing.T) {
	cases := []struct {
		name    string
		marker  bool
		install bool
		want    bool
	}{
		{name: "marker and binary", marker: true, install: true, want: true},
		{name: "marker without binary", marker: true, install: false, want: false},
		{name: "binary without marker", marker: false, install: true, want: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if tc.marker {
				writeFile(t, filepath.Join(root, "go.mod"), "module example.com/stub\n")
			}
			if tc.install {
				installFakeServer(t, stubModeOK, "gopls")
			} else {
				noServers(t)
			}
			manager := newTestManager(t, root)
			if got := manager.Supports(filepath.Join(root, "main.go")); got != tc.want {
				t.Errorf("Supports(main.go) = %v, want %v", got, tc.want)
			}
		})
	}
}

// A positive answer is what the session is committed to, exactly like the
// auto-test's cached detection: once a workspace has gopls, it keeps it even if
// the marker or the binary disappears mid-session.
func TestResolveCachesTheFirstPositive(t *testing.T) {
	root := goRoot(t)
	installFakeServer(t, stubModeOK, "gopls")
	manager := newTestManager(t, root)

	file := filepath.Join(root, "main.go")
	if !manager.Supports(file) {
		t.Fatal("Supports(main.go) = false, want true while gopls and go.mod are there")
	}

	if err := os.Remove(filepath.Join(root, "go.mod")); err != nil {
		t.Fatalf("remove the marker: %v", err)
	}
	noServers(t)

	if !manager.Supports(file) {
		t.Error("Supports(main.go) = false after the marker and the binary went away, want the cached yes")
	}
	if resolved := manager.Resolved(); resolved["go"] != "gopls" {
		t.Errorf("Resolved() = %v, want go to stay resolved", resolved)
	}
}

// A negative answer is re-checked, so installing a server mid-session works.
func TestResolveRetriesANegative(t *testing.T) {
	root := goRoot(t)
	noServers(t)
	manager := newTestManager(t, root)

	file := filepath.Join(root, "main.go")
	if manager.Supports(file) {
		t.Fatal("Supports(main.go) = true with no gopls on PATH")
	}

	installFakeServer(t, stubModeOK, "gopls")
	if !manager.Supports(file) {
		t.Error("Supports(main.go) = false after gopls appeared, want the negative re-probed")
	}
}

func TestProbeHonorsOverridesWithoutMarker(t *testing.T) {
	root := t.TempDir()
	installFakeServer(t, stubModeOK, "gopls")
	manager := NewManager(Config{
		Root:    root,
		Servers: map[string][]string{"go": {"gopls", "-remote=auto"}},
	})
	t.Cleanup(manager.Close)

	if got := manager.Resolved()["go"]; got != "gopls -remote=auto" {
		t.Errorf("Resolved()[go] = %q, want the configured command line", got)
	}
}

func TestProbeRejectsOverrideWithoutBinary(t *testing.T) {
	root := t.TempDir()
	noServers(t)
	manager := NewManager(Config{
		Root:    root,
		Servers: map[string][]string{"go": {"gopls"}},
	})
	t.Cleanup(manager.Close)

	if resolved := manager.Resolved(); len(resolved) != 0 {
		t.Errorf("Resolved() = %v, want nothing when the named binary is missing", resolved)
	}
	if manager.Available() {
		t.Error("Available() = true, want false when the only configured server is missing")
	}
}

// A workspace can hold several languages while only one of them has a server
// installed; the others must simply stay unresolved.
func TestResolvedListsOnlyUsableFamilies(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "go.mod"), "module example.com/stub\n")
	writeFile(t, filepath.Join(root, "package.json"), "{\"name\":\"stub\"}\n")
	installFakeServer(t, stubModeOK, "gopls")
	manager := newTestManager(t, root)

	resolved := manager.Resolved()
	if _, ok := resolved["go"]; !ok {
		t.Errorf("Resolved() = %v, want go", resolved)
	}
	if _, ok := resolved["typescript"]; ok {
		t.Errorf("Resolved() = %v, want no typescript without the binary", resolved)
	}
	if !manager.Available() {
		t.Error("Available() = false, want true while one language has a server")
	}
}

func TestSupportsRejectsUnsupportedFiles(t *testing.T) {
	root := goRoot(t)
	installFakeServer(t, stubModeOK, "gopls")
	manager := newTestManager(t, root)

	if manager.Supports(filepath.Join(root, "README.md")) {
		t.Error("Supports(README.md) = true, want no server for a file no family owns")
	}
	if !manager.Supports(filepath.Join(root, "main.go")) {
		t.Error("Supports(main.go) = false, want true")
	}
}

func TestDiagnosticsReportsNoServer(t *testing.T) {
	root := goRoot(t)
	noServers(t)
	manager := newTestManager(t, root)

	_, err := manager.Diagnostics(context.Background(), filepath.Join(root, "main.go"))
	if !errors.Is(err, ErrNoServer) {
		t.Errorf("Diagnostics(main.go) error = %v, want ErrNoServer", err)
	}
	_, err = manager.Diagnostics(context.Background(), filepath.Join(root, "README.md"))
	if !errors.Is(err, ErrNoServer) {
		t.Errorf("Diagnostics(README.md) error = %v, want ErrNoServer", err)
	}
}

// A missing file is reported as a missing file, not as a missing server: the
// caller asked about a path that is not there.
func TestDiagnosticsReportsAMissingFile(t *testing.T) {
	root := goRoot(t)
	installFakeServer(t, stubModeOK, "gopls")
	manager := newTestManager(t, root)

	_, err := manager.Diagnostics(context.Background(), filepath.Join(root, "missing.go"))
	if !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Diagnostics(missing.go) error = %v, want a not-exist error", err)
	}
}

func TestDiagnosticsAfterClose(t *testing.T) {
	installFakeServer(t, stubModeOK, "gopls")
	root, file := goWorkspace(t)
	manager := NewManager(Config{Root: root})
	manager.Close()

	_, err := manager.Diagnostics(context.Background(), file)
	if !errors.Is(err, ErrClosed) {
		t.Errorf("Diagnostics after Close error = %v, want ErrClosed", err)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	root := goRoot(t)
	installFakeServer(t, stubModeOK, "gopls")
	manager := NewManager(Config{Root: root})

	manager.Close()
	// Calling it twice is what makes it safe for every cleanup path to call it.
	manager.Close()
}

// A manager nobody asked for diagnostics from must not leave anything running:
// nothing is spawned until a file needs a server.
func TestNewManagerStartsNothing(t *testing.T) {
	root := goRoot(t)
	server := installFakeServer(t, stubModeOK, "gopls")
	manager := newTestManager(t, root)

	if !manager.Available() {
		t.Fatal("Available() = false, want true while gopls and go.mod are there")
	}
	if events := server.events(t); len(events) != 0 {
		t.Errorf("the server saw %v before any diagnostics were asked for, want nothing", events)
	}
}

// A caller may name any path relative to the workspace root.
func TestDiagnosticsAcceptsARelativePath(t *testing.T) {
	root := goRoot(t)
	writeFile(t, filepath.Join(root, "main.go"), "package main\n\nfunc main() {}\n")
	server := installFakeServer(t, stubModeOK, "gopls")
	manager := newTestManager(t, root)

	result, err := manager.Diagnostics(context.Background(), "main.go")
	if err != nil {
		t.Fatalf("Diagnostics(main.go) error = %v", err)
	}
	if !result.Fresh {
		t.Error("Fresh = false, want the publication for the version just sent")
	}
	if uri := fileURI(filepath.Join(root, "main.go")); !hasEvent(server.events(t), "open "+uri) {
		t.Errorf("the server was not asked about %s, events: %v", uri, server.events(t))
	}
}
