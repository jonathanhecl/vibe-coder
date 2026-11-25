package tui

import (
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/charmbracelet/lipgloss"
)

const hybridThinkMaxLines = 6

var (
	hybridThinkBorderStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("63"))
	hybridThinkTextStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("246"))
	hybridThinkMetaStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	hybridAsstBuildStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	hybridAsstPlanStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	hybridFooterDimStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("244"))
	hybridFooterSpdStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("81"))

	hybridUserBuildStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true)
	hybridUserPlanStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
)

func (h *HybridUI) hybridBoxWidth() int {
	cols := terminalColumns(h.plain.out)
	if cols <= 0 {
		cols = 80
	}
	w := cols - 2
	if w < 36 {
		w = 36
	}
	return w
}

func (h *HybridUI) openThinkingBlockLocked() {
	if h.thinkActive {
		return
	}
	h.thinkActive = true
	h.thinkStart = time.Now()
	h.thinkBuf.Reset()
	h.thinkRenderedRows = 0
}

func (h *HybridUI) writeHybridThinkingLocked(text string) {
	if text == "" {
		return
	}
	if h.plain.cfg != nil && h.plain.cfg.OllamaHideThink {
		return
	}
	if !h.thinkActive {
		h.openThinkingBlockLocked()
	}
	h.thinkBuf.WriteString(text)
	h.repaintThinkingBoxLocked(false, false)
}

func (h *HybridUI) closeHybridThinkingLocked(withElapsed bool) {
	if !h.thinkActive {
		return
	}
	if raw := strings.TrimSpace(h.thinkBuf.String()); raw != "" {
		h.mu.Lock()
		h.lastThinking = h.thinkBuf.String()
		h.mu.Unlock()
	}
	h.repaintThinkingBoxLocked(true, withElapsed)
	h.thinkActive = false
	h.thinkBuf.Reset()
	h.thinkRenderedRows = 0
}

func (h *HybridUI) repaintThinkingBoxLocked(settled, withElapsed bool) {
	box, rows := h.renderThinkingBoxLocked(settled, withElapsed)
	if rows == 0 {
		return
	}
	if h.thinkRenderedRows == 0 {
		_, _ = fmt.Fprintf(h.plain.out, "\n%s\n", box)
	} else {
		_, _ = fmt.Fprintf(h.plain.out, "\x1b[%dA\r\x1b[J%s\n", h.thinkRenderedRows, box)
	}
	h.thinkRenderedRows = rows
}

func (h *HybridUI) renderThinkingBoxLocked(settled, withElapsed bool) (string, int) {
	boxWidth := h.hybridBoxWidth()
	innerWidth := boxWidth - 4 // "│ " + content + " │"
	if innerWidth < 16 {
		innerWidth = 16
		boxWidth = innerWidth + 4
	}

	raw := strings.ReplaceAll(strings.ReplaceAll(h.thinkBuf.String(), "\r\n", "\n"), "\r", "\n")
	raw = strings.ReplaceAll(raw, "\t", "  ")
	if settled {
		raw = strings.TrimRight(raw, "\n")
	}

	allLines := wrapThinkingLines(raw, innerWidth)
	totalLines := len(allLines)
	start := 0
	if totalLines > hybridThinkMaxLines {
		start = totalLines - hybridThinkMaxLines
	}
	visible := allLines[start:]

	headerTitle := " 💭 Thinking "
	if totalLines > hybridThinkMaxLines {
		headerTitle = fmt.Sprintf(" 💭 Thinking (lines %d-%d of %d) ", start+1, totalLines, totalLines)
	}
	topBorder := renderThinkingTopBorder(headerTitle, boxWidth)

	var b strings.Builder
	b.WriteString(topBorder)
	b.WriteByte('\n')

	leftRail := hybridThinkBorderStyle.Render("│")
	rightRail := hybridThinkBorderStyle.Render("│")
	for _, line := range visible {
		w := displayWidth(line)
		if w > innerWidth {
			line = truncateDisplayWidth(line, innerWidth)
			w = displayWidth(line)
		}
		pad := strings.Repeat(" ", innerWidth-w)
		b.WriteString(leftRail)
		b.WriteByte(' ')
		b.WriteString(hybridThinkTextStyle.Render(line + pad))
		b.WriteByte(' ')
		b.WriteString(rightRail)
		b.WriteByte('\n')
	}

	meta := ""
	if withElapsed {
		meta = "thought for " + formatElapsed(time.Since(h.thinkStart))
	}
	if start > 0 {
		if meta != "" {
			meta = fmt.Sprintf("%s · +%d lines (Ctrl+T to scroll)", meta, start)
		} else {
			meta = fmt.Sprintf("+%d lines (Ctrl+T to scroll)", start)
		}
	}
	b.WriteString(renderThinkingBottomBorder(meta, boxWidth))

	rows := len(visible) + 2
	return b.String(), rows
}

func renderThinkingTopBorder(title string, boxWidth int) string {
	maxTitle := boxWidth - 5 // "╭─" + title + min 2 "─" + "╮"
	if maxTitle < 1 {
		maxTitle = 1
	}
	if displayWidth(title) > maxTitle {
		title = truncateDisplayWidth(title, maxTitle)
	}
	fill := boxWidth - 2 - displayWidth(title) - 1
	if fill < 1 {
		fill = 1
	}
	return hybridThinkBorderStyle.Render("╭─" + title + strings.Repeat("─", fill) + "╮")
}

func renderThinkingBottomBorder(meta string, boxWidth int) string {
	if meta == "" {
		fill := boxWidth - 2
		if fill < 2 {
			fill = 2
		}
		return hybridThinkBorderStyle.Render("╰" + strings.Repeat("─", fill) + "╯")
	}
	maxMeta := boxWidth - 6 // "╰─ " + meta + " " + min 1 "─" + "╯"
	if maxMeta < 1 {
		maxMeta = 1
	}
	if displayWidth(meta) > maxMeta {
		meta = truncateDisplayWidth(meta, maxMeta)
	}
	fill := boxWidth - 3 - displayWidth(meta) - 2
	if fill < 1 {
		fill = 1
	}
	return hybridThinkBorderStyle.Render("╰─ ") +
		hybridThinkMetaStyle.Render(meta) + " " +
		hybridThinkBorderStyle.Render(strings.Repeat("─", fill)+"╯")
}

func wrapThinkingLines(text string, width int) []string {
	if width < 8 {
		width = 8
	}
	if text == "" {
		return []string{""}
	}
	norm := strings.ReplaceAll(strings.ReplaceAll(text, "\r\n", "\n"), "\r", "\n")
	norm = strings.ReplaceAll(norm, "\t", "  ")
	paragraphs := strings.Split(norm, "\n")
	var out []string
	for _, p := range paragraphs {
		out = append(out, wrapThinkingParagraph(p, width)...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

func wrapThinkingParagraph(line string, width int) []string {
	if line == "" {
		return []string{""}
	}
	var out []string
	var cur strings.Builder
	curWidth := 0

	flush := func() {
		out = append(out, cur.String())
		cur.Reset()
		curWidth = 0
	}

	runes := []rune(line)
	for i := 0; i < len(runes); {
		if unicode.IsSpace(runes[i]) {
			spaceStart := i
			for i < len(runes) && unicode.IsSpace(runes[i]) {
				i++
			}
			// Drop leading whitespace after an automatic soft wrap.
			if curWidth == 0 && len(out) > 0 {
				continue
			}
			spaces := string(runes[spaceStart:i])
			sw := displayWidth(spaces)
			if curWidth+sw <= width {
				cur.WriteString(spaces)
				curWidth += sw
			} else if curWidth > 0 {
				flush()
			}
			continue
		}

		wordStart := i
		for i < len(runes) && !unicode.IsSpace(runes[i]) {
			i++
		}
		word := runes[wordStart:i]
		ww := displayWidth(string(word))
		if curWidth > 0 && curWidth+ww > width {
			out = append(out, strings.TrimRight(cur.String(), " \t"))
			cur.Reset()
			curWidth = 0
		}
		if ww <= width {
			cur.WriteString(string(word))
			curWidth += ww
			continue
		}
		for _, r := range word {
			rw := runeCellWidth(r)
			if rw <= 0 {
				rw = 1
			}
			if curWidth+rw > width && curWidth > 0 {
				flush()
			}
			cur.WriteRune(r)
			curWidth += rw
		}
	}
	if cur.Len() > 0 || len(out) == 0 {
		out = append(out, strings.TrimRight(cur.String(), " \t"))
	}
	return out
}

func truncateDisplayWidth(s string, maxCols int) string {
	if maxCols <= 0 {
		return ""
	}
	if displayWidth(s) <= maxCols {
		return s
	}
	var b strings.Builder
	w := 0
	for _, r := range s {
		rw := runeCellWidth(r)
		if rw <= 0 {
			rw = 1
		}
		if w+rw > maxCols {
			break
		}
		b.WriteRune(r)
		w += rw
	}
	return b.String()
}

func (h *HybridUI) streamThinking(text string) {
	if !h.plain.style.Enabled() {
		h.plain.StreamThinking(text)
		return
	}
	if text == "" {
		return
	}
	if h.plain.cfg != nil && h.plain.cfg.OllamaHideThink {
		return
	}
	h.plain.stopSpinner()
	h.plain.mu.Lock()
	defer h.plain.mu.Unlock()

	h.plain.flushPendingToolLocked()
	h.plain.endAssistantLineLocked()
	h.writeHybridThinkingLocked(text)
}

func (h *HybridUI) endThinking() {
	if !h.plain.style.Enabled() {
		h.plain.EndThinking()
		return
	}
	if h.plain.cfg != nil && h.plain.cfg.OllamaHideThink {
		return
	}
	h.plain.stopSpinner()
	h.plain.mu.Lock()
	defer h.plain.mu.Unlock()
	h.closeHybridThinkingLocked(true)
}

func (h *HybridUI) streamAssistant(text string) {
	if !h.plain.style.Enabled() {
		h.plain.StreamAssistant(text)
		return
	}
	h.plain.stopSpinner()
	h.plain.mu.Lock()
	defer h.plain.mu.Unlock()

	h.plain.flushPendingToolLocked()
	h.plain.streamBuffer.WriteString(text)

	for {
		buf := h.plain.streamBuffer.String()
		visible, thinking, leftover, hasMore := splitThinking(buf)
		visible = stripToolEnvelopes(visible)

		if visible != "" {
			h.closeHybridThinkingLocked(true)
			if !h.plain.streamingAssistant {
				hdr := hybridAsstBuildStyle.Render("● assistant")
				if h.plain.planMode {
					hdr = hybridAsstPlanStyle.Render("● assistant")
				}
				_, _ = fmt.Fprintf(h.plain.out, "\n%s\n", hdr)
				h.plain.streamingAssistant = true
				h.plain.assistantReplyStart = time.Now()
				h.plain.assistantHadVisible = false
				h.plain.assistantLines = 2
				h.plain.ensureMarkdownLocked()
			}
			h.plain.ensureMarkdownLocked()
			h.plain.markdown.Write(h.plain.out, visible)
			h.plain.assistantHadVisible = true
			h.plain.assistantLines += strings.Count(visible, "\n")
		}
		if thinking != "" {
			h.writeHybridThinkingLocked(thinking)
		}
		if !hasMore {
			h.plain.streamBuffer.Reset()
			h.plain.streamBuffer.WriteString(leftover)
			return
		}
		h.plain.streamBuffer.Reset()
		h.plain.streamBuffer.WriteString(leftover)
	}
}

func (h *HybridUI) endAssistant(usage TurnUsage) {
	if !h.plain.style.Enabled() {
		h.plain.EndAssistant(usage)
		return
	}
	h.plain.stopSpinner()
	h.plain.mu.Lock()
	defer h.plain.mu.Unlock()

	if rest := h.plain.streamBuffer.String(); rest != "" {
		rest = stripToolEnvelopes(rest)
		if rest != "" {
			h.closeHybridThinkingLocked(true)
			h.plain.ensureMarkdownLocked()
			h.plain.markdown.Write(h.plain.out, rest)
			h.plain.assistantHadVisible = true
			h.plain.assistantLines += strings.Count(rest, "\n")
		}
		h.plain.streamBuffer.Reset()
	}
	if h.plain.markdown != nil {
		h.plain.markdown.Flush(h.plain.out)
		h.plain.markdown.Reset()
	}
	h.closeHybridThinkingLocked(true)

	start := h.plain.turnStart
	if start.IsZero() {
		start = h.plain.assistantReplyStart
	}
	if h.plain.streamingAssistant && h.plain.assistantHadVisible && !start.IsZero() {
		elapsed := formatElapsed(time.Since(start))
		footer := hybridFooterDimStyle.Render("── responded in " + elapsed)
		if speed := formatTokensPerSecond(usage); speed != "" {
			footer += hybridFooterDimStyle.Render(" · ") + hybridFooterSpdStyle.Render(speed)
		}
		_, _ = fmt.Fprintf(h.plain.out, "  %s\n", footer)
		h.plain.assistantLines++
	}
	if h.plain.streamingAssistant && !h.plain.assistantHadVisible {
		if h.plain.assistantLines > 0 {
			_, _ = fmt.Fprintf(h.plain.out, "\x1b[%dA\x1b[J", h.plain.assistantLines)
		}
	}
	if !h.plain.assistantHadVisible {
		h.plain.assistantLines = 0
	}
	h.plain.assistantReplyStart = time.Time{}
	h.plain.assistantHadVisible = false
	h.plain.streamingAssistant = false
}

func (h *HybridUI) showToolCall(name string, params map[string]any) {
	if h.plain.style.Enabled() {
		h.plain.stopSpinner()
		h.plain.mu.Lock()
		h.closeHybridThinkingLocked(false)
		h.plain.mu.Unlock()
	}
	h.plain.ShowToolCall(name, params)
}

func (h *HybridUI) showToolResult(name, output string, isError bool, toolParams map[string]any) {
	if !h.plain.style.Enabled() {
		h.plain.ShowToolResult(name, output, isError, toolParams)
		return
	}
	h.plain.stopSpinner()
	h.plain.mu.Lock()
	defer h.plain.mu.Unlock()

	h.closeHybridThinkingLocked(false)
	if h.plain.pendingActive {
		h.plain.clearPendingLocked()
	}

	background := (name == "BackgroundRun" || name == "BackgroundStatus") && strings.Contains(output, "STILL RUNNING")
	borderColor := lipgloss.Color("42")
	glyph := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Bold(true).Render("✓")
	if isError {
		borderColor = lipgloss.Color("203")
		glyph = lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true).Render("✗")
	} else if background {
		borderColor = lipgloss.Color("214")
		glyph = lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Bold(true).Render("⏳")
	}

	header := toolHeader(h.plain.style, name, toolParams)
	if toolParams == nil && h.plain.pendingActive && h.plain.pendingTool == name {
		header = h.plain.pendingHeader
	}

	summary := toolResultSummary(h.plain.style, name, output, isError, toolParams)
	if !isError && name == "Bash" && h.plain.pendingActive && h.plain.pendingTool == name && !h.plain.pendingStart.IsZero() {
		summary = formatToolDuration(time.Since(h.plain.pendingStart)) + ", " + summary
	}

	var body strings.Builder
	switch {
	case isError:
		fmt.Fprintf(&body, "%s %s %s %s", glyph, header,
			lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Bold(true).Render("error"),
			h.plain.style.Red("→ "+summary))
	case background:
		fmt.Fprintf(&body, "%s %s %s %s", glyph, header,
			lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("running in background"),
			h.plain.style.Yellow("→ "+summary))
	default:
		fmt.Fprintf(&body, "%s %s %s", glyph, header, h.plain.style.Dim("→ ")+summary)
	}

	if !isError && name == "Edit" && toolParams != nil {
		if diffLines := buildHybridDiffLines(h.plain.style, toolParams); len(diffLines) > 0 {
			body.WriteByte('\n')
			body.WriteString(strings.Join(diffLines, "\n"))
		}
	}

	if !isError && name == "Bash" && h.plain.outIsTTY && strings.TrimSpace(output) != "" {
		lines := strings.Split(strings.TrimRight(output, "\n"), "\n")
		if len(lines) >= 2 {
			if len(lines) > bashOutputTailLines {
				lines = lines[len(lines)-bashOutputTailLines:]
			}
			for _, ln := range lines {
				body.WriteByte('\n')
				body.WriteString(h.plain.style.Dim("  " + trimOutputLine(ln, outputTailMaxRunes)))
			}
		}
	}

	if isError && name == "Bash" && toolParams != nil {
		if cmd, ok := toolParams["command"].(string); ok && bashCommandTruncated(cmd) {
			body.WriteByte('\n')
			body.WriteString(h.plain.style.Dim("$ " + strings.TrimSpace(cmd)))
		}
	}

	if isError && strings.TrimSpace(output) != "" {
		for _, ln := range strings.Split(strings.TrimRight(output, "\n"), "\n") {
			body.WriteByte('\n')
			body.WriteString(h.plain.style.Red(ln))
		}
	}

	card := lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), false, false, false, true).
		BorderForeground(borderColor).
		PaddingLeft(1).
		MarginLeft(1).
		Render(body.String())

	_, _ = fmt.Fprintln(h.plain.out, card)

	h.plain.pendingActive = false
	h.plain.pendingTool = ""
	h.plain.pendingHeader = ""
	h.plain.pendingStart = time.Time{}
}

func buildHybridDiffLines(st Style, params map[string]any) []string {
	if diff, ok := params["_diff"].(string); ok && diff != "" {
		raw := strings.Split(strings.TrimRight(diff, "\n"), "\n")
		truncated := false
		if len(raw) > 50 {
			raw = raw[:50]
			truncated = true
		}
		out := make([]string, 0, len(raw)+1)
		for _, line := range raw {
			switch {
			case strings.HasPrefix(line, "@@"):
				out = append(out, st.Cyan(line))
			case strings.HasPrefix(line, "-"):
				out = append(out, st.Red(line))
			case strings.HasPrefix(line, "+"):
				out = append(out, st.Green(line))
			default:
				out = append(out, st.Dim(line))
			}
		}
		if truncated {
			out = append(out, st.Dim("… diff truncated"))
		}
		return out
	}

	oldS, ok1 := params["old_string"].(string)
	newS, ok2 := params["new_string"].(string)
	if !ok1 || !ok2 || (strings.TrimSpace(oldS) == "" && strings.TrimSpace(newS) == "") {
		return nil
	}
	var out []string
	if fp, _ := params["file_path"].(string); fp != "" {
		out = append(out, st.Dim("∷ "+compactPath(fp)))
	}
	if ol := firstLineOrSummary(oldS, 96); ol != "" {
		out = append(out, st.Red("− ")+st.Dim(ol))
	}
	if nl := firstLineOrSummary(newS, 96); nl != "" {
		out = append(out, st.Green("+ ")+st.Dim(nl))
	}
	if strings.Count(oldS, "\n")+strings.Count(newS, "\n") > 2 {
		out = append(out, st.Dim("… multiline change (see file)"))
	}
	return out
}

func (h *HybridUI) showTodos(items []TodoItem) {
	if len(items) == 0 {
		return
	}
	done := 0
	for _, it := range items {
		if it.Status == todoStatusCompleted {
			done++
		}
	}
	h.mu.Lock()
	h.status.TodosDone = done
	h.status.TodosTotal = len(items)
	h.mu.Unlock()

	if !h.plain.style.Enabled() {
		h.plain.ShowTodos(items)
		return
	}

	h.plain.stopSpinner()
	h.plain.mu.Lock()
	defer h.plain.mu.Unlock()

	h.plain.flushPendingToolLocked()
	h.plain.endAssistantLineLocked()
	h.closeHybridThinkingLocked(false)

	total := len(items)
	const barWidth = 10
	filled := 0
	if total > 0 {
		filled = done * barWidth / total
	}
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render(strings.Repeat("█", filled)) +
		lipgloss.NewStyle().Foreground(lipgloss.Color("239")).Render(strings.Repeat("░", barWidth-filled))

	title := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Render("To-dos")
	countBadge := lipgloss.NewStyle().Foreground(lipgloss.Color("250")).Render(fmt.Sprintf("%d/%d", done, total))

	var b strings.Builder
	fmt.Fprintf(&b, "%s  %s  %s\n", title, countBadge, bar)
	for _, it := range items {
		glyph, color := todoGlyph(h.plain.style, it.Status)
		body := it.Content
		if strings.TrimSpace(body) == "" {
			body = it.ID
		}
		if it.Status == todoStatusCompleted {
			body = h.plain.style.Dim(body)
		}
		fmt.Fprintf(&b, " %s  %s\n", color(glyph), body)
	}

	boxWidth := h.hybridBoxWidth() - 2
	if boxWidth < 36 {
		boxWidth = 36
	}
	panel := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("63")).
		Padding(0, 1).
		Width(boxWidth).
		Render(strings.TrimRight(b.String(), "\n"))

	_, _ = fmt.Fprintf(h.plain.out, "\n%s\n", panel)
}

func (h *HybridUI) renderUserEcho(prompt, val string) string {
	if !h.plain.style.Enabled() {
		return h.plain.renderPromptString(prompt) + formatScrollbackEcho(val)
	}
	borderColor := lipgloss.Color("42")
	labelStyle := hybridUserBuildStyle
	if h.plain.planMode {
		borderColor = lipgloss.Color("220")
		labelStyle = hybridUserPlanStyle
	}
	body := labelStyle.Render("you") + " " + formatScrollbackEcho(val)
	return lipgloss.NewStyle().
		Border(lipgloss.ThickBorder(), false, false, false, true).
		BorderForeground(borderColor).
		PaddingLeft(1).
		Render(body)
}
