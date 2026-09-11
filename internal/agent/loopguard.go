package agent

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// ErrNoProgress marks a run that kept calling the same tool with byte-for-byte
// identical output. While a mission is active the runtime has no turn limit, so
// without this an unproductive loop would spin forever (and burn tokens).
var ErrNoProgress = errors.New("no progress: repeated identical tool call")

// IsNoProgressErr reports whether err is the repeated-tool-call stop signal.
func IsNoProgressErr(err error) bool {
	return err != nil && errors.Is(err, ErrNoProgress)
}

// noProgressLimit is how many consecutive identical (call, output) pairs are
// tolerated before the run stops. Kept comfortably above common legitimate
// repeats such as polling a job whose status output stays "running" for a few
// polls, so real work is not mistaken for a loop.
const noProgressLimit = 8

type noProgressGuard struct {
	mu     sync.Mutex
	call   string
	output string
	streak int
}

// observe records one tool outcome and reports whether the stream of identical
// outcomes has reached the limit. Any change (different call or output) resets
// the streak, so real progress clears it.
func (g *noProgressGuard) observe(callSig, outputSig string) (int, bool) {
	if callSig == "" {
		return 0, false
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if callSig == g.call && outputSig == g.output {
		g.streak++
	} else {
		g.call, g.output, g.streak = callSig, outputSig, 1
	}
	return g.streak, g.streak >= noProgressLimit
}

// toolCallSignature canonicalizes a tool call (name + params) so identical
// repeats compare equal regardless of map ordering. Internal UI-only keys
// (prefixed with "_") are ignored.
func toolCallSignature(name string, params map[string]any) string {
	filtered := make(map[string]any, len(params))
	for k, v := range params {
		if strings.HasPrefix(k, "_") {
			continue
		}
		filtered[k] = v
	}
	raw, err := json.Marshal(filtered)
	if err != nil {
		return name
	}
	return name + "\x00" + string(raw)
}

func toolOutputSignature(result tools.Result) string {
	sum := sha256.Sum256([]byte(result.Output))
	return fmt.Sprintf("%t:%x", result.IsError, sum[:8])
}
