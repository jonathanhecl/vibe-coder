package ollama

// defaultNumCtxCap is the context window assumed when the configuration does
// not name one. Ollama clamps num_ctx to the model's trained context itself,
// so this is only a budget for sizing the request, not a hard limit.
const defaultNumCtxCap = 16384

// NumCtxCap resolves the context-window cap used for worker requests
// (sidecar summaries, compaction): the configured window when set, else a
// sane default.
func NumCtxCap(configured int) int {
	if configured > 0 {
		return configured
	}
	return defaultNumCtxCap
}

// NumCtxFor sizes num_ctx for a chat request whose prompt is promptChars
// bytes long (system + user messages) and which may emit up to numPredict
// tokens. The estimate is chars/3 + numPredict + a 256-token slack, rounded
// up to a 1024 boundary with a 2048 floor, then capped at capTokens when
// capTokens > 0. Without an explicit num_ctx Ollama falls back to its own
// default (often 4096) and silently truncates larger prompts.
func NumCtxFor(promptChars, numPredict, capTokens int) int {
	need := promptChars/3 + numPredict + 256
	need = (need + 1023) / 1024 * 1024
	if need < 2048 {
		need = 2048
	}
	if capTokens > 0 && need > capTokens {
		need = capTokens
	}
	return need
}
