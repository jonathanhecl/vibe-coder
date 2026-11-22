package agent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/ollama"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// captureChatClient records every request so a continuation test can inspect
// the wire context instead of inferring it from the stored transcript.
type captureChatClient struct {
	fakeClient
	replies  []string
	requests []ollama.ChatRequest
	calls    int
}

func (c *captureChatClient) Chat(_ context.Context, req ollama.ChatRequest) (<-chan ollama.Chunk, error) {
	c.requests = append(c.requests, req)
	if c.calls >= len(c.replies) {
		return nil, context.Canceled
	}
	reply := c.replies[c.calls]
	c.calls++
	ch := make(chan ollama.Chunk, 1)
	ch <- ollama.Chunk{Delta: reply, Done: true}
	close(ch)
	return ch, nil
}

func TestWorkStatePersistsResumeCheckpoint(t *testing.T) {
	ag, _, reg := newTurnAgent(t, &fakeClient{})
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "write part 4", Status: tools.TodoStatusPending},
		{ID: "2", Content: "write part 5", Status: tools.TodoStatusPending},
	})
	ag.mu.Lock()
	ag.currentGoal = "write the five parts"
	ag.mu.Unlock()
	ag.PersistWorkState()

	raw := ag.sess.WorkState()
	var snap workStateSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("work state is not valid JSON: %v", err)
	}
	if snap.Resume == nil {
		t.Fatal("work state did not persist a resume checkpoint")
	}
	if snap.Resume.Goal != "write the five parts" {
		t.Fatalf("resume goal = %q", snap.Resume.Goal)
	}
	if snap.Resume.NextAction != "write part 4" {
		t.Fatalf("resume next action = %q, want write part 4", snap.Resume.NextAction)
	}

	restored, _, _ := newTurnAgent(t, &fakeClient{})
	restored.RestoreWorkState(raw)
	if !restored.CanResume() {
		t.Fatal("restored agent should be resumable")
	}
	if got := restored.resumeSnapshot().NextAction; got != "write part 4" {
		t.Fatalf("restored next action = %q, want write part 4", got)
	}

	// Completing the checklist must clear the stale checkpoint.
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "write part 4", Status: tools.TodoStatusCompleted},
		{ID: "2", Content: "write part 5", Status: tools.TodoStatusCompleted},
	})
	ag.PersistWorkState()
	if ag.CanResume() {
		t.Fatal("a finished checklist must not stay resumable")
	}
	if strings.Contains(string(ag.sess.WorkState()), `"resume"`) {
		t.Fatalf("finished work state still carries a checkpoint: %s", ag.sess.WorkState())
	}
}

func TestRunContinuationAddsNoUserMessageAndCarriesCheckpoint(t *testing.T) {
	client := &captureChatClient{replies: []string{
		`<invoke name="TodoWrite">{"todos":[{"id":"1","content":"write part 4","status":"completed"},{"id":"2","content":"write part 5","status":"completed"}]}</invoke>`,
		"parts 4 and 5 are written",
	}}
	ag, _, reg := newTurnAgent(t, client)
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "write part 4", Status: tools.TodoStatusPending},
		{ID: "2", Content: "write part 5", Status: tools.TodoStatusPending},
	})
	ag.sess.AddUser("write the five parts")
	ag.mu.Lock()
	ag.currentGoal = "write the five parts"
	ag.mu.Unlock()
	ag.PersistWorkState()

	if err := ag.RunContinuation(context.Background()); err != nil {
		t.Fatalf("continuation failed: %v", err)
	}
	if len(client.requests) == 0 {
		t.Fatal("continuation never reached the model")
	}
	for _, msg := range ag.sess.MessagesReadOnly() {
		if msg.Role == "user" && strings.TrimSpace(msg.Content) == "" {
			t.Fatal("continuation stored an empty user message")
		}
	}
	var wire strings.Builder
	for _, msg := range client.requests[0].Messages {
		wire.WriteString(msg.Content)
		wire.WriteString("\n")
	}
	for _, want := range []string{"Resume checkpoint", "write part 4", "Do not restate the plan"} {
		if !strings.Contains(wire.String(), want) {
			t.Fatalf("continuation wire context is missing %q:\n%s", want, wire.String())
		}
	}
	if strings.Contains(string(ag.sess.WorkState()), `"resume"`) {
		t.Fatalf("completed work state still carries the old checkpoint: %s", ag.sess.WorkState())
	}
}

func TestRunContinuationWithoutTranscriptCarriesCheckpoint(t *testing.T) {
	client := &captureChatClient{replies: []string{
		`<invoke name="TodoWrite">{"todos":[{"id":"1","content":"write part 4","status":"completed"}]}</invoke>`,
		"done",
	}}
	ag, _, reg := newTurnAgent(t, client)
	seedChecklist(t, reg, []tools.TodoItem{
		{ID: "1", Content: "write part 4", Status: tools.TodoStatusPending},
	})
	ag.mu.Lock()
	ag.currentGoal = "write the five parts"
	ag.mu.Unlock()
	ag.PersistWorkState()

	if err := ag.RunContinuation(context.Background()); err != nil {
		t.Fatalf("continuation failed: %v", err)
	}
	if len(client.requests) == 0 {
		t.Fatal("continuation never reached the model")
	}
	var wire strings.Builder
	for _, msg := range client.requests[0].Messages {
		wire.WriteString(msg.Content)
		wire.WriteString("\n")
	}
	if !strings.Contains(wire.String(), "Resume checkpoint") || !strings.Contains(wire.String(), "write part 4") {
		t.Fatalf("a transcript-less continuation lost its checkpoint:\n%s", wire.String())
	}
}

func TestRunContinuationReactivatesBlockedMission(t *testing.T) {
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	snap := ag.mission.Start("finish the migration")
	ag.mission.Block("needs a decision")
	if ag.MissionSnapshot().Status != tools.MissionStatusBlocked {
		t.Fatalf("setup mission status = %q", ag.MissionSnapshot().Status)
	}
	ag.PersistWorkState()

	if !ag.CanResume() {
		t.Fatal("a blocked mission with a goal should be resumable")
	}
	if err := ag.RunContinuation(context.Background()); err != nil {
		t.Fatalf("continuation failed: %v", err)
	}
	if got := ag.MissionSnapshot(); got.Status != tools.MissionStatusActive {
		t.Fatalf("mission status after continuation = %q, want %q (started %q)", got.Status, tools.MissionStatusActive, snap.Goal)
	}
}

func TestIterationCapWithoutChecklistLeavesResumeCursor(t *testing.T) {
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	ag.mu.Lock()
	ag.currentGoal = "write the five parts"
	ag.mu.Unlock()

	ag.finishRunCheckpoint(errors.New("iteration cap reached (50): the turn used its steps"))
	if !ag.CanResume() {
		t.Fatal("an interrupted turn without a checklist should still be resumable")
	}
	if next := ag.resumeSnapshot().NextAction; !strings.Contains(next, "interrupted task") {
		t.Fatalf("fallback next action = %q", next)
	}
	ag.PersistWorkState()
	restored, _, _ := newTurnAgent(t, &fakeClient{})
	restored.RestoreWorkState(ag.sess.WorkState())
	if !restored.CanResume() {
		t.Fatal("the interrupted checkpoint did not survive work-state restore")
	}

	ag.finishRunCheckpoint(nil)
	if ag.CanResume() {
		t.Fatal("a completed turn must clear an interrupted checkpoint")
	}
}

func TestRunContinuationRejectsWhenNothingToResume(t *testing.T) {
	ag, _, _ := newTurnAgent(t, &fakeClient{})
	if err := ag.RunContinuation(context.Background()); !errors.Is(err, ErrNothingToResume) {
		t.Fatalf("continuation error = %v, want ErrNothingToResume", err)
	}
}
