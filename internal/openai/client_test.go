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

func TestChatStreamingStall(t *testing.T) {
	old := streamStallTimeout
	streamStallTimeout = 100 * time.Millisecond
	defer func() { streamStallTimeout = old }()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Hold connection open with no data to simulate stalled stream.
		<-r.Context().Done()
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	ch, err := client.Chat(context.Background(), ollama.ChatRequest{
		Model:    "stall-model",
		Messages: []ollama.Message{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat returned unexpected error: %v", err)
	}

	var stallErr error
	for chunk := range ch {
		if chunk.Err != nil {
			stallErr = chunk.Err
		}
	}

	if stallErr == nil {
		t.Fatal("expected stall error, got nil")
	}
	if !strings.Contains(stallErr.Error(), "stalled") {
		t.Fatalf("expected stall error message, got %v", stallErr)
	}
}

// sseReply writes one content chunk and the end of the stream.
func sseReply(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = w.Write([]byte(`data: {"choices":[{"index":0,"delta":{"content":"ok"},"finish_reason":"stop"}]}` + "\n\n"))
	_, _ = w.Write([]byte("data: [DONE]\n\n"))
}

// TestChatSendsThinkingRequest checks the thinking level reaches the wire on
// the OpenAI path: it used to be dropped here, so a server whose template
// thinks by default (Strata, vLLM) ignored /think off and /think low.
func TestChatSendsThinkingRequest(t *testing.T) {
	var bodies []chatCompletionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request failed: %v", err)
		}
		bodies = append(bodies, req)
		sseReply(w)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	drain := func(think *ollama.ThinkSetting) {
		t.Helper()
		ch, err := client.Chat(ctx, ollama.ChatRequest{
			Model:    "m",
			Stream:   true,
			Messages: []ollama.Message{{Role: "user", Content: "hi"}},
			Think:    think,
		})
		if err != nil {
			t.Fatalf("Chat failed: %v", err)
		}
		for range ch {
		}
	}

	drain(ollama.ThinkLevel("low"))
	drain(ollama.ThinkOff())

	if len(bodies) != 2 {
		t.Fatalf("expected 2 requests, got %d", len(bodies))
	}
	if bodies[0].ReasoningEffort != "low" {
		t.Errorf("low level = %q on the wire, want low", bodies[0].ReasoningEffort)
	}
	if bodies[0].ChatTemplateKwargs != nil {
		t.Errorf("effort request should not send chat_template_kwargs: %+v", bodies[0].ChatTemplateKwargs)
	}
	off := bodies[1]
	if off.ReasoningEffort != "" {
		t.Errorf("off request carries reasoning_effort %q", off.ReasoningEffort)
	}
	if off.ChatTemplateKwargs == nil || off.ChatTemplateKwargs.EnableThinking == nil || *off.ChatTemplateKwargs.EnableThinking {
		t.Errorf("off request does not disable thinking in the template: %+v", off.ChatTemplateKwargs)
	}
}

// TestChatRetriesWithoutThinkingFields keeps non-thinking providers working:
// a 400 that names the thinking fields retries once without them and is
// remembered, so later turns do not pay the round trip again.
func TestChatRetriesWithoutThinkingFields(t *testing.T) {
	var bodies []chatCompletionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request failed: %v", err)
		}
		bodies = append(bodies, req)
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"type":"invalid_request_error","message":"unknown field chat_template_kwargs"}}`))
			return
		}
		sseReply(w)
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	ch, err := client.Chat(ctx, ollama.ChatRequest{
		Model:    "plain",
		Stream:   true,
		Messages: []ollama.Message{{Role: "user", Content: "hi"}},
		Think:    ollama.ThinkOff(),
	})
	if err != nil {
		t.Fatalf("Chat failed: %v", err)
	}
	for range ch {
	}

	if len(bodies) != 2 {
		t.Fatalf("expected a retry, got %d requests", len(bodies))
	}
	if bodies[1].ChatTemplateKwargs != nil || bodies[1].ReasoningEffort != "" {
		t.Errorf("retry still carries the thinking fields: %+v", bodies[1])
	}
	if !client.isReasoningUnsupported("plain") {
		t.Error("expected the model to be remembered as unable to take a thinking level")
	}
}

// TestChatSyncRetriesWithoutReasoningEffort covers the non-streaming path,
// which has its own request loop.
func TestChatSyncRetriesWithoutReasoningEffort(t *testing.T) {
	var bodies []chatCompletionRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req chatCompletionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request failed: %v", err)
		}
		bodies = append(bodies, req)
		if len(bodies) == 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":{"message":"unknown parameter reasoning_effort"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(chatCompletionResponse{
			Choices: []chatChoice{{Message: chatResponseMessage{Role: "assistant", Content: "ok"}}},
		})
	}))
	defer srv.Close()

	client := New(srv.URL, WithHTTPClient(srv.Client()))
	res, err := client.ChatSync(context.Background(), ollama.ChatRequest{
		Model:    "plain-sync",
		Messages: []ollama.Message{{Role: "user", Content: "hi"}},
		Think:    ollama.ThinkLevel("medium"),
	})
	if err != nil {
		t.Fatalf("ChatSync failed: %v", err)
	}
	if res.Content != "ok" {
		t.Errorf("unexpected content: %q", res.Content)
	}
	if len(bodies) != 2 {
		t.Fatalf("expected a retry, got %d requests", len(bodies))
	}
	if bodies[1].ReasoningEffort != "" {
		t.Errorf("retry still carries reasoning_effort %q", bodies[1].ReasoningEffort)
	}
}
