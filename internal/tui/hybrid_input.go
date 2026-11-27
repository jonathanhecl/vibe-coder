package tui

import (
	"fmt"
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

const maxSlashSuggestions = 6

type hybridInputModel struct {
	prompt          string
	status          StatusInfo
	buf             []rune
	cursor          int
	width           int
	history         []string
	histIdx         int
	draft           string
	slashCmds       []SlashCommand
	suggIdx         int
	notice          string
	hasThinking     bool
	inspectThinking bool
	submitted       bool
	eof             bool
	quitting        bool
}

func newHybridInputModel(prompt string, status StatusInfo, history []string, cmds []SlashCommand, width int) hybridInputModel {
	if width <= 0 {
		width = 80
	}
	return hybridInputModel{
		prompt:    prompt,
		status:    status,
		width:     width,
		history:   append([]string(nil), history...),
		histIdx:   len(history),
		slashCmds: append([]SlashCommand(nil), cmds...),
	}
}

func (m hybridInputModel) withThinkingAvailable(ok bool) hybridInputModel {
	m.hasThinking = ok
	return m
}

func (m hybridInputModel) withDraft(buf []rune, cursor int) hybridInputModel {
	if len(buf) == 0 {
		return m
	}
	m.buf = append([]rune(nil), buf...)
	if cursor < 0 {
		cursor = 0
	}
	if cursor > len(m.buf) {
		cursor = len(m.buf)
	}
	m.cursor = cursor
	return m
}

func (m hybridInputModel) Init() tea.Cmd { return nil }

func (m hybridInputModel) value() string { return string(m.buf) }

func (m hybridInputModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		return m, nil

	case tea.KeyMsg:
		m.notice = ""

		// Bracketed paste inserts pasted text verbatim (including newlines)
		// without triggering submit.
		if msg.Paste {
			pasted := strings.ReplaceAll(string(msg.Runes), "\r\n", "\n")
			pasted = strings.ReplaceAll(pasted, "\r", "\n")
			m.insertRunes([]rune(pasted))
			m.clampSuggIdx()
			return m, nil
		}

		// Alt+Enter inserts a newline for multi-line prompts.
		if msg.Alt && msg.Type == tea.KeyEnter {
			m.insertRunes([]rune{'\n'})
			m.clampSuggIdx()
			return m, nil
		}

		switch msg.Type {
		case tea.KeyCtrlC:
			if len(m.buf) > 0 {
				m.buf = nil
				m.cursor = 0
				m.suggIdx = 0
				return m, nil
			}
			m.notice = "(Press Ctrl+D or type /exit to quit)"
			return m, nil

		case tea.KeyCtrlD:
			if len(m.buf) == 0 {
				m.eof = true
				m.quitting = true
				return m, tea.Quit
			}
			if m.cursor < len(m.buf) {
				m.buf = append(m.buf[:m.cursor], m.buf[m.cursor+1:]...)
				m.clampSuggIdx()
			}
			return m, nil

		case tea.KeyCtrlT:
			if m.hasThinking {
				m.inspectThinking = true
				m.quitting = true
				return m, tea.Quit
			}
			m.notice = "No thinking trace recorded yet."
			return m, nil

		case tea.KeyEnter:
			m.submitted = true
			m.quitting = true
			return m, tea.Quit

		case tea.KeyCtrlJ:
			m.insertRunes([]rune{'\n'})
			m.clampSuggIdx()
			return m, nil

		case tea.KeyTab:
			suggs := m.matchingSlashCommands()
			if len(suggs) > 0 {
				m.applySuggestion(suggs[m.suggIdx])
			}
			return m, nil

		case tea.KeyShiftTab:
			suggs := m.matchingSlashCommands()
			if len(suggs) > 0 {
				m.suggIdx = (m.suggIdx + 1) % len(suggs)
			}
			return m, nil

		case tea.KeyUp:
			suggs := m.matchingSlashCommands()
			if len(suggs) > 0 && !strings.ContainsRune(string(m.buf), '\n') {
				if m.suggIdx > 0 {
					m.suggIdx--
				} else {
					m.suggIdx = len(suggs) - 1
				}
				return m, nil
			}
			if m.moveVertical(-1) {
				return m, nil
			}
			m.historyPrev()
			return m, nil

		case tea.KeyDown:
			suggs := m.matchingSlashCommands()
			if len(suggs) > 0 && !strings.ContainsRune(string(m.buf), '\n') {
				m.suggIdx = (m.suggIdx + 1) % len(suggs)
				return m, nil
			}
			if m.moveVertical(1) {
				return m, nil
			}
			m.historyNext()
			return m, nil

		case tea.KeyLeft:
			if m.cursor > 0 {
				m.cursor--
			}
			return m, nil

		case tea.KeyRight:
			if m.cursor < len(m.buf) {
				m.cursor++
			}
			return m, nil

		case tea.KeyHome, tea.KeyCtrlA:
			m.cursor = m.lineStart(m.cursor)
			return m, nil

		case tea.KeyEnd, tea.KeyCtrlE:
			m.cursor = m.lineEnd(m.cursor)
			return m, nil

		case tea.KeyBackspace:
			if m.cursor > 0 {
				m.buf = append(m.buf[:m.cursor-1], m.buf[m.cursor:]...)
				m.cursor--
				m.clampSuggIdx()
			}
			return m, nil

		case tea.KeyDelete:
			if m.cursor < len(m.buf) {
				m.buf = append(m.buf[:m.cursor], m.buf[m.cursor+1:]...)
				m.clampSuggIdx()
			}
			return m, nil

		case tea.KeyCtrlU:
			start := m.lineStart(m.cursor)
			if start < m.cursor {
				m.buf = append(m.buf[:start], m.buf[m.cursor:]...)
				m.cursor = start
				m.clampSuggIdx()
			}
			return m, nil

		case tea.KeyCtrlK:
			end := m.lineEnd(m.cursor)
			if m.cursor < end {
				m.buf = append(m.buf[:m.cursor], m.buf[end:]...)
				m.clampSuggIdx()
			}
			return m, nil

		case tea.KeyCtrlW:
			if m.cursor > 0 {
				pos := m.cursor
				for pos > 0 && unicode.IsSpace(m.buf[pos-1]) {
					pos--
				}
				for pos > 0 && !unicode.IsSpace(m.buf[pos-1]) {
					pos--
				}
				m.buf = append(m.buf[:pos], m.buf[m.cursor:]...)
				m.cursor = pos
				m.clampSuggIdx()
			}
			return m, nil

		case tea.KeySpace:
			m.insertRunes([]rune{' '})
			m.clampSuggIdx()
			return m, nil

		case tea.KeyRunes:
			if len(msg.Runes) > 0 {
				m.insertRunes(msg.Runes)
				m.clampSuggIdx()
			}
			return m, nil
		}
	}
	return m, nil
}

func (m *hybridInputModel) insertRunes(rs []rune) {
	if len(rs) == 0 {
		return
	}
	next := make([]rune, 0, len(m.buf)+len(rs))
	next = append(next, m.buf[:m.cursor]...)
	next = append(next, rs...)
	next = append(next, m.buf[m.cursor:]...)
	m.buf = next
	m.cursor += len(rs)
}

func (m hybridInputModel) lineStart(pos int) int {
	for pos > 0 && m.buf[pos-1] != '\n' {
		pos--
	}
	return pos
}

func (m hybridInputModel) lineEnd(pos int) int {
	for pos < len(m.buf) && m.buf[pos] != '\n' {
		pos++
	}
	return pos
}

func (m *hybridInputModel) moveVertical(delta int) bool {
	curStart := m.lineStart(m.cursor)
	col := m.cursor - curStart
	if delta < 0 {
		if curStart == 0 {
			return false
		}
		prevEnd := curStart - 1
		prevStart := m.lineStart(prevEnd)
		target := prevStart + col
		if target > prevEnd {
			target = prevEnd
		}
		m.cursor = target
		return true
	}
	curEnd := m.lineEnd(m.cursor)
	if curEnd >= len(m.buf) {
		return false
	}
	nextStart := curEnd + 1
	nextEnd := m.lineEnd(nextStart)
	target := nextStart + col
	if target > nextEnd {
		target = nextEnd
	}
	m.cursor = target
	return true
}

func (m *hybridInputModel) historyPrev() {
	if len(m.history) == 0 || m.histIdx <= 0 {
		return
	}
	if m.histIdx == len(m.history) {
		m.draft = string(m.buf)
	}
	m.histIdx--
	m.buf = []rune(m.history[m.histIdx])
	m.cursor = len(m.buf)
	m.suggIdx = 0
}

func (m *hybridInputModel) historyNext() {
	if m.histIdx >= len(m.history) {
		return
	}
	m.histIdx++
	if m.histIdx == len(m.history) {
		m.buf = []rune(m.draft)
	} else {
		m.buf = []rune(m.history[m.histIdx])
	}
	m.cursor = len(m.buf)
	m.suggIdx = 0
}

func (m hybridInputModel) matchingSlashCommands() []SlashCommand {
	if len(m.slashCmds) == 0 {
		return nil
	}
	raw := string(m.buf)
	if strings.ContainsRune(raw, '\n') {
		return nil
	}
	trimmed := strings.TrimLeft(raw, " \t")
	if !strings.HasPrefix(trimmed, "/") || strings.ContainsRune(trimmed, ' ') {
		return nil
	}
	prefix := strings.ToLower(trimmed)
	var out []SlashCommand
	for _, cmd := range m.slashCmds {
		if strings.HasPrefix(strings.ToLower(cmd.Name), prefix) {
			out = append(out, cmd)
			if len(out) >= maxSlashSuggestions {
				break
			}
		}
	}
	return out
}

func (m *hybridInputModel) clampSuggIdx() {
	suggs := m.matchingSlashCommands()
	if len(suggs) == 0 || m.suggIdx >= len(suggs) {
		m.suggIdx = 0
	}
}

func (m *hybridInputModel) applySuggestion(cmd SlashCommand) {
	completed := cmd.Name + " "
	m.buf = []rune(completed)
	m.cursor = len(m.buf)
	m.suggIdx = 0
}

func (m hybridInputModel) View() string {
	if m.quitting {
		return ""
	}
	maxCols := m.width
	if maxCols <= 0 {
		maxCols = 80
	}
	boxWidth := fitBoxWidth(maxCols)
	innerWidth := boxWidth - 4
	if innerWidth < 2 {
		innerWidth = 2
	}

	var out strings.Builder
	out.WriteString(m.renderStatusBar(boxWidth))
	out.WriteString("\n")
	out.WriteString(m.renderEditorBox(boxWidth, innerWidth))

	if suggs := m.matchingSlashCommands(); len(suggs) > 0 {
		out.WriteString("\n")
		out.WriteString(m.renderSuggestions(suggs, boxWidth))
	}

	out.WriteString("\n")
	footerMax := maxCols - 2
	if footerMax < 4 {
		footerMax = 4
	}
	if m.notice != "" {
		noticeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
		out.WriteString("  " + truncateANSIWidth(noticeStyle.Render(m.notice), footerMax))
	} else {
		helpStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))
		helpText := "Enter send · Alt+Enter newline · Tab complete · /ui cli to switch back · Ctrl+D exit"
		if m.hasThinking {
			helpText = "Enter send · Alt+Enter newline · Tab complete · Ctrl+T think · /ui cli · Ctrl+D exit"
		}
		if displayWidth(helpText) > footerMax {
			if m.hasThinking {
				helpText = "Enter send · Ctrl+T think · /ui cli · Ctrl+D exit"
			} else {
				helpText = "Enter send · Alt+Enter newline · /ui cli · Ctrl+D exit"
			}
		}
		out.WriteString("  " + truncateANSIWidth(helpStyle.Render(helpText), footerMax))
	}
	return out.String()
}

func (m hybridInputModel) renderStatusBar(maxWidth int) string {
	modeLabel := "BUILD"
	modeBg := lipgloss.Color("35")
	if m.status.PlanMode {
		modeLabel = "PLAN"
		modeBg = lipgloss.Color("178")
	}
	modePill := lipgloss.NewStyle().
		Foreground(lipgloss.Color("232")).
		Background(modeBg).
		Bold(true).
		Padding(0, 1).
		Render(modeLabel)

	tuiPill := lipgloss.NewStyle().
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("63")).
		Bold(true).
		Padding(0, 1).
		Render("TUI")

	metaStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("250"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	parts := []string{modePill, tuiPill}
	if model := strings.TrimSpace(m.status.Model); model != "" {
		parts = append(parts, metaStyle.Render(model))
	}
	if m.status.ContextPct >= 0 {
		pctText := fmt.Sprintf("ctx %d%%", m.status.ContextPct)
		if m.status.ContextPct >= 75 {
			parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(pctText))
		} else {
			parts = append(parts, dimStyle.Render(pctText))
		}
	}
	if m.status.TodosTotal > 0 {
		parts = append(parts, dimStyle.Render(fmt.Sprintf("todos %d/%d", m.status.TodosDone, m.status.TodosTotal)))
	}
	if m.status.MissionActive {
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true).Render("MISSION"))
	}
	if m.status.Offline {
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Render("OFFLINE"))
	}
	if m.status.SessionMode != "" {
		parts = append(parts, lipgloss.NewStyle().Foreground(lipgloss.Color("203")).Render(m.status.SessionMode))
	}

	joined := strings.Join(parts, dimStyle.Render(" · "))
	return truncateANSIWidth(joined, maxWidth)
}

func (m hybridInputModel) renderEditorBox(boxWidth, innerWidth int) string {
	borderColor := lipgloss.Color("42")
	promptColor := lipgloss.Color("42")
	if m.status.PlanMode {
		borderColor = lipgloss.Color("220")
		promptColor = lipgloss.Color("220")
	}

	body := m.renderBufferWithCursor(innerWidth, promptColor)
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	return renderRoundedBox(strings.Split(body, "\n"), borderStyle, boxWidth)
}

func (m hybridInputModel) renderBufferWithCursor(innerWidth int, promptColor lipgloss.Color) string {
	promptText := strings.TrimSpace(m.prompt)
	if promptText == "" {
		promptText = ">"
	}
	prefix := lipgloss.NewStyle().Foreground(promptColor).Bold(true).Render(promptText) + " "
	prefixWidth := displayWidth(promptText) + 1
	contPrefix := strings.Repeat(" ", prefixWidth)
	contentWidth := innerWidth - prefixWidth
	if contentWidth < 4 {
		contentWidth = 4
	}

	cursorStyle := lipgloss.NewStyle().Reverse(true)
	placeholderStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("242"))

	if len(m.buf) == 0 {
		avail := innerWidth - prefixWidth - 1
		if avail >= 6 {
			ph := truncateANSIWidth(" Ask anything or type /help…", avail)
			return prefix + cursorStyle.Render(" ") + placeholderStyle.Render(ph)
		}
		return prefix + cursorStyle.Render(" ")
	}

	type seg struct {
		text     string
		isCursor bool
	}
	var lines [][]seg
	var current []seg
	col := 0
	flushLine := func() {
		lines = append(lines, current)
		current = nil
		col = 0
	}

	for i := 0; i <= len(m.buf); i++ {
		atCursor := i == m.cursor
		if i == len(m.buf) {
			if atCursor {
				if col+1 > contentWidth && col > 0 {
					flushLine()
				}
				current = append(current, seg{text: " ", isCursor: true})
			}
			break
		}
		r := m.buf[i]
		if r == '\n' {
			if atCursor {
				if col+1 > contentWidth && col > 0 {
					flushLine()
				}
				current = append(current, seg{text: " ", isCursor: true})
			}
			flushLine()
			continue
		}
		w := runeCellWidth(r)
		if w <= 0 {
			w = 1
		}
		if col+w > contentWidth && col > 0 {
			flushLine()
		}
		current = append(current, seg{text: string(r), isCursor: atCursor})
		col += w
	}
	lines = append(lines, current)

	var b strings.Builder
	for li, lineSegs := range lines {
		if li > 0 {
			b.WriteByte('\n')
		}
		if li == 0 {
			b.WriteString(prefix)
		} else {
			b.WriteString(contPrefix)
		}
		for _, s := range lineSegs {
			if s.isCursor {
				b.WriteString(cursorStyle.Render(s.text))
			} else {
				b.WriteString(s.text)
			}
		}
	}
	return b.String()
}

func (m hybridInputModel) renderSuggestions(suggs []SlashCommand, maxWidth int) string {
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(lipgloss.Color("42")).Bold(true)
	cmdStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	lines := make([]string, 0, len(suggs))
	for i, s := range suggs {
		var line strings.Builder
		name := s.Name
		if i == m.suggIdx {
			line.WriteString("  ▸ ")
			line.WriteString(activeStyle.Render(" " + name + " "))
		} else {
			line.WriteString("    ")
			line.WriteString(cmdStyle.Render(name))
		}
		if s.Description != "" {
			line.WriteString("  ")
			line.WriteString(descStyle.Render(s.Description))
		}
		lines = append(lines, truncateANSIWidth(line.String(), maxWidth))
	}
	return strings.Join(lines, "\n")
}
