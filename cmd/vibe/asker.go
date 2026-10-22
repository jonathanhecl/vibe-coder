package main

import (
	"os"

	"golang.org/x/term"

	"github.com/jonathanhecl/vibe-coder/internal/tools"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// tuiAsker bridges the AskUserQuestion tool to the terminal UI. The bridge
// lives in the composition root because internal/tui cannot import
// internal/tools: tools already imports tui for styling, so the reverse would
// cycle.
type tuiAsker struct{ ui tui.OptionAsker }

func (a tuiAsker) Ask(req tools.AskRequest) (string, error) {
	opts := make([]string, len(req.Options))
	for i, opt := range req.Options {
		opts[i] = opt.Label
	}
	return a.ui.AskOptions(tui.AskCard{
		Title:   req.Title,
		Prompt:  req.Prompt,
		Options: opts,
		Note:    req.ErrNote,
	})
}

// wireAsker connects the tool to the UI only when both ends are a terminal.
// Headless runs (-p, pipes) have nobody to answer, so the tool keeps its own
// stdin path there instead of blocking on a prompt no one sees.
func wireAsker(reg *tools.Registry, ui tui.UI) {
	if !term.IsTerminal(int(os.Stdin.Fd())) || !term.IsTerminal(int(os.Stdout.Fd())) {
		return
	}
	asker, ok := ui.(tui.OptionAsker)
	if !ok {
		return
	}
	reg.SetAsker(tuiAsker{ui: asker})
}
