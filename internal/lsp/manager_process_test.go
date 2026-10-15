package lsp

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// goWorkspace is a root with a Go marker and one file: the smallest setup in
// which diagnostics resolve against the fake gopls on PATH.
func goWorkspace(t *testing.T) (root string, file string) {
	t.Helper()
	root = goRoot(t)
	file = filepath.Join(root, "main.go")
	writeFile(t, file, "package main\n\nfunc main() {}\n")
	return root, file
}

func TestDiagnosticsReportsACleanFile(t *testing.T) {
	server := installFakeServer(t, stubModeOK, "gopls")
	root, file := goWorkspace(t)
	manager := newTestManager(t, root)

	result, err := manager.Diagnostics(context.Background(), file)
	if err != nil {
		t.Fatalf("Diagnostics error = %v", err)
	}
	if result.Language != "go" {
		t.Errorf("Language = %q, want go", result.Language)
	}
	if !result.Fresh {
		t.Error("Fresh = false, want the publication for the version just sent")
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %v, want none in a clean file", result.Diagnostics)
	}

	events := server.events(t)
	for _, want := range []string{"initialize", "initialized", "open " + fileURI(file) + " 1 go"} {
		if !hasEvent(events, want) {
			t.Errorf("the server saw no %q, events: %v", want, events)
		}
	}
}

func TestDiagnosticsReportsAnError(t *testing.T) {
	installFakeServer(t, stubModeOK, "gopls")
	root, file := goWorkspace(t)
	writeFile(t, file, "package main\n\nfunc main() { "+stubBugMarker+" }\n")
	manager := newTestManager(t, root)

	result, err := manager.Diagnostics(context.Background(), file)
	if err != nil {
		t.Fatalf("Diagnostics error = %v", err)
	}
	if len(result.Diagnostics) != 1 {
		t.Fatalf("Diagnostics = %v, want exactly one", result.Diagnostics)
	}
	diagnostic := result.Diagnostics[0]
	if diagnostic.Severity != SeverityError || diagnostic.Label() != "error" {
		t.Errorf("severity = %d (%s), want an error", diagnostic.Severity, diagnostic.Label())
	}
	if diagnostic.Source != "stub" {
		t.Errorf("source = %q, want stub", diagnostic.Source)
	}
	if diagnostic.Code.Value != "stub" {
		t.Errorf("code = %q, want stub (a server sends the code as a string)", diagnostic.Code.Value)
	}
	if diagnostic.Message != "undefined: "+stubBugMarker {
		t.Errorf("message = %q", diagnostic.Message)
	}
	if diagnostic.Range.Start.Line != 0 {
		t.Errorf("range start line = %d, want 0", diagnostic.Range.Start.Line)
	}
}

// One server per language per session: the second edit goes to the server that
// is already running, as a change to a document it already has open.
func TestDiagnosticsReusesOneServerAndIncrementsVersion(t *testing.T) {
	server := installFakeServer(t, stubModeOK, "gopls")
	root, file := goWorkspace(t)
	manager := newTestManager(t, root)

	for attempt := 1; attempt <= 2; attempt++ {
		if _, err := manager.Diagnostics(context.Background(), file); err != nil {
			t.Fatalf("call %d error = %v", attempt, err)
		}
	}

	if starts := server.count(t, "start"); starts != 1 {
		t.Errorf("server starts = %d, want 1", starts)
	}
	uri := fileURI(file)
	if opens := server.count(t, "open "+uri); opens != 1 {
		t.Errorf("didOpen for %s = %d, want 1", uri, opens)
	}
	if changes := server.count(t, "change "+uri+" 2"); changes != 1 {
		t.Errorf("didChange at version 2 = %d, want 1", changes)
	}
}

func TestDiagnosticsOpensASecondFile(t *testing.T) {
	server := installFakeServer(t, stubModeOK, "gopls")
	root, file := goWorkspace(t)
	other := filepath.Join(root, "other.go")
	writeFile(t, other, "package main\n")
	manager := newTestManager(t, root)

	for _, path := range []string{file, other} {
		if _, err := manager.Diagnostics(context.Background(), path); err != nil {
			t.Fatalf("Diagnostics(%s) error = %v", filepath.Base(path), err)
		}
	}

	if starts := server.count(t, "start"); starts != 1 {
		t.Errorf("server starts = %d, want 1: both files belong to the same language", starts)
	}
	if !hasEvent(server.events(t), "open "+fileURI(other)+" 1 go") {
		t.Errorf("the second file was never opened, events: %v", server.events(t))
	}
}

func TestCloseShutsTheServerDown(t *testing.T) {
	server := installFakeServer(t, stubModeOK, "gopls")
	root, file := goWorkspace(t)
	manager := newTestManager(t, root)

	if _, err := manager.Diagnostics(context.Background(), file); err != nil {
		t.Fatalf("Diagnostics error = %v", err)
	}
	manager.Close()
	manager.Close()

	events := server.events(t)
	if !hasEvent(events, "shutdown") {
		t.Errorf("the server saw no shutdown, events: %v", events)
	}
	if last := events[len(events)-1]; last != "exit" {
		t.Errorf("the last thing the server saw was %q, want exit: no language server is left behind", last)
	}

	if _, err := manager.Diagnostics(context.Background(), file); !errors.Is(err, ErrClosed) {
		t.Errorf("Diagnostics after Close error = %v, want ErrClosed", err)
	}
}

// A server that accepts the connection and never answers must not hold a turn.
// The session is killed rather than talked to, because a handshake that never
// completed has no conversation to end politely.
func TestSilentServerIsBoundedByTheHandshakeTimeout(t *testing.T) {
	installFakeServer(t, stubModeSilent, "gopls")
	root, file := goWorkspace(t)
	manager := NewManager(Config{Root: root, HandshakeTimeout: 300 * time.Millisecond})
	t.Cleanup(manager.Close)

	start := time.Now()
	_, err := manager.Diagnostics(context.Background(), file)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Diagnostics error = nil, want the handshake bound to report")
	}
	if !strings.Contains(err.Error(), "did not answer") {
		t.Errorf("error = %v, want it to name the unanswered handshake", err)
	}
	if elapsed > 5*time.Second {
		t.Errorf("waited %s for a silent server, want the handshake bound to apply", elapsed)
	}
}

// A server that cannot start is not fatal and not permanent: the next edit
// tries again, which is what makes a mid-session fix enough.
func TestCrashingServerIsNotFatalAndIsRetried(t *testing.T) {
	server := installFakeServer(t, stubModeFail, "gopls")
	root, file := goWorkspace(t)
	manager := newTestManager(t, root)

	if _, err := manager.Diagnostics(context.Background(), file); err == nil {
		t.Fatal("Diagnostics error = nil, want a failure from a server that cannot start")
	}

	server.setMode(t, stubModeOK)

	result, err := manager.Diagnostics(context.Background(), file)
	if err != nil {
		t.Fatalf("Diagnostics after the server was fixed error = %v, want a retry", err)
	}
	if !result.Fresh {
		t.Error("Fresh = false, want diagnostics from the server that now works")
	}
}

// A cancelled call is reported as cancelled, starts nothing, and must not take
// the shared server down with it: every later edit still needs it.
func TestCancelledCallKeepsTheSharedServer(t *testing.T) {
	server := installFakeServer(t, stubModeOK, "gopls")
	root, file := goWorkspace(t)
	manager := newTestManager(t, root)

	if _, err := manager.Diagnostics(context.Background(), file); err != nil {
		t.Fatalf("first call error = %v", err)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := manager.Diagnostics(cancelled, file); !errors.Is(err, context.Canceled) {
		t.Errorf("cancelled call error = %v, want context.Canceled", err)
	}

	result, err := manager.Diagnostics(context.Background(), file)
	if err != nil {
		t.Fatalf("call after the cancelled one error = %v", err)
	}
	if !result.Fresh {
		t.Error("Fresh = false, want the server to have survived the cancelled call")
	}
	if starts := server.count(t, "start"); starts != 1 {
		t.Errorf("server starts = %d, want 1", starts)
	}
	if changes := server.count(t, "change "+fileURI(file)+" 2"); changes != 1 {
		t.Errorf("didChange at version 2 = %d, want 1: the cancelled call must not have sync'd anything", changes)
	}
}

// A server that answers the handshake and never publishes is the timeout case,
// and a timeout is explicitly not an answer.
func TestServerThatNeverPublishesTimesOut(t *testing.T) {
	installFakeServer(t, stubModeQuiet, "gopls")
	root, file := goWorkspace(t)
	manager := NewManager(Config{Root: root, Timeout: 200 * time.Millisecond})
	t.Cleanup(manager.Close)

	start := time.Now()
	result, err := manager.Diagnostics(context.Background(), file)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Diagnostics error = %v, want a bounded wait to be silent, not fatal", err)
	}
	if result.Fresh {
		t.Error("Fresh = true, want false: a timeout must never look like a clean file")
	}
	if len(result.Diagnostics) != 0 {
		t.Errorf("Diagnostics = %v, want none", result.Diagnostics)
	}
	if elapsed < 200*time.Millisecond {
		t.Errorf("returned after %s, want it to wait for the timeout", elapsed)
	}
	if elapsed > 5*time.Second {
		t.Errorf("waited %s, want the diagnostics timeout to bound the turn", elapsed)
	}
}

// Eight edits of the same language at once must produce one server, not eight:
// the single-flight entry is what keeps a burst of edits from spawning a
// language server per file.
func TestConcurrentDiagnosticsStartOneServer(t *testing.T) {
	server := installFakeServer(t, stubModeOK, "gopls")
	root := goRoot(t)
	manager := newTestManager(t, root)

	files := make([]string, 8)
	for i := range files {
		files[i] = filepath.Join(root, "file"+string(rune('a'+i))+".go")
		writeFile(t, files[i], "package main\n")
	}

	errs := make([]error, len(files))
	var group sync.WaitGroup
	for i, file := range files {
		group.Add(1)
		go func(i int, file string) {
			defer group.Done()
			_, errs[i] = manager.Diagnostics(context.Background(), file)
		}(i, file)
	}
	group.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("call %d error = %v", i, err)
		}
	}
	if starts := server.count(t, "start"); starts != 1 {
		t.Errorf("server starts = %d, want 1 under concurrent edits", starts)
	}
	if events := len(server.events(t)); events == 0 {
		t.Error("the server saw nothing")
	}
}

// Every file the manager reports on is a file the server was told about, so a
// publication can always be matched to a document the server holds.
func TestEveryReportIsAboutAnOpenedDocument(t *testing.T) {
	server := installFakeServer(t, stubModeOK, "gopls")
	root, file := goWorkspace(t)
	manager := newTestManager(t, root)

	for i := 0; i < 3; i++ {
		if _, err := manager.Diagnostics(context.Background(), file); err != nil {
			t.Fatalf("call %d error = %v", i+1, err)
		}
	}

	opens := server.count(t, "open "+fileURI(file))
	if opens != 1 {
		t.Errorf("didOpen for the file = %d, want 1", opens)
	}
	if versions := server.count(t, "publish "+fileURI(file)+" "); versions != 3 {
		t.Errorf("publications = %d, want one per sync", versions)
	}
	if last := server.events(t); len(last) == 0 || !strings.HasPrefix(last[len(last)-1], "publish ") {
		t.Errorf("last event = %v, want a publication", last)
	}
}
