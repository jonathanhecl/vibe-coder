package lsp

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

func intPtr(v int) *int { return &v }

// diagnosticEntry builds one diagnostic the way a server sends it, so the tests
// exercise the decoding path rather than a hand-built struct.
func diagnosticEntry(message string) map[string]any {
	return map[string]any{
		"range": map[string]any{
			"start": map[string]any{"line": 1, "character": 2},
			"end":   map[string]any{"line": 1, "character": 5},
		},
		"severity": SeverityError,
		"message":  message,
	}
}

func publish(uri string, version int, messages ...string) map[string]any {
	diagnostics := make([]any, 0, len(messages))
	for _, message := range messages {
		diagnostics = append(diagnostics, diagnosticEntry(message))
	}
	return map[string]any{"uri": uri, "version": version, "diagnostics": diagnostics}
}

// waitForDiagnostics polls until a list has been published for uri.
func waitForDiagnostics(t *testing.T, client *Client, uri string) []Diagnostic {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if diagnostics, ok := client.Diagnostics(uri); ok {
			return diagnostics
		}
		if time.Now().After(deadline) {
			t.Fatalf("no diagnostics were published for %s", uri)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestParsePublishDiagnostics(t *testing.T) {
	tests := []struct {
		name        string
		payload     string
		wantURI     string
		wantVersion *int
		wantCount   int
		wantCode    string
		wantLabel   string
		wantErr     bool
	}{
		{
			name:        "numeric code",
			payload:     `{"uri":"file:///a.go","version":3,"diagnostics":[{"range":{"start":{"line":1,"character":2},"end":{"line":1,"character":5}},"severity":1,"code":12,"source":"compiler","message":"undefined: x"}]}`,
			wantURI:     "file:///a.go",
			wantVersion: intPtr(3),
			wantCount:   1,
			wantCode:    "12",
			wantLabel:   "error",
		},
		{
			name:      "string code",
			payload:   `{"uri":"file:///a.go","diagnostics":[{"range":{},"severity":2,"code":"SA1000","message":"warn"}]}`,
			wantURI:   "file:///a.go",
			wantCount: 1,
			wantCode:  "SA1000",
			wantLabel: "warning",
		},
		{
			name:        "clean document",
			payload:     `{"uri":"file:///a.go","version":9,"diagnostics":[]}`,
			wantURI:     "file:///a.go",
			wantVersion: intPtr(9),
			wantCount:   0,
		},
		{
			name:      "severity omitted",
			payload:   `{"uri":"file:///a.go","diagnostics":[{"range":{},"message":"?"}]}`,
			wantURI:   "file:///a.go",
			wantCount: 1,
			wantLabel: "unknown",
		},
		{
			name:        "diagnostics field absent",
			payload:     `{"uri":"file:///a.go","version":2}`,
			wantURI:     "file:///a.go",
			wantVersion: intPtr(2),
			wantCount:   0,
		},
		{name: "uri missing", payload: `{"diagnostics":[]}`, wantErr: true},
		{name: "payload malformed", payload: `{"uri":`, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			uri, version, diagnostics, err := parsePublishDiagnostics(json.RawMessage(tt.payload))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("parsePublishDiagnostics returned %d diagnostics, want an error", len(diagnostics))
				}
				return
			}
			if err != nil {
				t.Fatalf("parsePublishDiagnostics: %v", err)
			}
			if uri != tt.wantURI {
				t.Fatalf("uri = %q, want %q", uri, tt.wantURI)
			}
			switch {
			case tt.wantVersion == nil && version != nil:
				t.Fatalf("version = %d, want none", *version)
			case tt.wantVersion != nil && version == nil:
				t.Fatalf("version = none, want %d", *tt.wantVersion)
			case tt.wantVersion != nil && *version != *tt.wantVersion:
				t.Fatalf("version = %d, want %d", *version, *tt.wantVersion)
			}
			if len(diagnostics) != tt.wantCount {
				t.Fatalf("diagnostics = %+v, want %d", diagnostics, tt.wantCount)
			}
			if tt.wantCount == 0 {
				return
			}
			if diagnostics[0].Code.Value != tt.wantCode {
				t.Fatalf("code = %q, want %q", diagnostics[0].Code.Value, tt.wantCode)
			}
			if got := diagnostics[0].Label(); got != tt.wantLabel {
				t.Fatalf("label = %q, want %q", got, tt.wantLabel)
			}
		})
	}
}

func TestDiagnosticLabelNamesEverySeverity(t *testing.T) {
	tests := []struct {
		severity int
		want     string
	}{
		{severity: SeverityError, want: "error"},
		{severity: SeverityWarning, want: "warning"},
		{severity: SeverityInformation, want: "info"},
		{severity: SeverityHint, want: "hint"},
		{severity: 0, want: "unknown"},
		{severity: 99, want: "unknown"},
	}
	for _, tt := range tests {
		t.Run("severity "+strconv.Itoa(tt.severity), func(t *testing.T) {
			if got := (Diagnostic{Severity: tt.severity}).Label(); got != tt.want {
				t.Fatalf("Label() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDiagnosticsStoreTreatsACleanPublicationAsAnAnswer(t *testing.T) {
	store := newDiagnosticsStore()
	const uri = "file:///a.go"
	store.publish(uri, intPtr(3), nil)

	diagnostics, ok := store.wait(context.Background(), uri, 3, time.Second)
	if !ok {
		t.Fatal("a publication with no diagnostics was not treated as an answer")
	}
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %+v, want an empty list", diagnostics)
	}
}

func TestDiagnosticsStoreKeepsWaitingThroughAnOlderVersion(t *testing.T) {
	store := newDiagnosticsStore()
	const uri = "file:///a.go"
	store.publish(uri, intPtr(1), []Diagnostic{{Message: "stale"}})

	type answer struct {
		diagnostics []Diagnostic
		ok          bool
	}
	done := make(chan answer, 1)
	go func() {
		diagnostics, ok := store.wait(context.Background(), uri, 2, time.Second)
		done <- answer{diagnostics: diagnostics, ok: ok}
	}()

	// Give the waiter time to subscribe so the repeat publication below is
	// delivered to it and not merely stored.
	time.Sleep(20 * time.Millisecond)
	store.publish(uri, intPtr(1), []Diagnostic{{Message: "still stale"}})
	store.publish(uri, intPtr(2), []Diagnostic{{Message: "fresh"}})

	select {
	case got := <-done:
		if !got.ok {
			t.Fatal("the wait reported no publication for the version it asked for")
		}
		if len(got.diagnostics) != 1 || got.diagnostics[0].Message != "fresh" {
			t.Fatalf("diagnostics = %+v, want the list for version 2", got.diagnostics)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the wait never returned")
	}
}

func TestWaitDiagnosticsReturnsTheVersionJustSent(t *testing.T) {
	const uri = "file:///workspace/a.go"
	// A server that answers an edit twice: once for the previous version,
	// still in flight, and once for the edit that was just sent.
	stub, client := startStub(t, func(s *stubServer, method string, params json.RawMessage, id *json.RawMessage) {
		if method != "textDocument/didChange" {
			return
		}
		var change struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int    `json:"version"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(params, &change); err != nil {
			return
		}
		s.notify("textDocument/publishDiagnostics", publish(change.TextDocument.URI, change.TextDocument.Version-1, "stale"))
		s.notify("textDocument/publishDiagnostics", publish(change.TextDocument.URI, change.TextDocument.Version, "fresh"))
	})

	if err := client.DidChange(uri, 2, "package a\n\n"); err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	diagnostics, ok := client.WaitDiagnostics(ctx, uri, 2, time.Second)
	if !ok {
		t.Fatal("no publication matched the version that was sent")
	}
	if len(diagnostics) != 1 || diagnostics[0].Message != "fresh" {
		t.Fatalf("diagnostics = %+v, want the list for version 2", diagnostics)
	}
	stub.await(t, "textDocument/didChange")
}

func TestWaitDiagnosticsTimesOutWithoutAPublication(t *testing.T) {
	_, client := startStub(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	const timeout = 40 * time.Millisecond
	start := time.Now()
	diagnostics, ok := client.WaitDiagnostics(ctx, "file:///never.go", 1, timeout)

	if ok {
		t.Fatal("a wait without any publication reported success")
	}
	if diagnostics != nil {
		t.Fatalf("diagnostics = %+v, want none", diagnostics)
	}
	if elapsed := time.Since(start); elapsed < timeout {
		t.Fatalf("the wait returned after %s, before its timeout", elapsed)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the wait took %s, want the timeout to bound it", elapsed)
	}
}

func TestWaitDiagnosticsAcceptsAVersionlessPublication(t *testing.T) {
	const uri = "file:///workspace/a.go"
	stub, client := startStub(t, nil)
	go func() {
		// Published after the wait is registered, the way a server answers an
		// edit it was just told about.
		time.Sleep(50 * time.Millisecond)
		stub.notify("textDocument/publishDiagnostics", map[string]any{
			"uri": uri, "diagnostics": []any{diagnosticEntry("because")},
		})
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	diagnostics, ok := client.WaitDiagnostics(ctx, uri, 7, time.Second)
	if !ok {
		t.Fatal("a publication without a version was ignored")
	}
	if len(diagnostics) != 1 || diagnostics[0].Message != "because" {
		t.Fatalf("diagnostics = %+v, want the published list", diagnostics)
	}
}

func TestWaitDiagnosticsHonoursContextCancellation(t *testing.T) {
	_, client := startStub(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	_, ok := client.WaitDiagnostics(ctx, "file:///never.go", 1, 5*time.Second)
	if ok {
		t.Fatal("a cancelled wait reported success")
	}
	if elapsed := time.Since(start); elapsed > time.Second {
		t.Fatalf("the wait took %s, want the context to bound it", elapsed)
	}
}

func TestDiagnosticsReturnsTheLastPublishedList(t *testing.T) {
	const uri = "file:///workspace/a.go"
	stub, client := startStub(t, nil)
	stub.notify("textDocument/publishDiagnostics", publish(uri, 4, "boom"))

	diagnostics := waitForDiagnostics(t, client, uri)
	if len(diagnostics) != 1 || diagnostics[0].Message != "boom" {
		t.Fatalf("diagnostics = %+v, want the published list", diagnostics)
	}
	if _, ok := client.Diagnostics("file:///workspace/other.go"); ok {
		t.Fatal("a document that was never published reported diagnostics")
	}
}

func TestWaitDiagnosticsReturnsImmediatelyForAKnownVersion(t *testing.T) {
	const uri = "file:///workspace/a.go"
	stub, client := startStub(t, nil)
	stub.notify("textDocument/publishDiagnostics", publish(uri, 5, "cached"))
	waitForDiagnostics(t, client, uri)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	diagnostics, ok := client.WaitDiagnostics(ctx, uri, 5, 5*time.Second)
	if !ok {
		t.Fatal("a list for the requested version was not recognised")
	}
	if len(diagnostics) != 1 || diagnostics[0].Message != "cached" {
		t.Fatalf("diagnostics = %+v, want the stored list", diagnostics)
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("the wait took %s, want the stored list without waiting", elapsed)
	}
}
