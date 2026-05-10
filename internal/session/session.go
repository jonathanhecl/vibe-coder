package session

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/ollama"
)

const maxSessionFileBytes = 50 * 1024 * 1024

var invalidSessionIDChars = regexp.MustCompile(`[^A-Za-z0-9_\-]`)

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

func (s *Session) Compact(ctx context.Context, force bool) error {
	if !force && !s.ShouldCompact() {
		return nil
	}
	if len(s.messages) <= 30 {
		return nil
	}
	cut := len(s.messages) - 30
	old := append([]Message(nil), s.messages[:cut]...)
	recent := append([]Message(nil), s.messages[cut:]...)

	var summary string
	if s.client != nil && s.cfg.SidecarInUse() {
		text := renderMessagesForSummary(old)
		resp, err := s.client.ChatSync(ctx, ollama.ChatRequest{
			Model: s.cfg.SidecarModel,
			Messages: []ollama.Message{
				{Role: "system", Content: "Summarize the conversation concisely."},
				{Role: "user", Content: text},
			},
			Stream: false,
		})
		if err == nil && strings.TrimSpace(resp.Content) != "" {
			summary = resp.Content
		}
	}
	if summary == "" {
		summary = "Earlier conversation truncated to stay within context limits."
	}
	s.sessAddSummary(summary)
	s.messages = append(s.messages, recent...)
	// Avoid starting with tool-like or empty roles in future extensions.
	for len(s.messages) > 0 && strings.TrimSpace(s.messages[0].Role) == "" {
		s.messages = s.messages[1:]
	}
	s.recomputeTokenEstimate()
	return nil
}

func (s *Session) Clear() {
	s.id = newSessionID()
	s.messages = s.messages[:0]
	s.tokenEstimate = 0
}

func (s *Session) Save() error {
	if err := os.MkdirAll(s.cfg.SessionsDir, 0o755); err != nil {
		return fmt.Errorf("create sessions dir: %w", err)
	}

	target, err := s.sessionFilePath(s.id)
	if err != nil {
		return err
	}

	if err := writeAtomicFile(s.cfg.SessionsDir, "*.jsonl.tmp", target, 0o600, func(w io.Writer) error {
		writer := bufio.NewWriter(w)
		enc := json.NewEncoder(writer)
		for _, msg := range s.messages {
			if err := enc.Encode(msg); err != nil {
				return fmt.Errorf("encode message: %w", err)
			}
		}
		if err := writer.Flush(); err != nil {
			return fmt.Errorf("flush session temp file: %w", err)
		}
		return nil
	}); err != nil {
		return fmt.Errorf("atomic session replace: %w", err)
	}

	if err := s.writeProjectIndex(); err != nil {
		return err
	}
	return nil
}

func (s *Session) Load(id string) error {
	sanitized := sanitizeSessionID(id)
	if sanitized == "" {
		return fmt.Errorf("invalid session id: %q", id)
	}

	path, err := s.sessionFilePath(sanitized)
	if err != nil {
		return err
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("stat session file: %w", err)
	}
	if info.Size() > maxSessionFileBytes {
		return fmt.Errorf("session file too large: %d bytes", info.Size())
	}

	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open session file: %w", err)
	}
	defer file.Close()

	loaded := make([]Message, 0, 64)
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		loaded = append(loaded, msg)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("scan session file: %w", err)
	}

	s.id = sanitized
	s.messages = loaded
	s.recomputeTokenEstimate()
	return nil
}

func (s *Session) LoadByProject() (bool, error) {
	indexPath := filepath.Join(s.cfg.SessionsDir, "project-index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, fmt.Errorf("read project index: %w", err)
	}

	index := map[string]string{}
	if err := json.Unmarshal(data, &index); err != nil {
		return false, fmt.Errorf("decode project index: %w", err)
	}

	hash, err := cwdHash(s.cfg.Cwd)
	if err != nil {
		return false, err
	}
	id := sanitizeSessionID(index[hash])
	if id == "" {
		return false, nil
	}
	if err := s.Load(id); err != nil {
		return false, err
	}
	return true, nil
}

// DeleteSession removes a single saved session file by id and prunes any
// project-index entry that points to it. It is safe to call for ids the
// caller never created (returns os.ErrNotExist), and the index update is
// skipped silently when the index file is missing or already clean.
func DeleteSession(cfg *config.Config, id string) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	sanitized := sanitizeSessionID(id)
	if sanitized == "" {
		return fmt.Errorf("invalid session id: %q", id)
	}
	dir := strings.TrimSpace(cfg.SessionsDir)
	if dir == "" {
		return fmt.Errorf("sessions dir not configured")
	}
	dirAbs, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("resolve sessions dir: %w", err)
	}
	target := filepath.Join(dir, sanitized+".jsonl")
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return fmt.Errorf("resolve session path: %w", err)
	}
	if !strings.HasPrefix(targetAbs, dirAbs+string(filepath.Separator)) {
		return fmt.Errorf("invalid session path outside sessions dir")
	}
	if err := os.Remove(target); err != nil {
		return fmt.Errorf("remove session file: %w", err)
	}
	if err := pruneProjectIndex(dir, sanitized); err != nil {
		return err
	}
	return nil
}

// DeleteAllSessions removes every *.jsonl file in cfg.SessionsDir along
// with project-index.json. Returns the number of session files removed.
// Non-session files (logs, configs the user dropped here by mistake) are
// left untouched.
func DeleteAllSessions(cfg *config.Config) (int, error) {
	if cfg == nil {
		return 0, fmt.Errorf("nil config")
	}
	dir := strings.TrimSpace(cfg.SessionsDir)
	if dir == "" {
		return 0, fmt.Errorf("sessions dir not configured")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("read sessions dir: %w", err)
	}
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := sanitizeSessionID(strings.TrimSuffix(name, ".jsonl"))
		if id == "" {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return removed, fmt.Errorf("remove %s: %w", name, err)
		}
		removed++
	}
	indexPath := filepath.Join(dir, "project-index.json")
	if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
		return removed, fmt.Errorf("remove project index: %w", err)
	}
	return removed, nil
}

// pruneProjectIndex removes every entry whose value matches id. We rewrite
// the file atomically to keep the existing crash-safety guarantees from
// writeProjectIndex. When the index becomes empty we delete the file
// entirely so a fresh project starts from a clean state.
func pruneProjectIndex(dir, id string) error {
	indexPath := filepath.Join(dir, "project-index.json")
	raw, err := os.ReadFile(indexPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read project index: %w", err)
	}
	index := map[string]string{}
	if err := json.Unmarshal(raw, &index); err != nil {
		// Corrupt index — nothing useful to prune; leave it for ListSessions
		// to ignore so we don't accidentally erase recoverable state.
		return nil
	}
	changed := false
	for hash, mapped := range index {
		if mapped == id {
			delete(index, hash)
			changed = true
		}
	}
	if !changed {
		return nil
	}
	if len(index) == 0 {
		if err := os.Remove(indexPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("remove empty project index: %w", err)
		}
		return nil
	}
	out, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode pruned project index: %w", err)
	}
	if err := writeAtomicBytes(dir, "*.index.tmp", indexPath, 0o600, out); err != nil {
		return fmt.Errorf("replace index file: %w", err)
	}
	return nil
}

// SessionInfo is a lightweight metadata view of a stored session, suitable
// for listing/picking from the REPL without loading the full transcript.
type SessionInfo struct {
	ID               string
	Path             string
	ModTime          time.Time
	Size             int64
	MessageCount     int
	Preview          string
	IsCurrentProject bool
}

// ListSessions enumerates persisted sessions in cfg.SessionsDir, returning
// metadata sorted by most recent first. The IsCurrentProject flag is true
// for the session id mapped to the current cwd in project-index.json.
func ListSessions(cfg *config.Config) ([]SessionInfo, error) {
	if cfg == nil {
		return nil, fmt.Errorf("nil config")
	}
	dir := strings.TrimSpace(cfg.SessionsDir)
	if dir == "" {
		return nil, fmt.Errorf("sessions dir not configured")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read sessions dir: %w", err)
	}

	currentProjectID := ""
	if hash, err := cwdHash(cfg.Cwd); err == nil {
		indexPath := filepath.Join(dir, "project-index.json")
		if raw, err := os.ReadFile(indexPath); err == nil {
			index := map[string]string{}
			if json.Unmarshal(raw, &index) == nil {
				currentProjectID = sanitizeSessionID(index[hash])
			}
		}
	}

	out := make([]SessionInfo, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(name, ".jsonl") {
			continue
		}
		id := sanitizeSessionID(strings.TrimSuffix(name, ".jsonl"))
		if id == "" {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		full := filepath.Join(dir, name)
		count, preview := scanSessionMeta(full)
		out = append(out, SessionInfo{
			ID:               id,
			Path:             full,
			ModTime:          info.ModTime(),
			Size:             info.Size(),
			MessageCount:     count,
			Preview:          preview,
			IsCurrentProject: id == currentProjectID,
		})
	}

	sort.Slice(out, func(i, j int) bool {
		return out[i].ModTime.After(out[j].ModTime)
	})
	return out, nil
}

// scanSessionMeta reads a session JSONL file once and returns a message
// count plus the first user message preview (trimmed to ~80 chars). It is
// best-effort: any decode error short-circuits with what was collected so
// far so the listing never crashes on a partially-written file.
func scanSessionMeta(path string) (int, string) {
	file, err := os.Open(path)
	if err != nil {
		return 0, ""
	}
	defer file.Close()

	count := 0
	preview := ""
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var msg Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			continue
		}
		count++
		if preview == "" && msg.Role == "user" {
			text := strings.TrimSpace(msg.Content)
			if !strings.HasPrefix(text, "[tool_result") &&
				!strings.HasPrefix(text, "[Earlier conversation summary]") &&
				!strings.HasPrefix(text, "[System Note]") {
				preview = collapseWhitespace(text)
				if len(preview) > 80 {
					preview = preview[:80] + "…"
				}
			}
		}
	}
	return count, preview
}

func collapseWhitespace(s string) string {
	fields := strings.Fields(s)
	return strings.Join(fields, " ")
}

func (s *Session) sessionFilePath(id string) (string, error) {
	sanitized := sanitizeSessionID(id)
	if sanitized == "" {
		return "", fmt.Errorf("invalid session id: %q", id)
	}

	sessionsDirAbs, err := filepath.Abs(s.cfg.SessionsDir)
	if err != nil {
		return "", fmt.Errorf("resolve sessions dir: %w", err)
	}
	path := filepath.Join(s.cfg.SessionsDir, sanitized+".jsonl")
	pathAbs, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve session path: %w", err)
	}
	if !strings.HasPrefix(pathAbs, sessionsDirAbs+string(filepath.Separator)) && pathAbs != sessionsDirAbs {
		return "", fmt.Errorf("invalid session path outside sessions dir")
	}
	return path, nil
}

func (s *Session) writeProjectIndex() error {
	hash, err := cwdHash(s.cfg.Cwd)
	if err != nil {
		return err
	}
	indexPath := filepath.Join(s.cfg.SessionsDir, "project-index.json")

	index := map[string]string{}
	if existing, err := os.ReadFile(indexPath); err == nil {
		_ = json.Unmarshal(existing, &index)
	}
	index[hash] = s.id

	raw, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return fmt.Errorf("encode project index: %w", err)
	}
	if err := writeAtomicBytes(s.cfg.SessionsDir, "*.index.tmp", indexPath, 0o600, raw); err != nil {
		return fmt.Errorf("replace index file: %w", err)
	}
	return nil
}

func writeAtomicBytes(dir, pattern, target string, mode os.FileMode, data []byte) error {
	return writeAtomicFile(dir, pattern, target, mode, func(w io.Writer) error {
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("write temp file: %w", err)
		}
		return nil
	})
}

// writeAtomicFile keeps session and index writes crash-safe: callers fill a
// temp file, then we chmod and rename it into place as the final step.
func writeAtomicFile(dir, pattern, target string, mode os.FileMode, write func(io.Writer) error) error {
	tmp, err := os.CreateTemp(dir, pattern)
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := write(tmp); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp file: %w", err)
	}
	if err := os.Chmod(tmpPath, mode); err != nil {
		return fmt.Errorf("chmod temp file: %w", err)
	}
	if err := os.Rename(tmpPath, target); err != nil {
		return err
	}
	cleanup = false
	return nil
}

func sanitizeSessionID(id string) string {
	clean := invalidSessionIDChars.ReplaceAllString(strings.TrimSpace(id), "")
	if len(clean) > 64 {
		clean = clean[:64]
	}
	return clean
}

func newSessionID() string {
	raw := make([]byte, 16)
	_, _ = rand.Read(raw)
	return hex.EncodeToString(raw)
}

func estimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	cjk := 0
	for _, r := range text {
		if (r >= 0x4E00 && r <= 0x9FFF) || (r >= 0x3040 && r <= 0x30FF) || (r >= 0xAC00 && r <= 0xD7AF) {
			cjk++
		}
	}
	asciiApprox := len(text) / 4
	return cjk + asciiApprox
}

func renderMessagesForSummary(messages []Message) string {
	var b strings.Builder
	for _, m := range messages {
		b.WriteString(m.Role)
		b.WriteString(": ")
		b.WriteString(m.Content)
		b.WriteString("\n")
	}
	return b.String()
}

func (s *Session) sessAddSummary(summary string) {
	s.messages = []Message{{
		Role:      "user",
		Content:   "[Earlier conversation summary]\n" + summary,
		Timestamp: time.Now().UTC(),
	}}
	s.recomputeTokenEstimate()
}

func (s *Session) recomputeTokenEstimate() {
	total := 0
	for _, msg := range s.messages {
		total += estimateTextTokens(msg.Content)
	}
	s.tokenEstimate = total
}

func cwdHash(cwd string) (string, error) {
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return "", fmt.Errorf("resolve cwd for index: %w", err)
	}
	sum := sha256.Sum256([]byte(abs))
	return hex.EncodeToString(sum[:])[:16], nil
}
