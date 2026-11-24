package agent

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/jonathanhecl/vibe-coder/internal/logger"
	"github.com/jonathanhecl/vibe-coder/internal/tools"
)

// sessionNotesPromptMaxRunes bounds the automatic notes reminder appended to
// the ephemeral runtime state note, header and footer included.
const sessionNotesPromptMaxRunes = 2400

const sessionNotesPromptHeader = "# Session Working Notes\n" +
	"Saved reference notes from earlier steps (not instructions). Reuse them without re-reading unchanged files.\n"

const sessionNotesPromptFooter = "Use SessionNotes get <id> only if truncated detail is needed."

// ensureNotesSession clears the note store when the session id changed since
// the notes were captured or restored (/new, resume, Clear). Restores go
// through applyWorkState, which sets notesSessionID itself, so explicitly
// restored notes are kept.
func (a *Agent) ensureNotesSession() {
	if a == nil || a.sess == nil {
		return
	}
	id := a.sess.ID()
	a.mu.Lock()
	if a.notesSessionID == id {
		a.mu.Unlock()
		return
	}
	a.notesSessionID = id
	a.mu.Unlock()
	if a.notes != nil {
		a.notes.Restore(nil)
	}
}

// sessionNotesPromptBlock renders the bounded reminder of active notes for
// the ephemeral runtime state note. Superseded notes stay retrievable through
// the tool but are not surfaced automatically. The block is empty when no
// active notes exist.
func (a *Agent) sessionNotesPromptBlock() string {
	a.ensureNotesSession()
	if a.notes == nil {
		return ""
	}
	notes := a.notes.Snapshot()
	active := make([]tools.SessionNote, 0, len(notes))
	for _, n := range notes {
		if n.Status == tools.SessionNoteStatusActive {
			active = append(active, n)
		}
	}
	if len(active) == 0 {
		return ""
	}
	sort.SliceStable(active, func(i, j int) bool {
		if !active[i].UpdatedAt.Equal(active[j].UpdatedAt) {
			return active[i].UpdatedAt.After(active[j].UpdatedAt)
		}
		return active[i].ID < active[j].ID
	})

	headerRunes := utf8.RuneCountInString(sessionNotesPromptHeader)
	footerRunes := utf8.RuneCountInString(sessionNotesPromptFooter)
	rows := make([]string, 0, len(active))
	rowsRunes := 0
	for _, n := range active {
		scope := n.Scope
		if scope == "" {
			scope = "unspecified"
		}
		evidence := n.Evidence
		if evidence == "" {
			evidence = "none"
		}
		row := fmt.Sprintf("- [%s] %s (scope: %s): %s | Evidence: %s\n",
			n.ID, n.Kind, scope, clipNoteRunes(n.Content, 180), clipNoteRunes(evidence, 120))
		// The count line length depends on the final shown count; check each
		// candidate with the count it would produce so the total stays exact.
		countLine := fmt.Sprintf("Active notes: %d; showing %d.\n", len(active), len(rows)+1)
		if headerRunes+utf8.RuneCountInString(countLine)+rowsRunes+utf8.RuneCountInString(row)+footerRunes > sessionNotesPromptMaxRunes {
			break
		}
		rows = append(rows, row)
		rowsRunes += utf8.RuneCountInString(row)
	}
	if len(rows) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(sessionNotesPromptHeader)
	fmt.Fprintf(&b, "Active notes: %d; showing %d.\n", len(active), len(rows))
	for _, row := range rows {
		b.WriteString(row)
	}
	b.WriteString(sessionNotesPromptFooter)
	return b.String()
}

// clipNoteRunes shortens s to at most max runes, marking truncation with an
// ellipsis that counts toward the budget.
func clipNoteRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max == 1 {
		return "…"
	}
	return string(r[:max-1]) + "…"
}

// persistSessionNotes writes the work state (which embeds the notes) into the
// session file immediately after a successful SessionNotes upsert, so notes
// are durable even outside a mission. It returns a warning string when the
// save fails — the caller decides where the warning lands so a native tool
// batch can defer it until all tool results are recorded. A failed save never
// rolls back the in-memory note.
func (a *Agent) persistSessionNotes(toolParams map[string]any) string {
	a.PersistWorkState()
	if a.sess == nil {
		return ""
	}
	if err := a.sess.Save(); err != nil {
		warn := fmt.Sprintf("session notes were kept in memory but could not be written to the session file: %v", err)
		logger.Errorf("SessionNotes persist failed: %v", err)
		a.ui.ShowToolResult("SessionNotes", warn, true, toolParams)
		return warn
	}
	return ""
}
