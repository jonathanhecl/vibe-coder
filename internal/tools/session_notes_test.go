package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestSessionNotesUpsertGetList(t *testing.T) {
	t.Parallel()
	store := NewNoteStore()
	tool := NewSessionNotesTool(store)

	res := tool.Execute(context.Background(), map[string]any{
		"action":   "upsert",
		"id":       "root-cause",
		"kind":     "fact",
		"content":  "The timeout comes from the 30s HTTP client default.",
		"evidence": "internal/client.go:42 sets no Timeout",
		"scope":    "auth flow",
	})
	if res.IsError {
		t.Fatalf("upsert failed: %s", res.Output)
	}
	var saved SessionNote
	if err := json.Unmarshal([]byte(res.Output), &saved); err != nil {
		t.Fatalf("upsert output is not JSON: %v (%q)", err, res.Output)
	}
	if saved.ID != "root-cause" || saved.Kind != "fact" || saved.Status != "active" {
		t.Fatalf("unexpected saved note: %+v", saved)
	}
	if saved.UpdatedAt.IsZero() {
		t.Fatalf("expected UpdatedAt to be set")
	}

	res = tool.Execute(context.Background(), map[string]any{"action": "get", "id": "root-cause"})
	if res.IsError {
		t.Fatalf("get failed: %s", res.Output)
	}
	var got SessionNote
	if err := json.Unmarshal([]byte(res.Output), &got); err != nil {
		t.Fatalf("get output is not JSON: %v", err)
	}
	if got.Content != saved.Content || got.Evidence != saved.Evidence {
		t.Fatalf("get returned different note: %+v vs %+v", got, saved)
	}

	res = tool.Execute(context.Background(), map[string]any{"action": "list"})
	if res.IsError {
		t.Fatalf("list failed: %s", res.Output)
	}
	var list []SessionNote
	if err := json.Unmarshal([]byte(res.Output), &list); err != nil {
		t.Fatalf("list output is not JSON: %v", err)
	}
	if len(list) != 1 || list[0].ID != "root-cause" {
		t.Fatalf("unexpected list: %+v", list)
	}
}

func TestSessionNotesListEmptyReturnsEmptyArray(t *testing.T) {
	t.Parallel()
	tool := NewSessionNotesTool(NewNoteStore())
	res := tool.Execute(context.Background(), nil)
	if res.IsError {
		t.Fatalf("list failed: %s", res.Output)
	}
	if res.Output != "[]" {
		t.Fatalf("expected empty JSON array, got %q", res.Output)
	}
}

func TestSessionNotesUpdateSameIDReplacesAndKeepsPosition(t *testing.T) {
	t.Parallel()
	store := NewNoteStore()
	tool := NewSessionNotesTool(store)
	upsert := func(id, content string) {
		t.Helper()
		res := tool.Execute(context.Background(), map[string]any{
			"action": "upsert", "id": id, "kind": "next_step", "content": content,
		})
		if res.IsError {
			t.Fatalf("upsert %s: %s", id, res.Output)
		}
	}
	upsert("first", "step one")
	upsert("second", "step two")
	upsert("first", "step one revised")

	snap := store.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 notes, got %d", len(snap))
	}
	if snap[0].ID != "first" || snap[0].Content != "step one revised" {
		t.Fatalf("update did not replace in place: %+v", snap[0])
	}
	if snap[1].ID != "second" {
		t.Fatalf("order changed: %+v", snap)
	}
}

func TestSessionNotesEvidenceRules(t *testing.T) {
	t.Parallel()
	tool := NewSessionNotesTool(NewNoteStore())
	ctx := context.Background()

	for _, kind := range []string{"fact", "failed_attempt"} {
		res := tool.Execute(ctx, map[string]any{
			"action": "upsert", "id": "n-" + kind, "kind": kind, "content": "claim",
		})
		if !res.IsError {
			t.Fatalf("%s without evidence should fail", kind)
		}
		if !strings.Contains(res.Output, "evidence") {
			t.Fatalf("%s error should mention evidence, got %q", kind, res.Output)
		}
	}

	res := tool.Execute(ctx, map[string]any{
		"action": "upsert", "id": "hyp", "kind": "hypothesis", "content": "maybe the cache is stale",
	})
	if res.IsError {
		t.Fatalf("hypothesis without evidence should be allowed: %s", res.Output)
	}
}

func TestSessionNotesRejectsInvalidInputWithoutMutation(t *testing.T) {
	t.Parallel()
	store := NewNoteStore()
	tool := NewSessionNotesTool(store)
	ctx := context.Background()

	good := map[string]any{
		"action": "upsert", "id": "ok", "kind": "decision", "content": "use sqlite",
	}
	if res := tool.Execute(ctx, good); res.IsError {
		t.Fatalf("setup upsert failed: %s", res.Output)
	}
	before := store.Snapshot()

	cases := []map[string]any{
		{"action": "upsert", "id": "", "kind": "decision", "content": "x"},
		{"action": "upsert", "id": "n", "kind": "bogus", "content": "x"},
		{"action": "upsert", "id": "n", "kind": "decision", "content": ""},
		{"action": "upsert", "id": "n", "kind": "decision", "content": "x", "status": "deleted"},
		{"action": "upsert", "id": "n", "kind": "decision", "content": "x", "scope": strings.Repeat("s", 161)},
		{"action": "upsert", "id": "n", "kind": "decision", "content": strings.Repeat("c", 1201)},
		{"action": "upsert", "id": "n", "kind": "fact", "content": "x", "evidence": strings.Repeat("e", 601)},
		{"action": "upsert", "id": strings.Repeat("i", 65), "kind": "decision", "content": "x"},
		{"action": "upsert", "id": 42, "kind": "decision", "content": "x"},
		{"action": "upsert", "id": "n", "kind": "decision", "content": "x", "status": 1},
		{"action": "bogus", "id": "n"},
		{"action": "get"},
		{"action": "get", "id": "missing-note"},
	}
	for i, params := range cases {
		res := tool.Execute(ctx, params)
		if !res.IsError {
			t.Fatalf("case %d should fail: %+v -> %s", i, params, res.Output)
		}
	}
	after := store.Snapshot()
	if len(after) != len(before) {
		t.Fatalf("rejected operations mutated the store: %d -> %d", len(before), len(after))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Fatalf("rejected operations mutated note %d", i)
		}
	}
}

func TestSessionNotesCanceledContext(t *testing.T) {
	t.Parallel()
	store := NewNoteStore()
	tool := NewSessionNotesTool(store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := tool.Execute(ctx, map[string]any{
		"action": "upsert", "id": "n", "kind": "decision", "content": "x",
	})
	if !res.IsError {
		t.Fatalf("canceled context should fail")
	}
	if store.Len() != 0 {
		t.Fatalf("canceled upsert mutated the store")
	}
}

func TestSessionNotesCapacityNoEviction(t *testing.T) {
	t.Parallel()
	store := NewNoteStore()
	tool := NewSessionNotesTool(store)
	ctx := context.Background()
	for i := 0; i < maxSessionNotes; i++ {
		res := tool.Execute(ctx, map[string]any{
			"action": "upsert", "id": fmt.Sprintf("n%02d", i), "kind": "next_step", "content": "step",
		})
		if res.IsError {
			t.Fatalf("upsert %d failed: %s", i, res.Output)
		}
	}
	res := tool.Execute(ctx, map[string]any{
		"action": "upsert", "id": "overflow", "kind": "next_step", "content": "one too many",
	})
	if !res.IsError {
		t.Fatalf("65th note should be rejected")
	}
	if store.Len() != maxSessionNotes {
		t.Fatalf("expected %d notes, got %d", maxSessionNotes, store.Len())
	}
	// Updating an existing id is still allowed at the cap.
	res = tool.Execute(ctx, map[string]any{
		"action": "upsert", "id": "n00", "kind": "next_step", "content": "revised",
	})
	if res.IsError {
		t.Fatalf("update at cap should succeed: %s", res.Output)
	}
	if _, ok := store.Get("n00"); !ok {
		t.Fatalf("n00 missing after update")
	}
	if _, ok := store.Get("overflow"); ok {
		t.Fatalf("rejected note must not be stored")
	}
}

func TestSessionNotesSnapshotIndependent(t *testing.T) {
	t.Parallel()
	store := NewNoteStore()
	tool := NewSessionNotesTool(store)
	tool.Execute(context.Background(), map[string]any{
		"action": "upsert", "id": "a", "kind": "decision", "content": "keep",
	})
	snap := store.Snapshot()
	snap[0].Content = "mutated"
	snap[0].Status = SessionNoteStatusSuperseded
	again := store.Snapshot()
	if again[0].Content != "keep" || again[0].Status != SessionNoteStatusActive {
		t.Fatalf("snapshot shares memory with store: %+v", again[0])
	}
}

func TestSessionNotesSupersededRetrievable(t *testing.T) {
	t.Parallel()
	store := NewNoteStore()
	tool := NewSessionNotesTool(store)
	ctx := context.Background()
	tool.Execute(ctx, map[string]any{
		"action": "upsert", "id": "old", "kind": "hypothesis", "content": "was wrong", "status": "superseded",
	})
	note, ok := store.Get("old")
	if !ok || note.Status != SessionNoteStatusSuperseded {
		t.Fatalf("superseded note not retrievable: %+v", note)
	}
	res := tool.Execute(ctx, map[string]any{"action": "list"})
	var list []SessionNote
	if err := json.Unmarshal([]byte(res.Output), &list); err != nil {
		t.Fatalf("list not JSON: %v", err)
	}
	if len(list) != 1 || list[0].Status != SessionNoteStatusSuperseded {
		t.Fatalf("list should include superseded notes: %+v", list)
	}
}

func TestSessionNotesPerStoreIsolation(t *testing.T) {
	t.Parallel()
	a := NewSessionNotesTool(NewNoteStore())
	b := NewSessionNotesTool(NewNoteStore())
	params := map[string]any{
		"action": "upsert", "id": "shared", "kind": "decision", "content": "only in A",
	}
	if res := a.Execute(context.Background(), params); res.IsError {
		t.Fatalf("upsert A: %s", res.Output)
	}
	res := b.Execute(context.Background(), map[string]any{"action": "get", "id": "shared"})
	if !res.IsError {
		t.Fatalf("store B must not see store A notes")
	}
}

func TestSessionNotesRestorePreservesTimestampsAndSkipsInvalid(t *testing.T) {
	t.Parallel()
	store := NewNoteStore()
	ts := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	store.Restore([]SessionNote{
		{ID: "good", Kind: "fact", Content: "real", Evidence: "src/x.go:1", Status: "active", UpdatedAt: ts},
		{ID: "bad-kind", Kind: "nope", Content: "x", Status: "active"},
		{ID: "bad-evidence", Kind: "fact", Content: "x", Status: "active"},
		{ID: "good-2", Kind: "hypothesis", Content: "guess", Status: "superseded", UpdatedAt: ts.Add(time.Hour)},
	})
	snap := store.Snapshot()
	if len(snap) != 2 {
		t.Fatalf("expected 2 valid notes restored, got %d: %+v", len(snap), snap)
	}
	if !snap[0].UpdatedAt.Equal(ts) {
		t.Fatalf("restore regenerated timestamp: %v", snap[0].UpdatedAt)
	}
	if snap[1].Status != SessionNoteStatusSuperseded {
		t.Fatalf("status not preserved: %+v", snap[1])
	}
}

func TestSessionNotesRestoreRespectsCapacity(t *testing.T) {
	t.Parallel()
	store := NewNoteStore()
	input := make([]SessionNote, 0, maxSessionNotes+10)
	for i := 0; i < maxSessionNotes+10; i++ {
		input = append(input, SessionNote{
			ID: fmt.Sprintf("n%03d", i), Kind: "next_step", Content: "x", Status: "active",
		})
	}
	store.Restore(input)
	if store.Len() != maxSessionNotes {
		t.Fatalf("expected cap %d, got %d", maxSessionNotes, store.Len())
	}
}

func TestSessionNotesSchemaShape(t *testing.T) {
	t.Parallel()
	tool := NewSessionNotesTool(NewNoteStore())
	if tool.Name() != "SessionNotes" {
		t.Fatalf("unexpected name %q", tool.Name())
	}
	if tool.Description() == "" {
		t.Fatalf("empty description")
	}
	schema := tool.Schema()
	params := schema.Function.Parameters
	if params["additionalProperties"] != false {
		t.Fatalf("additionalProperties must be false")
	}
	if _, hasRequired := params["required"]; hasRequired {
		t.Fatalf("schema must not declare top-level required keys")
	}
	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("properties not a map")
	}
	for _, key := range []string{"action", "id", "kind", "content", "evidence", "scope", "status"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("missing property %q", key)
		}
	}
	if EffectOf(tool) != EffectState {
		t.Fatalf("SessionNotes must be EffectState, got %v", EffectOf(tool))
	}
	for _, mode := range [][3]bool{{true, false, false}, {false, true, false}, {false, false, true}, {false, false, false}} {
		if !IsAllowedInMode(tool, mode[0], mode[1], mode[2]) {
			t.Fatalf("SessionNotes must be allowed in mode %v", mode)
		}
	}
}
