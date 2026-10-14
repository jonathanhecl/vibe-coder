package agent

import (
	"context"
	"sort"
	"strings"
)

// relevanceJudge scores how relevant each candidate is to a query. The decision
// model implements it, so the harness can rerank retrieved context with the
// model it already keeps loaded instead of adding another one. Reranking is
// skipped when the decider cannot judge relevance.
type relevanceJudge interface {
	ScoreRelevance(ctx context.Context, query string, candidates []string) ([]float64, error)
}

// ragCandidateProvider is an optional extension of ragProvider: an engine that
// can return the individual candidate blocks it retrieved, so the harness can
// rerank them. It is optional on purpose, so an engine (or a test stub) that
// only implements ragProvider keeps working.
type ragCandidateProvider interface {
	QueryCandidates(ctx context.Context, query string, k int) ([]string, error)
}

// retrieveContext returns the additive RAG context block for a run. When the
// engine can expose individual candidates and the decider can judge relevance,
// it fetches a wider candidate set and keeps the k most relevant ones instead
// of trusting the engine's own order. It falls back to the engine's ordering
// whenever the judge is unavailable, the engine cannot list candidates, there
// is nothing to choose between, or the judge fails, so retrieval never fails
// the turn and costs at most one judge call.
func (a *Agent) retrieveContext(ctx context.Context, rag ragProvider, query string, k int) (string, error) {
	lister, canList := rag.(ragCandidateProvider)
	judge := a.relevanceJudge()
	if judge == nil || !canList || k <= 0 {
		return rag.QueryText(ctx, query, k)
	}
	items, err := lister.QueryCandidates(ctx, query, k*3)
	if err != nil || len(items) <= k {
		return rag.QueryText(ctx, query, k)
	}
	reranked, ok := rerankCandidates(ctx, query, items, judge, k)
	if !ok {
		return rag.QueryText(ctx, query, k)
	}
	return formatRAGBlock(reranked), nil
}

// rerankCandidates asks the judge to score every candidate and returns the k
// most relevant, in judge order. It reports false when the judge fails or
// returns an unexpected number of scores, so the caller can fall back.
func rerankCandidates(ctx context.Context, query string, items []string, judge relevanceJudge, k int) ([]string, bool) {
	scores, err := judge.ScoreRelevance(ctx, query, items)
	if err != nil || len(scores) != len(items) {
		return nil, false
	}
	selected := make([]string, 0, k)
	for _, index := range rankByScore(scores) {
		if len(selected) == k {
			break
		}
		selected = append(selected, items[index])
	}
	return selected, true
}

// rankByScore returns candidate indices ordered by descending score, keeping
// the original order on ties so retrieval stays deterministic.
func rankByScore(scores []float64) []int {
	order := make([]int, len(scores))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(x, y int) bool { return scores[order[x]] > scores[order[y]] })
	return order
}

// formatRAGBlock renders reranked candidate blocks as the additive context
// block the harness injects, matching the shape the RAG engine produces.
func formatRAGBlock(items []string) string {
	return strings.TrimSpace("[RAG Context]\n" + strings.Join(items, "\n"))
}

// relevanceJudge returns the current decider as a relevance judge when it can
// score candidates, and nil otherwise. The decision model is the only judge
// today; without it retrieval keeps the engine's own ordering.
func (a *Agent) relevanceJudge() relevanceJudge {
	a.mu.RLock()
	defer a.mu.RUnlock()
	if judge, ok := a.decider.(relevanceJudge); ok {
		return judge
	}
	return nil
}
