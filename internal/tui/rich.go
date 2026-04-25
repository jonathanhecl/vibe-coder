package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type richStatusMsg struct {
	phase string
}

type richPlanModeMsg struct {
	enabled bool
}

type richTickMsg struct {
	now time.Time
}

type richStatusModel struct {
	phase    string
	planMode bool
	now      time.Time
	style    lipgloss.Style
	muted    lipgloss.Style
}

func newRichStatusModel() richStatusModel {
	return richStatusModel{
		phase: "idle",
		now:   time.Now(),
		style: lipgloss.NewStyle().
			Foreground(lipgloss.Color("230")).
			Background(lipgloss.Color("63")).
			Padding(0, 1).
			Bold(true),
		muted: lipgloss.NewStyle().
			Foreground(lipgloss.Color("252")).
			Background(lipgloss.Color("63")).
			Padding(0, 1),
	}
}

func (m richStatusModel) Init() tea.Cmd { return nil }

func (m richStatusModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch v := msg.(type) {
	case richStatusMsg:
		m.phase = strings.TrimSpace(v.phase)
		if m.phase == "" {
			m.phase = "idle"
		}
	case richPlanModeMsg:
		m.planMode = v.enabled
	case richTickMsg:
		m.now = v.now
	}
	return m, nil
}

func (m richStatusModel) View() string {
	mode := "BUILD"
	if m.planMode {
		mode = "PLAN"
	}
	left := m.style.Render("vibe-coder · " + mode)
	phase := m.muted.Render("phase: " + m.phase)
	clock := m.muted.Render(m.now.Format("15:04:05"))
	return lipgloss.JoinHorizontal(lipgloss.Top, left, " ", phase, " ", clock)
}

// RichUI is a second UI implementation selected by --ui rich.
// It reuses the stable PlainUI interaction flow and layers a pinned status bar
// plus richer markdown rendering (syntax-highlighted fenced code lines).
type RichUI struct {
	plain *PlainUI

	mu       sync.Mutex
	model    richStatusModel
	stopCh   chan struct{}
	stopOnce sync.Once
}

func NewRich() *RichUI {
	base := NewPlain()
	base.markdown = NewRichMarkdownRenderer(base.style)
	r := &RichUI{
		plain:  base,
		model:  newRichStatusModel(),
		stopCh: make(chan struct{}),
	}
	if base.style.Enabled() {
		go r.runStatusTicker()
	}
	return r
}

func (r *RichUI) StartESCMonitor(interrupt func()) error { return r.plain.StartESCMonitor(interrupt) }
func (r *RichUI) StopESCMonitor()                        { r.plain.StopESCMonitor() }

func (r *RichUI) SetPlanMode(enabled bool) {
	r.plain.SetPlanMode(enabled)
	r.updateStatus(richPlanModeMsg{enabled: enabled})
}

func (r *RichUI) StreamAssistant(text string) {
	r.updateStatus(richStatusMsg{phase: "assistant"})
	r.plain.StreamAssistant(text)
}

func (r *RichUI) EndAssistant() {
	r.plain.EndAssistant()
	r.updateStatus(richStatusMsg{phase: "idle"})
}

func (r *RichUI) StreamThinking(text string) {
	r.updateStatus(richStatusMsg{phase: "thinking"})
	r.plain.StreamThinking(text)
}

func (r *RichUI) EndThinking() {
	r.plain.EndThinking()
	r.updateStatus(richStatusMsg{phase: "assistant"})
}

func (r *RichUI) StartWaiting(label string) {
	p := strings.TrimSpace(label)
	if p == "" {
		p = "waiting"
	}
	r.updateStatus(richStatusMsg{phase: p})
	r.plain.StartWaiting(label)
}

func (r *RichUI) StopWaiting() {
	r.plain.StopWaiting()
	r.updateStatus(richStatusMsg{phase: "idle"})
}

func (r *RichUI) ShowToolCall(name string, params map[string]any) {
	r.updateStatus(richStatusMsg{phase: "tool " + strings.TrimSpace(name)})
	r.plain.ShowToolCall(name, params)
}

func (r *RichUI) ShowToolResult(name, output string, isError bool, toolParams map[string]any) {
	r.plain.ShowToolResult(name, output, isError, toolParams)
	r.updateStatus(richStatusMsg{phase: "idle"})
}

func (r *RichUI) ShowTodos(items []TodoItem) {
	r.updateStatus(richStatusMsg{phase: "todos"})
	r.plain.ShowTodos(items)
}

func (r *RichUI) AskPermission(tool string, params map[string]any) Decision {
	r.updateStatus(richStatusMsg{phase: "permission"})
	return r.plain.AskPermission(tool, params)
}

func (r *RichUI) GetInput(prompt string) (string, error) {
	r.updateStatus(richStatusMsg{phase: "input"})
	return r.plain.GetInput(prompt)
}

func (r *RichUI) Stop() {
	r.stopOnce.Do(func() {
		close(r.stopCh)
	})
	r.clearStatusLine()
	r.plain.Stop()
}

func (r *RichUI) runStatusTicker() {
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case now := <-ticker.C:
			r.updateStatus(richTickMsg{now: now})
		}
	}
}

func (r *RichUI) updateStatus(msg tea.Msg) {
	if !r.plain.style.Enabled() {
		return
	}
	r.mu.Lock()
	model, _ := r.model.Update(msg)
	r.model = model.(richStatusModel)
	line := r.model.View()
	r.mu.Unlock()
	r.paintStatusLine(line)
}

func (r *RichUI) paintStatusLine(line string) {
	f, ok := r.plain.out.(*os.File)
	if !ok || f == nil {
		return
	}
	r.plain.mu.Lock()
	defer r.plain.mu.Unlock()
	_, _ = fmt.Fprintf(f, "\x1b7\x1b[999;1H\x1b[2K%s\x1b8", line)
}

func (r *RichUI) clearStatusLine() {
	if !r.plain.style.Enabled() {
		return
	}
	f, ok := r.plain.out.(*os.File)
	if !ok || f == nil {
		return
	}
	r.plain.mu.Lock()
	defer r.plain.mu.Unlock()
	_, _ = fmt.Fprint(f, "\x1b7\x1b[999;1H\x1b[2K\x1b8")
}

var _ UI = (*RichUI)(nil)
