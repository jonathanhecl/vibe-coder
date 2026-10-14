package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/decision"
)

type fakeJudge struct {
	scores []float64
	err    error
	calls  int
}

func (f *fakeJudge) ScoreRelevance(_ context.Context, _ string, _ []string) ([]float64, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	return f.scores, nil
}

// judgeDecider satisfies decision.Decider through the embedded interface and
// adds relevance scoring, so it stands in for the decision model as the judge.
type judgeDecider struct {
	decision.Decider
	scores []float64
	err    error
}

func (d judgeDecider) ScoreRelevance(_ context.Context, _ string, _ []string) ([]float64, error) {
	if d.err != nil {
		return nil, d.err
	}
	return d.scores, nil
}

type fakeRAG struct {
	text       string
	candidates []string
	queryErr   error
	listErr    error
	textCalls  int
}

func (f *fakeRAG) QueryText(_ context.Context, _ string, _ int) (string, error) {
	f.textCalls++
	return f.text, f.queryErr
}

func (f *fakeRAG) QueryCandidates(_ context.Context, _ string, _ int) ([]string, error) {
	return f.candidates, f.listErr
}

// textOnlyRAG implements ragProvider alone, to prove the candidate extension is
// genuinely optional.
type textOnlyRAG struct{ text string }

func (t textOnlyRAG) QueryText(_ context.Context, _ string, _ int) (string, error) {
	return t.text, nil
}

func TestRerankCandidatesPrefersJudgeTopScore(t *testing.T) {
	items := []string{"a", "b", "c"}
	judge := &fakeJudge{scores: []float64{0.1, 0.9, 0.2}}

	got, ok := rerankCandidates(context.Background(), "q", items, judge, 2)
	if !ok {
		t.Fatalf("expected the rerank to succeed")
	}
	if len(got) != 2 || got[0] != "b" || got[1] != "c" {
		t.Fatalf("expected the two most relevant candidates, got %v", got)
	}
	if judge.calls != 1 {
		t.Fatalf("expected exactly one judge call, got %d", judge.calls)
	}
}

func TestRerankCandidatesFallsBackOnJudgeError(t *testing.T) {
	judge := &fakeJudge{err: errors.New("judge unavailable")}
	if _, ok := rerankCandidates(context.Background(), "q", []string{"a", "b"}, judge, 1); ok {
		t.Fatalf("expected a judge error to report failure so the caller falls back")
	}
}

func TestRerankCandidatesFallsBackOnScoreCountMismatch(t *testing.T) {
	judge := &fakeJudge{scores: []float64{1}}
	if _, ok := rerankCandidates(context.Background(), "q", []string{"a", "b"}, judge, 1); ok {
		t.Fatalf("expected a score count mismatch to report failure")
	}
}

func TestRankByScoreStableOnTies(t *testing.T) {
	order := rankByScore([]float64{0.5, 0.5, 1.0})
	want := []int{2, 0, 1}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("expected order %v, got %v", want, order)
		}
	}
}

func TestFormatRAGBlockShape(t *testing.T) {
	got := formatRAGBlock([]string{"// a:1-1\nA", "// b:2-2\nB"})
	if !strings.HasPrefix(got, "[RAG Context]\n") {
		t.Fatalf("expected the RAG context header, got %q", got)
	}
	if !strings.Contains(got, "// a:1-1\nA") || !strings.Contains(got, "// b:2-2\nB") {
		t.Fatalf("expected both candidate blocks, got %q", got)
	}
}

func TestRetrieveContextFallsBackWithoutJudge(t *testing.T) {
	rag := &fakeRAG{text: "ENGINE", candidates: []string{"x", "y", "z"}}
	ag := &Agent{} // no decider: no judge

	got, err := ag.retrieveContext(context.Background(), rag, "q", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ENGINE" {
		t.Fatalf("expected the engine ordering without a judge, got %q", got)
	}
	if rag.textCalls != 1 {
		t.Fatalf("expected the engine query to be used, got %d calls", rag.textCalls)
	}
}

func TestRetrieveContextFallsBackWhenEngineCannotList(t *testing.T) {
	ag := &Agent{decider: judgeDecider{scores: []float64{1, 0}}}
	got, err := ag.retrieveContext(context.Background(), textOnlyRAG{text: "ENGINE"}, "q", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ENGINE" {
		t.Fatalf("expected the engine ordering when candidates cannot be listed, got %q", got)
	}
}

func TestRetrieveContextReranksWithJudge(t *testing.T) {
	rag := &fakeRAG{
		text:       "ENGINE",
		candidates: []string{"// a:1-1\nA", "// b:2-2\nB", "// c:3-3\nC"},
	}
	ag := &Agent{decider: judgeDecider{scores: []float64{0, 1, 0}}}

	got, err := ag.retrieveContext(context.Background(), rag, "q", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "[RAG Context]\n// b:2-2\nB" {
		t.Fatalf("expected the judge's top candidate, got %q", got)
	}
	if rag.textCalls != 0 {
		t.Fatalf("expected the engine ordering to be skipped, got %d text calls", rag.textCalls)
	}
}

func TestRetrieveContextFallsBackWhenPoolTooSmall(t *testing.T) {
	rag := &fakeRAG{text: "ENGINE", candidates: []string{"x", "y"}}
	ag := &Agent{decider: judgeDecider{scores: []float64{1, 0}}}

	got, err := ag.retrieveContext(context.Background(), rag, "q", 3)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ENGINE" {
		t.Fatalf("expected the engine ordering when there is nothing to choose between, got %q", got)
	}
}

func TestRetrieveContextFallsBackOnJudgeError(t *testing.T) {
	rag := &fakeRAG{text: "ENGINE", candidates: []string{"a", "b", "c"}}
	ag := &Agent{decider: judgeDecider{err: errors.New("judge down")}}

	got, err := ag.retrieveContext(context.Background(), rag, "q", 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "ENGINE" {
		t.Fatalf("expected the engine ordering after a judge error, got %q", got)
	}
}

// TestRerankCandidatesImprovesRelevanceOrdering is a PROXY measurement, not a
// claim about real model quality: the judge is scripted to score by keyword
// overlap, and the fixture is built so the engine order is wrong. It only pins
// that a judge which knows the answer changes the order the harness injects.
func TestRerankCandidatesImprovesRelevanceOrdering(t *testing.T) {
	items := []string{
		"// noise.go:1-2\nunrelated helper",
		"// memory.go:3-4\ncompaction demotes history into the memory store",
		"// misc.go:5-6\nformatting helper",
	}
	judge := &fakeJudge{scores: []float64{0.1, 1.0, 0.2}}

	got, ok := rerankCandidates(context.Background(), "how does compaction demote history", items, judge, 1)
	if !ok {
		t.Fatalf("expected the rerank to succeed")
	}
	if len(got) != 1 || !strings.Contains(got[0], "memory.go") {
		t.Fatalf("expected the relevant candidate first, got %v", got)
	}
}
