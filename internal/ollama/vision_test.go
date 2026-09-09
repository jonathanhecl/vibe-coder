package ollama

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestModelSupportsVision(t *testing.T) {
	t.Parallel()
	m := Model{Name: "llava", Capabilities: []string{"vision", "completion"}}
	if !m.SupportsVision() {
		t.Fatal("expected vision support")
	}
	if (Model{Name: "x", Capabilities: []string{"completion"}}).SupportsVision() {
		t.Fatal("did not expect vision support")
	}
	if (Model{}).SupportsVision() {
		t.Fatal("did not expect vision support for empty model")
	}
}

func TestLookupVision(t *testing.T) {
	t.Parallel()
	byModel := map[string]bool{"llava:latest": true, "qwen3.5:9b": false}
	for _, tc := range []struct {
		name      string
		available bool
		known     bool
	}{
		{"llava", true, true},
		{"llava:latest", true, true},
		{"LLAVA", true, true},
		{"qwen3.5:9b", false, true},
		{"qwen3.5", false, true},
		{"missing", false, false},
		{"", false, false},
	} {
		available, known := LookupVision(byModel, tc.name)
		if available != tc.available || known != tc.known {
			t.Fatalf("LookupVision(%q) = (%t,%t), want (%t,%t)",
				tc.name, available, known, tc.available, tc.known)
		}
	}
	if _, known := LookupVision(map[string]bool{"m:1": true, "m:2": false}, "m"); known {
		t.Fatal("expected ambiguous base to be unknown")
	}
}

func TestMatchModelToleratesTags(t *testing.T) {
	t.Parallel()
	models := []Model{{Name: "llava:latest"}, {Name: "qwen3.5:9b"}}
	if m := MatchModel(models, "llava"); m == nil || m.Name != "llava:latest" {
		t.Fatalf("expected llava match, got %+v", m)
	}
	if m := MatchModel(models, "QWEN3.5"); m == nil || m.Name != "qwen3.5:9b" {
		t.Fatalf("expected qwen match, got %+v", m)
	}
	if m := MatchModel(models, "missing"); m != nil {
		t.Fatalf("expected nil, got %+v", m)
	}
}

func TestMessageSerializesImagesWhenPresent(t *testing.T) {
	t.Parallel()
	with := Message{Role: "user", Content: "see this", Images: []string{"aGVsbG8="}}
	raw, err := json.Marshal(with)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"images":["aGVsbG8="]`) {
		t.Fatalf("expected images field, got %s", raw)
	}
	without := Message{Role: "user", Content: "plain"}
	raw, err = json.Marshal(without)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "images") {
		t.Fatalf("expected no images field, got %s", raw)
	}
}
