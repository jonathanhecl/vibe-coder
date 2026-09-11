package tools

import (
	"context"
	"strings"
	"sync"
	"time"
)

// Mission statuses. A mission is the agent's own declaration that a task needs
// sustained work across many turns. While a mission is active the runtime keeps
// starting turns for the agent; only the agent ends it by completing or
// blocking it. There is intentionally no turn limit.
const (
	MissionStatusActive    = "active"
	MissionStatusCompleted = "completed"
	MissionStatusBlocked   = "blocked"
)

// Mission is the durable, task-agnostic description of a long-running goal.
type Mission struct {
	Goal      string    `json:"goal,omitempty"`
	Status    string    `json:"status"`
	Summary   string    `json:"summary,omitempty"`
	StartedAt time.Time `json:"started_at,omitempty"`
	UpdatedAt time.Time `json:"updated_at,omitempty"`
	// Turns counts how many autonomous continuation turns have run. It is
	// informational only; it never stops the mission.
	Turns int `json:"turns,omitempty"`
}

// MissionStore is a goroutine-safe holder for the current mission. The agent
// owns one instance and persists it with the rest of the work state.
type MissionStore struct {
	mu      sync.Mutex
	current Mission
}

// NewMissionStore returns an empty mission store.
func NewMissionStore() *MissionStore { return &MissionStore{} }

// Start activates a new mission, replacing any previous one.
func (s *MissionStore) Start(goal string) Mission {
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now().UTC()
	s.current = Mission{
		Goal:      strings.TrimSpace(goal),
		Status:    MissionStatusActive,
		StartedAt: now,
		UpdatedAt: now,
	}
	return s.current
}

// Complete marks the active mission as done with a summary.
func (s *MissionStore) Complete(summary string) Mission {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current.Status = MissionStatusCompleted
	s.current.Summary = strings.TrimSpace(summary)
	s.current.UpdatedAt = time.Now().UTC()
	return s.current
}

// Block marks the mission as blocked so the runtime stops and the user can help.
func (s *MissionStore) Block(reason string) Mission {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current.Status = MissionStatusBlocked
	s.current.Summary = strings.TrimSpace(reason)
	s.current.UpdatedAt = time.Now().UTC()
	return s.current
}

// Active reports whether a mission is currently running.
func (s *MissionStore) Active() bool {
	if s == nil {
		return false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current.Status == MissionStatusActive
}

// IncrementTurns records one more autonomous continuation turn.
func (s *MissionStore) IncrementTurns() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current.Turns++
}

// Snapshot returns a copy of the current mission.
func (s *MissionStore) Snapshot() Mission {
	if s == nil {
		return Mission{}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current
}

// Restore replaces the current mission with a persisted one.
func (s *MissionStore) Restore(m Mission) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = m
}

// MissionStartTool lets the agent declare a long-running mission. Activating it
// is what turns a single reply into an unattended run that keeps going until
// the agent itself calls MissionComplete or MissionBlocked.
type MissionStartTool struct{ store *MissionStore }

func NewMissionStartTool(store *MissionStore) *MissionStartTool {
	return &MissionStartTool{store: store}
}

func (t *MissionStartTool) Name() string { return "MissionStart" }
func (t *MissionStartTool) Description() string {
	return "Activate an autonomous mission for a task that needs sustained work beyond a single reply " +
		"(long batches, migrations, iterative generate-and-verify loops, audits over many items). " +
		"While a mission is active the runtime keeps starting new turns for you until YOU end it. " +
		"Declare a TODO checklist first so progress is visible. Pass the user's goal verbatim so it survives " +
		"context compaction and restarts. End the mission with MissionComplete when the goal is fully done, " +
		"or MissionBlocked when you truly need user input."
}
func (t *MissionStartTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"goal": map[string]any{
						"type":        "string",
						"description": "The mission goal, in the user's own words. Required.",
					},
				},
				"required": []string{"goal"},
			},
		},
	}
}
func (t *MissionStartTool) Execute(_ context.Context, params map[string]any) Result {
	goal := strings.TrimSpace(asStringParam(params, "goal"))
	if goal == "" {
		return errResult("goal is required to start a mission")
	}
	t.store.Start(goal)
	return Result{Output: "Mission active. Keep working the checklist with tool calls; the runtime will continue " +
		"starting turns for you. Call MissionComplete with a summary when the goal is done, or MissionBlocked " +
		"if you need user input."}
}

// MissionCompleteTool ends the active mission successfully.
type MissionCompleteTool struct{ store *MissionStore }

func NewMissionCompleteTool(store *MissionStore) *MissionCompleteTool {
	return &MissionCompleteTool{store: store}
}

func (t *MissionCompleteTool) Name() string { return "MissionComplete" }
func (t *MissionCompleteTool) Description() string {
	return "Mark the active mission as complete once the goal is fully accomplished and verified. " +
		"Provide a short summary of what was done. After this the runtime stops the autonomous run."
}
func (t *MissionCompleteTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"summary": map[string]any{
						"type":        "string",
						"description": "Short summary of the completed mission.",
					},
				},
				"required": []string{"summary"},
			},
		},
	}
}
func (t *MissionCompleteTool) Execute(_ context.Context, params map[string]any) Result {
	summary := strings.TrimSpace(asStringParam(params, "summary"))
	if summary == "" {
		summary = "Mission completed."
	}
	t.store.Complete(summary)
	return Result{Output: "Mission marked complete. The autonomous run will stop after this turn."}
}

// MissionBlockedTool pauses the mission when the agent cannot proceed without
// the user (missing input, credentials, or an unrecoverable ambiguity).
type MissionBlockedTool struct{ store *MissionStore }

func NewMissionBlockedTool(store *MissionStore) *MissionBlockedTool {
	return &MissionBlockedTool{store: store}
}

func (t *MissionBlockedTool) Name() string { return "MissionBlocked" }
func (t *MissionBlockedTool) Description() string {
	return "Pause the active mission when you cannot continue without user input (missing credentials, an " +
		"ambiguous requirement, or an external failure you cannot resolve). Explain what you need. " +
		"Use this instead of spinning; the autonomous run stops until the user responds."
}
func (t *MissionBlockedTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"reason": map[string]any{
						"type":        "string",
						"description": "What blocks progress and what the user must provide or decide.",
					},
				},
				"required": []string{"reason"},
			},
		},
	}
}
func (t *MissionBlockedTool) Execute(_ context.Context, params map[string]any) Result {
	reason := strings.TrimSpace(asStringParam(params, "reason"))
	if reason == "" {
		reason = "Mission blocked; user input required."
	}
	t.store.Block(reason)
	return Result{Output: "Mission paused. Tell the user what you need and wait for their response."}
}

func asStringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	v, _ := params[key].(string)
	return v
}
