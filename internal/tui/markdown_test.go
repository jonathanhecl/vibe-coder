package tui

import (
	"bytes"
	"strings"
	"testing"
)

// plainStyle returns a Style with ANSI disabled so tests can compare
// transformed text without escape-sequence noise.
func plainStyle() Style { return Style{} }

func render(t *testing.T, chunks ...string) string {
	t.Helper()
	r := NewMarkdownRenderer(plainStyle())
	var buf bytes.Buffer
	for _, c := range chunks {
		r.Write(&buf, c)
	}
	r.Flush(&buf)
	return buf.String()
}

func TestRenderHeadingsBulletsAndRule(t *testing.T) {
	got := render(t,
		"# Title\n",
		"## Sub\n",
		"### Tiny\n",
		"- one\n",
		"- two\n",
		"1. first\n",
		"2) second\n",
		"---\n",
	)
	wantLines := []string{
		"# Title",
		"## Sub",
		"### Tiny",
		"• one",
		"• two",
		"1. first",
		"2) second",
		strings.Repeat("─", 48),
	}
	for _, w := range wantLines {
		if !strings.Contains(got, w) {
			t.Fatalf("expected %q in output, got:\n%s", w, got)
		}
	}
}

func TestRenderFencedCodeBlock(t *testing.T) {
	got := render(t,
		"before\n",
		"```go\n",
		"func main() {}\n",
		"```\n",
		"after\n",
	)
	// Open fence header carries the language; close fence prints `└─`;
	// the body line is prefixed with the bar so it visually sits inside
	// a contiguous code box even when colors are disabled.
	for _, w := range []string{"┌─ go", "💭 func main() {}", "└─", "before", "after"} {
		if !strings.Contains(got, w) {
			t.Fatalf("expected %q in:\n%s", w, got)
		}
	}
}

func TestRenderInlineMarksDoNotApplyInsideCode(t *testing.T) {
	got := render(t,
		"```\n",
		"**not bold** and `not code`\n",
		"```\n",
	)
	// Inside the fence the inline marks must survive verbatim — the
	// renderer should treat the whole line as code, not markdown.
	if !strings.Contains(got, "**not bold** and `not code`") {
		t.Fatalf("inline marks should not be processed inside code block:\n%s", got)
	}
}

func TestRenderInlineMarksPreservedWhenColorsDisabled(t *testing.T) {
	// Contract: when ANSI is disabled the renderer keeps inline source
	// markers verbatim so a piped/redirected transcript stays readable
	// (the alternative — stripping `, *, [], () — would leave the user
	// with mangled prose without any visual replacement). Block-level
	// transforms (bullets, fences) still apply since they add structure
	// that is useful even in plain text.
	got := render(t, "see [docs](https://x/y) and `code` plus **bold**\n")
	for _, want := range []string{"`code`", "**bold**", "[docs](https://x/y)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("expected %q untouched in plain-text render, got:\n%s", want, got)
		}
	}
}

func TestRenderInlineLinkRewriteWhenColorsEnabled(t *testing.T) {
	r := NewMarkdownRenderer(Style{enabled: true})
	var buf bytes.Buffer
	r.Write(&buf, "see [docs](https://x/y)\n")
	r.Flush(&buf)
	if !strings.Contains(buf.String(), "https://x/y") {
		t.Fatalf("expected URL surfaced beside label when colored, got:\n%s", buf.String())
	}
}

func TestRenderStreamingSplitsAcrossChunks(t *testing.T) {
	// A real stream often slices a single line in arbitrary places. The
	// renderer must hold back the trailing partial line until a newline
	// is seen, otherwise headings/bullets would be misclassified.
	got := render(t, "# Hea", "ding\nrest\n")
	if !strings.Contains(got, "# Heading") {
		t.Fatalf("expected merged heading, got:\n%s", got)
	}
	if strings.Contains(got, "Hea\n") {
		t.Fatalf("partial line should not have been flushed mid-token:\n%s", got)
	}
}

func TestRenderFlushEmitsTrailingPartialLine(t *testing.T) {
	// Partial lines (no trailing newline) are intentionally buffered
	// until either a newline arrives or Flush is called. Streaming them
	// in place would require redrawing with `\r\033[2K`, which only
	// clears the current visual row and leaves "ghost" copies of any
	// wrapped prefix when the line is wider than the terminal.
	r := NewMarkdownRenderer(plainStyle())
	var buf bytes.Buffer
	r.Write(&buf, "no trailing newline here")
	if buf.Len() != 0 {
		t.Fatalf("partial line should be buffered, got: %q", buf.String())
	}
	r.Flush(&buf)
	out := buf.String()
	if out != "no trailing newline here\n" {
		t.Fatalf("expected single flushed line with trailing newline, got: %q", out)
	}
}

func TestRenderBlockquoteAndStripCR(t *testing.T) {
	// Some streams (Windows tools, copy-pasted prompts) include CRLF.
	// The renderer should strip the CR before classifying the line so
	// "> quote" is not mis-detected as plain text containing "\r".
	got := render(t, "> hello world\r\n")
	if !strings.Contains(got, "💭 hello world") {
		t.Fatalf("blockquote not rendered, got:\n%s", got)
	}
}

func TestRenderInlineBoldItalicWithColors(t *testing.T) {
	// With colors *enabled* we can assert the right ANSI sequences are
	// emitted so a regression that swaps Bold↔Italic shows up in CI.
	r := NewMarkdownRenderer(Style{enabled: true})
	var buf bytes.Buffer
	r.Write(&buf, "**bold** and _italic_ and *also*\n")
	r.Flush(&buf)
	out := buf.String()
	if !strings.Contains(out, "\x1b[1mbold\x1b[0m") {
		t.Fatalf("bold not wrapped with ANSI bold: %q", out)
	}
	if !strings.Contains(out, "\x1b[3mitalic\x1b[0m") {
		t.Fatalf("italic (_…_) not wrapped: %q", out)
	}
	if !strings.Contains(out, "\x1b[3malso\x1b[0m") {
		t.Fatalf("italic (*…*) not wrapped: %q", out)
	}
}

func TestRenderResetClearsCodeBlockState(t *testing.T) {
	r := NewMarkdownRenderer(plainStyle())
	var buf bytes.Buffer
	r.Write(&buf, "```\nstuff\n")
	r.Reset()
	r.Write(&buf, "# Heading\n")
	r.Flush(&buf)
	if !strings.Contains(buf.String(), "# Heading") {
		t.Fatalf("after Reset the next heading should render normally, got:\n%s", buf.String())
	}
}
