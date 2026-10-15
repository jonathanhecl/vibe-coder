package tools

import (
	"context"
	"fmt"
	"io/fs"
	"strconv"
	"strings"
	"testing"

	"github.com/jonathanhecl/vibe-coder/internal/lsp"
)

// stubDiagnostics answers with a canned result and remembers what it was asked,
// so rendering is tested without a language server.
type stubDiagnostics struct {
	result lsp.Result
	err    error

	calls []string
}

func (s *stubDiagnostics) Diagnostics(ctx context.Context, filePath string) (lsp.Result, error) {
	s.calls = append(s.calls, filePath)
	if err := ctx.Err(); err != nil {
		return lsp.Result{}, err
	}
	return s.result, s.err
}

func severityDiagnostic(severity, line, column int, message string) lsp.Diagnostic {
	return lsp.Diagnostic{
		Range: lsp.Range{
			Start: lsp.Position{Line: line, Character: column},
			End:   lsp.Position{Line: line, Character: column + 1},
		},
		Severity: severity,
		Source:   "gopls",
		Message:  message,
	}
}

func runDiagnostics(t *testing.T, tool *DiagnosticsTool, params map[string]any) Result {
	t.Helper()
	return tool.Execute(context.Background(), params)
}

func TestDiagnosticsToolRejectsAnEmptyPath(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
	}{
		{name: "absent", params: map[string]any{}},
		{name: "empty", params: map[string]any{"file_path": ""}},
		{name: "blank", params: map[string]any{"file_path": "   "}},
		{name: "not a string", params: map[string]any{"file_path": 42}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			source := &stubDiagnostics{}
			result := runDiagnostics(t, NewDiagnosticsTool(source), tc.params)
			if !result.IsError {
				t.Errorf("IsError = false, want true")
			}
			if result.Output != "file_path is required" {
				t.Errorf("Output = %q", result.Output)
			}
			if len(source.calls) != 0 {
				t.Errorf("the source was asked for %v, want nothing", source.calls)
			}
		})
	}
}

func TestDiagnosticsToolRejectsAnUnknownSeverity(t *testing.T) {
	source := &stubDiagnostics{}
	result := runDiagnostics(t, NewDiagnosticsTool(source), map[string]any{
		"file_path": "main.go",
		"severity":  "fatal",
	})
	if !result.IsError {
		t.Fatalf("IsError = false, want true")
	}
	if !strings.Contains(result.Output, `unknown severity "fatal"`) {
		t.Errorf("Output = %q, want it to name the bad value", result.Output)
	}
	for _, want := range []string{"error", "warning", "info", "hint", "all"} {
		if !strings.Contains(result.Output, want) {
			t.Errorf("Output = %q, want it to list %q", result.Output, want)
		}
	}
	if len(source.calls) != 0 {
		t.Errorf("the source was asked for %v, want nothing", source.calls)
	}
}

func TestDiagnosticsToolReportsACleanFileInOneLine(t *testing.T) {
	source := &stubDiagnostics{result: lsp.Result{Language: "go", Fresh: true}}
	result := runDiagnostics(t, NewDiagnosticsTool(source), map[string]any{"file_path": "main.go"})
	if result.IsError {
		t.Fatalf("IsError = true: %s", result.Output)
	}
	if want := "no diagnostics: main.go (go)"; result.Output != want {
		t.Errorf("Output = %q, want %q", result.Output, want)
	}
}

func TestDiagnosticsToolRendersOneLinePerDiagnosticSeverityFirst(t *testing.T) {
	source := &stubDiagnostics{result: lsp.Result{
		Language: "go",
		Fresh:    true,
		Diagnostics: []lsp.Diagnostic{
			severityDiagnostic(lsp.SeverityWarning, 9, 4, "declared and not used: unused"),
			severityDiagnostic(lsp.SeverityError, 30, 0, "too many arguments in call to do"),
			severityDiagnostic(lsp.SeverityHint, 0, 0, "consider simplifying this expression"),
			severityDiagnostic(lsp.SeverityError, 2, 11, "undefined: helper"),
			severityDiagnostic(lsp.SeverityInformation, 5, 1, "this value is never used"),
		},
	}}
	result := runDiagnostics(t, NewDiagnosticsTool(source), map[string]any{"file_path": "main.go"})
	want := strings.Join([]string{
		"main.go:3:12 error undefined: helper [gopls]",
		"main.go:31:1 error too many arguments in call to do [gopls]",
		"main.go:10:5 warning declared and not used: unused [gopls]",
		"main.go:6:2 info this value is never used [gopls]",
		"main.go:1:1 hint consider simplifying this expression [gopls]",
	}, "\n")
	if result.Output != want {
		t.Errorf("Output =\n%s\nwant\n%s", result.Output, want)
	}
}

func TestDiagnosticsToolFiltersByMinimumSeverity(t *testing.T) {
	diagnostics := []lsp.Diagnostic{
		severityDiagnostic(lsp.SeverityError, 0, 0, "an error"),
		severityDiagnostic(lsp.SeverityWarning, 1, 0, "a warning"),
		severityDiagnostic(lsp.SeverityInformation, 2, 0, "an info"),
		severityDiagnostic(lsp.SeverityHint, 3, 0, "a hint"),
	}
	cases := []struct {
		name     string
		severity any
		want     []string
	}{
		{name: "default is everything", want: []string{"error", "warning", "info", "hint"}},
		{name: "all is everything", severity: "all", want: []string{"error", "warning", "info", "hint"}},
		{name: "error means errors", severity: "error", want: []string{"error"}},
		{name: "warning means warnings and worse", severity: "warning", want: []string{"error", "warning"}},
		{name: "info means info and worse", severity: "info", want: []string{"error", "warning", "info"}},
		{name: "hint means everything", severity: "hint", want: []string{"error", "warning", "info", "hint"}},
		{name: "case and spaces are forgiven", severity: "  WARNING ", want: []string{"error", "warning"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"file_path": "main.go"}
			if tc.severity != nil {
				params["severity"] = tc.severity
			}
			source := &stubDiagnostics{result: lsp.Result{Language: "go", Fresh: true, Diagnostics: diagnostics}}
			result := runDiagnostics(t, NewDiagnosticsTool(source), params)
			if result.IsError {
				t.Fatalf("IsError = true: %s", result.Output)
			}
			lines := strings.Split(result.Output, "\n")
			if len(lines) != len(tc.want) {
				t.Fatalf("Output =\n%s\nwant %d line(s)", result.Output, len(tc.want))
			}
			for i, label := range tc.want {
				if !strings.Contains(lines[i], " "+label+" ") {
					t.Errorf("line %d = %q, want a %s diagnostic", i+1, lines[i], label)
				}
			}
		})
	}
}

func TestDiagnosticsToolTreatsAMissingSeverityAsAnError(t *testing.T) {
	// A server may omit severity, which decodes as zero. Hiding such a report
	// from an errors-only query would be the wrong way to be unsure.
	source := &stubDiagnostics{result: lsp.Result{
		Language:    "go",
		Fresh:       true,
		Diagnostics: []lsp.Diagnostic{severityDiagnostic(0, 4, 2, "something is wrong")},
	}}
	result := runDiagnostics(t, NewDiagnosticsTool(source), map[string]any{
		"file_path": "main.go",
		"severity":  "error",
	})
	if want := "main.go:5:3 error something is wrong [gopls]"; result.Output != want {
		t.Errorf("Output = %q, want %q", result.Output, want)
	}
}

func TestDiagnosticsToolCollapsesAMessageToOneLine(t *testing.T) {
	source := &stubDiagnostics{result: lsp.Result{
		Language: "go",
		Fresh:    true,
		Diagnostics: []lsp.Diagnostic{
			severityDiagnostic(lsp.SeverityError, 0, 0, "undefined: helper\n\n\t5 |\thelper()\n\t  |\t^^^^^^"),
		},
	}}
	result := runDiagnostics(t, NewDiagnosticsTool(source), map[string]any{"file_path": "main.go"})
	want := "main.go:1:1 error undefined: helper 5 | helper() | ^^^^^^ [gopls]"
	if result.Output != want {
		t.Errorf("Output = %q, want %q", result.Output, want)
	}
}

func TestDiagnosticsToolOmitsTheSourceWhenTheServerSendsNone(t *testing.T) {
	diagnostic := severityDiagnostic(lsp.SeverityError, 0, 0, "undefined: helper")
	diagnostic.Source = ""
	source := &stubDiagnostics{result: lsp.Result{Language: "go", Fresh: true, Diagnostics: []lsp.Diagnostic{diagnostic}}}
	result := runDiagnostics(t, NewDiagnosticsTool(source), map[string]any{"file_path": "main.go"})
	if want := "main.go:1:1 error undefined: helper"; result.Output != want {
		t.Errorf("Output = %q, want %q", result.Output, want)
	}
}

func TestDiagnosticsToolEchoesThePathItWasGiven(t *testing.T) {
	source := &stubDiagnostics{result: lsp.Result{Language: "go", Fresh: true}}
	tool := NewDiagnosticsTool(source)
	result := runDiagnostics(t, tool, map[string]any{"file_path": "  internal/lsp/manager.go  "})
	if want := "no diagnostics: internal/lsp/manager.go (go)"; result.Output != want {
		t.Errorf("Output = %q, want %q", result.Output, want)
	}
	if len(source.calls) != 1 || source.calls[0] != "internal/lsp/manager.go" {
		t.Errorf("source was asked for %v, want the trimmed path", source.calls)
	}
}

func TestDiagnosticsToolDoesNotCallAStaleAnswerClean(t *testing.T) {
	source := &stubDiagnostics{result: lsp.Result{Language: "go", Fresh: false}}
	result := runDiagnostics(t, NewDiagnosticsTool(source), map[string]any{"file_path": "main.go"})
	want := "no diagnostics reported for main.go (go): the language server did not answer for the version just sent"
	if result.Output != want {
		t.Errorf("Output = %q, want %q", result.Output, want)
	}
	if result.IsError {
		t.Errorf("IsError = true: a slow server is not a tool failure")
	}
}

func TestDiagnosticsToolMarksAStaleList(t *testing.T) {
	source := &stubDiagnostics{result: lsp.Result{
		Language:    "go",
		Fresh:       false,
		Diagnostics: []lsp.Diagnostic{severityDiagnostic(lsp.SeverityError, 0, 0, "undefined: helper")},
	}}
	result := runDiagnostics(t, NewDiagnosticsTool(source), map[string]any{"file_path": "main.go"})
	lines := strings.Split(result.Output, "\n")
	if len(lines) != 2 {
		t.Fatalf("Output =\n%s\nwant the diagnostic plus one caveat line", result.Output)
	}
	if lines[0] != "main.go:1:1 error undefined: helper [gopls]" {
		t.Errorf("line 1 = %q", lines[0])
	}
	if !strings.Contains(lines[1], "may be stale") {
		t.Errorf("line 2 = %q, want a staleness caveat", lines[1])
	}
}

func TestDiagnosticsToolReportsAMissingServerWithoutBlamingTheTool(t *testing.T) {
	for _, tc := range []struct {
		name string
		tool *DiagnosticsTool
	}{
		{name: "the source says so", tool: NewDiagnosticsTool(&stubDiagnostics{err: lsp.ErrNoServer})},
		{name: "there is no source", tool: NewDiagnosticsTool(nil)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runDiagnostics(t, tc.tool, map[string]any{"file_path": "main.go"})
			if result.IsError {
				t.Errorf("IsError = true: a missing server is not a failure to report")
			}
			if want := "no language server available for main.go"; result.Output != want {
				t.Errorf("Output = %q, want %q", result.Output, want)
			}
			if strings.TrimSpace(result.HintsForModel) == "" {
				t.Errorf("HintsForModel is empty, want guidance to fall back on the tests")
			}
		})
	}
}

func TestDiagnosticsToolReportsAFailure(t *testing.T) {
	source := &stubDiagnostics{err: fmt.Errorf("read file: %w", fs.ErrNotExist)}
	result := runDiagnostics(t, NewDiagnosticsTool(source), map[string]any{"file_path": "main.go"})
	if !result.IsError {
		t.Fatalf("IsError = false, want true")
	}
	if !strings.Contains(result.Output, "diagnostics failed for main.go") {
		t.Errorf("Output = %q", result.Output)
	}
	if !strings.Contains(result.Output, fs.ErrNotExist.Error()) {
		t.Errorf("Output = %q, want the cause in it", result.Output)
	}
}

func TestDiagnosticsToolOmitsDiagnosticsPastTheLimit(t *testing.T) {
	const limit = 200
	diagnostics := make([]lsp.Diagnostic, 0, 20)
	for i := 0; i < 20; i++ {
		diagnostics = append(diagnostics, severityDiagnostic(lsp.SeverityError, i, 0, fmt.Sprintf("undefined: symbol%02d", i)))
	}
	source := &stubDiagnostics{result: lsp.Result{Language: "go", Fresh: true, Diagnostics: diagnostics}}
	tool := NewDiagnosticsTool(source)
	tool.SetMaxOutputBytes(limit)
	result := runDiagnostics(t, tool, map[string]any{"file_path": "main.go"})

	lines := strings.Split(result.Output, "\n")
	footer := lines[len(lines)-1]
	if !strings.Contains(footer, "more diagnostics omitted") {
		t.Fatalf("Output =\n%s\nwant a truncation footer", result.Output)
	}
	omitted, err := strconv.Atoi(strings.Fields(footer)[1])
	if err != nil {
		t.Fatalf("footer = %q, want a count", footer)
	}
	shown := len(lines) - 1
	if shown < 1 {
		t.Fatalf("Output =\n%s\nwant at least one diagnostic", result.Output)
	}
	if shown+omitted != len(diagnostics) {
		t.Errorf("shown %d + omitted %d != %d", shown, omitted, len(diagnostics))
	}
	if body := strings.TrimRight(result.Output[:strings.LastIndex(result.Output, footer)], "\n"); len(body) > limit {
		t.Errorf("the shown lines are %d bytes, over the %d limit", len(body), limit)
	}
	for _, line := range lines[:shown] {
		if !strings.HasSuffix(line, "[gopls]") {
			t.Errorf("line %q was cut in half", line)
		}
	}
}

func TestDiagnosticsToolStillShowsOneOversizedDiagnostic(t *testing.T) {
	source := &stubDiagnostics{result: lsp.Result{
		Language:    "go",
		Fresh:       true,
		Diagnostics: []lsp.Diagnostic{severityDiagnostic(lsp.SeverityError, 0, 0, strings.Repeat("long: ", 60))},
	}}
	tool := NewDiagnosticsTool(source)
	tool.SetMaxOutputBytes(64)
	result := runDiagnostics(t, tool, map[string]any{"file_path": "main.go"})
	if strings.Contains(result.Output, "\n") {
		t.Fatalf("Output = %q, want a single clipped line", result.Output)
	}
	if !strings.HasSuffix(result.Output, "...") {
		t.Errorf("Output = %q, want it clipped", result.Output)
	}
	if len(result.Output) > 64 {
		t.Errorf("Output is %d bytes, over the 64 limit", len(result.Output))
	}
	if !strings.HasPrefix(result.Output, "main.go:1:1 error") {
		t.Errorf("Output = %q, want the position kept", result.Output)
	}
}

func TestDiagnosticsToolIsAReadTool(t *testing.T) {
	tool := NewDiagnosticsTool(&stubDiagnostics{})
	if effect := EffectOf(tool); effect != EffectRead {
		t.Errorf("EffectOf = %v, want EffectRead so it runs in plan and review mode", effect)
	}
	if tool.Name() != "Diagnostics" {
		t.Errorf("Name = %q", tool.Name())
	}
	schema := tool.Schema()
	required, _ := schema.Function.Parameters["required"].([]string)
	if len(required) != 1 || required[0] != "file_path" {
		t.Errorf("required = %v, want [file_path]", required)
	}
	properties, _ := schema.Function.Parameters["properties"].(map[string]any)
	if _, ok := properties["severity"]; !ok {
		t.Errorf("properties = %v, want a severity parameter", properties)
	}
}

func TestDiagnosticsToolTakesTheSharedOutputCap(t *testing.T) {
	reg := NewRegistry()
	tool := NewDiagnosticsTool(&stubDiagnostics{})
	reg.Register(tool)
	reg.SetOutputLimit(4096)
	if tool.maxOutput != 4096 {
		t.Errorf("maxOutput = %d, want the shared cap applied", tool.maxOutput)
	}
}

func TestDiagnosticsToolIsNotARegistryDefault(t *testing.T) {
	// The tool needs a live source, so the agent registers it once it knows
	// which language servers this workspace has; a default registration would
	// hand the model a tool that can only answer "no server".
	reg := NewRegistry()
	reg.RegisterDefaults()
	if reg.Get("Diagnostics") != nil {
		t.Error("Diagnostics is registered by RegisterDefaults, want it registered with a source")
	}
}

func TestHasErrorsCountsOnlyErrorsAndUnknownSeverities(t *testing.T) {
	cases := []struct {
		name        string
		diagnostics []lsp.Diagnostic
		want        bool
	}{
		{name: "no diagnostics", diagnostics: nil, want: false},
		{name: "empty list", diagnostics: []lsp.Diagnostic{}, want: false},
		{name: "an error", diagnostics: []lsp.Diagnostic{severityDiagnostic(lsp.SeverityError, 0, 0, "boom")}, want: true},
		{name: "a warning only", diagnostics: []lsp.Diagnostic{severityDiagnostic(lsp.SeverityWarning, 0, 0, "careful")}, want: false},
		{name: "information only", diagnostics: []lsp.Diagnostic{severityDiagnostic(lsp.SeverityInformation, 0, 0, "fyi")}, want: false},
		{name: "a hint only", diagnostics: []lsp.Diagnostic{severityDiagnostic(lsp.SeverityHint, 0, 0, "maybe")}, want: false},
		{name: "one error among warnings", diagnostics: []lsp.Diagnostic{
			severityDiagnostic(lsp.SeverityWarning, 0, 0, "careful"),
			severityDiagnostic(lsp.SeverityError, 1, 0, "boom"),
		}, want: true},
		{
			// A server that omits severity is still reporting something it wants
			// fixed, and the renderer shows it as an error.
			name:        "an omitted severity",
			diagnostics: []lsp.Diagnostic{severityDiagnostic(0, 0, 0, "unlabelled")},
			want:        true,
		},
		{
			name:        "an out of range severity",
			diagnostics: []lsp.Diagnostic{severityDiagnostic(9, 0, 0, "unlabelled")},
			want:        true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := HasErrors(tc.diagnostics); got != tc.want {
				t.Errorf("HasErrors = %v, want %v", got, tc.want)
			}
		})
	}
}
