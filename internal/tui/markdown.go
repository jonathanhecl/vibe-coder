package tui

import (
	"fmt"
	"io"
	"regexp"
	"strings"
)

// MarkdownRenderer turns model output into ANSI-styled lines suitable for
// a terminal. It is **streaming-friendly**: callers feed chunks via Write
// as they arrive and the renderer holds back any partial trailing line
// until the next chunk (or Flush) so multi-character markers like
// "```lang" never get split between two render calls.
//
// Scope: a deliberate subset of CommonMark that covers what coding
// assistants actually produce — headings, bullets, numbered lists,
// blockquotes, fenced code blocks, horizontal rules, and the four common
// inline marks (code, bold, italic, links). Tables, footnotes, raw HTML
// and reference-style links are rendered verbatim.
//
// When the supplied Style is disabled (NO_COLOR / non-TTY) the renderer
// still applies the *structural* transforms (bullet glyph, code-block
// borders, header prefixes) but skips ANSI codes, so transcripts copied
// out of a redirected log stay readable.
type MarkdownRenderer struct {
	style    Style
	inCode   bool
	codeLang string
	buf      strings.Builder
}

// NewMarkdownRenderer constructs a renderer bound to the given style.
func NewMarkdownRenderer(style Style) *MarkdownRenderer {
	return &MarkdownRenderer{style: style}
}

// Reset clears any partial line and code-block state. Call between turns
// so a half-streamed code fence in turn N doesn't leak into turn N+1.
func (r *MarkdownRenderer) Reset() {
	r.buf.Reset()
	r.inCode = false
	r.codeLang = ""
}

// Write feeds a chunk of markdown text into the renderer. Complete lines
// are rendered immediately to w; the trailing partial line (if any) is
// buffered for the next call. Errors writing to w are silently dropped to
// match the rest of the TUI's "best effort" stdout writes.
func (r *MarkdownRenderer) Write(w io.Writer, chunk string) {
	if chunk == "" {
		return
	}
	r.buf.WriteString(chunk)
	pending := r.buf.String()
	r.buf.Reset()

	for {
		idx := strings.IndexByte(pending, '\n')
		if idx < 0 {
			r.buf.WriteString(pending)
			return
		}
		line := stripCR(pending[:idx])
		pending = pending[idx+1:]
		fmt.Fprintln(w, r.renderLine(line))
	}
}

// Flush emits any buffered partial line. Call it at the end of an
// assistant turn so a final line without trailing newline still appears.
// Resets the buffer but preserves code-block state in case the same
// renderer is reused.
func (r *MarkdownRenderer) Flush(w io.Writer) {
	if r.buf.Len() == 0 {
		return
	}
	line := stripCR(r.buf.String())
	r.buf.Reset()
	fmt.Fprintln(w, r.renderLine(line))
}

// renderLine is the heart of the renderer: pure function from a single
// line of source markdown to a styled line. State changes (entering or
// leaving a code block) live here as well so the streaming wrapper stays
// trivial.
func (r *MarkdownRenderer) renderLine(line string) string {
	trimmed := strings.TrimSpace(line)

	// Fenced code blocks: ```lang on open, ``` on close.
	if strings.HasPrefix(trimmed, "```") {
		if r.inCode {
			r.inCode = false
			r.codeLang = ""
			return r.style.Dim("└─")
		}
		lang := strings.TrimSpace(strings.TrimPrefix(trimmed, "```"))
		r.inCode = true
		r.codeLang = lang
		if lang == "" {
			return r.style.Dim("┌─")
		}
		return r.style.Dim("┌─ ") + r.style.Cyan(lang)
	}
	if r.inCode {
		// Inside code blocks we deliberately do NOT apply inline marks
		// (** _ ` []) — they are part of the code, not formatting.
		return r.style.Dim(iconBar+" ") + r.style.Cyan(line)
	}

	// Block-level constructs.
	switch {
	case strings.HasPrefix(line, "# "):
		return r.style.BoldGreen("# " + strings.TrimPrefix(line, "# "))
	case strings.HasPrefix(line, "## "):
		return r.style.BoldBlue("## " + strings.TrimPrefix(line, "## "))
	case strings.HasPrefix(line, "### "):
		return r.style.BoldYellow("### " + strings.TrimPrefix(line, "### "))
	case strings.HasPrefix(line, "#### "):
		return r.style.Bold("#### " + strings.TrimPrefix(line, "#### "))
	case strings.HasPrefix(line, "> "):
		return r.style.Dim(iconBar+" ") + r.renderInline(strings.TrimPrefix(line, "> "))
	case trimmed == "---" || trimmed == "***" || trimmed == "___":
		return r.style.Dim(strings.Repeat("─", 48))
	}

	if indent, body, ok := bulletPrefix(line); ok {
		return indent + r.style.BrightGreen("•") + " " + r.renderInline(body)
	}
	if indent, num, body, ok := numberedPrefix(line); ok {
		return indent + r.style.BrightBlue(num) + " " + r.renderInline(body)
	}

	if line == "" {
		return ""
	}
	return r.renderInline(line)
}

// bulletPrefix matches "- " / "* " / "+ " possibly indented; returns the
// preserved indent, the body, and whether the match succeeded.
var bulletRE = regexp.MustCompile(`^([ \t]*)[-*+] (.*)$`)

func bulletPrefix(line string) (indent, body string, ok bool) {
	m := bulletRE.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	return m[1], m[2], true
}

// numberedPrefix matches "1. " / "12) " etc.; returns indent, the marker
// (e.g. "1." kept verbatim so the user sees the order), the body, and ok.
var numberedRE = regexp.MustCompile(`^([ \t]*)(\d+[.)]) (.*)$`)

func numberedPrefix(line string) (indent, marker, body string, ok bool) {
	m := numberedRE.FindStringSubmatch(line)
	if m == nil {
		return "", "", "", false
	}
	return m[1], m[2], m[3], true
}

// renderInline applies the four common inline marks. Order matters:
//   - inline code first, because backticks suppress every other mark inside;
//   - then links, so the bracket text isn't accidentally bolded;
//   - then bold (**) before italic (* / _) so "***x***" parses cleanly.
//
// When colors are disabled (NO_COLOR / non-TTY) we leave the source
// markdown untouched so a redirected log keeps the original `code`,
// **bold**, _italic_ and [label](url) markers — which is more useful to
// a human re-reading the transcript than silently stripped delimiters.
func (r *MarkdownRenderer) renderInline(text string) string {
	if !r.style.Enabled() {
		return text
	}
	text = applyInlineCode(r.style, text)
	text = applyInlineLinks(r.style, text)
	text = applyInlineBold(r.style, text)
	text = applyInlineItalic(r.style, text)
	return text
}

var (
	inlineCodeRE = regexp.MustCompile("`([^`\n]+)`")
	inlineBoldRE = regexp.MustCompile(`\*\*([^*\n]+)\*\*`)
	// Italic accepts *foo* or _foo_ but avoids matching ** (handled above)
	// or word-internal underscores like snake_case.
	inlineItalicAsterRE = regexp.MustCompile(`(^|[^*\w])\*([^*\n]+)\*([^*\w]|$)`)
	inlineItalicUnderRE = regexp.MustCompile(`(^|[^_\w])_([^_\n]+)_([^_\w]|$)`)
	inlineLinkRE        = regexp.MustCompile(`\[([^\]]+)\]\(([^)\s]+)\)`)
)

func applyInlineCode(s Style, text string) string {
	if !strings.Contains(text, "`") {
		return text
	}
	return inlineCodeRE.ReplaceAllStringFunc(text, func(m string) string {
		inner := m[1 : len(m)-1]
		return s.Cyan(inner)
	})
}

func applyInlineBold(s Style, text string) string {
	if !strings.Contains(text, "**") {
		return text
	}
	return inlineBoldRE.ReplaceAllStringFunc(text, func(m string) string {
		inner := m[2 : len(m)-2]
		return s.Bold(inner)
	})
}

func applyInlineItalic(s Style, text string) string {
	text = inlineItalicAsterRE.ReplaceAllStringFunc(text, func(m string) string {
		sub := inlineItalicAsterRE.FindStringSubmatch(m)
		return sub[1] + s.Italic(sub[2]) + sub[3]
	})
	text = inlineItalicUnderRE.ReplaceAllStringFunc(text, func(m string) string {
		sub := inlineItalicUnderRE.FindStringSubmatch(m)
		return sub[1] + s.Italic(sub[2]) + sub[3]
	})
	return text
}

func applyInlineLinks(s Style, text string) string {
	if !strings.Contains(text, "](") {
		return text
	}
	return inlineLinkRE.ReplaceAllStringFunc(text, func(m string) string {
		sub := inlineLinkRE.FindStringSubmatch(m)
		label, url := sub[1], sub[2]
		return s.BrightBlue(label) + s.Dim(" ("+url+")")
	})
}

func stripCR(s string) string {
	if strings.HasSuffix(s, "\r") {
		return s[:len(s)-1]
	}
	return s
}
