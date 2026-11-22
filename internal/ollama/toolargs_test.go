package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestParseToolArgumentsRepairsAndPreservesTruncated(t *testing.T) {
	t.Parallel()

	t.Run("fenced json", func(t *testing.T) {
		raw := "```json\n{\"file_path\": \"a.go\", \"start_line\": 3}\n```"
		got, err := ParseToolArguments(raw)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got["file_path"] != "a.go" {
			t.Fatalf("got %#v", got)
		}
	})

	t.Run("trailing comma and windows escape", func(t *testing.T) {
		raw := `{"file_path":"C:\Users\me\dir\a.txt","items":[1,2,],}`
		got, err := ParseToolArguments(raw)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got["file_path"] != `C:\Users\me\dir\a.txt` {
			t.Fatalf("unexpected path: %#v", got["file_path"])
		}
	})

	t.Run("raw newlines inside closed string", func(t *testing.T) {
		raw := "{\"file_path\":\"a.txt\",\"contents\":\"line1\nline2\tindented\"}"
		got, err := ParseToolArguments(raw)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got["contents"] != "line1\nline2\tindented" {
			t.Fatalf("unexpected contents: %#v", got["contents"])
		}
	})

	t.Run("truncated json stays an error", func(t *testing.T) {
		for _, raw := range []string{
			`{"file_path":"/tmp/x"`,
			`{"file_path": "a.txt", "contents": "abc`,
			`null`,
			`[]`,
			`   `,
		} {
			if _, err := ParseToolArguments(raw); err == nil {
				t.Fatalf("expected error for %q", raw)
			}
		}
	})
}

func TestResponseFormatMarshalJSON(t *testing.T) {
	t.Parallel()

	jsonMode, err := json.Marshal(ResponseFormat{})
	if err != nil {
		t.Fatal(err)
	}
	if string(jsonMode) != `"json"` {
		t.Fatalf("expected \"json\", got %s", jsonMode)
	}

	schemaMode, err := json.Marshal(ResponseFormat{
		Name:   "edit_args",
		Schema: map[string]any{"type": "object"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(schemaMode) != `{"type":"object"}` {
		t.Fatalf("expected schema object, got %s", schemaMode)
	}

	reqBody, err := json.Marshal(ChatRequest{Model: "m", Messages: []Message{{Role: "user", Content: "hi"}}})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(reqBody), `"format"`) {
		t.Fatalf("expected nil format to be omitted, got %s", reqBody)
	}
}

func TestMessageToolCallFunctionMalformedPreservesParseError(t *testing.T) {
	t.Parallel()

	raw := `{"name":"Write","arguments":"{\"file_path\":\"/tmp/x\""}`
	var fn MessageToolCallFunction
	if err := json.Unmarshal([]byte(raw), &fn); err != nil {
		t.Fatalf("expected malformed stringified args not to fail UnmarshalJSON, got: %v", err)
	}
	if _, ok := fn.Arguments[ParseErrorKey]; !ok {
		t.Fatalf("expected ParseErrorKey in %#v", fn.Arguments)
	}
	if fn.Arguments[RawArgsKey] != `{"file_path":"/tmp/x"` {
		t.Fatalf("unexpected RawArgsKey: %#v", fn.Arguments[RawArgsKey])
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestHTTPClientFormatFallbackOn400(t *testing.T) {
	t.Parallel()

	var formats []string
	client := NewHTTP("http://ollama.local").(*HTTPClient)
	client.http = &http.Client{
		Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			raw, _ := io.ReadAll(r.Body)
			var payload map[string]any
			_ = json.Unmarshal(raw, &payload)
			if f, exists := payload["format"]; exists {
				fb, _ := json.Marshal(f)
				formats = append(formats, string(fb))
			} else {
				formats = append(formats, "<omitted>")
			}
			switch len(formats) {
			case 1:
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(`{"error":"invalid format schema"}`)),
				}, nil
			case 2:
				return &http.Response{
					StatusCode: http.StatusBadRequest,
					Body:       io.NopCloser(strings.NewReader(`{"error":"format json is not supported"}`)),
				}, nil
			default:
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewBufferString(`{"message":{"content":"{\"ok\":true}"},"done":true}`)),
				}, nil
			}
		}),
	}

	resp, err := client.ChatSync(context.Background(), ChatRequest{
		Model:    "qwen3:4b",
		Messages: []Message{{Role: "user", Content: "hi"}},
		Format: &ResponseFormat{
			Name:   "test",
			Schema: map[string]any{"type": "object"},
		},
	})
	if err != nil {
		t.Fatalf("ChatSync failed: %v", err)
	}
	if resp.Content != `{"ok":true}` {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if len(formats) != 3 || formats[0] != `{"type":"object"}` || formats[1] != `"json"` || formats[2] != `<omitted>` {
		t.Fatalf("unexpected format fallback sequence: %v", formats)
	}
}
