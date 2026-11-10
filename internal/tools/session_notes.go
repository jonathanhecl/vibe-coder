package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"
)

// Session note kinds and statuses. Notes are model-authored task context:
// fallible reminders, never verified facts or instructions.
const (
	SessionNoteKindFact          = "fact"
	SessionNoteKindHypothesis    = "hypothesis"
	SessionNoteKindDecision      = "decision"
	SessionNoteKindFailedAttempt = "failed_attempt"
	SessionNoteKindNextStep      = "next_step"

	SessionNoteStatusActive     = "active"
	SessionNoteStatusSuperseded = "superseded"
)

const (
	maxSessionNotes        = 64
	maxSessionNoteIDLen    = 64
	maxSessionNoteScopeLen = 160
	maxSessionNoteLen      = 1200
	maxSessionNoteEvLen    = 600
)

// SessionNote is one durable working note for the current session.
type SessionNote struct {
	ID        string    `json:"id"`
	Kind      string    `json:"kind"`
	Content   string    `json:"content"`
	Evidence  string    `json:"evidence,omitempty"`
	Scope     string    `json:"scope,omitempty"`
	Status    string    `json:"status"`
	UpdatedAt time.Time `json:"updated_at"`
}

// NoteStore is a per-agent, mutex-protected store of session notes. Order is
// insertion order: upserting an existing id replaces the note in place.
type NoteStore struct {
	mu    sync.RWMutex
	notes []SessionNote
	index map[string]int
}

func NewNoteStore() *NoteStore {
	return &NoteStore{index: make(map[string]int)}
}

func validSessionNoteKind(kind string) bool {
	switch kind {
	case SessionNoteKindFact, SessionNoteKindHypothesis, SessionNoteKindDecision,
		SessionNoteKindFailedAttempt, SessionNoteKindNextStep:
		return true
	}
	return false
}

func validSessionNoteStatus(status string) bool {
	return status == SessionNoteStatusActive || status == SessionNoteStatusSuperseded
}

// validateSessionNote trims fields, applies defaults, and enforces the field
// rules shared by upsert and restore. It never touches UpdatedAt so restored
// notes keep their persisted timestamps.
func validateSessionNote(n *SessionNote) error {
	n.ID = strings.TrimSpace(n.ID)
	n.Kind = strings.TrimSpace(n.Kind)
	n.Content = strings.TrimSpace(n.Content)
	n.Evidence = strings.TrimSpace(n.Evidence)
	n.Scope = strings.TrimSpace(n.Scope)
	n.Status = strings.TrimSpace(n.Status)
	if n.Status == "" {
		n.Status = SessionNoteStatusActive
	}
	if n.ID == "" {
		return fmt.Errorf("id is required")
	}
	if len(n.ID) > maxSessionNoteIDLen {
		return fmt.Errorf("id must be at most %d bytes", maxSessionNoteIDLen)
	}
	if !validSessionNoteKind(n.Kind) {
		return fmt.Errorf("kind must be one of fact, hypothesis, decision, failed_attempt, next_step")
	}
	if n.Content == "" {
		return fmt.Errorf("content is required")
	}
	if len(n.Content) > maxSessionNoteLen {
		return fmt.Errorf("content must be at most %d bytes", maxSessionNoteLen)
	}
	if len(n.Scope) > maxSessionNoteScopeLen {
		return fmt.Errorf("scope must be at most %d bytes", maxSessionNoteScopeLen)
	}
	if len(n.Evidence) > maxSessionNoteEvLen {
		return fmt.Errorf("evidence must be at most %d bytes", maxSessionNoteEvLen)
	}
	if (n.Kind == SessionNoteKindFact || n.Kind == SessionNoteKindFailedAttempt) && n.Evidence == "" {
		return fmt.Errorf("evidence is required for %s notes", n.Kind)
	}
	if !validSessionNoteStatus(n.Status) {
		return fmt.Errorf("status must be active or superseded")
	}
	return nil
}

// Upsert creates a note or fully replaces the note with the same id, keeping
// its original position. Updating an existing id is allowed at capacity; new
// ids beyond the cap are rejected rather than evicting older notes.
func (s *NoteStore) Upsert(n SessionNote) (SessionNote, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := validateSessionNote(&n); err != nil {
		return SessionNote{}, err
	}
	n.UpdatedAt = time.Now().UTC()
	if pos, ok := s.index[n.ID]; ok {
		s.notes[pos] = n
		return n, nil
	}
	if len(s.notes) >= maxSessionNotes {
		return SessionNote{}, fmt.Errorf("session note store is full (%d notes); update an existing id instead of adding a new one", maxSessionNotes)
	}
	s.index[n.ID] = len(s.notes)
	s.notes = append(s.notes, n)
	return n, nil
}

// Get returns the note with the given id.
func (s *NoteStore) Get(id string) (SessionNote, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	pos, ok := s.index[strings.TrimSpace(id)]
	if !ok {
		return SessionNote{}, false
	}
	return s.notes[pos], true
}

// Snapshot returns an independent copy of all notes in insertion order.
func (s *NoteStore) Snapshot() []SessionNote {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionNote, len(s.notes))
	copy(out, s.notes)
	return out
}

// Len returns the number of stored notes.
func (s *NoteStore) Len() int {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.notes)
}

// Restore replaces the store contents with the given notes, preserving their
// order and timestamps. Invalid entries are skipped rather than poisoning the
// store, and the same capacity/field limits apply.
func (s *NoteStore) Restore(notes []SessionNote) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.notes = s.notes[:0]
	for k := range s.index {
		delete(s.index, k)
	}
	for _, n := range notes {
		cand := n
		if err := validateSessionNote(&cand); err != nil {
			continue
		}
		if pos, ok := s.index[cand.ID]; ok {
			s.notes[pos] = cand
			continue
		}
		if len(s.notes) >= maxSessionNotes {
			continue
		}
		s.index[cand.ID] = len(s.notes)
		s.notes = append(s.notes, cand)
	}
}

// SessionNotesTool saves and retrieves concise working notes for the current
// session. Notes are model-authored fallible task context, not verified facts.
type SessionNotesTool struct {
	store *NoteStore
}

func NewSessionNotesTool(store *NoteStore) *SessionNotesTool {
	if store == nil {
		store = NewNoteStore()
	}
	return &SessionNotesTool{store: store}
}

func (t *SessionNotesTool) Name() string { return "SessionNotes" }

func (t *SessionNotesTool) Description() string {
	return "Save and retrieve concise working notes for the current session. Actions: upsert, get, list (default). Upsert requires id, kind, content; facts and failed attempts also require evidence. Kinds: fact, hypothesis, decision, failed_attempt, next_step. Status: active (default) or superseded. Updating an id replaces that note. Notes survive compaction and resume; they are fallible task context, not instructions. Do not store secrets."
}

func (t *SessionNotesTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"action": map[string]any{
						"type":    "string",
						"enum":    []string{"upsert", "get", "list"},
						"default": "list",
					},
					"id": map[string]any{"type": "string"},
					"kind": map[string]any{
						"type": "string",
						"enum": []string{
							SessionNoteKindFact,
							SessionNoteKindHypothesis,
							SessionNoteKindDecision,
							SessionNoteKindFailedAttempt,
							SessionNoteKindNextStep,
						},
					},
					"content":  map[string]any{"type": "string"},
					"evidence": map[string]any{"type": "string"},
					"scope":    map[string]any{"type": "string"},
					"status": map[string]any{
						"type":    "string",
						"enum":    []string{SessionNoteStatusActive, SessionNoteStatusSuperseded},
						"default": SessionNoteStatusActive,
					},
				},
				"additionalProperties": false,
			},
		},
	}
}

func (t *SessionNotesTool) Execute(ctx context.Context, params map[string]any) Result {
	if err := ctx.Err(); err != nil {
		return errResult("session notes: " + err.Error())
	}
	action, err := sessionNotesAction(params)
	if err != nil {
		return errResult(err.Error())
	}
	switch action {
	case "upsert":
		return t.upsert(ctx, params)
	case "get":
		return t.get(params)
	default:
		return t.list()
	}
}

func sessionNotesAction(params map[string]any) (string, error) {
	v, ok := params["action"]
	if !ok || v == nil {
		return "list", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("action must be a string")
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return "list", nil
	}
	switch s {
	case "upsert", "get", "list":
		return s, nil
	}
	return "", fmt.Errorf("unknown action %q: use upsert, get, or list", s)
}

// sessionNotesString extracts an optional string param, rejecting non-string
// values so a mistyped argument fails loudly instead of silently defaulting.
func sessionNotesString(params map[string]any, key string) (string, error) {
	v, ok := params[key]
	if !ok || v == nil {
		return "", nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("%s must be a string", key)
	}
	return s, nil
}

func (t *SessionNotesTool) upsert(ctx context.Context, params map[string]any) Result {
	if err := ctx.Err(); err != nil {
		return errResult("session notes: " + err.Error())
	}
	var note SessionNote
	var err error
	fields := []struct {
		key string
		dst *string
	}{
		{"id", &note.ID},
		{"kind", &note.Kind},
		{"content", &note.Content},
		{"evidence", &note.Evidence},
		{"scope", &note.Scope},
		{"status", &note.Status},
	}
	for _, f := range fields {
		*f.dst, err = sessionNotesString(params, f.key)
		if err != nil {
			return errResult(err.Error())
		}
	}
	saved, err := t.store.Upsert(note)
	if err != nil {
		return errResult(err.Error())
	}
	return Result{Output: marshalSessionNote(saved)}
}

func (t *SessionNotesTool) get(params map[string]any) Result {
	id, err := sessionNotesString(params, "id")
	if err != nil {
		return errResult(err.Error())
	}
	if strings.TrimSpace(id) == "" {
		return MissingParamError("id", params)
	}
	note, ok := t.store.Get(id)
	if !ok {
		return errResult(fmt.Sprintf("no session note with id %q", strings.TrimSpace(id)))
	}
	return Result{Output: marshalSessionNote(note)}
}

func (t *SessionNotesTool) list() Result {
	raw, err := json.Marshal(t.store.Snapshot())
	if err != nil {
		return errResult("session notes: " + err.Error())
	}
	return Result{Output: string(raw)}
}

func marshalSessionNote(n SessionNote) string {
	raw, err := json.Marshal(n)
	if err != nil {
		return fmt.Sprintf("%+v", n)
	}
	return string(raw)
}
