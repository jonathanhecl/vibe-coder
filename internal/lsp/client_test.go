package lsp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInitializeWaitsForResponseBeforeInitializedNotification(t *testing.T) {
	release := make(chan struct{})
	stub, client := startStub(t, func(s *stubServer, method string, params json.RawMessage, id *json.RawMessage) {
		if method != "initialize" {
			return
		}
		<-release
		s.respond(id, map[string]any{"capabilities": map[string]any{"textDocumentSync": 2}})
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := client.Initialize(ctx, "file:///workspace")
		done <- err
	}()

	stub.await(t, "initialize")
	if stub.saw("initialized") {
		t.Fatal("initialized was sent before the initialize response arrived")
	}
	close(release)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Initialize: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Initialize never returned after its response arrived")
	}
	stub.await(t, "initialized")
}

func TestInitializeAdvertisesWhatTheClientNeeds(t *testing.T) {
	stub, client := startStub(t, func(s *stubServer, method string, params json.RawMessage, id *json.RawMessage) {
		if method == "initialize" {
			s.respond(id, map[string]any{"capabilities": map[string]any{}})
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.Initialize(ctx, "file:///workspace"); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	stub.await(t, "initialize")

	// The stub records raw params, so these assertions read the message that
	// actually went on the wire.
	var params map[string]any
	decodeParams(t, stub.paramsFor("initialize"), &params)

	tests := []struct {
		name  string
		check func(t *testing.T)
	}{
		{
			name: "announces the workspace root",
			check: func(t *testing.T) {
				if got := params["rootUri"]; got != "file:///workspace" {
					t.Fatalf("rootUri = %v, want file:///workspace", got)
				}
			},
		},
		{
			name: "reports its own process so the server can exit with it",
			check: func(t *testing.T) {
				pid, ok := params["processId"].(float64)
				if !ok || pid <= 0 {
					t.Fatalf("processId = %v, want a live process id", params["processId"])
				}
			},
		},
		{
			name: "asks for diagnostic versions",
			check: func(t *testing.T) {
				if got := publishDiagnosticsCapability(t, params)["versionSupport"]; got != true {
					t.Fatalf("versionSupport = %v, want true", got)
				}
			},
		},
		{
			name: "refuses related information",
			check: func(t *testing.T) {
				if got := publishDiagnosticsCapability(t, params)["relatedInformation"]; got != false {
					t.Fatalf("relatedInformation = %v, want false", got)
				}
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, tt.check)
	}
}

// publishDiagnosticsCapability reaches into the initialize params the way a
// server would, so a capability nested one level too deep fails loudly.
func publishDiagnosticsCapability(t *testing.T, params map[string]any) map[string]any {
	t.Helper()
	capabilities, ok := params["capabilities"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities = %T, want an object", params["capabilities"])
	}
	textDocument, ok := capabilities["textDocument"].(map[string]any)
	if !ok {
		t.Fatalf("capabilities.textDocument = %T, want an object", capabilities["textDocument"])
	}
	publish, ok := textDocument["publishDiagnostics"].(map[string]any)
	if !ok {
		t.Fatalf("textDocument.publishDiagnostics = %T, want an object", textDocument["publishDiagnostics"])
	}
	return publish
}

func TestCallReturnsServerError(t *testing.T) {
	_, client := startStub(t, func(s *stubServer, method string, params json.RawMessage, id *json.RawMessage) {
		s.respondError(id, -32602, "invalid params")
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := client.Call(ctx, "textDocument/hover", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid params") {
		t.Fatalf("error = %v, want it to carry the server message", err)
	}
}

func TestCallHonoursContextDeadline(t *testing.T) {
	_, client := startStub(t, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	err := client.Call(ctx, "initialize", nil, nil)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want context.DeadlineExceeded", err)
	}
}

func TestCallAfterCloseFailsFast(t *testing.T) {
	_, client := startStub(t, nil)
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := client.Call(context.Background(), "initialize", nil, nil); !errors.Is(err, ErrClientClosed) {
		t.Fatalf("error = %v, want ErrClientClosed", err)
	}
}

func TestCloseReleasesPendingCalls(t *testing.T) {
	stub, client := startStub(t, nil)
	done := make(chan error, 1)
	go func() {
		done <- client.Call(context.Background(), "textDocument/hover", nil, nil)
	}()
	stub.await(t, "textDocument/hover")

	start := time.Now()
	if err := client.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("a pending call returned no error after the client closed")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a pending call was left waiting after the client closed")
	}
	if elapsed := time.Since(start); elapsed > closeGrace {
		t.Fatalf("Close took %s, want less than %s", elapsed, closeGrace)
	}
}

func TestServerRequestsAreAlwaysAnswered(t *testing.T) {
	t.Run("unsupported request", func(t *testing.T) {
		stub, _ := startStub(t, nil)
		_, ch := stub.request("workspace/configuration", map[string]any{"items": []any{}})
		msg := stub.awaitAnswer(t, ch)
		if msg.Error == nil {
			t.Fatal("an unsupported server request was left unanswered")
		}
		if msg.Error.Code != codeMethodNotFound {
			t.Fatalf("error code = %d, want %d", msg.Error.Code, codeMethodNotFound)
		}
		if !strings.Contains(msg.Error.Message, "workspace/configuration") {
			t.Fatalf("error message = %q, want it to name the method", msg.Error.Message)
		}
	})

	t.Run("registered request", func(t *testing.T) {
		stub, client := startStub(t, nil)
		client.RegisterRequest("workspace/configuration", func(params json.RawMessage) (any, error) {
			return []map[string]any{{"lintOnSave": true}}, nil
		})
		_, ch := stub.request("workspace/configuration", map[string]any{"items": []any{}})
		msg := stub.awaitAnswer(t, ch)
		if msg.Error != nil {
			t.Fatalf("registered request answered with an error: %v", msg.Error)
		}
		var settings []map[string]any
		if err := json.Unmarshal(msg.Result, &settings); err != nil {
			t.Fatalf("decode result: %v", err)
		}
		if len(settings) != 1 || settings[0]["lintOnSave"] != true {
			t.Fatalf("result = %s, want the handler's settings", msg.Result)
		}
	})

	t.Run("failing request handler", func(t *testing.T) {
		stub, client := startStub(t, nil)
		client.RegisterRequest("workspace/configuration", func(params json.RawMessage) (any, error) {
			return nil, errors.New("no configuration here")
		})
		_, ch := stub.request("workspace/configuration", map[string]any{"items": []any{}})
		msg := stub.awaitAnswer(t, ch)
		if msg.Error == nil {
			t.Fatal("a failing handler left the server unanswered")
		}
		if msg.Error.Code != codeInternalError || !strings.Contains(msg.Error.Message, "no configuration here") {
			t.Fatalf("error = %+v, want the handler's error", msg.Error)
		}
	})
}

func TestMalformedMessageDoesNotStopTheClient(t *testing.T) {
	stub, client := startStub(t, nil)
	uri := "file:///workspace/a.go"
	stub.write([]byte("{not json at all"))
	stub.notify("textDocument/publishDiagnostics", map[string]any{
		"uri": uri, "version": 1, "diagnostics": []any{},
	})

	got := waitForDiagnostics(t, client, uri)
	if len(got) != 0 {
		t.Fatalf("diagnostics = %+v, want the clean list that followed the bad frame", got)
	}
}

func TestNotificationsRouteToHandlers(t *testing.T) {
	stub, client := startStub(t, nil)
	got := make(chan string, 1)
	client.OnNotification("window/logMessage", func(params json.RawMessage) {
		var payload struct {
			Message string `json:"message"`
		}
		if err := json.Unmarshal(params, &payload); err != nil {
			got <- "decode error: " + err.Error()
			return
		}
		got <- payload.Message
	})
	stub.notify("window/logMessage", map[string]any{"type": 3, "message": "indexing"})

	select {
	case message := <-got:
		if message != "indexing" {
			t.Fatalf("handler saw %q, want indexing", message)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the notification never reached its handler")
	}
}

func TestConcurrentCallsGetTheirOwnAnswers(t *testing.T) {
	_, client := startStub(t, func(s *stubServer, method string, params json.RawMessage, id *json.RawMessage) {
		s.respond(id, method)
	})

	const calls = 24
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	failures := make(chan error, calls)
	for i := 0; i < calls; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			want := fmt.Sprintf("method-%d", i)
			var got string
			if err := client.Call(ctx, want, nil, &got); err != nil {
				failures <- err
				return
			}
			if got != want {
				failures <- fmt.Errorf("call %s got answer %q", want, got)
			}
		}(i)
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}

func TestDocumentNotificationsCarryFullText(t *testing.T) {
	stub, client := startStub(t, nil)
	const uri = "file:///workspace/a.go"

	if err := client.DidOpen(uri, "go", 1, "package a\n"); err != nil {
		t.Fatalf("DidOpen: %v", err)
	}
	stub.await(t, "textDocument/didOpen")

	var open struct {
		TextDocument struct {
			URI        string `json:"uri"`
			LanguageID string `json:"languageId"`
			Version    int    `json:"version"`
			Text       string `json:"text"`
		} `json:"textDocument"`
	}
	decodeParams(t, stub.paramsFor("textDocument/didOpen"), &open)
	if open.TextDocument.URI != uri || open.TextDocument.LanguageID != "go" ||
		open.TextDocument.Version != 1 || open.TextDocument.Text != "package a\n" {
		t.Fatalf("didOpen carried %+v", open.TextDocument)
	}

	if err := client.DidChange(uri, 2, "package a\n\n"); err != nil {
		t.Fatalf("DidChange: %v", err)
	}
	stub.await(t, "textDocument/didChange")

	var change struct {
		TextDocument   map[string]any   `json:"textDocument"`
		ContentChanges []map[string]any `json:"contentChanges"`
	}
	decodeParams(t, stub.paramsFor("textDocument/didChange"), &change)
	if len(change.ContentChanges) != 1 {
		t.Fatalf("contentChanges = %+v, want one entry", change.ContentChanges)
	}
	if _, ranged := change.ContentChanges[0]["range"]; ranged {
		t.Fatal("a full-text change must not carry a range")
	}
	if change.ContentChanges[0]["text"] != "package a\n\n" {
		t.Fatalf("change text = %v, want the whole document", change.ContentChanges[0]["text"])
	}

	if err := client.DidClose(uri); err != nil {
		t.Fatalf("DidClose: %v", err)
	}
	stub.await(t, "textDocument/didClose")
}

func TestStreamEndFailsPendingCalls(t *testing.T) {
	_, client := startStub(t, nil)
	done := make(chan error, 1)
	go func() {
		done <- client.Call(context.Background(), "textDocument/hover", nil, nil)
	}()
	_ = client.Close()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("the call returned no error after the stream ended")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the call was left waiting after the stream ended")
	}
}
