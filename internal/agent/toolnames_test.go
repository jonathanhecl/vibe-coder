package agent

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/permissions"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func newToolNameAgent(t *testing.T, client ollama.Client) *Agent {
	t.Helper()
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "test-model",
		ContextWindow: 32768,
		MaxTokens:     128,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	return New(cfg, client, reg, perm, session.New(cfg), &fakeUI{})
}

func TestResolveToolName(t *testing.T) {
	t.Parallel()
	ag := newToolNameAgent(t, fakeClient{})
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Read", "Read", true},
		{"read", "Read", true},
		{"READ", "Read", true},
		{"todo_write", "TodoWrite", true},
		{"Todo-Write", "TodoWrite", true},
		{"web-fetch", "WebFetch", true},
		{"Web Fetch", "WebFetch", true},
		{"functions.Read", "Read", true},
		{"tools.glob", "Glob", true},
		{"Functions.TodoWrite", "TodoWrite", true},
		{"read_file", "", false},
		{"MissingTool", "", false},
		{"", "", false},
		{"functions.", "", false},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.in, func(t *testing.T) {
			t.Parallel()
			got, ok := ag.resolveToolName(tc.in)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("resolveToolName(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestAvailableToolNamesSortedAndAllowed(t *testing.T) {
	t.Parallel()
	ag := newToolNameAgent(t, fakeClient{})
	names := ag.availableToolNames()
	if len(names) == 0 {
		t.Fatal("expected available tool names")
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("expected sorted names, got %v around index %d", names, i)
		}
	}
	joined := strings.Join(names, ",")
	for _, want := range []string{"Read", "Bash", "TodoWrite", "WebFetch"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("expected %q in available tools %v", want, names)
		}
	}
}

func TestUnknownToolMessageListsAvailableTools(t *testing.T) {
	t.Parallel()
	ag := newToolNameAgent(t, fakeClient{})
	msg := ag.unknownToolMessage("MissingTool")
	if !strings.Contains(msg, `tool "MissingTool" does not exist`) {
		t.Fatalf("expected missing tool named, got %q", msg)
	}
	if !strings.Contains(msg, "Read") || !strings.Contains(msg, "Bash") {
		t.Fatalf("expected available tools listed, got %q", msg)
	}
}

// A native batch mixing a mis-cased name with a truly unknown one must
// canonicalize the first, execute it, and answer the second with an error
// result instead of dropping the whole batch.
func TestRunNativeMixedKnownUnknownToolBatch(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(filePath, []byte("hello note\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	cfg := &config.Config{
		Model:         "test-model",
		ContextWindow: 32768,
		MaxTokens:     128,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	sess := session.New(cfg)
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	ui := &fakeUI{}
	client := &nativeSequenceClient{turns: []ollama.Chunk{
		{
			ToolCalls: []ollama.MessageToolCall{
				{Function: ollama.MessageToolCallFunction{Name: "read", Arguments: map[string]any{"file_path": filepath.ToSlash(filePath)}}},
				{Function: ollama.MessageToolCallFunction{Name: "MissingTool", Arguments: map[string]any{"x": 1}}},
			},
			Done: true,
		},
		{Delta: "Done.", Done: true},
	}}
	ag := New(cfg, client, reg, perm, sess, ui)

	if err := ag.Run(context.Background(), "read the note"); err != nil {
		t.Fatalf("run agent: %v", err)
	}
	var sawCanonicalRead, sawUnknownResult bool
	for _, m := range sess.MessagesReadOnly() {
		if m.Role == "assistant" {
			for _, tc := range m.ToolCalls {
				if tc.Function.Name == "Read" {
					sawCanonicalRead = true
				}
				if tc.Function.Name == "read" {
					t.Fatalf("expected canonical Read in history, got %q", tc.Function.Name)
				}
			}
		}
		if m.Role == "tool" && strings.Contains(m.Content, `tool "MissingTool" does not exist`) {
			sawUnknownResult = true
		}
	}
	if !sawCanonicalRead {
		t.Fatal("expected canonical Read tool call in history")
	}
	if !sawUnknownResult {
		t.Fatal("expected unknown-tool error result in history")
	}
}

// A native batch made only of unknown names feeds the per-turn counter; the
// fourth consecutive all-unknown round stops the turn with a resumable
// error naming the invented tools.
func TestRunNativeUnknownToolRoundsCap(t *testing.T) {
	tmp := t.TempDir()
	cfg := &config.Config{
		Model:         "test-model",
		ContextWindow: 32768,
		MaxTokens:     128,
		Cwd:           tmp,
		SessionsDir:   filepath.Join(tmp, "sessions"),
	}
	reg := tools.NewRegistry()
	reg.RegisterDefaults()
	perm := permissions.NewManager(&config.Config{YesMode: true})
	// The model invents the same unknown tool every turn; the client replays
	// the batch so each Run iteration sees one all-unknown round.
	turns := make([]ollama.Chunk, 0, maxUnknownToolRounds+2)
	for i := 0; i < maxUnknownToolRounds+2; i++ {
		turns = append(turns, ollama.Chunk{
			ToolCalls: []ollama.MessageToolCall{
				{Function: ollama.MessageToolCallFunction{Name: "MissingTool", Arguments: map[string]any{}}},
			},
			Done: true,
		})
	}
	client := &nativeSequenceClient{turns: turns}
	ag := New(cfg, client, reg, perm, session.New(cfg), &fakeUI{})

	err := ag.Run(context.Background(), "do something")
	if err == nil {
		t.Fatal("expected the unknown-tool cap to stop the run")
	}
	if !strings.Contains(err.Error(), "MissingTool") || !strings.Contains(err.Error(), "kept calling tools") {
		t.Fatalf("expected resumable unknown-tool error naming MissingTool, got %v", err)
	}
	if client.calls != maxUnknownToolRounds+1 {
		t.Fatalf("expected %d chat calls before the cap, got %d", maxUnknownToolRounds+1, client.calls)
	}
}

// An XML envelope naming an unknown tool with no visible prose no longer
// ends the turn with an error: the assistant reply is kept, the unknown call
// gets an error observation naming the real tools, and the model gets
// another turn to answer.
func TestRunXMLUnknownToolRecordsObservationAndContinues(t *testing.T) {
	client := &sequenceClient{replies: []string{
		`<invoke name="MissingTool">{"x":1}</invoke>`,
		"all done",
	}}
	ag := newToolNameAgent(t, client)

	if err := ag.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("run agent: %v", err)
	}
	var sawObservation bool
	for _, m := range ag.sess.MessagesReadOnly() {
		if m.Role == "user" && strings.Contains(m.Content, "[tool_result name=MissingTool]") &&
			strings.Contains(m.Content, `tool "MissingTool" does not exist`) {
			sawObservation = true
		}
	}
	if !sawObservation {
		t.Fatal("expected unknown-tool observation in session")
	}
	if client.calls != 2 {
		t.Fatalf("expected the turn to continue after the unknown call, got %d calls", client.calls)
	}
}

// A mis-cased XML envelope name resolves to the canonical tool and executes;
// a sibling unknown envelope in the same reply gets an observation instead
// of sinking the known call.
func TestRunXMLMixedKnownUnknownExecutesKnown(t *testing.T) {
	tmp := t.TempDir()
	filePath := filepath.Join(tmp, "note.txt")
	if err := os.WriteFile(filePath, []byte("hello note\n"), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	client := &sequenceClient{replies: []string{
		`<invoke name="read">{"file_path":"` + filepath.ToSlash(filePath) + `"}</invoke>` +
			`<invoke name="MissingTool">{"x":1}</invoke>`,
		"done",
	}}
	ag := newToolNameAgent(t, client)

	if err := ag.Run(context.Background(), "read the note"); err != nil {
		t.Fatalf("run agent: %v", err)
	}
	var sawRead, sawUnknown bool
	for _, m := range ag.sess.MessagesReadOnly() {
		if m.Role == "user" && strings.Contains(m.Content, "[tool_result name=Read]") &&
			strings.Contains(m.Content, "hello note") {
			sawRead = true
		}
		if strings.Contains(m.Content, `tool "MissingTool" does not exist`) {
			sawUnknown = true
		}
	}
	if !sawRead {
		t.Fatal("expected the resolved Read call to execute")
	}
	if !sawUnknown {
		t.Fatal("expected the unknown call to get an error observation")
	}
}
