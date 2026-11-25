package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/jonathanhecl/vibe-coder/internal/agent"
	"github.com/jonathanhecl/vibe-coder/internal/session"
	"github.com/jonathanhecl/vibe-coder/internal/tui"
)

// reviewStaleChecklist asks the user what to do with a pending checklist that
// has gone stale. It returns true only when the user explicitly chose to
// resume; Later and Discard both leave the REPL loop running without a
// continuation. Discard still persists the cleared work state so the decision
// survives a restart. The approval clock is sampled only after the answer
// arrives, so a slow prompt cannot backdate the fresh TTL window.
func reviewStaleChecklist(ag *agent.Agent, sess *session.Session, asker tui.OptionAsker, clock func() time.Time) (bool, error) {
	// The summary's stale-review footer tells the user to press Enter, which
	// would default to Later here; keep only the work description on the card.
	details, _, _ := strings.Cut(ag.PendingWorkSummary(), " — Checklist needs review;")
	answer, err := asker.AskOptions(tui.AskCard{
		Title:  "Stale checklist",
		Prompt: details,
		Options: []tui.AskOption{
			{Label: "Later", Description: "Keep the checklist paused"},
			{Label: "Continue", Description: "Resume this checklist now"},
			{Label: "Discard", Description: "Remove the checklist and its resume point; keep the conversation"},
		},
	})
	if err != nil {
		return false, err
	}
	switch strings.ToLower(strings.TrimSpace(answer)) {
	case "2", "continue":
		if !ag.ResolveStaleChecklist(clock(), false) {
			return false, nil
		}
		if err := sess.Save(); err != nil {
			return false, fmt.Errorf("save checklist decision: %w", err)
		}
		return true, nil
	case "3", "discard":
		if ag.ResolveStaleChecklist(clock(), true) {
			if err := sess.Save(); err != nil {
				return false, fmt.Errorf("save checklist decision: %w", err)
			}
		}
	}
	return false, nil
}
