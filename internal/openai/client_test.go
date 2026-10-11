package openai

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

func TestNewEndpointNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"http://127.0.0.1:8080", "http://127.0.0.1:8080/v1"},
		{"http://127.0.0.1:8080/v1", "http://127.0.0.1:8080/v1"},
		{"http://127.0.0.1:8080/v1/", "http://127.0.0.1:8080/v1"},
		{"https://api.openai.com/v1", "https://api.openai.com/v1"},
	}

	for _, tt := range tests {
		c := New(tt.input)
		if c.baseURL != tt.want {
			t.Errorf("New(%q).baseURL = %q, want %q", tt.input, c.baseURL, tt.want)
		}
	}
}

func TestTags(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("Authorization") != "Bearer secret-key" {
			t.Errorf("missing or invalid auth header: %s", r.Header.Get("Authorization"))
		}

		resp := modelsResponse{
			Object: "list",
			Data: []modelData{
				{ID: "qwen2.5-coder:7b", Object: "model"},
				{ID: "llava-vision-v1", Object: "model"},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := New(srv.URL, WithAPIKey("secret-key"), WithHTTPClient(srv.Client()))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	models, err := client.Tags(ctx)
	if err != nil {
		t.Fatalf("Tags failed: %v", err)
	}

	if len(models) != 2 {
		t.Fatalf("expected 2 models, got %d", len(models))
	}

	if models[0].Name != "qwen2.5-coder:7b" || models[0].SupportsVision() {
		t.Errorf("unexpected model[0]: %+v", models[0])
	}
	if models[1].Name != "llava-vision-v1" || !models[1].SupportsVision() {
		t.Errorf("unexpected model[1]: %+v", models[1])
	}
}

func TestVersion(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v1/models" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte(`{"object":"list","data":[]}`))
			return
		}
		http.NotFound(w, r)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	v, err := client.Version(context.Background())
	if err != nil {
		t.Fatalf("Version failed: %v", err)
	}
	if !strings.Contains(v, "OpenAI-compatible") {
		t.Errorf("unexpected version string: %q", v)
	}
}

func TestShow(t *testing.T) {
	client := New("http://dummy/v1")
	m, err := client.Show(context.Background(), "qwen-vl-max")
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	if !m.SupportsVision() || !m.SupportsTools() || !m.SupportsThinking() {
		t.Errorf("expected vision, tools, and thinking supported for qwen-vl-max, got %+v", m)
	}
}

func TestChatSync(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected path: %s", r.URL.Path)
			http.NotFound(w, r)
			return
		}

		var req chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request failed: %v", err)
		}
		if req.Stream {
			t.Errorf("expected stream=false for ChatSync")
		}

		resp := chatCompletionResponse{
			ID: "chatcmpl-123",
			Choices: []chatChoice{
				{
					Index: 0,
					Message: chatResponseMessage{
						Role:             "assistant",
						Content:          "Hello from OpenAI endpoint!",
						ReasoningContent: "Thinking about the reply...",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	res, err := client.ChatSync(context.Background(), ollama.ChatRequest{
		Model: "llama3",
		Messages: []ollama.Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("ChatSync failed: %v", err)
	}

	if res.Content != "Hello from OpenAI endpoint!" {
		t.Errorf("unexpected content: %q", res.Content)
	}
	if res.Thinking != "Thinking about the reply..." {
		t.Errorf("unexpected thinking: %q", res.Thinking)
	}
}

func TestChatSyncWithToolCalls(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := chatCompletionResponse{
			ID: "chatcmpl-tool",
			Choices: []chatChoice{
				{
					Index: 0,
					Message: chatResponseMessage{
						Role:    "assistant",
						Content: "",
						ToolCalls: []chatToolCall{
							{
								ID:   "call_read_0",
								Type: "function",
								Function: chatFunctionCall{
									Name:      "Read",
									Arguments: `{"path":"main.go"}`,
								},
							},
						},
					},
					FinishReason: "tool_calls",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	res, err := client.ChatSync(context.Background(), ollama.ChatRequest{
		Model: "test-model",
		Messages: []ollama.Message{
			{Role: "user", Content: "Read main.go"},
		},
		Tools: []ollama.ChatTool{
			{
				Type: "function",
				Function: ollama.ChatToolFunction{
					Name:        "Read",
					Description: "Read file",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("ChatSync with tools failed: %v", err)
	}

	if len(res.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(res.ToolCalls))
	}
	tc := res.ToolCalls[0]
	if tc.Function.Name != "Read" || tc.Function.Arguments["path"] != "main.go" {
		t.Errorf("unexpected tool call: %+v", tc)
	}
}

func TestChatStreamingSSE(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatalf("expected flusher")
		}

		send := func(chunk string) {
			_, _ = fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}

		send(`{"choices":[{"index":0,"delta":{"role":"assistant","reasoning_content":"Step 1"}}],"finish_reason":null}`)
		send(`{"choices":[{"index":0,"delta":{"content":"Hi "}}],"finish_reason":null}`)
		send(`{"choices":[{"index":0,"delta":{"content":"there!"}}],"finish_reason":null}`)
		send(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"Read","arguments":"{\"path\":\"foo\"}"}}]},"finish_reason":"tool_calls"}]}`)
		send(`[DONE]`)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	ch, err := client.Chat(context.Background(), ollama.ChatRequest{
		Model: "qwen-coder",
		Messages: []ollama.Message{
			{Role: "user", Content: "Hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}

	var content strings.Builder
	var thinking strings.Builder
	var toolCalls []ollama.MessageToolCall

	for chunk := range ch {
		if chunk.Err != nil {
			t.Fatalf("unexpected chunk error: %v", chunk.Err)
		}
		content.WriteString(chunk.Delta)
		thinking.WriteString(chunk.Thinking)
		if len(chunk.ToolCalls) > 0 {
			toolCalls = append(toolCalls, chunk.ToolCalls...)
		}
	}

	if content.String() != "Hi there!" {
		t.Errorf("unexpected content: %q", content.String())
	}
	if thinking.String() != "Step 1" {
		t.Errorf("unexpected thinking: %q", thinking.String())
	}
	if len(toolCalls) != 1 || toolCalls[0].Function.Name != "Read" {
		t.Errorf("unexpected tool calls: %+v", toolCalls)
	}
}

func TestChatToolErrorFallback(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		var req chatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		if attempts == 1 {
			if len(req.Tools) == 0 {
				t.Errorf("expected tools on first attempt")
			}
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"Model does not support tools/functions"}}`))
			return
		}

		if len(req.Tools) != 0 {
			t.Errorf("expected no tools on retry attempt")
		}
		resp := chatCompletionResponse{
			Choices: []chatChoice{
				{
					Index: 0,
					Message: chatResponseMessage{
						Role:    "assistant",
						Content: "Fallback success",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	res, err := client.ChatSync(context.Background(), ollama.ChatRequest{
		Model: "no-tool-model",
		Messages: []ollama.Message{
			{Role: "user", Content: "Hello"},
		},
		Tools: []ollama.ChatTool{
			{Type: "function", Function: ollama.ChatToolFunction{Name: "Read"}},
		},
	})
	if err != nil {
		t.Fatalf("ChatSync with fallback failed: %v", err)
	}
	if res.Content != "Fallback success" {
		t.Errorf("unexpected content: %q", res.Content)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func TestChatMultimodalMessage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		_ = json.NewDecoder(r.Body).Decode(&req)

		if len(req.Messages) != 1 {
			t.Fatalf("expected 1 message, got %d", len(req.Messages))
		}
		msg := req.Messages[0]
		parts, ok := msg.Content.([]any)
		if !ok || len(parts) != 2 {
			t.Fatalf("expected content parts array of length 2, got %T: %+v", msg.Content, msg.Content)
		}

		resp := chatCompletionResponse{
			Choices: []chatChoice{
				{
					Index: 0,
					Message: chatResponseMessage{
						Role:    "assistant",
						Content: "I see the image!",
					},
					FinishReason: "stop",
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	res, err := client.ChatSync(context.Background(), ollama.ChatRequest{
		Model: "vision-model",
		Messages: []ollama.Message{
			{
				Role:    "user",
				Content: "What is this?",
				Images:  []string{"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk+M9QDwADhgGAWjR9awAAAABJRU5ErkJggg=="},
			},
		},
	})
	if err != nil {
		t.Fatalf("ChatSync with image failed: %v", err)
	}
	if res.Content != "I see the image!" {
		t.Errorf("unexpected content: %q", res.Content)
	}
}

func TestTagsArchitectureInputModalities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			http.NotFound(w, r)
			return
		}
		resp := modelsResponse{
			Object: "list",
			Data: []modelData{
				{
					ID:     "custom-model",
					Object: "model",
					Architecture: &modelArchitecture{
						InputModalities: []string{"text", "image"},
					},
				},
				{
					ID:     "text-only-model",
					Object: "model",
					Architecture: &modelArchitecture{
						InputModalities: []string{"text"},
					},
				},
				{
					ID:         "modality-model",
					Object:     "model",
					Modalities: []string{"image"},
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	models, err := client.Tags(ctx)
	if err != nil {
		t.Fatalf("Tags failed: %v", err)
	}
	if len(models) != 3 {
		t.Fatalf("expected 3 models, got %d", len(models))
	}

	// custom-model has input_modalities: ["text", "image"] -> vision: yes
	if !models[0].SupportsVision() || !models[0].CapabilitiesKnown {
		t.Errorf("expected vision supported and known for custom-model, got %+v", models[0])
	}

	// text-only-model has input_modalities: ["text"] -> vision: no, but known
	if models[1].SupportsVision() || !models[1].CapabilitiesKnown {
		t.Errorf("expected vision unsupported and known for text-only-model, got %+v", models[1])
	}

	// modality-model has modalities: ["image"] -> vision: yes
	if !models[2].SupportsVision() || !models[2].CapabilitiesKnown {
		t.Errorf("expected vision supported and known for modality-model, got %+v", models[2])
	}

	// Show should return the cached capability accurately
	showModel, err := client.Show(ctx, "custom-model")
	if err != nil {
		t.Fatalf("Show failed: %v", err)
	}
	if !showModel.SupportsVision() {
		t.Errorf("expected Show on custom-model to support vision, got %+v", showModel)
	}
}

func TestToDataURL(t *testing.T) {
	if got := toDataURL("data:image/png;base64,abc"); got != "data:image/png;base64,abc" {
		t.Errorf("unexpected data URL pass-through: %q", got)
	}
	if got := toDataURL("iVBORw0KGgoAAAANSUhEUgAAAAE="); !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Errorf("expected png mime, got %q", got)
	}
	if got := toDataURL("/9j/4AAQSkZJRg=="); !strings.HasPrefix(got, "data:image/jpeg;base64,") {
		t.Errorf("expected jpeg mime, got %q", got)
	}
	if got := toDataURL("R0lGODlh"); !strings.HasPrefix(got, "data:image/gif;base64,") {
		t.Errorf("expected gif mime, got %q", got)
	}
	if got := toDataURL("Qk02"); !strings.HasPrefix(got, "data:image/bmp;base64,") {
		t.Errorf("expected bmp mime, got %q", got)
	}
	if got := toDataURL("UklGR"); !strings.HasPrefix(got, "data:image/webp;base64,") {
		t.Errorf("expected webp mime, got %q", got)
	}
}
