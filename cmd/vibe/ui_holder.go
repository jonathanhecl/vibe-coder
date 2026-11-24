package main

import (
	"strings"
	"sync"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/config"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/slash"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// uiHolder tracks the active tui.UI behind a mutex so /ui hot-swaps keep
// signal handlers, defer Stop(), and the REPL loop targeting the live UI.
type uiHolder struct {
	mu sync.RWMutex
	ui tui.UI
}

func newUIHolder(initial tui.UI) *uiHolder {
	return &uiHolder{ui: initial}
}

func (h *uiHolder) Get() tui.UI {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.ui
}

func (h *uiHolder) Set(next tui.UI) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.ui = next
	h.mu.Unlock()
}

func (h *uiHolder) Stop() {
	if ui := h.Get(); ui != nil {
		ui.Stop()
	}
}

func wireHybridSlashCommands(ui tui.UI) {
	setter, ok := ui.(tui.SlashCommandSetter)
	if !ok {
		return
	}
	catalog := slash.CommandCatalog()
	cmds := make([]tui.SlashCommand, 0, len(catalog))
	for _, entry := range catalog {
		cmds = append(cmds, tui.SlashCommand{
			Name:        entry[0],
			Description: entry[1],
		})
	}
	setter.SetSlashCommands(cmds)
}

func syncUIStatus(ui tui.UI, cfg *config.Config, ag *agent.Agent, sess *session.Session) {
	updater, ok := ui.(tui.StatusUpdater)
	if !ok {
		return
	}
	info := tui.StatusInfo{ContextPct: -1}
	if cfg != nil {
		info.Model = compactModelLabel(cfg.Model)
		info.Offline = cfg.Offline
		switch {
		case cfg.Temporal:
			info.SessionMode = "temporal"
		case cfg.Isolated:
			info.SessionMode = "isolated"
		}
		if cfg.ContextWindow > 0 && sess != nil {
			pct := sess.TokenEstimate() * 100 / cfg.ContextWindow
			if pct > 100 {
				pct = 100
			}
			info.ContextPct = pct
		}
	}
	if ag != nil {
		info.PlanMode = ag.InPlanMode()
		info.MissionActive = ag.MissionActive()
		info.TodosDone, info.TodosTotal = ag.TodoCounts()
	}
	updater.SetStatusInfo(info)
}

func compactModelLabel(name string) string {
	s := strings.TrimSpace(name)
	if idx := strings.LastIndex(s, "/"); idx >= 0 && idx+1 < len(s) {
		s = s[idx+1:]
	}
	return s
}

func makeUISwitcher(cfg *config.Config, ag *agent.Agent, sess *session.Session, reg *tools.Registry, uiBox *uiHolder, slashCtx *slash.Ctx) func(string) error {
	return func(_ string) error {
		if prev := uiBox.Get(); prev != nil {
			prev.Stop()
		}
		next, err := tui.NewFromMode(cfg)
		if err != nil {
			return err
		}
		wireHybridSlashCommands(next)
		if reg != nil {
			wireAsker(reg, next)
		}
		if ag != nil {
			ag.SetUI(next)
		}
		if cfg != nil && cfg.Isolated {
			seedIsolatedPromptHistory(next, sess)
		}
		syncUIStatus(next, cfg, ag, sess)
		uiBox.Set(next)
		if slashCtx != nil {
			slashCtx.Prompter = next
		}
		return nil
	}
}
