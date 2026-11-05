package agent

import (
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

func TestExtractPriorGoalPrefersMostRecent(t *testing.T) {
	t.Parallel()
	cfg := &config.Config{Cwd: t.TempDir(), SessionsDir: t.TempDir()}
	s := session.New(cfg)
	s.AddUser("a very long first request that describes the whole project in detail and keeps going")
	s.AddUser("fix typo")
	if g := resolveGoalForRun(s, "continua"); g != "fix typo" {
		t.Fatalf("expected most-recent goal, got %q", g)
	}
}

func TestRuntimeStateNoteDedupsGoalWhenMissionActive(t *testing.T) {
	t.Parallel()
	ag, _, reg := newTurnAgent(t, &fakeClient{})
	seedChecklist(t, reg, []tools.TodoItem{{ID: "1", Content: "open step", Status: tools.TodoStatusPending}})
	ag.currentGoal = "do the thing"
	ag.StartMission("do the thing")
	ag.mission.IncrementTurns()
	note := ag.runtimeStateNote()
	if strings.Count(note, "do the thing") > 1 {
		t.Fatalf("goal duplicated with active mission: %q", note)
	}
}
