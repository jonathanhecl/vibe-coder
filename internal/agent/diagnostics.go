package agent

import (
	"context"
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
	"github.com/jonathanhecl/vibe-coder/internal/lsp"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// diagnosticsSource is the language-server seam the agent needs after an edit:
// whether a file is this source's business at all, and the verdict if it is.
// The source owns the server processes; the agent only reads what they said.
type diagnosticsSource interface {
	Supports(filePath string) bool
	Diagnostics(ctx context.Context, filePath string) (lsp.Result, error)
}

// SetDiagnostics installs the language-server source behind the automatic
// post-edit diagnostics. A nil source (the default) disables that path, which is
// also what a workspace with no usable language server looks like.
func (a *Agent) SetDiagnostics(src diagnosticsSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.diagnostics = src
}

func (a *Agent) getDiagnostics() diagnosticsSource {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.diagnostics
}

// observeEditDiagnostics records what the language server said about the file
// just edited as a [DIAGNOSTICS] observation, and reports whether it found a
// FRESH error. A red squiggle that arrived in milliseconds is a verdict a test
// suite cannot improve on, so a fresh error is what lets the caller skip the
// auto-test for this edit.
//
// Everything that can go wrong here is silent as far as the model is concerned
// (S10): no server for this file type, a server that crashed, a wait that timed
// out. A language server never degrades a run, and the transcript only ever
// receives an answer that is evidence about this edit.
func (a *Agent) observeEditDiagnostics(ctx context.Context, filePath string) bool {
	path := strings.TrimSpace(filePath)
	src := a.getDiagnostics()
	if src == nil || path == "" || !src.Supports(path) {
		return false
	}
	// A cold server can take seconds to index, and an unannounced pause of that
	// length reads as a hang.
	a.ui.StartWaiting("checking diagnostics…")
	result, err := src.Diagnostics(ctx, path)
	a.ui.StopWaiting()
	if err != nil {
		logger.Infof("Diagnostics unavailable for %s: %v", path, err)
		return false
	}
	// Only a fresh list is evidence about this edit. A stale list still renders
	// (with its caveat) because it is worth reading, but it must not be the
	// reason the test suite is skipped.
	errorsFound := result.Fresh && tools.HasErrors(result.Diagnostics)
	output := tools.RenderDiagnostics(path, result, lsp.SeverityHint, tools.OutputLimitBytes(a.cfg))
	if errorsFound {
		// Say why no suite ran: an unexplained missing AUTO-TEST is exactly the
		// silence that makes a model run it anyway.
		output += "\n(auto-test skipped: clear these errors first)"
	}
	a.ui.ShowToolResult("DIAGNOSTICS", output, errorsFound, nil)
	a.recordToolObservation(ctx, "DIAGNOSTICS", output, "", false)
	return errorsFound
}
