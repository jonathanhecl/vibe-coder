package lsp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The stub language server the process tests spawn runs inside the test binary
// itself. TestMain hands stdio over when the script standing in for a real
// server binary sets LSP_STUB_SERVER, so spawn, handshake, document sync,
// diagnostics and shutdown are exercised across a real process boundary on a
// machine with no gopls, pylsp or rust-analyzer installed.
const (
	stubServerEnv = "LSP_STUB_SERVER"
	stubModeEnv   = "LSP_STUB_MODE"
	stubLogEnv    = "LSP_STUB_LOG"

	// stubBugMarker is the text a document has to contain for the stub to
	// report an error, so a test states its case in the file it writes.
	stubBugMarker = "STUB_BUG"
)

// Stub modes: what kind of misbehaving or well-behaved server to be.
const (
	stubModeOK     = "ok"
	stubModeFail   = "fail"
	stubModeSilent = "silent"
	stubModeQuiet  = "quiet"
)

func TestMain(m *testing.M) {
	if os.Getenv(stubServerEnv) == "1" {
		os.Exit(stubServe(os.Getenv(stubModeEnv), os.Getenv(stubLogEnv)))
	}
	os.Exit(m.Run())
}

// stubServe is the server side of the protocol:
//
//   - ok: initialize, then a publication per document sync
//   - fail: exits before reading, standing in for a server that cannot start
//   - silent: reads and never answers, standing in for a hung server
//   - quiet: completes the handshake and never publishes diagnostics
func stubServe(mode, logPath string) int {
	events := &stubEvents{path: logPath}
	switch mode {
	case stubModeFail:
		events.add("fail")
		return 1
	case stubModeSilent:
		events.add("silent")
		_, _ = io.Copy(io.Discard, os.Stdin)
		return 0
	}

	events.add("start")
	reader := bufio.NewReader(os.Stdin)
	for {
		payload, err := ReadMessage(reader)
		if err != nil {
			events.add("eof")
			return 0
		}
		var msg incomingMessage
		if err := json.Unmarshal(payload, &msg); err != nil {
			continue
		}
		switch msg.Method {
		case "initialize":
			events.add("initialize")
			stubWrite(events, outgoingMessage{
				JSONRPC: protocolVersion,
				ID:      msg.ID,
				Result:  json.RawMessage(`{"capabilities":{"textDocumentSync":1}}`),
			})
		case "initialized":
			events.add("initialized")
		case "shutdown":
			events.add("shutdown")
			stubWrite(events, outgoingMessage{JSONRPC: protocolVersion, ID: msg.ID, Result: json.RawMessage("null")})
		case "exit":
			events.add("exit")
			return 0
		case "textDocument/didOpen", "textDocument/didChange":
			doc := stubDocument(msg.Params)
			verb := "open"
			if msg.Method == "textDocument/didChange" {
				verb = "change"
			}
			events.add("%s %s %d %s", verb, doc.URI, doc.Version, doc.LanguageID)
			if mode == stubModeQuiet {
				continue
			}
			events.add("publish %s %d", doc.URI, doc.Version)
			stubWrite(events, outgoingMessage{
				JSONRPC: protocolVersion,
				Method:  "textDocument/publishDiagnostics",
				Params:  stubPublishParams(doc),
			})
		case "textDocument/didClose":
			events.add("close %s", stubDocument(msg.Params).URI)
		}
	}
}

// stubDocumentPayload is the textDocument object of a didOpen/didChange.
type stubDocumentPayload struct {
	URI        string
	LanguageID string
	Version    int
	Text       string
}

func stubDocument(raw json.RawMessage) stubDocumentPayload {
	var payload struct {
		TextDocument struct {
			URI        string `json:"uri"`
			LanguageID string `json:"languageId"`
			Version    int    `json:"version"`
			Text       string `json:"text"`
		} `json:"textDocument"`
	}
	_ = json.Unmarshal(raw, &payload)
	return stubDocumentPayload{
		URI:        payload.TextDocument.URI,
		LanguageID: payload.TextDocument.LanguageID,
		Version:    payload.TextDocument.Version,
		Text:       payload.TextDocument.Text,
	}
}

// stubPublishParams builds the notification payload as raw wire JSON rather
// than from the client's own types. A server writes the format, not this
// package's structs, and building it by hand keeps the test honest: the code is
// a string here exactly as a real server sends it, whether or not this client's
// decoder could also produce one.
func stubPublishParams(doc stubDocumentPayload) json.RawMessage {
	items := "[]"
	if strings.Contains(doc.Text, stubBugMarker) {
		items = `[{"range":{"start":{"line":0,"character":0},"end":{"line":0,"character":1}},` +
			`"severity":1,"code":"stub","source":"stub","message":"undefined: ` + stubBugMarker + `"}]`
	}
	return json.RawMessage(fmt.Sprintf(`{"uri":%q,"version":%d,"diagnostics":%s}`, doc.URI, doc.Version, items))
}

func stubWrite(events *stubEvents, msg outgoingMessage) {
	payload, err := json.Marshal(msg)
	if err != nil {
		events.add("encode-error %v", err)
		return
	}
	if err := WriteMessage(os.Stdout, payload); err != nil {
		events.add("write-error %v", err)
	}
}

// stubEvents appends one line per protocol event to a log the test reads, so
// assertions are about what the server actually received rather than about the
// state the client believes it is in.
type stubEvents struct {
	mu   sync.Mutex
	path string
}

func (e *stubEvents) add(format string, args ...any) {
	if e.path == "" {
		return
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	file, err := os.OpenFile(e.path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer file.Close()
	_, _ = fmt.Fprintf(file, format+"\n", args...)
}

// fakeServer is a shell script named after a real language server and placed on
// PATH, which re-executes the test binary in stub mode. Naming it after the real
// binary is what lets marker detection and PATH lookup be exercised for real
// instead of being stubbed out.
type fakeServer struct {
	dir     string
	name    string
	logPath string
	mode    string
}

// installFakeServer installs one fake server and leaves only its directory on
// PATH, so detection sees exactly the servers a test intends and none the
// machine happens to have.
func installFakeServer(t *testing.T, mode, name string) *fakeServer {
	t.Helper()
	return installFakeServers(t, mode, name)[0]
}

// installFakeServers installs several fake servers into one directory on PATH.
func installFakeServers(t *testing.T, mode string, names ...string) []*fakeServer {
	t.Helper()
	dir := t.TempDir()
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	servers := make([]*fakeServer, 0, len(names))
	for _, name := range names {
		server := &fakeServer{
			dir:     dir,
			name:    name,
			logPath: filepath.Join(dir, name+".events.log"),
			mode:    mode,
		}
		server.write(t, bin)
		servers = append(servers, server)
	}
	t.Setenv("PATH", dir)
	return servers
}

func (f *fakeServer) write(t *testing.T, bin string) {
	t.Helper()
	// exec replaces the shell, so killing the server kills the stub itself and no
	// shell process is left behind holding the pipes.
	script := fmt.Sprintf("#!/bin/sh\n%s=1 %s=%s %s=%s exec %s\n",
		stubServerEnv,
		stubModeEnv, shellQuote(f.mode),
		stubLogEnv, shellQuote(f.logPath),
		shellQuote(bin),
	)
	if err := os.WriteFile(filepath.Join(f.dir, f.name), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake server %s: %v", f.name, err)
	}
}

// setMode rewrites the script, which is how a test turns a server that could not
// start into one that can, without restarting the manager.
func (f *fakeServer) setMode(t *testing.T, mode string) {
	t.Helper()
	bin, err := os.Executable()
	if err != nil {
		t.Fatalf("locate the test binary: %v", err)
	}
	f.mode = mode
	f.write(t, bin)
}

// events returns the protocol events the server logged, in order.
func (f *fakeServer) events(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile(f.logPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		t.Fatalf("read the stub log: %v", err)
	}
	text := strings.TrimSpace(string(data))
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// count counts logged events carrying a prefix.
func (f *fakeServer) count(t *testing.T, prefix string) int {
	t.Helper()
	total := 0
	for _, event := range f.events(t) {
		if strings.HasPrefix(event, prefix) {
			total++
		}
	}
	return total
}

func hasEvent(events []string, prefix string) bool {
	for _, event := range events {
		if strings.HasPrefix(event, prefix) {
			return true
		}
	}
	return false
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}
