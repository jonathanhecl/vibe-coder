package lsp

import (
	"context"
	"errors"
	"fmt"
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

// eventIndex is the position of the first event carrying a prefix, or -1.
func eventIndex(events []string, prefix string) int {
	for i, event := range events {
		if strings.HasPrefix(event, prefix) {
			return i
		}
	}
	return -1
}

// awaitCount waits for a prefix to appear at least want times in the server's
// log. The log is written by the server process as it reads, so it lags the call
// that caused it: an assertion on it has to wait rather than assume the write
// already happened. Callers keep their own equality check afterwards, so more
// events than expected still fail.
func awaitCount(t *testing.T, server *fakeServer, prefix string, want int) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for server.count(t, prefix) < want {
		if time.Now().After(deadline) {
			t.Fatalf("the server logged %q %d times, want %d; events: %v", prefix, server.count(t, prefix), want, server.events(t))
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestEvictionClosesTheLeastRecentlyUsedDocument(t *testing.T) {
	server := installFakeServer(t, stubModeOK, "gopls")
	root, first := goWorkspace(t)
	manager := newTestManager(t, root)
	ctx := context.Background()

	if _, err := manager.Diagnostics(ctx, first); err != nil {
		t.Fatalf("first edit error = %v", err)
	}
	for i := 1; i < maxOpenDocuments; i++ {
		file := filepath.Join(root, fmt.Sprintf("file%02d.go", i))
		writeFile(t, file, "package main\n")
		if _, err := manager.Diagnostics(ctx, file); err != nil {
			t.Fatalf("edit %d error = %v", i, err)
		}
	}
	if closes := server.count(t, "close "); closes != 0 {
		t.Fatalf("closed %d documents with the window still full, want 0", closes)
	}

	// One edit past the window pushes out the document that has gone longest
	// without being edited, which is the first one.
	extra := filepath.Join(root, "extra.go")
	writeFile(t, extra, "package main\n")
	if _, err := manager.Diagnostics(ctx, extra); err != nil {
		t.Fatalf("edit past the window error = %v", err)
	}

	awaitCount(t, server, "close ", 1)
	events := server.events(t)
	if closes := server.count(t, "close "); closes != 1 {
		t.Errorf("closed documents = %d, want exactly the evicted one; events: %v", closes, events)
	}
	if want := "close " + fileURI(first); !hasEvent(events, want) {
		t.Errorf("the evicted document is not the least recently edited one; events: %v", events)
	}

	// The version the server had for the evicted document went with it, so
	// coming back is an open, not a change at a version the server forgot.
	if _, err := manager.Diagnostics(ctx, first); err != nil {
		t.Fatalf("reopen error = %v", err)
	}
	awaitCount(t, server, "open "+fileURI(first), 2)
	if opens := server.count(t, "open "+fileURI(first)); opens != 2 {
		t.Errorf("opens for the reopened document = %d, want 2; events: %v", opens, server.events(t))
	}
}

func TestCloseReleasesOpenDocumentsBeforeShutdown(t *testing.T) {
	server := installFakeServer(t, stubModeOK, "gopls")
	root, first := goWorkspace(t)
	second := filepath.Join(root, "second.go")
	writeFile(t, second, "package main\n")

	manager := NewManager(Config{Root: root})
	ctx := context.Background()
	for _, file := range []string{first, second} {
		if _, err := manager.Diagnostics(ctx, file); err != nil {
			t.Fatalf("edit %s error = %v", file, err)
		}
	}

	manager.Close()

	events := server.events(t)
	shutdown := eventIndex(events, "shutdown")
	if shutdown < 0 {
		t.Fatalf("the server was never asked to shut down; events: %v", events)
	}
	for _, file := range []string{first, second} {
		at := eventIndex(events, "close "+fileURI(file))
		if at < 0 {
			t.Errorf("document %s was still open when the server was shut down; events: %v", file, events)
			continue
		}
		// A server that has been told to shut down stops reading, so a document
		// released after that point keeps the parse it was supposed to free.
		if at > shutdown {
			t.Errorf("close for %s arrived after the shutdown (at %d, shutdown at %d); events: %v", file, at, shutdown, events)
		}
	}
}
