package lsp

import (
	"context"
	"encoding/json"
	"os"
	"time"
)

// DefaultDiagnosticsTimeout bounds a diagnostics wait when the caller names no
// timeout. Diagnostics are pushed, never requested, so this is the only thing
// standing between a silent server and a blocked turn.
const DefaultDiagnosticsTimeout = 5 * time.Second

type initializeParams struct {
	ProcessID    int              `json:"processId"`
	ClientInfo   *clientInfo      `json:"clientInfo,omitempty"`
	RootURI      string           `json:"rootUri"`
	Capabilities clientCapability `json:"capabilities"`
}

type clientInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

type clientCapability struct {
	TextDocument *textDocumentClientCapability `json:"textDocument,omitempty"`
}

type textDocumentClientCapability struct {
	PublishDiagnostics *publishDiagnosticsClientCapability `json:"publishDiagnostics,omitempty"`
	Synchronization    *synchronizationClientCapability    `json:"synchronization,omitempty"`
}

// publishDiagnosticsClientCapability asks for exactly what makes waiting
// deterministic. Version support is what makes a server stamp each list with
// the document version it describes; related information is refused because it
// multiplies the size of every payload for context the model cannot use.
type publishDiagnosticsClientCapability struct {
	RelatedInformation bool `json:"relatedInformation"`
	VersionSupport     bool `json:"versionSupport"`
}

type synchronizationClientCapability struct {
	DynamicRegistration bool `json:"dynamicRegistration"`
	DidSave             bool `json:"didSave"`
	WillSave            bool `json:"willSave"`
}

type InitializeResult struct {
	Capabilities struct {
		// TextDocumentSync is sent as either a bare number or an object
		// carrying the same field, so it stays raw until someone needs it.
		TextDocumentSync json.RawMessage `json:"textDocumentSync,omitempty"`
	} `json:"capabilities"`
	ServerInfo *struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	} `json:"serverInfo,omitempty"`
}

type textDocumentIdentifier struct {
	URI string `json:"uri"`
}

type textDocumentItem struct {
	URI        string `json:"uri"`
	LanguageID string `json:"languageId"`
	Version    int    `json:"version"`
	Text       string `json:"text"`
}

type versionedTextDocumentIdentifier struct {
	URI     string `json:"uri"`
	Version int    `json:"version"`
}

// Initialize performs the handshake and returns the server's capabilities. The
// response is received before the initialized notification is sent, because a
// server is allowed to answer anything that arrives before its own startup has
// finished with an error.
func (c *Client) Initialize(ctx context.Context, rootURI string) (*InitializeResult, error) {
	params := initializeParams{
		ProcessID:  os.Getpid(),
		ClientInfo: &clientInfo{Name: "vibe-coder", Version: "dev"},
		RootURI:    rootURI,
		Capabilities: clientCapability{
			TextDocument: &textDocumentClientCapability{
				PublishDiagnostics: &publishDiagnosticsClientCapability{
					RelatedInformation: false,
					VersionSupport:     true,
				},
			},
		},
	}
	var result InitializeResult
	if err := c.Call(ctx, "initialize", params, &result); err != nil {
		return nil, err
	}
	if err := c.Notify("initialized", struct{}{}); err != nil {
		return nil, err
	}
	return &result, nil
}

// DidOpen tells the server the client now owns a document.
func (c *Client) DidOpen(uri, languageID string, version int, text string) error {
	return c.Notify("textDocument/didOpen", map[string]any{
		"textDocument": textDocumentItem{URI: uri, LanguageID: languageID, Version: version, Text: text},
	})
}

// DidChange replaces a document's whole content. A change event without a range
// means "this is the whole document" in every sync mode, so the client never
// has to track incremental edits to stay correct.
func (c *Client) DidChange(uri string, version int, text string) error {
	return c.Notify("textDocument/didChange", map[string]any{
		"textDocument":   versionedTextDocumentIdentifier{URI: uri, Version: version},
		"contentChanges": []map[string]any{{"text": text}},
	})
}

// DidClose releases a document the client no longer owns, so a reused server
// does not hold every file the session ever touched.
func (c *Client) DidClose(uri string) error {
	return c.Notify("textDocument/didClose", map[string]any{
		"textDocument": textDocumentIdentifier{URI: uri},
	})
}

// Diagnostics returns the last published list for a document and whether any
// list was ever published for it.
func (c *Client) Diagnostics(uri string) ([]Diagnostic, bool) {
	return c.store.latest(uri)
}

// WaitDiagnostics blocks until the server publishes a list for uri describing
// at least minVersion, then returns it. The bool reports whether such a
// publication was seen: a timeout returns the last known list flagged as not an
// answer to the edit at hand, and an empty list with true means the file is
// clean. A timeout of zero uses DefaultDiagnosticsTimeout.
func (c *Client) WaitDiagnostics(ctx context.Context, uri string, minVersion int, timeout time.Duration) ([]Diagnostic, bool) {
	if timeout <= 0 {
		timeout = DefaultDiagnosticsTimeout
	}
	return c.store.wait(ctx, uri, minVersion, timeout)
}
