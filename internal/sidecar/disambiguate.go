package sidecar

import (
	"context"
	"fmt"
	"strings"
)

const disambigNumPredict = 96 // one line "PICK: N"

const disambiguateSystem = "You resolve ambiguous file references for a coding agent. " +
	"You will receive the user's request and a numbered list of absolute " +
	"candidate paths. Reply with ONE line in the exact form `PICK: <number>` " +
	"choosing the candidate that best matches the request. If none clearly " +
	"matches, reply `PICK: 0`. Never output anything else."

// DisambiguatePath asks the sidecar to choose one of the candidate
// absolute paths for the user's intent. The hint is typically the original
// (relative or basename) path the model wrote plus the user goal for the
// turn. Returns ("", false, nil) when the sidecar declines or is disabled,
// in which case the caller should fall back to its default behaviour
// (refuse the rescue).
func (p *Pool) DisambiguatePath(ctx context.Context, hint string, candidates []string) (string, bool, error) {
	if !p.Enabled() {
		return "", false, nil
	}
	if len(candidates) == 0 {
		return "", false, nil
	}
	if len(candidates) == 1 {
		return candidates[0], true, nil
	}

	var b strings.Builder
	for i, c := range candidates {
		fmt.Fprintf(&b, "%d. %s\n", i+1, c)
	}
	user := fmt.Sprintf(
		"User reference: %q\n\nCandidates (absolute paths):\n%sPick one.",
		strings.TrimSpace(hint), b.String(),
	)
	key := cacheKey("disambig", p.cfg.SidecarModel, hint, b.String())
	if cached, ok := p.cache.get(key); ok {
		return cached, true, nil
	}

	v, err, _ := p.sf.Do(key, func() (any, error) {
		return p.chatDisambig(ctx, disambiguateSystem, user)
	})
	if err != nil {
		return "", false, nil
	}
	pick := parsePick(v.(string), len(candidates))
	if pick <= 0 {
		return "", false, nil
	}
	chosen := candidates[pick-1]
	p.cache.put(key, chosen)
	return chosen, true, nil
}

func (p *Pool) chatDisambig(ctx context.Context, system, user string) (string, error) {
	return p.chat(ctx, system, user, disambigNumPredict)
}

// parsePick extracts the integer N from the first occurrence of
// "PICK: N" in the sidecar's reply. Returns 0 on any parse failure or
// out-of-range index, which the caller treats as "decline".
func parsePick(reply string, max int) int {
	low := strings.ToLower(reply)
	idx := strings.Index(low, "pick:")
	if idx < 0 {
		return 0
	}
	rest := strings.TrimSpace(reply[idx+len("pick:"):])
	var n int
	for _, r := range rest {
		if r < '0' || r > '9' {
			break
		}
		n = n*10 + int(r-'0')
	}
	if n < 1 || n > max {
		return 0
	}
	return n
}
