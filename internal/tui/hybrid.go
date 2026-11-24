package tui

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"golang.org/x/term"
)

// StatusInfo carries live session telemetry displayed in HybridUI's inline status bar.
type StatusInfo struct {
	Model         string
	ContextPct    int // < 0 means unknown/omitted
	TodosDone     int
	TodosTotal    int
	MissionActive bool
	Offline       bool
	PlanMode      bool
	SessionMode   string // "", "temporal", or "isolated"
}

// StatusUpdater is implemented by UIs that render a live status strip.
type StatusUpdater interface {
	SetStatusInfo(info StatusInfo)
}

// SlashCommand describes one slash command for interactive autocomplete.
type SlashCommand struct {
	Name        string
	Description string
}

// SlashCommandSetter is implemented by UIs that offer slash-command autocomplete.
type SlashCommandSetter interface {
	SetSlashCommands(cmds []SlashCommand)
}

// HybridUI renders a hybrid terminal interface: completed turns, tool cards,
// diffs, and streaming output go straight to native scrollback (preserving mouse
// copy-paste and terminal search), while interactive input, permissions, and
// multi-choice prompts run inside an inline Bubble Tea + Lipgloss widget
// without AltScreen.
type HybridUI struct {
	plain *PlainUI

	mu        sync.Mutex
	status    StatusInfo
	slashCmds []SlashCommand
}

// NewHybrid constructs a HybridUI bound to standard streams.
func NewHybrid(cfg ...*config.Config) *HybridUI {
	base := NewPlain(cfg...)
	base.markdown = NewRichMarkdownRenderer(base.style)
	info := StatusInfo{ContextPct: -1}
	if len(cfg) > 0 && cfg[0] != nil {
		info.Model = compactHybridModel(cfg[0].Model)
		info.Offline = cfg[0].Offline
		switch {
		case cfg[0].Temporal:
			info.SessionMode = "temporal"
		case cfg[0].Isolated:
			info.SessionMode = "isolated"
		}
	}
	return &HybridUI{
		plain:  base,
		status: info,
	}
}

func compactHybridModel(name string) string {
	s := strings.TrimSpace(name)
	if idx := strings.LastIndex(s, "/"); idx >= 0 && idx+1 < len(s) {
		s = s[idx+1:]
	}
	return s
}

func (h *HybridUI) isInteractiveTTY() bool {
	if h == nil || h.plain == nil || h.plain.in == nil || !h.plain.outIsTTY {
		return false
	}
	return term.IsTerminal(int(h.plain.in.Fd()))
}

func (h *HybridUI) StartESCMonitor(interrupt func()) error {
	return h.plain.StartESCMonitor(interrupt)
}

func (h *HybridUI) StopESCMonitor() { h.plain.StopESCMonitor() }

func (h *HybridUI) StartTurn() { h.plain.StartTurn() }

func (h *HybridUI) SeedHistory(entries []string) { h.plain.SeedHistory(entries) }

func (h *HybridUI) SetPlanMode(enabled bool) {
	h.plain.SetPlanMode(enabled)
	h.mu.Lock()
	h.status.PlanMode = enabled
	h.mu.Unlock()
}

func (h *HybridUI) SetStatusInfo(info StatusInfo) {
	h.mu.Lock()
	h.status = info
	h.mu.Unlock()
}

func (h *HybridUI) SetSlashCommands(cmds []SlashCommand) {
	h.mu.Lock()
	h.slashCmds = append([]SlashCommand(nil), cmds...)
	h.mu.Unlock()
}

func (h *HybridUI) StreamAssistant(text string) { h.plain.StreamAssistant(text) }

func (h *HybridUI) EndAssistant(usage TurnUsage) { h.plain.EndAssistant(usage) }

func (h *HybridUI) StreamThinking(text string) { h.plain.StreamThinking(text) }

func (h *HybridUI) EndThinking() { h.plain.EndThinking() }

func (h *HybridUI) StartWaiting(label string) { h.plain.StartWaiting(label) }

func (h *HybridUI) StopWaiting() { h.plain.StopWaiting() }

func (h *HybridUI) ShowToolCall(name string, params map[string]any) {
	h.plain.ShowToolCall(name, params)
}

func (h *HybridUI) ShowToolResult(name, output string, isError bool, toolParams map[string]any) {
	h.plain.ShowToolResult(name, output, isError, toolParams)
}

func (h *HybridUI) ShowTodos(items []TodoItem) {
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
	h.plain.ShowTodos(items)
}

func (h *HybridUI) CollapseAssistantOutput() { h.plain.CollapseAssistantOutput() }

func (h *HybridUI) NotifyAssisted(notice AssistedNotice) { h.plain.NotifyAssisted(notice) }

func (h *HybridUI) Stop() { h.plain.Stop() }

func (h *HybridUI) GetInput(prompt string) (string, error) {
	if !h.isInteractiveTTY() {
		return h.plain.GetInput(prompt)
	}

	h.plain.stopSpinner()
	h.plain.mu.Lock()
	h.plain.flushPendingToolLocked()
	h.plain.endAssistantLineLocked()
	h.plain.mu.Unlock()

	h.mu.Lock()
	status := h.status
	cmds := append([]SlashCommand(nil), h.slashCmds...)
	h.mu.Unlock()

	width := terminalColumns(h.plain.out)
	if width <= 0 {
		width = 80
	}
	model := newHybridInputModel(prompt, status, h.plain.history.snapshot(), cmds, width)
	p := tea.NewProgram(model, tea.WithInput(h.plain.in), tea.WithOutput(h.plain.out))
	finalModel, err := p.Run()
	if err != nil {
		return "", err
	}
	res, ok := finalModel.(hybridInputModel)
	if !ok {
		return "", io.EOF
	}
	if res.eof {
		return "", io.EOF
	}
	val := cleanBracketedPasteMarkers(res.value())
	if strings.TrimSpace(val) != "" {
		h.plain.history.add(val)
	}

	h.plain.mu.Lock()
	echo := h.plain.renderPromptString(prompt) + formatScrollbackEcho(val)
	_, _ = fmt.Fprintln(h.plain.out, echo)
	h.plain.mu.Unlock()
	return val, nil
}

func formatScrollbackEcho(val string) string {
	if val == "" {
		return ""
	}
	if !strings.Contains(val, "\n") {
		return val
	}
	return strings.ReplaceAll(val, "\n", "\n  ")
}

func (h *HybridUI) AskPermission(tool string, params map[string]any) Decision {
	if !h.isInteractiveTTY() {
		return h.plain.AskPermission(tool, params)
	}
	if interrupt, paused := h.plain.pauseESCMonitor(); paused {
		defer func() { _ = h.plain.StartESCMonitor(interrupt) }()
	}

	h.plain.stopSpinner()
	h.plain.mu.Lock()
	h.plain.flushPendingToolLocked()
	h.plain.mu.Unlock()

	timeout := h.plain.cfg.EffectivePermissionTimeout()
	payload := permissionPayloadLines(tool, params)
	width := terminalColumns(h.plain.out)
	if width <= 0 {
		width = 80
	}

	m := newHybridPermissionModel(payload, timeout, width)
	p := tea.NewProgram(m, tea.WithInput(h.plain.in), tea.WithOutput(h.plain.out))
	finalModel, err := p.Run()
	if err != nil {
		return DecisionDenyOnce
	}
	res, ok := finalModel.(hybridPermissionModel)
	if !ok {
		return DecisionDenyOnce
	}

	answer := permissionDecisionLabel(res.decision)
	if res.timedOut {
		answer = fmt.Sprintf("⏳ no answer after %s — denied", formatBadgeWait(timeout))
	}
	summaryHead := compactPermissionScrollbackSummary(tool, params)
	h.plain.mu.Lock()
	if summaryHead != "" {
		_, _ = fmt.Fprintf(h.plain.out, "  %s %s\n", h.plain.style.BoldYellow("?"), h.plain.style.Dim(summaryHead))
	}
	_, _ = fmt.Fprintf(h.plain.out, "%s%s\n", optionPromptLine(h.plain.style, "", permissionInstruction), answer)
	h.plain.mu.Unlock()
	return res.decision
}

func compactPermissionScrollbackSummary(tool string, params map[string]any) string {
	header := CompactToolHeader(tool, params)
	if header == "" {
		return strings.TrimSpace(tool)
	}
	return "PERMISSION · " + header
}

func (h *HybridUI) AskOptions(card AskCard) (string, error) {
	if !h.isInteractiveTTY() {
		return h.plain.AskOptions(card)
	}
	if interrupt, paused := h.plain.pauseESCMonitor(); paused {
		defer func() { _ = h.plain.StartESCMonitor(interrupt) }()
	}

	h.plain.stopSpinner()
	h.plain.mu.Lock()
	h.plain.flushPendingToolLocked()
	h.plain.endAssistantLineLocked()
	h.plain.mu.Unlock()

	timeout := h.plain.cfg.EffectivePermissionTimeout()
	width := terminalColumns(h.plain.out)
	if width <= 0 {
		width = 80
	}

	m := newHybridAskModel(card, timeout, width)
	p := tea.NewProgram(m, tea.WithInput(h.plain.in), tea.WithOutput(h.plain.out))
	finalModel, err := p.Run()
	if err != nil {
		return "", err
	}
	res, ok := finalModel.(hybridAskModel)
	if !ok {
		return "", nil
	}
	if res.timedOut {
		h.plain.reportAskTimeout(card)
		return "", nil
	}
	if res.escaped {
		return "", nil
	}

	h.plain.mu.Lock()
	promptTitle := strings.TrimSpace(card.Title)
	if promptTitle == "" {
		promptTitle = strings.TrimSpace(card.Prompt)
	}
	if promptTitle != "" {
		_, _ = fmt.Fprintf(h.plain.out, "  %s %s\n", h.plain.style.BoldCyan("?"), h.plain.style.BrightWhite(promptTitle))
	}
	displayAns := res.answer
	if n, err := strconv.Atoi(res.answer); err == nil && n >= 1 && n <= len(card.Options) {
		displayAns = fmt.Sprintf("[%d] %s", n, card.Options[n-1].Label)
	}
	_, _ = fmt.Fprintf(h.plain.out, "%s%s\n", optionPromptLine(h.plain.style, "", fmt.Sprintf("Select option (1-%d) [default: 1]", len(card.Options))), displayAns)
	h.plain.mu.Unlock()
	return res.answer, nil
}

// Verify HybridUI exposes os.Stdin/Stdout cleanly even when unused directly.
var _ = os.Stdin
var _ UI = (*HybridUI)(nil)
var _ OptionAsker = (*HybridUI)(nil)
var _ StatusUpdater = (*HybridUI)(nil)
var _ SlashCommandSetter = (*HybridUI)(nil)
