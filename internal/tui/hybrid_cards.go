package tui

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type hybridTickMsg time.Time

func hybridCountdownTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg {
		return hybridTickMsg(t)
	})
}

type hybridPermissionOption struct {
	key      string
	label    string
	desc     string
	decision Decision
}

var hybridPermissionOptions = []hybridPermissionOption{
	{key: "1", label: "Allow once", desc: "this action only", decision: DecisionAllowOnce},
	{key: "2", label: "Allow this session", desc: "until exit", decision: DecisionAllowSession},
	{key: "3", label: "Always allow", desc: "save permission", decision: DecisionAllowPersistent},
	{key: "4", label: "Deny once", desc: "ask again next time", decision: DecisionDenyOnce},
	{key: "5", label: "Block this session", desc: "until exit", decision: DecisionDenySession},
	{key: "6", label: "Always deny", desc: "save block", decision: DecisionDenyPersistent},
	{key: "7", label: "Cancel", desc: "abort this run", decision: DecisionCancel},
}

type hybridPermissionModel struct {
	payload  []string
	cursor   int
	width    int
	deadline time.Time
	now      time.Time
	decision Decision
	timedOut bool
	quitting bool
}

func newHybridPermissionModel(payload []string, timeout time.Duration, width int) hybridPermissionModel {
	if width <= 0 {
		width = 80
	}
	now := time.Now()
	var deadline time.Time
	if timeout > 0 {
		deadline = now.Add(timeout)
	}
	return hybridPermissionModel{
		payload:  append([]string(nil), payload...),
		cursor:   0,
		width:    width,
		deadline: deadline,
		now:      now,
		decision: DecisionDenyOnce,
	}
}

func (m hybridPermissionModel) Init() tea.Cmd {
	if !m.deadline.IsZero() {
		return hybridCountdownTick()
	}
	return nil
}

func (m hybridPermissionModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		return m, nil

	case hybridTickMsg:
		m.now = time.Time(msg)
		if !m.deadline.IsZero() && !m.now.Before(m.deadline) {
			m.decision = DecisionTimeout
			m.timedOut = true
			m.quitting = true
			return m, tea.Quit
		}
		return m, hybridCountdownTick()

	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC, tea.KeyEsc:
			m.decision = DecisionDenyOnce
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp:
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.cursor = len(hybridPermissionOptions) - 1
			}
			return m, nil
		case tea.KeyDown:
			m.cursor = (m.cursor + 1) % len(hybridPermissionOptions)
			return m, nil
		case tea.KeyEnter:
			m.decision = hybridPermissionOptions[m.cursor].decision
			m.quitting = true
			return m, tea.Quit
		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				switch strings.ToLower(string(msg.Runes)) {
				case "k":
					if m.cursor > 0 {
						m.cursor--
					} else {
						m.cursor = len(hybridPermissionOptions) - 1
					}
					return m, nil
				case "j":
					m.cursor = (m.cursor + 1) % len(hybridPermissionOptions)
					return m, nil
				case "1", "2", "3", "4", "5", "6", "7", "q", "c":
					m.decision = permissionDecision(strings.ToLower(string(msg.Runes)))
					m.quitting = true
					return m, tea.Quit
				}
			}
		}
	}
	return m, nil
}

func (m hybridPermissionModel) View() string {
	if m.quitting {
		return ""
	}
	boxWidth := fitBoxWidth(m.width)
	innerWidth := boxWidth - 4
	if innerWidth < 2 {
		innerWidth = 2
	}

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	payloadStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("252"))
	addStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("42"))
	delStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("203"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(lipgloss.Color("220")).Bold(true)
	numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	var lines []string
	head := titleStyle.Render("? PERMISSION REQUIRED")
	if !m.deadline.IsZero() {
		if badge := askBadge(m.deadline.Sub(m.now)); badge != "" {
			head += "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(badge)
		}
	}
	lines = append(lines, head, "")

	for _, raw := range m.payload {
		line := fitGateLine(raw, permissionDisplayMaxRunes)
		switch {
		case strings.HasPrefix(line, "+ "):
			lines = append(lines, "  "+addStyle.Render(line))
		case strings.HasPrefix(line, "- "):
			lines = append(lines, "  "+delStyle.Render(line))
		case strings.HasPrefix(line, "WARNING:") || strings.HasPrefix(line, "GRAVITY"):
			lines = append(lines, "  "+titleStyle.Render(line))
		default:
			lines = append(lines, "  "+payloadStyle.Render(line))
		}
	}
	lines = append(lines, "")

	sepWidth := innerWidth - 2
	if sepWidth > 29 {
		sepWidth = 29
	}
	if sepWidth < 4 {
		sepWidth = 4
	}
	sepLine := descStyle.Render("  " + strings.Repeat("─", sepWidth))

	for i, opt := range hybridPermissionOptions {
		if i == 3 {
			lines = append(lines, sepLine)
		}
		var optLine string
		if i == m.cursor {
			optLine = "▸ " + selectedStyle.Render(fmt.Sprintf(" [%s] %s ", opt.key, opt.label))
		} else {
			optLine = "  " + numStyle.Render("["+opt.key+"] ") + payloadStyle.Render(opt.label)
		}
		if opt.desc != "" {
			optLine += "  " + descStyle.Render(opt.desc)
		}
		lines = append(lines, optLine)
	}
	lines = append(lines, "", descStyle.Render("↑/↓ + Enter or press 1-7 · Esc denies once"))

	return renderRoundedBox(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("220")), boxWidth)
}

type hybridAskModel struct {
	card       AskCard
	cursor     int
	customMode bool
	customBuf  []rune
	width      int
	deadline   time.Time
	now        time.Time
	answer     string
	escaped    bool
	timedOut   bool
	quitting   bool
}

func newHybridAskModel(card AskCard, timeout time.Duration, width int) hybridAskModel {
	if width <= 0 {
		width = 80
	}
	now := time.Now()
	var deadline time.Time
	if timeout > 0 {
		deadline = now.Add(timeout)
	}
	return hybridAskModel{
		card:     card,
		cursor:   0,
		width:    width,
		deadline: deadline,
		now:      now,
	}
}

func (m hybridAskModel) Init() tea.Cmd {
	if !m.deadline.IsZero() {
		return hybridCountdownTick()
	}
	return nil
}

func (m hybridAskModel) totalRows() int {
	return len(m.card.Options) + 1 // +1 for custom free-text option
}

func (m hybridAskModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		return m, nil

	case hybridTickMsg:
		m.now = time.Time(msg)
		if !m.deadline.IsZero() && !m.now.Before(m.deadline) {
			m.timedOut = true
			m.quitting = true
			return m, tea.Quit
		}
		return m, hybridCountdownTick()

	case tea.KeyMsg:
		if m.customMode {
			if msg.Paste {
				cleaned := strings.ReplaceAll(string(msg.Runes), "\r\n", " ")
				cleaned = strings.ReplaceAll(cleaned, "\n", " ")
				m.customBuf = append(m.customBuf, []rune(cleaned)...)
				return m, nil
			}
			switch msg.Type {
			case tea.KeyEsc:
				m.customMode = false
				return m, nil
			case tea.KeyCtrlC:
				m.escaped = true
				m.quitting = true
				return m, tea.Quit
			case tea.KeyEnter:
				txt := strings.TrimSpace(string(m.customBuf))
				if txt == "" {
					m.escaped = true
				} else {
					m.answer = txt
				}
				m.quitting = true
				return m, tea.Quit
			case tea.KeyBackspace:
				if len(m.customBuf) > 0 {
					m.customBuf = m.customBuf[:len(m.customBuf)-1]
				}
				return m, nil
			case tea.KeySpace:
				m.customBuf = append(m.customBuf, ' ')
				return m, nil
			case tea.KeyRunes:
				m.customBuf = append(m.customBuf, msg.Runes...)
				return m, nil
			}
			return m, nil
		}

		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC:
			m.escaped = true
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp:
			rows := m.totalRows()
			if m.cursor > 0 {
				m.cursor--
			} else {
				m.cursor = rows - 1
			}
			return m, nil
		case tea.KeyDown:
			m.cursor = (m.cursor + 1) % m.totalRows()
			return m, nil
		case tea.KeyEnter:
			if m.cursor >= len(m.card.Options) {
				m.customMode = true
				return m, nil
			}
			m.answer = fmt.Sprintf("%d", m.cursor+1)
			m.quitting = true
			return m, tea.Quit
		case tea.KeyRunes:
			if len(msg.Runes) == 1 {
				r := msg.Runes[0]
				if r >= '1' && r <= '9' {
					idx := int(r - '1')
					if idx < len(m.card.Options) {
						m.answer = string(r)
						m.quitting = true
						return m, tea.Quit
					}
				}
				if r == 'c' || r == 'C' || r == 'o' || r == 'O' {
					m.customMode = true
					m.cursor = len(m.card.Options)
					return m, nil
				}
			}
		}
	}
	return m, nil
}

func (m hybridAskModel) View() string {
	if m.quitting {
		return ""
	}
	boxWidth := fitBoxWidth(m.width)

	titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	promptStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("255")).Bold(true)
	noteStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("220"))
	selectedStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("232")).Background(lipgloss.Color("81")).Bold(true)
	numStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("81")).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("244"))

	var lines []string
	title := strings.TrimSpace(m.card.Title)
	prompt := strings.TrimSpace(m.card.Prompt)
	head := titleStyle.Render("? QUESTION")
	if title != "" {
		head = titleStyle.Render("? " + title)
	}
	if !m.deadline.IsZero() {
		if badge := askBadge(m.deadline.Sub(m.now)); badge != "" {
			head += "  " + lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render(badge)
		}
	}
	lines = append(lines, head)
	if m.card.Note != "" {
		lines = append(lines, "  "+noteStyle.Render(m.card.Note))
	}
	if prompt != "" {
		lines = append(lines, "  "+promptStyle.Render(prompt))
	}
	lines = append(lines, "")

	for i, opt := range m.card.Options {
		var optLine string
		if i == m.cursor && !m.customMode {
			optLine = "▸ " + selectedStyle.Render(fmt.Sprintf(" [%d] %s ", i+1, opt.Label))
		} else {
			optLine = "  " + numStyle.Render(fmt.Sprintf("[%d] ", i+1)) + opt.Label
		}
		if opt.Description != "" {
			optLine += "  " + descStyle.Render(opt.Description)
		}
		lines = append(lines, optLine)
	}

	if m.customMode {
		cursor := lipgloss.NewStyle().Reverse(true).Render(" ")
		lines = append(lines,
			fmt.Sprintf("▸ %s %s%s", numStyle.Render("Custom:"), string(m.customBuf), cursor),
			"",
			descStyle.Render("Enter submit · Esc back to options"),
		)
	} else {
		if m.cursor == len(m.card.Options) {
			lines = append(lines, "▸ "+selectedStyle.Render(" [c] Type custom answer… "))
		} else {
			lines = append(lines, "  "+numStyle.Render("[c] ")+descStyle.Render("Type custom answer…"))
		}
		lines = append(lines, "", descStyle.Render("↑/↓ + Enter or press 1-9 · Esc uses default [1]"))
	}

	return renderRoundedBox(lines, lipgloss.NewStyle().Foreground(lipgloss.Color("81")), boxWidth)
}

type hybridThinkingViewerModel struct {
	raw        string
	lines      []string
	offset     int
	viewHeight int
	width      int
	quitting   bool
}

func newHybridThinkingViewerModel(rawThinking string, width, termHeight int) hybridThinkingViewerModel {
	if width <= 0 {
		width = 80
	}
	m := hybridThinkingViewerModel{
		raw:        strings.TrimRight(strings.ReplaceAll(strings.ReplaceAll(rawThinking, "\r\n", "\n"), "\r", "\n"), "\n"),
		width:      width,
		viewHeight: computeThinkingViewerHeight(termHeight),
	}
	m.rewrap()
	return m
}

func computeThinkingViewerHeight(termHeight int) int {
	if termHeight <= 0 {
		return 12
	}
	avail := termHeight - 6
	if avail < 6 {
		return 6
	}
	if avail > 18 {
		return 18
	}
	return avail
}

func (m hybridThinkingViewerModel) boxWidth() int {
	return fitBoxWidth(m.width)
}

func (m *hybridThinkingViewerModel) rewrap() {
	innerWidth := m.boxWidth() - 4
	if innerWidth < 2 {
		innerWidth = 2
	}
	if strings.TrimSpace(m.raw) == "" {
		m.lines = []string{"(empty)"}
	} else {
		m.lines = wrapThinkingLines(m.raw, innerWidth)
	}
	m.clampOffset()
}

func (m hybridThinkingViewerModel) maxOffset() int {
	maxOff := len(m.lines) - m.viewHeight
	if maxOff < 0 {
		return 0
	}
	return maxOff
}

func (m *hybridThinkingViewerModel) clampOffset() {
	if m.offset < 0 {
		m.offset = 0
	}
	if maxOff := m.maxOffset(); m.offset > maxOff {
		m.offset = maxOff
	}
}

func (m *hybridThinkingViewerModel) scrollBy(delta int) {
	m.offset += delta
	m.clampOffset()
}

func (m hybridThinkingViewerModel) Init() tea.Cmd { return nil }

func (m hybridThinkingViewerModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		if msg.Width > 0 {
			m.width = msg.Width
		}
		if msg.Height > 0 {
			m.viewHeight = computeThinkingViewerHeight(msg.Height)
		}
		m.rewrap()
		return m, nil

	case tea.KeyMsg:
		halfPage := m.viewHeight / 2
		if halfPage < 1 {
			halfPage = 1
		}
		switch msg.Type {
		case tea.KeyEsc, tea.KeyCtrlC, tea.KeyCtrlT, tea.KeyEnter:
			m.quitting = true
			return m, tea.Quit
		case tea.KeyUp:
			m.scrollBy(-1)
			return m, nil
		case tea.KeyDown:
			m.scrollBy(1)
			return m, nil
		case tea.KeyPgUp, tea.KeyCtrlU:
			m.scrollBy(-halfPage)
			return m, nil
		case tea.KeyPgDown, tea.KeyCtrlD:
			m.scrollBy(halfPage)
			return m, nil
		case tea.KeyHome:
			m.offset = 0
			return m, nil
		case tea.KeyEnd:
			m.offset = m.maxOffset()
			return m, nil
		case tea.KeySpace:
			m.scrollBy(halfPage)
			return m, nil
		case tea.KeyRunes:
			switch strings.ToLower(string(msg.Runes)) {
			case "q":
				m.quitting = true
				return m, tea.Quit
			case "j":
				m.scrollBy(1)
			case "k":
				m.scrollBy(-1)
			case "b":
				m.scrollBy(-halfPage)
			case "f":
				m.scrollBy(halfPage)
			case "g":
				if string(msg.Runes) == "G" {
					m.offset = m.maxOffset()
				} else {
					m.offset = 0
				}
			}
			return m, nil
		}
	}
	return m, nil
}

func (m hybridThinkingViewerModel) View() string {
	if m.quitting {
		return ""
	}
	boxWidth := m.boxWidth()
	innerWidth := boxWidth - 4
	if innerWidth < 2 {
		innerWidth = 2
		boxWidth = 6
	}

	total := len(m.lines)
	end := m.offset + m.viewHeight
	if end > total {
		end = total
	}
	visible := m.lines[m.offset:end]

	title := fmt.Sprintf(" 💭 Thinking (lines %d-%d of %d) ", m.offset+1, end, total)
	if total == 0 {
		title = " 💭 Thinking "
	}

	var b strings.Builder
	b.WriteString(renderThinkingTopBorder(title, boxWidth))
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

	help := "↑/↓ scroll · PgUp/PgDn page · Home/End · Esc close"
	b.WriteString(renderThinkingBottomBorder(help, boxWidth))
	return b.String()
}
