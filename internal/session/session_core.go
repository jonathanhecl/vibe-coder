package session

import (
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
}

type Session struct {
	cfg           *config.Config
	id            string
	messages      []Message
	client        ollama.Client
	tokenEstimate int
}

func New(cfg *config.Config) *Session {
	return &Session{
		cfg:      cfg,
		id:       newSessionID(),
		messages: make([]Message, 0, 32),
	}
}

func (s *Session) ID() string {
	return s.id
}

// Messages returns a copy of the in-memory transcript. Safe to mutate; the
// underlying slice is cloned. Used by the agent runtime (e.g. compaction
// heuristics) and by tests that need to assert the exact wrapping of tool
// observations.
func (s *Session) Messages() []Message {
	out := make([]Message, len(s.messages))
	copy(out, s.messages)
	return out
}

// MessagesReadOnly returns the live transcript slice. Callers must treat it
// as read-only: do not mutate elements or reorder the backing slice in place.
func (s *Session) MessagesReadOnly() []Message {
	return s.messages
}

func (s *Session) MessageCount() int {
	return len(s.messages)
}

func (s *Session) SetClient(client ollama.Client) {
	s.client = client
}

func (s *Session) AddUser(content string) {
	s.addMessage(Message{
		Role:      "user",
		Content:   content,
		Timestamp: time.Now().UTC(),
	})
}

func (s *Session) AddAssistant(content string) {
	s.addMessage(Message{
		Role:      "assistant",
		Content:   content,
		Timestamp: time.Now().UTC(),
	})
}

// AddSystemNote records an out-of-band note from the agent runtime
// (permission denied, plan-mode block, auto-test failure, etc.). It is
// stored under the assistant role for visibility but prefixed so the model
// recognises it as a system status rather than its own reasoning.
func (s *Session) AddSystemNote(text string) {
	s.addMessage(Message{
		Role:      "assistant",
		Content:   "[runtime] " + strings.TrimSpace(text),
		Timestamp: time.Now().UTC(),
	})
}

// AddToolObservation records a tool's output as a *user-role* message
// wrapped in an unambiguous envelope. This prevents the model from
// adopting the file/command output as if it were its own assistant text in
// the next turn — the most common cause of "the user has said…"
// hallucinations after the agent reads instruction files like AGENTS.md.
//
// We deliberately use role="user" (not role="tool") because role="tool" is
// inconsistently supported across local Ollama models, while every model
// understands a clearly-marked user observation block.
// ToolObservationUserContent builds the user-role text for a tool result. The
// agent loop must use the same string when advancing to the next model turn so
// the stored session transcript matches what the API receives.
func ToolObservationUserContent(toolName, output string) string {
	body := strings.TrimSpace(output)
	if body == "" {
		body = "(no output)"
	}
	if toolName == "" {
		toolName = "unknown"
	}
	return fmt.Sprintf(
		"[tool_result name=%s]\n%s\n[/tool_result]\n"+
			"(This is data from a tool. Use this information to complete the current and subsequent TODO steps. Do not re-run the same investigation — you already have the results above. Continue working on the user's original request.)",
		toolName, body,
	)
}

func (s *Session) AddToolObservation(toolName, output string) {
	content := ToolObservationUserContent(toolName, output)
	s.addMessage(Message{
		Role:      "user",
		Content:   content,
		Timestamp: time.Now().UTC(),
	})
}

func (s *Session) addMessage(msg Message) {
	s.messages = append(s.messages, msg)
	s.tokenEstimate += estimateTextTokens(msg.Content)
}

func (s *Session) TokenEstimate() int {
	return s.tokenEstimate
}

func (s *Session) ShouldCompact() bool {
	if s == nil || s.cfg == nil {
		return false
	}
	if len(s.messages) <= 30 {
		return false
	}
	return len(s.messages) > 300 || s.tokenEstimate > int(0.7*float64(s.cfg.ContextWindow))
}
