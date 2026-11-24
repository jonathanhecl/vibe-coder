package tools

import (
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

// Resume reactivates a blocked mission. It is used when the user explicitly
// continues paused work; active and completed missions are left unchanged.
func (s *MissionStore) Resume() Mission {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.current.Status == MissionStatusBlocked {
		s.current.Status = MissionStatusActive
		s.current.Summary = ""
		s.current.UpdatedAt = time.Now().UTC()
	}
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
