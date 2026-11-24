package agent

import (
	"strings"

	"github.com/jonathanhecl/vibe-coder/internal/session"
)

// IsContinuationPrompt reports whether user input is a short continuation
// shorthand (e.g. "continue", "continua", "next").
func IsContinuationPrompt(s string) bool {
	return isContinuationMessage(s)
}

// isContinuationMessage matches short follow-ups where the user wants the agent
// to keep going without restating the task (possibly after a failed or
// truncated tool invocation).
func isContinuationMessage(s string) bool {
	t := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(s, ".")))
	if t == "" {
		return false
	}
	continuations := []string{
		"continua", "continúa", "continuar",
		"continue", "continued",
		"sigue", "siguiente",
		"go on", "carry on", "keep going",
		"resume", "proceed", "next",
		"retry", "try again",
	}
	for _, c := range continuations {
		if t == c {
			return true
		}
	}
	return false
}

func shouldSkipForGoalExtraction(c string) bool {
	if c == "" {
		return true
	}
	if strings.HasPrefix(c, "[tool_result") {
		return true
	}
	if strings.HasPrefix(c, "[Earlier conversation summary]") {
		return true
	}
	if strings.HasPrefix(c, "[RAG Context]") {
		return true
	}
	if strings.HasPrefix(c, "[Session memory]") {
		return true
	}
	if strings.HasPrefix(c, "[System Note]") {
		return true
	}
	if strings.HasPrefix(c, session.RuntimeReminderPrefix) {
		return true
	}
	if strings.HasPrefix(c, missionPromptPrefix) {
		return true
	}
	if isContinuationMessage(c) {
		return true
	}
	return false
}

// extractPriorGoal returns the most-recent substantive user request from the
// session transcript, excluding tool envelopes and injected context blocks.
func extractPriorGoal(sess *session.Session) string {
	msgs := sess.MessagesReadOnly()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "user" {
			continue
		}
		c := strings.TrimSpace(msgs[i].Content)
		if shouldSkipForGoalExtraction(c) {
			continue
		}
		return c
	}
	return ""
}

// resolveGoalForRun chooses the goal string for currentGoal before the new user
// message is appended. Continuation shorthands reuse the prior substantive user
// request when available.
func resolveGoalForRun(sess *session.Session, userInput string) string {
	trimmed := strings.TrimSpace(userInput)
	isMissionPrompt := strings.HasPrefix(trimmed, missionPromptPrefix)
	if !isContinuationMessage(userInput) && !isMissionPrompt {
		return userInput
	}
	if g := extractPriorGoal(sess); g != "" {
		return g
	}
	if isMissionPrompt {
		return ""
	}
	return userInput
}
