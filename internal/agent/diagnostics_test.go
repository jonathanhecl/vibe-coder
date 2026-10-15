package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/lsp"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// stubDiagnosticsSource stands in for the language-server manager: it decides
// whether it can answer for a file, remembers what it was asked, and returns
// whatever the test told it to.
type stubDiagnosticsSource struct {
	result   lsp.Result
	err      error
	supports bool
	paths    []string
}

func (s *stubDiagnosticsSource) Supports(string) bool { return s.supports }

func (s *stubDiagnosticsSource) Diagnostics(_ context.Context, filePath string) (lsp.Result, error) {
	s.paths = append(s.paths, filePath)
	return s.result, s.err
}

// errorDiagnostic is one compiler error at LSP line 2, character 14, which the
// model-facing rendering prints as line 3, column 15.
func errorDiagnostic() lsp.Diagnostic {
	return lsp.Diagnostic{
		Range:    lsp.Range{Start: lsp.Position{Line: 2, Character: 14}, End: lsp.Position{Line: 2, Character: 20}},
		Severity: lsp.SeverityError,
		Source:   "compiler",
		Message:  "undefined: thing",
	}
}

// editThroughWrite performs one Write of main.go in a workspace that looks like
// a Go module, with a stub language server in place (nil keeps the automatic
// path disabled). The auto-test's shell command is denied so the suite is never
// actually executed: whether it was ATTEMPTED is what these tests read from the
// permission requests.
func editThroughWrite(t *testing.T, source *stubDiagnosticsSource) (*Agent, *policyUI) {
	t.Helper()
	ag := newCoverageAgent(t, fakeClient{}, tui.DecisionAllowOnce, false)
	ui := &policyUI{deny: "Bash"}
	ag.ui = ui
	if err := os.WriteFile(filepath.Join(ag.cfg.Cwd, "go.mod"), []byte("module example.test/fixture\ngo 1.26\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if source != nil {
		ag.SetDiagnostics(source)
	}
	tool := ag.reg.Get("Write")
	if tool == nil {
		t.Fatal("the Write tool is not registered")
	}
	if _, _, err := ag.executeTool(context.Background(), tool, tool.Name(), map[string]any{
		"file_path": filepath.Join(ag.cfg.Cwd, "main.go"),
		"contents":  "package fixture\n",
	}, toolExecutionMode{}); err != nil {
		t.Fatalf("executeTool: %v", err)
	}
	return ag, ui
}

func diagnosticsTranscriptHas(ag *Agent, needle string) bool {
	for _, msg := range ag.sess.Messages() {
		if strings.Contains(msg.Content, needle) {
			return true
		}
	}
	return false
}

// A fresh error is a verdict the test suite cannot improve on: the model is told
// what is wrong and the suite is not run at all.
func TestEditDiagnosticsSkipTheAutoTest(t *testing.T) {
	source := &stubDiagnosticsSource{
		supports: true,
		result:   lsp.Result{Language: "go", Fresh: true, Diagnostics: []lsp.Diagnostic{errorDiagnostic()}},
	}
	ag, ui := editThroughWrite(t, source)

	if got := strings.Join(ui.asked, ","); got != "Write" {
		t.Errorf("permission requests = %q, want only Write: a fresh error must skip the suite", got)
	}
	if !diagnosticsTranscriptHas(ag, "DIAGNOSTICS") {
		t.Error("the diagnostics were not recorded as an observation")
	}
	if !diagnosticsTranscriptHas(ag, "main.go:3:15 error undefined: thing [compiler]") {
		t.Errorf("the transcript does not carry the rendered diagnostic: %v", transcriptOf(ag))
	}
	if !diagnosticsTranscriptHas(ag, "(auto-test skipped") {
		t.Error("a skipped suite was not explained to the model")
	}
	if diagnosticsTranscriptHas(ag, "AUTO-TEST") {
		t.Error("the test suite ran despite a fresh error")
	}
}

// A clean file is what earns the suite.
func TestCleanEditStillRunsTheAutoTest(t *testing.T) {
	source := &stubDiagnosticsSource{
		supports: true,
		result:   lsp.Result{Language: "go", Fresh: true},
	}
	ag, ui := editThroughWrite(t, source)

	if got := strings.Join(ui.asked, ","); got != "Write,Bash" {
		t.Errorf("permission requests = %q, want Write,Bash: a clean file must still be tested", got)
	}
	if !diagnosticsTranscriptHas(ag, "no diagnostics: ") {
		t.Error("the clean result was not reported to the model")
	}
	if !diagnosticsTranscriptHas(ag, "AUTO-TEST") {
		t.Error("the suite was not attempted after a clean edit")
	}
}

// Only a fresh answer is evidence about THIS edit. A list left over from an
// earlier version still renders, with its caveat, but never skips the suite.
func TestStaleDiagnosticsDoNotSkipTheSuite(t *testing.T) {
	source := &stubDiagnosticsSource{
		supports: true,
		result:   lsp.Result{Language: "go", Fresh: false, Diagnostics: []lsp.Diagnostic{errorDiagnostic()}},
	}
	ag, ui := editThroughWrite(t, source)

	if got := strings.Join(ui.asked, ","); got != "Write,Bash" {
		t.Errorf("permission requests = %q, want Write,Bash: a stale list must not skip the suite", got)
	}
	if !diagnosticsTranscriptHas(ag, "may be stale") {
		t.Error("a stale list was presented without its caveat")
	}
}

// A file no server can answer for is not worth a word to the model.
func TestUnsupportedFileIsSilent(t *testing.T) {
	source := &stubDiagnosticsSource{
		supports: false,
		result:   lsp.Result{Language: "go", Fresh: true, Diagnostics: []lsp.Diagnostic{errorDiagnostic()}},
	}
	ag, ui := editThroughWrite(t, source)

	if diagnosticsTranscriptHas(ag, "DIAGNOSTICS") {
		t.Error("an unsupported file produced a diagnostics observation")
	}
	if got := strings.Join(ui.asked, ","); got != "Write,Bash" {
		t.Errorf("permission requests = %q, want the suite to run as before", got)
	}
	if len(source.paths) != 0 {
		t.Errorf("the server was asked about %v, want nothing", source.paths)
	}
}

// A broken language server never degrades the session: the edit is simply
// checked by the suite, and the model hears nothing about the failure.
func TestFailingSourceIsSilent(t *testing.T) {
	source := &stubDiagnosticsSource{
		supports: true,
		err:      errors.New("lsp: gopls: initialize did not answer within 20s"),
	}
	ag, ui := editThroughWrite(t, source)

	if diagnosticsTranscriptHas(ag, "DIAGNOSTICS") {
		t.Error("a failing server produced a diagnostics observation")
	}
	if !diagnosticsTranscriptHas(ag, "AUTO-TEST") {
		t.Error("a failing server stopped the suite from running")
	}
	if got := strings.Join(ui.asked, ","); got != "Write,Bash" {
		t.Errorf("permission requests = %q, want Write,Bash", got)
	}
}

// Without a source the agent behaves exactly as it did before this feature.
func TestWithoutADiagnosticsSourceTheEditIsUnchanged(t *testing.T) {
	ag, ui := editThroughWrite(t, nil)

	if diagnosticsTranscriptHas(ag, "DIAGNOSTICS") {
		t.Error("an observation was recorded with no source configured")
	}
	if got := strings.Join(ui.asked, ","); got != "Write,Bash" {
		t.Errorf("permission requests = %q, want Write,Bash", got)
	}
}

// S11: diagnostics are transcript observations, never part of the prompt that
// has to stay prefix stable for the model cache.
func TestDiagnosticsNeverReachTheSystemPrompt(t *testing.T) {
	source := &stubDiagnosticsSource{
		supports: true,
		result:   lsp.Result{Language: "go", Fresh: true, Diagnostics: []lsp.Diagnostic{errorDiagnostic()}},
	}
	ag, _ := editThroughWrite(t, source)

	if !diagnosticsTranscriptHas(ag, "DIAGNOSTICS") {
		t.Fatal("no diagnostics observation to compare against")
	}
	// The observation's name is allowed to appear in tool guidance — the model
	// cannot act on a report it has never heard of. What must never reach the
	// prompt is the report itself.
	if prompt := ag.buildSystemPrompt(); strings.Contains(prompt, "undefined: thing") || strings.Contains(prompt, "main.go:3:15") {
		t.Error("diagnostics leaked into the system prompt")
	}
}

// The server is asked about the file that was edited, once.
func TestDiagnosticsAskAboutTheEditedFile(t *testing.T) {
	source := &stubDiagnosticsSource{supports: true, result: lsp.Result{Language: "go", Fresh: true}}
	ag, _ := editThroughWrite(t, source)

	want := filepath.Join(ag.cfg.Cwd, "main.go")
	if len(source.paths) != 1 || source.paths[0] != want {
		t.Errorf("asked about %v, want [%s]", source.paths, want)
	}
}

func transcriptOf(ag *Agent) []string {
	messages := ag.sess.Messages()
	out := make([]string, 0, len(messages))
	for _, msg := range messages {
		out = append(out, msg.Content)
	}
	return out
}
