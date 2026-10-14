package decision

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
)

func TestScoreRelevanceDisabledClient(t *testing.T) {
	if _, err := New("", "m").ScoreRelevance(context.Background(), "q", []string{"a"}); err == nil {
		t.Fatal("expected an error for a disabled client")
	}
}

func TestScoreRelevanceEmptyCandidates(t *testing.T) {
	srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
		t.Error("no request expected for an empty candidate list")
	})
	defer srv.Close()

	scores, err := New(srv.URL, "m").ScoreRelevance(context.Background(), "q", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if scores == nil || len(scores) != 0 {
		t.Fatalf("expected an empty non-nil slice, got %#v", scores)
	}
}

func TestScoreRelevanceBatchesAllCandidatesInOneRequest(t *testing.T) {
	var requests int32
	srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
		atomic.AddInt32(&requests, 1)
		questions := req["questions"].(map[string]any)
		if len(questions) != 3 {
			t.Errorf("expected one question per candidate, got %d", len(questions))
		}
		for i := range 3 {
			q := questions[relevanceQuestionID(i)].(map[string]any)
			if q["type"] != "score" {
				t.Errorf("question %d must use the score primitive, got %v", i, q["type"])
			}
		}
		state, _ := req["state"].(string)
		if !strings.Contains(state, "compaction") || !strings.Contains(state, "CSS") {
			t.Errorf("state must carry the query and every candidate: %q", state)
		}
		w.Write(answersBody(t, map[string]any{
			"relevance0": map[string]any{"type": "score", "score": 4},
			"relevance1": map[string]any{"type": "score", "score": 0},
			"relevance2": map[string]any{"type": "score", "score": 2},
		}))
	})
	defer srv.Close()

	scores, err := New(srv.URL, "m").ScoreRelevance(context.Background(),
		"how does context compaction work",
		[]string{"compaction demotes history", "CSS grid layout", "compaction notes"})
	if err != nil {
		t.Fatal(err)
	}
	if got := atomic.LoadInt32(&requests); got != 1 {
		t.Fatalf("expected a single batched request, got %d", got)
	}
	want := []float64{1, 0, 0.5}
	for i := range want {
		if scores[i] != want[i] {
			t.Fatalf("score[%d] = %v, want %v", i, scores[i], want[i])
		}
	}
}

func TestScoreRelevanceFallsBackToSequentialCalls(t *testing.T) {
	var requests int32
	srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
		if atomic.AddInt32(&requests, 1) == 1 {
			// An endpoint that cannot carry several questions in one payload.
			w.WriteHeader(http.StatusBadRequest)
			w.Write([]byte(`{"error":"only one question per request"}`))
			return
		}
		q := req["questions"].(map[string]any)["decision"].(map[string]any)
		if q["type"] != "score" {
			t.Errorf("sequential fallback must use the score primitive, got %v", q["type"])
		}
		w.Write(answersBody(t, map[string]any{
			"decision": map[string]any{"type": "score", "score": 4},
		}))
	})
	defer srv.Close()

	scores, err := New(srv.URL, "m").ScoreRelevance(context.Background(), "q", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(scores) != 2 || scores[0] != 1 || scores[1] != 1 {
		t.Fatalf("unexpected scores: %v", scores)
	}
	if got := atomic.LoadInt32(&requests); got != 3 {
		t.Fatalf("expected 1 batched + 2 sequential requests, got %d", got)
	}
}

// TestScoreRelevanceFixtureRanking is a proxy measurement, not a claim about
// any real model: the test server plays the decision model and scores each
// candidate by keyword overlap with the query. It exists to prove the plumbing
// (batched request -> parse -> normalize -> rank) puts the relevant candidate
// above the irrelevant ones over a small fixture set; it cannot show how a
// real judge model would behave.
func TestScoreRelevanceFixtureRanking(t *testing.T) {
	fixtures := []struct {
		name     string
		query    string
		relevant string
		others   []string
	}{
		{
			name:     "compaction query",
			query:    "how does context compaction demote history",
			relevant: "context compaction demotes discarded history into the session memory store",
			others: []string{
				"the CSS grid layout uses twelve columns",
				"Ollama keep_alive unloads idle worker models",
			},
		},
		{
			name:     "embeddings query",
			query:    "batched embeddings endpoint",
			relevant: "batched embeddings are sent to the /api/embed endpoint in one request",
			others: []string{
				"the TUI renders a spinner while waiting",
				"commit messages follow conventional commits",
			},
		},
	}

	for _, fx := range fixtures {
		t.Run(fx.name, func(t *testing.T) {
			srv := soServer(t, func(w http.ResponseWriter, req map[string]any) {
				state, _ := req["state"].(string)
				answers := map[string]any{}
				for i, candidate := range fixtureCandidates(state) {
					answers[relevanceQuestionID(i)] = map[string]any{
						"type":  "score",
						"score": fixtureRelevance(fx.query, candidate),
					}
				}
				w.Write(answersBody(t, answers))
			})
			defer srv.Close()

			candidates := append([]string{fx.relevant}, fx.others...)
			scores, err := New(srv.URL, "m").ScoreRelevance(context.Background(), fx.query, candidates)
			if err != nil {
				t.Fatal(err)
			}
			for i := 1; i < len(scores); i++ {
				if scores[0] <= scores[i] {
					t.Fatalf("relevant candidate scored %v, not above candidate %d at %v", scores[0], i, scores[i])
				}
			}
		})
	}
}

// fixtureCandidates reads the numbered candidate list back out of the request
// state so the scripted judge can score the text it was given.
func fixtureCandidates(state string) []string {
	_, after, ok := strings.Cut(state, "Candidates:\n")
	if !ok {
		return nil
	}
	var out []string
	for _, line := range strings.Split(after, "\n") {
		if _, rest, ok := strings.Cut(line, ". "); ok {
			out = append(out, rest)
		}
	}
	return out
}

// fixtureRelevance is the scripted judge: a keyword-overlap score on the
// relevanceScale. It stands in for a real model so the fixture test is
// deterministic and needs no live endpoint.
func fixtureRelevance(query, candidate string) float64 {
	overlap := 0
	candidateLower := strings.ToLower(candidate)
	for _, word := range strings.Fields(strings.ToLower(query)) {
		if len(word) < 4 {
			continue
		}
		if strings.Contains(candidateLower, word) {
			overlap++
		}
	}
	switch {
	case overlap >= 2:
		return float64(len(relevanceScale) - 1)
	case overlap == 1:
		return 2
	default:
		return 0
	}
}
