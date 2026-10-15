package tools

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jonathanhecl/vibe-coder/internal/lsp"
)

// defaultDiagnosticsMaxBytes caps the model-facing diagnostic list when no
// shared limit is set. Compiler output is repetitive and the first screenful
// carries the actionable errors.
const defaultDiagnosticsMaxBytes = 8 * 1024

// DiagnosticsSource reports the diagnostics a language server published for one
// file. The agent owns the server processes and their lifetime; this tool only
// reads what they said, which is what keeps it read-only and cheap.
type DiagnosticsSource interface {
	Diagnostics(ctx context.Context, filePath string) (lsp.Result, error)
}

// DiagnosticsTool surfaces compiler and linter diagnostics for a single file,
// as the file is on disk. It is a read tool: it works in plan and review mode
// and never asks for permission.
type DiagnosticsTool struct {
	source DiagnosticsSource
	// maxOutput caps the model-facing output; set via Registry.SetOutputLimit.
	maxOutput int
}

func NewDiagnosticsTool(source DiagnosticsSource) *DiagnosticsTool {
	return &DiagnosticsTool{source: source}
}

// SetMaxOutputBytes implements outputLimiter for the shared tool cap.
func (t *DiagnosticsTool) SetMaxOutputBytes(n int) { t.maxOutput = n }

func (t *DiagnosticsTool) Name() string { return "Diagnostics" }
func (t *DiagnosticsTool) Description() string {
	return "Report the language server's diagnostics (errors, warnings) for one file as it is on disk. Read-only, and far cheaper than running the test suite to find a typo."
}
func (t *DiagnosticsTool) Schema() Schema {
	return Schema{
		Type: "function",
		Function: FunctionSchema{
			Name:        t.Name(),
			Description: t.Description(),
			Parameters: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"file_path": map[string]any{"type": "string"},
					"severity": map[string]any{
						"type":        "string",
						"enum":        []string{"error", "warning", "info", "hint", "all"},
						"description": "Minimum severity to report. Defaults to all.",
					},
				},
				"required": []string{"file_path"},
			},
		},
	}
}

func (t *DiagnosticsTool) Execute(ctx context.Context, params map[string]any) Result {
	path, _ := params["file_path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return errResult("file_path is required")
	}
	minSeverity, err := severityFilter(params["severity"])
	if err != nil {
		return errResult(err.Error())
	}
	if t.source == nil {
		return noServerResult(path)
	}
	result, err := t.source.Diagnostics(ctx, path)
	if err != nil {
		// A missing language server is not a failure the model can act on, and
		// reporting it as an error would teach it to distrust a working tool.
		if errors.Is(err, lsp.ErrNoServer) {
			return noServerResult(path)
		}
		return errResult(fmt.Sprintf("diagnostics failed for %s: %v", path, err))
	}
	return t.render(path, minSeverity, result)
}

// render formats what the server said, severity first, one line per diagnostic.
// The path is echoed as the model passed it: a relative path stays short, and
// it is the string the model will use in its next edit.
func (t *DiagnosticsTool) render(path string, minSeverity int, result lsp.Result) Result {
	items := make([]lsp.Diagnostic, 0, len(result.Diagnostics))
	for _, d := range result.Diagnostics {
		if severityFloor(d.Severity) > minSeverity {
			continue
		}
		items = append(items, d)
	}
	sort.SliceStable(items, func(i, j int) bool { return lessDiagnostic(items[i], items[j]) })

	if len(items) == 0 {
		// A clean file and an unanswered wait are different answers, and the
		// model must never read the second as the first: swallowing a timeout
		// as "no diagnostics" is how a broken server silently approves edits.
		if result.Fresh {
			return Result{Output: fmt.Sprintf("no diagnostics: %s (%s)", path, result.Language)}
		}
		return Result{Output: fmt.Sprintf("no diagnostics reported for %s (%s): the language server did not answer for the version just sent", path, result.Language)}
	}

	limit := t.maxOutput
	if limit <= 0 {
		limit = defaultDiagnosticsMaxBytes
	}
	var b strings.Builder
	shown := 0
	for _, d := range items {
		line := diagnosticLine(path, d)
		if shown > 0 && b.Len()+len(line)+1 > limit {
			break
		}
		if shown == 0 && len(line) > limit {
			line = clipRunes(line, limit)
		}
		b.WriteString(line)
		b.WriteString("\n")
		shown++
	}
	if omitted := len(items) - shown; omitted > 0 {
		fmt.Fprintf(&b, "... %d more diagnostics omitted\n", omitted)
	}
	if !result.Fresh {
		b.WriteString("(the language server did not answer for the version just sent; this list may be stale)\n")
	}
	return Result{Output: strings.TrimRight(b.String(), "\n")}
}

// diagnosticLine renders one diagnostic the way a compiler does. LSP positions
// are zero-based and every editor prints them one-based, so the model sees the
// same coordinates the user sees.
func diagnosticLine(path string, d lsp.Diagnostic) string {
	line := fmt.Sprintf("%s:%d:%d %s %s",
		path,
		d.Range.Start.Line+1,
		d.Range.Start.Character+1,
		severityLabel(d.Severity),
		oneLine(d.Message),
	)
	if d.Source != "" {
		line += " [" + d.Source + "]"
	}
	return line
}

// oneLine collapses a message to a single line. Servers routinely send
// multi-line messages with a code frame in them, and one diagnostic has to stay
// one line to keep the payload scannable.
func oneLine(message string) string {
	return strings.Join(strings.Fields(message), " ")
}

func severityLabel(severity int) string {
	return lsp.Diagnostic{Severity: severityFloor(severity)}.Label()
}

// severityFilter turns the optional parameter into the lowest severity to
// report, so "warning" means warnings and worse rather than warnings only.
func severityFilter(raw any) (int, error) {
	value, _ := raw.(string)
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", "all":
		return lsp.SeverityHint, nil
	case "error":
		return lsp.SeverityError, nil
	case "warning":
		return lsp.SeverityWarning, nil
	case "info", "information":
		return lsp.SeverityInformation, nil
	case "hint":
		return lsp.SeverityHint, nil
	default:
		return 0, fmt.Errorf("unknown severity %q: use error, warning, info, hint or all", value)
	}
}

// severityFloor maps an absent or out-of-range severity to error level: a
// server that omits severity is still reporting something worth showing, and
// hiding it from an errors-only query would be the wrong way to be unsure.
func severityFloor(severity int) int {
	if severity < lsp.SeverityError || severity > lsp.SeverityHint {
		return lsp.SeverityError
	}
	return severity
}

// lessDiagnostic orders by severity, then position, then message, so the same
// file always renders the same way: the model reads errors before style notes,
// and a test never depends on the order a server happened to publish.
func lessDiagnostic(a, b lsp.Diagnostic) bool {
	left, right := severityFloor(a.Severity), severityFloor(b.Severity)
	if left != right {
		return left < right
	}
	if a.Range.Start.Line != b.Range.Start.Line {
		return a.Range.Start.Line < b.Range.Start.Line
	}
	if a.Range.Start.Character != b.Range.Start.Character {
		return a.Range.Start.Character < b.Range.Start.Character
	}
	return a.Message < b.Message
}

// clipRunes shortens a line to a byte budget without splitting a rune: a
// diagnostic message can carry non-ASCII text and half a rune is corruption.
func clipRunes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		size := utf8.RuneLen(r)
		if used+size > maxBytes-3 {
			break
		}
		b.WriteRune(r)
		used += size
	}
	return b.String() + "..."
}

func noServerResult(path string) Result {
	return Result{
		Output:        fmt.Sprintf("no language server available for %s", path),
		HintsForModel: "No language server is installed for this file type, so there are no diagnostics to read. Run the project's tests to check your change.",
	}
}
