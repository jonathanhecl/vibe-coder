package session

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

type Message struct {
	Role      string    `json:"role"`
	Content   string    `json:"content"`
	Timestamp time.Time `json:"timestamp"`
	// Structured native-tool fields: assistant turns can replay ToolCalls,
	// and role "tool" messages carry ToolName. They persist alongside the
	// text content so the wire history matches /api/chat's native schema.
	ToolCalls []ollama.MessageToolCall `json:"tool_calls,omitempty"`
	ToolName  string                   `json:"tool_name,omitempty"`
}

type Session struct {
	mu            sync.RWMutex
	cfg           *config.Config
	id            string
	messages      []Message
	client        ollama.Client
	tokenEstimate int
	revision      uint64
	// pinnedContexts holds absolute paths of user-pinned guide files
	// (--context / /context). Only the paths are stored here; file
	// contents are reloaded into the agent system prompt on demand.
	pinnedContexts []string
	// workState is an opaque JSON blob owned by the agent runtime that
	// captures durable work state (the TODO checklist and task store). It is
	// persisted in a session sidecar so long autonomous runs can resume
	// without losing what is done and what remains.
	workState []byte
	// lastSavedRevision is the revision successfully persisted by Save.
	// Save skips the rewrite when nothing changed since (0 = never saved).
	lastSavedRevision uint64
}

func New(cfg *config.Config) *Session {
	return &Session{
		cfg:      cfg,
		id:       newSessionID(),
		messages: make([]Message, 0, 32),
	}
}

func (s *Session) ID() string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.id
}

// Messages returns a copy of the in-memory transcript. Safe to mutate; the
// underlying slice is cloned. Used by the agent runtime (e.g. compaction
// heuristics) and by tests that need to assert the exact wrapping of tool
// observations.
func (s *Session) Messages() []Message {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return cloneMessages(s.messages)
}

// MessagesReadOnly returns a snapshot of the transcript. The returned slice is
// independent from the session and safe to inspect after this method returns.
func (s *Session) MessagesReadOnly() []Message {
	return s.Messages()
}

func (s *Session) MessageCount() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.messages)
}

func (s *Session) SetClient(client ollama.Client) {
	s.mu.Lock()
	defer s.mu.Unlock()
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

// AddAssistantToolCalls stores a native tool-call assistant turn with the
// exact calls the model issued (not just their names). The XML fallback
// path stores its envelopes in Content instead and does not use this.
func (s *Session) AddAssistantToolCalls(content string, calls []ollama.MessageToolCall) {
	s.addMessage(Message{
		Role:      "assistant",
		Content:   content,
		ToolCalls: calls,
		Timestamp: time.Now().UTC(),
	})
}

// AddToolResult stores a native tool result (role "tool") tied to the
// calling tool name. The envelope-based AddToolObservation stays the
// representation for the XML fallback path.
func (s *Session) AddToolResult(toolName, output string) {
	body := strings.TrimSpace(output)
	if body == "" {
		body = "(no output)"
	}
	s.addMessage(Message{
		Role:      "tool",
		Content:   body,
		ToolName:  toolName,
		Timestamp: time.Now().UTC(),
	})
}

func (s *Session) addMessage(msg Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	s.tokenEstimate += estimateMessageTokens(msg.Content)
	s.revision++
}

func (s *Session) TokenEstimate() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.tokenEstimate
}

// SetPinnedContexts replaces the pinned context path list. Paths are
// stored as given; callers should pass absolute paths. The revision only
// moves when the list actually changes so unchanged syncs keep Save cheap.
func (s *Session) SetPinnedContexts(paths []string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if equalStrings(s.pinnedContexts, paths) {
		return
	}
	s.pinnedContexts = append([]string(nil), paths...)
	s.revision++
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if strings.TrimSpace(a[i]) != strings.TrimSpace(b[i]) {
			return false
		}
	}
	return true
}

// PinnedContexts returns a copy of the pinned context path list.
func (s *Session) PinnedContexts() []string {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]string(nil), s.pinnedContexts...)
}

// SetWorkState replaces the opaque durable work-state blob. It only bumps
// the revision when the bytes actually change so unchanged syncs keep Save
// cheap. Pass nil to clear it.
func (s *Session) SetWorkState(raw []byte) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if bytesEqual(s.workState, raw) {
		return
	}
	if len(raw) == 0 {
		s.workState = nil
	} else {
		s.workState = append([]byte(nil), raw...)
	}
	s.revision++
}

// WorkState returns a copy of the durable work-state blob (nil when unset).
func (s *Session) WorkState() []byte {
	if s == nil {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.workState) == 0 {
		return nil
	}
	return append([]byte(nil), s.workState...)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// firstUserMessageUnlocked returns the first user-role message that is not a
// tool observation envelope or a runtime note. This is the original task the
// user asked the agent to perform; compaction preserves it verbatim so the
// agent never loses its goal. Caller must hold s.mu.
func (s *Session) firstUserMessageUnlocked() *Message {
	for i := range s.messages {
		m := &s.messages[i]
		if m.Role != "user" {
			continue
		}
		if strings.HasPrefix(m.Content, "[tool_result") {
			continue
		}
		if strings.HasPrefix(m.Content, "[Earlier conversation summary]") {
			continue
		}
		return m
	}
	return nil
}

func (s *Session) ShouldCompact() bool {
	if s == nil {
		return false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.cfg == nil || len(s.messages) <= 30 {
		return false
	}
	return len(s.messages) > 300 || s.tokenEstimate > int(0.7*float64(s.cfg.ContextWindow))
}

func cloneMessages(messages []Message) []Message {
	out := make([]Message, len(messages))
	copy(out, messages)
	return out
}
